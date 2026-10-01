package optics

import (
	"math"
	"testing"
)

// TestProfileOfMergedPlaneUnits cuts a plane that combined several mutually
// incoherent units. Such a plane keeps only an amplitude proxy for the total
// intensity in Ex and carries no Ey of its own (the parts hold the fields), so
// ProfileOf used to index Ey unconditionally and panicked — which is how the
// multi-wavelength 衍射光栅光谱 preset exposed it.
func TestProfileOfMergedPlaneUnits(t *testing.T) {
	pol := false
	const n, width = 64, 4e-3
	cfg := Config{
		Grid:       GridSpec{Size: n, Width: width},
		Wavelength: 600e-9,
		Polarized:  &pol,
		Method:     string(MethodASM),
		Sources: []SourceSpec{
			{ID: "a", Type: "plane", Wavelength: 500e-9, Params: map[string]any{"power": 5e-4}},
			{ID: "b", Type: "plane", Wavelength: 650e-9, Params: map[string]any{"power": 5e-4}},
		},
		Scene: &SceneSpec{Components: []ComponentSpec{
			{ID: "det", Type: "sensor", Label: "out", Pos: v3(0, 0, 0.05), Yaw: math.Pi,
				Shape: &ShapeSpec{Kind: "rectangle", Params: map[string]any{"width": width, "height": width}}},
		}},
	}
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("want 1 plane, got %d", len(res.Planes))
	}
	pl := res.Planes[0]
	if !pl.MergedUnits() || len(pl.Parts) != 2 {
		t.Fatalf("want a merged plane with 2 parts, got merged=%v parts=%d", pl.Merged, len(pl.Parts))
	}
	if pl.Ey != nil || pl.Ez != nil {
		t.Fatalf("a merged plane carries no Ey/Ez of its own (Ey=%d, Ez=%d samples)", len(pl.Ey), len(pl.Ez))
	}
	tot := pl.TotalIntensity()
	// Two plane waves at different wavelengths add up to a uniform intensity.
	for _, axis := range []string{"x", "y"} {
		prof, err := pl.ProfileOf(axis, KindIntensity, nil)
		if err != nil {
			t.Fatalf("ProfileOf(%q) on a merged plane: %v", axis, err)
		}
		if len(prof.V) != n {
			t.Fatalf("ProfileOf(%q): got %d samples, want %d", axis, len(prof.V), n)
		}
		for _, i := range []int{n/2 - 8, n / 2, n/2 + 8} {
			if tot[i] <= 0 {
				t.Fatalf("total intensity vanished at %d", i)
			}
			if rel := math.Abs(prof.V[i]-tot[i]) / tot[i]; rel > 0.02 {
				t.Errorf("ProfileOf(%q)[%d] = %.6g, want the merged intensity %.6g (%.1f%% off)", axis, i, prof.V[i], tot[i], rel*100)
			}
		}
	}
}
