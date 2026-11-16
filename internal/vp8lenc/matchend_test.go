package vp8lenc

import (
	"image"
	"image/color"
	"io"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestMatchAt checks the remembered ends of matches against comparing the
// pixels: the tokenizer's searches go forward through the stream, at
// distances that repeat, with limits that vary (the buffer's end, the
// longest match), on data with long repeats and short ones.
func TestMatchAt(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for round := 0; round < 20; round++ {
		n := 2000 + r.IntN(20000)
		buf := make([]uint32, n)
		for i := 0; i < n; {
			switch k := r.IntN(4); {
			case k == 0 && i > 0: // a run
				l := min(r.IntN(3000), n-i)
				for j := 0; j < l; j++ {
					buf[i+j] = buf[i-1]
				}
				i += max(l, 1)
			case k == 1 && i > 100: // a copy from back
				d := 1 + r.IntN(min(i, 900))
				l := min(r.IntN(2000), n-i)
				for j := 0; j < l; j++ {
					buf[i+j] = buf[i+j-d]
				}
				i += max(l, 1)
			default:
				buf[i] = uint32(r.IntN(3))
				i++
			}
		}
		tk := &tokenizer{buf: buf, n: n, ends: make([]matchEnd, matchEnds)}
		for p := 1; p < n; p += 1 + r.IntN(3) {
			for k := 0; k < 4; k++ {
				d := 1 + r.IntN(min(p, 40))
				if k == 0 {
					d = 1 + r.IntN(min(p, 1000))
				}
				limit := min(maxMatch, n-p)
				if r.IntN(4) == 0 {
					limit = min(limit, 1+r.IntN(50))
				}
				if got, want := tk.matchAt(p, d, limit), matchLen(buf, p-d, p, limit); got != want {
					t.Fatalf("round %d: at %d, distance %d, limit %d: %d, want %d", round, p, d, limit, got, want)
				}
			}
		}
	}
}

// TestPredictorBits: larger predictor blocks give files that decode to the
// same pixels.
func TestPredictorBits(t *testing.T) {
	imgs := testutil.Images(67, 45)
	for _, name := range []string{"nrgba", "rgba-opaque", "gray", "paletted"} {
		for _, bits := range []int{2, 3, 4, 5, 9} {
			roundTrip(t, name, imgs[name], 7, &Options{Effort: 4, PredictorBits: bits})
		}
	}
}

// TestFindRuns: passing over the pixels of the run a search is in finds
// what going through the whole hash chain finds. (With the starting cost
// model, under which a distance costs no less than the one before it, the
// run's own pixels never win; refFind is find as it was.)
func TestFindRuns(t *testing.T) {
	r := rand.New(rand.NewPCG(5, 6))
	for round := 0; round < 30; round++ {
		w := 1 + r.IntN(60)
		n := 5000 + r.IntN(20000)
		buf := make([]uint32, n)
		for i := 0; i < n; {
			l := 1 + r.IntN(1+r.IntN(300))
			v := uint32(r.IntN(4))
			above := r.IntN(5) == 0 && i >= w
			for j := 0; j < l && i < n; j, i = j+1, i+1 {
				if above {
					buf[i] = buf[i-w]
				} else {
					buf[i] = v
				}
			}
		}
		mk := func() *tokenizer {
			tk := newTokenizer(w, int64(n), 1<<12, buf, io.Discard, newStats(nil))
			tk.chain, tk.nice = []int{4, 32, 256}[round%3], []int{64, 4096}[round%2]
			return tk
		}
		a, b := mk(), mk()
		for p := 0; p < n; p++ {
			a.insertTo(p)
			b.insertTo(p)
			l1, d1, v1 := a.find(p)
			l2, d2, v2 := refFind(b, p)
			if l1 != l2 || d1 != d2 || v1 != v2 {
				t.Fatalf("round %d, width %d, at %d: length %d at %d (%d), want %d at %d (%d)", round, w, p, l1, d1, v1, l2, d2, v2)
			}
		}
	}
}

func refFind(t *tokenizer, pos int) (bestLen, bestDist int, value int32) {
	buf := t.buf
	p := pos - t.base
	limit := min(maxMatch, t.n-p)
	if limit < minMatch {
		return 0, 0, 0
	}
	avg := int32(t.cm.avg)
	value = math.MinInt32
	if p >= 1 && buf[p-1] == buf[p] {
		bestLen, bestDist = t.matchAt(p, 1, limit), 1
		value = int32(bestLen)*avg - t.matchCost(bestLen, 1)
	}
	if w := t.width; w > 1 && p >= w && w <= t.maxDist && buf[p-w] == buf[p] && bestLen < limit {
		l := t.matchAt(p, w, limit)
		if v := int32(l)*avg - t.matchCost(l, w); v > value {
			bestLen, bestDist, value = l, w, v
		}
	}
	if bestLen >= t.nice || bestLen == limit || limit < minMatch {
		return
	}
	lowest := max(pos-t.maxDist, t.base)
	cand := int(t.head[t.hash(p)])
	for chain := t.chain; cand > 0 && chain > 0; chain-- {
		c := cand - 1
		if c < lowest {
			break
		}
		q := c - t.base
		if chk := max(bestLen-1, 0); buf[q+chk] == buf[p+chk] && buf[q] == buf[p] {
			if l := t.matchAt(p, pos-c, limit); l >= bestLen && l >= minMatch {
				if v := int32(l)*avg - t.matchCost(l, pos-c); v > value {
					bestLen, bestDist, value = l, pos-c, v
					if l >= t.nice || l == limit {
						break
					}
				}
			}
		}
		next := int(t.prev[c&t.wmask])
		if next >= cand {
			break
		}
		cand = next
	}
	return
}

// TestTreeSize: the size worked out for a tree is the size written, for
// codes of every shape: one or two symbols, below 256 or not, few or many,
// with runs of equal and of zero lengths, evened out or not.
func TestTreeSize(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	for round := 0; round < 3000; round++ {
		n := []int{40, 256, 280, 280 + 1<<r.IntN(11)}[r.IntN(4)]
		hist := make([]uint32, n)
		switch used := 1 + r.IntN(n); r.IntN(4) {
		case 0: // one or two symbols
			for k := 0; k < 1+r.IntN(2); k++ {
				hist[r.IntN(n)] = 1 + uint32(r.IntN(100))
			}
		case 1: // a smooth spread
			for s := 0; s < used; s++ {
				hist[s] = uint32(1000 / (1 + s))
			}
		default:
			for k := 0; k < used; k++ {
				hist[r.IntN(n)] += uint32(r.IntN(50))
			}
		}
		c, bits := bestCode(hist)
		var bw bitWriter
		writeTree(&bw, c)
		if got := treeBits(c); got != bw.bitLen() || bits != c.cost(hist)+got {
			t.Fatalf("round %d: tree %d bits, written %d; total %d, want %d", round, got, bw.bitLen(), bits, c.cost(hist)+got)
		}
	}
}

// TestCrossColorInverse: what the decoder does (the specification's
// inverse transform) undoes transformRed and transformBlue, for every
// green and red and any multipliers.
func TestCrossColorInverse(t *testing.T) {
	r := rand.New(rand.NewPCG(9, 10))
	for k := 0; k < 200; k++ {
		m := multipliers{uint8(r.IntN(256)), uint8(r.IntN(256)), uint8(r.IntN(256))}
		if multipliersOf(m.code()) != m {
			t.Fatalf("%v packs to %#x", m, m.code())
		}
		for g := 0; g < 256; g++ {
			red, blue := uint8(r.IntN(256)), uint8(r.IntN(256))
			p := 0xff000000 | uint32(red)<<16 | uint32(g)<<8 | uint32(blue)
			q := []uint32{p}
			m.apply(q)
			tr, tb := uint8(q[0]>>16), uint8(q[0])
			dr := tr + uint8(uint32(int32(int8(m.g2r))*int32(int8(g)))>>5)
			db := tb + uint8(uint32(int32(int8(m.g2b))*int32(int8(g)))>>5) + uint8(uint32(int32(int8(m.r2b))*int32(int8(dr)))>>5)
			if dr != red || db != blue || q[0]&0xff00ff00 != p&0xff00ff00 {
				t.Fatalf("%v on %#x: %#x decodes to red %d blue %d", m, p, q[0], dr, db)
			}
		}
	}
}

// TestCrossColor codes images with the cross-color variant alone: the
// files decode to the same pixels, whatever the strips (the variant
// carries the last row of a band into the next).
func TestCrossColor(t *testing.T) {
	grad := image.NewNRGBA(image.Rect(0, 0, 97, 70))
	r := rand.New(rand.NewPCG(11, 12))
	for y := 0; y < 70; y++ {
		for x := 0; x < 97; x++ {
			g := uint8(x*2 + y + r.IntN(3))
			grad.SetNRGBA(x, y, color.NRGBA{uint8(int(g) * 3 / 4), g, uint8(255 - int(g)/2), uint8(255 - r.IntN(2)*40)})
		}
	}
	imgs := testutil.Images(67, 45)
	imgs["gradient"] = grad
	testCrossOnly = true
	defer func() { testCrossOnly = false }()
	sizes := map[string]int{}
	for name, m := range imgs {
		for _, n := range []int{1, 7, 64} {
			sizes[name] = len(roundTrip(t, name, m, n, &Options{Effort: 5}))
		}
	}
	testCrossOnly = false
	plain := len(roundTrip(t, "gradient", grad, 7, &Options{Effort: 5}))
	t.Logf("gradient: %d bytes with the transform, %d without", sizes["gradient"], plain)
}

// TestRLE codes images with matches only at distance 1 and a row up: the
// files decode to the same pixels, whatever the strips and the variant.
func TestRLE(t *testing.T) {
	testRLE = true
	defer func() { testRLE = false }()
	for name, m := range testutil.Images(61, 39) {
		for _, n := range []int{1, 5, 64} {
			roundTrip(t, name, m, n, &Options{Effort: 5})
		}
	}
}
