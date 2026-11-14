package vp8enc

import (
	"errors"
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Tuning constants. They were set by measuring PSNR against file size on
// photographs; none of them affects the validity of the output.
const (
	// The Lagrange multiplier that trades one bit for squared error is
	// lambdaScale/256 times the square of the AC quantizer step. Chroma
	// uses a quarter of that: an error in a chroma sample is spread over
	// four pixels.
	lambdaScale   = 8
	lambdaScaleUV = 2
	// i4Tries is how many of the ten 4x4 modes get the full
	// transform-quantize-reconstruct evaluation; the rest are ruled out by
	// the size of their prediction error.
	i4Tries = 4
	// The loop filter level is filterScale/64 times the luma AC step.
	filterScale = 20
	// The chroma quantizer index is uvDeltaQ/10 below the luma one (at most
	// 15, the format's limit), for the same reason as lambdaScaleUV.
	uvDeltaQ = 3
)

// Rounding offsets of the quantizer, out of 256, for the DC and the AC
// coefficients: 128 would round to nearest, less favours smaller values.
var (
	biasY1 = [2]int32{110, 86}
	biasY2 = [2]int32{96, 90}
	biasUV = [2]int32{115, 90}
)

// qualityToIndex maps Quality to the quantizer index (0 finest .. 127),
// following libwebp: 127 * (1 - cbrt(c)), where c is quality/100 scaled to
// 2/3 of its value below 75 and 2c-1 above.
var qualityToIndex = [101]uint8{
	127, 103, 96, 92, 89, 86, 83, 81, 79, 77, 75, 73, 72, 70, 69, 68, 66, 65, 64, 63,
	62, 61, 60, 59, 58, 57, 56, 55, 54, 53, 52, 51, 51, 50, 49, 48, 48, 47, 46, 45,
	45, 44, 43, 43, 42, 41, 41, 40, 40, 39, 38, 38, 37, 37, 36, 36, 35, 35, 34, 33,
	33, 32, 32, 31, 31, 30, 30, 29, 29, 28, 28, 28, 27, 27, 26, 26, 24, 23, 22, 21,
	19, 18, 17, 16, 15, 14, 13, 12, 11, 10, 9, 8, 7, 6, 5, 4, 3, 2, 1, 0, 0,
}

// quantizer holds the DC and AC step of one plane type.
type quantizer struct {
	q    [2]int32
	iq   [2]int32 // 1<<17 / q
	bias [2]int32
}

func (z *quantizer) set(dc, ac int, bias [2]int32) {
	z.q = [2]int32{int32(dc), int32(ac)}
	for i := range z.q {
		z.iq[i] = (1 << 17) / z.q[i]
		z.bias[i] = bias[i] << 9
	}
}

// quantize quantizes the coefficients in (raster order) from scan position
// first on. It stores the levels in lv (scan order) and what the decoder
// will get back from them in deq (raster order), and returns the scan
// position of the last non-zero level, or -1.
func (z *quantizer) quantize(in *[16]int16, first int, lv, deq *[16]int16) int {
	last := -1
	lv[0] = 0
	k := first // 0 selects the DC step, anything else the AC step
	for n := first; n < 16; n++ {
		j := zigzag[n]
		c := int32(in[j])
		neg := c < 0
		if neg {
			c = -c
		}
		l := (c*z.iq[k] + z.bias[k]) >> 17
		q := z.q[k]
		k = 1
		if l == 0 {
			lv[n], deq[j] = 0, 0
			continue
		}
		if l > maxLevel {
			l = maxLevel
		}
		d := l * q
		if neg {
			l, d = -l, -d
		}
		lv[n], deq[j] = int16(l), int16(d)
		last = n
	}
	return last
}

// mbCtx is what coding a macroblock needs to know about the one above or to
// the left of it (the same state the decoder keeps).
type mbCtx struct {
	pred   [4]uint8 // predictor modes of the 4 nearest 4x4 luma blocks
	nzMask uint8    // which of the nearest blocks have coefficients: 4 luma, 2 U, 2 V
	nzY16  uint8    // whether the last 16x16-predicted macroblock had a Y2 block
}

// cand16 is the luma of a macroblock coded with a 16x16 predictor.
type cand16 struct {
	mode   uint8
	y2     [16]int16
	y2last int
	lv     [16][16]int16
	last   [16]int8
	rec    [256]uint8
	score  int64
}

// cand4 is the luma of a macroblock coded with 4x4 predictors. Its
// reconstruction is written straight into the workspace.
type cand4 struct {
	modes [16]uint8
	lv    [16][16]int16
	last  [16]int8
}

// candUV is the chroma of a macroblock.
type candUV struct {
	mode  uint8
	lv    [8][16]int16
	last  [8]int8
	rec   [2][64]uint8
	score int64
}

type encoder struct {
	env  *pipeline.Env
	b    image.Rectangle
	w, h int
	mbw  int
	mbh  int
	dbg  *debug

	fixed int64 // budget reserved for the buffers below

	// Quantization.
	baseQ       int
	uvDQ        int // chroma quantizer index minus baseQ
	qY1         quantizer
	qY2         quantizer
	qUV         quantizer
	lamY, lamUV int64
	filterLevel int

	// The current macroblock row and the reconstruction above it.
	conv *converter
	topY []uint8
	topU []uint8
	topV []uint8
	up   []mbCtx
	left mbCtx
	ws   workspace

	// Scratch for the macroblock being coded.
	ysrc   [256]uint8
	usrc   [64]uint8
	vsrc   [64]uint8
	pred16 [256]uint8
	pred4  [nPred][16]uint8
	predC  [2][64]uint8
	c16    [2]cand16
	c4     cand4
	cuv    [2]candUV

	// Entropy model: counts of what has been coded so far, and the bit
	// costs derived from them for the mode decisions.
	stats     tokenStats
	skipStats tokenStats // "end of block" bits that skipped macroblocks would need
	probs     probs
	costs     costTables
	sinceCost int
	nSkip     int
	modeBits  int64 // bits (1/256) of the modes written so far
	modeMax   int64 // what all modes together may use (modeBudget)
	mbDone    int64

	// The spool of what was decided, and the coder that writes it.
	spool *store
	sw    boolEnc

	// Alpha plane, one byte per pixel, when the source may be transparent.
	alpha      *alphaSpool
	alphaStore *store

	// Output of the arithmetic coder: the first partition and, one after
	// the other, the token partitions.
	part0    *store
	coded    *store
	ioBuf    []byte
	ioBuf0   []byte
	readBuf  []byte
	nParts   int
	partLen  [8]int64
	p0Len    int64
	useSkip  bool
	skipProb uint8
}

const (
	ioBufSize   = 4 << 10
	readBufSize = 32 << 10

	// The first partition's length field has 19 bits. modeBudget is what
	// the prediction modes may use of it, in 1/256 bit, leaving room for
	// the frame header and for the arithmetic coder being slightly less
	// efficient than the cost tables say.
	maxPartition0 = 1<<19 - 1
	modeBudget    = int64(maxPartition0-8192) * 8 * 256 * 31 / 32
)

func hasAlphaChannel(m image.Image) bool {
	switch m.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		return false
	}
	return true
}

func (e *encoder) init(quality int, rr *pipeline.RowReader, src pipeline.Source) error {
	first, err := rr.Row(e.b.Min.Y)
	if err != nil {
		return err
	}
	wantAlpha := hasAlphaChannel(first) && !pipeline.IsOpaque(src)

	w16 := e.mbw * 16
	e.fixed = converterBytes(e.w, w16, wantAlpha) +
		int64(w16+4) + int64(w16) + // top rows
		int64(e.mbw)*6 + // contexts
		2*ioBufSize + readBufSize
	if err := e.env.Budget.Reserve(e.fixed, "webp encoder rows"); err != nil {
		e.fixed = 0
		return err
	}
	e.spool = newStore(e.env)
	if wantAlpha {
		e.alphaStore = newStore(e.env)
		e.alpha = newAlphaSpool(e.alphaStore, e.w)
	}
	e.conv = newConverter(e.b, w16, e.alpha)
	e.topY = make([]uint8, w16+4)
	e.topU = make([]uint8, w16/2)
	e.topV = make([]uint8, w16/2)
	e.up = make([]mbCtx, e.mbw)
	e.ioBuf = make([]byte, ioBufSize)
	e.ioBuf0 = make([]byte, ioBufSize)
	e.readBuf = make([]byte, readBufSize)
	e.sw.init(e.spool, e.ioBuf)
	if e.dbg != nil && e.dbg.rec {
		e.dbg.y = make([]uint8, w16*16*e.mbh)
		e.dbg.u = make([]uint8, w16/2*8*e.mbh)
		e.dbg.v = make([]uint8, w16/2*8*e.mbh)
	}

	q := int(qualityToIndex[quality])
	e.baseQ = q
	dc, ac := int(dequantTableDC[q]), int(dequantTableAC[q])
	e.qY1.set(dc, ac, biasY1)
	e.qY2.set(dc*2, max(ac*155/100, 8), biasY2)
	e.uvDQ = -min(15, (uvDeltaQ*q+5)/10)
	uvq := q + e.uvDQ
	e.qUV.set(int(dequantTableDC[min(uvq, 117)]), int(dequantTableAC[min(uvq, 127)]), biasUV)
	e.lamY = lambdaScale * int64(ac*ac)
	e.lamUV = lambdaScaleUV * int64(e.qUV.q[1]*e.qUV.q[1])
	e.filterLevel = min(ac*filterScale/64, 63)
	if e.dbg != nil && e.dbg.noFilter {
		e.filterLevel = 0
	}

	e.modeMax = modeBudget
	if e.dbg != nil && e.dbg.modeBudget != 0 {
		e.modeMax = e.dbg.modeBudget
	}

	e.probs = defaultTokenProb
	e.costs.build(&e.probs)
	return nil
}

func (e *encoder) release() {
	for _, s := range []*store{e.spool, e.alphaStore, e.coded, e.part0} {
		s.Close()
	}
	e.spool, e.alphaStore, e.coded, e.part0 = nil, nil, nil, nil
	e.env.Budget.Release(e.fixed)
	e.fixed = 0
}

// analyse reads the image, decides how every macroblock is coded and spools
// the decisions.
func (e *encoder) analyse(rr *pipeline.RowReader) error {
	for mby := 0; mby < e.mbh; mby++ {
		n := min(16, e.h-mby*16)
		for k := 0; k < n; k++ {
			y := e.b.Min.Y + mby*16 + k
			m, err := rr.Row(y)
			if err != nil {
				return err
			}
			if mb := m.Bounds(); mb.Min.X != e.b.Min.X || mb.Max.X != e.b.Max.X {
				return errors.New("goimgtools: webp: strip does not span the image width")
			}
			if err := e.conv.row(m, y, k, y == e.b.Max.Y-1); err != nil {
				return err
			}
		}
		e.conv.pad(n)
		if err := e.encodeRow(mby); err != nil {
			return err
		}
	}
	// Let the source finish verifying its input.
	return rr.Drain()
}

func (e *encoder) encodeRow(mby int) error {
	// Follow the statistics of the image coded so far.
	if e.sinceCost >= 64 {
		e.stats.finalize(&e.probs)
		e.costs.build(&e.probs)
		e.sinceCost = 0
	}
	e.left = mbCtx{}
	for mbx := 0; mbx < e.mbw; mbx++ {
		e.encodeMB(mbx, mby)
	}
	e.sinceCost += e.mbw
	return e.sw.err
}

// prepare loads the borders of the workspace for macroblock (mbx, mby): the
// samples above, to the left and above-right of it, or the fixed values the
// format prescribes at the frame's edges.
func (e *encoder) prepare(mbx, mby int) {
	ws := &e.ws
	if mbx == 0 {
		for y := 0; y < 17; y++ {
			ws[y*bps+7] = 0x81
		}
		for y := 17; y < 26; y++ {
			ws[y*bps+7] = 0x81
			ws[y*bps+23] = 0x81
		}
	} else {
		for y := 0; y < 17; y++ {
			ws[y*bps+7] = ws[y*bps+7+16]
		}
		for y := 17; y < 26; y++ {
			ws[y*bps+7] = ws[y*bps+15]
			ws[y*bps+23] = ws[y*bps+31]
		}
	}
	if mby == 0 {
		for x := 7; x < 28; x++ {
			ws[x] = 0x7f
		}
		for x := 7; x < 16; x++ {
			ws[17*bps+x] = 0x7f
		}
		for x := 23; x < 32; x++ {
			ws[17*bps+x] = 0x7f
		}
	} else {
		copy(ws[8:24], e.topY[16*mbx:16*mbx+16])
		copy(ws[17*bps+8:17*bps+16], e.topU[8*mbx:8*mbx+8])
		copy(ws[17*bps+24:17*bps+32], e.topV[8*mbx:8*mbx+8])
		if mbx == e.mbw-1 {
			v := e.topY[16*mbx+15]
			ws[24], ws[25], ws[26], ws[27] = v, v, v, v
		} else {
			copy(ws[24:28], e.topY[16*mbx+16:16*mbx+20])
		}
	}
	for y := 4; y < 16; y += 4 {
		copy(ws[y*bps+24:y*bps+28], ws[24:28])
	}
}

func (e *encoder) encodeMB(mbx, mby int) {
	e.prepare(mbx, mby)
	w16 := e.mbw * 16
	for j := 0; j < 16; j++ {
		copy(e.ysrc[j*16:j*16+16], e.conv.y[j*w16+mbx*16:])
	}
	for j := 0; j < 8; j++ {
		copy(e.usrc[j*8:j*8+8], e.conv.u[j*(w16/2)+mbx*8:])
		copy(e.vsrc[j*8:j*8+8], e.conv.v[j*(w16/2)+mbx*8:])
	}
	up := &e.up[mbx]

	// Luma: the best 16x16 predictor, then 4x4 predictors if they do better.
	best := &e.c16[0]
	e.evalI16(best, predDC, mbx, mby, up)
	other := &e.c16[1]
	for _, mode := range [3]uint8{predTM, predVE, predHE} {
		e.evalI16(other, mode, mbx, mby, up)
		if other.score < best.score {
			best, other = other, best
		}
	}
	i4 := e.searchI4(up, best.score)

	// Chroma.
	uv := &e.cuv[0]
	e.evalUV(uv, predDC, mbx, mby, up)
	otherUV := &e.cuv[1]
	for _, mode := range [3]uint8{predTM, predVE, predHE} {
		e.evalUV(otherUV, mode, mbx, mby, up)
		if otherUV.score < uv.score {
			uv, otherUV = otherUV, uv
		}
	}

	// The modes of all macroblocks have to fit in the first partition,
	// which is limited to 512KB. Each macroblock may use its share of that
	// plus what earlier ones left over; one that would go beyond falls back
	// to cheaper modes. (Only images of tens of megapixels get here.)
	e.mbDone++
	allowed := e.modeMax / (int64(e.mbw) * int64(e.mbh)) * e.mbDone
	bits := e.modeCost(i4, best, uv, up)
	if e.modeBits+bits > allowed && i4 {
		i4 = false
		bits = e.modeCost(i4, best, uv, up)
	}
	if e.modeBits+bits > allowed {
		if best.mode != predDC {
			e.evalI16(best, predDC, mbx, mby, up)
		}
		if uv.mode != predDC {
			e.evalUV(uv, predDC, mbx, mby, up)
		}
		bits = e.modeCost(i4, best, uv, up)
	}
	e.modeBits += bits

	// Put the reconstruction in the workspace and keep its edges for the
	// macroblocks to the right and below.
	ws := &e.ws
	if !i4 {
		for j := 0; j < 16; j++ {
			copy(ws[wsY+j*bps:wsY+j*bps+16], best.rec[j*16:j*16+16])
		}
	}
	for j := 0; j < 8; j++ {
		copy(ws[wsB+j*bps:wsB+j*bps+8], uv.rec[0][j*8:j*8+8])
		copy(ws[wsR+j*bps:wsR+j*bps+8], uv.rec[1][j*8:j*8+8])
	}
	copy(e.topY[16*mbx:16*mbx+16], ws[wsY+15*bps:])
	copy(e.topU[8*mbx:8*mbx+8], ws[wsB+7*bps:])
	copy(e.topV[8*mbx:8*mbx+8], ws[wsR+7*bps:])
	if d := e.dbg; d != nil && d.rec {
		for j := 0; j < 16; j++ {
			copy(d.y[(mby*16+j)*w16+mbx*16:][:16], ws[wsY+j*bps:])
		}
		for j := 0; j < 8; j++ {
			copy(d.u[(mby*8+j)*(w16/2)+mbx*8:][:8], ws[wsB+j*bps:])
			copy(d.v[(mby*8+j)*(w16/2)+mbx*8:][:8], ws[wsR+j*bps:])
		}
	}

	e.emitMB(up, i4, best, uv)
}

// modeCost is the cost of a macroblock's prediction modes in the first
// partition, in 1/256 bit.
func (e *encoder) modeCost(i4 bool, c *cand16, uv *candUV, up *mbCtx) int64 {
	bits := int64(costUV[uv.mode])
	if !i4 {
		return bits + int64(costI16[c.mode])
	}
	bits += int64(costI4)
	for n, m := range e.c4.modes {
		bits += int64(costMode4[e.above4(n, up)][e.left4(n)][m])
	}
	return bits
}

// above4 and left4 are the modes of the 4x4 blocks above and to the left of
// block n of the current macroblock.
func (e *encoder) above4(n int, up *mbCtx) uint8 {
	if n < 4 {
		return up.pred[n]
	}
	return e.c4.modes[n-4]
}

func (e *encoder) left4(n int) uint8 {
	if n&3 == 0 {
		return e.left.pred[n>>2]
	}
	return e.c4.modes[n-1]
}

// evalI16 codes the luma with 16x16 predictor mode into c and scores it.
func (e *encoder) evalI16(c *cand16, mode uint8, mbx, mby int, up *mbCtx) {
	pred := &e.pred16
	predLarge(&e.ws, wsY, 16, mode, mbx, mby, pred[:])
	var (
		coef     [16][16]int16
		dcs, y2c [16]int16
		deq      [16]int16
	)
	for n := 0; n < 16; n++ {
		off := (n>>2)*64 + (n&3)*4
		fdct(e.ysrc[off:], 16, pred[off:], 16, &coef[n])
		dcs[n] = coef[n][0]
	}
	// The DC coefficients go through a second transform of their own.
	fwht(&dcs, &y2c)
	c.mode = mode
	c.y2last = e.qY2.quantize(&y2c, 0, &c.y2, &deq)
	iwht(&deq, &dcs)
	rate := int(costI16[mode]) + e.costs.blockCost(planeY2, 0, int(e.left.nzY16+up.nzY16), c.y2last, &c.y2)

	unz, lnz := up.nzMask, e.left.nzMask
	dist := 0
	for n := 0; n < 16; n++ {
		bx, by := uint(n&3), uint(n>>2)
		off := (n>>2)*64 + (n&3)*4
		last := e.qY1.quantize(&coef[n], 1, &c.lv[n], &deq)
		c.last[n] = int8(last)
		ctx := int(unz>>bx&1 + lnz>>by&1)
		rate += e.costs.blockCost(planeY1WithY2, 1, ctx, last, &c.lv[n])
		nz := uint8(0)
		if last >= 1 {
			nz = 1
		}
		unz = unz&^(1<<bx) | nz<<bx
		lnz = lnz&^(1<<by) | nz<<by
		switch {
		case last >= 1:
			deq[0] = dcs[n]
			idctAdd(&deq, pred[off:], 16, c.rec[off:], 16)
		case dcs[n] != 0:
			dcAdd(dcs[n], pred[off:], 16, c.rec[off:], 16)
		default:
			copy4(pred[off:], 16, c.rec[off:], 16)
		}
		dist += sse4(e.ysrc[off:], 16, c.rec[off:], 16)
	}
	c.score = int64(dist)<<16 + int64(rate)*e.lamY
}

// searchI4 codes the luma with a predictor per 4x4 block, giving up as soon
// as that is certain to score worse than limit. It reports whether 4x4
// prediction won; if so the reconstruction is in the workspace.
func (e *encoder) searchI4(up *mbCtx, limit int64) bool {
	c := &e.c4
	ws := &e.ws
	total := int64(costI4) * e.lamY
	if total >= limit {
		return false
	}
	unz, lnz := up.nzMask, e.left.nzMask
	var (
		coef, deq, lv [16]int16
		rec           [16]uint8
		bestRec       [16]uint8
		order         [nPred]uint8
		perr          [nPred]int64
	)
	for n := 0; n < 16; n++ {
		bx, by := uint(n&3), uint(n>>2)
		at := wsY + (n>>2)*4*bps + (n&3)*4
		off := (n>>2)*64 + (n&3)*4
		src := e.ysrc[off:]
		pred4All(ws, at, &e.pred4)
		mc := &costMode4[e.above4(n, up)][e.left4(n)]
		ctx := int(unz>>bx&1 + lnz>>by&1)

		// Rank the modes by prediction error plus the cost of the mode.
		for m := range order {
			order[m] = uint8(m)
			perr[m] = int64(sse4(src, 16, e.pred4[m][:], 4))<<16 + int64(mc[m])*e.lamY
		}
		tries := min(i4Tries, nPred)
		for i := 0; i < tries; i++ {
			k := i
			for j := i + 1; j < nPred; j++ {
				if perr[order[j]] < perr[order[k]] {
					k = j
				}
			}
			order[i], order[k] = order[k], order[i]
		}

		best := int64(-1)
		for _, m := range order[:tries] {
			p := e.pred4[m][:]
			fdct(src, 16, p, 4, &coef)
			last := e.qY1.quantize(&coef, 0, &lv, &deq)
			rate := int(mc[m]) + e.costs.blockCost(planeY1SansY2, 0, ctx, last, &lv)
			var dist int
			if last >= 0 {
				idctAdd(&deq, p, 4, rec[:], 4)
				dist = sse4(src, 16, rec[:], 4)
			} else {
				dist = sse4(src, 16, p, 4)
			}
			score := int64(dist)<<16 + int64(rate)*e.lamY
			if best < 0 || score < best {
				best = score
				c.modes[n] = m
				c.lv[n] = lv
				c.last[n] = int8(last)
				if last >= 0 {
					bestRec = rec
				} else {
					copy(bestRec[:], p)
				}
			}
		}
		total += best
		if total >= limit {
			return false
		}
		copy4(bestRec[:], 4, ws[at:], bps)
		nz := uint8(0)
		if c.last[n] >= 0 {
			nz = 1
		}
		unz = unz&^(1<<bx) | nz<<bx
		lnz = lnz&^(1<<by) | nz<<by
	}
	return true
}

// evalUV codes both chroma planes with predictor mode into c and scores it.
func (e *encoder) evalUV(c *candUV, mode uint8, mbx, mby int, up *mbCtx) {
	var coef, deq [16]int16
	c.mode = mode
	rate := int(costUV[mode])
	dist := 0
	unz, lnz := up.nzMask>>4, e.left.nzMask>>4
	for pl := 0; pl < 2; pl++ {
		src, at := e.usrc[:], wsB
		if pl == 1 {
			src, at = e.vsrc[:], wsR
		}
		pred := e.predC[pl][:]
		predLarge(&e.ws, at, 8, mode, mbx, mby, pred)
		rec := c.rec[pl][:]
		for n := 0; n < 4; n++ {
			bx, by := uint(pl*2+n&1), uint(pl*2+n>>1)
			off := (n>>1)*32 + (n&1)*4
			fdct(src[off:], 8, pred[off:], 8, &coef)
			lv := &c.lv[pl*4+n]
			last := e.qUV.quantize(&coef, 0, lv, &deq)
			c.last[pl*4+n] = int8(last)
			ctx := int(unz>>bx&1 + lnz>>by&1)
			rate += e.costs.blockCost(planeUV, 0, ctx, last, lv)
			nz := uint8(0)
			if last >= 0 {
				nz = 1
				idctAdd(&deq, pred[off:], 8, rec[off:], 8)
			} else {
				copy4(pred[off:], 8, rec[off:], 8)
			}
			unz = unz&^(1<<bx) | nz<<bx
			lnz = lnz&^(1<<by) | nz<<by
			dist += sse4(src[off:], 8, rec[off:], 8)
		}
	}
	c.score = int64(dist)<<16 + int64(rate)*e.lamUV
}
