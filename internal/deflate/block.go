package deflate

import "encoding/binary"

// bitWriter writes bits least significant first.
type bitWriter struct {
	out   []byte
	buf   uint64
	count uint
}

// add writes the n low bits of bits (n <= 32; the bits above must be 0).
func (w *bitWriter) add(bits uint32, n uint) {
	w.buf |= uint64(bits) << w.count
	w.count += n
	if w.count >= 32 {
		w.out = binary.LittleEndian.AppendUint32(w.out, uint32(w.buf))
		w.buf >>= 32
		w.count -= 32
	}
}

// align pads to a byte with zero bits and writes out what is held.
func (w *bitWriter) align() {
	for w.count > 0 {
		w.out = append(w.out, byte(w.buf))
		w.buf >>= 8
		w.count -= min(w.count, 8)
	}
	w.buf = 0
}

// computePrecodeItems codes the codeword lengths with the precode: runs of
// a length repeated (16), and of zeros (17 and 18), with their extra bits
// above the low five; it counts the precode symbols in freqs.
func computePrecodeItems(lens []uint8, freqs *[numPrecodeSyms]uint32, items []uint32) int {
	*freqs = [numPrecodeSyms]uint32{}
	n := 0
	put := func(sym, extra uint32) {
		freqs[sym]++
		items[n] = sym | extra<<5
		n++
	}
	runStart := 0
	for runStart < len(lens) {
		l := lens[runStart]
		runEnd := runStart + 1
		for runEnd < len(lens) && lens[runEnd] == l {
			runEnd++
		}
		if l == 0 {
			for runEnd-runStart >= 11 {
				x := min(runEnd-runStart-11, 0x7f)
				put(18, uint32(x))
				runStart += 11 + x
			}
			if runEnd-runStart >= 3 {
				x := min(runEnd-runStart-3, 0x7)
				put(17, uint32(x))
				runStart += 3 + x
			}
		} else if runEnd-runStart >= 4 {
			put(uint32(l), 0)
			runStart++
			for {
				x := min(runEnd-runStart-3, 0x3)
				put(16, uint32(x))
				runStart += 3 + x
				if runEnd-runStart < 3 {
					break
				}
			}
		}
		for ; runStart < runEnd; runStart++ {
			put(uint32(l), 0)
		}
	}
	return n
}

// precomputeHeader works out the header of a dynamic block with the
// current codes.
func (c *Compressor) precomputeHeader() { precomputeHeaderOf(&c.codes, &c.precode) }

// precomputeHeaderOf works out the header of a dynamic block with cd.
func precomputeHeaderOf(cd *codes, pc *precode) {
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
	pc.numItems = computePrecodeItems(lens[:pc.numLitlen+pc.numOffset], &pc.freqs, pc.items[:])
	makeHuffmanCode(maxPreCodewordLen, pc.freqs[:], pc.lens[:], pc.codewords[:])
	pc.numExplicit = numPrecodeSyms
	for pc.numExplicit > 4 && pc.lens[precodeLensPermutation[pc.numExplicit-1]] == 0 {
		pc.numExplicit--
	}
}

// flushBlock writes the block (with the items of the parse, or as literals
// only) as whichever is smallest of a dynamic block, a static block and
// stored blocks.
func (c *Compressor) flushBlock(block []byte, onlyLiterals, isFinal bool) {
	w := &c.w
	c.precomputeHeader()
	pc := &c.precode
	f := &c.freqs
	dynamicCost := dynamicBits(f, &c.codes, pc)
	if c.p.SplitByCost {
		// Beyond libdeflate, as zopfli does (TryOptimizeHuffmanForRle):
		// counts evened out along runs make code lengths whose header
		// codes smaller, which can pay for the data coding a little worse;
		// the smaller block is kept.
		alt := *f
		evenForRLE(alt.litlen[:])
		evenForRLE(alt.offset[:])
		var cd codes
		var ap precode
		makeCodes(&alt, &cd)
		precomputeHeaderOf(&cd, &ap)
		if b := dynamicBits(f, &cd, &ap); b < dynamicCost {
			c.codes, c.precode, dynamicCost = cd, ap, b
		}
	}
	staticCost := uint32(3)
	for sym := range numLiterals {
		if sym < 144 {
			staticCost += f.litlen[sym] * 8
		} else {
			staticCost += f.litlen[sym] * 9
		}
	}
	staticCost += 7
	for slot, x := range extraLengthBits {
		sym := firstLenSym + slot
		staticCost += f.litlen[sym] * (uint32(x) + uint32(staticCodes.litlenLens[sym]))
	}
	for slot, x := range extraOffsetBits {
		staticCost += f.offset[slot] * (uint32(x) + 5)
	}
	n := len(block)
	storedCost := uint32(3) + uint32((8-(w.count+3)%8)%8) + 32 + 40*uint32((n+0xffff-1)/0xffff-1) + 8*uint32(n)

	final := uint32(0)
	if isFinal {
		final = 1
	}
	best := min(dynamicCost, staticCost, storedCost)
	if best == storedCost {
		for len(block) > 0 {
			k := min(len(block), 0xffff)
			last := uint32(0)
			if k == len(block) {
				last = final
			}
			w.add(last, 3)
			w.align()
			w.out = binary.LittleEndian.AppendUint16(w.out, uint16(k))
			w.out = binary.LittleEndian.AppendUint16(w.out, ^uint16(k))
			w.out = append(w.out, block[:k]...)
			block = block[k:]
		}
		return
	}

	codes := &c.codes
	if best == staticCost {
		codes = staticCodes
		w.add(final|1<<1, 3)
	} else {
		w.add(final|2<<1, 3)
		w.add(uint32(pc.numLitlen-257), 5)
		w.add(uint32(pc.numOffset-1), 5)
		w.add(uint32(pc.numExplicit-4), 4)
		for _, sym := range precodeLensPermutation[:pc.numExplicit] {
			w.add(uint32(pc.lens[sym]), 3)
		}
		for _, item := range pc.items[:pc.numItems] {
			sym := item & 0x1f
			w.add(pc.codewords[sym], uint(pc.lens[sym]))
			w.add(item>>5, uint(extraPrecodeBits[sym]))
		}
	}

	// The codeword of each length with its extra bits.
	for l := minMatch; l <= maxMatch; l++ {
		slot := lengthSlot[l]
		sym := firstLenSym + int(slot)
		c.lenCodewords[l] = codes.litlenCodewords[sym] | (uint32(l)-lengthSlotBase[slot])<<codes.litlenLens[sym]
		c.lenLens[l] = codes.litlenLens[sym] + extraLengthBits[slot]
	}

	if onlyLiterals {
		for _, b := range block {
			w.add(codes.litlenCodewords[b], uint(codes.litlenLens[b]))
		}
	} else {
		for i := 0; i < n; {
			item := c.items[i]
			if item < numLiterals {
				w.add(codes.litlenCodewords[item], uint(codes.litlenLens[item]))
				i++
				continue
			}
			length := item >> optimumLenShift
			offset := item & optimumOffsetMask
			slot := offsetSlot(offset)
			w.add(c.lenCodewords[length], uint(c.lenLens[length]))
			w.add(codes.offsetCodewords[slot], uint(codes.offsetLens[slot]))
			w.add(offset-offsetSlotBase[slot], uint(extraOffsetBits[slot]))
			i += int(length)
		}
	}
	w.add(codes.litlenCodewords[endOfBlock], uint(codes.litlenLens[endOfBlock]))
}

// dynamicBits is the size of a dynamic block of the frequencies f coded
// with cd, whose header is pc, in bits.
func dynamicBits(f *freqs, cd *codes, pc *precode) uint32 {
	b := uint32(3 + 5 + 5 + 4 + 3*pc.numExplicit)
	for sym, n := range pc.freqs {
		b += n * (uint32(extraPrecodeBits[sym]) + uint32(pc.lens[sym]))
	}
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

// evenForRLE replaces runs of nearly equal counts by their mean, so that
// their code lengths come out equal and the header can code them as
// repeats (zopfli's OptimizeHuffmanForRle, src/zopfli/deflate.c). A count
// that is not zero stays at least 1, so every symbol in use keeps a code; a
// stretch of zeros stays zero, and so do the zeros at the end.
func evenForRLE(counts []uint32) {
	length := len(counts)
	// The zeros at the end are left alone.
	for length > 0 && counts[length-1] == 0 {
		length--
	}
	if length == 0 {
		return
	}
	// Runs that already code well as repeats: of zeros at least 5 long,
	// of others at least 7.
	good := make([]bool, length)
	symbol, stride := counts[0], 0
	for i := 0; i <= length; i++ {
		if i == length || counts[i] != symbol {
			if symbol == 0 && stride >= 5 || symbol != 0 && stride >= 7 {
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
	// Even out the stretches that would make more repeats.
	stride, limit, sum := 0, counts[0], uint32(0)
	for i := 0; i <= length; i++ {
		if i == length || good[i] || absDiff(counts[i], limit) >= 4 {
			if stride >= 4 || stride >= 3 && sum == 0 {
				count := (sum + uint32(stride)/2) / uint32(stride)
				if count < 1 {
					count = 1
				}
				if sum == 0 {
					count = 0
				}
				for k := 0; k < stride; k++ {
					counts[i-k-1] = count
				}
			}
			stride, sum = 0, 0
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

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
