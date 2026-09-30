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

// azimuthLUT colours the polarization azimuth. Azimuth is defined modulo π (ψ
// and ψ+π describe the same axis), so the hue wheel must span exactly one π of
// azimuth: ψ = -π/2 and ψ = +π/2 then land on the same colour and the map has
// no seam at the wrap, unlike mapping the range onto the full 2π phase wheel.
func azimuthLUT() [256]color.RGBA {
	var out [256]color.RGBA
	for i := 0; i < 256; i++ {
		psi := (float64(i)/255 - 0.5) * math.Pi // -π/2 … +π/2
		h := 2 * psi * 180 / math.Pi            // -180° … +180°
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

// ---- encoding ---------------------------------------------------------------

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

// quantumChartDims returns the sanitized photon-number range (base) and mode
// count used to lay out the quantum charts. It clamps a nil or malformed
// QuantumResult so the renderers cannot panic (indexing short slices / a
// missing joint entry) or exhaust memory (an absurd Cutoff).
func quantumChartDims(res *optics.QuantumResult) (base, modes int) {
	if res == nil {
		return 1, 0
	}
	base = res.Cutoff + 1
	if base < 1 {
		base = 1
	}
	if base > 256 { // far beyond MaxQuantumCutoff; only reachable via library misuse
		base = 256
	}
	modes = res.Modes
	if modes < 0 {
		modes = 0
	}
	if modes > len(res.Dist) {
		modes = len(res.Dist)
	}
	return base, modes
}

// renderQuantumChart rasterizes a quantum result: the per-mode photon-number
// distributions (bar charts, one band per mode) stacked above the first joint
// distribution heatmap (log scale). No text labels (stdlib has no font); the
// layout is documented in docs/QUANTUM.md.
func renderQuantumChart(res *optics.QuantumResult) *image.RGBA {
	base, modes := quantumChartDims(res)
	const barW = 8
	const bandH = 72
	const cell = 10
	const pad = 6
	distW := base * barW
	jointSize := 0
	if modes >= 2 {
		jointSize = base * cell
	}
	width := distW
	if jointSize > width {
		width = jointSize
	}
	if width < 64 {
		width = 64
	}
	height := modes*bandH + jointSize + pad
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{10, 13, 18, 255}), image.Point{}, draw.Src)

	// Photon-number distribution bar charts (linear, self-normalized per mode).
	innerH := bandH - 4
	for m := 0; m < modes; m++ {
		dist := res.Dist[m]
		nlim := base
		if nlim > len(dist) {
			nlim = len(dist)
		}
		mx := 0.0
		for _, p := range dist {
			if p > mx {
				mx = p
			}
		}
		y0 := m * bandH
		for n := 0; n < nlim; n++ {
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
	if modes >= 2 {
		flat := res.Joint["0,1"]
		if len(flat) >= base*base {
			mx := 0.0
			for _, v := range flat {
				if v > mx {
					mx = v
				}
			}
			yOff := modes*bandH + pad
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
	base, modes := quantumChartDims(res)
	const barW = 8
	const bandH = 72
	const cell = 10
	const pad = 6
	distW := base * barW
	jointSize := 0
	if modes >= 2 {
		jointSize = base * cell
	}
	width := distW
	if jointSize > width {
		width = jointSize
	}
	if width < 64 {
		width = 64
	}
	height := modes*bandH + jointSize + pad
	var sb strings.Builder
	fmt.Fprintf(&sb, `<svg xmlns="http://www.w3.org/2000/svg" width="%d" height="%d" viewBox="0 0 %d %d">`, width, height, width, height)
	sb.WriteString(`<rect width="100%" height="100%" fill="#0a0d12"/>`)

	innerH := bandH - 4
	for m := 0; m < modes; m++ {
		dist := res.Dist[m]
		nlim := base
		if nlim > len(dist) {
			nlim = len(dist)
		}
		mx := 0.0
		for _, p := range dist {
			if p > mx {
				mx = p
			}
		}
		y0 := m * bandH
		for n := 0; n < nlim; n++ {
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

	if modes >= 2 {
		flat := res.Joint["0,1"]
		if len(flat) >= base*base {
			mx := 0.0
			for _, v := range flat {
				if v > mx {
					mx = v
				}
			}
			yOff := modes*bandH + pad
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
	}
	sb.WriteString("</svg>")
	return sb.String()
}

// RenderQuantumSVG writes a quantum result chart to an SVG file.
func RenderQuantumSVG(path string, res *optics.QuantumResult) error {
	return os.WriteFile(path, []byte(renderQuantumSVG(res)), 0o644)
}

// RenderPlanePNG writes one field view of a plane to a PNG file. field is any
// view name accepted by the plane endpoint (total/ex/ey/phase_x/phase_u/
// pol_azimuth/color/...), scale is lin or log and cmap is inferno, phase, gray,
// diverging. This is the kernel-level visualization helper; the web GUI fetches
// raw float32 or the same PNG through the HTTP API.
func RenderPlanePNG(path string, pl *optics.Plane, field, scale, cmap string) error {
	q := url.Values{}
	if scale != "" {
		q.Set("scale", scale)
	}
	if cmap != "" {
		q.Set("cmap", cmap)
	}
	var img *image.RGBA
	var err error
	if field == "color" || field == "" {
		img, err = renderColor(pl, q)
	} else {
		var vals []float64
		var info viewInfo
		vals, info, _, err = planeValues(pl, field, -1, 0)
		if err == nil {
			img, err = renderValues(vals, pl.Size, info, q)
		}
	}
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
