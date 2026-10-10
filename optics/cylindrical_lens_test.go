package optics

import (
	"math"
	"testing"
)

// A cylindrical lens has power along one in-plane direction only: the phase it
// applies must be completely independent of the perpendicular coordinate, and
// along its own direction it must be the usual thin-lens quadratic
// -k·u²/(2f), u = cos(rotation)·x + sin(rotation)·y. This one-directional phase
// is what lets a grating's grooves stay invariant while its dispersion direction
// is focused (见 example 衍射光栅光谱, where every order is a spectral line).
func TestCylindricalLensPhase(t *testing.T) {
	n, width, wl := 256, 0.008, 632.8e-9
	f0 := 0.3
	ctx := ctxFor(wl)
	k := 2 * math.Pi / wl

	apply := func(params map[string]any) *Field {
		f, err := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{"power": 1e-3}}, n, width, false, wl)
		if err != nil {
			t.Fatal(err)
		}
		el, err := NewElement(ElementSpec{Type: "cylindrical_lens", Params: params})
		if err != nil {
			t.Fatal(err)
		}
		if err := el.Apply(f, ctx); err != nil {
			t.Fatal(err)
		}
		return f
	}
	// got returns the phase the element applied at pixel (i,j).
	got := func(g *Field, i, j int) float64 {
		v := g.Ex[j*n+i]
		return math.Atan2(imag(v), real(v))
	}
	wantPhase := func(u float64) float64 { return math.Mod(-k*u*u/(2*f0), 2*math.Pi) }
	dphi := func(a, b float64) float64 {
		d := math.Mod(a-b, 2*math.Pi)
		if d > math.Pi {
			d -= 2 * math.Pi
		}
		if d < -math.Pi {
			d += 2 * math.Pi
		}
		return math.Abs(d)
	}

	// Power along x: the phase must not depend on y at all …
	gx := apply(map[string]any{"f": f0, "rotation": 0})
	i := n/2 + n/8 // x = +1 mm
	ref := got(gx, i, n/2)
	for j := 0; j < n; j++ {
		if d := dphi(got(gx, i, j), ref); d > 1e-12 {
			t.Fatalf("rotation=0 leaves a y-dependence: phase at row %d differs by %g rad from the axis", j, d)
		}
	}
	// … and along x it is exactly -k·x²/(2f).
	for _, ii := range []int{n / 2, n/2 + n/16, n/2 + n/8, n/2 - n/8} {
		x := gx.X(ii)
		if d := dphi(got(gx, ii, n/2), wantPhase(x)); d > 1e-9 {
			t.Fatalf("rotation=0 phase at x = %g m is off by %g rad (want -k·x²/2f)", x, d)
		}
	}
	// Rotation π/2 swaps the roles: now x is untouched and y is focused.
	gy := apply(map[string]any{"f": f0, "rotation": math.Pi / 2})
	ref = got(gy, n/2, n/2+n/8)
	for i := 0; i < n; i++ {
		if d := dphi(got(gy, i, n/2+n/8), ref); d > 1e-12 {
			t.Fatalf("rotation=π/2 leaves an x-dependence: phase at column %d differs by %g rad from the axis", i, d)
		}
	}
	if d := dphi(got(gy, n/2, n/2+n/8), wantPhase(gy.Y(n/2+n/8))); d > 1e-9 {
		t.Fatalf("rotation=π/2 phase at y = %g m is off by %g rad", gy.Y(n/2+n/8), d)
	}
	// A negative focal length flips the sign of the curvature (diverging).
	gn := apply(map[string]any{"f": -f0, "rotation": 0})
	if d := dphi(got(gn, i, n/2), -wantPhase(gx.X(i))); d > 1e-9 {
		t.Fatalf("f<0 does not mirror the phase: off by %g rad", d)
	}
	// The optional circular aperture blocks outside its radius.
	r := 2e-3
	ga := apply(map[string]any{"f": f0, "rotation": 0, "aperture": r})
	at := func(i, j int) int { return j*n + i }
	for _, p := range [][2]int{{n / 2, n / 2}, {n/2 + 40, n/2 + 40}, {n/2 - 50, n / 2}} {
		if ga.Ex[at(p[0], p[1])] == 0 {
			t.Fatalf("the cylindrical lens blocked light inside its aperture at (%d,%d)", p[0], p[1])
		}
	}
	for _, p := range [][2]int{{n/2 + 80, n / 2}, {n / 2, n/2 + 80}, {n/2 + 60, n/2 + 60}} {
		if ga.Ex[at(p[0], p[1])] != 0 {
			t.Fatalf("the cylindrical lens transmitted light outside its aperture at (%d,%d)", p[0], p[1])
		}
	}
	// Bad arguments are rejected in the constructor.
	if _, err := NewElement(ElementSpec{Type: "cylindrical_lens", Params: map[string]any{"f": 0}}); err == nil {
		t.Fatalf("f = 0 must be rejected")
	}
}
