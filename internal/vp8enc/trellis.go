package vp8enc

// trellis quantizes coefficients the way libwebp does at its slowest
// setting (src/enc/quant_enc.c: TrellisQuantizeBlock): rather than
// rounding each coefficient on its own, it chooses for each between its
// value rounded down and one more (when that is not beyond rounding to
// nearest), and where the block ends, by the cheapest path through the
// block, counting the bits of every coefficient in the context the one
// before leaves and the distortion it adds, weighted by frequency.
//
// It is used for 4x4 blocks, as libwebp does. libwebp also uses it for
// the AC of 16x16 blocks; measured here, that made files larger at the
// same SSIMULACRA2 and butteraugli (about 1%), so it is not.

// kWeightTrellis weighs the squared error of each coefficient (raster
// order): low frequencies most (libwebp's, with USE_TDISTO).
var kWeightTrellis = [16]int64{30, 27, 19, 11, 27, 24, 17, 10, 19, 17, 12, 8, 11, 10, 8, 6}

type trellisNode struct {
	level int32
	prev  int8
}

// trellis quantizes in[first..15] (raster order) with z into lv (scan
// order) and deq (dequantized, raster order), for a block of the given
// plane coded with initial context ctx0, and returns the scan position of
// the last coefficient that is not zero, or -1. lambda weighs the bits
// (1/256 bit) against 256 times the weighted squared error.
func (c *mbCoder) trellis(in *[16]int16, first, ctx0, plane int, z *quantizer, lambda int64, lv, deq *[16]int16) int {
	const maxCost = int64(1) << 62
	t := c.costs
	var (
		nodes  [16][2]trellisNode
		scores [2][2]int64 // [state][delta]: score of the path so far
		ctxs   [2][2]int   // [state][delta]: context the node leaves
		cur    = 0
	)
	// Where the coefficients worth looking at end.
	thresh := int32(z.q[1]) * int32(z.q[1]) / 4
	last := first - 1
	for n := 15; n >= first; n-- {
		v := int32(in[zigzag[n]])
		if v*v > thresh {
			last = n
			break
		}
	}
	if last < 15 {
		last++
	}
	// Coding nothing.
	bestScore := int64(t.eob[plane][bands[first]][ctx0]) * lambda
	bestN, bestM, bestPrev := -1, 0, 0
	// The start: the "not end of block" bit, which a context above 0
	// counts with the first level.
	start := int64(0)
	if ctx0 == 0 {
		start = int64(t.notEOB[plane][bands[first]][0]) * lambda
	}
	for m := 0; m < 2; m++ {
		scores[cur][m], ctxs[cur][m] = start, ctx0
	}
	for n := first; n <= last; n++ {
		j := zigzag[n]
		q, iq := z.q[1], z.iq[1]
		if n == 0 {
			q, iq = z.q[0], z.iq[0]
		}
		v := int32(in[j])
		neg := v < 0
		if neg {
			v = -v
		}
		level0 := min((v*iq)>>17, maxLevel)
		threshLevel := min((v*iq+1<<16)>>17, maxLevel)
		prev := cur
		cur ^= 1
		for m := 0; m < 2; m++ {
			level := level0 + int32(m)
			ctxs[cur][m] = int(min(level, 2))
			if level > threshLevel {
				scores[cur][m] = maxCost
				continue
			}
			e := int64(v - level*q)
			base := 256 * kWeightTrellis[j] * (e*e - int64(v)*int64(v))
			// The best of the nodes before.
			best, bp := maxCost, 0
			for p := 0; p < 2; p++ {
				if scores[prev][p] >= maxCost {
					continue
				}
				tab := &t.level[plane][bands[n]][ctxs[prev][p]]
				cost := int64(fixedCost[level])
				if level <= maxVarLevel {
					cost += int64(tab[level])
				} else {
					cost += int64(tab[maxVarLevel])
				}
				if s := scores[prev][p] + cost*lambda; s < best {
					best, bp = s, p
				}
			}
			if best >= maxCost {
				scores[cur][m] = maxCost
				continue
			}
			best += base
			nodes[n][m] = trellisNode{level: level, prev: int8(bp)}
			scores[cur][m] = best
			// Ending the block here.
			if level != 0 && best < bestScore {
				s := best
				if n < 15 {
					s += int64(t.eob[plane][bands[n+1]][ctxs[cur][m]]) * lambda
				}
				if s < bestScore {
					bestScore, bestN, bestM, bestPrev = s, n, m, bp
				}
			}
		}
	}
	for n := first; n < 16; n++ {
		lv[n], deq[zigzag[n]] = 0, 0
	}
	if first == 0 {
		lv[0], deq[0] = 0, 0
	}
	if bestN < 0 {
		return -1
	}
	nodes[bestN][bestM].prev = int8(bestPrev)
	m := bestM
	lastNZ := -1
	for n := bestN; n >= first; n-- {
		nd := nodes[n][m]
		j := zigzag[n]
		l := int16(nd.level)
		if in[j] < 0 {
			l = -l
		}
		q := z.q[1]
		if n == 0 {
			q = z.q[0]
		}
		lv[n], deq[j] = l, l*int16(q)
		if l != 0 && lastNZ < 0 {
			lastNZ = n
		}
		m = int(nd.prev)
	}
	return lastNZ
}
