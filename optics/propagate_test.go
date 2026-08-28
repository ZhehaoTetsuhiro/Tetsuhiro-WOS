package optics

import (
	"math"
	"testing"
)

func ctxFor(wl float64) *Context {
	return &Context{Wavelength: wl, Evanescent: "decay", Warnings: &Warnings{}}
}

// An untilted plane wave must stay exactly flat and advance its phase by kz.
func TestPlaneWavePhase(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	f, err := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{}}, n, width, false, wl)
	if err != nil {
		t.Fatal(err)
	}
	z := 0.3
	if err := Propagate(f, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	want := 2 * math.Pi / wl * z
	c := f.Ex[n/2*n+n/2]
	got := math.Atan2(imag(c), real(c))
	got = math.Remainder(got-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("plane wave phase wrong: got %g want %g", got, want)
	}
	for i := range f.Ex {
		if math.Abs(f.Intensity(i)-f.Intensity(0)) > 1e-12*f.Intensity(0) {
			t.Fatalf("plane wave intensity not flat at %d", i)
		}
	}
}

// A tilted Gaussian beam translates laterally by z*tan(theta): the centroid
// of intensity is an exact observable that tests the direction-cosine physics.
func TestTiltedBeamShift(t *testing.T) {
	n, width, wl := 1024, 0.01, 632.8e-9
	theta := 0.002
	f, err := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "tilt_x": theta}}, n, width, false, wl)
	if err != nil {
		t.Fatal(err)
	}
	z := 0.3
	if err := Propagate(f, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	var p, px float64
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			w := f.Intensity(j*n + i)
			p += w
			px += w * f.X(i)
		}
	}
	got := px / p
	want := z * math.Tan(theta)
	if math.Abs(got-want) > 2e-6 {
		t.Fatalf("beam shift wrong: got %g m, want %g m", got, want)
	}
}

// Gaussian beam: ASM waist evolution must match w(z)=w0 sqrt(1+(z/zR)^2).
func TestGaussianWaistEvolution(t *testing.T) {
	n, width, wl := 1024, 0.01, 632.8e-9
	w0 := 3e-4
	f, err := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": w0}}, n, width, false, wl)
	if err != nil {
		t.Fatal(err)
	}
	zR := math.Pi * w0 * w0 / wl
	z := 0.4
	if err := Propagate(f, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	var p, pr float64
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			w := f.Intensity(j*n + i)
			p += w
			pr += w * (f.X(i)*f.X(i) + y*y)
		}
	}
	got := math.Sqrt(2 * pr / p)
	want := w0 * math.Sqrt(1+(z/zR)*(z/zR))
	if math.Abs(got-want)/want > 0.01 {
		t.Fatalf("waist mismatch: got %g m, want %g m", got, want)
	}
}

// ASM must conserve power for a band-limited field (evanescent content ~0).
func TestPowerConservationASM(t *testing.T) {
	n, width, wl := 512, 0.005, 532e-9
	f, err := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 5e-4}}, n, width, false, wl)
	if err != nil {
		t.Fatal(err)
	}
	p0 := f.Power()
	for _, z := range []float64{0.01, 0.1, 0.5, 1.5} {
		g := f.Clone()
		if err := Propagate(g, z, MethodASM, ctxFor(wl)); err != nil {
			t.Fatal(err)
		}
		if rel := math.Abs(g.Power()-p0) / p0; rel > 1e-6 {
			t.Fatalf("power not conserved at z=%g: rel loss %g", z, rel)
		}
	}
}

// Fresnel and ASM must agree in their common validity region (large enough z
// for Fresnel-TF sampling, small enough for paraxial accuracy).
func TestFresnelMatchesASM(t *testing.T) {
	n, width, wl := 1024, 0.01, 633e-9
	f, _ := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 5e-4, "tilt_x": 0.002}}, n, width, false, wl)
	z := 0.15
	ref := f.Clone()
	if err := Propagate(ref, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	g := f.Clone()
	if err := Propagate(g, z, MethodFresnelTF, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	var num, den float64
	for i := range ref.Ex {
		d := real(g.Ex[i]) - real(ref.Ex[i])
		e := imag(g.Ex[i]) - imag(ref.Ex[i])
		num += d*d + e*e
		den += real(ref.Ex[i])*real(ref.Ex[i]) + imag(ref.Ex[i])*imag(ref.Ex[i])
	}
	if rel := math.Sqrt(num / den); rel > 0.01 {
		t.Fatalf("Fresnel-TF deviates from ASM by %g", rel)
	}
}

// The Fresnel impulse-response form must agree with ASM in its validity region
// (z >= N*dx^2/lambda) as a complex field: amplitude AND phase. This guards the
// Riemann-sum dx^2 factor, the wrap-layout convolution kernel, and the absence
// of the spurious output quadratic phase (all three were once wrong together,
// which intensity-only checks could not see).
func TestFresnelIRMatchesASM(t *testing.T) {
	n, dx, wl := 256, 4e-6, 632.8e-9
	w0 := 100e-6
	z := 0.05 // N*dx^2/lambda = 6.47e-3 m << z
	if z < float64(n)*dx*dx/wl {
		t.Fatalf("test setup outside IR validity region")
	}
	f := NewField(n, dx, false)
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			x := f.X(i)
			f.Ex[j*n+i] = complex(math.Exp(-(x*x+y*y)/(w0*w0)), 0)
		}
	}
	ref := f.Clone()
	if err := Propagate(ref, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	g := f.Clone()
	if err := Propagate(g, z, MethodFresnelIR, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	var num, den float64
	for i := range ref.Ex {
		d := real(g.Ex[i]) - real(ref.Ex[i])
		e := imag(g.Ex[i]) - imag(ref.Ex[i])
		num += d*d + e*e
		den += real(ref.Ex[i])*real(ref.Ex[i]) + imag(ref.Ex[i])*imag(ref.Ex[i])
	}
	// With the zero-padded linear convolution the IR form matches ASM to
	// roundoff-level discretization error in this regime; the threshold keeps
	// two orders of margin while still catching any reintroduced kernel-layout,
	// normalization or phase error (those show up at O(0.1) or larger).
	if rel := math.Sqrt(num / den); rel > 1e-4 {
		t.Fatalf("Fresnel-IR deviates from ASM by %g", rel)
	}
	// Phase: over bright pixels the ratio g/ref must be a constant phase.
	var peak float64
	for i := range ref.Ex {
		if a := math.Hypot(real(ref.Ex[i]), imag(ref.Ex[i])); a > peak {
			peak = a
		}
	}
	ph0 := 0.0
	first := true
	for i := range ref.Ex {
		if math.Hypot(real(ref.Ex[i]), imag(ref.Ex[i])) < 0.2*peak {
			continue
		}
		ph := math.Atan2(imag(g.Ex[i]/ref.Ex[i]), real(g.Ex[i]/ref.Ex[i]))
		if first {
			ph0, first = ph, false
		}
		if d := math.Abs(math.Remainder(ph-ph0, 2*math.Pi)); d > 1e-3 {
			t.Fatalf("Fresnel-IR phase deviates from ASM by %g rad at index %d", d, i)
		}
	}
}
