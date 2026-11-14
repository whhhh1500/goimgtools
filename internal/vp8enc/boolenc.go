package vp8enc

import (
	"io"
	"math/bits"
)

// boolEnc is the boolean arithmetic encoder of RFC 6386 chapter 7, the
// mirror image of the decoder's partition reader. Finished bytes go to out
// through a small fixed buffer, so a partition never sits in memory whole.
//
// A carry can still change the last byte produced and turns any 0xff bytes
// after it into zeros, so that byte (prev) and the count of 0xff bytes
// following it (run) are held back until a later byte settles them.
type boolEnc struct {
	rng      int32 // range minus 1
	value    int32
	nbBits   int32
	run      int
	havePrev bool
	prev     byte

	buf  []byte
	n    int
	out  io.Writer
	size int64 // bytes handed to out or sitting in buf
	err  error
}

func (b *boolEnc) init(out io.Writer, buf []byte) {
	*b = boolEnc{rng: 254, nbBits: -8, buf: buf, out: out}
}

func (b *boolEnc) emit(c byte) {
	if b.n == len(b.buf) {
		b.drain()
	}
	b.buf[b.n] = c
	b.n++
	b.size++
}

func (b *boolEnc) drain() {
	if b.n > 0 && b.err == nil {
		_, b.err = b.out.Write(b.buf[:b.n])
	}
	b.n = 0
}

func (b *boolEnc) flush() {
	s := uint(8 + b.nbBits)
	v := b.value >> s
	b.value -= v << s
	b.nbBits -= 8
	if v&0xff == 0xff {
		b.run++ // may still become 0x00 through a carry
		return
	}
	carry := v&0x100 != 0
	if b.havePrev {
		if carry {
			b.prev++
		}
		b.emit(b.prev)
	}
	if b.run > 0 {
		fill := byte(0xff)
		if carry {
			fill = 0
		}
		for ; b.run > 0; b.run-- {
			b.emit(fill)
		}
	}
	b.prev, b.havePrev = byte(v), true
}

// putBit codes one bit; prob is the probability (out of 256) that it is 0.
func (b *boolEnc) putBit(bit bool, prob uint8) {
	split := (b.rng * int32(prob)) >> 8
	if bit {
		b.value += split + 1
		b.rng -= split + 1
	} else {
		b.rng = split
	}
	if b.rng < 127 {
		shift := int32(bits.LeadingZeros8(uint8(b.rng + 1)))
		b.rng = (b.rng+1)<<uint(shift) - 1
		b.value <<= uint(shift)
		b.nbBits += shift
		if b.nbBits > 0 {
			b.flush()
		}
	}
}

// putUint codes the low n bits of v, most significant first, at even odds.
func (b *boolEnc) putUint(v uint32, n int) {
	for n--; n >= 0; n-- {
		b.putBit(v>>uint(n)&1 != 0, 128)
	}
}

// finish pads the stream so every coded bit can be decoded, writes the held
// back bytes and returns the partition's length.
func (b *boolEnc) finish() (int64, error) {
	b.putUint(0, int(9-b.nbBits))
	b.nbBits = 0
	b.flush()
	if b.havePrev {
		b.emit(b.prev)
		b.havePrev = false
	}
	for ; b.run > 0; b.run-- {
		b.emit(0xff)
	}
	b.drain()
	return b.size, b.err
}
