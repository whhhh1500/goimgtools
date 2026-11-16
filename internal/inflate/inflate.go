// Package inflate decompresses zlib streams (RFC 1950 and 1951) about three
// times as fast as compress/zlib, with the method of libdeflate's
// decompressor (by Eric Biggers, MIT licence; rewritten in Go): a 64-bit
// bit buffer refilled eight bytes at a time without a branch, decode tables
// of 11 bits for literals and lengths (8 for distances) whose entries hold
// everything a symbol needs, its extra bits included, and matches copied
// eight bytes at a time. Output goes into a buffer holding the last 32KB
// as the window and up to 64KB more.
//
// It accepts and rejects the same streams as compress/zlib: the same codes
// are valid (complete ones, and a single code of length 1), the same
// symbols and distances fail, and input is read from the underlying reader
// as compress/zlib reads it through a bufio.Reader, 4096 bytes at a time,
// and only once a byte of it is needed, so that a caller checking what is
// left after the stream (as image/png does) sees the same.
package inflate

import (
	"compress/flate"
	"compress/zlib"
	"encoding/binary"
	"hash"
	"hash/adler32"
	"io"
	mbits "math/bits"
	"sync"
)

const (
	windowSize = 1 << 15
	outFull    = windowSize + 1<<16 // no symbol is begun past this
	// A match begun just before outFull ends at most 258 bytes later, and
	// is copied in words of 8 bytes, the last of which may go 7 past it.
	outSize = outFull + 258 + 8

	fillSize = 4096 // what bufio.Reader reads at a time
	keep     = 8    // bytes of the last fill kept ahead of the new one
	inSize   = keep + fillSize

	litlenBits  = 11
	distBits    = 8
	precodeBits = 7
	// The largest tables these root sizes need (zlib's "enough": 288
	// symbols, codes up to 15 bits; 32 symbols; 19 symbols of up to 7).
	litlenEnough  = 2342
	distEnough    = 402
	precodeEnough = 1 << precodeBits
)

// Size is about the memory a Reader holds.
const Size = outSize + inSize + 4*(litlenEnough+distEnough+precodeEnough) + 1024

// A table entry is a uint32:
//
//	bits 0-7:   the bits to consume: the code's (those beyond the root
//	            table's for an entry of a subtable) and its extra bits;
//	            for a subtable pointer the root table's bits
//	bits 8-11:  the code's bits (of a subtable pointer: the subtable's)
//	bit 12:     end of block
//	bit 13:     subtable pointer
//	bit 14:     not a literal or a length or distance: end of block,
//	            subtable pointer, or a symbol that fails
//	bit 15:     literal
//	bits 16-31: the literal, the base length or distance, or where the
//	            subtable starts
const (
	fEOB      = 1 << 12
	fSubtable = 1 << 13
	fExcept   = 1 << 14
	fLiteral  = 1 << 15
)

var (
	lengthBase = [29]uint16{3, 4, 5, 6, 7, 8, 9, 10, 11, 13, 15, 17, 19, 23, 27, 31,
		35, 43, 51, 59, 67, 83, 99, 115, 131, 163, 195, 227, 258}
	lengthExtra = [29]uint8{0, 0, 0, 0, 0, 0, 0, 0, 1, 1, 1, 1, 2, 2, 2, 2,
		3, 3, 3, 3, 4, 4, 4, 4, 5, 5, 5, 5, 0}
	distBase = [30]uint16{1, 2, 3, 4, 5, 7, 9, 13, 17, 25, 33, 49, 65, 97, 129, 193,
		257, 385, 513, 769, 1025, 1537, 2049, 3073, 4097, 6145, 8193, 12289, 16385, 24577}
	distExtra = [30]uint8{0, 0, 0, 0, 1, 1, 2, 2, 3, 3, 4, 4, 5, 5, 6, 6,
		7, 7, 8, 8, 9, 9, 10, 10, 11, 11, 12, 12, 13, 13}
	// The order in which the code lengths of the code-length code come.
	precodeOrder = [19]uint8{16, 17, 18, 0, 8, 7, 9, 6, 10, 5, 11, 4, 12, 3, 13, 2, 14, 1, 15}

	// The entries of each symbol, before the lengths of its code are added.
	litlenSym  [288]uint32
	distSym    [32]uint32
	precodeSym [19]uint32

	fixedOnce   sync.Once
	fixedLitlen [litlenEnough]uint32
	fixedDist   [distEnough]uint32
)

func init() {
	for s := range litlenSym {
		switch {
		case s < 256:
			litlenSym[s] = fLiteral | uint32(s)<<16
		case s == 256:
			litlenSym[s] = fExcept | fEOB
		case s < 286:
			litlenSym[s] = uint32(lengthBase[s-257])<<16 | uint32(lengthExtra[s-257])
		default: // 286 and 287 have codes in the fixed code only, and fail
			litlenSym[s] = fExcept
		}
	}
	for s := range distSym {
		if s < 30 {
			distSym[s] = uint32(distBase[s])<<16 | uint32(distExtra[s])
		} else { // 30 and 31, as 286 and 287
			distSym[s] = fExcept
		}
	}
	for s := range precodeSym {
		precodeSym[s] = uint32(s) << 16
	}
}

func buildFixed() {
	var lens [288 + 32]uint8
	var sorted [288]uint16
	for s := 0; s < 288; s++ {
		switch {
		case s < 144:
			lens[s] = 8
		case s < 256:
			lens[s] = 9
		case s < 280:
			lens[s] = 7
		default:
			lens[s] = 8
		}
	}
	for s := 288; s < 288+32; s++ {
		lens[s] = 5
	}
	build(fixedLitlen[:], lens[:288], litlenBits, litlenSym[:], sorted[:])
	build(fixedDist[:], lens[288:], distBits, distSym[:], sorted[:])
}

// build makes in t the decode table of the code whose lengths are lens
// (symbol s has a code of lens[s] bits, none for 0), with root bits of
// index: an entry is sym[s] with the lengths of its code added. It reports
// false for lengths that compress/flate does not accept: over-subscribed,
// or incomplete other than a single code of length 1. An empty code is
// accepted, its entries all failing; so are those of the missing code of
// an incomplete one.
func build(t []uint32, lens []uint8, root int, sym []uint32, sorted []uint16) bool {
	var count [16]int
	for _, l := range lens {
		count[l]++
	}
	count[0] = 0
	maxLen := 15
	for maxLen > 0 && count[maxLen] == 0 {
		maxLen--
	}
	left := 1
	for l := 1; l <= 15; l++ {
		if left = left<<1 - count[l]; left < 0 {
			return false
		}
	}
	if maxLen == 0 || left > 0 {
		if maxLen > 1 || maxLen == 1 && count[1] != 1 {
			return false
		}
		for i := range t[:1<<root] {
			t[i] = fExcept
		}
		if maxLen == 0 {
			return true
		}
	}
	var offs [17]int
	for l := 1; l < 16; l++ {
		offs[l+1] = offs[l] + count[l]
	}
	for s, l := range lens {
		if l != 0 {
			sorted[offs[l]] = uint16(s)
			offs[l]++
		}
	}
	// Codes in canonical order: by length, then by symbol; code is the
	// next one, its first bit the most significant.
	code, next, prefix, k := 0, 1<<root, -1, 0
	var subStart, subBits int
	for l := 1; l <= maxLen; l++ {
		for ; count[l] > 0; count[l]-- {
			s := sorted[k]
			k++
			rev := int(mbits.Reverse16(uint16(code)) >> (16 - l))
			code++
			if l <= root {
				e := sym[s] + uint32(l)<<8 + uint32(l)
				for i := rev; i < 1<<root; i += 1 << l {
					t[i] = e
				}
				continue
			}
			if p := rev & (1<<root - 1); p != prefix {
				// A subtable for the codes starting with p: they come
				// one after the other from here, and fill 1<<subBits
				// entries once subBits covers them all.
				prefix, subBits = p, l-root
				for room := 1 << subBits; root+subBits < maxLen; room <<= 1 {
					if room -= count[root+subBits]; room <= 0 {
						break
					}
					subBits++
				}
				if next+1<<subBits > len(t) {
					return false
				}
				t[p] = fExcept | fSubtable | uint32(next)<<16 | uint32(subBits)<<8 | uint32(root)
				subStart, next = next, next+1<<subBits
			}
			sl := l - root
			e := sym[s] + uint32(sl)<<8 + uint32(sl)
			for i := rev >> root; i < 1<<subBits; i += 1 << sl {
				t[subStart+i] = e
			}
		}
		code <<= 1
	}
	return true
}

// The parts of a stream.
const (
	stHeader  = iota // a block's header comes next
	stStored         // within a stored block
	stHuffman        // within a block of codes
	stTrailer        // the checksum comes next
	stDone
)

// Reader decompresses a zlib stream.
type Reader struct {
	r    io.Reader
	rerr error // an error of r, returned once what came with it is used
	err  error

	// in[ip:iend] is input not yet in the bit buffer; in[:ip] holds
	// at least keep bytes before it, for giving back the whole bytes of
	// the bit buffer when the stream needs to go on a byte boundary.
	in       [inSize]byte
	ip, iend int
	base     int64 // input read before in[keep]
	// bitbuf holds bitsleft bits of input, the first in the lowest bit;
	// the bits above are 0 or the input that follows.
	bitbuf   uint64
	bitsleft uint

	// out[rp:op] is output not yet read; out[:op] holds the last 32KB
	// before it (or all of it, when there is less). op is the count of
	// output so far modulo 32KB, as the place in compress/flate's window.
	out    [outSize]byte
	op, rp int

	state  int
	final  bool // the block is the last one
	stored int  // bytes of the stored block to come
	lt     *[litlenEnough]uint32
	dt     *[distEnough]uint32
	litlen [litlenEnough]uint32
	dist   [distEnough]uint32
	pre    [precodeEnough]uint32
	lens   [288 + 32]uint8
	sorted [288]uint16
	adler  hash.Hash32
	sum    uint32 // the checksum at the end of the stream
}

// NewReader returns a Reader decompressing the zlib stream read from r. It
// reads the stream's header, and returns an error for one compress/zlib
// rejects (a preset dictionary is one, unless its checksum is that of no
// dictionary at all).
func NewReader(r io.Reader) (*Reader, error) {
	fixedOnce.Do(buildFixed)
	z := &Reader{adler: adler32.New()}
	if err := z.Reset(r); err != nil {
		return nil, err
	}
	return z, nil
}

// Reset makes z decompress the zlib stream read from r, as a Reader from
// NewReader(r) would, keeping its buffers.
func (z *Reader) Reset(r io.Reader) error {
	z.r, z.rerr, z.err = r, nil, nil
	z.ip, z.iend, z.base = keep, keep, 0
	z.bitbuf, z.bitsleft = 0, 0
	z.op, z.rp = 0, 0
	z.state, z.final, z.stored = stHeader, false, 0
	z.adler.Reset()
	var h [6]byte
	if !z.bytes(h[:2]) {
		return z.err
	}
	if h[0]&0x0f != 8 || h[0]>>4 > 7 || binary.BigEndian.Uint16(h[:])%31 != 0 {
		z.err = zlib.ErrHeader
		return z.err
	}
	if h[1]&0x20 != 0 {
		if !z.bytes(h[2:6]) {
			return z.err
		}
		if binary.BigEndian.Uint32(h[2:]) != adler32.Checksum(nil) {
			z.err = zlib.ErrDictionary
			return z.err
		}
	}
	return nil
}

// Read reads decompressed data. With the last of it, it returns io.EOF, or
// zlib.ErrChecksum when the stream's checksum differs, or the error the
// stream fails with; as compress/zlib does, together with the data that
// precede it, unless their count is a multiple of 32KB, and then with no
// data at the next call. (compress/flate hands out its window of 32KB
// whenever it is full, before it goes on, so the data are handed out here
// up to such a point while the stream goes on.)
func (z *Reader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	lim := z.limit()
	for z.rp == lim {
		if err := z.end(); err != nil {
			return 0, err
		}
		z.decode()
		lim = z.limit()
	}
	n := copy(p, z.out[z.rp:lim])
	z.rp += n
	if z.rp == z.op && z.op%windowSize != 0 {
		return n, z.end()
	}
	return n, nil
}

// limit returns the end of the output that can be handed out.
func (z *Reader) limit() int {
	if z.err == nil && z.state != stDone {
		return z.op - z.op%windowSize
	}
	return z.op
}

// end returns what the stream ends with, nil while it goes on.
func (z *Reader) end() error {
	if z.state == stDone && z.err == nil {
		if z.err = io.EOF; z.sum != z.adler.Sum32() {
			z.err = zlib.ErrChecksum
		}
	}
	return z.err
}

// Close returns the error the stream ended with, if not io.EOF, as
// compress/zlib's reader does.
func (z *Reader) Close() error {
	if z.err != nil && z.err != io.EOF {
		return z.err
	}
	return nil
}

// decode decompresses until the output buffer is full or the stream ends.
// All of the output that can be handed out must have been read.
func (z *Reader) decode() {
	if z.op >= outFull {
		// Keep the window and what is past rp, moving by a multiple of
		// the window's size.
		shift := z.op - windowSize - z.op%windowSize
		copy(z.out[:], z.out[shift:z.op])
		z.op -= shift
		z.rp -= shift
	}
	op0 := z.op
	for z.err == nil && z.state != stDone && z.op < outFull {
		switch z.state {
		case stHeader:
			z.header()
		case stStored:
			z.copyStored()
		case stHuffman:
			z.huffman()
		case stTrailer:
			z.align()
			var h [4]byte
			if z.bytes(h[:]) {
				z.sum, z.state = binary.BigEndian.Uint32(h[:]), stDone
			}
		}
	}
	z.adler.Write(z.out[op0:z.op])
}

func (z *Reader) corrupt() {
	z.err = flate.CorruptInputError(z.base + int64(z.ip-keep))
}

// fill reads the next input, as bufio.Reader's fill does, once all of in
// is in the bit buffer.
func (z *Reader) fill() bool {
	if z.err != nil {
		return false
	}
	if z.rerr != nil {
		z.err = z.rerr
		return false
	}
	z.base += int64(z.iend - keep)
	copy(z.in[:keep], z.in[z.iend-keep:z.iend])
	z.ip, z.iend = keep, keep
	for i := 0; i < 100; i++ {
		n, err := z.r.Read(z.in[keep:])
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		if n > 0 {
			z.iend, z.rerr = keep+n, err
			return true
		}
		if err != nil {
			z.err = err
			return false
		}
	}
	z.err = io.ErrNoProgress
	return false
}

// need loads input into the bit buffer a byte at a time until it holds n
// bits.
func (z *Reader) need(n uint) bool {
	for z.bitsleft < n {
		if z.ip == z.iend && !z.fill() {
			return false
		}
		z.bitbuf |= uint64(z.in[z.ip]) << z.bitsleft
		z.ip++
		z.bitsleft += 8
	}
	return true
}

func (z *Reader) consume(n uint) {
	z.bitbuf >>= n
	z.bitsleft -= n
}

// align drops the bits of the byte begun, and gives back the whole bytes of
// the bit buffer to the input.
func (z *Reader) align() {
	z.ip -= int(z.bitsleft >> 3)
	z.bitbuf, z.bitsleft = 0, 0
}

// bytes reads len(p) bytes from the input on a byte boundary.
func (z *Reader) bytes(p []byte) bool {
	for i := range p {
		if z.ip == z.iend && !z.fill() {
			return false
		}
		p[i] = z.in[z.ip]
		z.ip++
	}
	return true
}

// sym returns the entry for the code next in the input in table t (root
// bits of index), reading input as compress/flate does: only once the code
// is known to need it. The bits of the code are then in the bit buffer, a
// subtable's root bits consumed.
func (z *Reader) sym(t []uint32, root uint) (uint32, bool) {
	for {
		e := t[z.bitbuf&(1<<root-1)]
		n := uint(e >> 8 & 15)
		if e&fSubtable != 0 {
			s := t[e>>16+uint32(z.bitbuf>>root)&(1<<n-1)]
			if root+uint(s>>8&15) <= z.bitsleft {
				z.consume(root)
				return s, true
			}
		} else if n <= z.bitsleft {
			return e, true
		}
		if !z.need(z.bitsleft + 1) {
			return 0, false
		}
	}
}

// header reads the header of a block, and the code of a dynamic one.
func (z *Reader) header() {
	if !z.need(3) {
		return
	}
	z.final = z.bitbuf&1 != 0
	typ := z.bitbuf >> 1 & 3
	z.consume(3)
	switch typ {
	case 0:
		z.align()
		var h [4]byte
		if !z.bytes(h[:]) {
			return
		}
		n := binary.LittleEndian.Uint16(h[:])
		if binary.LittleEndian.Uint16(h[2:]) != ^n {
			z.corrupt()
			return
		}
		z.stored, z.state = int(n), stStored
	case 1:
		z.lt, z.dt, z.state = &fixedLitlen, &fixedDist, stHuffman
	case 2:
		if z.dynamic() {
			z.lt, z.dt, z.state = &z.litlen, &z.dist, stHuffman
		}
	default:
		z.corrupt()
	}
}

// dynamic reads the code of a dynamic block, checking it as compress/flate
// does.
func (z *Reader) dynamic() bool {
	if !z.need(14) {
		return false
	}
	nlit := int(z.bitbuf&0x1f) + 257
	ndist := int(z.bitbuf>>5&0x1f) + 1
	npre := int(z.bitbuf>>10&0xf) + 4
	if nlit > 286 || ndist > 30 {
		z.corrupt()
		return false
	}
	z.consume(14)
	var pl [19]uint8
	for i := 0; i < npre; i++ {
		if !z.need(3) {
			return false
		}
		pl[precodeOrder[i]] = uint8(z.bitbuf & 7)
		z.consume(3)
	}
	if !build(z.pre[:], pl[:], precodeBits, precodeSym[:], z.sorted[:]) {
		z.corrupt()
		return false
	}
	lens := z.lens[:nlit+ndist]
	for i := 0; i < len(lens); {
		e, ok := z.sym(z.pre[:], precodeBits)
		if !ok {
			return false
		}
		if e&fExcept != 0 {
			z.corrupt()
			return false
		}
		z.consume(uint(e & 0xff))
		x := e >> 16
		if x < 16 {
			lens[i] = uint8(x)
			i++
			continue
		}
		var rep int
		var nb uint
		var v uint8
		switch x {
		case 16:
			if i == 0 {
				z.corrupt()
				return false
			}
			rep, nb, v = 3, 2, lens[i-1]
		case 17:
			rep, nb = 3, 3
		default:
			rep, nb = 11, 7
		}
		if !z.need(nb) {
			return false
		}
		rep += int(z.bitbuf & (1<<nb - 1))
		z.consume(nb)
		if i+rep > len(lens) {
			z.corrupt()
			return false
		}
		for ; rep > 0; rep-- {
			lens[i] = v
			i++
		}
	}
	if !build(z.litlen[:], lens[:nlit], litlenBits, litlenSym[:], z.sorted[:]) ||
		!build(z.dist[:], lens[nlit:], distBits, distSym[:], z.sorted[:]) {
		z.corrupt()
		return false
	}
	return true
}

// copyStored copies the data of a stored block, reading it as compress/flate
// does: up to the end of its window at a time, with io.ReadFull through a
// bufio.Reader, which gives what it holds, then reads straight into the
// window when 4096 bytes or more are wanted, else fills itself.
func (z *Reader) copyStored() {
	for z.stored > 0 && z.op < outFull {
		want, idle := min(z.stored, windowSize-z.op%windowSize), 0
		for want > 0 {
			var n int
			switch {
			case z.ip < z.iend:
				n = copy(z.out[z.op:z.op+want], z.in[z.ip:z.iend])
				z.ip += n
			case want < fillSize:
				if !z.fill() {
					return
				}
			case z.err != nil:
				return
			case z.rerr != nil:
				z.err = z.rerr
				return
			default:
				var err error
				n, err = z.r.Read(z.out[z.op : z.op+want])
				z.base += int64(n)
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				if err != nil && n < want {
					z.op += n
					z.err = err
					return
				}
				if n == 0 {
					if idle++; idle == 100 {
						z.err = io.ErrNoProgress
						return
					}
				}
			}
			z.op += n
			z.stored -= n
			want -= n
		}
	}
	if z.stored == 0 {
		z.endBlock()
	}
}

func (z *Reader) endBlock() {
	if z.final {
		z.state = stTrailer
	} else {
		z.state = stHeader
	}
}

// copyMatch copies length bytes from dist bytes back to out[op:], and may
// write up to 7 bytes past them.
func copyMatch(out []byte, op, dist, length int) {
	src, end := op-dist, op+length
	switch {
	case dist >= 8:
		for ; op < end; op, src = op+8, src+8 {
			binary.LittleEndian.PutUint64(out[op:], binary.LittleEndian.Uint64(out[src:]))
		}
	case dist == 1:
		v := uint64(out[src]) * 0x0101010101010101
		for ; op < end; op += 8 {
			binary.LittleEndian.PutUint64(out[op:], v)
		}
	default:
		// The pattern of dist bytes repeated over 8, written at steps of
		// the largest multiple of dist up to 8.
		var v uint64
		for i := 0; i < 8; i++ {
			v |= uint64(out[src+i%dist]) << (8 * i)
		}
		step := 8 - 8%dist
		for ; op < end; op += step {
			binary.LittleEndian.PutUint64(out[op:], v)
		}
	}
}

// huffman decodes a block of codes until it ends or the output is full.
func (z *Reader) huffman() {
	lt, dt := z.lt, z.dt
	out, in := z.out[:], z.in[:]
	for {
		bitbuf, bitsleft := z.bitbuf, z.bitsleft
		ip, op, iend := z.ip, z.op, z.iend
		// The fast loop, while there is input for a refill of the bit buffer
		// and room for a match. A refill brings it to 56 bits or more,
		// enough for a length (code and extra bits: 20) and a distance
		// (28), or for three literals (15 each) with the 11 bits of the
		// next look-up left over: so the entry for the next code is looked
		// up before the refill, which leaves the bits below bitsleft as
		// they are, as libdeflate does.
		if ip+8 <= iend && op < outFull {
			bitbuf |= binary.LittleEndian.Uint64(in[ip:]) << (bitsleft & 63)
			ip += int(63-bitsleft) >> 3
			bitsleft |= 56
			e := lt[bitbuf&(1<<litlenBits-1)]
			for {
				if e&fLiteral != 0 {
					out[op] = byte(e >> 16)
					op++
					bitbuf >>= e & 63
					bitsleft -= uint(e & 0xff)
					e = lt[bitbuf&(1<<litlenBits-1)]
					if e&fLiteral != 0 {
						out[op] = byte(e >> 16)
						op++
						bitbuf >>= e & 63
						bitsleft -= uint(e & 0xff)
						e = lt[bitbuf&(1<<litlenBits-1)]
						if e&fLiteral != 0 {
							out[op] = byte(e >> 16)
							op++
							bitbuf >>= e & 63
							bitsleft -= uint(e & 0xff)
							e = lt[bitbuf&(1<<litlenBits-1)]
						}
					}
					if ip+8 > iend || op >= outFull {
						break
					}
					bitbuf |= binary.LittleEndian.Uint64(in[ip:]) << (bitsleft & 63)
					ip += int(63-bitsleft) >> 3
					bitsleft |= 56
					continue
				}
				if e&fSubtable != 0 {
					bitbuf >>= litlenBits
					bitsleft -= litlenBits
					e = lt[e>>16+uint32(bitbuf)&(1<<(e>>8&15)-1)]
				}
				if e&fLiteral != 0 {
					out[op] = byte(e >> 16)
					op++
					bitbuf >>= e & 63
					bitsleft -= uint(e & 0xff)
				} else {
					if e&fExcept != 0 {
						z.bitbuf, z.bitsleft, z.ip, z.op = bitbuf, bitsleft, ip, op
						if e&fEOB != 0 {
							z.consume(uint(e & 0xff))
							z.endBlock()
						} else {
							z.corrupt()
						}
						return
					}
					n := e & 63
					length := int(e>>16) + int(bitbuf&(1<<n-1)>>(e>>8&15))
					bitbuf >>= n
					bitsleft -= uint(n)
					e = dt[bitbuf&(1<<distBits-1)]
					if e&fSubtable != 0 {
						bitbuf >>= distBits
						bitsleft -= distBits
						e = dt[e>>16+uint32(bitbuf)&(1<<(e>>8&15)-1)]
					}
					if e&fExcept != 0 {
						z.bitbuf, z.bitsleft, z.ip, z.op = bitbuf, bitsleft, ip, op
						z.corrupt()
						return
					}
					n = e & 63
					dist := int(e>>16) + int(bitbuf&(1<<n-1)>>(e>>8&15))
					bitbuf >>= n
					bitsleft -= uint(n)
					if dist > op {
						z.bitbuf, z.bitsleft, z.ip, z.op = bitbuf, bitsleft, ip, op
						z.corrupt()
						return
					}
					copyMatch(out, op, dist, length)
					op += length
				}
				if ip+8 > iend || op >= outFull {
					break
				}
				bitbuf |= binary.LittleEndian.Uint64(in[ip:]) << (bitsleft & 63)
				ip += int(63-bitsleft) >> 3
				bitsleft |= 56
				e = lt[bitbuf&(1<<litlenBits-1)]
			}
		}
		z.bitbuf, z.bitsleft, z.ip, z.op = bitbuf, bitsleft, ip, op
		if op >= outFull || !z.slowSymbol() {
			return
		}
	}
}

// slowSymbol decodes one symbol (with its match), reading input a byte at a
// time as it is needed. It reports whether the block goes on.
func (z *Reader) slowSymbol() bool {
	e, ok := z.sym(z.lt[:], litlenBits)
	if !ok {
		return false
	}
	if e&fLiteral != 0 {
		z.consume(uint(e & 0xff))
		z.out[z.op] = byte(e >> 16)
		z.op++
		return true
	}
	if e&fExcept != 0 {
		if e&fEOB != 0 {
			z.consume(uint(e & 0xff))
			z.endBlock()
		} else {
			z.corrupt()
		}
		return false
	}
	n := uint(e & 0xff)
	if !z.need(n) {
		return false
	}
	length := int(e>>16) + int(z.bitbuf&(1<<n-1)>>(e>>8&15))
	z.consume(n)
	if e, ok = z.sym(z.dt[:], distBits); !ok {
		return false
	}
	if e&fExcept != 0 {
		z.corrupt()
		return false
	}
	n = uint(e & 0xff)
	if !z.need(n) {
		return false
	}
	dist := int(e>>16) + int(z.bitbuf&(1<<n-1)>>(e>>8&15))
	z.consume(n)
	if dist > z.op {
		z.corrupt()
		return false
	}
	copyMatch(z.out[:], z.op, dist, length)
	z.op += length
	return true
}
