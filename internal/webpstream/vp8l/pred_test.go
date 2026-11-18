//go:build amd64 && !purego

package vp8l

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestPredictSIMD: with the SIMD inverse predictors, predictRun changes the
// row as the Go code does, for every mode, on random rows of every width
// up to 70 and every run in them (from x0 >= 1, as inversePredictor calls
// it), the pixels smooth, noisy or alike enough for every branch of Select
// and of the clamps.
func TestPredictSIMD(t *testing.T) {
	if !predSIMD {
		t.Skip("no SIMD")
	}
	defer func() { predSIMD = true }()
	rng := rand.New(rand.NewPCG(15, 16))
	for i := range 200000 {
		w := int32(2 + rng.IntN(69))
		x0 := 1 + int32(rng.IntN(int(w-1)))
		x1 := x0 + int32(rng.IntN(int(w-x0)+1))
		mode := uint8(rng.IntN(14))
		pix := make([]byte, 8*w)
		base, spread := rng.IntN(256), []int{2, 16, 256}[rng.IntN(3)]
		for j := range pix {
			pix[j] = byte(base + rng.IntN(spread))
		}
		want, got := bytes.Clone(pix), bytes.Clone(pix)
		predSIMD = false
		predictRun(mode, want, w, x0, x1)
		predSIMD = true
		predictRun(mode, got, w, x0, x1)
		if !bytes.Equal(got, want) {
			t.Fatalf("case %d: mode %d, width %d, run %d to %d:\ngot  %v\nwant %v", i, mode, w, x0, x1, got[4*w:], want[4*w:])
		}
	}
}
