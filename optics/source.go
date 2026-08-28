package optics

import (
	"fmt"
	"math"
)

// SourceSpec describes the initial field injected at z = 0 of a train.
type SourceSpec struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// BuildSource constructs the normalized source field on the given grid.
func BuildSource(spec SourceSpec, size int, width float64, polarized bool, wl float64) (*Field, error) {
	f := NewField(size, width/float64(size), polarized)
	p := spec.Params
	if p == nil {
		p = map[string]any{}
	}
	get := func(key string, def float64) float64 {
		if v, ok := p[key]; ok {
			if x, err := asFloat(v); err == nil {
				return x
			}
		}
		return def
	}
	power := get("power", 1e-3)
	x0, y0 := get("x", 0), get("y", 0)
	tx, ty := get("tilt_x", 0), get("tilt_y", 0)
	n := f.N

	switch spec.Type {
	case "plane":
		for i := range f.Ex {
			f.Ex[i] = 1
		}
	case "gaussian":
		w0 := get("waist", 1e-3)
		if w0 <= 0 {
			return nil, fmt.Errorf("gaussian: waist must be > 0")
		}
		for j := 0; j < n; j++ {
			dy := f.Y(j) - y0
			for i := 0; i < n; i++ {
				dx := f.X(i) - x0
				r2 := dx*dx + dy*dy
				f.Ex[j*n+i] = complex(math.Exp(-r2/(w0*w0)), 0)
			}
		}
	case "laguerre_gaussian":
		w0 := get("waist", 1e-3)
		pIdx := int(get("p", 0))
		l := int(get("l", 1))
		if w0 <= 0 {
			return nil, fmt.Errorf("laguerre_gaussian: waist must be > 0")
		}
		lp := laguerre(pIdx, l)
		for j := 0; j < n; j++ {
			dy := f.Y(j) - y0
			for i := 0; i < n; i++ {
				dx := f.X(i) - x0
				r := math.Hypot(dx, dy)
				u := 2 * r * r / (w0 * w0)
				amp := math.Exp(-u/2) * math.Pow(math.Sqrt(u), float64(l))
				if r == 0 && l > 0 {
					amp = 0
				}
				var pl float64
				for k, c := range lp {
					pl += c * math.Pow(u, float64(k))
				}
				theta := math.Atan2(dy, dx)
				f.Ex[j*n+i] = complex(amp*pl*math.Cos(float64(l)*theta), amp*pl*math.Sin(float64(l)*theta))
			}
		}
	case "hermite_gaussian":
		w0 := get("waist", 1e-3)
		m := int(get("m", 1))
		mn := int(get("n", 0))
		if w0 <= 0 {
			return nil, fmt.Errorf("hermite_gaussian: waist must be > 0")
		}
		hm := hermite(m)
		hn := hermite(mn)
		hs := math.Sqrt(2) / w0
		for j := 0; j < n; j++ {
			dy := f.Y(j) - y0
			for i := 0; i < n; i++ {
				dx := f.X(i) - x0
				var px, py float64
				for k, c := range hm {
					px += c * math.Pow(hs*dx, float64(k))
				}
				for k, c := range hn {
					py += c * math.Pow(hs*dy, float64(k))
				}
				amp := px * py * math.Exp(-(dx*dx+dy*dy)/(w0*w0))
				f.Ex[j*n+i] = complex(amp, 0)
			}
		}
	case "bessel":
		beta := get("beta", 0.01)
		radius := get("radius", width/2)
		kr := 2 * math.Pi / wl * math.Sin(beta)
		for j := 0; j < n; j++ {
			dy := f.Y(j) - y0
			for i := 0; i < n; i++ {
				dx := f.X(i) - x0
				r := math.Hypot(dx, dy)
				if r <= radius {
					f.Ex[j*n+i] = complex(math.J0(kr*r), 0)
				}
			}
		}
	case "spherical":
		rad := get("radius", 0.1)
		conv := get("converging", 0)
		if rad <= 0 {
			return nil, fmt.Errorf("spherical: radius must be > 0")
		}
		k := 2 * math.Pi / wl
		s := 1.0
		if conv != 0 {
			s = -1.0
		}
		for j := 0; j < n; j++ {
			dy := f.Y(j) - y0
			for i := 0; i < n; i++ {
				dx := f.X(i) - x0
				dist := math.Sqrt(rad*rad + dx*dx + dy*dy)
				f.Ex[j*n+i] = complex(math.Cos(k*dist)*s/dist, math.Sin(k*dist)*s/dist)
			}
		}
	default:
		return nil, fmt.Errorf("unknown source type %q", spec.Type)
	}

	// Polarization (Jones vector of the source).
	pol := "x"
	if v, ok := p["polarization"]; ok {
		if s, ok := v.(string); ok {
			pol = s
		}
	}
	jx, jy := complex(1, 0), complex(0, 0)
	switch pol {
	case "x":
	case "y":
		jx, jy = 0, 1
	case "d":
		jx, jy = 1/complex(math.Sqrt2, 0), 1/complex(math.Sqrt2, 0)
	case "a":
		jx, jy = 1/complex(math.Sqrt2, 0), -1/complex(math.Sqrt2, 0)
	case "r":
		jx, jy = 1/complex(math.Sqrt2, 0), complex(0, 1/math.Sqrt2)
	case "l":
		jx, jy = 1/complex(math.Sqrt2, 0), complex(0, -1/math.Sqrt2)
	case "custom":
		jx = complex(get("jx_re", 1), get("jx_im", 0))
		jy = complex(get("jy_re", 0), get("jy_im", 0))
		nm := math.Hypot(real(jx), imag(jx)) + math.Hypot(real(jy), imag(jy))
		if nm == 0 {
			return nil, fmt.Errorf("source: custom polarization must be non-zero")
		}
		jx, jy = jx/complex(nm, 0), jy/complex(nm, 0)
	default:
		return nil, fmt.Errorf("unknown polarization %q", pol)
	}
	if polarized {
		for i := range f.Ex {
			ex := f.Ex[i]
			f.Ex[i] = ex * jx
			f.Ey[i] = ex * jy
		}
	} else {
		// Scalar (unpolarized) simulation keeps only the total power of the
		// requested Jones vector, so a y/circular/... polarization does not
		// zero the field (the polarization state itself is discarded).
		mag := math.Hypot(math.Hypot(real(jx), imag(jx)), math.Hypot(real(jy), imag(jy)))
		if mag != 0 {
			for i := range f.Ex {
				f.Ex[i] *= complex(mag, 0)
			}
		}
	}

	if tx != 0 || ty != 0 {
		f.ApplyTilt(tx, ty, wl)
	}
	if err := f.NormalizePower(power); err != nil {
		return nil, err
	}
	return f, nil
}

// laguerre returns coefficients (ascending powers) of the associated
// Laguerre polynomial L_p^l via the three-term recurrence.
func laguerre(p, l int) []float64 {
	if p < 0 {
		p = 0
	}
	prev := []float64{1} // L_0^l
	if p == 0 {
		return prev
	}
	cur := []float64{float64(l + 1), -1} // L_1^l = l+1 - x
	for k := 1; k < p; k++ {
		next := make([]float64, len(cur)+1)
		coef := 2*float64(k) + float64(l) + 1
		for i, c := range cur {
			next[i] += coef * c / float64(k+1)
			next[i+1] -= c / float64(k+1)
		}
		for i, c := range prev {
			next[i] -= float64(k+l) * c / float64(k+1)
		}
		prev, cur = cur, next
	}
	return cur
}

// hermite returns coefficients of the physicists' Hermite polynomial H_n.
func hermite(nn int) []float64 {
	if nn < 0 {
		nn = 0
	}
	prev := []float64{1}
	if nn == 0 {
		return prev
	}
	cur := []float64{0, 2} // H_1 = 2x
	for k := 1; k < nn; k++ {
		next := make([]float64, len(cur)+1)
		for i, c := range cur {
			next[i+1] += 2 * c
		}
		for i, c := range prev {
			next[i] -= 2 * float64(k) * c
		}
		prev, cur = cur, next
	}
	return cur
}

// asFloat converts a JSON number (float64) or numeric string to float64.
func asFloat(v any) (float64, error) {
	switch x := v.(type) {
	case float64:
		return x, nil
	case int:
		return float64(x), nil
	case int64:
		return float64(x), nil
	default:
		return 0, fmt.Errorf("expected number, got %T", v)
	}
}
