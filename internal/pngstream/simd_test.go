package pngstream

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestFilterSIMD: rows of every width up to 300 bytes and every pixel size,
// smooth, noisy and flat, get the filter and the filtered row filterGo
// gives.
func TestFilterSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(21, 22))
	for _, bpp := range []int{1, 2, 3, 4, 6, 8} {
		for n := bpp; n <= 300; n += 1 + rng.IntN(3) {
			for range 10 {
				cur, prev := make([]byte, n+1), make([]byte, n+1)
				base, spread := rng.IntN(256), []int{1, 4, 40, 256}[rng.IntN(4)]
				for i := 1; i <= n; i++ {
					cur[i] = byte(base + rng.IntN(spread))
					prev[i] = byte(base + rng.IntN(spread))
				}
				var cg, cs [nFilter][]byte
				for k := range cg {
					cg[k], cs[k] = make([]byte, n+1), make([]byte, n+1)
				}
				copy(cg[0], cur)
				copy(cs[0], cur)
				fg := filterGo(&cg, prev, bpp)
				fs, ok := filterSIMD(&cs, prev, bpp)
				if !ok {
					continue
				}
				if fs != fg || !bytes.Equal(cs[fs][1:], cg[fg][1:]) {
					t.Fatalf("bpp %d, %d bytes: filter %d, want %d", bpp, n, fs, fg)
				}
			}
		}
	}
}
