package optics

// Complex-field (amplitude AND phase) regression tests for the propagation
// kernel. Intensity-only checks cannot see sign/phase errors such as a
// missing FFT-layout checkerboard; these tests pin the kernel to analytic
// ground truths at the complex-field level.

import (
	"math"
	"math/cmplx"
	"testing"
)

// relCmpL2 returns the relative L2 distance between two complex slices.
func relCmpL2(a, b []complex128) float64 {
	var num, den float64
	for i := range b {
		d := a[i] - b[i]
		num += real(d)*real(d) + imag(d)*imag(d)
		den += real(b[i])*real(b[i]) + imag(b[i])*imag(b[i])
	}
	if den <= 0 {
		return 0
	}
	return math.Sqrt(num / den)
}

// singleBinTiltedPlaneWave builds Ex = exp(i k (sx x + sy y)) with
// (sx/lambda, sy/lambda) exactly on FFT bins, so the sampled field has a
// single spectral bin and ASM must reproduce it exactly:
// U(z) = exp(i k z sqrt(1-sx^2-sy^2)) * exp(i k (sx x + sy y)).
func singleBinTiltedPlaneWave(n int, dx, wl, sx, sy float64) *Field {
	f := NewField(n, dx, false)
	k := 2 * math.Pi / wl
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			f.Ex[j*n+i] = cexpI(k * (sx*f.X(i) + sy*y))
		}
	}
	return f
}

// ASM and asm_shift must be exact on a single-bin tilted plane wave, forward
// and backward, including a strong tilt near the Nyquist band edge.
func TestTiltedPlaneWaveSingleBinExact(t *testing.T) {
	wl := 632.8e-9
	n := 256
	k := 2 * math.Pi / wl
	cases := []struct {
		sx float64
		px int
	}{
		{0.25, 30},
		{0.9, 45}, // near band edge
	}
	for _, c := range cases {
		dx := float64(c.px) / (c.sx / wl * float64(n)) // sx/wl = px/(n*dx)
		z := 5e-4
		kz := k * math.Sqrt(1-c.sx*c.sx)
		for _, m := range []Method{MethodASM, MethodASMShift} {
			for _, zz := range []float64{z, -z} {
				f := singleBinTiltedPlaneWave(n, dx, wl, c.sx, 0)
				if err := Propagate(f, zz, m, ctxFor(wl)); err != nil {
					t.Fatal(err)
				}
				var maxErr float64
				for j := 0; j < n; j++ {
					for i := 0; i < n; i++ {
						want := cexpI(k*c.sx*f.X(i) + kz*zz)
						if e := cabs(f.Ex[j*n+i] - want); e > maxErr {
							maxErr = e
						}
					}
				}
				if maxErr > 1e-8 {
					t.Errorf("%s sx=%g z=%+g: tilted plane wave not exact (max err %.3e)", m, c.sx, zz, maxErr)
				}
			}
		}
	}
}

// analyticGaussianFresnel evaluates the closed-form Fresnel integral of the
// separable Gaussian exp(-(x^2+y^2)/w0^2):
//
//	U(z) = e^{ikz}/(i lambda z) * G(x) * G(y),
//	G(x) = sqrt(pi/beta) * exp(i pi x^2/(lambda z)) * exp(-gamma^2/(4 beta)),
//	beta = 1/w0^2 - i pi/(lambda z), gamma = 2 pi x/(lambda z).
func analyticGaussianFresnel(f *Field, wl, z, w0 float64) []complex128 {
	n := f.N
	beta := complex(1/(w0*w0), -math.Pi/(wl*z))
	G := make([]complex128, n)
	for i := 0; i < n; i++ {
		x := f.X(i)
		gamma := 2 * math.Pi * x / (wl * z)
		G[i] = cmplx.Sqrt(complex(math.Pi, 0)/beta) *
			cexpI(math.Pi*x*x/(wl*z)) *
			cmplx.Exp(-complex(gamma*gamma, 0)/(4*beta))
	}
	pre := cmplx.Exp(complex(0, 2*math.Pi/wl*z)) / complex(0, wl*z)
	out := make([]complex128, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			out[j*n+i] = pre * G[i] * G[j]
		}
	}
	return out
}

// The scalar propagation core must match the analytic Fresnel integral of a
// Gaussian on the complex field (amplitude, wavefront curvature and Gouy
// phase together), not merely in intensity.
func TestASMGaussianMatchesAnalyticComplex(t *testing.T) {
	wl := 632.8e-9
	n, width := 256, 0.004
	w0 := 5e-4
	for _, z := range []float64{0.005, 0.05, 0.3} {
		f := NewField(n, width/float64(n), false)
		for j := 0; j < n; j++ {
			y := f.Y(j)
			for i := 0; i < n; i++ {
				x := f.X(i)
				f.Ex[j*n+i] = complex(math.Exp(-(x*x+y*y)/(w0*w0)), 0)
			}
		}
		want := analyticGaussianFresnel(f, wl, z, w0)
		for _, m := range []Method{MethodASM, MethodASMPad, MethodFresnelTF} {
			g := f.Clone()
			if err := Propagate(g, z, m, ctxFor(wl)); err != nil {
				t.Fatal(err)
			}
			if r := relCmpL2(g.Ex, want); r > 2e-3 {
				t.Errorf("z=%g %s: deviates from analytic Fresnel-Gaussian by %g", z, m, r)
			}
		}
	}
}

// Regression: the Fraunhofer kernel must compensate the centered-layout
// checkerboard e^{i pi (p+q)} of the raw DFT. Without it the complex far
// field is wrong by (-1)^(i+j) (pi flips between adjacent pixels): invisible
// in intensity, wrong in phase, and corrupting for any later coherent use.
func TestFraunhoferComplexFieldExact(t *testing.T) {
	wl := 632.8e-9
	n, width := 256, 0.002
	w0 := 1e-4
	z := 20.0 // Fresnel number ~ 2.5e-3 << 1
	dx := width / float64(n)
	f := NewField(n, dx, false)
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			x := f.X(i)
			f.Ex[j*n+i] = complex(math.Exp(-(x*x+y*y)/(w0*w0)), 0)
		}
	}
	k := 2 * math.Pi / wl
	dxOut := wl * z / (float64(n) * dx)
	want := make([]complex128, n*n)
	for j := 0; j < n; j++ {
		y := (float64(j) - float64(n)/2) * dxOut
		for i := 0; i < n; i++ {
			x := (float64(i) - float64(n)/2) * dxOut
			r2 := x*x + y*y
			s := math.Pi * w0 * math.Sqrt(r2) / (wl * z)
			want[j*n+i] = cmplx.Exp(complex(0, k*z)) / complex(0, wl*z) *
				cexpI(k*r2/(2*z)) * complex(math.Pi*w0*w0, 0) *
				cmplx.Exp(-complex(s*s, 0))
		}
	}
	g := f.Clone()
	if err := Propagate(g, z, MethodFraunhofer, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	if r := relCmpL2(g.Ex, want); r > 1e-3 {
		t.Errorf("fraunhofer deviates from the analytic complex far field by %g (checkerboard regression?)", r)
	}
}

// Regression: the Fraunhofer Fresnel-number warning must measure the INPUT
// beam support D (F = D^2/(lambda z) > 0.5), not the propagated far-field
// pattern. A shrinking Gaussian input (w0 = 2e-4, D = sqrt(2) w0) crosses
// F = 0.5 at z ~ 0.25 m: it must warn at z = 0.1 m and stay silent at 50 m.
func TestFraunhoferNearfieldWarningMeasuresInput(t *testing.T) {
	wl := 632.8e-9
	n, width := 256, 0.002
	w0 := 2e-4
	build := func() *Field {
		f := NewField(n, width/float64(n), false)
		for j := 0; j < n; j++ {
			y := f.Y(j)
			for i := 0; i < n; i++ {
				x := f.X(i)
				f.Ex[j*n+i] = complex(math.Exp(-(x*x+y*y)/(w0*w0)), 0)
			}
		}
		return f
	}
	for _, tc := range []struct {
		z    float64
		want bool
	}{{0.1, true}, {50.0, false}} {
		w := &Warnings{}
		if err := Propagate(build(), tc.z, MethodFraunhofer, &Context{Wavelength: wl, Warnings: w}); err != nil {
			t.Fatal(err)
		}
		fired := false
		for _, x := range w.List() {
			if x.Code == "fraunhofer_nearfield" {
				fired = true
			}
		}
		if fired != tc.want {
			t.Errorf("fraunhofer_nearfield at z=%g m: fired=%v, want %v", tc.z, fired, tc.want)
		}
	}
}

// With a well-contained moderate tilt (no wrap, no aliasing) all ASM variants
// must agree with plain asm on the complex field.
func TestASMPadShiftVariantsComplexAgree(t *testing.T) {
	wl := 632.8e-9
	n, width := 256, 0.01
	z := 0.1
	src := SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "tilt_x": 0.002}}
	ref, err := BuildSource(src, n, width, false, wl)
	if err != nil {
		t.Fatal(err)
	}
	if err := Propagate(ref, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	for _, m := range []Method{MethodASMPad, MethodASMShift, MethodASMShiftPad} {
		g, err := BuildSource(src, n, width, false, wl)
		if err != nil {
			t.Fatal(err)
		}
		if err := Propagate(g, z, m, ctxFor(wl)); err != nil {
			t.Fatal(err)
		}
		if r := relCmpL2(g.Ex, ref.Ex); r > 1e-6 {
			t.Errorf("%s deviates from asm (complex field) by %g", m, r)
		}
	}
}

// Regression: the vectorial ASM must keep a single-bin tilted plane wave
// exact, including the longitudinal component Ez = -(kx Ex + ky Ey)/kz.
func TestVectorialPlaneWaveSingleBinExact(t *testing.T) {
	wl := 632.8e-9
	n := 256
	sx, px := 0.3, 40
	dx := float64(px) / (sx / wl * float64(n))
	z := 1e-3
	k := 2 * math.Pi / wl
	kz := k * math.Sqrt(1-sx*sx)
	kx := k * sx

	f := NewField(n, dx, true)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			f.Ex[j*n+i] = cexpI(k * sx * f.X(i))
		}
	}
	if err := PropagateVectorial(f, z, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			idx := j*n + i
			wantEx := cexpI(k*sx*f.X(i) + kz*z)
			wantEz := complex(-kx/kz, 0) * wantEx
			if e := cabs(f.Ex[idx] - wantEx); e > 1e-8 {
				t.Fatalf("vectorial Ex err %.3e at (%d,%d)", e, i, j)
			}
			if e := cabs(f.Ez[idx] - wantEz); e > 1e-8 {
				t.Fatalf("vectorial Ez err %.3e at (%d,%d)", e, i, j)
			}
		}
	}
}

// Regression: the converging spherical source must be the phase-conjugate of
// the diverging one (phase -k*d), so it focuses at z = +radius. The old
// implementation multiplied the amplitude by -1, which only adds a constant
// pi phase and leaves the wave diverging.
func TestSphericalSourceConvergingPhase(t *testing.T) {
	wl := 632.8e-9
	k := 2 * math.Pi / wl
	rad := 0.1
	n := 64
	row := n / 2
	i := 40
	build := func(conv int) *Field {
		p := map[string]any{"radius": rad}
		if conv != 0 {
			p["converging"] = conv
		}
		f, err := BuildSource(SourceSpec{Type: "spherical", Params: p}, n, 0.01, false, wl)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	x := build(0).X(i)
	d := math.Sqrt(rad*rad + x*x)
	for _, tc := range []struct {
		conv int
		want float64
	}{{0, k * d}, {1, -k * d}} {
		f := build(tc.conv)
		ph := math.Atan2(imag(f.Ex[row*n+i]), real(f.Ex[row*n+i]))
		if got := math.Remainder(ph-tc.want, 2*math.Pi); math.Abs(got) > 1e-9 {
			t.Errorf("spherical (converging=%d) phase %+.9f rad, want %+.9f (mod 2pi)", tc.conv, ph, math.Remainder(tc.want, 2*math.Pi))
		}
	}
}

// Behavioral companion to the phase check: a converging spherical wave must
// concentrate toward its focus (on-axis peak growing as z approaches the
// radius). The aperture is chosen small enough (edge direction cosine
// 0.01 < lambda/(2 dx)) that the source is properly sampled across the whole
// window, and the focal Airy disk (0.61 lambda/NA ~ 3.9e-5 m = 20 px) is
// resolved. The diverging branch is pinned exactly by the phase test above;
// a peak-based diverging control would be meaningless because the Fresnel
// diffraction of a uniform aperture oscillates strongly on axis.
func TestSphericalSourceConvergingFocuses(t *testing.T) {
	wl := 632.8e-9
	n, width := 512, 1e-3
	rad := 0.05
	build := func(conv int) *Field {
		p := map[string]any{"radius": rad}
		if conv != 0 {
			p["converging"] = conv
		}
		f, err := BuildSource(SourceSpec{Type: "spherical", Params: p}, n, width, false, wl)
		if err != nil {
			t.Fatal(err)
		}
		return f
	}
	peak := func(f *Field) float64 {
		var m float64
		for i := range f.Ex {
			if v := f.Intensity(i); v > m {
				m = v
			}
		}
		return m
	}
	p0 := peak(build(1))
	p1 := peak(prop(t, build(1), 0.9*rad, wl))
	p2 := peak(prop(t, build(1), 0.95*rad, wl))
	t.Logf("converging peak: z=0: %g, z=0.9R: %g, z=0.95R: %g", p0, p1, p2)
	if !(p2 > 1.5*p1 && p1 > 1.5*p0) {
		t.Errorf("converging wave does not concentrate toward focus: peak(0)=%g, peak(0.9R)=%g, peak(0.95R)=%g", p0, p1, p2)
	}
}

func prop(t *testing.T, f *Field, z float64, wl float64) *Field {
	t.Helper()
	if err := Propagate(f, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	return f
}

// Regression: Noll-normalized Zernike modes. m != 0 modes carry
// sqrt(2(n+1)); the m = 0 modes (defocus c4, spherical c11) must carry
// sqrt(n+1) as well, so all coefficients share the same RMS meaning.
func TestZernikeNollNormalization(t *testing.T) {
	wl := 632.8e-9
	n, dx := 64, 1e-4
	el, err := NewElement(ElementSpec{Type: "zernike", Params: map[string]any{"radius": 3e-3, "c4": 0.25}})
	if err != nil {
		t.Fatal(err)
	}
	f := NewField(n, dx, false)
	for i := range f.Ex {
		f.Ex[i] = 1
	}
	if err := el.Apply(f, &Context{Wavelength: wl}); err != nil {
		t.Fatal(err)
	}
	// Defocus R_2^0 = 2 rho^2 - 1, Noll Z4 = sqrt(3) R_2^0. Phase difference
	// between the grid center (rho=0) and the rim (rho=1) along x:
	// 2 pi * c * sqrt(3) * 2.
	cx := (n/2)*n + n/2
	rim := (n/2)*n + n/2 + 30 // x = 30*dx = 3e-3 = radius -> rho = 1
	phC := math.Atan2(imag(f.Ex[cx]), real(f.Ex[cx]))
	phR := math.Atan2(imag(f.Ex[rim]), real(f.Ex[rim]))
	want := 2 * math.Pi * 0.25 * math.Sqrt(3) * 2
	if got := math.Remainder(phR-phC, 2*math.Pi); math.Abs(got-math.Remainder(want, 2*math.Pi)) > 1e-9 {
		t.Errorf("defocus phase span %+.6f rad, want %+.6f (missing sqrt(n+1) Noll normalization?)", got, want)
	}
}
