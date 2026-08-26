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

// structuralTypes are handled by the simulator rather than the element
// registry.
var structuralTypes = map[string]bool{
	"propagate": true, "sensor": true, "beamsplitter": true, "combiner": true,
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
	if !knownSources[cfg.Source.Type] {
		add("source.type", fmt.Sprintf("unknown source type %q", cfg.Source.Type))
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
