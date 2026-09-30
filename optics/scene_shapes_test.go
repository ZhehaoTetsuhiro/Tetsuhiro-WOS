package optics

import (
	"math"
	"testing"
)

// shapedScene builds a scene with one shaped aperture and one detector that
// collects the whole grid, lit by a plane wave filling the grid.
func shapedScene(n int, width float64, kind string, params map[string]any) Config {
	pos := Vec3{X: 0, Y: 0, Z: -0.05}
	dir := Vec3{X: 0, Y: 0, Z: 1}
	det := &ShapeSpec{Kind: "rectangle", Params: map[string]any{"width": width, "height": width}}
	return Config{
		Grid:       GridSpec{Size: n, Width: width},
		Wavelength: 632.8e-9,
		Method:     "asm",
		Evanescent: "decay",
		Sources: []SourceSpec{{
			Type: "plane", Pos: &pos, Dir: &dir,
			Params: map[string]any{"power": 1e-3},
		}},
		Scene: &SceneSpec{Components: []ComponentSpec{
			{ID: "ap", Type: "aperture", Label: "孔径", Pos: Vec3{X: 0, Y: 0, Z: 0},
				Shape: &ShapeSpec{Kind: kind, Params: params}},
			{ID: "det", Type: "sensor", Label: "屏", Pos: Vec3{X: 0, Y: 0, Z: 0.05},
				Yaw: math.Pi, Shape: det},
		}},
	}
}

// TestSceneShapeTransmittance verifies that an element's outline really masks
// the field with its own geometry: a plane wave filling the grid must keep the
// fraction of power given by the shape's open area. The tolerance is dominated
// by the binary (pixel) boundary of the outline, so thin shapes — long
// perimeter for their area — need a looser bound.
func TestSceneShapeTransmittance(t *testing.T) {
	const n = 512
	const width = 8e-3 // dx = 15.6 um, so every feature below is at least ~10 px wide
	area := width * width
	cases := []struct {
		name   string
		kind   string
		params map[string]any
		open   float64
		tol    float64
	}{
		{"circle r=1mm", "circle", map[string]any{"radius": 1e-3}, math.Pi * 1e-6, 0.05},
		{"rectangle 1x2mm", "rectangle", map[string]any{"width": 1e-3, "height": 2e-3}, 2e-6, 0.05},
		{"ring rin=0.5mm rout=1mm", "ring", map[string]any{"rin": 5e-4, "rout": 1e-3},
			math.Pi * (1e-6 - 2.5e-7), 0.06},
		{"slit 0.4x4mm", "slit", map[string]any{"width": 4e-4, "height": 4e-3}, 1.6e-6, 0.10},
		{"double_slit 0.4x3mm sep=1.5mm", "double_slit",
			map[string]any{"width": 4e-4, "height": 3e-3, "separation": 1.5e-3}, 2.4e-6, 0.10},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			res, err := Simulate(shapedScene(n, width, tc.kind, tc.params))
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if len(res.Planes) != 1 {
				t.Fatalf("want 1 recorded plane, got %d", len(res.Planes))
			}
			got := res.Planes[0].Stats.Power
			want := 1e-3 * tc.open / area
			rel := math.Abs(got-want) / want
			t.Logf("%s: %.6g W of %.6g W (%.2f%% off)", tc.kind, got, want, rel*100)
			if rel > tc.tol {
				t.Errorf("%s: transmitted %.6g W, want %.6g W (%.1f%% off, tolerance %.0f%%)",
					tc.kind, got, want, rel*100, tc.tol*100)
			}
		})
	}
}

// TestSceneShapeOutsideIsOpaque checks the complement of the transmittance
// test: outside the outline the element is a hard stop. A narrow beam must pass
// through a hole it hits and be stopped completely by one it misses.
func TestSceneShapeOutsideIsOpaque(t *testing.T) {
	beam := func(holeX float64) Config {
		pos := Vec3{X: 0, Y: 0, Z: -0.05}
		dir := Vec3{X: 0, Y: 0, Z: 1}
		return Config{
			Grid:       GridSpec{Size: 256, Width: 8e-3},
			Wavelength: 632.8e-9,
			Method:     "asm",
			Evanescent: "decay",
			Sources: []SourceSpec{{
				Type: "gaussian", Pos: &pos, Dir: &dir,
				Params: map[string]any{"power": 1e-3, "waist": 3e-4},
			}},
			Scene: &SceneSpec{Components: []ComponentSpec{
				{ID: "ap", Type: "aperture", Label: "孔径", Pos: Vec3{X: holeX, Y: 0, Z: 0},
					Shape: &ShapeSpec{Kind: "circle", Params: map[string]any{"radius": 5e-4}}},
				{ID: "det", Type: "sensor", Label: "屏", Pos: Vec3{X: 0, Y: 0, Z: 0.05}, Yaw: math.Pi,
					Shape: &ShapeSpec{Kind: "rectangle", Params: map[string]any{"width": 8e-3, "height": 8e-3}}},
			}},
		}
	}
	power := func(cfg Config) float64 {
		res, err := Simulate(cfg)
		if err != nil {
			t.Fatalf("Simulate: %v", err)
		}
		if len(res.Planes) == 0 {
			return 0 // nothing reached the detector: fully stopped
		}
		return res.Planes[0].Stats.Power
	}
	onAxis := power(beam(0))
	if onAxis < 0.9e-3 {
		t.Errorf("beam through a centred 1 mm hole kept only %.6g W of 1e-3 W", onAxis)
	}
	if off := power(beam(3e-3)); off > 1e-9 {
		t.Errorf("hole 3 mm off the beam transmitted %.6g W, want ~0", off)
	}
}

// beamsplitterScene feeds one plane wave into a splitter and records both
// output ports.
func beamsplitterScene(refl float64) Config {
	pos := Vec3{X: 0, Y: 0, Z: -0.05}
	dir := Vec3{X: 0, Y: 0, Z: 1}
	const w = 8e-3
	face := func() *ShapeSpec {
		return &ShapeSpec{Kind: "rectangle", Params: map[string]any{"width": w, "height": w}}
	}
	return Config{
		Grid:       GridSpec{Size: 256, Width: w},
		Wavelength: 632.8e-9,
		Method:     "asm",
		Evanescent: "decay",
		Sources: []SourceSpec{{
			Type: "plane", Pos: &pos, Dir: &dir,
			Params: map[string]any{"power": 1e-3},
		}},
		Scene: &SceneSpec{Components: []ComponentSpec{
			{ID: "bs", Type: "beamsplitter", Label: "分束器", Pos: Vec3{X: 0, Y: 0, Z: 0},
				Yaw: math.Pi / 4, Shape: face(),
				Params: map[string]any{"reflectivity": refl}},
			{ID: "thru", Type: "sensor", Label: "透射", Pos: Vec3{X: 0, Y: 0, Z: 0.05}, Yaw: math.Pi, Shape: face()},
			{ID: "side", Type: "sensor", Label: "反射", Pos: Vec3{X: -0.05, Y: 0, Z: 0}, Yaw: math.Pi / 2, Shape: face()},
		}},
	}
}

// TestSceneBeamsplitterSplitRatio pins the beamsplitter power convention: at
// reflectivity 0 nothing reaches the side port, and at 0.5 the power is halved
// between the two ports while still conserving energy.
func TestSceneBeamsplitterSplitRatio(t *testing.T) {
	portPower := func(res *Result, label string) float64 {
		for _, pl := range res.Planes {
			if pl.Label == label {
				return pl.Stats.Power
			}
		}
		return 0
	}

	ref, err := Simulate(beamsplitterScene(0))
	if err != nil {
		t.Fatalf("reference run: %v", err)
	}
	pRef := portPower(ref, "透射")
	if pRef <= 0 {
		t.Fatal("reference run recorded no transmitted power")
	}
	if side := portPower(ref, "反射"); side > 1e-12 {
		t.Errorf("reflectivity 0 must send nothing to the side port, got %.6g W", side)
	}

	res, err := Simulate(beamsplitterScene(0.5))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Planes) != 2 {
		t.Fatalf("want both ports recorded, got %v", planeLabels(res))
	}
	thru, side := portPower(res, "透射"), portPower(res, "反射")
	for name, p := range map[string]float64{"透射": thru, "反射": side} {
		if rel := math.Abs(p-pRef/2) / (pRef / 2); rel > 0.02 {
			t.Errorf("%s port %.9g W, want %.9g W (%.2f%% off)", name, p, pRef/2, rel*100)
		}
	}
	if sum := thru + side; math.Abs(sum-pRef) > 1e-9*pRef {
		t.Errorf("ports must conserve energy: %.9g + %.9g vs %.9g W", thru, side, pRef)
	}
}
