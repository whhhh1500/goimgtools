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
func (c *Compressor) precomputeHeader() {
	pc := &c.precode
	pc.numLitlen = numLitlenSyms
	for pc.numLitlen > 257 && c.codes.litlenLens[pc.numLitlen-1] == 0 {
		pc.numLitlen--
	}
	pc.numOffset = numOffsetSyms
	for pc.numOffset > 1 && c.codes.offsetLens[pc.numOffset-1] == 0 {
		pc.numOffset--
	}
	var lens [numLitlenSyms + numOffsetSyms]uint8
	copy(lens[:], c.codes.litlenLens[:pc.numLitlen])
	copy(lens[pc.numLitlen:], c.codes.offsetLens[:pc.numOffset])
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

	dynamicCost := uint32(3 + 5 + 5 + 4 + 3*pc.numExplicit)
	for sym, n := range pc.freqs {
		dynamicCost += n * (uint32(extraPrecodeBits[sym]) + uint32(pc.lens[sym]))
	}
	staticCost := uint32(3)
	for sym := range numLiterals {
		dynamicCost += f.litlen[sym] * uint32(c.codes.litlenLens[sym])
		if sym < 144 {
			staticCost += f.litlen[sym] * 8
		} else {
			staticCost += f.litlen[sym] * 9
		}
	}
	dynamicCost += uint32(c.codes.litlenLens[endOfBlock])
	staticCost += 7
	for slot, x := range extraLengthBits {
		sym := firstLenSym + slot
		dynamicCost += f.litlen[sym] * (uint32(x) + uint32(c.codes.litlenLens[sym]))
		staticCost += f.litlen[sym] * (uint32(x) + uint32(staticCodes.litlenLens[sym]))
	}
	for slot, x := range extraOffsetBits {
		dynamicCost += f.offset[slot] * (uint32(x) + uint32(c.codes.offsetLens[slot]))
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
