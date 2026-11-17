// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package jpeg implements a JPEG image decoder and encoder.
//
// JPEG is defined in ITU-T T.81: https://www.w3.org/Graphics/JPEG/itu-t81.pdf.
package jpegstream

import (
	"github.com/whhhh1500/goimgtools/internal/density"
	"github.com/whhhh1500/goimgtools/pipeline"

	"image"
	"io"
)

// A FormatError reports that the input is not a valid JPEG.
type FormatError string

func (e FormatError) Error() string { return "invalid JPEG format: " + string(e) }

// An UnsupportedError reports that the input uses a valid but unimplemented JPEG feature.
type UnsupportedError string

func (e UnsupportedError) Error() string { return "unsupported JPEG feature: " + string(e) }

var errUnsupportedSubsamplingRatio = UnsupportedError("luma/chroma subsampling ratio")

// Component specification, specified in section B.2.2.
type component struct {
	h       int   // Horizontal sampling factor.
	v       int   // Vertical sampling factor.
	c       uint8 // Component identifier.
	tq      uint8 // Quantization table destination selector.
	expandH int   // Horizontal expansion factor for non-standard subsampling.
	expandV int   // Vertical expansion factor for non-standard subsampling.
}

const (
	dcTable = 0
	acTable = 1
	maxTc   = 1
	maxTh   = 3
	maxTq   = 3

	maxComponents = 4
)

const (
	sof0Marker = 0xc0 // Start Of Frame (Baseline Sequential).
	sof1Marker = 0xc1 // Start Of Frame (Extended Sequential).
	sof2Marker = 0xc2 // Start Of Frame (Progressive).
	dhtMarker  = 0xc4 // Define Huffman Table.
	rst0Marker = 0xd0 // ReSTart (0).
	rst7Marker = 0xd7 // ReSTart (7).
	soiMarker  = 0xd8 // Start Of Image.
	eoiMarker  = 0xd9 // End Of Image.
	sosMarker  = 0xda // Start Of Scan.
	dqtMarker  = 0xdb // Define Quantization Table.
	driMarker  = 0xdd // Define Restart Interval.
	comMarker  = 0xfe // COMment.
	// "APPlication specific" markers aren't part of the JPEG spec per se,
	// but in practice, their use is described at
	// https://www.sno.phy.queensu.ca/~phil/exiftool/TagNames/JPEG.html
	app0Marker  = 0xe0
	app1Marker  = 0xe1 // EXIF
	app2Marker  = 0xe2 // ICC profile
	app14Marker = 0xee
	app15Marker = 0xef
)

// See https://www.sno.phy.queensu.ca/~phil/exiftool/TagNames/JPEG.html#Adobe
const (
	adobeTransformUnknown = 0
	adobeTransformYCbCr   = 1
	adobeTransformYCbCrK  = 2
)

// unzig maps from the zig-zag ordering to the natural ordering. For example,
// unzig[3] is the column and row of the fourth element in zig-zag order. The
// value is 16, which means first column (16%8 == 0) and third row (16/8 == 2).
var unzig = [blockSize]int{
	0, 1, 8, 16, 9, 2, 3, 10,
	17, 24, 32, 25, 18, 11, 4, 5,
	12, 19, 26, 33, 40, 48, 41, 34,
	27, 20, 13, 6, 7, 14, 21, 28,
	35, 42, 49, 56, 57, 50, 43, 36,
	29, 22, 15, 23, 30, 37, 44, 51,
	58, 59, 52, 45, 38, 31, 39, 46,
	53, 60, 61, 54, 47, 55, 62, 63,
}

// Deprecated: Reader is not used by the [image/jpeg] package and should
// not be used by others. It is kept for compatibility.
type Reader interface {
	io.ByteReader
	io.Reader
}

// bits holds the unprocessed bits that have been taken from the byte-stream.
// The n most significant bits of a form the unread bits, to be read in MSB to
// LSB order; the bits below them are zero.
type bits struct {
	a   uint64 // accumulator.
	n   int32  // the number of unread bits in a.
	end error  // why fillBits last stopped short, or nil
}

// byteBuf is a byte buffer, similar to a bufio.Reader, except that fillBits
// can look back at the byte before the first unread one. Byte stuffing is
// specified in section F.1.2.3.
type byteBuf struct {
	// buf[i:j] are the buffered bytes read from the underlying io.Reader
	// that haven't yet been passed further on.
	buf  [4096]byte
	i, j int
}

type decoder struct {
	r    io.Reader
	bits bits
	// bytes is own, or a cursor's buffer while that scan is decoded
	// (multiscan.go).
	bytes         *byteBuf
	own           byteBuf
	consumed      int64 // bytes read from r
	width, height int

	img1        *image.Gray
	img3        *image.YCbCr
	blackPix    []byte
	blackStride int

	// For non-standard subsampling ratios (flex mode).
	flex       bool // True if using non-standard subsampling that requires manual pixel expansion.
	maxH, maxV int  // Maximum horizontal and vertical sampling factors across all components.

	ri    int // Restart Interval.
	nComp int

	// As per section 4.5, there are four modes of operation (selected by the
	// SOF? markers): sequential DCT, progressive DCT, lossless and
	// hierarchical, although this implementation does not support the latter
	// two non-DCT modes. Sequential DCT is further split into baseline and
	// extended, as per section 4.11.
	baseline    bool
	progressive bool

	jfif                bool
	jfifDensity         float64 // pixels per inch, 0 when not given
	adobeTransformValid bool
	adobeTransform      uint8
	eobRun              uint16 // End-of-Band run, specified in section G.1.2.2.

	comp       [maxComponents]component
	progCoeffs [maxComponents][]block         // Saved state between progressive-mode scans.
	huff       [maxTc + 1][maxTh + 1]*huffman // noTable until defined
	quant      [maxTq + 1]block               // Quantization tables, in zig-zag order.
	tmp        [2 * blockSize]byte

	// Streaming state (goimgtools).
	ready    bool                      // setup has run
	mxx, myy int                       // image size in MCUs
	ratio    image.YCbCrSubsampleRatio // of the output strips
	scan     scanState
	full     bool // coefficients of every scan are stored, pixels made at the end
	coef     *coefStore
	// Metadata found before the first scan (goimgtools addition).
	meta     pipeline.Meta
	iccParts map[int][]byte
	iccTotal int
	metaDone bool
	touched  [maxComponents]bool // components that appeared in a scan
	bandMy   int                 // first MCU row of the current band
	scale    uint                // the image is shrunk by 1<<scale (ShrinkOnLoad)
	// rowCoef holds the coefficients of one MCU row, by component, when
	// the scans of a multi-scan image are decoded in step (multiscan.go).
	rowCoef [maxComponents][]block
	rowNZ   [maxComponents][]uint64 // rowCoef's non-zero AC coefficients (refine)
	// overrun records a scan that ended inside an end-of-band run, which
	// only a corrupt image has; the standard library carries the rest of
	// the run into the next scan (tests compare the pixels only without).
	overrun bool
	tables  int // Huffman tables defined
	// cscale is the scale each component is decoded at (setScale).
	cscale [maxComponents]uint
	// dcOnly tells reconstructBlock that the block's AC coefficients are
	// all zero (decodeBlock, stepRows).
	dcOnly bool
}

// newDecoder returns a decoder that reads r.
func newDecoder(r io.Reader) *decoder {
	d := &decoder{r: r}
	d.bytes = &d.own
	for i := range d.huff {
		for j := range d.huff[i] {
			d.huff[i][j] = noTable
		}
	}
	return d
}

// offset is the position in r of the next unread byte.
func (d *decoder) offset() int64 { return d.consumed - int64(d.bytes.j-d.bytes.i) }

// fill fills up the d.bytes.buf buffer from the underlying io.Reader. It
// should only be called when there are no unread bytes in d.bytes.
func (d *decoder) fill() error {
	if d.bytes.i != d.bytes.j {
		panic("jpeg: fill called when unread bytes exist")
	}
	// Move the last 2 bytes to the start of the buffer: fillBits looks
	// back at a 0xff that ended the buffer.
	if d.bytes.j > 2 {
		d.bytes.buf[0] = d.bytes.buf[d.bytes.j-2]
		d.bytes.buf[1] = d.bytes.buf[d.bytes.j-1]
		d.bytes.i, d.bytes.j = 2, 2
	}
	// Fill in the rest of the buffer.
	n, err := d.r.Read(d.bytes.buf[d.bytes.j:])
	d.bytes.j += n
	d.consumed += int64(n)
	if n > 0 {
		return nil
	}
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// readByte returns the next byte, whether buffered or not buffered. It does
// not care about byte stuffing.
func (d *decoder) readByte() (x byte, err error) {
	for d.bytes.i == d.bytes.j {
		if err = d.fill(); err != nil {
			return 0, err
		}
	}
	x = d.bytes.buf[d.bytes.i]
	d.bytes.i++
	return x, nil
}

// errMissingFF00 means that fillBits encountered an 0xff byte (a
// marker byte) that wasn't the expected byte-stuffed sequence 0xff, 0x00.
var errMissingFF00 = FormatError("missing 0xff00 sequence")

// readFull reads exactly len(p) bytes into p. It does not care about byte
// stuffing.
func (d *decoder) readFull(p []byte) error {
	for {
		n := copy(p, d.bytes.buf[d.bytes.i:d.bytes.j])
		p = p[n:]
		d.bytes.i += n
		if len(p) == 0 {
			break
		}
		if err := d.fill(); err != nil {
			return err
		}
	}
	return nil
}

// ignore ignores the next n bytes.
func (d *decoder) ignore(n int) error {
	for {
		m := d.bytes.j - d.bytes.i
		if m > n {
			m = n
		}
		d.bytes.i += m
		n -= m
		if n == 0 {
			break
		}
		if err := d.fill(); err != nil {
			return err
		}
	}
	return nil
}

// Specified in section B.2.2.
func (d *decoder) processSOF(n int) error {
	if d.nComp != 0 {
		return FormatError("multiple SOF markers")
	}
	switch n {
	case 6 + 3*1: // Grayscale image.
		d.nComp = 1
	case 6 + 3*3: // YCbCr or RGB image.
		d.nComp = 3
	case 6 + 3*4: // YCbCrK or CMYK image.
		d.nComp = 4
	default:
		return UnsupportedError("number of components")
	}
	if err := d.readFull(d.tmp[:n]); err != nil {
		return err
	}
	// We only support 8-bit precision.
	if d.tmp[0] != 8 {
		return UnsupportedError("precision")
	}
	d.height = int(d.tmp[1])<<8 + int(d.tmp[2])
	d.width = int(d.tmp[3])<<8 + int(d.tmp[4])
	if int(d.tmp[5]) != d.nComp {
		return FormatError("SOF has wrong length")
	}

	for i := 0; i < d.nComp; i++ {
		d.comp[i].c = d.tmp[6+3*i]
		// Section B.2.2 states that "the value of C_i shall be different from
		// the values of C_1 through C_(i-1)".
		for j := 0; j < i; j++ {
			if d.comp[i].c == d.comp[j].c {
				return FormatError("repeated component identifier")
			}
		}

		d.comp[i].tq = d.tmp[8+3*i]
		if d.comp[i].tq > maxTq {
			return FormatError("bad Tq value")
		}

		hv := d.tmp[7+3*i]
		h, v := int(hv>>4), int(hv&0x0f)
		if h < 1 || 4 < h || v < 1 || 4 < v {
			return FormatError("luma/chroma subsampling ratio")
		}
		if h == 3 || v == 3 {
			return errUnsupportedSubsamplingRatio
		}
		switch d.nComp {
		case 1:
			// If a JPEG image has only one component, section A.2 says "this data
			// is non-interleaved by definition" and section A.2.2 says "[in this
			// case...] the order of data units within a scan shall be left-to-right
			// and top-to-bottom... regardless of the values of H_1 and V_1". Section
			// 4.8.2 also says "[for non-interleaved data], the MCU is defined to be
			// one data unit". Similarly, section A.1.1 explains that it is the ratio
			// of H_i to max_j(H_j) that matters, and similarly for V. For grayscale
			// images, H_1 is the maximum H_j for all components j, so that ratio is
			// always 1. The component's (h, v) is effectively always (1, 1): even if
			// the nominal (h, v) is (2, 1), a 20x5 image is encoded in three 8x8
			// MCUs, not two 16x8 MCUs.
			h, v = 1, 1

		case 3:
			// For YCbCr images, we support both standard subsampling ratios
			// (4:4:4, 4:4:0, 4:2:2, 4:2:0, 4:1:1, 4:1:0) and non-standard ratios
			// where components may have different sampling factors. The only
			// restriction is that each component's sampling factors must evenly
			// divide the maximum factors (validated after the loop).

		case 4:
			// For 4-component images (either CMYK or YCbCrK), we only support two
			// hv vectors: [0x11 0x11 0x11 0x11] and [0x22 0x11 0x11 0x22].
			// Theoretically, 4-component JPEG images could mix and match hv values
			// but in practice, those two combinations are the only ones in use,
			// and it simplifies the applyBlack code below if we can assume that:
			//	- for CMYK, the C and K channels have full samples, and if the M
			//	  and Y channels subsample, they subsample both horizontally and
			//	  vertically.
			//	- for YCbCrK, the Y and K channels have full samples.
			switch i {
			case 0:
				if hv != 0x11 && hv != 0x22 {
					return errUnsupportedSubsamplingRatio
				}
			case 1, 2:
				if hv != 0x11 {
					return errUnsupportedSubsamplingRatio
				}
			case 3:
				if d.comp[0].h != h || d.comp[0].v != v {
					return errUnsupportedSubsamplingRatio
				}
			}
		}

		d.maxH, d.maxV = max(d.maxH, h), max(d.maxV, v)
		d.comp[i].h = h
		d.comp[i].v = v
	}

	// For 3-component images, validate that maxH and maxV are evenly divisible
	// by each component's sampling factors.
	if d.nComp == 3 {
		for i := 0; i < 3; i++ {
			if d.maxH%d.comp[i].h != 0 || d.maxV%d.comp[i].v != 0 {
				return errUnsupportedSubsamplingRatio
			}
		}
	}

	// Compute expansion factors for each component.
	for i := 0; i < d.nComp; i++ {
		d.comp[i].expandH = d.maxH / d.comp[i].h
		d.comp[i].expandV = d.maxV / d.comp[i].v
	}

	return nil
}

// Specified in section B.2.4.1.
func (d *decoder) processDQT(n int) error {
loop:
	for n > 0 {
		n--
		x, err := d.readByte()
		if err != nil {
			return err
		}
		tq := x & 0x0f
		if tq > maxTq {
			return FormatError("bad Tq value")
		}
		switch x >> 4 {
		default:
			return FormatError("bad Pq value")
		case 0:
			if n < blockSize {
				break loop
			}
			n -= blockSize
			if err := d.readFull(d.tmp[:blockSize]); err != nil {
				return err
			}
			for i := range d.quant[tq] {
				d.quant[tq][i] = int32(d.tmp[i])
			}
		case 1:
			if n < 2*blockSize {
				break loop
			}
			n -= 2 * blockSize
			if err := d.readFull(d.tmp[:2*blockSize]); err != nil {
				return err
			}
			for i := range d.quant[tq] {
				d.quant[tq][i] = int32(d.tmp[2*i])<<8 | int32(d.tmp[2*i+1])
			}
		}
	}
	if n != 0 {
		return FormatError("DQT has wrong length")
	}
	return nil
}

// Specified in section B.2.4.4.
func (d *decoder) processDRI(n int) error {
	if n != 2 {
		return FormatError("DRI has wrong length")
	}
	if err := d.readFull(d.tmp[:2]); err != nil {
		return err
	}
	d.ri = int(d.tmp[0])<<8 + int(d.tmp[1])
	return nil
}

func (d *decoder) processApp0Marker(n int) error {
	if n < 5 {
		return d.ignore(n)
	}
	if err := d.readFull(d.tmp[:5]); err != nil {
		return err
	}
	n -= 5

	d.jfif = d.tmp[0] == 'J' && d.tmp[1] == 'F' && d.tmp[2] == 'I' && d.tmp[3] == 'F' && d.tmp[4] == '\x00'
	if d.jfif && n >= 7 {
		// Version, units, horizontal and vertical density.
		if err := d.readFull(d.tmp[:7]); err != nil {
			return err
		}
		n -= 7
		x := float64(int(d.tmp[3])<<8 | int(d.tmp[4]))
		switch d.tmp[2] {
		case 1:
			d.jfifDensity = density.FromPerInch(x)
		case 2:
			d.jfifDensity = density.FromPerCm(x)
		}
	}

	if n > 0 {
		return d.ignore(n)
	}
	return nil
}

func (d *decoder) processApp14Marker(n int) error {
	if n < 12 {
		return d.ignore(n)
	}
	if err := d.readFull(d.tmp[:12]); err != nil {
		return err
	}
	n -= 12

	if d.tmp[0] == 'A' && d.tmp[1] == 'd' && d.tmp[2] == 'o' && d.tmp[3] == 'b' && d.tmp[4] == 'e' {
		d.adobeTransformValid = true
		d.adobeTransform = d.tmp[11]
	}

	if n > 0 {
		return d.ignore(n)
	}
	return nil
}

func (d *decoder) isRGB() bool {
	if d.jfif {
		return false
	}
	if d.adobeTransformValid && d.adobeTransform == adobeTransformUnknown {
		// https://www.sno.phy.queensu.ca/~phil/exiftool/TagNames/JPEG.html#Adobe
		// says that 0 means Unknown (and in practice RGB) and 1 means YCbCr.
		return true
	}
	return d.comp[0].c == 'R' && d.comp[1].c == 'G' && d.comp[2].c == 'B'
}

// processApp1 keeps the EXIF block of an APP1 segment (goimgtools addition;
// the standard library ignores the segment).
func (d *decoder) processApp1(n int) error {
	const prefix, xmpPrefix = "Exif\x00\x00", "http://ns.adobe.com/xap/1.0/\x00"
	if d.metaDone || (d.meta.Exif != nil && d.meta.Xmp != nil) || n <= len(prefix) {
		return d.ignore(n)
	}
	buf := make([]byte, n)
	if err := d.readFull(buf); err != nil {
		return err
	}
	switch {
	case d.meta.Exif == nil && string(buf[:len(prefix)]) == prefix:
		d.meta.Exif = buf[len(prefix):]
	case d.meta.Xmp == nil && len(buf) > len(xmpPrefix) && string(buf[:len(xmpPrefix)]) == xmpPrefix:
		d.meta.Xmp = buf[len(xmpPrefix):]
	}
	return nil
}

// processApp2 collects the pieces of an ICC profile, which is split over
// APP2 segments numbered from 1.
func (d *decoder) processApp2(n int) error {
	const prefix = "ICC_PROFILE\x00"
	if d.metaDone || n < len(prefix)+2 {
		return d.ignore(n)
	}
	buf := make([]byte, n)
	if err := d.readFull(buf); err != nil {
		return err
	}
	if string(buf[:len(prefix)]) != prefix {
		return nil
	}
	seq, total := int(buf[len(prefix)]), int(buf[len(prefix)+1])
	if d.iccParts == nil {
		d.iccParts, d.iccTotal = map[int][]byte{}, total
	}
	if seq >= 1 && seq <= total && total == d.iccTotal {
		d.iccParts[seq] = buf[len(prefix)+2:]
	}
	return nil
}

// finishMeta assembles the ICC profile once the header has been read.
func (d *decoder) finishMeta() {
	d.metaDone = true
	// The resolution of the EXIF block, when it has one, as libvips takes
	// it; else that of the JFIF segment.
	d.meta.Density = d.jfifDensity
	if v := density.FromExif(d.meta.Exif); v > 0 {
		d.meta.Density = v
	}
	if len(d.iccParts) != 0 && len(d.iccParts) == d.iccTotal {
		var icc []byte
		for i := 1; i <= d.iccTotal; i++ {
			icc = append(icc, d.iccParts[i]...)
		}
		if len(icc) <= pipeline.MaxMetaBytes {
			d.meta.ICC = icc
		}
	}
	d.iccParts = nil
}
