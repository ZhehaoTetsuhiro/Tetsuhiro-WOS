package optics

import "math"

// IndexFunc returns the complex refractive index n(x, y, z) at a point. The
// real part is the phase index and the imaginary part is the absorption
// (Im > 0) or gain (Im < 0) coefficient.
type IndexFunc func(x, y, z float64) complex128

// UniformIndex returns an IndexFunc for a constant (possibly complex) index.
func UniformIndex(n complex128) IndexFunc {
	return func(x, y, z float64) complex128 { return n }
}

// PropagateSplitStep advances the field by distance z through an inhomogeneous
// medium with refractive index n(x,y,z), using symmetric split-step (Strang)
// propagation: each of 'steps' slices applies [ASM dz/2, medium phase dz,
// ASM dz/2]. Because n may vary in x, y and z, stratified n(z), gradient
// n(x,y) and absorption/gain media are supported. This is a paraxial BPM
// approximation: the medium contributes the phase k0*(n-1)*dz in real space
// while the ASM kernel keeps the vacuum dispersion sqrt(1-(lambda0 f)^2).
func PropagateSplitStep(f *Field, z float64, n IndexFunc, steps int, ctx *Context) error {
	if z == 0 {
		return nil
	}
	if steps <= 0 {
		steps = 1
	}
	if ctx == nil {
		ctx = &Context{Evanescent: "decay"}
	}
	dz := z / float64(steps)
	k := 2 * math.Pi / ctx.Wavelength
	for s := 0; s < steps; s++ {
		zc := z*float64(s)/float64(steps) + dz/2
		if err := Propagate(f, dz/2, MethodASM, ctx); err != nil {
			return err
		}
		applyMediumPhase(f, n, zc, k, dz)
		if err := Propagate(f, dz/2, MethodASM, ctx); err != nil {
			return err
		}
	}
	return nil
}

// applyMediumPhase multiplies the field by exp(i k0 (n-1) dz) at every pixel
// for the slice centered at zc. Complex n: real part -> phase, imaginary part
// -> absorption (Im>0) or gain (Im<0).
func applyMediumPhase(f *Field, n IndexFunc, zc, k, dz float64) {
	nn := f.N
	for j := 0; j < nn; j++ {
		y := f.Y(j)
		for i := 0; i < nn; i++ {
			nv := n(f.X(i), y, zc)
			ph := k * (real(nv) - 1) * dz
			t := complex(math.Exp(-k*imag(nv)*dz), 0) * cexpI(ph)
			idx := j*nn + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
}
