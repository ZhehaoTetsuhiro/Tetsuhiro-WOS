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
// A configuration describes either a *positioned scene* (components with a
// place and a size, one or more sources, the light path derived from the
// geometry) or, for backwards compatibility, a sequential element train
// ("elements"). When both are present the scene wins.
//
//	{
//	  "grid": {"size": 1024, "width": 0.01},
//	  "wavelength": 632.8e-9,
//	  "polarized": true,
//	  "method": "asm",
//	  "sources": [
//	    {"type": "gaussian", "params": {"waist": 0.001}, "pos": {"x":0,"y":0,"z":-0.1}}
//	  ],
//	  "scene": {
//	    "components": [
//	      {"type": "lens", "pos": {"z": 0.05}, "shape": {"kind":"circle","params":{"radius":0.0127}},
//	       "params": {"f": 0.2}},
//	      {"type": "sensor", "pos": {"z": 0.25}, "label": "焦面"}
//	    ]
//	  }
//	}
type Config struct {
	Grid               GridSpec       `json:"grid"`
	Wavelength         float64        `json:"wavelength"`
	Polarized          *bool          `json:"polarized"`
	Method             string         `json:"method"`
	Evanescent         string         `json:"evanescent"`
	EvanescentLimit    float64        `json:"evanescent_limit"`
	BackwardRegularize bool           `json:"backward_regularize"`
	TikhonovAlpha      float64        `json:"tikhonov_alpha"`
	Bandlimit          *BandlimitOpts `json:"bandlimit"`
	Source             SourceSpec     `json:"source"`
	Sources            []SourceSpec   `json:"sources,omitempty"`
	Elements           []ElementSpec  `json:"elements,omitempty"`
	Scene              *SceneSpec     `json:"scene,omitempty"`
}

// AllSources returns every light source of the configuration. A legacy single
// "source" is used when no "sources" list is given; a scene always needs at
// least one entry.
func (c *Config) AllSources() []SourceSpec {
	if len(c.Sources) > 0 {
		out := make([]SourceSpec, len(c.Sources))
		copy(out, c.Sources)
		return out
	}
	return []SourceSpec{c.Source}
}

// IsScene reports whether the configuration is a positioned scene.
func (c *Config) IsScene() bool {
	return c.Scene != nil && len(c.Scene.Components) > 0
}

// PolarizationEnabled returns whether Jones two-component simulation is on
// (default true).
func (c *Config) PolarizationEnabled() bool {
	return c.Polarized == nil || *c.Polarized
}

// Limits enforced by validation to protect memory and CPU.
const (
	// 网格边长：允许小于 64、超过 2048，上限 65536×4（=262144）；超大网格将占用巨量内存。
	MaxGridSize = 65536 * 4
	MinGridSize = 2
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
	Ez    []complex128
	Stats PlaneStats
	// Wavelength and UnitKey identify the coherent unit whose field is stored
	// in Ex/Ey (the strongest one when several units reach this plane).
	Wavelength float64
	UnitKey    string
	// Parts carries the per-coherent-unit field, which is what makes
	// multi-wavelength (colour) rendering and multi-source layouts possible:
	// the units are mutually incoherent, so the total intensity is their sum.
	Parts []PlanePart
	// Merged is set when several incoherent units contributed to this plane;
	// Ex/Ey/Ez then hold an amplitude proxy (Ex = sqrt(I_total)) instead of a
	// physical field. See MergedUnits.
	Merged bool
}

// PlanePart is one coherent unit's contribution to a plane. The units are
// mutually incoherent, so the plane's total intensity is the sum over parts
// and each part keeps its own complex field (Jones vector) — that is what
// makes per-source colour, phase and polarization views possible.
type PlanePart struct {
	Source     int          `json:"source"`
	Label      string       `json:"label"`
	Wavelength float64      `json:"wavelength"`
	Power      float64      `json:"power"`
	Peak       float64      `json:"peak"`
	Ex         []complex128 `json:"-"`
	Ey         []complex128 `json:"-"`
	Ez         []complex128 `json:"-"`
}

// Intensity returns the part's per-pixel intensity in W/m^2.
func (pp *PlanePart) Intensity() []float64 {
	out := make([]float64, len(pp.Ex))
	for i := range pp.Ex {
		out[i] = norm2c(pp.Ex[i])
		if pp.Ey != nil {
			out[i] += norm2c(pp.Ey[i])
		}
		if pp.Ez != nil {
			out[i] += norm2c(pp.Ez[i])
		}
	}
	return out
}

// Bytes estimates the retained memory of a part.
func (pp *PlanePart) Bytes() int64 {
	return int64(len(pp.Ex)+len(pp.Ey)+len(pp.Ez)) * 16
}

// Merged reports whether the plane combines several incoherent units. When it
// does, the plane's own complex field is only an amplitude proxy for the total
// intensity (Ex = sqrt(I), Ey = 0) and phase/polarization views must be taken
// from a part instead.
func (p *Plane) MergedUnits() bool { return p.Merged }

// TotalIntensity returns the summed intensity of every contributing unit.
func (p *Plane) TotalIntensity() []float64 {
	if !p.Merged || len(p.Parts) == 0 {
		return intensityOf(p)
	}
	out := make([]float64, p.Size*p.Size)
	for i := range p.Parts {
		pi := p.Parts[i]
		if len(pi.Ex) != len(out) {
			continue
		}
		for k := range pi.Ex {
			out[k] += norm2c(pi.Ex[k])
			if pi.Ey != nil {
				out[k] += norm2c(pi.Ey[k])
			}
			if pi.Ez != nil {
				out[k] += norm2c(pi.Ez[k])
			}
		}
	}
	return out
}

// DominantPart returns the index of the part carrying the most power.
func (p *Plane) DominantPart() int {
	best, bestP := 0, -1.0
	for i := range p.Parts {
		if p.Parts[i].Power > bestP {
			best, bestP = i, p.Parts[i].Power
		}
	}
	return best
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
	// Scene is the routed geometry of the light path (nil for element trains).
	Scene *SceneTrace
}

// PlaneInfo is the lightweight JSON form of a plane (no field data).
type PlaneInfo struct {
	ID         string      `json:"id"`
	Label      string      `json:"label"`
	Path       string      `json:"path"`
	Size       int         `json:"size"`
	DX         float64     `json:"dx"`
	Stats      PlaneStats  `json:"stats"`
	Wavelength float64     `json:"wavelength,omitempty"`
	Unit       string      `json:"unit,omitempty"`
	Merged     bool        `json:"merged_units,omitempty"`
	Parts      []PlanePart `json:"parts,omitempty"`
}

// Info converts a Plane to its JSON form.
func (p *Plane) Info() PlaneInfo {
	return PlaneInfo{ID: p.ID, Label: p.Label, Path: p.Path, Size: p.Size, DX: p.DX,
		Stats: p.Stats, Wavelength: p.Wavelength, Unit: p.UnitKey, Merged: p.Merged, Parts: p.Parts}
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
	if err := CheckGridMemory(cfg.Grid.Size); err != nil {
		return nil, err
	}
	if cfg.IsScene() {
		return SimulateScene(cfg)
	}
	return simulateTrain(cfg)
}

// simulateTrain runs the legacy sequential element train. Multiple sources are
// handled by simulating one coherent unit at a time (same coherent group and
// wavelength) and adding the resulting intensities.
func simulateTrain(cfg Config) (*Result, error) {
	start := time.Now()
	polarized := cfg.PolarizationEnabled()
	base := &Context{
		Wavelength:         cfg.Wavelength,
		Evanescent:         cfg.Evanescent,
		EvanescentLimit:    cfg.EvanescentLimit,
		BackwardRegularize: cfg.BackwardRegularize,
		TikhonovAlpha:      cfg.TikhonovAlpha,
		Bandlimit:          cfg.Bandlimit,
		Warnings:           &Warnings{},
	}
	if base.Evanescent == "" {
		base.Evanescent = "decay"
	}
	sources := cfg.AllSources()
	for i := range sources {
		if sources[i].Type == "" {
			sources[i].Type = "plane"
		}
	}
	units := splitUnits(sources, cfg.Wavelength, base.Warnings)
	var planes []*Plane
	parts := map[string][]PlanePart{}
	for _, u := range units {
		wl := sources[u.sources[0]].EffectiveWavelength(cfg.Wavelength)
		f, err := BuildUnitField(sources, u.sources, cfg.Grid.Size, cfg.Grid.Width, polarized, wl)
		if err != nil {
			return nil, err
		}
		// The kernel propagates and applies elements at this unit's
		// wavelength, not at the configuration default.
		uctx := *base
		uctx.Wavelength = wl
		t := &trainer{cfg: &cfg, base: &uctx, arms: map[string]*Field{}}
		if err := t.runTrain(cfg.Elements, f, "", 0); err != nil {
			return nil, err
		}
		unit := unitLabel(u.key.group, wl)
		for _, pl := range t.pl {
			pl.Wavelength = wl
			pl.UnitKey = unit
			parts[pl.ID] = append(parts[pl.ID], PlanePart{
				Source: u.sources[0], Label: unit, Wavelength: wl,
				Power: pl.Stats.Power, Peak: pl.Stats.Peak,
				Ex: pl.Ex, Ey: pl.Ey, Ez: pl.Ez,
			})
		}
		planes = mergeUnitPlanes(planes, t.pl)
	}
	finishPlanes(planes, parts)
	// The element train is a sequence, not a layout: synthesize the equivalent
	// positioned scene so the 3D view has something to show and old presets
	// keep working.
	var tr *SceneTrace
	scene := LayoutFromElements(&cfg)
	if scene != nil && len(scene.Components) > 0 {
		var terr error
		tr, terr = TraceScene(scene, sources, cfg.Wavelength)
		if terr != nil {
			base.Warnings.Add("layout", "无法为元件序列合成布局视图: "+terr.Error(), 0)
			tr = nil
		}
	}
	return &Result{
		RunID:      randomID(),
		Size:       cfg.Grid.Size,
		Width:      cfg.Grid.Width,
		DX:         cfg.Grid.Width / float64(cfg.Grid.Size),
		Wavelength: cfg.Wavelength,
		ElapsedMS:  float64(time.Since(start).Microseconds()) / 1000,
		Warnings:   base.Warnings.List(),
		Planes:     planes,
		Scene:      tr,
	}, nil
}

// finishPlanes attaches the per-unit parts to every plane and, when several
// mutually incoherent units contributed, replaces the plane's complex field
// with an amplitude proxy so every intensity consumer sees the true total.
func finishPlanes(planes []*Plane, parts map[string][]PlanePart) {
	for _, pl := range planes {
		ps := parts[pl.ID]
		pl.Parts = ps
		if len(ps) <= 1 {
			continue
		}
		pl.Merged = true
		tot := pl.TotalIntensity()
		proxy := &Field{N: pl.Size, DX: pl.DX, Ex: make([]complex128, len(tot))}
		min, max := math.Inf(1), 0.0
		for i, v := range tot {
			if v > 0 {
				proxy.Ex[i] = complex(math.Sqrt(v), 0)
			}
			if v < min {
				min = v
			}
			if v > max {
				max = v
			}
		}
		pl.Ex, pl.Ey, pl.Ez = proxy.Ex, nil, nil
		// Every statistic must describe the *total* intensity of the merged
		// plane: they used to come from a single unit while the field held
		// another, so the centroid and spot radii disagreed with the power.
		strehl, phMin, phMax := pl.Stats.Strehl, pl.Stats.PhaseMin, pl.Stats.PhaseMax
		pl.Stats = ComputeStats(proxy, pl.Wavelength, 0, 0)
		pl.Stats.Strehl, pl.Stats.PhaseMin, pl.Stats.PhaseMax = strehl, phMin, phMax
		pl.Stats.Peak = max
		pl.Stats.IntensityMin, pl.Stats.IntensityMax = min, max
	}
}

// BuildUnitField sums the fields of one coherent unit's sources on the grid.
func BuildUnitField(sources []SourceSpec, idx []int, size int, width float64, polarized bool, wl float64) (*Field, error) {
	var sum *Field
	for _, si := range idx {
		f, err := BuildSource(sources[si], size, width, polarized, wl)
		if err != nil {
			return nil, fmt.Errorf("source %d: %v", si, err)
		}
		if sum == nil {
			sum = f
			continue
		}
		for i := range sum.Ex {
			sum.Ex[i] += f.Ex[i]
			if polarized {
				sum.Ey[i] += f.Ey[i]
			}
		}
	}
	if sum == nil {
		return nil, fmt.Errorf("no sources")
	}
	return sum, nil
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
			method, err := ParseMethod(ps(spec.Params, "method", t.cfg.Method))
			if err != nil {
				return fmt.Errorf("element %d: %v", k, err)
			}
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
		// Apply bandlimit only after propagation. Applying it after
		// beamsplitters or mirrors introduces asymmetric FFT round-trip
		// noise between the main and child arms (the child arm's sub-train
		// does not see the parent's post-BS bandlimit), which produces
		// structured fringes in nominally dark interferometer ports.
		if ctx.Bandlimit != nil && spec.Type == "propagate" {
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
		Ez:    append([]complex128(nil), f.Ez...),
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
