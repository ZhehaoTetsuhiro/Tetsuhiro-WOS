package optics

import "testing"

// A concave mirror of radius R is a converging reflector equivalent to a lens
// of focal length f = R/2; a convex mirror is equivalent to f = -R/2.
func TestSphericalMirrors(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	R := 0.4
	ctx := ctxFor(wl)
	src := func() *Field {
		f, _ := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 3e-3}}, n, width, false, wl)
		return f
	}

	conc, err := NewElement(ElementSpec{Type: "concave_mirror", Params: map[string]any{"radius": R}})
	if err != nil {
		t.Fatal(err)
	}
	lensConv, err := NewElement(ElementSpec{Type: "lens", Params: map[string]any{"f": R / 2}})
	if err != nil {
		t.Fatal(err)
	}
	a := src()
	if err := conc.Apply(a, ctx); err != nil {
		t.Fatal(err)
	}
	b := src()
	if err := lensConv.Apply(b, ctx); err != nil {
		t.Fatal(err)
	}
	if r := relL2(a, b); r > 1e-12 {
		t.Fatalf("concave mirror (R=%g) != lens f=R/2: rel %g", R, r)
	}

	convx, err := NewElement(ElementSpec{Type: "convex_mirror", Params: map[string]any{"radius": R}})
	if err != nil {
		t.Fatal(err)
	}
	lensDiv, err := NewElement(ElementSpec{Type: "lens", Params: map[string]any{"f": -R / 2}})
	if err != nil {
		t.Fatal(err)
	}
	c := src()
	if err := convx.Apply(c, ctx); err != nil {
		t.Fatal(err)
	}
	d := src()
	if err := lensDiv.Apply(d, ctx); err != nil {
		t.Fatal(err)
	}
	if r := relL2(c, d); r > 1e-12 {
		t.Fatalf("convex mirror (R=%g) != lens f=-R/2: rel %g", R, r)
	}
}
