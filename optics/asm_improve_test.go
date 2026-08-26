package optics

import (
	"math"
	"testing"
)

// ---------- Bluestein FFT (non-power-of-two) ----------

func naiveDFT1D(a []complex128) []complex128 {
	n := len(a)
	out := make([]complex128, n)
	for k := 0; k < n; k++ {
		var s complex128
		for j := 0; j < n; j++ {
			s += a[j] * cexpI(-2*math.Pi*float64(k*j)/float64(n))
		}
		out[k] = s
	}
	return out
}

func TestBluesteinMatchesNaiveDFT(t *testing.T) {
	rng := &simpleRNG{seed: 99}
	for _, n := range []int{100, 200, 480} {
		a := make([]complex128, n)
		for i := range a {
			a[i] = complex(rng.next(), rng.next())
		}
		want := naiveDFT1D(a)
		fft1DAny(a, false)
		for k := range a {
			if cabs(a[k]-want[k]) > 1e-9 {
				t.Fatalf("n=%d: Bluestein vs naive DFT mismatch at %d: %v vs %v", n, k, a[k], want[k])
			}
		}
	}
}

func TestBluesteinRoundTrip(t *testing.T) {
	n := 100
	rng := &simpleRNG{seed: 5}
	a := make([]complex128, n)
	for i := range a {
		a[i] = complex(rng.next(), rng.next())
	}
	orig := make([]complex128, n)
	copy(orig, a)
	fft1DAny(a, false)
	fft1DAny(a, true)
	for i := range a {
		if cabs(a[i]-orig[i]) > 1e-12 {
			t.Fatalf("round trip mismatch at %d: %v vs %v", i, a[i], orig[i])
		}
	}
}

func TestFFT2DNonPowerOfTwo(t *testing.T) {
	n := 100
	a := make([]complex128, n*n)
	rng := &simpleRNG{seed: 11}
	for i := range a {
		a[i] = complex(rng.next(), rng.next())
	}
	orig := make([]complex128, len(a))
	copy(orig, a)
	fft2D(a, n, false)
	fft2D(a, n, true)
	for i := range a {
		if cabs(a[i]-orig[i]) > 1e-11 {
			t.Fatalf("2D round trip mismatch at %d", i)
		}
	}
	// Impulse -> flat spectrum on a non-power-of-two grid too.
	b := make([]complex128, n*n)
	b[0] = 1
	fft2D(b, n, false)
	for i := range b {
		if cabs(b[i]-1) > 1e-11 {
			t.Fatalf("impulse spectrum not flat at %d: %v", i, b[i])
		}
	}
}

// ASM on an even non-power-of-two grid must give the same plane-wave phase and
// conserve power as on a power-of-two grid.
func TestASMNonPowerOfTwoGrid(t *testing.T) {
	n, width, wl := 100, 0.01, 632.8e-9
	f, err := BuildSource(SourceSpec{Type: "plane", Params: map[string]any{}}, n, width, false, wl)
	if err != nil {
		t.Fatal(err)
	}
	p0 := f.Power()
	z := 0.3
	if err := Propagate(f, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	want := 2 * math.Pi / wl * z
	c := f.Ex[(n/2)*n+n/2]
	got := math.Remainder(math.Atan2(imag(c), real(c))-want, 2*math.Pi)
	if math.Abs(got) > 1e-6 {
		t.Fatalf("plane wave phase wrong: got %g want %g", got, want)
	}
	if rel := math.Abs(f.Power()-p0) / p0; rel > 1e-6 {
		t.Fatalf("power not conserved: rel %g", rel)
	}
}

func TestValidateEvenGridSize(t *testing.T) {
	base := Config{Grid: GridSpec{Size: 100, Width: 0.01}, Wavelength: 632.8e-9, Method: "asm", Evanescent: "decay", Source: SourceSpec{Type: "plane"}}
	if issues := ValidateConfig(&base); len(issues) > 0 {
		t.Fatalf("even non-power-of-two grid rejected: %v", issues)
	}
	odd := base
	odd.Grid.Size = 101
	found := false
	for _, is := range ValidateConfig(&odd) {
		if is.Path == "grid.size" {
			found = true
		}
	}
	if !found {
		t.Fatalf("odd grid size should be rejected")
	}
}

// ---------- asm_shift_pad combination ----------

// With a moderate, well-contained tilt, asm_shift_pad must reproduce plain ASM
// (the combined shift + pad is a correct propagator, not a double shift).
func TestASMShiftPadModerateTilt(t *testing.T) {
	n, width, wl := 512, 0.01, 632.8e-9
	theta := 0.005
	z := 0.2
	ref := buildGaussianTilted(n, width, wl, 1e-3, theta)
	if err := Propagate(ref, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	sp := buildGaussianTilted(n, width, wl, 1e-3, theta)
	if err := Propagate(sp, z, MethodASMShiftPad, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	if r := relL2(sp, ref); r > 1e-3 {
		t.Fatalf("asm_shift_pad deviates from asm (moderate tilt) by %g", r)
	}
	want := z * math.Tan(theta)
	if math.Abs(centroidX(sp)-want) > 2e-6 {
		t.Fatalf("asm_shift_pad centroid %g m, want %g m", centroidX(sp), want)
	}
}

// A tilted beam that also walks off near the window edge wraps in plain ASM;
// asm_shift_pad (shift + 2x pad) must keep the lateral shift exact.
func TestASMShiftPadWalkOff(t *testing.T) {
	n, width, wl := 1024, 0.01, 632.8e-9
	waist, theta, z := 1e-3, 0.03, 0.15
	want := z * math.Tan(theta)
	f := buildGaussianTilted(n, width, wl, waist, theta)
	if err := Propagate(f, z, MethodASMShiftPad, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	if err := math.Abs(centroidX(f) - want); err > 3e-4 {
		t.Fatalf("asm_shift_pad centroid error %g m (>3e-4)", err)
	}
}

// ---------- evanescent handling ----------

func evanescentField(n int, dx, wl float64, q int) *Field {
	f := NewField(n, dx, false)
	f0 := float64(q) / (float64(n) * dx)
	for j := 0; j < n; j++ {
		for i := 0; i < n; i++ {
			f.Ex[j*n+i] = cexpI(2 * math.Pi * f0 * f.X(i))
		}
	}
	return f
}

// A single sub-wavelength spatial frequency is evanescent: forward ASM decays
// it by exp(-k z sqrt((lambda*f)^2 - 1)); EvanescentLimit truncates it to 0.
func TestEvanescentLimitTruncation(t *testing.T) {
	n, dx, wl := 256, 0.25e-6, 1e-6
	q := 96
	f0 := float64(q) / (float64(n) * dx)
	if wl*f0 <= 1 {
		t.Fatalf("test setup: not evanescent (lambda*f=%g)", wl*f0)
	}
	z := 1e-6
	decay := (2 * math.Pi / wl) * z * math.Sqrt((wl*f0)*(wl*f0)-1)

	fd := evanescentField(n, dx, wl, q)
	if err := Propagate(fd, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	got := cabs(fd.Ex[0])
	want := math.Exp(-decay)
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("evanescent decay wrong: got %g want %g", got, want)
	}

	fl := evanescentField(n, dx, wl, q)
	cl := &Context{Wavelength: wl, Evanescent: "decay", EvanescentLimit: 1.0, Warnings: &Warnings{}}
	if err := Propagate(fl, z, MethodASM, cl); err != nil {
		t.Fatal(err)
	}
	if fl.Power() > 1e-20 {
		t.Fatalf("limit truncation should zero the evanescent component, power=%g", fl.Power())
	}
}

// Backward propagation: default zeroes the amplifying evanescent components;
// Tikhonov regularization damps them to a finite value. Propagating
// (band-limited) content round-trips exactly in both cases.
func TestBackwardTikhonov(t *testing.T) {
	n, width, wl := 256, 0.01, 632.8e-9
	z := 0.05
	f, err := BuildSource(SourceSpec{Type: "gaussian", Params: map[string]any{"waist": 1e-3}}, n, width, false, wl)
	if err != nil {
		t.Fatal(err)
	}
	orig := f.Clone()
	if err := Propagate(f, z, MethodASM, ctxFor(wl)); err != nil {
		t.Fatal(err)
	}
	cb := &Context{Wavelength: wl, Evanescent: "decay", BackwardRegularize: true, TikhonovAlpha: 1e-3, Warnings: &Warnings{}}
	if err := Propagate(f, -z, MethodASM, cb); err != nil {
		t.Fatal(err)
	}
	if r := relL2(f, orig); r > 1e-6 {
		t.Fatalf("backward round trip (regularized) deviates by %g", r)
	}

	// Evanescent content: regularized backward stays finite; default zeroes it.
	nE, dx, wlE := 256, 0.25e-6, 1e-6
	evReg := evanescentField(nE, dx, wlE, 96)
	cr := &Context{Wavelength: wlE, Evanescent: "decay", BackwardRegularize: true, TikhonovAlpha: 1e-3, Warnings: &Warnings{}}
	if err := Propagate(evReg, -1e-6, MethodASM, cr); err != nil {
		t.Fatal(err)
	}
	assertFinite(t, evReg)
	if evReg.Power() <= 0 {
		t.Fatalf("regularized backward evanescent power should be positive")
	}
	evZero := evanescentField(nE, dx, wlE, 96)
	if err := Propagate(evZero, -1e-6, MethodASM, ctxFor(wlE)); err != nil {
		t.Fatal(err)
	}
	assertFinite(t, evZero)
	if evZero.Power() > 1e-20 {
		t.Fatalf("default backward evanescent should be zeroed, power=%g", evZero.Power())
	}
}

func assertFinite(t *testing.T, f *Field) {
	t.Helper()
	for i := range f.Ex {
		if math.IsInf(real(f.Ex[i]), 0) || math.IsInf(imag(f.Ex[i]), 0) || math.IsNaN(real(f.Ex[i])) || math.IsNaN(imag(f.Ex[i])) {
			t.Fatalf("non-finite field value at %d: %v", i, f.Ex[i])
		}
	}
}
