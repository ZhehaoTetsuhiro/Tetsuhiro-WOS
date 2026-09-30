package optics

import "math"

// ---------------------------------------------------------------------------
// Polarization and phase inspection
//
// The wave simulation carries a full Jones vector (Ex, Ey, Ez) on every plane,
// so the state of polarization and the wavefront phase can be read out per
// pixel instead of only through intensity maps. Everything here works on one
// PlanePart (one coherent unit), which is the only place where the complex
// field is physically meaningful when several sources are present.
//
// Conventions: the field is written E = A exp(i(k·r - ωt)) (the kernel uses
// e^{+ikz} propagation), x is the horizontal and y the vertical transverse
// axis. With that choice S3 > 0 is left-handed rotation of the electric vector
// as seen looking towards the source (anticlockwise on a screen showing the
// beam travelling towards the viewer).
// ---------------------------------------------------------------------------

// Stokes is the polarization state of one point: the four Stokes parameters
// plus the derived ellipse geometry.
type Stokes struct {
	S0         float64 `json:"s0"`          // total intensity
	S1         float64 `json:"s1"`          // |Ex|^2 - |Ey|^2
	S2         float64 `json:"s2"`          // 2 Re(Ex* Ey)
	S3         float64 `json:"s3"`          // 2 Im(Ex* Ey)
	Azimuth    float64 `json:"azimuth"`     // rad, major axis vs x ([-pi/2,pi/2])
	Ellipt     float64 `json:"ellipticity"` // rad, atan(b/a) in [-pi/4,pi/4], >0 left
	AxRatio    float64 `json:"axis_ratio"`  // b/a in [0,1]
	Handedness string  `json:"handedness"`  // 左旋 / 右旋 / 线偏振
	Degree     float64 `json:"degree"`      // degree of polarization (1 for coherent light)
	AmplX      float64 `json:"amp_x"`       // |Ex|
	AmplY      float64 `json:"amp_y"`       // |Ey|
	PhaseX     float64 `json:"phase_x"`     // rad
	PhaseY     float64 `json:"phase_y"`     // rad
	Delta      float64 `json:"delta"`       // phase_y - phase_x, wrapped to [-pi,pi]
}

// StokesAt computes the Stokes parameters of one pixel of a Jones field.
func StokesAt(ex, ey complex128) Stokes {
	ax, ay := math.Hypot(real(ex), imag(ex)), math.Hypot(real(ey), imag(ey))
	px, py := math.Atan2(imag(ex), real(ex)), math.Atan2(imag(ey), real(ey))
	var st Stokes
	st.AmplX, st.AmplY = ax, ay
	st.PhaseX, st.PhaseY = px, py
	st.Delta = wrapPi(py - px)
	st.S0 = ax*ax + ay*ay
	st.S1 = ax*ax - ay*ay
	if ax > 0 && ay > 0 {
		st.S2 = 2 * ax * ay * math.Cos(st.Delta)
		st.S3 = 2 * ax * ay * math.Sin(st.Delta)
	}
	return st.derive()
}

// StokesFromS builds a Stokes vector from the four measured parameters and
// derives the rest. Stokes vectors of mutually incoherent fields add, so this is
// how the state of a plane fed by several coherent units is described (and how
// its degree of polarization comes out below 1).
func StokesFromS(s0, s1, s2, s3 float64) Stokes {
	return Stokes{S0: s0, S1: s1, S2: s2, S3: s3}.derive()
}

// derive fills in the ellipse parameters, the handedness and the degree of
// polarization from S0..S3.
func (st Stokes) derive() Stokes {
	if st.S0 <= 0 {
		return st
	}
	st.Azimuth = wrapHalfPi(0.5 * math.Atan2(st.S2, st.S1))
	st.Ellipt = 0.5 * math.Asin(clamp(st.S3/st.S0, -1, 1))
	st.AxRatio = math.Abs(math.Tan(st.Ellipt))
	st.Degree = math.Hypot(math.Hypot(st.S1, st.S2), st.S3) / st.S0
	switch {
	case st.AxRatio < 1e-3:
		st.Handedness = "线偏振"
	case st.S3 > 0:
		st.Handedness = "左旋"
	default:
		st.Handedness = "右旋"
	}
	return st
}

// StokesAtPixel reads the state of one pixel of a part.
func (pp *PlanePart) StokesAtPixel(i int) Stokes {
	if i < 0 || i >= len(pp.Ex) {
		return Stokes{}
	}
	var ey complex128
	if pp.Ey != nil {
		ey = pp.Ey[i]
	}
	return StokesAt(pp.Ex[i], ey)
}

// PhaseAt returns the phase of the dominant field component (the component with
// the larger amplitude) at one pixel, in radians.
func (pp *PlanePart) PhaseAt(i int) float64 {
	if i < 0 || i >= len(pp.Ex) {
		return 0
	}
	ex := pp.Ex[i]
	ax := norm2c(ex)
	ay := 0.0
	var ey complex128
	if pp.Ey != nil {
		ey = pp.Ey[i]
		ay = norm2c(ey)
	}
	z := ex
	if ay > ax {
		z = ey
	}
	if norm2c(z) == 0 {
		return 0
	}
	return math.Atan2(imag(z), real(z))
}

// RelativePhaseAt returns the phase of Ex plus the phase difference to Ey,
// useful for showing how the two components beat.
func (pp *PlanePart) RelativePhaseAt(i int) float64 {
	if i < 0 || i >= len(pp.Ex) {
		return 0
	}
	return math.Atan2(imag(pp.Ex[i]), real(pp.Ex[i]))
}

// UnwrapPhase returns a continuously varying phase map (in radians) obtained by
// unwrapping the wrapped phase along each row and then each column, stopping
// where the intensity falls below `floor` times the peak intensity so that dark
// regions (where the phase is pure noise) do not inject false 2π steps. A
// piston term is removed so the map starts near zero at the brightest pixel.
func (pp *PlanePart) UnwrapPhase(floor float64) []float64 {
	n := int(math.Round(math.Sqrt(float64(len(pp.Ex)))))
	if n <= 0 || n*n != len(pp.Ex) {
		return nil
	}
	inten := pp.Intensity()
	peak := 0.0
	for _, v := range inten {
		if v > peak {
			peak = v
		}
	}
	if peak <= 0 {
		return make([]float64, len(pp.Ex))
	}
	thr := floor * peak
	ok := make([]bool, len(pp.Ex))
	for i, v := range inten {
		ok[i] = v >= thr
	}
	// Wrapped phase, NaN where the intensity is too low to carry phase.
	raw := make([]float64, len(pp.Ex))
	for i := range raw {
		if ok[i] {
			raw[i] = pp.PhaseAt(i)
		} else {
			raw[i] = math.NaN()
		}
	}
	out := make([]float64, len(raw))
	copy(out, raw)
	twoPi := 2 * math.Pi
	// Unwrap each row left to right, resetting the offset at every masked
	// stretch so a dark gap never carries a jump across it.
	for j := 0; j < n; j++ {
		off := 0.0
		prev := math.NaN()
		for i := 0; i < n; i++ {
			idx := j*n + i
			if math.IsNaN(raw[idx]) {
				prev = math.NaN()
				continue
			}
			v := raw[idx] + off
			if !math.IsNaN(prev) {
				for v-prev > math.Pi {
					v -= twoPi
					off -= twoPi
				}
				for v-prev < -math.Pi {
					v += twoPi
					off += twoPi
				}
			}
			out[idx] = v
			prev = v
		}
	}
	// Then unwrap each column top to bottom.
	for i := 0; i < n; i++ {
		prev := math.NaN()
		for j := 0; j < n; j++ {
			idx := j*n + i
			if math.IsNaN(raw[idx]) {
				prev = math.NaN()
				continue
			}
			v := out[idx]
			if !math.IsNaN(prev) {
				d := math.Mod(v-prev, twoPi)
				if d > math.Pi {
					d -= twoPi
				} else if d < -math.Pi {
					d += twoPi
				}
				v = prev + d
				out[idx] = v
			}
			prev = v
		}
	}
	// Remove the piston and keep the dark pixels marked with NaN.
	ref := 0.0
	for idx := range out {
		if !math.IsNaN(raw[idx]) && inten[idx] == peak {
			ref = out[idx]
			break
		}
	}
	for idx := range out {
		if math.IsNaN(raw[idx]) {
			out[idx] = math.NaN()
		} else {
			out[idx] -= ref
		}
	}
	return out
}

// PhaseStats summarizes a phase map: peak-to-valley and RMS in radians, plus
// the wavefront error in waves at the part's wavelength.
func PhaseStats(ph []float64, wl float64) (pv, rms, waves float64) {
	mn, mx := math.Inf(1), math.Inf(-1)
	sum, cnt := 0.0, 0
	for _, v := range ph {
		if math.IsNaN(v) {
			continue
		}
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
		sum += v
		cnt++
	}
	if cnt == 0 {
		return 0, 0, 0
	}
	mean := sum / float64(cnt)
	ss := 0.0
	for _, v := range ph {
		if math.IsNaN(v) {
			continue
		}
		ss += (v - mean) * (v - mean)
	}
	pv = mx - mn
	rms = math.Sqrt(ss / float64(cnt))
	if wl > 0 {
		waves = pv / (2 * math.Pi)
	}
	return pv, rms, waves
}

func wrapPi(a float64) float64 {
	for a > math.Pi {
		a -= 2 * math.Pi
	}
	for a < -math.Pi {
		a += 2 * math.Pi
	}
	return a
}

func wrapHalfPi(a float64) float64 {
	for a > math.Pi/2 {
		a -= math.Pi
	}
	for a < -math.Pi/2 {
		a += math.Pi
	}
	return a
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
