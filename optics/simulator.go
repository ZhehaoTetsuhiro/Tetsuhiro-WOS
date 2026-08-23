package optics

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// GridSpec selects the sampling grid: Size pixels over physical Width (m).
type GridSpec struct {
	Size  int     `json:"size"`
	Width float64 `json:"width"`
}

// Config is the full JSON configuration of one simulation run.
//
//	{
//	  "grid": {"size": 1024, "width": 0.01},
//	  "wavelength": 632.8e-9,
//	  "polarized": true,
//	  "method": "asm",
//	  "evanescent": "decay",
//	  "bandlimit": {"fraction": 0.9, "sigma": 0.05},
//	  "source": {"type": "gaussian", "params": {"waist": 0.001}},
//	  "elements": [
//	    {"type": "propagate", "params": {"distance": 0.1}},
//	    {"type": "lens", "params": {"f": 0.2}},
//	    {"type": "sensor", "params": {"label": "focus"}}
//	  ]
//	}
type Config struct {
	Grid       GridSpec       `json:"grid"`
	Wavelength float64        `json:"wavelength"`
	Polarized  *bool          `json:"polarized"`
	Method     string         `json:"method"`
	Evanescent string         `json:"evanescent"`
	Bandlimit  *BandlimitOpts `json:"bandlimit"`
	Source     SourceSpec     `json:"source"`
	Elements   []ElementSpec  `json:"elements"`
}

// PolarizationEnabled returns whether Jones two-component simulation is on
// (default true).
func (c *Config) PolarizationEnabled() bool {
	return c.Polarized == nil || *c.Polarized
}

// Limits enforced by validation to protect memory and CPU.
const (
	MaxGridSize = 2048
	MinGridSize = 64
	MaxElements = 256
	MaxPlanes   = 64
	MaxArmDepth = 8
)

// Plane is one recorded output plane (sensor or combiner output).
type Plane struct {
	ID    string
	Label string
	Path  string // arm path this plane lives on ("" = main train)
	Size  int
	DX    float64
	Ex    []complex128
	Ey    []complex128
	Stats PlaneStats
}

// Result is the output of Simulate.
type Result struct {
	RunID      string
	Size       int
	Width      float64
	DX         float64
	Wavelength float64
	ElapsedMS  float64
	Warnings   []Warning
	Planes     []*Plane
}

// PlaneInfo is the lightweight JSON form of a plane (no field data).
type PlaneInfo struct {
	ID    string     `json:"id"`
	Label string     `json:"label"`
	Path  string     `json:"path"`
	Size  int        `json:"size"`
	DX    float64    `json:"dx"`
	Stats PlaneStats `json:"stats"`
}

// Info converts a Plane to its JSON form.
func (p *Plane) Info() PlaneInfo {
	return PlaneInfo{ID: p.ID, Label: p.Label, Path: p.Path, Size: p.Size, DX: p.DX, Stats: p.Stats}
}

// trainer runs one linear train (possibly with beam-splitter sub-arms).
type trainer struct {
	cfg  *Config
	base *Context
	arms map[string]*Field
	nEl  int
	nPl  int
	pl   []*Plane
}

// Simulate validates and runs the full simulation.
func Simulate(cfg Config) (*Result, error) {
	issues := ValidateConfig(&cfg)
	if len(issues) > 0 {
		msg := "config validation failed:"
		for _, is := range issues {
			msg += "\n  " + is.Path + ": " + is.Message
		}
		return nil, fmt.Errorf("%s", msg)
	}
	start := time.Now()
	polarized := cfg.PolarizationEnabled()
	f, err := BuildSource(cfg.Source, cfg.Grid.Size, cfg.Grid.Width, polarized, cfg.Wavelength)
	if err != nil {
		return nil, err
	}
	base := &Context{
		Wavelength: cfg.Wavelength,
		Evanescent: cfg.Evanescent,
		Bandlimit:  cfg.Bandlimit,
		Warnings:   &Warnings{},
	}
	if base.Evanescent == "" {
		base.Evanescent = "decay"
	}
	t := &trainer{cfg: &cfg, base: base, arms: map[string]*Field{}}
	if err := t.runTrain(cfg.Elements, f, "", 0); err != nil {
		return nil, err
	}
	return &Result{
		RunID:      randomID(),
		Size:       cfg.Grid.Size,
		Width:      cfg.Grid.Width,
		DX:         cfg.Grid.Width / float64(cfg.Grid.Size),
		Wavelength: cfg.Wavelength,
		ElapsedMS:  float64(time.Since(start).Microseconds()) / 1000,
		Warnings:   base.Warnings.List(),
		Planes:     t.pl,
	}, nil
}

// runTrain evaluates one element train on field f.
// armID is "" for the main train, otherwise the dotted arm identifier.
func (t *trainer) runTrain(elements []ElementSpec, f *Field, armID string, depth int) error {
	if depth > MaxArmDepth {
		return fmt.Errorf("beam-splitter arm nesting exceeds %d levels", MaxArmDepth)
	}
	ctx := *t.base // fresh copy per train (shared warnings, independent state)
	bsSeen := 0
	finish := func() error {
		if armID != "" {
			t.arms[armID] = f
		}
		return nil
	}

	for k := range elements {
		spec := &elements[k]
		t.nEl++
		if t.nEl > MaxElements {
			return fmt.Errorf("too many elements (limit %d)", MaxElements)
		}
		switch spec.Type {
		case "propagate":
			dist, err := pf(spec.Params, "distance", 0)
			if err != nil || dist < 0 {
				return fmt.Errorf("element %d: propagate distance must be >= 0", k)
			}
			method, _ := ParseMethod(ps(spec.Params, "method", t.cfg.Method))
			// The distance is always the path length along the beam's own
			// direction of travel. After a mirror the beam folds back, but the
			// field still advances by +ik*s per traveled meter: a round trip
			// of 2L accumulates phase 2kL. Using -z here (the inverse
			// propagator) would cancel the outbound phase and break the
			// physics of folded interferometers.
			if err := Propagate(f, dist, method, &ctx); err != nil {
				return fmt.Errorf("element %d: %v", k, err)
			}
		case "mirror":
			el, err := NewElement(*spec)
			if err != nil {
				return fmt.Errorf("element %d: %v", k, err)
			}
			if err := el.Apply(f, &ctx); err != nil {
				return fmt.Errorf("element %d: %v", k, err)
			}
		case "sensor":
			label := ps(spec.Params, "label", "")
			if label == "" {
				label = fmt.Sprintf("sensor_%d", t.nPl)
			}
			if err := t.recordPlane(f, armID, "sensor_"+strconv.Itoa(t.nPl), label, spec.Params); err != nil {
				return err
			}
		case "beamsplitter":
			r := pfd(spec.Params, "reflectivity", 0.5)
			if r < 0 || r > 1 {
				return fmt.Errorf("element %d: beamsplitter reflectivity must be in [0,1]", k)
			}
			phase := pfd(spec.Params, "phase", 0)
			// Reflected arm: clone BEFORE scaling the transmitted main beam.
			childID := armID + "bs" + strconv.Itoa(bsSeen)
			bsSeen++
			var child *Field
			if r > 0 {
				child = f.Clone()
				child.ScaleAmplitude(complex(0, math.Sqrt(r)) * cexpI(phase))
			}
			// Transmitted arm continues the train.
			f.ScaleAmplitude(complex(math.Sqrt(1-r), 0))
			if child != nil {
				sub, err := reflectedArmElements(spec.Params)
				if err != nil {
					return fmt.Errorf("element %d: %v", k, err)
				}
				if err := t.runTrain(sub, child, childID, depth+1); err != nil {
					return err
				}
			}
		case "combiner":
			if err := t.applyCombiner(f, armID, spec.Params); err != nil {
				return fmt.Errorf("element %d: %v", k, err)
			}
			return finish()
		default:
			el, err := NewElement(*spec)
			if err != nil {
				return fmt.Errorf("element %d: %v", k, err)
			}
			if err := el.Apply(f, &ctx); err != nil {
				return fmt.Errorf("element %d: %v", k, err)
			}
		}
		if ctx.Bandlimit != nil {
			f.ApplyBandlimit(ctx.Bandlimit)
		}
	}
	return finish()
}

// reflectedArmElements extracts the nested arm train of a beamsplitter.
func reflectedArmElements(params map[string]any) ([]ElementSpec, error) {
	raw, ok := params["reflected_arm"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := json.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("beamsplitter reflected_arm: %v", err)
	}
	var arm struct {
		Elements []ElementSpec `json:"elements"`
	}
	if err := json.Unmarshal(b, &arm); err != nil {
		return nil, fmt.Errorf("beamsplitter reflected_arm: %v", err)
	}
	return arm.Elements, nil
}

// applyCombiner coherently sums arm fields into output planes (terminal).
func (t *trainer) applyCombiner(f *Field, armID string, params map[string]any) error {
	raw, ok := params["outputs"]
	if !ok {
		return fmt.Errorf("combiner requires an outputs list")
	}
	outs, ok := raw.([]any)
	if !ok {
		return fmt.Errorf("combiner outputs must be a list")
	}
	for oi, o := range outs {
		om, ok := o.(map[string]any)
		if !ok {
			return fmt.Errorf("combiner output %d must be an object", oi)
		}
		label := "out"
		if s, ok := om["label"].(string); ok && s != "" {
			label = s
		}
		rawW, ok := om["weights"].([]any)
		if !ok {
			return fmt.Errorf("combiner output %d needs a weights list", oi)
		}
		out := NewField(f.N, f.DX, f.Polarized)
		used := false
		for wi, w := range rawW {
			wm, ok := w.(map[string]any)
			if !ok {
				return fmt.Errorf("combiner output %d weight %d must be an object", oi, wi)
			}
			arm, _ := wm["arm"].(string)
			re := asF(wm["re"])
			im := asF(wm["im"])
			var src *Field
			if arm == "main" || arm == armID || arm == "" {
				src = f
			} else {
				var found bool
				src, found = t.arms[arm]
				if !found {
					return fmt.Errorf("combiner references undefined arm %q", arm)
				}
			}
			if src.DX != f.DX {
				return fmt.Errorf("combiner: arm %q grid (dx=%g m) differs from main (dx=%g m); Fraunhofer propagation changes the output grid and cannot be combined directly", arm, src.DX, f.DX)
			}
			wc := complex(re, im)
			for i := range out.Ex {
				out.Ex[i] += wc * src.Ex[i]
				if f.Polarized {
					out.Ey[i] += wc * src.Ey[i]
				}
			}
			used = true
		}
		if !used {
			return fmt.Errorf("combiner output %d has no weights", oi)
		}
		id := armID + ":combiner_" + strconv.Itoa(oi) + "_" + label
		if err := t.recordPlaneRaw(out, armID, id, label, [2]float64{}); err != nil {
			return err
		}
	}
	return nil
}

func asF(v any) float64 {
	if v == nil {
		return 0
	}
	if x, err := asFloat(v); err == nil {
		return x
	}
	return 0
}

func (t *trainer) recordPlane(f *Field, armID, id, label string, params map[string]any) error {
	strehlA := pfd(params, "strehl_aperture", 0)
	strehlD := pfd(params, "strehl_distance", 0)
	return t.recordPlaneRaw(f, armID, id, label, [2]float64{strehlA, strehlD})
}

func (t *trainer) recordPlaneRaw(f *Field, armID, id, label string, strehl [2]float64) error {
	t.nPl++
	if t.nPl > MaxPlanes {
		return fmt.Errorf("too many output planes (limit %d)", MaxPlanes)
	}
	wl := t.cfg.Wavelength
	p := &Plane{
		ID:    id,
		Label: label,
		Path:  armID,
		Size:  f.N,
		DX:    f.DX,
		Ex:    append([]complex128(nil), f.Ex...),
		Ey:    append([]complex128(nil), f.Ey...),
	}
	p.Stats = ComputeStats(f, wl, strehl[0], strehl[1])
	t.pl = append(t.pl, p)
	return nil
}

// randomID returns a short hex identifier for a run.
func randomID() string {
	var b [8]byte
	now := time.Now().UnixNano()
	for i := range b {
		now = now*6364136223846793005 + 1442695040888963407
		b[i] = byte(now >> 40)
	}
	const hex = "0123456789abcdef"
	s := make([]byte, 16)
	for i, c := range b {
		s[2*i] = hex[c>>4]
		s[2*i+1] = hex[c&15]
	}
	return string(s)
}
