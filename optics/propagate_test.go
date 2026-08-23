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
