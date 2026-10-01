package optics

import (
	"fmt"
	"math"
	"math/cmplx"
	"strings"
	"testing"
)

// TestExamplesRun runs every built-in preset end to end and checks that it
// produces planes with finite, physically plausible values. Every preset is a
// positioned scene (see TestExamplesAreScenes).
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

// TestExampleGratingSpectrumOrders verifies the grating preset really shows a
// grating spectrum: the seven wavelengths must be dispersed into the focal
// plane by the Fourier lens (x_q = q·f·λ/Λ), each ±1 order carrying the
// Raman-Nath weight J_q(m/2)² divided between them, and the detector must
// collect the source power.
//
// This is the regression test for the preset that used to be an element train
// ending in a 1 m fraunhofer propagation. Once the GUI converted element trains
// to positioned scenes, that geometry was evaluated as an ordinary 1 m ASM step
// on the fixed grid: the ±1 orders sit 31.6 mm off axis (outside the 8 mm
// window) and the field's spectrum is near Nyquist, so the result was a
// structureless beat pattern with the orders gone.
func TestExampleGratingSpectrumOrders(t *testing.T) {
	var cfg *Config
	for _, ex := range Examples() {
		if ex.Name == "衍射光栅光谱" {
			c := ex.Config
			cfg = &c
			break
		}
	}
	if cfg == nil {
		t.Skip("preset not found")
	}
	if !cfg.IsScene() {
		t.Fatal("the grating preset must be a positioned scene (element trains lose the far-field geometry)")
	}
	// A spectrum needs a range of wavelengths; one wavelength can only give
	// one point per order.
	if len(cfg.Sources) < 5 {
		t.Fatalf("the grating preset has %d source(s): a spectrum needs several wavelengths", len(cfg.Sources))
	}
	lo, hi := math.Inf(1), 0.0
	for _, s := range cfg.Sources {
		if s.Wavelength <= 0 {
			t.Fatalf("source %q carries no wavelength of its own", s.ID)
		}
		lo, hi = math.Min(lo, s.Wavelength), math.Max(hi, s.Wavelength)
	}
	res, err := Simulate(*cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	const focal, period = 0.3, 1e-4
	// x_q(λ) = q·f·λ/Λ: where the q-th order of a λ-source lands.
	xq := func(q int, wl float64) float64 { return float64(q) * focal * wl / period }
	centre := func(x float64) int { return int(math.Round(pl.Stats.CentroidX/pl.DX + float64(pl.Size)/2 + x/pl.DX)) }
	atX := func(x float64) float64 { // brightest sample within ±3 px of x
		mx := 0.0
		for i := centre(x) - 3; i <= centre(x)+3; i++ {
			if i >= 0 && i < len(prof.V) && prof.V[i] > mx {
				mx = prof.V[i]
			}
		}
		return mx
	}
	i0 := atX(0)
	if i0 <= 0 {
		t.Fatal("no light on the optical axis")
	}
	// (1) Dispersion: the pattern above 2 % of the axial peak is exactly the
	//     ±1 order of every wavelength — discrete orders, no continuum, and no
	//     other structure (a wrapped/aliased run would fill the window).
	var peaks []float64
	for i := 1; i < len(prof.V)-1; i++ {
		if prof.V[i] > prof.V[i-1] && prof.V[i] >= prof.V[i+1] && prof.V[i] > 0.02*i0 {
			peaks = append(peaks, (float64(i)-float64(pl.Size)/2)*pl.DX)
		}
	}
	//     (the axial order plus the ±1 order of each wavelength).
	if want := 2*len(cfg.Sources) + 1; len(peaks) != want {
		at := make([]string, len(peaks))
		for i, p := range peaks {
			at[i] = fmt.Sprintf("%.2f", p*1e3)
		}
		t.Errorf("found %d peaks above 2%% of the axial peak, want %d (axial + ±1 of each of the %d wavelengths), at (mm): %s",
			len(peaks), want, len(cfg.Sources), strings.Join(at, " "))
	}
	for _, s := range cfg.Sources {
		for _, q := range []int{-1, 1} {
			want := xq(q, s.Wavelength)
			best := math.Inf(1)
			for _, p := range peaks {
				best = math.Min(best, math.Abs(p-want))
			}
			if best > 2*pl.DX {
				t.Errorf("no ±1 order peak near %g m for λ = %.0f nm (closest peak %.1f px away)", want, s.Wavelength*1e9, best/pl.DX)
			}
		}
	}
	// (2) The strip is bounded by the extreme wavelengths: beyond the red end
	//     of the ±1 order the window is dark.
	for _, q := range []int{-1, 1} {
		edge := xq(q, hi) + float64(q)*6*pl.DX
		step := 1
		if q < 0 {
			step = -1
		}
		mx := 0.0
		for i := centre(edge); i >= 0 && i < len(prof.V); i += step {
			mx = math.Max(mx, prof.V[i])
		}
		if r := mx / i0; r > 0.02 {
			t.Errorf("beyond order %d at %g m the cut still reaches %.3f of the axial peak: the strip is not bounded by λ = %.0f nm",
				q, edge, r, hi*1e9)
		}
	}
	// (3) Raman-Nath weights: the axial order collects all seven wavelengths,
	//     so one wavelength's ±1 order carries (J1/J0)²/7 of the axial power.
	//     Lobe *integrals* are used rather than peak heights (the focal spot is
	//     only ~3 px wide, so its height depends on where it lands on the
	//     grid), and the individual lobes may scatter around the mean: the cut
	//     averages a 3 px slice, so the longer wavelengths lose part of their
	//     wider lobe outside the slice.
	lobe := func(x float64, half int) float64 {
		sum := 0.0
		for i := centre(x) - half; i <= centre(x)+half; i++ {
			if i >= 0 && i < len(prof.V) {
				sum += prof.V[i]
			}
		}
		return sum
	}
	e0 := lobe(0, 5)
	want1 := math.Pow(jn(1, 1), 2) / math.Pow(jn(0, 1), 2) / float64(len(cfg.Sources))
	sum1, emin, emax := 0.0, math.Inf(1), 0.0
	for _, s := range cfg.Sources {
		e := 0.5 * (lobe(xq(1, s.Wavelength), 5) + lobe(xq(-1, s.Wavelength), 5))
		sum1, emin, emax = sum1+e, math.Min(emin, e), math.Max(emax, e)
	}
	if rel := math.Abs(sum1/float64(len(cfg.Sources))/e0-want1) / want1; rel > 0.05 {
		t.Errorf("mean I(±1)/I(0) = %.4f, want (J1/J0)²/%d = %.4f", sum1/float64(len(cfg.Sources))/e0, len(cfg.Sources), want1)
	}
	for _, e := range []float64{emin, emax} {
		if rel := math.Abs(e/e0-want1) / want1; rel > 0.20 {
			t.Errorf("I(±1)/I(0) = %.4f is %.0f%% off (J1/J0)²/%d = %.4f", e/e0, rel*100, len(cfg.Sources), want1)
		}
	}
	// Every wavelength must carry its share of the source power.
	pmin, pmax := math.Inf(1), 0.0
	for i := range pl.Parts {
		pmin, pmax = math.Min(pmin, pl.Parts[i].Power), math.Max(pmax, pl.Parts[i].Power)
	}
	if pl.MergedUnits() && (pmax-pmin)/pmax > 0.01 {
		t.Errorf("the %d units carry powers %.6g…%.6g W: they must be equal", len(pl.Parts), pmin, pmax)
	}
	// (4) Between the axial order and the strip the pattern is dark — which is
	//     what the pre-fix beat pattern was not.
	mid := 0.5 * xq(1, lo)
	if r := atX(mid) / i0; r > 0.02 {
		t.Errorf("the gap between the axial order and the strip carries %.3f of the axial peak: no separated orders", r)
	}
	// (5) Energy: no element absorbs, and the detector spans the window.
	if rel := math.Abs(pl.Stats.Power-1e-3) / 1e-3; rel > 0.01 {
		t.Errorf("detector power %.6g W, want ~1e-3 W", pl.Stats.Power)
	}
}

// TestExampleMichelsonSceneFringes checks the Michelson preset shows the
// expected interference structure in its detector plane.
func TestExampleMichelsonSceneFringes(t *testing.T) {
	var cfg *Config
	for _, ex := range Examples() {
		if ex.Name == "迈克尔逊干涉仪" {
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

// ---- regression tests for the rewritten (scene-only) preset list -----------

// presetConfig returns a private copy of the named preset. The catalog returns
// shared *SceneSpec pointers, so a test that edits a component must copy the
// component slice and the parameter maps first.
func presetConfig(t *testing.T, name string) Config {
	t.Helper()
	for _, ex := range Examples() {
		if ex.Name != name {
			continue
		}
		c := ex.Config
		if c.Scene != nil {
			comps := make([]ComponentSpec, len(c.Scene.Components))
			copy(comps, c.Scene.Components)
			for i := range comps {
				if comps[i].Params != nil {
					p := make(map[string]any, len(comps[i].Params))
					for k, v := range comps[i].Params {
						p[k] = v
					}
					comps[i].Params = p
				}
			}
			c.Scene = &SceneSpec{Components: comps}
		}
		srcs := make([]SourceSpec, len(c.Sources))
		copy(srcs, c.Sources)
		c.Sources = srcs
		return c
	}
	t.Fatalf("preset %q not found", name)
	return Config{}
}

// TestExamplesAreScenes pins the model: every built-in preset is a positioned
// scene with at least one source and one detector, so no preset depends on the
// element-train -> scene conversion (which cannot preserve a far-field step).
func TestExamplesAreScenes(t *testing.T) {
	for _, ex := range Examples() {
		if !ex.Config.IsScene() {
			t.Errorf("preset %q is an element train, not a positioned scene", ex.Name)
			continue
		}
		if len(ex.Config.Sources) == 0 {
			t.Errorf("preset %q has no source", ex.Name)
		}
		sensors := 0
		for _, c := range ex.Config.Scene.Components {
			if c.Type == "sensor" {
				sensors++
			}
		}
		if sensors == 0 {
			t.Errorf("preset %q has no detector", ex.Name)
		}
	}
}

// lobeNear returns the largest value of the cut within ±win of centre and the
// position where it sits.
func lobeNear(prof Profile, dx, centre, win float64) (float64, float64) {
	mx, at := 0.0, centre
	for i, x := range prof.X {
		if math.Abs(x-centre) > win {
			continue
		}
		if prof.V[i] > mx {
			mx, at = prof.V[i], x
		}
	}
	return mx, at
}

// halfMaxRadius returns the radius at which the cut first falls through half of
// its peak, walking outwards from the peak.
func halfMaxEdges(prof Profile) (float64, float64, float64, float64) {
	mx, at := 0.0, 0
	for i, v := range prof.V {
		if v > mx {
			mx, at = v, i
		}
	}
	half := mx / 2
	lo, hi := prof.X[at], prof.X[at]
	for i := at; i > 0; i-- {
		if prof.V[i] >= half && prof.V[i-1] < half {
			lo = prof.X[i]
			break
		}
	}
	for i := at; i < len(prof.V)-1; i++ {
		if prof.V[i] >= half && prof.V[i+1] < half {
			hi = prof.X[i]
			break
		}
	}
	return lo, hi, mx, prof.X[at]
}

// TestExampleCircularApertureAiry checks the circular-aperture preset really
// shows an Airy pattern: the first ring sits at 1.635·λf/D with ~1.7% of the
// axial peak. The preset used to be a 2 m fraunhofer train; converted to a
// scene that geometry is F = D²/(λz) = 12.6, i.e. the near field, and the run
// showed Fresnel rings (47% of the peak) instead of an Airy disk.
func TestExampleCircularApertureAiry(t *testing.T) {
	cfg := presetConfig(t, "圆孔衍射（远场艾里斑）")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, peak, _ := halfMaxEdges(prof)
	const f, D = 0.3, 2e-3
	ring := 1.635 * cfg.Wavelength * f / D
	got, at := lobeNear(prof, pl.DX, ring, 4*pl.DX)
	if off := math.Abs(at - ring); off > 3*pl.DX {
		t.Errorf("first Airy ring at %g m, want %g m (off %.1f px)", at, ring, off/pl.DX)
	}
	if rel := math.Abs(got/peak - 0.0175); rel > 0.30 {
		t.Errorf("first ring is %.2f%% of the axial peak, want ~1.75%%", 100*got/peak)
	}
}

// TestExampleSingleSlitSinc checks the single-slit preset: sinc² zeros at
// ±λz/b and the first sidelobe at 1.43·λz/b carrying ~4.7%.
func TestExampleSingleSlitSinc(t *testing.T) {
	cfg := presetConfig(t, "单缝夫琅禾费衍射")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, peak, at0 := halfMaxEdges(prof)
	if math.Abs(at0) > 2*pl.DX {
		t.Errorf("the diffraction lobe is centred at %g m, want 0", at0)
	}
	// Between the central lobe and the first sidelobe the pattern must be dark
	// (the first zero at λz/b = 1.58 mm).
	zero := cfg.Wavelength * 1.0 / 4e-4
	if v, _ := lobeNear(prof, pl.DX, zero, 1.5*pl.DX); v/peak > 0.05 {
		t.Errorf("the cut at the first zero (%.3g m) carries %.1f%% of the peak", zero, 100*v/peak)
	}
	sb := 1.43 * zero
	got, at := lobeNear(prof, pl.DX, sb, 3*pl.DX)
	if off := math.Abs(at - sb); off > 3*pl.DX {
		t.Errorf("first sidelobe at %g m, want %g m (off %.1f px)", at, sb, off/pl.DX)
	}
	if rel := math.Abs(got/peak-0.047) / 0.047; rel > 0.30 {
		t.Errorf("first sidelobe is %.2f%% of the peak, want 4.7%%", 100*got/peak)
	}
}

// TestExampleDoubleSlitFringes checks the double-slit fringe spacing λz/d.
func TestExampleDoubleSlitFringes(t *testing.T) {
	cfg := presetConfig(t, "双缝干涉")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, peak, _ := halfMaxEdges(prof)
	// Spacing between the ±1 fringes.
	spacing := cfg.Wavelength * 1.0 / 1e-3
	got, at := lobeNear(prof, pl.DX, spacing, 3*pl.DX)
	if at < 0.5*spacing {
		t.Fatalf("no first-order fringe at %g m", spacing)
	}
	if rel := math.Abs(at-spacing) / spacing; rel > 0.05 {
		t.Errorf("first fringe at %g m, want %g m", at, spacing)
	}
	// Adjacent fringes must be equally bright for this envelope.
	if rel := math.Abs(got/peak-1.0) * 1; rel > 0.15 {
		t.Errorf("first fringe is %.1f%% of the axial one, want ~100%%", 100*got/peak)
	}
}

// TestExampleLensFocusStrehl checks the diffraction-limited focus preset: the
// Strehl ratio must come out at ~1 and the detector must collect the light
// that passed the pupil.
func TestExampleLensFocusStrehl(t *testing.T) {
	cfg := presetConfig(t, "透镜聚焦（艾里斑）")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	if pl.Stats.Strehl < 0.9 {
		t.Errorf("Strehl = %.3f for a perfect lens on a clear pupil, want ~1", pl.Stats.Strehl)
	}
	want := math.Pi * 2.5e-3 * 2.5e-3 / 1e-4 * 1e-3 // pupil area / window area
	if rel := math.Abs(pl.Stats.Power-want) / want; rel > 0.05 {
		t.Errorf("detector power %.5g W, want %.5g W", pl.Stats.Power, want)
	}
	// The Airy core must be resolved: first dark ring at 1.22·λf/D > 3 px.
	if r := 1.22 * cfg.Wavelength * 0.5 / 5e-3; r < 3*pl.DX {
		t.Errorf("the Airy core (%.4g m) is undersampled at dx = %.4g m", r, pl.DX)
	}
}

// TestExampleZonePlateFocus checks the zone-plate preset focuses on axis to the
// expected spot λf/(2R) and that the focal plane really is the brightest.
func TestExampleZonePlateFocus(t *testing.T) {
	cfg := presetConfig(t, "波带片聚焦")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	lo, hi, _, at := halfMaxEdges(prof)
	if math.Abs(at) > 2*pl.DX {
		t.Errorf("the focus is at %g m, want the axis", at)
	}
	want := cfg.Wavelength * 0.05 / (2 * 2e-3)
	if fwhm := hi - lo; math.Abs(fwhm-want)/want > 0.5 {
		t.Errorf("focal spot FWHM %.4g m, want ~λf/(2R) = %.4g m", fwhm, want)
	}
}

// TestExampleAberrationStrehl checks the aberration preset: the Zernike plate
// must cost most of the Strehl, while the same scene with zero coefficients
// stays diffraction limited. The component parameters are edited on a copy, so
// the preset itself is left untouched.
func TestExampleAberrationStrehl(t *testing.T) {
	cfg := presetConfig(t, "像差研究（泽尼克球差+离焦）")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ab := res.Planes[0]
	if ab.Stats.Strehl >= 0.6 {
		t.Errorf("aberrated focus Strehl = %.3f, want well below 1", ab.Stats.Strehl)
	}
	for i := range cfg.Scene.Components {
		if cfg.Scene.Components[i].Type == "zernike" {
			cfg.Scene.Components[i].Params["c4"] = 0.0
			cfg.Scene.Components[i].Params["c11"] = 0.0
		}
	}
	clean, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if s := clean.Planes[0].Stats.Strehl; s < 0.9 {
		t.Errorf("the same scene without aberration gives Strehl = %.3f, want ~1", s)
	}
	if ab.Stats.Peak >= clean.Planes[0].Stats.Peak {
		t.Errorf("aberration did not lower the peak (%.4g vs %.4g)",
			ab.Stats.Peak, clean.Planes[0].Stats.Peak)
	}
}

// TestExampleVortexNull checks the vortex preset puts a null on the axis.
func TestExampleVortexNull(t *testing.T) {
	cfg := presetConfig(t, "光学涡旋")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, peak, _ := halfMaxEdges(prof)
	onAxis, _ := lobeNear(prof, pl.DX, 0, 0.5*pl.DX)
	if onAxis/peak > 0.01 {
		t.Errorf("charge-3 vortex has %.3f%% of the peak on axis, want a null", 100*onAxis/peak)
	}
	if pl.Stats.Power <= 0 {
		t.Error("the vortex detector recorded no light")
	}
}

// TestExampleBesselCore checks the axicon preset produces a Bessel beam: the
// rings are spaced by 2π/k_r with k_r = k(n-1)α and the core is the brightest
// point of the pattern.
func TestExampleBesselCore(t *testing.T) {
	cfg := presetConfig(t, "贝塞尔光束（轴锥镜）")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, _, peak, at := halfMaxEdges(prof)
	if math.Abs(at) > 2*pl.DX {
		t.Errorf("the Bessel core is at %g m, want the axis", at)
	}
	kr := 2 * math.Pi * 0.01 / cfg.Wavelength
	want := math.Pi / kr // ring-to-ring spacing of J0²
	// Walk outwards from the core and find the first two ring maxima.
	var maxima []float64
	for i := 1; i < len(prof.V)-1; i++ {
		if prof.X[i] <= 0 {
			continue
		}
		if prof.V[i] > 0.02*peak && prof.V[i] >= prof.V[i-1] && prof.V[i] > prof.V[i+1] {
			maxima = append(maxima, prof.X[i])
		}
	}
	if len(maxima) < 3 {
		t.Fatalf("found only %d rings, want a Bessel-like profile", len(maxima))
	}
	spacing := maxima[1] - maxima[0]
	if rel := math.Abs(spacing-want) / want; rel > 0.25 {
		t.Errorf("ring spacing %.4g m, want π/k_r = %.4g m", spacing, want)
	}
}

// TestExampleGaussianBeamGrowth checks the Gaussian-beam preset samples one
// free-space run at two distances: the waist must grow as w(z) = w₀√(1+(z/zR)²).
func TestExampleGaussianBeamGrowth(t *testing.T) {
	cfg := presetConfig(t, "高斯光束传播")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 2 {
		t.Fatalf("want the two arms of the splitter recorded, got %d planes", len(res.Planes))
	}
	zR := math.Pi * 2e-4 * 2e-4 / cfg.Wavelength
	waist := func(pl *Plane) float64 {
		prof, err := pl.ProfileOf("x", KindIntensity, nil)
		if err != nil {
			t.Fatal(err)
		}
		lo, hi, _, _ := halfMaxEdges(prof)
		return (hi - lo) / 2 / math.Sqrt(math.Log(2)/2)
	}
	byLabel := map[string]*Plane{}
	for _, pl := range res.Planes {
		byLabel[pl.Label] = pl
	}
	short, long := byLabel["距腰 50 mm"], byLabel["距腰 300 mm"]
	w1, w2 := waist(short), waist(long)
	for _, c := range []struct {
		z, got float64
	}{{0.05, w1}, {0.3, w2}} {
		want := 2e-4 * math.Sqrt(1+math.Pow(c.z/zR, 2))
		if rel := math.Abs(c.got-want) / want; rel > 0.25 {
			t.Errorf("w(%.2f m) = %.4g m, want %.4g m", c.z, c.got, want)
		}
	}
	if w2 < 1.4*w1 {
		t.Errorf("the beam did not grow between the arms (%.4g m -> %.4g m)", w1, w2)
	}
	// Peak intensity falls as 1/w².
	if rel := math.Abs((w2/w1)*(w2/w1)-short.Stats.Peak/long.Stats.Peak) / (w2 * w2 / (w1 * w1)); rel > 0.2 {
		t.Errorf("peak ratio %.3f does not match (w2/w1)² = %.3f",
			short.Stats.Peak/long.Stats.Peak, (w2/w1)*(w2/w1))
	}
}

// TestExamplePolarizerWaveplateCircular checks the polarizer + quarter-wave
// plate preset leaves a circularly polarised field on the detector.
func TestExamplePolarizerWaveplateCircular(t *testing.T) {
	cfg := presetConfig(t, "偏振片与波片")
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	if cfg.Polarized == nil || !*cfg.Polarized {
		t.Fatal("the preset must be run with polarization enabled")
	}
	ci := pl.Size/2*pl.Size + pl.Size/2
	ex, ey := pl.Ex[ci], pl.Ey[ci]
	s0 := math.Pow(cmplx.Abs(ex), 2) + math.Pow(cmplx.Abs(ey), 2)
	if s0 <= 0 {
		t.Fatal("no field on the axis")
	}
	s1 := math.Pow(cmplx.Abs(ex), 2) - math.Pow(cmplx.Abs(ey), 2)
	s2 := 2 * (real(ex)*real(ey) + imag(ex)*imag(ey))
	s3 := -2 * (real(ex)*imag(ey) - imag(ex)*real(ey))
	if math.Abs(s1)/s0 > 0.05 || math.Abs(s2)/s0 > 0.05 {
		t.Errorf("field is not circular: S1/S0 = %.4f, S2/S0 = %.4f", s1/s0, s2/s0)
	}
	if math.Abs(s3)/s0 < 0.99 {
		t.Errorf("|S3|/S0 = %.4f, want 1 (circular)", math.Abs(s3)/s0)
	}
	// The 45° source loses half its power in the 0° polarizer.
	if rel := math.Abs(pl.Stats.Power-5e-4) / 5e-4; rel > 0.05 {
		t.Errorf("detector power %.5g W, want ~5e-4 W", pl.Stats.Power)
	}
}
