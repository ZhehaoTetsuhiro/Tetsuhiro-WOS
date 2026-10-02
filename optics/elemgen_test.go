package optics

import (
	"math"
	"os"
	"strings"
	"testing"
)

// The generated element (optics/elemgen_metalens.go) must stay the exact output
// of the generator for elements/metalens.json: the file is the generator's
// regression oracle, so drift in either direction is a failure here.
func TestGeneratedSourceIsUpToDate(t *testing.T) {
	const defPath = "../elements/metalens.json"
	const filePath = "elemgen_metalens.go"
	def, err := LoadElementDefinitionFile(defPath)
	if err != nil {
		t.Fatalf("load %s: %v", defPath, err)
	}
	want, err := GenerateElementGo(def, "metalens_native")
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	// The header comment quotes the invocation path, so only the body (from
	// the package clause on) is compared; that is the part that must match.
	if bodyOf(string(got)) != bodyOf(want) {
		t.Fatalf("%s is not the current generator output; regenerate with:\n  wos -gen-go elements/metalens.json -gen-name metalens_native > optics/%s", filePath, filePath)
	}
	// Generation is deterministic.
	again, err := GenerateElementGo(def, "metalens_native")
	if err != nil {
		t.Fatal(err)
	}
	if again != want {
		t.Fatal("GenerateElementGo is not deterministic")
	}
}

// bodyOf strips the generated header comment (which quotes the path the
// generator was invoked with) so the comparison covers the code.
func bodyOf(src string) string {
	if i := strings.Index(src, "package "); i >= 0 {
		return src[i:]
	}
	return src
}

// A generated element must compute what the scripted one computes: same
// operator, same parameter handling, same routing behaviour. The comparison
// runs on a raw application and again after free-space propagation, since a
// phase error that is invisible at the element plane shows up after a metre.
func TestGeneratedMatchesScripted(t *testing.T) {
	rep := ReloadElementDefinitions([]string{"../elements"})
	if len(rep.Errors) != 0 {
		t.Fatalf("loading the example definitions failed: %+v", rep.Errors)
	}
	t.Cleanup(func() { ReloadElementDefinitions([]string{t.TempDir()}) })

	for _, params := range []map[string]any{
		{"f": 0.25, "sign": 1.0},
		{"f": 0.25, "sign": -1.0},
		{"f": 0.05, "sign": "1"},
	} {
		scripted, err := NewElement(ElementSpec{Type: "metalens", Params: params})
		if err != nil {
			t.Fatalf("scripted element: %v", err)
		}
		native, err := NewElement(ElementSpec{Type: "metalens_native", Params: params})
		if err != nil {
			t.Fatalf("generated element: %v", err)
		}
		const n = 64
		const width = 4e-3
		ctx := &Context{Wavelength: 633e-9}
		a := NewField(n, width/n, true)
		b := NewField(n, width/n, true)
		for i := range a.Ex {
			a.Ex[i] += 1
			a.Ey[i] += complex(0.25, -0.5)
			b.Ex[i] = a.Ex[i]
			b.Ey[i] = a.Ey[i]
		}
		if err := scripted.Apply(a, ctx); err != nil {
			t.Fatal(err)
		}
		if err := native.Apply(b, ctx); err != nil {
			t.Fatal(err)
		}
		compareFields(t, "at the element plane", params, a, b)
		if err := Propagate(a, 0.15, MethodASM, ctx); err != nil {
			t.Fatal(err)
		}
		if err := Propagate(b, 0.15, MethodASM, ctx); err != nil {
			t.Fatal(err)
		}
		compareFields(t, "after 0.15 m of propagation", params, a, b)
	}
}

func compareFields(t *testing.T, when string, params map[string]any, a, b *Field) {
	t.Helper()
	for i := range a.Ex {
		if math.Abs(real(a.Ex[i])-real(b.Ex[i])) > 1e-12 || math.Abs(imag(a.Ex[i])-imag(b.Ex[i])) > 1e-12 {
			t.Fatalf("%s (params %v): Ex[%d] scripted %v vs generated %v", when, params, i, a.Ex[i], b.Ex[i])
		}
		if math.Abs(real(a.Ey[i])-real(b.Ey[i])) > 1e-12 || math.Abs(imag(a.Ey[i])-imag(b.Ey[i])) > 1e-12 {
			t.Fatalf("%s (params %v): Ey[%d] scripted %v vs generated %v", when, params, i, a.Ey[i], b.Ey[i])
		}
	}
}

func TestGeneratedElementInCatalog(t *testing.T) {
	var doc *ElementDoc
	cat := BuildCatalog()
	for i := range cat.Elements {
		if cat.Elements[i].Type == "metalens_native" {
			doc = &cat.Elements[i]
		}
	}
	if doc == nil {
		t.Fatal("the generated element is missing from the catalog")
	}
	if doc.Custom {
		t.Fatal("a generated element is native, not custom")
	}
	if len(doc.Params) != 3 || doc.Params[0].Key != "f" || doc.Params[1].Key != "wl0" {
		t.Fatalf("generated doc params = %+v", doc.Params)
	}
	if cat.Classes["metalens_native"] != "lens" {
		t.Fatalf("generated element class = %q, want lens", cat.Classes["metalens_native"])
	}
	// The generated element is usable in a scene like any built-in.
	if b, err := componentBehavior("metalens_native"); err != nil || b != behaviorTransmit {
		t.Fatalf("componentBehavior = %d, %v", b, err)
	}
}

// A mirror definition generates a mirror: the routing behaviour travels with
// the generated registration.
func TestGenerateMirrorElement(t *testing.T) {
	def := ElementDefinition{
		Name: "asphere_mirror", Label: "非球面镜", Behavior: "mirror",
		Params: []ParamSpec{{Key: "R", Label: "曲率半径", Unit: "m", Kind: "float", Default: 0.2}},
		Phase:  "-k*r*r/R",
	}
	src, err := GenerateElementGo(def, "asphere_mirror")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "BehaviorMirror") {
		t.Fatalf("generated mirror does not declare its routing behaviour:\n%s", src)
	}
	if !strings.Contains(src, "math.Sqrt") == false {
		// radexpression has no sqrt; just make sure the message is sane
		_ = src
	}
	if _, err := GenerateElementGo(def, "Bad Name"); err == nil {
		t.Fatal("an invalid type name must be rejected")
	}
	if _, err := GenerateElementGo(ElementDefinition{Name: "x", Phase: "1 +"}, "x"); err == nil {
		t.Fatal("an invalid definition must be rejected")
	}
}

// if(...) is lazy in the interpreter; the generated code must keep it that way
// (hoisting the taken branch into a statement), or a guarded singular point
// turns into a NaN.
func TestGenerateIfStaysLazy(t *testing.T) {
	def := ElementDefinition{
		Name: "guard", Label: "护栏", Params: []ParamSpec{{Key: "a", Kind: "float", Default: 1e-3}},
		Phase: "if(r > a, -k*r*r/(2*0.3), 0)",
	}
	src, err := GenerateElementGo(def, "guard")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(src, "if rv > e.p_a {") {
		t.Fatalf("if(...) was not hoisted into a statement:\n%s", src)
	}
	if strings.Contains(src, "cexpI(if ") {
		t.Fatalf("if(...) leaked into an expression position:\n%s", src)
	}
}
