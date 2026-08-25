package optics

import "math"

// Small dense complex-matrix helpers for the quantum-optics backend. All
// matrices are row-major square matrices of size d x d.

func qmatDim(A []complex128) int {
	return int(math.Sqrt(float64(len(A))))
}

func qmatIdentity(d int) []complex128 {
	out := make([]complex128, d*d)
	for i := 0; i < d; i++ {
		out[i*d+i] = 1
	}
	return out
}

func qmatScale(A []complex128, s complex128) []complex128 {
	out := make([]complex128, len(A))
	for i, v := range A {
		out[i] = v * s
	}
	return out
}

func qmatAdd(A, B []complex128) []complex128 {
	out := make([]complex128, len(A))
	for i := range A {
		out[i] = A[i] + B[i]
	}
	return out
}

func qmatMul(A, B []complex128) []complex128 {
	d := qmatDim(A)
	out := make([]complex128, d*d)
	for i := 0; i < d; i++ {
		for k := 0; k < d; k++ {
			aik := A[i*d+k]
			if aik == 0 {
				continue
			}
			row := out[i*d : i*d+d]
			brow := B[k*d : k*d+d]
			for j := 0; j < d; j++ {
				row[j] += aik * brow[j]
			}
		}
	}
	return out
}

func qmatDagger(A []complex128) []complex128 {
	d := qmatDim(A)
	out := make([]complex128, d*d)
	for i := 0; i < d; i++ {
		for j := 0; j < d; j++ {
			v := A[j*d+i]
			out[i*d+j] = complex(real(v), -imag(v))
		}
	}
	return out
}

// annihilatorMatrix returns the (base x base) annihilation operator a with
// a|n> = sqrt(n)|n-1>, row-major.
func annihilatorMatrix(base int) []complex128 {
	A := make([]complex128, base*base)
	for n := 1; n < base; n++ {
		A[(n-1)*base+n] = complex(math.Sqrt(float64(n)), 0)
	}
	return A
}

// displacementMatrix returns the single-mode displacement operator
// D(alpha) = exp(alpha a† - alpha* a) in the base-dim Fock basis.
func displacementMatrix(base int, alpha complex128) []complex128 {
	A := annihilatorMatrix(base)
	At := qmatDagger(A)
	// M = alpha a† - alpha* a  (anti-Hermitian, so exp(M) is unitary).
	ac := complex(real(alpha), -imag(alpha))
	M := qmatAdd(qmatScale(At, alpha), qmatScale(A, complex(-real(ac), -imag(ac))))
	return expm(M)
}

// squeezeMatrix returns the single-mode squeezing operator
// S(z) = exp(1/2 (z* a^2 - z a†^2)) in the base-dim Fock basis.
func squeezeMatrix(base int, z complex128) []complex128 {
	A := annihilatorMatrix(base)
	At := qmatDagger(A)
	A2 := qmatMul(A, A)
	At2 := qmatMul(At, At)
	zc := complex(real(z), -imag(z)) // z*
	// M = 1/2 (z* a^2 - z a†^2) (anti-Hermitian).
	M := qmatScale(qmatAdd(qmatScale(A2, zc), qmatScale(At2, complex(-real(z), -imag(z)))), complex(0.5, 0))
	return expm(M)
}

// expm computes the matrix exponential by scaling-and-squaring a Taylor series.
func expm(A []complex128) []complex128 {
	d := qmatDim(A)
	// Infinity norm (maximum absolute row sum).
	norm := 0.0
	for i := 0; i < d; i++ {
		var s float64
		for j := 0; j < d; j++ {
			s += math.Hypot(real(A[i*d+j]), imag(A[i*d+j]))
		}
		if s > norm {
			norm = s
		}
	}
	if norm == 0 {
		return qmatIdentity(d)
	}
	s := 0
	for norm > 1 {
		norm /= 2
		s++
	}
	As := qmatScale(A, complex(1/float64(int(1)<<s), 0))
	const terms = 24
	term := qmatIdentity(d)
	sum := qmatIdentity(d)
	fac := 1.0
	for k := 1; k <= terms; k++ {
		fac *= float64(k)
		term = qmatMul(term, As)
		sum = qmatAdd(sum, qmatScale(term, complex(1/fac, 0)))
	}
	for i := 0; i < s; i++ {
		sum = qmatMul(sum, sum)
	}
	return sum
}

// beamSplitterMatrix builds the two-mode lossless symmetric beamsplitter
// unitary U = exp(i theta (a0†a1 + a0 a1†)) in the base^2 Fock basis with
// local index L = n0 + base*n1. The Heisenberg transform is
//
//	a0 -> cos(theta) a0 + i sin(theta) a1
//	a1 -> i sin(theta) a0 + cos(theta) a1
//
// matching the classical symmetric beamsplitter (transmission sqrt(1-R),
// reflection i sqrt(R)).
func beamSplitterMatrix(base int, theta float64) []complex128 {
	d := base * base
	U := qmatIdentity(d)
	maxN := 2 * (base - 1)
	for N := 0; N <= maxN; N++ {
		alo := N - (base - 1)
		if alo < 0 {
			alo = 0
		}
		ahi := N
		if ahi > base-1 {
			ahi = base - 1
		}
		if ahi < alo {
			continue
		}
		sz := ahi - alo + 1
		G := make([]complex128, sz*sz)
		for ia := 0; ia < sz; ia++ {
			a := alo + ia
			b := N - a
			if a+1 <= ahi {
				G[(a+1-alo)*sz+ia] += complex(math.Sqrt(float64((a+1)*b)), 0)
			}
			if a-1 >= alo {
				G[(a-1-alo)*sz+ia] += complex(math.Sqrt(float64(a*(b+1))), 0)
			}
		}
		block := expm(qmatScale(G, complex(0, theta)))
		for ia := 0; ia < sz; ia++ {
			a := alo + ia
			linIn := a + base*(N-a)
			for ja := 0; ja < sz; ja++ {
				ap := alo + ja
				linOut := ap + base*(N-ap)
				U[linOut*d+linIn] = block[ja*sz+ia]
			}
		}
	}
	return U
}
