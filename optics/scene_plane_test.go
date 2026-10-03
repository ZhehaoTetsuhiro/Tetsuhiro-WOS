package optics

import (
	"math"
	"strings"
	"testing"
)

// coplanarScene builds the evidence §9.2/§9.3 layout: a plane source, two
// slits printed on the same z, and a wide detector behind them.
func coplanarScene(w float64, parallel bool, comps ...ComponentSpec) Config {
	src := planeSrc(v3(0, 0, -0.3), v3(0, 0, 1))
	slit := func(label string, x float64) ComponentSpec {
		p := map[string]any{}
		if parallel {
			p["parallel"] = true
		}
		return ComponentSpec{Type: "aperture", Label: label, Pos: v3(x, 0, -0.2),
			Shape: RectOutline(0.8e-3, 4e-3), Params: p}
	}
	all := append([]ComponentSpec{slit("左缝", -1e-3), slit("右缝", 1e-3)}, comps...)
	return sceneCfg(256, w, []SourceSpec{src}, all...)
}

// TestSceneCoplanarStackIsOnePlane is the §9.3 regression: two elements on the
// same z used to make the router declare a zero-length cavity, drop the whole
// path as scene_cycle_dropped and record no plane at all. They now share one
// plane visit, the path survives, and the run says so out loud.
func TestSceneCoplanarStackIsOnePlane(t *testing.T) {
	const w = 8e-3
	cfg := coplanarScene(w, false, bigDet("屏", v3(0, 0, 0.05), math.Pi, w))
	res, err := Simulate(cfg)
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Planes) != 1 {
		t.Fatalf("coplanar elements dropped the path: planes=%v warnings=%v", planeLabels(res), res.Warnings)
	}
	warned, dropped := false, false
	for _, wn := range res.Warnings {
		if wn.Code == "scene_cycle_dropped" {
			dropped = true
		}
		if strings.Contains(wn.Message, "共面元件") {
			warned = true
		}
	}
	if dropped {
		t.Errorf("coplanar elements still report a false cavity: %v", res.Warnings)
	}
	if !warned {
		t.Errorf("the coplanar stack was silent; want a scene_geometry warning naming it (got %v)", res.Warnings)
	}
}

// TestSceneCoplanarSeriesIntersects pins the default composition: two thin
// elements on one plane multiply their transmittances, so two disjoint slits
// leave (almost) no light. The parallel flag is the documented way to get the
// union instead — §9.2.
func TestSceneCoplanarSeriesIntersects(t *testing.T) {
	const w = 8e-3
	series, err := Simulate(coplanarScene(w, false, bigDet("屏", v3(0, 0, 0.05), math.Pi, w)))
	if err != nil {
		t.Fatalf("Simulate series: %v", err)
	}
	union, err := Simulate(coplanarScene(w, true, bigDet("屏", v3(0, 0, 0.05), math.Pi, w)))
	if err != nil {
		t.Fatalf("Simulate parallel: %v", err)
	}
	if len(series.Planes) != 1 || len(union.Planes) != 1 {
		t.Fatalf("want one plane each, got %v / %v", planeLabels(series), planeLabels(union))
	}
	// Single slit reference: the same scene with the right slit removed.
	one, err := Simulate(sceneCfg(256, w, []SourceSpec{planeSrc(v3(0, 0, -0.3), v3(0, 0, 1))},
		ComponentSpec{Type: "aperture", Label: "左缝", Pos: v3(-1e-3, 0, -0.2), Shape: RectOutline(0.8e-3, 4e-3)},
		bigDet("屏", v3(0, 0, 0.05), math.Pi, w)))
	if err != nil {
		t.Fatalf("Simulate single: %v", err)
	}
	p1 := one.Planes[0].Stats.Power
	ps, pu := series.Planes[0].Stats.Power, union.Planes[0].Stats.Power
	if ps > p1*1e-3 {
		t.Errorf("series composition of disjoint slits transmitted %.3g W (single slit %.3g W): it must multiply to ~0", ps, p1)
	}
	if rel := math.Abs(pu-2*p1) / (2 * p1); rel > 0.05 {
		t.Errorf("parallel union transmitted %.3g W, want ~2x the single slit %.3g W (rel %.3g)", pu, p1, rel)
	}
}

// TestSceneMonitorReadsEveryPort is the §9.5 regression: a plain detector ends
// the beam, so only the first of two detectors used to appear in planes[]. A
// detector marked passthrough records the field and lets it continue, so one
// run reads both ports.
func TestSceneMonitorReadsEveryPort(t *testing.T) {
	const w = 8e-3
	m1 := bigDet("监视1", v3(0, 0, 0.05), math.Pi, w)
	m1.Params = map[string]any{"passthrough": true}
	m2 := bigDet("监视2", v3(0, 0, 0.10), math.Pi, w)
	res, err := Simulate(sceneCfg(256, w, []SourceSpec{planeSrc(v3(0, 0, -0.05), v3(0, 0, 1))}, m1, m2))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Planes) != 2 {
		t.Fatalf("monitors: got %v, want both ports (a plain detector would stop at the first)", planeLabels(res))
	}
	for _, pl := range res.Planes {
		if math.Abs(pl.Stats.Power-1e-3)/1e-3 > 0.02 {
			t.Errorf("monitor %s read %.6g W, want ~1e-3 W (a monitor is lossless)", pl.Label, pl.Stats.Power)
		}
	}
}

// TestScenePlainDetectorStillStops pins the old behaviour: a detector without
// passthrough absorbs the light, so a second detector behind it reads nothing
// and is not recorded.
func TestScenePlainDetectorStillStops(t *testing.T) {
	const w = 8e-3
	d1 := bigDet("探测器1", v3(0, 0, 0.05), math.Pi, w)
	d2 := bigDet("探测器2", v3(0, 0, 0.10), math.Pi, w)
	res, err := Simulate(sceneCfg(256, w, []SourceSpec{planeSrc(v3(0, 0, -0.05), v3(0, 0, 1))}, d1, d2))
	if err != nil {
		t.Fatalf("Simulate: %v", err)
	}
	if len(res.Planes) != 1 || res.Planes[0].Label != "探测器1" {
		t.Fatalf("a plain detector must end the path at the first surface, got %v", planeLabels(res))
	}
}
