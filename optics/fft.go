// Package optics implements the wave-optics simulation kernel.
//
// Numerical core: a dependency-free, parallel, radix-2 Cooley-Tukey FFT
// operating on row-major complex128 arrays (N x N grids, N a power of two).
package optics

import (
	"math"
	"runtime"
	"sync"
)

// fftPlan caches bit-reversal permutation and twiddle factors for one size N.
type fftPlan struct {
	n    int
	bits int
	rev  []int
	// tw[k] = exp(-2*pi*i*k/n), k = 0..n/2 (forward transform).
	tw []complex128
}

var fftPlanCache sync.Map // int -> *fftPlan

func planFFT(n int) *fftPlan {
	if p, ok := fftPlanCache.Load(n); ok {
		return p.(*fftPlan)
	}
	bits := 0
	for m := n; m > 1; m >>= 1 {
		bits++
	}
	rev := make([]int, n)
	for i := 0; i < n; i++ {
		r := 0
		v := i
		for b := 0; b < bits; b++ {
			r = (r << 1) | (v & 1)
			v >>= 1
		}
		rev[i] = r
	}
	half := n / 2
	tw := make([]complex128, half+1)
	for k := 0; k <= half; k++ {
		ang := -2 * math.Pi * float64(k) / float64(n)
		tw[k] = complex(math.Cos(ang), math.Sin(ang))
	}
	p := &fftPlan{n: n, bits: bits, rev: rev, tw: tw}
	fftPlanCache.Store(n, p)
	return p
}

// fft1D transforms a in place. inverse=true uses conjugated twiddles and
// applies the 1/N normalization so that fft1D(fft1D(x)) == x.
func fft1D(a []complex128, plan *fftPlan, inverse bool) {
	n := plan.n
	// Bit-reversal permutation, in place.
	for i := 0; i < n; i++ {
		j := plan.rev[i]
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	// Iterative butterflies.
	for size := 2; size <= n; size <<= 1 {
		half := size / 2
		step := n / size
		for i := 0; i < n; i += size {
			for j := 0; j < half; j++ {
				w := plan.tw[j*step]
				if inverse {
					w = complex(real(w), -imag(w))
				}
				u := a[i+j]
				v := a[i+j+half] * w
				a[i+j] = u + v
				a[i+j+half] = u - v
			}
		}
	}
	if inverse {
		inv := 1 / complex(float64(n), 0)
		for i := range a {
			a[i] *= inv
		}
	}
}

// parFor runs f(i) for i in [0,n) spread over GOMAXPROCS goroutines.
// Each index must be safe to process concurrently with the others.
func parFor(n int, f func(i int)) {
	P := runtime.GOMAXPROCS(0)
	if P > n {
		P = n
	}
	if P <= 1 || n < 64 {
		for i := 0; i < n; i++ {
			f(i)
		}
		return
	}
	chunk := (n + P - 1) / P
	var wg sync.WaitGroup
	for c := 0; c < P; c++ {
		lo := c * chunk
		hi := lo + chunk
		if hi > n {
			hi = n
		}
		if lo >= hi {
			break
		}
		wg.Add(1)
		go func(lo, hi int) {
			defer wg.Done()
			for i := lo; i < hi; i++ {
				f(i)
			}
		}(lo, hi)
	}
	wg.Wait()
}

// colBufPool hands out per-goroutine scratch buffers for strided column
// transforms (a shared buffer would race).
var colBufPool = sync.Pool{New: func() any {
	b := make([]complex128, 0, 1024)
	return &b
}}

// fft2D transforms a (length n*n, row-major) in place.
// A forward 2-D transform maps f(x,y) -> sum f exp(-i 2pi (fx x + fy y)).
func fft2D(a []complex128, n int, inverse bool) {
	plan := planFFT(n)
	// Rows.
	parFor(n, func(r int) {
		fft1D(a[r*n:(r+1)*n], plan, inverse)
	})
	// Columns (stride n), each worker with its own scratch.
	parFor(n, func(c int) {
		bp := colBufPool.Get().(*[]complex128)
		col := (*bp)[:0]
		if cap(col) < n {
			col = make([]complex128, 0, n)
			*bp = col
		}
		col = col[:n]
		for r := 0; r < n; r++ {
			col[r] = a[r*n+c]
		}
		fft1D(col, plan, inverse)
		for r := 0; r < n; r++ {
			a[r*n+c] = col[r]
		}
		colBufPool.Put(bp)
	})
}
