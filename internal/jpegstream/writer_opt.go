package jpegstream

// Options.Optimize: a baseline file whose Huffman tables are built from the
// image's own symbol statistics.
//
// The tables come before the entropy-coded data in the file, so the image
// has to be seen once before the first coded byte can be written. Pass 1 is
// the plain encoder writing its entropy-coded data (coded with the standard
// tables) into a pipeline.Spill while counting how often each symbol occurs;
// pass 2 reads that data back symbol by symbol and writes it again with the
// optimal tables. What is staged is therefore about as large as the plain
// JPEG's image data (a few megabytes for a 24 megapixel photo, against 72MB
// for its coefficients as int16) and stays in memory while the budget's
// staging share has room for it.
//
// optimalSpec is a port of jpeg_gen_optimal_table from the Independent JPEG
// Group's library as kept in libjpeg-turbo (jchuff.c; copyright 1991-1997
// Thomas G. Lane, libjpeg-turbo modifications copyright D. R. Commander;
// IJG and BSD-style licences), which follows section K.2 of the JPEG
// specification.

import (
	"errors"
	"image"
	"io"
	"math"
	mbits "math/bits"
	"sort"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// optimalSpec builds the Huffman table that codes symbols occurring
// freq[symbol] times in the fewest bits, under JPEG's rules: no code longer
// than 16 bits and no code of all 1 bits. freq[256] is a slot for the
// pseudo-symbol that reserves the all-ones code; freq is destroyed.
func optimalSpec(freq *[257]int64) huffmanSpec {
	const none = -1
	var (
		// bits[n] is the number of symbols with a code of n bits. A code can
		// be up to 256 bits long before the limit below is applied.
		bits     [258]int
		codesize [257]int
		others   [257]int
	)
	used := false
	for _, f := range freq[:256] {
		used = used || f != 0
	}
	if !used {
		// A table nothing uses still has to be a valid one.
		freq[0] = 1
	}
	for i := range others {
		others[i] = none
	}
	// The pseudo-symbol 256 has a nonzero count, so it gets a code; being
	// the rarest and the last, it gets the longest code, which is the one
	// of all 1 bits.
	freq[256] = 1
	for {
		// Find the two least frequent symbols; among equals the larger one.
		c1, c2 := none, none
		v := int64(math.MaxInt64)
		for i, f := range freq {
			if f != 0 && f <= v {
				v, c1 = f, i
			}
		}
		v = math.MaxInt64
		for i, f := range freq {
			if f != 0 && f <= v && i != c1 {
				v, c2 = f, i
			}
		}
		if c2 < 0 {
			break
		}
		// Merge the two subtrees.
		freq[c1] += freq[c2]
		freq[c2] = 0
		codesize[c1]++
		for others[c1] >= 0 {
			c1 = others[c1]
			codesize[c1]++
		}
		others[c1] = c2
		codesize[c2]++
		for others[c2] >= 0 {
			c2 = others[c2]
			codesize[c2]++
		}
	}
	for _, n := range codesize {
		if n != 0 {
			bits[n]++
		}
	}
	// Limit the code length to 16 bits (figure K.3 of the spec): take a
	// pair of the longest codes, give one of them the prefix (one bit
	// shorter), and make room for the other by splitting the longest
	// shorter code into two.
	for i := len(bits) - 1; i > 16; i-- {
		for bits[i] > 0 {
			j := i - 2
			for bits[j] == 0 {
				j--
			}
			bits[i] -= 2
			bits[i-1]++
			bits[j+1] += 2
			bits[j]--
		}
	}
	// Take the pseudo-symbol's code away again: it is one of the longest.
	i := 16
	for bits[i] == 0 {
		i--
	}
	bits[i]--

	var s huffmanSpec
	for i := range s.count {
		s.count[i] = byte(bits[i+1])
	}
	// The symbols in order of code length, those of one length by value,
	// as libjpeg orders them. (The lengths in bits may differ from codesize
	// after the limiting, the order does not.)
	for sym, cs := range codesize[:256] {
		if cs != 0 {
			s.value = append(s.value, byte(sym))
		}
	}
	sort.SliceStable(s.value, func(i, j int) bool {
		return codesize[s.value[i]] < codesize[s.value[j]]
	})
	return s
}

// codeLUT maps a symbol to its code: the size in bits in the top 8 bits and
// the code in the low 24, as in huffmanLUT; 0 for a symbol without a code.
type codeLUT [256]uint32

func (h *codeLUT) init(s *huffmanSpec) {
	*h = codeLUT{}
	code, k := uint32(0), 0
	for i, n := range s.count {
		for j := 0; j < int(n); j++ {
			h[s.value[k]] = uint32(i+1)<<24 | code
			code++
			k++
		}
		code <<= 1
	}
}

// emitCode emits the code of sym. A symbol without a code means the
// statistics and the data disagree, which is a bug, not bad input.
func (e *encoder) emitCode(h *codeLUT, sym uint8) {
	x := h[sym]
	if x == 0 {
		if e.err == nil {
			e.err = errors.New("goimgtools: jpeg symbol without a Huffman code")
		}
		return
	}
	e.emit(x&(1<<24-1), x>>24)
}

// writeDHTSpecs writes a Define Huffman Table marker for each of specs, as
// libjpeg does; ids[i] is table i's class (high 4 bits: 0 DC, 1 AC) and
// number.
func (e *encoder) writeDHTSpecs(ids []byte, specs []huffmanSpec) {
	for i := range specs {
		e.writeMarkerHeader(dhtMarker, 2+1+16+len(specs[i].value))
		e.writeByte(ids[i])
		e.write(specs[i].count[:])
		e.write(specs[i].value)
	}
}

const stagedReadBuf = 64 << 10

// The staged symbols of writeOptimized go to the spill in pieces of about
// stageFlush bytes; a block's take at most stageBlockMax.
const (
	stageFlush    = 32 << 10
	stageBlockMax = 3 * blockSize
)

// stageBlock appends the symbols of a quantized block, its DC as the
// difference to prevDC, to b, and counts them in dc and ac: each symbol a
// byte, followed by the low bits of its value (as writeQuantized writes
// them) in one byte, or two (little-endian) for more than 8 bits. It
// returns b and the block's DC.
func stageBlock(b []byte, zz *qblock, prevDC int32, dc, ac *[257]int64) ([]byte, int32) {
	value := func(b []byte, v int32, nb uint32) []byte {
		s := v >> 31
		x := uint32(v+s) & (1<<nb - 1)
		switch {
		case nb == 0:
			return b
		case nb <= 8:
			return append(b, byte(x))
		}
		return append(b, byte(x), byte(x>>8))
	}
	d := int32(zz[0])
	v := d - prevDC
	s := v >> 31
	nb := uint32(mbits.Len32(uint32((v ^ s) - s)))
	dc[nb]++
	b = value(append(b, byte(nb)), v, nb)
	nz := nonzeros(zz) &^ 1
	last := 0
	for ; nz != 0; nz &= nz - 1 {
		zig := mbits.TrailingZeros64(nz)
		run := zig - last - 1
		for ; run > 15; run -= 16 {
			ac[0xf0]++
			b = append(b, 0xf0)
		}
		v := int32(zz[zig])
		s := v >> 31
		nb := uint32(mbits.Len32(uint32((v ^ s) - s)))
		sym := byte(run<<4) | byte(nb)
		ac[sym]++
		b = value(append(b, sym), v, nb)
		last = zig
	}
	if last != blockSize-1 {
		ac[0x00]++
		b = append(b, 0x00)
	}
	return b, d
}

// stageReader reads the symbols stageBlock staged in a Spill.
type stageReader struct {
	sp   *pipeline.Spill
	off  int64
	buf  []byte
	r, w int
	err  error
}

// need makes at least 3 bytes available (a symbol and its value), or what
// is left of the data; past its end it supplies zero bytes, and sets err.
func (b *stageReader) need() {
	if b.w-b.r >= 3 {
		return
	}
	n := copy(b.buf, b.buf[b.r:b.w])
	b.r, b.w = 0, n
	if k := int(min(int64(len(b.buf)-n), b.sp.Size()-b.off)); k > 0 && b.err == nil {
		if m, err := b.sp.ReadAt(b.buf[n:n+k], b.off); m < k {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			b.err = err
		} else {
			b.off += int64(k)
			b.w += k
		}
	}
	if b.w-b.r < 3 {
		if b.w == b.r && b.err == nil {
			b.err = io.ErrUnexpectedEOF
		}
		clear(b.buf[b.w : b.r+3])
		b.w = b.r + 3
	}
}

// value reads the bits of a value of nb bits.
func (b *stageReader) value(nb uint32) uint32 {
	x := uint32(b.buf[b.r])
	b.r++
	if nb > 8 {
		x |= uint32(b.buf[b.r]) << 8
		b.r++
	}
	return x
}

// writeOptimized writes the Huffman tables and the scan of a baseline file
// with tables made for this image.
func (e *encoder) writeOptimized(src pipeline.Source, first image.Image, size image.Point, nComponent int) error {
	env := pipeline.EnvOf(src)
	sp := pipeline.NewSpill(env)
	defer sp.Close()
	// Only a hint for whether to start in memory: half a byte per pixel is
	// a common size of a photo's symbols staged.
	sp.Expect(int64(size.X) * int64(size.Y) / 2)

	// Pass 1: the symbols of the scan, into the spill, counted.
	var freq [nHuffIndex][257]int64
	var prevDC [3]int32
	staged := make([]byte, 0, stageFlush+stageBlockMax)
	var werr error
	err := e.quantBlocks(src, first, func(zz *qblock, comp int) {
		q := quantIndex((comp + 1) / 2)
		staged, prevDC[comp] = stageBlock(staged, zz, prevDC[comp], &freq[2*q], &freq[2*q+1])
		if len(staged) >= stageFlush && werr == nil {
			_, werr = sp.Write(staged)
			staged = staged[:0]
		}
	})
	if err == nil && werr == nil {
		_, werr = sp.Write(staged)
	}
	if err != nil {
		return err
	}
	if werr != nil {
		return werr
	}

	// The tables.
	nTables := 2 * min(nComponent, 2)
	var (
		specs [nHuffIndex]huffmanSpec
		luts  [nHuffIndex]codeLUT
	)
	for i := 0; i < nTables; i++ {
		specs[i] = optimalSpec(&freq[i])
		luts[i].init(&specs[i])
	}
	e.writeDHTSpecs([]byte("\x00\x10\x01\x11")[:nTables], specs[:nTables])
	if nComponent == 1 {
		e.write(sosHeaderY)
	} else {
		e.write(sosHeaderYCbCr)
	}

	// Pass 2: the same symbols with the new codes.
	if err := env.Budget.Reserve(stagedReadBuf, "jpeg optimize read buffer"); err != nil {
		return err
	}
	defer env.Budget.Release(stagedReadBuf)
	rd := &stageReader{sp: sp, buf: make([]byte, stagedReadBuf)}
	recode := func(q int) {
		dc, ac := &luts[2*q], &luts[2*q+1]
		rd.need()
		s := rd.buf[rd.r]
		rd.r++
		e.emitCode(dc, s)
		if s != 0 {
			e.emit(rd.value(uint32(s)), uint32(s))
		}
		for k := 1; k < blockSize; {
			rd.need()
			rs := rd.buf[rd.r]
			rd.r++
			e.emitCode(ac, rs)
			if rs == 0x00 {
				break
			}
			k += int(rs>>4) + 1
			if n := uint32(rs & 0x0f); n != 0 {
				e.emit(rd.value(n), n)
			}
		}
	}
	if nComponent == 1 {
		for n := int64((size.X+7)/8) * int64((size.Y+7)/8); n > 0 && rd.err == nil && e.err == nil; n-- {
			recode(0)
		}
	} else {
		mcus := int64((size.X+8*e.hY-1)/(8*e.hY)) * int64((size.Y+8*e.vY-1)/(8*e.vY))
		for n := mcus; n > 0 && rd.err == nil && e.err == nil; n-- {
			for k := 0; k < e.hY*e.vY; k++ {
				recode(0)
			}
			recode(1)
			recode(1)
		}
	}
	if rd.err != nil {
		return rd.err
	}
	// Pad the last byte with 1's.
	e.padBits()
	return e.err
}
