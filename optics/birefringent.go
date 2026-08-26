package optics

import (
	"fmt"
	"math"
)

// PropagateUniaxial advances the field through a homogeneous uniaxial crystal
// of thickness z with optic axis along x. The extraordinary (Ex) and ordinary
// (Ey) eigenmodes propagate with the anisotropic transfer functions
//
//	kz_e = sqrt(k0^2 n_e^2 - (n_e/n_o)^2 kx^2 - ky^2)
//	kz_o = sqrt(k0^2 n_o^2 - kx^2 - ky^2)
//
// where k0 = 2*pi/lambda, kx = 2*pi*fx, ky = 2*pi*fy. This is the exact
// anisotropic dispersion in the x-z plane and the standard uncoupled
// approximation for general ky. z may be negative. Evanescent components
// (arg < 0) decay as exp(-k0 |z| ...) consistent with the scalar ASM.
func PropagateUniaxial(f *Field, z float64, no, ne float64, ctx *Context) error {
	if no <= 0 || ne <= 0 {
		return fmt.Errorf("uniaxial: n_o and n_e must be > 0")
	}
	n := f.N
	if ctx == nil {
		ctx = &Context{}
	}
	wl := ctx.Wavelength
	k0 := 2 * math.Pi / wl
	az := math.Abs(z)
	apply := func(a []complex128, h func(fx, fy float64) complex128) {
		fft2D(a, n, false)
		for j := 0; j < n; j++ {
			fy := f.freq(j)
			for i := 0; i < n; i++ {
				a[j*n+i] *= h(f.freq(i), fy)
			}
		}
		fft2D(a, n, true)
	}
	ho := func(fx, fy float64) complex128 {
		arg := k0*k0*no*no - (2*math.Pi*fx)*(2*math.Pi*fx) - (2*math.Pi*fy)*(2*math.Pi*fy)
		if arg < 0 {
			return complex(math.Exp(-az*math.Sqrt(-arg)), 0)
		}
		return cexpI(z * math.Sqrt(arg))
	}
	he := func(fx, fy float64) complex128 {
		kx := 2 * math.Pi * fx
		ky := 2 * math.Pi * fy
		arg := k0*k0*ne*ne - (ne*ne/(no*no))*kx*kx - ky*ky
		if arg < 0 {
			return complex(math.Exp(-az*math.Sqrt(-arg)), 0)
		}
		return cexpI(z * math.Sqrt(arg))
	}
	apply(f.Ex, he)
	if f.Polarized {
		apply(f.Ey, ho)
	}
	return nil
}
