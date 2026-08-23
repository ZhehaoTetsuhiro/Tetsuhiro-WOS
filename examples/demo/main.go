// Command demo is the minimal kernel usage example: a lens focusing a plane
// wave, with metrics printed to stdout and PNG images exported.
//
//	go run ./examples/demo
package main

import (
	"fmt"
	"log"

	"wos/optics"
	"wos/server"
)

func main() {
	cfg := optics.Config{
		Grid:       optics.GridSpec{Size: 1024, Width: 0.01},
		Wavelength: 632.8e-9,
		Method:     "asm",
		Evanescent: "decay",
		Bandlimit:  &optics.BandlimitOpts{Fraction: 0.9, Sigma: 0.05},
		Source: optics.SourceSpec{
			Type:   "plane",
			Params: map[string]any{"power": 1e-3},
		},
		Elements: []optics.ElementSpec{
			{Type: "lens", Params: map[string]any{"f": 0.5, "aperture": 0.0025}},
			{Type: "propagate", Params: map[string]any{"distance": 0.5}},
			{Type: "sensor", Params: map[string]any{"label": "focus",
				"strehl_aperture": 0.0025, "strehl_distance": 0.5}},
		},
	}
	res, err := optics.Simulate(cfg)
	if err != nil {
		log.Fatalf("simulate: %v", err)
	}
	fmt.Printf("运行 %s  耗时 %.1f ms  网格 %d^2 (dx=%.3g m)  λ=%.1f nm\n",
		res.RunID, res.ElapsedMS, res.Size, res.DX, res.Wavelength*1e9)
	for _, w := range res.Warnings {
		fmt.Printf("  警告: [%s] %s\n", w.Code, w.Message)
	}
	for _, p := range res.Planes {
		st := p.Stats
		fmt.Printf("平面 %-10s %-8s 功率=%.4g W  峰值=%.4g W/m²\n",
			p.ID, p.Label, st.Power, st.Peak)
		fmt.Printf("        质心=(%.3g, %.3g) m  RMS=(%.3g, %.3g) m  Strehl=%.3f\n",
			st.CentroidX, st.CentroidY, st.RMSX, st.RMSY, st.Strehl)
		// 理论值：P*pi*R^2/(lambda^2*f^2)，用于对比。
		ideal := st.Power * 3.141592653589793 * 0.0025 * 0.0025 /
			(cfg.Wavelength * cfg.Wavelength * 0.5 * 0.5)
		fmt.Printf("        理想衍射极限峰值=%.4g W/m²\n", ideal)
		if err := server.RenderPlanePNG("demo_intensity.png", p, "total", "log", "inferno"); err != nil {
			log.Printf("png: %v", err)
		}
		_ = server.RenderPlanePNG("demo_phase.png", p, "phase_x", "lin", "phase")
	}
	fmt.Println("已输出 demo_intensity.png / demo_phase.png")
}
