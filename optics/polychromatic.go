package optics

import "fmt"

// WavelengthSample is one spectral component of a broadband source: the
// wavelength (m) and its relative spectral power density weight.
type WavelengthSample struct {
	Wavelength float64 `json:"wavelength"`
	Weight     float64 `json:"weight"`
}

// PropagatePolychromatic propagates the monochromatic field f a distance z at
// each wavelength in samples and returns the incoherently summed intensity
// I(x,y) = sum_k weight_k * |U_k(x,y)|^2 over the spectrum (length N*N,
// row-major). The input field's complex amplitude profile is reused at every
// wavelength (assumed wavelength-independent over the band), and the input
// field is not modified.
func PropagatePolychromatic(f *Field, z float64, samples []WavelengthSample, method Method, ctx *Context) ([]float64, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("polychromatic: no wavelength samples")
	}
	var base Context
	if ctx != nil {
		base = *ctx
	} else {
		base = Context{Evanescent: "decay", Warnings: &Warnings{}}
	}
	out := make([]float64, len(f.Ex))
	for _, s := range samples {
		if s.Wavelength <= 0 {
			return nil, fmt.Errorf("polychromatic: wavelength must be > 0")
		}
		g := f.Clone()
		c := base
		c.Wavelength = s.Wavelength
		if err := Propagate(g, z, method, &c); err != nil {
			return nil, err
		}
		for i := range out {
			out[i] += s.Weight * g.Intensity(i)
		}
	}
	return out, nil
}
