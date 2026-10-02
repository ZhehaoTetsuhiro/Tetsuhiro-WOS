package optics

import (
	"fmt"
	"math"
)

// ---------------------------------------------------------------------------
// Scene model
//
// A scene is a positioned optical layout: every component has a place on the
// optical table (x right, y up, z along the nominal axis) and an orientation,
// plus a transverse shape and size. The light path is therefore stored as
// *positions of components* rather than as an ordered list of elements: the
// beam router below traces each source's rays through the layout, works out
// which component each ray meets and in what order, and derives the
// propagation distances from the geometry.
// ---------------------------------------------------------------------------

// Vec3 is a point or vector in meters on the optical table.
type Vec3 struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	Z float64 `json:"z"`
}

func v3(x, y, z float64) Vec3       { return Vec3{X: x, Y: y, Z: z} }
func (a Vec3) Add(b Vec3) Vec3      { return v3(a.X+b.X, a.Y+b.Y, a.Z+b.Z) }
func (a Vec3) Sub(b Vec3) Vec3      { return v3(a.X-b.X, a.Y-b.Y, a.Z-b.Z) }
func (a Vec3) Scale(s float64) Vec3 { return v3(a.X*s, a.Y*s, a.Z*s) }
func (a Vec3) Dot(b Vec3) float64   { return a.X*b.X + a.Y*b.Y + a.Z*b.Z }
func (a Vec3) Norm() float64        { return math.Sqrt(a.Dot(a)) }
func (a Vec3) Unit() Vec3 {
	// A zero, NaN or infinite norm must not propagate NaN coordinates into the
	// layout: an undefined direction stays undefined (the beam escapes).
	n := a.Norm()
	if !(n > 0) || math.IsInf(n, 0) {
		return v3(0, 0, 0)
	}
	return a.Scale(1 / n)
}
func (a Vec3) IsZero() bool             { return a.X == 0 && a.Y == 0 && a.Z == 0 }
func (a Vec3) Cross(b Vec3) Vec3        { return v3(a.Y*b.Z-a.Z*b.Y, a.Z*b.X-a.X*b.Z, a.X*b.Y-a.Y*b.X) }
func (a Vec3) String() string           { return fmt.Sprintf("(%g, %g, %g) m", a.X, a.Y, a.Z) }
func reflectDir(d, n Vec3) Vec3         { return d.Sub(n.Scale(2 * d.Dot(n))) }
func lerpVec(a, b Vec3, t float64) Vec3 { return a.Add(b.Sub(a).Scale(t)) }
func (a Vec3) RotateAboutY(th float64) Vec3 {
	s, c := math.Sincos(th)
	return v3(a.X*c+a.Z*s, a.Y, -a.X*s+a.Z*c)
}
func (a Vec3) RotateAboutX(th float64) Vec3 {
	s, c := math.Sincos(th)
	return v3(a.X, a.Y*c-a.Z*s, a.Y*s+a.Z*c)
}

// ComponentSpec is one positioned optical component of a scene.
//
// Orientation: the surface normal is yaw (rotation about y) then pitch
// (rotation about x) applied to +z, so yaw=0,pitch=0 faces +z; a 45° folding
// mirror is yaw=+45° and the beam it folds by 90° is the one travelling along
// -z. Roll rotates the component about its own normal (it orients the shape,
// the grating grooves, the polarizer axis, ...).
type ComponentSpec struct {
	ID     string         `json:"id,omitempty"`
	Type   string         `json:"type"`
	Label  string         `json:"label,omitempty"`
	Pos    Vec3           `json:"pos"`
	Yaw    float64        `json:"yaw,omitempty"`
	Pitch  float64        `json:"pitch,omitempty"`
	Roll   float64        `json:"roll,omitempty"`
	Shape  *ShapeSpec     `json:"shape,omitempty"`
	Params map[string]any `json:"params,omitempty"`
}

// SceneSpec is a positioned layout.
type SceneSpec struct {
	Components []ComponentSpec `json:"components"`
}

// compGeom is the resolved geometry of a component.
type compGeom struct {
	pos Vec3
	n   Vec3 // surface normal
	u   Vec3 // in-plane axis 1
	v   Vec3 // in-plane axis 2
}

// local returns the in-plane coordinates of a point relative to the component
// centre.
func (g compGeom) local(p Vec3) (float64, float64) {
	d := p.Sub(g.pos)
	return d.Dot(g.u), d.Dot(g.v)
}

// resolveGeom computes the normal and in-plane axes of a component.
func resolveGeom(c *ComponentSpec) compGeom {
	n := v3(0, 0, 1).RotateAboutX(c.Pitch).RotateAboutY(c.Yaw).Unit()
	ref := v3(0, 1, 0)
	if math.Abs(n.Dot(ref)) > 0.999 {
		ref = v3(1, 0, 0)
	}
	u := ref.Cross(n).Unit()
	if c.Roll != 0 {
		w := n.Cross(u).Unit()
		sr, cr := math.Sincos(c.Roll)
		u = u.Scale(cr).Add(w.Scale(sr)).Unit()
	}
	v := n.Cross(u).Unit()
	return compGeom{pos: c.Pos, n: n, u: u, v: v}
}

// Component behaviour classes.
const (
	behaviorTransmit = iota // passes the beam through (lens, filter, stop, ...)
	behaviorMirror          // folds the beam (mirror family)
	behaviorSplit           // both transmits and reflects (beamsplitter)
	behaviorSensor          // records the field and ends the beam
)

// The behaviour classes are exported for generated elements, which declare
// theirs at registration time (RegisterGeneratedElement).
const (
	BehaviorTransmit = behaviorTransmit
	BehaviorMirror   = behaviorMirror
	BehaviorSplit    = behaviorSplit
	BehaviorSensor   = behaviorSensor
)

// componentBehavior classifies a scene component type.
func componentBehavior(t string) (int, error) {
	switch t {
	case "sensor", "detector":
		return behaviorSensor, nil
	case "beamsplitter", "bs":
		return behaviorSplit, nil
	case "mirror", "concave_mirror", "convex_mirror", "retro_reflector":
		return behaviorMirror, nil
	case "aperture", "iris", "stop", "slit":
		return behaviorTransmit, nil
	case "propagate", "combiner":
		return 0, fmt.Errorf("scene component type %q is a train directive, not a positioned component", t)
	case "source", "laser":
		return 0, fmt.Errorf("light sources belong in the scene's sources list, not in components")
	}
	if _, ok := elementRegistry[t]; !ok {
		if cd, ok := scriptedElementFor(t); ok {
			return cd.behavior, nil
		}
		return 0, fmt.Errorf("unknown scene component type %q (a scripted element needs its definition file in elements/, see docs/KERNEL.md)", t)
	}
	if b, ok := elementBehaviorOverrides[t]; ok {
		return b, nil
	}
	return behaviorTransmit, nil
}

// stopLike reports whether a type blocks (instead of simply missing) a ray that
// falls outside its outline.
func stopLike(t string) bool {
	switch t {
	case "aperture", "iris", "stop":
		return true
	}
	return false
}

// sceneTraceLimit bounds the ray tree so a mis-designed (or cyclic) layout
// cannot explode.
const (
	MaxSceneBeams       = 512
	MaxSceneVisits      = 512
	MaxVisitsPerComp    = 12
	sceneEpsilon        = 1e-9
	sceneParallelCos    = 1e-6
	sceneTiltDesignFold = 1e-2 // rad: larger direction differences are design folds
)

// sceneBeam is a beam launched either by a source or by a component.
type sceneBeam struct {
	id       int
	src      int         // source index that ultimately feeds this beam
	comp     int         // launching component (-1 = source launch)
	coef     complex128  // amplitude/phase transfer applied when the beam was launched
	dir      Vec3        // propagation direction (unit)
	from     Vec3        // launch point
	pathLen  float64     // accumulated optical path length at the launch point (m)
	blocked  bool        // the component stopped the light: no amplitude left
	producer *sceneVisit // visit that launched this beam (nil for a source)
	visit    *sceneVisit
	segLen   float64 // geometric length of the segment to visit
	escaped  bool    // no further component in front of it
	end      Vec3    // end point of the drawn/escaped segment
}

// sceneIncoming is one contribution arriving at a visit.
type sceneIncoming struct {
	beam   *sceneBeam
	segLen float64
}

// sceneVisit is one component interaction (a component hit by a beam
// travelling in a given direction).
type sceneVisit struct {
	id       int
	comp     int
	src      int // source that first reached this interaction (-1 = unknown)
	inDir    Vec3
	arrival  float64 // earliest arrival path length (sorts the evaluation)
	in       []sceneIncoming
	out      []*sceneBeam
	refDir   Vec3 // reference direction for relative-tilt bookkeeping
	arriveAt Vec3 // arrival point of the earliest contribution
	launched bool // outgoing beams have been created
}

// sceneGraph is the routed light path.
type sceneGraph struct {
	comps    []ComponentSpec
	geoms    []compGeom
	shapes   []*shapeGeom
	behavior []int
	elements []Element // compiled thin-element operators (nil for sensors/splitters)
	sources  []SourceSpec
	beams    []*sceneBeam
	visits   []*sceneVisit
	warnings []string
	// visitAt indexes visits by "comp|dirBucket".
	visitAt map[string]*sceneVisit
	// planar reports whether every beam travels in the x-z table plane.
	planar bool
	// gridSize/gridWidth describe the sampling grid used by the wave evaluation.
	gridSize  int
	gridWidth float64
	// wl is the fallback wavelength used to label sources in the trace.
	wl float64
}

// sceneDirKey buckets a direction so that beams that are collinear (to within
// 1e-4 rad) share a visit.
// matchVisit returns the visit of a component that a beam travelling in dir
// belongs to: the first whose reference direction is within the misalignment
// angle (sceneTiltDesignFold). Contributions that close in angle this way are
// the same beam — their fields are summed coherently at the component and the
// residual difference is applied as a wavefront tilt — while a larger
// difference is a design fold (a 90° mirror, say) and gets its own visit, so
// beams that are not meant to interfere are never mixed.
func (g *sceneGraph) matchVisit(comp int, dir Vec3) *sceneVisit {
	best := -1
	bestAng := math.Inf(1)
	for i := range g.visits {
		v := g.visits[i]
		if v.comp != comp {
			continue
		}
		ang := v.refDir.Sub(dir).Norm()
		if ang <= sceneTiltDesignFold && ang < bestAng {
			best, bestAng = i, ang
		}
	}
	if best < 0 {
		return nil
	}
	return g.visits[best]
}

// BuildSceneGraph routes every source ray through the scene.
func BuildSceneGraph(scene *SceneSpec, sources []SourceSpec) (*sceneGraph, error) {
	if scene == nil || len(scene.Components) == 0 {
		return nil, fmt.Errorf("scene has no components")
	}
	if len(scene.Components) > MaxElements {
		return nil, fmt.Errorf("scene has too many components (%d, limit %d)", len(scene.Components), MaxElements)
	}
	g := &sceneGraph{sources: sources, visitAt: map[string]*sceneVisit{}}
	for i := range scene.Components {
		c := scene.Components[i]
		kind, err := componentBehavior(c.Type)
		if err != nil {
			return nil, fmt.Errorf("component %d (%s): %v", i, c.Type, err)
		}
		sh, err := shapeFromSpec(c.Shape)
		if err != nil {
			return nil, fmt.Errorf("component %d (%s): %v", i, c.Type, err)
		}
		g.comps = append(g.comps, c)
		g.geoms = append(g.geoms, resolveGeom(&scene.Components[i]))
		g.shapes = append(g.shapes, sh)
		g.behavior = append(g.behavior, kind)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("scene has no light sources")
	}
	// A scene source without an explicit place is put on the optical axis just
	// in front of the first component, which is where a real source would sit.
	sources = placeSceneSources(scene, sources)
	for si := range sources {
		s := &sources[si]
		if !finiteVec(s.Position()) || !finiteVec(s.Direction()) {
			return nil, fmt.Errorf("source %d: position and direction must be finite", si)
		}
		dir := s.Direction()
		from := s.Position()
		b := &sceneBeam{id: len(g.beams), src: si, comp: -1, coef: 1, dir: dir, from: from, pathLen: 0}
		g.beams = append(g.beams, b)
		if err := g.traceBeam(b); err != nil {
			return nil, err
		}
	}
	g.sources = sources
	g.checkPlanar()
	return g, nil
}

// placeSceneSources fills in a default position/direction for scene sources
// that do not carry one: the source is placed in front of the first component
// (or at the -z end of the layout) emitting along +z.
func placeSceneSources(scene *SceneSpec, sources []SourceSpec) []SourceSpec {
	front := 0.0
	if len(scene.Components) > 0 {
		front = scene.Components[0].Pos.Z
		for i := range scene.Components {
			if z := scene.Components[i].Pos.Z; z < front {
				front = z
			}
		}
	}
	out := make([]SourceSpec, len(sources))
	copy(out, sources)
	for i := range out {
		if out[i].Pos == nil {
			out[i].Pos = &Vec3{X: 0, Y: 0, Z: front - 0.05}
		}
		if out[i].Dir == nil {
			out[i].Dir = &Vec3{X: 0, Y: 0, Z: 1}
		}
	}
	return out
}

// visitCount counts how many visits already exist for a component.
func (g *sceneGraph) visitCount(comp int) int {
	n := 0
	for _, v := range g.visits {
		if v.comp == comp {
			n++
		}
	}
	return n
}

// traceBeam walks one beam forward to its next component (following splits).
func (g *sceneGraph) traceBeam(b *sceneBeam) error {
	if len(g.beams) > MaxSceneBeams {
		g.warn(fmt.Sprintf("light path exceeds %d beams; further splitting was truncated", MaxSceneBeams))
		return nil
	}
	comp, t, hit, ok := g.nextHit(b.from, b.dir, b.comp)
	if !ok {
		b.escaped = true
		b.end = b.from.Add(b.dir.Scale(g.escapeDistance(b.from, b.dir)))
		return nil
	}
	spec := &g.comps[comp]
	sh := g.shapes[comp]
	if sh != nil && !stopLike(spec.Type) {
		u, v := g.geoms[comp].local(hit)
		if !sh.contains(u, v) {
			// The beam slips past this optic; it is already the nearest
			// surface, so nothing else is in front of it.
			b.escaped = true
			b.end = b.from.Add(b.dir.Scale(g.escapeDistance(b.from, b.dir)))
			return nil
		}
	}
	if g.visitCount(comp) >= MaxVisitsPerComp {
		g.warn(fmt.Sprintf("组件 %q 被访问超过 %d 次，光路在此截断（谐振腔按固定往返次数截断）", g.compName(comp), MaxVisitsPerComp))
		b.escaped = true
		b.end = hit
		return nil
	}
	v := g.addVisit(comp, b.dir, b, t, hit)
	b.visit = v
	b.segLen = t
	if len(g.visits) > MaxSceneVisits {
		g.warn(fmt.Sprintf("light path exceeds %d interactions; further tracing was truncated", MaxSceneVisits))
		return nil
	}
	// Launch the outgoing beams of a brand-new visit.
	if !v.launched {
		v.launched = true
		if err := g.launch(v); err != nil {
			return err
		}
	}
	return nil
}

// compName returns a display name for a component index.
func (g *sceneGraph) compName(i int) string {
	if i < 0 || i >= len(g.comps) {
		return "?"
	}
	c := &g.comps[i]
	if c.Label != "" {
		return c.Label
	}
	if c.ID != "" {
		return c.ID
	}
	return fmt.Sprintf("%s#%d", c.Type, i)
}

// addVisit finds or creates the visit of (component, incoming direction).
func (g *sceneGraph) addVisit(comp int, inDir Vec3, b *sceneBeam, segLen float64, hit Vec3) *sceneVisit {
	v := g.matchVisit(comp, inDir)
	if v == nil {
		v = &sceneVisit{id: len(g.visits), comp: comp, inDir: inDir, arrival: math.Inf(1), arriveAt: hit, src: -1}
		g.visits = append(g.visits, v)
	}
	if v.src < 0 {
		v.src = b.src
	}
	arr := b.pathLen + segLen
	if arr < v.arrival {
		v.arrival = arr
		v.arriveAt = hit
	}
	v.in = append(v.in, sceneIncoming{beam: b, segLen: segLen})
	v.refDir = g.referenceDir(v)
	return v
}

// referenceDir returns the direction a contribution is compared against when
// the relative wavefront tilt is evaluated: the direction the merged beam
// continues in (the fold direction for mirrors, the incoming direction for
// transmissive components).
func (g *sceneGraph) referenceDir(v *sceneVisit) Vec3 {
	switch g.behavior[v.comp] {
	case behaviorMirror:
		return reflectDir(v.inDir, g.geoms[v.comp].n)
	case behaviorSensor:
		// sensors record whatever arrives: compare against the first
		// contribution, updated as more arrive.
		if len(v.in) > 0 {
			return v.in[0].beam.dir
		}
	}
	return v.inDir
}

// launch creates the outgoing beams of a visit.
func (g *sceneGraph) launch(v *sceneVisit) error {
	gc := &g.comps[v.comp]
	geom := g.geoms[v.comp]
	switch g.behavior[v.comp] {
	case behaviorSensor:
		return nil
	case behaviorSplit:
		r := pfd(gc.Params, "reflectivity", 0.5)
		if r < 0 || r > 1 {
			return fmt.Errorf("component %d: beamsplitter reflectivity must be in [0,1]", v.comp)
		}
		ph := pfd(gc.Params, "phase", 0)
		if r > 0 {
			if err := g.newBeam(v, reflectDir(v.inDir, geom.n), complex(0, math.Sqrt(r))*cexpI(ph)); err != nil {
				return err
			}
		}
		if r < 1 {
			if err := g.newBeam(v, v.inDir, complex(math.Sqrt(1-r), 0)); err != nil {
				return err
			}
		}
	case behaviorMirror:
		refl := pfd(gc.Params, "reflectivity", 1)
		if refl < 0 || refl > 1 {
			return fmt.Errorf("component %d: mirror reflectivity must be in [0,1]", v.comp)
		}
		dOut := reflectDir(v.inDir, geom.n)
		retro := gc.Type == "retro_reflector"
		if retro {
			// A retro-reflector sends the light back along its own path.
			dOut = v.inDir.Scale(-1)
		}
		if refl > 0 {
			if err := g.newBeam(v, dOut, complex(refl, 0)); err != nil {
				return err
			}
		}
		if refl < 1 && !retro {
			// Partial reflector: the remainder is transmitted.
			if err := g.newBeam(v, v.inDir, complex(math.Sqrt(1-refl*refl), 0)); err != nil {
				return err
			}
		}
	default:
		if err := g.newBeam(v, v.inDir, 1); err != nil {
			return err
		}
	}
	return nil
}

// newBeam records an outgoing beam of v and traces it to its next interaction.
// A tracing failure (an invalid parameter on the next component, a truncated
// path) is reported to the caller instead of being downgraded to a warning, so
// a scene never silently drops light.
func (g *sceneGraph) newBeam(v *sceneVisit, dir Vec3, coef complex128) error {
	b := &sceneBeam{
		id: len(g.beams), src: v.src, comp: v.comp, coef: coef,
		dir: dir.Unit(), from: v.arriveAt, pathLen: v.arrival, producer: v,
	}
	g.beams = append(g.beams, b)
	v.out = append(v.out, b)
	return g.traceBeam(b)
}

// nextHit returns the nearest component in front of the ray. skipComp is the
// component the beam was just launched from: its own plane would be hit at
// t = 0, so it is ignored at zero distance (a beam may still return to it
// later). A component whose clear aperture the ray misses is skipped, except
// for stops, which block the beam instead.
func (g *sceneGraph) nextHit(from, dir Vec3, skipComp int) (int, float64, Vec3, bool) {
	best := -1
	bestT := math.Inf(1)
	for i := range g.comps {
		geom := g.geoms[i]
		denom := dir.Dot(geom.n)
		if math.Abs(denom) < sceneParallelCos {
			continue
		}
		t := geom.pos.Sub(from).Dot(geom.n) / denom
		if t < 0 || t >= bestT {
			continue
		}
		if t <= sceneEpsilon && i == skipComp {
			continue
		}
		hit := from.Add(dir.Scale(t))
		if g.behavior[i] == behaviorSensor && dir.Dot(geom.n) >= 0 {
			// A detector only sees light arriving on its front face (the +n
			// side, i.e. d·n < 0); light coming the other way passes behind it.
			continue
		}
		sh := g.shapes[i]
		if sh != nil && !stopLike(g.comps[i].Type) {
			u, v := geom.local(hit)
			if !sh.contains(u, v) {
				continue
			}
		}
		best, bestT = i, t
	}
	if best < 0 {
		return -1, 0, Vec3{}, false
	}
	return best, bestT, from.Add(dir.Scale(bestT)), true
}

// escapeDistance returns how far a beam is drawn after leaving the layout.
func (g *sceneGraph) escapeDistance(from, dir Vec3) float64 {
	span := 0.0
	for _, c := range g.comps {
		span = math.Max(span, c.Pos.Sub(from).Norm())
	}
	if span < 1e-3 {
		// The beam sits on its launching component, so the only component in
		// front of it is the one it just left: draw a visible tail instead of
		// collapsing to the fixed offset.
		span = 0.1
	}
	return span + 0.05*span + 0.01
}

func (g *sceneGraph) warn(msg string) {
	for _, w := range g.warnings {
		if w == msg {
			return
		}
	}
	if len(g.warnings) < 32 {
		g.warnings = append(g.warnings, msg)
	}
}

// checkPlanar records whether all beams stay in the x-z plane (used for the
// layout warning shown in the GUI).
func (g *sceneGraph) checkPlanar() {
	g.planar = true
	for _, b := range g.beams {
		if math.Abs(b.dir.Y) > 1e-9 || math.Abs(b.from.Y) > 1e-9 || math.Abs(b.end.Y) > 1e-9 {
			g.planar = false
			return
		}
	}
}

// ---------------------------------------------------------------------------
// Scene trace: the geometry-only result used by the GUI's 3-D layout view
// ---------------------------------------------------------------------------

// TraceSegment is one straight beam segment of the layout.
type TraceSegment struct {
	From   Vec3    `json:"from"`
	To     Vec3    `json:"to"`
	Source int     `json:"source"` // index into the scene's source list
	BeamID int     `json:"beam_id"`
	Order  int     `json:"order"` // number of interactions before this segment
	Power  float64 `json:"power"` // relative geometric power (product of |coef|^2)
	Kind   string  `json:"kind"`  // pass | reflect | split
}

// TraceComponent is a component as the layout view needs it.
type TraceComponent struct {
	ID     string  `json:"id"`
	Type   string  `json:"type"`
	Label  string  `json:"label"`
	Pos    Vec3    `json:"pos"`
	Normal Vec3    `json:"normal"`
	U      Vec3    `json:"u"`
	V      Vec3    `json:"v"`
	Hu     float64 `json:"hu"` // half extent along u (m)
	Hv     float64 `json:"hv"` // half extent along v (m)
	Hits   int     `json:"hits"`
	Class  string  `json:"class"` // mirror | splitter | lens | detector | stop | other
}

// SceneTrace is the routed geometry of a scene.
type SceneTrace struct {
	Components []TraceComponent `json:"components"`
	Segments   []TraceSegment   `json:"segments"`
	Sources    []SourceGeom     `json:"sources"`
	Min        Vec3             `json:"min"`
	Max        Vec3             `json:"max"`
	Planar     bool             `json:"planar"`
	Warnings   []string         `json:"warnings"`
}

// SourceGeom is a source's placement as the layout view needs it.
type SourceGeom struct {
	Index      int     `json:"index"`
	ID         string  `json:"id"`
	Type       string  `json:"type"`
	Label      string  `json:"label"`
	Pos        Vec3    `json:"pos"`
	Dir        Vec3    `json:"dir"`
	Wavelength float64 `json:"wavelength"`
	Power      float64 `json:"power"`
}

// componentClass classifies a scene component for the GUI (colours and the
// insert menu). A class declared by a scripted or generated element wins over
// the built-in name lists below, which stay for the kernel's own types.
func componentClass(t string) string {
	if cd, ok := scriptedElementFor(t); ok {
		return cd.class
	}
	for _, doc := range GeneratedElementDocs() {
		if doc.Type == t && doc.Class != "" {
			return doc.Class
		}
	}
	switch t {
	case "mirror", "concave_mirror", "convex_mirror", "retro_reflector":
		return "mirror"
	case "beamsplitter", "bs":
		return "splitter"
	case "sensor", "detector":
		return "detector"
	case "aperture", "iris", "stop", "slit":
		return "stop"
	case "lens", "concave_lens", "axicon", "zone_plate", "spherical_mirror":
		return "lens"
	}
	return "other"
}

// ComponentClasses maps every known component type to its GUI class
// (lens/mirror/splitter/detector/stop/other), including scripted and generated
// elements. The GUI reads it from the catalog instead of keeping a second,
// drifting list.
func ComponentClasses() map[string]string {
	out := map[string]string{}
	add := func(t string) {
		if _, ok := out[t]; !ok {
			out[t] = componentClass(t)
		}
	}
	for name := range elementRegistry {
		add(name)
	}
	for _, t := range []string{"sensor", "detector", "beamsplitter", "bs", "mirror",
		"concave_mirror", "convex_mirror", "retro_reflector", "aperture", "iris",
		"stop", "slit", "concave_lens", "axicon", "zone_plate", "spherical_mirror"} {
		add(t)
	}
	// A declared class wins: it is the element's own statement about how it
	// should be drawn, and it is the only class information a generated or
	// scripted element has.
	for _, doc := range GeneratedElementDocs() {
		if doc.Class != "" {
			out[doc.Type] = doc.Class
		} else {
			add(doc.Type)
		}
	}
	for _, doc := range ScriptedElementDocs() {
		if doc.Class != "" {
			out[doc.Type] = doc.Class
		} else {
			add(doc.Type)
		}
	}
	return out
}

// TraceScene routes the light path of a scene without running the wave
// simulation. It is cheap enough to call on every geometry edit.
func TraceScene(scene *SceneSpec, sources []SourceSpec, wl float64) (*SceneTrace, error) {
	g, err := BuildSceneGraph(scene, sources)
	if err != nil {
		return nil, err
	}
	g.wl = wl
	return traceFromGraph(g), nil
}

// traceFromGraph turns a routed graph into the layout-view trace.
func traceFromGraph(g *sceneGraph) *SceneTrace {
	sources := g.sources
	wl := g.wl
	out := &SceneTrace{Planar: g.planar, Warnings: g.warnings}
	hits := make([]int, len(g.comps))
	for i := range g.comps {
		c := &g.comps[i]
		geom := g.geoms[i]
		hu, hv := 2.5e-3, 2.5e-3
		if g.shapes[i] != nil {
			hu, hv = g.shapes[i].halfExtents()
		}
		label := c.Label
		if label == "" {
			label = c.ID
		}
		out.Components = append(out.Components, TraceComponent{
			ID: c.ID, Type: c.Type, Label: label, Pos: c.Pos,
			Normal: geom.n, U: geom.u, V: geom.v, Hu: hu, Hv: hv,
			Class: componentClass(c.Type),
		})
	}
	for _, b := range g.beams {
		pw := real(b.coef)*real(b.coef) + imag(b.coef)*imag(b.coef)
		kind := "pass"
		if b.blocked {
			kind = "blocked"
		} else if b.comp >= 0 {
			switch g.behavior[b.comp] {
			case behaviorMirror:
				if reflectDir(b.dir, g.geoms[b.comp].n).Sub(b.dir).Norm() < 1e-12 {
					kind = "pass"
				} else {
					kind = "reflect"
				}
			case behaviorSplit:
				kind = "split"
			}
		}
		to := b.end
		if b.visit != nil {
			to = b.visit.arriveAt
			hits[b.visit.comp]++
		} else if to.IsZero() {
			to = b.from.Add(b.dir.Scale(g.escapeDistance(b.from, b.dir)))
		}
		out.Segments = append(out.Segments, TraceSegment{
			From: b.from, To: to, Source: b.src, BeamID: b.id, Power: pw, Kind: kind,
		})
	}
	for i := range g.comps {
		if hits[i] > 0 {
			out.Components[i].Hits = hits[i]
		}
	}
	for i := range sources {
		s := &sources[i]
		out.Sources = append(out.Sources, SourceGeom{
			Index: i, ID: s.ResolvedID(i), Type: s.Type, Label: s.ResolvedLabel(i),
			Pos: s.Position(), Dir: s.Direction(),
			Wavelength: s.EffectiveWavelength(wl), Power: s.Power(),
		})
	}
	// Bounding box over components, segments and sources.
	first := true
	acc := func(p Vec3) {
		if first {
			out.Min, out.Max, first = p, p, false
			return
		}
		out.Min.X = math.Min(out.Min.X, p.X)
		out.Min.Y = math.Min(out.Min.Y, p.Y)
		out.Min.Z = math.Min(out.Min.Z, p.Z)
		out.Max.X = math.Max(out.Max.X, p.X)
		out.Max.Y = math.Max(out.Max.Y, p.Y)
		out.Max.Z = math.Max(out.Max.Z, p.Z)
	}
	for _, c := range out.Components {
		acc(c.Pos.Sub(v3(c.Hu, c.Hv, 0)))
		acc(c.Pos.Add(v3(c.Hu, c.Hv, 0)))
		acc(c.Pos)
	}
	for _, s := range out.Segments {
		acc(s.From)
		acc(s.To)
	}
	for _, s := range out.Sources {
		acc(s.Pos)
	}
	if first {
		out.Min, out.Max = v3(0, 0, 0), v3(0, 0, 0)
	}
	return out
}

// LayoutFromElements synthesizes a scene for a legacy element train: the
// components are laid out along +z at the accumulated propagation distances.
// The layout view uses it so element-sequence configurations still draw, and
// the server's convert endpoint uses it to hand an old file to the scene GUI —
// which then *simulates* the synthesized scene, so it must stay faithful: a
// sensor keeps the legacy semantics (no outline = record the whole field).
func LayoutFromElements(cfg *Config) *SceneSpec {
	scene := &SceneSpec{}
	z := 0.0
	bs := 0
	for i := range cfg.Elements {
		el := &cfg.Elements[i]
		switch el.Type {
		case "propagate":
			z += pfd(el.Params, "distance", 0)
		case "sensor":
			// 旧模型没有朝向概念：探测器正对沿 +z 传来的光（法线指向 −z）。
			// 旧模型的 sensor 也没有轮廓、记录整幅场；不要给它编造一个 3 mm 小窗，
			// 否则转换后的剖面会被裁掉（单缝 2.4 mm 处的一级旁瓣就是这样丢的）。
			scene.Components = append(scene.Components, ComponentSpec{
				Type: "sensor", Label: ps(el.Params, "label", "sensor"), Pos: v3(0, 0, z), Yaw: math.Pi,
			})
		case "combiner":
			outs, _ := el.Params["outputs"].([]any)
			for oi := range outs {
				om, _ := outs[oi].(map[string]any)
				scene.Components = append(scene.Components, ComponentSpec{
					Type: "sensor", Label: ps(om, "label", fmt.Sprintf("out%d", oi)), Pos: v3(0, 0, z), Yaw: math.Pi,
				})
			}
		case "beamsplitter":
			c := ComponentSpec{Type: "beamsplitter", ID: fmt.Sprintf("bs%d", bs),
				Label: fmt.Sprintf("分束器 %d", bs), Pos: v3(0, 0, z), Yaw: math.Pi / 4}
			bs++
			c.Params = map[string]any{"reflectivity": pfd(el.Params, "reflectivity", 0.5)}
			scene.Components = append(scene.Components, c)
		default:
			c := ComponentSpec{Type: el.Type, Pos: v3(0, 0, z), Params: map[string]any{}}
			for k, v := range el.Params {
				c.Params[k] = v
			}
			scene.Components = append(scene.Components, c)
		}
	}
	return scene
}
