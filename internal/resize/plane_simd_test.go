package resize

import (
	"bytes"
	"math/rand/v2"
	"testing"

	"golang.org/x/image/draw"
)

// TestPlaneSIMD checks the SIMD passes of planeScaler against the Go code,
// byte for byte: planes of random content and sizes, scaled down and up by
// each kernel, with the extent sometimes less than the whole plane.
func TestPlaneSIMD(t *testing.T) {
	if !planeSIMD {
		t.Skip("no SIMD passes here")
	}
	rng := rand.New(rand.NewPCG(7, 11))
	kernels := []*draw.Kernel{draw.CatmullRom, draw.BiLinear, Lanczos3, Mitchell, Nearest, MKS2021}
	scale := func(q *draw.Kernel, plane []byte, sw, sh, dw, dh int, ew, eh float64, simd bool) []byte {
		defer func(v bool) { planeSIMD = v }(planeSIMD)
		planeSIMD = simd
		p, _ := newPlaneScaler(q, sw, sh, dw, dh, ew, eh)
		out := make([]byte, dw*dh)
		band := make([][]byte, dh)
		for i := range band {
			band[i] = out[i*dw : i*dw+dw]
		}
		p.fill(0, band)
		for y := range sh {
			p.feed(plane[y*sw : y*sw+sw])
		}
		if p.done != dh {
			t.Fatalf("%d of %d rows made", p.done, dh)
		}
		return out
	}
	used := 0
	for it := 0; it < 400; it++ {
		q := kernels[it%len(kernels)]
		sw, sh := 1+rng.IntN(300), 1+rng.IntN(60)
		dw := 1 + rng.IntN(2*sw+20)
		if it%3 != 0 {
			dw = max(1, sw*(10+rng.IntN(30))/100) // down by up to 10 times
		}
		dh := 1 + rng.IntN(2*sh)
		ew, eh := float64(sw), float64(sh)
		if it%5 == 0 {
			ew, eh = ew*(0.5+rng.Float64()/2), eh*(0.5+rng.Float64()/2)
		}
		plane := make([]byte, sw*sh)
		for i := range plane {
			switch it % 4 {
			case 0:
				plane[i] = uint8(rng.IntN(256)) // noise
			case 1:
				plane[i] = uint8(255 * (rng.IntN(2))) // the largest steps
			default:
				plane[i] = uint8((i%sw)*255/sw + rng.IntN(8)) // a gradient
			}
		}
		want := scale(q, plane, sw, sh, dw, dh, ew, eh, false)
		got := scale(q, plane, sw, sh, dw, dh, ew, eh, true)
		if !bytes.Equal(got, want) {
			t.Fatalf("%dx%d to %dx%d (extent %.1fx%.1f), kernel support %.1f: SIMD differs", sw, sh, dw, dh, ew, eh, q.Support)
		}
		p, _ := newPlaneScaler(q, sw, sh, dw, dh, ew, eh)
		if p.hGroups > 0 && p.vSIMD {
			used++
		}
	}
	if used < 100 {
		t.Errorf("both SIMD passes in only %d cases", used)
	}
}
