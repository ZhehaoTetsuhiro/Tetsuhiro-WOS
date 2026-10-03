//go:build cuda

// GPU acceleration backend for the wave-optics kernel: cuFFT (Z2Z) transforms
// behind the same package-private fft2D / fft1DAny entry points as the pure-Go
// code, so every propagation method (asm, asm_pad, fresnel_*, fraunhofer),
// element application (thin lenses, Berreman, birefringent), coherence and
// field metrics that touch the FFT automatically use the GPU.
//
// Build:
//
//	go build -tags cuda ./cmd/wos
//
// Runtime: the backend is off by default; enable it with the environment
// variable WOS_GPU=1 or by calling SetGPU(true). If no CUDA device is present,
// or any CUDA call fails, the transform returns an error and the caller falls
// back to the pure-Go implementation, so results stay correct on GPU-less
// machines and the kernel never depends on a device being available.

package optics

/*
#cgo CFLAGS: -I/usr/local/cuda/include
#cgo LDFLAGS: -L/usr/local/cuda/lib64 -lcufft -lcudart
#include <cufft.h>
#include <cuda_runtime.h>
#include <stdio.h>

static int wos_device_count(void) {
	int n = 0;
	if (cudaGetDeviceCount(&n) != cudaSuccess) return 0;
	return n;
}

static const char* wos_device_name(void) {
	static char buf[256];
	struct cudaDeviceProp p;
	if (cudaGetDeviceProperties(&p, 0) != cudaSuccess) return "";
	snprintf(buf, sizeof(buf), "%s", p.name);
	return buf;
}

static cufftResult wos_plan2d(cufftHandle *p, int nx, int ny) {
	return cufftPlan2d(p, nx, ny, CUFFT_Z2Z);
}
static cufftResult wos_plan1d(cufftHandle *p, int n) {
	return cufftPlan1d(p, n, CUFFT_Z2Z, 1);
}
static cufftResult wos_exec(cufftHandle p, void *d, int dir) {
	return cufftExecZ2Z(p, (cufftDoubleComplex*)d, (cufftDoubleComplex*)d, dir);
}
static const char* wos_cufft_strerror(cufftResult r) {
	switch (r) {
	case CUFFT_SUCCESS:        return "CUFFT_SUCCESS";
	case CUFFT_INVALID_PLAN:   return "CUFFT_INVALID_PLAN";
	case CUFFT_ALLOC_FAILED:   return "CUFFT_ALLOC_FAILED";
	case CUFFT_INVALID_TYPE:   return "CUFFT_INVALID_TYPE";
	case CUFFT_INVALID_VALUE:  return "CUFFT_INVALID_VALUE";
	case CUFFT_INTERNAL_ERROR: return "CUFFT_INTERNAL_ERROR";
	case CUFFT_EXEC_FAILED:    return "CUFFT_EXEC_FAILED";
	case CUFFT_SETUP_FAILED:   return "CUFFT_SETUP_FAILED";
	case CUFFT_INVALID_SIZE:   return "CUFFT_INVALID_SIZE";
	case CUFFT_UNALIGNED_DATA: return "CUFFT_UNALIGNED_DATA";
	default:                   return "cuFFT error";
	}
}

static int wos_malloc(void **p, size_t n) { return (int)cudaMalloc(p, n); }
static void wos_free(void *p) { cudaFree(p); }
static int wos_host_alloc(void **p, size_t n) { return (int)cudaHostAlloc(p, n, cudaHostAllocDefault); }
static void wos_host_free(void *p) { cudaFreeHost(p); }
static int wos_h2d(void *d, const void *h, size_t n) {
	return (int)cudaMemcpy(d, h, n, cudaMemcpyHostToDevice);
}
static int wos_d2h(void *h, const void *d, size_t n) {
	return (int)cudaMemcpy(h, d, n, cudaMemcpyDeviceToHost);
}
static const char* wos_cuda_strerror(int e) {
	return cudaGetErrorString((cudaError_t)e);
}
*/
import "C"

import (
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"unsafe"
)

var (
	gpuOnce    sync.Once
	gpuPresent bool
	gpuName    string
	gpuEnabled atomic.Bool

	// gpuExecMu serializes host<->device transfers and cuFFT execution.
	// Plans are cached and reused; concurrent executions on one handle are not
	// guaranteed safe, and serializing also keeps peak device memory bounded.
	gpuExecMu sync.Mutex

	gpuPlanMu sync.Mutex
	gpuPlans  = map[[2]int]C.cufftHandle{}
)

func gpuInit() {
	if int(C.wos_device_count()) > 0 {
		gpuPresent = true
		gpuName = C.GoString(C.wos_device_name())
	}
	if os.Getenv("WOS_GPU") == "1" {
		gpuEnabled.Store(true)
	}
}

// GPUAvailable reports whether a CUDA device is present and the backend was
// compiled in (build tag `cuda`).
func GPUAvailable() bool { gpuOnce.Do(gpuInit); return gpuPresent }

// GPUEnabled reports whether GPU acceleration is currently active.
func GPUEnabled() bool { gpuOnce.Do(gpuInit); return gpuPresent && gpuEnabled.Load() }

// SetGPU turns GPU acceleration on or off. It returns false if the request
// cannot be honoured (no CUDA device / backend not compiled in).
func SetGPU(on bool) bool {
	gpuOnce.Do(gpuInit)
	if on && !gpuPresent {
		return false
	}
	gpuEnabled.Store(on)
	return true
}

// GPUBackendInfo returns a human-readable description of the active backend.
func GPUBackendInfo() string {
	gpuOnce.Do(gpuInit)
	if !gpuPresent {
		return "CPU (pure Go; no CUDA device)"
	}
	if gpuEnabled.Load() {
		return "GPU: CUDA/cuFFT on " + gpuName
	}
	return "GPU available (CUDA/cuFFT on " + gpuName + ") but disabled; CPU in use"
}

func gpuReady() bool { gpuOnce.Do(gpuInit); return gpuPresent && gpuEnabled.Load() }

// gpuPlan returns (creating and caching on first use) a Z2Z plan for the given
// transform shape. ny==1 selects a 1-D transform; otherwise a 2-D transform.
func gpuPlan(nx, ny int) (C.cufftHandle, error) {
	key := [2]int{nx, ny}
	gpuPlanMu.Lock()
	defer gpuPlanMu.Unlock()
	if h, ok := gpuPlans[key]; ok {
		return h, nil
	}
	var h C.cufftHandle
	var rc C.cufftResult
	if ny == 1 {
		rc = C.wos_plan1d(&h, C.int(nx))
	} else {
		rc = C.wos_plan2d(&h, C.int(nx), C.int(ny))
	}
	if rc != C.CUFFT_SUCCESS {
		return 0, fmt.Errorf("cuFFT plan %dx%d failed: %s", nx, ny, C.GoString(C.wos_cufft_strerror(rc)))
	}
	gpuPlans[key] = h
	return h, nil
}

// gpuExec runs one in-place Z2Z transform of a on the device, applying
// invScale to every element when inverse is true (cuFFT does not normalize).
func gpuExec(h C.cufftHandle, a []complex128, inverse bool, invScale float64) error {
	if len(a) == 0 {
		return nil
	}
	nb := len(a) * int(unsafe.Sizeof(complex128(0)))
	gpuExecMu.Lock()
	defer gpuExecMu.Unlock()

	d, err := gpuAlloc(nb)
	if err != nil {
		return err
	}
	defer gpuRelease(d, nb)

	if rc := C.wos_h2d(d, unsafe.Pointer(&a[0]), C.size_t(nb)); rc != 0 {
		return fmt.Errorf("cudaMemcpy H2D failed: %s", C.GoString(C.wos_cuda_strerror(C.int(rc))))
	}
	dir := C.CUFFT_FORWARD
	if inverse {
		dir = C.CUFFT_INVERSE
	}
	if rc := C.wos_exec(h, d, C.int(dir)); rc != C.CUFFT_SUCCESS {
		return fmt.Errorf("cuFFT exec failed: %s", C.GoString(C.wos_cufft_strerror(rc)))
	}
	if rc := C.wos_d2h(unsafe.Pointer(&a[0]), d, C.size_t(nb)); rc != 0 {
		return fmt.Errorf("cudaMemcpy D2H failed: %s", C.GoString(C.wos_cuda_strerror(C.int(rc))))
	}
	if inverse && invScale != 1 {
		s := complex(invScale, 0)
		for i := range a {
			a[i] *= s
		}
	}
	return nil
}

// Device buffer pool. cudaMalloc/cudaFree are expensive relative to the
// transforms themselves, so buffers are recycled per byte size. This matters
// most at the grid sizes where the GPU actually wins (>= 2048^2), and it keeps
// the per-call cost dominated by the PCIe transfer rather than allocation.
var (
	gpuBufMu   sync.Mutex
	gpuBufFree = map[int][]unsafe.Pointer{}
)

func gpuAlloc(nb int) (unsafe.Pointer, error) {
	gpuBufMu.Lock()
	if free := gpuBufFree[nb]; len(free) > 0 {
		p := free[len(free)-1]
		gpuBufFree[nb] = free[:len(free)-1]
		gpuBufMu.Unlock()
		return p, nil
	}
	gpuBufMu.Unlock()
	var d unsafe.Pointer
	if rc := C.wos_malloc(&d, C.size_t(nb)); rc != 0 {
		return nil, fmt.Errorf("cudaMalloc(%d B) failed: %s", nb, C.GoString(C.wos_cuda_strerror(C.int(rc))))
	}
	return d, nil
}

func gpuRelease(p unsafe.Pointer, nb int) {
	gpuBufMu.Lock()
	if len(gpuBufFree[nb]) < 8 {
		gpuBufFree[nb] = append(gpuBufFree[nb], p)
		gpuBufMu.Unlock()
		return
	}
	gpuBufMu.Unlock()
	C.wos_free(p)
}

// gpuHostAlloc / gpuHostFree manage page-locked (pinned) staging buffers.
func gpuHostAlloc(nb int) (unsafe.Pointer, error) {
	var p unsafe.Pointer
	if rc := C.wos_host_alloc(&p, C.size_t(nb)); rc != 0 {
		return nil, fmt.Errorf("cudaHostAlloc(%d B) failed: %s", nb, C.GoString(C.wos_cuda_strerror(C.int(rc))))
	}
	return p, nil
}

func gpuHostFree(p unsafe.Pointer) { C.wos_host_free(p) }

// gpuCopyH2D / gpuCopyD2H are thin wrappers used by the bandwidth benchmarks
// and available to the backend for explicit staging.
func gpuCopyH2D(d, host unsafe.Pointer, nb int) error {
	if rc := C.wos_h2d(d, host, C.size_t(nb)); rc != 0 {
		return fmt.Errorf("cudaMemcpy H2D failed: %s", C.GoString(C.wos_cuda_strerror(C.int(rc))))
	}
	return nil
}

func gpuCopyD2H(host, d unsafe.Pointer, nb int) error {
	if rc := C.wos_d2h(host, d, C.size_t(nb)); rc != 0 {
		return fmt.Errorf("cudaMemcpy D2H failed: %s", C.GoString(C.wos_cuda_strerror(C.int(rc))))
	}
	return nil
}

// gpuFFT2D performs an in-place 2-D transform of a length n*n (row-major) via
// a single cuFFT Z2Z plan, matching the sign/normalization convention of the
// pure-Go fft2D.
func gpuFFT2D(a []complex128, n int, inverse bool) error {
	if !gpuReady() {
		return errGPUUnavailable
	}
	if len(a) != n*n {
		return fmt.Errorf("gpuFFT2D: length %d != %d*%d", len(a), n, n)
	}
	h, err := gpuPlan(n, n)
	if err != nil {
		return err
	}
	return gpuExec(h, a, inverse, 1/float64(n*n))
}

// gpuFFT1D performs an in-place 1-D transform of arbitrary length via cuFFT,
// matching the sign/normalization convention of the pure-Go fft1DAny.
func gpuFFT1D(a []complex128, inverse bool) error {
	if !gpuReady() {
		return errGPUUnavailable
	}
	n := len(a)
	if n == 0 {
		return nil
	}
	h, err := gpuPlan(n, 1)
	if err != nil {
		return err
	}
	return gpuExec(h, a, inverse, 1/float64(n))
}
