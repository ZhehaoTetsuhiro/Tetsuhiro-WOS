package optics

import (
	"fmt"
	"math"
)

// Method selects the free-space propagation algorithm.
//
//	asm         Angular Spectrum Method: exact solution of the Helmholtz
//	            equation in a homogeneous medium for the sampled field.
//	            Default; no paraxial approximation, no z-range restriction
//	            (only the Nyquist bandlimit of the discrete grid applies).
//	asm_pad     Zero-padded (2x) angular spectrum method. The field is padded
//	            to a 2N x 2N grid so the FFT convolution is linear over a
//	            doubled real-space window; this removes wrap-around aliasing
//	            for beams that walk off or expand beyond the N x N window.
//	            Costs ~4x memory/time. The output is cropped back to N x N.
//	asm_shift   Off-axis (frequency-shifted) angular spectrum method. The
//	            spectral centroid (carrier frequency) of a tilted beam is
//	            removed before propagation and restored afterwards, centering
//	            the spectrum so energy near the band edge does not alias.
//	            Best for strongly tilted illumination; exact for tilt within
//	            the Nyquist limit.
//	asm_shift_pad  Off-axis shift combined with 2x zero padding. Removes the
//	            carrier, propagates with the shifted transfer function on a
//	            2N x 2N linear-convolution grid, and restores the carrier.
//	            Exact for strongly tilted beams that also walk off / diverge
//	            beyond the N x N window. Costs ~4x memory/time like asm_pad.
//	fresnel_tf  Fresnel diffraction, transfer-function form (paraxial).
//	            Valid when |z| <= N*dx^2/lambda; aliases beyond that.
//	fresnel_ir  Fresnel diffraction, impulse-response form (paraxial).
//	            Valid when |z| >= N*dx^2/lambda.
//	fraunhofer  Far-field (Fraunhofer) diffraction. The output pixel size
//	            becomes lambda*|z|/(N*dx); valid when the Fresnel number
//	            D^2/(lambda*|z|) << 1.
//	auto        Choose the exact method (asm).
type Method string

const (
	MethodAuto        Method = "auto"
	MethodASM         Method = "asm"
	MethodASMPad      Method = "asm_pad"
	MethodASMShift    Method = "asm_shift"
	MethodASMShiftPad Method = "asm_shift_pad"
	MethodFresnelTF   Method = "fresnel_tf"
	MethodFresnelIR   Method = "fresnel_ir"
	MethodFraunhofer  Method = "fraunhofer"
	MethodVectorial   Method = "vectorial"
)

// ParseMethod maps a method name to a Method; unknown names error.
func ParseMethod(s string) (Method, error) {
	switch Method(s) {
	case MethodAuto, MethodASM, MethodASMPad, MethodASMShift, MethodASMShiftPad, MethodFresnelTF, MethodFresnelIR, MethodFraunhofer, MethodVectorial:
		return Method(s), nil
	case "":
		return MethodAuto, nil
	}
	return "", fmt.Errorf("unknown propagation method %q", s)
}

// Propagate advances the field by distance z (m, may be negative after a
// mirror) using the selected method.
func Propagate(f *Field, z float64, method Method, ctx *Context) error {
	if z == 0 {
		return nil
	}
	if method == MethodAuto || method == "" {
		method = MethodASM
	}
	if ctx == nil {
		ctx = &Context{Evanescent: "decay"}
	}
	switch method {
	case MethodASM:
		propASM(f, z, ctx)
	case MethodASMPad:
		propASMPad(f, z, ctx)
	case MethodASMShift:
		propASMShift(f, z, ctx)
	case MethodASMShiftPad:
		propASMShiftPad(f, z, ctx)
	case MethodFresnelTF:
		propFresnelTF(f, z, ctx)
	case MethodFresnelIR:
		propFresnelIR(f, z, ctx)
	case MethodFraunhofer:
		propFraunhofer(f, z, ctx)
	case MethodVectorial:
		if err := PropagateVectorial(f, z, ctx); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown propagation method %q", method)
	}
	// Note: the bandlimit (Nyquist regularization) is applied by the simulator
	// trainer after each element, not here, so a propagate element is never
	// filtered twice. Low-level Propagate callers may call ApplyBandlimit
	// explicitly when they want it.
	return nil
}

// rmsRadius estimates the radius of the field support (m) for validity checks.
func (f *Field) rmsRadius() float64 {
	n := f.N
	var p, px, py float64
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			w := f.Intensity(j*n + i)
			p += w
			px += w * f.X(i)
			py += w * y
		}
	}
	if p <= 0 {
		return 0
	}
	cx, cy := px/p, py/p
	var vr float64
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			w := f.Intensity(j*n + i)
			vr += w * ((f.X(i)-cx)*(f.X(i)-cx) + (y-cy)*(y-cy))
		}
	}
	return 2 * math.Sqrt(vr/p)
}

func (ctx *Context) transfer(f *Field, z float64, h func(fx, fy float64) complex128) {
	n := f.N
	apply := func(a []complex128) {
		fft2D(a, n, false)
		for j := 0; j < n; j++ {
			fy := f.freq(j)
			for i := 0; i < n; i++ {
				a[j*n+i] *= h(f.freq(i), fy)
			}
		}
		fft2D(a, n, true)
	}
	apply(f.Ex)
	if f.Polarized {
		apply(f.Ey)
	}
}

// asmTF returns the ASM transfer function H(fx,fy).
//
//	propagating (rho<=1): exp(i k z sqrt(1-rho^2)), evaluated with the
//	    factored form sqrt((1-rho)(1+rho)) to avoid cancellation near rho=1.
//	evanescent  (rho>1):  forward (z>0) exp(-k|z| sqrt(rho^2-1)) physical
//	    decay, unless zeroEv or the decay exceeds limit (nepers), in which
//	    case the component is truncated to 0. Backward (z<0): hard zero, or
//	    the Tikhonov-damped inverse A/(1+(alpha*A)^2) with A=exp(decay) when
//	    reg is set.
func asmTF(wl, z float64, zeroEv, reg bool, alpha, limit float64) func(fx, fy float64) complex128 {
	k := 2 * math.Pi / wl
	az := math.Abs(z)
	backward := z < 0
	return func(fx, fy float64) complex128 {
		rho := math.Hypot(wl*fx, wl*fy)
		if rho <= 1 {
			return cexpI(k * z * math.Sqrt((1-rho)*(1+rho)))
		}
		decay := k * az * math.Sqrt((rho-1)*(rho+1))
		if zeroEv || (limit > 0 && decay > limit) {
			return 0
		}
		if backward {
			if reg {
				a := math.Exp(decay)
				return complex(a/(1+alpha*alpha*a*a), 0)
			}
			return complex(math.Exp(decay), 0)
		}
		return complex(math.Exp(-decay), 0)
	}
}

// evanescentPolicy resolves the evanescent handling for one propagation step:
// zero (hard zero), reg (Tikhonov-damped backward inverse), alpha, and the
// truncation limit (nepers, 0 = off).
func (ctx *Context) evanescentPolicy(z float64) (zero, reg bool, alpha, limit float64) {
	limit = ctx.EvanescentLimit
	if z < 0 {
		if ctx.BackwardRegularize {
			alpha = ctx.TikhonovAlpha
			if alpha <= 0 {
				alpha = 1e-3
			}
			return false, true, alpha, limit
		}
		return true, false, 0, limit
	}
	return ctx.Evanescent == "zero", false, 0, limit
}

// asmWarn records the standard ASM diagnostics (evanescent loss / backward
// evanescent zeroing) after a propagation step.
func asmWarn(f *Field, z float64, ctx *Context, pIn, pOut float64, zeroEv, reg bool) {
	if pIn > 0 && pOut < pIn*(1-1e-8) && z > 0 {
		ctx.Warnings.Add("evanescent_filtered",
			fmt.Sprintf("角谱传播中滤除了衰逝波分量（功率损失 %.2e%%）", (pIn-pOut)/pIn*100),
			(pIn-pOut)/pIn)
	}
	if z < 0 {
		if reg {
			ctx.Warnings.Add("backward_regularized",
				"反向传播对衰逝波施加 Tikhonov 正则化（不稳定的放大分量被阻尼）", 0)
		} else if zeroEv {
			ctx.Warnings.Add("backward_evanescent",
				"反向传播（反射后）时衰逝波被置零（物理上为不稳定的放大分量）", 0)
		}
	}
}

// propASM: U(z) = F^-1{ F{U} * exp(i k z sqrt(1-(lambda fx)^2-(lambda fy)^2)) }.
// Evanescent components (lambda*f > 1) decay as exp(-k|z| sqrt((lambda f)^2-1))
// forward, and are zeroed on backward propagation (they would amplify).
func propASM(f *Field, z float64, ctx *Context) {
	zero, reg, alpha, limit := ctx.evanescentPolicy(z)
	pIn := f.Power()
	ctx.transfer(f, z, asmTF(ctx.Wavelength, z, zero, reg, alpha, limit))
	pOut := f.Power()
	asmWarn(f, z, ctx, pIn, pOut, zero, reg)
}

// propASMPad is the zero-padded (linear-convolution) angular spectrum method.
// The field is placed in the central N x N block of a 2N x 2N buffer (same
// pixel size dx), propagated, and cropped back. Doubling the real-space window
// removes wrap-around aliasing for fields that spread beyond the original
// N x N window during propagation (tilted/diverging beams, long distances).
func propASMPad(f *Field, z float64, ctx *Context) {
	propASMPadCore(f, z, ctx, 0, 0)
}

// propASMPadCore is the zero-padded ASM with an optional carrier offset
// (fxOff, fyOff) added to the transfer function's frequency argument. It is
// shared by propASMPad (offset 0) and propASMShiftPad (offset = carrier).
func propASMPadCore(f *Field, z float64, ctx *Context, fxOff, fyOff float64) {
	n := f.N
	m := 2 * n
	dx := f.DX
	zero, reg, alpha, limit := ctx.evanescentPolicy(z)
	h := asmTF(ctx.Wavelength, z, zero, reg, alpha, limit)
	freqM := func(i int) float64 {
		if i <= m/2 {
			return float64(i) / (float64(m) * dx)
		}
		return float64(i-m) / (float64(m) * dx)
	}
	pIn := f.Power()
	buf := make([]complex128, m*m)
	off := n / 2
	apply := func(a []complex128) {
		for i := range buf {
			buf[i] = 0
		}
		for j := 0; j < n; j++ {
			copy(buf[(j+off)*m+off:(j+off)*m+off+n], a[j*n:(j+1)*n])
		}
		fft2D(buf, m, false)
		for j := 0; j < m; j++ {
			fy := freqM(j) + fyOff
			for i := 0; i < m; i++ {
				buf[j*m+i] *= h(freqM(i)+fxOff, fy)
			}
		}
		fft2D(buf, m, true)
		for j := 0; j < n; j++ {
			copy(a[j*n:(j+1)*n], buf[(j+off)*m+off:(j+off)*m+off+n])
		}
	}
	apply(f.Ex)
	if f.Polarized {
		apply(f.Ey)
	}
	pOut := f.Power()
	asmWarn(f, z, ctx, pIn, pOut, zero, reg)
}

// spectralCentroid returns the power-spectrum centroid (carrier spatial
// frequency, 1/m) of the field, weighting both Jones components when present.
func spectralCentroid(f *Field) (fx, fy float64) {
	n := f.N
	var sx, sy, sw float64
	comp := func(c []complex128) {
		a := append([]complex128(nil), c...)
		fft2D(a, n, false)
		for j := 0; j < n; j++ {
			fy := f.freq(j)
			for i := 0; i < n; i++ {
				fx := f.freq(i)
				w := real(a[j*n+i])*real(a[j*n+i]) + imag(a[j*n+i])*imag(a[j*n+i])
				sw += w
				sx += w * fx
				sy += w * fy
			}
		}
	}
	comp(f.Ex)
	if f.Polarized {
		comp(f.Ey)
	}
	if sw <= 0 {
		return 0, 0
	}
	return sx / sw, sy / sw
}

// propASMShift is the off-axis (frequency-shifted) angular spectrum method.
// A tilted beam concentrates its spectrum away from DC; energy near the band
// edge wraps around and aliases. We remove the carrier (the spectral centroid)
// with a linear phase ramp, then propagate with the transfer function
// evaluated at the shifted frequency H(f+fc), and finally restore the carrier.
// This keeps the spectrum centered where H varies slowly, and is exact for a
// carrier within the Nyquist limit. The shift is a pure unitary phase, so
// power is unaffected.
func propASMShift(f *Field, z float64, ctx *Context) {
	fxC, fyC := spectralCentroid(f)
	fbin := 1 / (float64(f.N) * f.DX)
	if math.Abs(fxC) < 0.5*fbin && math.Abs(fyC) < 0.5*fbin {
		propASM(f, z, ctx)
		return
	}
	wl := ctx.Wavelength
	zero, reg, alpha, limit := ctx.evanescentPolicy(z)
	h := asmTF(wl, z, zero, reg, alpha, limit)
	n := f.N
	shift := func(a []complex128, sgn float64) {
		for j := 0; j < n; j++ {
			y := f.Y(j)
			for i := 0; i < n; i++ {
				a[j*n+i] *= cexpI(sgn * 2 * math.Pi * (fxC*f.X(i) + fyC*y))
			}
		}
	}
	pIn := f.Power()
	shift(f.Ex, -1)
	if f.Polarized {
		shift(f.Ey, -1)
	}
	apply := func(a []complex128) {
		fft2D(a, n, false)
		for j := 0; j < n; j++ {
			fy := f.freq(j) + fyC
			for i := 0; i < n; i++ {
				a[j*n+i] *= h(f.freq(i)+fxC, fy)
			}
		}
		fft2D(a, n, true)
	}
	apply(f.Ex)
	if f.Polarized {
		apply(f.Ey)
	}
	shift(f.Ex, +1)
	if f.Polarized {
		shift(f.Ey, +1)
	}
	pOut := f.Power()
	asmWarn(f, z, ctx, pIn, pOut, zero, reg)
}

// propASMShiftPad combines the off-axis carrier shift with 2x zero padding:
// the spectral centroid is removed, the field propagates on a 2N x 2N
// linear-convolution grid with the transfer function evaluated at the shifted
// frequency H(f+fc), and the carrier is restored. This is exact for strongly
// tilted beams that also walk off or diverge beyond the N x N window — the
// regime where asm_shift and asm_pad are each alone insufficient.
func propASMShiftPad(f *Field, z float64, ctx *Context) {
	fxC, fyC := spectralCentroid(f)
	fbin := 1 / (float64(f.N) * f.DX)
	if math.Abs(fxC) < 0.5*fbin && math.Abs(fyC) < 0.5*fbin {
		propASMPad(f, z, ctx)
		return
	}
	n := f.N
	shift := func(a []complex128, sgn float64) {
		for j := 0; j < n; j++ {
			y := f.Y(j)
			for i := 0; i < n; i++ {
				a[j*n+i] *= cexpI(sgn * 2 * math.Pi * (fxC*f.X(i) + fyC*y))
			}
		}
	}
	shift(f.Ex, -1)
	if f.Polarized {
		shift(f.Ey, -1)
	}
	propASMPadCore(f, z, ctx, fxC, fyC)
	shift(f.Ex, +1)
	if f.Polarized {
		shift(f.Ey, +1)
	}
}

// propFresnelTF: paraxial transfer function H = exp(i k z) exp(-i pi lambda z f^2).
func propFresnelTF(f *Field, z float64, ctx *Context) {
	wl := ctx.Wavelength
	k := 2 * math.Pi / wl
	n := f.N
	h := func(fx, fy float64) complex128 {
		return cexpI(k*z) * cexpI(-math.Pi*wl*z*(fx*fx+fy*fy))
	}
	ctx.transfer(f, z, h)
	if math.Abs(z) > float64(n)*f.DX*f.DX/wl {
		ctx.Warnings.Add("fresnel_tf_alias",
			fmt.Sprintf("Fresnel 传递函数法在 |z|=%g m 超过 N*dx^2/lambda=%g m，存在混叠；请改用角谱法或 Fresnel 冲激响应法", math.Abs(z), float64(n)*f.DX*f.DX/wl), 0)
	}
}

// propFresnelIR: U2 = exp(i k z)/(i lambda z) * h * F^-1{ F{U} * F{h} },
// h = exp(i pi r^2/(lambda z)).
func propFresnelIR(f *Field, z float64, ctx *Context) {
	wl := ctx.Wavelength
	k := 2 * math.Pi / wl
	n := f.N
	// Impulse response on the same grid.
	h := make([]complex128, n*n)
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			x := f.X(i)
			h[j*n+i] = cexpI(math.Pi * (x*x + y*y) / (wl * z))
		}
	}
	fft2D(h, n, false)
	pre := complex(0, -1/(wl*z)) * cexpI(k*z)
	apply := func(a []complex128) {
		fft2D(a, n, false)
		for i := range a {
			a[i] *= h[i]
		}
		fft2D(a, n, true)
		for j := 0; j < n; j++ {
			y := f.Y(j)
			for i := 0; i < n; i++ {
				x := f.X(i)
				a[j*n+i] *= pre * cexpI(math.Pi*(x*x+y*y)/(wl*z))
			}
		}
	}
	apply(f.Ex)
	if f.Polarized {
		apply(f.Ey)
	}
	if math.Abs(z) < float64(n)*f.DX*f.DX/wl {
		ctx.Warnings.Add("fresnel_ir_alias",
			fmt.Sprintf("Fresnel 冲激响应法在 |z|=%g m 小于 N*dx^2/lambda=%g m，存在混叠；请改用角谱法或 Fresnel 传递函数法", math.Abs(z), float64(n)*f.DX*f.DX/wl), 0)
	}
}

// propFraunhofer: far field. The FFT output is reordered so that the new
// grid keeps the same centered layout as the input grid (pixel size changes
// to lambda*|z|/(N*dx)).
func propFraunhofer(f *Field, z float64, ctx *Context) {
	wl := ctx.Wavelength
	k := 2 * math.Pi / wl
	n := f.N
	dxOut := wl * math.Abs(z) / (float64(n) * f.DX)
	shift := make([]complex128, n*n)
	apply := func(a []complex128) {
		fft2D(a, n, false)
		for i := range a {
			a[i] *= complex(f.DX*f.DX, 0)
		}
		// 2D fftshift by N/2 so the DC bin maps to the grid center (N/2, N/2).
		half := n / 2
		for j := 0; j < n; j++ {
			row := j * n
			copy(shift[row:row+half], a[row+half:row+n])
			copy(shift[row+half:row+n], a[row:row+half])
		}
		for i := 0; i < n; i++ {
			for j := 0; j < half; j++ {
				shift[j*n+i], shift[(j+half)*n+i] = shift[(j+half)*n+i], shift[j*n+i]
			}
		}
		// Now multiply the quadratic phase on the centered grid.
		for j := 0; j < n; j++ {
			y := (float64(j) - float64(n)/2) * dxOut
			for i := 0; i < n; i++ {
				x := (float64(i) - float64(n)/2) * dxOut
				shift[j*n+i] *= cexpI(k*z) * complex(0, -1/(wl*z)) * cexpI(k*(x*x+y*y)/(2*z))
			}
		}
		copy(a, shift)
	}
	apply(f.Ex)
	if f.Polarized {
		apply(f.Ey)
	}
	// Fresnel-number validity check (D = field support radius).
	if a := f.rmsRadius(); a > 0 {
		nf := a * a / (wl * math.Abs(z))
		if nf > 0.5 {
			ctx.Warnings.Add("fraunhofer_nearfield",
				fmt.Sprintf("夫琅禾费传播的菲涅耳数 F=%g > 0.5，远场条件不满足，结果可能不准确；请改用角谱法", nf), nf)
		}
	}
	f.DX = dxOut
}

// ---------- 1-D transverse field (x-z cross-section) ----------

// freq1D returns the spatial frequency (1/m) of a 1-D FFT index on a grid of
// N points with pixel size dx.
func freq1D(i, n int, dx float64) float64 {
	if i <= n/2 {
		return float64(i) / (float64(n) * dx)
	}
	return float64(i-n) / (float64(n) * dx)
}

// asmTF1D is the 1-D angular-spectrum transfer function H(fx). It mirrors
// asmTF: propagating components (|lambda fx| <= 1) use the factored sqrt to
// avoid cancellation near the band edge; evanescent components decay forward,
// or are zeroed / Tikhonov-regularized backward.
func asmTF1D(wl, z float64, zeroEv, reg bool, alpha, limit float64) func(fx float64) complex128 {
	k := 2 * math.Pi / wl
	az := math.Abs(z)
	backward := z < 0
	return func(fx float64) complex128 {
		rho := math.Abs(wl * fx)
		if rho <= 1 {
			return cexpI(k * z * math.Sqrt((1-rho)*(1+rho)))
		}
		decay := k * az * math.Sqrt((rho-1)*(rho+1))
		if zeroEv || (limit > 0 && decay > limit) {
			return 0
		}
		if backward {
			if reg {
				a := math.Exp(decay)
				return complex(a/(1+alpha*alpha*a*a), 0)
			}
			return complex(math.Exp(decay), 0)
		}
		return complex(math.Exp(-decay), 0)
	}
}

// sum1DSq returns the summed intensity (no dx factor) of a 1-D field.
func sum1DSq(a []complex128) float64 {
	var p float64
	for i := range a {
		p += real(a[i])*real(a[i]) + imag(a[i])*imag(a[i])
	}
	return p
}

// Propagate1D advances a 1-D transverse field profile (an x-z cross-section,
// useful for cylindrical systems separable in y) by distance z using the
// angular spectrum method. a has length N on a grid of pixel size dx and is
// updated in place. Evanescent handling follows the same Context policy as the
// 2-D Propagate (Evanescent / EvanescentLimit / BackwardRegularize /
// TikhonovAlpha).
func Propagate1D(a []complex128, dx, z, wl float64, ctx *Context) error {
	n := len(a)
	if ctx == nil {
		ctx = &Context{Evanescent: "decay"}
	}
	zero, reg, alpha, limit := ctx.evanescentPolicy(z)
	h := asmTF1D(wl, z, zero, reg, alpha, limit)
	pIn := sum1DSq(a)
	fft1DAny(a, false)
	for i := 0; i < n; i++ {
		a[i] *= h(freq1D(i, n, dx))
	}
	fft1DAny(a, true)
	pOut := sum1DSq(a)
	if pIn > 0 && pOut < pIn*(1-1e-8) && z > 0 && ctx.Warnings != nil {
		ctx.Warnings.Add("evanescent_filtered",
			fmt.Sprintf("角谱传播中滤除了衰逝波分量（功率损失 %.2e%%）", (pIn-pOut)/pIn*100),
			(pIn-pOut)/pIn)
	}
	return nil
}
