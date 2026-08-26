package optics

import (
	"fmt"
	"math"
	"math/rand"
)

// SchellSourceParams describes a Gaussian Schell-model source: an intensity
// profile exp(-2 r^2 / w0^2) and a Gaussian degree of coherence
// exp(-|dr|^2 / (2 sigma_c^2)).
type SchellSourceParams struct {
	Width     float64 // w0, intensity 1/e^2 radius (m)
	Coherence float64 // sigma_c, transverse coherence width (m)
	Seed      int64
}

// GenerateSchellRealizations returns m independent coherent realizations of a
// Gaussian Schell-model field on an N x N grid. Each realization is a complex
// amplitude whose ensemble statistics match the prescribed intensity and
// degree of coherence: <U*(r1) U(r2)> = sqrt(I(r1) I(r2)) mu(r2-r1).
func GenerateSchellRealizations(n int, dx float64, p SchellSourceParams, m int) ([]*Field, error) {
	if p.Width <= 0 || p.Coherence <= 0 {
		return nil, fmt.Errorf("schell: width and coherence must be > 0")
	}
	if m <= 0 {
		m = 1
	}
	// diffCoord maps index to the wrapped difference coordinate (index 0 = 0).
	diffCoord := func(i int) float64 {
		if i <= n/2 {
			return float64(i) * dx
		}
		return float64(i-n) * dx
	}
	// sqrt of the power spectral density S = DFT of mu(dr) (separable, >= 0).
	sqrtS := make([]complex128, n*n)
	for j := 0; j < n; j++ {
		dy := diffCoord(j)
		for i := 0; i < n; i++ {
			d := diffCoord(i)
			mu := math.Exp(-(d*d + dy*dy) / (2 * p.Coherence * p.Coherence))
			sqrtS[j*n+i] = complex(mu, 0)
		}
	}
	fft2D(sqrtS, n, false)
	for i := range sqrtS {
		s := real(sqrtS[i])
		if s < 0 {
			s = 0
		}
		sqrtS[i] = complex(math.Sqrt(s), 0)
	}
	// intensity envelope (centered coordinate).
	I := make([]float64, n*n)
	for j := 0; j < n; j++ {
		y := (float64(j) - float64(n)/2) * dx
		for i := 0; i < n; i++ {
			x := (float64(i) - float64(n)/2) * dx
			I[j*n+i] = math.Exp(-2 * (x*x + y*y) / (p.Width * p.Width))
		}
	}
	rng := rand.New(rand.NewSource(p.Seed))
	invSqrt2 := 1 / math.Sqrt2
	fields := make([]*Field, m)
	for k := 0; k < m; k++ {
		f := NewField(n, dx, false)
		for i := range f.Ex {
			f.Ex[i] = complex(rng.NormFloat64()*invSqrt2, rng.NormFloat64()*invSqrt2)
		}
		for i := range f.Ex {
			f.Ex[i] *= sqrtS[i]
		}
		fft2D(f.Ex, n, true)
		for i := range f.Ex {
			f.Ex[i] *= complex(math.Sqrt(I[i]), 0)
		}
		fields[k] = f
	}
	return fields, nil
}

// AverageIntensity returns the ensemble-averaged intensity (length N*N) over a
// set of coherent fields (e.g. propagated Schell realizations).
func AverageIntensity(fields []*Field) []float64 {
	if len(fields) == 0 {
		return nil
	}
	out := make([]float64, len(fields[0].Ex))
	for _, f := range fields {
		for i := range out {
			out[i] += f.Intensity(i)
		}
	}
	inv := 1 / float64(len(fields))
	for i := range out {
		out[i] *= inv
	}
	return out
}

// PropagatePartiallyCoherent generates m Gaussian Schell-model realizations,
// propagates each a distance z with the given method, and returns the
// ensemble-averaged (incoherent) intensity.
func PropagatePartiallyCoherent(n int, dx float64, src SchellSourceParams, m int, z float64, method Method, ctx *Context) ([]float64, error) {
	fields, err := GenerateSchellRealizations(n, dx, src, m)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if err := Propagate(f, z, method, ctx); err != nil {
			return nil, err
		}
	}
	return AverageIntensity(fields), nil
}
