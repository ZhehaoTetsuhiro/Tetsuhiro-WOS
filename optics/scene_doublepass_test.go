package optics

import (
	"math"
	"testing"
)

// ---------------------------------------------------------------------------
// Partial-reflector double pass and recirculating evaluation order
//
// A beam returning through a plane-parallel gap arrives at the reflector along
// the fold direction of the forward pass. Visits are keyed by the *arrival*
// direction, so the return gets its own interaction and is launched: keying a
// visit by the mirror's fold direction merged the return into the
// already-launched forward visit, the return wave was never emitted (the
// trace showed the mirror hit twice while the beam list ended before the
// return) and a gap interference came out a flat field with no warning.
//
// A recirculating layout makes the visit graph cyclic, and the evaluation
// order must respect dependencies, not the earliest arrival: with an observer
// placed closer to the reflector than the gap round trip, an arrival-ordered
// evaluation used to run the observer before the return visit, skip its field
// and record the reflected wave alone. Further round trips beyond the first
// are still cut (bounded); they must be counted and reported, never dropped
// silently.
// ---------------------------------------------------------------------------

// TestScenePartialMirrorLaunchesReturnPass pins the routing: a partial mirror
// hit by its own return beam must produce a second interaction with its own
// outgoing beams, one of which carries the return to the detector.
func TestScenePartialMirrorLaunchesReturnPass(t *testing.T) {
	src := planeSrc(v3(0, 0, -0.05), v3(0, 0, 1))
	s1 := ComponentSpec{ID: "s1", Type: "mirror", Label: "部分反射镜", Pos: v3(0, 0, 0), Yaw: 0,
		Shape: circleShape(3.5e-3), Params: map[string]any{"reflectivity": 0.2}}
	m2 := ComponentSpec{ID: "m2", Type: "mirror", Label: "后镜", Pos: v3(0, 0, 3e-3), Yaw: 0,
		Shape: circleShape(3.5e-3), Params: map[string]any{"reflectivity": 1.0}}
	det := ComponentSpec{Type: "sensor", Label: "返回端口", Pos: v3(0, 0, -5e-4), Yaw: 0,
		Shape: circleShape(3.3e-3)}
	tr, err := TraceScene(&SceneSpec{Components: []ComponentSpec{s1, m2, det}}, []SourceSpec{src}, 632.8e-9)
	if err != nil {
		t.Fatalf("TraceScene: %v", err)
	}
	left, arrivals := 0, 0
	for _, s := range tr.Segments {
		if s.From.Sub(v3(0, 0, 0)).Norm() < 1e-9 {
			left++
		}
		if s.To.Sub(v3(0, 0, -5e-4)).Norm() < 1e-9 {
			arrivals++
		}
	}
	// Four beams leave s1: the forward pass's reflection and transmission, plus
	// the return pass's two outgoing beams (reflected back into the gap and
	// transmitted on to the detector). Before the fix only the forward two
	// existed — the return was merged into the forward visit.
	if left != 4 {
		t.Errorf("beams launched by the partial mirror = %d, want 4 (the return pass is missing)", left)
	}
	if arrivals != 2 {
		t.Errorf("beams reaching the detector = %d, want 2 (forward reflection + return wave)", arrivals)
	}
}

// ringWL and ringReff fix the two-wave gap construction: a partial reflector,
// a thin lens f = −R_eff as the gap sag and a full mirror as the second
// surface, so the round trip carries the quadratic phase 2k·r²/(2R_eff).
const (
	ringWL   = 632.8e-9
	ringReff = 1.0
)

// ringGapScene builds the gap interferometer with the observer behind the
// partial reflector at z = sensorZ (negative). The gap pistons 2h and 2d are
// integer multiples of the wavelength so the centre sits on an exact extremum.
// partial is "mirror" (reflectivity 0.2, the natural spelling) or
// "beamsplitter" (the equivalent power reflectivity 0.04).
func ringGapScene(t *testing.T, partial string, sensorZ float64) Config {
	t.Helper()
	wl := ringWL
	h := math.Round(1e-3/(wl/2)) * (wl / 2) // reflector → gap plane
	d := math.Round(4e-4/wl) * wl           // gap plane → second mirror
	var s1 ComponentSpec
	switch partial {
	case "mirror":
		s1 = ComponentSpec{Type: "mirror", Label: "部分反射镜", Pos: v3(0, 0, 0), Yaw: 0,
			Shape: circleShape(3.5e-3), Params: map[string]any{"reflectivity": 0.2}}
	case "beamsplitter":
		s1 = ComponentSpec{Type: "beamsplitter", Label: "部分反射镜", Pos: v3(0, 0, 0), Yaw: 0,
			Shape: circleShape(3.5e-3), Params: map[string]any{"reflectivity": 0.04}}
	default:
		t.Fatalf("unknown partial reflector %q", partial)
	}
	lens := ComponentSpec{Type: "lens", Label: "间隙透镜", Pos: v3(0, 0, h), Yaw: 0,
		Params: map[string]any{"f": -ringReff}}
	m2 := ComponentSpec{Type: "mirror", Label: "后镜", Pos: v3(0, 0, h+d), Yaw: 0,
		Params: map[string]any{"reflectivity": 1.0}}
	det := ComponentSpec{Type: "sensor", Label: "观察面", Pos: v3(0, 0, sensorZ), Yaw: 0,
		Shape: circleShape(3.3e-3)}
	src := planeSrc(v3(0, 0, -0.05), v3(0, 0, 1))
	return sceneCfg(512, 8e-3, []SourceSpec{src}, s1, lens, m2, det)
}

// ringPattern measures a recorded two-wave gap pattern: the contrast
// (strongest minimum over the peak) inside r ≤ 2 mm and the ring orders
// r²/((R_eff+2L)·λ) of the intensity minima along the horizontal centre row,
// with L the observer distance from the reflector. Ring orders come out as
// ±1 apart for a quadratic gap whatever the piston, which keeps the check
// independent of the absolute phase convention.
func ringPattern(t *testing.T, pl *Plane, sensorZ float64) (contrast float64, orders []float64) {
	t.Helper()
	n := pl.Size
	j := n / 2
	I := intensityOf(pl)
	row := make([]float64, n) // 3-pixel smoothing to stabilise the minima
	for i := 1; i < n-1; i++ {
		row[i] = (I[j*n+i-1] + I[j*n+i] + I[j*n+i+1]) / 3
	}
	const rmax = 2.0e-3
	mx := 0.0
	for i := 0; i < n; i++ {
		if math.Abs(pl.DX*(float64(i)-float64(n)/2)) > rmax {
			continue
		}
		mx = math.Max(mx, row[i])
	}
	mn := math.Inf(1)
	for i := n/2 + 1; i < n-1; i++ {
		x := pl.DX * (float64(i) - float64(n)/2)
		if x > rmax {
			break
		}
		mn = math.Min(mn, row[i])
		// A real dark ring dips far below the peak; the strict local-minimum
		// test plus the depth threshold keeps a flat (return-wave missing)
		// plane from reporting every pixel as a "minimum".
		if row[i] < row[i-1] && row[i] < row[i+1] && row[i] < 0.7*mx {
			orders = append(orders, x*x/((ringReff+2*math.Abs(sensorZ))*ringWL))
		}
	}
	if !(mx > 0) {
		t.Fatalf("recorded plane carries no intensity")
	}
	return mn / mx, orders
}

// ringCheck asserts the two-beam ring invariants: contrast ((t²−r)/(t²+r))² =
// 0.429 for amplitudes 0.2 and 0.96, at least five dark rings, and the
// quadratic scaling law (successive r² orders one apart). offset is where the
// minima sit modulo 1 (0.5 for the real mirror coefficient, 0.75 for the
// splitter's i·sqrt(R)); it is a consequence of the reflection phase
// convention, so it is asserted loosely.
func ringCheck(t *testing.T, pl *Plane, sensorZ, offset float64) {
	t.Helper()
	contrast, orders := ringPattern(t, pl, sensorZ)
	t.Logf("contrast = %.4f, %d minima", contrast, len(orders))
	if contrast < 0.40 || contrast > 0.46 {
		t.Errorf("ring contrast (min/max) = %.4f, want ≈ 0.429 — a flat field (the dropped return wave) reads ≈ 1", contrast)
		if contrast > 0.9 {
			// The plane carries no fringes at all; the minima list would only
			// be noise from the flat background.
			return
		}
	}
	if len(orders) < 5 {
		t.Fatalf("found %d intensity minima, want ≥ 5 dark rings: %v", len(orders), orders)
	}
	for m := 0; m < 3 && m < len(orders); m++ {
		want := offset + math.Round(orders[m]-offset) // nearest order with this phase offset
		if d := math.Abs(orders[m] - want); d > 0.15 {
			t.Errorf("dark ring %d at r²/((R_eff+2L)·λ) = %.3f, want %.2f (mod 1 = %.2f)", m, orders[m], want, offset)
		}
	}
	for m := 1; m < len(orders); m++ {
		if d := math.Abs(orders[m] - orders[m-1] - 1); d > 0.15 {
			t.Errorf("ring spacing %.3f orders between minima #%d and #%d, want 1.0", orders[m]-orders[m-1], m-1, m)
		}
	}
}

// TestScenePartialMirrorGapInterference is the regression for the partial
// mirror: the literature two-wave rings through a `mirror(reflectivity=0.2)`
// with the observer closer to the reflector than the gap round trip — the
// construction that used to record the reflected wave alone, flat, silently.
func TestScenePartialMirrorGapInterference(t *testing.T) {
	res, err := Simulate(ringGapScene(t, "mirror", -0.5e-3))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("want the observer plane, got %v", planeLabels(res))
	}
	ringCheck(t, res.Planes[0], -0.5e-3, 0.5)
	// The second round trip is cut on purpose (bounded round trips); it must be
	// counted and reported instead of vanishing.
	found := false
	for _, w := range res.Warnings {
		if w.Code == "scene_cycle_dropped" {
			found = true
			if w.Value < 1 {
				t.Errorf("scene_cycle_dropped with value %g, want the number of skipped contributions", w.Value)
			}
		}
	}
	if !found {
		t.Errorf("no scene_cycle_dropped warning: the cut round trip was silent (warnings: %v)", res.Warnings)
	}
}

// TestSceneGapInterferenceObserverOrder checks that the observer's distance
// from the reflector no longer decides whether the return wave is included:
// near (closer than the gap round trip) and far observers record the same
// rings, for both spellings of the partial reflector.
func TestSceneGapInterferenceObserverOrder(t *testing.T) {
	firsts := map[string]float64{}
	for _, c := range []struct {
		name, partial string
		sensorZ       float64
		offset        float64
	}{
		{"mirror-near", "mirror", -0.5e-3, 0.5},
		{"mirror-far", "mirror", -3.5e-3, 0.5},
		{"splitter-near", "beamsplitter", -0.5e-3, 0.75},
		{"splitter-far", "beamsplitter", -3.5e-3, 0.75},
	} {
		t.Run(c.name, func(t *testing.T) {
			res, err := Simulate(ringGapScene(t, c.partial, c.sensorZ))
			if err != nil {
				t.Fatalf("Simulate: %v", err)
			}
			if len(res.Planes) != 1 {
				t.Fatalf("want one plane, got %v", planeLabels(res))
			}
			ringCheck(t, res.Planes[0], c.sensorZ, c.offset)
			_, orders := ringPattern(t, res.Planes[0], c.sensorZ)
			if len(orders) > 0 {
				firsts[c.name] = math.Sqrt(orders[0]) // ∝ radius of the first dark ring
			}
		})
	}
	// The first ring of the mirror construction must not move with the
	// observer's distance beyond the (R_eff+2L) scaling — a few parts in 10³.
	if a, b := firsts["mirror-near"], firsts["mirror-far"]; a > 0 && b > 0 {
		if rel := math.Abs(a-b) / b; rel > 0.02 {
			t.Errorf("first dark ring moves with the observer distance: √order %.4f vs %.4f (rel %.3f)", a, b, rel)
		}
	}
}
