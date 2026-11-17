// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package jpegstream is Go's image/jpeg encoder (go1.27.1) changed to read
// the image from a pipeline.Source in 16-row bands, and made faster (the bit
// writer, quantization, the colour conversion and the gathering of the
// blocks); the output bytes are the same.
package jpegstream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	mbits "math/bits"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// div returns a/b rounded to the nearest integer, instead of rounded to zero.
func div(a, b int32) int32 {
	if a >= 0 {
		return (a + (b >> 1)) / b
	}
	return -((-a + (b >> 1)) / b)
}

// recip returns the number divBy needs to divide by b: 2^32 / b, rounded up.
func recip(b int32) uint64 { return (1<<32 + uint64(b) - 1) / uint64(b) }

// divBy is div(a, b) for a b whose recip is m, with a multiplication in
// place of the division. The result is the same for every a an encoder
// meets: it is exact while (|a| + b/2) * b < 2^32, and coefficients are
// below 2^19, divisors (8 times a quantization value) at most 2040.
//
// It has no branch on the sign: s is 0 for a >= 0 and -1 below, and
// x^s - s is x or -x.
func divBy(a, b int32, m uint64) int32 {
	s := a >> 31
	v := int32(uint64((a^s)-s+(b>>1)) * m >> 32)
	return (v ^ s) - s
}

type quantIndex int

const (
	quantIndexLuminance quantIndex = iota
	quantIndexChrominance
	nQuantIndex
)

type huffIndex int

const (
	huffIndexLuminanceDC huffIndex = iota
	huffIndexLuminanceAC
	huffIndexChrominanceDC
	huffIndexChrominanceAC
	nHuffIndex
)

// huffmanSpec specifies a Huffman encoding.
type huffmanSpec struct {
	// count[i] is the number of codes of length i+1 bits.
	count [16]byte
	// value[i] is the decoded value of the i'th codeword.
	value []byte
}

// theHuffmanSpec is the Huffman encoding specifications.
//
// This encoder uses the same Huffman encoding for all images. It is also the
// same Huffman encoding used by section K.3 of the spec.
//
// The DC tables have 12 decoded values, called categories.
//
// The AC tables have 162 decoded values: bytes that pack a 4-bit Run and a
// 4-bit Size. There are 16 valid Runs and 10 valid Sizes, plus two special R|S
// cases: 0|0 (meaning EOB) and F|0 (meaning ZRL).
var theHuffmanSpec = [nHuffIndex]huffmanSpec{
	// Luminance DC.
	{
		[16]byte{0, 1, 5, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0},
		[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
	},
	// Luminance AC.
	{
		[16]byte{0, 2, 1, 3, 3, 2, 4, 3, 5, 5, 4, 4, 0, 0, 1, 125},
		[]byte{
			0x01, 0x02, 0x03, 0x00, 0x04, 0x11, 0x05, 0x12,
			0x21, 0x31, 0x41, 0x06, 0x13, 0x51, 0x61, 0x07,
			0x22, 0x71, 0x14, 0x32, 0x81, 0x91, 0xa1, 0x08,
			0x23, 0x42, 0xb1, 0xc1, 0x15, 0x52, 0xd1, 0xf0,
			0x24, 0x33, 0x62, 0x72, 0x82, 0x09, 0x0a, 0x16,
			0x17, 0x18, 0x19, 0x1a, 0x25, 0x26, 0x27, 0x28,
			0x29, 0x2a, 0x34, 0x35, 0x36, 0x37, 0x38, 0x39,
			0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48, 0x49,
			0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58, 0x59,
			0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68, 0x69,
			0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78, 0x79,
			0x7a, 0x83, 0x84, 0x85, 0x86, 0x87, 0x88, 0x89,
			0x8a, 0x92, 0x93, 0x94, 0x95, 0x96, 0x97, 0x98,
			0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7,
			0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6,
			0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3, 0xc4, 0xc5,
			0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2, 0xd3, 0xd4,
			0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda, 0xe1, 0xe2,
			0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9, 0xea,
			0xf1, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
			0xf9, 0xfa,
		},
	},
	// Chrominance DC.
	{
		[16]byte{0, 3, 1, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0},
		[]byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11},
	},
	// Chrominance AC.
	{
		[16]byte{0, 2, 1, 2, 4, 4, 3, 4, 7, 5, 4, 4, 0, 1, 2, 119},
		[]byte{
			0x00, 0x01, 0x02, 0x03, 0x11, 0x04, 0x05, 0x21,
			0x31, 0x06, 0x12, 0x41, 0x51, 0x07, 0x61, 0x71,
			0x13, 0x22, 0x32, 0x81, 0x08, 0x14, 0x42, 0x91,
			0xa1, 0xb1, 0xc1, 0x09, 0x23, 0x33, 0x52, 0xf0,
			0x15, 0x62, 0x72, 0xd1, 0x0a, 0x16, 0x24, 0x34,
			0xe1, 0x25, 0xf1, 0x17, 0x18, 0x19, 0x1a, 0x26,
			0x27, 0x28, 0x29, 0x2a, 0x35, 0x36, 0x37, 0x38,
			0x39, 0x3a, 0x43, 0x44, 0x45, 0x46, 0x47, 0x48,
			0x49, 0x4a, 0x53, 0x54, 0x55, 0x56, 0x57, 0x58,
			0x59, 0x5a, 0x63, 0x64, 0x65, 0x66, 0x67, 0x68,
			0x69, 0x6a, 0x73, 0x74, 0x75, 0x76, 0x77, 0x78,
			0x79, 0x7a, 0x82, 0x83, 0x84, 0x85, 0x86, 0x87,
			0x88, 0x89, 0x8a, 0x92, 0x93, 0x94, 0x95, 0x96,
			0x97, 0x98, 0x99, 0x9a, 0xa2, 0xa3, 0xa4, 0xa5,
			0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xb2, 0xb3, 0xb4,
			0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xc2, 0xc3,
			0xc4, 0xc5, 0xc6, 0xc7, 0xc8, 0xc9, 0xca, 0xd2,
			0xd3, 0xd4, 0xd5, 0xd6, 0xd7, 0xd8, 0xd9, 0xda,
			0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7, 0xe8, 0xe9,
			0xea, 0xf2, 0xf3, 0xf4, 0xf5, 0xf6, 0xf7, 0xf8,
			0xf9, 0xfa,
		},
	},
}

// huffmanLUT is a compiled look-up table representation of a huffmanSpec.
// Each value maps to a uint32 of which the 8 most significant bits hold the
// codeword size in bits and the 24 least significant bits hold the codeword.
// The maximum codeword size is 16 bits.
type huffmanLUT []uint32

func (h *huffmanLUT) init(s huffmanSpec) {
	maxValue := 0
	for _, v := range s.value {
		if int(v) > maxValue {
			maxValue = int(v)
		}
	}
	*h = make([]uint32, maxValue+1)
	code, k := uint32(0), 0
	for i := 0; i < len(s.count); i++ {
		nBits := uint32(i+1) << 24
		for j := uint8(0); j < s.count[i]; j++ {
			(*h)[s.value[k]] = nBits | code
			code++
			k++
		}
		code <<= 1
	}
}

// theHuffmanLUT are compiled representations of theHuffmanSpec.
var theHuffmanLUT [4]huffmanLUT

func init() {
	for i, s := range theHuffmanSpec {
		theHuffmanLUT[i].init(s)
	}
}

// writer is a buffered writer.
type writer interface {
	Flush() error
	io.Writer
	io.ByteWriter
}

// encoder encodes an image to the JPEG format.
type encoder struct {
	// w is the writer to write to. err is the first error encountered during
	// writing. All attempted writes after the first error become no-ops.
	w   writer
	err error
	// buf is a scratch buffer.
	buf [16]byte
	// bits holds nBits accumulated bits (the low ones) to write to w, by
	// way of out, where whole bytes go, stuffed, until there are enough to
	// write.
	bits  uint64
	nBits uint32
	out   []byte
	// quant is the scaled quantization tables, in zig-zag order.
	quant [nQuantIndex][blockSize]byte
	// recip is recip(8 * quant), for dividing by a quantization value
	// without a division.
	recip [nQuantIndex][blockSize]uint64
	// natHalf and natRecip are half of 8 * quant and recip, in natural
	// order, for fdctQuantSIMD.
	natHalf  [nQuantIndex][blockSize]int32
	natRecip [nQuantIndex][blockSize]uint32
	// deadZone, when set, is how far from zero (in 1/100000 of a step) an
	// AC coefficient of each table must be to be kept (QuantTableJpegli).
	deadZone *[2]int64
	// trellis is set for Options.Trellis.
	trellis bool
	// hY and vY are the sampling factors of Y in a colour image: 2 and 2
	// for 4:2:0, 2 and 1 for 4:2:2, 1 and 1 for 4:4:4 (Cb and Cr 1 and 1).
	hY, vY int
}

// drain writes out the bytes of the bit-stream gathered in out.
func (e *encoder) drain() {
	if len(e.out) == 0 {
		return
	}
	if e.err == nil {
		_, e.err = e.w.Write(e.out)
	}
	e.out = e.out[:0]
}

func (e *encoder) flush() {
	e.drain()
	if e.err != nil {
		return
	}
	e.err = e.w.Flush()
}

func (e *encoder) write(p []byte) {
	e.drain()
	if e.err != nil {
		return
	}
	_, e.err = e.w.Write(p)
}

func (e *encoder) writeByte(b byte) {
	e.drain()
	if e.err != nil {
		return
	}
	e.err = e.w.WriteByte(b)
}

// emit emits the least significant nBits bits of bits to the bit-stream.
// The precondition is bits < 1<<nBits && nBits <= 32. The bytes are what
// the standard library's emit writes, a byte at a time, gathered 32 bits at
// a time.
func (e *encoder) emit(bits, nBits uint32) {
	e.bits = e.bits<<nBits | uint64(bits)
	e.nBits += nBits
	if e.nBits >= 32 {
		e.spill()
	}
}

// spill moves the oldest 32 bits of the accumulator to out as four bytes,
// each 0xff followed by a stuffed 0x00.
func (e *encoder) spill() {
	e.bits, e.nBits = e.spillAcc(e.bits, e.nBits)
}

// spillAcc is spill for an accumulator acc of n >= 32 bits held outside e;
// it returns what is left of it.
func (e *encoder) spillAcc(acc uint64, n uint32) (uint64, uint32) {
	n -= 32
	w := uint32(acc >> n)
	acc &= 1<<n - 1
	if (^w-0x01010101)&w&0x80808080 == 0 { // no 0xff byte
		e.out = binary.BigEndian.AppendUint32(e.out, w)
	} else {
		for s := 24; s >= 0; s -= 8 {
			b := byte(w >> s)
			e.out = append(e.out, b)
			if b == 0xff {
				e.out = append(e.out, 0x00)
			}
		}
	}
	if len(e.out) >= 4096 {
		e.drain()
	}
	return acc, n
}

// padBits ends a scan: the last byte is padded with 1's, as the standard
// library's emit(0x7f, 7) does, and the bits left over are dropped.
func (e *encoder) padBits() {
	e.emit(0x7f, 7)
	for e.nBits >= 8 {
		b := byte(e.bits >> (e.nBits - 8))
		e.nBits -= 8
		e.out = append(e.out, b)
		if b == 0xff {
			e.out = append(e.out, 0x00)
		}
	}
	e.bits, e.nBits = 0, 0
}

// writeMarkerHeader writes the header for a marker with the given length.
func (e *encoder) writeMarkerHeader(marker uint8, markerlen int) {
	e.buf[0] = 0xff
	e.buf[1] = marker
	e.buf[2] = uint8(markerlen >> 8)
	e.buf[3] = uint8(markerlen & 0xff)
	e.write(e.buf[:4])
}

// writeDQT writes a Define Quantization Table marker for each table the
// components use (a gray image only the first), as libjpeg does.
func (e *encoder) writeDQT(nComponent int) {
	for i := range e.quant {
		if i > 0 && nComponent == 1 {
			break
		}
		e.writeMarkerHeader(dqtMarker, 2+1+blockSize)
		e.writeByte(uint8(i))
		e.write(e.quant[i][:])
	}
}

// writeSOF0 writes the Start Of Frame (Baseline Sequential) marker.
func (e *encoder) writeSOF0(size image.Point, nComponent int) {
	e.writeSOF(sof0Marker, size, nComponent)
}

// writeSOF writes a Start Of Frame marker of the given kind.
func (e *encoder) writeSOF(marker uint8, size image.Point, nComponent int) {
	markerlen := 8 + 3*nComponent
	e.writeMarkerHeader(marker, markerlen)
	e.buf[0] = 8 // 8-bit color.
	e.buf[1] = uint8(size.Y >> 8)
	e.buf[2] = uint8(size.Y & 0xff)
	e.buf[3] = uint8(size.X >> 8)
	e.buf[4] = uint8(size.X & 0xff)
	e.buf[5] = uint8(nComponent)
	if nComponent == 1 {
		e.buf[6] = 1
		// No subsampling for grayscale image.
		e.buf[7] = 0x11
		e.buf[8] = 0x00
	} else {
		for i := 0; i < nComponent; i++ {
			e.buf[3*i+6] = uint8(i + 1)
			// Y at hY x vY, Cb and Cr at 1 x 1.
			e.buf[3*i+7] = 0x11
			if i == 0 {
				e.buf[7] = uint8(e.hY<<4 | e.vY)
			}
			e.buf[3*i+8] = "\x00\x01\x01"[i]
		}
	}
	e.write(e.buf[:3*(nComponent-1)+9])
}

// writeDHT writes the standard Huffman tables.
func (e *encoder) writeDHT(nComponent int) {
	specs := theHuffmanSpec[:]
	if nComponent == 1 {
		// Drop the Chrominance tables.
		specs = specs[:2]
	}
	e.writeDHTSpecs([]byte("\x00\x10\x01\x11")[:len(specs)], specs)
}

// writeBlock writes a block of pixel data using the given quantization table,
// returning the post-quantized DC value of the DCT-transformed block. b is in
// natural (not zig-zag) order.
func (e *encoder) writeBlock(b *block, q quantIndex, prevDC int32) int32 {
	var zz qblock
	e.quantize(b, q, &zz)
	return e.writeQuantized(&zz, q, prevDC)
}

// qblock is a block transformed and quantized, in zig-zag order. Baseline
// coefficients of 8-bit samples fit 16 bits.
type qblock [blockSize]int16

// quantize transforms b (in place, unless with AVX2) and quantizes it with
// table q into zz.
func (e *encoder) quantize(b *block, q quantIndex, zz *qblock) {
	if useSIMD && e.deadZone == nil {
		fdctQuantSIMD(b, &e.natHalf[q], &e.natRecip[q], (*[blockSize]int16)(zz))
		return
	}
	fdct(b)
	e.quantizeCoefs(b, q, zz)
}

// quantizeCoefs quantizes the DCT coefficients b (natural order, 8 times
// their value) into zz (zig-zag order) with table q.
func (e *encoder) quantizeCoefs(b *block, q quantIndex, zz *qblock) {
	quant, rec := &e.quant[q], &e.recip[q]
	for zig := 0; zig < blockSize; zig++ {
		zz[zig] = int16(divBy(b[unzig[zig]], 8*int32(quant[zig]), rec[zig]))
	}
	if e.deadZone != nil {
		// An AC coefficient that rounds to one step is dropped unless it
		// is at least the dead zone's fraction of a step from zero.
		dz := e.deadZone[q]
		for zig := 1; zig < blockSize; zig++ {
			if v := zz[zig]; v == 1 || v == -1 {
				x := int64(b[unzig[zig]])
				if x < 0 {
					x = -x
				}
				if x*100000 < dz*8*int64(quant[zig]) {
					zz[zig] = 0
				}
			}
		}
	}
}

// writeQuantized emits a quantized block, its DC as the difference to
// prevDC, and returns its DC.
//
// Each value goes in with its code, the accumulator held in local variables
// for the whole block; within the block it fills up to 64 bits before
// eight bytes go out (as libjpeg-turbo's put_buffer), and is left with
// fewer than 32, as emit leaves it.
func (e *encoder) writeQuantized(zz *qblock, q quantIndex, prevDC int32) int32 {
	dcLUT, acLUT := theHuffmanLUT[2*q], theHuffmanLUT[2*q+1]
	acc, n := e.bits, e.nBits
	// Each value goes in as its code x (a huffmanLUT entry) followed by its
	// nb low bits (of v-1 for a negative v: v+s, s its sign).
	dc := int32(zz[0])
	v := dc - prevDC
	s := v >> 31
	nb := uint32(mbits.Len32(uint32((v ^ s) - s)))
	x := dcLUT[nb]
	size := x>>24 + nb
	code := uint64((x&(1<<24-1))<<nb | uint32(v+s)&(1<<nb-1))
	if n+size > 64 {
		acc, n = e.put64(acc, n, code, size)
	} else {
		acc, n = acc<<(size&63)|code, n+size
	}
	nz := nonzeros(zz) &^ 1
	last := 0
	for ; nz != 0; nz &= nz - 1 {
		zig := mbits.TrailingZeros64(nz)
		run := uint32(zig - last - 1)
		for ; run > 15; run -= 16 {
			x := acLUT[0xf0]
			size := x >> 24
			if n+size > 64 {
				acc, n = e.put64(acc, n, uint64(x&(1<<24-1)), size)
			} else {
				acc, n = acc<<(size&63)|uint64(x&(1<<24-1)), n+size
			}
		}
		v := int32(zz[zig])
		s := v >> 31
		nb := uint32(mbits.Len32(uint32((v ^ s) - s)))
		x := acLUT[run<<4|nb]
		size := x>>24 + nb
		code := uint64((x&(1<<24-1))<<nb | uint32(v+s)&(1<<nb-1))
		if n+size > 64 {
			acc, n = e.put64(acc, n, code, size)
		} else {
			acc, n = acc<<(size&63)|code, n+size
		}
		last = zig
	}
	if last != blockSize-1 {
		x := acLUT[0x00]
		size := x >> 24
		if n+size > 64 {
			acc, n = e.put64(acc, n, uint64(x&(1<<24-1)), size)
		} else {
			acc, n = acc<<(size&63)|uint64(x&(1<<24-1)), n+size
		}
	}
	for n >= 32 {
		acc, n = e.spillAcc(acc, n)
	}
	e.bits, e.nBits = acc, n
	return dc
}

// put64 adds the size low bits of code to the accumulator acc of n bits
// when they do not fit: the accumulator filled to 64 bits goes out as
// eight bytes (each 0xff followed by a stuffed 0x00), and the rest of code
// is what is left.
func (e *encoder) put64(acc uint64, n uint32, code uint64, size uint32) (uint64, uint32) {
	k := 64 - n
	w := acc<<k | code>>(size-k)
	if (^w-0x0101010101010101)&w&0x8080808080808080 == 0 { // no 0xff byte
		e.out = binary.BigEndian.AppendUint64(e.out, w)
	} else {
		for s := 56; s >= 0; s -= 8 {
			b := byte(w >> s)
			e.out = append(e.out, b)
			if b == 0xff {
				e.out = append(e.out, 0x00)
			}
		}
	}
	if len(e.out) >= 4096 {
		e.drain()
	}
	n = size - k
	return code & (1<<n - 1), n
}

// nonzeros returns the mask of the nonzero values of zz: bit k is set when
// zz[k] != 0. Four values at a time, as the lanes of a uint64: the top bit
// of a 16-bit lane of (w&0x7fff...)+0x7fff... | w is set when the lane is
// not 0 (the sum does not carry out of the lane), and multiplying the top
// bits, moved to bits 0, 16, 32 and 48, by 2^48+2^33+2^18+2^3 gathers them
// in bits 48 to 51, with no other product landing there or carrying.
func nonzerosGo(zz *qblock) uint64 {
	const low, top = 0x7fff7fff7fff7fff, 0x8000800080008000
	var nz uint64
	for i := 0; i < blockSize; i += 4 {
		q := zz[i : i+4 : i+4]
		w := uint64(uint16(q[0])) | uint64(uint16(q[1]))<<16 | uint64(uint16(q[2]))<<32 | uint64(uint16(q[3]))<<48
		t := ((w & low) + low | w) & top >> 15
		nz |= (t * (1<<48 | 1<<33 | 1<<18 | 1<<3) >> 48) << i
	}
	return nz
}

// toYCbCr converts the 8x8 region of m whose top-left corner is p to its
// YCbCr values.
func toYCbCr(m image.Image, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	xmax := b.Max.X - 1
	ymax := b.Max.Y - 1
	for j := 0; j < 8; j++ {
		for i := 0; i < 8; i++ {
			r, g, b, _ := m.At(min(p.X+i, xmax), min(p.Y+j, ymax)).RGBA()
			yBlock[8*j+i], cbBlock[8*j+i], crBlock[8*j+i] = ycc(int32(r>>8), int32(g>>8), int32(b>>8))
		}
	}
}

// grayToY stores the 8x8 region of m whose top-left corner is p in yBlock.
func grayToY(m *image.Gray, p image.Point, yBlock *block) {
	b := m.Bounds()
	xmax := b.Max.X - 1
	ymax := b.Max.Y - 1
	pix := m.Pix
	for j := 0; j < 8; j++ {
		for i := 0; i < 8; i++ {
			idx := m.PixOffset(min(p.X+i, xmax), min(p.Y+j, ymax))
			yBlock[8*j+i] = int32(pix[idx])
		}
	}
}

// ycc converts RGB to YCbCr as libjpeg does (jccolor.c), so that a JPEG
// comes out as libjpeg-turbo writes it: its 16-bit fixed-point factors,
// with Cb and Cr rounded by one half less a little, which keeps them
// within 0 to 255.
func ycc(r, g, b int32) (yy, cb, cr int32) {
	yy = (19595*r + 38470*g + 7471*b + 1<<15) >> 16
	cb = (-11059*r - 21709*g + 32768*b + 128<<16 + 1<<15 - 1) >> 16
	cr = (32768*r - 27439*g - 5329*b + 128<<16 + 1<<15 - 1) >> 16
	return yy, cb, cr
}

// pixCols returns the byte offsets, in a row of m's pixels (bpp bytes
// each), of the 8 columns from x on, clamped to m's last column.
func pixCols(b image.Rectangle, x, bpp int) (col [8]int) {
	for i := range col {
		col[i] = (min(x+i, b.Max.X-1) - b.Min.X) * bpp
	}
	return col
}

// rgbaToYCbCr is a specialized version of toYCbCr for image.RGBA images.
func rgbaToYCbCr(m *image.RGBA, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	if useSIMD && image.Rect(p.X, p.Y, p.X+8, p.Y+8).In(b) {
		o := m.PixOffset(p.X, p.Y)
		_ = m.Pix[o+7*m.Stride+31]
		rgbaBlockSIMD(&m.Pix[o], m.Stride, yBlock, cbBlock, crBlock)
		return
	}
	col := pixCols(b, p.X, 4)
	for j := 0; j < 8; j++ {
		row := m.Pix[(min(p.Y+j, b.Max.Y-1)-b.Min.Y)*m.Stride:]
		for i := 0; i < 8; i++ {
			pix := row[col[i] : col[i]+3 : col[i]+3]
			yBlock[8*j+i], cbBlock[8*j+i], crBlock[8*j+i] = ycc(int32(pix[0]), int32(pix[1]), int32(pix[2]))
		}
	}
}

// nrgbaToYCbCr is a specialized version of toYCbCr for image.NRGBA images:
// the values At().RGBA() gives (colour multiplied by alpha, as
// color.NRGBA.RGBA computes it), without making a color for every pixel.
func nrgbaToYCbCr(m *image.NRGBA, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	col := pixCols(b, p.X, 4)
	for j := 0; j < 8; j++ {
		row := m.Pix[(min(p.Y+j, b.Max.Y-1)-b.Min.Y)*m.Stride:]
		for i := 0; i < 8; i++ {
			pix := row[col[i] : col[i]+4 : col[i]+4]
			r, g, bl := pix[0], pix[1], pix[2]
			if a := uint32(pix[3]); a != 0xff {
				r = uint8(uint32(r) * 0x101 * a / 0xff >> 8)
				g = uint8(uint32(g) * 0x101 * a / 0xff >> 8)
				bl = uint8(uint32(bl) * 0x101 * a / 0xff >> 8)
			}
			yBlock[8*j+i], cbBlock[8*j+i], crBlock[8*j+i] = ycc(int32(r), int32(g), int32(bl))
		}
	}
}

// paletteYCbCr converts a palette once, to what toYCbCr makes of each of
// its colours.
func paletteYCbCr(pal color.Palette, lut *[256][3]uint8) {
	for i, c := range pal {
		if i > 255 {
			break
		}
		r, g, b, _ := c.RGBA()
		yy, cb, cr := ycc(int32(r>>8), int32(g>>8), int32(b>>8))
		lut[i][0], lut[i][1], lut[i][2] = uint8(yy), uint8(cb), uint8(cr)
	}
}

// palettedToYCbCr is a specialized version of toYCbCr for image.Paletted
// images whose palette has been converted with paletteYCbCr.
func palettedToYCbCr(m *image.Paletted, lut *[256][3]uint8, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	xmax := b.Max.X - 1
	ymax := b.Max.Y - 1
	for j := 0; j < 8; j++ {
		sj := p.Y + j
		if sj > ymax {
			sj = ymax
		}
		offset := (sj-b.Min.Y)*m.Stride - b.Min.X
		for i := 0; i < 8; i++ {
			sx := p.X + i
			if sx > xmax {
				sx = xmax
			}
			c := &lut[m.Pix[offset+sx]]
			yBlock[8*j+i] = int32(c[0])
			cbBlock[8*j+i] = int32(c[1])
			crBlock[8*j+i] = int32(c[2])
		}
	}
}

// inPalette reports whether every pixel of the strip is an index of its
// palette. (One that is not makes At panic, which the general path does.)
func inPalette(m *image.Paletted) bool {
	n := len(m.Palette)
	if n >= 256 {
		return true
	}
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for _, v := range m.Pix[m.PixOffset(b.Min.X, y):][:b.Dx()] {
			if int(v) >= n {
				return false
			}
		}
	}
	return true
}

// yCbCrToYCbCr is a specialized version of toYCbCr for image.YCbCr images.
//
// The offsets of the samples are worked out once for the 8 columns and the
// 8 rows: for every subsampling ratio YOffset and COffset are a part that
// depends on y alone plus one that depends on x alone, each 0 at the
// image's corner, so COffset(x, y) = COffset(x, Min.Y) + COffset(Min.X, y).
func yCbCrToYCbCr(m *image.YCbCr, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	var yCol, cCol [8]int
	for i := range yCol {
		sx := min(p.X+i, b.Max.X-1)
		yCol[i], cCol[i] = sx-b.Min.X, m.COffset(sx, b.Min.Y)
	}
	for j := 0; j < 8; j++ {
		sy := min(p.Y+j, b.Max.Y-1)
		yRow, cRow := m.Y[(sy-b.Min.Y)*m.YStride:], m.COffset(b.Min.X, sy)
		cb, cr := m.Cb[cRow:], m.Cr[cRow:]
		for i := 0; i < 8; i++ {
			yBlock[8*j+i] = int32(yRow[yCol[i]])
			cbBlock[8*j+i] = int32(cb[cCol[i]])
			crBlock[8*j+i] = int32(cr[cCol[i]])
		}
	}
}

// chroma444 stores in dst the chroma block (Cb or Cr, plane) of the 16x16
// MCU of a 4:4:4 image m whose top-left corner is p, which must lie inside
// m: the rounded averages of 2x2 samples, what yCbCrToYCbCr and scale make
// of it.
func chroma444(m *image.YCbCr, plane []byte, p image.Point, dst *block) {
	b := m.Bounds()
	off := (p.Y-b.Min.Y)*m.CStride + p.X - b.Min.X
	for j := 0; j < 8; j++ {
		r0 := plane[off : off+16 : off+16]
		r1 := plane[off+m.CStride : off+m.CStride+16 : off+m.CStride+16]
		d := dst[8*j : 8*j+8 : 8*j+8]
		for i := range d {
			d[i] = (int32(r0[2*i]) + int32(r0[2*i+1]) + int32(r1[2*i]) + int32(r1[2*i+1]) + 1 + int32(i&1)) >> 2
		}
		off += 2 * m.CStride
	}
}

// yOfYCbCr stores the luma of the 8x8 region of m whose top-left corner is
// p in yBlock, as yCbCrToYCbCr does.
func yOfYCbCr(m *image.YCbCr, p image.Point, yBlock *block) {
	b := m.Bounds()
	if p.X+8 <= b.Max.X && p.Y+8 <= b.Max.Y { // nothing to clamp
		off := (p.Y-b.Min.Y)*m.YStride + p.X - b.Min.X
		for j := 0; j < 8; j++ {
			row := m.Y[off : off+8 : off+8]
			d := yBlock[8*j : 8*j+8 : 8*j+8]
			d[0], d[1], d[2], d[3] = int32(row[0]), int32(row[1]), int32(row[2]), int32(row[3])
			d[4], d[5], d[6], d[7] = int32(row[4]), int32(row[5]), int32(row[6]), int32(row[7])
			off += m.YStride
		}
		return
	}
	var yCol [8]int
	for i := range yCol {
		yCol[i] = min(p.X+i, b.Max.X-1) - b.Min.X
	}
	for j := 0; j < 8; j++ {
		yRow := m.Y[(min(p.Y+j, b.Max.Y-1)-b.Min.Y)*m.YStride:]
		for i := 0; i < 8; i++ {
			yBlock[8*j+i] = int32(yRow[yCol[i]])
		}
	}
}

// chroma420 stores in dst the chroma block (Cb or Cr, plane) of the 16x16
// MCU of a 4:2:0 image m whose top-left corner is p, what yCbCrToYCbCr and
// scale make of it: when m's corner is even and not negative, the four
// pixels of every 2x2 cell of the MCU have the same chroma sample (those
// past the right or bottom edge, clamped to it, too: the last column or
// row is even then, or shares its sample with the one before), and the
// rounded average of four equal values is that value.
func chroma420(m *image.YCbCr, plane []byte, p image.Point, dst *block) {
	b := m.Bounds()
	if p.X+16 <= b.Max.X && p.Y+16 <= b.Max.Y { // nothing to clamp
		off := (p.Y/2-b.Min.Y/2)*m.CStride + p.X/2 - b.Min.X/2
		for j := 0; j < 8; j++ {
			row := plane[off : off+8 : off+8]
			d := dst[8*j : 8*j+8 : 8*j+8]
			d[0], d[1], d[2], d[3] = int32(row[0]), int32(row[1]), int32(row[2]), int32(row[3])
			d[4], d[5], d[6], d[7] = int32(row[4]), int32(row[5]), int32(row[6]), int32(row[7])
			off += m.CStride
		}
		return
	}
	var cCol [8]int
	for i := range cCol {
		cCol[i] = min(p.X/2+i, (b.Max.X-1)/2) - b.Min.X/2
	}
	for j := 0; j < 8; j++ {
		row := plane[(min(p.Y/2+j, (b.Max.Y-1)/2)-b.Min.Y/2)*m.CStride:]
		for i := 0; i < 8; i++ {
			dst[8*j+i] = int32(row[cCol[i]])
		}
	}
}

// scale scales the 16x16 region represented by the 4 src blocks to the 8x8
// dst block, averaging squares of 4, the rounding bias alternating 1 and 2
// across as libjpeg's h2v2 downsampling does.
func scale(dst *block, src *[4]block) {
	if useSIMD {
		scaleSIMD(dst, src)
		return
	}
	for i := 0; i < 4; i++ {
		dstOff := (i&2)<<4 | (i&1)<<2
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				j := 16*y + 2*x
				sum := src[i][j] + src[i][j+1] + src[i][j+8] + src[i][j+9]
				dst[8*y+x+dstOff] = (sum + 1 + int32(x&1)) >> 2
			}
		}
	}
}

// sosHeaderY is the SOS marker "\xff\xda" followed by 8 bytes:
//   - the marker length "\x00\x08",
//   - the number of components "\x01",
//   - component 1 uses DC table 0 and AC table 0 "\x01\x00",
//   - the bytes "\x00\x3f\x00". Section B.2.3 of the spec says that for
//     sequential DCTs, those bytes (8-bit Ss, 8-bit Se, 4-bit Ah, 4-bit Al)
//     should be 0x00, 0x3f, 0x00<<4 | 0x00.
var sosHeaderY = []byte{
	0xff, 0xda, 0x00, 0x08, 0x01, 0x01, 0x00, 0x00, 0x3f, 0x00,
}

// sosHeaderYCbCr is the SOS marker "\xff\xda" followed by 12 bytes:
//   - the marker length "\x00\x0c",
//   - the number of components "\x03",
//   - component 1 uses DC table 0 and AC table 0 "\x01\x00",
//   - component 2 uses DC table 1 and AC table 1 "\x02\x11",
//   - component 3 uses DC table 1 and AC table 1 "\x03\x11",
//   - the bytes "\x00\x3f\x00". Section B.2.3 of the spec says that for
//     sequential DCTs, those bytes (8-bit Ss, 8-bit Se, 4-bit Ah, 4-bit Al)
//     should be 0x00, 0x3f, 0x00<<4 | 0x00.
var sosHeaderYCbCr = []byte{
	0xff, 0xda, 0x00, 0x0c, 0x03, 0x01, 0x00, 0x02,
	0x11, 0x03, 0x11, 0x00, 0x3f, 0x00,
}

// writeSOS writes the StartOfScan marker.
// It is the standard library's writeSOS with the image arriving as bands of
// 16 rows (the last may be shorter) instead of one image. The block
// converters clamp to the band's bounds exactly as they clamp to the whole
// image's, since only the last band is short.
func (e *encoder) writeSOS(src pipeline.Source, first image.Image) error {
	switch first.(type) {
	case *image.Gray:
		e.write(sosHeaderY)
	default:
		e.write(sosHeaderYCbCr)
	}
	// DC components are delta-encoded.
	var prevDC [3]int32
	err := e.quantBlocks(src, first, func(zz *qblock, comp int) {
		prevDC[comp] = e.writeQuantized(zz, quantIndex((comp+1)/2), prevDC[comp])
	})
	if err != nil {
		return err
	}
	// Pad the last byte with 1's.
	e.padBits()
	return nil
}

// scanBlocks reads the image band by band and hands every 8x8 block of
// samples to sink in the order of the baseline scan: for a gray image the
// blocks row by row (comp 0), for a colour image per MCU its Y blocks (comp
// 0; four of a 16x16 MCU, two of a 16x8 one, one of an 8x8 one), then Cb
// (comp 1) and Cr (comp 2). The blocks are in natural (not zig-zag) order
// and may be changed by sink.
func (e *encoder) scanBlocks(src pipeline.Source, first image.Image, sink func(b *block, comp int)) error {
	var t unitTemp
	return e.bands(src, first, func(c *bandConv) error {
		for u := 0; u < c.units; u++ {
			c.unit(u, &t, sink)
		}
		return nil
	})
}

// quantBlocks hands the quantized blocks of the image to sink in the order
// of scanBlocks. With helpers in the pipeline (see pipeline.Env.Parallel)
// the blocks of a band are converted, transformed and quantized at once on
// the cores that are free, into a buffer of the band, and then handed on
// in order; the result is the same to the byte. Without, block by block.
func (e *encoder) quantBlocks(src pipeline.Source, first image.Image, sink func(zz *qblock, comp int)) error {
	env := pipeline.EnvOf(src)
	ways := env.Helpers() + 1
	if ways == 1 && !e.trellis {
		var zz qblock
		return e.scanBlocks(src, first, func(b *block, comp int) {
			if b == nil {
				// A dummy block: the DC of the block before, which zz
				// still holds.
				clear(zz[1:])
			} else {
				e.quantize(b, quantIndex((comp+1)/2), &zz)
			}
			sink(&zz, comp)
		})
	}
	var (
		buf      []qblock
		comps    []int8
		dcs      []dcChoice
		reserved int64
		tt       [nQuantIndex]*trellisTables
		prevDC   [3]int32
		order    []*qblock
		dcList   []dcChoice
		scratch  dcScratch
	)
	if e.trellis {
		for q := range tt {
			tt[q] = e.trellisTables(quantIndex(q))
		}
	}
	defer func() { env.Budget.Release(reserved) }()
	return e.bands(src, first, func(c *bandConv) error {
		n := c.units * c.perUnit
		if n > len(buf) {
			need := int64(n) * (int64(len(qblock{}))*2 + 1 + 16*3)
			if err := env.Budget.Reserve(need-reserved, "jpeg band coefficients"); err != nil {
				return err
			}
			reserved = need
			buf, comps, dcs = make([]qblock, n), make([]int8, n), make([]dcChoice, n)
		}
		parts := min(ways, max(1, c.units/16))
		env.Parallel(parts, func(p int) {
			var t unitTemp
			lo, hi := p*c.units/parts, (p+1)*c.units/parts
			for u := lo; u < hi; u++ {
				k := u * c.perUnit
				c.unit(u, &t, func(b *block, comp int) {
					q := quantIndex((comp + 1) / 2)
					switch {
					case b == nil:
						// A dummy block: the DC of the block before, in
						// the same unit.
						buf[k] = qblock{0: buf[k-1][0]}
					case e.trellis:
						fdct(b)
						dcs[k] = tt[q].trellisBlock(b, &buf[k])
					default:
						e.quantize(b, q, &buf[k])
					}
					comps[k] = int8(comp)
					k++
				})
			}
		})
		if e.trellis {
			// The DC values, one component after the other, in coding order.
			for comp := 0; comp < 3; comp++ {
				order, dcList = order[:0], dcList[:0]
				for k := 0; k < n; k++ {
					if int(comps[k]) == comp {
						order, dcList = append(order, &buf[k]), append(dcList, dcs[k])
					}
				}
				prevDC[comp] = tt[(comp+1)/2].trellisDC(dcList, order, prevDC[comp], &scratch)
			}
		}
		for k := 0; k < n; k++ {
			sink(&buf[k], int(comps[k]))
		}
		return nil
	})
}

// unitTemp holds the samples of one unit (a block of a gray image, an MCU
// of a colour one) while it is converted.
type unitTemp struct {
	b      block
	cb, cr [4]block
}

// bandConv cuts one band of the image into units: blocks of a gray image,
// row by row, or 16x16 MCUs of a colour one.
type bandConv struct {
	m      image.Image
	bounds image.Rectangle
	gray   *image.Gray
	rgba   *image.RGBA
	ycbcr  *image.YCbCr
	nrgba  *image.NRGBA
	pal    *image.Paletted
	lut    *[256][3]uint8 // the palette of pal
	// direct420: ycbcr is 4:2:0 with its corner even and not negative, so
	// its chroma samples are the MCUs' chroma blocks (see chroma420).
	direct420 bool
	// direct444: ycbcr is 4:4:4; the chroma blocks of an MCU with nothing
	// to clamp are averaged from it directly (see chroma444).
	direct444 bool
	// dummies: a Y block of an MCU that lies past the right or the bottom
	// edge of the image is handed on as nil, for libjpeg's dummy block (no
	// AC coefficients, the DC of the block before).
	dummies bool
	across  int // blocks (gray) or MCUs (colour) across
	hY, vY  int // the sampling factors of Y (colour)
	units   int
	perUnit int // blocks a unit has
}

// unit hands the blocks of unit u to sink, as scanBlocks does.
func (c *bandConv) unit(u int, t *unitTemp, sink func(b *block, comp int)) {
	if c.gray != nil {
		p := image.Pt(c.bounds.Min.X+8*(u%c.across), c.bounds.Min.Y+8*(u/c.across))
		grayToY(c.gray, p, &t.b)
		sink(&t.b, 0)
		return
	}
	x, y := c.bounds.Min.X+8*c.hY*(u%c.across), c.bounds.Min.Y+8*c.vY*(u/c.across)
	switch {
	case c.hY == 1:
		// 4:4:4: one block, its chroma as it is.
		c.convert(image.Pt(x, y), &t.b, &t.cb[0], &t.cr[0])
		sink(&t.b, 0)
		sink(&t.cb[0], 1)
		sink(&t.cr[0], 2)
		return
	case c.vY == 1:
		// 4:2:2: two blocks across, the chroma averaged in pairs.
		for i := 0; i < 2; i++ {
			c.convert(image.Pt(x+8*i, y), &t.b, &t.cb[i], &t.cr[i])
			sink(c.yBlock(&t.b, x+8*i, y), 0)
		}
		scaleH(&t.b, &t.cb)
		sink(&t.b, 1)
		scaleH(&t.b, &t.cr)
		sink(&t.b, 2)
		return
	}
	if c.direct444 && x+16 <= c.bounds.Max.X && y+16 <= c.bounds.Max.Y {
		for i := 0; i < 4; i++ {
			yOfYCbCr(c.ycbcr, image.Pt(x+(i&1)*8, y+(i&2)*4), &t.b)
			sink(&t.b, 0)
		}
		chroma444(c.ycbcr, c.ycbcr.Cb, image.Pt(x, y), &t.b)
		sink(&t.b, 1)
		chroma444(c.ycbcr, c.ycbcr.Cr, image.Pt(x, y), &t.b)
		sink(&t.b, 2)
		return
	}
	if c.direct420 {
		for i := 0; i < 4; i++ {
			yOfYCbCr(c.ycbcr, image.Pt(x+(i&1)*8, y+(i&2)*4), &t.b)
			sink(c.yBlock(&t.b, x+(i&1)*8, y+(i&2)*4), 0)
		}
		chroma420(c.ycbcr, c.ycbcr.Cb, image.Pt(x, y), &t.b)
		sink(&t.b, 1)
		chroma420(c.ycbcr, c.ycbcr.Cr, image.Pt(x, y), &t.b)
		sink(&t.b, 2)
		return
	}
	for i := 0; i < 4; i++ {
		xOff := (i & 1) * 8
		yOff := (i & 2) * 4
		c.convert(image.Pt(x+xOff, y+yOff), &t.b, &t.cb[i], &t.cr[i])
		sink(c.yBlock(&t.b, x+xOff, y+yOff), 0)
	}
	// Below the image, libjpeg repeats the last row of chroma it made
	// (jcprepct.c), not the last row of pixels.
	rows := c.bounds.Max.Y - y
	scale(&t.b, &t.cb)
	padRows(&t.b, (rows+1)/2)
	sink(&t.b, 1)
	scale(&t.b, &t.cr)
	padRows(&t.b, (rows+1)/2)
	sink(&t.b, 2)
}

// padRows repeats row n-1 of b in the rows below it, if n < 8.
func padRows(b *block, n int) {
	for r := n; r < 8 && n > 0; r++ {
		copy(b[8*r:8*r+8], b[8*(n-1):8*n])
	}
}

// yBlock is b, the Y block whose top-left corner is (x, y), or nil when
// it lies past the right or the bottom edge of the image and is a dummy
// block (see dummies).
func (c *bandConv) yBlock(b *block, x, y int) *block {
	if c.dummies && (x >= c.bounds.Max.X || y >= c.bounds.Max.Y) {
		return nil
	}
	return b
}

// convert makes the Y, Cb and Cr blocks of the 8x8 region of the band
// whose top-left corner is p.
func (c *bandConv) convert(p image.Point, y, cb, cr *block) {
	switch {
	case c.rgba != nil:
		rgbaToYCbCr(c.rgba, p, y, cb, cr)
	case c.ycbcr != nil:
		yCbCrToYCbCr(c.ycbcr, p, y, cb, cr)
	case c.nrgba != nil:
		nrgbaToYCbCr(c.nrgba, p, y, cb, cr)
	case c.pal != nil:
		palettedToYCbCr(c.pal, c.lut, p, y, cb, cr)
	default:
		toYCbCr(c.m, p, y, cb, cr)
	}
}

// scaleH scales the 16x8 region of the blocks src[0] and src[1] (left and
// right) to the 8x8 dst block, averaging pairs across, the rounding bias
// alternating as libjpeg's h2v1 downsampling does.
func scaleH(dst *block, src *[4]block) {
	for i := 0; i < 2; i++ {
		for y := 0; y < 8; y++ {
			s := src[i][8*y : 8*y+8 : 8*y+8]
			d := dst[8*y+4*i : 8*y+4*i+4 : 8*y+4*i+4]
			for x := range d {
				d[x] = (s[2*x] + s[2*x+1] + int32(x&1)) >> 1
			}
		}
	}
}

// bands reads the image band by band and hands each, cut into units, to do.
func (e *encoder) bands(src pipeline.Source, first image.Image, do func(c *bandConv) error) error {
	var lut [256][3]uint8 // the palette of a paletted strip
	_, gray := first.(*image.Gray)
	// NRGBA strips of an image without transparency are RGBA as they are.
	opaque := pipeline.IsOpaque(src)
	for m := first; ; {
		bounds := m.Bounds()
		c := &bandConv{m: m, bounds: bounds, lut: &lut}
		switch m := m.(type) {
		case *image.Gray:
			if !gray {
				return errors.New("goimgtools: jpeg source changed pixel type")
			}
			c.gray = m
			c.across = (bounds.Dx() + 7) / 8
			c.units, c.perUnit = c.across*((bounds.Dy()+7)/8), 1
		default:
			if gray {
				return errors.New("goimgtools: jpeg source changed pixel type")
			}
			c.rgba, _ = m.(*image.RGBA)
			c.ycbcr, _ = m.(*image.YCbCr)
			c.direct420 = c.ycbcr != nil && c.ycbcr.SubsampleRatio == image.YCbCrSubsampleRatio420 &&
				bounds.Min.X >= 0 && bounds.Min.Y >= 0 && bounds.Min.X%2 == 0 && bounds.Min.Y%2 == 0
			c.direct444 = c.ycbcr != nil && c.ycbcr.SubsampleRatio == image.YCbCrSubsampleRatio444
			c.nrgba, _ = m.(*image.NRGBA)
			if c.nrgba != nil && opaque {
				c.rgba, c.nrgba = &image.RGBA{Pix: c.nrgba.Pix, Stride: c.nrgba.Stride, Rect: c.nrgba.Rect}, nil
			}
			c.pal, _ = m.(*image.Paletted)
			if c.pal != nil {
				if len(c.pal.Palette) > 256 || !inPalette(c.pal) {
					c.pal = nil // let the general path deal with it
				} else {
					paletteYCbCr(c.pal.Palette, &lut)
				}
			}
			c.hY, c.vY = e.hY, e.vY
			c.dummies = !e.trellis
			c.across = (bounds.Dx() + 8*c.hY - 1) / (8 * c.hY)
			c.units = c.across * ((bounds.Dy() + 8*c.vY - 1) / (8 * c.vY))
			c.perUnit = c.hY*c.vY + 2
		}
		if err := do(c); err != nil {
			return err
		}
		if e.err != nil {
			return e.err
		}
		next, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		m = next
	}
	return nil
}

// DefaultQuality is the default quality encoding parameter.
const DefaultQuality = 75

// Options are the encoding parameters.
// Quality ranges from 1 to 100 inclusive, higher is better.
type Options struct {
	Quality int
	// Exif (in TIFF form), ICC and Xmp are written into the file when set.
	// They are the only bytes jpeg.Encode would not write.
	Exif []byte
	ICC  []byte
	Xmp  []byte
	// Optimize computes Huffman tables for this image instead of using the
	// standard ones. The pixels a decoder gets are the same; the file is
	// smaller. The entropy-coded data is staged (see writer_opt.go).
	Optimize bool
	// Progressive writes a progressive JPEG (spectral selection and
	// successive approximation, the scan script searched for as mozjpeg
	// does; see writer_progsearch.go) with optimized tables. The pixels a decoder gets are again the same. The
	// quantized coefficients are staged (see writer_prog.go).
	Progressive bool
	// Trellis chooses the quantized coefficients to trade fidelity for
	// bits where that pays, as mozjpeg does (see writer_trellis.go): a
	// smaller file at about the same look, with other pixels than plain
	// rounding.
	Trellis bool
	// Subsampling is how the colour (Cb and Cr) is sampled: Subsample420
	// (the default, half the width and height, as the standard library),
	// Subsample422 (half the width) or Subsample444 (all of it).
	Subsampling int
	// Density, when above 0, is the resolution to record in a JFIF
	// segment, in pixels per inch (rounded, 1 to 65535). Without it there
	// is no JFIF segment, as jpeg.Encode writes none.
	Density float64
	// QuantTable picks the quantization tables, 0 to NQuantTables-1, as
	// mozjpeg's -quant-table does (see qtables.go), or QuantTableJpegli
	// (qtables_jpegli.go). 0, the tables of the JPEG standard, is what the
	// standard library uses.
	QuantTable int
}

// The colour sampling of Options.Subsampling.
const (
	Subsample420 = iota
	Subsample422
	Subsample444
)

// Encode writes src to w in JPEG 4:2:0 baseline format, producing the same
// bytes as the standard library's jpeg.Encode for the same pixels unless
// Options.Optimize or Options.Progressive ask for a smaller file. It does
// not close src. If it returns an error, what was written to w is not a
// valid image and must be discarded.
func Encode(w io.Writer, src pipeline.Source, o *Options) error {
	b := src.Bounds()
	if b.Dx() >= 1<<16 || b.Dy() >= 1<<16 {
		return fmt.Errorf("goimgtools: a JPEG holds at most 65535 pixels a side, the image is %dx%d", b.Dx(), b.Dy())
	}
	bands, err := pipeline.Rechunk(src, 16)
	if err != nil {
		return err
	}
	// Encode does not close src, but its own band must not stay reserved.
	if r, ok := bands.(interface{ Release() }); ok {
		defer r.Release()
	}
	// The pixel type (gray or colour) is only known from the first band.
	first, err := bands.Next()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	var e encoder
	e.trellis = o != nil && o.Trellis
	e.hY, e.vY = 2, 2
	if o != nil {
		switch o.Subsampling {
		case Subsample420:
		case Subsample422:
			e.vY = 1
		case Subsample444:
			e.hY, e.vY = 1, 1
		default:
			return fmt.Errorf("goimgtools: unknown JPEG chroma subsampling %d", o.Subsampling)
		}
	}
	if ww, ok := w.(writer); ok {
		e.w = ww
	} else {
		e.w = bufio.NewWriter(w)
	}
	// Clip quality to [1, 100].
	quality := DefaultQuality
	if o != nil {
		quality = o.Quality
		if quality < 1 {
			quality = 1
		} else if quality > 100 {
			quality = 100
		}
	}
	table := 0
	if o != nil {
		table = o.QuantTable
	}
	if table < 0 || table > QuantTableJpegli {
		return fmt.Errorf("goimgtools: JPEG quantization table must be 0 to %d, got %d", QuantTableJpegli, table)
	}
	// Compute number of components based on input image type.
	nComponent := 3
	switch first.(type) {
	case *image.Gray:
		nComponent = 1
	}
	// Convert from a quality rating to a scaling factor.
	var scale int
	if quality < 50 {
		scale = 5000 / quality
	} else {
		scale = 200 - quality*2
	}
	// Initialize the quantization tables.
	for i := range e.quant {
		for j := range e.quant[i] {
			var x int
			if table == QuantTableJpegli {
				x = jpegliQuant(quality, quantIndex(i), unzig[j], nComponent == 3 && e.hY == 2 && e.vY == 2)
			} else {
				x = int(baseQuant[table][i][unzig[j]])
				x = (x*scale + 50) / 100
				if x < 1 {
					x = 1
				} else if x > 255 {
					x = 255
				}
			}
			e.quant[i][j] = uint8(x)
			e.recip[i][j] = recip(8 * int32(x))
			e.natHalf[i][unzig[j]] = 4 * int32(x)
			e.natRecip[i][unzig[j]] = uint32(e.recip[i][j])
		}
	}
	// The tables of jpegli come with its dead zone, unless the trellis
	// chooses the coefficients.
	if table == QuantTableJpegli && !e.trellis {
		e.deadZone = &jpegliDeadZone
	}
	// Write the Start Of Image marker.
	e.buf[0] = 0xff
	e.buf[1] = 0xd8
	e.write(e.buf[:2])
	if o != nil {
		if o.Density > 0 {
			// JFIF 1.01 (as libjpeg writes it), in dots per inch, no
			// thumbnail.
			d := uint16(max(1, min(math.Round(o.Density), 65535)))
			e.write([]byte{0xff, 0xe0, 0, 16, 'J', 'F', 'I', 'F', 0, 1, 1, 1, byte(d >> 8), byte(d), byte(d >> 8), byte(d), 0, 0})
		}
		e.writeMeta(o.Exif, o.ICC, o.Xmp)
	}
	// Write the quantization tables.
	e.writeDQT(nComponent)
	switch {
	case o != nil && o.Progressive:
		if err := e.writeProgressive(bands, first, b.Size(), nComponent); err != nil {
			return err
		}
	case o != nil && o.Optimize:
		e.writeSOF0(b.Size(), nComponent)
		if err := e.writeOptimized(bands, first, b.Size(), nComponent); err != nil {
			return err
		}
	default:
		// Write the image dimensions.
		e.writeSOF0(b.Size(), nComponent)
		// Write the Huffman tables.
		e.writeDHT(nComponent)
		// Write the image data.
		if err := e.writeSOS(bands, first); err != nil {
			return err
		}
	}
	// Write the End Of Image marker.
	e.buf[0] = 0xff
	e.buf[1] = 0xd9
	e.write(e.buf[:2])
	e.flush()
	return e.err
}

// writeMeta writes an APP1 segment with the EXIF block, APP2 segments with
// the ICC profile and an APP1 segment with the XMP packet. A block too
// large for its segments is left out.
func (e *encoder) writeMeta(exif, icc, xmp []byte) {
	seg := func(marker byte, head string, extra, data []byte) {
		n := 2 + len(head) + len(extra) + len(data)
		e.write([]byte{0xff, marker, byte(n >> 8), byte(n)})
		e.write([]byte(head))
		e.write(extra)
		e.write(data)
	}
	const exifHead, iccHead = "Exif\x00\x00", "ICC_PROFILE\x00"
	if len(exif) > 0 && len(exif) <= 0xffff-2-len(exifHead) {
		seg(0xe1, exifHead, nil, exif)
	}
	const part = 0xffff - 2 - len(iccHead) - 2
	if total := (len(icc) + part - 1) / part; total >= 1 && total <= 255 {
		for i := 0; i < total; i++ {
			seg(0xe2, iccHead, []byte{byte(i + 1), byte(total)}, icc[i*part:min((i+1)*part, len(icc))])
		}
	}
	const xmpHead = "http://ns.adobe.com/xap/1.0/\x00"
	if len(xmp) > 0 && len(xmp) <= 0xffff-2-len(xmpHead) {
		seg(0xe1, xmpHead, nil, xmp)
	}
}
