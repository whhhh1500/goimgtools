package zopfli

// Sizes of blocks and the code lengths of dynamic ones (deflate.c).

func fixedTree(llLengths, dLengths []uint32) {
	for i := 0; i < 144; i++ {
		llLengths[i] = 8
	}
	for i := 144; i < 256; i++ {
		llLengths[i] = 9
	}
	for i := 256; i < 280; i++ {
		llLengths[i] = 7
	}
	for i := 280; i < 288; i++ {
		llLengths[i] = 8
	}
	for i := 0; i < 32; i++ {
		dLengths[i] = 5
	}
}

func symbolSizeSmall(llLengths, dLengths []uint32, s *lz77Store, lstart, lend int) uint64 {
	var result uint64
	for i := lstart; i < lend; i++ {
		if s.dists[i] == 0 {
			result += uint64(llLengths[s.litlens[i]])
			continue
		}
		ls, ds := lengthSymbol(int(s.litlens[i])), distSymbol(int(s.dists[i]))
		result += uint64(llLengths[ls]) + uint64(dLengths[ds]) + uint64(lengthSymbolExtra(ls)) + uint64(distSymbolExtra(ds))
	}
	return result + uint64(llLengths[256])
}

// symbolSizeGivenCounts is the size of the symbols of items lstart to lend
// of s, whose counts are given; with s nil, of the symbols counted. (For
// few items Zopfli adds up the items, which comes to the same.)
func symbolSizeGivenCounts(llCounts, dCounts []uint64, llLengths, dLengths []uint32, s *lz77Store, lstart, lend int) uint64 {
	if s != nil && lstart+numLL*3 > lend {
		return symbolSizeSmall(llLengths, dLengths, s, lstart, lend)
	}
	var result uint64
	for i := 0; i < 256; i++ {
		result += uint64(llLengths[i]) * llCounts[i]
	}
	for i := 257; i < 286; i++ {
		result += uint64(llLengths[i]) * llCounts[i]
		result += uint64(lengthSymbolExtra(i)) * llCounts[i]
	}
	for i := 0; i < 30; i++ {
		result += uint64(dLengths[i]) * dCounts[i]
		result += uint64(distSymbolExtra(i)) * dCounts[i]
	}
	return result + uint64(llLengths[256])
}

func symbolSize(llLengths, dLengths []uint32, s *lz77Store, lstart, lend int) uint64 {
	if lstart+numLL*3 > lend {
		return symbolSizeSmall(llLengths, dLengths, s, lstart, lend)
	}
	var llCounts [numLL]uint64
	var dCounts [numD]uint64
	s.histogram(lstart, lend, llCounts[:], dCounts[:])
	return symbolSizeGivenCounts(llCounts[:], dCounts[:], llLengths, dLengths, s, lstart, lend)
}

func absDiff(x, y uint64) uint64 {
	if x > y {
		return x - y
	}
	return y - x
}

// optimizeHuffmanForRle evens out stretches of nearly equal counts, so
// that the code lengths run-length code well.
func optimizeHuffmanForRle(length int, counts []uint64) {
	for ; length >= 0; length-- {
		if length == 0 {
			return
		}
		if counts[length-1] != 0 {
			break
		}
	}
	var goodAll [numLL]bool // length is numLL at most
	good := goodAll[:length]
	symbol := counts[0]
	stride := 0
	for i := 0; i < length+1; i++ {
		if i == length || counts[i] != symbol {
			if (symbol == 0 && stride >= 5) || (symbol != 0 && stride >= 7) {
				for k := 0; k < stride; k++ {
					good[i-k-1] = true
				}
			}
			stride = 1
			if i != length {
				symbol = counts[i]
			}
		} else {
			stride++
		}
	}
	stride = 0
	limit := counts[0]
	var sum uint64
	for i := 0; i < length+1; i++ {
		if i == length || good[i] || absDiff(counts[i], limit) >= 4 {
			if stride >= 4 || (stride >= 3 && sum == 0) {
				count := int((sum + uint64(stride/2)) / uint64(stride))
				if count < 1 {
					count = 1
				}
				if sum == 0 {
					count = 0
				}
				for k := 0; k < stride; k++ {
					counts[i-k-1] = uint64(count)
				}
			}
			stride = 0
			sum = 0
			switch {
			case i < length-3:
				limit = (counts[i] + counts[i+1] + counts[i+2] + counts[i+3] + 2) / 4
			case i < length:
				limit = counts[i]
			default:
				limit = 0
			}
		}
		stride++
		if i != length {
			sum += counts[i]
		}
	}
}

func tryOptimizeHuffmanForRle(s *lz77Store, lstart, lend int, llCounts, dCounts []uint64, llLengths, dLengths []uint32) float64 {
	treeSize := float64(calculateTreeSize(llLengths, dLengths))
	dataSize := float64(symbolSizeGivenCounts(llCounts, dCounts, llLengths, dLengths, s, lstart, lend))
	var ll2 [numLL]uint64
	var d2 [numD]uint64
	copy(ll2[:], llCounts)
	copy(d2[:], dCounts)
	optimizeHuffmanForRle(numLL, ll2[:])
	optimizeHuffmanForRle(numD, d2[:])
	var llLengths2 [numLL]uint32
	var dLengths2 [numD]uint32
	calculateBitLengths(ll2[:], 15, llLengths2[:])
	calculateBitLengths(d2[:], 15, dLengths2[:])
	patchDistanceCodes(dLengths2[:])
	treeSize2 := float64(calculateTreeSize(llLengths2[:], dLengths2[:]))
	dataSize2 := float64(symbolSizeGivenCounts(llCounts, dCounts, llLengths2[:], dLengths2[:], s, lstart, lend))
	if treeSize2+dataSize2 < treeSize+dataSize {
		copy(llLengths, llLengths2[:])
		copy(dLengths, dLengths2[:])
		return treeSize2 + dataSize2
	}
	return treeSize + dataSize
}

func dynamicLengths(s *lz77Store, lstart, lend int, llLengths, dLengths []uint32) float64 {
	var llCounts [numLL]uint64
	var dCounts [numD]uint64
	s.histogram(lstart, lend, llCounts[:], dCounts[:])
	llCounts[256] = 1
	calculateBitLengths(llCounts[:], 15, llLengths)
	calculateBitLengths(dCounts[:], 15, dLengths)
	patchDistanceCodes(dLengths)
	return tryOptimizeHuffmanForRle(s, lstart, lend, llCounts[:], dCounts[:], llLengths, dLengths)
}

// calculateBlockSize is the size in bits of items lstart to lend as a
// block of type btype (0 stored, 1 fixed, 2 dynamic).
func calculateBlockSize(s *lz77Store, lstart, lend, btype int) float64 {
	var llLengths [numLL]uint32
	var dLengths [numD]uint32
	result := 3.0
	switch btype {
	case 0:
		length := s.byteRange(lstart, lend)
		blocks := length / 65535
		if length%65535 != 0 {
			blocks++
		}
		return float64(blocks*5*8 + length*8)
	case 1:
		fixedTree(llLengths[:], dLengths[:])
		result += float64(symbolSize(llLengths[:], dLengths[:], s, lstart, lend))
	default:
		result += dynamicLengths(s, lstart, lend, llLengths[:], dLengths[:])
	}
	return result
}

// dynamicBlockSize is calculateBlockSize of a parse as a dynamic block,
// from the counts of its symbols.
func dynamicBlockSize(c *symbolCounts) float64 {
	ll, d := c.ll, c.d
	ll[256] = 1
	var llLengths [numLL]uint32
	var dLengths [numD]uint32
	calculateBitLengths(ll[:], 15, llLengths[:])
	calculateBitLengths(d[:], 15, dLengths[:])
	patchDistanceCodes(dLengths[:])
	return 3 + tryOptimizeHuffmanForRle(nil, 0, 0, ll[:], d[:], llLengths[:], dLengths[:])
}

// fixedBlockSize is calculateBlockSize of a parse as a fixed block, from
// the counts of its symbols.
func fixedBlockSize(c *symbolCounts) float64 {
	var llLengths [numLL]uint32
	var dLengths [numD]uint32
	fixedTree(llLengths[:], dLengths[:])
	return 3 + float64(symbolSizeGivenCounts(c.ll[:], c.d[:], llLengths[:], dLengths[:], nil, 0, 0))
}

func calculateBlockSizeAutoType(s *lz77Store, lstart, lend int) float64 {
	return blockSizeAutoType(s, lstart, lend, s.size())
}

// blockSizeAutoType is calculateBlockSizeAutoType for a store of size items
// (the fixed code is tried for small stores only): items lstart to lend of
// s may be such a store on its own.
func blockSizeAutoType(s *lz77Store, lstart, lend, size int) float64 {
	unc := calculateBlockSize(s, lstart, lend, 0)
	fixed := unc
	if size <= 1000 {
		fixed = calculateBlockSize(s, lstart, lend, 1)
	}
	dyn := calculateBlockSize(s, lstart, lend, 2)
	if unc < fixed && unc < dyn {
		return unc
	}
	if fixed < dyn {
		return fixed
	}
	return dyn
}

// brotliEvenForRle evens out stretches of nearly equal counts as Brotli
// does (OptimizeHuffmanCountsForRle, as ECT uses it), in 24.8 fixed point.
func brotliEvenForRle(length int, counts []uint64) {
	for ; length > 0 && counts[length-1] == 0; length-- {
	}
	if length == 0 {
		return
	}
	var goodAll [numLL]bool
	good := goodAll[:length]
	symbol, stride := counts[0], 0
	for i := 0; i < length+1; i++ {
		if i == length || counts[i] != symbol {
			if (symbol == 0 && stride >= 5) || stride >= 7 {
				for k := 0; k < stride; k++ {
					good[i-k-1] = true
				}
			}
			stride = 1
			if i != length {
				symbol = counts[i]
			}
		} else {
			stride++
		}
	}
	const streakLimit = 1240
	stride = 0
	limit := int64(256*(counts[0]+counts[1]+counts[2]))/3 + 420
	var sum int64
	for i := 0; i < length+1; i++ {
		if i == length || good[i] || (i > 0 && good[i-1]) || abs64(int64(256*counts[i])-limit) >= streakLimit {
			if stride >= 4 {
				count := (sum + int64(stride/2)) / int64(stride)
				if count < 1 && sum != 0 {
					count = 1
				}
				for k := 0; k < stride; k++ {
					counts[i-k-1] = uint64(count)
				}
			}
			stride, sum = 0, 0
			if i < length-2 {
				limit = int64(256*(counts[i]+counts[i+1]+counts[i+2]))/3 + 420
			} else if i < length {
				limit = int64(256 * counts[i])
			} else {
				limit = 0
			}
		}
		stride++
		if i != length {
			sum += int64(counts[i])
			if stride >= 4 {
				limit = (256*sum + int64(stride/2)) / int64(stride)
			}
			if stride == 4 {
				limit += 120
			}
		}
	}
}

func abs64(v int64) int64 {
	if v < 0 {
		return -v
	}
	return v
}

// countsLengths sets the code lengths of a dynamic block of the symbols
// counted, as ECT chooses them, and returns the size of its header and
// symbols in bits: the smallest of codes made from the counts evened out
// as Zopfli evens them for the header, as they are, or evened out as
// Brotli does; then of those limited to 14 bits, 13, and down to 9, while
// the block gets no larger. Zopfli's choice (see dynamicLengths) is among
// them: never larger.
func countsLengths(llCounts *[numLL]uint64, dCounts *[numD]uint64, llLengths, dLengths []uint32) uint64 {
	counts := [3][numLL]uint64{*llCounts, *llCounts, *llCounts}
	dcounts := [3][numD]uint64{*dCounts, *dCounts, *dCounts}
	optimizeHuffmanForRle(numLL, counts[1][:])
	optimizeHuffmanForRle(numD, dcounts[1][:])
	brotliEvenForRle(numLL, counts[2][:])
	brotliEvenForRle(numD, dcounts[2][:])
	var ll [numLL]uint32
	var d [numD]uint32
	size := func(set, maxBits int) (uint64, bool) {
		if !lengthLimitedCodeLengths(counts[set][:], maxBits, ll[:]) || !lengthLimitedCodeLengths(dcounts[set][:], maxBits, d[:]) {
			return 0, false
		}
		patchDistanceCodes(d[:])
		return symbolSizeGivenCounts(llCounts[:], dCounts[:], ll[:], d[:], nil, 0, 0) + uint64(treeSize(ll[:], d[:], allWays)), true
	}
	best, bestSet := uint64(0), -1
	for set := range counts {
		if n, ok := size(set, 15); ok && (bestSet < 0 || n < best) {
			best, bestSet = n, set
			copy(llLengths, ll[:])
			copy(dLengths, d[:])
		}
	}
	for maxBits := 14; maxBits >= 9; maxBits-- {
		n, ok := size(bestSet, maxBits)
		if !ok || n > best {
			break
		}
		if n < best {
			best = n
			copy(llLengths, ll[:])
			copy(dLengths, d[:])
		}
	}
	return best
}

// writeMore writes items lstart to lend as the header and symbols of a
// dynamic block as ECT writes them (see Options.MoreCodes): with the codes
// of countsLengths, after replacing, as ECT's ReplaceBadCodes does, each
// match of 3 to 7 bytes that costs more under them than its bytes as
// literals (all of which have codes) by those literals, the codes made
// again and the matches looked at again, until none is replaced; the
// smallest of the blocks made on the way is written (ECT writes the last).
// A match replaced stays replaced, so the replaced ones are one bit each.
// room is two bits an item at least.
func (s *lz77Store) writeMore(lstart, lend int, room []uint64, w *bitWriter) {
	n := lend - lstart
	k := (n + 63) / 64
	marks, bestMarks := room[:k], room[k:2*k]
	clear(marks)
	clear(bestMarks)
	marked := func(m []uint64, i int) bool { return m[i>>6]&(1<<(i&63)) != 0 }
	counts := func() (ll [numLL]uint64, d [numD]uint64) {
		for i := 0; i < n; i++ {
			l, dist := s.litlens[lstart+i], s.dists[lstart+i]
			switch {
			case dist == 0:
				ll[l]++
			case marked(marks, i):
				p := int(s.pos[lstart+i])
				for _, b := range s.data[p : p+int(l)] {
					ll[b]++
				}
			default:
				ll[lengthSymbol(int(l))]++
				d[distSymbol(int(dist))]++
			}
		}
		ll[256] = 1
		return
	}
	var llLengths, bestLL [numLL]uint32
	var dLengths, bestD [numD]uint32
	llc, dc := counts()
	best := countsLengths(&llc, &dc, bestLL[:], bestD[:])
	llLengths, dLengths = bestLL, bestD
	for {
		replaced := false
		for i := 0; i < n; i++ {
			l, dist := int(s.litlens[lstart+i]), int(s.dists[lstart+i])
			if dist == 0 || l > 7 || marked(marks, i) {
				continue
			}
			price := uint32(0)
			p := int(s.pos[lstart+i])
			for _, b := range s.data[p : p+l] {
				if llLengths[b] == 0 {
					price = ^uint32(0)
					break
				}
				price += llLengths[b]
			}
			if price < llLengths[lengthSymbol(l)]+uint32(lengthExtraBits(l))+uint32(distExtraBits(dist))+dLengths[distSymbol(dist)] {
				marks[i>>6] |= 1 << (i & 63)
				replaced = true
			}
		}
		if !replaced {
			break
		}
		llc, dc = counts()
		if size := countsLengths(&llc, &dc, llLengths[:], dLengths[:]); size < best {
			best, bestLL, bestD = size, llLengths, dLengths
			copy(bestMarks, marks)
		}
	}
	var llSymbols [numLL]uint32
	var dSymbols [numD]uint32
	addDynamicTree(bestLL[:], bestD[:], allWays, w)
	lengthsToSymbols(bestLL[:], 15, llSymbols[:])
	lengthsToSymbols(bestD[:], 15, dSymbols[:])
	for i := 0; i < n; i++ {
		l, dist := int(s.litlens[lstart+i]), int(s.dists[lstart+i])
		if dist != 0 && marked(bestMarks, i) {
			p := int(s.pos[lstart+i])
			for _, b := range s.data[p : p+l] {
				addItem(int(b), 0, llSymbols[:], bestLL[:], dSymbols[:], bestD[:], w)
			}
			continue
		}
		addItem(l, dist, llSymbols[:], bestLL[:], dSymbols[:], bestD[:], w)
	}
	w.addHuffmanBits(llSymbols[256], bestLL[256])
}
