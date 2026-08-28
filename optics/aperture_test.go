package optics

import (
	"math"
	"testing"
)

// Every advertised aperture shape must construct with its default (or, for
// custom, a supplied vertex list) parameters.
func TestApertureAllShapesConstruct(t *testing.T) {
	plain := []string{
		"circle", "square", "rectangle", "ellipse", "triangle", "ring",
		"polygon", "double_slit", "cross", "star", "superellipse",
	}
	for _, s := range plain {
		if _, err := newAperture(map[string]any{"shape": s}); err != nil {
			t.Fatalf("shape %q: %v", s, err)
		}
	}
	if _, err := newAperture(map[string]any{"shape": "custom", "vertices": "1e-3,0;0,1e-3;-1e-3,0;0,-1e-3"}); err != nil {
		t.Fatalf("custom: %v", err)
	}
}

// The polygon-family signed distance must be positive inside and negative
// outside, for convex (triangle/square) and concave (star) outlines.
func TestPolygonSignedDistanceSign(t *testing.T) {
	tri := regularPolygonVertices(3, 1e-3, math.Pi/2)
	if got := polygonSignedDistance(tri, 0, 0); got <= 0 {
		t.Fatalf("triangle center should be inside, got %g", got)
	}
	if got := polygonSignedDistance(tri, 2e-3, 2e-3); got >= 0 {
		t.Fatalf("point beyond circumradius should be outside triangle, got %g", got)
	}

	sq := regularPolygonVertices(4, 1e-3, math.Pi/4)
	if got := polygonSignedDistance(sq, 0, 0); got <= 0 {
		t.Fatalf("square center should be inside, got %g", got)
	}
	if got := polygonSignedDistance(sq, 2e-3, 2e-3); got >= 0 {
		t.Fatalf("point beyond circumradius should be outside square, got %g", got)
	}

	st := starVertices(5, 1e-3, 5e-4, math.Pi/2)
	if got := polygonSignedDistance(st, 0, 0); got <= 0 {
		t.Fatalf("star center should be inside, got %g", got)
	}
	if got := polygonSignedDistance(st, 2e-3, 0); got >= 0 {
		t.Fatalf("point beyond outer radius should be outside star, got %g", got)
	}
}

// Invalid shape parameters must be rejected at construction time.
func TestApertureValidation(t *testing.T) {
	bad := []map[string]any{
		{"shape": "nope"},
		{"shape": "polygon", "sides": 2},
		{"shape": "star", "inner": 2e-3}, // inner > radius
		{"shape": "ring", "rin": 2e-3, "rout": 1e-3},
		{"shape": "custom", "vertices": "0,0;1,1"}, // too few vertices
		{"shape": "circle", "radius": 0},
	}
	for _, p := range bad {
		if _, err := newAperture(p); err == nil {
			t.Fatalf("expected error for params %v", p)
		}
	}
}
