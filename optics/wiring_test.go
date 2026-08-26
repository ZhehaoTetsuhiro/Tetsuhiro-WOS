package optics

import (
	"math"
	"testing"
)

// The new media elements must be registered and reachable through the full
// Simulate path (validate -> runTrain -> propagation -> sensor).
func TestNewElementsRegistered(t *testing.T) {
	for _, name := range []string{"uniaxial", "medium", "biaxial"} {
		found := false
		for _, r := range RegisteredElements() {
			if r == name {
				found = true
			}
		}
		if !found {
			t.Fatalf("element %q not registered", name)
		}
	}
}

// A uniaxial element run through Simulate must produce the birefringent phase
// split on the recorded plane.
func TestSimulateUniaxialElement(t *testing.T) {
	b := true
	cfg := Config{
		Grid:       GridSpec{Size: 128, Width: 0.01},
		Wavelength: 632.8e-9,
		Polarized:  &b,
		Method:     "asm",
		Evanescent: "decay",
		Source:     SourceSpec{Type: "plane", Params: map[string]any{"polarization": "d"}},
		Elements: []ElementSpec{
			{Type: "uniaxial", Params: map[string]any{"distance": 1e-7, "n_o": 1.5, "n_e": 1.7}},
			{Type: "sensor", Params: map[string]any{"label": "out"}},
		},
	}
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("expected 1 plane, got %d", len(res.Planes))
	}
	pl := res.Planes[0]
	k0 := 2 * math.Pi / cfg.Wavelength
	c := (pl.Size/2)*pl.Size + pl.Size/2
	ex, ey := pl.Ex[c], pl.Ey[c]
	gx := math.Remainder(math.Atan2(imag(ex), real(ex))-k0*1.7*1e-7, 2*math.Pi)
	gy := math.Remainder(math.Atan2(imag(ey), real(ey))-k0*1.5*1e-7, 2*math.Pi)
	if math.Abs(gx) > 1e-6 || math.Abs(gy) > 1e-6 {
		t.Fatalf("phase split wrong: Ex rem %g, Ey rem %g", gx, gy)
	}
}

// A vectorial propagate method must populate the recorded plane's Ez.
func TestSimulateVectorialEz(t *testing.T) {
	b := false
	cfg := Config{
		Grid:       GridSpec{Size: 128, Width: 0.01},
		Wavelength: 632.8e-9,
		Polarized:  &b,
		Method:     "asm",
		Evanescent: "decay",
		Source:     SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3, "tilt_x": 0.02}},
		Elements: []ElementSpec{
			{Type: "propagate", Params: map[string]any{"distance": 0.05, "method": "vectorial"}},
			{Type: "sensor", Params: map[string]any{"label": "out"}},
		},
	}
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatal(err)
	}
	pl := res.Planes[0]
	if pl.Ez == nil || len(pl.Ez) != pl.Size*pl.Size {
		t.Fatalf("Ez not populated (len %d)", len(pl.Ez))
	}
	var mx float64
	for i := range pl.Ez {
		v := real(pl.Ez[i])*real(pl.Ez[i]) + imag(pl.Ez[i])*imag(pl.Ez[i])
		if v > mx {
			mx = v
		}
	}
	if mx <= 0 {
		t.Fatal("Ez is all zero for a tilted beam")
	}
}
