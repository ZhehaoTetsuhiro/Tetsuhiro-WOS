package optics

import (
	"math"
	"testing"
)

func TestPropagate3DVolume(t *testing.T) {
	n, width, wl := 512, 0.01, 632.8e-9
	w0 := 3e-4
	f, _ := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": w0}}, n, width, false, wl)
	p0 := f.Power()
	zs := []float64{0.05, 0.1, 0.2, 0.3}
	vol, err := Propagate3D(f, zs, MethodASM, ctxFor(wl))
	if err != nil {
		t.Fatal(err)
	}
	if len(vol) != len(zs) {
		t.Fatalf("volume length %d, want %d", len(vol), len(zs))
	}
	zR := math.Pi * w0 * w0 / wl
	for k, z := range zs {
		got := waistRadius(vol[k])
		want := w0 * math.Sqrt(1+(z/zR)*(z/zR))
		if math.Abs(got-want)/want > 0.01 {
			t.Fatalf("plane %d (z=%g): waist %g want %g", k, z, got, want)
		}
	}
	if rel := math.Abs(f.Power()-p0) / p0; rel > 1e-12 {
		t.Fatalf("input field modified: rel %g", rel)
	}
}

// waistRadius estimates the 1/e^2 intensity radius (m) of a 2-D beam.
func waistRadius(f *Field) float64 {
	var p, pr float64
	for j := 0; j < f.N; j++ {
		y := f.Y(j)
		for i := 0; i < f.N; i++ {
			w := f.Intensity(j*f.N + i)
			p += w
			pr += w * (f.X(i)*f.X(i) + y*y)
		}
	}
	return math.Sqrt(2 * pr / p)
}
