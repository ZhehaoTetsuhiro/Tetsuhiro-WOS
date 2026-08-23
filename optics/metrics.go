package optics

import (
	"fmt"
	"math"
)

// PlaneStats summarizes a recorded plane with SI quantities.
type PlaneStats struct {
	Power        float64 `json:"power"`      // W
	Peak         float64 `json:"peak"`       // W/m^2
	CentroidX    float64 `json:"centroid_x"` // m
	CentroidY    float64 `json:"centroid_y"` // m
	RMSX         float64 `json:"rms_x"`      // m
	RMSY         float64 `json:"rms_y"`      // m
	Strehl       float64 `json:"strehl"`     // 1 if ideal (see below)
	IntensityMin float64 `json:"intensity_min"`
	IntensityMax float64 `json:"intensity_max"`
	PhaseMin     float64 `json:"phase_min"` // rad, phase of Ex
	PhaseMax     float64 `json:"phase_max"` // rad
}

// ComputeStats integrates intensity moments over the field. When both
// strehlAperture (pupil radius, m) and strehlDistance (m) are > 0, the
// Strehl ratio is defined as the on-axis intensity relative to the ideal
// diffraction-limited focus of the same power through a clear circular
// pupil of that radius at that distance:
//
//	I_ideal(0) = P * pi*R^2 / (lambda^2 * z^2)
//	Strehl     = I(0) / I_ideal(0)
func ComputeStats(f *Field, wl, strehlAperture, strehlDistance float64) PlaneStats {
	n := f.N
	var s PlaneStats
	var p, px, py float64
	s.IntensityMin = math.Inf(1)
	phMin, phMax := math.Inf(1), math.Inf(-1)
	peakIdx := 0
	peak := -1.0
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			idx := j*n + i
			w := f.Intensity(idx)
			p += w
			px += w * f.X(i)
			py += w * y
			if w < s.IntensityMin {
				s.IntensityMin = w
			}
			if w > s.IntensityMax {
				s.IntensityMax = w
			}
			if w > peak {
				peak = w
				peakIdx = idx
			}
			ph := math.Atan2(imag(f.Ex[idx]), real(f.Ex[idx]))
			if ph < phMin {
				phMin = ph
			}
			if ph > phMax {
				phMax = ph
			}
		}
	}
	s.Power = p * f.DX * f.DX
	s.Peak = peak
	s.PhaseMin, s.PhaseMax = phMin, phMax
	if p <= 0 {
		return s
	}
	s.CentroidX = px / p
	s.CentroidY = py / p
	var vx, vy float64
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			w := f.Intensity(j*n + i)
			dx := f.X(i) - s.CentroidX
			dy := y - s.CentroidY
			vx += w * dx * dx
			vy += w * dy * dy
		}
	}
	s.RMSX = math.Sqrt(vx / p)
	s.RMSY = math.Sqrt(vy / p)
	if strehlAperture > 0 && strehlDistance > 0 {
		ideal := s.Power * math.Pi * strehlAperture * strehlAperture / (wl * wl * strehlDistance * strehlDistance)
		if ideal > 0 {
			s.Strehl = s.Peak / ideal
		}
	}
	_ = peakIdx
	return s
}

// Profile is a 1-D cut through a plane.
type Profile struct {
	Axis  string    `json:"axis"`
	Coord float64   `json:"coord"` // fixed coordinate of the cut, m
	X     []float64 `json:"x"`     // positions along the cut, m
	V     []float64 `json:"v"`     // field values
}

// Profile kinds understood by ProfileOf.
const (
	KindIntensity = "intensity"
	KindPhaseX    = "phase_x"
	KindPhaseY    = "phase_y"
	KindEx        = "ex" // |Ex|^2
	KindEy        = "ey" // |Ey|^2
)

// ProfileOf extracts a 1-D cut through the plane. axis is "x" or "y"; the
// fixed transverse coordinate is taken at coord (m) or, when coord is nil,
// at the intensity centroid. The cut averages a 3-pixel-wide slice.
func (p *Plane) ProfileOf(axis, kind string, coord *float64) (Profile, error) {
	n := p.Size
	get := func(idx int) float64 {
		ex := p.Ex[idx]
		ey := p.Ey[idx]
		ix := real(ex)*real(ex) + imag(ex)*imag(ex)
		iy := real(ey)*real(ey) + imag(ey)*imag(ey)
		switch kind {
		case KindIntensity:
			return ix + iy
		case KindPhaseX:
			return math.Atan2(imag(ex), real(ex))
		case KindPhaseY:
			return math.Atan2(imag(ey), real(ey))
		case KindEx:
			return ix
		case KindEy:
			return iy
		}
		return ix + iy
	}
	prof := Profile{Axis: axis, X: make([]float64, n), V: make([]float64, n)}
	if axis == "x" {
		j0 := 0
		if coord != nil {
			j0 = int(math.Round(*coord/p.DX + float64(n)/2))
		} else {
			j0 = int(math.Round(p.Stats.CentroidY/p.DX + float64(n)/2))
		}
		if j0 < 1 {
			j0 = 1
		}
		if j0 > n-2 {
			j0 = n - 2
		}
		prof.Coord = (float64(j0) - float64(n)/2) * p.DX
		for i := 0; i < n; i++ {
			prof.X[i] = (float64(i) - float64(n)/2) * p.DX
			prof.V[i] = (get((j0-1)*n+i) + get(j0*n+i) + get((j0+1)*n+i)) / 3
		}
		return prof, nil
	}
	if axis == "y" {
		i0 := 0
		if coord != nil {
			i0 = int(math.Round(*coord/p.DX + float64(n)/2))
		} else {
			i0 = int(math.Round(p.Stats.CentroidX/p.DX + float64(n)/2))
		}
		if i0 < 1 {
			i0 = 1
		}
		if i0 > n-2 {
			i0 = n - 2
		}
		prof.Coord = (float64(i0) - float64(n)/2) * p.DX
		for j := 0; j < n; j++ {
			prof.X[j] = (float64(j) - float64(n)/2) * p.DX
			prof.V[j] = (get(j*n+i0-1) + get(j*n+i0) + get(j*n+i0+1)) / 3
		}
		return prof, nil
	}
	return prof, fmt.Errorf("profile axis must be x or y, got %q", axis)
}
