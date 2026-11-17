package vp8enc

// mbCoder codes macroblocks: the decisions for one macroblock row at a
// time, on one goroutine. Several of them code rows of the same frame at
// once (see rows.go); what they share is in the encoder.
type mbCoder struct {
	e     *encoder
	costs *costTables // the bit costs this row is decided with
	seg   *segment    // the segment of the macroblock being coded

	// The row's Y, U and V samples, padded to whole macroblocks.
	buf     []uint8
	y, u, v []uint8

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

	// What the row decided: the modes and the coefficients, each its own
	// arithmetic-coded stream, and the contexts found above each
	// macroblock, which decoding the coefficients again needs.
	modes, coeffs rowBuf
	mw, cw        boolEnc
	ctxs          []byte
	ioBufs        [2][]byte

	// What the row adds to the frame's totals.
	stats, skipStats tokenStats
	nSkip, usedI4    int
	leftDerr         [2][2]int8   // chroma DC errors from the left (dcdiffuse.go)
	err0             [2]int8      // the error of the first chroma block of each plane
	lf               *filterStats // SSIM per segment and filter level, or nil
	mf               *mbFilter
	mbSrc            [384]uint8
	grant            int64 // what the row's modes may take, in 1/256 bit
	rowBits          int64 // what they take
}

// mbCoderBytes is the memory of an mbCoder besides its stores.
func mbCoderBytes(w16 int) int64 {
	return int64(w16)*16 + int64(w16/2)*8*2 + // input row
		int64(w16/16)*2 + // contexts
		2*ioBufSize + 24<<10 + // buffers, and the fixed scratch above
		mbFilterBytes
}

func newMBCoder(e *encoder) *mbCoder {
	w16 := e.mbw * 16
	buf := make([]uint8, e.yuvRowBytes())
	c := &mbCoder{
		e:      e,
		buf:    buf,
		y:      buf[:w16*16],
		u:      buf[w16*16 : w16*20],
		v:      buf[w16*20:],
		modes:  rowBuf{env: e.env},
		coeffs: rowBuf{env: e.env},
		ctxs:   make([]byte, 2*e.mbw),
		ioBufs: [2][]byte{make([]byte, ioBufSize), make([]byte, ioBufSize)},
	}
	if e.autoFilter {
		c.lf, c.mf = new(filterStats), new(mbFilter)
	}
	return c
}

func (c *mbCoder) release() {
	c.modes.release()
	c.coeffs.release()
}

// startRow gets ready to code a macroblock row with the given bit costs
// and room for its modes.
func (c *mbCoder) startRow(costs *costTables, grant int64) {
	c.costs, c.grant, c.rowBits = costs, grant, 0
	c.left = mbCtx{}
	c.leftDerr = [2][2]int8{}
	c.stats, c.skipStats = tokenStats{}, tokenStats{}
	c.nSkip, c.usedI4 = 0, 0
	if c.lf != nil {
		*c.lf = filterStats{}
	}
	c.modes.reset()
	c.coeffs.reset()
	c.mw.init(&c.modes, c.ioBufs[0])
	c.cw.init(&c.coeffs, c.ioBufs[1])
}

// finishRow ends the row's streams.
func (c *mbCoder) finishRow() error {
	if _, err := c.mw.finish(); err != nil {
		return err
	}
	_, err := c.cw.finish()
	return err
}

// prepare loads the borders of the workspace for macroblock (mbx, mby): the
// samples above, to the left and above-right of it, or the fixed values the
// format prescribes at the frame's edges.
func (c *mbCoder) prepare(mbx, mby int) {
	e, ws := c.e, &c.ws
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

// encodeMB decides how macroblock (mbx, mby) is coded, records it and
// leaves its reconstruction's edges and contexts for its neighbours. The
// row above must be done to mbx+1.
func (c *mbCoder) encodeMB(mbx, mby int) {
	e := c.e
	c.seg = &e.segs[e.segMap[mby*e.mbw+mbx]]
	c.prepare(mbx, mby)
	w16 := e.mbw * 16
	for j := 0; j < 16; j++ {
		copy(c.ysrc[j*16:j*16+16], c.y[j*w16+mbx*16:])
	}
	for j := 0; j < 8; j++ {
		copy(c.usrc[j*8:j*8+8], c.u[j*(w16/2)+mbx*8:])
		copy(c.vsrc[j*8:j*8+8], c.v[j*(w16/2)+mbx*8:])
	}
	up := &e.up[mbx]

	// Luma: the best 16x16 predictor, then 4x4 predictors if they do better.
	best := &c.c16[0]
	c.evalI16(best, predDC, mbx, mby, up)
	other := &c.c16[1]
	for _, mode := range [3]uint8{predTM, predVE, predHE} {
		c.evalI16(other, mode, mbx, mby, up)
		if other.score < best.score {
			best, other = other, best
		}
	}
	i4 := c.searchI4(up, best.score)

	// Chroma.
	uv := &c.cuv[0]
	c.evalUV(uv, predDC, mbx, mby, up)
	otherUV := &c.cuv[1]
	for _, mode := range [3]uint8{predTM, predVE, predHE} {
		c.evalUV(otherUV, mode, mbx, mby, up)
		if otherUV.score < uv.score {
			uv, otherUV = otherUV, uv
		}
	}

	// The modes of all macroblocks have to fit in the first partition,
	// which is limited to 512KB. Each row has its share of that (see
	// rows.go), and each macroblock its share of the row's plus what
	// those before it left over; one that would go beyond falls back to
	// cheaper modes. (Only images of tens of megapixels get here.)
	allowed := c.grant - e.mbShare*int64(e.mbw-1-mbx)
	bits := c.modeCost(i4, best, uv, up)
	if c.rowBits+bits > allowed && i4 {
		i4 = false
		bits = c.modeCost(i4, best, uv, up)
	}
	if c.rowBits+bits > allowed {
		if best.mode != predDC {
			c.evalI16(best, predDC, mbx, mby, up)
		}
		if uv.mode != predDC {
			c.evalUV(uv, predDC, mbx, mby, up)
		}
		bits = c.modeCost(i4, best, uv, up)
	}
	c.rowBits += bits
	if e.topDerr != nil {
		c.storeDerr(mbx, uv)
	}

	// Put the reconstruction in the workspace and keep its edges for the
	// macroblocks to the right and below.
	ws := &c.ws
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

	// A decoder filters the edges inside a macroblock unless it has
	// 16x16 prediction and no coefficients.
	if skip := c.emitMB(mbx, up, i4, best, uv); c.lf != nil && (i4 || !skip) {
		m := c.mf
		for j := 0; j < 16; j++ {
			copy(m.rec[j*16:j*16+16], ws[wsY+j*bps:])
		}
		for j := 0; j < 8; j++ {
			copy(m.rec[256+j*8:256+j*8+8], ws[wsB+j*bps:])
			copy(m.rec[320+j*8:320+j*8+8], ws[wsR+j*bps:])
		}
		copy(c.mbSrc[:256], c.ysrc[:])
		copy(c.mbSrc[256:320], c.usrc[:])
		copy(c.mbSrc[320:], c.vsrc[:])
		s := int(e.segMap[mby*e.mbw+mbx])
		m.measure(&c.mbSrc, c.seg, s, c.lf)
	}
}

// modeCost is the cost of a macroblock's prediction modes in the first
// partition, in 1/256 bit.
func (c *mbCoder) modeCost(i4 bool, c16 *cand16, uv *candUV, up *mbCtx) int64 {
	bits := int64(costUV[uv.mode])
	if !i4 {
		return bits + int64(costI16[c16.mode])
	}
	bits += int64(costI4)
	for n, m := range c.c4.modes {
		bits += int64(costMode4[c.above4(n, up)][c.left4(n)][m])
	}
	return bits
}

// above4 and left4 are the modes of the 4x4 blocks above and to the left of
// block n of the current macroblock.
func (c *mbCoder) above4(n int, up *mbCtx) uint8 {
	if n < 4 {
		return up.pred[n]
	}
	return c.c4.modes[n-4]
}

func (c *mbCoder) left4(n int) uint8 {
	if n&3 == 0 {
		return c.left.pred[n>>2]
	}
	return c.c4.modes[n-1]
}

// evalI16 codes the luma with 16x16 predictor mode into cd and scores it.
func (c *mbCoder) evalI16(cd *cand16, mode uint8, mbx, mby int, up *mbCtx) {
	pred := &c.pred16
	predLarge(&c.ws, wsY, 16, mode, mbx, mby, pred[:])
	var (
		coef     [16][16]int16
		dcs, y2c [16]int16
		deq      [16]int16
	)
	for n := 0; n < 16; n++ {
		off := (n>>2)*64 + (n&3)*4
		fdct(c.ysrc[off:], 16, pred[off:], 16, &coef[n])
		dcs[n] = coef[n][0]
	}
	// The DC coefficients go through a second transform of their own.
	fwht(&dcs, &y2c)
	cd.mode = mode
	cd.y2last = c.seg.qY2.quantize(&y2c, 0, &cd.y2, &deq)
	iwht(&deq, &dcs)
	rate := int(costI16[mode]) + c.costs.blockCost(planeY2, 0, int(c.left.nzY16+up.nzY16), cd.y2last, &cd.y2)

	unz, lnz := up.nzMask, c.left.nzMask
	dist := 0
	for n := 0; n < 16; n++ {
		bx, by := uint(n&3), uint(n>>2)
		off := (n>>2)*64 + (n&3)*4
		ctx := int(unz>>bx&1 + lnz>>by&1)
		last := c.seg.qY1.quantize(&coef[n], 1, &cd.lv[n], &deq)
		cd.last[n] = int8(last)
		rate += c.costs.blockCost(planeY1WithY2, 1, ctx, last, &cd.lv[n])
		nz := uint8(0)
		if last >= 1 {
			nz = 1
		}
		unz = unz&^(1<<bx) | nz<<bx
		lnz = lnz&^(1<<by) | nz<<by
		switch {
		case last >= 1:
			deq[0] = dcs[n]
			idctAdd(&deq, pred[off:], 16, cd.rec[off:], 16)
		case dcs[n] != 0:
			dcAdd(dcs[n], pred[off:], 16, cd.rec[off:], 16)
		default:
			copy4(pred[off:], 16, cd.rec[off:], 16)
		}
		dist += sse4(c.ysrc[off:], 16, cd.rec[off:], 16)
	}
	cd.score = int64(dist)<<16 + int64(rate)*c.seg.lamY
}

// searchI4 codes the luma with a predictor per 4x4 block, giving up as soon
// as that is certain to score worse than limit. It reports whether 4x4
// prediction won; if so the reconstruction is in the workspace.
func (c *mbCoder) searchI4(up *mbCtx, limit int64) bool {
	sg := c.seg
	c4 := &c.c4
	ws := &c.ws
	total := int64(costI4) * sg.lamY
	if total >= limit {
		return false
	}
	unz, lnz := up.nzMask, c.left.nzMask
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
		src := c.ysrc[off:]
		pred4All(ws, at, &c.pred4)
		mc := &costMode4[c.above4(n, up)][c.left4(n)]
		ctx := int(unz>>bx&1 + lnz>>by&1)

		// Rank the modes by prediction error plus the cost of the mode.
		for m := range order {
			order[m] = uint8(m)
			perr[m] = int64(sse4(src, 16, c.pred4[m][:], 4))<<16 + int64(mc[m])*sg.lamY
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
			p := c.pred4[m][:]
			fdct(src, 16, p, 4, &coef)
			var last, bits int
			if c.e.trellis && !c.e.trellisFinal {
				last, bits = c.trellis(&coef, 0, ctx, planeY1SansY2, &sg.qY1, sg.lamT, &lv, &deq)
			} else {
				last = sg.qY1.quantize(&coef, 0, &lv, &deq)
				bits = c.costs.blockCost(planeY1SansY2, 0, ctx, last, &lv)
			}
			rate := int(mc[m]) + bits
			out := p
			if last >= 0 {
				idctAdd(&deq, p, 4, rec[:], 4)
				out = rec[:]
			}
			score := int64(sse4(src, 16, out, 4))<<16 + int64(rate)*sg.lamY
			if best < 0 || score < best {
				best = score
				c4.modes[n] = m
				c4.lv[n] = lv
				c4.last[n] = int8(last)
				if last >= 0 {
					bestRec = rec
				} else {
					copy(bestRec[:], p)
				}
			}
		}
		if c.e.trellis && c.e.trellisFinal {
			// The mode chosen by plain quantization (effort 3), coded
			// again with the trellis.
			m := c4.modes[n]
			p := c.pred4[m][:]
			fdct(src, 16, p, 4, &coef)
			last, bits := c.trellis(&coef, 0, ctx, planeY1SansY2, &sg.qY1, sg.lamT, &lv, &deq)
			rate := int(mc[m]) + bits
			out := p
			if last >= 0 {
				idctAdd(&deq, p, 4, rec[:], 4)
				out = rec[:]
			}
			best = int64(sse4(src, 16, out, 4))<<16 + int64(rate)*sg.lamY
			c4.lv[n] = lv
			c4.last[n] = int8(last)
			if last >= 0 {
				bestRec = rec
			} else {
				copy(bestRec[:], p)
			}
		}
		total += best
		if total >= limit {
			return false
		}
		copy4(bestRec[:], 4, ws[at:], bps)
		nz := uint8(0)
		if c4.last[n] >= 0 {
			nz = 1
		}
		unz = unz&^(1<<bx) | nz<<bx
		lnz = lnz&^(1<<by) | nz<<by
	}
	return true
}

// evalUV codes both chroma planes with predictor mode into cd and scores it.
func (c *mbCoder) evalUV(cd *candUV, mode uint8, mbx, mby int, up *mbCtx) {
	var coef, deq [16]int16
	cd.mode = mode
	rate := int(costUV[mode])
	dist := 0
	unz, lnz := up.nzMask>>4, c.left.nzMask>>4
	for pl := 0; pl < 2; pl++ {
		src, at := c.usrc[:], wsB
		if pl == 1 {
			src, at = c.vsrc[:], wsR
		}
		pred := c.predC[pl][:]
		predLarge(&c.ws, at, 8, mode, mbx, mby, pred)
		rec := cd.rec[pl][:]
		for n := 0; n < 4; n++ {
			bx, by := uint(pl*2+n&1), uint(pl*2+n>>1)
			off := (n>>1)*32 + (n&1)*4
			fdct(src[off:], 8, pred[off:], 8, &coef)
			if c.e.topDerr != nil {
				c.diffuseDC(&coef, pl, n, mbx, cd)
			}
			lv := &cd.lv[pl*4+n]
			last := c.seg.qUV.quantize(&coef, 0, lv, &deq)
			cd.last[pl*4+n] = int8(last)
			ctx := int(unz>>bx&1 + lnz>>by&1)
			rate += c.costs.blockCost(planeUV, 0, ctx, last, lv)
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
	cd.score = int64(dist)<<16 + int64(rate)*c.seg.lamUV
}

// emitMB records the final coding of macroblock mbx: it counts its tokens,
// appends it to the row's streams and leaves the contexts for its
// neighbours.
func (c *mbCoder) emitMB(mbx int, up *mbCtx, i4 bool, c16 *cand16, uv *candUV) (skip bool) {
	lv, lasts := &c16.lv, &c16.last
	if i4 {
		lv, lasts = &c.c4.lv, &c.c4.last
	}
	// quantize returns -1 for an empty block whether it starts at
	// coefficient 0 or (below a Y2 block) at 1.
	skip = i4 || c16.y2last < 0
	for _, l := range lasts {
		skip = skip && l < 0
	}
	for _, l := range uv.last {
		skip = skip && l < 0
	}

	mw := &c.mw
	mw.putBit(skip, 128)
	mw.putBit(i4, 128)
	if i4 {
		for _, m := range c.c4.modes {
			mw.putUint(uint32(m), 4)
		}
		for i := 0; i < 4; i++ {
			up.pred[i] = c.c4.modes[12+i]
			c.left.pred[i] = c.c4.modes[4*i+3]
		}
		c.usedI4++
	} else {
		mw.putUint(uint32(c16.mode), 2)
		for i := 0; i < 4; i++ {
			up.pred[i], c.left.pred[i] = c16.mode, c16.mode
		}
	}
	mw.putUint(uint32(uv.mode), 2)

	// The contexts from above, which the coefficients are coded with.
	c.ctxs[2*mbx], c.ctxs[2*mbx+1] = up.nzMask, up.nzY16

	st := &c.stats
	if skip {
		// Count the "end of block" bits it would take to code this
		// macroblock without the skip flag.
		c.nSkip++
		st = &c.skipStats
	}
	walkBlocks(i4, up, &c.left, func(plane, first, ctx, n int) bool {
		var b *[16]int16
		var last int
		switch {
		case n < 16:
			b, last = &lv[n], int(lasts[n])
		case n < 24:
			b, last = &uv.lv[n-16], int(uv.last[n-16])
		default:
			b, last = &c16.y2, c16.y2last
		}
		st.record(plane, first, ctx, last, b)
		if skip {
			return false
		}
		putCoeffs(&c.cw, &defaultTokenProb[plane], first, ctx, last, b)
		return last >= 0
	})
	return skip
}
