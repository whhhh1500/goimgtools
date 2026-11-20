package vp8lenc

import (
	"io"
	"math/rand/v2"
	"slices"
	"testing"
)

// chainOf lists the candidates a search at pos goes through, as find
// walks them: from the head of the hash, or ahead from pos's own link.
func chainOf(t *tokenizer, pos int) []int {
	p := pos - t.base
	lowest := max(pos-t.maxDist, t.base)
	var cand int
	if t.ahead {
		cand = int(t.prev[pos&t.wmask])
	} else {
		cand = int(t.head[t.hash(p)])
	}
	var out []int
	for ; cand > 0; cand-- {
		c := cand - 1
		if c < lowest {
			break
		}
		out = append(out, c)
		next := t.link(c)
		if next >= cand {
			break
		}
		cand = next
	}
	return out
}

// TestSearchAheadChains: entering a whole stretch into the hash chains
// first, and reading the links it overwrote from those saved, a search
// from a pixel's own link goes through the candidates it would one pixel
// after the other. Without the saved links it does not (the data, of
// three colours, makes many links).
func TestSearchAheadChains(t *testing.T) {
	r := rand.New(rand.NewPCG(7, 8))
	const total, width = 200000, 100
	data := make([]uint32, total)
	for i := range data {
		data[i] = []uint32{0xff000000, 0xff00ff00, 0xffff0000}[r.IntN(3)]
	}
	for _, window := range []int{1 << 15, 1 << 16} {
		seq := newTokenizer(width, total, window, data, io.Discard, newStats(nil))
		ahead := newTokenizer(width, total, window, data, io.Discard, newStats(nil))
		tight := newTokenizer(width, total, window, data, io.Discard, newStats(nil))
		ahead.ahead, tight.ahead = true, true
		ahead.saved = make([]uint32, optimalStretch)
		differ := 0
		for cur := 0; cur < total-8; cur += optimalStretch {
			n := min(optimalStretch, total-8-cur)
			ahead.cur = cur
			ahead.enterAhead(n)
			tight.insertTo(cur + n)
			for i := 0; i < n; i++ {
				seq.insertTo(cur + i)
				want := chainOf(seq, cur+i)
				if got := chainOf(ahead, cur+i); !slices.Equal(got, want) {
					t.Fatalf("window %d, pixel %d: candidates %v, one after the other %v", window, cur+i, got, want)
				}
				if !slices.Equal(chainOf(tight, cur+i), want) {
					differ++
				}
			}
		}
		if differ == 0 {
			t.Errorf("window %d: without the saved links the chains are the same too: the test sees nothing", window)
		}
		t.Logf("window %d: without the saved links, %d searches go another way", window, differ)
	}
}
