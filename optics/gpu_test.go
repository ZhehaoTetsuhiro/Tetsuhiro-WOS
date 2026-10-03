//go:build cuda

package optics

import (
	"math"
	"math/cmplx"
	"testing"
	"unsafe"
)

func refField(n int, seed float64) []complex128 {
	a := make([]complex128, n*n)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			re := math.Sin(seed*float64(i+1)*0.7) + 0.5*math.Cos(seed*float64(j+1))
			im := math.Cos(seed*float64(i-j)*0.3) - 0.25*math.Sin(seed*float64(i*j+1))
			a[j*n+i] = complex(re, im)
		}
	}
	return a
}

func maxAbsDiff(a, b []complex128) float64 {
	m := 0.0
	for i := range a {
		if d := cmplx.Abs(a[i] - b[i]); d > m {
			m = d
		}
	}
	return m
}

func maxAbs(a []complex128) float64 {
	m := 0.0
	for _, v := range a {
		if d := cmplx.Abs(v); d > m {
			m = d
		}
	}
	return m
}

func TestGPUFFT2DMatchesCPU(t *testing.T) {
	if !GPUAvailable() {
		t.Skip("no CUDA device")
	}
	if !SetGPU(true) {
		t.Skip("could not enable GPU")
	}
	defer SetGPU(false)

	// Power-of-two and non-power-of-two (Bluestein on CPU, native on cuFFT).
	for _, n := range []int{8, 16, 32, 64, 128, 12, 27, 100} {
		src := refField(n, 1.3)
		cpu := append([]complex128(nil), src...)
		fft2DCPU(cpu, n, false)
		gpu := append([]complex128(nil), src...)
		if err := gpuFFT2D(gpu, n, false); err != nil {
			t.Fatalf("n=%d: gpuFFT2D forward: %v", n, err)
		}
		if d := maxAbsDiff(cpu, gpu) / math.Max(1, maxAbs(cpu)); d > 1e-9 {
			t.Errorf("n=%d forward: cpu/gpu rel diff %.3e", n, d)
		}

		// Round trip: inverse(forward(x)) == x.
		back := append([]complex128(nil), gpu...)
		if err := gpuFFT2D(back, n, true); err != nil {
			t.Fatalf("n=%d: gpuFFT2D inverse: %v", n, err)
		}
		if d := maxAbsDiff(back, src); d > 1e-9 {
			t.Errorf("n=%d round-trip: |back-src| %.3e", n, d)
		}
	}
}

func TestGPUFFT1DMatchesCPU(t *testing.T) {
	if !GPUAvailable() {
		t.Skip("no CUDA device")
	}
	if !SetGPU(true) {
		t.Skip("could not enable GPU")
	}
	defer SetGPU(false)

	for _, n := range []int{1024, 4096, 1000, 2048 + 1} {
		src := make([]complex128, n)
		for i := range src {
			src[i] = complex(math.Sin(float64(i)*0.01), math.Cos(float64(i)*0.017))
		}
		cpu := append([]complex128(nil), src...)
		fft1DAnyCPU(cpu, false)
		gpu := append([]complex128(nil), src...)
		if err := gpuFFT1D(gpu, false); err != nil {
			t.Fatalf("n=%d: gpuFFT1D forward: %v", n, err)
		}
		if d := maxAbsDiff(cpu, gpu) / math.Max(1, maxAbs(cpu)); d > 1e-9 {
			t.Errorf("n=%d forward: cpu/gpu rel diff %.3e", n, d)
		}
		back := append([]complex128(nil), gpu...)
		if err := gpuFFT1D(back, true); err != nil {
			t.Fatalf("n=%d: gpuFFT1D inverse: %v", n, err)
		}
		if d := maxAbsDiff(back, src); d > 1e-9 {
			t.Errorf("n=%d round-trip: |back-src| %.3e", n, d)
		}
	}
}

// TestGPUASMPropagationMatchesCPU checks the whole ASM propagation step (FFT ->
// transfer-function multiply -> inverse FFT), not just the raw transform: the
// GPU and CPU paths must agree to machine precision.
func TestGPUASMPropagationMatchesCPU(t *testing.T) {
	if !GPUAvailable() {
		t.Skip("no CUDA device")
	}
	n, dx, wl := 256, 0.25e-6, 632.8e-9
	mk := func() *Field {
		f := NewField(n, dx, false)
		w := 30e-6
		for j := 0; j < n; j++ {
			y := f.Y(j)
			for i := 0; i < n; i++ {
				x := f.X(i)
				a := math.Exp(-(x*x + y*y) / (w * w))
				f.Ex[j*n+i] = complex(a, 0)
			}
		}
		return f
	}
	for _, m := range []Method{MethodASM, MethodASMPad} {
		SetGPU(false)
		fc := mk()
		if err := Propagate(fc, 0.01, m, ctxFor(wl)); err != nil {
			t.Fatalf("%s cpu: %v", m, err)
		}
		if !SetGPU(true) {
			t.Skip("could not enable GPU")
		}
		fg := mk()
		if err := Propagate(fg, 0.01, m, ctxFor(wl)); err != nil {
			t.Fatalf("%s gpu: %v", m, err)
		}
		var maxd, maxa float64
		for i := range fc.Ex {
			if d := cmplx.Abs(fc.Ex[i] - fg.Ex[i]); d > maxd {
				maxd = d
			}
			if a := cmplx.Abs(fc.Ex[i]); a > maxa {
				maxa = a
			}
		}
		if rel := maxd / math.Max(1e-300, maxa); rel > 1e-9 {
			t.Errorf("%s: cpu/gpu propagation rel diff %.3e", m, rel)
		}
	}
	SetGPU(false)
}

func BenchmarkFFT2DCPU1024(b *testing.B) {
	n := 1024
	src := refField(n, 1.0)
	buf := make([]complex128, n*n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(buf, src)
		fft2DCPU(buf, n, false)
	}
}

func BenchmarkFFT2DGPU1024(b *testing.B) {
	if !GPUAvailable() || !SetGPU(true) {
		b.Skip("no CUDA device")
	}
	defer SetGPU(false)
	n := 1024
	src := refField(n, 1.0)
	buf := make([]complex128, n*n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(buf, src)
		if err := gpuFFT2D(buf, n, false); err != nil {
			b.Fatal(err)
		}
	}
}

func benchFFT2D(b *testing.B, n int, useGPU bool) {
	src := refField(n, 1.0)
	buf := make([]complex128, n*n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		copy(buf, src)
		if useGPU {
			if err := gpuFFT2D(buf, n, false); err != nil {
				b.Fatal(err)
			}
		} else {
			fft2DCPU(buf, n, false)
		}
	}
}

func BenchmarkFFT2DCPU2048(b *testing.B) { benchFFT2D(b, 2048, false) }
func BenchmarkFFT2DGPU2048(b *testing.B) {
	if !GPUAvailable() || !SetGPU(true) {
		b.Skip("no CUDA device")
	}
	defer SetGPU(false)
	benchFFT2D(b, 2048, true)
}
func BenchmarkFFT2DCPU4096(b *testing.B) { benchFFT2D(b, 4096, false) }
func BenchmarkFFT2DGPU4096(b *testing.B) {
	if !GPUAvailable() || !SetGPU(true) {
		b.Skip("no CUDA device")
	}
	defer SetGPU(false)
	benchFFT2D(b, 4096, true)
}

// benchTransfer measures a full two-way (H2D + D2H) copy of an n*n complex128
// buffer, pageable vs pinned.
func benchTransfer(b *testing.B, n int, pinned bool) {
	nn := n * n
	nb := nn * 16
	a := make([]complex128, nn)
	d, err := gpuAlloc(nb)
	if err != nil {
		b.Fatal(err)
	}
	defer gpuRelease(d, nb)
	var hp unsafe.Pointer
	if pinned {
		hp, err = gpuHostAlloc(nb)
		if err != nil {
			b.Fatal(err)
		}
		defer gpuHostFree(hp)
	}
	src := nb
	b.SetBytes(int64(nb) * 2)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if pinned {
			gpuCopyH2D(d, hp, src)
			gpuCopyD2H(hp, d, src)
			copy(a, unsafe.Slice((*complex128)(hp), nn))
		} else {
			gpuCopyH2D(d, unsafe.Pointer(&a[0]), src)
			gpuCopyD2H(unsafe.Pointer(&a[0]), d, src)
		}
	}
}

func BenchmarkTransferPageable4096(b *testing.B) { benchTransfer(b, 4096, false) }
func BenchmarkTransferPinned4096(b *testing.B)   { benchTransfer(b, 4096, true) }
