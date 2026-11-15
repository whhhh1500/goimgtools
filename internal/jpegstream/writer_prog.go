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
// The scan script is jpeg_simple_progression's and the coding of a scan
// (EOB runs, correction bits of refinement scans) is a port of jcphuff.c,
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
		_, err := st.sc.ReadAt(st.row, st.rowOff(m))
		return err
	}
	for _, ci := range comps {
		c := &st.comp[ci]
		if _, err := st.sc.ReadAt(c.buf, st.rowOff(m)+int64(c.off)); err != nil {
			return err
		}
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
	gather bool // count symbols instead of writing
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

// progScript is libjpeg's jpeg_simple_progression.
func progScript(nComponent int) []*progScan {
	all := []int{0}
	if nComponent == 3 {
		all = []int{0, 1, 2}
	}
	y, cb, cr := []int{0}, []int{1}, []int{2}
	if nComponent == 3 {
		return []*progScan{
			{comps: all, ss: 0, se: 0, ah: 0, al: 1}, // DC, all but the last bit
			{comps: y, ss: 1, se: 5, ah: 0, al: 2},   // some luma soon
			{comps: cr, ss: 1, se: 63, ah: 0, al: 1}, // chroma is small: one scan each
			{comps: cb, ss: 1, se: 63, ah: 0, al: 1}, //
			{comps: y, ss: 6, se: 63, ah: 0, al: 2},  // the rest of luma
			{comps: y, ss: 1, se: 63, ah: 2, al: 1},  // next bit of luma
			{comps: all, ss: 0, se: 0, ah: 1, al: 0}, // last bit of DC
			{comps: cr, ss: 1, se: 63, ah: 1, al: 0}, // last bit of chroma
			{comps: cb, ss: 1, se: 63, ah: 1, al: 0}, //
			{comps: y, ss: 1, se: 63, ah: 1, al: 0},  // last bit of luma: the largest scan
		}
	}
	return []*progScan{
		{comps: all, ss: 0, se: 0, ah: 0, al: 1},
		{comps: y, ss: 1, se: 5, ah: 0, al: 2},
		{comps: y, ss: 6, se: 63, ah: 0, al: 2},
		{comps: y, ss: 1, se: 63, ah: 2, al: 1},
		{comps: all, ss: 0, se: 0, ah: 1, al: 0},
		{comps: y, ss: 1, se: 63, ah: 1, al: 0},
	}
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
	if s.gather || n == 0 {
		return
	}
	s.e.emit(v&(1<<n-1), n)
}

func (s *progScan) putCorr(p []byte) {
	if s.gather {
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

// acFirst codes coefficients ss to se without their low al bits.
func (s *progScan) acFirst(t int, w []byte) {
	r := 0
	for k := s.ss; k <= s.se; k++ {
		v := coefAt(w, k)
		if v == 0 {
			r++
			continue
		}
		// The point transform of AC divides, rounding towards zero.
		var a, b int32
		if v < 0 {
			a = -v >> s.al
			b = ^a
		} else {
			a = v >> s.al
			b = a
		}
		if a == 0 {
			r++
			continue
		}
		if s.eobRun > 0 {
			s.emitEOBRun(t)
		}
		for r > 15 {
			s.symbol(t, 0xf0)
			r -= 16
		}
		n := uint32(mbits.Len32(uint32(a)))
		s.symbol(t, uint8(r<<4)|uint8(n))
		s.putBits(uint32(b), n)
		r = 0
	}
	if r > 0 {
		s.eobRun++
		if s.eobRun == 0x7fff {
			s.emitEOBRun(t)
		}
	}
}

// acRefine sends bit al of coefficients ss to se: for a coefficient that
// was already nonzero a correction bit, for one that becomes nonzero now a
// symbol with the run of still-zero coefficients before it.
func (s *progScan) acRefine(t int, w []byte) {
	var abs [blockSize]int32
	// eob is the last coefficient that becomes nonzero in this scan.
	eob := 0
	for k := s.ss; k <= s.se; k++ {
		a := coefAt(w, k)
		if a < 0 {
			a = -a
		}
		a >>= s.al
		abs[k] = a
		if a == 1 {
			eob = k
		}
	}
	// This block's correction bits are corr[start:start+nbr]: after those of
	// the pending EOB run, or at the front once that run has been written.
	r, start, nbr := 0, s.be, 0
	for k := s.ss; k <= s.se; k++ {
		a := abs[k]
		if a == 0 {
			r++
			continue
		}
		// Write the zero runs that are needed; not after the last newly
		// nonzero coefficient, where an EOB does the job.
		for r > 15 && k <= eob {
			s.emitEOBRun(t)
			s.symbol(t, 0xf0)
			r -= 16
			s.putCorr(s.corr[start : start+nbr])
			start, nbr = 0, 0
		}
		if a > 1 {
			// Already nonzero: only its correction bit.
			s.corr[start+nbr] = byte(a & 1)
			nbr++
			continue
		}
		s.emitEOBRun(t)
		s.symbol(t, uint8(r<<4)|1)
		if coefAt(w, k) < 0 {
			s.putBits(0, 1)
		} else {
			s.putBits(1, 1)
		}
		s.putCorr(s.corr[start : start+nbr])
		start, nbr = 0, 0
		r = 0
	}
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
		for mx := 0; mx < st.mxx; mx++ {
			dc(0, y.buf[(2*mx)*blockBytes:])
			dc(0, y.buf[(2*mx+1)*blockBytes:])
			dc(0, y.buf[(y.stride+2*mx)*blockBytes:])
			dc(0, y.buf[(y.stride+2*mx+1)*blockBytes:])
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
			if s.ah == 0 {
				s.acFirst(t, w)
			} else {
				s.acRefine(t, w)
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
		s.e.emit(0x7f, 7)
		s.e.bits, s.e.nBits = 0, 0
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
		st.mxx, st.myy = (size.X+15)/16, (size.Y+15)/16
		st.comp = []progComp{
			{stride: 2 * st.mxx, v: 2, bw: (size.X + 7) / 8, bh: (size.Y + 7) / 8},
			{stride: st.mxx, v: 1, bw: st.mxx, bh: st.myy},
			{stride: st.mxx, v: 1, bw: st.mxx, bh: st.myy},
		}
	}
	rowBytes := 0
	for i := range st.comp {
		st.comp[i].off = rowBytes
		rowBytes += st.comp[i].stride * st.comp[i].v * blockBytes
	}
	if err := env.Budget.Reserve(int64(rowBytes), "jpeg coefficient row"); err != nil {
		return err
	}
	defer env.Budget.Release(int64(rowBytes))
	st.row = make([]byte, rowBytes)
	for i := range st.comp {
		c := &st.comp[i]
		c.buf = st.row[c.off : c.off+c.stride*c.v*blockBytes]
	}
	sc, err := pipeline.NewScratch(env, int64(rowBytes)*int64(st.myy))
	if err != nil {
		return err
	}
	defer sc.Close()
	st.sc = sc

	scans := progScript(nComponent)
	for _, s := range scans {
		s.e, s.gather = e, true
	}

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
		for _, s := range scans {
			s.mcuRow(st, m)
		}
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
			bx, r = 2*mx+nY&1, nY>>1
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
	for _, s := range scans {
		s.finish()
	}

	// The scans.
	for _, s := range scans {
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
