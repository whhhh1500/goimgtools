// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package vp8

import (
	"encoding/binary"
	"io"
	"math/bits"
)

// Each VP8 frame consists of between 2 and 9 bitstream partitions.
// Each partition is byte-aligned and is independently arithmetic-encoded.
//
// This file implements decoding a partition's bitstream, as specified in
// chapter 7. The implementation follows libwebp's approach (VP8GetBit)
// instead of the specification's reference C implementation: the input is
// taken 56 bits at a time, and the range is renormalized with one shift.

// uniformProb represents a 50% probability that the next bit is 0.
const uniformProb = 128

// partition holds arithmetic-coded bits.
//
// Unlike golang.org/x/image/vp8, which reads every partition into memory
// whole, a partition here is a window of a few kilobytes that is refilled
// from the io.ReaderAt as decoding proceeds.
type partition struct {
	// buf is the window of input bytes currently in memory.
	buf []byte
	// store is the backing array of buf.
	store []byte
	// ra, off and end locate the bytes that have not been read into buf yet.
	ra       io.ReaderAt
	off, end int64
	// err is the first I/O error from ra.
	err error
	// r is how many of buf's bytes have been consumed.
	r int
	// rangeM1 is range minus 1, where range is in the arithmetic coding sense,
	// not the Go language sense.
	rangeM1 uint32
	// As in libwebp's VP8BitReader, value holds bits+8 bits shifted out of
	// buf but not yet consumed; bits goes below 0 when fewer than 8 are
	// left, and value is then refilled, 56 bits at a time.
	value uint64
	bits  int
	// unexpectedEOF tells whether we tried to read past buf.
	unexpectedEOF bool
}

// partitionWindow is the size of a partition's read window.
const partitionWindow = 8 << 10

// init initializes the partition to read the n bytes at off of ra.
func (p *partition) init(ra io.ReaderAt, off int64, n int) {
	if p.store == nil {
		p.store = make([]byte, partitionWindow)
	}
	p.ra, p.off, p.end, p.err = ra, off, off+int64(n), nil
	p.buf = p.store[:0]
	p.r = 0
	p.rangeM1 = 254
	p.value = 0
	p.bits = -8
	p.unexpectedEOF = false
}

// fill reads the next window and reports whether it holds any bytes.
func (p *partition) fill() bool {
	if p.off >= p.end {
		return false
	}
	n := int(min(int64(len(p.store)), p.end-p.off))
	k, err := p.ra.ReadAt(p.store[:n], p.off)
	if k < n {
		if err == nil || err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		p.err = err
		p.end = p.off + int64(k)
	}
	p.off += int64(k)
	p.buf = p.store[:k]
	p.r = 0
	return k > 0
}

// load adds 56 bits to value, or 8 near the end of the window, and
// reports false when the partition has no more.
func (p *partition) load() bool {
	if p.r+8 <= len(p.buf) {
		p.value = p.value<<56 | binary.BigEndian.Uint64(p.buf[p.r:])>>8
		p.r += 7
		p.bits += 56
		return true
	}
	if p.r >= len(p.buf) && !p.fill() {
		return false
	}
	p.value = p.value<<8 | uint64(p.buf[p.r])
	p.r++
	p.bits += 8
	return true
}

// refill is load for a reader whose value and bits are held by the caller.
func (p *partition) refill(value uint64, nb int) (uint64, int, bool) {
	p.value, p.bits = value, nb
	if !p.load() {
		return value, nb, false
	}
	return p.value, p.bits, true
}

// bitOf is readBit for a reader whose value, bits and rangeM1 are held by
// the caller in local variables (refilled first when bits is below 0),
// small enough to be inlined: it returns the bit and the new state.
//
// As libwebp's VP8GetBitAlt, a range that stays at 127 or more (less 1:
// 126) needs no renormalizing, and one below is renormalized from tables.
func bitOf(value uint64, nb int, rangeM1 uint32, prob uint8) (bool, uint64, int, uint32) {
	pos := uint(nb) & 63
	split := rangeM1 * uint32(prob) >> 8
	r := split
	bit := uint32(value>>pos) > split
	if bit {
		r = rangeM1 - split - 1
		value -= uint64(split+1) << pos
	}
	if r <= 0x7e {
		nb -= int(log2Range[r&0x7f])
		r = uint32(newRange[r&0x7f])
	}
	return bit, value, nb, r
}

// log2Range[r] is how far a range of r+1 (below 128) is shifted to reach
// 128 or more, and newRange[r] the range less 1 after that.
var log2Range, newRange = func() (l, n [128]uint8) {
	for r := range l {
		shift := 7 ^ (31 - bits.LeadingZeros32(uint32(r+1)))
		l[r], n[r] = uint8(shift), uint8((r+1)<<shift-1)
	}
	return
}()

// signedOf is libwebp's VP8GetSigned: the next bit, of probability one
// half, as the sign of v, without a branch (the sign of a coefficient is as
// likely to be either). It takes one bit off the range, which comes to the
// same as bitOf with probability 128 except when rangeM1 is 254, which it
// is only before the first bit of a partition.
func signedOf(value uint64, nb int, rangeM1 uint32, v int32) (int32, uint64, int, uint32) {
	pos := uint(nb) & 63
	split := rangeM1 >> 1
	mask := int32(split-uint32(value>>pos)) >> 31 // -1 for a 1
	value -= uint64((split+1)&uint32(mask)) << pos
	return (v ^ mask) - mask, value, nb - 1, (rangeM1 + uint32(mask)) | 1
}

// readBit returns the next bit, as libwebp's VP8GetBit does.
func (p *partition) readBit(prob uint8) bool {
	rng := p.rangeM1
	if p.bits < 0 && !p.load() {
		p.unexpectedEOF = true
		return false
	}
	pos := uint(p.bits) & 63
	split := rng * uint32(prob) >> 8
	bit := uint32(p.value>>pos) > split
	if bit {
		rng -= split
		p.value -= uint64(split+1) << pos
	} else {
		rng = split + 1
	}
	shift := 7 ^ (31 - bits.LeadingZeros32(rng))
	p.rangeM1 = rng<<shift - 1
	p.bits -= shift
	return bit
}

// readUint returns the next n-bit unsigned integer.
func (p *partition) readUint(prob, n uint8) uint32 {
	var u uint32
	for n > 0 {
		n--
		if p.readBit(prob) {
			u |= 1 << n
		}
	}
	return u
}

// readInt returns the next n-bit signed integer.
func (p *partition) readInt(prob, n uint8) int32 {
	u := p.readUint(prob, n)
	b := p.readBit(prob)
	if b {
		return -int32(u)
	}
	return int32(u)
}

// readOptionalInt returns the next n-bit signed integer in an encoding
// where the likely result is zero.
func (p *partition) readOptionalInt(prob, n uint8) int32 {
	if !p.readBit(prob) {
		return 0
	}
	return p.readInt(prob, n)
}
