package optics

import (
	"math"
	"testing"
)

// buildGaussianTilted constructs a normalized Gaussian beam with explicit
// waist and x-tilt.
func buildGaussianTilted(n int, width, wl, waist, theta float64) *Field {
	f, err := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": waist, "tilt_x": theta}}, n, width, false, wl)
	if err != nil {
		panic(err)
	}
	return f
}

func centroidX(f *Field) float64 {
	var p, px float64
	for j := 0; j < f.N; j++ {
		for i := 0; i < f.N; i++ {
			w := f.Intensity(j*f.N + i)
			p += w
			px += w * f.X(i)
		}
	}
	if p <= 0 {
		return 0
	}
	return px / p
}

// relL2 returns the relative L2 distance between two fields' Ex components.
func relL2(a, b *Field) float64 {
	var num, den float64
	for i := range a.Ex {
		d := real(a.Ex[i]) - real(b.Ex[i])
		e := imag(a.Ex[i]) - imag(b.Ex[i])
		num += d*d + e*e
		den += real(b.Ex[i])*real(b.Ex[i]) + imag(b.Ex[i])*imag(b.Ex[i])
	}
	if den <= 0 {
		return 0
	}
	return math.Sqrt(num / den)
}

// When the beam is well contained inside the window there is no wrap-around,
// so the zero-padded ASM must reproduce the standard ASM to high precision.
func TestASMPadAgreesASMNoWrap(t *testing.T) {
	n, width, wl := 512, 0.01, 632.8e-9
	z := 0.2
	ref := buildGaussianTilted(n, width, wl, 1e-3, 0)
	if err := Propagate(ref, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	pad := buildGaussianTilted(n, width, wl, 1e-3, 0)
	if err := Propagate(pad, z, MethodASMPad, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	if r := relL2(pad, ref); r > 1e-4 {
		t.Fatalf("asm_pad deviates from asm (no wrap) by %g", r)
	}
}

// The zero-padded ASM must conserve power (the propagating spectrum is lossless).
func TestASMPadPowerConservation(t *testing.T) {
	n, width, wl := 512, 0.005, 532e-9
	// A beam well inside the window: the zero-pad boundary truncation is
	// negligible, so cropping loses no power.
	f := buildGaussianTilted(n, width, wl, 5e-4, 0)
	p0 := f.Power()
	for _, z := range []float64{0.01, 0.1, 0.5} {
		g := f.Clone()
		if err := Propagate(g, z, MethodASMPad, ctxFor(wl)); err != nil {
			t.Fatal(err)
		}
		if rel := math.Abs(g.Power()-p0) / p0; rel > 1e-6 {
			t.Fatalf("asm_pad power not conserved at z=%g: rel %g", z, rel)
		}
	}
}

// For a moderate tilt the carrier shift must cancel exactly: asm_shift agrees
// with asm and lands on the analytic lateral shift z*tan(theta).
func TestASMShiftMatchesASMModerateTilt(t *testing.T) {
	n, width, wl := 512, 0.01, 632.8e-9
	theta := 0.005
	z := 0.2
	ref := buildGaussianTilted(n, width, wl, 1e-3, theta)
	if err := Propagate(ref, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	sh := buildGaussianTilted(n, width, wl, 1e-3, theta)
	if err := Propagate(sh, z, MethodASMShift, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	if r := relL2(sh, ref); r > 1e-3 {
		t.Fatalf("asm_shift deviates from asm (moderate tilt) by %g", r)
	}
	want := z * math.Tan(theta)
	if math.Abs(centroidX(sh)-want) > 2e-6 {
		t.Fatalf("asm_shift centroid %g m, want %g m", centroidX(sh), want)
	}
}

// Spectral wrap: a tightly focused, strongly tilted beam has a wide spectrum
// centered near the Nyquist band edge; its upper tail wraps around in the
// plain ASM, corrupting the result. The off-axis shift centers the spectrum
// before propagation and avoids the wrap, keeping the lateral shift exact.
func TestASMShiftSpectralWrapAccuracy(t *testing.T) {
	n, width, wl := 1024, 0.01, 632.8e-9
	waist, theta, z := 1e-4, 0.03, 0.05
	want := z * math.Tan(theta)
	run := func(m Method) (*Field, float64) {
		f := buildGaussianTilted(n, width, wl, waist, theta)
		if err := Propagate(f, z, m, ctxFor(wl)); err != nil {
			t.Fatal(err)
		}
		return f, math.Abs(centroidX(f) - want)
	}
	_, es := run(MethodASMShift)
	_, ea := run(MethodASM)
	if es > 1e-6 {
		t.Fatalf("asm_shift centroid error %g m (>1e-6)", es)
	}
	if es >= ea/100 {
		t.Fatalf("asm_shift error %g m not far better than plain asm %g m", es, ea)
	}
}

// Walk-off: a beam that translates near the edge of the N x N window wraps
// around in the plain ASM (circular convolution), but the 2x zero-padded ASM
// keeps it accurate.
func TestASMPadWalkOffAccuracy(t *testing.T) {
	n, width, wl := 1024, 0.01, 632.8e-9
	waist, theta, z := 1e-3, 0.03, 0.15
	want := z * math.Tan(theta)
	run := func(m Method) float64 {
		f := buildGaussianTilted(n, width, wl, waist, theta)
		if err := Propagate(f, z, m, ctxFor(wl)); err != nil {
			t.Fatal(err)
		}
		return math.Abs(centroidX(f) - want)
	}
	ep := run(MethodASMPad)
	ea := run(MethodASM)
	if ep > 3e-4 {
		t.Fatalf("asm_pad centroid error %g m (>3e-4)", ep)
	}
	if ep >= ea/2 {
		t.Fatalf("asm_pad error %g m not clearly better than plain asm %g m", ep, ea)
	}
}
