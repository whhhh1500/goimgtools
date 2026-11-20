package vp8lenc

// A code's lengths are written in the header, run-length coded (a length
// repeated, or a run of zeros, takes one token). Counts that are nearly
// equal give lengths that are nearly equal, and so many tokens; evened out
// first, they give runs. That costs a little in the symbols and can save
// more in the tree: libwebp does it to every code
// (src/utils/huffman_encode_utils.c: OptimizeHuffmanForRle; Copyright 2012
// Google Inc., BSD licence, see licenses/LICENSE-LIBWEBP). Here both codes
// are made, and the one that takes fewer bits, tree included, is kept.

// bestCode is the code for hist, made from hist itself or from it evened
// out for run-length coding, whichever takes fewer bits with its tree; it
// returns those bits too (symbols and tree, extra bits not included).
func bestCode(hist []uint32) (*code, int64) {
	c := newCode(hist, maxCodeLen)
	sc := treePool.Get().(*treeScratch)
	defer treePool.Put(sc)
	bits := c.cost(hist) + treeSize(c.lens, sc)
	if c.used <= 2 {
		return c, bits // a simple code: no lengths are written
	}
	if !evenForRLE(hist, sc) {
		return c, bits
	}
	if cap(sc.lens) < len(hist) {
		sc.lens = make([]uint8, len(hist))
	}
	lens := sc.lens[:len(hist)]
	codeLengths(sc.even, maxCodeLen, lens)
	var b int64
	for s, h := range hist {
		b += int64(h) * int64(lens[s])
	}
	if b += treeSize(lens, sc); b < bits {
		return codeOf(append([]uint8(nil), lens...)), b
	}
	return c, bits
}

// bestBits is the bits of bestCode(hist), without making the code: lens,
// as long as hist, is scratch.
func bestBits(hist []uint32, lens []uint8) int64 {
	sc := treePool.Get().(*treeScratch)
	defer treePool.Put(sc)
	codeLengths(hist, maxCodeLen, lens)
	var bits int64
	used := 0
	for s, l := range lens {
		if l != 0 {
			used++
			bits += int64(hist[s]) * int64(l)
		}
	}
	if used <= 1 {
		bits = 0
	}
	bits += treeSize(lens, sc)
	if used <= 2 || !evenForRLE(hist, sc) {
		return bits
	}
	codeLengths(sc.even, maxCodeLen, lens)
	var b int64
	for s, h := range hist {
		b += int64(h) * int64(lens[s])
	}
	return min(bits, b+treeSize(lens, sc))
}

// evenForRLE puts in sc.even a copy of counts in which the stretches of
// nearly equal counts are replaced by their mean, and reports whether that
// changed anything. A count that is not zero stays at least 1, so every
// symbol in use keeps a code; a stretch of zeros stays zero, and so do the
// zeros at the end.
func evenForRLE(counts []uint32, sc *treeScratch) bool {
	length := len(counts)
	for length > 0 && counts[length-1] == 0 {
		length--
	}
	if length == 0 {
		return false
	}
	// The stretches that already code well as repeats are kept: zeros at
	// least 5 long, other counts at least 7.
	if cap(sc.good) < length {
		sc.good = make([]bool, length)
	}
	good := sc.good[:length]
	clear(good)
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
	sc.even = append(sc.even[:0], counts...)
	out := sc.even
	changed := false
	stride, limit, sum := 0, counts[0], uint32(0)
	for i := 0; i <= length; i++ {
		if i == length || good[i] || i != 0 && good[i-1] || absDiff(counts[i], limit) >= 4 {
			if stride >= 4 || stride >= 3 && sum == 0 {
				// The stretch ends: even it out, if it is long enough.
				count := max((sum+uint32(stride)/2)/uint32(stride), 1)
				if sum == 0 {
					count = 0
				}
				for k := 0; k < stride; k++ {
					if out[i-k-1] != count {
						out[i-k-1], changed = count, true
					}
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
			if stride >= 4 {
				limit = (sum + uint32(stride)/2) / uint32(stride)
			}
		}
	}
	return changed
}
