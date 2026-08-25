package optics

import (
	"fmt"
	"math"
	"testing"
)

func closeTo(t *testing.T, name string, got, want, tol float64) {
	t.Helper()
	if math.Abs(got-want) > tol {
		t.Fatalf("%s = %g, want %g (tol %g)", name, got, want, tol)
	}
}

// The Hong-Ou-Mandel effect: two indistinguishable photons entering a 50:50
// beamsplitter bunch into one output port — the (1,1) coincidence vanishes and
// both photons emerge together.
func TestHongOuMandel(t *testing.T) {
	q, err := FockState(2, 4, []int{1, 1})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.BeamSplitter(0, 1, 0.5); err != nil {
		t.Fatal(err)
	}
	if p := q.JointProb(1, 1); p > 1e-12 {
		t.Fatalf("HOM coincidence P(1,1) = %g, want 0", p)
	}
	closeTo(t, "P(2,0)", q.JointProb(2, 0), 0.5, 1e-9)
	closeTo(t, "P(0,2)", q.JointProb(0, 2), 0.5, 1e-9)
}

// Distinguishable photons do not bunch: sending |1>|1> of orthogonal modes (or
// with a photon-number-resolving tag) still gives a (1,1) coincidence. Here we
// check the complementary single-photon case |1>|0> splits 50/50.
func TestSinglePhotonSplit(t *testing.T) {
	q, err := FockState(2, 4, []int{1, 0})
	if err != nil {
		t.Fatal(err)
	}
	if err := q.BeamSplitter(0, 1, 0.5); err != nil {
		t.Fatal(err)
	}
	closeTo(t, "P(1,0)", q.JointProb(1, 0), 0.5, 1e-9)
	closeTo(t, "P(0,1)", q.JointProb(0, 1), 0.5, 1e-9)
	closeTo(t, "mean mode0", q.MeanPhotonNumber(0), 0.5, 1e-9)
	closeTo(t, "mean mode1", q.MeanPhotonNumber(1), 0.5, 1e-9)
}

// A coherent state has Poissonian photon statistics: mean = variance = |alpha|^2
// and g²(0) = 1.
func TestCoherentStateStatistics(t *testing.T) {
	alpha := 1.0
	q, err := CoherentState(1, 20, []complex128{complex(alpha, 0)})
	if err != nil {
		t.Fatal(err)
	}
	mean := q.MeanPhotonNumber(0)
	closeTo(t, "coherent mean", mean, alpha*alpha, 1e-3)
	dist := q.PhotonNumberDistribution(0)
	var n2 float64
	for n, p := range dist {
		n2 += float64(n*n) * p
	}
	variance := n2 - mean*mean
	closeTo(t, "coherent variance", variance, alpha*alpha, 1e-3)
	closeTo(t, "coherent g2", q.G2(0), 1.0, 1e-3)
}

// A Fock state |n> is antibunched: g²(0) = 1 - 1/n (and g²(0)=0 for n=1).
func TestFockStateG2(t *testing.T) {
	q, err := FockState(1, 8, []int{3})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, "fock |3> g2", q.G2(0), 1.0-1.0/3.0, 1e-12)
	closeTo(t, "fock |3> mean", q.MeanPhotonNumber(0), 3.0, 1e-12)

	one, err := FockState(1, 8, []int{1})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, "fock |1> g2", one.G2(0), 0.0, 1e-12)
}

// Squeezed vacuum: one quadrature is squeezed below the vacuum shot noise
// (1/4) while the orthogonal quadrature is anti-squeezed; their product stays
// at the Heisenberg limit 1/16.
func TestSqueezedVacuumQuadratures(t *testing.T) {
	r := 0.5
	q, err := NewQState(1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if err := q.Squeeze(0, complex(r, 0)); err != nil {
		t.Fatal(err)
	}
	_, vx := q.QuadratureStats(0, 0)
	_, vp := q.QuadratureStats(0, math.Pi/2)
	// Exactly: vx = exp(-2r)/4, vp = exp(2r)/4 (up to Fock truncation).
	closeTo(t, "squeezed var_x", vx, math.Exp(-2*r)/4, 1e-6)
	closeTo(t, "anti-squeezed var_p", vp, math.Exp(2*r)/4, 1e-6)
	if vx >= 0.25 {
		t.Fatalf("squeezed quadrature not below shot noise: var_x=%g", vx)
	}
	closeTo(t, "Heisenberg product", vx*vp, 1.0/16.0, 1e-6)
}

// The two-mode squeezed vacuum (EPR) state has perfect photon-number
// correlation and thermal single-mode statistics (g²(0)=2).
func TestTwoModeSqueezedVacuum(t *testing.T) {
	r := 0.5
	q, err := TwoModeSqueezedVacuum(20, r)
	if err != nil {
		t.Fatal(err)
	}
	nbar := math.Sinh(r) * math.Sinh(r)
	closeTo(t, "TMSV mean mode0", q.MeanPhotonNumber(0), nbar, 1e-9)
	closeTo(t, "TMSV mean mode1", q.MeanPhotonNumber(1), nbar, 1e-9)
	closeTo(t, "TMSV g2 mode0", q.G2(0), 2.0, 1e-3)
	// Perfect correlation: off-diagonal joint probabilities vanish.
	base := q.Cutoff + 1
	for a := 0; a <= 5; a++ {
		for b := 0; b <= 5; b++ {
			if a == b {
				continue
			}
			idx := a + base*b
			_ = idx
			// JointProb expects one entry per mode.
			p := q.JointProb(a, b)
			if p > 1e-12 {
				t.Fatalf("TMSV joint P(%d,%d) = %g, want 0 (perfect correlation)", a, b, p)
			}
		}
	}
	// The marginal P(n) is geometric: P(n) = sech²(r) tanh^(2n)(r).
	dist := q.PhotonNumberDistribution(0)
	p0 := 1 / (math.Cosh(r) * math.Cosh(r))
	closeTo(t, "TMSV P(0)", dist[0], p0, 1e-9)
}

// A thermal state has a geometric photon-number distribution, mean n̄ and
// g²(0) = 2 (super-Poissonian bunching).
func TestThermalState(t *testing.T) {
	res, err := SimulateQuantum(QuantumConfig{
		Modes:  1,
		Cutoff: 20,
		State:  QuantumStateSpec{Type: "thermal", Params: map[string]any{"mean_n": []any{1.0}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, "thermal mean", res.MeanN[0], 1.0, 1e-4) // Fock truncation at cutoff 20
	closeTo(t, "thermal g2", res.G2[0], 2.0, 5e-4)
	// Geometric distribution: P(n) = (1-p) p^n, p = 1/2 for n̄=1.
	dist := res.Dist[0]
	closeTo(t, "thermal P(0)", dist[0], 0.5, 1e-9)
	closeTo(t, "thermal P(1)", dist[1], 0.25, 1e-9)
	closeTo(t, "thermal P(2)", dist[2], 0.125, 1e-9)
}

// A lossy channel (transmittance T) turns a Fock state |1> into a binomial
// mixture and attenuates a coherent state while keeping it coherent.
func TestLossChannel(t *testing.T) {
	// Fock |1> through T=0.5: P(0)=P(1)=0.5, g²(0)=0 (still antibunched).
	res, err := SimulateQuantum(QuantumConfig{
		Modes:  1,
		Cutoff: 8,
		State:  QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1}}},
		Gates:  []QuantumGateSpec{{Type: "loss", Params: map[string]any{"mode": 0, "transmittance": 0.5}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, "loss Fock mean", res.MeanN[0], 0.5, 1e-9)
	closeTo(t, "loss Fock P(0)", res.Dist[0][0], 0.5, 1e-9)
	closeTo(t, "loss Fock P(1)", res.Dist[0][1], 0.5, 1e-9)
	closeTo(t, "loss Fock g2", res.G2[0], 0.0, 1e-9)

	// Coherent |alpha=1> through T=0.5: mean = T|alpha|², still g²=1.
	res2, err := SimulateQuantum(QuantumConfig{
		Modes:  1,
		Cutoff: 20,
		State:  QuantumStateSpec{Type: "coherent", Params: map[string]any{"mode": 0, "alpha_re": 1.0}},
		Gates:  []QuantumGateSpec{{Type: "loss", Params: map[string]any{"mode": 0, "transmittance": 0.5}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	closeTo(t, "loss coherent mean", res2.MeanN[0], 0.5, 1e-9)
	closeTo(t, "loss coherent g2", res2.G2[0], 1.0, 1e-6)
}

// The loss channel must preserve the trace (the input state's total probability).
func TestLossTracePreserving(t *testing.T) {
	d, err := ThermalState(1, 15, []float64{2})
	if err != nil {
		t.Fatal(err)
	}
	traceIn := d.Norm()
	for _, T := range []float64{0.1, 0.5, 0.9, 1.0} {
		g := &DensityMatrix{Modes: d.Modes, Cutoff: d.Cutoff, Rho: append([]complex128(nil), d.Rho...)}
		if err := g.Loss(0, T); err != nil {
			t.Fatal(err)
		}
		if math.Abs(g.Norm()-traceIn) > 1e-9 {
			t.Fatalf("loss T=%g changed trace from %g to %g", T, traceIn, g.Norm())
		}
	}
}

// A single photon through a Mach-Zehnder interferometer shows interference:
// P(1,0) = (1 - cos phi)/2, P(0,1) = (1 + cos phi)/2.
func TestSinglePhotonMachZehnder(t *testing.T) {
	for _, phi := range []float64{0, math.Pi / 2, math.Pi} {
		q, err := FockState(2, 4, []int{1, 0})
		if err != nil {
			t.Fatal(err)
		}
		if err := q.BeamSplitter(0, 1, 0.5); err != nil {
			t.Fatal(err)
		}
		if err := q.PhaseShift(1, phi); err != nil {
			t.Fatal(err)
		}
		if err := q.BeamSplitter(0, 1, 0.5); err != nil {
			t.Fatal(err)
		}
		closeTo(t, fmt.Sprintf("MZ P(1,0) at phi=%.3f", phi), q.JointProb(1, 0), (1-math.Cos(phi))/2, 1e-9)
		closeTo(t, fmt.Sprintf("MZ P(0,1) at phi=%.3f", phi), q.JointProb(0, 1), (1+math.Cos(phi))/2, 1e-9)
	}
}

// The beamsplitter is unitary: applying it must preserve the state norm.
func TestBeamSplitterUnitarity(t *testing.T) {
	q, err := CoherentState(2, 15, []complex128{complex(1, 0), complex(0.5, 0)})
	if err != nil {
		t.Fatal(err)
	}
	n0 := q.Norm()
	for _, R := range []float64{0.1, 0.5, 0.9} {
		g := q.Clone()
		if err := g.BeamSplitter(0, 1, R); err != nil {
			t.Fatal(err)
		}
		if math.Abs(g.Norm()-n0) > 1e-9 {
			t.Fatalf("beamsplitter R=%g changed norm %g -> %g", R, n0, g.Norm())
		}
	}
}
