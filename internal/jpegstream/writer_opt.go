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
	"bufio"
	"errors"
	"image"
	"io"
	"math"
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
	orig := *freq
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
	// The symbols in order of code length. The lengths in bits may differ
	// from codesize after the limiting, the order does not. libjpeg orders
	// symbols of one codesize by value; ordering them by frequency instead
	// costs nothing and matters when the limiting gave symbols of one
	// codesize different lengths: the more frequent get the shorter.
	for sym, cs := range codesize[:256] {
		if cs != 0 {
			s.value = append(s.value, byte(sym))
		}
	}
	sort.SliceStable(s.value, func(i, j int) bool {
		a, b := s.value[i], s.value[j]
		if codesize[a] != codesize[b] {
			return codesize[a] < codesize[b]
		}
		return orig[a] > orig[b]
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

// writeDHTSpecs writes one Define Huffman Table marker holding specs;
// ids[i] is table i's class (high 4 bits: 0 DC, 1 AC) and number.
func (e *encoder) writeDHTSpecs(ids []byte, specs []huffmanSpec) {
	markerlen := 2
	for i := range specs {
		markerlen += 1 + 16 + len(specs[i].value)
	}
	e.writeMarkerHeader(dhtMarker, markerlen)
	for i := range specs {
		e.writeByte(ids[i])
		e.write(specs[i].count[:])
		e.write(specs[i].value)
	}
}

// stdDecoder decodes symbols coded with one of the standard tables.
type stdDecoder struct {
	// fast maps the next 8 bits to size<<8 | symbol for codes of at most 8
	// bits, and to 0 for longer ones.
	fast [256]uint16
	// For codes of n bits: the first code, how many there are, and where
	// their symbols start in value.
	first, count, index [17]int32
	value               []byte
}

func (d *stdDecoder) init(s *huffmanSpec) {
	d.value = s.value
	code, k := int32(0), int32(0)
	for i, n := range s.count {
		size := int32(i + 1)
		d.first[size], d.count[size], d.index[size] = code, int32(n), k
		if size <= 8 {
			for j := int32(0); j < int32(n); j++ {
				lo := (code + j) << (8 - size)
				for x := lo; x < lo+1<<(8-size); x++ {
					d.fast[x] = uint16(size)<<8 | uint16(s.value[k+j])
				}
			}
		}
		code = (code + int32(n)) << 1
		k += int32(n)
	}
}

// stagedBits reads the entropy-coded data staged in a Spill.
type stagedBits struct {
	sp       *pipeline.Spill
	off      int64
	buf      []byte
	r, w     int
	acc      uint64 // the low n bits are unread
	n        uint
	overread int // bytes made up after the end of the data
	err      error
}

// fill makes at least 32 bits available. Past the end of the data it
// supplies zero bytes (decoding looks 16 bits ahead); needing more of them
// than a look-ahead can explain is an error.
func (b *stagedBits) fill() {
	for b.n <= 32 {
		c, ok := b.readByte()
		if !ok {
			b.overread++
			if b.overread > 8 && b.err == nil {
				b.err = io.ErrUnexpectedEOF
			}
		} else if c == 0xff {
			// The plain encoder stuffs a zero after every 0xff.
			b.readByte()
		}
		b.acc = b.acc<<8 | uint64(c)
		b.n += 8
	}
}

func (b *stagedBits) readByte() (byte, bool) {
	if b.r == b.w {
		if b.err != nil || b.off >= b.sp.Size() {
			return 0, false
		}
		k := int(min(int64(len(b.buf)), b.sp.Size()-b.off))
		if n, err := b.sp.ReadAt(b.buf[:k], b.off); n < k {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			b.err = err
			return 0, false
		}
		b.off += int64(k)
		b.r, b.w = 0, k
	}
	c := b.buf[b.r]
	b.r++
	return c, true
}

// bits returns the next n bits, n <= 16.
func (b *stagedBits) bits(n uint) uint32 {
	if b.n < n {
		b.fill()
	}
	b.n -= n
	return uint32(b.acc>>b.n) & (1<<n - 1)
}

// symbol decodes one symbol.
func (b *stagedBits) symbol(d *stdDecoder) uint8 {
	if b.n < 16 {
		b.fill()
	}
	if f := d.fast[uint8(b.acc>>(b.n-8))]; f != 0 {
		b.n -= uint(f >> 8)
		return uint8(f)
	}
	for size := uint(9); size <= 16; size++ {
		code := int32(b.acc>>(b.n-size)) & (1<<size - 1)
		if i := code - d.first[size]; i >= 0 && i < d.count[size] {
			b.n -= size
			return d.value[d.index[size]+i]
		}
	}
	if b.err == nil {
		b.err = errors.New("goimgtools: staged jpeg data is damaged")
	}
	return 0
}

const stagedReadBuf = 64 << 10

// writeOptimized writes the Huffman tables and the scan of a baseline file
// with tables made for this image.
func (e *encoder) writeOptimized(src pipeline.Source, first image.Image, size image.Point, nComponent int) error {
	env := pipeline.EnvOf(src)
	sp := pipeline.NewSpill(env)
	defer sp.Close()
	// Only a hint for whether to start in memory: a quarter of a byte per
	// pixel is a common size for a photo's image data.
	sp.Expect(int64(size.X) * int64(size.Y) / 4)

	// Pass 1: the plain scan, into the spill, counting symbols.
	var freq [nHuffIndex][257]int64
	out := e.w
	e.drain()
	e.w, e.freq = bufio.NewWriterSize(sp, 4<<10), &freq
	var prevDC [3]int32
	err := e.quantBlocks(src, first, func(zz *qblock, comp int) {
		prevDC[comp] = e.writeQuantized(zz, quantIndex((comp+1)/2), prevDC[comp])
	})
	e.padBits()
	e.flush()
	e.w, e.freq, e.bits, e.nBits = out, nil, 0, 0
	if err != nil {
		return err
	}
	if e.err != nil {
		return e.err
	}

	// The tables.
	nTables := 2 * min(nComponent, 2)
	var (
		specs [nHuffIndex]huffmanSpec
		luts  [nHuffIndex]codeLUT
		decs  [nHuffIndex]stdDecoder
	)
	for i := 0; i < nTables; i++ {
		specs[i] = optimalSpec(&freq[i])
		luts[i].init(&specs[i])
		decs[i].init(&theHuffmanSpec[i])
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
	rd := &stagedBits{sp: sp, buf: make([]byte, stagedReadBuf)}
	recode := func(q int) {
		dc, ac := huffIndex(2*q), huffIndex(2*q+1)
		s := rd.symbol(&decs[dc])
		e.emitCode(&luts[dc], s)
		if s != 0 {
			e.emit(rd.bits(uint(s)), uint32(s))
		}
		for k := 1; k < blockSize; {
			rs := rd.symbol(&decs[ac])
			e.emitCode(&luts[ac], rs)
			if rs == 0x00 {
				break
			}
			k += int(rs>>4) + 1
			if n := rs & 0x0f; n != 0 {
				e.emit(rd.bits(uint(n)), uint32(n))
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
