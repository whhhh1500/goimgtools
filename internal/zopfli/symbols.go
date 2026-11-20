package zopfli

import "math/bits"

// Deflate's symbols for lengths and distances, and their extra bits
// (symbols.h).

var (
	lengthSymbolTab [259]uint16
	lengthExtraTab  [259]uint8
	lengthValueTab  [259]uint16
)

func init() {
	base := [29]int{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31, 35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	extra := [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	for s := 0; s < 29; s++ {
		n := 1 << extra[s]
		if s == 28 {
			n = 1
		}
		for l := base[s]; l < base[s]+n && l <= 258; l++ {
			if s == 27 && l == 258 {
				break // 258 has its own symbol
			}
			lengthSymbolTab[l] = uint16(257 + s)
			lengthExtraTab[l] = extra[s]
			lengthValueTab[l] = uint16(l - base[s])
		}
	}
}

func lengthSymbol(l int) int     { return int(lengthSymbolTab[l]) }
func lengthExtraBits(l int) int  { return int(lengthExtraTab[l]) }
func lengthExtraValue(l int) int { return int(lengthValueTab[l]) }
func lengthSymbolExtra(s int) int {
	return [29]int{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}[s-257]
}

func distExtraBits(d int) int {
	if d < 5 {
		return 0
	}
	return bits.Len(uint(d-1)) - 2 // log2(d-1) - 1
}

func distExtraValue(d int) int {
	if d < 5 {
		return 0
	}
	l := bits.Len(uint(d-1)) - 1
	return (d - (1 + 1<<l)) & (1<<(l-1) - 1)
}

func distSymbol(d int) int {
	if d < 5 {
		return d - 1
	}
	l := bits.Len(uint(d-1)) - 1
	r := ((d - 1) >> (l - 1)) & 1
	return l*2 + r
}

func distSymbolExtra(s int) int {
	return [30]int{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6, 7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}[s]
}
