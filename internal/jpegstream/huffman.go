// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package jpegstream

import (
	"encoding/binary"
	"io"
)

// maxCodeLength is the maximum (inclusive) number of bits in a Huffman code.
const maxCodeLength = 16

// maxNCodes is the maximum (inclusive) number of codes in a Huffman tree.
const maxNCodes = 256

// lutBits is the log-2 size of the Huffman decoder's look-up tables. The
// standard library looks up 8 bits; 11 decode most codes, with their extra
// bits, in one step (9 and 10 measured 15% and 5% slower, 12 no faster).
const lutBits = 11

// huffman is a Huffman decoder, specified in section C.
type huffman struct {
	// length is the number of codes in the tree.
	nCodes int32
	// lut is the look-up table for the next lutBits bits in the bit-stream.
	// The high 8 bits of the uint16 are the encoded value. The low 8 bits
	// are 1 plus the code length, or 0 if the value is too large to fit in
	// lutBits bits.
	lut [1 << lutBits]uint16
	// fast decodes a code together with the extra bits that follow it
	// (RECEIVE and EXTEND, section F.2.2.1) when both fit in the next
	// lutBits bits: the extended value in the high 16 bits, the run of zero
	// coefficients (the high nibble of the code's value) in bits 8 to 11
	// and the number of bits used in the low 8; 0 if they do not fit. A DC
	// table has entries for the size-0 value too; an AC table has none for
	// its end-of-band and zero-run values.
	fast [1 << lutBits]int32
	// oversub marks a table with more codes of some length than fit in
	// that many bits, which only corrupt files have: its lut repeats the
	// standard library's 8-bit table, so it decodes what that decodes.
	oversub bool
	// vals are the decoded values, sorted by their encoding.
	vals [maxNCodes]uint8
	// minCodes[i] is the minimum code of length i, or -1 if there are no
	// codes of that length.
	minCodes [maxCodeLength]int32
	// maxCodes[i] is the maximum code of length i, or -1 if there are no
	// codes of that length.
	maxCodes [maxCodeLength]int32
	// valsIndices[i] is the index into vals of minCodes[i].
	valsIndices [maxCodeLength]int32
}

// noTable stands for a table that has not been defined: it has no codes.
var noTable = new(huffman)

// errShortHuffmanData means that an unexpected EOF occurred while decoding
// Huffman data.
var errShortHuffmanData = FormatError("short Huffman data")

// fillBits tops up d.bits with whole bytes of entropy-coded data to more than
// 56 bits. It stops early before a marker or at the end of the input, with
// d.bits.end set to the error that reading on would give: errMissingFF00,
// errShortHuffmanData or the reader's error. The standard library reads a
// byte at a time as the bits are needed; reading ahead within the entropy-
// coded segment decodes the same, since every byte before the marker belongs
// to the segment.
func (d *decoder) fillBits() {
	const lo, hi = 0x0101010101010101, 0x8080808080808080
	for d.bits.n <= 56 && d.bits.end == nil {
		i, j := d.bytes.i, d.bytes.j
		if j-i >= 8 {
			w := binary.BigEndian.Uint64(d.bytes.buf[i:])
			if (^w-lo)&w&hi == 0 { // no 0xff byte among the eight
				n := uint(d.bits.n)
				k := (64 - n) >> 3
				d.bits.a |= w >> n & (^uint64(0) << (64 - n - 8*k))
				d.bits.n += int32(8 * k)
				d.bytes.i = i + int(k)
				return
			}
		}
		if i == j {
			if err := d.fill(); err != nil {
				d.bits.end = shortData(err)
				return
			}
			continue
		}
		c := d.bytes.buf[i]
		if c == 0xff {
			// A stuffed 0xff is followed by 0x00; anything else is a
			// marker, which is left unread.
			if i+1 == j {
				// fill keeps the last two bytes, so the 0xff stays at
				// buf[d.bytes.i-1].
				d.bytes.i = j
				for d.bytes.i == d.bytes.j {
					if err := d.fill(); err != nil {
						d.bits.end = shortData(err)
						return
					}
				}
				i = d.bytes.i - 1
			}
			if d.bytes.buf[i+1] != 0x00 {
				d.bytes.i = i
				d.bits.end = errMissingFF00
				return
			}
			i++
		}
		d.bytes.i = i + 1
		d.bits.a |= uint64(c) << (56 - d.bits.n)
		d.bits.n += 8
	}
}

// shortData maps the end of the input to errShortHuffmanData, as the
// standard library's ensureNBits does.
func shortData(err error) error {
	if err == io.ErrUnexpectedEOF {
		return errShortHuffmanData
	}
	return err
}

// receiveExtend is the composition of RECEIVE and EXTEND, specified in section
// F.2.2.1.
//
// It returns the signed integer that's encoded in t bits, where t < 16. The
// possible return values are:
//
//   - t ==  0:   0
//   - t ==  1:   -1, +1
//   - t ==  2:   -3, -2, +2, +3
//   - t ==  3:   -7, -6, -5, -4, +4, +5, +6, +7
//   - ...
//   - t == 15:   -32767, -32766, ..., -16384, +16384, ..., +32766, +32767
func (d *decoder) receiveExtend(t uint8) (int32, error) {
	if d.bits.n < int32(t) {
		d.fillBits()
		if d.bits.n < int32(t) {
			return 0, d.bits.end
		}
	}
	if t == 0 {
		return 0, nil
	}
	x := int32(d.bits.a >> (64 - t))
	d.bits.a <<= t
	d.bits.n -= int32(t)
	if x < 1<<(t-1) {
		x += (-1 << t) + 1
	}
	return x, nil
}

// processDHT processes a Define Huffman Table marker, and initializes a huffman
// struct from its contents. Specified in section B.2.4.2.
func (d *decoder) processDHT(n int) error {
	for n > 0 {
		if n < 17 {
			return FormatError("DHT has wrong length")
		}
		if err := d.readFull(d.tmp[:17]); err != nil {
			return err
		}
		tc := d.tmp[0] >> 4
		if tc > maxTc {
			return FormatError("bad Tc value")
		}
		th := d.tmp[0] & 0x0f
		// The baseline th <= 1 restriction is specified in table B.5.
		if th > maxTh || (d.baseline && th > 1) {
			return FormatError("bad Th value")
		}
		// A new table, as the old one may be a scan's (multiscan.go).
		h := new(huffman)
		d.huff[tc][th] = h

		// Read nCodes and h.vals (and derive h.nCodes).
		// nCodes[i] is the number of codes with code length i.
		// h.nCodes is the total number of codes.
		h.nCodes = 0
		var nCodes [maxCodeLength]int32
		for i := range nCodes {
			nCodes[i] = int32(d.tmp[i+1])
			h.nCodes += nCodes[i]
		}
		if h.nCodes == 0 {
			return FormatError("Huffman table has zero length")
		}
		if h.nCodes > maxNCodes {
			return FormatError("Huffman table has excessive length")
		}
		n -= int(h.nCodes) + 17
		if n < 0 {
			return FormatError("DHT has wrong length")
		}
		if err := d.readFull(h.vals[:h.nCodes]); err != nil {
			return err
		}
		h.buildLUT(&nCodes, tc)

		// Derive minCodes, maxCodes, and valsIndices.
		var c, index int32
		for i, n := range nCodes {
			if n == 0 {
				h.minCodes[i] = -1
				h.maxCodes[i] = -1
				h.valsIndices[i] = -1
			} else {
				h.minCodes[i] = c
				h.maxCodes[i] = c + n - 1
				h.valsIndices[i] = index
				c += n
				index += n
			}
			c <<= 1
		}
	}
	return nil
}

// buildLUT derives h.lut and h.fast from the code counts.
func (h *huffman) buildLUT(nCodes *[maxCodeLength]int32, tc uint8) {
	clear(h.lut[:])
	clear(h.fast[:])
	h.oversub = false
	// code is the first code of the next length; the codes of length i+1
	// fit in i+1 bits while it is at most 1<<(i+2).
	var code int32
	for i := range nCodes {
		code = (code + nCodes[i]) << 1
		if code > 4<<i {
			h.oversub = true
		}
	}
	if h.oversub {
		// The standard library's table: codes of up to 8 bits, whose
		// positions wrap around for an oversubscribed length.
		var lut8 [256]uint16
		var x, code uint32
		for i := uint32(0); i < 8; i++ {
			code <<= 1
			for j := int32(0); j < nCodes[i]; j++ {
				base := uint8(code << (7 - i))
				lutValue := uint16(h.vals[x])<<8 | uint16(2+i)
				for k := uint8(0); k < 1<<(7-i); k++ {
					lut8[base|k] = lutValue
				}
				code++
				x++
			}
		}
		for i := range h.lut {
			h.lut[i] = lut8[i>>(lutBits-8)]
		}
	} else {
		var x, code uint32
		for i := uint32(0); i < lutBits; i++ {
			code <<= 1
			for j := int32(0); j < nCodes[i]; j++ {
				base := code << (lutBits - 1 - i)
				lutValue := uint16(h.vals[x])<<8 | uint16(2+i)
				for k := uint32(0); k < 1<<(lutBits-1-i); k++ {
					h.lut[base|k] = lutValue
				}
				code++
				x++
			}
		}
	}
	for i, v := range h.lut {
		if v == 0 {
			continue
		}
		l, val := uint32(v&0xff)-1, v>>8
		run, size := uint32(val>>4), uint32(val&0x0f)
		if tc == dcTable {
			if run != 0 {
				continue // a DC size above 15
			}
		} else if size == 0 {
			continue // EOB or ZRL
		}
		if l+size > lutBits {
			continue
		}
		var x int32
		if size != 0 {
			x = int32(uint32(i)>>(lutBits-l-size)) & (1<<size - 1)
			if x < 1<<(size-1) {
				x += (-1 << size) + 1
			}
		}
		h.fast[i] = x<<16 | int32(run<<8|(l+size))
	}
}

// decodeHuffman returns the next Huffman-coded value from the bit-stream,
// decoded according to h.
func (d *decoder) decodeHuffman(h *huffman) (uint8, error) {
	if h.nCodes == 0 {
		return 0, FormatError("uninitialized Huffman table")
	}
	if d.bits.n < lutBits {
		d.fillBits()
		if d.bits.n < lutBits {
			return d.decodeSlow(h)
		}
	}
	if v := h.lut[d.bits.a>>(64-lutBits)]; v != 0 {
		n := v&0xff - 1
		d.bits.a <<= n
		d.bits.n -= int32(n)
		return uint8(v >> 8), nil
	}
	return d.decodeSlow(h)
}

// decodeSlow is the standard library's decoding of a code a bit at a time,
// for codes longer than lutBits and for the last bits before a marker or the
// end of the data.
func (d *decoder) decodeSlow(h *huffman) (uint8, error) {
	if d.bits.n < 8 {
		// The standard library's look-up needs 8 bits; short of them it
		// carries on a bit at a time unless reading failed for a reason
		// other than the end of the entropy-coded data.
		if e := d.bits.end; e != nil && e != errMissingFF00 && e != errShortHuffmanData {
			return 0, e
		}
	} else if h.oversub {
		// The standard library's 8-bit look-up, which for such a table
		// can disagree with the code lengths.
		if v := h.lut[d.bits.a>>56<<(lutBits-8)]; v != 0 {
			n := v&0xff - 1
			d.bits.a <<= n
			d.bits.n -= int32(n)
			return uint8(v >> 8), nil
		}
	}
	for i, code := 0, int32(0); i < maxCodeLength; i++ {
		if d.bits.n == 0 {
			d.fillBits()
			if d.bits.n == 0 {
				return 0, d.bits.end
			}
		}
		code |= int32(d.bits.a >> 63)
		d.bits.a <<= 1
		d.bits.n--
		if code <= h.maxCodes[i] {
			return h.vals[h.valsIndices[i]+code-h.minCodes[i]], nil
		}
		code <<= 1
	}
	return 0, FormatError("bad Huffman code")
}

func (d *decoder) decodeBit() (bool, error) {
	if d.bits.n == 0 {
		d.fillBits()
		if d.bits.n == 0 {
			return false, d.bits.end
		}
	}
	ret := d.bits.a>>63 != 0
	d.bits.a <<= 1
	d.bits.n--
	return ret, nil
}

func (d *decoder) decodeBits(n int32) (uint32, error) {
	if d.bits.n < n {
		d.fillBits()
		if d.bits.n < n {
			return 0, d.bits.end
		}
	}
	ret := uint32(d.bits.a >> (64 - n))
	d.bits.a <<= n
	d.bits.n -= n
	return ret, nil
}
