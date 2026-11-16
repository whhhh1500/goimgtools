package vp8lenc

import (
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
