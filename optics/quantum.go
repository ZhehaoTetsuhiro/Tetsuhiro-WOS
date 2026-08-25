package optics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// QState is a multi-mode pure quantum-optical state in the truncated Fock
// basis. Occupation indices are encoded little-endian:
//
//	idx = n0 + (cutoff+1)*n1 + (cutoff+1)^2*n2 + ...
//
// so mode 0 varies fastest. Amps holds the state-vector coefficients. The
// photon number of each mode is truncated to 0..cutoff (errors are reported
// when a gate would push occupation beyond the cutoff).
type QState struct {
	Modes  int
	Cutoff int
	Amps   []complex128
}

func qpow(base, exp int) int {
	r := 1
	for i := 0; i < exp; i++ {
		r *= base
	}
	return r
}

func (q *QState) base() int { return q.Cutoff + 1 }
func (q *QState) dim() int  { return qpow(q.base(), q.Modes) }

// NewQState allocates the M-mode vacuum state |0...0> with the given per-mode
// photon-number cutoff.
func NewQState(modes, cutoff int) (*QState, error) {
	if modes < 1 || cutoff < 0 {
		return nil, fmt.Errorf("quantum state needs modes>=1 and cutoff>=0")
	}
	q := &QState{Modes: modes, Cutoff: cutoff}
	q.Amps = make([]complex128, q.dim())
	q.Amps[0] = 1
	return q, nil
}

// Clone deep-copies the state.
func (q *QState) Clone() *QState {
	return &QState{Modes: q.Modes, Cutoff: q.Cutoff, Amps: append([]complex128(nil), q.Amps...)}
}

// FockState builds the number state |n0, n1, ...>.
func FockState(modes, cutoff int, occ []int) (*QState, error) {
	q, err := NewQState(modes, cutoff)
	if err != nil {
		return nil, err
	}
	if len(occ) != modes {
		return nil, fmt.Errorf("fock state occupation list must have %d entries, got %d", modes, len(occ))
	}
	base := q.base()
	idx := 0
	stride := 1
	for m := 0; m < modes; m++ {
		if occ[m] < 0 || occ[m] > cutoff {
			return nil, fmt.Errorf("fock occupation %d out of range [0,%d]", occ[m], cutoff)
		}
		idx += occ[m] * stride
		stride *= base
	}
	for i := range q.Amps {
		q.Amps[i] = 0
	}
	q.Amps[idx] = 1
	return q, nil
}

// CoherentState builds a tensor product of coherent states |alpha_m> (one per
// mode) by displacing the vacuum.
func CoherentState(modes, cutoff int, alphas []complex128) (*QState, error) {
	q, err := NewQState(modes, cutoff)
	if err != nil {
		return nil, err
	}
	if len(alphas) != modes {
		return nil, fmt.Errorf("coherent state needs %d alpha values, got %d", modes, len(alphas))
	}
	for m := 0; m < modes; m++ {
		if err := q.Displace(m, alphas[m]); err != nil {
			return nil, err
		}
	}
	return q, nil
}

// SqueezedVacuumState builds a tensor product of squeezed vacuum states
// S(z_m)|0> (one per mode).
func SqueezedVacuumState(modes, cutoff int, zs []complex128) (*QState, error) {
	q, err := NewQState(modes, cutoff)
	if err != nil {
		return nil, err
	}
	if len(zs) != modes {
		return nil, fmt.Errorf("squeezed vacuum needs %d z values, got %d", modes, len(zs))
	}
	for m := 0; m < modes; m++ {
		if err := q.Squeeze(m, zs[m]); err != nil {
			return nil, err
		}
	}
	return q, nil
}

// TwoModeSqueezedVacuum builds the two-mode squeezed vacuum (EPR) state
//
//	sech(r) * sum_n tanh(r)^n |n, n>
//
// on modes 0 and 1. It is entangled and has perfect photon-number correlation.
func TwoModeSqueezedVacuum(cutoff int, r float64) (*QState, error) {
	q, err := NewQState(2, cutoff)
	if err != nil {
		return nil, err
	}
	base := q.base()
	ch := 1 / math.Cosh(r)
	t := math.Tanh(r)
	for n := 0; n <= cutoff; n++ {
		q.Amps[n+base*n] = complex(ch*math.Pow(t, float64(n)), 0)
	}
	return q, nil
}

func (q *QState) checkMode(m int) error {
	if m < 0 || m >= q.Modes {
		return fmt.Errorf("mode %d out of range [0,%d)", m, q.Modes)
	}
	return nil
}

// PhaseShift applies exp(i phi n) on one mode.
func (q *QState) PhaseShift(mode int, phi float64) error {
	if err := q.checkMode(mode); err != nil {
		return err
	}
	base := q.base()
	s := qpow(base, mode)
	for idx := 0; idx < len(q.Amps); idx++ {
		n := (idx / s) % base
		q.Amps[idx] *= cexpI(phi * float64(n))
	}
	return nil
}

// Displace applies the displacement operator D(alpha) = exp(alpha a† - alpha* a)
// on one mode.
func (q *QState) Displace(mode int, alpha complex128) error {
	if err := q.checkMode(mode); err != nil {
		return err
	}
	return q.applySingle(mode, displacementMatrix(q.base(), alpha))
}

// Squeeze applies the single-mode squeezing operator
// S(z) = exp(1/2 (z* a^2 - z a†^2)) on one mode, z = r e^{i theta}.
func (q *QState) Squeeze(mode int, z complex128) error {
	if err := q.checkMode(mode); err != nil {
		return err
	}
	return q.applySingle(mode, squeezeMatrix(q.base(), z))
}

// BeamSplitter applies the lossless symmetric beamsplitter
//
//	U = exp(i theta (a_m0† a_m1 + a_m0 a_m1†)),  theta = asin(sqrt(R))
//
// with reflectivity R in [0,1]. R = 0.5 is the balanced splitter
// (transmission 1/sqrt(2), reflection i/sqrt(2)), matching the classical
// symmetric beamsplitter convention used elsewhere in this kernel.
func (q *QState) BeamSplitter(m0, m1 int, reflectivity float64) error {
	if err := q.checkMode(m0); err != nil {
		return err
	}
	if err := q.checkMode(m1); err != nil {
		return err
	}
	if m0 == m1 {
		return fmt.Errorf("beam splitter needs two distinct modes")
	}
	if reflectivity < 0 || reflectivity > 1 {
		return fmt.Errorf("reflectivity must be in [0,1]")
	}
	theta := math.Asin(math.Sqrt(reflectivity))
	return q.applyTwo(m0, m1, beamSplitterMatrix(q.base(), theta))
}

// applySingle applies a (base x base) unitary to one mode.
func (q *QState) applySingle(mode int, U []complex128) error {
	base := q.base()
	if len(U) != base*base {
		return fmt.Errorf("single-mode operator size mismatch")
	}
	s := qpow(base, mode)
	out := make([]complex128, len(q.Amps))
	for idx := 0; idx < len(q.Amps); idx++ {
		amp := q.Amps[idx]
		if amp == 0 {
			continue
		}
		n := (idx / s) % base
		for np := 0; np < base; np++ {
			c := U[np*base+n]
			if c == 0 {
				continue
			}
			out[idx+(np-n)*s] += c * amp
		}
	}
	q.Amps = out
	return nil
}

// applyTwo applies a (base^2 x base^2) unitary to two modes; the local index
// is L = n_m0 + base*n_m1.
func (q *QState) applyTwo(m0, m1 int, U []complex128) error {
	base := q.base()
	b2 := base * base
	if len(U) != b2*b2 {
		return fmt.Errorf("two-mode operator size mismatch")
	}
	s0 := qpow(base, m0)
	s1 := qpow(base, m1)
	out := make([]complex128, len(q.Amps))
	for idx := 0; idx < len(q.Amps); idx++ {
		amp := q.Amps[idx]
		if amp == 0 {
			continue
		}
		a := (idx / s0) % base
		b := (idx / s1) % base
		lin := a + base*b
		for ap := 0; ap < base; ap++ {
			for bp := 0; bp < base; bp++ {
				c := U[(ap+base*bp)*b2+lin]
				if c == 0 {
					continue
				}
				out[idx+(ap-a)*s0+(bp-b)*s1] += c * amp
			}
		}
	}
	q.Amps = out
	return nil
}

// annihilate returns a|ψ> for one mode (an unnormalized state vector).
func annihilate(amps []complex128, base, mode int) []complex128 {
	s := qpow(base, mode)
	out := make([]complex128, len(amps))
	for idx := 0; idx < len(amps); idx++ {
		n := (idx / s) % base
		if n >= 1 {
			out[idx-s] += complex(math.Sqrt(float64(n)), 0) * amps[idx]
		}
	}
	return out
}

// Norm returns the 2-norm of the state vector.
func (q *QState) Norm() float64 {
	var s float64
	for _, v := range q.Amps {
		s += real(v)*real(v) + imag(v)*imag(v)
	}
	return math.Sqrt(s)
}

// Normalize rescales the state to unit norm.
func (q *QState) Normalize() error {
	n := q.Norm()
	if n == 0 {
		return fmt.Errorf("cannot normalize the zero state")
	}
	for i := range q.Amps {
		q.Amps[i] *= complex(1/n, 0)
	}
	return nil
}

// MeanPhotonNumber returns <a†a> for one mode.
func (q *QState) MeanPhotonNumber(mode int) float64 {
	base := q.base()
	s := qpow(base, mode)
	var sum float64
	for idx := 0; idx < len(q.Amps); idx++ {
		n := (idx / s) % base
		v := q.Amps[idx]
		sum += float64(n) * (real(v)*real(v) + imag(v)*imag(v))
	}
	return sum
}

// PhotonNumberDistribution returns P(n) for one mode.
func (q *QState) PhotonNumberDistribution(mode int) []float64 {
	base := q.base()
	s := qpow(base, mode)
	dist := make([]float64, base)
	for idx := 0; idx < len(q.Amps); idx++ {
		n := (idx / s) % base
		v := q.Amps[idx]
		dist[n] += real(v)*real(v) + imag(v)*imag(v)
	}
	return dist
}

// G2 returns the second-order coherence g²(0) = <a†a†aa>/<a†a>² = <n(n-1)>/<n>².
func (q *QState) G2(mode int) float64 {
	base := q.base()
	s := qpow(base, mode)
	var nbar, nn float64
	for idx := 0; idx < len(q.Amps); idx++ {
		n := (idx / s) % base
		v := q.Amps[idx]
		p := real(v)*real(v) + imag(v)*imag(v)
		nbar += float64(n) * p
		nn += float64(n*(n-1)) * p
	}
	if nbar <= 0 {
		return 0
	}
	return nn / (nbar * nbar)
}

// JointProb returns |<occ|ψ>|² for the given occupation tuple (one entry per
// mode).
func (q *QState) JointProb(occ ...int) float64 {
	if len(occ) != q.Modes {
		return 0
	}
	base := q.base()
	idx := 0
	stride := 1
	for m := 0; m < q.Modes; m++ {
		if occ[m] < 0 || occ[m] > q.Cutoff {
			return 0
		}
		idx += occ[m] * stride
		stride *= base
	}
	v := q.Amps[idx]
	return real(v)*real(v) + imag(v)*imag(v)
}

// QuadratureStats returns the mean and variance of the quadrature
//
//	x_θ = (a e^{-iθ} + a† e^{iθ}) / 2
//
// for which the vacuum has variance 1/4 (shot-noise level).
func (q *QState) QuadratureStats(mode int, theta float64) (mean, variance float64) {
	base := q.base()
	w := annihilate(q.Amps, base, mode) // a|ψ>
	w2 := annihilate(w, base, mode)     // a²|ψ>
	var sumA, sumA2 complex128
	var nbar float64
	for i := range q.Amps {
		c := complex(real(q.Amps[i]), -imag(q.Amps[i]))
		sumA += c * w[i]
		sumA2 += c * w2[i]
		nbar += real(w[i])*real(w[i]) + imag(w[i])*imag(w[i])
	}
	mean = real(cexpI(-theta) * sumA)
	x2 := real(cexpI(-2*theta)*sumA2)/2 + nbar/2 + 0.25
	return mean, x2 - mean*mean
}

// Fidelity returns |<ψ|φ>|² between two states of identical shape.
func (q *QState) Fidelity(other *QState) float64 {
	if q.Modes != other.Modes || q.Cutoff != other.Cutoff || len(q.Amps) != len(other.Amps) {
		return 0
	}
	var s complex128
	for i := range q.Amps {
		s += complex(real(q.Amps[i]), -imag(q.Amps[i])) * other.Amps[i]
	}
	return real(s)*real(s) + imag(s)*imag(s)
}

// ---- configuration / result types for the quantum simulator ----------------

// Limits for the quantum backend.
const (
	MaxQuantumModes  = 4
	MaxQuantumCutoff = 20
)

// QuantumStateSpec describes the initial quantum state.
type QuantumStateSpec struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// QuantumGateSpec describes one linear-optical gate.
type QuantumGateSpec struct {
	Type   string         `json:"type"`
	Params map[string]any `json:"params"`
}

// QuantumConfig is the full JSON configuration of a quantum run.
type QuantumConfig struct {
	Modes  int               `json:"modes"`
	Cutoff int               `json:"cutoff"`
	State  QuantumStateSpec  `json:"state"`
	Gates  []QuantumGateSpec `json:"gates"`
}

// QuadratureStat reports the mean and variance of two orthogonal quadratures.
type QuadratureStat struct {
	Mode  int     `json:"mode"`
	MeanX float64 `json:"mean_x"`
	VarX  float64 `json:"var_x"`
	MeanP float64 `json:"mean_p"`
	VarP  float64 `json:"var_p"`
}

// QuantumResult is the measurement summary of one quantum run.
type QuantumResult struct {
	Modes  int                  `json:"modes"`
	Cutoff int                  `json:"cutoff"`
	Norm   float64              `json:"norm"`
	MeanN  []float64            `json:"mean_photons"`
	G2     []float64            `json:"g2"`
	Dist   [][]float64          `json:"photon_distributions"`
	Joint  map[string][]float64 `json:"joint_distributions"`
	Quad   []QuadratureStat     `json:"quadratures"`
}

// parseOccupation accepts a Fock occupation list as a JSON array, a string of
// comma-separated ints ("1,1"), or a single number.
func parseOccupation(v any) ([]int, error) {
	switch x := v.(type) {
	case []any:
		out := make([]int, len(x))
		for i, e := range x {
			out[i] = int(asF(e))
		}
		return out, nil
	case []int:
		return x, nil
	case []float64:
		out := make([]int, len(x))
		for i, e := range x {
			out[i] = int(e)
		}
		return out, nil
	case string:
		parts := strings.Split(x, ",")
		out := make([]int, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			n, err := strconv.Atoi(p)
			if err != nil {
				return nil, fmt.Errorf("invalid occupation %q: %v", x, err)
			}
			out = append(out, n)
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("empty occupation list")
		}
		return out, nil
	case float64:
		return []int{int(x)}, nil
	case int:
		return []int{x}, nil
	default:
		return nil, fmt.Errorf("occupation must be a list of ints or a comma-separated string")
	}
}

func quantumFloat(p map[string]any, key string, def float64) float64 {
	if v, ok := p[key]; ok {
		if x, err := asFloat(v); err == nil {
			return x
		}
	}
	return def
}

func quantumInt(p map[string]any, key string, def int) int {
	if v, ok := p[key]; ok {
		if x, err := asFloat(v); err == nil {
			return int(x)
		}
	}
	return def
}

// buildQuantumState constructs the initial state from its spec.
func buildQuantumState(spec QuantumStateSpec, modes, cutoff int) (*QState, error) {
	q, err := NewQState(modes, cutoff)
	if err != nil {
		return nil, err
	}
	p := spec.Params
	if p == nil {
		p = map[string]any{}
	}
	switch spec.Type {
	case "vacuum", "":
		return q, nil
	case "fock":
		raw, ok := p["occupation"]
		if !ok {
			return nil, fmt.Errorf("fock state needs an occupation list")
		}
		occ, err := parseOccupation(raw)
		if err != nil {
			return nil, err
		}
		return FockState(modes, cutoff, occ)
	case "coherent":
		mode := quantumInt(p, "mode", 0)
		alpha := complex(quantumFloat(p, "alpha_re", 1), quantumFloat(p, "alpha_im", 0))
		if err := q.Displace(mode, alpha); err != nil {
			return nil, err
		}
		return q, nil
	case "squeezed_vacuum":
		mode := quantumInt(p, "mode", 0)
		r := quantumFloat(p, "r", 0.5)
		phase := quantumFloat(p, "phase", 0)
		z := complex(r, 0) * cexpI(phase)
		if err := q.Squeeze(mode, z); err != nil {
			return nil, err
		}
		return q, nil
	case "two_mode_squeezed":
		r := quantumFloat(p, "r", 0.5)
		return TwoModeSqueezedVacuum(cutoff, r)
	default:
		return nil, fmt.Errorf("unknown quantum state type %q", spec.Type)
	}
}

// applyQuantumGates applies a gate sequence to the state.
func applyQuantumGates(q *QState, gates []QuantumGateSpec) error {
	for i, g := range gates {
		p := g.Params
		if p == nil {
			p = map[string]any{}
		}
		var err error
		switch g.Type {
		case "phase_shift":
			err = q.PhaseShift(quantumInt(p, "mode", 0), quantumFloat(p, "phase", 0))
		case "beam_splitter":
			err = q.BeamSplitter(quantumInt(p, "mode0", 0), quantumInt(p, "mode1", 1), quantumFloat(p, "reflectivity", 0.5))
		case "displacement":
			err = q.Displace(quantumInt(p, "mode", 0), complex(quantumFloat(p, "alpha_re", 1), quantumFloat(p, "alpha_im", 0)))
		case "squeeze":
			z := complex(quantumFloat(p, "r", 0.5), 0) * cexpI(quantumFloat(p, "phase", 0))
			err = q.Squeeze(quantumInt(p, "mode", 0), z)
		default:
			return fmt.Errorf("gate %d: unknown quantum gate type %q", i, g.Type)
		}
		if err != nil {
			return fmt.Errorf("gate %d (%s): %v", i, g.Type, err)
		}
	}
	return nil
}

// MeasureQuantum computes the standard observables of a state.
func MeasureQuantum(q *QState) QuantumResult {
	res := QuantumResult{
		Modes:  q.Modes,
		Cutoff: q.Cutoff,
		Norm:   q.Norm(),
		MeanN:  make([]float64, q.Modes),
		G2:     make([]float64, q.Modes),
		Dist:   make([][]float64, q.Modes),
		Joint:  map[string][]float64{},
		Quad:   make([]QuadratureStat, q.Modes),
	}
	base := q.base()
	for m := 0; m < q.Modes; m++ {
		res.MeanN[m] = q.MeanPhotonNumber(m)
		res.G2[m] = q.G2(m)
		res.Dist[m] = q.PhotonNumberDistribution(m)
		mx, vx := q.QuadratureStats(m, 0)
		mp, vp := q.QuadratureStats(m, math.Pi/2)
		res.Quad[m] = QuadratureStat{Mode: m, MeanX: mx, VarX: vx, MeanP: mp, VarP: vp}
	}
	for m0 := 0; m0 < q.Modes; m0++ {
		for m1 := m0 + 1; m1 < q.Modes; m1++ {
			s0 := qpow(base, m0)
			s1 := qpow(base, m1)
			flat := make([]float64, base*base)
			for idx := 0; idx < len(q.Amps); idx++ {
				v := q.Amps[idx]
				p := real(v)*real(v) + imag(v)*imag(v)
				if p == 0 {
					continue
				}
				a := (idx / s0) % base
				b := (idx / s1) % base
				flat[a*base+b] += p
			}
			res.Joint[fmt.Sprintf("%d,%d", m0, m1)] = flat
		}
	}
	return res
}

// SimulateQuantum validates and runs a quantum-optical simulation. Runs that
// need mixed states (a thermal initial state or a lossy gate) use the density
// matrix backend; otherwise the faster pure-state (state vector) backend.
func SimulateQuantum(cfg QuantumConfig) (*QuantumResult, error) {
	if cfg.Modes < 1 || cfg.Modes > MaxQuantumModes {
		return nil, fmt.Errorf("quantum modes must be between 1 and %d", MaxQuantumModes)
	}
	if cfg.Cutoff < 1 || cfg.Cutoff > MaxQuantumCutoff {
		return nil, fmt.Errorf("quantum cutoff must be between 1 and %d", MaxQuantumCutoff)
	}
	mixed := cfg.State.Type == "thermal"
	for _, g := range cfg.Gates {
		if g.Type == "loss" {
			mixed = true
			break
		}
	}
	if mixed {
		d, err := buildDensityState(cfg.State, cfg.Modes, cfg.Cutoff)
		if err != nil {
			return nil, err
		}
		if err := applyDensityGates(d, cfg.Gates); err != nil {
			return nil, err
		}
		res := MeasureDensity(d)
		return &res, nil
	}
	q, err := buildQuantumState(cfg.State, cfg.Modes, cfg.Cutoff)
	if err != nil {
		return nil, err
	}
	if err := applyQuantumGates(q, cfg.Gates); err != nil {
		return nil, err
	}
	res := MeasureQuantum(q)
	return &res, nil
}
