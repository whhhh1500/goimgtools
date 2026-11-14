package vp8enc

// Bit costs are in units of 1/256 bit.

// cost0[p] is the cost of coding a 0 whose probability is p/256, that is
// round(-256 * log2(p/256)); a 1 with the same probability parameter costs
// cost0[256-p]. (p is never 0; cost0[0] repeats cost0[1].)
var cost0 = [257]uint16{
	2048, 2048, 1792, 1642, 1536, 1454, 1386, 1329, 1280, 1236, 1198, 1162,
	1130, 1101, 1073, 1048, 1024, 1002, 980, 961, 942, 924, 906, 890,
	874, 859, 845, 831, 817, 804, 792, 780, 768, 757, 746, 735,
	724, 714, 705, 695, 686, 676, 668, 659, 650, 642, 634, 626,
	618, 611, 603, 596, 589, 582, 575, 568, 561, 555, 548, 542,
	536, 530, 524, 518, 512, 506, 501, 495, 490, 484, 479, 474,
	468, 463, 458, 453, 449, 444, 439, 434, 430, 425, 420, 416,
	412, 407, 403, 399, 394, 390, 386, 382, 378, 374, 370, 366,
	362, 358, 355, 351, 347, 343, 340, 336, 333, 329, 326, 322,
	319, 315, 312, 309, 305, 302, 299, 296, 292, 289, 286, 283,
	280, 277, 274, 271, 268, 265, 262, 259, 256, 253, 250, 247,
	245, 242, 239, 236, 234, 231, 228, 226, 223, 220, 218, 215,
	212, 210, 207, 205, 202, 200, 197, 195, 193, 190, 188, 185,
	183, 181, 178, 176, 174, 171, 169, 167, 164, 162, 160, 158,
	156, 153, 151, 149, 147, 145, 143, 140, 138, 136, 134, 132,
	130, 128, 126, 124, 122, 120, 118, 116, 114, 112, 110, 108,
	106, 104, 102, 101, 99, 97, 95, 93, 91, 89, 87, 86,
	84, 82, 80, 78, 77, 75, 73, 71, 70, 68, 66, 64,
	63, 61, 59, 58, 56, 54, 53, 51, 49, 48, 46, 44,
	43, 41, 40, 38, 36, 35, 33, 32, 30, 28, 27, 25,
	24, 22, 21, 19, 18, 16, 15, 13, 12, 10, 9, 7,
	6, 4, 3, 1, 0,
}

func bitCost(bit bool, p uint8) int {
	if bit {
		return int(cost0[256-int(p)])
	}
	return int(cost0[p])
}

var (
	// bands maps a coefficient position to its probability band (13.3).
	bands = [17]uint8{0, 1, 2, 3, 6, 4, 5, 6, 6, 6, 6, 6, 6, 6, 6, 7, 0}
	// zigzag maps a scan position to the raster index of the coefficient.
	zigzag = [16]uint8{0, 1, 4, 8, 5, 2, 3, 6, 9, 12, 13, 10, 7, 11, 14, 15}
	// cat3456 are the fixed probabilities of the extra bits of the large
	// value categories (13.2), each list ending in 0.
	cat3456 = [4][12]uint8{
		{173, 148, 140, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		{176, 155, 140, 135, 0, 0, 0, 0, 0, 0, 0, 0},
		{180, 157, 141, 134, 130, 0, 0, 0, 0, 0, 0, 0},
		{254, 254, 243, 230, 196, 177, 153, 140, 133, 130, 129, 0},
	}
	catBits = [4]int{3, 4, 5, 11}
)

const (
	maxLevel    = 2047 // largest coefficient magnitude coded
	maxVarLevel = 67   // from here on the probability-dependent cost is constant
)

var (
	// fixedCost[v] is the part of the cost of magnitude v that does not
	// depend on the adaptive probabilities: the sign and the extra bits.
	fixedCost [maxLevel + 1]uint16
	// Costs of the prediction modes in the first partition.
	costI16   [4]uint16 // includes the "16x16" flag
	costI4    uint16    // the "4x4" flag
	costUV    [4]uint16
	costMode4 [nPred][nPred][nPred]uint16 // [above][left][mode]
)

func init() {
	for v := 1; v <= maxLevel; v++ {
		c := 256 // sign
		switch {
		case v < 5:
		case v < 7:
			c += bitCost(v == 6, 159)
		case v < 11:
			c += bitCost(v >= 9, 165) + bitCost(v&1 == 0, 145)
		default:
			cat := 3
			for cat > 0 && v < 3+8<<uint(cat) {
				cat--
			}
			extra := v - (3 + 8<<uint(cat))
			for i, n := 0, catBits[cat]; i < n; i++ {
				c += bitCost(extra>>uint(n-1-i)&1 != 0, cat3456[cat][i])
			}
		}
		fixedCost[v] = uint16(c)
	}

	costI4 = uint16(bitCost(false, 145))
	i16 := bitCost(true, 145)
	costI16[predDC] = uint16(i16 + bitCost(false, 156) + bitCost(false, 163))
	costI16[predVE] = uint16(i16 + bitCost(false, 156) + bitCost(true, 163))
	costI16[predHE] = uint16(i16 + bitCost(true, 156) + bitCost(false, 128))
	costI16[predTM] = uint16(i16 + bitCost(true, 156) + bitCost(true, 128))
	costUV[predDC] = uint16(bitCost(false, 142))
	costUV[predVE] = uint16(bitCost(true, 142) + bitCost(false, 114))
	costUV[predHE] = uint16(bitCost(true, 142) + bitCost(true, 114) + bitCost(false, 183))
	costUV[predTM] = uint16(bitCost(true, 142) + bitCost(true, 114) + bitCost(true, 183))
	for a := range costMode4 {
		for l := range costMode4[a] {
			for m := range costMode4[a][l] {
				c := 0
				walkMode4(uint8(m), &predProb[a][l], func(bit bool, p uint8) { c += bitCost(bit, p) })
				costMode4[a][l][m] = uint16(c)
			}
		}
	}
}

// walkMode4 calls put for every bit of the tree code of a 4x4 predictor
// mode (section 11.2), given the probabilities for its two neighbours.
func walkMode4(mode uint8, p *[9]uint8, put func(bit bool, prob uint8)) {
	put(mode != predDC, p[0])
	if mode == predDC {
		return
	}
	put(mode != predTM, p[1])
	if mode == predTM {
		return
	}
	put(mode != predVE, p[2])
	if mode == predVE {
		return
	}
	low := mode == predHE || mode == predRD || mode == predVR
	put(!low, p[3])
	if low {
		put(mode != predHE, p[4])
		if mode != predHE {
			put(mode != predRD, p[5])
		}
		return
	}
	put(mode != predLD, p[6])
	if mode == predLD {
		return
	}
	put(mode != predVL, p[7])
	if mode == predVL {
		return
	}
	put(mode != predHD, p[8])
}

// probs are the token probabilities of a frame: [plane][band][context][node].
type probs [nPlane][nBand][nContext][nProb]uint8

// tokenStats counts, for every token probability, how often the node was
// reached and how often the coded bit was 1.
type tokenStats [nPlane][nBand][nContext][nProb]struct{ ones, total uint32 }

// costTables turn token probabilities into the cost of coding a coefficient.
type costTables struct {
	// level[plane][band][ctx][v] is the probability-dependent cost of
	// magnitude v (capped at maxVarLevel). For ctx > 0 it includes the
	// "not end of block" bit, which is coded exactly when the previous
	// coefficient was non-zero.
	level [nPlane][nBand][nContext][maxVarLevel + 1]uint16
	// eob[plane][band][ctx] is the cost of the "end of block" bit and
	// notEOB that of its opposite.
	eob    [nPlane][nBand][nContext]uint16
	notEOB [nPlane][nBand][nContext]uint16
}

// varCost is the cost of the tree bits of magnitude v >= 1 that use the
// adaptive probabilities p[2:].
func varCost(v int, p *[nProb]uint8) int {
	if v == 1 {
		return bitCost(false, p[2])
	}
	c := bitCost(true, p[2])
	if v <= 4 {
		c += bitCost(false, p[3])
		if v == 2 {
			return c + bitCost(false, p[4])
		}
		return c + bitCost(true, p[4]) + bitCost(v == 4, p[5])
	}
	c += bitCost(true, p[3])
	if v <= 10 {
		return c + bitCost(false, p[6]) + bitCost(v > 6, p[7])
	}
	c += bitCost(true, p[6])
	switch {
	case v < 3+8<<1:
		return c + bitCost(false, p[8]) + bitCost(false, p[9])
	case v < 3+8<<2:
		return c + bitCost(false, p[8]) + bitCost(true, p[9])
	case v < 3+8<<3:
		return c + bitCost(true, p[8]) + bitCost(false, p[10])
	}
	return c + bitCost(true, p[8]) + bitCost(true, p[10])
}

func (t *costTables) build(pr *probs) {
	for pl := range pr {
		for b := range pr[pl] {
			for c := range pr[pl][b] {
				p := &pr[pl][b][c]
				t.eob[pl][b][c] = uint16(bitCost(false, p[0]))
				t.notEOB[pl][b][c] = uint16(bitCost(true, p[0]))
				base := 0
				if c > 0 {
					base = bitCost(true, p[0])
				}
				tab := &t.level[pl][b][c]
				tab[0] = uint16(base + bitCost(false, p[1]))
				base += bitCost(true, p[1])
				for v := 1; v <= maxVarLevel; v++ {
					tab[v] = uint16(base + varCost(v, p))
				}
			}
		}
	}
}

// blockCost is the cost of coding the coefficients lv[first..last] (scan
// order, last < first for an empty block) with initial context ctx.
func (t *costTables) blockCost(plane, first, ctx, last int, lv *[16]int16) int {
	if last < first {
		return int(t.eob[plane][bands[first]][ctx])
	}
	lc := &t.level[plane]
	cost := 0
	if ctx == 0 {
		cost = int(t.notEOB[plane][bands[first]][0])
	}
	for n := first; n <= last; n++ {
		v := int(lv[n])
		if v < 0 {
			v = -v
		}
		tab := &lc[bands[n]][ctx]
		if v <= maxVarLevel {
			cost += int(tab[v])
		} else {
			cost += int(tab[maxVarLevel])
		}
		cost += int(fixedCost[v])
		ctx = min(v, 2)
	}
	if last < 15 {
		cost += int(t.eob[plane][bands[last+1]][ctx])
	}
	return cost
}

// record adds the token decisions of one block to the statistics. It must
// visit exactly the probabilities putCoeffs uses.
func (s *tokenStats) record(plane, first, ctx, last int, lv *[16]int16) {
	st := &s[plane]
	p := &st[bands[first]][ctx]
	p[0].total++
	if last < first {
		return
	}
	p[0].ones++
	for n := first; n <= last; {
		v := int(lv[n])
		n++
		p[1].total++
		if v == 0 {
			p = &st[bands[n]][0]
			continue
		}
		p[1].ones++
		if v < 0 {
			v = -v
		}
		p[2].total++
		if v == 1 {
			p = &st[bands[n]][1]
		} else {
			p[2].ones++
			p[3].total++
			if v <= 4 {
				p[4].total++
				if v != 2 {
					p[4].ones++
					p[5].total++
					if v == 4 {
						p[5].ones++
					}
				}
			} else {
				p[3].ones++
				p[6].total++
				if v <= 10 {
					p[7].total++
					if v > 6 {
						p[7].ones++
					}
				} else {
					p[6].ones++
					p[8].total++
					if v < 3+8<<2 {
						p[9].total++
						if v >= 3+8<<1 {
							p[9].ones++
						}
					} else {
						p[8].ones++
						p[10].total++
						if v >= 3+8<<3 {
							p[10].ones++
						}
					}
				}
			}
			p = &st[bands[n]][2]
		}
		if n == 16 {
			return
		}
		p[0].total++
		if n <= last {
			p[0].ones++
		}
	}
}

// putCoeffs codes one block of coefficients (RFC 6386 section 13).
func putCoeffs(bw *boolEnc, pr *[nBand][nContext][nProb]uint8, first, ctx, last int, lv *[16]int16) {
	p := &pr[bands[first]][ctx]
	bw.putBit(last >= first, p[0])
	if last < first {
		return
	}
	for n := first; n < 16; {
		c := int(lv[n])
		n++
		neg := c < 0
		v := c
		if neg {
			v = -c
		}
		bw.putBit(v != 0, p[1])
		if v == 0 {
			p = &pr[bands[n]][0]
			continue
		}
		bw.putBit(v > 1, p[2])
		if v == 1 {
			p = &pr[bands[n]][1]
		} else {
			bw.putBit(v > 4, p[3])
			if v <= 4 {
				bw.putBit(v != 2, p[4])
				if v != 2 {
					bw.putBit(v == 4, p[5])
				}
			} else {
				bw.putBit(v > 10, p[6])
				if v <= 10 {
					bw.putBit(v > 6, p[7])
					if v <= 6 {
						bw.putBit(v == 6, 159)
					} else {
						bw.putBit(v >= 9, 165)
						bw.putBit(v&1 == 0, 145)
					}
				} else {
					var cat int
					switch {
					case v < 3+8<<1:
						bw.putBit(false, p[8])
						bw.putBit(false, p[9])
						cat = 0
					case v < 3+8<<2:
						bw.putBit(false, p[8])
						bw.putBit(true, p[9])
						cat = 1
					case v < 3+8<<3:
						bw.putBit(true, p[8])
						bw.putBit(false, p[10])
						cat = 2
					default:
						bw.putBit(true, p[8])
						bw.putBit(true, p[10])
						cat = 3
					}
					extra := v - (3 + 8<<uint(cat))
					tab := &cat3456[cat]
					for i, nb := 0, catBits[cat]; i < nb; i++ {
						bw.putBit(extra>>uint(nb-1-i)&1 != 0, tab[i])
					}
				}
			}
			p = &pr[bands[n]][2]
		}
		bw.putBit(neg, 128)
		if n == 16 {
			return
		}
		bw.putBit(n <= last, p[0])
		if n > last {
			return
		}
	}
}

// probFromStats is the probability of a 0 implied by the counts.
func probFromStats(ones, total uint32) uint8 {
	if ones == 0 {
		return 255
	}
	return uint8(255 - uint64(ones)*255/uint64(total))
}

// branchCost is the cost of coding the counted bits with probability p.
func branchCost(ones, total uint32, p uint8) int64 {
	return int64(ones)*int64(bitCost(true, p)) + int64(total-ones)*int64(bitCost(false, p))
}

// finalize picks the frame's token probabilities from the statistics: a
// default is replaced only when sending the new value in the frame header
// pays for itself.
func (s *tokenStats) finalize(pr *probs) {
	for pl := range s {
		for b := range s[pl] {
			for c := range s[pl][b] {
				for i := range s[pl][b][c] {
					st := s[pl][b][c][i]
					old := defaultTokenProb[pl][b][c][i]
					pr[pl][b][c][i] = old
					if st.total == 0 {
						continue
					}
					upd := tokenProbUpdateProb[pl][b][c][i]
					nw := probFromStats(st.ones, st.total)
					if nw == 0 {
						nw = 1
					}
					oldCost := branchCost(st.ones, st.total, old) + int64(bitCost(false, upd))
					newCost := branchCost(st.ones, st.total, nw) + int64(bitCost(true, upd)) + 8*256
					if newCost < oldCost {
						pr[pl][b][c][i] = nw
					}
				}
			}
		}
	}
}
