package optics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
)

// DensityMatrix is a multi-mode quantum state in the truncated Fock basis,
// represented by its density operator rho (Dim x Dim, row-major). It handles
// mixed states (thermal, lossy channels) that a pure state vector cannot.
type DensityMatrix struct {
	Modes  int
	Cutoff int
	Rho    []complex128 // Dim x Dim
}

func (d *DensityMatrix) base() int { return d.Cutoff + 1 }
func (d *DensityMatrix) dim() int  { return qpow(d.base(), d.Modes) }

// NewDensityMatrix allocates the M-mode vacuum density operator |0...0><0...0|.
func NewDensityMatrix(modes, cutoff int) (*DensityMatrix, error) {
	if modes < 1 || cutoff < 0 {
		return nil, fmt.Errorf("density matrix needs modes>=1 and cutoff>=0")
	}
	d := &DensityMatrix{Modes: modes, Cutoff: cutoff}
	d.Rho = make([]complex128, d.dim()*d.dim())
	d.Rho[0] = 1
	return d, nil
}

// DensityFromPure builds rho = |psi><psi| from a pure state.
func DensityFromPure(q *QState) *DensityMatrix {
	dim := q.dim()
	d := &DensityMatrix{Modes: q.Modes, Cutoff: q.Cutoff, Rho: make([]complex128, dim*dim)}
	for i := 0; i < dim; i++ {
		ci := q.Amps[i]
		if ci == 0 {
			continue
		}
		row := i * dim
		for j := 0; j < dim; j++ {
			cj := q.Amps[j]
			if cj == 0 {
				continue
			}
			d.Rho[row+j] = ci * complex(real(cj), -imag(cj))
		}
	}
	return d
}

// ThermalState builds a tensor product of single-mode thermal states with the
// given mean photon numbers (geometric photon-number distribution).
func ThermalState(modes, cutoff int, meanNs []float64) (*DensityMatrix, error) {
	d, err := NewDensityMatrix(modes, cutoff)
	if err != nil {
		return nil, err
	}
	if len(meanNs) != modes {
		return nil, fmt.Errorf("thermal state needs %d mean photon numbers, got %d", modes, len(meanNs))
	}
	base := d.base()
	dim := d.dim()
	for idx := 0; idx < dim; idx++ {
		p := 1.0
		for m := 0; m < modes; m++ {
			n := (idx / qpow(base, m)) % base
			nbar := meanNs[m]
			var pn float64
			if nbar <= 0 {
				if n == 0 {
					pn = 1
				}
			} else {
				pp := nbar / (1 + nbar)
				pn = (1 - pp) * math.Pow(pp, float64(n))
			}
			p *= pn
		}
		d.Rho[idx*dim+idx] = complex(p, 0)
	}
	return d, nil
}

// applyLocalBoth applies rho -> left * rho * right, where left and right are
// local operators (size base^k x base^k) acting on the given modes. This is
// implemented blockwise: for every spectator configuration it extracts the
// L x L sub-block, multiplies by left/right, and scatters back.
func (d *DensityMatrix) applyLocalBoth(modes []int, left, right []complex128) {
	base := d.base()
	dim := d.dim()
	k := len(modes)
	L := qpow(base, k)
	strides := make([]int, k)
	for i, m := range modes {
		strides[i] = qpow(base, m)
	}
	isMode := make([]bool, d.Modes)
	for _, m := range modes {
		isMode[m] = true
	}
	localOf := make([]int, dim)
	specOf := make([]int, dim)
	for idx := 0; idx < dim; idx++ {
		local := 0
		for i := range modes {
			n := (idx / strides[i]) % base
			local += n * qpow(base, i)
		}
		spec := 0
		sp := 1
		for m := 0; m < d.Modes; m++ {
			if !isMode[m] {
				n := (idx / qpow(base, m)) % base
				spec += n * sp
				sp *= base
			}
		}
		localOf[idx] = local
		specOf[idx] = spec
	}
	rev := make([]int, dim)
	for idx := 0; idx < dim; idx++ {
		rev[specOf[idx]*L+localOf[idx]] = idx
	}
	nSpec := dim / L
	out := make([]complex128, dim*dim)
	block := make([]complex128, L*L)
	for s1 := 0; s1 < nSpec; s1++ {
		for s2 := 0; s2 < nSpec; s2++ {
			for a := 0; a < L; a++ {
				i1 := rev[s1*L+a]
				row := i1 * dim
				for b := 0; b < L; b++ {
					block[a*L+b] = d.Rho[row+rev[s2*L+b]]
				}
			}
			block = qmatMul(left, block)
			block = qmatMul(block, right)
			for a := 0; a < L; a++ {
				i1 := rev[s1*L+a]
				row := i1 * dim
				for b := 0; b < L; b++ {
					out[row+rev[s2*L+b]] = block[a*L+b]
				}
			}
		}
	}
	d.Rho = out
}

func (d *DensityMatrix) checkMode(m int) error {
	if m < 0 || m >= d.Modes {
		return fmt.Errorf("mode %d out of range [0,%d)", m, d.Modes)
	}
	return nil
}

// PhaseShift applies exp(i phi n) on one mode (rho -> U rho U†).
func (d *DensityMatrix) PhaseShift(mode int, phi float64) error {
	if err := d.checkMode(mode); err != nil {
		return err
	}
	base := d.base()
	U := make([]complex128, base*base)
	for n := 0; n < base; n++ {
		U[n*base+n] = cexpI(phi * float64(n))
	}
	d.applyLocalBoth([]int{mode}, U, qmatDagger(U))
	return nil
}

// Displace applies D(alpha) on one mode.
func (d *DensityMatrix) Displace(mode int, alpha complex128) error {
	if err := d.checkMode(mode); err != nil {
		return err
	}
	U := displacementMatrix(d.base(), alpha)
	d.applyLocalBoth([]int{mode}, U, qmatDagger(U))
	return nil
}

// Squeeze applies S(z) on one mode.
func (d *DensityMatrix) Squeeze(mode int, z complex128) error {
	if err := d.checkMode(mode); err != nil {
		return err
	}
	U := squeezeMatrix(d.base(), z)
	d.applyLocalBoth([]int{mode}, U, qmatDagger(U))
	return nil
}

// BeamSplitter applies the symmetric beamsplitter between two modes.
func (d *DensityMatrix) BeamSplitter(m0, m1 int, reflectivity float64) error {
	if err := d.checkMode(m0); err != nil {
		return err
	}
	if err := d.checkMode(m1); err != nil {
		return err
	}
	if m0 == m1 {
		return fmt.Errorf("beam splitter needs two distinct modes")
	}
	if reflectivity < 0 || reflectivity > 1 {
		return fmt.Errorf("reflectivity must be in [0,1]")
	}
	theta := math.Asin(math.Sqrt(reflectivity))
	U := beamSplitterMatrix(d.base(), theta)
	d.applyLocalBoth([]int{m0, m1}, U, qmatDagger(U))
	return nil
}

// Loss applies a lossy channel with transmittance T in [0,1] on one mode via
// the amplitude-damping Kraus operators
// E_l = sqrt((1-T)^l/l!) T^{(n-l)/2} a^l, rho -> sum_l E_l rho E_l†.
func (d *DensityMatrix) Loss(mode int, T float64) error {
	if err := d.checkMode(mode); err != nil {
		return err
	}
	if T < 0 || T > 1 {
		return fmt.Errorf("transmittance must be in [0,1]")
	}
	base := d.base()
	dim := d.dim()
	orig := d.Rho
	acc := make([]complex128, dim*dim)
	E := make([]complex128, base*base)
	for l := 0; l < base; l++ {
		for i := range E {
			E[i] = 0
		}
		facL := factorial(l)
		scale := math.Sqrt(math.Pow(1-T, float64(l)) / facL)
		for n := l; n < base; n++ {
			// E_l[n-l, n] = sqrt((1-T)^l/l!) * T^((n-l)/2) * sqrt(n!/(n-l)!)
			fall := fallingFactorial(n, l)
			coef := scale * math.Pow(T, float64(n-l)/2) * math.Sqrt(fall)
			E[(n-l)*base+n] = complex(coef, 0)
		}
		d.Rho = append([]complex128(nil), orig...)
		d.applyLocalBoth([]int{mode}, E, qmatDagger(E))
		for i := range acc {
			acc[i] += d.Rho[i]
		}
	}
	d.Rho = acc
	return nil
}

// fallingFactorial returns n!/(n-l)! = n*(n-1)*...*(n-l+1).
func fallingFactorial(n, l int) float64 {
	if l < 0 || l > n {
		return 0
	}
	p := 1.0
	for k := 0; k < l; k++ {
		p *= float64(n - k)
	}
	return p
}

// Norm returns the trace of rho (1 for a valid state).
func (d *DensityMatrix) Norm() float64 {
	dim := d.dim()
	var s float64
	for i := 0; i < dim; i++ {
		s += real(d.Rho[i*dim+i])
	}
	return s
}

// Normalize rescales rho to unit trace.
func (d *DensityMatrix) Normalize() error {
	n := d.Norm()
	if n == 0 {
		return fmt.Errorf("cannot normalize the zero density matrix")
	}
	for i := range d.Rho {
		d.Rho[i] *= complex(1/n, 0)
	}
	return nil
}

// MeanPhotonNumber returns Tr(rho a†a) for one mode.
func (d *DensityMatrix) MeanPhotonNumber(mode int) float64 {
	base := d.base()
	dim := d.dim()
	s := qpow(base, mode)
	var sum float64
	for idx := 0; idx < dim; idx++ {
		n := (idx / s) % base
		sum += float64(n) * real(d.Rho[idx*dim+idx])
	}
	return sum
}

// PhotonNumberDistribution returns P(n) for one mode (diagonal of rho).
func (d *DensityMatrix) PhotonNumberDistribution(mode int) []float64 {
	base := d.base()
	dim := d.dim()
	s := qpow(base, mode)
	dist := make([]float64, base)
	for idx := 0; idx < dim; idx++ {
		n := (idx / s) % base
		dist[n] += real(d.Rho[idx*dim+idx])
	}
	return dist
}

// G2 returns g²(0) = Tr(rho a†a†aa) / Tr(rho a†a)².
func (d *DensityMatrix) G2(mode int) float64 {
	base := d.base()
	dim := d.dim()
	s := qpow(base, mode)
	var nbar, nn float64
	for idx := 0; idx < dim; idx++ {
		n := (idx / s) % base
		p := real(d.Rho[idx*dim+idx])
		nbar += float64(n) * p
		nn += float64(n*(n-1)) * p
	}
	if nbar <= 0 {
		return 0
	}
	return nn / (nbar * nbar)
}

// QuadratureStats returns the mean and variance of x_theta = (a e^{-i theta} +
// a† e^{i theta})/2, computed from Tr(rho a), Tr(rho a²) and Tr(rho a†a).
func (d *DensityMatrix) QuadratureStats(mode int, theta float64) (mean, variance float64) {
	base := d.base()
	dim := d.dim()
	s := qpow(base, mode)
	var sumA, sumA2 complex128
	var nbar float64
	for i := 0; i < dim; i++ {
		n := (i / s) % base
		nbar += float64(n) * real(d.Rho[i*dim+i])
		if n >= 1 {
			sumA += complex(math.Sqrt(float64(n)), 0) * d.Rho[(i-s)*dim+i]
		}
		if n >= 2 {
			sumA2 += complex(math.Sqrt(float64(n*(n-1))), 0) * d.Rho[(i-2*s)*dim+i]
		}
	}
	mean = real(cexpI(-theta) * sumA)
	x2 := real(cexpI(-2*theta)*sumA2)/2 + nbar/2 + 0.25
	return mean, x2 - mean*mean
}

// ---- density-matrix configuration flow --------------------------------------

// buildDensityState constructs the initial density operator from its spec.
func buildDensityState(spec QuantumStateSpec, modes, cutoff int) (*DensityMatrix, error) {
	p := spec.Params
	if p == nil {
		p = map[string]any{}
	}
	switch spec.Type {
	case "thermal":
		raw, ok := p["mean_n"]
		if !ok {
			return nil, fmt.Errorf("thermal state needs a mean_n list")
		}
		meanNs, err := parseMeanList(raw, modes)
		if err != nil {
			return nil, err
		}
		return ThermalState(modes, cutoff, meanNs)
	case "vacuum", "":
		return NewDensityMatrix(modes, cutoff)
	default:
		// Pure states: build the vector, then form rho = |psi><psi|.
		q, err := buildQuantumState(spec, modes, cutoff)
		if err != nil {
			return nil, err
		}
		return DensityFromPure(q), nil
	}
}

// parseMeanList accepts a JSON array or a comma-separated string of mean photon
// numbers, one per mode.
func parseMeanList(v any, modes int) ([]float64, error) {
	var out []float64
	switch x := v.(type) {
	case []any:
		for _, e := range x {
			out = append(out, asF(e))
		}
	case []float64:
		out = x
	case string:
		for _, s := range strings.Split(x, ",") {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			f, err := strconv.ParseFloat(s, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid mean_n %q: %v", x, err)
			}
			out = append(out, f)
		}
	default:
		// single number applies to mode 0
		out = append(out, asF(v))
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("empty mean_n list")
	}
	if len(out) > modes {
		out = out[:modes]
	}
	for len(out) < modes {
		out = append(out, 0)
	}
	return out, nil
}

// applyDensityGates applies a gate sequence to a density operator.
func applyDensityGates(d *DensityMatrix, gates []QuantumGateSpec) error {
	for i, g := range gates {
		p := g.Params
		if p == nil {
			p = map[string]any{}
		}
		var err error
		switch g.Type {
		case "phase_shift":
			err = d.PhaseShift(quantumInt(p, "mode", 0), quantumFloat(p, "phase", 0))
		case "beam_splitter":
			err = d.BeamSplitter(quantumInt(p, "mode0", 0), quantumInt(p, "mode1", 1), quantumFloat(p, "reflectivity", 0.5))
		case "displacement":
			err = d.Displace(quantumInt(p, "mode", 0), complex(quantumFloat(p, "alpha_re", 1), quantumFloat(p, "alpha_im", 0)))
		case "squeeze":
			z := complex(quantumFloat(p, "r", 0.5), 0) * cexpI(quantumFloat(p, "phase", 0))
			err = d.Squeeze(quantumInt(p, "mode", 0), z)
		case "loss":
			err = d.Loss(quantumInt(p, "mode", 0), quantumFloat(p, "transmittance", 0.5))
		default:
			return fmt.Errorf("gate %d: unknown quantum gate type %q", i, g.Type)
		}
		if err != nil {
			return fmt.Errorf("gate %d (%s): %v", i, g.Type, err)
		}
	}
	return nil
}

// MeasureDensity computes the standard observables of a density operator.
func MeasureDensity(d *DensityMatrix) QuantumResult {
	res := QuantumResult{
		Modes:  d.Modes,
		Cutoff: d.Cutoff,
		Norm:   d.Norm(),
		MeanN:  make([]float64, d.Modes),
		G2:     make([]float64, d.Modes),
		Dist:   make([][]float64, d.Modes),
		Joint:  map[string][]float64{},
		Quad:   make([]QuadratureStat, d.Modes),
	}
	base := d.base()
	dim := d.dim()
	for m := 0; m < d.Modes; m++ {
		res.MeanN[m] = d.MeanPhotonNumber(m)
		res.G2[m] = d.G2(m)
		res.Dist[m] = d.PhotonNumberDistribution(m)
		mx, vx := d.QuadratureStats(m, 0)
		mp, vp := d.QuadratureStats(m, math.Pi/2)
		res.Quad[m] = QuadratureStat{Mode: m, MeanX: mx, VarX: vx, MeanP: mp, VarP: vp}
	}
	for m0 := 0; m0 < d.Modes; m0++ {
		for m1 := m0 + 1; m1 < d.Modes; m1++ {
			s0 := qpow(base, m0)
			s1 := qpow(base, m1)
			flat := make([]float64, base*base)
			for idx := 0; idx < dim; idx++ {
				p := real(d.Rho[idx*dim+idx])
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
