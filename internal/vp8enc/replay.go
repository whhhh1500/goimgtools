package vp8enc

import (
	"errors"
	"io"
	"math/bits"
)

// The spool is itself one arithmetic-coded stream, so that it is about as
// small as the final file. For every macroblock it holds a "no coefficients"
// bit, a "4x4 prediction" bit, the prediction modes as plain numbers and,
// unless the macroblock is empty, its coefficient blocks coded exactly as in
// a VP8 token partition but with the default probabilities.
//
// Writing the file decodes the spool once per partition and codes the
// macroblocks again with the frame's final probabilities.

// walkBlocks visits the coefficient blocks of a macroblock in coding order:
// the Y2 block (index 24) when the luma is 16x16-predicted, the 16 luma
// blocks (0..15), then 4 U and 4 V blocks (16..23). For each it passes the
// plane type, the first coefficient position and the context from the blocks
// above and to the left; visit reports whether the block has coefficients.
// up and left are updated for the macroblocks below and to the right.
func walkBlocks(i4 bool, up, left *mbCtx, visit func(plane, first, ctx, n int) bool) {
	plane, first := planeY1SansY2, 0
	if !i4 {
		nz := uint8(0)
		if visit(planeY2, 0, int(left.nzY16+up.nzY16), 24) {
			nz = 1
		}
		left.nzY16, up.nzY16 = nz, nz
		plane, first = planeY1WithY2, 1
	}
	unz, lnz := up.nzMask, left.nzMask
	for n := 0; n < 24; n++ {
		bx, by := uint(n&3), uint(n>>2)
		if n >= 16 {
			// Bits 4-5 of the masks are for U, bits 6-7 for V.
			m := n - 16
			bx, by = uint(4+m>>2*2+m&1), uint(4+m>>2*2+m>>1&1)
			plane, first = planeUV, 0
		}
		nz := uint8(0)
		if visit(plane, first, int(unz>>bx&1+lnz>>by&1), n) {
			nz = 1
		}
		unz = unz&^(1<<bx) | nz<<bx
		lnz = lnz&^(1<<by) | nz<<by
	}
	up.nzMask, left.nzMask = unz, lnz
}

// emitMB records the final coding of a macroblock: it counts its tokens,
// appends it to the spool and leaves the contexts for its neighbours.
func (e *encoder) emitMB(up *mbCtx, i4 bool, c *cand16, uv *candUV) {
	lv, lasts := &c.lv, &c.last
	if i4 {
		lv, lasts = &e.c4.lv, &e.c4.last
	}
	// quantize returns -1 for an empty block whether it starts at
	// coefficient 0 or (below a Y2 block) at 1.
	skip := i4 || c.y2last < 0
	for _, l := range lasts {
		skip = skip && l < 0
	}
	for _, l := range uv.last {
		skip = skip && l < 0
	}

	sw := &e.sw
	sw.putBit(skip, 128)
	sw.putBit(i4, 128)
	if i4 {
		for _, m := range e.c4.modes {
			sw.putUint(uint32(m), 4)
		}
		for i := 0; i < 4; i++ {
			up.pred[i] = e.c4.modes[12+i]
			e.left.pred[i] = e.c4.modes[4*i+3]
		}
		if e.dbg != nil {
			e.dbg.usedI4++
		}
	} else {
		sw.putUint(uint32(c.mode), 2)
		for i := 0; i < 4; i++ {
			up.pred[i], e.left.pred[i] = c.mode, c.mode
		}
	}
	sw.putUint(uint32(uv.mode), 2)

	st := &e.stats
	if skip {
		// Count the "end of block" bits it would take to code this
		// macroblock without the skip flag.
		e.nSkip++
		st = &e.skipStats
	}
	walkBlocks(i4, up, &e.left, func(plane, first, ctx, n int) bool {
		var b *[16]int16
		var last int
		switch {
		case n < 16:
			b, last = &lv[n], int(lasts[n])
		case n < 24:
			b, last = &uv.lv[n-16], int(uv.last[n-16])
		default:
			b, last = &c.y2, c.y2last
		}
		st.record(plane, first, ctx, last, b)
		if skip {
			return false
		}
		putCoeffs(sw, &defaultTokenProb[plane], first, ctx, last, b)
		return last >= 0
	})
}

// boolDec is the boolean arithmetic decoder (the decoder's partition
// reader), reading the spool through a buffer.
type boolDec struct {
	sp       *store
	off, end int64
	buf      []byte
	r, n     int
	err      error

	rangeM1 uint32
	bits    uint32
	nBits   uint8
}

func (d *boolDec) init(sp *store, buf []byte) {
	*d = boolDec{sp: sp, end: sp.Size(), buf: buf, rangeM1: 254}
}

func (d *boolDec) fill() {
	d.r, d.n = 0, 0
	k := int(min(int64(len(d.buf)), d.end-d.off))
	if k <= 0 || d.err != nil {
		return
	}
	n, err := d.sp.ReadAt(d.buf[:k], d.off)
	if n < k {
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		d.err = err
		return
	}
	d.off += int64(n)
	d.n = n
}

func (d *boolDec) readBit(prob uint8) bool {
	if d.nBits < 8 {
		if d.r == d.n {
			d.fill()
		}
		// Past the end the stream continues with zeros.
		if d.r < d.n {
			d.bits |= uint32(d.buf[d.r]) << (8 - d.nBits)
			d.r++
		}
		d.nBits += 8
	}
	split := (d.rangeM1*uint32(prob))>>8 + 1
	bit := d.bits >= split<<8
	if bit {
		d.rangeM1 -= split
		d.bits -= split << 8
	} else {
		d.rangeM1 = split - 1
	}
	if d.rangeM1 < 127 {
		shift := uint8(bits.LeadingZeros8(uint8(d.rangeM1 + 1)))
		d.rangeM1 = (d.rangeM1+1)<<shift - 1
		d.bits <<= shift
		d.nBits -= shift
	}
	return bit
}

func (d *boolDec) readUint(n int) uint8 {
	var v uint8
	for ; n > 0; n-- {
		v <<= 1
		if d.readBit(128) {
			v |= 1
		}
	}
	return v
}

// getCoeffs is the inverse of putCoeffs: it reads one block into lv (scan
// order) and returns the position of its last non-zero coefficient, or -1.
func getCoeffs(d *boolDec, pr *[nBand][nContext][nProb]uint8, first, ctx int, lv *[16]int16) int {
	n := first
	p := &pr[bands[n]][ctx]
	if !d.readBit(p[0]) {
		return -1
	}
	last := -1
	for n < 16 {
		n++
		if !d.readBit(p[1]) {
			lv[n-1] = 0
			p = &pr[bands[n]][0]
			continue
		}
		v := 1
		if !d.readBit(p[2]) {
			p = &pr[bands[n]][1]
		} else {
			switch {
			case !d.readBit(p[3]):
				v = 2
				if d.readBit(p[4]) {
					v = 3
					if d.readBit(p[5]) {
						v = 4
					}
				}
			case !d.readBit(p[6]):
				if !d.readBit(p[7]) {
					v = 5
					if d.readBit(159) {
						v = 6
					}
				} else {
					v = 7
					if d.readBit(165) {
						v += 2
					}
					if d.readBit(145) {
						v++
					}
				}
			default:
				cat := 0
				if d.readBit(p[8]) {
					cat = 2
					if d.readBit(p[10]) {
						cat = 3
					}
				} else if d.readBit(p[9]) {
					cat = 1
				}
				v = 0
				for i, nb := 0, catBits[cat]; i < nb; i++ {
					v <<= 1
					if d.readBit(cat3456[cat][i]) {
						v |= 1
					}
				}
				v += 3 + 8<<uint(cat)
			}
			p = &pr[bands[n]][2]
		}
		if d.readBit(128) {
			v = -v
		}
		lv[n-1] = int16(v)
		last = n - 1
		if n == 16 || !d.readBit(p[0]) {
			break
		}
	}
	return last
}

// putHeader writes the frame header fields of the first partition
// (RFC 6386 section 9.2 onwards).
func (e *encoder) putHeader(bw *boolEnc) {
	bw.putBit(false, 128) // colour space: YUV as in BT.601
	bw.putBit(false, 128) // decoder must clamp pixel values
	bw.putBit(false, 128) // no segmentation
	bw.putBit(false, 128) // normal (not simple) loop filter
	bw.putUint(uint32(e.filterLevel), 6)
	bw.putUint(0, 3)      // filter sharpness
	bw.putBit(false, 128) // no filter level adjustments
	log2Parts := uint32(0)
	for 1<<log2Parts < e.nParts {
		log2Parts++
	}
	bw.putUint(log2Parts, 2)
	bw.putUint(uint32(e.baseQ), 7)
	for i := 0; i < 3; i++ {
		bw.putBit(false, 128) // no quantizer delta for luma DC and Y2
	}
	for i := 0; i < 2; i++ { // chroma DC and AC
		bw.putBit(e.uvDQ != 0, 128)
		if e.uvDQ != 0 {
			bw.putUint(uint32(max(e.uvDQ, -e.uvDQ)), 4)
			bw.putBit(e.uvDQ < 0, 128)
		}
	}
	bw.putBit(false, 128) // probabilities are not kept for a next frame
	for pl := range e.probs {
		for b := range e.probs[pl] {
			for c := range e.probs[pl][b] {
				for i, p := range e.probs[pl][b][c] {
					upd := p != defaultTokenProb[pl][b][c][i]
					bw.putBit(upd, tokenProbUpdateProb[pl][b][c][i])
					if upd {
						bw.putUint(uint32(p), 8)
					}
				}
			}
		}
	}
	bw.putBit(e.useSkip, 128)
	if e.useSkip {
		bw.putUint(uint32(e.skipProb), 8)
	}
}

// putModes writes the prediction modes of one macroblock to the first
// partition (chapter 11). modes holds the sixteen 4x4 modes when i4 is set
// and the 16x16 mode in modes[0] otherwise.
func (e *encoder) putModes(bw *boolEnc, put func(bool, uint8), up *mbCtx, skip, i4 bool, modes *[16]uint8, uvMode uint8) {
	if e.useSkip {
		bw.putBit(skip, e.skipProb)
	}
	bw.putBit(!i4, 145)
	if i4 {
		for n, m := range modes {
			above, left := up.pred[n&3], e.left.pred[n>>2]
			if n >= 4 {
				above = modes[n-4]
			}
			if n&3 != 0 {
				left = modes[n-1]
			}
			walkMode4(m, &predProb[above][left], put)
		}
	} else {
		m := modes[0]
		horiz := m == predTM || m == predHE
		bw.putBit(horiz, 156)
		if horiz {
			bw.putBit(m == predTM, 128)
		} else {
			bw.putBit(m == predVE, 163)
		}
	}
	bw.putBit(uvMode != predDC, 142)
	if uvMode != predDC {
		bw.putBit(uvMode != predVE, 114)
		if uvMode != predVE {
			bw.putBit(uvMode != predHE, 183)
		}
	}
}

// replay decodes the spool and appends token partition part to e.coded: the
// coefficients of every e.nParts-th macroblock row, coded with the final
// probabilities of the frame. The pass for partition 0 also produces the
// first partition (frame header and modes) in e.part0.
func (e *encoder) replay(part int) error {
	var d boolDec
	d.init(e.spool, e.readBuf)
	var bw, mw boolEnc
	bw.init(e.coded, e.ioBuf)
	withModes := part == 0
	if withModes {
		mw.init(e.part0, e.ioBuf0)
		e.putHeader(&mw)
	}
	putMode := mw.putBit
	for i := range e.up {
		e.up[i] = mbCtx{}
	}
	var (
		modes [16]uint8
		lv    [16]int16
	)
	for mby := 0; mby < e.mbh; mby++ {
		e.left = mbCtx{}
		code := mby%e.nParts == part
		for mbx := 0; mbx < e.mbw; mbx++ {
			up := &e.up[mbx]
			skip := d.readBit(128)
			i4 := d.readBit(128)
			if i4 {
				for i := range modes {
					modes[i] = d.readUint(4)
				}
			} else {
				modes[0] = d.readUint(2)
			}
			uvMode := d.readUint(2)
			if withModes {
				e.putModes(&mw, putMode, up, skip, i4, &modes, uvMode)
			}
			if i4 {
				for i := 0; i < 4; i++ {
					up.pred[i] = modes[12+i]
					e.left.pred[i] = modes[4*i+3]
				}
			} else {
				for i := 0; i < 4; i++ {
					up.pred[i], e.left.pred[i] = modes[0], modes[0]
				}
			}
			walkBlocks(i4, up, &e.left, func(plane, first, ctx, n int) bool {
				if skip {
					// Without the skip flag an empty macroblock is
					// an "end of block" for each of its blocks.
					if code && !e.useSkip {
						bw.putBit(false, e.probs[plane][bands[first]][ctx][0])
					}
					return false
				}
				last := getCoeffs(&d, &defaultTokenProb[plane], first, ctx, &lv)
				if code {
					putCoeffs(&bw, &e.probs[plane], first, ctx, last, &lv)
				}
				return last >= 0
			})
		}
		if d.err != nil {
			return d.err
		}
	}
	var err error
	if withModes {
		if e.p0Len, err = mw.finish(); err != nil {
			return err
		}
	}
	e.partLen[part], err = bw.finish()
	return err
}

// write arithmetic codes the spooled frame and writes the file.
func (e *encoder) write(w io.Writer) error {
	if _, err := e.sw.finish(); err != nil {
		return err
	}
	// The skip flag costs every macroblock a bit in the first partition;
	// use it when some macroblocks are empty and there is room for it.
	total := int64(e.mbw) * int64(e.mbh)
	if e.nSkip > 0 {
		p := uint8(max(1, min(255, 255*(total-int64(e.nSkip))/total)))
		bits := int64(e.nSkip)*int64(bitCost(true, p)) + (total-int64(e.nSkip))*int64(bitCost(false, p))
		if e.modeBits+bits <= e.modeMax {
			e.useSkip, e.skipProb = true, p
		}
	}
	if !e.useSkip {
		for pl := range e.stats {
			for b := range e.stats[pl] {
				for c := range e.stats[pl][b] {
					e.stats[pl][b][c][0].total += e.skipStats[pl][b][c][0].total
				}
			}
		}
	}
	e.stats.finalize(&e.probs)

	// A token partition is limited to 16MB; large frames are split over
	// several (macroblock row y goes to partition y mod n). The spool is a
	// little larger than the partitions together will be.
	e.nParts = 1
	for e.nParts < 8 && e.spool.Size()/int64(e.nParts) > 12<<20 {
		e.nParts *= 2
	}
	if e.dbg != nil && e.dbg.forceParts != 0 {
		e.nParts = e.dbg.forceParts
	}
	for {
		e.coded, e.part0 = newStore(e.env), newStore(e.env)
		fits := true
		for part := 0; part < e.nParts && fits; part++ {
			if err := e.replay(part); err != nil {
				return err
			}
			fits = e.partLen[part] < 1<<24
		}
		if fits {
			break
		}
		if e.nParts == 8 {
			return errors.New("goimgtools: webp: image too large to encode at this quality")
		}
		e.coded.Close()
		e.part0.Close()
		e.nParts *= 2
	}
	if e.p0Len > maxPartition0 {
		return errors.New("goimgtools: webp: image too large to encode")
	}
	if e.dbg != nil {
		e.dbg.spool = e.spool.Size()
		e.dbg.partitions = e.nParts
		e.dbg.modeBits, e.dbg.p0Len = e.modeBits, e.p0Len
		e.dbg.usedSkip = 0
		if e.useSkip {
			e.dbg.usedSkip = e.nSkip
		}
	}
	// The spool has served its purpose.
	e.spool.Close()
	e.spool = nil

	vp8Size := 10 + e.p0Len + 3*int64(e.nParts-1)
	for _, n := range e.partLen[:e.nParts] {
		vp8Size += n
	}
	// The alpha plane: compressed when that is smaller (and the budget has
	// room for the coder's row buffers), else as it is.
	hasAlpha := e.alpha != nil && e.alpha.transparent()
	var (
		alphaEnc  *alphaEnc
		alphaSize int64
		alphaHdr  byte // no preprocessing, no filter, no compression
	)
	if hasAlpha {
		alphaSize = 1 + int64(e.w)*int64(e.h)
		scratch := alphaScratchBytes(e.w)
		if e.env.Budget.Reserve(scratch, "webp alpha rows") == nil {
			defer e.env.Budget.Release(scratch)
			filter := e.alpha.useFilter()
			enc := newAlphaEnc(e.alpha, e.w, e.h, filter)
			if err := enc.prepare(e.readBuf); err != nil {
				return err
			}
			if 1+enc.size() < alphaSize {
				alphaEnc, alphaSize = enc, 1+enc.size()
				alphaHdr = 1 // VP8L compression
				if filter {
					alphaHdr |= 1 << 2 // horizontal filter
				}
			}
		}
	}
	riffSize := 4 + 8 + vp8Size + vp8Size&1
	if hasAlpha {
		riffSize += 8 + 10 + 8 + alphaSize + alphaSize&1
	}
	if riffSize > 1<<32-1 {
		return errors.New("goimgtools: webp: file would exceed 4GB")
	}

	hdr := make([]byte, 0, 64)
	le32 := func(v int64) { hdr = append(hdr, byte(v), byte(v>>8), byte(v>>16), byte(v>>24)) }
	le24 := func(v int64) { hdr = append(hdr, byte(v), byte(v>>8), byte(v>>16)) }
	hdr = append(hdr, "RIFF"...)
	le32(riffSize)
	hdr = append(hdr, "WEBP"...)
	if hasAlpha {
		hdr = append(hdr, "VP8X"...)
		le32(10)
		hdr = append(hdr, 0x10, 0, 0, 0) // has alpha
		le24(int64(e.w - 1))
		le24(int64(e.h - 1))
		hdr = append(hdr, "ALPH"...)
		le32(alphaSize)
		hdr = append(hdr, alphaHdr)
		if _, err := w.Write(hdr); err != nil {
			return err
		}
		if alphaEnc != nil {
			if err := alphaEnc.write(w, e.ioBuf, e.readBuf); err != nil {
				return err
			}
		} else {
			e.alpha.rewind(e.readBuf)
			row := e.conv.arow
			for y := 0; y < e.h; y++ {
				if err := e.alpha.next(row); err != nil {
					return err
				}
				if _, err := w.Write(row); err != nil {
					return err
				}
			}
		}
		hdr = hdr[:0]
		if alphaSize&1 != 0 {
			hdr = append(hdr, 0)
		}
	}
	// The alpha plane is no longer needed.
	e.alphaStore.Close()
	e.alphaStore = nil
	hdr = append(hdr, "VP8 "...)
	le32(vp8Size)
	le24(e.p0Len<<5 | 1<<4) // key frame, version 0, shown
	hdr = append(hdr, 0x9d, 0x01, 0x2a)
	hdr = append(hdr, byte(e.w), byte(e.w>>8), byte(e.h), byte(e.h>>8))
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if err := e.copyOut(w, e.part0, 0, e.p0Len); err != nil {
		return err
	}
	hdr = hdr[:0]
	for _, n := range e.partLen[:e.nParts-1] {
		le24(n)
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	if err := e.copyOut(w, e.coded, 0, e.coded.Size()); err != nil {
		return err
	}
	if vp8Size&1 != 0 {
		if _, err := w.Write([]byte{0}); err != nil {
			return err
		}
	}
	return nil
}

// copyOut writes n bytes of s, starting at off, to w.
func (e *encoder) copyOut(w io.Writer, s *store, off, n int64) error {
	for n > 0 {
		k := int(min(n, int64(len(e.readBuf))))
		if _, err := s.ReadAt(e.readBuf[:k], off); err != nil && err != io.EOF {
			return err
		}
		if _, err := w.Write(e.readBuf[:k]); err != nil {
			return err
		}
		off += int64(k)
		n -= int64(k)
	}
	return nil
}
