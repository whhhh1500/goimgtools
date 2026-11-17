package vp8enc

import (
	"math/rand/v2"
	"testing"
)

// TestTrellisSame: the trellis gives exactly what the plain version of it
// (libwebp's structure, kept here) gives.
func TestTrellisSame(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	e := &encoder{}
	c := &mbCoder{e: e}
	for _, seed := range []uint8{0, 60, 128, 200, 255} {
		var pr probs
		for i := range pr {
			for j := range pr[i] {
				for k := range pr[i][j] {
					for l := range pr[i][j][k] {
						pr[i][j][k][l] = uint8(r.IntN(254) + 1)
						if seed == 0 {
							pr[i][j][k][l] = defaultTokenProb[i][j][k][l]
						}
					}
				}
			}
		}
		c.costs = new(costTables)
		c.costs.build(&pr)
		for _, q := range []int{0, 4, 20, 60, 127} {
			var s segment
			s.q = q
			e.setQuant(&s)
			for i := 0; i < 3000; i++ {
				var in [16]int16
				scale := int32(r.IntN(2000) + 1)
				if i%4 == 0 {
					scale = int32(r.IntN(30) + 1)
				}
				for j := range in {
					in[j] = int16(r.Int32N(2*scale+1) - scale)
					if r.IntN(3) == 0 {
						in[j] = 0
					}
				}
				for _, first := range []int{0, 1} {
					plane := planeY1SansY2
					if first == 1 {
						plane = planeY1WithY2
					}
					ctx := r.IntN(3)
					var lv, deq, lv2, deq2 [16]int16
					for k := range lv {
						lv[k], deq[k] = int16(k+1), int16(k+7)
					}
					lv2, deq2 = lv, deq
					got, bits := c.trellis(&in, first, ctx, plane, &s.qY1, s.lamT, &lv, &deq)
					if want := c.costs.blockCost(plane, first, ctx, got, &lv); bits != want {
						t.Fatalf("q %d first %d: %d bits, blockCost %d", q, first, bits, want)
					}
					want := c.trellisRef(&in, first, ctx, plane, &s.qY1, s.lamT, &lv2, &deq2)
					if got != want || lv != lv2 || deq != deq2 {
						t.Fatalf("q %d first %d ctx %d %v: %d %v %v, want %d %v %v", q, first, ctx, in, got, lv, deq, want, lv2, deq2)
					}
				}
			}
		}
	}
}

// trellis quantizes in[first..15] (raster order) with z into lv (scan
// order) and deq (dequantized, raster order), for a block of the given
// plane coded with initial context ctx0, and returns the scan position of
// the last coefficient that is not zero, or -1. lambda weighs the bits
// (1/256 bit) against 256 times the weighted squared error.
type trellisNode struct {
	level int32
	prev  int8
}

func (c *mbCoder) trellisRef(in *[16]int16, first, ctx0, plane int, z *quantizer, lambda int64, lv, deq *[16]int16) int {
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
	levels := &t.level[plane]
	for n := first; n <= last; n++ {
		j := zigzag[n]
		q, iq := z.q[1], z.iq[1]
		if n == 0 {
			q, iq = z.q[0], z.iq[0]
		}
		v := int32(in[j])
		if v < 0 {
			v = -v
		}
		level0 := min((v*iq)>>17, maxLevel)
		threshLevel := min((v*iq+1<<16)>>17, maxLevel)
		prev := cur
		cur ^= 1
		// The tables of the contexts the two nodes before leave, and
		// their scores.
		band := &levels[bands[n]]
		tab0, tab1 := &band[ctxs[prev][0]], &band[ctxs[prev][1]]
		s0, s1 := scores[prev][0], scores[prev][1]
		w := 256 * kWeightTrellis[j]
		for m := 0; m < 2; m++ {
			level := level0 + int32(m)
			ctxs[cur][m] = int(min(level, 2))
			if level > threshLevel {
				scores[cur][m] = maxCost
				continue
			}
			e := int64(v - level*q)
			base := w * (e*e - int64(v)*int64(v))
			// The best of the nodes before.
			fixed, vl := int64(fixedCost[level]), min(level, maxVarLevel)
			best, bp := maxCost, 0
			if s0 < maxCost {
				best = s0 + (fixed+int64(tab0[vl]))*lambda
			}
			if s1 < maxCost {
				if s := s1 + (fixed+int64(tab1[vl]))*lambda; s < best {
					best, bp = s, 1
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
