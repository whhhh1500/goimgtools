package deflate

import "math/bits"

// Not in libdeflate: with Params.EntropyCosts, the passes after the first
// price each symbol at its entropy under the frequencies of the last
// pass, -log2(frequency/total) to a sixteenth of a bit, as zopfli prices
// them, rather than at the length of its code (whole bits). Code lengths
// round a symbol's cost up or down by up to a bit; the entropy steers the
// parse towards the symbols that are really common. On the filtered rows
// of 17 PNG images (level 9): 0.18% smaller, the most 0.7%.

// log2Frac[i] is 1024·log2(1 + i/256), rounded. Costs are worked out in
// integers, so that they are the same on every processor.
var log2Frac = [256]uint16{
	0, 6, 11, 17, 23, 29, 34, 40, 45, 51, 57, 62, 68, 73, 79, 84,
	90, 95, 100, 106, 111, 116, 122, 127, 132, 138, 143, 148, 153, 159, 164, 169,
	174, 179, 184, 189, 194, 199, 204, 209, 214, 219, 224, 229, 234, 239, 244, 249,
	254, 259, 264, 268, 273, 278, 283, 288, 292, 297, 302, 306, 311, 316, 320, 325,
	330, 334, 339, 343, 348, 353, 357, 362, 366, 371, 375, 380, 384, 388, 393, 397,
	402, 406, 411, 415, 419, 424, 428, 432, 436, 441, 445, 449, 454, 458, 462, 466,
	470, 475, 479, 483, 487, 491, 495, 500, 504, 508, 512, 516, 520, 524, 528, 532,
	536, 540, 544, 548, 552, 556, 560, 564, 568, 572, 576, 580, 584, 587, 591, 595,
	599, 603, 607, 610, 614, 618, 622, 626, 629, 633, 637, 641, 644, 648, 652, 656,
	659, 663, 667, 670, 674, 678, 681, 685, 689, 692, 696, 699, 703, 707, 710, 714,
	717, 721, 724, 728, 731, 735, 738, 742, 745, 749, 752, 756, 759, 763, 766, 770,
	773, 776, 780, 783, 787, 790, 793, 797, 800, 803, 807, 810, 813, 817, 820, 823,
	827, 830, 833, 837, 840, 843, 846, 850, 853, 856, 859, 863, 866, 869, 872, 875,
	879, 882, 885, 888, 891, 894, 898, 901, 904, 907, 910, 913, 916, 919, 922, 926,
	929, 932, 935, 938, 941, 944, 947, 950, 953, 956, 959, 962, 965, 968, 971, 974,
	977, 980, 983, 986, 989, 992, 995, 998, 1001, 1004, 1007, 1010, 1012, 1015, 1018, 1021,
}

// log2Fix is log2(v) in 1/1024 bit, for v of 1 or more, to within 1/180
// bit.
func log2Fix(v uint32) uint32 {
	n := uint32(bits.Len32(v))
	var m uint32
	if n > 9 {
		m = v >> (n - 9)
	} else {
		m = v << (9 - n)
	}
	return (n-1)*1024 + uint32(log2Frac[m-256])
}

// setCostsFromFreqs prices the symbols at their entropy under f; one that
// does not occur costs what libdeflate charges for it.
func (c *Compressor) setCostsFromFreqs(f *freqs) {
	var tl, to uint32
	for _, v := range f.litlen {
		tl += v
	}
	for _, v := range f.offset {
		to += v
	}
	cost := func(v, total, nostat uint32) uint32 {
		if v == 0 {
			return nostat * bitCost
		}
		return max((log2Fix(total)-log2Fix(v)+32)/64, 1)
	}
	for i := range numLiterals {
		c.costs.literal[i] = cost(f.litlen[i], tl, literalNostatBits)
	}
	for l := minMatch; l <= maxMatch; l++ {
		slot := lengthSlot[l]
		c.costs.length[l] = cost(f.litlen[firstLenSym+int(slot)], tl, lengthNostatBits) + uint32(extraLengthBits[slot])*bitCost
	}
	for slot := range offsetSlotBase {
		c.costs.offsetSlot[slot] = cost(f.offset[slot], to, offsetNostatBits) + uint32(extraOffsetBits[slot])*bitCost
	}
}
