package optics

import (
	"fmt"
	"math"
)

// ShapeSpec describes the transverse outline ("clear aperture") of an optic:
// its kind and the parameters that size it. The same vocabulary is used by the
// aperture element (kind -> "shape" param) and by scene components (their
// clear aperture), so one catalog entry documents both.
//
// Convention: the signed distance d(u,v) is positive inside the outline and
// negative outside, so a mask is "open" where d >= 0. All lengths are meters in
// the optic's own local (u,v) axes.
type ShapeSpec struct {
	Kind   string         `json:"kind"`
	Params map[string]any `json:"params,omitempty"`
}

// ShapeKinds lists the supported outlines in catalog order.
var ShapeKinds = []string{
	"circle", "square", "rectangle", "ellipse", "triangle", "ring", "polygon",
	"double_slit", "cross", "star", "superellipse", "custom", "slit",
}

// shapeGeom is the parsed, validated form of a ShapeSpec.
type shapeGeom struct {
	kind      string
	radius    float64
	width     float64
	height    float64
	a         float64
	b         float64
	order     float64
	rin       float64
	rout      float64
	inner     float64
	length    float64
	sep       float64
	sides     int
	points    int
	rotation  float64
	edgeSigma float64
	verts     [][2]float64
}

// shapeDefaults are the fallback sizes used when a parameter is absent.
var shapeDefaults = map[string]float64{
	"radius": 1e-3, "width": 2e-3, "height": 2e-3, "a": 1e-3, "b": 2e-3,
	"order": 2, "rin": 5e-4, "rout": 1e-3, "inner": 5e-4, "length": 4e-3,
	"separation": 1e-3, "rotation": 0, "edge_sigma": 0,
}

// parseShapeGeom validates a shape definition. params carries the flat numeric
// parameters (the same keys an aperture element uses).
func parseShapeGeom(kind string, params map[string]any) (*shapeGeom, error) {
	if kind == "" {
		kind = "circle"
	}
	g := &shapeGeom{kind: kind}
	get := func(key string) float64 {
		if v, ok := params[key]; ok && v != nil {
			if x, err := asFloat(v); err == nil {
				return x
			}
		}
		return shapeDefaults[key]
	}
	g.radius = get("radius")
	g.width = get("width")
	g.height = get("height")
	g.a = get("a")
	g.b = get("b")
	g.order = get("order")
	g.rin = get("rin")
	g.rout = get("rout")
	g.inner = get("inner")
	g.length = get("length")
	g.sep = get("separation")
	g.rotation = get("rotation")
	g.edgeSigma = get("edge_sigma")
	g.sides = pi_(params, "sides", 6)
	g.points = pi_(params, "points", 5)

	switch kind {
	case "circle":
		if g.radius <= 0 {
			return nil, fmt.Errorf("shape: circle radius must be > 0")
		}
	case "slit":
		if g.width <= 0 {
			return nil, fmt.Errorf("shape: slit width must be > 0")
		}
		if g.height <= 0 {
			g.height = g.width / 1000
		}
	case "square":
		if g.width <= 0 {
			return nil, fmt.Errorf("shape: square width must be > 0")
		}
		g.height = g.width
	case "rectangle":
		if g.width <= 0 || g.height <= 0 {
			return nil, fmt.Errorf("shape: rectangle width and height must be > 0")
		}
	case "ellipse":
		if g.a <= 0 || g.b <= 0 {
			return nil, fmt.Errorf("shape: ellipse a and b must be > 0")
		}
	case "superellipse":
		if g.a <= 0 || g.b <= 0 || g.order < 0.1 {
			return nil, fmt.Errorf("shape: superellipse a,b > 0 and order >= 0.1")
		}
	case "triangle":
		if g.radius <= 0 {
			return nil, fmt.Errorf("shape: triangle radius must be > 0")
		}
		g.verts = regularPolygonVertices(3, g.radius, math.Pi/2)
	case "ring":
		if g.rout <= 0 || g.rin < 0 || g.rin >= g.rout {
			return nil, fmt.Errorf("shape: ring requires 0 <= rin < rout")
		}
	case "polygon":
		if g.radius <= 0 {
			return nil, fmt.Errorf("shape: polygon radius must be > 0")
		}
		if g.sides < 3 {
			return nil, fmt.Errorf("shape: polygon sides must be >= 3")
		}
		g.verts = regularPolygonVertices(g.sides, g.radius, math.Pi/float64(g.sides))
	case "double_slit":
		if g.width <= 0 || g.height <= 0 || g.sep <= 0 {
			return nil, fmt.Errorf("shape: double_slit width/height/separation must be > 0")
		}
	case "cross":
		if g.width <= 0 || g.length <= 0 {
			return nil, fmt.Errorf("shape: cross width and length must be > 0")
		}
	case "star":
		if g.radius <= 0 || g.inner <= 0 || g.inner >= g.radius {
			return nil, fmt.Errorf("shape: star requires 0 < inner < radius")
		}
		if g.points < 3 {
			return nil, fmt.Errorf("shape: star points must be >= 3")
		}
		g.verts = starVertices(g.points, g.radius, g.inner, math.Pi/2)
	case "custom":
		s, _ := params["vertices"].(string)
		vs, err := parseVertices(s)
		if err != nil {
			return nil, err
		}
		g.verts = vs
	default:
		return nil, fmt.Errorf("shape: unknown kind %q", kind)
	}
	return g, nil
}

// signedDistance returns the signed distance to the outline at the point
// (dx, dy) measured from the shape centre: positive inside, negative outside.
func (g *shapeGeom) signedDistance(dx, dy float64) float64 {
	rot := g.rotation
	cr, sr := math.Cos(rot), math.Sin(rot)
	u := cr*dx + sr*dy
	v := -sr*dx + cr*dy
	switch g.kind {
	case "circle":
		return g.radius - math.Hypot(dx, dy)
	case "slit":
		return math.Min(g.width/2-math.Abs(u), g.height/2-math.Abs(v))
	case "square":
		return math.Min(g.width/2-math.Abs(u), g.width/2-math.Abs(v))
	case "rectangle":
		return math.Min(g.width/2-math.Abs(u), g.height/2-math.Abs(v))
	case "ellipse":
		return (1 - math.Hypot(u/g.a, v/g.b)) * math.Min(g.a, g.b)
	case "triangle", "polygon", "star", "custom":
		return polygonSignedDistance(g.verts, u, v)
	case "ring":
		r := math.Hypot(dx, dy)
		return math.Min(r-g.rin, g.rout-r)
	case "double_slit":
		d1 := math.Min(g.height/2-math.Abs(v), g.width/2-math.Abs(u-g.sep/2))
		d2 := math.Min(g.height/2-math.Abs(v), g.width/2-math.Abs(u+g.sep/2))
		return math.Max(d1, d2)
	case "cross":
		hw := g.width / 2
		hl := g.length / 2
		return math.Max(math.Min(hw-math.Abs(u), hl-math.Abs(v)), math.Min(hl-math.Abs(u), hw-math.Abs(v)))
	case "superellipse":
		rn := math.Pow(math.Abs(u)/g.a, g.order) + math.Pow(math.Abs(v)/g.b, g.order)
		return (1 - rn) * math.Min(g.a, g.b)
	}
	return -1e300
}

// contains reports whether a local point lies inside the outline.
func (g *shapeGeom) contains(dx, dy float64) bool {
	return g.signedDistance(dx, dy) >= 0
}

// transmission returns the (edge-softened) amplitude transmission at a local
// point.
func (g *shapeGeom) transmission(dx, dy float64) complex128 {
	d := g.signedDistance(dx, dy)
	if g.edgeSigma > 0 {
		return smoothStep(d, g.edgeSigma)
	}
	return step(d)
}

// halfExtents returns the bounding half-size of the outline (m), used for
// drawing the optic in the layout view.
func (g *shapeGeom) halfExtents() (float64, float64) {
	switch g.kind {
	case "circle", "triangle", "polygon", "star", "custom":
		if g.kind == "circle" {
			return g.radius, g.radius
		}
		var hu, hv float64
		for _, p := range g.verts {
			hu = math.Max(hu, math.Abs(p[0]))
			hv = math.Max(hv, math.Abs(p[1]))
		}
		if hu == 0 {
			return g.radius, g.radius
		}
		return hu, hv
	case "slit":
		return g.width / 2, g.height / 2
	case "square":
		return g.width / 2, g.width / 2
	case "rectangle":
		return g.width / 2, g.height / 2
	case "double_slit":
		h := math.Max(g.height, g.sep+g.width)
		return math.Max(g.width/2, g.sep/2+g.width/2), h / 2
	case "ellipse", "superellipse":
		return g.a, g.b
	case "ring":
		return g.rout, g.rout
	case "cross":
		hu := math.Max(g.width, g.length) / 2
		return hu, hu
	}
	return g.radius, g.radius
}

// toParams flattens the shape into element parameters (as the aperture element
// expects them).
func (g *shapeGeom) toParams() map[string]any {
	p := map[string]any{
		"shape": g.kind, "radius": g.radius, "width": g.width, "height": g.height,
		"a": g.a, "b": g.b, "order": g.order, "rin": g.rin, "rout": g.rout,
		"inner": g.inner, "length": g.length, "separation": g.sep,
		"sides": g.sides, "points": g.points, "rotation": g.rotation,
		"edge_sigma": g.edgeSigma,
	}
	if len(g.verts) > 0 && g.kind == "custom" {
		s := ""
		for i, v := range g.verts {
			if i > 0 {
				s += ";"
			}
			s += fmt.Sprintf("%g,%g", v[0], v[1])
		}
		p["vertices"] = s
	}
	return p
}

// shapeFromSpec parses a ShapeSpec (used by scene components).
func shapeFromSpec(s *ShapeSpec) (*shapeGeom, error) {
	if s == nil {
		return nil, nil
	}
	return parseShapeGeom(s.Kind, s.Params)
}

// CircleOutline returns a circular clear aperture of radius r (m).
func CircleOutline(r float64) *ShapeSpec {
	return &ShapeSpec{Kind: "circle", Params: map[string]any{"radius": r}}
}

// RectOutline returns a rectangular clear aperture (m).
func RectOutline(w, h float64) *ShapeSpec {
	return &ShapeSpec{Kind: "rectangle", Params: map[string]any{"width": w, "height": h}}
}

// SlitOutline returns a rectangular slit of width w and height h (m).
func SlitOutline(w, h float64) *ShapeSpec {
	return &ShapeSpec{Kind: "slit", Params: map[string]any{"width": w, "height": h}}
}
