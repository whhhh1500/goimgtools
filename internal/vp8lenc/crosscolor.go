package vp8lenc

import "math/bits"

// The cross-color transform: after the predictor, red is made smaller by a
// multiple of green, and blue by multiples of green and red, with the
// multipliers chosen for each block, so that colours that move together
// (a gradient in one hue, the shading of a coloured surface) leave less
// in red and blue. It is libwebp's (src/enc/predictor_enc.c:
// VP8LColorSpaceTransform, GetBestColorTransformForTile and what they
// call, and src/dsp/lossless_enc.c: VP8LTransformColor; Copyright 2012
// Google Inc., BSD licence, see licenses/LICENSE-LIBWEBP), rewritten in Go
// with costs in integers, so that the choices are the same on every
// processor. The blocks are the predictor's, chosen in order a band at a
// time, after the band's residuals. A variant with it runs against the
// others: it is no help on most images, and much on some.

// colorDelta is ColorTransformDelta: the multiplier times the colour, both
// signed bytes, in 3.5 fixed point.
func colorDelta(m, c int8) int32 { return int32(m) * int32(c) >> 5 }

// transformRed and transformBlue are a pixel's red and blue transformed
// (the red taken from blue is the pixel's own, untransformed).
func transformRed(g2r uint8, p uint32) uint8 {
	return uint8(int32(p>>16&0xff) - colorDelta(int8(g2r), int8(p>>8)))
}

func transformBlue(g2b, r2b uint8, p uint32) uint8 {
	return uint8(int32(p&0xff) - colorDelta(int8(g2b), int8(p>>8)) - colorDelta(int8(r2b), int8(p>>16)))
}

// multipliers of one block.
type multipliers struct{ g2r, g2b, r2b uint8 }

// code packs them as the transform's image holds them: red_to_blue in bits
// 16-23, green_to_blue in 8-15, green_to_red in 0-7.
func (m multipliers) code() uint32 {
	return 0xff000000 | uint32(m.r2b)<<16 | uint32(m.g2b)<<8 | uint32(m.g2r)
}

func multipliersOf(c uint32) multipliers {
	return multipliers{g2r: uint8(c), g2b: uint8(c >> 8), r2b: uint8(c >> 16)}
}

// apply transforms pixels.
func (m multipliers) apply(px []uint32) {
	for i, p := range px {
		px[i] = p&0xff00ff00 | uint32(transformRed(m.g2r, p))<<16 | uint32(transformBlue(m.g2b, m.r2b, p))
	}
}

// Costs are in 1/1024 bit.

// ccBias is what keeping the left or the upper block's multiplier, or a
// zero one, is worth (libwebp's 3 bits).
const ccBias = 3 << 10

// log2Frac[i] is 1024·log2(1 + i/256), rounded.
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

const slogSmall = 4096

var slogTable = func() [slogSmall]int64 {
	var t [slogSmall]int64
	for n := 1; n < slogSmall; n++ {
		t[n] = int64(n) * int64(log2Fix(uint32(n)))
	}
	return t
}()

// slog is n·log2(n) in 1/1024 bit.
func slog(n uint32) int64 {
	if n < slogSmall {
		return slogTable[n]
	}
	return int64(n) * int64(log2Fix(n))
}

// ccSmall is what libwebp's PredictionCostBias(counts, 3, 240) takes off
// for each value: 0.3 bit for a zero, 0.24 for ±1, less for larger ones.
var ccSmall = func() [256]int64 {
	var t [256]int64
	t[0] = 3 * 1024 / 10
	exp := int64(240)
	for i := 1; i < 16; i++ {
		v := (exp*1024 + 500) / 1000
		t[i], t[256-i] = v, v
		exp = (6*exp + 5) / 10
	}
	return t
}()

// crossColor chooses the multipliers of a variant's blocks.
type crossColor struct {
	acc  [2][256]uint32 // red and blue of what was transformed so far
	accS [2]int64       // Σ slog over each
	accN [2]uint32      // and its total
	hist [256]uint32
	used []uint8 // the values in hist
	left multipliers
	// The multipliers chosen so far, each byte: what coding them will
	// take is part of what the variant costs.
	chosen [3][256]uint32
}

// bits estimates what the multipliers chosen so far take to code: the
// entropy of each of their bytes.
func (t *crossColor) bits() float64 {
	var b int64
	for k := range t.chosen {
		var n uint32
		var s int64
		for _, c := range t.chosen[k] {
			n += c
			s += slog(c)
		}
		b += slog(n) - s
	}
	return float64(b) / 1024
}

func (t *crossColor) count(v uint8) {
	if t.hist[v] == 0 {
		t.used = append(t.used, v)
	}
	t.hist[v]++
}

// cost is libwebp's PredictionCostCrossColor of the values counted
// against the accumulated histogram a (0 red, 1 blue): the entropy of the
// block's values and of the block's and all before together, less a
// little for small values. The counts are cleared.
func (t *crossColor) cost(a int) int64 {
	acc := &t.acc[a]
	var n uint32
	var sx, bias int64
	sxy := t.accS[a]
	for _, v := range t.used {
		x, y := t.hist[v], acc[v]
		n += x
		sx += slog(x)
		sxy += slog(x+y) - slog(y)
		bias += int64(x) * ccSmall[v]
		t.hist[v] = 0
	}
	t.used = t.used[:0]
	return slog(n) - sx + slog(n+t.accN[a]) - sxy - bias
}

// choose searches as libwebp does above quality 50: green to red by
// halving steps around the best, then green and red to blue along eight
// directions. rows are the block's rows, of which x0 to x1; up is the
// multipliers of the block above.
func (t *crossColor) choose(rows [][]uint32, x0, x1 int, up multipliers) multipliers {
	left := t.left
	redCost := func(g2r int) int64 {
		for _, r := range rows {
			for _, p := range r[x0:x1] {
				t.count(transformRed(uint8(g2r), p))
			}
		}
		c := t.cost(0)
		if uint8(g2r) == left.g2r {
			c -= ccBias
		}
		if uint8(g2r) == up.g2r {
			c -= ccBias
		}
		if g2r == 0 {
			c -= ccBias
		}
		return c
	}
	r2 := 0
	best := redCost(0)
	for iter := 0; iter < 6; iter++ {
		for _, off := range [2]int{-32 >> iter, 32 >> iter} {
			if c := redCost(r2 + off); c < best {
				best, r2 = c, r2+off
			}
		}
	}
	blueCost := func(g2b, r2b int) int64 {
		for _, r := range rows {
			for _, p := range r[x0:x1] {
				t.count(transformBlue(uint8(g2b), uint8(r2b), p))
			}
		}
		c := t.cost(1)
		for _, same := range [6]bool{uint8(g2b) == left.g2b, uint8(g2b) == up.g2b, uint8(r2b) == left.r2b,
			uint8(r2b) == up.r2b, g2b == 0, r2b == 0} {
			if same {
				c -= ccBias
			}
		}
		return c
	}
	axes := [8][2]int{{0, -1}, {0, 1}, {-1, 0}, {1, 0}, {-1, -1}, {-1, 1}, {1, -1}, {1, 1}}
	g, b := 0, 0
	best = blueCost(0, 0)
	for _, delta := range [7]int{16, 16, 8, 4, 2, 2, 2} {
		for _, a := range axes {
			gc, bc := a[0]*delta+g, a[1]*delta+b
			if c := blueCost(gc, bc); c < best {
				best, g, b = c, gc, bc
			}
		}
		if delta == 2 && g == 0 && b == 0 {
			break
		}
	}
	m := multipliers{g2r: uint8(r2), g2b: uint8(g), r2b: uint8(b)}
	t.left = m
	t.chosen[0][m.g2r]++
	t.chosen[1][m.g2b]++
	t.chosen[2][m.r2b]++
	return m
}

// accumulate counts the transformed red and blue of a block, but for
// pixels that repeat the two before them, or whose two before them and
// itself repeat the row above (LZ77 takes those), as libwebp does. buf
// holds rows of w pixels, of which those from lowest on may be read; the
// block is rows r0 to r1-1, x0 to x1.
func (t *crossColor) accumulate(buf []uint32, w, lowest, r0, r1, x0, x1 int) {
	for r := r0; r < r1; r++ {
		for x := x0; x < x1; x++ {
			i := r*w + x
			p := buf[i]
			if i-2 >= lowest && p == buf[i-2] && p == buf[i-1] {
				continue
			}
			if i-w-2 >= lowest && buf[i-2] == buf[i-w-2] && buf[i-1] == buf[i-w-1] && p == buf[i-w] {
				continue
			}
			for k, v := range [2]uint8{uint8(p >> 16), uint8(p)} {
				y := t.acc[k][v]
				t.accS[k] += slog(y+1) - slog(y)
				t.acc[k][v] = y + 1
				t.accN[k]++
			}
		}
	}
}
