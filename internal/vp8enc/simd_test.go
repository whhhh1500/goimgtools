//go:build (amd64 || (arm64 && go1.27)) && !purego

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

// TestTransformsSIMD: the SIMD forward and inverse transforms and squared
// error give what the Go code gives, for random samples and coefficients
// and the extremes.
func TestTransformsSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(19, 20))
	sample := func(kind int) uint8 {
		switch kind {
		case 0:
			return uint8(rng.IntN(256))
		case 1:
			return uint8(rng.IntN(2) * 255)
		}
		return uint8(120 + rng.IntN(16))
	}
	for i := range 200000 {
		kind := i % 3
		var a, b [64]uint8
		for j := range a {
			a[j], b[j] = sample(kind), sample((kind+i/3)%3)
		}
		as, bs := 4+rng.IntN(13), 4+rng.IntN(13)
		var want, got [16]int16
		fdctGo(a[:], as, b[:], bs, &want)
		fdct(a[:], as, b[:], bs, &got)
		if got != want {
			t.Fatalf("fdct %d: %v, want %v", i, got, want)
		}
		if g, w := sse4(a[:], as, b[:], bs), sse4Go(a[:], as, b[:], bs); g != w {
			t.Fatalf("sse4 %d: %d, want %d", i, g, w)
		}
		var hw, hg coeffHist
		var bins [16]uint8
		rest := histBinsSIMD(&a[0], as, &b[0], bs, &bins)
		for k, c := range want {
			v := int(c)
			if v < 0 {
				v = -v
			}
			hw[min(v>>3, coeffBins-1)]++
			if int(bins[k]) != min(v>>3, coeffBins-1) || (rest>>k&1 != 0) != (bins[k] != 0) {
				t.Fatalf("histBins %d: coefficient %d in bin %d (%v), want %d", i, k, bins[k], rest>>k&1, min(v>>3, coeffBins-1))
			}
		}
		hg.add(a[:], as, b[:], bs)
		if hg != hw {
			t.Fatalf("coeffHist.add %d: %v, want %v", i, hg, hw)
		}
		var coef [16]int16
		for j := range coef {
			switch (i / 3) % 4 {
			case 0:
				coef[j] = int16(rng.IntN(65536) - 32768)
			case 1:
				coef[j] = int16(rng.IntN(4096) - 2048)
			case 2:
				coef[j] = []int16{-32768, 32767, 0, -1, 1}[rng.IntN(5)]
			default:
				coef[j] = want[j] * int16(1+rng.IntN(8))
			}
		}
		var wd, gd [64]uint8
		wd, gd = a, a
		idctAddGo(&coef, b[:], bs, wd[:], as)
		idctAdd(&coef, b[:], bs, gd[:], as)
		if gd != wd {
			t.Fatalf("idctAdd %d: %v, want %v (coef %v)", i, gd, wd, coef)
		}
	}
}

// TestQuantizeSIMD: the SIMD quantizer gives the levels, dequantized
// values and last position the Go code gives, for every quantizer index of
// every plane type, from the DC and from the first AC coefficient on.
func TestQuantizeSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(21, 22))
	e := &encoder{}
	for q := 0; q < 128; q++ {
		var s segment
		s.q = q
		e.setQuant(&s)
		for zi, z := range []*quantizer{&s.qY1, &s.qY2, &s.qUV} {
			if !z.simd {
				t.Fatalf("q %d, quantizer %d: not for SIMD", q, zi)
			}
			for i := range 3000 {
				var in [16]int16
				for j := range in {
					switch i % 4 {
					case 0:
						in[j] = int16(rng.IntN(65536) - 32768)
					case 1:
						in[j] = int16(rng.IntN(400) - 200)
					case 2:
						in[j] = []int16{-32768, 32767, 0, -1, 1, int16(z.q[1] / 2), -int16(z.q[1] / 2)}[rng.IntN(7)]
					default:
						if rng.IntN(4) == 0 {
							in[j] = int16(rng.IntN(100) - 50)
						}
					}
				}
				for first := 0; first < 2; first++ {
					var wl, wd, gl, gd [16]int16
					for j := range wl {
						wl[j], wd[j] = int16(j+3), int16(j*7)
					}
					gl, gd = wl, wd
					want := z.quantizeGo(&in, first, &wl, &wd)
					got := z.quantize(&in, first, &gl, &gd)
					if got != want || gl != wl || gd != wd {
						t.Fatalf("q %d, quantizer %d, first %d, %v: %d %v %v, want %d %v %v", q, zi, first, in, got, gl, gd, want, wl, wd)
					}
				}
			}
		}
	}
}

// TestIdctAddSIMDBounds: at the bound of the 16-bit lanes (coefficients
// of 2048 in every pattern of signs, and next to it) the inverse transform
// is still that of the Go code.
func TestIdctAddSIMDBounds(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	var ref [16]uint8
	for i := range ref {
		ref[i] = uint8(i * 17)
	}
	for _, mag := range []int16{2048, 2047, 2049, 1500} {
		for signs := 0; signs < 1<<16; signs++ {
			var coef [16]int16
			for k := range coef {
				coef[k] = mag
				if signs>>k&1 != 0 {
					coef[k] = -mag
				}
			}
			var want, got [16]uint8
			idctAddGo(&coef, ref[:], 4, want[:], 4)
			idctAdd(&coef, ref[:], 4, got[:], 4)
			if got != want {
				t.Fatalf("%v: %v, want %v", coef, got, want)
			}
		}
	}
}
