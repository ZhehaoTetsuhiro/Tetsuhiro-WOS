package optics

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeDef writes one definition file into dir and returns its path.
func writeDef(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name+".json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// reloadInto loads a single temporary directory and restores an empty registry
// when the test finishes, so no test leaks scripted elements into another.
func reloadInto(t *testing.T, dirs ...string) DefinitionReport {
	t.Helper()
	rep := ReloadElementDefinitions(dirs)
	t.Cleanup(func() { ReloadElementDefinitions([]string{t.TempDir()}) })
	return rep
}

const metalensJSON = `{
  "name": "metalens",
  "label": "超表面透镜",
  "help": "双曲相位",
  "behavior": "transmit",
  "class": "lens",
  "params": [
    {"key": "f", "label": "焦距", "unit": "m", "kind": "float", "min": 1e-3, "max": 10, "step": 1e-3, "default": 0.3}
  ],
  "phase": "-k*(sqrt(r*r+f*f) - f)",
  "amp": "1"
}`

// The metalens phase must be the two hyperbolic terms, evaluated locally: the
// applied transmission equals exp(i*phase) exactly.
func TestScriptedElementPhaseMatchesAnalytic(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "metalens", metalensJSON)
	rep := reloadInto(t, dir)
	if len(rep.Errors) != 0 || len(rep.Loaded) != 1 {
		t.Fatalf("load report = %+v, want one loaded file and no errors", rep)
	}
	el, err := NewElement(ElementSpec{Type: "metalens", Params: map[string]any{"f": 0.3}})
	if err != nil {
		t.Fatal(err)
	}
	f := NewField(64, 4e-3/64, false)
	for i := range f.Ex {
		f.Ex[i] = 1
	}
	wl := 633e-9
	if err := el.Apply(f, &Context{Wavelength: wl}); err != nil {
		t.Fatal(err)
	}
	k := 2 * math.Pi / wl
	for j := 0; j < f.N; j++ {
		for i := 0; i < f.N; i++ {
			x, y := f.X(i), f.Y(j)
			r := math.Hypot(x, y)
			want := cexpI(-k * (math.Sqrt(r*r+0.09) - 0.3))
			if got := f.Ex[j*f.N+i]; math.Abs(real(got)-real(want)) > 1e-12 || math.Abs(imag(got)-imag(want)) > 1e-12 {
				t.Fatalf("at (%g,%g): t = %v, want %v", x, y, got, want)
			}
		}
	}
}

// A mirror definition is routed as a mirror by the scene layer and its phase is
// applied on reflection.
func TestScriptedMirrorBehavior(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "asphere_mirror", `{
	  "name": "asphere_mirror", "behavior": "mirror",
	  "params": [{"key": "R", "kind": "float", "unit": "m", "default": 0.2}],
	  "phase": "-k*r*r/R"
	}`)
	reloadInto(t, dir)
	got, err := componentBehavior("asphere_mirror")
	if err != nil {
		t.Fatal(err)
	}
	if got != behaviorMirror {
		t.Fatalf("behavior = %d, want behaviorMirror (%d)", got, behaviorMirror)
	}
	if class := componentClass("asphere_mirror"); class != "mirror" {
		t.Fatalf("class = %q, want mirror", class)
	}
	if classes := ComponentClasses(); classes["asphere_mirror"] != "mirror" {
		t.Fatalf("catalog classes = %v, want the scripted mirror in it", classes)
	}
}

// A scripted element reaches the catalog, so the GUI insert dialog and the
// parameter panel pick it up with no client-side list of types.
func TestScriptedElementInCatalog(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "metalens", metalensJSON)
	reloadInto(t, dir)
	var doc *ElementDoc
	for i := range BuildCatalog().Elements {
		if BuildCatalog().Elements[i].Type == "metalens" {
			doc = &BuildCatalog().Elements[i]
		}
	}
	if doc == nil {
		t.Fatal("catalog does not contain the scripted element")
	}
	if !doc.Custom || doc.Label != "超表面透镜" || len(doc.Params) != 1 || doc.Params[0].Key != "f" {
		t.Fatalf("catalog doc = %+v", *doc)
	}
	if doc.Source == "" {
		t.Fatal("catalog doc must name the definition file it came from")
	}
	if !IsScriptedElement("metalens") {
		t.Fatal("IsScriptedElement(metalens) = false")
	}
	if _, ok := ScriptedDefinition("metalens"); !ok {
		t.Fatal("ScriptedDefinition(metalens) not found")
	}
}

func TestScriptedElementAmpAndJones(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "half_absorber", `{"name": "half_absorber", "amp": "0.5"}`)
	writeDef(t, dir, "swapper", `{
	  "name": "swapper", "amp": "1",
	  "jones": {"a": {"re": "0"}, "b": {"re": "1"}, "c": {"re": "1"}, "d": {"re": "0"}}
	}`)
	reloadInto(t, dir)

	el, err := NewElement(ElementSpec{Type: "half_absorber"})
	if err != nil {
		t.Fatal(err)
	}
	f := NewField(8, 1e-3/8, false)
	for i := range f.Ex {
		f.Ex[i] = 2
	}
	if err := el.Apply(f, &Context{Wavelength: 633e-9}); err != nil {
		t.Fatal(err)
	}
	if got := f.Ex[0]; math.Abs(real(got)-1) > 1e-12 || imag(got) != 0 {
		t.Fatalf("amplitude 0.5 on a field of 2 gives %v, want 1", got)
	}

	el, err = NewElement(ElementSpec{Type: "swapper"})
	if err != nil {
		t.Fatal(err)
	}
	pf := NewField(8, 1e-3/8, true)
	for i := range pf.Ex {
		pf.Ex[i] = 2
		pf.Ey[i] = 1
	}
	if err := el.Apply(pf, &Context{Wavelength: 633e-9}); err != nil {
		t.Fatal(err)
	}
	if pf.Ex[0] != 1 || pf.Ey[0] != 2 {
		t.Fatalf("Jones swap gives (Ex,Ey) = (%v, %v), want (1, 2)", pf.Ex[0], pf.Ey[0])
	}

	// A Jones element promotes a scalar field to a vectorial one.
	sf := NewField(8, 1e-3/8, false)
	for i := range sf.Ex {
		sf.Ex[i] = 3
	}
	if err := el.Apply(sf, &Context{Wavelength: 633e-9}); err != nil {
		t.Fatal(err)
	}
	if !sf.Polarized || sf.Ex[0] != 0 || sf.Ey[0] != 3 {
		t.Fatalf("scalar promotion gives Polarized=%v (Ex,Ey) = (%v,%v), want true (0, 3)", sf.Polarized, sf.Ex[0], sf.Ey[0])
	}
}

func TestScriptedDefinitionErrors(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"not JSON", "{", "unexpected EOF"},
		{"unknown field", `{"name": "a1", "phaze": "1"}`, "unknown field"},
		{"bad name", `{"name": "Bad Name", "phase": "1"}`, "must match"},
		{"reserved name", `{"name": "lens", "phase": "1"}`, "built-in"},
		{"bad behavior", `{"name": "a1", "behavior": "split", "phase": "1"}`, "behavior"},
		{"bad class", `{"name": "a1", "class": "splitter", "phase": "1"}`, "class"},
		{"no effect", `{"name": "a1", "params": []}`, "no effect"},
		{"param no default", `{"name": "a1", "phase": "p", "params": [{"key": "p", "kind": "float"}]}`, "default"},
		{"param shadows builtin", `{"name": "a1", "phase": "1", "params": [{"key": "k", "kind": "float", "default": 1}]}`, "shadow"},
		{"param bad kind", `{"name": "a1", "phase": "1", "params": [{"key": "p", "kind": "vector", "default": 1}]}`, "kind"},
		{"unknown variable", `{"name": "a1", "phase": "q*2"}`, "unknown variable"},
		{"unknown function", `{"name": "a1", "phase": "foo(1)"}`, "unknown function"},
		{"syntax error with column", `{"name": "a1", "phase": "1 + "}`, "column 5"},
		{"non-finite probe", `{"name": "a1", "phase": "log(x)"}`, "not finite"},
		{"bad phase unit", `{"name": "a1", "phase_unit": "degree", "phase": "1"}`, "phase_unit"},
	}
	for _, c := range cases {
		_, err := ParseElementDefinition([]byte(c.body), c.name+".json")
		if err == nil {
			t.Errorf("%s: expected an error", c.name)
			continue
		}
		if !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %q does not contain %q", c.name, err.Error(), c.want)
		}
	}
}

// Later directories override earlier ones, and the report says so.
func TestScriptedDefinitionPrecedence(t *testing.T) {
	low, high := t.TempDir(), t.TempDir()
	writeDef(t, low, "metalens", metalensJSON)
	writeDef(t, high, "metalens", strings.Replace(metalensJSON, "超表面透镜", "覆盖版透镜", 1))
	rep := reloadInto(t, low, high)
	if len(rep.Loaded) != 1 || rep.Loaded[0].Label != "覆盖版透镜" {
		t.Fatalf("loaded = %+v, want the higher-priority definition", rep.Loaded)
	}
	if len(rep.Skipped) != 1 || !strings.Contains(rep.Skipped[0].Reason, "overridden") {
		t.Fatalf("skipped = %+v, want one override note", rep.Skipped)
	}
	// A missing directory is not an error.
	rep = ReloadElementDefinitions([]string{filepath.Join(low, "does-not-exist"), high})
	if len(rep.Errors) != 0 || len(rep.Loaded) != 1 {
		t.Fatalf("missing directory must be ignored: %+v", rep)
	}
}

// Reloading replaces the registry: a definition that disappeared is gone from
// the catalog and from NewElement.
func TestScriptedReloadReplaces(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "metalens", metalensJSON)
	reloadInto(t, dir)
	empty := t.TempDir()
	rep := ReloadElementDefinitions([]string{empty})
	if len(rep.Loaded) != 0 {
		t.Fatalf("reload into an empty dir loaded %+v", rep.Loaded)
	}
	if _, err := NewElement(ElementSpec{Type: "metalens"}); err == nil {
		t.Fatal("a removed definition must no longer build")
	}
	if _, ok := ScriptedDefinition("metalens"); ok {
		t.Fatal("a removed definition must no longer be registered")
	}
}

// A component of an unloaded scripted type reports where to look.
func TestUnknownScriptedTypeMessage(t *testing.T) {
	reloadInto(t, t.TempDir())
	_, err := componentBehavior("metalens")
	if err == nil || !strings.Contains(err.Error(), "elements/") {
		t.Fatalf("error = %v, want a hint about the definition file", err)
	}
}

func TestSampleScriptedMask(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "vortex", `{
	  "name": "vortex",
	  "params": [{"key": "l", "kind": "int", "default": 1}],
	  "phase": "l*th",
	  "phase_unit": "rad"
	}`)
	writeDef(t, dir, "qwave", `{
	  "name": "qwave",
	  "phase": "0.25",
	  "phase_unit": "waves"
	}`)
	reloadInto(t, dir)

	const n = 5
	const width = 2e-3
	dx := width / n
	amp, phase, err := SampleScriptedMask("vortex", nil, n, width, 633e-9)
	if err != nil {
		t.Fatal(err)
	}
	if len(amp) != n*n || len(phase) != n*n {
		t.Fatalf("mask size = %d/%d, want %d", len(amp), len(phase), n*n)
	}
	for j := 0; j < n; j++ {
		y := (float64(j) - float64(n)/2) * dx
		for i := 0; i < n; i++ {
			x := (float64(i) - float64(n)/2) * dx
			idx := j*n + i
			if amp[idx] != 1 {
				t.Fatalf("amp[%d] = %g, want 1", idx, amp[idx])
			}
			want := math.Remainder(math.Atan2(y, x), 2*math.Pi)
			if math.Abs(phase[idx]-want) > 1e-12 {
				t.Fatalf("phase at (%g, %g) = %g, want %g", x, y, phase[idx], want)
			}
			if phase[idx] < -math.Pi || phase[idx] > math.Pi {
				t.Fatalf("phase[%d] = %g, want wrapped into [-pi, pi]", idx, phase[idx])
			}
		}
	}

	// phase_unit "waves" scales by 2π.
	_, ph, err := SampleScriptedMask("qwave", nil, 2, 1e-3, 633e-9)
	if err != nil {
		t.Fatal(err)
	}
	for i, v := range ph {
		if math.Abs(v-math.Pi/2) > 1e-12 {
			t.Fatalf("waves phase[%d] = %g, want pi/2", i, v)
		}
	}
}

// The default parameter set is what the loader probes, and the probe must
// reject a definition whose default values are singular.
func TestScriptedParamOverridesAtBuild(t *testing.T) {
	dir := t.TempDir()
	writeDef(t, dir, "expander", `{
	  "name": "expander",
	  "params": [{"key": "g", "kind": "float", "default": 1}],
	  "amp": "g"
	}`)
	reloadInto(t, dir)
	el, err := NewElement(ElementSpec{Type: "expander", Params: map[string]any{"g": 0.25}})
	if err != nil {
		t.Fatal(err)
	}
	f := NewField(4, 1e-3/4, false)
	for i := range f.Ex {
		f.Ex[i] = 1
	}
	if err := el.Apply(f, &Context{Wavelength: 633e-9}); err != nil {
		t.Fatal(err)
	}
	if got := real(f.Ex[0]); math.Abs(got-0.25) > 1e-12 {
		t.Fatalf("amp g=0.25 gives %g, want 0.25", got)
	}
	// A non-numeric parameter value is a clear error, not a silent zero.
	if _, err := NewElement(ElementSpec{Type: "expander", Params: map[string]any{"g": "many"}}); err == nil {
		t.Fatal("non-numeric parameter value must be rejected")
	}
}
