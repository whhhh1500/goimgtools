package vp8l

import (
	"math/rand/v2"
	"testing"
)

// TestInverseSubtractGreen: red and blue each get green added, modulo 256,
// green and alpha unchanged, for rows of every length up to 20 pixels.
func TestInverseSubtractGreen(t *testing.T) {
	rng := rand.New(rand.NewPCG(17, 18))
	for i := range 20000 {
		pix := make([]byte, 4*rng.IntN(21))
		for j := range pix {
			pix[j] = byte(rng.Uint32())
		}
		want := make([]byte, len(pix))
		for p := 0; p < len(pix); p += 4 {
			g := pix[p+1]
			want[p], want[p+1], want[p+2], want[p+3] = pix[p]+g, g, pix[p+2]+g, pix[p+3]
		}
		inverseSubtractGreen(pix)
		if string(pix) != string(want) {
			t.Fatalf("case %d: %v, want %v", i, pix, want)
		}
	}
}
