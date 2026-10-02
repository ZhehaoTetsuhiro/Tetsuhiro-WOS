package optics

import (
	"bytes"
	"fmt"
	"go/format"
	"os"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Scripted element → native Go element (graduation)
//
// `wos -gen-go elements/metalens.json` prints a native kernel element that
// computes the same operator as the scripted definition, with the expressions
// inlined as arithmetic. The generated file registers itself (element factory,
// catalog documentation, routing behaviour) and can be dropped into optics/ —
// useful when an element has proved itself and should stop being interpreted,
// or when it should ship as part of the kernel.
//
// Both paths funnel through the same runtime helpers (exprgen_rt.go) and the
// same parameter conversion, so a generated element and the scripted one agree
// numerically; optics/elemgen_test.go asserts that on a probe grid and through
// a full simulation.
// ---------------------------------------------------------------------------

// LoadElementDefinitionFile reads, parses and validates one definition file and
// returns the normalized definition (labels filled in, defaults checked).
func LoadElementDefinitionFile(path string) (ElementDefinition, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return ElementDefinition{}, err
	}
	cd, err := ParseElementDefinition(data, path)
	if err != nil {
		return ElementDefinition{}, err
	}
	def := cd.def
	def.Params = append([]ParamSpec(nil), cd.def.Params...)
	return def, nil
}

// GenerateElementGo returns the Go source of a native element implementing def.
// typeName is the element type the file registers; callers pass the definition
// name, or another one when that name is already taken by a built-in.
func GenerateElementGo(def ElementDefinition, typeName string) (string, error) {
	if !nameRe.MatchString(typeName) {
		return "", fmt.Errorf("type name %q must match %s", typeName, nameRe.String())
	}
	cd, err := compileDefinition(&def)
	if err != nil {
		return "", fmt.Errorf("%s: %v", sourceLabel(def.Source), err)
	}
	def = cd.def // normalized: labels and defaults filled in
	g := &goGen{def: def, typeName: typeName, paramField: map[string]string{}}
	for _, p := range def.Params {
		if p.Kind != "text" {
			g.paramField[p.Key] = "e.p_" + p.Key
		}
	}
	used := map[string]bool{}
	if strings.TrimSpace(def.Phase) != "" {
		if g.phase, err = parseExprNode(def.Phase); err != nil {
			return "", fmt.Errorf("phase: %v", err)
		}
		astNames(g.phase, used)
	}
	if strings.TrimSpace(def.Amp) != "" {
		if g.amp, err = parseExprNode(def.Amp); err != nil {
			return "", fmt.Errorf("amp: %v", err)
		}
		astNames(g.amp, used)
	}
	if j := def.Jones; j != nil {
		entries := [4]JonesEntry{j.A, j.B, j.C, j.D}
		defaults := [4][2]string{{"1", "0"}, {"0", "0"}, {"0", "0"}, {"1", "0"}}
		for i := range entries {
			re, im := strings.TrimSpace(entries[i].Re), strings.TrimSpace(entries[i].Im)
			if re == "" {
				re = defaults[i][0]
			}
			if im == "" {
				im = defaults[i][1]
			}
			if g.jones[i][0], err = parseExprNode(re); err != nil {
				return "", fmt.Errorf("jones entry %d real part: %v", i, err)
			}
			if g.jones[i][1], err = parseExprNode(im); err != nil {
				return "", fmt.Errorf("jones entry %d imaginary part: %v", i, err)
			}
			astNames(g.jones[i][0], used)
			astNames(g.jones[i][1], used)
		}
	}
	g.used = used
	return g.render()
}

// astNames lists the identifiers an AST references.
func astNames(n *exprNode, out map[string]bool) {
	if n == nil {
		return
	}
	if n.kind == exprVar {
		out[n.name] = true
	}
	for _, k := range n.args {
		astNames(k, out)
	}
}

type goGen struct {
	def        ElementDefinition
	typeName   string
	paramField map[string]string
	used       map[string]bool
	phase      *exprNode
	amp        *exprNode
	jones      [4][2]*exprNode // nil when the definition has no Jones matrix
	needsMath  bool
	needsFmt   bool
	hoisted    []string // if(...) conditionals turned into statements
	tmp        int
}

// goMathFuncs maps expression functions onto the standard library.
var goMathFuncs = map[string]string{
	"sin": "math.Sin", "cos": "math.Cos", "tan": "math.Tan",
	"asin": "math.Asin", "acos": "math.Acos", "atan": "math.Atan", "atan2": "math.Atan2",
	"sinh": "math.Sinh", "cosh": "math.Cosh", "tanh": "math.Tanh",
	"exp": "math.Exp", "log": "math.Log", "log10": "math.Log10", "sqrt": "math.Sqrt",
	"abs": "math.Abs", "floor": "math.Floor", "ceil": "math.Ceil", "round": "math.Round",
	"min": "math.Min", "max": "math.Max", "pow": "math.Pow", "hypot": "math.Hypot",
	"mod": "math.Mod", "erf": "math.Erf",
}

// goHelperFuncs maps the remaining functions onto the generated-element runtime
// helpers (exprgen_rt.go).
var goHelperFuncs = map[string]string{
	"sign": "genSign", "clamp": "genClamp", "step": "genStep", "rect": "genRect",
	"smoothstep": "genSmoothstep", "sinc": "genSinc", "rad": "genRad", "deg": "genDeg",
}

// goCamel turns a definition name (snake_case) into a Go identifier suffix.
func goCamel(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, "")
}

// render assembles the whole generated file and gofmt's it, which also proves
// that the output parses.
func (g *goGen) render() (string, error) {
	source := g.def.Source
	if source == "" {
		source = g.def.Name + ".json"
	}
	cmd := "wos -gen-go " + source
	if g.typeName != g.def.Name {
		cmd += " -gen-name " + g.typeName
	}
	var out bytes.Buffer
	fmt.Fprintf(&out, "// Code generated by `%s`; DO NOT EDIT.\n//\n", cmd)
	label := g.def.Label
	if label == "" {
		label = g.def.Name
	}
	fmt.Fprintf(&out, "// %s — %s, graduated from the scripted element %q.\n", g.typeName, label, g.def.Name)
	if strings.TrimSpace(g.def.Phase) != "" {
		fmt.Fprintf(&out, "// phase (%s): %s\n", phaseUnitLabel(g.def), g.def.Phase)
	}
	if strings.TrimSpace(g.def.Amp) != "" {
		fmt.Fprintf(&out, "// amplitude: %s\n", g.def.Amp)
	}
	out.WriteString("package optics\n\n")

	var body bytes.Buffer
	g.renderInit(&body, label)
	g.renderCtor(&body)
	if err := g.renderApply(&body); err != nil {
		return "", err
	}

	var imports []string
	if g.needsFmt {
		imports = append(imports, "fmt")
	}
	if g.needsMath {
		imports = append(imports, "math")
	}
	switch len(imports) {
	case 0:
	case 1:
		fmt.Fprintf(&out, "import %q\n\n", imports[0])
	default:
		out.WriteString("import (\n")
		for _, imp := range imports {
			fmt.Fprintf(&out, "\t%q\n", imp)
		}
		out.WriteString(")\n\n")
	}
	out.Write(body.Bytes())

	formatted, err := format.Source(out.Bytes())
	if err != nil {
		return "", fmt.Errorf("generated source does not parse: %v", err)
	}
	return string(formatted), nil
}

func phaseUnitLabel(def ElementDefinition) string {
	if def.PhaseUnit == "waves" {
		return "waves"
	}
	return "rad"
}

// paramSpecLiteral renders one ParamSpec as a Go composite literal, keeping
// only the fields the definition set.
func paramSpecLiteral(p ParamSpec) string {
	parts := []string{fmt.Sprintf("Key: %q", p.Key), fmt.Sprintf("Label: %q", p.Label)}
	if p.Unit != "" {
		parts = append(parts, fmt.Sprintf("Unit: %q", p.Unit))
	}
	kind := p.Kind
	if kind == "" {
		kind = "float"
	}
	parts = append(parts, fmt.Sprintf("Kind: %q", kind))
	if p.Min != 0 {
		parts = append(parts, "Min: "+goFloat(p.Min))
	}
	if p.Max != 0 {
		parts = append(parts, "Max: "+goFloat(p.Max))
	}
	if p.Step != 0 {
		parts = append(parts, "Step: "+goFloat(p.Step))
	}
	parts = append(parts, "Default: "+goValueLiteral(p.Default))
	if len(p.Choices) > 0 {
		var cs []string
		for _, c := range p.Choices {
			cs = append(cs, strconv.Quote(c))
		}
		parts = append(parts, "Choices: []string{"+strings.Join(cs, ", ")+"}")
	}
	if p.Help != "" {
		parts = append(parts, fmt.Sprintf("Help: %q", p.Help))
	}
	if p.ShowIf != "" {
		parts = append(parts, fmt.Sprintf("ShowIf: %q", p.ShowIf))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

func goFloat(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }

func goValueLiteral(v any) string {
	switch x := v.(type) {
	case nil:
		return "0"
	case float64:
		return goFloat(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case bool:
		if x {
			return "true"
		}
		return "false"
	case string:
		return strconv.Quote(x)
	}
	return "0"
}

// renderInit emits the init() that registers the element.
func (g *goGen) renderInit(body *bytes.Buffer, label string) {
	camel := goCamel(g.typeName)
	body.WriteString("func init() {\n")
	fmt.Fprintf(body, "\tRegisterGeneratedElement(%q, newGen%s, ElementDoc{\n", g.typeName, camel)
	fmt.Fprintf(body, "\t\tLabel: %q,\n", label)
	if g.def.Help != "" {
		fmt.Fprintf(body, "\t\tHelp:  %q,\n", g.def.Help)
	}
	if len(g.def.Params) > 0 {
		body.WriteString("\t\tParams: []ParamSpec{\n")
		for _, p := range g.def.Params {
			fmt.Fprintf(body, "\t\t\t%s,\n", paramSpecLiteral(p))
		}
		body.WriteString("\t\t},\n")
	}
	behavior := "BehaviorTransmit"
	if g.def.Behavior == "mirror" {
		behavior = "BehaviorMirror"
	}
	if g.def.Class != "" {
		fmt.Fprintf(body, "		Class: %q,\n", g.def.Class)
	}
	fmt.Fprintf(body, "	}, %s)\n}\n\n", behavior)
}

// renderCtor emits the element struct and its factory.
func (g *goGen) renderCtor(body *bytes.Buffer) {
	camel := goCamel(g.typeName)
	fmt.Fprintf(body, "// gen%sEl implements %q as native code.\n", camel, g.typeName)
	fmt.Fprintf(body, "type gen%sEl struct {\n", camel)
	for _, p := range g.def.Params {
		if p.Kind == "text" {
			continue
		}
		fmt.Fprintf(body, "\tp_%s float64\n", p.Key)
	}
	body.WriteString("}\n\n")

	fmt.Fprintf(body, "func newGen%s(p map[string]any) (Element, error) {\n", camel)
	fmt.Fprintf(body, "\tel := &gen%sEl{}\n", camel)
	for _, p := range g.def.Params {
		if p.Kind == "text" {
			continue
		}
		def, err := paramNumericValue(p.Default)
		if err != nil {
			// compileDefinition already validated this; keep the generator
			// honest rather than emitting a zero default.
			def = 0
		}
		fmt.Fprintf(body, "\tv_%s, err := genParamNum(p, %q, %s)\n", p.Key, p.Key, goFloat(def))
		body.WriteString("\tif err != nil {\n")
		fmt.Fprintf(body, "\t\treturn nil, fmt.Errorf(%q, err)\n", g.typeName+": %v")
		body.WriteString("\t}\n")
		fmt.Fprintf(body, "\tel.p_%s = v_%s\n", p.Key, p.Key)
		g.needsFmt = true
	}
	body.WriteString("\treturn el, nil\n}\n\n")
}

// renderApply emits the Apply method: a loop over the grid with the operator's
// expressions inlined.
func (g *goGen) renderApply(body *bytes.Buffer) error {
	needK, needWL := g.used["k"], g.used["wl"]
	needR, needTh := g.used["r"], g.used["th"]
	needXY := g.used["x"] || g.used["y"] || needR || needTh
	camel := goCamel(g.typeName)

	// Render every expression first: the loop body must emit the hoisted
	// conditional statements before the lines that use them.
	g.hoisted = nil
	ph, err := g.renderNode(g.phase)
	if err != nil {
		return err
	}
	amp, err := g.renderNode(g.amp)
	if err != nil {
		return err
	}
	var jonesExpr [4][2]string
	for i := range g.jones {
		for k := range g.jones[i] {
			if g.jones[i][k] == nil {
				continue
			}
			s, err := g.renderNode(g.jones[i][k])
			if err != nil {
				return err
			}
			jonesExpr[i][k] = s
		}
	}
	tExpr := ""
	switch {
	case ph != "" && amp != "":
		tExpr = "cexpI(" + ph + ") * (" + amp + ")"
	case ph != "":
		tExpr = "cexpI(" + ph + ")"
	case amp != "":
		tExpr = "complex(" + amp + ", 0)"
	}
	// Fold a constant amplitude/phase: * (1) and cexpI(0) are noise in
	// generated code, and a literal amplitude reads better as a number.
	if v, ok := constExprValue(g.amp); ok && v == 1 {
		tExpr = ""
		if ph != "" {
			tExpr = "cexpI(" + ph + ")"
		}
	}
	if v, ok := constExprValue(g.phase); ok && v == 0 {
		switch {
		case amp != "":
			tExpr = "complex(" + amp + ", 0)"
		default:
			tExpr = ""
		}
	}
	if v, ok := constExprValue(g.amp); ok && v != 1 && ph == "" {
		tExpr = "complex(" + goFloat(v) + ", 0)"
	}

	fmt.Fprintf(body, "func (e *gen%sEl) Apply(f *Field, ctx *Context) error {\n", camel)
	if needK {
		body.WriteString("\tk := 2 * math.Pi / ctx.Wavelength\n")
		g.needsMath = true
	}
	if needWL {
		body.WriteString("\twl := ctx.Wavelength\n")
	}
	if g.jones[0][0] != nil {
		body.WriteString("\t// A Jones matrix turns a scalar field into a vectorial one.\n")
		body.WriteString("\tif !f.Polarized {\n\t\tf.Polarized = true\n\t\tif f.Ey == nil {\n\t\t\tf.Ey = make([]complex128, len(f.Ex))\n\t\t}\n\t}\n")
	}
	body.WriteString("\tn := f.N\n\tfor j := 0; j < n; j++ {\n")
	if needXY {
		body.WriteString("\t\tyv := f.Y(j)\n")
	}
	body.WriteString("\t\tfor i := 0; i < n; i++ {\n")
	if needXY {
		body.WriteString("\t\t\txv := f.X(i)\n")
	}
	if needR {
		body.WriteString("\t\t\trv := math.Hypot(xv, yv)\n")
		g.needsMath = true
	}
	if needTh {
		body.WriteString("\t\t\tthv := math.Atan2(yv, xv)\n")
		g.needsMath = true
	}
	body.WriteString("\t\t\tidx := j*n + i\n")
	for _, stmt := range g.hoisted {
		body.WriteString(stmt)
	}
	if tExpr != "" && g.jones[0][0] == nil {
		body.WriteString("\t\t\tt := " + tExpr + "\n")
		body.WriteString("\t\t\tf.Ex[idx] *= t\n")
		body.WriteString("\t\t\tif f.Polarized && f.Ey != nil {\n\t\t\t\tf.Ey[idx] *= t\n\t\t\t}\n")
	}
	if g.jones[0][0] != nil {
		names := [4]string{"a", "b", "c", "d"}
		for i := 0; i < 4; i++ {
			if jonesExpr[i][0] == "" {
				continue
			}
			body.WriteString("\t\t\t" + names[i] + " := complex(" + jonesExpr[i][0] + ", " + jonesExpr[i][1] + ")\n")
		}
		body.WriteString("\t\t\tex, ey := f.Ex[idx], f.Ey[idx]\n")
		scale := ""
		if tExpr != "" {
			scale = " * (" + tExpr + ")"
		}
		fmt.Fprintf(body, "\t\t\tf.Ex[idx] = (a*ex + b*ey)%s\n", scale)
		fmt.Fprintf(body, "\t\t\tf.Ey[idx] = (c*ex + d*ey)%s\n", scale)
	}
	body.WriteString("\t\t}\n\t}\n\treturn nil\n}\n")
	return nil
}

// constExprValue returns the value of an expression that is a numeric literal.
func constExprValue(n *exprNode) (float64, bool) {
	if n != nil && n.kind == exprNum {
		return n.num, true
	}
	return 0, false
}

// renderCond renders an expression in a Go *boolean* position (the condition of
// a hoisted if): comparisons and logic stay boolean instead of being squashed
// through genB2F, and any other value is tested against zero.
func (g *goGen) renderCond(n *exprNode) (string, error) {
	if n == nil {
		return "false", nil
	}
	switch {
	case n.kind == exprBin && (n.op == "&&" || n.op == "||"):
		a, err := g.renderCond(n.args[0])
		if err != nil {
			return "", err
		}
		b, err := g.renderCond(n.args[1])
		if err != nil {
			return "", err
		}
		return "(" + a + " " + n.op + " " + b + ")", nil
	case n.kind == exprUn && n.op == "!":
		a, err := g.renderCond(n.args[0])
		if err != nil {
			return "", err
		}
		return "!(" + a + ")", nil
	case n.kind == exprBin && (n.op == ">" || n.op == "<" || n.op == ">=" || n.op == "<=" || n.op == "==" || n.op == "!="):
		a, err := g.renderNode(n.args[0])
		if err != nil {
			return "", err
		}
		b, err := g.renderNode(n.args[1])
		if err != nil {
			return "", err
		}
		return a + " " + n.op + " " + b, nil
	}
	a, err := g.renderNode(n)
	if err != nil {
		return "", err
	}
	return a + " != 0", nil
}

// renderNode renders one expression node as Go source, appending any hoisted
// if(...) statements to g.hoisted.
func (g *goGen) renderNode(n *exprNode) (string, error) {
	if n == nil {
		return "", nil
	}
	switch n.kind {
	case exprNum:
		return goFloat(n.num), nil
	case exprVar:
		switch n.name {
		case "x", "y", "r", "th":
			return map[string]string{"x": "xv", "y": "yv", "r": "rv", "th": "thv"}[n.name], nil
		case "wl":
			return "wl", nil
		case "k":
			return "k", nil
		case "pi":
			g.needsMath = true
			return "math.Pi", nil
		case "e":
			g.needsMath = true
			return "math.E", nil
		}
		if sel, ok := g.paramField[n.name]; ok {
			return sel, nil
		}
		return "", &ExprError{Pos: n.pos, Msg: fmt.Sprintf("unknown variable %q", n.name), Name: n.name}
	case exprUn:
		a, err := g.renderNode(n.args[0])
		if err != nil {
			return "", err
		}
		switch n.op {
		case "-":
			return "-(" + a + ")", nil
		case "!":
			return "genB2F(" + a + " == 0)", nil
		}
		return "", fmt.Errorf("unsupported unary operator %q", n.op)
	case exprBin:
		a, err := g.renderNode(n.args[0])
		if err != nil {
			return "", err
		}
		b, err := g.renderNode(n.args[1])
		if err != nil {
			return "", err
		}
		switch n.op {
		case "+", "-", "*", "/":
			return "(" + a + " " + n.op + " " + b + ")", nil
		case "%":
			g.needsMath = true
			return "math.Mod(" + a + ", " + b + ")", nil
		case "^":
			g.needsMath = true
			return "math.Pow(" + a + ", " + b + ")", nil
		case ">", "<", ">=", "<=", "==", "!=":
			return "genB2F(" + a + " " + n.op + " " + b + ")", nil
		case "&&":
			return "genB2F(" + a + " != 0 && " + b + " != 0)", nil
		case "||":
			return "genB2F(" + a + " != 0 || " + b + " != 0)", nil
		}
		return "", fmt.Errorf("unsupported operator %q", n.op)
	case exprCall:
		spec, ok := exprFuncs[n.name]
		if !ok {
			return "", fmt.Errorf("unknown function %q", n.name)
		}
		if len(n.args) != spec.arity {
			return "", fmt.Errorf("%s takes %d argument(s)", n.name, spec.arity)
		}
		if n.name == "if" {
			// if(...) must keep its laziness: hoist it into a statement so the
			// untaken branch is never evaluated (its whole purpose is guarding
			// a singular point).
			c, err := g.renderCond(n.args[0])
			if err != nil {
				return "", err
			}
			t, err := g.renderNode(n.args[1])
			if err != nil {
				return "", err
			}
			f, err := g.renderNode(n.args[2])
			if err != nil {
				return "", err
			}
			g.tmp++
			name := fmt.Sprintf("c%d", g.tmp)
			g.hoisted = append(g.hoisted, fmt.Sprintf("			var %s float64\n			if %s {\n				%s = %s\n			} else {\n				%s = %s\n			}\n",
				name, c, name, t, name, f))
			return name, nil
		}
		args := make([]string, 0, len(n.args))
		for _, a := range n.args {
			s, err := g.renderNode(a)
			if err != nil {
				return "", err
			}
			args = append(args, s)
		}
		if fn, ok := goMathFuncs[n.name]; ok {
			g.needsMath = true
			return fn + "(" + strings.Join(args, ", ") + ")", nil
		}
		if fn, ok := goHelperFuncs[n.name]; ok {
			return fn + "(" + strings.Join(args, ", ") + ")", nil
		}
		return "", fmt.Errorf("no Go mapping for function %q", n.name)
	}
	return "", fmt.Errorf("bad expression node")
}
