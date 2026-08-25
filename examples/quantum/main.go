// Command quantum demonstrates the Fock-basis quantum-optics kernel: the
// Hong-Ou-Mandel effect, single-photon Mach-Zehnder interference, coherent /
// thermal / squeezed states, the two-mode squeezed (EPR) state, and a lossy
// channel.
//
//	go run ./examples/quantum
package main

import (
	"fmt"
	"math"

	"twos/optics"
	"twos/server"
)

func main() {
	// 1. Hong-Ou-Mandel: |1,1> through a 50:50 beamsplitter.
	hom, _ := optics.FockState(2, 4, []int{1, 1})
	_ = hom.BeamSplitter(0, 1, 0.5)
	fmt.Println("== Hong-Ou-Mandel 效应（两个不可分辨光子 → 50:50 分束器）==")
	fmt.Printf("  符合计数 P(1,1) = %.2e（应≈0，光子聚束）\n", hom.JointProb(1, 1))
	fmt.Printf("  P(2,0) = %.3f   P(0,2) = %.3f（两光子总在同一端口）\n", hom.JointProb(2, 0), hom.JointProb(0, 2))

	// 2. Coherent state: Poisson statistics, g²(0)=1.
	coh, _ := optics.CoherentState(1, 20, []complex128{complex(2, 0)})
	fmt.Println("\n== 相干态 |α=2⟩ ==")
	fmt.Printf("  <n> = %.4f（理论 4）  g²(0) = %.4f（理论 1，泊松）\n", coh.MeanPhotonNumber(0), coh.G2(0))

	// 3. Squeezed vacuum: quadrature noise below shot noise (1/4).
	z := complex(0.5, 0)
	sq, _ := optics.NewQState(1, 20)
	_ = sq.Squeeze(0, z)
	_, vx := sq.QuadratureStats(0, 0)
	_, vp := sq.QuadratureStats(0, 1.5707963267948966)
	fmt.Println("\n== 压缩真空 S(r=0.5)|0⟩ ==")
	fmt.Printf("  Var(x) = %.4f（< 1/4=0.25，低于散粒噪声）  Var(p) = %.4f\n", vx, vp)

	// 4. Two-mode squeezed vacuum (EPR): perfect photon-number correlation.
	tmsv, _ := optics.TwoModeSqueezedVacuum(20, 0.5)
	nbar := math.Sinh(0.5) * math.Sinh(0.5)
	fmt.Println("\n== 双模压缩真空（EPR，r=0.5）==")
	fmt.Printf("  <n0> = %.4f  <n1> = %.4f（理论 sinh²(0.5)=%.4f）\n",
		tmsv.MeanPhotonNumber(0), tmsv.MeanPhotonNumber(1), nbar)

	// 5. Through the JSON configuration interface (same as the HTTP /api/quantum).
	res, err := optics.SimulateQuantum(optics.QuantumConfig{
		Modes:  2,
		Cutoff: 4,
		State:  optics.QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1, 1}}},
		Gates:  []optics.QuantumGateSpec{{Type: "beam_splitter", Params: map[string]any{"mode0": 0, "mode1": 1, "reflectivity": 0.5}}},
	})
	if err != nil {
		fmt.Println("SimulateQuantum error:", err)
		return
	}
	fmt.Println("\n== 通过 QuantumConfig（与 HTTP API 同协议）==")
	fmt.Printf("  P(1,1) = %.2e   P(2,0) = %.3f   P(0,2) = %.3f\n",
		res.Joint["0,1"][1*5+1], res.Joint["0,1"][2*5+0], res.Joint["0,1"][0*5+2])

	// 6. Single-photon Mach-Zehnder interferometer: P(1,0) = (1-cos phi)/2.
	fmt.Println("\n== 单光子马赫-曾德尔干涉仪 ==")
	for _, phi := range []float64{0, math.Pi / 2, math.Pi} {
		mz, _ := optics.FockState(2, 4, []int{1, 0})
		_ = mz.BeamSplitter(0, 1, 0.5)
		_ = mz.PhaseShift(1, phi)
		_ = mz.BeamSplitter(0, 1, 0.5)
		fmt.Printf("  φ=%.2f  P(1,0)=%.3f  P(0,1)=%.3f（理论 %.3f / %.3f）\n",
			phi, mz.JointProb(1, 0), mz.JointProb(0, 1), (1-math.Cos(phi))/2, (1+math.Cos(phi))/2)
	}

	// 7. Mixed states: thermal statistics and a lossy channel.
	thermal, _ := optics.SimulateQuantum(optics.QuantumConfig{
		Modes:  1,
		Cutoff: 20,
		State:  optics.QuantumStateSpec{Type: "thermal", Params: map[string]any{"mean_n": []any{1.0}}},
	})
	fmt.Println("\n== 热态（平均光子 1，混合态）==")
	fmt.Printf("  <n> = %.4f  g²(0) = %.4f（理论 2，超泊松聚束）\n", thermal.MeanN[0], thermal.G2[0])

	loss, _ := optics.SimulateQuantum(optics.QuantumConfig{
		Modes:  1,
		Cutoff: 8,
		State:  optics.QuantumStateSpec{Type: "fock", Params: map[string]any{"occupation": []any{1}}},
		Gates:  []optics.QuantumGateSpec{{Type: "loss", Params: map[string]any{"mode": 0, "transmittance": 0.5}}},
	})
	fmt.Println("\n== 单光子经损耗信道（T=0.5，混合态）==")
	fmt.Printf("  P(0)=%.3f  P(1)=%.3f（二项分布，<n>=%.3f）\n", loss.Dist[0][0], loss.Dist[0][1], loss.MeanN[0])

	// 8. Export the HOM result as a PNG chart.
	if err := server.RenderQuantumPNG("quantum_hom.png", res); err != nil {
		fmt.Println("PNG 导出失败:", err)
	} else {
		fmt.Println("\n已导出 quantum_hom.png（上方：光子数分布柱状图；下方：联合分布热图）")
	}
}
