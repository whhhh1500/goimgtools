package deflate

import "slices"

// Splitting a block further (Params.FineSplit), as the zopfli package's
// FineSplit does: after the search for the least cost, which tries nine
// points a round and misses a part that differs over a short stretch, and
// a block whose rows alternate between two kinds every few kilobytes,
// where splitting in two does not gain and no search starts.

const (
	fineSegments   = 32   // stretches of a part, the cheapest point of each a candidate
	fineCandidates = 4    // candidates looked at closely
	fineWindow     = 16   // items either side of a candidate tried
	fineHeaderBits = 1536 // a part's header for each symbol it uses, about, in 1/1024 bit (1.5 bits)
)

// fineSplits splits the parts between bounds further, and the parts that
// leaves again, while that saves bits. The points tried are those where
// the symbols on either side, each part coded by its own counts, take the
// fewest bits by their entropy and an estimate of the headers, found in
// one pass over the part (the counts moved one item at a time, in integer
// arithmetic): the cheapest of each of fineSegments stretches, the
// fineCandidates cheapest of those, and the items near them, judged by
// rangeCost. Each part is at least minSplitBlock long.
func (c *Compressor) fineSplits(bounds []int) []int {
	type span struct{ a, b int }
	work := make([]span, 0, len(bounds))
	for k := 0; k+1 < len(bounds); k++ {
		work = append(work, span{bounds[k], bounds[k+1]})
	}
	for len(work) > 0 && len(bounds)-1 < c.p.maxParts() {
		sp := work[len(work)-1]
		work = work[:len(work)-1]
		a, b := sp.a, sp.b
		if b-a < 2*minSplitBlock {
			continue
		}
		whole := c.rangeCost(a, b)
		best, bestAt := whole, -1
		for _, from := range c.candidatePoints(a, b) {
			if at, cost := c.tryWindow(a, b, from); at >= 0 && cost < best {
				best, bestAt = cost, at
			}
		}
		if bestAt < 0 {
			continue
		}
		k, _ := slices.BinarySearch(bounds, bestAt)
		bounds = slices.Insert(bounds, k, bestAt)
		work = append(work, span{a, bestAt}, span{bestAt, b})
	}
	return bounds
}

// xlog is v·log2(v) in 1/1024 bit.
func xlog(v uint32) int64 {
	if v == 0 {
		return 0
	}
	return int64(v) * int64(log2Fix(v))
}

// sideCounts are the counts of the symbols on one side of a point.
type sideCounts struct {
	f         freqs
	xl        [numLitlenSyms + numOffsetSyms]int64 // xlog of each count: the offsets' after the others
	sum       int64                                // of xl
	nLL, nOff uint32
	xOff      int64 // xlog of nOff
	used      int32 // symbols with a count
}

// set makes the side's counts f.
func (s *sideCounts) set(f *freqs) {
	*s = sideCounts{f: *f}
	for sym, v := range f.litlen {
		s.xl[sym] = xlog(v)
		s.sum += s.xl[sym]
		s.nLL += v
		if v != 0 {
			s.used++
		}
	}
	for sym, v := range f.offset {
		s.xl[numLitlenSyms+sym] = xlog(v)
		s.sum += s.xl[numLitlenSyms+sym]
		s.nOff += v
		if v != 0 {
			s.used++
		}
	}
	s.xOff = xlog(s.nOff)
}

func (s *sideCounts) add(sym int, off bool, d int32) {
	p, x := &s.f.litlen[sym], &s.xl[sym]
	if off {
		p, x = &s.f.offset[sym], &s.xl[numLitlenSyms+sym]
		s.nOff = uint32(int32(s.nOff) + d)
		s.xOff = xlog(s.nOff)
	} else {
		s.nLL = uint32(int32(s.nLL) + d)
	}
	old := *p
	nw := uint32(int32(old) + d)
	v := xlog(nw)
	s.sum += v - *x
	*p, *x = nw, v
	switch {
	case old == 0 && nw != 0:
		s.used++
	case old != 0 && nw == 0:
		s.used--
	}
}

// entropy is the bits of the side's symbols by their entropy, with the
// estimate of a header, in 1/1024 bit.
func (s *sideCounts) entropy() int64 {
	return xlog(s.nLL) + s.xOff - s.sum + fineHeaderBits*int64(s.used)
}

// itemSyms returns the litlen symbol of the item at i, its offset slot
// (-1 for a literal) and its length in bytes.
func (c *Compressor) itemSyms(i int) (ll, off, n int) {
	item := c.items[i]
	if item < numLiterals {
		return int(item), -1, 1
	}
	length := item >> optimumLenShift
	return firstLenSym + int(lengthSlot[length]), int(offsetSlot(item & optimumOffsetMask)), int(length)
}

type fineCand struct {
	from int   // an item start fineWindow items before the point
	cost int64 // the point's
}

// candidatePoints returns, for the fineCandidates cheapest stretches of
// [a, b), an item start fineWindow items before their cheapest points.
func (c *Compressor) candidatePoints(a, b int) []int {
	var left, right sideCounts
	var all freqs
	c.itemFreqs(a, b, &all)
	right.set(&all)
	lo, hi := a+minSplitBlock, b-minSplitBlock // the points allowed
	var ring [fineWindow]int
	for k := range ring {
		ring[k] = a
	}
	cands := make([]fineCand, 0, fineSegments)
	g, end := 0, lo+(hi-lo)/fineSegments
	cur := fineCand{from: -1}
	r := 0
	for i := a; i < b; {
		ring[r%fineWindow] = i
		r++
		ll, off, n := c.itemSyms(i)
		left.add(ll, false, 1)
		right.add(ll, false, -1)
		if off >= 0 {
			left.add(off, true, 1)
			right.add(off, true, -1)
		}
		i += n
		if i < lo {
			continue
		}
		if i > hi {
			break
		}
		for i >= end && g < fineSegments {
			// The stretch before point i is over.
			if cur.from >= 0 {
				cands = append(cands, cur)
			}
			cur = fineCand{from: -1}
			g++
			end = lo + (g+1)*(hi-lo)/fineSegments
		}
		if cost := left.entropy() + right.entropy(); cur.from < 0 || cost < cur.cost {
			cur = fineCand{ring[r%fineWindow], cost}
		}
	}
	if cur.from >= 0 {
		cands = append(cands, cur)
	}
	slices.SortStableFunc(cands, func(x, y fineCand) int {
		switch {
		case x.cost < y.cost:
			return -1
		case x.cost > y.cost:
			return 1
		}
		return 0
	})
	out := make([]int, 0, fineCandidates)
	for _, cd := range cands[:min(len(cands), fineCandidates)] {
		out = append(out, cd.from)
	}
	return out
}

// tryWindow tries the item starts from from on, 2·fineWindow+1 of them,
// as the point splitting [a, b), and returns the cheapest by rangeCost
// with its cost (-1 if none is allowed).
func (c *Compressor) tryWindow(a, b, from int) (int, uint32) {
	var left, right freqs
	xl := c.rangeFreqs(a, from, &left)
	xr := c.rangeFreqs(from, b, &right)
	best, bestAt := uint32(1<<32-1), -1
	i := from
	for k := 0; k <= 2*fineWindow && i < b; k++ {
		if i-a >= minSplitBlock && b-i >= minSplitBlock {
			if cost := freqsCost(&left, xl, i-a) + freqsCost(&right, xr, b-i); cost < best {
				best, bestAt = cost, i
			}
		}
		item := c.items[i]
		if item < numLiterals {
			left.litlen[item]++
			right.litlen[item]--
			i++
			continue
		}
		length := item >> optimumLenShift
		ls := lengthSlot[length]
		os := offsetSlot(item & optimumOffsetMask)
		e := uint32(extraLengthBits[ls]) + uint32(extraOffsetBits[os])
		left.litlen[firstLenSym+uint32(ls)]++
		right.litlen[firstLenSym+uint32(ls)]--
		left.offset[os]++
		right.offset[os]--
		xl += e
		xr -= e
		i += int(length)
	}
	return bestAt, best
}
