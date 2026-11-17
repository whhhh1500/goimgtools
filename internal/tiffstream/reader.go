// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package tiffstream is a fork of golang.org/x/image/tiff's decoder that
// hands out the image as horizontal strips instead of decoding it whole.
//
// The header parsing and the per-row sample conversion (decoder.decode) are
// unchanged. Only the block loop of Decode differs: instead of decompressing
// a whole strip or tile into memory it reads one row at a time from a
// streaming decompressor (see stream.go).
//
// The TIFF specification is at http://partners.adobe.com/public/developer/en/tiff/TIFF6.pdf
package tiffstream

import (
	"encoding/binary"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"math/bits"

	"golang.org/x/image/ccitt"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// A FormatError reports that the input is not a valid TIFF image.
type FormatError string

func (e FormatError) Error() string {
	return "tiff: invalid format: " + string(e)
}

// An UnsupportedError reports that the input uses a valid but
// unimplemented feature.
type UnsupportedError string

func (e UnsupportedError) Error() string {
	return "tiff: unsupported feature: " + string(e)
}

var (
	errNoPixels          = FormatError("not enough pixel data")
	errInvalidColorIndex = FormatError("invalid color index")
)

const maxChunkSize = 10 << 20 // 10M

// safeReadAt is a verbatim copy of internal/saferio.ReadDataAt from the
// standard library, which is used to read data from a reader using a length
// provided by untrusted data, without allocating the entire slice ahead of time
// if it is large (>maxChunkSize). This allows us to avoid allocating giant
// slices before learning that we can't actually read that much data from the
// reader.
func safeReadAt(r io.ReaderAt, n uint64, off int64) ([]byte, error) {
	if int64(n) < 0 || n != uint64(int(n)) {
		// n is too large to fit in int, so we can't allocate
		// a buffer large enough. Treat this as a read failure.
		return nil, io.ErrUnexpectedEOF
	}

	if n < maxChunkSize {
		buf := make([]byte, n)
		_, err := r.ReadAt(buf, off)
		if err != nil {
			// io.SectionReader can return EOF for n == 0,
			// but for our purposes that is a success.
			if err != io.EOF || n > 0 {
				return nil, err
			}
		}
		return buf, nil
	}

	var buf []byte
	buf1 := make([]byte, maxChunkSize)
	for n > 0 {
		next := n
		if next > maxChunkSize {
			next = maxChunkSize
		}
		_, err := r.ReadAt(buf1[:next], off)
		if err != nil {
			return nil, err
		}
		buf = append(buf, buf1[:next]...)
		n -= next
		off += int64(next)
	}
	return buf, nil
}

type decoder struct {
	r         io.ReaderAt
	byteOrder binary.ByteOrder
	config    image.Config
	mode      imageMode
	bpp       uint
	features  map[int][]uint
	ifd       map[int][ifdLen]byte
	palette   []color.Color
	icc       []byte  // the colour profile, if the file has one
	xres      float64 // the resolution across, in resUnit
	xmp       []byte  // the XMP packet, if the file has one
	// jpegTables are the tables JPEG-compressed blocks share.
	jpegTables []byte
	resUnit    uint

	buf   []byte
	off   int    // Current offset in buf.
	v     uint32 // Buffer value for reading with arbitrary bit depths.
	nbits uint   // Remaining number of bits in v.
}

// firstVal returns the first uint of the features entry with the given tag,
// or 0 if the tag does not exist.
func (d *decoder) firstVal(tag int) uint {
	f := d.features[tag]
	if len(f) == 0 {
		return 0
	}
	return f[0]
}

// firstIntVal returns the first int of the features entry with the given tag,
// or 0 if the tag does not exist.
// If the tag overflows an int, it returns an error.
func (d *decoder) firstIntVal(tag int) (int, error) {
	v := d.firstVal(tag)
	if v > math.MaxInt {
		return 0, FormatError("IFD value too large")
	}
	return int(v), nil
}

// ifdUint decodes the IFD entry in p, which must be of the Byte, Short
// or Long type, and returns the decoded uint values.
//
// maxCount limits the number of values.
// If the entry contains more than maxCount values, only the first maxCount are parsed.
func (d *decoder) ifdUint(p []byte, maxCount int) (u []uint, err error) {
	var raw []byte
	if len(p) < ifdLen {
		return nil, FormatError("bad IFD entry")
	}

	datatype := d.byteOrder.Uint16(p[2:4])
	if dt := int(datatype); dt <= 0 || dt >= len(lengths) {
		return nil, UnsupportedError("IFD entry datatype")
	}

	count := d.byteOrder.Uint32(p[4:8])
	if count > math.MaxInt32/lengths[datatype] {
		return nil, FormatError("IFD data too large")
	}
	truncatedCount := min(int(count), maxCount)
	if datalen := lengths[datatype] * count; datalen > 4 {
		truncatedLen := uint64(lengths[datatype]) * uint64(truncatedCount)
		// The IFD contains a pointer to the real value.
		raw, err = safeReadAt(d.r, truncatedLen, int64(d.byteOrder.Uint32(p[8:12])))
	} else {
		raw = p[8 : 8+datalen]
	}
	if err != nil {
		return nil, err
	}

	u = make([]uint, truncatedCount)
	switch datatype {
	case dtByte:
		for i := range u {
			u[i] = uint(raw[i])
		}
	case dtShort:
		for i := range u {
			u[i] = uint(d.byteOrder.Uint16(raw[2*i : 2*(i+1)]))
		}
	case dtLong:
		for i := range u {
			u[i] = uint(d.byteOrder.Uint32(raw[4*i : 4*(i+1)]))
		}
	default:
		return nil, UnsupportedError("data type")
	}
	return u, nil
}

// parseIFDOffsets parses an IFD entry stored in d.ifd using ifdUint.
// For IFD entries which can be large, we delay reading and parsing the entry
// until we know the image size, which lets us set reasonable bounds on the IFD entry.
func (d *decoder) parseIFDOffsets(tag int, maxCount int) (u []uint, err error) {
	p, ok := d.ifd[tag]
	if !ok {
		return nil, nil
	}
	return d.ifdUint(p[:], maxCount)
}

// parseIFD decides whether the IFD entry in p is "interesting" and
// stows away the data in the decoder. It returns the tag number of the
// entry and an error, if any.
func (d *decoder) parseIFD(p []byte) (int, error) {
	// smallEntryMaxCount is the limit to use for parsed IFD entries that
	// don't scale with the image size.
	//
	// We could be more precise and limit the number of entries to exactly the
	// correct limit (for example, tTileWidth should always have exactly one value),
	// but 16 is small enough to prevent excessive allocation and large enough to
	// allow for any multi-value entry.
	const smallEntryMaxCount = 16

	tag := d.byteOrder.Uint16(p[0:2])
	switch tag {
	case tBitsPerSample,
		tExtraSamples,
		tPhotometricInterpretation,
		tCompression,
		tPredictor,
		tRowsPerStrip,
		tTileWidth,
		tTileLength,
		tImageLength,
		tImageWidth,
		tFillOrder,
		tT4Options,
		tT6Options:
		val, err := d.ifdUint(p, smallEntryMaxCount)
		if err != nil {
			return 0, err
		}
		d.features[int(tag)] = val
	case tStripOffsets,
		tStripByteCounts,
		tTileOffsets,
		tTileByteCounts:
		// These keys may contain many values.
		// Stash the IFD entry for later parsing.
		var v [ifdLen]byte
		copy(v[:], p)
		d.ifd[int(tag)] = v
	case tColorMap:
		const maxColors = 256
		val, err := d.ifdUint(p, (3*maxColors)+1)
		if err != nil {
			return 0, err
		}
		numcolors := len(val) / 3
		if len(val)%3 != 0 || numcolors <= 0 || numcolors > maxColors {
			return 0, FormatError("bad ColorMap length")
		}
		d.palette = make([]color.Color, numcolors)
		for i := 0; i < numcolors; i++ {
			d.palette[i] = color.RGBA64{
				uint16(val[i]),
				uint16(val[i+numcolors]),
				uint16(val[i+2*numcolors]),
				0xffff,
			}
		}
	case tICCProfile:
		// Kept when it can be read and is not too large; a bad one only
		// costs the profile, not the image.
		dt := d.byteOrder.Uint16(p[2:4])
		n := d.byteOrder.Uint32(p[4:8])
		if (dt == dtUndefined || dt == dtByte) && n > 4 && n <= pipeline.MaxMetaBytes && d.icc == nil {
			if b, err := safeReadAt(d.r, uint64(n), int64(d.byteOrder.Uint32(p[8:12]))); err == nil {
				d.icc = b
			}
		}
	case tXMP:
		// goimgtools addition: the XMP packet.
		dt := d.byteOrder.Uint16(p[2:4])
		n := d.byteOrder.Uint32(p[4:8])
		if (dt == dtUndefined || dt == dtByte) && n > 4 && n <= pipeline.MaxMetaBytes && d.xmp == nil {
			if b, err := safeReadAt(d.r, uint64(n), int64(d.byteOrder.Uint32(p[8:12]))); err == nil {
				d.xmp = b
			}
		}
	case tJPEGTables:
		// goimgtools addition: the tables of JPEG compression.
		dt := d.byteOrder.Uint16(p[2:4])
		n := d.byteOrder.Uint32(p[4:8])
		if (dt == dtUndefined || dt == dtByte) && n > 4 && n <= 1<<20 && d.jpegTables == nil {
			if b, err := safeReadAt(d.r, uint64(n), int64(d.byteOrder.Uint32(p[8:12]))); err == nil {
				d.jpegTables = b
			}
		}
	case tXResolution:
		// goimgtools addition: the resolution across, a fraction at an
		// offset. One that cannot be read is left out.
		if d.byteOrder.Uint16(p[2:4]) == dtRational && d.byteOrder.Uint32(p[4:8]) >= 1 {
			if b, err := safeReadAt(d.r, 8, int64(d.byteOrder.Uint32(p[8:12]))); err == nil {
				if den := d.byteOrder.Uint32(b[4:]); den != 0 {
					d.xres = float64(d.byteOrder.Uint32(b)) / float64(den)
				}
			}
		}
	case tResolutionUnit:
		if val, err := d.ifdUint(p, smallEntryMaxCount); err == nil && len(val) > 0 {
			d.resUnit = val[0]
		}
	case tSampleFormat:
		// Page 27 of the spec: If the SampleFormat is present and
		// the value is not 1 [= unsigned integer data], a Baseline
		// TIFF reader that cannot handle the SampleFormat value
		// must terminate the import process gracefully.
		val, err := d.ifdUint(p, smallEntryMaxCount)
		if err != nil {
			return 0, err
		}
		for _, v := range val {
			if v != 1 {
				return 0, UnsupportedError("sample format")
			}
		}
	}
	return int(tag), nil
}

// readBits reads n bits from the internal buffer starting at the current offset.
func (d *decoder) readBits(n uint) (v uint32, ok bool) {
	for d.nbits < n {
		d.v <<= 8
		if d.off >= len(d.buf) {
			return 0, false
		}
		d.v |= uint32(d.buf[d.off])
		d.off++
		d.nbits += 8
	}
	d.nbits -= n
	rv := d.v >> d.nbits
	d.v &^= rv << d.nbits
	return rv, true
}

// flushBits discards the unread bits in the buffer used by readBits.
// It is used at the end of a line.
func (d *decoder) flushBits() {
	d.v = 0
	d.nbits = 0
}

// minInt returns the smaller of x or y.
func minInt(a, b int) int {
	if a <= b {
		return a
	}
	return b
}

// decode decodes the raw data of an image.
// It reads from d.buf and writes the strip or tile into dst.
func (d *decoder) decode(dst image.Image, xmin, ymin, xmax, ymax int) error {
	d.off = 0

	// Apply horizontal predictor if necessary.
	// In this case, p contains the color difference to the preceding pixel.
	// See page 64-65 of the spec.
	if d.firstVal(tPredictor) == prHorizontal {
		switch d.bpp {
		case 16:
			var off int
			n := 2 * len(d.features[tBitsPerSample]) // bytes per sample times samples per pixel
			for y := ymin; y < ymax; y++ {
				off += n
				for x := 0; x < (xmax-xmin-1)*n; x += 2 {
					if off+2 > len(d.buf) {
						return errNoPixels
					}
					v0 := d.byteOrder.Uint16(d.buf[off-n : off-n+2])
					v1 := d.byteOrder.Uint16(d.buf[off : off+2])
					d.byteOrder.PutUint16(d.buf[off:off+2], v1+v0)
					off += 2
				}
			}
		case 8:
			var off int
			n := 1 * len(d.features[tBitsPerSample]) // bytes per sample times samples per pixel
			for y := ymin; y < ymax; y++ {
				off += n
				for x := 0; x < (xmax-xmin-1)*n; x++ {
					if off >= len(d.buf) {
						return errNoPixels
					}
					d.buf[off] += d.buf[off-n]
					off++
				}
			}
		case 1:
			return UnsupportedError("horizontal predictor with 1 BitsPerSample")
		}
	}

	// calcRowBytes returns the number of bytes in a row with numSamples samples per pixel
	// and bytesPerSample bytes per sample.
	calcRowBytes := func(xmin, xmax, numSamples int, bitsPerSample uint) int {
		return int((int64(xmax-xmin)*int64(numSamples)*int64(bitsPerSample) + 7) / 8)
	}
	// calcRowOff returns the offset of the next row.
	calcRowOff := func(y, ymin, rowBytes int) int {
		return (y - ymin) * rowBytes
	}

	rMaxX := minInt(xmax, dst.Bounds().Max.X)
	rMaxY := minInt(ymax, dst.Bounds().Max.Y)
	switch d.mode {
	case mGray, mGrayInvert:
		rowBytes := calcRowBytes(xmin, xmax, 1, d.bpp)
		if d.bpp == 16 {
			img := dst.(*image.Gray16)
			for y := ymin; y < rMaxY; y++ {
				d.off = calcRowOff(y, ymin, rowBytes)
				for x := xmin; x < rMaxX; x++ {
					if d.off+2 > len(d.buf) {
						return errNoPixels
					}
					v := d.byteOrder.Uint16(d.buf[d.off : d.off+2])
					d.off += 2
					if d.mode == mGrayInvert {
						v = 0xffff - v
					}
					img.SetGray16(x, y, color.Gray16{v})
				}
			}
		} else {
			img := dst.(*image.Gray)
			max := uint32((1 << d.bpp) - 1)
			for y := ymin; y < rMaxY; y++ {
				d.off = calcRowOff(y, ymin, rowBytes)
				d.flushBits()
				for x := xmin; x < rMaxX; x++ {
					v, ok := d.readBits(d.bpp)
					if !ok {
						return errNoPixels
					}
					v = v * 0xff / max
					if d.mode == mGrayInvert {
						v = 0xff - v
					}
					img.SetGray(x, y, color.Gray{uint8(v)})
				}
			}
		}
	case mPaletted:
		img := dst.(*image.Paletted)
		pLen := len(d.palette)
		rowBytes := calcRowBytes(xmin, xmax, 1, d.bpp)
		for y := ymin; y < rMaxY; y++ {
			d.off = calcRowOff(y, ymin, rowBytes)
			d.flushBits()
			for x := xmin; x < rMaxX; x++ {
				v, ok := d.readBits(d.bpp)
				if !ok {
					return errNoPixels
				}
				idx := uint8(v)
				if int(idx) >= pLen {
					return errInvalidColorIndex
				}
				img.SetColorIndex(x, y, idx)
			}
		}
	case mRGB:
		rowBytes := calcRowBytes(xmin, xmax, 3, d.bpp)
		if d.bpp == 16 {
			img := dst.(*image.RGBA64)
			for y := ymin; y < rMaxY; y++ {
				d.off = calcRowOff(y, ymin, rowBytes)
				for x := xmin; x < rMaxX; x++ {
					if d.off+6 > len(d.buf) {
						return errNoPixels
					}
					r := d.byteOrder.Uint16(d.buf[d.off+0 : d.off+2])
					g := d.byteOrder.Uint16(d.buf[d.off+2 : d.off+4])
					b := d.byteOrder.Uint16(d.buf[d.off+4 : d.off+6])
					d.off += 6
					img.SetRGBA64(x, y, color.RGBA64{r, g, b, 0xffff})
				}
			}
		} else {
			img := dst.(*image.RGBA)
			for y := ymin; y < rMaxY; y++ {
				d.off = calcRowOff(y, ymin, rowBytes)
				min := img.PixOffset(xmin, y)
				max := img.PixOffset(rMaxX, y)
				off := (y - ymin) * (xmax - xmin) * 3
				for i := min; i < max; i += 4 {
					if off+3 > len(d.buf) {
						return errNoPixels
					}
					img.Pix[i+0] = d.buf[off+0]
					img.Pix[i+1] = d.buf[off+1]
					img.Pix[i+2] = d.buf[off+2]
					img.Pix[i+3] = 0xff
					off += 3
				}
			}
		}
	case mNRGBA:
		rowBytes := calcRowBytes(xmin, xmax, 4, d.bpp)
		if d.bpp == 16 {
			img := dst.(*image.NRGBA64)
			for y := ymin; y < rMaxY; y++ {
				d.off = calcRowOff(y, ymin, rowBytes)
				for x := xmin; x < rMaxX; x++ {
					if d.off+8 > len(d.buf) {
						return errNoPixels
					}
					r := d.byteOrder.Uint16(d.buf[d.off+0 : d.off+2])
					g := d.byteOrder.Uint16(d.buf[d.off+2 : d.off+4])
					b := d.byteOrder.Uint16(d.buf[d.off+4 : d.off+6])
					a := d.byteOrder.Uint16(d.buf[d.off+6 : d.off+8])
					d.off += 8
					img.SetNRGBA64(x, y, color.NRGBA64{r, g, b, a})
				}
			}
		} else {
			img := dst.(*image.NRGBA)
			for y := ymin; y < rMaxY; y++ {
				min := img.PixOffset(xmin, y)
				max := img.PixOffset(rMaxX, y)
				i0, i1 := (y-ymin)*(xmax-xmin)*4, (y-ymin+1)*(xmax-xmin)*4
				if i1 > len(d.buf) {
					return errNoPixels
				}
				copy(img.Pix[min:max], d.buf[i0:i1])
			}
		}
	case mRGBA:
		rowBytes := calcRowBytes(xmin, xmax, 4, d.bpp)
		if d.bpp == 16 {
			img := dst.(*image.RGBA64)
			for y := ymin; y < rMaxY; y++ {
				d.off = calcRowOff(y, ymin, rowBytes)
				for x := xmin; x < rMaxX; x++ {
					if d.off+8 > len(d.buf) {
						return errNoPixels
					}
					r := d.byteOrder.Uint16(d.buf[d.off+0 : d.off+2])
					g := d.byteOrder.Uint16(d.buf[d.off+2 : d.off+4])
					b := d.byteOrder.Uint16(d.buf[d.off+4 : d.off+6])
					a := d.byteOrder.Uint16(d.buf[d.off+6 : d.off+8])
					d.off += 8
					img.SetRGBA64(x, y, color.RGBA64{r, g, b, a})
				}
			}
		} else {
			img := dst.(*image.RGBA)
			for y := ymin; y < rMaxY; y++ {
				min := img.PixOffset(xmin, y)
				max := img.PixOffset(rMaxX, y)
				i0, i1 := (y-ymin)*(xmax-xmin)*4, (y-ymin+1)*(xmax-xmin)*4
				if i1 > len(d.buf) {
					return errNoPixels
				}
				copy(img.Pix[min:max], d.buf[i0:i1])
			}
		}
	}

	return nil
}

// maxBytesPerPixel is the maximum possible bytes-per-pixel,
// used for conservative bounds checking.
const maxBytesPerPixel = 8

func newDecoder(r io.ReaderAt) (*decoder, error) {
	d := &decoder{
		r:        r,
		features: make(map[int][]uint),
		ifd:      make(map[int][ifdLen]byte),
	}

	p := make([]byte, 8)
	if _, err := d.r.ReadAt(p, 0); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	switch string(p[0:4]) {
	case leHeader:
		d.byteOrder = binary.LittleEndian
	case beHeader:
		d.byteOrder = binary.BigEndian
	default:
		return nil, FormatError("malformed header")
	}

	ifdOffset := int64(d.byteOrder.Uint32(p[4:8]))

	// The first two bytes contain the number of entries (12 bytes each).
	if _, err := d.r.ReadAt(p[0:2], ifdOffset); err != nil {
		return nil, err
	}
	numItems := int(d.byteOrder.Uint16(p[0:2]))

	// All IFD entries are read in one chunk.
	var err error
	p, err = safeReadAt(d.r, uint64(ifdLen*numItems), ifdOffset+2)
	if err != nil {
		return nil, err
	}

	prevTag := -1
	for i := 0; i < len(p); i += ifdLen {
		tag, err := d.parseIFD(p[i : i+ifdLen])
		if err != nil {
			return nil, err
		}
		if tag <= prevTag {
			return nil, FormatError("tags are not sorted in ascending order")
		}
		prevTag = tag
	}

	if d.config.Width, err = d.firstIntVal(tImageWidth); err != nil {
		return nil, err
	}
	if d.config.Height, err = d.firstIntVal(tImageLength); err != nil {
		return nil, err
	}
	if d.config.Width == 0 || d.config.Height == 0 {
		return nil, FormatError("zero-size image")
	}
	// Check that the image fits in memory.
	// This conservatively assumes 8 bytes per pixel,
	// rather than using the actual pixel size.
	if _, ok := mul3(d.config.Width, d.config.Height, maxBytesPerPixel); !ok {
		return nil, FormatError("image too large")
	}

	if _, ok := d.features[tBitsPerSample]; !ok {
		// Default is 1 per specification.
		d.features[tBitsPerSample] = []uint{1}
	}
	d.bpp = d.firstVal(tBitsPerSample)
	switch d.bpp {
	case 0:
		return nil, FormatError("BitsPerSample must not be 0")
	case 1, 8, 16:
		// Nothing to do, these are accepted by this implementation.
	default:
		return nil, UnsupportedError(fmt.Sprintf("BitsPerSample of %v", d.bpp))
	}

	// Determine the image mode.
	switch d.firstVal(tPhotometricInterpretation) {
	case pRGB:
		if d.bpp == 16 {
			for _, b := range d.features[tBitsPerSample] {
				if b != 16 {
					return nil, FormatError("wrong number of samples for 16bit RGB")
				}
			}
		} else {
			for _, b := range d.features[tBitsPerSample] {
				if b != 8 {
					return nil, FormatError("wrong number of samples for 8bit RGB")
				}
			}
		}
		// RGB images normally have 3 samples per pixel.
		// If there are more, ExtraSamples (p. 31-32 of the spec)
		// gives their meaning (usually an alpha channel).
		//
		// This implementation does not support extra samples
		// of an unspecified type.
		switch len(d.features[tBitsPerSample]) {
		case 3:
			d.mode = mRGB
			if d.bpp == 16 {
				d.config.ColorModel = color.RGBA64Model
			} else {
				d.config.ColorModel = color.RGBAModel
			}
		case 4:
			switch d.firstVal(tExtraSamples) {
			case 1:
				d.mode = mRGBA
				if d.bpp == 16 {
					d.config.ColorModel = color.RGBA64Model
				} else {
					d.config.ColorModel = color.RGBAModel
				}
			case 2:
				d.mode = mNRGBA
				if d.bpp == 16 {
					d.config.ColorModel = color.NRGBA64Model
				} else {
					d.config.ColorModel = color.NRGBAModel
				}
			default:
				return nil, FormatError("wrong number of samples for RGB")
			}
		default:
			return nil, FormatError("wrong number of samples for RGB")
		}
	case pPaletted:
		d.mode = mPaletted
		d.config.ColorModel = color.Palette(d.palette)
	case pWhiteIsZero:
		d.mode = mGrayInvert
		if d.bpp == 16 {
			d.config.ColorModel = color.Gray16Model
		} else {
			d.config.ColorModel = color.GrayModel
		}
	case pBlackIsZero:
		d.mode = mGray
		if d.bpp == 16 {
			d.config.ColorModel = color.Gray16Model
		} else {
			d.config.ColorModel = color.GrayModel
		}
	case pYCbCr:
		// goimgtools addition: YCbCr compressed as JPEG, which the JPEG
		// decoder turns into RGB.
		if d.firstVal(tCompression) != cJPEG || d.bpp != 8 || len(d.features[tBitsPerSample]) != 3 {
			return nil, UnsupportedError("color model")
		}
		d.mode = mRGB
		d.config.ColorModel = color.RGBAModel
	default:
		return nil, UnsupportedError("color model")
	}
	if p := d.firstVal(tPhotometricInterpretation); p != pRGB && p != pYCbCr {
		if len(d.features[tBitsPerSample]) != 1 {
			return nil, UnsupportedError("extra samples")
		}
	}

	return d, nil
}

func ccittFillOrder(tiffFillOrder uint) ccitt.Order {
	if tiffFillOrder == 2 {
		return ccitt.LSB
	}
	return ccitt.MSB
}

// mul3 is a copy of golang.org/x/image/internal/safemath.Mul3: it returns
// (x * y * z), unless at least one argument is negative or if the
// computation overflows the int type.
func mul3(x, y, z int) (_ int, ok bool) {
	if x < 0 || y < 0 || z < 0 {
		return -1, false
	}
	hi, lo := bits.Mul64(uint64(x), uint64(y))
	if hi != 0 {
		return -1, false
	}
	hi, lo = bits.Mul64(lo, uint64(z))
	if hi != 0 {
		return -1, false
	}
	a := int(lo)
	if a < 0 || uint64(a) != lo {
		return -1, false
	}
	return a, true
}
