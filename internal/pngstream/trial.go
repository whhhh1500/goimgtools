package pngstream

import (
	"container/heap"
	"encoding/binary"
	"io"
	mbits "math/bits"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// filterTrial chooses, a segment of rows at a time, between the filters
// the rows chose for themselves (the minimum sum of absolute differences,
// as libpng and png.Encode do) and no filter at all, by estimating the
// compressed size of the segment both ways and keeping the smaller.
// Photographs are smaller filtered; screenshots, icons and drawings, with
// their runs of the same colours, are often smaller unfiltered (an icon
// 19%, a page of text 11%).
//
// Where filtering wins clearly, as it does all through a photograph, the
// next segments are taken filtered without a trial, more of them each time
// (up to trialSkipMax), so that a photograph pays little for the trials; a
// segment where no filter wins, or nearly, starts the trials again.
//
// From level 8 the rows are also filtered as oxipng's Brute strategy
// filters them (see bruteFilter), and every segment is tried three ways,
// with no skipping: the smallest of the rows' own filters, Brute's and no
// filter is kept (in that order on a tie).
//
// A segment is whole rows, about trialBytes of them, and every choice
// depends on the pixels alone, in integer arithmetic: the file is the same
// for any number of helpers, any memory limit and on every processor. The
// estimate is deflate's in miniature (see estimate): it chooses as the
// real compressor would and needs little memory.
type filterTrial struct {
	env      *pipeline.Env
	none     []byte // the segment unfiltered
	filtered []byte // the segment as the rows filtered themselves
	brute    []byte // the segment as bruteFilter filtered it, from level 8
	est      *estimator
	skip     int // segments to take filtered without a trial
	streak   int // clear wins of filtering in a row
	reserved int64
}

// testNoTrial turns the trials off, for tests.
var testNoTrial bool

const (
	trialBytes   = 32 << 10
	trialSkipMax = 32
)

// newFilterTrial makes room for segments of rows of rowBytes, and for
// Brute's version of them when brute is set.
func newFilterTrial(env *pipeline.Env, rowBytes int, brute bool) (*filterTrial, error) {
	seg := trialBytes + rowBytes
	ways := int64(2)
	if brute {
		ways = 3
	}
	need := ways*int64(seg) + estimatorBytes
	if err := env.Budget.Reserve(need, "png filter trial"); err != nil {
		return nil, err
	}
	t := &filterTrial{env: env, none: make([]byte, 0, seg), filtered: make([]byte, 0, seg), est: newEstimator(estHashBits, estWindow), reserved: need}
	if brute {
		t.brute = make([]byte, 0, seg)
	}
	return t, nil
}

func (t *filterTrial) release() {
	t.env.Budget.Release(t.reserved)
	t.reserved, t.none, t.filtered, t.brute = 0, nil, nil, nil
}

// add takes a row unfiltered, filtered as it chose and, from level 8, as
// Brute chose (each with its filter type byte), and writes the segment to
// w once it is full.
func (t *filterTrial) add(none, filtered, brute []byte, w io.Writer) error {
	t.none = append(t.none, none...)
	t.filtered = append(t.filtered, filtered...)
	if t.brute != nil {
		t.brute = append(t.brute, brute...)
	}
	if len(t.none) >= trialBytes {
		return t.flush(w)
	}
	return nil
}

// flush writes the segment to w: filtered, or unfiltered when that
// compresses smaller.
func (t *filterTrial) flush(w io.Writer) error {
	if len(t.none) == 0 {
		return nil
	}
	seg := t.filtered
	if t.brute != nil {
		best := t.est.estimate(t.filtered)
		if b := t.est.estimate(t.brute); b < best {
			best, seg = b, t.brute
		}
		if n := t.est.estimate(t.none); n < best {
			seg = t.none
		}
	} else if t.skip > 0 {
		t.skip--
	} else {
		n, f := t.est.estimate(t.none), t.est.estimate(t.filtered)
		switch {
		case n < f:
			seg = t.none
			t.streak = 0
		case 8*f < 7*n:
			// Filtering wins by more than an eighth: trust it for a while.
			t.skip = min(1<<t.streak, trialSkipMax)
			t.streak++
		default:
			t.streak = 0
		}
	}
	_, err := w.Write(seg)
	t.none, t.filtered = t.none[:0], t.filtered[:0]
	if t.brute != nil {
		t.brute = t.brute[:0]
	}
	return err
}

// estimator estimates how many bits deflate makes of a stretch of data:
// greedy LZ77 matching, the longest of up to estChain earlier positions
// with the same hash (every position is entered, those inside matches
// too), then the bits Huffman codes made for the symbols take, with the
// extra bits of lengths and distances.
type estimator struct {
	head     []int32
	prev     []uint16 // distance to the position before with the same hash; 0 for none
	hashBits uint
	window   int // the matches it looks for are at most this far back
	litLen   [286]int64
	dist     [30]int64
	h        symHeap
}

const (
	estHashBits    = 11
	estChain       = 16
	estWindow      = 8 << 10
	estimatorBytes = 4<<estHashBits + 2*estWindow + 8*(286+30) + 8*286
)

func newEstimator(hashBits uint, window int) *estimator {
	return &estimator{head: make([]int32, 1<<hashBits), prev: make([]uint16, window), hashBits: hashBits, window: window}
}

func (e *estimator) estimate(p []byte) int64 {
	for i := range e.head {
		e.head[i] = -1
	}
	clear(e.litLen[:])
	clear(e.dist[:])
	var extra int64
	n := len(p)
	// insert enters position i and returns the one before with its hash.
	insert := func(i int) int {
		h := binary.LittleEndian.Uint32(p[i:]) * 2654435761 >> (32 - e.hashBits)
		c := int(e.head[h])
		e.head[h] = int32(i)
		e.prev[i&(e.window-1)] = 0
		if c >= 0 && i-c < e.window {
			e.prev[i&(e.window-1)] = uint16(i - c)
		}
		return c
	}
	for i := 0; i < n; {
		if i+4 <= n {
			c := insert(i)
			l, m := 0, -1
			for k := 0; k < estChain && c >= 0 && i-c < e.window; k++ {
				if p[c] == p[i] && p[c+1] == p[i+1] && p[c+2] == p[i+2] {
					j := matchLen(p, i, c, 3, min(258, n-i))
					if j > l {
						l, m = j, c
					}
				}
				d := int(e.prev[c&(e.window-1)])
				if d == 0 {
					break
				}
				c -= d
			}
			if m >= 0 {
				c := m
				ls, lx := lengthCode(l)
				ds, dx := distCode(i - c)
				e.litLen[ls]++
				e.dist[ds]++
				extra += int64(lx + dx)
				for j := i + 1; j < i+l && j+4 <= n; j++ {
					insert(j)
				}
				i += l
				continue
			}
		}
		e.litLen[p[i]]++
		i++
	}
	e.litLen[256]++ // end of block
	return e.h.cost(e.litLen[:]) + e.h.cost(e.dist[:]) + extra
}

// matchLen returns how far, up to maxLen, d[p:] and d[c:] agree, knowing
// that they do for l bytes.
func matchLen(d []byte, p, c, l, maxLen int) int {
	for l+8 <= maxLen {
		if x := binary.LittleEndian.Uint64(d[p+l:]) ^ binary.LittleEndian.Uint64(d[c+l:]); x != 0 {
			return l + mbits.TrailingZeros64(x)>>3
		}
		l += 8
	}
	for l < maxLen && d[p+l] == d[c+l] {
		l++
	}
	return l
}

// lengthCode is deflate's length symbol for a match of l (3 to 258) and
// the number of its extra bits.
func lengthCode(l int) (sym, extra int) {
	if l == 258 {
		return 285, 0
	}
	l -= 3
	if l < 8 {
		return 257 + l, 0
	}
	x := mbits.Len(uint(l)) - 3 // extra bits
	return 257 + 4*x + 4 + (l>>x - 4), x
}

// distCode is deflate's distance symbol for a distance d (1 to 32768)
// and the number of its extra bits.
func distCode(d int) (sym, extra int) {
	d--
	if d < 4 {
		return d, 0
	}
	x := mbits.Len(uint(d)) - 2
	return 2*x + 2 + (d>>x - 2), x
}

// symHeap computes the size in bits of symbols coded with a Huffman code
// made for their counts: the sum of the weights of the inner nodes.
type symHeap []int64

func (h symHeap) Len() int           { return len(h) }
func (h symHeap) Less(i, j int) bool { return h[i] < h[j] }
func (h symHeap) Swap(i, j int)      { h[i], h[j] = h[j], h[i] }
func (h *symHeap) Push(x any)        { *h = append(*h, x.(int64)) }
func (h *symHeap) Pop() any {
	old := *h
	x := old[len(old)-1]
	*h = old[:len(old)-1]
	return x
}

func (h *symHeap) cost(counts []int64) int64 {
	*h = (*h)[:0]
	for _, c := range counts {
		if c > 0 {
			*h = append(*h, c)
		}
	}
	if len(*h) < 2 {
		var n int64
		for _, c := range *h {
			n += c // one symbol still takes a bit each
		}
		return n
	}
	heap.Init(h)
	var total int64
	for h.Len() > 1 {
		a := heap.Pop(h).(int64)
		b := heap.Pop(h).(int64)
		total += a + b
		heap.Push(h, a+b)
	}
	return total
}
