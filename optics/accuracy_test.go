package optics

import (
	"math"
	"testing"
)

func ptrBool(b bool) *bool { return &b }

func mustSim(t *testing.T, cfg Config) *Result {
	t.Helper()
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatalf("Simulate failed: %v", err)
	}
	return res
}

func scalarCfg(size int, width, wl float64) Config {
	return Config{
		Grid:       GridSpec{Size: size, Width: width},
		Wavelength: wl,
		Polarized:  ptrBool(false),
		Method:     "asm",
		Evanescent: "decay",
	}
}

func planeSource() SourceSpec {
	return SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}}
}

// --- 1. Airy disk: uniform illumination through a circular aperture + lens ---

func TestAiryFocus(t *testing.T) {
	cfg := scalarCfg(1024, 0.01, 632.8e-9)
	cfg.Bandlimit = &BandlimitOpts{Fraction: 0.9, Sigma: 0.05}
	cfg.Source = planeSource()
	f, z, R := 0.5, 0.5, 0.0025
	cfg.Elements = []ElementSpec{
		{Type: "lens", Params: map[string]any{"f": f, "aperture": R}},
		{Type: "propagate", Params: map[string]any{"distance": z}},
		{Type: "sensor", Params: map[string]any{"label": "focus", "strehl_aperture": R, "strehl_distance": z}},
	}
	res := mustSim(t, cfg)
	pl := res.Planes[0]
	// Ideal on-axis intensity of a diffraction-limited circular pupil:
	// I(0) = P * pi*R^2 / (lambda^2 f^2).
	ideal := pl.Stats.Power * math.Pi * R * R / (cfg.Wavelength * cfg.Wavelength * z * z)
	if rel := math.Abs(pl.Stats.Peak-ideal) / ideal; rel > 0.10 {
		t.Fatalf("Airy peak wrong: got %g, want %g (rel %g)", pl.Stats.Peak, ideal, rel)
	}
	// First null at 1.22 lambda f / D.
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	null := 1.22 * cfg.Wavelength * z / (2 * R)
	ci := int(math.Round(pl.Stats.CentroidX/pl.DX + float64(pl.Size)/2))
	// Locate the first dark ring: first dip below 2% of the peak, then
	// refine to the local minimum around it.
	first := -1
	for i := ci + 3; i < ci+30; i++ {
		if prof.V[i] < 0.02*pl.Stats.Peak {
			first = i
			break
		}
	}
	if first < 0 {
		t.Fatal("no dark ring found in Airy profile")
	}
	best, bestV := -1, math.Inf(1)
	for i := first - 3; i <= first+3; i++ {
		if prof.V[i] < bestV {
			bestV, best = prof.V[i], i
		}
	}
	got := prof.X[best]
	if math.Abs(got-null)/null > 0.25 {
		t.Fatalf("Airy first null at %g m, want %g m", got, null)
	}
	if s := pl.Stats.Strehl; math.Abs(s-1) > 0.10 {
		t.Fatalf("Strehl of perfect system = %g, want ~1", s)
	}
}

// --- 2. Single slit: Fraunhofer sinc^2 pattern ---

func TestSlitFraunhofer(t *testing.T) {
	cfg := scalarCfg(1024, 0.02, 632.8e-9)
	cfg.Source = planeSource()
	z := 1.0
	// Integer pixel width avoids the Dirichlet/sinc mismatch of the sampled
	// rectangle function; the y-extent fills the grid (effectively 1-D).
	dx := cfg.Grid.Width / float64(cfg.Grid.Size)
	a := 20 * dx
	cfg.Elements = []ElementSpec{
		{Type: "aperture", Params: map[string]any{"shape": "rectangle", "width": a, "height": cfg.Grid.Width}},
		{Type: "propagate", Params: map[string]any{"distance": z, "method": "fraunhofer"}},
		{Type: "sensor", Params: map[string]any{"label": "screen"}},
	}
	res := mustSim(t, cfg)
	pl := res.Planes[0]
	// Fraunhofer output pixel size must be lambda*z/(N*dx).
	wantDX := cfg.Wavelength * z / (float64(cfg.Grid.Size) * cfg.Grid.Width / float64(cfg.Grid.Size))
	if math.Abs(pl.DX-wantDX)/wantDX > 1e-9 {
		t.Fatalf("Fraunhofer pixel size %g, want %g", pl.DX, wantDX)
	}
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	// First minimum at lambda z / a.
	null := cfg.Wavelength * z / a
	peak := pl.Stats.Peak
	ci := int(math.Round(pl.Stats.CentroidX/pl.DX + float64(pl.Size)/2))
	best, bestV := -1, math.Inf(1)
	nullIdx := int(math.Round(null / pl.DX))
	for i := ci + nullIdx - 25; i <= ci+nullIdx+25; i++ {
		if i < 0 || i >= len(prof.V) {
			continue
		}
		if prof.V[i] < bestV {
			bestV, best = prof.V[i], i
		}
	}
	if bestV > 2e-3*peak {
		t.Fatalf("slit first minimum not dark: %g of peak", bestV/peak)
	}
	if math.Abs(prof.X[best]-null)/null > 0.10 {
		t.Fatalf("slit first null at %g m, want %g m", prof.X[best], null)
	}
	// Half-intensity point: sinc^2(x) = 0.4053 of the PROFILE maximum at
	// exactly half the measured null distance (shape ratio check).
	pmax := 0.0
	for _, v := range prof.V {
		if v > pmax {
			pmax = v
		}
	}
	half := 0.5 * prof.X[best]
	hi := ci + int(math.Round(half/pl.DX))
	for i := hi - 6; i < hi+6 && i+1 < len(prof.V); i++ {
		if prof.V[i] >= 0.4053*pmax && prof.V[i+1] < 0.4053*pmax {
			got := prof.X[i] + (prof.X[i+1]-prof.X[i])*(0.4053*pmax-prof.V[i])/(prof.V[i+1]-prof.V[i])
			if math.Abs(got-half)/half > 0.10 {
				t.Fatalf("slit half-max at %g m, want %g m", got, half)
			}
			return
		}
	}
	t.Fatal("could not locate half-intensity crossing")
}

// --- 3. Sinusoidal phase grating: Raman-Nath order intensities ---

func TestGratingOrders(t *testing.T) {
	cfg := scalarCfg(1024, 0.004, 632.8e-9)
	cfg.Source = planeSource()
	period, z := 2e-5, 1.0
	m := 2.0 // phase modulation depth
	cfg.Elements = []ElementSpec{
		{Type: "grating", Params: map[string]any{"kind": "phase_sin", "period": period, "modulation": m}},
		{Type: "propagate", Params: map[string]any{"distance": z, "method": "fraunhofer"}},
		{Type: "sensor", Params: map[string]any{"label": "orders"}},
	}
	res := mustSim(t, cfg)
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Orders at sin(theta_q) = q*lambda/period.
	ci := int(math.Round(pl.Stats.CentroidX/pl.DX + float64(pl.Size)/2))
	lobe := func(q int) float64 {
		xq := z * math.Sin(math.Asin(float64(q)*cfg.Wavelength/period))
		lo := ci + int(math.Round((xq-0.002)/pl.DX))
		hi := ci + int(math.Round((xq+0.002)/pl.DX))
		if lo < 0 {
			lo = 0
		}
		if hi >= len(prof.V) {
			hi = len(prof.V) - 1
		}
		mx := 0.0
		for i := lo; i <= hi; i++ {
			if prof.V[i] > mx {
				mx = prof.V[i]
			}
		}
		return mx
	}
	i0, i1, i2 := lobe(0), lobe(1), lobe(2)
	// Raman-Nath: I_q = J_q(m/2)^2 with m/2 = 1.
	want := math.Pow(jn(1, 1), 2) / math.Pow(jn(0, 1), 2) // I1/I0
	if rel := math.Abs(i1/i0-want) / want; rel > 0.15 {
		t.Fatalf("I1/I0 = %g, want %g", i1/i0, want)
	}
	want2 := math.Pow(jn(2, 1), 2) / math.Pow(jn(1, 1), 2)
	if rel := math.Abs(i2/i1-want2) / want2; rel > 0.35 {
		t.Fatalf("I2/I1 = %g, want %g", i2/i1, want2)
	}
}

// jn returns the Bessel function of the first kind of integer order via the
// ascending series (adequate for the test values).
func jn(nn int, x float64) float64 {
	s := 0.0
	for k := 0; k < 40; k++ {
		term := math.Pow(x/2, float64(2*k+nn)) / (factorial(k) * factorial(k+nn))
		if k%2 == 1 {
			term = -term
		}
		if math.Abs(term) < 1e-16 {
			break
		}
		s += term
	}
	return s
}

// --- 4. Jones calculus ---

func TestJonesCalculus(t *testing.T) {
	cfg := scalarCfg(256, 0.01, 632.8e-9)
	cfg.Polarized = ptrBool(true)
	cfg.Source = SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "polarization": "d"}}
	// QWP with fast axis x converts 45-degree linear to circular.
	cfg.Elements = []ElementSpec{
		{Type: "retarder", Params: map[string]any{"retardance": math.Pi / 2, "axis": 0}},
		{Type: "sensor", Params: map[string]any{}},
	}
	res := mustSim(t, cfg)
	pl := res.Planes[0]
	c := pl.Size/2*pl.Size + pl.Size/2
	ix := math.Hypot(real(pl.Ex[c]), imag(pl.Ex[c]))
	iy := math.Hypot(real(pl.Ey[c]), imag(pl.Ey[c]))
	if math.Abs(ix-iy)/ix > 0.01 {
		t.Fatalf("circular polarization amplitudes unequal: |Ex|=%g |Ey|=%g", ix, iy)
	}
	dph := math.Remainder(math.Atan2(imag(pl.Ex[c]), real(pl.Ex[c]))-math.Atan2(imag(pl.Ey[c]), real(pl.Ey[c])), 2*math.Pi)
	if math.Abs(math.Abs(dph)-math.Pi/2) > 1e-6 {
		t.Fatalf("QWP phase difference = %g, want +-pi/2", dph)
	}
	if rel := math.Abs(pl.Stats.Power-1e-3) / 1e-3; rel > 1e-6 {
		t.Fatalf("retarder must conserve power, loss rel=%g", rel)
	}

	// Crossed polarizers extinguish the beam.
	cfg2 := scalarCfg(256, 0.01, 632.8e-9)
	cfg2.Polarized = ptrBool(true)
	cfg2.Source = SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "polarization": "x"}}
	cfg2.Elements = []ElementSpec{
		{Type: "polarizer", Params: map[string]any{"angle": 0}},
		{Type: "polarizer", Params: map[string]any{"angle": math.Pi / 2}},
		{Type: "sensor", Params: map[string]any{}},
	}
	res2 := mustSim(t, cfg2)
	if res2.Planes[0].Stats.Power > 1e-9 {
		t.Fatalf("crossed polarizers transmitted %g W", res2.Planes[0].Stats.Power)
	}

	// A 45-degree polarizer on x light transmits half the power.
	cfg3 := scalarCfg(256, 0.01, 632.8e-9)
	cfg3.Polarized = ptrBool(true)
	cfg3.Source = SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "polarization": "x"}}
	cfg3.Elements = []ElementSpec{
		{Type: "polarizer", Params: map[string]any{"angle": math.Pi / 4}},
		{Type: "sensor", Params: map[string]any{}},
	}
	res3 := mustSim(t, cfg3)
	if rel := math.Abs(res3.Planes[0].Stats.Power-5e-4) / 5e-4; rel > 1e-3 {
		t.Fatalf("45 deg polarizer transmitted %g W, want 5e-4", res3.Planes[0].Stats.Power)
	}
}

// --- 5. Mach-Zehnder interferometer: complementary outputs, energy balance ---

func mzConfig(d2, d3 float64) Config {
	cfg := scalarCfg(256, 0.01, 632.8e-9)
	cfg.Source = planeSource()
	cfg.Elements = []ElementSpec{
		{Type: "propagate", Params: map[string]any{"distance": 0.01}},
		{Type: "beamsplitter", Params: map[string]any{"reflectivity": 0.5, "reflected_arm": map[string]any{
			"elements": []any{map[string]any{"type": "propagate", "params": map[string]any{"distance": d3}}},
		}}},
		{Type: "propagate", Params: map[string]any{"distance": d2}},
		{Type: "combiner", Params: map[string]any{"outputs": []any{
			map[string]any{"label": "p1", "weights": []any{
				map[string]any{"arm": "main", "re": 0.70710678, "im": 0},
				map[string]any{"arm": "bs0", "re": 0, "im": 0.70710678}}},
			map[string]any{"label": "p2", "weights": []any{
				map[string]any{"arm": "main", "re": 0, "im": 0.70710678},
				map[string]any{"arm": "bs0", "re": 0.70710678, "im": 0}}},
		}}},
	}
	return cfg
}

func TestMachZehnder(t *testing.T) {
	// Balanced: destructive on p1, constructive on p2.
	res := mustSim(t, mzConfig(0.01, 0.01))
	p1 := res.Planes[0].Stats.Power
	p2 := res.Planes[1].Stats.Power
	if p1 > 1e-4 {
		t.Fatalf("balanced MZ dark port power %g W, want ~0", p1)
	}
	if rel := math.Abs(p2-1e-3) / 1e-3; rel > 0.01 {
		t.Fatalf("balanced MZ bright port power %g W, want 1e-3", p2)
	}
	// Half-wave path difference flips the outputs.
	res = mustSim(t, mzConfig(0.01, 0.01+316.4e-9))
	p1 = res.Planes[0].Stats.Power
	p2 = res.Planes[1].Stats.Power
	if rel := math.Abs(p1-1e-3) / 1e-3; rel > 0.01 {
		t.Fatalf("half-wave MZ p1 power %g W, want 1e-3", p1)
	}
	if p2 > 1e-4 {
		t.Fatalf("half-wave MZ p2 power %g W, want ~0", p2)
	}
	// Quarter-wave difference: 50/50 split, energy conserved.
	res = mustSim(t, mzConfig(0.01, 0.01+158.2e-9))
	p1 = res.Planes[0].Stats.Power
	p2 = res.Planes[1].Stats.Power
	if rel := math.Abs(p1-5e-4) / 5e-4; rel > 0.02 {
		t.Fatalf("quarter-wave MZ p1 = %g W, want 5e-4", p1)
	}
	if rel := math.Abs(p1+p2-1e-3) / 1e-3; rel > 1e-3 {
		t.Fatalf("MZ energy not conserved: p1+p2 = %g", p1+p2)
	}
}

// --- 6. Michelson interferometer (folded arm with mirror) ---

func TestMichelson(t *testing.T) {
	cfg := scalarCfg(256, 0.01, 632.8e-9)
	cfg.Source = planeSource()
	wl := cfg.Wavelength
	d1, d2 := 0.005, 0.005
	// Arm length chosen so the round-trip path difference
	// Delta = 2k(L1 - d2) equals pi/2.
	L1 := d2 + wl/8
	// Symmetric BS: [out1,out2] = [[t, ir],[ir, t]] with t=r=1/sqrt(2).
	// Both arms double-pass their mirrors; det = t*arm + ir*main,
	// src_port = ir*arm + t*main.
	cfg.Elements = []ElementSpec{
		{Type: "propagate", Params: map[string]any{"distance": d1}},
		{Type: "beamsplitter", Params: map[string]any{"reflectivity": 0.5, "reflected_arm": map[string]any{
			"elements": []any{
				map[string]any{"type": "propagate", "params": map[string]any{"distance": L1}},
				map[string]any{"type": "mirror", "params": map[string]any{}},
				map[string]any{"type": "propagate", "params": map[string]any{"distance": L1}},
			},
		}}},
		{Type: "propagate", Params: map[string]any{"distance": d2}},
		{Type: "mirror", Params: map[string]any{}},
		{Type: "propagate", Params: map[string]any{"distance": d2}},
		{Type: "combiner", Params: map[string]any{"outputs": []any{
			map[string]any{"label": "det", "weights": []any{
				map[string]any{"arm": "main", "re": 0.70710678, "im": 0},
				map[string]any{"arm": "bs0", "re": 0, "im": 0.70710678}}},
			map[string]any{"label": "src_port", "weights": []any{
				map[string]any{"arm": "main", "re": 0, "im": 0.70710678},
				map[string]any{"arm": "bs0", "re": 0.70710678, "im": 0}}},
		}}},
	}
	res := mustSim(t, cfg)
	pDet := res.Planes[0].Stats.Power
	pSrc := res.Planes[1].Stats.Power
	// P_det = P(1-cos Delta)/2, P_src = P(1+cos Delta)/2, Delta = pi/2.
	if rel := math.Abs(pDet-5e-4) / 5e-4; rel > 0.02 {
		t.Fatalf("Michelson detector power %g W, want 5e-4", pDet)
	}
	if rel := math.Abs(pSrc-5e-4) / 5e-4; rel > 0.02 {
		t.Fatalf("Michelson source port power %g W, want 5e-4", pSrc)
	}
	if rel := math.Abs(pDet+pSrc-1e-3) / 1e-3; rel > 1e-3 {
		t.Fatalf("Michelson energy not conserved: %g W", pDet+pSrc)
	}
}

func TestMichelsonBalancedArms(t *testing.T) {
	cfg := scalarCfg(256, 0.01, 632.8e-9)
	cfg.Source = planeSource()
	cfg.Elements = []ElementSpec{
		{Type: "propagate", Params: map[string]any{"distance": 0.01}},
		{Type: "beamsplitter", Params: map[string]any{"reflectivity": 0.5, "reflected_arm": map[string]any{
			"elements": []any{
				map[string]any{"type": "propagate", "params": map[string]any{"distance": 0.01}},
				map[string]any{"type": "mirror", "params": map[string]any{}},
				map[string]any{"type": "propagate", "params": map[string]any{"distance": 0.01}},
			},
		}}},
		{Type: "propagate", Params: map[string]any{"distance": 0.01}},
		{Type: "mirror", Params: map[string]any{}},
		{Type: "propagate", Params: map[string]any{"distance": 0.01}},
		{Type: "combiner", Params: map[string]any{"outputs": []any{
			map[string]any{"label": "det", "weights": []any{
				map[string]any{"arm": "main", "re": 0.70710678, "im": 0},
				map[string]any{"arm": "bs0", "re": 0, "im": 0.70710678}}},
			map[string]any{"label": "src_port", "weights": []any{
				map[string]any{"arm": "main", "re": 0, "im": 0.70710678},
				map[string]any{"arm": "bs0", "re": 0.70710678, "im": 0}}},
		}}},
	}
	res := mustSim(t, cfg)
	pDet := res.Planes[0].Stats.Power
	pSrc := res.Planes[1].Stats.Power
	if pDet > 1e-4 {
		t.Fatalf("balanced Michelson detector power %g W, want ~0", pDet)
	}
	if rel := math.Abs(pSrc - 1e-3) / 1e-3; rel > 0.01 {
		t.Fatalf("balanced Michelson source-port power %g W, want 1e-3", pSrc)
	}
}

// --- 7. Fresnel zone plate: strong focus at f, none at 2f ---

func TestZonePlate(t *testing.T) {
	cfg := scalarCfg(1024, 0.004, 632.8e-9)
	cfg.Source = planeSource()
	f, R := 0.05, 0.002
	cfg.Elements = []ElementSpec{
		{Type: "zone_plate", Params: map[string]any{"f": f, "radius": R, "kind": "phase"}},
		{Type: "propagate", Params: map[string]any{"distance": f}},
		{Type: "sensor", Params: map[string]any{"label": "at_f"}},
		{Type: "propagate", Params: map[string]any{"distance": f}},
		{Type: "sensor", Params: map[string]any{"label": "at_2f"}},
	}
	res := mustSim(t, cfg)
	pf := res.Planes[0].Stats.Peak
	p2 := res.Planes[1].Stats.Peak
	if pf < 5*p2 {
		t.Fatalf("zone plate focus not dominant: peak(f)=%g, peak(2f)=%g", pf, p2)
	}
	// Phase zone plate concentrates ~4/pi^2 of the power into the +1 order.
	ideal := 1e-3 * math.Pi * R * R / (cfg.Wavelength * cfg.Wavelength * f * f)
	if pf < 0.15*ideal || pf > 0.8*ideal {
		t.Fatalf("zone plate focal peak %g, expected ~0.4*%g", pf, ideal)
	}
}

// --- 8. Mirror fold: round trip doubles the path phase (2kL), amplitude and
// transverse profile are restored exactly. The on-axis phase also carries the
// doubled Gouy shift, which verifies the phase bookkeeping of folded paths.

func TestBackwardRoundTrip(t *testing.T) {
	cfg := scalarCfg(512, 0.005, 532e-9)
	w0 := 4e-4
	cfg.Source = SourceSpec{Type: "gaussian", Params: map[string]any{"waist": w0}}
	z := 0.1
	cfg.Elements = []ElementSpec{
		{Type: "propagate", Params: map[string]any{"distance": z}},
		{Type: "mirror", Params: map[string]any{}},
		{Type: "propagate", Params: map[string]any{"distance": z}},
		{Type: "sensor", Params: map[string]any{}},
	}
	res := mustSim(t, cfg)
	pl := res.Planes[0]
	// The returning beam has traveled 2z total: compare against the source
	// propagated forward by 2z (waist has evolved, so w != w0).
	src, _ := BuildSource(cfg.Source, cfg.Grid.Size, cfg.Grid.Width, false, cfg.Wavelength)
	ref := src.Clone()
	if err := Propagate(ref, 2*z, MethodASM, ctxFor(cfg.Wavelength)); err != nil {
		t.Fatal(err)
	}
	var num, den float64
	for i := range src.Ex {
		a1 := math.Hypot(real(pl.Ex[i]), imag(pl.Ex[i]))
		a0 := math.Hypot(real(ref.Ex[i]), imag(ref.Ex[i]))
		d := a1 - a0
		num += d * d
		den += a0 * a0
	}
	if rel := math.Sqrt(num / den); rel > 1e-9 {
		t.Fatalf("round trip amplitude differs from 2z propagation: rel err %g", rel)
	}
	// On-axis phase of a Gaussian after traveling s from the waist:
	// phi(s) = k*s - atan(s/zR). The round trip travels s = 2z, so the Gouy
	// shift is atan(2z/zR) (it is a function of total distance, not additive).
	k := 2 * math.Pi / cfg.Wavelength
	zR := math.Pi * w0 * w0 / cfg.Wavelength
	want := 2*k*z - math.Atan(2*z/zR)
	c := pl.Ex[pl.Size/2*pl.Size+pl.Size/2]
	got := math.Remainder(math.Atan2(imag(c), real(c))-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("round trip on-axis phase %g, want %g (2kz + Gouy)", got, want)
	}
}

// --- 9. Double slit: Young fringes spaced lambda*z/d ---

func TestDoubleSlit(t *testing.T) {
	cfg := scalarCfg(1024, 0.02, 632.8e-9)
	cfg.Source = planeSource()
	z := 1.0
	dx := cfg.Grid.Width / float64(cfg.Grid.Size)
	a := 10 * dx    // slit width (integer pixels)
	sep := 100 * dx // slit separation
	cfg.Elements = []ElementSpec{
		{Type: "aperture", Params: map[string]any{"shape": "double_slit", "width": a, "height": cfg.Grid.Width, "separation": sep}},
		{Type: "propagate", Params: map[string]any{"distance": z, "method": "fraunhofer"}},
		{Type: "sensor", Params: map[string]any{"label": "fringes"}},
	}
	res := mustSim(t, cfg)
	pl := res.Planes[0]
	prof, err := pl.ProfileOf("x", KindIntensity, nil)
	if err != nil {
		t.Fatal(err)
	}
	ci := int(math.Round(pl.Stats.CentroidX/pl.DX + float64(pl.Size)/2))
	// Fringe spacing lambda*z/d, modulated by the single-slit sinc envelope.
	spacing := cfg.Wavelength * z / sep
	// Find the maxima around +/- 2 spacings and measure their distance.
	locate := func(dir int) float64 {
		target := ci + dir*int(math.Round(2*spacing/pl.DX))
		best, bestV := -1, -1.0
		for i := target - 6; i <= target+6; i++ {
			if prof.V[i] > bestV {
				bestV, best = prof.V[i], i
			}
		}
		return prof.X[best]
	}
	xp := locate(1)
	xm := locate(-1)
	got := (xp - xm) / 4 // four fringe spacings apart
	if math.Abs(got-spacing)/spacing > 0.10 {
		t.Fatalf("fringe spacing %g m, want %g m", got, spacing)
	}
}

// --- 10. Strong diffuser produces fully developed speckle ---

func TestSpeckleContrast(t *testing.T) {
	cfg := scalarCfg(512, 0.01, 532e-9)
	cfg.Source = planeSource()
	cfg.Elements = []ElementSpec{
		{Type: "diffuser", Params: map[string]any{"sigma": math.Pi, "correlation": 1e-5, "seed": 42}},
		{Type: "propagate", Params: map[string]any{"distance": 0.2}},
		{Type: "sensor", Params: map[string]any{}},
	}
	res := mustSim(t, cfg)
	pl := res.Planes[0]
	if rel := math.Abs(pl.Stats.Power-1e-3) / 1e-3; rel > 0.005 {
		t.Fatalf("phase diffuser must conserve power, rel loss %g", rel)
	}
	// Contrast over the central half of the grid.
	n := pl.Size
	q := n / 4
	var s, s2, cnt float64
	for j := q; j < 3*q; j++ {
		for i := q; i < 3*q; i++ {
			idx := j*n + i
			v := math.Hypot(real(pl.Ex[idx]), imag(pl.Ex[idx]))
			v = v * v
			s += v
			s2 += v * v
			cnt++
		}
	}
	mean := s / cnt
	c := math.Sqrt(s2/cnt-mean*mean) / mean
	if c < 0.7 || c > 1.1 {
		t.Fatalf("speckle contrast = %g, want ~1", c)
	}
}
