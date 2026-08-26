package optics

import (
	"math"
	"testing"
)

// The generated Schell-model realizations must reproduce the prescribed
// Gaussian degree of coherence at the source plane.
func TestSchellSourceCoherence(t *testing.T) {
	n, dx := 256, 1e-5
	w0, sigc := 1e-3, 2e-4
	fields, err := GenerateSchellRealizations(n, dx, SchellSourceParams{Width: w0, Coherence: sigc, Seed: 1}, 128)
	if err != nil {
		t.Fatal(err)
	}
	muMeasured := func(dPx int) float64 {
		var num, da, db float64
		for _, f := range fields {
			for j := 0; j < n; j++ {
				row := j * n
				for i := 0; i < n; i++ {
					i2 := i + dPx
					if i2 < 0 || i2 >= n {
						continue
					}
					a := f.Ex[row+i]
					b := f.Ex[row+i2]
					num += real(a)*real(b) + imag(a)*imag(b)
					da += real(a)*real(a) + imag(a)*imag(a)
					db += real(b)*real(b) + imag(b)*imag(b)
				}
			}
		}
		return num / math.Sqrt(da*db)
	}
	for _, dPx := range []int{1, 2, 4, 8, 16} {
		d := float64(dPx) * dx
		want := math.Exp(-d * d / (2 * sigc * sigc))
		got := muMeasured(dPx)
		if math.Abs(got-want) > 0.03 {
			t.Fatalf("degree of coherence at %d px (d=%g m): got %g want %g", dPx, d, got, want)
		}
	}
}

// The far-field intensity of a Gaussian Schell-model beam is Gaussian with
// 1/e^2 radius R = (lambda z / pi) sqrt(1/w0^2 + 1/sigma_c^2). We estimate R
// from a radial-average + log-linear fit, which is robust against speckle.
func TestSchellFarField(t *testing.T) {
	n, dx := 512, 1.25e-5
	w0, sigc := 1e-3, 3.75e-5
	wl := 632.8e-9
	z := 1.0
	I, err := PropagatePartiallyCoherent(n, dx, SchellSourceParams{Width: w0, Coherence: sigc, Seed: 2}, 160, z, MethodFraunhofer, ctxFor(wl))
	if err != nil {
		t.Fatal(err)
	}
	dxOut := wl * z / (float64(n) * dx)
	got := farFieldRadius(I, n, dxOut)
	want := (wl * z / math.Pi) * math.Sqrt(1/(w0*w0)+1/(sigc*sigc))
	if math.Abs(got-want)/want > 0.1 {
		t.Fatalf("far-field radius: got %g want %g", got, want)
	}
}

// farFieldRadius estimates the 1/e^2 intensity radius of a centered radial
// profile by fitting log(I) vs r^2 over the bins above a small floor.
func farFieldRadius(I []float64, n int, dxOut float64) float64 {
	const nBins = 128
	sum := make([]float64, nBins)
	cnt := make([]int, nBins)
	maxR := dxOut * float64(n) / 2
	dr := maxR / nBins
	for j := 0; j < n; j++ {
		y := (float64(j) - float64(n)/2) * dxOut
		for i := 0; i < n; i++ {
			x := (float64(i) - float64(n)/2) * dxOut
			r := math.Hypot(x, y)
			b := int(r / dr)
			if b < nBins {
				sum[b] += I[j*n+i]
				cnt[b]++
			}
		}
	}
	peak := 0.0
	for b := 0; b < nBins; b++ {
		if cnt[b] > 0 {
			if v := sum[b] / float64(cnt[b]); v > peak {
				peak = v
			}
		}
	}
	var sw, sx, sy, sxx, sxy float64
	for b := 0; b < nBins; b++ {
		if cnt[b] == 0 {
			continue
		}
		v := sum[b] / float64(cnt[b])
		if v < 0.02*peak {
			continue
		}
		r := (float64(b) + 0.5) * dr
		r2 := r * r
		w := float64(cnt[b])
		lv := math.Log(v)
		sw += w
		sx += w * r2
		sy += w * lv
		sxx += w * r2 * r2
		sxy += w * r2 * lv
	}
	if sw < 4 {
		return 0
	}
	denom := sw*sxx - sx*sx
	if denom == 0 {
		return 0
	}
	c := (sw*sxy - sx*sy) / denom
	if c >= 0 {
		return 0
	}
	return math.Sqrt(-2 / c)
}
