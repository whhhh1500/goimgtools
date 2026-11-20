package zopfli

// Writing deflate (deflate.c): bits, the dynamic tree header in the best
// of its 8 ways (with or without the run codes 16, 17 and 18), the symbols.

// bitWriter appends bits to out; bp is the bit position in the last byte.
type bitWriter struct {
	out []byte
	bp  uint8
}

func (w *bitWriter) addBit(bit uint32) {
	if w.bp == 0 {
		w.out = append(w.out, 0)
	}
	w.out[len(w.out)-1] |= byte(bit << w.bp)
	w.bp = (w.bp + 1) & 7
}

func (w *bitWriter) addBits(symbol, length uint32) {
	for i := uint32(0); i < length; i++ {
		w.addBit((symbol >> i) & 1)
	}
}

func (w *bitWriter) addHuffmanBits(symbol, length uint32) {
	for i := uint32(0); i < length; i++ {
		w.addBit((symbol >> (length - i - 1)) & 1)
	}
}

func patchDistanceCodes(dLengths []uint32) {
	n := 0
	for i := 0; i < 30; i++ {
		if dLengths[i] != 0 {
			n++
		}
		if n >= 2 {
			return
		}
	}
	if n == 0 {
		dLengths[0], dLengths[1] = 1, 1
	} else if n == 1 {
		if dLengths[0] != 0 {
			dLengths[1] = 1
		} else {
			dLengths[0] = 1
		}
	}
}

var clOrder = [19]int{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

// encodeTree writes (with w not nil) the code lengths of a dynamic block's
// header and returns its size in bits.
func encodeTree(llLengths, dLengths []uint32, use16, use17, use18 bool, w *bitWriter) int {
	var rle, rleBits []uint32
	hlit, hdist := 29, 29
	var clCounts [19]uint64
	var clcl, clSymbols [19]uint32
	sizeOnly := w == nil
	for hlit > 0 && llLengths[257+hlit-1] == 0 {
		hlit--
	}
	for hdist > 0 && dLengths[1+hdist-1] == 0 {
		hdist--
	}
	hlit2 := hlit + 257
	total := hlit2 + hdist + 1
	at := func(j int) uint32 {
		if j < hlit2 {
			return llLengths[j]
		}
		return dLengths[j-hlit2]
	}
	for i := 0; i < total; i++ {
		symbol := uint32(byte(at(i)))
		count := 1
		if use16 || (symbol == 0 && (use17 || use18)) {
			for j := i + 1; j < total && symbol == at(j); j++ {
				count++
			}
		}
		i += count - 1
		if symbol == 0 && count >= 3 {
			if use18 {
				for count >= 11 {
					c2 := min(count, 138)
					if !sizeOnly {
						rle, rleBits = append(rle, 18), append(rleBits, uint32(c2-11))
					}
					clCounts[18]++
					count -= c2
				}
			}
			if use17 {
				for count >= 3 {
					c2 := min(count, 10)
					if !sizeOnly {
						rle, rleBits = append(rle, 17), append(rleBits, uint32(c2-3))
					}
					clCounts[17]++
					count -= c2
				}
			}
		}
		if use16 && count >= 4 {
			count--
			clCounts[symbol]++
			if !sizeOnly {
				rle, rleBits = append(rle, symbol), append(rleBits, 0)
			}
			for count >= 3 {
				c2 := min(count, 6)
				if !sizeOnly {
					rle, rleBits = append(rle, 16), append(rleBits, uint32(c2-3))
				}
				clCounts[16]++
				count -= c2
			}
		}
		clCounts[symbol] += uint64(count)
		for ; count > 0; count-- {
			if !sizeOnly {
				rle, rleBits = append(rle, symbol), append(rleBits, 0)
			}
		}
	}
	calculateBitLengths(clCounts[:], 7, clcl[:])
	if !sizeOnly {
		lengthsToSymbols(clcl[:], 7, clSymbols[:])
	}
	hclen := 15
	for hclen > 0 && clCounts[clOrder[hclen+4-1]] == 0 {
		hclen--
	}
	if !sizeOnly {
		w.addBits(uint32(hlit), 5)
		w.addBits(uint32(hdist), 5)
		w.addBits(uint32(hclen), 4)
		for i := 0; i < hclen+4; i++ {
			w.addBits(clcl[clOrder[i]], 3)
		}
		for i, r := range rle {
			w.addHuffmanBits(clSymbols[r], clcl[r])
			switch r {
			case 16:
				w.addBits(rleBits[i], 2)
			case 17:
				w.addBits(rleBits[i], 3)
			case 18:
				w.addBits(rleBits[i], 7)
			}
		}
	}
	size := 14 + (hclen+4)*3
	for i := 0; i < 19; i++ {
		size += int(clcl[i]) * int(clCounts[i])
	}
	size += int(clCounts[16])*2 + int(clCounts[17])*3 + int(clCounts[18])*7
	return size
}

func addDynamicTree(llLengths, dLengths []uint32, w *bitWriter) {
	best, bestSize := 0, 0
	for i := 0; i < 8; i++ {
		if size := encodeTree(llLengths, dLengths, i&1 != 0, i&2 != 0, i&4 != 0, nil); bestSize == 0 || size < bestSize {
			best, bestSize = i, size
		}
	}
	encodeTree(llLengths, dLengths, best&1 != 0, best&2 != 0, best&4 != 0, w)
}

func calculateTreeSize(llLengths, dLengths []uint32) int {
	result := 0
	for i := 0; i < 8; i++ {
		if size := encodeTree(llLengths, dLengths, i&1 != 0, i&2 != 0, i&4 != 0, nil); result == 0 || size < result {
			result = size
		}
	}
	return result
}

func addLZ77Data(s *lz77Store, lstart, lend int, llSymbols, llLengths, dSymbols, dLengths []uint32, w *bitWriter) {
	for i := lstart; i < lend; i++ {
		addItem(int(s.litlens[i]), int(s.dists[i]), llSymbols, llLengths, dSymbols, dLengths, w)
	}
}

func addItem(litlen, dist int, llSymbols, llLengths, dSymbols, dLengths []uint32, w *bitWriter) {
	if dist == 0 {
		w.addHuffmanBits(llSymbols[litlen], llLengths[litlen])
		return
	}
	lls, ds := lengthSymbol(litlen), distSymbol(dist)
	w.addHuffmanBits(llSymbols[lls], llLengths[lls])
	w.addBits(uint32(lengthExtraValue(litlen)), uint32(lengthExtraBits(litlen)))
	w.addHuffmanBits(dSymbols[ds], dLengths[ds])
	w.addBits(uint32(distExtraValue(dist)), uint32(distExtraBits(dist)))
}

// fixedWriter writes the items of a parse with the fixed code, as they come
// (a parseSink).
type fixedWriter struct {
	w                    *bitWriter
	llLengths, llSymbols [numLL]uint32
	dLengths, dSymbols   [numD]uint32
}

func newFixedWriter(w *bitWriter) *fixedWriter {
	f := &fixedWriter{w: w}
	fixedTree(f.llLengths[:], f.dLengths[:])
	lengthsToSymbols(f.llLengths[:], 15, f.llSymbols[:])
	lengthsToSymbols(f.dLengths[:], 15, f.dSymbols[:])
	return f
}

func (f *fixedWriter) add(length, dist uint16, _ int) {
	addItem(int(length), int(dist), f.llSymbols[:], f.llLengths[:], f.dSymbols[:], f.dLengths[:], f.w)
}
