package jpegstream

import "math"

// Trellis quantization, as mozjpeg does it (jcdctmgr.c, quantize_trellis;
// Copyright (C) 2014, Mozilla Corporation, under the IJG and BSD licences
// of libjpeg-turbo, see licenses/THIRD-PARTY.md). Rounding every DCT
// coefficient to the nearest step of its quantization table is the most
// faithful choice but not the cheapest to code: a coefficient rounded down
// to a smaller value, or to zero where it would end a run, can save more
// bits than it costs in fidelity. The trellis weighs both, for each block
// on its own: it minimises
//
//	distortion + lambda * bits
//
// over the values of the AC coefficients, the bits being the lengths of
// their Huffman codes and the distortion the squared error of each
// coefficient divided by the square of its step (so that it counts as the
// eye sees it in the decoded image). lambda grows smaller the more detail
// the block has: textured blocks hide more change. The DC coefficients,
// coded as differences, are chosen by a second trellis along the blocks of
// each component in the order they are coded.
//
// The bit counts come from the standard Huffman tables (mozjpeg first runs
// a pass over the whole image to count symbols; here the image streams
// through once). With Options.Optimize the tables written are then made
// for the coefficients the trellis chose.
//
// The arithmetic is in float64, every product rounded before it is added
// (the explicit conversions), so that the choice is the same on every
// processor whether or not it fuses a multiplication and an addition.

// mozjpeg's defaults (jcparam.c).
const (
	trellisLambdaLog1 = 14.75
	trellisLambdaLog2 = 16.5
	dcCandidatesMax   = 9
)

// trellisTables are the code lengths and constants a trellis needs for
// one quantization table.
type trellisTables struct {
	q       *[blockSize]byte // the steps, zig-zag order
	dcLen   [12]int          // code length of each DC size category
	acLen   [256]int         // code length of each AC symbol, 0 for none
	weight  [blockSize]float64
	nDC     int // DC candidates
	lambda1 float64
	lambda2 float64
}

func (e *encoder) trellisTables(q quantIndex) *trellisTables {
	t := &trellisTables{q: &e.quant[q], lambda1: math.Pow(2, trellisLambdaLog1), lambda2: math.Pow(2, trellisLambdaLog2)}
	for v := range t.dcLen {
		t.dcLen[v] = int(theHuffmanLUT[2*q][v] >> 24)
	}
	for v := range t.acLen {
		if v < len(theHuffmanLUT[2*q+1]) {
			t.acLen[v] = int(theHuffmanLUT[2*q+1][v] >> 24)
		}
	}
	for zig := 0; zig < blockSize; zig++ {
		s := float64(t.q[zig])
		t.weight[zig] = 1 / float64(s*s)
	}
	dq := int(t.q[0])
	t.nDC = min(dcCandidatesMax, (2+60/dq)|1)
	return t
}

// dcChoice is what the DC trellis needs of one block: the coefficient
// before quantization and the weight of its squared error.
type dcChoice struct {
	x      int32 // unquantized DC (times 8, as the DCT gives it)
	weight float64
}

// trellisBlock quantizes the AC coefficients of b (natural order, after
// the DCT) into zz and returns what the DC trellis needs; zz[0] is set to
// the nearest DC, which the DC trellis may change.
func (t *trellisTables) trellisBlock(b *block, zz *qblock) dcChoice {
	// lambda for this block, from the energy of its AC coefficients.
	var norm float64
	for i := 1; i < blockSize; i++ {
		v := float64(b[i])
		norm += float64(v * v)
	}
	norm /= 63
	lambda := t.lambda1 / (t.lambda2 + norm)

	var (
		zeroDist [blockSize]float64 // distortion of zeroing 1..i
		cost     [blockSize]float64 // best cost of coding 1..i, i coded
		runStart [blockSize]int
		val      [blockSize]int32 // the value chosen at i, signed
	)
	for i := 1; i < blockSize; i++ {
		z := unzig[i]
		x := b[z]
		sign := int32(1)
		if x < 0 {
			x, sign = -x, -1
		}
		q := 8 * int32(t.q[i])
		w := float64(lambda * t.weight[i])
		zeroDist[i] = zeroDist[i-1] + float64(float64(x)*float64(x)*w)
		qval := (x + q/2) / q
		if qval == 0 {
			val[i], cost[i] = 0, math.MaxFloat64
			continue
		}
		qval = min(qval, 1<<10-1) // 8-bit samples: AC magnitudes of 10 bits
		// Candidates: the largest value of each bit length up to qval's,
		// and qval itself.
		n := bitLen(qval)
		cost[i] = math.MaxFloat64
		for j := 0; j < i; j++ {
			if j != 0 && val[j] == 0 {
				continue
			}
			run := i - 1 - j
			zrl := run >> 4
			if zrl > 0 && t.acLen[0xf0] == 0 {
				continue
			}
			runBits := zrl * t.acLen[0xf0]
			run &= 15
			for k := 0; k < n; k++ {
				cand := qval
				if k < n-1 {
					cand = 2<<k - 1
				}
				code := t.acLen[run<<4|(k+1)]
				if code == 0 {
					continue
				}
				d := float64(cand*q - x)
				c := float64(code+k+1+runBits) + float64(d*d*w)
				c += zeroDist[i-1] - zeroDist[j] + cost[j]
				if c < cost[i] {
					cost[i], val[i], runStart[i] = c, cand*sign, j
				}
			}
		}
	}
	// The end of the block: after the last coefficient worth coding, an
	// end-of-block code unless it is the last position.
	eob := float64(t.acLen[0])
	last := 0
	best := zeroDist[blockSize-1] + eob
	for i := 1; i < blockSize; i++ {
		if val[i] == 0 {
			continue
		}
		c := cost[i] + zeroDist[blockSize-1] - zeroDist[i]
		if i < blockSize-1 {
			c += eob
		}
		if c < best {
			best, last = c, i
		}
	}
	// Walk back the chosen path; everything off it is zero.
	for i := 1; i < blockSize; i++ {
		zz[i] = 0
	}
	for i := last; i > 0; i = runStart[i] {
		zz[i] = int16(val[i])
	}

	dq := 8 * int32(t.q[0])
	x := b[0]
	if x >= 0 {
		zz[0] = int16((x + dq/2) / dq)
	} else {
		zz[0] = -int16((-x + dq/2) / dq)
	}
	return dcChoice{x: x, weight: float64(lambda * t.weight[0])}
}

// bitLen is the number of bits of v > 0.
func bitLen(v int32) int {
	n := 0
	for v > 0 {
		v >>= 1
		n++
	}
	return n
}

// dcScratch is the working space of trellisDC, kept from band to band.
type dcScratch struct {
	cand []int32
	acc  []float64
	back []int8
}

// trellisDC chooses the DC values of blocks (one component, in coding
// order), the one before them having DC prev, to minimise distortion and
// the bits of the differences; it sets zz[0] of each block and returns the
// last DC.
func (t *trellisTables) trellisDC(dc []dcChoice, blocks []*qblock, prev int32, s *dcScratch) int32 {
	n := len(dc)
	if n == 0 {
		return prev
	}
	nc := t.nDC
	q := 8 * int32(t.q[0])
	if len(s.cand) < n*nc {
		s.cand, s.acc, s.back = make([]int32, n*nc), make([]float64, n*nc), make([]int8, n*nc)
	}
	cand, acc, back := s.cand[:n*nc], s.acc[:n*nc], s.back[:n*nc]
	for bi, c := range dc {
		x, sign := c.x, int32(1)
		if x < 0 {
			x, sign = -x, -1
		}
		qval := (x + q/2) / q
		for k := 0; k < nc; k++ {
			v := qval - int32(nc/2) + int32(k)
			v = max(-(1<<11 - 1), min(1<<11-1, v)) // 8-bit samples: DC of 11 bits
			d := float64(v*q - x)
			dist := float64(d * d * c.weight)
			v *= sign
			cand[bi*nc+k] = v
			if bi == 0 {
				acc[k] = float64(t.dcBits(v-prev)) + dist
				back[k] = -1
				continue
			}
			best := math.MaxFloat64
			for l := 0; l < nc; l++ {
				c := float64(t.dcBits(v-cand[(bi-1)*nc+l])) + dist + acc[(bi-1)*nc+l]
				if c < best {
					best, back[bi*nc+k] = c, int8(l)
				}
			}
			acc[bi*nc+k] = best
		}
	}
	k := 0
	for l := 1; l < nc; l++ {
		if acc[(n-1)*nc+l] < acc[(n-1)*nc+k] {
			k = l
		}
	}
	last := cand[(n-1)*nc+k]
	for bi := n - 1; bi >= 0; bi-- {
		blocks[bi][0] = int16(cand[bi*nc+k])
		k = int(back[bi*nc+k])
	}
	return last
}

// dcBits is the bits a DC difference takes: its Huffman code and its
// magnitude bits.
func (t *trellisTables) dcBits(d int32) int {
	if d < 0 {
		d = -d
	}
	n := bitLen(d)
	return n + t.dcLen[n]
}
