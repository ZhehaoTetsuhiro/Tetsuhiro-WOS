package optics

import (
	"math"
	"testing"
)

// bigDet returns a detector whose outline covers the whole grid, so a port
// measurement is not limited by the detector's active area (the grid is a
// square whose corners lie sqrt(2)/2 * width from the centre).
func bigDet(label string, pos Vec3, yaw float64, width float64) ComponentSpec {
	r := width * 0.75
	return ComponentSpec{Type: "sensor", Label: label, Pos: pos, Yaw: yaw, Shape: circleShape(r)}
}

// TestSceneMirrorSplitIsLossless pins the mirror power convention: the
// reflectivity is an amplitude factor applied once, so a mirror at 0.5 sends
// 25% of the power back and passes the other 75% — nothing vanishes.
func TestSceneMirrorSplitIsLossless(t *testing.T) {
	const w = 8e-3
	pos := v3(0, 0, -0.05)
	dir := v3(0, 0, 1)
	cfg := Config{
		Grid:       GridSpec{Size: 256, Width: w},
		Wavelength: 632.8e-9,
		Method:     "asm",
		Evanescent: "decay",
		Sources: []SourceSpec{{Type: "plane", Pos: &pos, Dir: &dir,
			Params: map[string]any{"power": 1e-3}}},
		Scene: &SceneSpec{Components: []ComponentSpec{
			{Type: "mirror", Label: "半透镜", Pos: v3(0, 0, 0), Yaw: -math.Pi / 4,
				Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 0.5}},
			bigDet("反射", v3(0.05, 0, 0), -math.Pi/2, w),
			bigDet("透射", v3(0, 0, 0.05), math.Pi, w),
		}},
	}
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	got := map[string]float64{}
	for _, pl := range res.Planes {
		got[pl.Label] = pl.Stats.Power
	}
	if len(res.Planes) != 2 {
		t.Fatalf("want both ports, got %v", planeLabels(res))
	}
	if want := 0.25e-3; math.Abs(got["反射"]-want)/want > 0.01 {
		t.Errorf("reflected port %.6g W, want %.6g W (amplitude 0.5 → 25%% of the power)", got["反射"], want)
	}
	if want := 0.75e-3; math.Abs(got["透射"]-want)/want > 0.01 {
		t.Errorf("transmitted port %.6g W, want %.6g W", got["透射"], want)
	}
	if sum := got["反射"] + got["透射"]; math.Abs(sum-1e-3)/1e-3 > 1e-3 {
		t.Errorf("a lossless split must conserve power: got %.9g W of 1e-3 W", sum)
	}
}

// TestSceneUnequalArmsConservePower guards the evaluation order: a visit fed by
// a short path and a long one must not be merged before its long contribution
// exists, which used to lose half the light whenever the arms differed.
func TestSceneUnequalArmsConservePower(t *testing.T) {
	src := gaussSrc(v3(0, 0, -0.05), v3(0, 0, 1), 2e-3)
	bs := ComponentSpec{Type: "beamsplitter", Label: "分束器", Pos: v3(0, 0, 0), Yaw: -math.Pi / 4,
		Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 0.5}}
	mz := ComponentSpec{Type: "mirror", Pos: v3(0, 0, 0.4), Yaw: 0,
		Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 1.0}}
	mx := ComponentSpec{Type: "mirror", Pos: v3(0.1, 0, 0), Yaw: math.Pi / 2,
		Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 1.0}}
	back := bigDet("回光源端口", v3(0, 0, -0.05), 0, 8e-3)
	side := bigDet("侧向端口", v3(-0.05, 0, 0), math.Pi/2, 8e-3)
	res, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, bs, mz, mx, back, side))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	sum := 0.0
	for _, pl := range res.Planes {
		sum += pl.Stats.Power
	}
	if len(res.Planes) != 2 {
		t.Fatalf("want both ports, got %v", planeLabels(res))
	}
	if math.Abs(sum-1e-3)/1e-3 > 1e-4 {
		t.Errorf("unequal arms: ports carry %.9g W of 1e-3 W (light was dropped)", sum)
	}
}

// TestSceneRetroReflectorReturnsLight checks the retro-reflector alias: it must
// send the beam back along the path it came from and still record a plane.
func TestSceneRetroReflectorReturnsLight(t *testing.T) {
	src := planeSrc(v3(0, 0, -0.06), v3(0, 0, 1))
	rr := ComponentSpec{Type: "retro_reflector", Label: "角反射器", Pos: v3(0, 0, 0), Yaw: 0,
		Shape: circleShape(6e-3)}
	back := bigDet("返回端口", v3(0, 0, -0.045), 0, 8e-3)
	res, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, rr, back))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("want the return port, got %v", planeLabels(res))
	}
	if p := res.Planes[0].Stats.Power; math.Abs(p-1e-3)/1e-3 > 0.02 {
		t.Errorf("retro-reflected power %.6g W, want ~1e-3 W", p)
	}
}

// TestSceneSensorOutlineIsActiveArea verifies that a detector measures only the
// light falling inside its own outline.
func TestSceneSensorOutlineIsActiveArea(t *testing.T) {
	const w = 8e-3
	src := planeSrc(v3(0, 0, -0.05), v3(0, 0, 1))
	small := ComponentSpec{Type: "sensor", Label: "小靶面", Pos: v3(0, 0, 0.05), Yaw: math.Pi,
		Shape: circleShape(5e-4)}
	res, err := Simulate(sceneCfg(256, w, []SourceSpec{src}, small))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	want := 1e-3 * math.Pi * 5e-4 * 5e-4 / (w * w)
	got := res.Planes[0].Stats.Power
	if math.Abs(got-want)/want > 0.05 {
		t.Errorf("0.5 mm detector measured %.6g W, want %.6g W (its own area only)", got, want)
	}
}

// TestSceneSourceWavelengthDrivesKernel checks that a per-source wavelength
// actually reaches propagation and the elements: an explicit 532 nm source must
// give exactly the same result as a run configured at 532 nm.
func TestSceneSourceWavelengthDrivesKernel(t *testing.T) {
	build := func(cfgWL float64) *Result {
		lens := ComponentSpec{Type: "lens", Label: "透镜", Pos: v3(0, 0, 0.05),
			Shape: circleShape(4e-3), Params: map[string]any{"focal_length": 0.2}}
		sensor := bigDet("焦面", v3(0, 0, 0.15), math.Pi, 8e-3)
		src := SourceSpec{Type: "plane", Pos: v3p(0, 0, 0), Dir: v3p(0, 0, 1),
			Wavelength: 532e-9, Params: map[string]any{"power": 1e-3}}
		cfg := sceneCfg(256, 8e-3, []SourceSpec{src}, lens, sensor)
		cfg.Wavelength = cfgWL
		res, err := Simulate(cfg)
		if err != nil {
			t.Fatalf("Simulate(%g): %v", cfgWL, err)
		}
		return res
	}
	mixed := build(632.8e-9) // config default red, source green
	pure := build(532e-9)    // everything green
	if len(mixed.Planes) == 0 || len(pure.Planes) == 0 {
		t.Fatal("no planes recorded")
	}
	a, b := mixed.Planes[0].Stats, pure.Planes[0].Stats
	if rel := math.Abs(a.Peak-b.Peak) / b.Peak; rel > 1e-6 {
		t.Errorf("mixed-wavelength run peak %.9g vs pure green %.9g (rel %.3g): the kernel did not use the source wavelength",
			a.Peak, b.Peak, rel)
	}
	if wl := mixed.Planes[0].Wavelength; math.Abs(wl-532e-9) > 1e-18 {
		t.Errorf("plane wavelength = %g, want 532 nm", wl)
	}
}

// func v3p is a helper for taking the address of a Vec3 literal.
func v3p(x, y, z float64) *Vec3 { return &Vec3{X: x, Y: y, Z: z} }
