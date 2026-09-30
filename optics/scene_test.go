package optics

import (
	"math"
	"testing"
)

// sceneCfg builds a positioned-scene configuration with a sensible grid.
func sceneCfg(size int, width float64, sources []SourceSpec, comps ...ComponentSpec) Config {
	pf := false
	return Config{
		Grid:       GridSpec{Size: size, Width: width},
		Wavelength: 632.8e-9,
		Polarized:  &pf,
		Method:     "asm",
		Evanescent: "decay",
		Bandlimit:  &BandlimitOpts{Fraction: 0.9, Sigma: 0.05},
		Sources:    sources,
		Scene:      &SceneSpec{Components: comps},
	}
}

func circleShape(r float64) *ShapeSpec {
	return &ShapeSpec{Kind: "circle", Params: map[string]any{"radius": r}}
}

func planeSrc(pos Vec3, dir Vec3) SourceSpec {
	return SourceSpec{Type: "plane", Pos: &pos, Dir: &dir,
		Params: map[string]any{"power": 1e-3}}
}

func gaussSrc(pos Vec3, dir Vec3, waist float64) SourceSpec {
	return SourceSpec{Type: "gaussian", Pos: &pos, Dir: &dir,
		Params: map[string]any{"power": 1e-3, "waist": waist}}
}

// TestSceneTraceStraightLine checks that the geometry of the simplest layout is
// routed correctly: the propagation distances come from the positions.
func TestSceneTraceStraightLine(t *testing.T) {
	src := gaussSrc(v3(0, 0, -0.2), v3(0, 0, 1), 1e-3)
	lens := ComponentSpec{Type: "lens", Pos: v3(0, 0, 0), Shape: circleShape(6e-3),
		Params: map[string]any{"f": 0.5}}
	sensor := ComponentSpec{Type: "sensor", Label: "焦面", Pos: v3(0, 0, 0.5),
		Yaw: math.Pi, Shape: circleShape(5e-3)}
	scene := &SceneSpec{Components: []ComponentSpec{lens, sensor}}
	tr, err := TraceScene(scene, []SourceSpec{src}, 632.8e-9)
	if err != nil {
		t.Fatal(err)
	}
	if len(tr.Segments) != 2 {
		t.Fatalf("want 2 segments (source→lens, lens→sensor), got %d", len(tr.Segments))
	}
	if got := tr.Segments[0].To.Sub(tr.Segments[0].From).Norm(); math.Abs(got-0.2) > 1e-9 {
		t.Errorf("first segment length = %g m, want 0.2 m", got)
	}
	if got := tr.Segments[1].To.Sub(tr.Segments[1].From).Norm(); math.Abs(got-0.5) > 1e-9 {
		t.Errorf("second segment length = %g m, want 0.5 m", got)
	}
	if tr.Components[0].Hits != 1 || tr.Components[1].Hits != 1 {
		t.Errorf("component hits = %d/%d, want 1/1", tr.Components[0].Hits, tr.Components[1].Hits)
	}
	if !tr.Planar {
		t.Error("layout should be reported as planar")
	}
}

// TestSceneFoldByMirror checks a 90° fold: a mirror at 45° must send the beam
// along +x and the beam must then reach a detector on that axis, with the
// correct distance derived from the positions.
func TestSceneFoldByMirror(t *testing.T) {
	src := gaussSrc(v3(0, 0, -0.1), v3(0, 0, 1), 1e-3)
	mirror := ComponentSpec{Type: "mirror", Pos: v3(0, 0, 0), Yaw: -math.Pi / 4,
		Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 1.0}}
	sensor := ComponentSpec{Type: "sensor", Label: "侧向", Pos: v3(0.3, 0, 0),
		Yaw: -math.Pi / 2, Shape: circleShape(5e-3)}
	res, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, mirror, sensor))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("want 1 recorded plane, got %d", len(res.Planes))
	}
	if res.Planes[0].Label != "侧向" {
		t.Errorf("plane label = %q", res.Planes[0].Label)
	}
	tr := res.Scene
	if tr == nil {
		t.Fatal("scene trace missing from the result")
	}
	if !res.Scene.Planar {
		t.Error("folded layout should stay in the x-z plane")
	}
	found := false
	for _, s := range res.Scene.Segments {
		if math.Abs(s.To.Y) > 1e-9 {
			t.Errorf("segment leaves the table plane: %v → %v", s.From, s.To)
		}
		if math.Abs(s.From.Y) < 1e-12 && math.Abs(s.To.Z-s.From.Z) < 1e-12 && s.To.X > s.From.X+0.29 {
			found = true
		}
	}
	if !found {
		t.Error("no segment travelling along +x to the side detector")
	}
	// Power must be conserved through a lossless fold.
	if got := res.Planes[0].Stats.Power; math.Abs(got-1e-3)/1e-3 > 0.02 {
		t.Errorf("power through a lossless fold = %g W, want ~1e-3 W", got)
	}
}

// TestSceneMachZehnderInterference builds a mach-zender from positioned parts
// and checks the classic result: with equal arms both ports get half the power,
// and a quarter-wave displacement of one end mirror (a π/2 path phase) sends
// everything into one port. This exercises routing, coherent recombination at
// the second splitter and the phase accumulated from the geometric path.
func TestSceneMachZehnderInterference(t *testing.T) {
	const wl = 632.8e-9
	build := func(mirrorShift float64) Config {
		src := planeSrc(v3(0, 0, -0.05), v3(0, 0, 1))
		bs1 := ComponentSpec{Type: "beamsplitter", Pos: v3(0, 0, 0), Yaw: -math.Pi / 4,
			Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 0.5}}
		ma := ComponentSpec{Type: "mirror", Pos: v3(0, 0, 0.1+mirrorShift), Yaw: -math.Pi / 4,
			Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 1.0}}
		mb := ComponentSpec{Type: "mirror", Pos: v3(0.1, 0, 0), Yaw: 3 * math.Pi / 4,
			Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 1.0}}
		bs2 := ComponentSpec{Type: "beamsplitter", Pos: v3(0.1, 0, 0.1), Yaw: -math.Pi / 4,
			Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 0.5}}
		// The detector only measures inside its own outline; this radius covers
		// the whole 8 mm square grid (its corners lie 5.66 mm from the centre).
		s1 := ComponentSpec{Type: "sensor", Label: "端口1", Pos: v3(0.1, 0, 0.2), Yaw: math.Pi,
			Shape: circleShape(6e-3)}
		s2 := ComponentSpec{Type: "sensor", Label: "端口2", Pos: v3(0.2, 0, 0.1), Yaw: -math.Pi / 2,
			Shape: circleShape(6e-3)}
		return sceneCfg(256, 8e-3, []SourceSpec{src}, bs1, ma, mb, bs2, s1, s2)
	}
	port := func(res *Result, label string) float64 {
		for _, p := range res.Planes {
			if p.Label == label {
				return p.Stats.Power
			}
		}
		t.Fatalf("port %q missing (planes: %v)", label, planeLabels(res))
		return 0
	}
	res, err := Simulate(build(0))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 2 {
		t.Fatalf("want both ports recorded, got %v", planeLabels(res))
	}
	p1, p2 := port(res, "端口1"), port(res, "端口2")
	if math.Abs(p1-1e-3)/1e-3 > 0.02 || p2 > 1e-9 {
		t.Errorf("equal arms: complementary ports should be bright/dark, got %.6g / %.6g W", p1, p2)
	}
	if math.Abs(p1+p2-1e-3)/1e-3 > 1e-6 {
		t.Errorf("port powers must sum to the input power: got %.9g", p1+p2)
	}

	// The 45° end mirror is displaced along z, so the arm's optical path
	// changes by the displacement itself (the extra inbound/outbound path is
	// cancelled by the shortened final leg). λ/2 therefore gives a π phase
	// difference and the ports swap.
	res2, err := Simulate(build(wl / 2))
	if err != nil {
		t.Fatal(err)
	}
	q1, q2 := port(res2, "端口1"), port(res2, "端口2")
	if q1 > 1e-9 {
		t.Errorf("π path shift: port1 = %.6g W, want dark", q1)
	}
	if math.Abs(q2-1e-3)/1e-3 > 0.02 {
		t.Errorf("π path shift: port2 = %.6g W, want ~1e-3 W", q2)
	}

	// A λ/4 displacement (π/2 path phase) splits the light equally.
	res3, err := Simulate(build(wl / 4))
	if err != nil {
		t.Fatal(err)
	}
	if p := port(res3, "端口1"); math.Abs(p-5e-4)/5e-4 > 0.02 {
		t.Errorf("π/2 path shift: port1 = %.6g W, want ~5e-4 W", p)
	}
}

func planeLabels(res *Result) []string {
	var out []string
	for _, p := range res.Planes {
		out = append(out, p.Label)
	}
	return out
}

// TestSceneMultipleSourcesIncoherent verifies that independent sources (no
// shared coherent group) add in intensity, that a shared group interferes, and
// that per-source colour parts are recorded.
func TestSceneMultipleSourcesIncoherent(t *testing.T) {
	const wl = 632.8e-9
	s := func(tilt float64, group string) SourceSpec {
		sp := planeSrc(v3(0, 0, -0.05), v3(0, 0, 1))
		sp.Params["tilt_x"] = tilt
		sp.Group = group
		return sp
	}
	// Two plane waves of the same width crossing a detector plane: coherent
	// they produce fringes, incoherent they simply add.
	det := ComponentSpec{Type: "sensor", Label: "det", Pos: v3(0, 0, 0.2), Yaw: math.Pi,
		Shape: circleShape(3e-3)}
	one := sceneCfg(256, 8e-3, []SourceSpec{s(0, "a")}, det)
	res1, err := Simulate(one)
	if err != nil {
		t.Fatal(err)
	}
	two := sceneCfg(256, 8e-3, []SourceSpec{s(0, "a"), s(0, "b")}, det)
	res2, err := Simulate(two)
	if err != nil {
		t.Fatal(err)
	}
	p1 := res1.Planes[0].Stats.Power
	p2 := res2.Planes[0].Stats.Power
	if math.Abs(p2-2*p1)/p1 > 1e-6 {
		t.Errorf("two incoherent sources: power = %.9g W, want twice %.9g W", p2, p1)
	}
	if len(res2.Planes[0].Parts) != 2 {
		t.Fatalf("want 2 colour parts, got %d", len(res2.Planes[0].Parts))
	}
	// Sources in the same coherent group with a transverse offset produce a
	// fringe pattern: the total power is no longer simply the sum, and the
	// intensity must show modulation.
	coherent := sceneCfg(256, 8e-3, []SourceSpec{s(-1e-4, "g"), s(1e-4, "g")}, det)
	res3, err := Simulate(coherent)
	if err != nil {
		t.Fatal(err)
	}
	pl := res3.Planes[0]
	if len(pl.Parts) != 1 {
		t.Fatalf("one coherent unit expected, got %d parts", len(pl.Parts))
	}
	mx := pl.Stats.IntensityMax
	mn := math.Inf(1)
	for i := range pl.Ex {
		v := norm2c(pl.Ex[i]) + norm2c(pl.Ey[i])
		if v < mn {
			mn = v
		}
	}
	if !(mx > 4*mn) {
		t.Errorf("coherent pair should form high-contrast fringes: max/min = %g/%g", mx, mn)
	}
	if wl > 0 && pl.Wavelength != wl {
		t.Errorf("plane wavelength = %g, want %g", pl.Wavelength, wl)
	}
}

// TestSceneStopBlocksBeam checks that an aperture stop placed in the beam path
// stops the light: no detector beyond it sees any power.
func TestSceneStopBlocksBeam(t *testing.T) {
	src := gaussSrc(v3(0, 0, -0.05), v3(0, 0, 1), 5e-4)
	// A tiny off-axis pinhole: the beam is centred on the axis, so it is
	// blocked.
	stop := ComponentSpec{Type: "aperture", Pos: v3(5e-3, 0, 0.1),
		Shape: circleShape(2e-4)}
	det := ComponentSpec{Type: "sensor", Label: "det", Pos: v3(0, 0, 0.2), Yaw: math.Pi,
		Shape: circleShape(3e-3)}
	res, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, stop, det))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("want the detector plane, got %v", planeLabels(res))
	}
	if p := res.Planes[0].Stats.Power; p > 1e-9 {
		t.Errorf("beam should be blocked by the off-axis stop, got power %g W", p)
	}
	// Moving the stop onto the axis opens the path again.
	stop.Pos = v3(0, 0, 0.1)
	stop.Shape = circleShape(2e-3)
	res2, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, stop, det))
	if err != nil {
		t.Fatal(err)
	}
	if p := res2.Planes[0].Stats.Power; math.Abs(p-1e-3)/1e-3 > 0.05 {
		t.Errorf("open aperture should pass the beam, got power %g W", p)
	}
}

// TestSceneEqualsLegacyTrain cross-checks the positioned scene against the
// equivalent sequential element train: a lens focusing a Gaussian beam must
// give the same focal-plane intensity from both models.
func TestSceneEqualsLegacyTrain(t *testing.T) {
	pf := false
	legacy := Config{
		Grid: GridSpec{Size: 256, Width: 8e-3}, Wavelength: 632.8e-9, Polarized: &pf,
		Method: "asm", Evanescent: "decay",
		Bandlimit: &BandlimitOpts{Fraction: 0.9, Sigma: 0.05},
		Source:    SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "power": 1e-3}},
		Elements: []ElementSpec{
			{Type: "lens", Params: map[string]any{"f": 0.5, "aperture": 3e-3}},
			{Type: "propagate", Params: map[string]any{"distance": 0.5}},
			{Type: "sensor", Params: map[string]any{"label": "焦面"}},
		},
	}
	resL, err := Simulate(legacy)
	if err != nil {
		t.Fatal(err)
	}
	src := gaussSrc(v3(0, 0, -0.05), v3(0, 0, 1), 1e-3)
	lens := ComponentSpec{Type: "lens", Pos: v3(0, 0, 0), Shape: circleShape(3e-3),
		Params: map[string]any{"f": 0.5}}
	det := ComponentSpec{Type: "sensor", Label: "焦面", Pos: v3(0, 0, 0.5), Yaw: math.Pi,
		Shape: circleShape(4e-3)}
	resS, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, lens, det))
	if err != nil {
		t.Fatal(err)
	}
	a, b := resL.Planes[0], resS.Planes[0]
	if math.Abs(a.Stats.Peak-b.Stats.Peak)/a.Stats.Peak > 1e-4 {
		t.Errorf("peak intensity differs: train %.9g vs scene %.9g W/m²", a.Stats.Peak, b.Stats.Peak)
	}
	if math.Abs(a.Stats.Power-b.Stats.Power)/a.Stats.Power > 1e-6 {
		t.Errorf("power differs: train %.9g vs scene %.9g W", a.Stats.Power, b.Stats.Power)
	}
	if math.Abs(a.Stats.RMSX-b.Stats.RMSX) > 1e-9 {
		t.Errorf("spot size differs: train %.9g vs scene %.9g m", a.Stats.RMSX, b.Stats.RMSX)
	}
}

// TestSceneMichelsonDarkPort reproduces the classic balanced Michelson from
// positioned components: the two return beams recombine at the splitter, and
// with equal arms one output port must be dark.
func TestSceneMichelsonDarkPort(t *testing.T) {
	const wl = 632.8e-9
	src := gaussSrc(v3(0, 0, -0.05), v3(0, 0, 1), 2e-3)
	bs := ComponentSpec{Type: "beamsplitter", Pos: v3(0, 0, 0), Yaw: -math.Pi / 4,
		Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 0.5}}
	// Transmitted arm: +z, mirror folds it back.
	mz := ComponentSpec{Type: "mirror", Pos: v3(0, 0, 0.1), Yaw: 0,
		Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 1.0}}
	// Reflected arm: +x, mirror folds it back.
	mx := ComponentSpec{Type: "mirror", Pos: v3(0.1, 0, 0), Yaw: math.Pi / 2,
		Shape: circleShape(6e-3), Params: map[string]any{"reflectivity": 1.0}}
	// Ports: back along -z (through the splitter) and along -x. A detector
	// faces the light it sees, so its normal points back along the incoming
	// beam.
	back := ComponentSpec{Type: "sensor", Label: "回光源端口", Pos: v3(0, 0, -0.05), Yaw: 0,
		Shape: circleShape(6e-3)}
	side := ComponentSpec{Type: "sensor", Label: "侧向端口", Pos: v3(-0.05, 0, 0), Yaw: math.Pi / 2,
		Shape: circleShape(6e-3)}
	res, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, bs, mz, mx, back, side))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 2 {
		t.Fatalf("want both ports, got %v", planeLabels(res))
	}
	var backP, sideP float64
	for _, p := range res.Planes {
		switch p.Label {
		case "回光源端口":
			backP = p.Stats.Power
		case "侧向端口":
			sideP = p.Stats.Power
		}
	}
	if math.Abs(backP+sideP-1e-3)/1e-3 > 1e-6 {
		t.Errorf("ports must conserve energy: back %.9g + side %.9g W", backP, sideP)
	}
	// The two ports are complementary: one of them is dark for a balanced
	// Michelson (which one depends on the sign convention of the splitter).
	lit, dark := math.Max(backP, sideP), math.Min(backP, sideP)
	if lit < 0.9e-3 {
		t.Errorf("one port should carry nearly all the power, got %.9g / %.9g W", backP, sideP)
	}
	if dark > 0.1e-3 {
		t.Errorf("one port should be dark, got %.9g / %.9g W", backP, sideP)
	}
}

// TestLayoutFromElementsLightsDetector guards the legacy → scene conversion:
// an element train converted by LayoutFromElements must still deliver light to
// its sensor when the converted scene is simulated with the scene engine.
func TestLayoutFromElementsLightsDetector(t *testing.T) {
	cfg := Config{
		Grid:       GridSpec{Size: 128, Width: 8e-3},
		Wavelength: 632.8e-9,
		Method:     "asm",
		Source:     SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}},
		Elements: []ElementSpec{
			{Type: "aperture", Params: map[string]any{"shape": "double_slit", "width": 1e-4, "height": 2e-3, "separation": 1e-3}},
			{Type: "propagate", Params: map[string]any{"distance": 0.2}},
			{Type: "sensor", Params: map[string]any{"label": "屏"}},
		},
	}
	scene := LayoutFromElements(&cfg)
	if len(scene.Components) != 2 {
		t.Fatalf("converted scene has %d components, want 2", len(scene.Components))
	}
	if yaw := scene.Components[1].Yaw; yaw != math.Pi {
		t.Fatalf("converted sensor yaw = %v, want π (facing the incoming beam)", yaw)
	}
	conv := cfg
	conv.Elements = nil
	conv.Scene = scene
	res, err := Simulate(conv)
	if err != nil {
		t.Fatalf("Simulate(converted): %v", err)
	}
	if len(res.Planes) == 0 {
		t.Fatal("converted scene recorded no planes")
	}
	if len(res.Planes) == 1 && res.Planes[0].Stats.Power <= 0 {
		t.Fatalf("converted sensor %q got no light", res.Planes[0].Label)
	}
	if res.Scene == nil || len(res.Scene.Segments) == 0 {
		t.Error("converted scene produced no routed segments")
	}
}
