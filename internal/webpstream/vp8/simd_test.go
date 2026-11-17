package vp8

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestFilterSIMD: the AVX2 loop filter changes the pixels around an edge
// as filter246 does, across rows and across columns, six and four pixels
// wide, for every level, interior level and variance threshold the
// decoder computes (and random ones), on smooth, noisy and flat pixels.
func TestFilterSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
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

// TestIDCTSIMD: the SIMD inverse transform adds to the prediction what
// inverseDCT4 adds, for coefficients a stream can hold (any 16-bit value)
// and those of the narrow lanes' bound.
func TestIDCTSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(9, 10))
	var want, got Decoder
	for i := range 200000 {
		for k := 0; k < 16; k++ {
			var v int16
			switch i % 4 {
			case 0:
				v = int16(rng.IntN(65536) - 32768)
			case 1:
				v = int16(rng.IntN(4097) - 2048)
			case 2:
				v = []int16{-32768, 32767, 2048, -2048, 2049, -2049, 0}[rng.IntN(7)]
			default:
				if rng.IntN(3) == 0 {
					v = int16(rng.IntN(200) - 100)
				}
			}
			want.coeff[16+k] = v
		}
		for y := range want.ybr {
			for x := range want.ybr[y] {
				want.ybr[y][x] = uint8(rng.IntN(256))
			}
		}
		got.coeff, got.ybr = want.coeff, want.ybr
		y, x := 1+rng.IntN(len(want.ybr)-4), rng.IntN(len(want.ybr[0])-3)
		want.inverseDCT4Go(y, x, 16)
		got.inverseDCT4(y, x, 16)
		if got.ybr != want.ybr {
			t.Fatalf("case %d at %d,%d: %v", i, y, x, want.coeff[16:32])
		}
	}
}

// TestDCSIMD: the SIMD DC-only transforms add to the prediction what
// inverseDCT4DCOnly and inverseDCT8DCOnly add in Go (each pixel plus
// (DC+4)>>3, clipped), for any 16-bit DC and those that clip.
func TestDCSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(11, 12))
	var want, got Decoder
	add := func(y, x, coeffBase int) {
		dc := (int32(want.coeff[coeffBase]) + 4) >> 3
		for j := 0; j < 4; j++ {
			for i := 0; i < 4; i++ {
				want.ybr[y+j][x+i] = clip8(int32(want.ybr[y+j][x+i]) + dc)
			}
		}
	}
	for i := range 200000 {
		for k := range want.coeff {
			v := int16(rng.IntN(65536) - 32768)
			if i%4 < 2 {
				v = int16(rng.IntN(4200) - 2100)
			}
			want.coeff[k] = v
		}
		for y := range want.ybr {
			for x := range want.ybr[y] {
				want.ybr[y][x] = uint8(rng.IntN(256))
			}
		}
		got.coeff, got.ybr = want.coeff, want.ybr
		if i%2 == 0 {
			y, x, cb := 1+rng.IntN(len(want.ybr)-4), rng.IntN(len(want.ybr[0])-3), 16*rng.IntN(24)
			add(y, x, cb)
			got.inverseDCT4DCOnly(y, x, cb)
		} else {
			y, x, cb := 1+rng.IntN(len(want.ybr)-8), rng.IntN(len(want.ybr[0])-7), bCoeffBase+64*rng.IntN(2)
			add(y, x, cb)
			add(y, x+4, cb+16)
			add(y+4, x, cb+32)
			add(y+4, x+4, cb+48)
			got.inverseDCT8DCOnly(y, x, cb)
		}
		if got.ybr != want.ybr {
			t.Fatalf("case %d", i)
		}
	}
}
