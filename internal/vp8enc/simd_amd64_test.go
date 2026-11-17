//go:build amd64 && !purego

package vp8enc

import (
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/webpstream/vp8"
)

// TestFilterInnerSIMD: the decoder's AVX2 filter of inner edges filters a
// reconstructed macroblock as loop24 does, at every level, and reports a
// change as it does.
func TestFilterInnerSIMD(t *testing.T) {
	if !vp8.SIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(17, 18))
	for i := range 20000 {
		var b [384]uint8
		base := rng.IntN(256)
		spread := []int{2, 6, 20, 256}[rng.IntN(4)]
		for j := range b {
			b[j] = uint8(min(255, max(0, base+rng.IntN(spread)-spread/2)))
		}
		level := 1 + rng.IntN(lfLevels-1)
		want, got := b, b
		vp8.SIMD = false
		wc := filterInner(&want, level)
		vp8.SIMD = true
		gc := filterInner(&got, level)
		if got != want || gc != wc {
			t.Fatalf("case %d, level %d: changed %v, want %v; blocks differ: %v", i, level, gc, wc, got != want)
		}
	}
}

// TestSSIMSumsSIMD: the AVX2 window sums of each plane are those of
// winSums, for flat, smooth and noisy samples and the extremes.
func TestSSIMSumsSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(19, 20))
	var m mbFilter
	for i := range 5000 {
		var a, b [384]uint8
		for j := range a {
			switch i % 4 {
			case 0:
				a[j], b[j] = 255, 255
			case 1:
				a[j], b[j] = uint8(rng.IntN(256)), uint8(rng.IntN(256))
			default:
				v := rng.IntN(240)
				a[j], b[j] = uint8(v+rng.IntN(16)), uint8(v+rng.IntN(16))
			}
		}
		for p, pl := range mbPlanes {
			pa, pb := a[pl.off:pl.off+pl.n*pl.n], b[pl.off:pl.off+pl.n*pl.n]
			var gy, gyy, gxy, wy, wyy, wxy [100]uint32
			m.sums(p, pa, pb, &gy, &gyy, &gxy)
			var prod [256]uint32
			for j := range pb {
				prod[j] = uint32(pb[j])
			}
			winSums(prod[:len(pb)], pl.n, wy[:])
			for j := range pb {
				prod[j] = uint32(pb[j]) * uint32(pb[j])
			}
			winSums(prod[:len(pb)], pl.n, wyy[:])
			for j := range pb {
				prod[j] = uint32(pb[j]) * uint32(pa[j])
			}
			winSums(prod[:len(pb)], pl.n, wxy[:])
			if gy != wy || gyy != wyy || gxy != wxy {
				t.Fatalf("case %d, plane %d differs", i, p)
			}
		}
	}
}
