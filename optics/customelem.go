package optics

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Scripted elements (元件定义文件)
//
// A scripted element is a thin-element operator described by a JSON definition
// file instead of Go code: a name, a parameter list (the same ParamSpec the
// catalog-driven GUI renders for built-in elements), a phase and/or amplitude
// expression over the element's local (x, y) coordinates, and optionally a
// 2×2 Jones matrix. Loading a directory of such files registers them like
// built-in elements — they appear in the GUI insert dialog, work in scenes and
// in element trains, and need no recompilation.
//
// The same operator can later be "graduated" into native Go source with
// `wos -gen-go file.json` (see exprgen.go), which prints a kernel element with
// the expressions inlined.
//
// File layout (elements/metalens.json):
//
//	{
//	  "name": "metalens",
//	  "label": "超表面透镜（双曲相位）",
//	  "help": "φ(r) = -k(√(r²+f²) − f)",
//	  "behavior": "transmit",          // or "mirror"
//	  "params": [ {...ParamSpec...} ],
//	  "phase": "-k*(sqrt(r*r+f*f) - f)",
//	  "phase_unit": "rad",             // or "waves"
//	  "amp": "1",
//	  "jones": {"a": {"re": "1"}, "b": {"re": "0"},
//	            "c": {"re": "0"}, "d": {"re": "-1"}}
//	}
// ---------------------------------------------------------------------------

// ElementDefinition is one scripted element definition as read from its file.
type ElementDefinition struct {
	Name      string      `json:"name"`
	Label     string      `json:"label,omitempty"`
	Help      string      `json:"help,omitempty"`
	Behavior  string      `json:"behavior,omitempty"` // "transmit" (default) | "mirror"
	Class     string      `json:"class,omitempty"`    // GUI class: lens|mirror|stop|other
	Params    []ParamSpec `json:"params,omitempty"`
	Phase     string      `json:"phase,omitempty"`
	PhaseUnit string      `json:"phase_unit,omitempty"` // "rad" (default) | "waves"
	Amp       string      `json:"amp,omitempty"`
	Jones     *JonesSpec  `json:"jones,omitempty"`

	// Source is the file the definition was loaded from (not part of the file).
	Source string `json:"-"`
}

// JonesEntry is one entry of a Jones matrix, as two expressions.
type JonesEntry struct {
	Re string `json:"re,omitempty"`
	Im string `json:"im,omitempty"`
}

// JonesSpec is a full 2×2 Jones matrix given as expressions.
type JonesSpec struct {
	A JonesEntry `json:"a"`
	B JonesEntry `json:"b"`
	C JonesEntry `json:"c"`
	D JonesEntry `json:"d"`
}

// scriptedDef is the compiled form of a definition, kept in the registry.
type scriptedDef struct {
	def        ElementDefinition
	behavior   int
	class      string
	phaseScale float64 // 1 (radians) or 2π (waves)
	doc        ElementDoc
}

var (
	scriptedMu    sync.RWMutex
	scriptedElems = map[string]*scriptedDef{}
	scriptedDirs  []string // dirs the registry was last loaded from
)

// nameRe restricts element names to identifiers so a definition name is safely
// usable as an element type in JSON configs and as a Go identifier in generated
// source.
var nameRe = regexp.MustCompile(`^[a-z][a-z0-9_]{0,47}$`)

// paramKeyRe allows the usual optics symbols (R, T, n, NA, ...) but keeps a
// parameter key a single identifier.
var paramKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,31}$`)

// ParseElementDefinition parses and validates one definition. source is the
// file path (empty for a literal definition); it only labels diagnostics.
func ParseElementDefinition(data []byte, source string) (*scriptedDef, error) {
	var def ElementDefinition
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&def); err != nil {
		return nil, fmt.Errorf("%s: %v", sourceLabel(source), err)
	}
	def.Source = source
	cd, err := compileDefinition(&def)
	if err != nil {
		return nil, fmt.Errorf("%s: %v", sourceLabel(source), err)
	}
	return cd, nil
}

func sourceLabel(source string) string {
	if source == "" {
		return "definition"
	}
	return filepath.Base(source)
}

// compileDefinition validates a parsed definition and compiles its expressions.
func compileDefinition(def *ElementDefinition) (*scriptedDef, error) {
	if !nameRe.MatchString(def.Name) {
		return nil, fmt.Errorf("name %q must match %s (lowercase identifier, ≤48 chars)", def.Name, nameRe.String())
	}
	if isReservedComponentName(def.Name) {
		return nil, fmt.Errorf("name %q is already a built-in component type", def.Name)
	}
	behavior := def.Behavior
	if behavior == "" {
		behavior = "transmit"
	}
	class := def.Class
	switch behavior {
	case "transmit":
		if class == "" {
			class = "other"
		}
	case "mirror":
		if class == "" {
			class = "mirror"
		}
	default:
		return nil, fmt.Errorf("behavior %q must be \"transmit\" or \"mirror\" (a scripted element cannot be a splitter or detector)", def.Behavior)
	}
	switch class {
	case "lens", "mirror", "stop", "other":
	default:
		return nil, fmt.Errorf("class %q must be lens, mirror, stop or other", class)
	}
	def.Class = class // normalized: the docs and the generator both read it
	// Parameters: each needs a key, a numeric-ish kind and a default (the GUI
	// inserts a component with the defaults, so a missing one would break it).
	seen := map[string]bool{}
	for i := range def.Params {
		p := &def.Params[i]
		if !paramKeyRe.MatchString(p.Key) {
			return nil, fmt.Errorf("params[%d].key %q must be an identifier like R, f, l or n_o", i, p.Key)
		}
		if seen[p.Key] {
			return nil, fmt.Errorf("duplicate parameter %q", p.Key)
		}
		seen[p.Key] = true
		if isReservedQueryKey(p.Key) {
			return nil, fmt.Errorf("parameter %q collides with the preview endpoint's own query parameters (%s)", p.Key, strings.Join(reservedQueryKeys, ", "))
		}
		if exprBuiltinName(p.Key) {
			return nil, fmt.Errorf("parameter %q shadows a built-in variable (%s)", p.Key, "x, y, r, th, wl, k, pi, e")
		}
		switch p.Kind {
		case "float", "int", "bool", "choice", "text":
		default:
			return nil, fmt.Errorf("parameter %q: kind %q must be float, int, bool, choice or text", p.Key, p.Kind)
		}
		if p.Default == nil {
			return nil, fmt.Errorf("parameter %q needs a default value", p.Key)
		}
		if p.Label == "" {
			p.Label = p.Key
		}
	}
	scale := 1.0
	switch def.PhaseUnit {
	case "", "rad":
	case "waves":
		scale = 2 * math.Pi
	default:
		return nil, fmt.Errorf("phase_unit %q must be \"rad\" or \"waves\"", def.PhaseUnit)
	}
	if strings.TrimSpace(def.Phase) == "" && strings.TrimSpace(def.Amp) == "" && def.Jones == nil {
		return nil, fmt.Errorf("definition has no effect: set phase, amp or jones")
	}
	if _, err := defaultParamValues(def); err != nil {
		return nil, err
	}
	cd := &scriptedDef{def: *def, class: class, phaseScale: scale}
	switch behavior {
	case "transmit":
		cd.behavior = behaviorTransmit
	case "mirror":
		cd.behavior = behaviorMirror
	}
	cd.doc = ElementDoc{
		Type: def.Name, Label: labelOrDefault(def.Label, def.Name), Help: def.Help,
		Params: append([]ParamSpec(nil), def.Params...), Class: class, Custom: true, Source: def.Source,
	}
	// Compile once with the defaults (catches unknown variables), then probe
	// the operator so a definition that divides by zero is reported at load.
	// The file label is added by the caller (ParseElementDefinition /
	// GenerateElementGo), so these messages carry only the element's own
	// context.
	el, err := cd.build(nil)
	if err != nil {
		return nil, err
	}
	if err := probeElement(el); err != nil {
		return nil, err
	}
	return cd, nil
}

func labelOrDefault(label, fallback string) string {
	if strings.TrimSpace(label) == "" {
		return fallback
	}
	return label
}

// exprBuiltinName reports whether a name is a built-in expression variable.
func exprBuiltinName(name string) bool {
	_, ok := exprBuiltins[name]
	return ok
}

// defaultParamValues extracts the numeric default of every parameter. A text
// parameter is allowed to be non-numeric (it just cannot be referenced from an
// expression).
func defaultParamValues(def *ElementDefinition) (map[string]float64, error) {
	out := map[string]float64{}
	for _, p := range def.Params {
		v, err := paramNumericValue(p.Default)
		if err != nil {
			if p.Kind == "text" {
				continue
			}
			return nil, fmt.Errorf("parameter %q: %v", p.Key, err)
		}
		out[p.Key] = v
	}
	return out, nil
}

// bindParams converts the parameters of one element instance the way
// paramNumericValue does; text parameters that do not reduce to a number are
// left unbound so referencing them fails with a clear message.
func (cd *scriptedDef) bindParams(params map[string]any) (map[string]float64, error) {
	bound := map[string]float64{}
	for _, p := range cd.def.Params {
		raw, ok := params[p.Key]
		if !ok || raw == nil {
			raw = p.Default
		}
		v, err := paramNumericValue(raw)
		if err != nil {
			if p.Kind == "text" {
				continue
			}
			return nil, fmt.Errorf("%s: parameter %q: %v", cd.def.Name, p.Key, err)
		}
		bound[p.Key] = v
	}
	return bound, nil
}

// compileParamExpr compiles one expression of the definition, explaining a
// reference to a non-numeric (text) parameter in terms the author can act on.
func (cd *scriptedDef) compileParamExpr(src string, bound map[string]float64) (*CompiledExpr, error) {
	e, err := CompileExpr(src, bound)
	if err != nil {
		if ee, ok := err.(*ExprError); ok && ee.Name != "" {
			for _, p := range cd.def.Params {
				if p.Key == ee.Name && p.Kind == "text" {
					return nil, fmt.Errorf("column %d: parameter %q has kind \"text\": a text parameter cannot be used in an expression", ee.Pos, p.Key)
				}
			}
		}
		return nil, err
	}
	return e, nil
}

// paramNumericValue converts a parameter value to the number expressions see.
func paramNumericValue(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64, int, int64:
		return asFloat(v)
	case bool:
		if v {
			return 1, nil
		}
		return 0, nil
	case string:
		var f float64
		if _, err := fmt.Sscanf(strings.TrimSpace(v), "%g", &f); err == nil {
			return f, nil
		}
		return 0, fmt.Errorf("value %q is not numeric (only float/int/bool/choice parameters can be used in expressions)", v)
	case nil:
		return 0, nil
	}
	// JSON numbers decoded into any: try the generic path.
	f, err := asFloat(raw)
	if err != nil {
		return 0, fmt.Errorf("value of type %T is not numeric", raw)
	}
	return f, nil
}

// probeElement evaluates an operator on a sample grid so a definition that
// divides by zero (or takes the sqrt of a negative number) is reported at load
// time instead of poisoning a simulation with NaN.
func probeElement(el Element) error {
	const n = 33
	const width = 4e-3 // ±2 mm: larger than any reasonable small optic
	f := NewField(n, width/float64(n), true)
	for i := range f.Ex {
		f.Ex[i] = 1
	}
	if err := el.Apply(f, &Context{Wavelength: 632.8e-9}); err != nil {
		return err
	}
	for j := 0; j < n; j++ {
		y := f.Y(j)
		for i := 0; i < n; i++ {
			idx := j*n + i
			if !finiteComplex(f.Ex[idx]) || !finiteComplex(f.Ey[idx]) {
				return fmt.Errorf("expression is not finite at (x, y) = (%.3g, %.3g) m with the default parameters (check division by zero, sqrt of a negative number, log of zero, ...)", f.X(i), y)
			}
		}
	}
	return nil
}

func finiteComplex(c complex128) bool {
	return !math.IsNaN(real(c)) && !math.IsInf(real(c), 0) &&
		!math.IsNaN(imag(c)) && !math.IsInf(imag(c), 0)
}

// ---- the element -----------------------------------------------------------

// scriptedElement is one instantiation of a scripted definition, with its
// parameters already bound.
type scriptedElement struct {
	cd      *scriptedDef
	phase   *CompiledExpr
	amp     *CompiledExpr
	jones   [4]*CompiledExpr // complex part: real parts
	jonesIm [4]*CompiledExpr // and imaginary parts of the Jones entries
	scale   float64
	// precomputed flags for the hot loop
	needR, needT bool
	anyJones     bool
}

// build instantiates the element; params may be nil to use the defaults.
func (cd *scriptedDef) build(params map[string]any) (Element, error) {
	bound, err := cd.bindParams(params)
	if err != nil {
		return nil, err
	}
	el := &scriptedElement{cd: cd, scale: cd.phaseScale}
	if err := cd.compileInto(bound, el); err != nil {
		return nil, fmt.Errorf("%s: %v", cd.def.Name, err)
	}
	return el, nil
}

// compileInto compiles the expressions with bound values into the element.
func (cd *scriptedDef) compileInto(bound map[string]float64, el *scriptedElement) error {
	compile := func(src string) (*CompiledExpr, error) {
		e, err := cd.compileParamExpr(src, bound)
		if err != nil {
			return nil, err
		}
		if e.NeedsRadius() {
			el.needR = true
		}
		if e.NeedsTheta() {
			el.needT = true
		}
		return e, nil
	}
	var err error
	if strings.TrimSpace(cd.def.Phase) != "" {
		if el.phase, err = compile(cd.def.Phase); err != nil {
			return fmt.Errorf("phase: %v", err)
		}
	}
	if strings.TrimSpace(cd.def.Amp) != "" {
		if el.amp, err = compile(cd.def.Amp); err != nil {
			return fmt.Errorf("amp: %v", err)
		}
	}
	if cd.def.Jones != nil {
		entries := [][2]string{
			{cd.def.Jones.A.Re, cd.def.Jones.A.Im},
			{cd.def.Jones.B.Re, cd.def.Jones.B.Im},
			{cd.def.Jones.C.Re, cd.def.Jones.C.Im},
			{cd.def.Jones.D.Re, cd.def.Jones.D.Im},
		}
		defaults := [4]string{"1", "0", "0", "1"}
		for i := range entries {
			re, im := strings.TrimSpace(entries[i][0]), strings.TrimSpace(entries[i][1])
			if re == "" {
				re = defaults[i]
			}
			if im == "" {
				im = "0"
			}
			if el.jones[i], err = compile(re); err != nil {
				return fmt.Errorf("jones entry %d real part: %v", i, err)
			}
			if el.jonesIm[i], err = compile(im); err != nil {
				return fmt.Errorf("jones entry %d imaginary part: %v", i, err)
			}
		}
		el.anyJones = true
	}
	return nil
}

// Apply multiplies the field by the scripted operator. Rows are split across
// the available CPUs: each row is independent, and the expressions are pure.
func (e *scriptedElement) Apply(f *Field, ctx *Context) error {
	n := f.N
	wl := ctx.Wavelength
	k := 2 * math.Pi / wl
	// A Jones matrix turns a scalar field into a vectorial one; do the
	// promotion once, before the workers touch the field.
	if e.anyJones && !f.Polarized {
		f.Polarized = true
		if f.Ey == nil {
			f.Ey = make([]complex128, len(f.Ex))
		}
	}
	workers := runtime.GOMAXPROCS(0)
	if workers > n {
		workers = n
	}
	chunk := (n + workers - 1) / workers
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		j0 := w * chunk
		j1 := j0 + chunk
		if j1 > n {
			j1 = n
		}
		if j0 >= j1 {
			break
		}
		wg.Add(1)
		go func(j0, j1 int) {
			defer wg.Done()
			env := &exprEnv{wl: wl, k: k}
			for j := j0; j < j1; j++ {
				env.y = f.Y(j)
				base := j * n
				for i := 0; i < n; i++ {
					env.x = f.X(i)
					if e.needR {
						env.r = math.Hypot(env.x, env.y)
					}
					if e.needT {
						env.th = math.Atan2(env.y, env.x)
					}
					var t complex128 = 1
					if e.amp != nil {
						t = complex(e.amp.EvalEnv(env), 0)
					}
					if e.phase != nil {
						t *= cexpI(e.phase.EvalEnv(env) * e.scale)
					}
					idx := base + i
					if e.anyJones {
						ex, ey := f.Ex[idx], f.Ey[idx]
						a, b := e.jonesValue(0, env), e.jonesValue(1, env)
						c, d := e.jonesValue(2, env), e.jonesValue(3, env)
						f.Ex[idx] = (a*ex + b*ey) * t
						f.Ey[idx] = (c*ex + d*ey) * t
						continue
					}
					f.Ex[idx] *= t
					if f.Polarized && f.Ey != nil {
						f.Ey[idx] *= t
					}
				}
			}
		}(j0, j1)
	}
	wg.Wait()
	return nil
}

// jonesValue evaluates one Jones entry at the current point.
func (e *scriptedElement) jonesValue(i int, env *exprEnv) complex128 {
	return complex(e.jones[i].EvalEnv(env), e.jonesIm[i].EvalEnv(env))
}

// ---- registry --------------------------------------------------------------

// scriptedElementFor returns the compiled definition registered under a name.
func scriptedElementFor(name string) (*scriptedDef, bool) {
	scriptedMu.RLock()
	defer scriptedMu.RUnlock()
	cd, ok := scriptedElems[name]
	return cd, ok
}

// IsScriptedElement reports whether a component type comes from a definition
// file rather than from the kernel.
func IsScriptedElement(name string) bool {
	_, ok := scriptedElementFor(name)
	return ok
}

// ScriptedElementDocs returns the catalog documentation of every registered
// scripted element, in name order.
func ScriptedElementDocs() []ElementDoc {
	scriptedMu.RLock()
	defer scriptedMu.RUnlock()
	out := make([]ElementDoc, 0, len(scriptedElems))
	for _, cd := range scriptedElems {
		out = append(out, cd.doc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// reservedComponentNames are structural component types the scene router knows
// by name (they are not thin elements in the registry).
var reservedComponentNames = []string{
	"sensor", "detector", "beamsplitter", "bs", "iris", "stop", "slit",
	"retro_reflector", "propagate", "combiner", "source", "laser",
}

// isReservedComponentName reports whether a name is already taken by a built-in
// (or structural) component type, so a definition file cannot silently shadow
// it. Scripted elements already loaded do not count: reloading a directory must
// accept the same names again.
func isReservedComponentName(name string) bool {
	if _, ok := elementRegistry[name]; ok {
		return true
	}
	for _, r := range reservedComponentNames {
		if name == r {
			return true
		}
	}
	return false
}

// ---- loading ---------------------------------------------------------------

// DefinitionInfo describes one successfully loaded definition.
type DefinitionInfo struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Behavior string `json:"behavior"`
	Class    string `json:"class"`
	Params   int    `json:"params"`
	Source   string `json:"source"`
}

// DefinitionNote records a definition that was not loaded.
type DefinitionNote struct {
	Source string `json:"source"`
	Name   string `json:"name,omitempty"`
	Reason string `json:"reason"`
}

// DefinitionReport is the result of one reload of the definition directory set.
type DefinitionReport struct {
	Dirs    []string          `json:"dirs"`
	Loaded  []DefinitionInfo  `json:"loaded"`
	Skipped []DefinitionNote  `json:"skipped"`
	Errors  []DefinitionError `json:"errors"`
	// Version increments on every reload so a client can tell a reload apart
	// from a no-op.
	Version int `json:"version"`
}

// DefinitionError is one file that failed to load.
type DefinitionError struct {
	Source  string `json:"source"`
	Message string `json:"message"`
}

var (
	scriptedVersion int
)

// DefaultElementDirs returns the directories scanned when the caller does not
// name any, in increasing priority: next to the executable, the working
// directory, then the user's home. Duplicates are removed.
func DefaultElementDirs() []string {
	var dirs []string
	add := func(d string) {
		if d == "" {
			return
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			return
		}
		for _, old := range dirs {
			if old == abs {
				return
			}
		}
		dirs = append(dirs, abs)
	}
	if exe, err := os.Executable(); err == nil {
		add(filepath.Join(filepath.Dir(exe), "elements"))
	}
	if wd, err := os.Getwd(); err == nil {
		add(filepath.Join(wd, "elements"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".wos", "elements"))
	}
	return dirs
}

// reservedQueryKeys are the query parameters of the element preview endpoint;
// a definition parameter may not use these names (the preview could not tell a
// parameter from its own control).
var reservedQueryKeys = []string{
	"kind", "size", "width", "wl", "mask", "scale", "cmap", "pmin", "pmax", "gamma", "part",
}

// isReservedQueryKey reports whether a parameter key collides with the preview
// endpoint's own query parameters.
func isReservedQueryKey(key string) bool {
	for _, r := range reservedQueryKeys {
		if key == r {
			return true
		}
	}
	return false
}

// SetElementDirs sets the directories that later reloads scan (nil restores the
// default set). The CLI calls it once at startup with the -elements flag
// appended to the defaults.
func SetElementDirs(dirs []string) {
	scriptedMu.Lock()
	defer scriptedMu.Unlock()
	scriptedDirs = append([]string(nil), dirs...)
}

// ElementDirs returns the directories reloads scan (the configured set, or the
// set a previous reload used).
func ElementDirs() []string {
	scriptedMu.RLock()
	defer scriptedMu.RUnlock()
	return append([]string(nil), scriptedDirs...)
}

// ReloadElementDefinitions rescans dirs (or the default set when dirs is empty)
// and replaces the scripted-element registry. Files earlier in the list are
// overridden by files later in it (the CLI appends user dirs after the
// defaults). A missing directory is not an error: it simply contributes
// nothing. The report always lists what happened per file.
func ReloadElementDefinitions(dirs []string) DefinitionReport {
	if len(dirs) == 0 {
		dirs = ElementDirs()
	}
	if len(dirs) == 0 {
		dirs = DefaultElementDirs()
	}
	rep := DefinitionReport{Dirs: append([]string(nil), dirs...)}
	type candidate struct {
		path string
		cd   *scriptedDef
	}
	var cands []candidate
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			if !os.IsNotExist(err) {
				rep.Errors = append(rep.Errors, DefinitionError{Source: dir, Message: err.Error()})
			}
			continue
		}
		var files []string
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".json") {
				continue
			}
			files = append(files, filepath.Join(dir, ent.Name()))
		}
		sort.Strings(files)
		for _, path := range files {
			data, err := os.ReadFile(path)
			if err != nil {
				rep.Errors = append(rep.Errors, DefinitionError{Source: path, Message: err.Error()})
				continue
			}
			cd, err := ParseElementDefinition(data, path)
			if err != nil {
				rep.Errors = append(rep.Errors, DefinitionError{Source: path, Message: err.Error()})
				continue
			}
			cands = append(cands, candidate{path: path, cd: cd})
		}
	}
	// Later definitions win: an explicit directory overrides a default one, a
	// home-directory file overrides the shipped example of the same name.
	final := map[string]*scriptedDef{}
	order := make([]string, 0, len(cands))
	for _, c := range cands {
		if prev, ok := final[c.cd.def.Name]; ok {
			rep.Skipped = append(rep.Skipped, DefinitionNote{Source: prev.def.Source, Name: c.cd.def.Name,
				Reason: fmt.Sprintf("overridden by the definition from %s", c.path)})
		} else {
			order = append(order, c.cd.def.Name)
		}
		final[c.cd.def.Name] = c.cd
	}
	scriptedMu.Lock()
	scriptedElems = final
	scriptedDirs = append([]string(nil), dirs...)
	scriptedVersion++
	rep.Version = scriptedVersion
	for _, name := range order {
		d := final[name]
		rep.Loaded = append(rep.Loaded, DefinitionInfo{Name: name, Label: d.doc.Label,
			Behavior: d.def.Behavior, Class: d.class, Params: len(d.def.Params), Source: d.def.Source})
	}
	scriptedMu.Unlock()
	sort.Slice(rep.Loaded, func(i, j int) bool { return rep.Loaded[i].Name < rep.Loaded[j].Name })
	return rep
}

// ParamsFromStrings converts raw string values (URL query parameters, form
// fields) into the typed parameter values a scripted element expects, guided by
// the definition's parameter specs. Keys the definition does not declare are
// ignored so the caller can pass a whole query string.
func ParamsFromStrings(name string, raw map[string]string) (map[string]any, error) {
	cd, ok := scriptedElementFor(name)
	if !ok {
		return nil, fmt.Errorf("no scripted element %q is loaded", name)
	}
	out := map[string]any{}
	for _, p := range cd.def.Params {
		v, ok := raw[p.Key]
		if !ok {
			continue
		}
		switch p.Kind {
		case "float", "int":
			f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
			if err != nil {
				return nil, fmt.Errorf("parameter %q: %q is not a number", p.Key, v)
			}
			if p.Kind == "int" {
				f = math.Round(f)
			}
			out[p.Key] = f
		case "bool":
			out[p.Key] = v == "1" || strings.EqualFold(v, "true") || strings.EqualFold(v, "on")
		default:
			out[p.Key] = v
		}
	}
	return out, nil
}

// DefinitionDirsSignature fingerprints the definition files in dirs: each
// directory's path, then every .json file's name, size, modification time and
// content hash, in sorted order. Two calls differ whenever a definition is
// added, edited or removed, which is what a poller needs — the newest-file
// modification time it replaces could only ever rise, so deleting the most
// recently written definition looked like "no change" and the deleted element
// stayed registered until a restart (or an explicit reload). Only .json files
// count, so dropping an unrelated file into the directory does not trigger a
// reload.
//
// The content hash is what keeps the fingerprint honest about in-place
// rewrites: size and mtime alone are not enough. A same-length edit written
// within one timestamp tick leaves both unchanged, which is the ordinary case
// on tmpfs, overlayfs and many network mounts, where timestamp granularity is
// far coarser than the write path — the edit would go unnoticed until the next
// restart. Hashing the bytes (definitions are small JSON documents, read once
// per poll) makes the signature depend on the content itself, so the watcher
// sees every rewrite on every file system.
func DefinitionDirsSignature(dirs []string) string {
	var b strings.Builder
	for _, dir := range dirs {
		b.WriteString(dir)
		b.WriteByte('\n')
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		var files []string
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(strings.ToLower(ent.Name()), ".json") {
				continue
			}
			files = append(files, ent.Name())
		}
		sort.Strings(files)
		for _, name := range files {
			path := filepath.Join(dir, name)
			info, err := os.Stat(path)
			if err != nil {
				b.WriteString(name + "	missing\n")
				continue
			}
			b.WriteString(name)
			b.WriteByte('	')
			b.WriteString(strconv.FormatInt(info.Size(), 10))
			b.WriteByte('	')
			b.WriteString(strconv.FormatInt(info.ModTime().UnixNano(), 10))
			b.WriteByte('	')
			if data, rerr := os.ReadFile(path); rerr != nil {
				// Unreadable this poll (permissions, removed in between): say
				// so instead of reusing a stale hash, and let the next poll
				// look again.
				b.WriteString("unreadable")
			} else {
				sum := sha256.Sum256(data)
				b.WriteString(hex.EncodeToString(sum[:]))
			}
			b.WriteByte('\n')
		}
	}
	return b.String()
}

// ScriptedElementsVersion returns a counter that increments on every reload of
// the definition directories, so a client can tell a reload apart from a no-op.
func ScriptedElementsVersion() int {
	scriptedMu.RLock()
	defer scriptedMu.RUnlock()
	return scriptedVersion
}

// ScriptedDefinition returns a copy of a loaded definition for inspection
// (the preview endpoint and the docs use it).
func ScriptedDefinition(name string) (ElementDefinition, bool) {
	cd, ok := scriptedElementFor(name)
	if !ok {
		return ElementDefinition{}, false
	}
	def := cd.def
	def.Params = append([]ParamSpec(nil), cd.def.Params...)
	return def, true
}

// SampleScriptedMask evaluates a scripted operator over an n×n grid spanning
// width meters at wavelength wl (used by the GUI's mask preview). It returns
// the amplitude |t| and the wrapped phase of each point, row-major.
func SampleScriptedMask(name string, params map[string]any, n int, width, wl float64) (amp, phase []float64, err error) {
	cd, ok := scriptedElementFor(name)
	if !ok {
		return nil, nil, fmt.Errorf("no scripted element %q is loaded", name)
	}
	el, err := cd.build(params)
	if err != nil {
		return nil, nil, err
	}
	se := el.(*scriptedElement)
	if n < 2 {
		n = 2
	}
	amp = make([]float64, n*n)
	phase = make([]float64, n*n)
	env := &exprEnv{wl: wl, k: 2 * math.Pi / wl}
	dx := width / float64(n)
	for j := 0; j < n; j++ {
		env.y = (float64(j) - float64(n)/2) * dx
		for i := 0; i < n; i++ {
			env.x = (float64(i) - float64(n)/2) * dx
			if se.needR {
				env.r = math.Hypot(env.x, env.y)
			}
			if se.needT {
				env.th = math.Atan2(env.y, env.x)
			}
			a := 1.0
			if se.amp != nil {
				a = se.amp.EvalEnv(env)
			}
			ph := 0.0
			if se.phase != nil {
				ph = se.phase.EvalEnv(env) * se.scale
			}
			idx := j*n + i
			amp[idx] = a
			phase[idx] = math.Remainder(ph, 2*math.Pi)
		}
	}
	return amp, phase, nil
}
