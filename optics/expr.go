package optics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------------------
// Expression language for scripted elements (元件定义文件)
//
// A scripted element (see customelem.go) describes its thin-element operator as
// one or more arithmetic expressions over the element's local coordinates and
// its own parameters. This file implements that language: a small recursive
// descent parser plus a closure compiler.
//
// Grammar (precedence low to high):
//
//	expr    := or
//	or      := and ('||' and)*
//	and     := cmp ('&&' cmp)*
//	cmp     := add (('>'|'<'|'>='|'<='|'=='|'!=') add)*
//	add     := mul (('+'|'-') mul)*
//	mul     := unary (('*'|'/'|'%') unary)*
//	unary   := ('-'|'+'|'!') unary | power
//	power   := primary ('^' unary)?          // right-associative
//	primary := number | ident | ident '(' args ')' | '(' expr ')'
//
// Comparisons and logical operators yield 1 or 0, so they compose with the
// arithmetic side (if(a > b, x, y)) without a separate boolean type.
//
// Built-in names: x, y (element-local coordinates, m), r (=hypot(x,y)), th
// (=atan2(y,x), rad), wl (wavelength, m), k (=2*pi/wl), pi, e. Any other
// identifier must be one of the element definition's parameters, bound as a
// constant at compile time — an unknown name is an error at load time, not a
// silent zero.
//
// The language has no loops, no arrays and no I/O: an expression cannot hang
// and cannot read anything but its own inputs. Division by zero produces
// +/-Inf or NaN as IEEE 754 prescribes; the loader probes each compiled
// operator on a sample grid and reports non-finite results instead of letting
// them reach a simulation silently.
// ---------------------------------------------------------------------------

// ExprError is a compile-time failure with the source column it was found at.
type ExprError struct {
	Pos int // 1-based column in the expression source
	Msg string
	// Name is the offending identifier when the error is about one, so a
	// caller can explain it in context (a parameter of a non-numeric kind,
	// say).
	Name string
}

func (e *ExprError) Error() string {
	return fmt.Sprintf("column %d: %s", e.Pos, e.Msg)
}

// exprKind classifies an AST node.
type exprKind int

const (
	exprNum  exprKind = iota // numeric literal
	exprVar                  // built-in variable or bound parameter
	exprUn                   // unary operator (op, args[0])
	exprBin                  // binary operator (op, args[0], args[1])
	exprCall                 // function call (name, args...)
)

// exprNode is one AST node. The AST is kept after compilation because the Go
// code generator (exprgen.go) prints it back out as native source.
type exprNode struct {
	kind exprKind
	num  float64
	name string // variable or function name
	op   string // operator spelling for exprUn/exprBin
	args []*exprNode
	pos  int
}

// exprEnv is the per-pixel evaluation environment. r and th are filled by the
// caller only when the expression references them.
type exprEnv struct {
	x, y, r, th, wl, k float64
}

type exprFunc func(*exprEnv) float64

// CompiledExpr is a parsed and compiled expression, ready to evaluate.
type CompiledExpr struct {
	src    string
	node   *exprNode
	root   exprFunc
	needsR bool
	needsT bool
	names  []string // referenced variables/parameters, sorted (documentation)
}

// Source returns the expression text it was compiled from.
func (c *CompiledExpr) Source() string { return c.src }

// NeedsRadius reports whether the expression reads r.
func (c *CompiledExpr) NeedsRadius() bool { return c.needsR }

// NeedsTheta reports whether the expression reads th.
func (c *CompiledExpr) NeedsTheta() bool { return c.needsT }

// Names lists the variables and parameters the expression references.
func (c *CompiledExpr) Names() []string { return append([]string(nil), c.names...) }

// Eval evaluates the expression at one point. r and th are derived on demand.
func (c *CompiledExpr) Eval(x, y, wl float64) float64 {
	e := &exprEnv{x: x, y: y, wl: wl, k: 2 * math.Pi / wl}
	if c.needsR || c.needsT {
		e.r = math.Hypot(x, y)
		if c.needsT {
			e.th = math.Atan2(y, x)
		}
	}
	return c.root(e)
}

// EvalEnv evaluates against a caller-managed environment (hot loops reuse one
// env struct instead of allocating per point).
func (c *CompiledExpr) EvalEnv(e *exprEnv) float64 { return c.root(e) }

// ParseExpr parses an expression without binding any parameter names.
func ParseExpr(src string) (*exprNode, error) { return parseExprNode(src) }

// CompileExpr parses src and compiles it, binding every non-built-in
// identifier to its numeric value in params.
func CompileExpr(src string, params map[string]float64) (*CompiledExpr, error) {
	node, err := parseExprNode(src)
	if err != nil {
		return nil, err
	}
	vars := map[string]exprFunc{}
	for k, v := range params {
		val := v
		vars[k] = func(*exprEnv) float64 { return val }
	}
	var names []string
	root, err := compileNode(node, vars, &names)
	if err != nil {
		return nil, err
	}
	c := &CompiledExpr{src: src, node: node, root: root}
	for _, n := range names {
		switch n {
		case "r":
			c.needsR = true
		case "th":
			c.needsT = true
		}
	}
	sortStrings(names)
	names = uniqueStrings(names)
	c.names = names
	return c, nil
}

// sortStrings sorts in place (insertion sort: the lists are tiny and this
// avoids pulling in "sort" for a handful of names).
func sortStrings(a []string) {
	for i := 1; i < len(a); i++ {
		for j := i; j > 0 && a[j] < a[j-1]; j-- {
			a[j], a[j-1] = a[j-1], a[j]
		}
	}
}

func uniqueStrings(in []string) []string {
	out := in[:0]
	for i, s := range in {
		if i == 0 || s != in[i-1] {
			out = append(out, s)
		}
	}
	return out
}

// ---- lexer -----------------------------------------------------------------

type exprTok struct {
	kind byte // 'n' number, 'i' ident, 'o' operator, 'e' end
	text string
	num  float64
	pos  int
}

func lexExpr(src string) ([]exprTok, error) {
	var toks []exprTok
	i := 0
	for i < len(src) {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case c >= '0' && c <= '9' || c == '.':
			j := i
			dot := false
			for j < len(src) && (src[j] >= '0' && src[j] <= '9' || src[j] == '.') {
				if src[j] == '.' {
					if dot {
						return nil, &ExprError{Pos: j + 1, Msg: "malformed number"}
					}
					dot = true
				}
				j++
			}
			// exponent
			if j < len(src) && (src[j] == 'e' || src[j] == 'E') {
				k := j + 1
				if k < len(src) && (src[k] == '+' || src[k] == '-') {
					k++
				}
				digits := k
				for k < len(src) && src[k] >= '0' && src[k] <= '9' {
					k++
				}
				if k == digits {
					return nil, &ExprError{Pos: j + 1, Msg: "malformed exponent"}
				}
				j = k
			}
			text := src[i:j]
			v, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return nil, &ExprError{Pos: i + 1, Msg: "malformed number " + text}
			}
			toks = append(toks, exprTok{kind: 'n', text: text, num: v, pos: i + 1})
			i = j
		case isIdentStart(c):
			j := i
			for j < len(src) && isIdentPart(src[j]) {
				j++
			}
			toks = append(toks, exprTok{kind: 'i', text: src[i:j], pos: i + 1})
			i = j
		default:
			// two-character operators first
			two := ""
			if i+1 < len(src) {
				two = src[i : i+2]
			}
			switch two {
			case "<=", ">=", "==", "!=", "&&", "||":
				toks = append(toks, exprTok{kind: 'o', text: two, pos: i + 1})
				i += 2
				continue
			}
			switch c {
			case '+', '-', '*', '/', '%', '^', '(', ')', ',', '!', '<', '>':
				toks = append(toks, exprTok{kind: 'o', text: string(c), pos: i + 1})
				i++
			default:
				return nil, &ExprError{Pos: i + 1, Msg: fmt.Sprintf("unexpected character %q", string(c))}
			}
		}
	}
	toks = append(toks, exprTok{kind: 'e', pos: len(src) + 1})
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// ---- parser ----------------------------------------------------------------

type exprParser struct {
	toks []exprTok
	i    int
}

func parseExprNode(src string) (*exprNode, error) {
	if strings.TrimSpace(src) == "" {
		return nil, &ExprError{Pos: 1, Msg: "empty expression"}
	}
	toks, err := lexExpr(src)
	if err != nil {
		return nil, err
	}
	p := &exprParser{toks: toks}
	n, err := p.parseOr()
	if err != nil {
		return nil, err
	}
	if t := p.peek(); t.kind != 'e' {
		return nil, &ExprError{Pos: t.pos, Msg: fmt.Sprintf("unexpected %q", t.text)}
	}
	return n, nil
}

func (p *exprParser) peek() exprTok { return p.toks[p.i] }
func (p *exprParser) next() exprTok { t := p.toks[p.i]; p.i++; return t }
func (p *exprParser) atOp(s string) bool {
	t := p.peek()
	return t.kind == 'o' && t.text == s
}

func (p *exprParser) parseOr() (*exprNode, error)  { return p.parseBinLevel(p.parseAnd, "||") }
func (p *exprParser) parseAnd() (*exprNode, error) { return p.parseBinLevel(p.parseCmp, "&&") }
func (p *exprParser) parseCmp() (*exprNode, error) {
	return p.parseBinLevel(p.parseAdd, ">", "<", ">=", "<=", "==", "!=")
}
func (p *exprParser) parseAdd() (*exprNode, error) { return p.parseBinLevel(p.parseMul, "+", "-") }
func (p *exprParser) parseMul() (*exprNode, error) {
	return p.parseBinLevel(p.parseUnary, "*", "/", "%")
}

// parseBinLevel parses one left-associative precedence level.
func (p *exprParser) parseBinLevel(sub func() (*exprNode, error), ops ...string) (*exprNode, error) {
	left, err := sub()
	if err != nil {
		return nil, err
	}
	for {
		t := p.peek()
		if t.kind != 'o' {
			return left, nil
		}
		matched := ""
		for _, op := range ops {
			if t.text == op {
				matched = op
				break
			}
		}
		if matched == "" {
			return left, nil
		}
		p.next()
		right, err := sub()
		if err != nil {
			return nil, err
		}
		left = &exprNode{kind: exprBin, op: matched, args: []*exprNode{left, right}, pos: t.pos}
	}
}

func (p *exprParser) parseUnary() (*exprNode, error) {
	t := p.peek()
	if t.kind == 'o' && (t.text == "-" || t.text == "+" || t.text == "!") {
		p.next()
		arg, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		if t.text == "+" {
			return arg, nil
		}
		return &exprNode{kind: exprUn, op: t.text, args: []*exprNode{arg}, pos: t.pos}, nil
	}
	return p.parsePower()
}

func (p *exprParser) parsePower() (*exprNode, error) {
	base, err := p.parsePrimary()
	if err != nil {
		return nil, err
	}
	t := p.peek()
	if t.kind == 'o' && t.text == "^" {
		p.next()
		// right-associative, and the exponent may carry its own sign
		exp, err := p.parseUnary()
		if err != nil {
			return nil, err
		}
		return &exprNode{kind: exprBin, op: "^", args: []*exprNode{base, exp}, pos: t.pos}, nil
	}
	return base, nil
}

func (p *exprParser) parsePrimary() (*exprNode, error) {
	t := p.next()
	switch {
	case t.kind == 'n':
		return &exprNode{kind: exprNum, num: t.num, pos: t.pos}, nil
	case t.kind == 'i':
		if p.atOp("(") {
			p.next()
			var args []*exprNode
			if !p.atOp(")") {
				for {
					a, err := p.parseOr()
					if err != nil {
						return nil, err
					}
					args = append(args, a)
					if p.atOp(",") {
						p.next()
						continue
					}
					break
				}
			}
			if !p.atOp(")") {
				return nil, &ExprError{Pos: p.peek().pos, Msg: fmt.Sprintf("expected ')' in call to %s", t.text)}
			}
			p.next()
			return &exprNode{kind: exprCall, name: t.text, args: args, pos: t.pos}, nil
		}
		return &exprNode{kind: exprVar, name: t.text, pos: t.pos}, nil
	case t.kind == 'o' && t.text == "(":
		e, err := p.parseOr()
		if err != nil {
			return nil, err
		}
		if !p.atOp(")") {
			return nil, &ExprError{Pos: p.peek().pos, Msg: "expected ')'"}
		}
		p.next()
		return e, nil
	case t.kind == 'e':
		return nil, &ExprError{Pos: t.pos, Msg: "unexpected end of expression"}
	}
	return nil, &ExprError{Pos: t.pos, Msg: fmt.Sprintf("unexpected %q", t.text)}
}

// ---- compiler --------------------------------------------------------------

// exprBuiltins are the variables every expression may use.
var exprBuiltins = map[string]exprFunc{
	"x":  func(e *exprEnv) float64 { return e.x },
	"y":  func(e *exprEnv) float64 { return e.y },
	"r":  func(e *exprEnv) float64 { return e.r },
	"th": func(e *exprEnv) float64 { return e.th },
	"wl": func(e *exprEnv) float64 { return e.wl },
	"k":  func(e *exprEnv) float64 { return e.k },
	"pi": func(*exprEnv) float64 { return math.Pi },
	"e":  func(*exprEnv) float64 { return math.E },
}

// exprFuncs are the callable functions, with their arity.
var exprFuncs = map[string]struct {
	arity int
	call  func(a []float64) float64
}{
	"sin":   {1, func(a []float64) float64 { return math.Sin(a[0]) }},
	"cos":   {1, func(a []float64) float64 { return math.Cos(a[0]) }},
	"tan":   {1, func(a []float64) float64 { return math.Tan(a[0]) }},
	"asin":  {1, func(a []float64) float64 { return math.Asin(a[0]) }},
	"acos":  {1, func(a []float64) float64 { return math.Acos(a[0]) }},
	"atan":  {1, func(a []float64) float64 { return math.Atan(a[0]) }},
	"atan2": {2, func(a []float64) float64 { return math.Atan2(a[0], a[1]) }},
	"sinh":  {1, func(a []float64) float64 { return math.Sinh(a[0]) }},
	"cosh":  {1, func(a []float64) float64 { return math.Cosh(a[0]) }},
	"tanh":  {1, func(a []float64) float64 { return math.Tanh(a[0]) }},
	"exp":   {1, func(a []float64) float64 { return math.Exp(a[0]) }},
	"log":   {1, func(a []float64) float64 { return math.Log(a[0]) }},
	"log10": {1, func(a []float64) float64 { return math.Log10(a[0]) }},
	"sqrt":  {1, func(a []float64) float64 { return math.Sqrt(a[0]) }},
	"abs":   {1, func(a []float64) float64 { return math.Abs(a[0]) }},
	"sign":  {1, func(a []float64) float64 { return signf(a[0]) }},
	"floor": {1, func(a []float64) float64 { return math.Floor(a[0]) }},
	"ceil":  {1, func(a []float64) float64 { return math.Ceil(a[0]) }},
	"round": {1, func(a []float64) float64 { return math.Round(a[0]) }},
	"min":   {2, func(a []float64) float64 { return math.Min(a[0], a[1]) }},
	"max":   {2, func(a []float64) float64 { return math.Max(a[0], a[1]) }},
	"pow":   {2, func(a []float64) float64 { return math.Pow(a[0], a[1]) }},
	"hypot": {2, func(a []float64) float64 { return math.Hypot(a[0], a[1]) }},
	"mod":   {2, func(a []float64) float64 { return math.Mod(a[0], a[1]) }},
	"clamp": {3, func(a []float64) float64 { return math.Min(math.Max(a[0], a[1]), a[2]) }},
	"if": {3, func(a []float64) float64 {
		if a[0] != 0 {
			return a[1]
		}
		return a[2]
	}},
	"step": {1, func(a []float64) float64 {
		if a[0] >= 0 {
			return 1
		}
		return 0
	}},
	"rect": {1, func(a []float64) float64 {
		if math.Abs(a[0]) <= 0.5 {
			return 1
		}
		return 0
	}},
	"smoothstep": {3, func(a []float64) float64 { return smoothstepd(a[0], a[1], a[2]) }},
	"erf":        {1, func(a []float64) float64 { return math.Erf(a[0]) }},
	// sinc here is the diffraction convention sin(pi*x)/(pi*x), with sinc(0)=1:
	// grating and aperture profiles are almost always written this way.
	"sinc": {1, func(a []float64) float64 { return sincPi(a[0]) }},
	"rad":  {1, func(a []float64) float64 { return a[0] * math.Pi / 180 }},
	"deg":  {1, func(a []float64) float64 { return a[0] * 180 / math.Pi }},
}

func signf(v float64) float64 {
	switch {
	case v > 0:
		return 1
	case v < 0:
		return -1
	}
	return 0
}

func smoothstepd(edge0, edge1, x float64) float64 {
	if edge1 == edge0 {
		if x < edge0 {
			return 0
		}
		return 1
	}
	t := (x - edge0) / (edge1 - edge0)
	t = math.Min(math.Max(t, 0), 1)
	return t * t * (3 - 2*t)
}

func sincPi(x float64) float64 {
	if x == 0 {
		return 1
	}
	p := math.Pi * x
	return math.Sin(p) / p
}

// compileNode turns an AST into a closure, checking names as it goes.
func compileNode(n *exprNode, vars map[string]exprFunc, names *[]string) (exprFunc, error) {
	switch n.kind {
	case exprNum:
		v := n.num
		return func(*exprEnv) float64 { return v }, nil
	case exprVar:
		if f, ok := vars[n.name]; ok {
			*names = append(*names, n.name)
			return f, nil
		}
		if f, ok := exprBuiltins[n.name]; ok {
			*names = append(*names, n.name)
			return f, nil
		}
		return nil, &ExprError{Pos: n.pos, Msg: fmt.Sprintf("unknown variable %q (not a parameter of this element)", n.name), Name: n.name}
	case exprUn:
		a, err := compileNode(n.args[0], vars, names)
		if err != nil {
			return nil, err
		}
		switch n.op {
		case "-":
			return func(e *exprEnv) float64 { return -a(e) }, nil
		case "!":
			return func(e *exprEnv) float64 {
				if a(e) == 0 {
					return 1
				}
				return 0
			}, nil
		}
		return nil, &ExprError{Pos: n.pos, Msg: "unsupported unary operator " + n.op}
	case exprBin:
		a, err := compileNode(n.args[0], vars, names)
		if err != nil {
			return nil, err
		}
		b, err := compileNode(n.args[1], vars, names)
		if err != nil {
			return nil, err
		}
		switch n.op {
		case "+":
			return func(e *exprEnv) float64 { return a(e) + b(e) }, nil
		case "-":
			return func(e *exprEnv) float64 { return a(e) - b(e) }, nil
		case "*":
			return func(e *exprEnv) float64 { return a(e) * b(e) }, nil
		case "/":
			return func(e *exprEnv) float64 { return a(e) / b(e) }, nil
		case "%":
			return func(e *exprEnv) float64 { return math.Mod(a(e), b(e)) }, nil
		case "^":
			return func(e *exprEnv) float64 { return math.Pow(a(e), b(e)) }, nil
		case ">":
			return func(e *exprEnv) float64 { return boolf(a(e) > b(e)) }, nil
		case "<":
			return func(e *exprEnv) float64 { return boolf(a(e) < b(e)) }, nil
		case ">=":
			return func(e *exprEnv) float64 { return boolf(a(e) >= b(e)) }, nil
		case "<=":
			return func(e *exprEnv) float64 { return boolf(a(e) <= b(e)) }, nil
		case "==":
			return func(e *exprEnv) float64 { return boolf(a(e) == b(e)) }, nil
		case "!=":
			return func(e *exprEnv) float64 { return boolf(a(e) != b(e)) }, nil
		case "&&":
			return func(e *exprEnv) float64 { return boolf(a(e) != 0 && b(e) != 0) }, nil
		case "||":
			return func(e *exprEnv) float64 { return boolf(a(e) != 0 || b(e) != 0) }, nil
		}
		return nil, &ExprError{Pos: n.pos, Msg: "unsupported operator " + n.op}
	case exprCall:
		spec, ok := exprFuncs[n.name]
		if !ok {
			return nil, &ExprError{Pos: n.pos, Msg: fmt.Sprintf("unknown function %q", n.name)}
		}
		if len(n.args) != spec.arity {
			return nil, &ExprError{Pos: n.pos, Msg: fmt.Sprintf("%s takes %d argument(s), got %d", n.name, spec.arity, len(n.args))}
		}
		if n.name == "if" {
			// Lazy: only the taken branch is evaluated, so if() can guard a
			// division or a sqrt away from its singular point.
			c, err := compileNode(n.args[0], vars, names)
			if err != nil {
				return nil, err
			}
			t, err := compileNode(n.args[1], vars, names)
			if err != nil {
				return nil, err
			}
			f, err := compileNode(n.args[2], vars, names)
			if err != nil {
				return nil, err
			}
			return func(e *exprEnv) float64 {
				if c(e) != 0 {
					return t(e)
				}
				return f(e)
			}, nil
		}
		args := make([]exprFunc, len(n.args))
		for i, a := range n.args {
			af, err := compileNode(a, vars, names)
			if err != nil {
				return nil, err
			}
			args[i] = af
		}
		call := spec.call
		return func(e *exprEnv) float64 {
			var buf [3]float64
			for i := range args {
				buf[i] = args[i](e)
			}
			return call(buf[:len(args)])
		}, nil
	}
	return nil, &ExprError{Pos: n.pos, Msg: "internal: bad expression node"}
}

func boolf(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
