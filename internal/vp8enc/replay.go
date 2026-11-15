package vp8enc

import (
	"errors"
	"io"
	"math/bits"
)

// The spool holds, per macroblock row (see rows.go): the contexts found
// above each macroblock, a stream with the modes of its macroblocks (a "no
// coefficients" bit, a "4x4 prediction" bit and the prediction modes as
// plain numbers) and a stream with their coefficient blocks, coded exactly
// as in a VP8 token partition but with the default probabilities. Both are
// arithmetic coded, so that the spool is about as small as the final file.
//
// Writing the file decodes the spool and codes it again with the frame's
// final probabilities: the modes into the first partition, the
// coefficients of every nParts-th row into each token partition. Each
// partition is coded on its own, at the same time as the others when there
// are helpers, which is what a row's contexts in the spool are for.

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

// init reads bytes off..end of sp.
func (d *boolDec) init(sp *store, off, end int64, buf []byte) {
	*d = boolDec{sp: sp, off: off, end: end, buf: buf, rangeM1: 254}
}

// readModes reads a macroblock's entry in a row's modes stream: modes holds
// the sixteen 4x4 modes when i4 is set and the 16x16 mode in modes[0]
// otherwise.
func readModes(d *boolDec, modes *[16]uint8) (skip, i4 bool, uvMode uint8) {
	skip = d.readBit(128)
	i4 = d.readBit(128)
	if i4 {
		for i := range modes {
			modes[i] = d.readUint(4)
		}
	} else {
		modes[0] = d.readUint(2)
	}
	return skip, i4, d.readUint(2)
}

// setPreds leaves a macroblock's luma modes for the macroblocks below and
// to the right.
func setPreds(up, left *mbCtx, i4 bool, modes *[16]uint8) {
	for i := 0; i < 4; i++ {
		if i4 {
			up.pred[i], left.pred[i] = modes[12+i], modes[4*i+3]
		} else {
			up.pred[i], left.pred[i] = modes[0], modes[0]
		}
	}
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
	bw.putBit(e.updateMap, 128)
	if e.updateMap {
		// The map, and each segment's quantizer and filter level as
		// absolute values (section 9.3). Unused segments repeat the last.
		bw.putBit(true, 128) // update the map
		bw.putBit(true, 128) // update the segments' values
		bw.putBit(true, 128) // absolute values
		seg := func(k int) *segment { return &e.segs[min(k, e.nSegs-1)] }
		signed := func(v, n int) {
			bw.putBit(v != 0, 128)
			if v != 0 {
				bw.putUint(uint32(max(v, -v)), n)
				bw.putBit(v < 0, 128)
			}
		}
		for k := 0; k < numSegments; k++ {
			signed(seg(k).q, 7)
		}
		for k := 0; k < numSegments; k++ {
			signed(seg(k).filter, 6)
		}
		for _, p := range e.segProb {
			bw.putBit(p != 255, 128)
			if p != 255 {
				bw.putUint(uint32(p), 8)
			}
		}
	}
	bw.putBit(false, 128) // normal (not simple) loop filter
	bw.putUint(uint32(e.segs[0].filter), 6)
	bw.putUint(0, 3)      // filter sharpness
	bw.putBit(false, 128) // no filter level adjustments
	log2Parts := uint32(0)
	for 1<<log2Parts < e.nParts {
		log2Parts++
	}
	bw.putUint(log2Parts, 2)
	bw.putUint(uint32(e.segs[0].q), 7)
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
func (e *encoder) putModes(bw *boolEnc, put func(bool, uint8), up, left *mbCtx, skip, i4 bool, modes *[16]uint8, uvMode uint8) {
	if e.useSkip {
		bw.putBit(skip, e.skipProb)
	}
	bw.putBit(!i4, 145)
	if i4 {
		for n, m := range modes {
			above, toLeft := up.pred[n&3], left.pred[n>>2]
			if n >= 4 {
				above = modes[n-4]
			}
			if n&3 != 0 {
				toLeft = modes[n-1]
			}
			walkMode4(m, &predProb[above][toLeft], put)
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

// writeModes codes the first partition: the frame header and the modes.
func (e *encoder) writeModes(ioBuf, readBuf []byte) error {
	var mw boolEnc
	mw.init(e.part0, ioBuf)
	e.putHeader(&mw)
	for i := range e.up {
		e.up[i] = mbCtx{}
	}
	var modes [16]uint8
	for r := range e.rows {
		var d boolDec
		d.init(e.spool, e.rows[r].modes, e.rows[r].coeffs, readBuf)
		var left mbCtx
		for x := range e.up {
			up := &e.up[x]
			skip, i4, uvMode := readModes(&d, &modes)
			if e.updateMap {
				s := e.segMap[r*e.mbw+x]
				if mw.putBit(s >= 2, e.segProb[0]); s >= 2 {
					mw.putBit(s == 3, e.segProb[2])
				} else {
					mw.putBit(s == 1, e.segProb[1])
				}
			}
			e.putModes(&mw, mw.putBit, up, &left, skip, i4, &modes, uvMode)
			setPreds(up, &left, i4, &modes)
		}
		if d.err != nil {
			return d.err
		}
	}
	var err error
	e.p0Len, err = mw.finish()
	return err
}

// writeTokens codes token partition part: the coefficients of every
// nParts-th macroblock row from row part on.
func (e *encoder) writeTokens(part int, ioBuf []byte, readBufs [2][]byte, ctxs []byte) error {
	var bw boolEnc
	e.partOff[part] = e.parts[part].Size()
	bw.init(e.parts[part], ioBuf)
	var (
		modes [16]uint8
		lv    [16]int16
	)
	for r := part; r < e.mbh; r += e.nParts {
		sp := e.rows[r]
		if _, err := e.spool.ReadAt(ctxs, sp.ctxs); err != nil {
			return err
		}
		var md, cd boolDec
		md.init(e.spool, sp.modes, sp.coeffs, readBufs[0])
		cd.init(e.spool, sp.coeffs, sp.end, readBufs[1])
		var left mbCtx
		for x := 0; x < e.mbw; x++ {
			skip, i4, _ := readModes(&md, &modes)
			up := mbCtx{nzMask: ctxs[2*x], nzY16: ctxs[2*x+1]}
			walkBlocks(i4, &up, &left, func(plane, first, ctx, n int) bool {
				if skip {
					// Without the skip flag an empty macroblock is an
					// "end of block" for each of its blocks.
					if !e.useSkip {
						bw.putBit(false, e.probs[plane][bands[first]][ctx][0])
					}
					return false
				}
				last := getCoeffs(&cd, &defaultTokenProb[plane], first, ctx, &lv)
				putCoeffs(&bw, &e.probs[plane], first, ctx, last, &lv)
				return last >= 0
			})
		}
		if md.err != nil {
			return md.err
		}
		if cd.err != nil {
			return cd.err
		}
	}
	var err error
	e.partLen[part], err = bw.finish()
	return err
}

// passReadBuf is the size of each read buffer a partition's coding uses.
const passReadBuf = 16 << 10

// spillBuf is the buffer a store takes once it moves to a temporary file.
const spillBuf = 256 << 10

// passBytes is the memory one partition's coding takes besides its store.
func (e *encoder) passBytes() int64 { return ioBufSize + 2*passReadBuf + 2*int64(e.mbw) }

// code codes the first partition and the token partitions: at the same
// time, each into a store of its own, when there are helpers and room for
// that; else one after the other into one store, as a small budget needs
// (a store that moves to a temporary file takes a 256KB buffer).
func (e *encoder) code() error {
	e.part0.Close()
	e.part0 = newStore(e.env)
	for p := range e.parts {
		e.parts[p].Close()
		e.parts[p] = nil
	}
	jobs := e.nParts + 1
	at := min(jobs, 1+e.env.Helpers())
	b := e.env.Budget
	if at > 1 && b.Limit()-b.Used() < int64(jobs)*(e.passBytes()+2*spillBuf) {
		at = 1
	}
	need := e.passBytes() * int64(at)
	if err := b.Reserve(need, "webp partitions"); err != nil {
		return err
	}
	defer b.Release(need)
	for p := 0; p < e.nParts; p++ {
		if at > 1 || p == 0 {
			e.parts[p] = newStore(e.env)
		} else {
			e.parts[p] = e.parts[0]
		}
	}
	errs := make([]error, jobs)
	type bufs struct {
		io   []byte
		read [2][]byte
		ctxs []byte
	}
	newBufs := func() *bufs {
		return &bufs{make([]byte, ioBufSize), [2][]byte{make([]byte, passReadBuf), make([]byte, passReadBuf)}, make([]byte, 2*e.mbw)}
	}
	job := func(j int, b *bufs) {
		if j == e.nParts {
			errs[j] = e.writeModes(b.io, b.read[0])
			return
		}
		errs[j] = e.writeTokens(j, b.io, b.read, b.ctxs)
	}
	if at == 1 {
		b := newBufs()
		for j := 0; j < jobs; j++ {
			job(j, b)
		}
	} else {
		e.env.Parallel(jobs, func(j int) { job(j, newBufs()) })
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// write arithmetic codes the spooled frame and writes the file.
func (e *encoder) write(w io.Writer) error {
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

	// The rows are spread over several token partitions (macroblock row y
	// goes to partition y mod n) so that the partitions can be coded at the
	// same time: up to 8, each of at least 8 rows (each costs a few bytes).
	// A partition is also limited to 16MB; the spool is a little larger
	// than the partitions together will be.
	e.nParts = 1
	for e.nParts < 8 && (e.mbh >= 16*e.nParts || e.spool.Size()/int64(e.nParts) > 12<<20) {
		e.nParts *= 2
	}
	if e.dbg != nil && e.dbg.forceParts != 0 {
		e.nParts = e.dbg.forceParts
	}
	// From here on several goroutines read the spool.
	if err := e.spool.settle(); err != nil {
		return err
	}
	for {
		if err := e.code(); err != nil {
			return err
		}
		fits := true
		for _, n := range e.partLen[:e.nParts] {
			fits = fits && n < 1<<24
		}
		if fits {
			break
		}
		if e.nParts == 8 {
			return errors.New("goimgtools: webp: image too large to encode at this quality")
		}
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
	for p, s := range e.parts[:e.nParts] {
		if err := e.copyOut(w, s, e.partOff[p], e.partLen[p]); err != nil {
			return err
		}
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
