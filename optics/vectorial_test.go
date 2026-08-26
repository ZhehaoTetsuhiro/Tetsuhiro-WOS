package optics

import (
	"math"
	"testing"
)

func TestVectorialPlaneWave(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	f, _ := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{}}, n, width, false, wl)
	z := 0.3
	p0 := f.Power()
	if err := PropagateVectorial(f, z, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	// On-axis plane wave: the longitudinal component is zero.
	for i := range f.Ez {
		if cabs(f.Ez[i]) > 1e-12 {
			t.Fatalf("Ez should be zero for on-axis plane wave at %d: %v", i, f.Ez[i])
		}
	}
	want := 2 * math.Pi / wl * z
	c := f.Ex[(n/2)*n+n/2]
	got := math.Remainder(math.Atan2(imag(c), real(c))-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("plane wave phase wrong: got %g want %g", got, want)
	}
	if rel := math.Abs(f.Power()-p0) / p0; rel > 1e-6 {
		t.Fatalf("power not conserved: rel %g", rel)
	}
}

func TestVectorialTiltedPlaneWave(t *testing.T) {
	n, wl := 512, 632.8e-9
	dx := 5e-7
	q := 180
	fx0 := float64(q) / (float64(n) * dx)
	sinT := wl * fx0
	if sinT <= 0.2 || sinT >= 1 {
		t.Fatalf("test setup: sin(theta)=%g not in (0.2,1)", sinT)
	}
	tanT := sinT / math.Sqrt(1-sinT*sinT)
	f := NewField(n, dx, false)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			f.Ex[j*n+i] = cexpI(2 * math.Pi * fx0 * f.X(i))
		}
	}
	if err := PropagateVectorial(f, 1e-4, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	// Divergence-free condition for a tilted x-polarized plane wave:
	// Ez = -(kx/kz) Ex = -tan(theta) Ex at every pixel.
	for i := range f.Ex {
		want := complex(-tanT, 0) * f.Ex[i]
		if cabs(f.Ez[i]-want) > 1e-6 {
			t.Fatalf("Ez mismatch at %d: got %v want %v", i, f.Ez[i], want)
		}
	}
}
