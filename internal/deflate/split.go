package deflate

import "slices"

// Splitting a block where the cost says so, after zopfli
// (src/zopfli/blocksplitter.c of github.com/google/zopfli; Copyright 2011
// Google Inc., Apache License 2.0, see licenses/LICENSE-ZOPFLI; changed:
// written in Go, on this package's parse, by byte position): the block
// is parsed whole, then split
// where the estimated sizes of the two parts add up to least, the largest
// part first, as long as splitting saves bits; each part is then parsed
// again with costs of its own.

// splitAndFlush parses the block whole (with SplitPasses passes at most),
// splits it and writes the parts, each parsed again with its own costs
// (unless the whole parse had all the passes and there is one part).
func (c *Compressor) splitAndFlush(block []byte, cacheEnd int, isFirst, isFinal bool) bool {
	n := len(block)
	passes := c.p.Passes
	c.p.Passes = min(passes, max(c.p.SplitPasses, 1))
	full := c.p.Passes == passes
	onlyLiterals := c.optimizeBlock(block, cacheEnd, func() { c.setInitialCosts(n, isFirst) })
	c.p.Passes = passes
	if onlyLiterals {
		c.flushBlock(block, true, isFinal)
		return true
	}
	bounds := c.findSplits(n)
	if len(bounds) == 2 && full {
		c.flushBlock(block, false, isFinal)
		return false
	}
	// The codes each part starts from, from the whole parse, before the
	// parts overwrite it.
	starts := c.partLens[:len(bounds)-1]
	for k := range starts {
		var f freqs
		var cd codes
		c.rangeFreqs(bounds[k], bounds[k+1], &f)
		makeCodes(&f, &cd)
		starts[k] = partLens{cd.litlenLens, cd.offsetLens}
	}
	c.marks = c.marks[:0] // the parts are parsed again
	// Where the matches of each part end in the cache.
	ends := c.ends[:len(bounds)]
	ends[len(bounds)-1] = cacheEnd
	cp, k := cacheEnd, len(bounds)-2
	for i := n - 1; i >= 0 && k > 0; i-- {
		cp -= 1 + int(c.cache[cp-1].length)
		if i == bounds[k] {
			ends[k] = cp
			k--
		}
	}
	for k := range starts {
		part := block[bounds[k]:bounds[k+1]]
		ol := c.optimizeBlock(part, ends[k+1], func() {
			c.setCostsFromCodes(&starts[k].litlen, &starts[k].offset)
		})
		c.flushBlock(part, ol, isFinal && k == len(starts)-1)
		onlyLiterals = ol
	}
	return onlyLiterals
}

const (
	minSplitBlock  = 1024 // the shortest part a split leaves
	maxSplitBlocks = 15   // the most parts of a block, as zopfli's default
)

// splitStride is how far apart the marks of the parse are (more than a
// match is long, so that each stride has an item start).
const splitStride = 8192

// splitMarks are used (a variable for the tests).
var splitMarks = true

// A splitMark is where the first item start at or after a multiple of
// splitStride is in the parse of a block, with the counts of the symbols
// of the items before it and their extra bits: the counts of a range are
// those of the marks around it, and of the few items between the marks
// and its ends, so that the searches of the split points, which count the
// symbols of a range at every point they try, do not count the whole range
// each time. The counts are the same.
type splitMark struct {
	pos   int
	extra uint32
	f     freqs
}

// markItems sets the marks of c.items, the parse of a block of n bytes.
func (c *Compressor) markItems(n int) {
	marks := c.marks[:0]
	if !splitMarks || cap(marks) == 0 {
		c.marks = marks
		return
	}
	var m splitMark
	for i := 0; i < n; {
		if i >= len(marks)*splitStride && len(marks) < cap(marks) {
			m.pos = i
			marks = append(marks, m)
		}
		i += m.add(c.items[i], 1)
	}
	c.marks = marks
}

// add counts the symbols of item d times (1 or -1 as a uint32) and
// returns the item's length.
func (m *splitMark) add(item, d uint32) int {
	if item < numLiterals {
		m.f.litlen[item] += d
		return 1
	}
	length := item >> optimumLenShift
	ls := lengthSlot[length]
	os := offsetSlot(item & optimumOffsetMask)
	m.f.litlen[firstLenSym+uint32(ls)] += d
	m.f.offset[os] += d
	m.extra += d * (uint32(extraLengthBits[ls]) + uint32(extraOffsetBits[os]))
	return int(length)
}

// markBefore returns the last mark at or before p (nil if there are no
// marks).
func (c *Compressor) markBefore(p int) *splitMark {
	if len(c.marks) == 0 {
		return nil
	}
	k := min(p/splitStride, len(c.marks)-1)
	if c.marks[k].pos > p {
		k--
	}
	return &c.marks[k]
}

// rangeFreqs counts the symbols of the items starting in [a, b) of c.items
// (a an item's start), with the end of block, and returns the extra bits.
func (c *Compressor) rangeFreqs(a, b int, f *freqs) (extra uint32) {
	extra = c.itemFreqs(a, b, f)
	f.litlen[endOfBlock]++
	return extra
}

// itemFreqs adds to f the counts of the symbols of the items starting in
// [a, b) of c.items (a an item's start), and returns their extra bits.
func (c *Compressor) itemFreqs(a, b int, f *freqs) (extra uint32) {
	if ma, mb := c.markBefore(a), c.markBefore(b); ma != nil && mb.pos-ma.pos > 2*splitStride {
		// The counts before b less those before a.
		m := splitMark{extra: mb.extra - ma.extra}
		for s := range m.f.litlen {
			m.f.litlen[s] = mb.f.litlen[s] - ma.f.litlen[s]
		}
		for s := range m.f.offset {
			m.f.offset[s] = mb.f.offset[s] - ma.f.offset[s]
		}
		for i := ma.pos; i < a; {
			i += m.add(c.items[i], 1<<32-1)
		}
		for i := mb.pos; i < b; {
			i += m.add(c.items[i], 1)
		}
		for s := range f.litlen {
			f.litlen[s] += m.f.litlen[s]
		}
		for s := range f.offset {
			f.offset[s] += m.f.offset[s]
		}
		return m.extra
	}
	for i := a; i < b; {
		item := c.items[i]
		if item < numLiterals {
			f.litlen[item]++
			i++
			continue
		}
		length := item >> optimumLenShift
		ls := lengthSlot[length]
		os := offsetSlot(item & optimumOffsetMask)
		f.litlen[firstLenSym+uint32(ls)]++
		f.offset[os]++
		extra += uint32(extraLengthBits[ls]) + uint32(extraOffsetBits[os])
		i += int(length)
	}
	return extra
}

// nextItem is the first item start at or after p (or n), a an item start
// before p.
func (c *Compressor) nextItem(a, p, n int) int {
	if m := c.markBefore(p); m != nil && m.pos > a {
		a = m.pos // an item start too, nearer p
	}
	for i := a; i < n; {
		if i >= p {
			return i
		}
		if item := c.items[i]; item < numLiterals {
			i++
		} else {
			i += int(item >> optimumLenShift)
		}
	}
	return n
}

// rangeCost estimates the bits of the items in [a, b) as one block: the
// least of a dynamic block, a static block and stored blocks.
func (c *Compressor) rangeCost(a, b int) uint32 {
	var f freqs
	extra := c.rangeFreqs(a, b, &f)
	return freqsCost(&f, extra, b-a)
}

// freqsCost is rangeCost of n bytes whose symbols are f, with the extra
// bits.
func freqsCost(f *freqs, extra uint32, n int) uint32 {
	var cd codes
	makeCodes(f, &cd)
	dynamic := 3 + headerBits(&cd) + extra
	static := 3 + extra
	for sym, k := range f.litlen {
		dynamic += k * uint32(cd.litlenLens[sym])
		static += k * uint32(staticCodes.litlenLens[sym])
	}
	for sym, k := range f.offset {
		dynamic += k * uint32(cd.offsetLens[sym])
		static += k * 5
	}
	stored := uint32(3 + 7 + 32 + 40*((n+0xffff-1)/0xffff-1) + 8*n)
	return min(dynamic, static, stored)
}

// headerBits is the size of the header of a dynamic block with the codes,
// after its first three bits.
func headerBits(cd *codes) uint32 {
	nl := numLitlenSyms
	for nl > 257 && cd.litlenLens[nl-1] == 0 {
		nl--
	}
	no := numOffsetSyms
	for no > 1 && cd.offsetLens[no-1] == 0 {
		no--
	}
	var lens [numLitlenSyms + numOffsetSyms]uint8
	copy(lens[:], cd.litlenLens[:nl])
	copy(lens[nl:], cd.offsetLens[:no])
	var pf [numPrecodeSyms]uint32
	var items [numLitlenSyms + numOffsetSyms]uint32
	ni := computePrecodeItems(lens[:nl+no], &pf, items[:])
	var plens [numPrecodeSyms]uint8
	var pcw [numPrecodeSyms]uint32
	makeHuffmanCode(maxPreCodewordLen, pf[:], plens[:], pcw[:])
	ne := numPrecodeSyms
	for ne > 4 && plens[precodeLensPermutation[ne-1]] == 0 {
		ne--
	}
	bits := uint32(5 + 5 + 4 + 3*ne)
	for _, it := range items[:ni] {
		sym := it & 0x1f
		bits += uint32(plens[sym]) + uint32(extraPrecodeBits[sym])
	}
	return bits
}

// findSplits returns the bounds of the parts of the block of n bytes
// (with c.items its parse): 0, the split points in order, n. As zopfli,
// it splits the largest part that may still gain, where its two halves
// cost least, until no part gains or there are maxSplitBlocks.
func (c *Compressor) findSplits(n int) []int {
	c.markItems(n)
	bounds := append(c.bounds[:0], 0, n)
	done := append(c.done[:0], false) // by part
	for len(bounds)-1 < c.p.maxParts() {
		k := -1
		for i := range done {
			if !done[i] && (k < 0 || bounds[i+1]-bounds[i] > bounds[k+1]-bounds[k]) {
				k = i
			}
		}
		if k < 0 {
			break
		}
		a, b := bounds[k], bounds[k+1]
		if b-a < 2*minSplitBlock {
			done[k] = true
			continue
		}
		p, cost := c.findMinimum(a, b)
		if p <= a || p >= b || cost >= c.rangeCost(a, b) {
			done[k] = true
			continue
		}
		bounds = slices.Insert(bounds, k+1, p)
		done = slices.Insert(done, k+1, false)
	}
	if c.p.FineSplit {
		bounds = c.fineSplits(bounds)
	}
	c.bounds, c.done = bounds, done
	return bounds
}

// findMinimum looks for the split point of [a, b) where the two parts
// cost least, as zopfli's FindMinimum does: nine points evenly spaced,
// then the stretch around the best, until it stops improving.
//
// The symbols before a point are counted on from the point before it (the
// points of a round go up, and the next round starts after one of them), and
// those after it are the rest of the range's: the counts, and so the costs,
// are those of counting each part of each point anew.
func (c *Compressor) findMinimum(a, b int) (int, uint32) {
	const num = 9
	lo, hi := a+minSplitBlock, b-minSplitBlock
	best, bestCost := -1, uint32(1<<32-1)
	last := uint32(1<<32 - 1)
	var whole, left freqs // of the items of [a, b), and of [a, at)
	wholeExtra := c.itemFreqs(a, b, &whole)
	leftExtra, at := uint32(0), a
	for hi-lo > num {
		var ps [num]int
		var vs [num]uint32
		var before [num + 1]freqs // of the items of [a, ps[i-1]); the first as the round starts
		var beforeExtra [num + 1]uint32
		before[0], beforeExtra[0] = left, leftExtra
		start := at
		bi := 0
		for i := range ps {
			ps[i] = c.nextItem(a, lo+(i+1)*((hi-lo)/(num+1)), b)
			leftExtra += c.itemFreqs(at, ps[i], &left)
			at = ps[i]
			before[i+1], beforeExtra[i+1] = left, leftExtra
			vs[i] = partsCost(&left, leftExtra, &whole, wholeExtra, ps[i]-a, b-ps[i])
			if vs[i] < vs[bi] {
				bi = i
			}
		}
		if vs[bi] > last {
			break
		}
		best, bestCost, last = ps[bi], vs[bi], vs[bi]
		nlo, nhi := lo, hi
		if bi > 0 {
			nlo = ps[bi-1]
		}
		if bi < num-1 {
			nhi = ps[bi+1]
		}
		if nhi-nlo >= hi-lo {
			// The points fell on the same items: as close as it gets.
			break
		}
		lo, hi = nlo, nhi
		// The next round's points are after the point before the best (or
		// where this round started).
		left, leftExtra, at = before[bi], beforeExtra[bi], start
		if bi > 0 {
			at = ps[bi-1]
		}
	}
	return best, bestCost
}

// partsCost is rangeCost of the two parts of a range, of nl and nr bytes:
// left are the counts of the first part's symbols, with their extra bits,
// and whole those of the range's.
func partsCost(left *freqs, leftExtra uint32, whole *freqs, wholeExtra uint32, nl, nr int) uint32 {
	var right freqs
	for s := range right.litlen {
		right.litlen[s] = whole.litlen[s] - left.litlen[s]
	}
	for s := range right.offset {
		right.offset[s] = whole.offset[s] - left.offset[s]
	}
	right.litlen[endOfBlock]++
	left.litlen[endOfBlock]++
	cost := freqsCost(left, leftExtra, nl) + freqsCost(&right, wholeExtra-leftExtra, nr)
	left.litlen[endOfBlock]--
	return cost
}
