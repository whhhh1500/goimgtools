// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package jpegstream is Go's image/jpeg encoder (go1.27.1) changed to read
// the image from a pipeline.Source in 16-row bands. Only writeSOS and Encode
// differ from the standard library; the output bytes are the same.
package jpegstream

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"

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
func divBy(a, b int32, m uint64) int32 {
	if a >= 0 {
		return int32(uint64(a+(b>>1)) * m >> 32)
	}
	return -int32(uint64(-a+(b>>1)) * m >> 32)
}

// bitCount counts the number of bits needed to hold an integer.
var bitCount = [256]byte{
	0, 1, 2, 2, 3, 3, 3, 3, 4, 4, 4, 4, 4, 4, 4, 4,
	5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5, 5,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6, 6,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7, 7,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
	8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8, 8,
}

type quantIndex int

const (
	quantIndexLuminance quantIndex = iota
	quantIndexChrominance
	nQuantIndex
)

// unscaledQuant are the unscaled quantization tables in zig-zag order. Each
// encoder copies and scales the tables according to its quality parameter.
// The values are derived from section K.1 of the spec, after converting from
// natural to zig-zag order.
var unscaledQuant = [nQuantIndex][blockSize]byte{
	// Luminance.
	{
		16, 11, 12, 14, 12, 10, 16, 14,
		13, 14, 18, 17, 16, 19, 24, 40,
		26, 24, 22, 22, 24, 49, 35, 37,
		29, 40, 58, 51, 61, 60, 57, 51,
		56, 55, 64, 72, 92, 78, 64, 68,
		87, 69, 55, 56, 80, 109, 81, 87,
		95, 98, 103, 104, 103, 62, 77, 113,
		121, 112, 100, 120, 92, 101, 103, 99,
	},
	// Chrominance.
	{
		17, 18, 18, 24, 21, 24, 47, 26,
		26, 47, 99, 66, 56, 66, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
		99, 99, 99, 99, 99, 99, 99, 99,
	},
}

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
	// bits and nBits are accumulated bits to write to w.
	bits, nBits uint32
	// quant is the scaled quantization tables, in zig-zag order.
	quant [nQuantIndex][blockSize]byte
	// recip is recip(8 * quant), for dividing by a quantization value
	// without a division.
	recip [nQuantIndex][blockSize]uint64
	// freq, when set, counts the symbols emitHuff writes (Options.Optimize).
	freq *[nHuffIndex][257]int64
	// trellis is set for Options.Trellis.
	trellis bool
}

func (e *encoder) flush() {
	if e.err != nil {
		return
	}
	e.err = e.w.Flush()
}

func (e *encoder) write(p []byte) {
	if e.err != nil {
		return
	}
	_, e.err = e.w.Write(p)
}

func (e *encoder) writeByte(b byte) {
	if e.err != nil {
		return
	}
	e.err = e.w.WriteByte(b)
}

// emit emits the least significant nBits bits of bits to the bit-stream.
// The precondition is bits < 1<<nBits && nBits <= 16.
func (e *encoder) emit(bits, nBits uint32) {
	nBits += e.nBits
	bits <<= 32 - nBits
	bits |= e.bits
	for nBits >= 8 {
		b := uint8(bits >> 24)
		e.writeByte(b)
		if b == 0xff {
			e.writeByte(0x00)
		}
		bits <<= 8
		nBits -= 8
	}
	e.bits, e.nBits = bits, nBits
}

// emitHuff emits the given value with the given Huffman encoder.
func (e *encoder) emitHuff(h huffIndex, value int32) {
	if e.freq != nil {
		e.freq[h][value]++
	}
	x := theHuffmanLUT[h][value]
	e.emit(x&(1<<24-1), x>>24)
}

// emitHuffRLE emits a run of runLength copies of value encoded with the given
// Huffman encoder.
func (e *encoder) emitHuffRLE(h huffIndex, runLength, value int32) {
	a, b := value, value
	if a < 0 {
		a, b = -value, value-1
	}
	var nBits uint32
	if a < 0x100 {
		nBits = uint32(bitCount[a])
	} else {
		nBits = 8 + uint32(bitCount[a>>8])
	}
	e.emitHuff(h, runLength<<4|int32(nBits))
	if nBits > 0 {
		e.emit(uint32(b)&(1<<nBits-1), nBits)
	}
}

// writeMarkerHeader writes the header for a marker with the given length.
func (e *encoder) writeMarkerHeader(marker uint8, markerlen int) {
	e.buf[0] = 0xff
	e.buf[1] = marker
	e.buf[2] = uint8(markerlen >> 8)
	e.buf[3] = uint8(markerlen & 0xff)
	e.write(e.buf[:4])
}

// writeDQT writes the Define Quantization Table marker.
func (e *encoder) writeDQT() {
	const markerlen = 2 + int(nQuantIndex)*(1+blockSize)
	e.writeMarkerHeader(dqtMarker, markerlen)
	for i := range e.quant {
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
			// We use 4:2:0 chroma subsampling.
			e.buf[3*i+7] = "\x22\x11\x11"[i]
			e.buf[3*i+8] = "\x00\x01\x01"[i]
		}
	}
	e.write(e.buf[:3*(nComponent-1)+9])
}

// writeDHT writes the Define Huffman Table marker.
func (e *encoder) writeDHT(nComponent int) {
	markerlen := 2
	specs := theHuffmanSpec[:]
	if nComponent == 1 {
		// Drop the Chrominance tables.
		specs = specs[:2]
	}
	for _, s := range specs {
		markerlen += 1 + 16 + len(s.value)
	}
	e.writeMarkerHeader(dhtMarker, markerlen)
	for i, s := range specs {
		e.writeByte("\x00\x10\x01\x11"[i])
		e.write(s.count[:])
		e.write(s.value)
	}
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

// quantize transforms b (in place) and quantizes it with table q into zz.
func (e *encoder) quantize(b *block, q quantIndex, zz *qblock) {
	fdct(b)
	quant, rec := &e.quant[q], &e.recip[q]
	for zig := 0; zig < blockSize; zig++ {
		zz[zig] = int16(divBy(b[unzig[zig]], 8*int32(quant[zig]), rec[zig]))
	}
}

// writeQuantized emits a quantized block, its DC as the difference to
// prevDC, and returns its DC.
func (e *encoder) writeQuantized(zz *qblock, q quantIndex, prevDC int32) int32 {
	// Emit the DC delta.
	dc := int32(zz[0])
	e.emitHuffRLE(huffIndex(2*q+0), 0, dc-prevDC)
	// Emit the AC components.
	h, runLength := huffIndex(2*q+1), int32(0)
	for zig := 1; zig < blockSize; zig++ {
		ac := int32(zz[zig])
		if ac == 0 {
			runLength++
		} else {
			for runLength > 15 {
				e.emitHuff(h, 0xf0)
				runLength -= 16
			}
			e.emitHuffRLE(h, runLength, ac)
			runLength = 0
		}
	}
	if runLength > 0 {
		e.emitHuff(h, 0x00)
	}
	return dc
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
			yy, cb, cr := color.RGBToYCbCr(uint8(r>>8), uint8(g>>8), uint8(b>>8))
			yBlock[8*j+i] = int32(yy)
			cbBlock[8*j+i] = int32(cb)
			crBlock[8*j+i] = int32(cr)
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

// rgbaToYCbCr is a specialized version of toYCbCr for image.RGBA images.
func rgbaToYCbCr(m *image.RGBA, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	xmax := b.Max.X - 1
	ymax := b.Max.Y - 1
	for j := 0; j < 8; j++ {
		sj := p.Y + j
		if sj > ymax {
			sj = ymax
		}
		offset := (sj-b.Min.Y)*m.Stride - b.Min.X*4
		for i := 0; i < 8; i++ {
			sx := p.X + i
			if sx > xmax {
				sx = xmax
			}
			pix := m.Pix[offset+sx*4:]
			yy, cb, cr := color.RGBToYCbCr(pix[0], pix[1], pix[2])
			yBlock[8*j+i] = int32(yy)
			cbBlock[8*j+i] = int32(cb)
			crBlock[8*j+i] = int32(cr)
		}
	}
}

// nrgbaToYCbCr is a specialized version of toYCbCr for image.NRGBA images:
// the values At().RGBA() gives (colour multiplied by alpha, as
// color.NRGBA.RGBA computes it), without making a color for every pixel.
func nrgbaToYCbCr(m *image.NRGBA, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	xmax := b.Max.X - 1
	ymax := b.Max.Y - 1
	for j := 0; j < 8; j++ {
		sj := p.Y + j
		if sj > ymax {
			sj = ymax
		}
		offset := (sj-b.Min.Y)*m.Stride - b.Min.X*4
		for i := 0; i < 8; i++ {
			sx := p.X + i
			if sx > xmax {
				sx = xmax
			}
			pix := m.Pix[offset+sx*4 : offset+sx*4+4 : offset+sx*4+4]
			r, g, bl := pix[0], pix[1], pix[2]
			if a := uint32(pix[3]); a != 0xff {
				r = uint8(uint32(r) * 0x101 * a / 0xff >> 8)
				g = uint8(uint32(g) * 0x101 * a / 0xff >> 8)
				bl = uint8(uint32(bl) * 0x101 * a / 0xff >> 8)
			}
			yy, cb, cr := color.RGBToYCbCr(r, g, bl)
			yBlock[8*j+i] = int32(yy)
			cbBlock[8*j+i] = int32(cb)
			crBlock[8*j+i] = int32(cr)
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
		lut[i][0], lut[i][1], lut[i][2] = color.RGBToYCbCr(uint8(r>>8), uint8(g>>8), uint8(b>>8))
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
func yCbCrToYCbCr(m *image.YCbCr, p image.Point, yBlock, cbBlock, crBlock *block) {
	b := m.Bounds()
	xmax := b.Max.X - 1
	ymax := b.Max.Y - 1
	for j := 0; j < 8; j++ {
		sy := p.Y + j
		if sy > ymax {
			sy = ymax
		}
		for i := 0; i < 8; i++ {
			sx := p.X + i
			if sx > xmax {
				sx = xmax
			}
			yi := m.YOffset(sx, sy)
			ci := m.COffset(sx, sy)
			yBlock[8*j+i] = int32(m.Y[yi])
			cbBlock[8*j+i] = int32(m.Cb[ci])
			crBlock[8*j+i] = int32(m.Cr[ci])
		}
	}
}

// scale scales the 16x16 region represented by the 4 src blocks to the 8x8
// dst block.
func scale(dst *block, src *[4]block) {
	for i := 0; i < 4; i++ {
		dstOff := (i&2)<<4 | (i&1)<<2
		for y := 0; y < 4; y++ {
			for x := 0; x < 4; x++ {
				j := 16*y + 2*x
				sum := src[i][j] + src[i][j+1] + src[i][j+8] + src[i][j+9]
				dst[8*y+x+dstOff] = (sum + 2) >> 2
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
	e.emit(0x7f, 7)
	return nil
}

// scanBlocks reads the image band by band and hands every 8x8 block of
// samples to sink in the order of the baseline scan: for a gray image the
// blocks row by row (comp 0), for a colour image per 16x16 MCU four Y blocks
// (comp 0), then Cb (comp 1) and Cr (comp 2). The blocks are in natural (not
// zig-zag) order and may be changed by sink.
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
			e.quantize(b, quantIndex((comp+1)/2), &zz)
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
					if e.trellis {
						fdct(b)
						dcs[k] = tt[q].trellisBlock(b, &buf[k])
					} else {
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
	m       image.Image
	bounds  image.Rectangle
	gray    *image.Gray
	rgba    *image.RGBA
	ycbcr   *image.YCbCr
	nrgba   *image.NRGBA
	pal     *image.Paletted
	lut     *[256][3]uint8 // the palette of pal
	across  int            // gray: blocks across
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
	x, y := c.bounds.Min.X+16*u, c.bounds.Min.Y
	for i := 0; i < 4; i++ {
		xOff := (i & 1) * 8
		yOff := (i & 2) * 4
		p := image.Pt(x+xOff, y+yOff)
		switch {
		case c.rgba != nil:
			rgbaToYCbCr(c.rgba, p, &t.b, &t.cb[i], &t.cr[i])
		case c.ycbcr != nil:
			yCbCrToYCbCr(c.ycbcr, p, &t.b, &t.cb[i], &t.cr[i])
		case c.nrgba != nil:
			nrgbaToYCbCr(c.nrgba, p, &t.b, &t.cb[i], &t.cr[i])
		case c.pal != nil:
			palettedToYCbCr(c.pal, c.lut, p, &t.b, &t.cb[i], &t.cr[i])
		default:
			toYCbCr(c.m, p, &t.b, &t.cb[i], &t.cr[i])
		}
		sink(&t.b, 0)
	}
	scale(&t.b, &t.cb)
	sink(&t.b, 1)
	scale(&t.b, &t.cr)
	sink(&t.b, 2)
}

// bands reads the image band by band and hands each, cut into units, to do.
func (e *encoder) bands(src pipeline.Source, first image.Image, do func(c *bandConv) error) error {
	var lut [256][3]uint8 // the palette of a paletted strip
	_, gray := first.(*image.Gray)
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
			c.nrgba, _ = m.(*image.NRGBA)
			c.pal, _ = m.(*image.Paletted)
			if c.pal != nil {
				if len(c.pal.Palette) > 256 || !inPalette(c.pal) {
					c.pal = nil // let the general path deal with it
				} else {
					paletteYCbCr(c.pal.Palette, &lut)
				}
			}
			c.units, c.perUnit = (bounds.Dx()+15)/16, 6
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
	// Exif (in TIFF form) and ICC are written into the file when set. They
	// are the only bytes jpeg.Encode would not write.
	Exif []byte
	ICC  []byte
	// Optimize computes Huffman tables for this image instead of using the
	// standard ones. The pixels a decoder gets are the same; the file is
	// smaller. The entropy-coded data is staged (see writer_opt.go).
	Optimize bool
	// Progressive writes a progressive JPEG (spectral selection and
	// successive approximation, libjpeg's default scan script) with
	// optimized tables. The pixels a decoder gets are again the same. The
	// quantized coefficients are staged (see writer_prog.go).
	Progressive bool
	// Trellis chooses the quantized coefficients to trade fidelity for
	// bits where that pays, as mozjpeg does (see writer_trellis.go): a
	// smaller file at about the same look, with other pixels than plain
	// rounding.
	Trellis bool
}

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
			x := int(unscaledQuant[i][j])
			x = (x*scale + 50) / 100
			if x < 1 {
				x = 1
			} else if x > 255 {
				x = 255
			}
			e.quant[i][j] = uint8(x)
			e.recip[i][j] = recip(8 * int32(x))
		}
	}
	// Compute number of components based on input image type.
	nComponent := 3
	switch first.(type) {
	case *image.Gray:
		nComponent = 1
	}
	// Write the Start Of Image marker.
	e.buf[0] = 0xff
	e.buf[1] = 0xd8
	e.write(e.buf[:2])
	if o != nil {
		e.writeMeta(o.Exif, o.ICC)
	}
	// Write the quantization tables.
	e.writeDQT()
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

// writeMeta writes an APP1 segment with the EXIF block and APP2 segments
// with the ICC profile. A block too large for its segments is left out.
func (e *encoder) writeMeta(exif, icc []byte) {
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
}
