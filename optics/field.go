package optics

import (
	"fmt"
	"math"
	"math/rand"
)

// Field is a sampled monochromatic field on an N x N grid with pixel size DX.
// Ex and Ey are the Jones components (length N*N, row-major, index = y*N+x);
// Ez is the longitudinal component populated by vectorial (non-paraxial)
// propagation when Vectorial is set. All quantities are SI: amplitudes carry
// sqrt(W)/m, |E|^2 is W/m^2.
type Field struct {
	N         int
	DX        float64 // pixel size, m
	Polarized bool    // false: Ey is unused (scalar simulation)
	Vectorial bool    // true: Ez is populated (3-component non-paraxial field)
	Ex        []complex128
	Ey        []complex128
	Ez        []complex128
}

// NewField allocates a zero field.
func NewField(n int, dx float64, polarized bool) *Field {
	return &Field{
		N: n, DX: dx, Polarized: polarized,
		Ex: make([]complex128, n*n),
		Ey: make([]complex128, n*n),
	}
}

// NewVectorialField allocates a zero 3-component field (Ex, Ey, Ez).
func NewVectorialField(n int, dx float64) *Field {
	f := NewField(n, dx, true)
	f.Ez = make([]complex128, n*n)
	f.Vectorial = true
	return f
}

// Clone deep-copies the field.
func (f *Field) Clone() *Field {
	g := &Field{N: f.N, DX: f.DX, Polarized: f.Polarized, Vectorial: f.Vectorial,
		Ex: make([]complex128, len(f.Ex)), Ey: make([]complex128, len(f.Ey))}
	copy(g.Ex, f.Ex)
	copy(g.Ey, f.Ey)
	if f.Vectorial && f.Ez != nil {
		g.Ez = make([]complex128, len(f.Ez))
		copy(g.Ez, f.Ez)
	}
	return g
}

// Width returns the physical width of the grid, m.
func (f *Field) Width() float64 { return f.DX * float64(f.N) }

// X returns the physical x coordinate of column i (grid centered on 0; the
// +N/2 point is excluded so the grid is exactly periodic under the FFT).
func (f *Field) X(i int) float64 { return (float64(i) - float64(f.N)/2) * f.DX }

// Y returns the physical y coordinate of row j.
func (f *Field) Y(j int) float64 { return (float64(j) - float64(f.N)/2) * f.DX }

// Intensity returns |Ex|^2 + |Ey|^2 (+ |Ez|^2 for vectorial fields) at idx.
func (f *Field) Intensity(idx int) float64 {
	p := real(f.Ex[idx])*real(f.Ex[idx]) + imag(f.Ex[idx])*imag(f.Ex[idx])
	if f.Polarized {
		p += real(f.Ey[idx])*real(f.Ey[idx]) + imag(f.Ey[idx])*imag(f.Ey[idx])
	}
	if f.Vectorial {
		p += real(f.Ez[idx])*real(f.Ez[idx]) + imag(f.Ez[idx])*imag(f.Ez[idx])
	}
	return p
}

// Power integrates intensity over the grid, W.
func (f *Field) Power() float64 {
	var p float64
	for i := range f.Ex {
		p += f.Intensity(i)
	}
	return p * f.DX * f.DX
}

// NormalizePower rescales the field so its integrated power equals p (W).
func (f *Field) NormalizePower(p float64) error {
	cur := f.Power()
	if cur <= 0 {
		return fmt.Errorf("cannot normalize an empty field")
	}
	s := math.Sqrt(p / cur)
	for i := range f.Ex {
		f.Ex[i] *= complex(s, 0)
	}
	if f.Polarized {
		for i := range f.Ey {
			f.Ey[i] *= complex(s, 0)
		}
	}
	if f.Vectorial {
		for i := range f.Ez {
			f.Ez[i] *= complex(s, 0)
		}
	}
	return nil
}

// ApplyJones multiplies the Jones vector by [[a b] [c d]] at every pixel.
func (f *Field) ApplyJones(a, b, c, d complex128) {
	if !f.Polarized {
		if b == 0 && c == 0 && d == a {
			for i := range f.Ex {
				f.Ex[i] *= a
			}
			return
		}
		// scalar -> vector transition
		f.Polarized = true
		for i := range f.Ex {
			ex := f.Ex[i]
			f.Ex[i] = a * ex
			f.Ey[i] = c * ex
		}
		return
	}
	for i := range f.Ex {
		ex, ey := f.Ex[i], f.Ey[i]
		f.Ex[i] = a*ex + b*ey
		f.Ey[i] = c*ex + d*ey
	}
}

// ScaleAmplitude multiplies both components by the complex factor s.
func (f *Field) ScaleAmplitude(s complex128) {
	for i := range f.Ex {
		f.Ex[i] *= s
	}
	if f.Polarized {
		for i := range f.Ey {
			f.Ey[i] *= s
		}
	}
	if f.Vectorial {
		for i := range f.Ez {
			f.Ez[i] *= s
		}
	}
}

// ApplyTilt adds linear phase k(sin(tx) x + sin(ty) y) (exact, not paraxial).
func (f *Field) ApplyTilt(tx, ty, wl float64) {
	k := 2 * math.Pi / wl
	sx, sy := math.Sin(tx), math.Sin(ty)
	for j := 0; j < f.N; j++ {
		ph := k * (sx*f.X(0) + sy*f.Y(j))
		for i := 0; i < f.N; i++ {
			idx := j*f.N + i
			f.Ex[idx] *= cexpI(ph)
			if f.Polarized {
				f.Ey[idx] *= cexpI(ph)
			}
			if f.Vectorial {
				f.Ez[idx] *= cexpI(ph)
			}
			ph += k * sx * f.DX
		}
	}
}

// cexpI returns exp(i*phi).
func cexpI(phi float64) complex128 {
	s, c := math.Sincos(phi)
	return complex(c, s)
}

// Context carries per-run settings through element application and propagation.
type Context struct {
	Wavelength float64 // m
	Evanescent string  // "decay" (physical) or "zero" for evanescent waves
	// EvanescentLimit truncates evanescent components whose decay exponent
	// (nepers) exceeds this value, guarding deep-evanescent underflow/overflow
	// and acting as a bandlimit on the evanescent tail. 0 disables truncation.
	EvanescentLimit float64
	// BackwardRegularize switches backward (z<0) propagation from hard-zeroing
	// the unstable, amplifying evanescent components to a Tikhonov-damped
	// inverse A/(1+(alpha*A)^2), improving inverse/retrieval applications.
	BackwardRegularize bool
	// TikhonovAlpha is the regularization strength for BackwardRegularize.
	// Values <= 0 fall back to 1e-3.
	TikhonovAlpha float64
	Bandlimit     *BandlimitOpts
	RNG           *rand.Rand // deterministic per-element randomness (diffusers)
	Warnings      *Warnings
}

// BandlimitOpts applies a smooth low-pass at a fraction of Nyquist to damp
// wrap-around aliasing. Fraction 1 disables it.
type BandlimitOpts struct {
	Fraction float64 `json:"fraction"` // cutoff as fraction of Nyquist, e.g. 0.9
	Sigma    float64 `json:"sigma"`    // roll-off width as fraction of Nyquist, e.g. 0.05
}

// ApplyBandlimit low-pass filters the field near the Nyquist frequency.
func (f *Field) ApplyBandlimit(bl *BandlimitOpts) {
	if bl == nil || bl.Fraction >= 1 || bl.Fraction <= 0 {
		return
	}
	n := f.N
	fnyq := 1 / (2 * f.DX)
	f0 := bl.Fraction * fnyq
	sig := bl.Sigma * fnyq
	if sig <= 0 {
		sig = fnyq * 0.01
	}
	cut := func(a []complex128) {
		fft2D(a, n, false)
		for j := 0; j < n; j++ {
			fy := f.freq(j)
			for i := 0; i < n; i++ {
				fx := f.freq(i)
				fr := math.Hypot(fx, fy)
				if fr > f0 {
					d := (fr - f0) / sig
					a[j*n+i] *= complex(math.Exp(-d*d), 0)
				}
			}
		}
		fft2D(a, n, true)
	}
	cut(f.Ex)
	if f.Polarized {
		cut(f.Ey)
	}
}

// freq returns the spatial frequency (1/m) of FFT index i.
func (f *Field) freq(i int) float64 {
	n := f.N
	if i <= n/2 {
		return float64(i) / (float64(n) * f.DX)
	}
	return float64(i-n) / (float64(n) * f.DX)
}

// Warnings collects non-fatal diagnostics emitted during a run.
type Warnings struct {
	items []Warning
	seen  map[string]int
}

// Warning is one diagnostic entry.
type Warning struct {
	Code    string  `json:"code"`
	Message string  `json:"message"`
	Count   int     `json:"count"`
	Value   float64 `json:"value,omitempty"`
}

// Add records a warning; identical codes accumulate a count. A nil *Warnings
// (e.g. a Context built by a low-level Propagate caller) silently drops the
// diagnostic instead of panicking.
func (w *Warnings) Add(code, msg string, value float64) {
	if w == nil {
		return
	}
	if w.seen == nil {
		w.seen = map[string]int{}
	}
	if i, ok := w.seen[code]; ok {
		w.items[i].Count++
		if value != 0 {
			w.items[i].Value = value
		}
		return
	}
	w.seen[code] = len(w.items)
	w.items = append(w.items, Warning{Code: code, Message: msg, Count: 1, Value: value})
}

// List returns the recorded warnings.
func (w *Warnings) List() []Warning {
	if w == nil {
		return nil
	}
	return w.items
}
