package vp8enc

import (
	"math/rand"
	"testing"
)

// TestWinSums: winSums gives the sums of the plain separable computation
// (the reference below) for both plane sizes, with the largest values a
// plane of products holds.
func TestWinSums(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for k := 0; k < 3000; k++ {
		for _, pl := range mbPlanes {
			v := make([]uint32, pl.n*pl.n)
			for i := range v {
				switch k % 3 {
				case 0:
					v[i] = uint32(r.Intn(256))
				case 1:
					v[i] = uint32(r.Intn(256) * r.Intn(256))
				default:
					v[i] = 255 * 255
				}
			}
			want, got := make([]uint32, 100), make([]uint32, 100)
			var tmp [16 * 10]uint32
			refSepSums(v, pl.n, pl.lo, pl.hi, tmp[:], want)
			winSums(v, pl.n, got)
			m := (pl.hi - pl.lo) * (pl.hi - pl.lo)
			for i := 0; i < m; i++ {
				if got[i] != want[i] {
					t.Fatalf("n %d, window %d: %d, want %d", pl.n, i, got[i], want[i])
				}
			}
		}
	}
}

// refSepSums sums plane v (n x n) under the window weights for each centre
// (cx, cy) in lo..hi-1, clipping the windows at the plane's edges, into
// out, row by row: across each row, then down each column of that.
func refSepSums(v []uint32, n, lo, hi int, tmp, out []uint32) {
	m := hi - lo
	for r := 0; r < n; r++ {
		refTri7(v[r*n:r*n+n], lo, hi, tmp[r*m:r*m+m])
	}
	var col [16]uint32
	var res [10]uint32
	for cx := 0; cx < m; cx++ {
		for r := 0; r < n; r++ {
			col[r] = tmp[r*m+cx]
		}
		refTri7(col[:n], lo, hi, res[:m])
		for cy := range m {
			out[cy*m+cx] = res[cy]
		}
	}
}

// refTri7 sums v (at most 16 values, zero beyond) under the weights 1, 2, 3,
// 4, 3, 2, 1 centred at lo..hi-1. The weights are two boxes of four, one
// after the other, so it takes running sums of four twice.
func refTri7(v []uint32, lo, hi int, out []uint32) {
	var p [22]uint32 // three zeros, v, three zeros
	n := copy(p[3:19], v)
	var b [19]uint32 // b[i] = p[i] + ... + p[i+3]
	s := p[0] + p[1] + p[2] + p[3]
	b[0] = s
	for i := 1; i <= n+2; i++ {
		s += p[i+3] - p[i-1]
		b[i] = s
	}
	for c := lo; c < hi; c++ {
		out[c-lo] = b[c] + b[c+1] + b[c+2] + b[c+3]
	}
}
