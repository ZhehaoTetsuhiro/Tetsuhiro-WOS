package optics

import (
	"math"
	"testing"
)

func sumF(v []float64) float64 {
	var s float64
	for _, x := range v {
		s += x
	}
	return s
}

// qIndex is the little-endian index of an occupation tuple into JointFull.
func qIndex(base int, occ ...int) int {
	idx, stride := 0, 1
	for _, n := range occ {
		idx += n * stride
		stride *= base
	}
	return idx
}

// TestQuantumFullJointThreeModes checks the 3-mode joint distribution is
// available and correctly indexed: a Hong-Ou-Mandel pair split on a balanced
// beamsplitter must put 1/2 on |2,0,0> and 1/2 on |0,2,0> with mode 2 empty,
// which the old pairwise-only output could not express.
func TestQuantumFullJointThreeModes(t *testing.T) {
	cfg := QuantumConfig{
		Modes: 3, Cutoff: 2,
		State: QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1, 1, 0}}},
		Gates: []QuantumGateSpec{{Type: "beam_splitter", Params: map[string]any{"mode0": 0, "mode1": 1, "reflectivity": 0.5}}},
	}
	res, err := SimulateQuantum(cfg)
	if err != nil {
		t.Fatalf("SimulateQuantum: %v", err)
	}
	base := cfg.Cutoff + 1
	if len(res.JointFull) != base*base*base {
		t.Fatalf("joint_full length %d, want %d", len(res.JointFull), base*base*base)
	}
	if s := sumF(res.JointFull); math.Abs(s-1) > 1e-9 {
		t.Errorf("joint_full sums to %.9g, want 1 (norm %.9g)", s, res.Norm)
	}
	if p := res.JointFull[qIndex(base, 2, 0, 0)]; math.Abs(p-0.5) > 1e-9 {
		t.Errorf("P(2,0,0)=%g, want 0.5", p)
	}
	if p := res.JointFull[qIndex(base, 0, 2, 0)]; math.Abs(p-0.5) > 1e-9 {
		t.Errorf("P(0,2,0)=%g, want 0.5", p)
	}
	if p := res.JointFull[qIndex(base, 1, 1, 0)] + res.JointFull[qIndex(base, 0, 0, 2)]; p > 1e-12 {
		t.Errorf("HOM bunching leaked into other outcomes: %g", p)
	}
	// The pairwise joint distribution must be the marginal of joint_full.
	marg := 0.0
	for n0 := 0; n0 <= cfg.Cutoff; n0++ {
		for n2 := 0; n2 <= cfg.Cutoff; n2++ {
			marg += res.JointFull[qIndex(base, n0, 2, n2)]
		}
	}
	if pair := res.Joint["0,1"][2*base+0]; math.Abs(pair-marg) > 1e-9 {
		t.Errorf("pairwise P(2,0)=%g disagrees with the full-distribution marginal %g", pair, marg)
	}
}

// TestQuantumModeCapByDimension pins the new limits: 6 modes at cutoff 1 (a
// KLM-sized circuit) is accepted, while a combination whose state space is over
// the dimension budget is rejected with a human-readable message.
func TestQuantumModeCapByDimension(t *testing.T) {
	six := QuantumConfig{
		Modes: 6, Cutoff: 1,
		State: QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1, 1, 1, 1, 1, 1}}},
		Gates: []QuantumGateSpec{
			{Type: "beam_splitter", Params: map[string]any{"mode0": 0, "mode1": 1, "reflectivity": 0.5}},
			{Type: "beam_splitter", Params: map[string]any{"mode0": 2, "mode1": 3, "reflectivity": 0.5}},
			{Type: "beam_splitter", Params: map[string]any{"mode0": 4, "mode1": 5, "reflectivity": 0.5}},
		},
	}
	res, err := SimulateQuantum(six)
	if err != nil {
		t.Fatalf("6 modes at cutoff 1 must run now (KLM needs 4-6 modes): %v", err)
	}
	if len(res.JointFull) != 1<<6 {
		t.Errorf("6-mode joint_full length %d, want %d", len(res.JointFull), 1<<6)
	}
	if s := sumF(res.JointFull); math.Abs(s-1) > 1e-9 {
		t.Errorf("6-mode joint_full sums to %.9g, want 1", s)
	}
	tooBig := six
	tooBig.Modes, tooBig.Cutoff = 8, 8 // 9^8 = 43M amplitudes, over the budget
	tooBig.State = QuantumStateSpec{Type: "vacuum"}
	tooBig.Gates = nil
	if _, err := SimulateQuantum(tooBig); err == nil {
		t.Errorf("8 modes at cutoff 8 (9^8 amplitudes) must be rejected by the dimension budget")
	} else {
		t.Logf("rejected as expected: %v", err)
	}
}

// TestQuantumPostselectHOM post-selects the Hong-Ou-Mandel state on one output
// being empty; the surviving branch is |2> with probability 1/2, and the
// post-selected state must be renormalized (mean photon = 2).
func TestQuantumPostselectHOM(t *testing.T) {
	cfg := QuantumConfig{
		Modes: 2, Cutoff: 4,
		State:      QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1, 1}}},
		Gates:      []QuantumGateSpec{{Type: "beam_splitter", Params: map[string]any{"mode0": 0, "mode1": 1, "reflectivity": 0.5}}},
		Postselect: &PostselectSpec{Modes: []int{1}, Counts: []int{0}},
	}
	res, err := SimulateQuantum(cfg)
	if err != nil {
		t.Fatalf("SimulateQuantum: %v", err)
	}
	if math.Abs(res.PostselectProb-0.5) > 1e-9 {
		t.Errorf("post-selection probability %g, want 0.5", res.PostselectProb)
	}
	if math.Abs(res.MeanN[0]-2) > 1e-9 {
		t.Errorf("post-selected mean photon in mode 0 = %g, want 2", res.MeanN[0])
	}
	if math.Abs(res.MeanN[1]) > 1e-9 {
		t.Errorf("post-selected mode 1 must be empty, got <n>=%g", res.MeanN[1])
	}
	if s := sumF(res.JointFull); math.Abs(s-1) > 1e-9 {
		t.Errorf("post-selected joint_full sums to %.9g, want 1 (renormalized)", s)
	}
}

// TestQuantumPostselectDensity does the same on the density-matrix backend: a
// single photon through a 50% loss channel, post-selected on detecting it.
func TestQuantumPostselectDensity(t *testing.T) {
	cfg := QuantumConfig{
		Modes: 1, Cutoff: 3,
		State:      QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1}}},
		Gates:      []QuantumGateSpec{{Type: "loss", Params: map[string]any{"mode": 0, "transmittance": 0.5}}},
		Postselect: &PostselectSpec{Modes: []int{0}, Counts: []int{1}},
	}
	res, err := SimulateQuantum(cfg)
	if err != nil {
		t.Fatalf("SimulateQuantum: %v", err)
	}
	if math.Abs(res.PostselectProb-0.5) > 1e-9 {
		t.Errorf("lossy photon detection probability %g, want 0.5", res.PostselectProb)
	}
	if math.Abs(res.MeanN[0]-1) > 1e-9 {
		t.Errorf("post-selected <n>=%g, want 1", res.MeanN[0])
	}
}

// TestQuantumPostselectImpossible checks a condition that never occurs: the
// probability is 0 and nothing panics.
func TestQuantumPostselectImpossible(t *testing.T) {
	cfg := QuantumConfig{
		Modes: 2, Cutoff: 2,
		State:      QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1, 0}}},
		Postselect: &PostselectSpec{Modes: []int{1}, Counts: []int{2}},
	}
	res, err := SimulateQuantum(cfg)
	if err != nil {
		t.Fatalf("SimulateQuantum: %v", err)
	}
	if res.PostselectProb != 0 {
		t.Errorf("an impossible post-selection must report probability 0, got %g", res.PostselectProb)
	}
}
