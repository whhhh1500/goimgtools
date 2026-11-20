package zopfli

import "slices"

// Block splitting (blocksplitter.c): where splitting the parse in two
// makes it smaller, found by a search of the cost along the parse.

func findMinimum(f func(i int) float64, start, end int) (int, float64) {
	if end-start < 1024 {
		best, result := largeFloat, start
		for i := start; i < end; i++ {
			if v := f(i); v < best {
				best, result = v, i
			}
		}
		return result, best
	}
	const num = 9
	var p [num]int
	var vp [num]float64
	lastBest := largeFloat
	pos := start
	for end-start > num {
		for i := 0; i < num; i++ {
			p[i] = start + (i+1)*((end-start)/(num+1))
			vp[i] = f(p[i])
		}
		besti, best := 0, vp[0]
		for i := 1; i < num; i++ {
			if vp[i] < best {
				best, besti = vp[i], i
			}
		}
		if best > lastBest {
			break
		}
		if besti != 0 {
			start = p[besti-1]
		}
		if besti != num-1 {
			end = p[besti+1]
		}
		pos = p[besti]
		lastBest = best
	}
	return pos, lastBest
}

func addSorted(value int, out []int) []int {
	out = append(out, value)
	for i := 0; i+1 < len(out); i++ {
		if out[i] > value {
			copy(out[i+1:], out[i:len(out)-1])
			out[i] = value
			break
		}
	}
	return out
}

// findLargestSplittable finds the longest part, between the points of
// split, that does not start at one of done (the parts found not to gain
// by a split; Zopfli marks them in an array as long as the parse).
func findLargestSplittable(lz77Size int, done []int, split []int) (lstart, lend int, found bool) {
	longest := 0
	for i := 0; i <= len(split); i++ {
		start, end := 0, lz77Size-1
		if i > 0 {
			start = split[i-1]
		}
		if i < len(split) {
			end = split[i]
		}
		if !slices.Contains(done, start) && end-start > longest {
			lstart, lend, found, longest = start, end, true, end-start
		}
	}
	return
}

// blockSplitLZ77 gives the item indices where the parse should be split.
func (c *Compressor) blockSplitLZ77(s *lz77Store, maxBlocks int) []int {
	var split []int
	if s.size() < 10 {
		return nil
	}
	var done []int
	lstart, lend := 0, s.size()
	numBlocks := 1
	for {
		if maxBlocks > 0 && numBlocks >= maxBlocks {
			break
		}
		ls, le := lstart, lend
		llpos, splitCost := findMinimum(func(i int) float64 {
			return calculateBlockSizeAutoType(s, ls, i) + calculateBlockSizeAutoType(s, i, le)
		}, lstart+1, lend)
		origCost := calculateBlockSizeAutoType(s, lstart, lend)
		if splitCost > origCost || llpos == lstart+1 || llpos == lend {
			done = append(done, lstart)
		} else {
			split = addSorted(llpos, split)
			numBlocks++
		}
		var found bool
		if lstart, lend, found = findLargestSplittable(s.size(), done, split); !found {
			break
		}
		if lend-lstart < 10 {
			break
		}
	}
	return split
}

// fineSplit splits each block of the parse further, after the optimal
// parse, while that saves bits, and looks at each part again. The points
// tried are those where the symbols on either side, each part coded by its
// own counts, take the fewest bits by their entropy and an estimate of the
// headers (found in one pass over the block, the counts moved one item at a
// time): the best of each of fineSegments stretches of the block, the
// fineCandidates best of those, and the items near them, judged by the
// estimate of the blocks' sizes Zopfli splits by (see
// calculateBlockSizeAutoType). Zopfli's search tries 9 points a round and
// narrows to the best, and misses a part that differs over a short
// stretch, such as the transparent rows at the end of an icon.
func fineSplit(s *lz77Store, split []int) []int {
	type span struct{ lo, hi int }
	bounds := append(append([]int{0}, split...), s.size())
	work := make([]span, 0, len(bounds))
	for b := 0; b+1 < len(bounds); b++ {
		work = append(work, span{bounds[b], bounds[b+1]})
	}
	out := slices.Clone(split)
	var e entropySweep
	for len(work) > 0 {
		sp := work[len(work)-1]
		work = work[:len(work)-1]
		lo, hi := sp.lo, sp.hi
		if hi-lo < fineMin {
			continue
		}
		f := func(i int) float64 {
			return calculateBlockSizeAutoType(s, lo, i) + calculateBlockSizeAutoType(s, i, hi)
		}
		best := calculateBlockSizeAutoType(s, lo, hi)
		bestAt := -1
		for _, at := range e.candidates(s, lo, hi) {
			for i := max(lo+1, at-fineWindow); i <= min(hi-1, at+fineWindow); i++ {
				if v := f(i); v < best {
					best, bestAt = v, i
				}
			}
		}
		if bestAt < 0 {
			continue
		}
		out = append(out, bestAt)
		work = append(work, span{lo, bestAt}, span{bestAt, hi})
	}
	slices.Sort(out)
	return out
}

const (
	fineMin        = 20 // the fewest items of a block split further
	fineSegments   = 32 // stretches of a block, the cheapest point of each a candidate
	fineCandidates = 4  // candidates looked at closely
	fineWindow     = 16 // items either side of a candidate tried
	// fineHeaderBits is the bits of a block's header for each symbol it
	// uses, about (a photograph's blocks of some 280 symbols take 400 bits
	// or so). On the 17 test images 1.5 gives the smallest files of 1 to 4;
	// 4 misses the most, on a photograph whose rows alternate between two
	// kinds every few kilobytes.
	fineHeaderBits = 1.5
)

// xlog2x is x*log2(x), 0 for 0.
func xlog2x(x uint64) float64 {
	if x == 0 {
		return 0
	}
	f := float64(x)
	return float64(f * zlog2(f))
}

// entropySweep finds the candidates of fineSplit.
type entropySweep struct {
	left, right [numLL + numD]uint64
	cands       []fineCand
	ats         []int
}

type fineCand struct {
	at   int
	cost float64
}

// candidates returns the points, lo < at < hi, of the fineCandidates
// cheapest stretches' cheapest points, cheapest first.
func (e *entropySweep) candidates(s *lz77Store, lo, hi int) []int {
	left, right := &e.left, &e.right
	*left = [numLL + numD]uint64{}
	s.histogram(lo, hi, right[:numLL], right[numLL:])
	var sumL, sumR float64 // sum of c*log2(c) over each side's counts
	var nLLR, nDR, nLLL, nDL uint64
	var usedL, usedR int
	for i, c := range right {
		sumR += xlog2x(c)
		if c != 0 {
			usedR++
		}
		if i < numLL {
			nLLR += c
		} else {
			nDR += c
		}
	}
	move := func(k int) {
		r := right[k]
		sumR += xlog2x(r-1) - xlog2x(r)
		right[k] = r - 1
		if r == 1 {
			usedR--
		}
		l := left[k]
		sumL += xlog2x(l+1) - xlog2x(l)
		left[k] = l + 1
		if l == 0 {
			usedL++
		}
	}
	// The split after item i is point j = i-lo of n, in stretch g.
	n := hi - lo - 1
	e.cands = e.cands[:0]
	g, end := 0, n/fineSegments
	best := fineCand{at: -1}
	for i := lo; i < hi-1; i++ {
		move(s.llSymbol(i))
		nLLL++
		nLLR--
		if s.dists[i] != 0 {
			move(numLL + s.dSymbol(i))
			nDL++
			nDR--
		}
		for i-lo >= end {
			// The stretch before point i-lo is over.
			if best.at >= 0 {
				e.cands = append(e.cands, best)
			}
			best = fineCand{at: -1}
			g++
			end = (g + 1) * n / fineSegments
		}
		cost := xlog2x(nLLL) + xlog2x(nDL) - sumL + xlog2x(nLLR) + xlog2x(nDR) - sumR +
			float64(fineHeaderBits*float64(usedL+usedR))
		if best.at < 0 || cost < best.cost {
			best = fineCand{i + 1, cost}
		}
	}
	if best.at >= 0 {
		e.cands = append(e.cands, best)
	}
	slices.SortStableFunc(e.cands, func(x, y fineCand) int {
		switch {
		case x.cost < y.cost:
			return -1
		case x.cost > y.cost:
			return 1
		}
		return 0
	})
	e.ats = e.ats[:0]
	for _, c := range e.cands[:min(len(e.cands), fineCandidates)] {
		e.ats = append(e.ats, c.at)
	}
	return e.ats
}

// blockSplit gives the byte positions where data[inStart:inEnd] should be
// split, judged on a greedy parse.
func (c *Compressor) blockSplit(data []byte, inStart, inEnd, maxBlocks int) []int {
	store := &c.whole
	store.reset(data)
	bs := c.own.blockState(&c.opts, inStart, inEnd, false)
	bs.greedy(data, inStart, inEnd, store, c.own.h)
	lzSplit := c.blockSplitLZ77(store, maxBlocks)
	if c.opts.FineSplit {
		// The parts are parsed each with its own statistics: split as finely
		// on the greedy parse as after the optimal one (see deflatePart).
		lzSplit = fineSplit(store, lzSplit)
	}
	var split []int
	pos := inStart
	if len(lzSplit) > 0 {
		for i := 0; i < store.size(); i++ {
			length := 1
			if store.dists[i] != 0 {
				length = int(store.litlens[i])
			}
			if lzSplit[len(split)] == i {
				split = append(split, pos)
				if len(split) == len(lzSplit) {
					break
				}
			}
			pos += length
		}
	}
	return split
}
