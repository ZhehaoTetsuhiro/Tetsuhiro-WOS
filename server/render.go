package server

import (
	"bufio"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"net/url"
	"os"
	"strings"

	"twos/optics"
)

// ---- colormaps -------------------------------------------------------------

type colormap struct {
	r, g, b []float64
}

func newColormap(stops [][3]float64) colormap {
	cm := colormap{r: make([]float64, len(stops)), g: make([]float64, len(stops)), b: make([]float64, len(stops))}
	for i, s := range stops {
		cm.r[i], cm.g[i], cm.b[i] = s[0], s[1], s[2]
	}
	return cm
}

// lut builds a 256-entry lookup table with linear interpolation.
func (cm colormap) lut() [256]color.RGBA {
	var out [256]color.RGBA
	n := len(cm.r) - 1
	for i := 0; i < 256; i++ {
		x := float64(i) / 255 * float64(n)
		k := int(x)
		if k >= n {
			k = n - 1
		}
		t := x - float64(k)
		r := cm.r[k] + (cm.r[k+1]-cm.r[k])*t
		g := cm.g[k] + (cm.g[k+1]-cm.g[k])*t
		b := cm.b[k] + (cm.b[k+1]-cm.b[k])*t
		out[i] = color.RGBA{uint8(clamp255(r)), uint8(clamp255(g)), uint8(clamp255(b)), 255}
	}
	return out
}

func clamp255(v float64) float64 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return v
}

// infernoLUT approximates matplotlib inferno (perceptually uniform).
var infernoLUT = newColormap([][3]float64{
	{0, 0, 4}, {55, 20, 115}, {139, 25, 98}, {203, 55, 74},
	{236, 101, 44}, {249, 156, 34}, {250, 200, 9}, {252, 255, 164},
}).lut()

// phaseLUT is a cyclic colormap for wrapped phase (-pi..pi).
func phaseLUT() [256]color.RGBA {
	var out [256]color.RGBA
	for i := 0; i < 256; i++ {
		h := 240 - 300*float64(i)/256 // blue -> red -> green -> blue wheel
		if h < 0 {
			h += 360
		}
		r, g, b := hsv2rgb(h, 0.85, 0.95)
		out[i] = color.RGBA{uint8(r * 255), uint8(g * 255), uint8(b * 255), 255}
	}
	return out
}

var grayLUT = func() [256]color.RGBA {
	var out [256]color.RGBA
	for i := 0; i < 256; i++ {
		out[i] = color.RGBA{uint8(i), uint8(i), uint8(i), 255}
	}
	return out
}()

func hsv2rgb(h, s, v float64) (float64, float64, float64) {
	c := v * s
	x := c * (1 - math.Abs(math.Mod(h/60, 2)-1))
	m := v - c
	var rp, gp, bp float64
	switch {
	case h < 60:
		rp, gp, bp = c, x, 0
	case h < 120:
		rp, gp, bp = x, c, 0
	case h < 180:
		rp, gp, bp = 0, c, x
	case h < 240:
		rp, gp, bp = 0, x, c
	case h < 300:
		rp, gp, bp = x, 0, c
	default:
		rp, gp, bp = c, 0, x
	}
	return rp + m, gp + m, bp + m
}

// ---- plane rendering -------------------------------------------------------

// renderPlane rasterizes one field view into an RGBA image using the
// requested colormap and scaling.
func renderPlane(pl *optics.Plane, get func(int) float64, q url.Values) (*image.RGBA, error) {
	n := pl.Size
	// Data range: explicit pmin/pmax, otherwise plane stats.
	var vmin, vmax float64
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
	field := q.Get("field")
	isPhase := field == "phase_x" || field == "phase_y" || field == "phase_z"
	if vmax == vmin {
		if isPhase {
			vmin, vmax = -math.Pi, math.Pi
		} else {
			vmin, vmax = pl.Stats.IntensityMin, pl.Stats.IntensityMax
		}
		if vmax <= vmin {
			vmax = vmin + 1
		}
	}
	scale := q.Get("scale")
	if scale == "" {
		if isPhase {
			scale = "lin"
		} else {
			scale = "log"
		}
	}
	cmap := q.Get("cmap")
	var lut [256]color.RGBA
	switch cmap {
	case "phase":
		lut = phaseLUT()
	case "gray":
		lut = grayLUT
	default:
		if isPhase {
			lut = phaseLUT()
		} else {
			lut = infernoLUT
		}
	}
	dyn := 1e4 // log dynamic range (4 decades below peak)
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	for j := 0; j < n; j++ {
		row := j * n
		for i := 0; i < n; i++ {
			v := get(row + i)
			var t float64
			if scale == "log" && !isPhase {
				vp := math.Max(v-vmin, 0) / (vmax - vmin)
				t = math.Log10(1+vp*(dyn-1)) / math.Log10(dyn)
			} else {
				t = (v - vmin) / (vmax - vmin)
			}
			if t < 0 {
				t = 0
			}
			if t > 1 {
				t = 1
			}
			c := lut[int(t*255)]
			img.SetRGBA(i, j, c)
		}
	}
	return img, nil
}

func pngEncode(w io.Writer, img image.Image) error {
	bw := bufio.NewWriter(w)
	if err := png.Encode(bw, img); err != nil {
		return err
	}
	return bw.Flush()
}

func infernoAt(t float64) color.RGBA {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return infernoLUT[int(t*255)]
}

// renderQuantumChart rasterizes a quantum result: the per-mode photon-number
// distributions (bar charts, one band per mode) stacked above the first joint
// distribution heatmap (log scale). No text labels (stdlib has no font); the
// layout is documented in docs/QUANTUM.md.
func renderQuantumChart(res *optics.QuantumResult) *image.RGBA {
	base := res.Cutoff + 1
	const barW = 8
	const bandH = 72
	const cell = 10
	const pad = 6
	distW := base * barW
	jointSize := 0
	if res.Modes >= 2 {
		jointSize = base * cell
	}
	width := distW
	if jointSize > width {
		width = jointSize
	}
	if width < 64 {
		width = 64
	}
	height := res.Modes*bandH + jointSize + pad
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{10, 13, 18, 255}), image.Point{}, draw.Src)

	// Photon-number distribution bar charts (linear, self-normalized per mode).
	innerH := bandH - 4
	for m := 0; m < res.Modes; m++ {
		dist := res.Dist[m]
		mx := 0.0
		for _, p := range dist {
			if p > mx {
				mx = p
			}
		}
		y0 := m * bandH
		for n := 0; n < base; n++ {
			t := 0.0
			if mx > 0 {
				t = dist[n] / mx
			}
			bh := int(t * float64(innerH))
			if bh < 0 {
				bh = 0
			}
			x0 := n * barW
			c := infernoAt(t)
			for y := 0; y < bh; y++ {
				for x := 0; x < barW-1; x++ {
					img.Set(x0+x, y0+innerH-y, c)
				}
			}
		}
	}

	// Joint distribution heatmap (log scale) for the first mode pair.
	if res.Modes >= 2 {
		flat := res.Joint["0,1"]
		mx := 0.0
		for _, v := range flat {
			if v > mx {
				mx = v
			}
		}
		yOff := res.Modes*bandH + pad
		const dyn = 1e4
		for b := 0; b < base; b++ {
			for a := 0; a < base; a++ {
				v := flat[a*base+b]
				t := 0.0
				if mx > 0 {
					vp := v / mx
					t = math.Log10(1+vp*(dyn-1)) / math.Log10(dyn)
				}
				for y := 0; y < cell; y++ {
					for x := 0; x < cell; x++ {
						img.Set(a*cell+x, yOff+b*cell+y, infernoAt(t))
					}
				}
			}
		}
	}
	return img
}

// RenderQuantumPNG writes a quantum result chart to a PNG file.
func RenderQuantumPNG(path string, res *optics.QuantumResult) error {
	img := renderQuantumChart(res)
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return pngEncode(f, img)
}

func svgHex(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}

// renderQuantumSVG renders a quantum result chart as an SVG string (vector
// bars for the photon distributions + a heatmap grid for the joint
// distribution). Layout mirrors renderQuantumChart.
func renderQuantumSVG(res *optics.QuantumResult) string {
	base := res.Cutoff + 1
	const barW = 8
	const bandH = 72
	const cell = 10
	const pad = 6
	distW := base * barW
	jointSize := 0
	if res.Modes >= 2 {
		jointSize = base * cell
	}
	width := distW
	if jointSize > width {
		width = jointSize
	}
	if width < 64 {
		width = 64
	}
	height := res.Modes*bandH + jointSize + pad
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height)
	sb.WriteString(`<rect width="100%" height="100%" fill="#0a0d12"/>`)

	innerH := bandH - 4
	for m := 0; m < res.Modes; m++ {
		dist := res.Dist[m]
		mx := 0.0
		for _, p := range dist {
			if p > mx {
				mx = p
			}
		}
		y0 := m * bandH
		for n := 0; n < base; n++ {
			t := 0.0
			if mx > 0 {
				t = dist[n] / mx
			}
			bh := int(t * float64(innerH))
			if bh <= 0 {
				continue
			}
			x := n * barW
			y := y0 + innerH - bh
			fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`, x, y, barW-1, bh, svgHex(infernoAt(t)))
		}
	}

	if res.Modes >= 2 {
		flat := res.Joint["0,1"]
		mx := 0.0
		for _, v := range flat {
			if v > mx {
				mx = v
			}
		}
		yOff := res.Modes*bandH + pad
		const dyn = 1e4
		for b := 0; b < base; b++ {
			for a := 0; a < base; a++ {
				v := flat[a*base+b]
				t := 0.0
				if mx > 0 {
					vp := v / mx
					t = math.Log10(1+vp*(dyn-1)) / math.Log10(dyn)
				}
				fmt.Fprintf(&sb, `<rect x="%d" y="%d" width="%d" height="%d" fill="%s"/>`, a*cell, yOff+b*cell, cell, cell, svgHex(infernoAt(t)))
			}
		}
	}
	sb.WriteString("</svg>")
	return sb.String()
}

// RenderQuantumSVG writes a quantum result chart to an SVG file.
func RenderQuantumSVG(path string, res *optics.QuantumResult) error {
	return os.WriteFile(path, []byte(renderQuantumSVG(res)), 0o644)
}

// RenderPlanePNG writes one field view of a plane to a PNG file. field is
// one of total/ex/ey/phase_x/phase_y, scale is lin or log, cmap is inferno,
// phase or gray. This is the kernel-level visualization helper; the web GUI
// fetches raw float32 instead and renders client-side.
func RenderPlanePNG(path string, pl *optics.Plane, field, scale, cmap string) error {
	get := fieldGetter(pl, field)
	if get == nil {
		return fmt.Errorf("unknown field %q", field)
	}
	q := url.Values{"field": []string{field}}
	if scale != "" {
		q.Set("scale", scale)
	}
	if cmap != "" {
		q.Set("cmap", cmap)
	}
	img, err := renderPlane(pl, get, q)
	if err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return pngEncode(f, img)
}
