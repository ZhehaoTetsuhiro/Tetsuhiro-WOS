package optics

import (
	"math"
	"testing"
)

// TestWavelengthRGBBands checks that a wavelength is rendered in the colour it
// looks like: the standard laser lines must land in their own hue.
func TestWavelengthRGBBands(t *testing.T) {
	cases := []struct {
		nm   float64
		want string
	}{
		{632.8, "r"}, {650, "r"}, {600, "r"},
		{532, "g"}, {520, "g"}, {510, "g"},
		{450, "b"}, {470, "b"},
	}
	for _, c := range cases {
		r, g, b, visible := WavelengthRGB(c.nm * 1e-9)
		if !visible {
			t.Errorf("%g nm should be visible", c.nm)
		}
		dom := "r"
		if g > r && g >= b {
			dom = "g"
		} else if b > r && b > g {
			dom = "b"
		}
		if dom != c.want {
			t.Errorf("%g nm renders as %s (rgb %.3f/%.3f/%.3f), want %s", c.nm, dom, r, g, b, c.want)
		}
		for _, v := range []float64{r, g, b} {
			if v < 0 || v > 1 || math.IsNaN(v) {
				t.Errorf("%g nm: component %g outside [0,1]", c.nm, v)
			}
		}
	}
}

// TestWavelengthRGBDeepRedTail guards the deep-red end: beyond ~700 nm the CIE
// fit's residual noise used to normalise to pure green, which is the opposite of
// what a 740 nm source looks like.
func TestWavelengthRGBDeepRedTail(t *testing.T) {
	for _, nm := range []float64{700, 710, 720, 730, 740, 760, 780, 900, 1550} {
		r, g, b, _ := WavelengthRGB(nm * 1e-9)
		if !(r >= g && r >= b) || r <= 0 {
			t.Errorf("%g nm renders as (%.3f, %.3f, %.3f): red must dominate the deep-red tail", nm, r, g, b)
		}
		if g > 0.25 {
			t.Errorf("%g nm keeps %.3f of green: the deep-red tail must fade to red", nm, g)
		}
	}
	if _, _, _, vis := WavelengthRGB(632.8e-9); !vis {
		t.Error("632.8 nm must report visible")
	}
	if _, _, _, vis := WavelengthRGB(1550e-9); vis {
		t.Error("1550 nm must report invisible")
	}
	if _, _, _, vis := WavelengthRGB(300e-9); vis {
		t.Error("300 nm must report invisible")
	}
}

// TestColorWheelCyclic checks the phase ramp: it must be finite, in range and
// close on itself after a full turn.
func TestColorWheelCyclic(t *testing.T) {
	r0, g0, b0 := ColorWheelRGB(0)
	r1, g1, b1 := ColorWheelRGB(2 * math.Pi)
	if math.Abs(r0-r1) > 1e-9 || math.Abs(g0-g1) > 1e-9 || math.Abs(b0-b1) > 1e-9 {
		t.Errorf("colour wheel does not close: %g/%g/%g vs %g/%g/%g", r0, g0, b0, r1, g1, b1)
	}
	for _, ph := range []float64{-2 * math.Pi, -1, 0, 1, math.Pi, 3 * math.Pi} {
		r, g, b := ColorWheelRGB(ph)
		for _, v := range []float64{r, g, b} {
			if math.IsNaN(v) || v < 0 || v > 1 {
				t.Errorf("phase %g → invalid component %g", ph, v)
			}
		}
	}
}
