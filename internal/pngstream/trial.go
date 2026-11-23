package pngstream

import (
	"bytes"
	"container/heap"
	"encoding/binary"
	"io"
	mbits "math/bits"

	"github.com/whhhh1500/goimgtools/internal/deflate"
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
// filter is kept (in that order on a tie). The estimates then look as far
// back as deflate does, into the segment chosen before too, which the
// compressor has before it (0.05% smaller files).
//
// A segment is whole rows, about trialBytes of them (trialBytes10 at level
// 10, where the versions are compressed rather than estimated), and every
// choice depends on the pixels alone, in integer arithmetic: the file is
// the same for any number of helpers, any memory limit and on every
// processor. The estimate is deflate's in miniature (see estimate): it
// chooses as the real compressor would and needs little memory.
type filterTrial struct {
	env      *pipeline.Env
	ctx      []byte // from level 8: the end of the segment chosen before
	buf      []byte // and a segment after it
	none     []byte // the segment unfiltered
	filtered []byte // the segment as the rows filtered themselves
	brute    []byte // the segment as bruteFilter filtered it, from level 8
	est      *estimator
	size     int // the data of a segment, about (see segmentBytes)
	// At level 10, Brute's compressors (see bruteFilter.reals): the three
	// versions of a segment are compressed rather than estimated, each
	// after the context, on helpers at the same time when there are
	// compressors for them; their inputs, outputs and costs.
	reals    []*deflate.Compressor
	ins      [nTrial][]byte
	outs     [][]byte
	costs    [nTrial]int64
	skip     int // segments to take filtered without a trial
	streak   int // clear wins of filtering in a row
	reserved int64
}

// testNoTrial turns the trials off, for tests.
var testNoTrial bool

const (
	trialBytes   = 32 << 10
	trialSkipMax = 32
	// trialBytes10 is a segment at level 10, compressed rather than
	// estimated: longer segments keep the rows unfiltered (or filtered)
	// over longer stretches. Against 32KB the 17 test images are 0.005%
	// smaller with 64KB, 0.010% with 128KB, 0.012% with 256KB, 0.007% with
	// 1MB (a picture of text 0.6% smaller with 256KB).
	trialBytes10 = 256 << 10
	nTrial       = 3 // the versions of a segment tried from level 8
)

// segmentBytes is the data of a segment at the level.
func segmentBytes(level int) int {
	if level >= zopfliLevel {
		return trialBytes10
	}
	return trialBytes
}

// trialNeed is the memory of a filterTrial (see newFilterTrial), with
// lanes compressors for the versions of a segment.
func trialNeed(rowBytes, size int, brute bool, lanes int) int64 {
	seg := size + rowBytes
	if !brute {
		return 2*int64(seg) + estimatorBytes
	}
	need := nTrial*int64(seg) + estWindowNear // the versions and the context
	if lanes == 0 {
		return need + nearEstimatorBytes + int64(estWindowNear+seg)
	}
	return need + nTrial*int64(estWindowNear+seg) + int64(lanes)*int64(seg+seg/8+1<<10)
}

// newFilterTrial makes room for segments of about size bytes of rows of
// rowBytes, and for Brute's version of them when brute is set; with reals
// (at level 10, the compressors with which Brute scores the rows), the
// three versions of a segment are compressed to choose between them rather
// than estimated (17 images 0.012% smaller, in about the same time).
func newFilterTrial(env *pipeline.Env, rowBytes, size int, brute bool, reals []*deflate.Compressor) (*filterTrial, error) {
	seg := size + rowBytes
	lanes := min(len(reals), nTrial)
	need := trialNeed(rowBytes, size, brute, lanes)
	if err := env.Budget.Reserve(need, "png filter trial"); err != nil {
		return nil, err
	}
	t := &filterTrial{env: env, size: size, none: make([]byte, 0, seg), filtered: make([]byte, 0, seg), reserved: need}
	switch {
	case !brute:
		t.est = newEstimator(estHashBits, estWindow)
	case lanes == 0:
		t.est = newEstimator(estHashBitsNear, estWindowNear)
		t.buf = make([]byte, 0, estWindowNear+seg)
	}
	if brute {
		t.brute = make([]byte, 0, seg)
		t.ctx = make([]byte, 0, estWindowNear)
	}
	if lanes > 0 {
		t.reals = reals[:lanes]
		for i := range t.ins {
			t.ins[i] = make([]byte, 0, estWindowNear+seg)
		}
		for range lanes {
			t.outs = append(t.outs, make([]byte, 0, seg+seg/8+1<<10))
		}
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
	if len(t.none) >= t.size {
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
		cands := [nTrial][]byte{t.filtered, t.brute, t.none}
		// Brute's version is often the rows' own (nearly every segment of
		// a photograph): then it costs what that one does.
		same := bytes.Equal(t.brute, t.filtered)
		if t.reals != nil {
			t.compressAll(cands, same)
		} else {
			for i, p := range cands {
				if i == 1 && same {
					t.costs[1] = t.costs[0]
					continue
				}
				t.buf = append(append(t.buf[:0], t.ctx...), p...)
				t.costs[i] = t.est.estimateAfter(t.buf, len(t.ctx))
			}
		}
		// The first of the smallest, in that order.
		best := 0
		for i := 1; i < nTrial; i++ {
			if t.costs[i] < t.costs[best] {
				best = i
			}
		}
		seg = cands[best]
		keep := min(len(seg), estWindowNear)
		t.ctx = append(t.ctx[:0], seg[len(seg)-keep:]...)
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

// compressAll sets the costs of the versions of a segment: each compressed
// after the context, which they share as their dictionary (entered once,
// with the first version after it), on helpers at the same time when there
// are compressors for them. The costs are the same either way. The versions
// differ from the end of the context on, and the matchfinder looks ahead
// into the first while it enters the context's last bytes (see
// deflate.SharedAfter): the costs of the others are near, not exactly, what
// compressing each alone would give. They only choose; what is written is
// compressed afresh. With same, the second version is the first: it is not
// compressed again.
func (t *filterTrial) compressAll(cands [nTrial][]byte, same bool) {
	for i, p := range cands {
		t.ins[i] = append(append(t.ins[i][:0], t.ctx...), p...)
	}
	start := len(t.ctx)
	t.reals[0].EnterShared(t.ins[0], start)
	for _, r := range t.reals[1:] {
		r.TakeShared(t.reals[0])
	}
	lanes := len(t.reals)
	score := func(k int) {
		for i := k; i < nTrial; i += lanes {
			if i == 1 && same {
				continue
			}
			var bits int
			t.outs[k], bits = t.reals[k].CompressShared(t.outs[k][:0], t.ins[i], start, true, false)
			t.costs[i] = int64(8*len(t.outs[k]) - 8 + bits)
		}
	}
	if lanes > 1 {
		t.env.Parallel(lanes, score)
	} else {
		score(0)
	}
	if same {
		t.costs[1] = t.costs[0]
	}
}

// estimator estimates how many bits deflate makes of a stretch of data:
// greedy LZ77 matching, the longest of up to estChain earlier positions
// with the same hash (every position is entered, those inside matches
// too), then the bits Huffman codes made for the symbols take, with the
// extra bits of lengths and distances.
type estimator struct {
	head     []int32
	prev     []uint16 // distance to the position before with the same hash, 0 for none; by position modulo its length, twice the window
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
	estimatorBytes = 4<<estHashBits + 4*estWindow + 2 + 8*(286+30) + 8*286
	// From level 8, as far back as deflate looks (15 bits, for the reason of
	// bruteHashBits).
	estHashBitsNear    = 15
	estWindowNear      = 32 << 10
	nearEstimatorBytes = 4<<estHashBitsNear + 4*estWindowNear + 2 + 8*(286+30) + 8*286
	// estStretch is how many positions are entered and marked before they
	// are parsed (see bruteFilter.parse): the chains keep the positions of
	// the window and a stretch.
	estStretch = 4096
)

func newEstimator(hashBits uint, window int) *estimator {
	// (An entry more than prev's length, for markChains.)
	return &estimator{head: make([]int32, 1<<hashBits), prev: make([]uint16, 2*window, 2*window+1), hashBits: hashBits, window: window}
}

func (e *estimator) estimate(p []byte) int64 { return e.estimateAfter(p, 0) }

// estimateAfter estimates p[start:], with p[:start] the data before it,
// which matches may reach into (but for its last three positions, which
// are not entered). It goes a stretch at a time, as bruteFilter.parse does.
func (e *estimator) estimateAfter(p []byte, start int) int64 {
	for i := range e.head {
		e.head[i] = int32(-e.window) // none: out of the window from every position
	}
	clear(e.litLen[:])
	clear(e.dist[:])
	var extra int64
	n := len(p)
	last := n - 4 // the last position with four bytes: those up to it are entered
	prev, m, window := e.prev, len(e.prev)-1, e.window
	e.enter(p, 0, start-3)
	var marks [estStretch]uint8
	i := start
	for a := start; a <= last; a += estStretch {
		z := min(a+estStretch-1, last)
		e.enter(p, a, z-a+1)
		markChains(p, prev, a, a&m, window, marks[:z-a+1])
		for i <= z {
			if mark := marks[i-a]; mark != 0 {
				if l, dist := longestMatch(p, prev, i, i&m, mark, min(258, n-i), window, estChain); l > 0 {
					ls, lx := lengthCode(l)
					ds, dx := distCode(dist)
					e.litLen[ls]++
					e.dist[ds]++
					extra += int64(lx + dx)
					i += l
					continue
				}
			}
			e.litLen[p[i]]++
			i++
		}
	}
	for ; i < n; i++ {
		e.litLen[p[i]]++
	}
	e.litLen[256]++ // end of block
	return e.h.cost(e.litLen[:]) + e.h.cost(e.dist[:]) + extra
}

// enter enters the count positions of p from a on in the hash chains.
func (e *estimator) enter(p []byte, a, count int) {
	if count <= 0 {
		return
	}
	head, prev, m := e.head, e.prev, len(e.prev)-1
	shift, window := 32-e.hashBits, int64(e.window)
	w := p[a : a+count+3]
	for k := 0; k+3 < len(w); k++ {
		h := load32(w, k) * 2654435761 >> shift
		dist := int64(a+k) - int64(head[h])
		head[h] = int32(a + k)
		prev[(a+k)&m] = uint16(dist & ((dist - window) >> 63)) // 0 for one out of the window
	}
}

// The marks of a position that may have a match (see markChains).
const (
	markFirst  = 1 << iota // the first position of its chain starts with its three bytes
	markSecond             // the second does
	markMore               // the chain goes on past the second, within the window
)

// markChains marks the positions of d from at on, one for each of marks,
// that may have a match: with what the first two positions of the chain
// are, and whether the chain goes on past them within the window. prev holds
// the chains, the position at's entry at ring, the others' after it, around
// its length (a power of two). There is no branch: a position of a chain
// that is not there, its distance 0, is taken to be the position itself, and
// its part of the mark left out.
//
// With AVX2 the positions are marked eight at a time (see markChainsSIMD),
// but those whose entries go round the end of prev and those too near the
// end of d; prev has room for an entry more than its length.
func markChains(d []byte, prev []uint16, at, ring, window int, marks []uint8) {
	m := len(prev) - 1
	k := 0
	if chainsSIMD && cap(prev) > len(prev) {
		for len(marks)-k >= 8 {
			r := (ring + k) & m
			// As many eights as there are before the end of marks, of
			// prev and of what can be read of d.
			n := min(len(marks)-k, m+1-r, len(d)-(at+k)-12) &^ 7
			if n <= 0 {
				n = 8
				markChainsGo(d, prev, at+k, r, window, marks[k:k+n])
			} else {
				markChainsSIMD(&d[at+k], &prev[0], m, r, window, &marks[k], n)
			}
			k += n
		}
	}
	markChainsGo(d, prev, at+k, (ring+k)&m, window, marks[k:])
}

func markChainsGo(d []byte, prev []uint16, at, ring, window int, marks []uint8) {
	m := len(prev) - 1
	for k := range marks {
		i, r := at+k, (ring+k)&m
		cur := load32(d, i)
		// How far back the first two positions of the chain are.
		d1 := int(prev[r])
		x1 := load32(d, i-d1) ^ cur
		step := int(prev[(r-d1)&m])
		d2 := (d1 + step) & ((d1 + step - window) >> 31) & -int(some(step))
		x2 := load32(d, i-d2) ^ cur
		step = int(prev[(r-d2)&m])
		more := some(step) & (uint32(d2+step-window) >> 31)
		marks[k] = uint8(some(d1)&(((x1&0xffffff)-1)>>31)*markFirst | some(d2)*((((x2&0xffffff)-1)>>31)*markSecond|more*markMore))
	}
}

// longestMatch returns the length of the longest match of the position i of
// d, the first found of that length, and how far back it is (0 and 0 for
// none): the longest of those at the positions of its chain, the nearest
// first, up to chain of them within the window, each of up to maxLen bytes.
// ring is the position's entry in prev, and mark its mark: of the first two
// of the chain only those marked are matches, and the chain is followed
// past them only when it is marked as going on.
func longestMatch(d []byte, prev []uint16, i, ring int, mark uint8, maxLen, window, chain int) (l, dist int) {
	m := len(prev) - 1
	far := int(prev[ring])
	if mark&markFirst != 0 && chain > 0 {
		l, dist = matchLen(d, i, i-far, 3, maxLen), far
	}
	if mark&(markSecond|markMore) == 0 || chain < 2 || l == maxLen {
		return l, dist
	}
	far += int(prev[(ring-far)&m])
	// A longer match agrees at the byte after the best so far.
	if mark&markSecond != 0 && d[i-far+l] == d[i+l] {
		if j := matchLen(d, i, i-far, 3, maxLen); j > l {
			l, dist = j, far
		}
	}
	if mark&markMore == 0 || l == maxLen {
		return l, dist
	}
	cur := load32(d, i)
	far += int(prev[(ring-far)&m])
	for k := 2; k < chain; k++ {
		// Most positions of a long chain do not agree at that byte.
		if c := i - far; d[c+l] == d[i+l] && (load32(d, c)^cur)&0xffffff == 0 {
			if j := matchLen(d, i, c, 3, maxLen); j > l {
				l, dist = j, far
				if l == maxLen {
					break // none is longer
				}
			}
		}
		step := int(prev[(ring-far)&m])
		far += step
		if step == 0 || far >= window {
			break
		}
	}
	return l, dist
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
