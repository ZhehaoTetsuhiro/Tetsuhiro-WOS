package optics

import (
	"fmt"
	"math"
)

// ---------------------------------------------------------------------------
// Runtime helpers for generated elements (see exprgen.go)
//
// `wos -gen-go` prints a native kernel element whose expressions are inlined
// arithmetic; the few operations that do not exist as Go operators or math
// functions call into this file. Keeping the semantics here (rather than in the
// generated text) means a generated element and the interpreter agree by
// construction: both sides funnel through the same definitions.
// ---------------------------------------------------------------------------

// genParamNum reads one parameter the way a scripted element does: a missing
// value falls back to the definition's default, anything present must reduce to
// a number.
func genParamNum(p map[string]any, key string, def float64) (float64, error) {
	raw, ok := p[key]
	if !ok || raw == nil {
		return def, nil
	}
	v, err := paramNumericValue(raw)
	if err != nil {
		return 0, fmt.Errorf("parameter %q: %v", key, err)
	}
	return v, nil
}

// genB2F turns a boolean expression into 1/0, the value comparisms carry in the
// expression language.
func genB2F(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func genSign(v float64) float64 { return signf(v) }
func genClamp(v, lo, hi float64) float64 {
	return math.Min(math.Max(v, lo), hi)
}
func genStep(v float64) float64 {
	if v >= 0 {
		return 1
	}
	return 0
}
func genRect(v float64) float64 {
	if math.Abs(v) <= 0.5 {
		return 1
	}
	return 0
}
func genSmoothstep(e0, e1, x float64) float64 { return smoothstepd(e0, e1, x) }
func genSinc(x float64) float64               { return sincPi(x) }
func genRad(deg float64) float64              { return deg * math.Pi / 180 }
func genDeg(rad float64) float64              { return rad * 180 / math.Pi }
