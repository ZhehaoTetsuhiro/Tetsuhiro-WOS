package optics

import "math"

// PropagateAnisotropic advances the field through a homogeneous anisotropic
// (possibly biaxial) medium described by its relative permittivity tensor eps
// (3x3, complex entries allowed for absorption/gain), using the Berreman 4x4
// matrix method. For every transverse wavevector the field is decomposed into
// the two forward eigenmodes, each propagated with its own complex kz, and
// recombined. This is the exact anisotropic transfer function (no paraxial
// approximation) and reduces to PropagateUniaxial for a uniaxial tensor with
// optic axis along x. z is assumed >= 0 (forward propagation).
func PropagateAnisotropic(f *Field, z float64, eps [3][3]complex128, ctx *Context) error {
	n := f.N
	if ctx == nil {
		ctx = &Context{}
	}
	k0 := 2 * math.Pi / ctx.Wavelength
	fft2D(f.Ex, n, false)
	fft2D(f.Ey, n, false)
	for j := 0; j < n; j++ {
		fy := f.freq(j)
		ky := 2 * math.Pi * fy
		for i := 0; i < n; i++ {
			fx := f.freq(i)
			kx := 2 * math.Pi * fx
			idx := j*n + i
			ex, ey := anisoMode(eps, kx/k0, ky/k0, k0, z, f.Ex[idx], f.Ey[idx])
			f.Ex[idx] = ex
			f.Ey[idx] = ey
		}
	}
	fft2D(f.Ex, n, true)
	fft2D(f.Ey, n, true)
	return nil
}

// anisoMode propagates one spectral component (normalized kx, ky) through the
// anisotropic medium and returns the propagated (Ex, Ey).
func anisoMode(eps [3][3]complex128, xi, eta, k0, z float64, Ex, Ey complex128) (complex128, complex128) {
	D := buildBerreman(eps, xi, eta)
	c0, c1, c2, c3 := charPoly4(D)
	roots := roots4(c0, c1, c2, c3)
	var qs [2]complex128
	var vs [2][4]complex128
	cnt := 0
	used := [4]bool{}
	for i, q := range roots {
		if forwardMode(q) && cnt < 2 {
			qs[cnt] = q
			vs[cnt] = nullVector4(D, q)
			used[i] = true
			cnt++
		}
	}
	if cnt < 2 {
		// Defensive fallback: pick the remaining roots with the largest real
		// part. Roots already selected as forward modes are skipped so the two
		// eigenvectors stay distinct (a duplicate would make the 2x2 solve
		// singular and produce NaN).
		for cnt < 2 {
			best, bi := -1e300, -1
			for i := range roots {
				if !used[i] && real(roots[i]) > best {
					best, bi = real(roots[i]), i
				}
			}
			if bi < 0 {
				break
			}
			qs[cnt] = roots[bi]
			vs[cnt] = nullVector4(D, roots[bi])
			used[bi] = true
			cnt++
		}
	}
	// Solve [v1Ex v2Ex; v1Ey v2Ey] [c1;c2] = [Ex;Ey].
	a, b := vs[0][0], vs[1][0]
	c, d := vs[0][2], vs[1][2]
	det := a*d - b*c
	m1 := (d*Ex - b*Ey) / det
	m2 := (-c*Ex + a*Ey) / det
	pf := func(q complex128) complex128 {
		return cexpI(k0*real(q)*z) * complex(math.Exp(-k0*imag(q)*z), 0)
	}
	outEx := m1*pf(qs[0])*a + m2*pf(qs[1])*b
	outEy := m1*pf(qs[0])*c + m2*pf(qs[1])*d
	return outEx, outEy
}

// buildBerreman returns the 4x4 Berreman matrix Delta in the field ordering
// psi = (Ex, Hy, Ey, -Hx) for a plane wave with normalized transverse
// wavevector (xi, eta) = (kx/k0, ky/k0) and relative permittivity eps.
func buildBerreman(eps [3][3]complex128, xi, eta float64) [4][4]complex128 {
	r := 1 / eps[2][2]
	x := complex(xi, 0)
	y := complex(eta, 0)
	var D [4][4]complex128
	D[0][0] = -x * r * eps[2][0]
	D[0][1] = 1 - x*x*r
	D[0][2] = -x * r * eps[2][1]
	D[0][3] = -x * r * y
	D[1][0] = eps[0][0] - y*y - eps[0][2]*r*eps[2][0]
	D[1][1] = -eps[0][2] * r * x
	D[1][2] = eps[0][1] + y*x - eps[0][2]*r*eps[2][1]
	D[1][3] = -eps[0][2] * r * y
	D[2][0] = -y * r * eps[2][0]
	D[2][1] = -y * r * x
	D[2][2] = -y * r * eps[2][1]
	D[2][3] = 1 - y*y*r
	D[3][0] = eps[1][0] + x*y - eps[1][2]*r*eps[2][0]
	D[3][1] = -eps[1][2] * r * x
	D[3][2] = eps[1][1] - x*x - eps[1][2]*r*eps[2][1]
	D[3][3] = -eps[1][2] * r * y
	return D
}

func forwardMode(q complex128) bool {
	qi := imag(q)
	if qi > 1e-12 {
		return true
	}
	if qi < -1e-12 {
		return false
	}
	return real(q) > 0
}

// charPoly4 returns the coefficients of the monic characteristic polynomial
// det(qI - A) = q^4 + c3 q^3 + c2 q^2 + c1 q + c0 via Faddeev-LeVerrier.
func charPoly4(A [4][4]complex128) (c0, c1, c2, c3 complex128) {
	var I [4][4]complex128
	for i := 0; i < 4; i++ {
		I[i][i] = 1
	}
	B := A
	c3 = -trace4(B)
	B = matMul4(A, addMat4(B, scaleMat4(I, c3)))
	c2 = -trace4(B) / 2
	B = matMul4(A, addMat4(B, scaleMat4(I, c2)))
	c1 = -trace4(B) / 3
	B = matMul4(A, addMat4(B, scaleMat4(I, c1)))
	c0 = -trace4(B) / 4
	return
}

func trace4(A [4][4]complex128) complex128 {
	return A[0][0] + A[1][1] + A[2][2] + A[3][3]
}

func scaleMat4(A [4][4]complex128, s complex128) [4][4]complex128 {
	var out [4][4]complex128
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			out[i][j] = A[i][j] * s
		}
	}
	return out
}

func addMat4(A, B [4][4]complex128) [4][4]complex128 {
	var out [4][4]complex128
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			out[i][j] = A[i][j] + B[i][j]
		}
	}
	return out
}

func matMul4(A, B [4][4]complex128) [4][4]complex128 {
	var out [4][4]complex128
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			var s complex128
			for k := 0; k < 4; k++ {
				s += A[i][k] * B[k][j]
			}
			out[i][j] = s
		}
	}
	return out
}

// roots4 finds the four complex roots of q^4 + c3 q^3 + c2 q^2 + c1 q + c0
// via simultaneous Durand-Kerner iteration.
func roots4(c0, c1, c2, c3 complex128) [4]complex128 {
	z := [4]complex128{
		complex(0.4, 0.9), complex(0.4, -0.9), complex(-0.4, 0.9), complex(-0.4, -0.9),
	}
	for iter := 0; iter < 300; iter++ {
		var maxD float64
		for i := 0; i < 4; i++ {
			num := poly4(z[i], c0, c1, c2, c3)
			den := complex(1, 0)
			for j := 0; j < 4; j++ {
				if j != i {
					den *= z[i] - z[j]
				}
			}
			dz := num / den
			z[i] -= dz
			d := math.Hypot(real(dz), imag(dz))
			if d > maxD {
				maxD = d
			}
		}
		if maxD < 1e-14 {
			break
		}
	}
	return z
}

func poly4(z, c0, c1, c2, c3 complex128) complex128 {
	return z*z*z*z + c3*z*z*z + c2*z*z + c1*z + c0
}

// det3 returns the determinant of a 3x3 complex matrix.
func det3(m [3][3]complex128) complex128 {
	return m[0][0]*(m[1][1]*m[2][2]-m[1][2]*m[2][1]) -
		m[0][1]*(m[1][0]*m[2][2]-m[1][2]*m[2][0]) +
		m[0][2]*(m[1][0]*m[2][1]-m[1][1]*m[2][0])
}

// nullVector4 returns a right null vector of the singular matrix A - qI by
// taking the largest-norm column of its adjugate.
func nullVector4(A [4][4]complex128, q complex128) [4]complex128 {
	var M [4][4]complex128
	for i := 0; i < 4; i++ {
		for j := 0; j < 4; j++ {
			M[i][j] = A[i][j]
		}
		M[i][i] -= q
	}
	var best [4]complex128
	bestN := -1.0
	for col := 0; col < 4; col++ {
		var v [4]complex128
		for row := 0; row < 4; row++ {
			var m [3][3]complex128
			ri := 0
			for r := 0; r < 4; r++ {
				if r == row {
					continue
				}
				ci := 0
				for c := 0; c < 4; c++ {
					if c == col {
						continue
					}
					m[ri][ci] = M[r][c]
					ci++
				}
				ri++
			}
			cof := det3(m)
			if (row+col)%2 == 1 {
				cof = -cof
			}
			v[row] = cof
		}
		n := 0.0
		for _, x := range v {
			n += real(x)*real(x) + imag(x)*imag(x)
		}
		if n > bestN {
			bestN, best = n, v
		}
	}
	return best
}
