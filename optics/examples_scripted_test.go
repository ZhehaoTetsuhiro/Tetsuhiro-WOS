package optics

import (
	"encoding/json"
	"math"
	"testing"
)

// scriptedScene builds a config around one scripted-element scene. The
// definition files come from the repository's elements/ directory, so these
// tests cover the shipped example as well as the machinery.
func scriptedScene(t *testing.T, defsDir string, size int, width float64, wl float64) Config {
	t.Helper()
	rep := ReloadElementDefinitions([]string{defsDir})
	if len(rep.Errors) != 0 {
		t.Fatalf("loading definitions from %s: %+v", defsDir, rep.Errors)
	}
	t.Cleanup(func() { ReloadElementDefinitions([]string{t.TempDir()}) })
	pol := false
	return Config{
		Grid: GridSpec{Size: size, Width: width}, Wavelength: wl, Polarized: &pol,
		Method: "asm", Evanescent: "decay",
		Bandlimit: &BandlimitOpts{Fraction: 0.9, Sigma: 0.05},
	}
}

// A scripted metalens must focus a plane wave into a clean Airy pattern at its
// design focal length: first ring at 1.635·λf/D carrying ~1.75 % of the peak,
// with the detector reading (nearly) the whole source power.
func TestScriptedMetalensFocus(t *testing.T) {
	bl := BandlimitOpts{Fraction: 0.9, Sigma: 0.05}
	cfg := scriptedScene(t, "../elements", 512, 4e-3, 632.8e-9)
	cfg.Bandlimit = &bl
	const f, D, power = 0.3, 3e-3, 1e-3
	cfg.Sources = []SourceSpec{{ID: "src", Label: "平面波", Type: "plane",
		Pos: &Vec3{X: 0, Y: 0, Z: -0.02}, Dir: &Vec3{X: 0, Y: 0, Z: 1}, Params: map[string]any{"power": power}}}
	cfg.Scene = &SceneSpec{Components: []ComponentSpec{
		{ID: "ml", Type: "metalens", Label: "超表面透镜", Pos: v3(0, 0, 0),
			Shape:  CircleOutline(D / 2),
			Params: map[string]any{"f": f, "sign": 1.0}},
		{ID: "det", Type: "sensor", Label: "焦面", Pos: v3(0, 0, f), Yaw: math.Pi,
			Shape:  CircleOutline(D / 2),
			Params: map[string]any{"strehl_aperture": D / 2, "strehl_distance": f}},
	}}
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("got %d planes, want 1", len(res.Planes))
	}
	pl := res.Planes[0]
	// The element's outline clips the plane wave to the aperture, and the
	// detector then reads everything that passed: the expected reading is the
	// aperture's share of the grid, not the source power (πR²/width²).
	wantPower := power * math.Pi * (D / 2) * (D / 2) / (cfg.Grid.Width * cfg.Grid.Width)
	if got := pl.Stats.Power; math.Abs(got-wantPower)/wantPower > 0.02 {
		t.Errorf("detector power %.4g W, want %.4g W (aperture share of the source, off %.1f%%)",
			got, wantPower, 100*math.Abs(got-wantPower)/wantPower)
	}
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, peak, at0 := halfMaxEdges(prof)
	if math.Abs(at0) > 2*pl.DX {
		t.Errorf("focal spot centred at %g m, want 0", at0)
	}
	ring := 1.635 * cfg.Wavelength * f / D
	got, at := lobeNear(prof, pl.DX, ring, 4*pl.DX)
	if off := math.Abs(at - ring); off > 3*pl.DX {
		t.Errorf("first Airy ring at %g m, want %g m (off %.1f px)", at, ring, off/pl.DX)
	}
	if rel := math.Abs(got/peak - 0.0175); rel > 0.30 {
		t.Errorf("first ring is %.2f%% of the axial peak, want ~1.75%%", 100*got/peak)
	}
	if pl.Stats.Strehl < 0.9 {
		t.Errorf("Strehl = %.3f, want > 0.9 for an ideal hyperbolic phase", pl.Stats.Strehl)
	}
}

// The phase profile is written for the design wavelength, so another wavelength
// focuses at f·λ0/λ — the diffraction chromaticity of a real metasurface. At
// 400 nm the design 0.3 m focus is 0.475 m.
func TestScriptedMetalensChromaticShift(t *testing.T) {
	const f0, wl0, D = 0.3, 632.8e-9, 3e-3
	wl := 400e-9
	zFocus := f0 * wl0 / wl // 0.4746 m

	run := func(detZ float64) *Plane {
		cfg := scriptedScene(t, "../elements", 512, 4e-3, wl)
		cfg.Sources = []SourceSpec{{ID: "src", Type: "plane",
			Pos: &Vec3{X: 0, Y: 0, Z: -0.02}, Dir: &Vec3{X: 0, Y: 0, Z: 1}, Params: map[string]any{"power": 1e-3}}}
		cfg.Scene = &SceneSpec{Components: []ComponentSpec{
			{ID: "ml", Type: "metalens", Pos: v3(0, 0, 0), Shape: CircleOutline(D / 2),
				Params: map[string]any{"f": f0, "wl0": wl0, "sign": 1.0}},
			{ID: "det", Type: "sensor", Pos: v3(0, 0, detZ), Yaw: math.Pi,
				Shape:  CircleOutline(D / 2),
				Params: map[string]any{"strehl_aperture": D / 2, "strehl_distance": detZ}},
		}}
		res, err := Simulate(cfg)
		if err != nil {
			t.Fatal(err)
		}
		return res.Planes[0]
	}

	focused := run(zFocus)
	defocused := run(f0)
	if focused.Stats.Peak <= 5*defocused.Stats.Peak {
		t.Errorf("peak at the shifted focus %.3g is not clearly above the design-plane peak %.3g",
			focused.Stats.Peak, defocused.Stats.Peak)
	}
	// At the shifted focus the pattern is again the Airy pattern of this
	// wavelength; at the design focus the light is spread out.
	prof, err := focused.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, peak, at0 := halfMaxEdges(prof)
	if math.Abs(at0) > 2*focused.DX {
		t.Errorf("shifted focus centred at %g m, want 0", at0)
	}
	ring := 1.635 * wl * zFocus / D
	got, at := lobeNear(prof, focused.DX, ring, 4*focused.DX)
	if off := math.Abs(at - ring); off > 3*focused.DX {
		t.Errorf("first ring at the shifted focus: %g m, want %g m (off %.1f px)", at, ring, off/focused.DX)
	}
	if rel := math.Abs(got/peak - 0.0175); rel > 0.35 {
		t.Errorf("first ring is %.2f%% of the peak, want ~1.75%%", 100*got/peak)
	}
}

// A scripted mirror takes the same optical path as the built-in concave mirror
// of the same curvature: same routed layout, same planes point by point.
func TestScriptedMirrorMatchesNative(t *testing.T) {
	defsDir := t.TempDir()
	writeDef(t, defsDir, "asphere_mirror", `{
	  "name": "asphere_mirror", "label": "非球面镜", "behavior": "mirror",
	  "params": [{"key": "R", "kind": "float", "unit": "m", "default": 0.5}],
	  "phase": "-k*r*r/R"
	}`)
	const R = 0.5
	run := func(mirrorType string, params map[string]any) *Result {
		cfg := scriptedScene(t, defsDir, 256, 6e-3, 632.8e-9)
		cfg.Sources = []SourceSpec{{ID: "src", Type: "plane",
			Pos: &Vec3{X: 0, Y: 0, Z: -0.05}, Dir: &Vec3{X: 0, Y: 0, Z: 1}, Params: map[string]any{"power": 1e-3}}}
		cfg.Scene = &SceneSpec{Components: []ComponentSpec{
			{ID: "m", Type: mirrorType, Pos: v3(0, 0, 0), Shape: CircleOutline(2e-3), Params: params},
			{ID: "focus", Type: "sensor", Pos: v3(0, 0, -R/2), Yaw: 0, Shape: CircleOutline(2e-3)},
		}}
		res, err := Simulate(cfg)
		if err != nil {
			t.Fatalf("%s: %v", mirrorType, err)
		}
		return res
	}
	scripted := run("asphere_mirror", map[string]any{"R": R})
	native := run("concave_mirror", map[string]any{"radius": R})
	if len(scripted.Planes) != 1 || len(native.Planes) != 1 {
		t.Fatalf("planes: scripted %d, native %d", len(scripted.Planes), len(native.Planes))
	}
	a, b := scripted.Planes[0], native.Planes[0]
	if a.Stats.Power == 0 {
		t.Fatal("the scripted mirror delivered no light to the detector")
	}
	for i := range a.Ex {
		if math.Abs(real(a.Ex[i])-real(b.Ex[i])) > 1e-12 || math.Abs(imag(a.Ex[i])-imag(b.Ex[i])) > 1e-12 {
			t.Fatalf("plane differs at pixel %d: scripted %v vs native %v", i, a.Ex[i], b.Ex[i])
		}
	}
	// The reflected beam is focused at R/2 behind the mirror: the focus sits on
	// axis and is much brighter than the defocused ring of the same layout.
	if math.Hypot(a.Stats.CentroidX, a.Stats.CentroidY) > 2*a.DX {
		t.Errorf("focus centroid at (%g, %g), want on axis", a.Stats.CentroidX, a.Stats.CentroidY)
	}
}

// The sine amplitude grating example: t(x) = (1 + m·cos(2πx/Λ))/(1+m) splits a
// plane wave into orders at x_q = q·f·λ/Λ with the closed-form weights
// 1 : (m/2)² : (m/2)² and a mean transmission of (1 + m²/2)/(1+m)².
func TestScriptedSineGratingOrders(t *testing.T) {
	const Lambda, m = 1e-4, 0.8
	const f = 0.3
	cfg := scriptedScene(t, "../elements", 1024, 8e-3, 632.8e-9)
	cfg.Sources = []SourceSpec{{ID: "src", Type: "plane",
		Pos: &Vec3{X: 0, Y: 0, Z: -0.05}, Dir: &Vec3{X: 0, Y: 0, Z: 1},
		Params: map[string]any{"power": 1e-3}}}
	cfg.Scene = &SceneSpec{Components: []ComponentSpec{
		{ID: "g", Type: "sine_amp_grating", Pos: v3(0, 0, 0),
			Params: map[string]any{"Lambda": Lambda, "m": m}},
		{ID: "lens", Type: "lens", Label: "傅里叶透镜", Pos: v3(0, 0, 0.02),
			Params: map[string]any{"f": f}},
		{ID: "det", Type: "sensor", Label: "衍射级（焦面）", Pos: v3(0, 0, 0.32), Yaw: math.Pi,
			Shape: &ShapeSpec{Kind: "rectangle", Params: map[string]any{"width": 8e-3, "height": 8e-3}}},
	}}
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	peakAt := func(center float64) (float64, float64) {
		v, at := lobeNear(prof, pl.DX, center, 3*pl.DX)
		return v, at
	}
	order := f * cfg.Wavelength / Lambda // 1.898 mm
	// No element outline: the grating spans the window, so ±1 orders are the
	// only non-zero lobes besides 0.
	i0, ax0 := peakAt(0)
	i1p, axp := peakAt(order)
	i1m, axm := peakAt(-order)
	if math.Abs(ax0) > 2*pl.DX {
		t.Errorf("0 order at %g m, want 0", ax0)
	}
	if off := math.Abs(axp - order); off > 2*pl.DX {
		t.Errorf("+1 order at %g m, want %g m (off %.1f px)", axp, order, off/pl.DX)
	}
	if off := math.Abs(axm + order); off > 2*pl.DX {
		t.Errorf("-1 order at %g m, want %g m (off %.1f px)", axm, -order, off/pl.DX)
	}
	if rel := math.Abs(i1p/i0 - m*m/4); rel > 0.08 {
		t.Errorf("I(+1)/I(0) = %.4f, want (m/2)² = %.4f (off %.1f%%)", i1p/i0, m*m/4, 100*rel)
	}
	if rel := math.Abs(i1m/i0 - m*m/4); rel > 0.08 {
		t.Errorf("I(-1)/I(0) = %.4f, want (m/2)² = %.4f (off %.1f%%)", i1m/i0, m*m/4, 100*rel)
	}
	// Between the orders the far field is dark.
	if v, _ := peakAt(order / 2); v/i0 > 0.03 {
		t.Errorf("the gap between orders carries %.2f%% of the 0 order", 100*v/i0)
	}
	// Mean transmission: (1 + m²/2)/(1+m)² of the 1 mW plane wave.
	want := 1e-3 * (1 + m*m/2) / ((1 + m) * (1 + m))
	if got := pl.Stats.Power; math.Abs(got-want)/want > 0.03 {
		t.Errorf("detector power %.4g W, want %.4g W (mean transmission %.4f)", got, want, (1+m*m/2)/((1+m)*(1+m)))
	}
}

// A scene file referencing a scripted element passes validation and runs: the
// JSON path (what the GUI and the HTTP API use) is the same object model.
func TestScriptedElementInJSONScene(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "half_stop", `{"name": "half_stop", "label": "半透光阑", "amp": "if(x > 0, 1, 0)"}`)
	rep := ReloadElementDefinitions([]string{dir})
	if len(rep.Errors) != 0 {
		t.Fatalf("load: %+v", rep.Errors)
	}
	t.Cleanup(func() { ReloadElementDefinitions([]string{t.TempDir()}) })

	cfgJSON := `{
	  "grid": {"size": 128, "width": 0.004},
	  "wavelength": 6.328e-7,
	  "method": "asm",
	  "sources": [{"id": "src", "type": "plane", "pos": {"x": 0, "y": 0, "z": -0.02},
	               "dir": {"x": 0, "y": 0, "z": 1}, "params": {"power": 0.001}}],
	  "scene": {"components": [
	    {"id": "h", "type": "half_stop", "pos": {"x": 0, "y": 0, "z": 0}, "shape": {"kind": "circle", "params": {"radius": 0.0015}}},
	    {"id": "d", "type": "sensor", "pos": {"x": 0, "y": 0, "z": 0.1}, "yaw": 3.14159265358979,
	     "shape": {"kind": "circle", "params": {"radius": 0.002}}}
	  ]}
	}`
	var cfg Config
	if err := json.Unmarshal([]byte(cfgJSON), &cfg); err != nil {
		t.Fatal(err)
	}
	if issues := ValidateConfig(&cfg); len(issues) != 0 {
		t.Fatalf("validation issues: %+v", issues)
	}
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	// Only the half of the aperture that transmits carries light.
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	left, right := 0.0, 0.0
	for i, x := range prof.X {
		if x < 0 {
			left += prof.V[i]
		} else {
			right += prof.V[i]
		}
	}
	if right <= 0 {
		t.Fatal("the transmitting half carries no light")
	}
	// The blocked half is not perfectly dark after 0.1 m of propagation: the
	// hard edge of the transmitted half diffracts into it. It must stay a
	// small fraction (measured ~2%), and the power must be the transmitting
	// half's share of the aperture.
	if left/right > 0.10 {
		t.Errorf("the blocked half carries %.2f%% of the open half, want a small diffraction tail", 100*left/right)
	}
	radius := 1.5e-3
	wantPower := 1e-3 * math.Pi * radius * radius / (2 * 4e-3 * 4e-3)
	if got := pl.Stats.Power; math.Abs(got-wantPower)/wantPower > 0.05 {
		t.Errorf("detector power %.4g W, want %.4g W (half the aperture's share)", got, wantPower)
	}
}
