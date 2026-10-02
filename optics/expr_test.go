package optics

import (
	"math"
	"strings"
	"testing"
)

func evalStr(t *testing.T, src string, params map[string]float64, x, y, wl float64) float64 {
	t.Helper()
	c, err := CompileExpr(src, params)
	if err != nil {
		t.Fatalf("CompileExpr(%q): %v", src, err)
	}
	return c.Eval(x, y, wl)
}

func TestExprArithmetic(t *testing.T) {
	wl := 632.8e-9
	cases := []struct {
		src  string
		want float64
	}{
		{"1+2*3", 7},
		{"(1+2)*3", 9},
		{"2^3^2", 512}, // right-associative
		{"-2^2", -4},   // power binds tighter than unary minus
		{"2^-1", 0.5},  // signed exponent
		{"10 % 3", 1},  // modulo
		{"7 / 2", 3.5},
		{"1 - 2 - 3", -4},
		{"+3", 3},
		{"1.5e-3 * 2", 3e-3},
		{".5 + .25", 0.75},
	}
	for _, c := range cases {
		if got := evalStr(t, c.src, nil, 0, 0, wl); math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s = %g, want %g", c.src, got, c.want)
		}
	}
}

func TestExprComparisonsAndLogic(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"2 > 1", 1},
		{"2 < 1", 0},
		{"2 >= 2", 1},
		{"2 <= 1", 0},
		{"3 == 3", 1},
		{"3 != 3", 0},
		{"1 > 0 && 2 > 1", 1},
		{"1 > 0 && 0 > 1", 0},
		{"1 > 0 || 0 > 1", 1},
		{"!(1 > 2)", 1},
		{"1 + (2 > 1)", 2}, // comparisons are arithmetic
	}
	for _, c := range cases {
		if got := evalStr(t, c.src, nil, 0, 0, 632.8e-9); got != c.want {
			t.Errorf("%s = %g, want %g", c.src, got, c.want)
		}
	}
}

func TestExprFunctions(t *testing.T) {
	cases := []struct {
		src  string
		want float64
	}{
		{"sin(pi/2)", 1},
		{"cos(0)", 1},
		{"atan2(1, 1)", math.Pi / 4},
		{"sqrt(16)", 4},
		{"abs(-3)", 3},
		{"sign(-2)", -1},
		{"sign(0)", 0},
		{"min(3, 5)", 3},
		{"max(3, 5)", 5},
		{"pow(2, 10)", 1024},
		{"hypot(3, 4)", 5},
		{"mod(7, 3)", 1},
		{"clamp(5, 0, 1)", 1},
		{"clamp(-5, 0, 1)", 0},
		{"step(-0.0)", 1},
		{"step(-0.1)", 0},
		{"rect(0.4)", 1},
		{"rect(0.6)", 0},
		{"smoothstep(0, 1, 0.5)", 0.5},
		{"smoothstep(0, 1, -3)", 0},
		{"erf(0)", 0},
		{"sinc(0)", 1},
		{"sinc(1)", 0},
		{"sinc(0.5)", 2 / math.Pi},
		{"rad(180)", math.Pi},
		{"deg(pi)", 180},
		{"floor(1.7)", 1},
		{"ceil(1.2)", 2},
		{"round(1.5)", 2},
		{"log(e)", 1},
		{"log10(1000)", 3},
		{"exp(0)", 1},
	}
	for _, c := range cases {
		got := evalStr(t, c.src, nil, 0, 0, 632.8e-9)
		if math.Abs(got-c.want) > 1e-12 {
			t.Errorf("%s = %g, want %g", c.src, got, c.want)
		}
	}
}

// if() must not evaluate the branch it did not take: the whole point of the
// guard is avoiding the singular point of the untaken branch.
func TestExprIfIsLazy(t *testing.T) {
	got := evalStr(t, "if(x > 0, 1/x, 0)", nil, 0, 0, 632.8e-9)
	if got != 0 {
		t.Fatalf("if at x=0 = %g, want 0 (untaken branch must not be evaluated)", got)
	}
	if got := evalStr(t, "if(x > 0, 1/x, 0)", nil, 2, 0, 632.8e-9); got != 0.5 {
		t.Fatalf("if at x=2 = %g, want 0.5", got)
	}
}

func TestExprVariablesAndParams(t *testing.T) {
	wl := 632.8e-9
	params := map[string]float64{"f": 0.3, "l": 2}
	got := evalStr(t, "-k*(x*x + y*y)/(2*f) + l*th", params, 0.001, 0, wl)
	want := -2*math.Pi/wl*(1e-6)/(2*0.3) + 2*0
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("phase = %g, want %g", got, want)
	}
	if got := evalStr(t, "r", params, 3, 4, wl); got != 5 {
		t.Fatalf("r = %g, want 5", got)
	}
	if got := evalStr(t, "k", params, 0, 0, wl); math.Abs(got-2*math.Pi/wl) > 1 {
		t.Fatalf("k = %g, want %g", got, 2*math.Pi/wl)
	}
}

func TestExprNamesAndNeeds(t *testing.T) {
	c, err := CompileExpr("x + f + x", map[string]float64{"f": 1})
	if err != nil {
		t.Fatal(err)
	}
	names := c.Names()
	if strings.Join(names, ",") != "f,x" {
		t.Fatalf("Names = %v, want [f x]", names)
	}
	if c.NeedsRadius() || c.NeedsTheta() {
		t.Fatal("x-only expression must not ask for r or th")
	}
	c2, err := CompileExpr("r*cos(th)", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.NeedsRadius() || !c2.NeedsTheta() {
		t.Fatal("radial expression must ask for r and th")
	}
}

func TestExprErrors(t *testing.T) {
	cases := []struct {
		src     string
		wantCol int
		wantMsg string
	}{
		{"1 + q", 5, "unknown variable"},
		{"1 + foo(2)", 5, "unknown function"},
		{"sqrt(1, 2)", 1, "takes 1 argument"},
		{"1 +", 4, "unexpected end"},
		{"1 + * 2", 5, "unexpected"},
		{"2 @ 3", 3, "unexpected character"},
		{"(1 + 2", 7, "expected ')'"},
		{"", 1, "empty expression"},
		{"1.2.3", 4, "malformed number"},
		{"1e", 2, "malformed exponent"},
	}
	for _, c := range cases {
		_, err := CompileExpr(c.src, map[string]float64{"f": 1})
		if err == nil {
			t.Errorf("CompileExpr(%q) succeeded, want error", c.src)
			continue
		}
		ee, ok := err.(*ExprError)
		if !ok {
			t.Errorf("CompileExpr(%q) error type %T, want *ExprError", c.src, err)
			continue
		}
		if ee.Pos != c.wantCol {
			t.Errorf("CompileExpr(%q) column %d, want %d (%s)", c.src, ee.Pos, c.wantCol, ee.Msg)
		}
		if !strings.Contains(ee.Msg, c.wantMsg) {
			t.Errorf("CompileExpr(%q) message %q, want it to contain %q", c.src, ee.Msg, c.wantMsg)
		}
	}
}

// A parameter is a constant: it must not be re-read from anywhere at run time,
// and a parameter whose name shadows a builtin must win (a definition may well
// want to call a parameter "e" or "k").
func TestExprParamShadowing(t *testing.T) {
	c, err := CompileExpr("k*2", map[string]float64{"k": 3})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Eval(0, 0, 632.8e-9); got != 6 {
		t.Fatalf("parameter k must shadow the builtin: got %g, want 6", got)
	}
}

func TestExprNonFiniteIsNotPanic(t *testing.T) {
	// Division by zero is IEEE 754, not a crash: the loader's probe is what
	// turns it into a diagnostic.
	got := evalStr(t, "1/x", nil, 0, 0, 632.8e-9)
	if !math.IsInf(got, 1) {
		t.Fatalf("1/0 = %g, want +Inf", got)
	}
}
