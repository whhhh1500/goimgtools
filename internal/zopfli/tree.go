package zopfli

// lengthsToSymbols gives the canonical code of each symbol from its code
// length (tree.c).
func lengthsToSymbols(lengths []uint32, maxBits uint32, symbols []uint32) {
	var blCount, nextCode [16]uint32 // maxBits is 15 at most
	for i := range lengths {
		symbols[i] = 0
	}
	for _, l := range lengths {
		blCount[l]++
	}
	code := uint32(0)
	blCount[0] = 0
	for bits := uint32(1); bits <= maxBits; bits++ {
		code = (code + blCount[bits-1]) << 1
		nextCode[bits] = code
	}
	for i, l := range lengths {
		if l != 0 {
			symbols[i] = nextCode[l]
			nextCode[l]++
		}
	}
}

// kInvLog2 is 1/ln(2) as Zopfli writes it.
const kInvLog2 = 1.4426950408889

// calculateEntropy sets bitLengths to the cost in bits of each symbol going
// by its count; an absent symbol costs as one of count 1 would.
func calculateEntropy(count []uint64, bitLengths []float64) {
	var sum uint32 // Zopfli sums into an unsigned
	for _, c := range count {
		sum += uint32(c)
	}
	var log2sum float64
	if sum == 0 {
		log2sum = float64(zlog(float64(len(count))) * kInvLog2)
	} else {
		log2sum = float64(zlog(float64(sum)) * kInvLog2)
	}
	for i, c := range count {
		if c == 0 {
			bitLengths[i] = log2sum
		} else {
			bitLengths[i] = log2sum - float64(zlog(float64(c))*kInvLog2)
		}
		if bitLengths[i] < 0 && bitLengths[i] > -1e-5 {
			bitLengths[i] = 0
		}
	}
}

// calculateBitLengths sets bitLengths to a length-limited Huffman code for
// count.
func calculateBitLengths(count []uint64, maxBits int, bitLengths []uint32) {
	if !lengthLimitedCodeLengths(count, maxBits, bitLengths) {
		panic("zopfli: no code of the length allowed")
	}
}
