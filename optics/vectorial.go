package optics

import "math"

// PropagateVectorial advances the field using the vector angular spectrum
// method (non-paraxial). The longitudinal component Ez is reconstructed from
// the transverse spectrum via the divergence-free condition
// kx Ex + ky Ey + kz Ez = 0, i.e. Ez = -(kx Ex + ky Ey)/kz, and all three
// components are propagated with the scalar transfer function exp(i kz z).
// The result is written back into f.Ex/f.Ey/f.Ez (Ez is allocated if needed
// and f.Vectorial is set). For evanescent components (rho>1) kz is imaginary
// and the same Context evanescent policy (decay/zero/Tikhonov) is applied to
// every component.
func PropagateVectorial(f *Field, z float64, ctx *Context) error {
	n := f.N
	if ctx == nil {
		ctx = &Context{Evanescent: "decay"}
	}
	wl := ctx.Wavelength
	k := 2 * math.Pi / wl
	az := math.Abs(z)
	backward := z < 0
	zero, reg, alpha, limit := ctx.evanescentPolicy(z)

	if f.Ey == nil || len(f.Ey) != n*n {
		f.Ey = make([]complex128, n*n)
	}
	if f.Ez == nil || len(f.Ez) != n*n {
		f.Ez = make([]complex128, n*n)
	}
	f.Vectorial = true

	fft2D(f.Ex, n, false)
	fft2D(f.Ey, n, false)
	ez := make([]complex128, n*n)

	for j := 0; j < n; j++ {
		fy := f.freq(j)
		ky := 2 * math.Pi * fy
		for i := 0; i < n; i++ {
			fx := f.freq(i)
			kx := 2 * math.Pi * fx
			rho := wl * math.Hypot(fx, fy)
			idx := j*n + i
			var H complex128
			if rho <= 1 {
				kz := k * math.Sqrt((1-rho)*(1+rho))
				H = cexpI(kz * z)
				if kz > 0 {
					ez[idx] = -(complex(kx, 0)*f.Ex[idx] + complex(ky, 0)*f.Ey[idx]) / complex(kz, 0)
				}
			} else {
				decay := k * az * math.Sqrt((rho-1)*(rho+1))
				switch {
				case zero || (limit > 0 && decay > limit):
					H = 0
				case backward && reg:
					a := math.Exp(decay)
					H = complex(a/(1+alpha*alpha*a*a), 0)
				case backward:
					H = complex(math.Exp(decay), 0)
				default:
					H = complex(math.Exp(-decay), 0)
				}
				kzi := k * math.Sqrt((rho-1)*(rho+1))
				if kzi > 0 {
					ez[idx] = -(complex(kx, 0)*f.Ex[idx] + complex(ky, 0)*f.Ey[idx]) / complex(0, kzi)
				}
			}
			f.Ex[idx] *= H
			f.Ey[idx] *= H
			ez[idx] *= H
		}
	}
	fft2D(f.Ex, n, true)
	fft2D(f.Ey, n, true)
	fft2D(ez, n, true)
	copy(f.Ez, ez)
	return nil
}
