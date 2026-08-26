package optics

import (
	"math"
	"testing"
)

// For a uniaxial permittivity tensor (optic axis along x) the full Berreman
// 4x4 propagation must reproduce the uncoupled PropagateUniaxial result.
func TestPropagateAnisotropicMatchesUniaxial(t *testing.T) {
	n, wl := 256, 632.8e-9
	dx := 1e-7
	no, ne := 1.5, 1.7
	q := 50
	fx0 := float64(q) / (float64(n) * dx)
	kx0 := 2 * math.Pi * fx0
	build := func() *Field {
		f := NewField(n, dx, false)
		for j := 0; j < n; j++ {
			for i := 0; i < n; i++ {
				f.Ex[j*n+i] = cexpI(kx0 * f.X(i))
			}
		}
		return f
	}
	z := 1e-7
	eps := [3][3]complex128{
		{complex(ne*ne, 0), 0, 0},
		{0, complex(no*no, 0), 0},
		{0, 0, complex(no*no, 0)},
	}
	fb := build()
	if err := PropagateAnisotropic(fb, z, eps, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	fu := build()
	if err := PropagateUniaxial(fu, z, no, ne, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	if r := relL2(fb, fu); r > 1e-6 {
		t.Fatalf("anisotropic deviates from uniaxial by %g", r)
	}
}

// An on-axis 45-degree plane wave in a biaxial crystal (principal indices
// nx, ny, nz) accumulates phases k0*nx*z on Ex and k0*ny*z on Ey.
func TestPropagateAnisotropicBiaxial(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	nx, ny, nz := 1.6, 1.5, 1.4
	eps := [3][3]complex128{
		{complex(nx*nx, 0), 0, 0},
		{0, complex(ny*ny, 0), 0},
		{0, 0, complex(nz*nz, 0)},
	}
	f, _ := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{"polarization": "d"}}, n, width, true, wl)
	p0 := f.Power()
	z := 1e-7
	if err := PropagateAnisotropic(f, z, eps, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	k0 := 2 * math.Pi / wl
	c := (n/2)*n + n/2
	ex, ey := f.Ex[c], f.Ey[c]
	gotX := math.Remainder(math.Atan2(imag(ex), real(ex))-k0*nx*z, 2*math.Pi)
	gotY := math.Remainder(math.Atan2(imag(ey), real(ey))-k0*ny*z, 2*math.Pi)
	if math.Abs(gotX) > 1e-6 {
		t.Fatalf("Ex phase: got remainder %g, want phase %g", gotX, k0*nx*z)
	}
	if math.Abs(gotY) > 1e-6 {
		t.Fatalf("Ey phase: got remainder %g, want phase %g", gotY, k0*ny*z)
	}
	if rel := math.Abs(f.Power()-p0) / p0; rel > 1e-9 {
		t.Fatalf("power not conserved: rel %g", rel)
	}
}
