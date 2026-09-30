package optics

import (
	"fmt"
	"math"
)

// Issue is one configuration validation problem.
type Issue struct {
	Path    string
	Message string
}

var knownSources = map[string]bool{
	"plane": true, "gaussian": true, "laguerre_gaussian": true,
	"hermite_gaussian": true, "bessel": true, "spherical": true,
}

// validateScene checks a positioned scene: component types, outlines, and that
// the configuration has something to record the light.
func validateScene(cfg *Config, add func(path, msg string)) error {
	scene := cfg.Scene
	if len(scene.Components) > MaxElements {
		add("scene.components", fmt.Sprintf("too many components (%d, limit %d)", len(scene.Components), MaxElements))
	}
	sensors := 0
	ids := map[string]int{}
	for i := range scene.Components {
		c := &scene.Components[i]
		path := fmt.Sprintf("scene.components[%d]", i)
		if _, err := componentBehavior(c.Type); err != nil {
			add(path+".type", err.Error())
		}
		if _, err := shapeFromSpec(c.Shape); err != nil {
			add(path+".shape", err.Error())
		}
		if math.IsNaN(c.Pos.X) || math.IsNaN(c.Pos.Y) || math.IsNaN(c.Pos.Z) ||
			math.IsInf(c.Pos.X, 0) || math.IsInf(c.Pos.Y, 0) || math.IsInf(c.Pos.Z, 0) {
			add(path+".pos", "must be finite")
		}
		if math.IsNaN(c.Yaw) || math.IsNaN(c.Pitch) || math.IsNaN(c.Roll) {
			add(path+".orientation", "must be finite")
		}
		if c.ID != "" {
			if prev, dup := ids[c.ID]; dup {
				add(path+".id", fmt.Sprintf("duplicate component id %q (also used by component %d)", c.ID, prev))
			}
			ids[c.ID] = i
		}
		if componentClass(c.Type) == "detector" {
			sensors++
		}
	}
	if sensors == 0 {
		add("scene.components", "no detector (sensor) in the scene: add one to record the light")
	}
	if len(cfg.Sources) == 0 && cfg.Source.Type == "" {
		add("sources", "a scene needs at least one light source")
	}
	// Check the effective source list: a scene may carry a single `source`
	// (not covered by the top-level check) and any source may be mis-placed.
	for i := range cfg.AllSources() {
		s := &cfg.AllSources()[i]
		path := fmt.Sprintf("sources[%d]", i)
		if len(cfg.Sources) == 0 {
			path = "source"
			if !knownSources[s.Type] {
				add("source.type", fmt.Sprintf("unknown source type %q", s.Type))
			}
			if s.Wavelength < 0 {
				add("source.wavelength", "must be > 0")
			}
		}
		if p := s.Position(); !finiteVec(p) {
			add(path+".pos", "must be finite")
		}
		if d := s.Direction(); !finiteVec(d) {
			add(path+".dir", "must be finite")
		}
	}
	return nil
}

// finiteVec reports whether every component of v is a finite number.
func finiteVec(v Vec3) bool {
	return !math.IsNaN(v.X) && !math.IsNaN(v.Y) && !math.IsNaN(v.Z) &&
		!math.IsInf(v.X, 0) && !math.IsInf(v.Y, 0) && !math.IsInf(v.Z, 0)
}

// ValidateSceneConfig validates only the scene part of a configuration.
func ValidateSceneConfig(cfg *Config) []Issue {
	var out []Issue
	if cfg == nil || !cfg.IsScene() {
		return out
	}
	_ = validateScene(cfg, func(path, msg string) { out = append(out, Issue{Path: path, Message: msg}) })
	return out
}

// ValidateConfig checks a Config and returns all issues found (empty = OK).
func ValidateConfig(cfg *Config) []Issue {
	var out []Issue
	add := func(path, msg string) { out = append(out, Issue{Path: path, Message: msg}) }

	if cfg.Grid.Size < MinGridSize || cfg.Grid.Size > MaxGridSize {
		add("grid.size", fmt.Sprintf("must be between %d and %d", MinGridSize, MaxGridSize))
	} else if cfg.Grid.Size%2 != 0 {
		add("grid.size", "must be even")
	}
	if !(cfg.Grid.Width > 0) || math.IsNaN(cfg.Grid.Width) {
		add("grid.width", "must be > 0")
	}
	if !(cfg.Wavelength > 0) || math.IsNaN(cfg.Wavelength) {
		add("wavelength", "must be > 0")
	}
	if _, err := ParseMethod(cfg.Method); err != nil {
		add("method", err.Error())
	}
	switch cfg.Evanescent {
	case "", "decay", "zero":
	default:
		add("evanescent", "must be decay or zero")
	}
	if cfg.EvanescentLimit < 0 {
		add("evanescent_limit", "must be >= 0")
	}
	if cfg.Bandlimit != nil {
		if cfg.Bandlimit.Fraction <= 0 || cfg.Bandlimit.Fraction > 1 {
			add("bandlimit.fraction", "must be in (0,1]")
		}
		if cfg.Bandlimit.Sigma <= 0 {
			add("bandlimit.sigma", "must be > 0")
		}
	}
	if cfg.IsScene() {
		if err := validateScene(cfg, add); err != nil {
			return out
		}
	} else if len(cfg.Sources) == 0 && !knownSources[cfg.Source.Type] {
		// A legacy train may take its light from sources[] alone.
		add("source.type", fmt.Sprintf("unknown source type %q", cfg.Source.Type))
	}
	for i := range cfg.Sources {
		s := &cfg.Sources[i]
		if !knownSources[s.Type] {
			add(fmt.Sprintf("sources[%d].type", i), fmt.Sprintf("unknown source type %q", s.Type))
		}
		if s.Wavelength < 0 {
			add(fmt.Sprintf("sources[%d].wavelength", i), "must be > 0")
		}
		if p := s.Position(); !finiteVec(p) {
			add(fmt.Sprintf("sources[%d].pos", i), "must be finite")
		}
		if d := s.Direction(); !finiteVec(d) {
			add(fmt.Sprintf("sources[%d].dir", i), "must be finite")
		}
	}
	if cfg.IsScene() {
		// A scene and an element train may coexist while a user migrates a
		// layout; the scene takes precedence (see Config.IsScene).
	}

	// Walk all element trains (main + beam-splitter arms) collecting arm ids
	// so combiner references can be checked statically.
	type walkState struct {
		elements int
		planes   int
	}
	st := &walkState{}
	var walk func(elements []ElementSpec, armID string, depth int)
	walk = func(elements []ElementSpec, armID string, depth int) {
		if depth > MaxArmDepth {
			add("elements", fmt.Sprintf("beam-splitter nesting exceeds %d levels", MaxArmDepth))
			return
		}
		bsSeen := 0
		for i := range elements {
			el := &elements[i]
			st.elements++
			path := fmt.Sprintf("elements[%d]", i)
			if armID != "" {
				path = "arm " + armID + " " + path
			}
			switch el.Type {
			case "propagate":
				d, err := pf(el.Params, "distance", 0)
				if err != nil || d < 0 || math.IsNaN(d) {
					add(path, "propagate distance must be >= 0")
				}
				if m := ps(el.Params, "method", ""); m != "" {
					if _, err := ParseMethod(m); err != nil {
						add(path, err.Error())
					}
				}
			case "combiner":
				if i != len(elements)-1 {
					add(path, "combiner must be the last element of its train")
				}
				outs, _ := el.Params["outputs"].([]any)
				if len(outs) == 0 {
					add(path, "combiner requires an outputs list")
				}
			case "sensor":
				st.planes++
			case "beamsplitter":
				r := pfd(el.Params, "reflectivity", 0.5)
				if r < 0 || r > 1 {
					add(path, "beamsplitter reflectivity must be in [0,1]")
				}
				sub, err := reflectedArmElements(el.Params)
				if err != nil {
					add(path, err.Error())
				} else if len(sub) > 0 {
					childID := armID + "bs" + fmt.Sprint(bsSeen)
					bsSeen++
					walk(sub, childID, depth+1)
				}
			case "mirror":
				if _, ok := elementRegistry[el.Type]; !ok {
					add(path, "mirror element not registered")
				}
			default:
				if _, ok := elementRegistry[el.Type]; !ok {
					add(path, fmt.Sprintf("unknown element type %q", el.Type))
				}
			}
		}
	}
	walk(cfg.Elements, "", 0)

	if st.elements > MaxElements {
		add("elements", fmt.Sprintf("too many elements (%d, limit %d)", st.elements, MaxElements))
	}
	if st.planes > MaxPlanes {
		add("elements", fmt.Sprintf("too many output planes (%d, limit %d)", st.planes, MaxPlanes))
	}
	return out
}
