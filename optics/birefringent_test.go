package optics

import (
	"math"
	"testing"
)

// An on-axis 45-degree-polarized plane wave in a uniaxial crystal (optic axis
// along x) accumulates different phases on Ex (extraordinary, n_e) and Ey
// (ordinary, n_o): the classic birefringent phase split.
func TestPropagateUniaxialPhase(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	no, ne := 1.5, 1.7
	f, _ := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{"polarization": "d"}}, n, width, true, wl)
	p0 := f.Power()
	z := 1e-7
	if err := PropagateUniaxial(f, z, no, ne, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	k0 := 2 * math.Pi / wl
	c := (n/2)*n + n/2
	ex, ey := f.Ex[c], f.Ey[c]
	wantX := k0 * ne * z
	wantY := k0 * no * z
	gotX := math.Remainder(math.Atan2(imag(ex), real(ex))-wantX, 2*math.Pi)
	gotY := math.Remainder(math.Atan2(imag(ey), real(ey))-wantY, 2*math.Pi)
	if math.Abs(gotX) > 1e-6 {
		t.Fatalf("Ex (extraordinary) phase: got remainder %g, want phase %g", gotX, wantX)
	}
	if math.Abs(gotY) > 1e-6 {
		t.Fatalf("Ey (ordinary) phase: got remainder %g, want phase %g", gotY, wantY)
	}
	if rel := math.Abs(f.Power()-p0) / p0; rel > 1e-9 {
		t.Fatalf("power not conserved: rel %g", rel)
	}
}

// A tilted x-polarized plane wave is the extraordinary eigenmode: its
// propagation phase uses the anisotropic dispersion kz_e = sqrt(k0^2 n_e^2 -
// (n_e/n_o)^2 kx^2), which differs from the ordinary kz_o.
func TestPropagateUniaxialTilted(t *testing.T) {
	n, wl := 256, 632.8e-9
	dx := 1e-7
	no, ne := 1.5, 1.7
	q := 50
	fx0 := float64(q) / (float64(n) * dx)
	kx0 := 2 * math.Pi * fx0
	k0 := 2 * math.Pi / wl
	argE := k0*k0*ne*ne - (ne*ne/(no*no))*kx0*kx0
	if argE <= 0 {
		t.Fatalf("test setup: extraordinary wave evanescent")
	}
	kzE := math.Sqrt(argE)
	f := NewField(n, dx, false)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			f.Ex[j*n+i] = cexpI(kx0 * f.X(i))
		}
	}
	z := 1e-7
	if err := PropagateUniaxial(f, z, no, ne, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	// At the center pixel (x=0) the tilt phase vanishes, leaving kz_e * z.
	c := (n/2)*n + n/2
	ex := f.Ex[c]
	want := kzE * z
	got := math.Remainder(math.Atan2(imag(ex), real(ex))-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("tilted extraordinary phase: got %g want %g", got, want)
	}
}
