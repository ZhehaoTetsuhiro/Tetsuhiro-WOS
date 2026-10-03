package optics

import (
	"math"
	"math/cmplx"
	"testing"
)

func uniformField(n int, dx, amp float64) *Field {
	f := NewField(n, dx, false)
	for i := range f.Ex {
		f.Ex[i] = complex(amp, 0)
	}
	return f
}

func applyElem(t *testing.T, typ string, params map[string]any, f *Field) {
	t.Helper()
	el, err := NewElement(ElementSpec{Type: typ, Params: params})
	if err != nil {
		t.Fatalf("NewElement(%s): %v", typ, err)
	}
	if err := el.Apply(f, &Context{Wavelength: 1e-6}); err != nil {
		t.Fatalf("%s.Apply: %v", typ, err)
	}
}

// TestKerrPhaseScalesWithIntensity pins the Kerr element: the phase is
// k*n2*L*|E|², i.e. proportional to the local intensity. That intensity
// dependence is exactly what the linear `medium` element cannot express.
func TestKerrPhaseScalesWithIntensity(t *testing.T) {
	const wl, n2, L = 1e-6, 1e-3, 1e-3
	k := 2 * math.Pi / wl
	phase := func(amp float64) float64 {
		f := uniformField(4, 1e-5, amp)
		applyElem(t, "kerr", map[string]any{"n2": n2, "length": L}, f)
		got := f.Ex[0]
		if amp == 0 {
			if got != 0 {
				t.Fatalf("a dark pixel must be untouched, got %v", got)
			}
			return 0
		}
		return math.Atan2(imag(got), real(got))
	}
	p1 := phase(0.1)
	if want := k * n2 * L * (0.1 * 0.1); math.Abs(p1-want) > 1e-9 {
		t.Errorf("phase %.9g, want k*n2*L*I = %.9g", p1, want)
	}
	if p2 := phase(0.2); math.Abs(p2-4*p1) > 1e-9 {
		t.Errorf("doubling the amplitude must quadruple the phase: %.9g vs 4*%.9g", p2, p1)
	}
	phase(0)
}

// TestKerrTwoPhotonAbsorption checks the optional two-photon loss: tpa > 0
// attenuates in proportion to the intensity, tpa = 0 keeps the amplitude.
func TestKerrTwoPhotonAbsorption(t *testing.T) {
	base := map[string]any{"n2": 1e-3, "length": 1e-3}
	f := uniformField(4, 1e-5, 1.0)
	applyElem(t, "kerr", base, f)
	if a := math.Abs(real(f.Ex[0])); math.Abs(a-1) > 1e-12 {
		t.Errorf("pure phase must not change the amplitude, |E|=%g", a)
	}
	g := uniformField(4, 1e-5, 1.0)
	applyElem(t, "kerr", map[string]any{"n2": 1e-3, "length": 1e-3, "tpa": 1e-2}, g)
	want := math.Exp(-0.5 * 1e-2 * 1.0 * 1e-3)
	if a := math.Abs(real(g.Ex[0])); math.Abs(a-want) > 1e-12 {
		t.Errorf("two-photon absorption |E|=%g, want %g", a, want)
	}
}

// TestSaturableAbsorberTransmitsBrighter is the amplitude-side complement: the
// absorption falls as the intensity rises, so a bright region transmits more
// than a dim one.
func TestSaturableAbsorberTransmitsBrighter(t *testing.T) {
	const alpha0, L, isat = 4.0, 1.0, 1.0
	trans := func(amp float64) float64 {
		f := uniformField(4, 1e-5, amp)
		applyElem(t, "saturable_absorber", map[string]any{"alpha0": alpha0, "length": L, "isat": isat}, f)
		return math.Abs(real(f.Ex[0])) / amp
	}
	dim, bright := trans(0.01), trans(100)
	if dim >= bright {
		t.Errorf("a saturable absorber must pass more light at high intensity: dim %.6g, bright %.6g", dim, bright)
	}
	if want := math.Exp(-alpha0 * L / (2 * (1 + 0.01*0.01/isat))); math.Abs(dim-want) > 1e-12 {
		t.Errorf("dim transmittance %.9g, want %g", dim, want)
	}
	if bright < 0.999 {
		t.Errorf("at I >> I_sat the absorber is transparent, got %.6g", bright)
	}
}

// TestNonlinearElementsRegistered checks the two new elements are registered
// and documented in the catalog (the GUI reads the catalog).
func TestNonlinearElementsRegistered(t *testing.T) {
	have := map[string]bool{}
	for _, n := range RegisteredElements() {
		have[n] = true
	}
	for _, want := range []string{"kerr", "saturable_absorber"} {
		if !have[want] {
			t.Errorf("element %q is not registered", want)
		}
	}
	docs := map[string]bool{}
	for _, d := range BuildCatalog().Elements {
		docs[d.Type] = true
	}
	for _, want := range []string{"kerr", "saturable_absorber"} {
		if !docs[want] {
			t.Errorf("element %q has no catalog entry", want)
		}
	}
}

// TestKerrSceneAddsIntensityPhase runs the element inside a scene: a bright
// uniform beam through a Kerr slab must come out with a uniform extra phase
// (and otherwise unchanged), which is the primitive a nonlinear cavity needs.
// A plane wave is used so the phase is uniform and does not itself diffract.
func TestKerrSceneAddsIntensityPhase(t *testing.T) {
	build := func(n2 float64) *Result {
		src := planeSrc(v3(0, 0, -0.02), v3(0, 0, 1))
		ke := ComponentSpec{Type: "kerr", Label: "克尔片", Pos: v3(0, 0, 0), Shape: circleShape(3e-3),
			Params: map[string]any{"n2": n2, "length": 1.0}}
		det := bigDet("屏", v3(0, 0, 0.001), math.Pi, 8e-3)
		res, err := Simulate(sceneCfg(128, 8e-3, []SourceSpec{src}, ke, det))
		if err != nil {
			t.Fatalf("Simulate(n2=%g): %v", n2, err)
		}
		if len(res.Planes) != 1 {
			t.Fatalf("want the detector plane, got %v", planeLabels(res))
		}
		return res
	}
	lin := build(0)
	nl := build(1e-8) // ~1 rad at the ~16 W/m^2 of a 1 mW plane wave on this grid
	idx := (128/2)*128 + 128/2
	a, b := lin.Planes[0].Ex[idx], nl.Planes[0].Ex[idx]
	if cmplx.Abs(a) == 0 || cmplx.Abs(b) == 0 {
		t.Fatal("centre pixel is dark; test setup is wrong")
	}
	aa := cmplx.Abs(a)
	ratio := b * conjc(a) / complex(aa*aa, 0)
	if d := math.Abs(cmplx.Abs(ratio) - 1); d > 1e-9 {
		t.Errorf("the Kerr slab changed the amplitude (|ratio|-1=%g); it must be a pure phase", d)
	}
	if ang := math.Atan2(imag(ratio), real(ratio)); math.Abs(ang) < 0.1 {
		t.Errorf("the nonlinear phase is %g rad, want a visible shift", ang)
	}
}
