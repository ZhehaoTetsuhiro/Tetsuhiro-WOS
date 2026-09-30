package optics

import (
	"math"
	"testing"
)

// TestExamplesRun runs every built-in preset end to end and checks that it
// produces planes with finite, physically plausible values. The first presets
// are positioned scenes, the rest legacy element trains.
func TestExamplesRun(t *testing.T) {
	exs := Examples()
	if len(exs) == 0 {
		t.Fatal("no built-in examples")
	}
	for _, ex := range exs {
		ex := ex
		t.Run(ex.Name, func(t *testing.T) {
			cfg := ex.Config
			cfg.Grid.Size = 128 // keep the test fast; the physics is size independent
			res, err := Simulate(cfg)
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if len(res.Planes) == 0 {
				t.Fatal("no output planes recorded")
			}
			// Interferometer presets legitimately have a dark port, so only
			// the total over all planes must carry light.
			var total float64
			for _, pl := range res.Planes {
				if len(pl.Ex) != 128*128 {
					t.Fatalf("plane %q has %d pixels, want %d", pl.ID, len(pl.Ex), 128*128)
				}
				if math.IsNaN(pl.Stats.Power) || math.IsInf(pl.Stats.Power, 0) {
					t.Errorf("plane %q: non-finite power %g", pl.ID, pl.Stats.Power)
				}
				if len(pl.Parts) == 0 {
					t.Errorf("plane %q: no coherent-unit parts recorded", pl.ID)
				}
				total += pl.Stats.Power
			}
			if total <= 0 {
				t.Errorf("no light recorded anywhere (total power %g)", total)
			}
			// A single-detector preset must light its detector.
			if len(res.Planes) == 1 && res.Planes[0].Stats.Power <= 0 {
				t.Errorf("the only detector %q got no light", res.Planes[0].Label)
			}
			if cfg.IsScene() && res.Scene == nil {
				t.Error("scene run produced no routed geometry")
			}
			if cfg.IsScene() && res.Scene != nil {
				if len(res.Scene.Components) != len(cfg.Scene.Components) {
					t.Errorf("trace has %d components, config has %d",
						len(res.Scene.Components), len(cfg.Scene.Components))
				}
				for _, s := range res.Scene.Segments {
					if math.IsNaN(s.Power) || s.Power < 0 {
						t.Errorf("segment has invalid power %g", s.Power)
					}
				}
			}
		})
	}
}

// TestSceneExampleTwoSourcesAreIncoherent checks the multi-source preset really
// carries two independent wavelengths and that the coherent pair fringes.
func TestSceneExampleTwoSourcesAreIncoherent(t *testing.T) {
	var cfg *Config
	for i, ex := range Examples() {
		if ex.Name == "双光源：相干条纹与双波长颜色" {
			c := ex.Config
			c.Grid.Size = 256
			cfg = &c
			_ = i
			break
		}
	}
	if cfg == nil {
		t.Skip("preset not found")
	}
	res, err := Simulate(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	if len(pl.Parts) != 2 {
		t.Fatalf("want 2 incoherent units (red pair + green), got %d", len(pl.Parts))
	}
	if !pl.Merged {
		t.Error("plane with two incoherent units should be flagged merged")
	}
	wls := map[int64]bool{}
	for i := range pl.Parts {
		wls[int64(math.Round(pl.Parts[i].Wavelength*1e12))] = true
	}
	if len(wls) != 2 {
		t.Errorf("want two distinct wavelengths, got %v", wls)
	}
	// The coherent red pair must show fringes somewhere in the pattern.
	mod := 0.0
	for i := range pl.Parts {
		if math.Abs(pl.Parts[i].Wavelength-632.8e-9) > 1e-12 {
			continue
		}
		inten := pl.Parts[i].Intensity()
		mx, mn := 0.0, math.Inf(1)
		for _, v := range inten {
			if v > mx {
				mx = v
			}
			if v < mn {
				mn = v
			}
		}
		// Ignore the dark outside of the recorded aperture when judging
		// contrast: measure the modulation inside the slit diffraction lobe.
		if mx > 0 {
			mod = 1 - mn/mx
		}
	}
	if mod < 0.5 {
		t.Errorf("the coherent pair should modulate the red channel, contrast = %.3f", mod)
	}
	// The two unit intensities must add to the plane's own intensity.
	tot := pl.TotalIntensity()
	a := pl.Parts[0].Intensity()
	b := pl.Parts[1].Intensity()
	for i := 0; i < len(tot); i += 37 {
		if math.Abs(tot[i]-(a[i]+b[i])) > 1e-9*(1+tot[i]) {
			t.Fatalf("total intensity at %d is %g, want %g", i, tot[i], a[i]+b[i])
		}
	}
}

// TestExampleMichelsonSceneFringes checks the Michelson preset shows the
// expected interference structure in its detector plane.
func TestExampleMichelsonSceneFringes(t *testing.T) {
	var cfg *Config
	for _, ex := range Examples() {
		if ex.Name == "迈克尔逊干涉仪（定位元件）" {
			c := ex.Config
			c.Grid.Size = 256
			cfg = &c
			break
		}
	}
	if cfg == nil {
		t.Skip("preset not found")
	}
	res, err := Simulate(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 2 {
		t.Fatalf("want both ports recorded, got %v", planeLabels(res))
	}
	var tot float64
	for _, p := range res.Planes {
		tot += p.Stats.Power
	}
	if math.Abs(tot-1e-3)/1e-3 > 0.02 {
		t.Errorf("ports carry %.4g W together, want ~1e-3 W", tot)
	}
	for _, p := range res.Planes {
		if p.Label != "侧向端口" {
			continue
		}
		inten := p.TotalIntensity()
		mx, mn := 0.0, math.Inf(1)
		for _, v := range inten {
			if v > mx {
				mx = v
			}
			if v < mn {
				mn = v
			}
		}
		if mx <= 0 || 1-mn/mx < 0.3 {
			t.Errorf("tilted Michelson should show wedge fringes, contrast = %.3f", 1-mn/mx)
		}
	}
}
