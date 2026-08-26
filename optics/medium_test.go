package optics

import (
	"math"
	"testing"
)

func TestSplitStepUniformIndex(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	f, _ := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{}}, n, width, false, wl)
	idx := 1.5
	z := 0.1
	p0 := f.Power()
	if err := PropagateSplitStep(f, z, UniformIndex(complex(idx, 0)), 20, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	k0 := 2 * math.Pi / wl
	want := k0 * idx * z
	c := f.Ex[(n/2)*n+n/2]
	got := math.Remainder(math.Atan2(imag(c), real(c))-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("plane wave phase wrong: got %g want %g", got, want)
	}
	if rel := math.Abs(f.Power()-p0) / p0; rel > 1e-9 {
		t.Fatalf("power not conserved: rel %g", rel)
	}
}

func TestSplitStepAbsorption(t *testing.T) {
	n, width, wl := 256, 0.01, 1e-6
	f, _ := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{}}, n, width, false, wl)
	p0 := f.Power()
	nim := 1e-4
	z := 1e-3
	if err := PropagateSplitStep(f, z, UniformIndex(complex(1, nim)), 10, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	k0 := 2 * math.Pi / wl
	want := p0 * math.Exp(-2*k0*nim*z)
	if rel := math.Abs(f.Power()-want) / want; rel > 1e-3 {
		t.Fatalf("absorbed power wrong: rel %g (got %g want %g)", rel, f.Power(), want)
	}
}

func TestSplitStepStratified(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	f, _ := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{}}, n, width, false, wl)
	z := 0.1
	nz := func(x, y, z float64) complex128 {
		if z < 0.05 {
			return complex(1.5, 0)
		}
		return complex(1, 0)
	}
	if err := PropagateSplitStep(f, z, nz, 20, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	// optical path = 1.5*0.05 + 1*0.05 = 0.125
	k0 := 2 * math.Pi / wl
	want := k0 * 0.125
	c := f.Ex[(n/2)*n+n/2]
	got := math.Remainder(math.Atan2(imag(c), real(c))-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("stratified phase wrong: got %g want %g", got, want)
	}
}
