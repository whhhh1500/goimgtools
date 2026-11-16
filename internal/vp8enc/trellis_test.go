package vp8enc

import (
	"image"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestTrellisLevels checks what trellis quantization may choose: each
// level is zero, the magnitude rounded down, or one more but not beyond
// rounding to nearest (divided as the quantizer divides), with the sign of
// the coefficient; the dequantized values and the last position follow the
// levels.
func TestTrellisLevels(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	e := &encoder{}
	c := &mbCoder{e: e}
	var pr probs = defaultTokenProb
	c.costs = new(costTables)
	c.costs.build(&pr)
	for _, q := range []int{4, 20, 60, 127} {
		var s segment
		s.q = q
		e.setQuant(&s)
		z := &s.qY1
		for i := 0; i < 2000; i++ {
			var in [16]int16
			scale := int32(r.IntN(400) + 1)
			for j := range in {
				in[j] = int16(r.Int32N(2*scale+1) - scale)
				if r.IntN(3) == 0 {
					in[j] = 0
				}
			}
			for _, first := range []int{0, 1} {
				plane := planeY1SansY2
				if first == 1 {
					plane = planeY1WithY2
				}
				var lv, deq [16]int16
				last := c.trellis(&in, first, r.IntN(3), plane, z, s.lamT, &lv, &deq)
				wantLast := -1
				for n := first; n < 16; n++ {
					j := zigzag[n]
					step, iq := z.q[1], z.iq[1]
					if n == 0 {
						step, iq = z.q[0], z.iq[0]
					}
					v := int32(in[j])
					if v < 0 {
						v = -v
					}
					l := int32(lv[n])
					if l < 0 {
						l = -l
					}
					// Divided as the quantizer divides, by the reciprocal.
					down, nearest := (v*iq)>>17, (v*iq+1<<16)>>17
					if l != 0 && l != down && (l != down+1 || l > nearest) {
						t.Fatalf("q%d: coefficient %d at %d: level %d (step %d)", q, in[j], n, lv[n], step)
					}
					if l != 0 && (lv[n] < 0) != (in[j] < 0) {
						t.Fatalf("q%d: level %d for %d: wrong sign", q, lv[n], in[j])
					}
					if deq[j] != lv[n]*int16(step) {
						t.Fatalf("q%d: dequantized %d for level %d step %d", q, deq[j], lv[n], step)
					}
					if lv[n] != 0 {
						wantLast = n
					}
				}
				if last != wantLast {
					t.Fatalf("q%d: last %d, want %d", q, last, wantLast)
				}
			}
		}
	}
}

// TestDCDiffuse checks that chroma DC error diffusion breaks up the bands
// of a smooth colour gradient: the colours of the decoded image, averaged
// over areas larger than a macroblock (where bands show), must be closer
// to those of the source.
func TestDCDiffuse(t *testing.T) {
	m := gradient(320, 96)
	var errs [2]float64
	for i, dbg := range []*debug{{noDiffuse: true}, nil} {
		data := mustEncode(t, testutil.Chopped(m, 16), 30, dbg)
		errs[i] = bandError(m, toRGB(decodeX(t, data)), 24)
	}
	t.Logf("banding error without diffusion %.3f, with %.3f", errs[0], errs[1])
	if errs[1] >= errs[0] {
		t.Errorf("diffusion does not reduce banding: %.3f against %.3f", errs[1], errs[0])
	}
}

// gradient is a slow blue to purple gradient with a little texture.
func gradient(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := (x*7 + y*13) % 3
			copy(m.Pix[m.PixOffset(x, y):], []uint8{uint8(30 + x*60/w + n), uint8(60 + y*20/h), uint8(200 - x*20/w), 255})
		}
	}
	return m
}

// bandError is the RMS difference between the means of each channel over
// squares of side k, of m and of interleaved RGB.
func bandError(m *image.NRGBA, rgb []uint8, k int) float64 {
	b := m.Bounds()
	var sum float64
	n := 0
	for y0 := 0; y0+k <= b.Dy(); y0 += k {
		for x0 := 0; x0+k <= b.Dx(); x0 += k {
			for ch := 0; ch < 3; ch++ {
				var a, d float64
				for y := y0; y < y0+k; y++ {
					for x := x0; x < x0+k; x++ {
						a += float64(m.Pix[m.PixOffset(x, y)+ch])
						d += float64(rgb[3*(y*b.Dx()+x)+ch])
					}
				}
				e := (a - d) / float64(k*k)
				sum += e * e
				n++
			}
		}
	}
	return math.Sqrt(sum / float64(n))
}
