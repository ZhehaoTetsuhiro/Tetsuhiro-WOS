package optics

import "errors"

// GPU backend sizing thresholds. Below these grid sizes the host<->device
// transfer overhead outweighs the cuFFT speedup, so the pure-Go path is used
// even when the GPU backend is enabled.
const (
	gpuMin2D = 64   // 2-D grid side length
	gpuMin1D = 1024 // 1-D vector length
)

// errGPUUnavailable is returned by the GPU FFT entry points when the backend
// was not compiled in (no `cuda` build tag) or no CUDA device is present.
var errGPUUnavailable = errors.New("optics: GPU backend unavailable")
