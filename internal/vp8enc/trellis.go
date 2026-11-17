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

// trellis quantizes in[first..15] (raster order) with z into lv (scan
// order) and deq (dequantized, raster order), for a block of the given
// plane coded with initial context ctx0, and returns the scan position of
// the last coefficient that is not zero, or -1, and the bits of the block
// (what costTables.blockCost gives for it). lambda weighs the bits (1/256
// bit) against 256 times the weighted squared error.
//
// The two nodes of each position (the level rounded down, and one more)
// are kept in variables, each with the path that leads to it as a mask
// (bit n set where position n takes one more), and a node that cannot be
// reached has the score maxCost: added to, it stays at or above maxCost,
// so the comparisons need no test of it (scores of nodes that can be
// reached stay far below).
func (c *mbCoder) trellis(in *[16]int16, first, ctx0, plane int, z *quantizer, lambda int64, lv, deq *[16]int16) (int, int) {
	const maxCost = int64(1) << 62
	t := c.costs
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
	levels := &t.level[plane]
	eobs := &t.eob[plane]
	// Coding nothing.
	bestRate := int32(eobs[bands[first]][ctx0])
	bestScore := int64(bestRate) * lambda
	bestN, bestPath := -1, uint32(0)
	// The start: the "not end of block" bit, which a context above 0
	// counts with the first level.
	start := int32(0)
	if ctx0 == 0 {
		start = int32(t.notEOB[plane][bands[first]][0])
	}
	// The scores of the two nodes before, the contexts they leave, their
	// paths and their bits.
	s0, s1 := int64(start)*lambda, int64(start)*lambda
	x0, x1 := ctx0, ctx0
	var p0, p1 uint32
	r0, r1 := start, start
	var level0s [16]int32
	for n := first; n <= last; n++ {
		j := zigzag[n]
		q, iq := z.q[1], z.iq[1]
		if n == 0 {
			q, iq = z.q[0], z.iq[0]
		}
		v := int32(in[j])
		sign := v >> 31
		v = v ^ sign - sign
		level0 := min((v*iq)>>17, maxLevel)
		threshLevel := min((v*iq+1<<16)>>17, maxLevel)
		level0s[n&15] = level0
		band := &levels[bands[n]]
		tab0, tab1 := &band[x0], &band[x1]
		w := 256 * kWeightTrellis[j]
		vv := int64(v) * int64(v)
		var eob *[nContext]uint16
		if n < 15 {
			eob = &eobs[bands[n+1]]
		}

		// The level rounded down, after the better node before.
		l := level0
		e := int64(v - l*q)
		vl := min(l, maxVarLevel)
		b0, b1 := int32(tab0[vl]), int32(tab1[vl])
		a0 := s0 + int64(b0)*lambda
		a1 := s1 + int64(b1)*lambda
		path, rate := p0, r0+b0
		if a1 < a0 {
			a0, path, rate = a1, p1, r1+b1
		}
		fixed := int32(fixedCost[l])
		n0 := maxCost
		if a0 < maxCost {
			n0 = a0 + int64(fixed)*lambda + w*(e*e-vv)
		}
		q0, t0 := path, rate+fixed
		c0 := int(min(l, 2))
		if l != 0 {
			s, sr := n0, t0
			if eob != nil {
				s += int64(eob[c0]) * lambda
				sr += int32(eob[c0])
			}
			if s < bestScore {
				bestScore, bestN, bestPath, bestRate = s, n, q0, sr
			}
		}

		// One more, when that is not beyond rounding to nearest.
		n1, q1, t1, c1 := maxCost, uint32(0), int32(0), c0
		if level0 < threshLevel {
			l++
			e -= int64(q)
			vl = min(l, maxVarLevel)
			b0, b1 = int32(tab0[vl]), int32(tab1[vl])
			a0 = s0 + int64(b0)*lambda
			a1 = s1 + int64(b1)*lambda
			path, rate = p0, r0+b0
			if a1 < a0 {
				a0, path, rate = a1, p1, r1+b1
			}
			fixed = int32(fixedCost[l])
			if a0 < maxCost {
				n1 = a0 + int64(fixed)*lambda + w*(e*e-vv)
			}
			q1, t1 = path|1<<uint(n), rate+fixed
			c1 = int(min(l, 2))
			s, sr := n1, t1
			if eob != nil {
				s += int64(eob[c1]) * lambda
				sr += int32(eob[c1])
			}
			if s < bestScore {
				bestScore, bestN, bestPath, bestRate = s, n, q1, sr
			}
		}
		s0, s1, x0, x1, p0, p1, r0, r1 = n0, n1, c0, c1, q0, q1, t0, t1
	}
	if first == 0 {
		*lv, *deq = [16]int16{}, [16]int16{}
	} else {
		for n := first; n < 16; n++ {
			lv[n], deq[zigzag[n]] = 0, 0
		}
	}
	// The level of each position on the best path; the last of them is
	// not zero (only such a node ends a block).
	for n := first; n <= bestN; n++ {
		j := zigzag[n]
		l := int16(level0s[n&15]) + int16(bestPath>>uint(n)&1)
		sign := in[j] >> 15
		l = l ^ sign - sign
		q := z.q[1]
		if n == 0 {
			q = z.q[0]
		}
		lv[n], deq[j] = l, l*int16(q)
	}
	return bestN, int(bestRate)
}
