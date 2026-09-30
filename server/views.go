package server

import (
	"fmt"
	"image"
	"image/color"
	"math"
	"net/url"
	"sort"

	"twos/optics"
)

// ---------------------------------------------------------------------------
// Field views
//
// One sampler produces the per-pixel values of every view the GUI offers, so
// the centre image, the profile curves and the numeric readouts all agree:
//
//	total | amplitude | ex | ey | ez      intensity views
//	phase_x | phase_y | phase_z           wrapped phase (rad)
//	phase_u                               unwrapped wavefront phase (rad)
//	pol_azimuth | pol_ellip               polarization ellipse geometry
//	pol_s1 | pol_s2 | pol_s3 | pol_degree Stokes parameters
//	color                                 real light colour (RGB raster)
//
// Views that need a complex field are taken from one coherent unit (a "part"):
// the one named by ?part=N, or the strongest one. Intensity views sum every
// unit, which is what makes multi-source layouts correct.
// ---------------------------------------------------------------------------

// viewKind classifies a view so the rasterizer can pick a scale and colormap.
type viewKind int

const (
	kindIntensity viewKind = iota
	kindAmplitude
	kindPhaseWrapped
	kindPhaseUnwrapped
	kindCyclic // cyclic quantity in [-π/2,π/2] (polarization azimuth)
	kindSigned
	kindUnit // value already in [-1,1] or [0,1]
)

// phaseMaskCut is the relative intensity below which phase-like quantities are
// treated as undefined (0.2% of the peak). One constant, so the wavefront image
// and the point readout cannot disagree.
const phaseMaskCut = 2e-3

// totalStokes returns the Stokes vector of all the light at one pixel of a
// plane. The coherent units are mutually incoherent, so their Stokes vectors
// add; for a plane without parts this is simply its own field.
func totalStokes(pl *optics.Plane, i int) optics.Stokes {
	if len(pl.Parts) == 0 {
		pp, _ := partField(pl, -1)
		return pp.StokesAtPixel(i)
	}
	var sum optics.Stokes
	for k := range pl.Parts {
		s := pl.Parts[k].StokesAtPixel(i)
		sum.S0 += s.S0
		sum.S1 += s.S1
		sum.S2 += s.S2
		sum.S3 += s.S3
	}
	return optics.StokesFromS(sum.S0, sum.S1, sum.S2, sum.S3)
}

// viewInfo describes one named view.
type viewInfo struct {
	kind viewKind
	unit string
	// perPart is true when the view needs one coherent unit's complex field.
	perPart bool
}

var viewTable = map[string]viewInfo{
	"total":         {kind: kindIntensity, unit: "W/m²"},
	"amplitude":     {kind: kindAmplitude, unit: "V/m"},
	"ex":            {kind: kindIntensity, unit: "W/m²", perPart: true},
	"ey":            {kind: kindIntensity, unit: "W/m²", perPart: true},
	"ez":            {kind: kindIntensity, unit: "W/m²", perPart: true},
	"phase_x":       {kind: kindPhaseWrapped, unit: "rad", perPart: true},
	"phase_y":       {kind: kindPhaseWrapped, unit: "rad", perPart: true},
	"phase_z":       {kind: kindPhaseWrapped, unit: "rad", perPart: true},
	"phase_u":       {kind: kindPhaseUnwrapped, unit: "rad", perPart: true},
	"pol_azimuth":   {kind: kindCyclic, unit: "rad", perPart: true},
	"pol_ellip":     {kind: kindSigned, unit: "rad", perPart: true},
	"pol_s1":        {kind: kindUnit, unit: "S1/S0", perPart: true},
	"pol_s2":        {kind: kindUnit, unit: "S2/S0", perPart: true},
	"pol_s3":        {kind: kindUnit, unit: "S3/S0", perPart: true},
	"pol_degree":    {kind: kindUnit, unit: "DOP", perPart: true},
	"pol_intensity": {kind: kindIntensity, unit: "W/m²", perPart: true},
}

// viewNames lists the supported field views (used by /api/catalog).
func viewNames() []string {
	out := make([]string, 0, len(viewTable))
	for k := range viewTable {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// partField returns the complex field of the selected coherent unit together
// with the part itself. index < 0 selects the strongest unit.
func partField(pl *optics.Plane, index int) (*optics.PlanePart, int) {
	if len(pl.Parts) == 0 {
		// A plane without parts (single unit recorded directly) exposes its
		// own field as part 0.
		return &optics.PlanePart{
			Source: 0, Label: pl.UnitKey, Wavelength: pl.Wavelength,
			Power: pl.Stats.Power, Peak: pl.Stats.Peak,
			Ex: pl.Ex, Ey: pl.Ey, Ez: pl.Ez,
		}, 0
	}
	if index < 0 || index >= len(pl.Parts) {
		index = pl.DominantPart()
	}
	p := pl.Parts[index]
	return &p, index
}

// planeValues samples one view of a plane. It returns the values, the view
// descriptor and the effective part index.
// planeValues extracts one named view of a plane. maskCut is the relative
// intensity below which quantities that are undefined in the dark (wavefront
// phase, polarization state) are masked out; maskCut <= 0 selects the default
// (2e-3 for the unwrapped wavefront, 1e-6 for the polarization views).
func planeValues(pl *optics.Plane, field string, part int, maskCut float64) ([]float64, viewInfo, int, error) {
	info, ok := viewTable[field]
	if !ok {
		return nil, info, -1, fmt.Errorf("unknown field %q", field)
	}
	n := pl.Size * pl.Size
	if info.perPart {
		pp, idx := partField(pl, part)
		out := make([]float64, n)
		switch field {
		case "ex", "ey", "ez":
			src := pp.Ex
			if field == "ey" {
				src = pp.Ey
			} else if field == "ez" {
				src = pp.Ez
			}
			for i := 0; i < n && i < len(src); i++ {
				out[i] = norm2(src[i])
			}
		case "phase_x", "phase_y", "phase_z":
			// The wrapped phase of the requested component on its own, masked
			// where that component carries no light (its phase is undefined
			// there). Falling through to the default returned the *dominant*
			// component's phase for all three.
			src := pp.Ex
			if field == "phase_y" {
				src = pp.Ey
			} else if field == "phase_z" {
				src = pp.Ez
			}
			mc := maskCut
			if mc <= 0 {
				mc = phaseMaskCut
			}
			peak := 0.0
			for i := 0; i < n && i < len(src); i++ {
				if v := norm2(src[i]); v > peak {
					peak = v
				}
			}
			thr := mc * peak
			for i := 0; i < n; i++ {
				// A component that carries no light at all has no phase either:
				// mask the whole map instead of painting a uniform zero.
				if peak <= 0 || i >= len(src) || norm2(src[i]) < thr {
					out[i] = math.NaN()
					continue
				}
				out[i] = math.Atan2(imag(src[i]), real(src[i]))
			}
		case "pol_intensity":
			return pp.Intensity(), info, idx, nil
		case "phase_u":
			mc := maskCut
			if mc <= 0 {
				mc = phaseMaskCut
			}
			out = pp.UnwrapPhase(mc)
			if out == nil {
				out = make([]float64, n)
			}
		case "pol_azimuth", "pol_ellip", "pol_s1", "pol_s2", "pol_s3", "pol_degree":
			// 先取一遍 Stokes 参数：低强度区域（无光）没有可定义偏振态，
			// 掩膜为 NaN，渲染时画成暗色，避免整幅图被“偏振角 0”的假色覆盖。
			specific := part >= 0 && part < len(pl.Parts)
			stokesAtPixel := func(i int) optics.Stokes {
				if specific {
					return pl.Parts[part].StokesAtPixel(i)
				}
				return totalStokes(pl, i)
			}
			cp := maskCut
			if cp <= 0 {
				cp = 1e-6
			}
			maxS0 := 0.0
			for i := 0; i < n; i++ {
				if s0 := stokesAtPixel(i).S0; s0 > maxS0 {
					maxS0 = s0
				}
			}
			cut := cp * maxS0
			for i := 0; i < n; i++ {
				st := stokesAtPixel(i)
				if maxS0 > 0 && st.S0 < cut {
					out[i] = math.NaN()
					continue
				}
				switch field {
				case "pol_azimuth":
					out[i] = st.Azimuth
				case "pol_ellip":
					out[i] = st.Ellipt
				case "pol_s1":
					if st.S0 > 0 {
						out[i] = st.S1 / st.S0
					}
				case "pol_s2":
					if st.S0 > 0 {
						out[i] = st.S2 / st.S0
					}
				case "pol_s3":
					if st.S0 > 0 {
						out[i] = st.S3 / st.S0
					}
				case "pol_degree":
					out[i] = st.Degree
				}
			}
		default:
			for i := 0; i < n; i++ {
				out[i] = pp.PhaseAt(i)
			}
		}
		return out, info, idx, nil
	}
	switch field {
	case "total":
		return pl.TotalIntensity(), info, -1, nil
	case "amplitude":
		vals := pl.TotalIntensity()
		for i := range vals {
			vals[i] = math.Sqrt(vals[i])
		}
		return vals, info, -1, nil
	}
	return nil, info, -1, fmt.Errorf("unknown field %q", field)
}

// autoRange picks a display range for a value set.
func autoRange(vals []float64, info viewInfo, scale string) (vmin, vmax float64) {
	mn, mx := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			continue
		}
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	switch info.kind {
	case kindPhaseWrapped:
		return -math.Pi, math.Pi
	case kindCyclic:
		return -math.Pi / 2, math.Pi / 2
	case kindUnit:
		return math.Min(mn, -1e-12), math.Max(mx, 1e-12)
	}
	if math.IsInf(mn, 1) || math.IsInf(mx, -1) {
		return 0, 1
	}
	if info.kind == kindIntensity || info.kind == kindAmplitude {
		mn = 0
	}
	if mx-mn < 1e-30 {
		mx = mn + 1
	}
	if scale == "log" && mx <= 0 {
		scale = "lin"
	}
	return mn, mx
}

// toneCurve maps a value through the display transfer (scale + gamma).
func toneCurve(v, vmin, vmax float64, scale string, gamma float64) float64 {
	if math.IsNaN(v) {
		return math.NaN()
	}
	if vmax <= vmin {
		vmax = vmin + 1
	}
	span := vmax - vmin
	var t float64
	if scale == "log" {
		const dyn = 1e5
		vp := (v - vmin) / span
		if vp < 0 {
			vp = 0
		}
		t = math.Log10(1+vp*(dyn-1)) / math.Log10(dyn)
	} else {
		t = (v - vmin) / span
	}
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	if gamma > 0 && gamma != 1 {
		t = math.Pow(t, gamma)
	}
	return t
}

// renderValues rasterizes a value slice with the requested look.
func renderValues(vals []float64, n int, info viewInfo, q url.Values) (*image.RGBA, error) {
	scale := q.Get("scale")
	if scale == "" {
		switch info.kind {
		case kindIntensity, kindAmplitude:
			scale = "log"
		default:
			scale = "lin"
		}
	}
	vmin, vmax := 0.0, 0.0
	if s := q.Get("pmin"); s != "" {
		if v, err := parseFloat(s); err == nil {
			vmin = v
		}
	}
	if s := q.Get("pmax"); s != "" {
		if v, err := parseFloat(s); err == nil {
			vmax = v
		}
	}
	avmin, avmax := autoRange(vals, info, scale)
	if vmax == vmin {
		vmin, vmax = avmin, avmax
	}
	gamma := 1.0
	if s := q.Get("gamma"); s != "" {
		if v, err := parseFloat(s); err == nil && v > 0 {
			gamma = v
		}
	}
	var lut [256]color.RGBA
	switch q.Get("cmap") {
	case "gray":
		lut = grayLUT
	case "inferno":
		lut = infernoLUT
	case "phase":
		lut = phaseLUT()
	case "diverging":
		lut = divergingLUT()
	default:
		switch info.kind {
		case kindCyclic:
			// The azimuth is defined modulo π (ψ and ψ+π are the same axis), so
			// it needs a wheel that wraps over π instead of the full 2π phase
			// wheel — otherwise the two ends of the range get different colours.
			lut = azimuthLUT()
		case kindPhaseWrapped, kindPhaseUnwrapped:
			lut = phaseLUT()
		case kindSigned, kindUnit:
			lut = divergingLUT()
		default:
			lut = infernoLUT
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for i := 0; i < n*n && i < len(vals); i++ {
		t := toneCurve(vals[i], vmin, vmax, scale, gamma)
		var c color.RGBA
		if math.IsNaN(t) {
			c = color.RGBA{18, 18, 22, 255} // masked (no light ⇒ no phase)
		} else {
			c = lut[int(t*255)]
		}
		img.SetRGBA(i%n, i/n, c)
	}
	return img, nil
}

// divergingLUT is a blue → light → red map for signed quantities (ellipticity,
// normalized Stokes parameters).
func divergingLUT() [256]color.RGBA {
	var out [256]color.RGBA
	for i := 0; i < 256; i++ {
		t := float64(i) / 255
		var r, g, b float64
		switch {
		case t < 0.5:
			u := t * 2 // 0 at blue, 1 at centre
			r = 0.15 * u
			g = 0.35 * u
			b = 0.55 + 0.35*u
		default:
			u := (t - 0.5) * 2 // 0 at centre, 1 at red
			r = 0.75 + 0.25*u
			g = 0.62 * (1 - u)
			b = 0.15 * (1 - u)
		}
		out[i] = color.RGBA{uint8(clamp255(r * 255)), uint8(clamp255(g * 255)), uint8(clamp255(b * 255)), 255}
	}
	return out
}

// renderColor rasterizes the plane as the light actually looks: every coherent
// unit contributes its own wavelength's colour, weighted by its intensity, and
// the summed intensity drives the brightness. With ?part=N only that unit is
// drawn.
func renderColor(pl *optics.Plane, q url.Values) (*image.RGBA, error) {
	n := pl.Size
	parts := pl.Parts
	if len(parts) == 0 {
		pp, _ := partField(pl, -1)
		parts = []optics.PlanePart{*pp}
	}
	only := -1
	if s := q.Get("part"); s != "" {
		if v, err := parseFloat(s); err == nil {
			only = int(v)
		}
	}
	type tint struct {
		r, g, b  float64
		intens   []float64
		visible  bool
		wl       float64
		fromPart int
	}
	var tints []tint
	for i := range parts {
		if only >= 0 && i != only {
			continue
		}
		r, g, b, vis := optics.WavelengthRGB(parts[i].Wavelength)
		tints = append(tints, tint{r: r, g: g, b: b, intens: parts[i].Intensity(), visible: vis, wl: parts[i].Wavelength, fromPart: i})
	}
	if len(tints) == 0 {
		return nil, fmt.Errorf("part %d out of range", only)
	}
	total := make([]float64, n*n)
	for i := range total {
		for _, t := range tints {
			if i < len(t.intens) {
				total[i] += t.intens[i]
			}
		}
	}
	scale := q.Get("scale")
	if scale == "" {
		scale = "lin"
	}
	gamma := 1 / 2.2
	if s := q.Get("gamma"); s != "" {
		if v, err := parseFloat(s); err == nil && v > 0 {
			gamma = v
		}
	}
	exposure := 1.0
	if s := q.Get("exposure"); s != "" {
		if v, err := parseFloat(s); err == nil && v > 0 {
			exposure = v
		}
	}
	info := viewInfo{kind: kindIntensity}
	avmin, avmax := autoRange(total, info, scale)
	vmin, vmax := 0.0, 0.0
	if s := q.Get("pmin"); s != "" {
		if v, err := parseFloat(s); err == nil {
			vmin = v
		}
	}
	if s := q.Get("pmax"); s != "" {
		if v, err := parseFloat(s); err == nil {
			vmax = v
		}
	}
	if vmax == vmin {
		vmin, vmax = avmin, avmax
	}
	if exposure != 1 {
		vmin, vmax = vmin/exposure, vmax/exposure
	}
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for i := 0; i < n*n; i++ {
		// Hue: the intensity-weighted mix of the contributing wavelengths.
		var r, g, b float64
		sum := 0.0
		for _, t := range tints {
			if i >= len(t.intens) {
				continue
			}
			w := math.Max(t.intens[i], 0)
			sum += w
			r += w * t.r
			g += w * t.g
			b += w * t.b
		}
		if sum > 0 {
			r, g, b = r/sum, g/sum, b/sum
		}
		t := toneCurve(total[i], vmin, vmax, scale, gamma)
		if math.IsNaN(t) {
			t = 0
		}
		img.SetRGBA(i%n, i/n, color.RGBA{
			uint8(clamp255(r * 255 * t)),
			uint8(clamp255(g * 255 * t)),
			uint8(clamp255(b * 255 * t)),
			255,
		})
	}
	return img, nil
}

// profileOfValues extracts a 1-D cut (3-pixel wide) from a value map.
func profileOfValues(vals []float64, n int, dx float64, axis string, coord *float64, cx, cy float64) (optics.Profile, error) {
	prof := optics.Profile{Axis: axis, X: make([]float64, n), V: make([]float64, n)}
	get := func(i int) float64 {
		if i < 0 || i >= len(vals) {
			return math.NaN()
		}
		return vals[i]
	}
	avg3 := func(i, j, k int) float64 {
		v, cnt := 0.0, 0
		for _, idx := range []int{i, j, k} {
			if x := get(idx); !math.IsNaN(x) {
				v += x
				cnt++
			}
		}
		if cnt == 0 {
			return math.NaN()
		}
		return v / float64(cnt)
	}
	switch axis {
	case "x":
		j0 := int(math.Round(cy/dx + float64(n)/2))
		if coord != nil {
			j0 = int(math.Round(*coord/dx + float64(n)/2))
		}
		j0 = clampInt(j0, 1, n-2)
		prof.Coord = (float64(j0) - float64(n)/2) * dx
		for i := 0; i < n; i++ {
			prof.X[i] = (float64(i) - float64(n)/2) * dx
			prof.V[i] = avg3((j0-1)*n+i, j0*n+i, (j0+1)*n+i)
		}
		return prof, nil
	case "y":
		i0 := int(math.Round(cx/dx + float64(n)/2))
		if coord != nil {
			i0 = int(math.Round(*coord/dx + float64(n)/2))
		}
		i0 = clampInt(i0, 1, n-2)
		prof.Coord = (float64(i0) - float64(n)/2) * dx
		for j := 0; j < n; j++ {
			prof.X[j] = (float64(j) - float64(n)/2) * dx
			prof.V[j] = avg3(j*n+i0-1, j*n+i0, j*n+i0+1)
		}
		return prof, nil
	}
	return prof, fmt.Errorf("profile axis must be x or y, got %q", axis)
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
