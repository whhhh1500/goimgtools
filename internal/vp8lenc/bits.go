package vp8lenc

import (
	"io"
	"math/bits"
)

// bitWriter packs values least-significant bit first, the order VP8L reads
// them in. With w == nil everything stays in buf (used for the header, whose
// length must be known before the RIFF sizes are written); once w is set the
// buffer is flushed to it as it fills.
type bitWriter struct {
	w       io.Writer
	buf     []byte
	acc     uint64
	nacc    uint
	flushed int64 // bytes already handed to w
	err     error
}

const bitFlush = 32 << 10

// put appends the low n bits of v; n <= 32 and v < 1<<n.
func (b *bitWriter) put(v uint32, n uint) {
	b.acc |= uint64(v) << b.nacc
	b.nacc += n
	if b.nacc >= 32 {
		a := uint32(b.acc)
		b.buf = append(b.buf, byte(a), byte(a>>8), byte(a>>16), byte(a>>24))
		b.acc >>= 32
		b.nacc -= 32
		if b.w != nil && len(b.buf) >= bitFlush {
			b.flush()
		}
	}
}

// bitLen is the number of bits written so far.
func (b *bitWriter) bitLen() int64 {
	return (b.flushed+int64(len(b.buf)))*8 + int64(b.nacc)
}

func (b *bitWriter) flush() {
	if b.err == nil && len(b.buf) > 0 {
		_, b.err = b.w.Write(b.buf)
	}
	b.flushed += int64(len(b.buf))
	b.buf = b.buf[:0]
}

// finish pads to a whole byte and flushes.
func (b *bitWriter) finish() error {
	for b.nacc > 0 {
		b.buf = append(b.buf, byte(b.acc))
		b.acc >>= 8
		if b.nacc >= 8 {
			b.nacc -= 8
		} else {
			b.nacc = 0
		}
	}
	if b.w != nil {
		b.flush()
	}
	return b.err
}

// prefixCode splits an LZ77 length or distance code (v >= 1) into its prefix
// symbol and extra bits (RFC 9649 section 5.2.2).
func prefixCode(v uint32) (sym uint32, nbits uint, extra uint32) {
	d := v - 1
	if d < 4 {
		return d, 0, 0
	}
	hb := uint(bits.Len32(d)) - 1
	nbits = hb - 1
	sym = uint32(2*hb) + (d>>nbits)&1
	extra = d & (1<<nbits - 1)
	return sym, nbits, extra
}

// distMap is the table of section 5.2.2 that gives the 120 smallest distance
// codes to the pixels closest in two dimensions: entry i is (yoffset << 4) |
// (8 - xoffset) for distance code i+1.
var distMap = [120]uint8{
	0x18, 0x07, 0x17, 0x19, 0x28, 0x06, 0x27, 0x29, 0x16, 0x1a,
	0x26, 0x2a, 0x38, 0x05, 0x37, 0x39, 0x15, 0x1b, 0x36, 0x3a,
	0x25, 0x2b, 0x48, 0x04, 0x47, 0x49, 0x14, 0x1c, 0x35, 0x3b,
	0x46, 0x4a, 0x24, 0x2c, 0x58, 0x45, 0x4b, 0x34, 0x3c, 0x03,
	0x57, 0x59, 0x13, 0x1d, 0x56, 0x5a, 0x23, 0x2d, 0x44, 0x4c,
	0x55, 0x5b, 0x33, 0x3d, 0x68, 0x02, 0x67, 0x69, 0x12, 0x1e,
	0x66, 0x6a, 0x22, 0x2e, 0x54, 0x5c, 0x43, 0x4d, 0x65, 0x6b,
	0x32, 0x3e, 0x78, 0x01, 0x77, 0x79, 0x53, 0x5d, 0x11, 0x1f,
	0x64, 0x6c, 0x42, 0x4e, 0x76, 0x7a, 0x21, 0x2f, 0x75, 0x7b,
	0x31, 0x3f, 0x63, 0x6d, 0x52, 0x5e, 0x00, 0x74, 0x7c, 0x41,
	0x4f, 0x10, 0x20, 0x62, 0x6e, 0x30, 0x73, 0x7d, 0x51, 0x5f,
	0x40, 0x72, 0x7e, 0x61, 0x6f, 0x50, 0x71, 0x7f, 0x60, 0x70,
}

// planeCode maps a distMap entry back to its distance code (0 = none).
var planeCode [128]uint8

func init() {
	for i, v := range distMap {
		planeCode[v] = uint8(i + 1)
	}
}

// maxDistance is the largest pixel distance a distance code can express:
// the 40 prefix symbols reach code 1<<20, and codes above 120 mean code-120.
const maxDistance = 1<<20 - 120

// distCode converts a backward distance in pixels into the VP8L distance
// code for an image of the given width.
func distCode(dist, width int) uint32 {
	yoff := dist / width
	xoff := dist - yoff*width
	if xoff <= 8 && yoff < 8 {
		if c := planeCode[yoff<<4|(8-xoff)]; c != 0 {
			return uint32(c)
		}
	}
	if xoff > width-8 && yoff < 7 {
		if c := planeCode[(yoff+1)<<4|(8+width-xoff)]; c != 0 {
			return uint32(c)
		}
	}
	return uint32(dist) + 120
}
