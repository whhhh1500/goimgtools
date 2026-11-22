//go:build (amd64 || (arm64 && go1.27)) && !purego

package ops

import (
	"math/rand/v2"
	"testing"
)

// TestBoxSIMD: the SIMD box passes give the bytes of the Go code, across
// (all channels, and the colours only with the alpha of dst kept) and
// down, for radii below, at and beyond the width, flat and noisy rows.
func TestBoxSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	defer func() { useSIMD = true }()
	rng := rand.New(rand.NewPCG(31, 32))
	for i := range 3000 {
		w := 1 + rng.IntN(70)
		if i%10 == 0 {
			w = 1 + rng.IntN(3)
		}
		r := float32(rng.IntN(40)) + float32(rng.IntN(4))*0.25
		if i%7 == 0 {
			r = float32(w + rng.IntN(5))
		}
		b := newBox(r)
		src := make([]uint8, 4*w)
		spread := []int{2, 30, 256}[i%3]
		base := rng.IntN(256)
		for k := range src {
			src[k] = uint8(min(255, max(0, base+rng.IntN(spread)-spread/2)))
		}
		for _, nc := range []int{3, 4} {
			want, got := make([]uint8, 4*w), make([]uint8, 4*w)
			for k := range want {
				want[k] = uint8(rng.IntN(256))
			}
			copy(got, want)
			h := &hbox{b: b, w: w, nc: nc, passes: 1}
			useSIMD = false
			h.pass(want, src)
			useSIMD = true
			// The pass by the sums of the row where there is one, and the
			// pass by a sum that slides.
			for _, prefix := range []bool{usePrefix, false} {
				was := usePrefix
				usePrefix = prefix
				out := append([]uint8(nil), got...)
				h.pass(out, src)
				usePrefix = was
				for k := range want {
					if out[k] != want[k] {
						t.Fatalf("case %d: w %d, r %v, %d channels, by sums %v: byte %d is %d, want %d", i, w, r, nc, prefix, k, out[k], want[k])
					}
				}
			}
		}

		// Down: n bytes of a column sum each.
		n := 8 * (1 + rng.IntN(20))
		a, bb, c := make([]uint8, n), make([]uint8, n), make([]uint8, n)
		sum := make([]uint32, n)
		for k := range a {
			a[k], bb[k], c[k] = uint8(rng.IntN(256)), uint8(rng.IntN(256)), uint8(rng.IntN(256))
			sum[k] = uint32(rng.IntN(255*(2*b.ri+1) + 1))
		}
		wantOut, gotOut := make([]uint8, n), make([]uint8, n)
		wantSum := append([]uint32(nil), sum...)
		for j := range wantOut {
			s, bj := wantSum[j], uint32(bb[j])
			wantOut[j] = b.value(s, uint32(a[j]), bj)
			wantSum[j] = s + bj - uint32(c[j])
		}
		vboxRowSIMD(&gotOut[0], &a[0], &bb[0], &c[0], &sum[0], n, b.ww, b.fw)
		for k := range wantOut {
			if gotOut[k] != wantOut[k] || sum[k] != wantSum[k] {
				t.Fatalf("case %d down: byte %d is %d (sum %d), want %d (%d)", i, k, gotOut[k], sum[k], wantOut[k], wantSum[k])
			}
		}
	}
}

// TestMedian3SIMD: the 3x3 median with SIMD gives the Go code's bytes for
// every width from 1 to 70, flat and noisy rows.
func TestMedian3SIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	defer func() { useSIMD = true }()
	rng := rand.New(rand.NewPCG(33, 34))
	for i := range 2000 {
		w := 1 + i%70
		win := make([][]uint8, 3)
		spread := []int{2, 30, 256}[i%3]
		base := rng.IntN(256)
		for k := range win {
			win[k] = make([]uint8, 4*w)
			for j := range win[k] {
				win[k][j] = uint8(min(255, max(0, base+rng.IntN(spread)-spread/2)))
			}
		}
		want, got := make([]uint8, 4*w), make([]uint8, 4*w)
		useSIMD = false
		median3Row(win, want, w)
		useSIMD = true
		median3Row(win, got, w)
		for k := range want {
			if got[k] != want[k] {
				t.Fatalf("case %d, width %d: byte %d is %d, want %d", i, w, k, got[k], want[k])
			}
		}
	}
}
