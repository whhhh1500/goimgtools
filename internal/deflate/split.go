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
	starts := c.partCodes[:len(bounds)-1]
	for k := range starts {
		var f freqs
		c.rangeFreqs(bounds[k], bounds[k+1], &f)
		makeCodes(&f, &starts[k])
	}
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
			c.setCostsFromCodes(&starts[k].litlenLens, &starts[k].offsetLens)
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

// rangeFreqs counts the symbols of the items starting in [a, b) of c.items
// (a an item's start), with the end of block, and returns the extra bits.
func (c *Compressor) rangeFreqs(a, b int, f *freqs) (extra uint32) {
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
	f.litlen[endOfBlock]++
	return extra
}

// nextItem is the first item start at or after p (or n).
func (c *Compressor) nextItem(a, p, n int) int {
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
	var cd codes
	makeCodes(&f, &cd)
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
	stored := uint32(3 + 7 + 32 + 40*((b-a+0xffff-1)/0xffff-1) + 8*(b-a))
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
	bounds := append(c.bounds[:0], 0, n)
	done := append(c.done[:0], false) // by part
	for len(bounds)-1 < maxSplitBlocks {
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
	c.bounds, c.done = bounds, done
	return bounds
}

// findMinimum looks for the split point of [a, b) where the two parts
// cost least, as zopfli's FindMinimum does: nine points evenly spaced,
// then the stretch around the best, until it stops improving.
func (c *Compressor) findMinimum(a, b int) (int, uint32) {
	const num = 9
	lo, hi := a+minSplitBlock, b-minSplitBlock
	best, bestCost := -1, uint32(1<<32-1)
	last := uint32(1<<32 - 1)
	for hi-lo > num {
		var ps [num]int
		var vs [num]uint32
		bi := 0
		for i := range ps {
			ps[i] = c.nextItem(a, lo+(i+1)*((hi-lo)/(num+1)), b)
			vs[i] = c.rangeCost(a, ps[i]) + c.rangeCost(ps[i], b)
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
	}
	return best, bestCost
}
