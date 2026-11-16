package deflate

import "math/bits"

// Huffman codes as libdeflate makes them (lib/deflate_compress.c): the
// symbols are sorted by frequency, the tree is built in place in the
// sorted array, and codeword lengths over the limit are fixed by moving
// leaves up the tree as little as possible. The codes are canonical, with
// the codewords bit-reversed for writing out least significant bit first.

const (
	numSymbolBits = 10
	symbolMask    = 1<<numSymbolBits - 1
	freqMask      = ^uint32(symbolMask)
)

// heapSort sorts a in place.
func heapSort(a []uint32) {
	// sift works on the heap a[0:length] with 1-based node numbers.
	sift := func(length, i int) {
		v := a[i-1]
		parent := i
		for {
			child := parent * 2
			if child > length {
				break
			}
			if child < length && a[child] > a[child-1] {
				child++
			}
			if v >= a[child-1] {
				break
			}
			a[parent-1] = a[child-1]
			parent = child
		}
		a[parent-1] = v
	}
	n := len(a)
	for i := n / 2; i >= 1; i-- {
		sift(n, i)
	}
	for n >= 2 {
		a[n-1], a[0] = a[0], a[n-1]
		n--
		sift(n, 1)
	}
}

// sortSymbols writes the symbols in use to symout, sorted by frequency,
// each with its frequency in the high bits, sets the length of the unused
// ones to 0 and returns how many are in use.
func sortSymbols(freqs []uint32, lens []uint8, symout []uint32) int {
	var counters [maxNumSyms]uint32
	n := uint32(len(freqs))
	for _, f := range freqs {
		counters[min(f, n-1)]++
	}
	var used uint32
	for i := uint32(1); i < n; i++ {
		c := counters[i]
		counters[i] = used
		used += c
	}
	for sym, f := range freqs {
		if f != 0 {
			k := min(f, n-1)
			symout[counters[k]] = uint32(sym) | f<<numSymbolBits
			counters[k]++
		} else {
			lens[sym] = 0
		}
	}
	// The symbols are sorted by a counting sort but for the last bucket,
	// those used n-1 times or more.
	heapSort(symout[counters[n-2]:counters[n-1]])
	return int(used)
}

// buildTree builds the Huffman tree in a, the sorted symbols: each
// non-leaf node is placed where a leaf was, and each node is given the
// index of its parent in the high bits (the low bits keep the symbols).
func buildTree(a []uint32, symCount int) {
	last := symCount - 1
	i, b, e := 0, 0, 0
	for {
		var f uint32
		if i+1 <= last && (b == e || a[i+1]&freqMask <= a[b]&freqMask) {
			// Two leaves.
			f = a[i]&freqMask + a[i+1]&freqMask
			i += 2
		} else if b+2 <= e && (i > last || a[b+1]&freqMask < a[i]&freqMask) {
			// Two non-leaves.
			f = a[b]&freqMask + a[b+1]&freqMask
			a[b] = uint32(e)<<numSymbolBits | a[b]&symbolMask
			a[b+1] = uint32(e)<<numSymbolBits | a[b+1]&symbolMask
			b += 2
		} else {
			// A leaf and a non-leaf.
			f = a[i]&freqMask + a[b]&freqMask
			a[b] = uint32(e)<<numSymbolBits | a[b]&symbolMask
			i++
			b++
		}
		a[e] = f | a[e]&symbolMask
		e++
		if e >= last {
			break
		}
	}
}

// computeLengthCounts counts the leaves at each depth of the tree, no
// deeper than maxLen: a leaf that would be deeper is placed instead under
// the deepest leaf above maxLen, which becomes a non-leaf.
func computeLengthCounts(a []uint32, root int, lenCounts []uint32, maxLen int) {
	for l := 0; l <= maxLen; l++ {
		lenCounts[l] = 0
	}
	lenCounts[1] = 2
	a[root] &= symbolMask
	for node := root - 1; node >= 0; node-- {
		parent := a[node] >> numSymbolBits
		depth := a[parent]>>numSymbolBits + 1
		a[node] = a[node]&symbolMask | depth<<numSymbolBits
		if int(depth) >= maxLen {
			depth = uint32(maxLen)
			for {
				depth--
				if lenCounts[depth] != 0 {
					break
				}
			}
		}
		lenCounts[depth]--
		lenCounts[depth+1] += 2
	}
}

// genCodewords gives the symbols (sorted in a by frequency) their lengths,
// longest for the least frequent, and writes their canonical codewords,
// bit-reversed, to a by symbol.
func genCodewords(a []uint32, lens []uint8, lenCounts []uint32, maxLen int) {
	i := 0
	for l := maxLen; l >= 1; l-- {
		for c := lenCounts[l]; c > 0; c-- {
			lens[a[i]&symbolMask] = uint8(l)
			i++
		}
	}
	var next [maxCodewordLen + 1]uint32
	for l := 2; l <= maxLen; l++ {
		next[l] = (next[l-1] + lenCounts[l-1]) << 1
	}
	for sym, l := range lens {
		a[sym] = uint32(bits.Reverse16(uint16(next[l]))) >> (16 - l)
		next[l]++
	}
}

// makeHuffmanCode makes a code of at most maxLen bits for the frequencies
// of the symbols, writing the lengths and the codewords (bit-reversed).
// The codewords must have room for all the symbols.
func makeHuffmanCode(maxLen int, freqs []uint32, lens []uint8, codewords []uint32) {
	a := codewords
	used := sortSymbols(freqs, lens, a)
	if used < 2 {
		// deflate requires at least two codewords, even of a code that
		// is unused.
		sym := 0
		if used == 1 {
			sym = int(a[0] & symbolMask)
		}
		other := sym
		if sym == 0 {
			other = 1
		}
		codewords[0], lens[0] = 0, 1
		codewords[other], lens[other] = 1, 1
		return
	}
	buildTree(a, used)
	var lenCounts [maxCodewordLen + 1]uint32
	computeLengthCounts(a, used-2, lenCounts[:], maxLen)
	genCodewords(a, lens, lenCounts[:], maxLen)
}
