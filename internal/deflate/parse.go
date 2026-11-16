package deflate

// The near-optimal parsing of libdeflate (deflate_compress_near_optimal
// and what it calls, lib/deflate_compress.c and
// lib/find_min_cost_path_template.h).

// compress writes in[start:] as deflate blocks, the last final if final
// is set; 0 < len(in)-start, start <= windowSize.
func (c *Compressor) compress(in []byte, start int, final bool) {
	inEnd := len(in)
	base := 0                           // of the matchfinder's positions
	nextSlide := min(inEnd, windowSize) // where the base moves on
	maxLen, niceLen := maxMatch, min(c.p.NiceLen, maxMatch)
	depth := c.p.SearchDepth
	var nh [2]uint32
	c.mf.init()

	// step makes way for the position i: moves the base on at the end of
	// a window, and shortens the matches looked for near the end.
	step := func(i int) {
		remaining := inEnd - i
		if i == nextSlide {
			c.mf.slide()
			base = i
			nextSlide = i + min(remaining, windowSize)
		}
		if remaining < maxMatch {
			maxLen = remaining
			niceLen = min(niceLen, maxLen)
		}
	}
	// The dictionary is entered as libdeflate enters the bytes it skips.
	for i := 0; i < start; i++ {
		step(i)
		if maxLen >= mfRequiredBytes {
			c.mf.advance(in, base, i-base, niceLen, niceLen, depth, &nh, nil, false)
		}
	}

	inNext, blockBegin := start, start
	cp := 0 // the end of the match cache
	cacheEnd := c.p.CachePerBlock * c.p.BlockLen
	prevOnlyLiterals := false
	c.initStats()
	for {
		maxBlockEnd := inEnd
		if inEnd-blockBegin >= c.p.BlockLen+minBlockLength {
			maxBlockEnd = blockBegin + c.p.BlockLen
		}
		prevEndBlockCheck := -1
		changeDetected := false
		nextObservation := inNext
		minLen := maxMatch + 1
		if !prevOnlyLiterals {
			minLen = calculateMinMatchLen(in[blockBegin:maxBlockEnd], depth)
		}
		for {
			step(inNext)
			matches := cp
			bestLen := 0
			if maxLen >= mfRequiredBytes {
				found := c.mf.advance(in, base, inNext-base, maxLen, niceLen, depth, &nh, c.cache[cp:cp], true)
				cp += len(found)
				if len(found) > 0 {
					bestLen = int(found[len(found)-1].length)
				}
			}
			if inNext >= nextObservation {
				if bestLen >= minLen {
					c.split.observeMatch(bestLen)
					nextObservation = inNext + bestLen
					c.newMatchLenFreqs[bestLen]++
				} else {
					c.split.observeLiteral(in[inNext])
					nextObservation = inNext + 1
				}
			}
			c.cache[cp] = lzMatch{uint16(cp - matches), uint16(in[inNext])}
			inNext++
			cp++
			if !c.p.NoSkip && bestLen >= minMatch && bestLen >= niceLen {
				// Skip past a long match: it will be taken.
				for bestLen--; bestLen > 0; bestLen-- {
					step(inNext)
					if maxLen >= mfRequiredBytes {
						c.mf.advance(in, base, inNext-base, niceLen, niceLen, depth, &nh, nil, false)
					}
					c.cache[cp] = lzMatch{0, uint16(in[inNext])}
					inNext++
					cp++
				}
			}
			if inNext >= maxBlockEnd || cp >= cacheEnd {
				break
			}
			if c.p.SplitByCost || !c.split.readyToCheck(blockBegin, inNext, inEnd) {
				continue
			}
			if c.split.endBlockCheck(uint32(inNext - blockBegin)) {
				changeDetected = true
				break
			}
			c.mergeStats()
			prevEndBlockCheck = inNext
		}
		if changeDetected && prevEndBlockCheck >= 0 {
			// The block ends where the change was not yet seen; the
			// bytes after are taken back, with their matches kept for
			// the next block.
			origCp := cp
			blockEnd := prevEndBlockCheck
			for n := inNext - blockEnd; n > 0; n-- {
				cp--
				cp -= int(c.cache[cp].length)
			}
			prevOnlyLiterals = c.optimizeAndFlushBlock(in[blockBegin:blockEnd], cp, blockBegin == start, false)
			cp = copy(c.cache, c.cache[cp:origCp])
			c.saveStats()
			c.clearOldStats()
			blockBegin = blockEnd
		} else {
			c.mergeStats()
			prevOnlyLiterals = c.optimizeAndFlushBlock(in[blockBegin:inNext], cp, blockBegin == start, final && inNext == inEnd)
			cp = 0
			c.saveStats()
			c.initStats()
			blockBegin = inNext
		}
		if inNext == inEnd && blockBegin == inEnd {
			return
		}
	}
}

func (s *splitStats) observeLiteral(lit byte) {
	s.newObservations[(lit>>5)&0x6|lit&1]++
	s.numNewObservations++
}

func (s *splitStats) observeMatch(length int) {
	k := 0
	if length >= 9 {
		k = 1
	}
	s.newObservations[numLiteralObservationTypes+k]++
	s.numNewObservations++
}

func (s *splitStats) mergeNew() {
	for i := range s.observations {
		s.observations[i] += s.newObservations[i]
		s.newObservations[i] = 0
	}
	s.numObservations += s.numNewObservations
	s.numNewObservations = 0
}

// endBlockCheck tells whether the latest observations differ enough from
// those of the block so far for the block to end; if not, they are added
// to the block's.
func (s *splitStats) endBlockCheck(blockLength uint32) bool {
	if s.numObservations > 0 {
		var totalDelta uint32
		for i := range s.observations {
			expected := s.observations[i] * s.numNewObservations
			actual := s.newObservations[i] * s.numObservations
			if actual > expected {
				totalDelta += actual - expected
			} else {
				totalDelta += expected - actual
			}
		}
		numItems := s.numObservations + s.numNewObservations
		// Ends the block on a difference of about 200/512 per
		// observation; more for short blocks with few observations.
		cutoff := s.numNewObservations * 200 / 512 * s.numObservations
		if blockLength < 10000 && numItems < 8192 {
			cutoff += uint32(uint64(cutoff) * uint64(8192-numItems) / 8192)
		}
		if totalDelta+(blockLength/4096)*s.numObservations >= cutoff {
			return true
		}
	}
	s.mergeNew()
	return false
}

func (s *splitStats) readyToCheck(blockBegin, inNext, inEnd int) bool {
	return s.numNewObservations >= numObservationsPerCheck &&
		inNext-blockBegin >= minBlockLength && inEnd-inNext >= minBlockLength
}

func (c *Compressor) initStats() {
	c.split = splitStats{}
	clear(c.newMatchLenFreqs[:])
	clear(c.matchLenFreqs[:])
}

func (c *Compressor) mergeStats() {
	c.split.mergeNew()
	for i := range c.matchLenFreqs {
		c.matchLenFreqs[i] += c.newMatchLenFreqs[i]
		c.newMatchLenFreqs[i] = 0
	}
}

func (c *Compressor) saveStats() {
	c.prevObservations = c.split.observations
	c.prevNumObs = c.split.numObservations
}

func (c *Compressor) clearOldStats() {
	clear(c.split.observations[:])
	c.split.numObservations = 0
	clear(c.matchLenFreqs[:])
}

// chooseMinMatchLen is the shortest match worth counting for the block
// splitting, by the number of different literals in use: with few, a
// literal is cheap and so are short repeats without a match.
func chooseMinMatchLen(numUsedLiterals uint32, maxSearchDepth int) int {
	if numUsedLiterals >= uint32(len(minLens)) {
		return 3
	}
	minLen := int(minLens[numUsedLiterals])
	if maxSearchDepth < 16 {
		switch {
		case maxSearchDepth < 5:
			minLen = min(minLen, 4)
		case maxSearchDepth < 10:
			minLen = min(minLen, 5)
		default:
			minLen = min(minLen, 7)
		}
	}
	return minLen
}

func calculateMinMatchLen(data []byte, maxSearchDepth int) int {
	if len(data) < 512 {
		return minMatch
	}
	var used [256]bool
	for _, b := range data[:min(len(data), 4096)] {
		used[b] = true
	}
	var n uint32
	for _, u := range used {
		if u {
			n++
		}
	}
	return chooseMinMatchLen(n, maxSearchDepth)
}

// optimizeAndFlushBlock parses the block with the matches in the cache
// before cacheEnd, over several passes, and writes it; it tells whether
// the block was written as literals only.
func (c *Compressor) optimizeAndFlushBlock(block []byte, cacheEnd int, isFirst, isFinal bool) (onlyLiterals bool) {
	if c.p.SplitByCost {
		return c.splitAndFlush(block, cacheEnd, isFirst, isFinal)
	}
	onlyLiterals = c.optimizeBlock(block, cacheEnd, func() { c.setInitialCosts(len(block), isFirst) })
	c.flushBlock(block, onlyLiterals, isFinal)
	return onlyLiterals
}

// optimizeBlock parses the block with the matches in the cache before
// cacheEnd, over several passes, starting from the costs initial sets
// (with the literals of the block counted in c.freqs), and leaves the
// best parse in c.items, c.freqs and c.codes; it tells whether literals
// only are best.
func (c *Compressor) optimizeBlock(block []byte, cacheEnd int, initial func()) (onlyLiterals bool) {
	n := len(block)
	passes := c.p.Passes
	bestTrueCost := uint32(1<<32 - 1)
	var trueCost uint32
	staticCost := uint32(1<<32 - 1)

	c.chooseAllLiterals(block)
	onlyLitsCost := c.computeTrueCost()
	initial()

	// Past the end of the block, positions cost more than any number of
	// literals could, so that no match reaches them.
	c.costToEnd[n] = 0
	for i := n + 1; i <= n+maxMatch; i++ {
		c.costToEnd[i] = (rebaseThreshold / 2) << packedCostShift
	}

	if n <= c.p.MaxStaticLen {
		c.costsSaved = c.costs
		c.setCostsFromCodes(&staticCodes.litlenLens, &staticCodes.offsetLens)
		staticCost = c.findMinCostPath(n, cacheEnd)/bitCost + 7 // and the end of block
		c.costs = c.costsSaved
	}

	for {
		c.findMinCostPath(n, cacheEnd)
		trueCost = c.computeTrueCost()
		if trueCost+uint32(c.p.MinImprove) > bestTrueCost {
			break
		}
		bestTrueCost = trueCost
		c.costsSaved = c.costs
		c.setCostsFromCodes(&c.codes.litlenLens, &c.codes.offsetLens)
		passes--
		if passes == 0 || c.costs == c.costsSaved {
			break
		}
	}

	if min(onlyLitsCost, staticCost) < bestTrueCost {
		if onlyLitsCost < staticCost {
			c.chooseAllLiterals(block)
			c.setCostsFromCodes(&c.codes.litlenLens, &c.codes.offsetLens)
			onlyLiterals = true
		} else {
			c.setCostsFromCodes(&staticCodes.litlenLens, &staticCodes.offsetLens)
			c.findMinCostPath(n, cacheEnd)
		}
	} else if trueCost >= bestTrueCost+uint32(c.p.MinNonfinal) {
		// The last pass did worse than the best: back to the best.
		c.costs = c.costsSaved
		c.findMinCostPath(n, cacheEnd)
		c.setCostsFromCodes(&c.codes.litlenLens, &c.codes.offsetLens)
	}
	return onlyLiterals
}

// findMinCostPath works out, from the end of the block back, the cheapest
// way from each position to the end with the current costs, and sets the
// frequencies and codes from the path from the start. It returns the cost
// of the path, in bitCost units.
//
// Costs to the end are kept shifted left by packedCostShift, with a match
// length in the low bits, so that the cheapest length of a match is one
// minimum; they are kept relative to baseCost, which rises with them so
// that they fit (see find_min_cost_path_template.h).
func (c *Compressor) findMinCostPath(n int, cacheEnd int) uint32 {
	var lengthCosts [maxMatch + 1]uint32
	for l := minMatch; l <= maxMatch; l++ {
		lengthCosts[l] = c.costs.length[l]<<packedCostShift | uint32(l)
	}
	best, baseCost := uint32(0), uint32(0)
	cache, costToEnd, items := c.cache, c.costToEnd, c.items
	cp := cacheEnd
	for i := n; i > 0; {
		cp--
		numMatches := int(cache[cp].length)
		literal := uint32(cache[cp].offset)
		best += c.costs.literal[literal]
		bestItem := literal
		i--
		if numMatches > 0 {
			l := minMatch
			for _, m := range cache[cp-numMatches : cp] {
				matchLen := int(m.length)
				offset := uint32(m.offset)
				packed := minPackedCost(lengthCosts[l:matchLen+1], costToEnd[i+l:i+matchLen+1])
				matchCost := c.costs.offsetSlot[offsetSlot(offset)] + packed>>packedCostShift
				if matchCost < best {
					best = matchCost
					bestItem = packed<<optimumLenShift | offset
				}
				l = matchLen + 1
			}
			cp -= numMatches
		}
		costToEnd[i] = (best + baseCost) << packedCostShift
		items[i] = bestItem
		if best >= rebaseThreshold {
			delta := best - rebaseMargin
			best = rebaseMargin
			baseCost += delta
			delta <<= packedCostShift
			for l := minMatch; l <= maxMatch; l++ {
				lengthCosts[l] -= delta
			}
		}
	}
	c.freqs = freqs{}
	c.tallyItems(n)
	makeCodes(&c.freqs, &c.codes)
	return best + baseCost
}

// minPackedCost is the least of lc[k]+ct[k]: four minimums at once,
// which the processor works on side by side.
func minPackedCost(lc, ct []uint32) uint32 {
	lc = lc[:len(ct)]
	m0, m1, m2, m3 := ^uint32(0), ^uint32(0), ^uint32(0), ^uint32(0)
	k := 0
	for ; k+4 <= len(ct); k += 4 {
		c, l := ct[k:k+4:k+4], lc[k:k+4:k+4]
		m0 = min(m0, l[0]+c[0])
		m1 = min(m1, l[1]+c[1])
		m2 = min(m2, l[2]+c[2])
		m3 = min(m3, l[3]+c[3])
	}
	for ; k < len(ct); k++ {
		m0 = min(m0, lc[k]+ct[k])
	}
	return min(m0, m1, m2, m3)
}

func (c *Compressor) tallyItems(n int) {
	for i := 0; i < n; {
		item := c.items[i]
		if item < numLiterals {
			c.freqs.litlen[item]++
			i++
			continue
		}
		length := item >> optimumLenShift
		c.freqs.litlen[firstLenSym+uint32(lengthSlot[length])]++
		c.freqs.offset[offsetSlot(item&optimumOffsetMask)]++
		i += int(length)
	}
	c.freqs.litlen[endOfBlock]++
}

func (c *Compressor) chooseAllLiterals(block []byte) {
	c.freqs = freqs{}
	for _, b := range block {
		c.freqs.litlen[b]++
	}
	c.freqs.litlen[endOfBlock]++
	makeCodes(&c.freqs, &c.codes)
}

// computeTrueCost is the size in bits of the block, but for its first
// three bits, with the current codes and frequencies.
func (c *Compressor) computeTrueCost() uint32 {
	c.precomputeHeader()
	cost := uint32(5 + 5 + 4 + 3*c.precode.numExplicit)
	for sym, f := range c.precode.freqs {
		cost += f * (uint32(c.precode.lens[sym]) + uint32(extraPrecodeBits[sym]))
	}
	for sym := 0; sym < firstLenSym; sym++ {
		cost += c.freqs.litlen[sym] * uint32(c.codes.litlenLens[sym])
	}
	for slot, x := range extraLengthBits {
		sym := firstLenSym + slot
		cost += c.freqs.litlen[sym] * (uint32(c.codes.litlenLens[sym]) + uint32(x))
	}
	for slot, x := range extraOffsetBits {
		cost += c.freqs.offset[slot] * (uint32(c.codes.offsetLens[slot]) + uint32(x))
	}
	return cost
}

func (c *Compressor) setCostsFromCodes(litlenLens *[numLitlenSyms]uint8, offsetLens *[numOffsetSyms]uint8) {
	for i := range numLiterals {
		bits := uint32(litlenLens[i])
		if bits == 0 {
			bits = literalNostatBits
		}
		c.costs.literal[i] = bits * bitCost
	}
	for l := minMatch; l <= maxMatch; l++ {
		slot := lengthSlot[l]
		bits := uint32(litlenLens[firstLenSym+int(slot)])
		if bits == 0 {
			bits = lengthNostatBits
		}
		c.costs.length[l] = (bits + uint32(extraLengthBits[slot])) * bitCost
	}
	for slot := range offsetSlotBase {
		bits := uint32(offsetLens[slot])
		if bits == 0 {
			bits = offsetNostatBits
		}
		c.costs.offsetSlot[slot] = (bits + uint32(extraOffsetBits[slot])) * bitCost
	}
}

// chooseDefaultLitlenCosts picks costs for literals and length symbols
// from the literals in use (counted in c.freqs) and how many matches the
// block splitting saw.
func (c *Compressor) chooseDefaultLitlenCosts(blockLength int) (litCost, lenSymCost uint32) {
	var numUsedLiterals uint32
	cutoff := uint32(blockLength) >> 11 // literals used very rarely do not count
	for i := range numLiterals {
		if c.freqs.litlen[i] > cutoff {
			numUsedLiterals++
		}
	}
	if numUsedLiterals == 0 {
		numUsedLiterals = 1
	}
	literalFreq := int64(blockLength)
	var matchFreq int64
	for i := chooseMinMatchLen(numUsedLiterals, c.p.SearchDepth); i < len(c.matchLenFreqs); i++ {
		matchFreq += int64(c.matchLenFreqs[i])
		literalFreq -= int64(i) * int64(c.matchLenFreqs[i])
	}
	literalFreq = max(literalFreq, 0)
	k := 0 // few matches
	if matchFreq > literalFreq {
		k = 2 // many
	} else if matchFreq*4 > literalFreq {
		k = 1 // neither
	}
	return uint32(defaultLitlenCosts[k].usedLitsToLitCost[numUsedLiterals]), defaultLitlenCosts[k].lenSymCost
}

func defaultLengthCost(l int, lenSymCost uint32) uint32 {
	return lenSymCost + uint32(extraLengthBits[lengthSlot[l]])*bitCost
}

func defaultOffsetSlotCost(slot int) uint32 {
	return 4*bitCost + 907*bitCost/1000 + uint32(extraOffsetBits[slot])*bitCost
}

func (c *Compressor) setDefaultCosts(litCost, lenSymCost uint32) {
	for i := range numLiterals {
		c.costs.literal[i] = litCost
	}
	for l := minMatch; l <= maxMatch; l++ {
		c.costs.length[l] = defaultLengthCost(l, lenSymCost)
	}
	for slot := range offsetSlotBase {
		c.costs.offsetSlot[slot] = defaultOffsetSlotCost(slot)
	}
}

// adjustCosts moves the costs left from the previous block toward the
// default ones, the more so the more the block differs from it.
func (c *Compressor) adjustCosts(litCost, lenSymCost uint32) {
	var totalDelta uint64
	for i := range c.split.observations {
		prev := uint64(c.prevObservations[i]) * uint64(c.split.numObservations)
		cur := uint64(c.split.observations[i]) * uint64(c.prevNumObs)
		if prev > cur {
			totalDelta += prev - cur
		} else {
			totalDelta += cur - prev
		}
	}
	cutoff := uint64(c.prevNumObs) * uint64(c.split.numObservations) * 200 / 512
	var change int
	switch {
	case totalDelta > 3*cutoff:
		c.setDefaultCosts(litCost, lenSymCost)
		return
	case 4*totalDelta > 9*cutoff:
		change = 3
	case 2*totalDelta > 3*cutoff:
		change = 2
	case 2*totalDelta > cutoff:
		change = 1
	}
	adjust := func(p *uint32, def uint32) {
		switch change {
		case 0:
			*p = (def + 3**p) / 4
		case 1:
			*p = (def + *p) / 2
		case 2:
			*p = (5*def + 3**p) / 8
		default:
			*p = (3*def + *p) / 4
		}
	}
	for i := range numLiterals {
		adjust(&c.costs.literal[i], litCost)
	}
	for l := minMatch; l <= maxMatch; l++ {
		adjust(&c.costs.length[l], defaultLengthCost(l, lenSymCost))
	}
	for slot := range offsetSlotBase {
		adjust(&c.costs.offsetSlot[slot], defaultOffsetSlotCost(slot))
	}
}

func (c *Compressor) setInitialCosts(blockLength int, isFirst bool) {
	litCost, lenSymCost := c.chooseDefaultLitlenCosts(blockLength)
	if isFirst {
		c.setDefaultCosts(litCost, lenSymCost)
	} else {
		c.adjustCosts(litCost, lenSymCost)
	}
}
