package deflate

// Choosing a dynamic block's codes among more (Params.MoreCodes; not in
// libdeflate; as the zopfli package's MoreCodes does, after ECT): the
// codeword lengths are made from the counts as they are, evened out as
// zopfli evens them (evenForRLE) and as Brotli does (brotliEven), and
// limited to the most bits libdeflate allows down to 9; the header is
// written the way it is smallest, the run codes 16, 17 and 18 used or not
// and repeats of 7 and 8 written as two; the smallest block is kept. All in
// integers: the same on every processor.

// Ways of coding the codeword lengths, as the zopfli package's encodeTree:
// the run codes used, and the repeats of 7 and 8 written as two (4 and 3,
// 4 and 4) rather than 6 and one or two.
const (
	use16 = 1 << iota
	use17
	use18
	fuse7
	fuse8
)

// precodeItemsWay codes the codeword lengths with the precode the way
// given, into items as computePrecodeItems makes them, and counts the
// precode symbols in freqs. With use16, use17 and use18 it codes them as
// computePrecodeItems does.
func precodeItemsWay(lens []uint8, freqs *[numPrecodeSyms]uint32, items []uint32, way int) int {
	*freqs = [numPrecodeSyms]uint32{}
	n := 0
	put := func(sym, extra uint32) {
		freqs[sym]++
		items[n] = sym | extra<<5
		n++
	}
	for i := 0; i < len(lens); {
		l := lens[i]
		count := 1
		if way&use16 != 0 || (l == 0 && way&(use17|use18) != 0) {
			for i+count < len(lens) && lens[i+count] == l {
				count++
			}
		}
		i += count
		if l == 0 && count >= 3 {
			if way&use18 != 0 {
				for count >= 11 {
					c2 := min(count, 138)
					put(18, uint32(c2-11))
					count -= c2
				}
			}
			if way&use17 != 0 {
				for count >= 3 {
					c2 := min(count, 10)
					put(17, uint32(c2-3))
					count -= c2
				}
			}
		}
		if way&use16 != 0 && count >= 4 {
			put(uint32(l), 0)
			count--
			for count >= 3 {
				if (count == 8 && way&fuse8 != 0) || (count == 7 && way&fuse7 != 0) {
					put(16, 1)
					put(16, uint32(count-7))
					count = 0
					break
				}
				c2 := min(count, 6)
				put(16, uint32(c2-3))
				count -= c2
			}
		}
		for ; count > 0; count-- {
			put(uint32(l), 0)
		}
	}
	return n
}

// precomputeHeaderWay is precomputeHeaderOf with the codeword lengths
// coded the way given.
func precomputeHeaderWay(cd *codes, pc *precode, way int) {
	pc.numLitlen = numLitlenSyms
	for pc.numLitlen > 257 && cd.litlenLens[pc.numLitlen-1] == 0 {
		pc.numLitlen--
	}
	pc.numOffset = numOffsetSyms
	for pc.numOffset > 1 && cd.offsetLens[pc.numOffset-1] == 0 {
		pc.numOffset--
	}
	var lens [numLitlenSyms + numOffsetSyms]uint8
	copy(lens[:], cd.litlenLens[:pc.numLitlen])
	copy(lens[pc.numLitlen:], cd.offsetLens[:pc.numOffset])
	pc.numItems = precodeItemsWay(lens[:pc.numLitlen+pc.numOffset], &pc.freqs, pc.items[:], way)
	makeHuffmanCode(maxPreCodewordLen, pc.freqs[:], pc.lens[:], pc.codewords[:])
	pc.numExplicit = numPrecodeSyms
	for pc.numExplicit > 4 && pc.lens[precodeLensPermutation[pc.numExplicit-1]] == 0 {
		pc.numExplicit--
	}
}

// headerBitsOf is the size of the header a dynamic block writes with pc,
// its first three bits included.
func headerBitsOf(pc *precode) uint32 {
	b := uint32(3 + 5 + 5 + 4 + 3*pc.numExplicit)
	for sym, n := range pc.freqs {
		b += n * (uint32(extraPrecodeBits[sym]) + uint32(pc.lens[sym]))
	}
	return b
}

// dataBits is the size of the symbols of f, with their extra bits and the
// end of block, coded with cd.
func dataBits(f *freqs, cd *codes) uint32 {
	var b uint32
	for sym := range numLiterals {
		b += f.litlen[sym] * uint32(cd.litlenLens[sym])
	}
	b += uint32(cd.litlenLens[endOfBlock])
	for slot, x := range extraLengthBits {
		sym := firstLenSym + slot
		b += f.litlen[sym] * (uint32(x) + uint32(cd.litlenLens[sym]))
	}
	for slot, x := range extraOffsetBits {
		b += f.offset[slot] * (uint32(x) + uint32(cd.offsetLens[slot]))
	}
	return b
}

// moreCodes sets c.codes and c.precode to the smallest dynamic block of
// the symbols of f among those above, and returns its size in bits.
func (c *Compressor) moreCodes(f *freqs) uint32 {
	var counts [3]freqs
	counts[0], counts[1], counts[2] = *f, *f, *f
	evenForRLE(counts[1].litlen[:])
	evenForRLE(counts[1].offset[:])
	brotliEven(counts[2].litlen[:])
	brotliEven(counts[2].offset[:])
	best := uint32(1<<32 - 1)
	var cd codes
	var pc precode
	for k := range counts {
		for maxBits := maxOffsetCodewordLen; maxBits >= 9; maxBits-- {
			makeHuffmanCode(min(maxBits, maxLitlenCodewordLen), counts[k].litlen[:], cd.litlenLens[:], cd.litlenCodewords[:])
			makeHuffmanCode(maxBits, counts[k].offset[:], cd.offsetLens[:], cd.offsetCodewords[:])
			data := dataBits(f, &cd)
			if data >= best {
				continue
			}
			bestWay, bestHeader := 0, uint32(1<<32-1)
			for way := 0; way < use18<<1; way++ {
				precomputeHeaderWay(&cd, &pc, way)
				if h := headerBitsOf(&pc); h < bestHeader {
					bestWay, bestHeader = way, h
				}
			}
			if bestWay&use16 != 0 {
				w := bestWay
				for _, fuse := range [...]int{fuse7, fuse8, fuse7 | fuse8} {
					precomputeHeaderWay(&cd, &pc, w|fuse)
					if h := headerBitsOf(&pc); h < bestHeader {
						bestWay, bestHeader = w|fuse, h
					}
				}
			}
			if data+bestHeader < best {
				best = data + bestHeader
				c.codes = cd
				precomputeHeaderWay(&c.codes, &c.precode, bestWay)
			}
		}
	}
	return best
}

// brotliEven evens out stretches of nearly equal counts as Brotli does
// (OptimizeHuffmanCountsForRle, as ECT uses it), in 24.8 fixed point; as
// the zopfli package's brotliEvenForRle.
func brotliEven(counts []uint32) {
	length := len(counts)
	for ; length > 0 && counts[length-1] == 0; length-- {
	}
	if length == 0 {
		return
	}
	var goodAll [numLitlenSyms]bool
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
	limit := int64(256*(int64(counts[0])+int64(counts[1])+int64(counts[2])))/3 + 420
	var sum int64
	for i := 0; i < length+1; i++ {
		if i == length || good[i] || (i > 0 && good[i-1]) || abs64(256*int64(counts[i])-limit) >= streakLimit {
			if stride >= 4 {
				count := (sum + int64(stride/2)) / int64(stride)
				if count < 1 && sum != 0 {
					count = 1
				}
				for k := 0; k < stride; k++ {
					counts[i-k-1] = uint32(count)
				}
			}
			stride, sum = 0, 0
			if i < length-2 {
				limit = 256*(int64(counts[i])+int64(counts[i+1])+int64(counts[i+2]))/3 + 420
			} else if i < length {
				limit = 256 * int64(counts[i])
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
