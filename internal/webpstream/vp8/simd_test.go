package vp8

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestFilterAVX2: the AVX2 loop filter changes the pixels around an edge
// as filter246 does, across rows and across columns, six and four pixels
// wide, for every level, interior level and variance threshold the
// decoder computes (and random ones), on smooth, noisy and flat pixels.
func TestFilterAVX2(t *testing.T) {
	if !useAVX2 {
		t.Skip("no AVX2")
	}
	rng := rand.New(rand.NewPCG(7, 8))
	const stride = 40
	for i := range 100000 {
		pix := make([]byte, 24*stride)
		base := rng.IntN(256)
		spread := []int{2, 8, 30, 256}[rng.IntN(4)]
		for j := range pix {
			v := base + rng.IntN(spread) - spread/2
			if rng.IntN(20) == 0 {
				v = rng.IntN(256)
			}
			pix[j] = byte(min(255, max(0, v)))
		}
		level, ilevel, hlevel := rng.IntN(200), 1+rng.IntN(63), rng.IntN(4)
		six := i&1 == 0
		at := 8*stride + 8
		for _, across := range []bool{false, true} {
			want, got := bytes.Clone(pix), bytes.Clone(pix)
			if across {
				// An edge between columns: 16 rows, 8 from at and 8 from at+8*stride.
				filter246(want, 16, level, ilevel, hlevel, at, stride, 1, !six)
				f := filterV4
				if six {
					f = filterV6
				}
				f(&got[at], &got[at+8*stride], stride, level, ilevel, hlevel)
			} else {
				filter246(want, 16, level, ilevel, hlevel, at, 1, stride, !six)
				f := filterH4
				if six {
					f = filterH6
				}
				f(&got[at], &got[at+8], stride, level, ilevel, hlevel)
			}
			if !bytes.Equal(got, want) {
				for j := range got {
					if got[j] != want[j] {
						t.Fatalf("case %d (six %v, across %v, levels %d %d %d): byte %d (row %d, col %d) is %d, want %d",
							i, six, across, level, ilevel, hlevel, j, j/stride, j%stride, got[j], want[j])
					}
				}
			}
		}
	}
}
