package optics

import (
	"math"
	"testing"
)

func cabs(z complex128) float64 { return math.Hypot(real(z), imag(z)) }

func TestFFTRoundTrip(t *testing.T) {
	n := 64
	a := make([]complex128, n*n)
	rng := &simpleRNG{seed: 42}
	for i := range a {
		a[i] = complex(rng.next(), rng.next())
	}
	orig := make([]complex128, len(a))
	copy(orig, a)
	fft2D(a, n, false)
	fft2D(a, n, true)
	for i := range a {
		if cabs(a[i]-orig[i]) > 1e-12 {
			t.Fatalf("round trip mismatch at %d: %v vs %v", i, a[i], orig[i])
		}
	}
}

// The 2-D FFT of an impulse must be the constant 1 everywhere.
func TestFFTImpulse(t *testing.T) {
	n := 32
	a := make([]complex128, n*n)
	a[0] = 1
	fft2D(a, n, false)
	for i := range a {
		if cabs(a[i]-1) > 1e-12 {
			t.Fatalf("impulse spectrum not flat at %d: %v", i, a[i])
		}
	}
}

// Parseval: total power conserved between space and frequency domain.
func TestFFTParseval(t *testing.T) {
	n := 128
	a := make([]complex128, n*n)
	rng := &simpleRNG{seed: 7}
	var p1 float64
	for i := range a {
		a[i] = complex(rng.next(), rng.next())
		p1 += cabs(a[i]) * cabs(a[i])
	}
	fft2D(a, n, false)
	var p2 float64
	for i := range a {
		p2 += cabs(a[i]) * cabs(a[i])
	}
	// FFT is unitary up to N^2: sum|F|^2 = N^2 * sum|f|^2
	if math.Abs(p2-p1*float64(n*n))/(p1*float64(n*n)) > 1e-12 {
		t.Fatalf("Parseval violated: %v vs %v", p2, p1*float64(n*n))
	}
}

// simpleRNG is a tiny deterministic uniform generator for tests.
type simpleRNG struct{ seed uint64 }

func (r *simpleRNG) next() float64 {
	r.seed = r.seed*6364136223846793005 + 1442695040888963407
	return float64(r.seed>>11)/9007199254740992.0 - 0.5
}
