package optics

import (
	"fmt"
	"math"
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

// extraElementDocs holds catalog entries registered by generated elements
// (RegisterGeneratedElement); BuildCatalog appends them after the built-ins.
var extraElementDocs []ElementDoc

// elementBehaviorOverrides records the routing behavior of registered elements
// whose class is not the default transmit (a generated mirror element, say).
var elementBehaviorOverrides = map[string]int{}

// RegisterGeneratedElement registers a native element produced by
// `wos -gen-go` (see exprgen.go): its factory, its catalog documentation and
// its routing behavior. Generated elements register from init(), where a
// duplicate name is an authoring mistake — it panics instead of silently
// replacing another element.
func RegisterGeneratedElement(name string, factory ElementFactory, doc ElementDoc, behavior int) {
	if _, exists := elementRegistry[name]; exists {
		panic(fmt.Sprintf("generated element %q duplicates an existing element type; regenerate with another -gen-name", name))
	}
	elementRegistry[name] = factory
	if behavior != behaviorTransmit {
		elementBehaviorOverrides[name] = behavior
	}
	doc.Type = name
	doc.Custom = false
	extraElementDocs = append(extraElementDocs, doc)
}

// GeneratedElementDocs returns the catalog entries of generated elements.
func GeneratedElementDocs() []ElementDoc {
	return append([]ElementDoc(nil), extraElementDocs...)
}

// RegisteredElements lists the names of all registered element types.
func RegisteredElements() []string {
	out := make([]string, 0, len(elementRegistry))
	for k := range elementRegistry {
		out = append(out, k)
	}
	return out
}

// NewElement instantiates an element from its spec. Besides the built-in
// element registry, scripted elements loaded from definition files (see
// customelem.go) are accepted.
func NewElement(spec ElementSpec) (Element, error) {
	if fac, ok := elementRegistry[spec.Type]; ok {
		return fac(spec.Params)
	}
	if cd, ok := scriptedElementFor(spec.Type); ok {
		return cd.build(spec.Params)
	}
	return nil, fmt.Errorf("unknown element type %q", spec.Type)
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
	RegisterElement("kerr", newKerr)
	RegisterElement("saturable_absorber", newSaturableAbsorber)
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
	geom   *shapeGeom
	x0, y0 float64
}

func newAperture(p map[string]any) (Element, error) {
	g, err := parseShapeGeom(ps(p, "shape", "circle"), p)
	if err != nil {
		return nil, fmt.Errorf("aperture: %v", strings.TrimPrefix(err.Error(), "shape: "))
	}
	return &apertureEl{geom: g, x0: pfd(p, "x", 0), y0: pfd(p, "y", 0)}, nil
}

func (e *apertureEl) Apply(f *Field, ctx *Context) error {
	n := f.N
	g := e.geom
	sig := g.edgeSigma
	for j := 0; j < n; j++ {
		dy := f.Y(j) - e.y0
		for i := 0; i < n; i++ {
			dx := f.X(i) - e.x0
			var t complex128
			if sig > 0 {
				t = smoothStep(g.signedDistance(dx, dy), sig)
			} else {
				t = step(g.signedDistance(dx, dy))
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
	// x0, y0 anchor the random screen to the lab coordinates of the element
	// (set from the scene component position). The screen is a physical
	// object: translating the element slides it under the beam, so the
	// speckle changes. A screen keyed to the array index instead did not move
	// with the element at all (evidence §9.4).
	x0, y0 float64
}

func newDiffuser(p map[string]any) (Element, error) {
	return &diffuserEl{
		sigma: pfd(p, "sigma", math.Pi),
		corr:  pfd(p, "correlation", 2e-5),
		amp:   pfd(p, "amplitude", 1),
		seed:  int64(pfd(p, "seed", 1)),
		x0:    pfd(p, "x", 0),
		y0:    pfd(p, "y", 0),
	}, nil
}

// hashNoise01 maps an integer lattice cell (and the per-element seed) onto a
// deterministic uniform value in [0,1). It replaces a sequential RNG so the
// white noise is a pure function of the *lab* cell, which is what makes the
// screen translate with its element.
func hashNoise01(x, y, seed int64) float64 {
	h := uint64(seed)*0x9E3779B97F4A7C15 ^
		uint64(x)*0xC2B2AE3D27D4EB4F ^
		uint64(y)*0x165667B19E3779F9
	h ^= h >> 30
	h *= 0xBF58476D1CE4E5B9
	h ^= h >> 27
	h *= 0x94D049BB133111EB
	h ^= h >> 31
	return float64(h>>11) / float64(uint64(1)<<53)
}

func (e *diffuserEl) Apply(f *Field, ctx *Context) error {
	n := f.N
	phase := make([]float64, n*n)
	dx := f.DX
	if dx <= 0 {
		dx = 1
	}
	for j := 0; j < n; j++ {
		gy := int64(math.Round((f.Y(j) + e.y0) / dx))
		for i := 0; i < n; i++ {
			gx := int64(math.Round((f.X(i) + e.x0) / dx))
			phase[j*n+i] = hashNoise01(gx, gy, e.seed)*2*math.Pi - math.Pi
		}
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
					// Noll normalization: sqrt(n+1) for the m=0 modes
					// (sqrt(2(n+1)) for m!=0 below), so every coefficient
					// carries the same RMS meaning.
					opd += c * math.Sqrt(float64(nn+1)) * rad
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

// ---- intensity-dependent (nonlinear) elements ------------------------------
//
// Every element above is linear: its transmittance does not depend on how
// bright the light is. These two do — the field's local intensity |E|² sets the
// extra phase (Kerr) or the absorption (saturable absorber). They are the
// primitive the iterative-cavity / optical-Ising / threshold-logic and truly
// nonlinear D2NN work needs (the `medium` element is intensity-independent).

// kerrEl is a thin Kerr slice: phi(x,y) = k * n2 * I(x,y) * L, i.e. the
// refractive index is n0 + n2 * I. An optional two-photon absorption
// coefficient beta adds an intensity-proportional loss.
type kerrEl struct {
	n2, tpa, length float64
}

func newKerr(p map[string]any) (Element, error) {
	l := pfd(p, "length", 1e-3)
	if l < 0 {
		return nil, fmt.Errorf("kerr: length must be >= 0")
	}
	return &kerrEl{
		n2:     pfd(p, "n2", 1e-14),
		tpa:    pfd(p, "tpa", 0),
		length: l,
	}, nil
}

// Apply multiplies the field by exp(i k n2 I L - beta I L / 2) with I = |E|².
func (e *kerrEl) Apply(f *Field, ctx *Context) error {
	k := 2 * math.Pi / ctx.Wavelength
	for i := range f.Ex {
		I := f.Intensity(i)
		if I == 0 {
			continue
		}
		t := cexpI(k * e.n2 * I * e.length)
		if e.tpa != 0 {
			t *= complex(math.Exp(-0.5*e.tpa*I*e.length), 0)
		}
		f.Ex[i] *= t
		if f.Polarized {
			f.Ey[i] *= t
		}
		if f.Vectorial && f.Ez != nil {
			f.Ez[i] *= t
		}
	}
	return nil
}

// saturableAbsorberEl is a thin saturable absorber: the absorption
// alpha(I) = alpha0 / (1 + I/I_sat) falls as the light gets brighter, so a dim
// region is absorbed and a bright one transmits. It is the amplitude-side
// complement of the Kerr phase (a simple threshold/bistability primitive).
type saturableAbsorberEl struct {
	alpha0, isat, length float64
}

func newSaturableAbsorber(p map[string]any) (Element, error) {
	a := pfd(p, "alpha0", 1.0) // 1/m
	l := pfd(p, "length", 1e-3)
	if a < 0 || l < 0 {
		return nil, fmt.Errorf("saturable_absorber: alpha0 and length must be >= 0")
	}
	isat := pfd(p, "isat", 1e6) // W/m^2
	if isat <= 0 {
		return nil, fmt.Errorf("saturable_absorber: saturation intensity isat must be > 0")
	}
	return &saturableAbsorberEl{alpha0: a, isat: isat, length: l}, nil
}

// Apply scales the field by exp(-alpha0 L / (2 (1 + I/I_sat))).
func (e *saturableAbsorberEl) Apply(f *Field, ctx *Context) error {
	for i := range f.Ex {
		I := f.Intensity(i)
		t := complex(math.Exp(-e.alpha0*e.length/(2*(1+I/e.isat))), 0)
		f.Ex[i] *= t
		if f.Polarized {
			f.Ey[i] *= t
		}
		if f.Vectorial && f.Ez != nil {
			f.Ez[i] *= t
		}
	}
	return nil
}
