// Command interferometer demonstrates a Mach-Zehnder with both output ports:
// it sweeps the arm length difference and prints the complementary fringe
// powers (which must always sum to the source power).
//
//	go run ./examples/interferometer
package main

import (
	"fmt"
	"log"

	"twos/optics"
)

func mz(armExtra float64) *optics.Result {
	cfg := optics.Config{
		Grid:       optics.GridSpec{Size: 512, Width: 0.01},
		Wavelength: 632.8e-9,
		Method:     "asm",
		Evanescent: "decay",
		Bandlimit:  &optics.BandlimitOpts{Fraction: 0.9, Sigma: 0.05},
		Source: optics.SourceSpec{
			Type:   "gaussian",
			Params: map[string]any{"waist": 2e-3, "power": 1e-3},
		},
		Elements: []optics.ElementSpec{
			{Type: "propagate", Params: map[string]any{"distance": 0.02}},
			{Type: "beamsplitter", Params: map[string]any{"reflectivity": 0.5, "reflected_arm": map[string]any{
				"elements": []any{
					map[string]any{"type": "propagate", "params": map[string]any{"distance": 0.04 + armExtra}},
				},
			}}},
			{Type: "propagate", Params: map[string]any{"distance": 0.04}},
			{Type: "combiner", Params: map[string]any{"outputs": []any{
				map[string]any{"label": "p1", "weights": []any{
					map[string]any{"arm": "main", "re": 0.70710678, "im": 0},
					map[string]any{"arm": "bs0", "re": 0, "im": 0.70710678}}},
				map[string]any{"label": "p2", "weights": []any{
					map[string]any{"arm": "main", "re": 0, "im": 0.70710678},
					map[string]any{"arm": "bs0", "re": 0.70710678, "im": 0}}},
			}}},
		},
	}
	res, err := optics.Simulate(cfg)
	if err != nil {
		log.Fatal(err)
	}
	return res
}

func main() {
	wl := 632.8e-9
	fmt.Println("马赫-曾德尔干涉仪：两输出端口功率 vs 臂长差（理论 P1=P·sin²(Δ/2), P2=P·cos²(Δ/2)）")
	fmt.Printf("%-12s %-12s %-12s %-10s\n", "臂长差/λ", "P1 (mW)", "P2 (mW)", "P1+P2 (mW)")
	for _, frac := range []float64{0, 0.25, 0.5, 0.75, 1.0} {
		res := mz(frac * wl)
		p1 := res.Planes[0].Stats.Power * 1e3
		p2 := res.Planes[1].Stats.Power * 1e3
		fmt.Printf("%-12.2f %-12.4f %-12.4f %-10.4f\n", frac, p1, p2, p1+p2)
	}
}
