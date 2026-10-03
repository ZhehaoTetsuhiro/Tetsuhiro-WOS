//go:build !cuda

package optics

// GPUAvailable reports whether a CUDA/cuFFT backend is compiled in and usable.
// This build (no `cuda` build tag) always reports false.
func GPUAvailable() bool { return false }

// GPUEnabled reports whether GPU acceleration is currently active.
func GPUEnabled() bool { return false }

// SetGPU requests GPU acceleration. The stub build cannot honour the request.
func SetGPU(on bool) bool { return false }

// GPUBackendInfo returns a human-readable description of the backend.
func GPUBackendInfo() string { return "CPU (pure Go; build with -tags cuda for GPU)" }

// gpuReady reports whether the GPU path should be taken.
func gpuReady() bool { return false }

func gpuFFT2D(a []complex128, n int, inverse bool) error { return errGPUUnavailable }

func gpuFFT1D(a []complex128, inverse bool) error { return errGPUUnavailable }
