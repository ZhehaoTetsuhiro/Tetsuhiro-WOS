package optics

import "math"

// This file turns a wavelength into a displayable colour, so the GUI can show
// light as it actually looks instead of through a false-colour ramp.
//
// The CIE 1931 colour matching functions are evaluated with the multi-lobe
// Gaussian fit of Wyman, Sloan & Shirley (2013), which is accurate to a few
// percent over the visible band. The resulting XYZ tristimulus is converted to
// linear sRGB and normalised to unit peak, so the colour holds the hue and the
// physical intensity keeps carrying the brightness.

// gaussLobe evaluates an asymmetric Gaussian as used by the CIE fit.
func gaussLobe(x, mu, s1, s2 float64) float64 {
	s := s1
	if x >= mu {
		s = s2
	}
	t := (x - mu) / s
	return math.Exp(-0.5 * t * t)
}

// CIE1931 returns the colour matching functions x̄(λ), ȳ(λ), z̄(λ) for a
// wavelength in metres.
func CIE1931(wl float64) (x, y, z float64) {
	nm := wl * 1e9
	x = 1.056*gaussLobe(nm, 599.8, 37.9, 31.0) +
		0.362*gaussLobe(nm, 442.0, 16.0, 26.7) -
		0.065*gaussLobe(nm, 501.1, 20.4, 26.2)
	y = 0.821*gaussLobe(nm, 568.8, 46.9, 40.5) +
		0.286*gaussLobe(nm, 530.9, 16.3, 31.1)
	z = 1.217*gaussLobe(nm, 437.0, 11.8, 36.0) +
		0.681*gaussLobe(nm, 459.0, 26.0, 13.8)
	return x, y, z
}

// VisibleRange is the range of wavelengths rendered with their own colour.
const (
	VisibleMin = 380e-9
	VisibleMax = 780e-9
)

// cieFitSafeMax is the longest wavelength at which the Gaussian fit of the
// colour matching functions is still trustworthy. Beyond it x̄ and ȳ both
// collapse towards zero, and normalising that residual noise turns deep red
// into green (the ȳ tail outlives x̄).
const cieFitSafeMax = 700e-9

// WavelengthRGB returns the display colour of a wavelength as linear sRGB in
// [0,1] with unit peak component. The second result reports whether the
// wavelength lies in the visible band; outside it the nearest band edge colour
// is returned and the caller can flag the light as invisible.
func WavelengthRGB(wl float64) (r, g, b float64, visible bool) {
	visible = wl >= VisibleMin && wl <= VisibleMax
	// Out-of-band light keeps the edge hue (deep violet / deep red).
	w := wl
	if w < VisibleMin {
		w = VisibleMin
	}
	if w > VisibleMax {
		w = VisibleMax
	}
	// The deep-red tail is coloured from the last wavelength the fit resolves,
	// then faded to pure red towards the band edge (what the eye actually sees
	// at 700–780 nm).
	tail := 0.0
	if w > cieFitSafeMax {
		tail = (w - cieFitSafeMax) / (VisibleMax - cieFitSafeMax)
		w = cieFitSafeMax
	}
	x, y, z := CIE1931(w)
	// XYZ -> linear sRGB (IEC 61966-2-1).
	r = 3.2406*x - 1.5372*y - 0.4986*z
	g = -0.9689*x + 1.8758*y + 0.0415*z
	b = 0.0557*x - 0.2040*y + 1.0570*z
	if r < 0 {
		r = 0
	}
	if g < 0 {
		g = 0
	}
	if b < 0 {
		b = 0
	}
	if tail > 0 {
		g *= 1 - tail
		b *= 1 - tail
	}
	m := math.Max(r, math.Max(g, b))
	if m <= 0 {
		return 0, 0, 0, visible
	}
	return r / m, g / m, b / m, visible
}

// ColorWheelRGB maps a phase in [-π,π] to a fully saturated colour, the
// standard cyclic phase ramp (red at 0, green at 2π/3, blue at 4π/3).
func ColorWheelRGB(phase float64) (r, g, b float64) {
	// HSV with s=v=1 gives a smooth, readable cycle.
	h := math.Mod(phase/(2*math.Pi), 1)
	if h < 0 {
		h++
	}
	i := math.Floor(h * 6)
	f := h*6 - i
	q := 1 - f
	switch int(i) % 6 {
	case 0:
		return 1, f, 0
	case 1:
		return q, 1, 0
	case 2:
		return 0, 1, f
	case 3:
		return 0, q, 1
	case 4:
		return f, 0, 1
	default:
		return 1, 0, q
	}
}
