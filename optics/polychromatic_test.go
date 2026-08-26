package optics

import (
	"math"
	"testing"
)

func TestPolychromaticPlaneWave(t *testing.T) {
	n, width := 256, 0.01
	wl := 632.8e-9
	f, _ := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{}}, n, width, false, wl)
	z := 0.05
	samples := []WavelengthSample{{Wavelength: 632.8e-9, Weight: 1}, {Wavelength: 532e-9, Weight: 0.5}}
	out, err := PropagatePolychromatic(f, z, samples, MethodASM, ctxFor(wl))
	if err != nil {
		t.Fatal(err)
	}
	// Plane-wave intensity is uniform = P/(N^2 dx^2); polychromatic = (sum w) * that.
	dx := width / float64(n)
	want := 1.5 * (1e-3 / (float64(n*n) * dx * dx))
	for i := range out {
		if math.Abs(out[i]-want) > 1e-9*want {
			t.Fatalf("polychromatic intensity not uniform at %d: %g want %g", i, out[i], want)
		}
	}
}

func TestPolychromaticSingleMatchesMono(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	f, _ := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3}}, n, width, false, wl)
	z := 0.1
	g := f.Clone()
	if err := Propagate(g, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	out, err := PropagatePolychromatic(f, z, []WavelengthSample{{Wavelength: wl, Weight: 1}}, MethodASM, ctxFor(wl))
	if err != nil {
		t.Fatal(err)
	}
	for i := range out {
		if math.Abs(out[i]-g.Intensity(i)) > 1e-9*g.Intensity(i) {
			t.Fatalf("single-sample mismatch at %d: %g vs %g", i, out[i], g.Intensity(i))
		}
	}
}
