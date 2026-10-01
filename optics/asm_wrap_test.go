package optics

import (
	"math"
	"testing"
)

// TestASMWrapWarning pins the diagnostic that makes the failure mode of a
// far-field geometry on a fixed grid visible: a plane wave on a fine-pitch
// grating propagated a long way. The orders land at z·λ/Λ (31.6 mm for
// Λ = 20 µm, z = 1 m), far outside the 8 mm window, so the circular convolution
// folds them back and the run used to return a structureless beat pattern with
// no warning at all.
func TestASMWrapWarning(t *testing.T) {
	bad := Config{
		Grid: GridSpec{Size: 1024, Width: 0.008}, Wavelength: 632.8e-9,
		Method: "asm", Evanescent: "decay",
		Sources: []SourceSpec{{ID: "src", Label: "平面波", Type: "plane",
			Pos: &Vec3{X: 0, Y: 0, Z: -0.05}, Dir: &Vec3{X: 0, Y: 0, Z: 1},
			Params: map[string]any{"power": 1e-3}}},
		Scene: &SceneSpec{Components: []ComponentSpec{
			{ID: "g", Type: "grating", Label: "光栅", Pos: v3(0, 0, 0),
				Params: map[string]any{"kind": "phase_sin", "period": 2e-5, "modulation": 2.0}},
			{ID: "det", Type: "sensor", Label: "远场", Pos: v3(0, 0, 1.0), Yaw: math.Pi,
				Shape: RectOutline(0.008, 0.008)},
		}},
	}
	res, err := Simulate(bad)
	if err != nil {
		t.Fatal(err)
	}
	fired := 0.0
	for _, w := range res.Warnings {
		if w.Code != "asm_alias_wrap" {
			continue
		}
		fired++
		if w.Value < 2 {
			t.Errorf("wrap ratio %.2f is not an unambiguous failure", w.Value)
		}
	}
	if fired == 0 {
		t.Fatalf("no asm_alias_wrap warning for the geometry that loses the orders: %v", planeLabels(res))
	}

	// The same far-field pattern taken in the focal plane of a Fourier lens is
	// a legitimate geometry on this grid and must stay silent.
	for _, ex := range Examples() {
		if ex.Name != "衍射光栅光谱" {
			continue
		}
		good, err := Simulate(ex.Config)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range good.Warnings {
			if w.Code == "asm_alias_wrap" {
				t.Errorf("the Fourier-lens preset must not warn about wrap-around: %+v", w)
			}
		}
	}

	// And a near-field step of the same field carries nothing that far.
	short := bad
	short.Scene = &SceneSpec{Components: []ComponentSpec{
		{ID: "g", Type: "grating", Label: "光栅", Pos: v3(0, 0, 0),
			Params: map[string]any{"kind": "phase_sin", "period": 2e-5, "modulation": 2.0}},
		{ID: "det", Type: "sensor", Label: "近场", Pos: v3(0, 0, 0.01), Yaw: math.Pi,
			Shape: RectOutline(0.008, 0.008)},
	}}
	res, err = Simulate(short)
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range res.Warnings {
		if w.Code == "asm_alias_wrap" {
			t.Errorf("a 10 mm step must not warn about wrap-around: %+v", w)
		}
	}
}
