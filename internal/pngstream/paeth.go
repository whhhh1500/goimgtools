// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package pngstream

// intSize is either 32 or 64.
const intSize = 32 << (^uint(0) >> 63)

func abs(x int) int {
	// m := -1 if x < 0. m := 0 otherwise.
	m := x >> (intSize - 1)

	// In two's complement representation, the negative number
	// of any number (except the smallest one) can be computed
	// by flipping all the bits and add 1. This is faster than
	// code with a branch.
	// See Hacker's Delight, section 2-4.
	return (x ^ m) - m
}

// paeth implements the Paeth filter function, as per the PNG specification.
func paeth(a, b, c uint8) uint8 {
	// This is an optimized version of the sample code in the PNG spec.
	// For example, the sample code starts with:
	//	p := int(a) + int(b) - int(c)
	//	pa := abs(p - int(a))
	// but the optimized form uses fewer arithmetic operations:
	//	pa := int(b) - int(c)
	//	pa = abs(pa)
	pc := int(c)
	pa := int(b) - pc
	pb := int(a) - pc
	pc = abs(pa + pb)
	pa = abs(pa)
	pb = abs(pb)
	if pa <= pb && pa <= pc {
		return a
	} else if pb <= pc {
		return b
	}
	return c
}

// paethOf is paeth on ints, without a branch: the comparisons become
// conditional moves, which a Paeth row, whose choices follow the picture,
// would otherwise mispredict.
func paethOf(a, b, c int) int {
	pa := b - c
	pb := a - c
	pc := max(pa+pb, -pa-pb)
	pa = max(pa, -pa)
	pb = max(pb, -pb)
	x := c
	if pb <= pc {
		x = b
	}
	if pa <= min(pb, pc) {
		x = a
	}
	return x
}

// filterPaeth applies the Paeth filter to the cdat slice.
// cdat is the current row's data, pdat is the previous row's data.
//
// The channels of a pixel are undone together, each from the same channel
// of the pixel before: the standard library undoes one channel along the
// whole row and then the next, so that each step waits for the one before.
func filterPaeth(cdat, pdat []byte, bytesPerPixel int) {
	n := min(len(cdat), len(pdat))
	switch bytesPerPixel {
	case 3:
		var a0, a1, a2, c0, c1, c2 int
		for j := 0; j+3 <= n; j += 3 {
			cd, pd := cdat[j:j+3:j+3], pdat[j:j+3:j+3]
			b0, b1, b2 := int(pd[0]), int(pd[1]), int(pd[2])
			a0 = (int(cd[0]) + paethOf(a0, b0, c0)) & 0xff
			a1 = (int(cd[1]) + paethOf(a1, b1, c1)) & 0xff
			a2 = (int(cd[2]) + paethOf(a2, b2, c2)) & 0xff
			cd[0], cd[1], cd[2] = uint8(a0), uint8(a1), uint8(a2)
			c0, c1, c2 = b0, b1, b2
		}
	case 4:
		var a0, a1, a2, a3, c0, c1, c2, c3 int
		for j := 0; j+4 <= n; j += 4 {
			cd, pd := cdat[j:j+4:j+4], pdat[j:j+4:j+4]
			b0, b1, b2, b3 := int(pd[0]), int(pd[1]), int(pd[2]), int(pd[3])
			a0 = (int(cd[0]) + paethOf(a0, b0, c0)) & 0xff
			a1 = (int(cd[1]) + paethOf(a1, b1, c1)) & 0xff
			a2 = (int(cd[2]) + paethOf(a2, b2, c2)) & 0xff
			a3 = (int(cd[3]) + paethOf(a3, b3, c3)) & 0xff
			cd[0], cd[1], cd[2], cd[3] = uint8(a0), uint8(a1), uint8(a2), uint8(a3)
			c0, c1, c2, c3 = b0, b1, b2, b3
		}
	default:
		// The first pixel has nothing to its left: a and c are 0, and the
		// prediction is b.
		for j := 0; j < bytesPerPixel && j < n; j++ {
			cdat[j] += pdat[j]
		}
		for j := bytesPerPixel; j < n; j++ {
			cdat[j] += uint8(paethOf(int(cdat[j-bytesPerPixel]), int(pdat[j]), int(pdat[j-bytesPerPixel])))
		}
	}
}
