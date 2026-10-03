package optics

import (
	"math"
	"testing"
)

// speckleScene runs a source through a diffuser to a detector and returns the
// intensity at the detector. shiftX moves the diffuser sideways.
func speckleScene(t *testing.T, shiftX float64) []float64 {
	t.Helper()
	src := planeSrc(v3(0, 0, -0.02), v3(0, 0, 1))
	d := ComponentSpec{Type: "diffuser", Label: "漫射体", Pos: v3(shiftX, 0, 0),
		Shape:  circleShape(3e-3),
		Params: map[string]any{"sigma": math.Pi, "correlation": 2e-5, "seed": 7}}
	res, err := Simulate(sceneCfg(256, 8e-3, []SourceSpec{src}, d, bigDet("屏", v3(0, 0, 0.1), math.Pi, 8e-3)))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("want the detector plane, got %v", planeLabels(res))
	}
	return intensityVector(res.Planes[0])
}

func intensityVector(pl *Plane) []float64 {
	out := make([]float64, len(pl.Ex))
	for i := range pl.Ex {
		v := norm2c(pl.Ex[i])
		if pl.Ey != nil {
			v += norm2c(pl.Ey[i])
		}
		if pl.Ez != nil {
			v += norm2c(pl.Ez[i])
		}
		out[i] = v
	}
	return out
}

func pearson(a, b []float64) float64 {
	if len(a) != len(b) || len(a) == 0 {
		return 0
	}
	var ma, mb float64
	for i := range a {
		ma += a[i]
		mb += b[i]
	}
	ma /= float64(len(a))
	mb /= float64(len(b))
	var sab, sa, sb float64
	for i := range a {
		da, db := a[i]-ma, b[i]-mb
		sab += da * db
		sa += da * da
		sb += db * db
	}
	if sa == 0 || sb == 0 {
		return 0
	}
	return sab / math.Sqrt(sa*sb)
}

// TestSceneDiffuserAnchoredToPosition is the §9.4 regression: the random phase
// screen is a physical object, so translating the element must slide it under
// the beam and change the speckle. When the screen was keyed to the array
// index, moving the diffuser by a correlation length changed nothing (r≈0.996).
func TestSceneDiffuserAnchoredToPosition(t *testing.T) {
	a := speckleScene(t, 0)
	b := speckleScene(t, 1e-4) // 100 µm = one correlation length
	c := speckleScene(t, 0)    // same position, same seed
	rShift, rRepeat := pearson(a, b), pearson(a, c)
	t.Logf("speckle correlation: shift 100µm r=%g, identical r=%g", rShift, rRepeat)
	if rShift > 0.5 {
		t.Errorf("translating the diffuser by 100 µm left the speckle nearly unchanged (r=%g); the screen is still not anchored to the element", rShift)
	}
	if rRepeat < 0.999 {
		t.Errorf("the diffuser is no longer deterministic for a fixed position and seed (r=%g)", rRepeat)
	}
}

// TestDiffuserStandaloneDeterministic checks the element used directly (no
// scene, no position) still reproduces for a seed and differs across seeds.
func TestDiffuserStandaloneDeterministic(t *testing.T) {
	run := func(seed float64) *Field {
		f := NewField(64, 5e-5, false)
		for i := range f.Ex {
			f.Ex[i] = 1
		}
		el, err := NewElement(ElementSpec{Type: "diffuser", Params: map[string]any{"seed": seed, "correlation": 2e-5}})
		if err != nil {
			t.Fatalf("NewElement: %v", err)
		}
		if err := el.Apply(f, &Context{Wavelength: 633e-9}); err != nil {
			t.Fatalf("Apply: %v", err)
		}
		return f
	}
	a, b, c := run(3), run(3), run(4)
	corr := func(x, y *Field) float64 {
		var s complex128
		var sx, sy float64
		for i := range x.Ex {
			s += conjc(x.Ex[i]) * y.Ex[i]
			sx += norm2c(x.Ex[i])
			sy += norm2c(y.Ex[i])
		}
		return real(s) / math.Sqrt(sx*sy)
	}
	if r := corr(a, b); r < 0.999999 {
		t.Errorf("same seed must reproduce the screen exactly, r=%g", r)
	}
	if r := corr(a, c); math.Abs(r) > 0.5 {
		t.Errorf("different seeds must give independent screens, r=%g", r)
	}
}

func conjc(z complex128) complex128 { return complex(real(z), -imag(z)) }
