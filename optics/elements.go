package optics

import (
	"fmt"
	"math"
	"math/rand"
	"strconv"
	"strings"
	"sync"
)

// ElementSpec is the JSON-serializable description of one optical element.
type ElementSpec struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// Element is a thin optical element: it multiplies the field by a position
// dependent complex transmission (and possibly a Jones matrix) at one plane.
// Structural elements (propagate, sensor, beamsplitter, combiner, mirror)
// are handled by the simulator; mirror also implements Apply for its phase.
type Element interface {
	Apply(f *Field, ctx *Context) error
}

// ElementFactory builds an element from its raw JSON params.
type ElementFactory func(params map[string]any) (Element, error)

var elementRegistry = map[string]ElementFactory{}

// RegisterElement makes a custom element type available to the simulator and
// to the catalog-driven GUI. See docs/KERNEL.md for the extension recipe.
func RegisterElement(name string, factory ElementFactory) {
	elementRegistry[name] = factory
}

// RegisteredElements lists the names of all registered element types.
func RegisteredElements() []string {
	out := make([]string, 0, len(elementRegistry))
	for k := range elementRegistry {
		out = append(out, k)
	}
	return out
}

// NewElement instantiates an element from its spec.
func NewElement(spec ElementSpec) (Element, error) {
	fac, ok := elementRegistry[spec.Type]
	if !ok {
		return nil, fmt.Errorf("unknown element type %q", spec.Type)
	}
	return fac(spec.Params)
}

// ---- parameter helpers ------------------------------------------------------

func pf(p map[string]any, key string, def float64) (float64, error) {
	v, ok := p[key]
	if !ok || v == nil {
		return def, nil
	}
	return asFloat(v)
}

func pfd(p map[string]any, key string, def float64) float64 {
	v, err := pf(p, key, def)
	if err != nil {
		return def
	}
	return v
}

func pi_(p map[string]any, key string, def int) int {
	v, ok := p[key]
	if !ok || v == nil {
		return def
	}
	if f, err := asFloat(v); err == nil {
		return int(f)
	}
	return def
}

func ps(p map[string]any, key, def string) string {
	if v, ok := p[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return def
}

// applyMask multiplies both components by t(i,j) at every pixel.
func applyMask(f *Field, t func(i, j int) complex128) {
	n := f.N
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			idx := j*n + i
			v := t(i, j)
			f.Ex[idx] *= v
			if f.Polarized {
				f.Ey[idx] *= v
			}
		}
	}
}

// step returns 1 for d >= 0 else 0 (hard edge).
func step(d float64) complex128 {
	if d >= 0 {
		return 1
	}
	return 0
}

// smoothStep softens an edge over width sigma: 0.5*(1+erf(d/(sqrt2 sigma))).
func smoothStep(d, sigma float64) complex128 {
	return complex(0.5*(1+math.Erf(d/(math.Sqrt2*sigma))), 0)
}

func init() {
	RegisterElement("lens", newLens)
	RegisterElement("aperture", newAperture)
	RegisterElement("apodizer", newApodizer)
	RegisterElement("grating", newGrating)
	RegisterElement("axicon", newAxicon)
	RegisterElement("spiral_phase", newSpiral)
	RegisterElement("wedge", newWedge)
	RegisterElement("zone_plate", newZonePlate)
	RegisterElement("diffuser", newDiffuser)
	RegisterElement("mirror", newMirror)
	RegisterElement("concave_mirror", newConcaveMirror)
	RegisterElement("convex_mirror", newConvexMirror)
	RegisterElement("zernike", newZernike)
	RegisterElement("polarizer", newPolarizer)
	RegisterElement("retarder", newRetarder)
	RegisterElement("rotator", newRotator)
	RegisterElement("custom_jones", newCustomJones)
	RegisterElement("uniaxial", newUniaxial)
	RegisterElement("medium", newMedium)
	RegisterElement("biaxial", newBiaxial)
}

// ---- lenses ----------------------------------------------------------------

type lensEl struct {
	f, aperture, x0, y0 float64
}

func newLens(p map[string]any) (Element, error) {
	f, err := pf(p, "f", 0.1)
	if err != nil || f == 0 {
		return nil, fmt.Errorf("lens: focal length f must be non-zero")
	}
	return &lensEl{f: f, aperture: pfd(p, "aperture", 0), x0: pfd(p, "x", 0), y0: pfd(p, "y", 0)}, nil
}

func (e *lensEl) Apply(f *Field, ctx *Context) error {
	k := 2 * math.Pi / ctx.Wavelength
	c := -k / (2 * e.f)
	ap := e.aperture
	n := f.N
	for j := 0; j < n; j++ {
		dy := f.Y(j) - e.y0
		for i := 0; i < n; i++ {
			dx := f.X(i) - e.x0
			r2 := dx*dx + dy*dy
			if ap > 0 && r2 > ap*ap {
				f.Ex[j*n+i] = 0
				if f.Polarized {
					f.Ey[j*n+i] = 0
				}
				continue
			}
			t := cexpI(c * r2)
			f.Ex[j*n+i] *= t
			if f.Polarized {
				f.Ey[j*n+i] *= t
			}
		}
	}
	return nil
}

// ---- apertures -------------------------------------------------------------

type apertureEl struct {
	shape      string
	radius     float64
	width      float64
	height     float64
	a          float64
	b          float64
	order      float64
	rin        float64
	rout       float64
	sides      int
	points     int
	inner      float64
	length     float64
	rotation   float64
	separation float64
	x0, y0     float64
	edgeSigma  float64

	// verts holds precomputed polygon vertices for triangle/polygon/star/custom.
	verts [][2]float64
}

func newAperture(p map[string]any) (Element, error) {
	e := &apertureEl{
		shape:      ps(p, "shape", "circle"),
		radius:     pfd(p, "radius", 0.001),
		width:      pfd(p, "width", 0.002),
		height:     pfd(p, "height", 0.002),
		a:          pfd(p, "a", 0.001),
		b:          pfd(p, "b", 0.002),
		order:      pfd(p, "order", 2),
		rin:        pfd(p, "rin", 0.0005),
		rout:       pfd(p, "rout", 0.001),
		sides:      pi_(p, "sides", 6),
		points:     pi_(p, "points", 5),
		inner:      pfd(p, "inner", 0.0005),
		length:     pfd(p, "length", 0.004),
		rotation:   pfd(p, "rotation", 0),
		separation: pfd(p, "separation", 0.001),
		x0:         pfd(p, "x", 0),
		y0:         pfd(p, "y", 0),
		edgeSigma:  pfd(p, "edge_sigma", 0),
	}
	switch e.shape {
	case "circle", "square", "rectangle", "ellipse", "triangle", "ring", "polygon", "double_slit", "cross", "star", "superellipse", "custom":
	default:
		return nil, fmt.Errorf("aperture: unknown shape %q", e.shape)
	}
	switch e.shape {
	case "circle":
		if e.radius <= 0 {
			return nil, fmt.Errorf("aperture: circle radius must be > 0")
		}
	case "square":
		if e.width <= 0 {
			return nil, fmt.Errorf("aperture: square width must be > 0")
		}
	case "rectangle":
		if e.width <= 0 || e.height <= 0 {
			return nil, fmt.Errorf("aperture: rectangle width and height must be > 0")
		}
	case "ellipse":
		if e.a <= 0 || e.b <= 0 {
			return nil, fmt.Errorf("aperture: ellipse a and b must be > 0")
		}
	case "superellipse":
		if e.a <= 0 || e.b <= 0 || e.order < 0.1 {
			return nil, fmt.Errorf("aperture: superellipse a,b > 0 and order >= 0.1")
		}
	case "triangle":
		if e.radius <= 0 {
			return nil, fmt.Errorf("aperture: triangle radius must be > 0")
		}
		e.verts = regularPolygonVertices(3, e.radius, math.Pi/2)
	case "ring":
		if e.rout <= 0 || e.rin < 0 || e.rin >= e.rout {
			return nil, fmt.Errorf("aperture: ring requires 0 <= rin < rout")
		}
	case "polygon":
		if e.radius <= 0 {
			return nil, fmt.Errorf("aperture: polygon radius must be > 0")
		}
		if e.sides < 3 {
			return nil, fmt.Errorf("aperture: polygon sides must be >= 3")
		}
		e.verts = regularPolygonVertices(e.sides, e.radius, math.Pi/float64(e.sides))
	case "double_slit":
		if e.width <= 0 || e.height <= 0 || e.separation <= 0 {
			return nil, fmt.Errorf("aperture: double_slit width/height/separation must be > 0")
		}
	case "cross":
		if e.width <= 0 || e.length <= 0 {
			return nil, fmt.Errorf("aperture: cross width and length must be > 0")
		}
	case "star":
		if e.radius <= 0 || e.inner <= 0 || e.inner >= e.radius {
			return nil, fmt.Errorf("aperture: star requires 0 < inner < radius")
		}
		if e.points < 3 {
			return nil, fmt.Errorf("aperture: star points must be >= 3")
		}
		e.verts = starVertices(e.points, e.radius, e.inner, math.Pi/2)
	case "custom":
		vs, err := parseVertices(ps(p, "vertices", ""))
		if err != nil {
			return nil, err
		}
		e.verts = vs
	}
	return e, nil
}

func (e *apertureEl) Apply(f *Field, ctx *Context) error {
	n := f.N
	rot := e.rotation
	cr, sr := math.Cos(rot), math.Sin(rot)
	sig := e.edgeSigma
	for j := 0; j < n; j++ {
		dy := f.Y(j) - e.y0
		for i := 0; i < n; i++ {
			dx := f.X(i) - e.x0
			u := cr*dx + sr*dy
			v := -sr*dx + cr*dy
			var d float64
			switch e.shape {
			case "circle":
				d = e.radius - math.Hypot(dx, dy)
			case "square":
				d = math.Min(e.width/2-math.Abs(u), e.width/2-math.Abs(v))
			case "rectangle":
				d = math.Min(e.width/2-math.Abs(u), e.height/2-math.Abs(v))
			case "ellipse":
				d = (1 - math.Hypot(u/e.a, v/e.b)) * math.Min(e.a, e.b)
			case "triangle", "polygon", "star", "custom":
				d = polygonSignedDistance(e.verts, u, v)
			case "ring":
				r := math.Hypot(dx, dy)
				d = math.Min(r-e.rin, e.rout-r)
			case "double_slit":
				d1 := math.Min(e.height/2-math.Abs(v), e.width/2-math.Abs(u-e.separation/2))
				d2 := math.Min(e.height/2-math.Abs(v), e.width/2-math.Abs(u+e.separation/2))
				d = math.Max(d1, d2)
			case "cross":
				hw := e.width / 2
				hl := e.length / 2
				d = math.Max(math.Min(hw-math.Abs(u), hl-math.Abs(v)), math.Min(hl-math.Abs(u), hw-math.Abs(v)))
			case "superellipse":
				rn := math.Pow(math.Abs(u)/e.a, e.order) + math.Pow(math.Abs(v)/e.b, e.order)
				d = (1 - rn) * math.Min(e.a, e.b)
			}
			var t complex128
			if sig > 0 {
				t = smoothStep(d, sig)
			} else {
				t = step(d)
			}
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- polygon geometry helpers ----------------------------------------------

// regularPolygonVertices returns n vertices of a regular polygon inscribed in
// a circle of the given radius, with the first vertex at angle phi0.
func regularPolygonVertices(n int, radius, phi0 float64) [][2]float64 {
	out := make([][2]float64, n)
	for i := 0; i < n; i++ {
		a := phi0 + 2*math.Pi*float64(i)/float64(n)
		out[i] = [2]float64{radius * math.Cos(a), radius * math.Sin(a)}
	}
	return out
}

// starVertices returns the 2n vertices of a regular star polygon alternating
// between outer radius and inner radius (a classic pointed star).
func starVertices(n int, outerR, innerR, phi0 float64) [][2]float64 {
	out := make([][2]float64, 2*n)
	for i := 0; i < 2*n; i++ {
		a := phi0 + math.Pi*float64(i)/float64(n)
		r := outerR
		if i%2 == 1 {
			r = innerR
		}
		out[i] = [2]float64{r * math.Cos(a), r * math.Sin(a)}
	}
	return out
}

// pointInPolygon reports whether (x,y) lies inside the closed polygon using
// the ray-casting test (handles concave and star-shaped polygons).
func pointInPolygon(pts [][2]float64, x, y float64) bool {
	n := len(pts)
	if n < 3 {
		return false
	}
	inside := false
	j := n - 1
	for i := 0; i < n; i++ {
		xi, yi := pts[i][0], pts[i][1]
		xj, yj := pts[j][0], pts[j][1]
		if (yi > y) != (yj > y) {
			xint := (xj-xi)*(y-yi)/(yj-yi) + xi
			if x < xint {
				inside = !inside
			}
		}
		j = i
	}
	return inside
}

// distToSegment returns the Euclidean distance from (px,py) to segment ab.
func distToSegment(px, py, ax, ay, bx, by float64) float64 {
	abx, aby := bx-ax, by-ay
	apx, apy := px-ax, py-ay
	t := (apx*abx + apy*aby) / (abx*abx + aby*aby)
	if t < 0 {
		t = 0
	} else if t > 1 {
		t = 1
	}
	return math.Hypot(px-(ax+t*abx), py-(ay+t*aby))
}

// polygonSignedDistance returns the signed distance to a polygon boundary:
// positive inside, negative outside (0 on the boundary).
func polygonSignedDistance(pts [][2]float64, x, y float64) float64 {
	n := len(pts)
	if n < 3 {
		return -1e300
	}
	d := math.Inf(1)
	for i := 0; i < n; i++ {
		a, b := pts[i], pts[(i+1)%n]
		if dd := distToSegment(x, y, a[0], a[1], b[0], b[1]); dd < d {
			d = dd
		}
	}
	if !pointInPolygon(pts, x, y) {
		d = -d
	}
	return d
}

// parseVertices parses a "x,y;x,y;..." string into polygon vertices.
func parseVertices(s string) ([][2]float64, error) {
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return r == ';' || r == '\n' || r == '\r'
	})
	out := make([][2]float64, 0, len(fields))
	for _, fld := range fields {
		nums := strings.FieldsFunc(fld, func(r rune) bool {
			return r == ',' || r == ' ' || r == '\t'
		})
		if len(nums) == 0 {
			continue
		}
		if len(nums) != 2 {
			return nil, fmt.Errorf("aperture: custom vertex %q must be x,y", fld)
		}
		x, err1 := strconv.ParseFloat(strings.TrimSpace(nums[0]), 64)
		y, err2 := strconv.ParseFloat(strings.TrimSpace(nums[1]), 64)
		if err1 != nil || err2 != nil {
			return nil, fmt.Errorf("aperture: invalid custom vertex %q", fld)
		}
		out = append(out, [2]float64{x, y})
	}
	if len(out) < 3 {
		return nil, fmt.Errorf("aperture: custom shape needs at least 3 vertices")
	}
	return out, nil
}

// ---- apodizer --------------------------------------------------------------

type apodizerEl struct {
	waist, amp, x0, y0 float64
}

func newApodizer(p map[string]any) (Element, error) {
	w := pfd(p, "waist", 0.001)
	if w <= 0 {
		return nil, fmt.Errorf("apodizer: waist must be > 0")
	}
	return &apodizerEl{waist: w, amp: pfd(p, "amplitude", 1), x0: pfd(p, "x", 0), y0: pfd(p, "y", 0)}, nil
}

func (e *apodizerEl) Apply(f *Field, ctx *Context) error {
	n := f.N
	for j := 0; j < n; j++ {
		dy := f.Y(j) - e.y0
		for i := 0; i < n; i++ {
			dx := f.X(i) - e.x0
			t := complex(e.amp*math.Exp(-(dx*dx+dy*dy)/(e.waist*e.waist)), 0)
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- gratings --------------------------------------------------------------

type gratingEl struct {
	kind                      string
	period, modulation, duty  float64
	blazeDepth, rotation, off float64
}

func newGrating(p map[string]any) (Element, error) {
	e := &gratingEl{
		kind:       ps(p, "kind", "amplitude_sin"),
		period:     pfd(p, "period", 1e-4),
		modulation: pfd(p, "modulation", 1),
		duty:       pfd(p, "duty", 0.5),
		blazeDepth: pfd(p, "blaze_depth", math.Pi),
		rotation:   pfd(p, "rotation", 0),
		off:        pfd(p, "offset", 0),
	}
	if e.period <= 0 {
		return nil, fmt.Errorf("grating: period must be > 0")
	}
	if e.duty < 0 || e.duty > 1 {
		return nil, fmt.Errorf("grating: duty must be in [0,1]")
	}
	switch e.kind {
	case "amplitude_sin", "amplitude_binary", "phase_sin", "phase_binary", "blazed":
	default:
		return nil, fmt.Errorf("grating: unknown kind %q", e.kind)
	}
	return e, nil
}

func (e *gratingEl) Apply(f *Field, ctx *Context) error {
	n := f.N
	c, s := math.Cos(e.rotation), math.Sin(e.rotation)
	u := func(i, j int) float64 { return c*f.X(i) + s*f.Y(j) }
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			uu := u(i, j)
			ph := 2 * math.Pi * uu / e.period
			var t complex128
			switch e.kind {
			case "amplitude_sin":
				// 0..1 cosine transmission with contrast m.
				t = complex(0.5*(1+e.modulation*math.Cos(ph+e.off)), 0)
			case "amplitude_binary":
				if math.Cos(ph+e.off) >= math.Cos(math.Pi*e.duty) {
					t = 1
				}
			case "phase_sin":
				// Raman-Nath thin phase grating, m = peak-to-peak phase depth.
				t = cexpI(e.modulation / 2 * math.Sin(ph+e.off))
			case "phase_binary":
				if math.Cos(ph+e.off) >= 0 {
					t = cexpI(e.modulation)
				} else {
					t = 1
				}
			case "blazed":
				// Ideal sawtooth phase, depth blaze_depth over one period.
				frac := math.Mod(uu/e.period, 1)
				if frac < 0 {
					frac += 1
				}
				t = cexpI(e.blazeDepth * frac)
			}
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- axicon ----------------------------------------------------------------

type axiconEl struct{ alpha, index float64 }

func newAxicon(p map[string]any) (Element, error) {
	return &axiconEl{alpha: pfd(p, "alpha", 0.02), index: pfd(p, "index", 1.5)}, nil
}

func (e *axiconEl) Apply(f *Field, ctx *Context) error {
	k := 2 * math.Pi / ctx.Wavelength
	c := -k * (e.index - 1) * math.Tan(e.alpha)
	n := f.N
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			t := cexpI(c * math.Hypot(f.X(i), y))
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- spiral phase plate (optical vortex) -----------------------------------

type spiralEl struct {
	charge int
	f      float64
	x0, y0 float64
}

func newSpiral(p map[string]any) (Element, error) {
	return &spiralEl{charge: pi_(p, "charge", 1), f: pfd(p, "f", 0), x0: pfd(p, "x", 0), y0: pfd(p, "y", 0)}, nil
}

func (e *spiralEl) Apply(f *Field, ctx *Context) error {
	k := 2 * math.Pi / ctx.Wavelength
	n := f.N
	var c float64
	if e.f != 0 {
		c = -k / (2 * e.f)
	}
	for j := 0; j < n; j++ {
		dy := f.Y(j) - e.y0
		for i := 0; i < n; i++ {
			dx := f.X(i) - e.x0
			ph := float64(e.charge) * math.Atan2(dy, dx)
			if c != 0 {
				ph += c * (dx*dx + dy*dy)
			}
			t := cexpI(ph)
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- wedge / prism ---------------------------------------------------------

type wedgeEl struct{ alpha, index, rotation float64 }

func newWedge(p map[string]any) (Element, error) {
	return &wedgeEl{alpha: pfd(p, "alpha", 0.01), index: pfd(p, "index", 1.5), rotation: pfd(p, "rotation", 0)}, nil
}

func (e *wedgeEl) Apply(f *Field, ctx *Context) error {
	k := 2 * math.Pi / ctx.Wavelength
	c := -k * (e.index - 1) * math.Tan(e.alpha)
	n := f.N
	cr, sr := math.Cos(e.rotation), math.Sin(e.rotation)
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			t := cexpI(c * (cr*f.X(i) + sr*y))
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- Fresnel zone plate ----------------------------------------------------

type zonePlateEl struct {
	focal  float64
	radius float64
	kind   string
}

func newZonePlate(p map[string]any) (Element, error) {
	e := &zonePlateEl{focal: pfd(p, "f", 0.1), radius: pfd(p, "radius", 0.01), kind: ps(p, "kind", "phase")}
	if e.focal <= 0 {
		return nil, fmt.Errorf("zone_plate: f must be > 0")
	}
	if e.kind != "phase" && e.kind != "amplitude" {
		return nil, fmt.Errorf("zone_plate: unknown kind %q", e.kind)
	}
	return e, nil
}

func (e *zonePlateEl) Apply(f *Field, ctx *Context) error {
	wl := ctx.Wavelength
	n := f.N
	// Zone radii r_m^2 = m*lambda*f + (m*lambda/2)^2 (exact point-to-point).
	maxR2 := e.radius * e.radius
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			r2 := f.X(i)*f.X(i) + y*y
			if r2 > maxR2 {
				continue
			}
			// Zone index from r_m^2 = m*lambda*f + (m*lambda/2)^2:
			// m = 2*(sqrt(f^2 + r^2) - f) / lambda.
			r := math.Sqrt(r2)
			m := int(math.Floor(2*(math.Sqrt(e.focal*e.focal+r*r)-e.focal)/wl + 0.5))
			var t complex128
			if e.kind == "phase" {
				if m%2 == 0 {
					t = 1
				} else {
					t = cexpI(math.Pi) // pi phase shift
				}
			} else { // amplitude zone plate: alternate opaque/clear
				if m%2 == 0 {
					t = 1
				} else {
					t = 0
				}
			}
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- random phase diffuser -------------------------------------------------

type diffuserEl struct {
	sigma, corr, amp float64
	seed             int64
}

func newDiffuser(p map[string]any) (Element, error) {
	return &diffuserEl{
		sigma: pfd(p, "sigma", math.Pi),
		corr:  pfd(p, "correlation", 2e-5),
		amp:   pfd(p, "amplitude", 1),
		seed:  int64(pfd(p, "seed", 1)),
	}, nil
}

func (e *diffuserEl) Apply(f *Field, ctx *Context) error {
	n := f.N
	phase := make([]float64, n*n)
	rng := rand.New(rand.NewSource(e.seed))
	for i := range phase {
		phase[i] = rng.Float64()*2*math.Pi - math.Pi
	}
	if e.corr > 0 {
		// Smooth the white noise in the frequency domain: multiply by
		// exp(-2 pi^2 lc^2 f^2) -> Gaussian correlation length lc.
		z := make([]complex128, n*n)
		for i, v := range phase {
			z[i] = complex(v, 0)
		}
		fft2D(z, n, false)
		for j := 0; j < n; j++ {
			fy := f.freq(j)
			for i := 0; i < n; i++ {
				fx := f.freq(i)
				f2 := fx*fx + fy*fy
				z[j*n+i] *= complex(math.Exp(-2*math.Pi*math.Pi*e.corr*e.corr*f2), 0)
			}
		}
		fft2D(z, n, true)
		for i, v := range z {
			phase[i] = real(v)
		}
	}
	// Rescale to the requested standard deviation.
	var m, s2 float64
	for _, v := range phase {
		m += v
		s2 += v * v
	}
	m /= float64(len(phase))
	s2 = s2/float64(len(phase)) - m*m
	if s2 > 0 {
		sc := e.sigma / math.Sqrt(s2)
		for i, v := range phase {
			phase[i] = (v - m) * sc
		}
	}
	amp := e.amp
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			t := complex(amp, 0) * cexpI(phase[j*n+i])
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- mirror ----------------------------------------------------------------

type mirrorEl struct {
	reflectivity, curvature, tiltX, tiltY float64
}

func newMirror(p map[string]any) (Element, error) {
	e := &mirrorEl{
		reflectivity: pfd(p, "reflectivity", 1),
		curvature:    pfd(p, "curvature", 0),
		tiltX:        pfd(p, "tilt_x", 0),
		tiltY:        pfd(p, "tilt_y", 0),
	}
	if e.reflectivity < 0 || e.reflectivity > 1 {
		return nil, fmt.Errorf("mirror: reflectivity must be in [0,1]")
	}
	return e, nil
}

// Apply implements the mirror phase: reflection at a spherical mirror of
// radius R = 1/curvature adds phase -k r^2/R (equivalent to a lens of focal
// length R/2), tilt deflects the beam by twice the mirror tilt angle.
// The propagation-direction flip itself is handled by the simulator.
func (e *mirrorEl) Apply(f *Field, ctx *Context) error {
	k := 2 * math.Pi / ctx.Wavelength
	c := -k * e.curvature
	d := -2 * k
	n := f.N
	r := complex(e.reflectivity, 0)
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			x := f.X(i)
			t := r * cexpI(c*(x*x+y*y)+d*(e.tiltX*x+e.tiltY*y))
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

// ---- spherical mirrors (concave / convex, adjustable radius of curvature) --

type sphericalMirrorEl struct {
	radius       float64 // radius of curvature R (m), > 0
	convex       bool    // true = convex (diverging); false = concave (converging)
	reflectivity float64
	aperture     float64
	x0, y0       float64
}

func newConcaveMirror(p map[string]any) (Element, error) {
	return newSphericalMirror(p, false)
}

func newConvexMirror(p map[string]any) (Element, error) {
	return newSphericalMirror(p, true)
}

func newSphericalMirror(p map[string]any, convex bool) (Element, error) {
	e := &sphericalMirrorEl{
		radius:       pfd(p, "radius", 0.5),
		convex:       convex,
		reflectivity: pfd(p, "reflectivity", 1),
		aperture:     pfd(p, "aperture", 0),
		x0:           pfd(p, "x", 0),
		y0:           pfd(p, "y", 0),
	}
	if e.radius <= 0 {
		return nil, fmt.Errorf("mirror: radius must be > 0")
	}
	if e.reflectivity < 0 || e.reflectivity > 1 {
		return nil, fmt.Errorf("mirror: reflectivity must be in [0,1]")
	}
	return e, nil
}

// Apply implements the spherical-mirror phase. A concave mirror (R > 0) is a
// converging reflector with focal length f = R/2 (phase -k r²/R); a convex
// mirror is diverging (phase +k r²/R). reflectivity is the amplitude
// reflectance and aperture is an optional circular pupil.
func (e *sphericalMirrorEl) Apply(f *Field, ctx *Context) error {
	k := 2 * math.Pi / ctx.Wavelength
	c := -k / e.radius
	if e.convex {
		c = k / e.radius
	}
	rr := complex(e.reflectivity, 0)
	ap := e.aperture
	n := f.N
	for j := 0; j < n; j++ {
		dy := f.Y(j) - e.y0
		for i := 0; i < n; i++ {
			dx := f.X(i) - e.x0
			r2 := dx*dx + dy*dy
			if ap > 0 && r2 > ap*ap {
				f.Ex[j*n+i] = 0
				if f.Polarized {
					f.Ey[j*n+i] = 0
				}
				continue
			}
			t := rr * cexpI(c*r2)
			f.Ex[j*n+i] *= t
			if f.Polarized {
				f.Ey[j*n+i] *= t
			}
		}
	}
	return nil
}

// ---- Zernike phase plate (wavefront aberrations) ---------------------------

type zernikeEl struct {
	coef [22]float64 // index 1..21, units of waves
	norm float64     // normalization radius
}

func newZernike(p map[string]any) (Element, error) {
	e := &zernikeEl{norm: pfd(p, "radius", 0.01)}
	if e.norm <= 0 {
		return nil, fmt.Errorf("zernike: radius must be > 0")
	}
	for j := 1; j <= 21; j++ {
		e.coef[j] = pfd(p, fmt.Sprintf("c%d", j), 0)
	}
	return e, nil
}

// nollNM maps the Noll index (1..21) to (n, m); m > 0 uses cos(m theta),
// m < 0 uses sin(|m| theta).
var nollNM = [22][2]int{
	{0, 0}, {0, 0}, {1, 1}, {1, -1}, {2, 0}, {2, -2}, {2, 2}, {3, -1}, {3, 1},
	{3, -3}, {3, 3}, {4, 0}, {4, 2}, {4, -2}, {4, 4}, {4, -4}, {5, 1}, {5, -1},
	{5, 3}, {5, -3}, {5, 5}, {5, -5},
}

// radialZernike returns R_n^m(rho) for m >= 0.
func radialZernike(n, m int, rho float64) float64 {
	var s float64
	for k := 0; k <= (n-m)/2; k++ {
		num := factorial(n - k)
		den := factorial(k) * factorial((n+m)/2-k) * factorial((n-m)/2-k)
		s += math.Pow(-1, float64(k)) * num / den * math.Pow(rho, float64(n-2*k))
	}
	return s
}

var (
	factCache = []float64{1, 1}
	factOnce  sync.Once
)

// maxFactorial is well beyond any factorial used in this package (Zernike
// radial polynomials use n<=5; the loss channel uses n<=cutoff<=20); 170! is
// the largest value comfortably representable in float64.
const maxFactorial = 170

// factorial returns n!. The table is built once, thread-safely, so concurrent
// Simulate/SimulateQuantum calls cannot race on the shared cache slice.
func factorial(n int) float64 {
	if n < 0 {
		return math.NaN()
	}
	factOnce.Do(func() {
		for i := len(factCache); i <= maxFactorial; i++ {
			factCache = append(factCache, factCache[i-1]*float64(i))
		}
	})
	if n < len(factCache) {
		return factCache[n]
	}
	return math.Inf(1)
}

func (e *zernikeEl) Apply(f *Field, ctx *Context) error {
	n := f.N
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			x := f.X(i)
			rho := math.Hypot(x, y) / e.norm
			if rho > 1 {
				continue
			}
			theta := math.Atan2(y, x)
			var opd float64
			for z := 1; z <= 21; z++ {
				c := e.coef[z]
				if c == 0 {
					continue
				}
				nn, mm := nollNM[z][0], nollNM[z][1]
				rad := radialZernike(nn, abs(mm), rho)
				var ang float64
				if mm >= 0 {
					ang = math.Cos(float64(mm) * theta)
				} else {
					ang = math.Sin(float64(-mm) * theta)
				}
				if mm == 0 {
					opd += c * rad
				} else {
					opd += c * math.Sqrt(2*float64(nn+1)) * rad * ang
				}
			}
			t := cexpI(2 * math.Pi * opd)
			idx := j*n + i
			f.Ex[idx] *= t
			if f.Polarized {
				f.Ey[idx] *= t
			}
		}
	}
	return nil
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// ---- Jones elements --------------------------------------------------------

type polarizerEl struct{ angle, transmission float64 }

func newPolarizer(p map[string]any) (Element, error) {
	e := &polarizerEl{angle: pfd(p, "angle", 0), transmission: pfd(p, "transmission", 1)}
	if e.transmission < 0 || e.transmission > 1 {
		return nil, fmt.Errorf("polarizer: transmission must be in [0,1]")
	}
	return e, nil
}

func (e *polarizerEl) Apply(f *Field, ctx *Context) error {
	c, s := math.Cos(e.angle), math.Sin(e.angle)
	t := complex(e.transmission, 0)
	// P = t * [[c^2, sc],[sc, s^2]]
	f.ApplyJones(t*complex(c*c, 0), t*complex(s*c, 0), t*complex(s*c, 0), t*complex(s*s, 0))
	return nil
}

type retarderEl struct{ retardance, axis float64 }

func newRetarder(p map[string]any) (Element, error) {
	return &retarderEl{retardance: pfd(p, "retardance", math.Pi/2), axis: pfd(p, "axis", 0)}, nil
}

func (e *retarderEl) Apply(f *Field, ctx *Context) error {
	d := e.retardance
	th := e.axis
	c, s := math.Cos(th), math.Sin(th)
	e1 := cexpI(d / 2)
	e2 := cexpI(-d / 2)
	// J = R(-th) diag(e1, e2) R(th)
	a := complex(c*c, 0)*e1 + complex(s*s, 0)*e2
	b := complex(s*c, 0) * (e1 - e2)
	dd := complex(s*s, 0)*e1 + complex(c*c, 0)*e2
	f.ApplyJones(a, b, b, dd)
	return nil
}

type rotatorEl struct{ angle float64 }

func newRotator(p map[string]any) (Element, error) {
	return &rotatorEl{angle: pfd(p, "angle", 0)}, nil
}

func (e *rotatorEl) Apply(f *Field, ctx *Context) error {
	c, s := math.Cos(e.angle), math.Sin(e.angle)
	f.ApplyJones(complex(c, 0), complex(-s, 0), complex(s, 0), complex(c, 0))
	return nil
}

type customJonesEl struct{ a, b, c, d complex128 }

func newCustomJones(p map[string]any) (Element, error) {
	e := &customJonesEl{
		a: complex(pfd(p, "a_re", 1), pfd(p, "a_im", 0)),
		b: complex(pfd(p, "b_re", 0), pfd(p, "b_im", 0)),
		c: complex(pfd(p, "c_re", 0), pfd(p, "c_im", 0)),
		d: complex(pfd(p, "d_re", 1), pfd(p, "d_im", 0)),
	}
	return e, nil
}

func (e *customJonesEl) Apply(f *Field, ctx *Context) error {
	f.ApplyJones(e.a, e.b, e.c, e.d)
	return nil
}

// ---- propagation media (structural, registered as elements) ----------------

type uniaxialEl struct{ distance, no, ne float64 }

func newUniaxial(p map[string]any) (Element, error) {
	d, err := pf(p, "distance", 0)
	if err != nil || d < 0 {
		return nil, fmt.Errorf("uniaxial: distance must be >= 0")
	}
	no := pfd(p, "n_o", 1.5)
	ne := pfd(p, "n_e", 1.7)
	if no <= 0 || ne <= 0 {
		return nil, fmt.Errorf("uniaxial: n_o and n_e must be > 0")
	}
	return &uniaxialEl{distance: d, no: no, ne: ne}, nil
}

func (e *uniaxialEl) Apply(f *Field, ctx *Context) error {
	return PropagateUniaxial(f, e.distance, e.no, e.ne, ctx)
}

type mediumEl struct {
	distance   float64
	index      float64
	absorption float64
	steps      int
}

func newMedium(p map[string]any) (Element, error) {
	d, err := pf(p, "distance", 0)
	if err != nil || d < 0 {
		return nil, fmt.Errorf("medium: distance must be >= 0")
	}
	idx := pfd(p, "index", 1.5)
	if idx <= 0 {
		return nil, fmt.Errorf("medium: index must be > 0")
	}
	return &mediumEl{distance: d, index: idx, absorption: pfd(p, "absorption", 0), steps: pi_(p, "steps", 20)}, nil
}

func (e *mediumEl) Apply(f *Field, ctx *Context) error {
	return PropagateSplitStep(f, e.distance, UniformIndex(complex(e.index, e.absorption)), e.steps, ctx)
}

type biaxialEl struct {
	distance   float64
	nx, ny, nz float64
}

func newBiaxial(p map[string]any) (Element, error) {
	d, err := pf(p, "distance", 0)
	if err != nil || d < 0 {
		return nil, fmt.Errorf("biaxial: distance must be >= 0")
	}
	nx := pfd(p, "n_x", 1.6)
	ny := pfd(p, "n_y", 1.5)
	nz := pfd(p, "n_z", 1.4)
	if nx <= 0 || ny <= 0 || nz <= 0 {
		return nil, fmt.Errorf("biaxial: n_x, n_y, n_z must be > 0")
	}
	return &biaxialEl{distance: d, nx: nx, ny: ny, nz: nz}, nil
}

func (e *biaxialEl) Apply(f *Field, ctx *Context) error {
	eps := [3][3]complex128{
		{complex(e.nx*e.nx, 0), 0, 0},
		{0, complex(e.ny*e.ny, 0), 0},
		{0, 0, complex(e.nz*e.nz, 0)},
	}
	return PropagateAnisotropic(f, e.distance, eps, ctx)
}
