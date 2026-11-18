package jpegstream

// Options.Progressive: a progressive JPEG with Huffman tables made for each
// scan.
//
// A progressive file codes the quantized coefficients in several scans, so
// the whole image's coefficients are needed several times. The pixel side
// stays as it is (16-row bands, FDCT, quantization); the coefficients go
// into a pipeline.Scratch as int16 in zig-zag order, 128 bytes per block, one
// MCU row after the other, which is a temporary file unless the budget's
// staging share has room for all of it. Only one MCU row is in memory.
//
// Every scan needs the statistics of its own symbols before its tables can
// be written. They are all gathered during the pixel pass: each MCU row is
// run through every scan's coder in counting mode as soon as it has been
// quantized. After the pixel pass each scan reads back the rows of its
// components once and writes them.
//
// The scan script is searched for (see writer_progsearch.go); the coding of
// a scan (EOB runs, correction bits of refinement scans) is a port of jcphuff.c,
// both from the Independent JPEG Group's library as kept in libjpeg-turbo
// (jcparam.c, jcphuff.c; copyright 1991-1997 Thomas G. Lane, libjpeg-turbo
// modifications copyright D. R. Commander and others; IJG and BSD-style
// licences). See section G of the JPEG specification.

import (
	"errors"
	"image"
	"io"
	mbits "math/bits"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// progComp is where a component's blocks are within a stored MCU row.
type progComp struct {
	stride int // stored blocks per block row (padded to whole MCUs)
	v      int // block rows per MCU row
	bw, bh int // the component's real size in blocks: what a scan of this component alone covers
	off    int // offset of the component's blocks in the MCU row
	buf    []byte
	// last is, for each block of buf, the zig-zag index of its last
	// nonzero coefficient (0 when only DC may be): beyond it a scan only
	// counts zeros.
	last []uint8
}

// findLast sets c.last for the blocks in c.buf.
func (c *progComp) findLast() {
	for i := range c.last {
		w := c.buf[i*blockSize*2 : (i+1)*blockSize*2]
		if acMaskSIMD {
			// The highest AC coefficient that is not zero, 0 for none.
			last := 0
			if m := acMaskAsm(&w[0], 0) &^ 1; m != 0 {
				last = 63 - mbits.LeadingZeros64(m)
			}
			c.last[i] = uint8(last)
			continue
		}
		k := blockSize - 1
		for k > 0 && w[2*k] == 0 && w[2*k+1] == 0 {
			k--
		}
		c.last[i] = uint8(k)
	}
}

// progStore holds the quantized coefficients.
type progStore struct {
	sc       *pipeline.Scratch
	comp     []progComp
	mxx, myy int    // MCUs per row, MCU rows
	row      []byte // one MCU row; the components' buf are parts of it
}

func (st *progStore) rowOff(m int) int64 { return int64(m) * int64(len(st.row)) }

// loadRow reads MCU row m of the given components.
func (st *progStore) loadRow(m int, comps []int) error {
	if len(comps) == len(st.comp) {
		if _, err := st.sc.ReadAt(st.row, st.rowOff(m)); err != nil {
			return err
		}
	} else {
		for _, ci := range comps {
			c := &st.comp[ci]
			if _, err := st.sc.ReadAt(c.buf, st.rowOff(m)+int64(c.off)); err != nil {
				return err
			}
		}
	}
	for _, ci := range comps {
		st.comp[ci].findLast()
	}
	return nil
}

// coefAt returns coefficient k (zig-zag) of the stored block w.
func coefAt(w []byte, k int) int32 {
	return int32(int16(uint16(w[2*k]) | uint16(w[2*k+1])<<8))
}

// maxCorrBits is the number of correction bits a refinement scan buffers
// before it has to end the EOB run they belong to.
const maxCorrBits = 1000

// progScan is one scan of the script and its coder.
type progScan struct {
	comps          []int // every component for a DC scan, one for an AC scan
	ss, se, ah, al int

	e      *encoder
	gather bool  // count symbols instead of writing
	extra  int64 // in counting mode, the bits besides the codes
	// Statistics and codes, by table number: 0 luma, 1 chroma.
	freq   [2][257]int64
	lut    [2]codeLUT
	lastDC [3]int32
	// eobRun is the number of blocks that ended (within this scan's band)
	// since the last coded coefficient; corr[:be] are the correction bits
	// of those blocks.
	eobRun int
	be     int
	corr   [maxCorrBits]byte
}

func (s *progScan) symbol(t int, v uint8) {
	if s.gather {
		s.freq[t][v]++
		return
	}
	s.e.emitCode(&s.lut[t], v)
}

// putBits writes the low n bits of v.
func (s *progScan) putBits(v uint32, n uint32) {
	if s.gather {
		s.extra += int64(n)
		return
	}
	if n == 0 {
		return
	}
	s.e.emit(v&(1<<n-1), n)
}

func (s *progScan) putCorr(p []byte) {
	if s.gather {
		s.extra += int64(len(p))
		return
	}
	for _, b := range p {
		s.e.emit(uint32(b), 1)
	}
}

// emitEOBRun codes the pending run of ended blocks and their correction bits.
func (s *progScan) emitEOBRun(t int) {
	if s.eobRun == 0 {
		return
	}
	n := uint32(mbits.Len32(uint32(s.eobRun)) - 1)
	s.symbol(t, uint8(n<<4))
	s.putBits(uint32(s.eobRun), n)
	s.eobRun = 0
	s.putCorr(s.corr[:s.be])
	s.be = 0
}

// dcFirst codes the DC coefficient without its low al bits.
func (s *progScan) dcFirst(ci int, w []byte) {
	// The point transform of DC is an arithmetic shift.
	v := coefAt(w, 0) >> s.al
	d := v - s.lastDC[ci]
	s.lastDC[ci] = v
	a, b := d, d
	if a < 0 {
		a, b = -d, d-1
	}
	n := uint32(mbits.Len32(uint32(a)))
	s.symbol(min(ci, 1), uint8(n))
	s.putBits(uint32(b), n)
}

// dcRefine sends bit al of the DC coefficient.
func (s *progScan) dcRefine(w []byte) {
	s.putBits(uint32(coefAt(w, 0)>>s.al)&1, 1)
}

// acFirst codes coefficients ss to se without their low al bits; those
// after last are zero.
//
// As libjpeg-turbo's encode_mcu_AC_first does, it first marks the
// coefficients that are not zero once their low bits are dropped (without
// a branch), then goes from one marked coefficient to the next: the scan
// search codes every candidate scan of every block this way, and most
// coefficients of a band are zero.
func (s *progScan) acFirst(t int, w []byte, last int) {
	end := min(s.se, last)
	al := uint(s.al)
	var m uint64
	if acMaskSIMD {
		if end >= s.ss {
			m = acMaskAsm(&w[0], al) & (^uint64(0) >> uint(63-end)) &^ (1<<uint(s.ss) - 1)
		}
	} else {
		for k := s.ss; k <= end; k++ {
			v := coefAt(w, k)
			a := uint32((v^v>>31)-v>>31) >> al
			m |= uint64((a|-a)>>31) << uint(k)
		}
	}
	k := s.ss // the position after the last coefficient coded
	for m != 0 {
		j := mbits.TrailingZeros64(m)
		m &= m - 1
		// The point transform of AC divides, rounding towards zero.
		v := coefAt(w, j)
		a, b := v>>al, v>>al
		if v < 0 {
			a = -v >> al
			b = ^a
		}
		if s.eobRun > 0 {
			s.emitEOBRun(t)
		}
		r := j - k
		for r > 15 {
			s.symbol(t, 0xf0)
			r -= 16
		}
		n := uint32(mbits.Len32(uint32(a)))
		s.symbol(t, uint8(r<<4)|uint8(n))
		s.putBits(uint32(b), n)
		k = j + 1
	}
	if k <= s.se {
		s.eobRun++
		if s.eobRun == 0x7fff {
			s.emitEOBRun(t)
		}
	}
}

// acRefine sends bit al of coefficients ss to se: for a coefficient that
// was already nonzero a correction bit, for one that becomes nonzero now a
// symbol with the run of still-zero coefficients before it.
//
// As acFirst, it goes from one coefficient that is not zero at this bit
// plane to the next, using two masks: those (nz), and those that become
// nonzero now (one: |c| >> al is 1); the coefficients in between are the
// zeros of the runs.
func (s *progScan) acRefine(t int, w []byte, last int) {
	end := min(s.se, last)
	al := uint(s.al)
	var nz, one uint64
	if end >= s.ss {
		if acMaskSIMD {
			rm := (^uint64(0) >> uint(63-end)) &^ (1<<uint(s.ss) - 1)
			n0, n1 := acMaskAsm(&w[0], al), acMaskAsm(&w[0], al+1)
			nz, one = n0&rm, n0&^n1&rm
		} else {
			for k := s.ss; k <= end; k++ {
				v := coefAt(w, k)
				a := uint32((v^v>>31)-v>>31) >> al
				nz |= uint64((a|-a)>>31) << uint(k)
				e := a ^ 1
				one |= uint64(1^(e|-e)>>31) << uint(k)
			}
		}
	}
	// eob is the last coefficient that becomes nonzero in this scan.
	eob := 0
	if one != 0 {
		eob = 63 - mbits.LeadingZeros64(one)
	}
	// This block's correction bits are corr[start:start+nbr]: after those of
	// the pending EOB run, or at the front once that run has been written.
	r, start, nbr := 0, s.be, 0
	p := s.ss - 1 // the last coefficient visited
	for m := nz; m != 0; m &= m - 1 {
		k := mbits.TrailingZeros64(m)
		r += k - p - 1
		p = k
		// Write the zero runs that are needed; not after the last newly
		// nonzero coefficient, where an EOB does the job.
		for r > 15 && k <= eob {
			s.emitEOBRun(t)
			s.symbol(t, 0xf0)
			r -= 16
			s.putCorr(s.corr[start : start+nbr])
			start, nbr = 0, 0
		}
		v := coefAt(w, k)
		if one&(1<<uint(k)) == 0 {
			// Already nonzero: only its correction bit.
			s.corr[start+nbr] = byte(((v^v>>31)-v>>31)>>al) & 1
			nbr++
			continue
		}
		s.emitEOBRun(t)
		s.symbol(t, uint8(r<<4)|1)
		if v < 0 {
			s.putBits(0, 1)
		} else {
			s.putBits(1, 1)
		}
		s.putCorr(s.corr[start : start+nbr])
		start, nbr = 0, 0
		r = 0
	}
	// The coefficients after the last one visited are zero.
	r += s.se - p
	if r > 0 || nbr > 0 {
		s.eobRun++
		s.be += nbr
		// Write the run before its counter or the bit buffer could overflow.
		if s.eobRun == 0x7fff || s.be > maxCorrBits-blockSize+1 {
			s.emitEOBRun(t)
		}
	}
}

// mcuRow codes the scan's part of MCU row m, which is in st's buffers.
func (s *progScan) mcuRow(st *progStore, m int) {
	if s.ss == 0 {
		dc := func(ci int, w []byte) {
			if s.ah == 0 {
				s.dcFirst(ci, w)
			} else {
				s.dcRefine(w)
			}
		}
		if len(st.comp) == 1 {
			c := &st.comp[0]
			for bx := 0; bx < c.bw; bx++ {
				dc(0, c.buf[bx*blockBytes:])
			}
			return
		}
		y, cb, cr := &st.comp[0], &st.comp[1], &st.comp[2]
		h := y.stride / st.mxx // Y blocks across an MCU
		for mx := 0; mx < st.mxx; mx++ {
			for r := 0; r < y.v; r++ {
				for x := 0; x < h; x++ {
					dc(0, y.buf[(r*y.stride+h*mx+x)*blockBytes:])
				}
			}
			dc(1, cb.buf[mx*blockBytes:])
			dc(2, cr.buf[mx*blockBytes:])
		}
		return
	}
	// A scan of one component covers the component's own blocks, without
	// the ones that only pad the image to whole MCUs.
	ci := s.comps[0]
	c, t := &st.comp[ci], min(ci, 1)
	for r := 0; r < c.v && m*c.v+r < c.bh; r++ {
		for bx := 0; bx < c.bw; bx++ {
			o := (r*c.stride + bx) * blockBytes
			w := c.buf[o : o+blockBytes : o+blockBytes]
			last := int(c.last[r*c.stride+bx])
			if s.ah == 0 {
				s.acFirst(t, w, last)
			} else {
				s.acRefine(t, w, last)
			}
		}
	}
}

// finish ends the scan: the pending EOB run, then padding to a whole byte.
func (s *progScan) finish() {
	if s.ss != 0 {
		s.emitEOBRun(min(s.comps[0], 1))
	}
	if !s.gather {
		// Pad the last byte with 1's.
		s.e.padBits()
	}
	s.lastDC = [3]int32{}
}

// writeHeader writes the scan's Huffman tables and its SOS marker.
func (s *progScan) writeHeader() {
	e := s.e
	switch {
	case s.ss == 0 && s.ah == 0:
		var specs [2]huffmanSpec
		n := min(len(s.comps), 2)
		for t := 0; t < n; t++ {
			specs[t] = optimalSpec(&s.freq[t])
			s.lut[t].init(&specs[t])
		}
		e.writeDHTSpecs([]byte{0x00, 0x01}[:n], specs[:n])
	case s.ss != 0:
		t := min(s.comps[0], 1)
		spec := optimalSpec(&s.freq[t])
		s.lut[t].init(&spec)
		e.writeDHTSpecs([]byte{0x10 | byte(t)}, []huffmanSpec{spec})
	}
	n := len(s.comps)
	e.writeMarkerHeader(sosMarker, 6+2*n)
	e.buf[0] = uint8(n)
	for i, ci := range s.comps {
		e.buf[1+2*i] = uint8(ci + 1)
		// A DC scan names a DC table (none when refining), an AC scan an
		// AC table.
		var tables uint8
		switch {
		case s.ss != 0:
			tables = uint8(min(ci, 1))
		case s.ah == 0:
			tables = uint8(min(ci, 1)) << 4
		}
		e.buf[2+2*i] = tables
	}
	e.buf[1+2*n] = uint8(s.ss)
	e.buf[2+2*n] = uint8(s.se)
	e.buf[3+2*n] = uint8(s.ah<<4 | s.al)
	e.write(e.buf[:4+2*n])
}

// writeProgressive writes the frame header and the scans of a progressive
// file.
func (e *encoder) writeProgressive(src pipeline.Source, first image.Image, size image.Point, nComponent int) error {
	env := pipeline.EnvOf(src)
	st := &progStore{}
	if nComponent == 1 {
		bw, bh := (size.X+7)/8, (size.Y+7)/8
		st.mxx, st.myy = bw, bh
		st.comp = []progComp{{stride: bw, v: 1, bw: bw, bh: bh}}
	} else {
		st.mxx, st.myy = (size.X+8*e.hY-1)/(8*e.hY), (size.Y+8*e.vY-1)/(8*e.vY)
		st.comp = []progComp{
			{stride: e.hY * st.mxx, v: e.vY, bw: (size.X + 7) / 8, bh: (size.Y + 7) / 8},
			{stride: st.mxx, v: 1, bw: st.mxx, bh: st.myy},
			{stride: st.mxx, v: 1, bw: st.mxx, bh: st.myy},
		}
	}
	rowBytes := 0
	for i := range st.comp {
		st.comp[i].off = rowBytes
		rowBytes += st.comp[i].stride * st.comp[i].v * blockBytes
	}
	// The row, and the index of the last nonzero coefficient of each block.
	need := int64(rowBytes) + int64(rowBytes/blockBytes)
	if err := env.Budget.Reserve(need, "jpeg coefficient row"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	st.row = make([]byte, rowBytes)
	for i := range st.comp {
		c := &st.comp[i]
		c.buf = st.row[c.off : c.off+c.stride*c.v*blockBytes]
		c.last = make([]uint8, c.stride*c.v)
	}
	sc, err := pipeline.NewScratch(env, int64(rowBytes)*int64(st.myy))
	if err != nil {
		return err
	}
	defer sc.Close()
	st.sc = sc

	search := newProgSearch(e, nComponent)
	counted := search.counted()

	e.writeSOF(sof2Marker, size, nComponent)

	// The pixel pass: quantize, store, count.
	var (
		m, mx, nY int // MCU row, MCU in it, Y block in the MCU
		passErr   error
	)
	rowDone := func() {
		if _, err := sc.WriteAt(st.row, st.rowOff(m)); err != nil {
			passErr = err
			return
		}
		for i := range st.comp {
			st.comp[i].findLast()
		}
		// The scans count on their own: on as many cores as are free.
		env.Parallel(len(counted), func(i int) { counted[i].mcuRow(st, m) })
		m, mx = m+1, 0
	}
	err = e.quantBlocks(src, first, func(zz *qblock, comp int) {
		if passErr != nil {
			return
		}
		if m >= st.myy {
			passErr = errors.New("goimgtools: jpeg source has more rows than its bounds")
			return
		}
		c := &st.comp[comp]
		bx, r := mx, 0
		if comp == 0 && nComponent == 3 {
			bx, r = e.hY*mx+nY%e.hY, nY/e.hY
			nY++
		}
		w := c.buf[(r*c.stride+bx)*blockBytes:]
		w = w[:blockBytes:blockBytes]
		for zig, v := range zz {
			w[2*zig], w[2*zig+1] = byte(v), byte(v>>8)
		}
		if comp == nComponent-1 {
			mx, nY = mx+1, 0
			if mx == st.mxx {
				rowDone()
			}
		}
	})
	if err == nil {
		err = passErr
	}
	if err == nil && m != st.myy {
		err = io.ErrUnexpectedEOF
	}
	if err != nil {
		return err
	}
	for _, s := range counted {
		s.finish()
	}

	// The band splits, once the bits left to refinement are chosen: one
	// more pass over the coefficients.
	splits := search.splitScans()
	all := []int{0, 1, 2}[:nComponent]
	for m := 0; m < st.myy; m++ {
		if err := st.loadRow(m, all); err != nil {
			return err
		}
		env.Parallel(len(splits), func(i int) { splits[i].mcuRow(st, m) })
	}
	for _, s := range splits {
		s.finish()
	}

	// The scans.
	for _, s := range search.script() {
		s.gather = false
		s.writeHeader()
		for m := 0; m < st.myy; m++ {
			if e.err != nil {
				return e.err
			}
			if err := st.loadRow(m, s.comps); err != nil {
				return err
			}
			s.mcuRow(st, m)
		}
		s.finish()
	}
	return e.err
}
