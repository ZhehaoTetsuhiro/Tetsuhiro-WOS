package optics

import (
	"math"
	"testing"
)

func TestPropagate1DPlaneWave(t *testing.T) {
	n, dx, wl := 256, 1e-5, 632.8e-9
	a := make([]complex128, n)
	for i := range a {
		a[i] = 1
	}
	p0 := 0.0
	for i := range a {
		p0 += cabs(a[i]) * cabs(a[i])
	}
	z := 0.3
	if err := Propagate1D(a, dx, z, wl, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	want := 2 * math.Pi / wl * z
	got := math.Remainder(math.Atan2(imag(a[n/2]), real(a[n/2]))-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("plane wave phase wrong: got %g want %g", got, want)
	}
	p1 := 0.0
	for i := range a {
		p1 += cabs(a[i]) * cabs(a[i])
	}
	if math.Abs(p1-p0)/p0 > 1e-9 {
		t.Fatalf("power not conserved: %g vs %g", p1, p0)
	}
}

func TestPropagate1DGaussian(t *testing.T) {
	n, width, wl := 512, 0.01, 632.8e-9
	w0 := 5e-4
	dx := width / float64(n)
	a := make([]complex128, n)
	for i := 0; i < n; i++ {
		x := (float64(i) - float64(n)/2) * dx
		a[i] = complex(math.Exp(-x*x/(w0*w0)), 0)
	}
	rms := func(a []complex128) float64 {
		var p, pr float64
		for i := 0; i < n; i++ {
			x := (float64(i) - float64(n)/2) * dx
			w := cabs(a[i]) * cabs(a[i])
			p += w
			pr += w * x * x
		}
		return math.Sqrt(pr / p)
	}
	sig0 := rms(a)
	zR := math.Pi * w0 * w0 / wl
	z := 0.4
	if err := Propagate1D(a, dx, z, wl, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	sigz := rms(a)
	got := (sigz / sig0) * (sigz / sig0)
	want := 1 + (z/zR)*(z/zR)
	if math.Abs(got-want)/want > 0.02 {
		t.Fatalf("1D waist evolution wrong: ratio %g want %g", got, want)
	}
}
