package optics

import (
	"fmt"
	"math"
	"sort"
	"time"
)

// ---------------------------------------------------------------------------
// Wave evaluation of a routed scene
//
// The geometry (scene.go) gives the light path: which component each beam meets,
// in what order, in which direction, and how far it travelled. This file turns
// that path into a wave simulation: the field of every beam is propagated along
// its segment, components multiply it by their element operator, beams that
// recombine at the same component are summed coherently (which is what makes
// interferometers work: the phase difference comes from the geometric path
// length difference), and detectors record planes.
//
// Propagation is evaluated on *unfolded* beam paths: each beam carries its field
// on its own transverse grid and advances along its own direction, so a layout
// folded by mirrors is simulated exactly as the equivalent train of on-axis
// propagation lengths. See docs/PHYSICS.md §"光路展开" for the approximation
// this entails.
// ---------------------------------------------------------------------------

// unitKey identifies one coherent simulation unit: a coherent group at one
// wavelength.
type unitKey struct {
	group string
	wlKey int64 // wavelength in nm*1000 (stable key)
}

// sceneUnit is one (group, wavelength) pair of sources simulated together.
type sceneUnit struct {
	key     unitKey
	sources []int
	hash    uint64
}

// splitUnits groups the sources into the smallest number of coherent runs.
func splitUnits(sources []SourceSpec, fallbackWL float64, warnings *Warnings) []sceneUnit {
	var units []sceneUnit
	index := map[unitKey]int{}
	for i := range sources {
		s := &sources[i]
		wl := s.EffectiveWavelength(fallbackWL)
		key := unitKey{group: s.GroupID(i), wlKey: int64(math.Round(wl * 1e12))}
		ui, ok := index[key]
		if !ok {
			ui = len(units)
			index[key] = ui
			units = append(units, sceneUnit{key: key})
		}
		units[ui].sources = append(units[ui].sources, i)
	}
	// A coherent group that mixes wavelengths cannot be coherent: it was split
	// per wavelength above. The check must look at the sources, not at the
	// units — every unit holds exactly one wavelength by construction, so the
	// previous version could never fire.
	perGroup := map[string]map[int64]bool{}
	for i := range sources {
		g := sources[i].GroupID(i)
		if perGroup[g] == nil {
			perGroup[g] = map[int64]bool{}
		}
		perGroup[g][int64(math.Round(sources[i].EffectiveWavelength(fallbackWL)*1e12))] = true
	}
	warned := map[string]bool{}
	for _, u := range units {
		g := u.key.group
		if warned[g] || len(perGroup[g]) < 2 {
			continue
		}
		warned[g] = true
		warnings.Add("coherent_group_multiple_wavelengths",
			"同一相干组内的光源波长不同，已按非相干处理（分别模拟后叠加强度）", float64(len(perGroup[g])))
	}
	return units
}

// sceneSim evaluates the routed graph for one coherent unit.
type sceneSim struct {
	g       *sceneGraph
	ctx     Context
	method  Method
	wl      float64
	pol     bool
	unitSet map[int]bool
	fields  map[int]*Field // beam id -> field at the launch point
	visits  []*sceneVisit
	planes  []*Plane
	nPl     int
	// dropped counts contributions of this unit that the merge had to skip
	// because their producer visit is evaluated later (a cycle-closing edge,
	// i.e. a further round trip): the run reports them as a warning.
	dropped int
	wlKey   int64
	group   string
}

// sortVisits returns the visits in an evaluation order of the beam graph: a
// visit is evaluated only after every visit that launches a beam into it, so
// all of its contributions are on the grid by the time it is merged.
//
// Sorting by the earliest arrival is NOT enough: a visit fed by a short path
// and a long one has an arrival earlier than the long path's producer, and the
// long contribution would be missing (half the power silently lost in a
// Michelson with unequal arms).
//
// A recirculating layout (a resonance cavity) makes the graph cyclic and no
// order satisfies every edge. The visits the Kahn pass cannot order are then
// emitted in dependency order (the strongly connected components come out
// producers-first), so the *first* round trip through the loop is always
// complete; only a contribution whose producer is evaluated later — an edge
// that closes a cycle, i.e. a further round trip — is skipped. The merge
// counts those skips and the run reports them (scene_cycle_dropped), so light
// is never dropped silently; the result is the two-beam (first round trip)
// approximation.
func sortVisits(g *sceneGraph) []*sceneVisit {
	indeg := make(map[*sceneVisit]int, len(g.visits))
	for _, v := range g.visits {
		for _, in := range v.in {
			if in.beam.producer != nil {
				indeg[v]++
			}
		}
	}
	var ready []*sceneVisit
	for _, v := range g.visits {
		if indeg[v] == 0 {
			ready = append(ready, v)
		}
	}
	sortVisitsByArrival(ready)
	order := make([]*sceneVisit, 0, len(g.visits))
	seen := make(map[*sceneVisit]bool, len(g.visits))
	for len(ready) > 0 {
		v := ready[0]
		ready = ready[1:]
		if seen[v] {
			continue
		}
		seen[v] = true
		order = append(order, v)
		var next []*sceneVisit
		for _, b := range v.out {
			t := b.visit
			if t == nil {
				continue
			}
			if indeg[t]--; indeg[t] == 0 {
				next = append(next, t)
			}
		}
		sortVisitsByArrival(next)
		ready = append(ready, next...)
	}
	if len(order) < len(g.visits) {
		var rest []*sceneVisit
		for _, v := range g.visits {
			if !seen[v] {
				rest = append(rest, v)
			}
		}
		order = append(order, cycleOrder(rest)...)
	}
	return order
}

// sortVisitsByArrival orders visits by their earliest arrival; ties keep the
// input order (the routing order), which keeps the evaluation deterministic.
func sortVisitsByArrival(vs []*sceneVisit) {
	sort.SliceStable(vs, func(i, j int) bool { return vs[i].arrival < vs[j].arrival })
}

// cycleOrder orders a cyclic remainder — every one of these visits is in a
// cycle or downstream of one, so the Kahn pass could not place any of them —
// in dependency order: Kosaraju's algorithm emits the strongly connected
// components in topological order of their condensation (a component that
// consumes beams from another comes after it), and the members of one
// component go in arrival order, which is the causal order of the first
// traversal through the loop.
//
// The edges that close a cycle cannot all be honoured, whatever the order is:
// where a producer ends up later than its consumer, the merge finds no field
// and counts the skipped contribution. Inside one component those edges are
// exactly the second and further round trips.
func cycleOrder(rest []*sceneVisit) []*sceneVisit {
	inRest := make(map[*sceneVisit]bool, len(rest))
	for _, v := range rest {
		inRest[v] = true
	}
	sortVisitsByArrival(rest)
	// First pass: finish order of a DFS over the subgraph.
	state := make(map[*sceneVisit]int, len(rest)) // 0 white, 1 grey, 2 black
	var finish []*sceneVisit
	var dfs1 func(v *sceneVisit)
	dfs1 = func(v *sceneVisit) {
		state[v] = 1
		for _, b := range v.out {
			if t := b.visit; t != nil && inRest[t] && state[t] == 0 {
				dfs1(t)
			}
		}
		state[v] = 2
		finish = append(finish, v)
	}
	for _, v := range rest {
		if state[v] == 0 {
			dfs1(v)
		}
	}
	// Second pass: DFS the transpose in decreasing finish order; each tree is
	// one strongly connected component, and the trees come out producers
	// first.
	trans := make(map[*sceneVisit][]*sceneVisit, len(rest))
	for _, v := range rest {
		for _, in := range v.in {
			if p := in.beam.producer; p != nil && inRest[p] {
				trans[v] = append(trans[v], p)
			}
		}
	}
	seen := make(map[*sceneVisit]bool, len(rest))
	out := make([]*sceneVisit, 0, len(rest))
	var component []*sceneVisit
	var dfs2 func(v *sceneVisit)
	dfs2 = func(v *sceneVisit) {
		seen[v] = true
		component = append(component, v)
		for _, p := range trans[v] {
			if !seen[p] {
				dfs2(p)
			}
		}
	}
	for i := len(finish) - 1; i >= 0; i-- {
		v := finish[i]
		if seen[v] {
			continue
		}
		component = component[:0]
		dfs2(v)
		sortVisitsByArrival(component)
		out = append(out, component...)
	}
	return out
}

func (s *sceneSim) run() error {
	// Seed the source beams of this unit.
	for _, b := range s.g.beams {
		if b.comp != -1 {
			continue
		}
		if !s.unitSet[b.src] {
			continue
		}
		src := s.g.sources[b.src]
		// The source field is built on the grid and then given the tilt that
		// an off-axis emission direction implies, measured against the
		// direction the first component will use as reference.
		f, err := BuildSource(src, s.g.gridSize, s.g.gridWidth, s.pol, s.wl)
		if err != nil {
			return fmt.Errorf("source %d: %v", b.src, err)
		}
		s.fields[b.id] = f
	}
	for _, v := range s.visits {
		merged, err := s.merge(v)
		if err != nil {
			return fmt.Errorf("component %d: %v", v.comp, err)
		}
		if merged == nil {
			continue
		}
		// The contribution fields are only ever read by this visit's merge, so
		// they can be released here: memory then scales with the live beams
		// instead of with every beam ever launched (a split-heavy layout
		// otherwise keeps one full grid per beam for the whole unit).
		for _, in := range v.in {
			delete(s.fields, in.beam.id)
		}
		if s.g.behavior[v.comp] == behaviorSensor {
			if err := s.record(merged, v); err != nil {
				return err
			}
			continue
		}
		if err := s.applyComponent(v, merged); err != nil {
			return fmt.Errorf("component %d (%s): %v", v.comp, s.g.comps[v.comp].Type, err)
		}
		inPow := fieldPower(merged)
		for _, b := range v.out {
			f := merged.Clone()
			f.ScaleAmplitude(b.coef)
			if s.ctx.Bandlimit != nil {
				f.ApplyBandlimit(s.ctx.Bandlimit)
			}
			// A component that left no amplitude (a stop whose opening the
			// light misses) stops the beam for good: it keeps its geometry so
			// downstream detectors still record a reading, but carries no
			// light and is flagged for the layout view.
			if inPow > 0 && fieldPower(f) <= 0 {
				b.blocked = true
			}
			s.fields[b.id] = f
		}
	}
	return nil
}

// merge sums every contribution arriving at a visit, propagating each along its
// own segment, moving its frame onto the component and applying the relative
// wavefront tilt of that contribution.
func (s *sceneSim) merge(v *sceneVisit) (*Field, error) {
	geom := s.g.geoms[v.comp]
	k := 2 * math.Pi / s.wl
	n := s.g.gridSize
	var out *Field
	dx := 0.0
	any := false
	tol := s.g.gridWidth * 1e-6
	for _, in := range v.in {
		f := s.fields[in.beam.id]
		if f == nil {
			// A contribution whose source belongs to this unit but whose field
			// is not on the grid is light this evaluation cannot include: the
			// producer visit is evaluated later, i.e. the beam runs along an
			// edge that closes a cycle. Skipping it is the bounded-round-trip
			// contract; count it so the run reports the truncation instead of
			// losing light silently. Contributions from other units are
			// skipped silently on purpose — they are added incoherently at the
			// end.
			if s.unitSet[in.beam.src] {
				s.dropped++
			}
			continue
		}
		g := f.Clone()
		if err := Propagate(g, in.segLen, s.method, &s.ctx); err != nil {
			return nil, err
		}
		if s.ctx.Bandlimit != nil && in.segLen != 0 {
			g.ApplyBandlimit(s.ctx.Bandlimit)
		}
		// Every contribution must sit on the same sampling grid, otherwise the
		// sum is meaningless: a far-field step re-samples onto a different
		// pixel size, and the merged plane would be labelled with the wrong DX.
		if !any {
			dx = g.DX
			if g.Vectorial {
				out = NewVectorialField(n, dx)
			} else {
				out = NewField(n, dx, s.pol)
			}
		} else if math.Abs(g.DX-dx) > 1e-9*dx {
			return nil, fmt.Errorf("beams sampled differently (%.4g m vs %.4g m per pixel) cannot be combined at %s",
				g.DX, dx, s.g.compName(v.comp))
		}
		// Move the beam's own frame onto the component's frame: the beam axis
		// crosses the component plane at the local point (u0, v0).
		hit := in.beam.from.Add(in.beam.dir.Scale(in.segLen))
		u0, v0 := geom.local(hit)
		if math.Abs(u0) > tol || math.Abs(v0) > tol {
			g.ShiftField(u0, v0)
		}
		// Relative wavefront tilt of this contribution (misalignment fringes).
		if v.deviation(in.beam.dir, geom) != 0 {
			d := in.beam.dir.Sub(v.inDir)
			g.ApplyLinearPhase(k, d.Dot(geom.u), d.Dot(geom.v))
		}
		for i := range out.Ex {
			out.Ex[i] += g.Ex[i]
			if s.pol {
				out.Ey[i] += g.Ey[i]
			}
			if out.Ez != nil && g.Ez != nil {
				out.Ez[i] += g.Ez[i]
			}
		}
		any = true
	}
	if !any {
		return nil, nil
	}
	return out, nil
}

// deviation reports whether the relative-tilt phase should be applied for a
// contribution: it is measured against the visit's first arrival, and only
// small direction differences are treated as misalignment (large ones are the
// design fold, e.g. a 90° mirror, where the wavefront is carried through
// unchanged).
func (v *sceneVisit) deviation(dir Vec3, geom compGeom) float64 {
	d := dir.Sub(v.inDir)
	n := d.Norm()
	if n < 1e-6 {
		return 0
	}
	if n > sceneTiltDesignFold {
		return 0
	}
	return n
}

// applyComponent multiplies the merged field by the component's operator: its
// clear aperture (the shape), then its thin-element transformation.
func (s *sceneSim) applyComponent(v *sceneVisit, f *Field) error {
	comp := &s.g.comps[v.comp]
	sh := s.g.shapes[v.comp]
	if sh != nil && !stopLike(comp.Type) {
		applyShapeMask(f, sh)
	}
	if el := s.g.elements[v.comp]; el != nil {
		return el.Apply(f, &s.ctx)
	}
	return nil
}

// fieldPower returns the total power carried by a field (W).
func fieldPower(f *Field) float64 {
	if f == nil {
		return 0
	}
	var p float64
	for i := range f.Ex {
		p += norm2c(f.Ex[i])
		if f.Polarized && f.Ey != nil {
			p += norm2c(f.Ey[i])
		}
	}
	return p * f.DX * f.DX
}

// record stores a detector plane. A detector reached more than once in the same
// coherent unit adds its contributions: the total field at the plane is the
// sum of everything arriving.
func (s *sceneSim) record(f *Field, v *sceneVisit) error {
	// The detector only measures light falling on its own active area: outside
	// its outline the reading is zero, exactly as for any other component.
	if sh := s.g.shapes[v.comp]; sh != nil {
		applyShapeMask(f, sh)
	}
	comp := &s.g.comps[v.comp]
	id := comp.ID
	if id == "" {
		id = fmt.Sprintf("sensor_%d", v.comp)
	}
	for _, old := range s.planes {
		if old.ID != id {
			continue
		}
		for i := range old.Ex {
			old.Ex[i] += f.Ex[i]
			if f.Polarized && old.Ey != nil && f.Ey != nil {
				old.Ey[i] += f.Ey[i]
			}
			if old.Ez != nil && f.Ez != nil {
				old.Ez[i] += f.Ez[i]
			}
		}
		merged := &Field{N: old.Size, DX: old.DX, Polarized: f.Polarized, Vectorial: f.Vectorial,
			Ex: old.Ex, Ey: old.Ey, Ez: old.Ez}
		old.Stats = ComputeStats(merged, s.wl, pfd(comp.Params, "strehl_aperture", 0), pfd(comp.Params, "strehl_distance", 0))
		return nil
	}
	s.nPl++
	if s.nPl > MaxPlanes {
		return fmt.Errorf("too many output planes (limit %d)", MaxPlanes)
	}
	label := comp.Label
	if label == "" {
		label = comp.ID
	}
	if label == "" {
		label = fmt.Sprintf("探测器 %d", v.comp)
	}
	pl := &Plane{
		ID: id, Label: label, Path: comp.ID, Size: f.N, DX: f.DX,
		Ex: append([]complex128(nil), f.Ex...),
		Ey: append([]complex128(nil), f.Ey...),
		Ez: append([]complex128(nil), f.Ez...),
	}
	pl.Stats = ComputeStats(f, s.wl, pfd(comp.Params, "strehl_aperture", 0), pfd(comp.Params, "strehl_distance", 0))
	pl.Wavelength = s.wl
	pl.UnitKey = unitLabel(s.group, s.wl)
	s.planes = append(s.planes, pl)
	return nil
}

// unitLabel renders a human-facing name for a coherent unit.
func unitLabel(group string, wl float64) string {
	g := group
	if len(g) > 6 && g[:6] == "__solo" {
		g = "单源"
	}
	return fmt.Sprintf("%s@%.1fnm", g, wl*1e9)
}

// applyShapeMask multiplies the field by the component's clear aperture.
func applyShapeMask(f *Field, sh *shapeGeom) {
	n := f.N
	sig := sh.edgeSigma
	for j := 0; j < n; j++ {
		v := f.Y(j)
		for i := 0; i < n; i++ {
			u := f.X(i)
			var t complex128
			d := sh.signedDistance(u, v)
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
}

// SimulateScene runs a positioned-scene configuration: the light path is routed
// from the component geometry and every source is simulated in its own coherent
// unit (same coherent group and wavelength), after which the units' intensities
// are added.
func SimulateScene(cfg Config) (*Result, error) {
	start := time.Now()
	base := &Context{
		Wavelength:         cfg.Wavelength,
		Evanescent:         cfg.Evanescent,
		EvanescentLimit:    cfg.EvanescentLimit,
		BackwardRegularize: cfg.BackwardRegularize,
		TikhonovAlpha:      cfg.TikhonovAlpha,
		Bandlimit:          cfg.Bandlimit,
		Warnings:           &Warnings{},
	}
	if base.Evanescent == "" {
		base.Evanescent = "decay"
	}
	method, err := ParseMethod(cfg.Method)
	if err != nil {
		return nil, err
	}
	sources := cfg.AllSources()
	g, err := BuildSceneGraph(cfg.Scene, sources)
	if err != nil {
		return nil, err
	}
	g.gridSize = cfg.Grid.Size
	g.gridWidth = cfg.Grid.Width
	g.wl = cfg.Wavelength
	if err := g.buildElements(); err != nil {
		return nil, err
	}
	for _, w := range g.warnings {
		base.Warnings.Add("scene_geometry", w, 0)
	}
	units := splitUnits(sources, cfg.Wavelength, base.Warnings)

	var planes []*Plane
	parts := map[string][]PlanePart{}
	dropped := 0
	for _, u := range units {
		wl := cfg.Wavelength
		for _, si := range u.sources {
			wl = sources[si].EffectiveWavelength(cfg.Wavelength)
			break
		}
		uctx := *base
		// The kernel propagates and applies elements at *this* unit's
		// wavelength, not at the configuration default.
		uctx.Wavelength = wl
		sim := &sceneSim{
			g: g, ctx: uctx, method: method, wl: wl, pol: cfg.PolarizationEnabled(),
			unitSet: map[int]bool{}, fields: map[int]*Field{},
			visits: sortVisits(g), wlKey: u.key.wlKey, group: u.key.group,
		}
		for _, si := range u.sources {
			sim.unitSet[si] = true
		}
		if err := sim.run(); err != nil {
			return nil, err
		}
		dropped += sim.dropped
		planes = mergeUnitPlanes(planes, sim.planes)
		for _, pl := range sim.planes {
			parts[pl.ID] = append(parts[pl.ID], PlanePart{
				Source: u.sources[0], Label: pl.UnitKey, Wavelength: pl.Wavelength,
				Power: pl.Stats.Power, Peak: pl.Stats.Peak,
				Ex: pl.Ex, Ey: pl.Ey, Ez: pl.Ez,
			})
		}
	}
	// A recirculating light path is evaluated as its first round trip only
	// (round trips are bounded, see sortVisits): every contribution skipped
	// along a cycle-closing edge was counted by the merge. Never let that be
	// silent — the result is the two-beam approximation, not the full Airy
	// sum.
	if dropped > 0 {
		base.Warnings.Add("scene_cycle_dropped",
			fmt.Sprintf("环路光路只计入首次往返：%d 条再次往返的贡献被跳过（结果为两光束近似）", dropped),
			float64(dropped))
	}
	finishPlanes(planes, parts)
	return &Result{
		RunID:      randomID(),
		Size:       cfg.Grid.Size,
		Width:      cfg.Grid.Width,
		DX:         cfg.Grid.Width / float64(cfg.Grid.Size),
		Wavelength: cfg.Wavelength,
		ElapsedMS:  float64(time.Since(start).Microseconds()) / 1000,
		Warnings:   base.Warnings.List(),
		Planes:     planes,
		Scene:      sceneTraceFromGraph(g),
	}, nil
}

// mergeUnitPlanes combines the planes of one unit with the planes collected so
// far: intensities add, and the field data kept for phase/polarization display
// comes from the strongest unit.
func mergeUnitPlanes(acc []*Plane, add []*Plane) []*Plane {
	if len(acc) == 0 {
		return add
	}
	byID := map[string]*Plane{}
	for _, p := range acc {
		byID[p.ID] = p
	}
	for _, p := range add {
		old, ok := byID[p.ID]
		if !ok {
			acc = append(acc, p)
			byID[p.ID] = p
			continue
		}
		if p.Stats.Power > old.Stats.Power {
			old.Ex, old.Ey, old.Ez = p.Ex, p.Ey, p.Ez
			old.Stats.Strehl = p.Stats.Strehl
			old.Wavelength = p.Wavelength
			old.UnitKey = p.UnitKey
		}
		old.Stats.Power += p.Stats.Power
		if p.Stats.IntensityMax > old.Stats.IntensityMax {
			old.Stats.IntensityMax = p.Stats.IntensityMax
		}
	}
	return acc
}

// intensityOf extracts the per-pixel intensity of a plane (W/m^2).
func intensityOf(pl *Plane) []float64 {
	out := make([]float64, len(pl.Ex))
	for i := range pl.Ex {
		// Merged planes carry no Ey/Ez of their own (the parts hold the real
		// fields), so every component must be probed for nil.
		v := norm2c(pl.Ex[i])
		if pl.Ey != nil {
			v += norm2c(pl.Ey[i])
		}
		if pl.Ez != nil {
			v += norm2c(pl.Ez[i])
		}
		out[i] = v
	}
	return out
}

func norm2c(z complex128) float64 { return real(z)*real(z) + imag(z)*imag(z) }

// buildElements compiles each component into its thin-element operator once.
func (g *sceneGraph) buildElements() error {
	g.elements = make([]Element, len(g.comps))
	for i := range g.comps {
		c := &g.comps[i]
		switch g.behavior[i] {
		case behaviorSensor, behaviorSplit:
			continue
		}
		params := map[string]any{}
		for k, v := range c.Params {
			params[k] = v
		}
		// The reflectivity of a mirror/splitter is applied once, when the
		// outgoing beams are launched (launch owns the amplitude split), so the
		// element itself must stay fully transmissive: otherwise a mirror at
		// reflectivity r would be applied twice and lose light (r^2).
		if g.behavior[i] == behaviorMirror {
			params["reflectivity"] = 1.0
		}
		// The component's position, orientation and clear aperture come from
		// the scene geometry, so the element itself is centred and unlimited
		// (applyComponent multiplies by the outline).
		delete(params, "x")
		delete(params, "y")
		if g.shapes[i] != nil {
			if _, ok := params["aperture"]; ok {
				params["aperture"] = 0
			}
			if stopLike(c.Type) {
				// An aperture *is* its outline: give the element the component's
				// own shape so the mask it applies is the one the user drew.
				for k, v := range g.shapes[i].toParams() {
					params[k] = v
				}
			}
		}
		el, err := NewElement(ElementSpec{Type: sceneElementType(c.Type), Params: params})
		if err != nil {
			return fmt.Errorf("component %d (%s): %v", i, c.Type, err)
		}
		g.elements[i] = el
	}
	return nil
}

// sceneElementType maps a scene component type onto the thin-element
// implementation that carries its phase: the scene aliases (aperture/iris/
// stop/slit) all reduce to the aperture element, and a retro-reflector is a
// plain mirror whose direction is handled by launch.
func sceneElementType(typ string) string {
	switch typ {
	case "iris", "stop", "slit":
		return "aperture"
	case "retro_reflector":
		return "mirror"
	}
	return typ
}

// sceneTraceFromGraph converts a routed graph into the geometry trace the
// layout view consumes.
func sceneTraceFromGraph(g *sceneGraph) *SceneTrace {
	return traceFromGraph(g)
}
