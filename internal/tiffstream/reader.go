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

	"github.com/whhhh1500/goimgtools/internal/jpegstream"
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
	// next is the place of the directory after this one (0: none), and
	// reduced whether this one is a reduced version of an image (its
	// NewSubfileType, read apart: see levelInfo).
	next    int64
	reduced bool

	r         io.ReaderAt
	byteOrder binary.ByteOrder
	config    image.Config
	mode      imageMode
	bpp       uint
	features  map[int][]uint
	ifd       map[int][bigIFDLen]byte
	// big: a BigTIFF, its offsets and counts of 8 bytes, its entries of
	// 20 (goimgtools addition).
	big     bool
	palette []color.Color
	icc     []byte  // the colour profile, if the file has one
	xres    float64 // the resolution across, in resUnit
	xmp     []byte  // the XMP packet, if the file has one
	iptc    []byte  // the IPTC record, if the file has one
	exif    []byte  // the EXIF block made of the first directory (exif.go)
	// jpegTables are the tables JPEG-compressed blocks share.
	jpegTables []byte
	resUnit    uint
	// grayAlpha: grey with an alpha sample after it (the mode mGray or
	// mGrayInvert); premult: the grey multiplied by it.
	grayAlpha, premult bool
	// stored: the samples of a file of floats or of Lab, which convert
	// makes the samples the features describe (convert.go).
	stored *stored

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
	if len(p) < d.entryLen() {
		return nil, FormatError("bad IFD entry")
	}

	datatype, count, val := d.field(p)
	if dt := int(datatype); dt <= 0 || dt >= len(lengths) {
		return nil, UnsupportedError("IFD entry datatype")
	}

	if count > math.MaxInt32/uint64(lengths[datatype]) {
		return nil, FormatError("IFD data too large")
	}
	truncatedCount := min(int(count), maxCount)
	if datalen := uint64(lengths[datatype]) * count; datalen > uint64(len(val)) {
		truncatedLen := uint64(lengths[datatype]) * uint64(truncatedCount)
		// The IFD contains a pointer to the real value.
		raw, err = safeReadAt(d.r, truncatedLen, d.pointer(val))
	} else {
		raw = val[:datalen]
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
	case dtLong8, dtIFD8:
		if !d.big {
			return nil, UnsupportedError("data type")
		}
		for i := range u {
			v := d.byteOrder.Uint64(raw[8*i : 8*(i+1)])
			if v > math.MaxInt {
				return nil, FormatError("IFD value too large")
			}
			u[i] = uint(v)
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
	return d.ifdUint(p[:d.entryLen()], maxCount)
}

// entryLen is the length of an IFD entry.
func (d *decoder) entryLen() int {
	if d.big {
		return bigIFDLen
	}
	return ifdLen
}

// field is the data type and the count of an IFD entry, and the bytes of
// it that hold its values or point to them.
func (d *decoder) field(p []byte) (datatype uint16, count uint64, val []byte) {
	datatype = d.byteOrder.Uint16(p[2:4])
	if d.big {
		return datatype, d.byteOrder.Uint64(p[4:12]), p[12:20]
	}
	return datatype, uint64(d.byteOrder.Uint32(p[4:8])), p[8:12]
}

// pointer is where the values of an entry are when they do not fit in it;
// -1 for a place no file has.
func (d *decoder) pointer(val []byte) int64 {
	if !d.big {
		return int64(d.byteOrder.Uint32(val))
	}
	if v := d.byteOrder.Uint64(val); v <= math.MaxInt64 {
		return int64(v)
	}
	return -1
}

// bytesOf is the bytes of an entry of bytes (a profile, a packet, tables)
// of more than 4 and no more than limit, read from where they are; nil
// for another kind of entry or one that cannot be read.
func (d *decoder) bytesOf(p []byte, limit uint64) []byte {
	dt, n, val := d.field(p)
	if (dt != dtUndefined && dt != dtByte) || n <= 4 || n > limit {
		return nil
	}
	if n <= uint64(len(val)) {
		return append([]byte(nil), val[:n]...)
	}
	b, err := safeReadAt(d.r, n, d.pointer(val))
	if err != nil {
		return nil
	}
	return b
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
		tT6Options,
		tInkSet:
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
		var v [bigIFDLen]byte
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
		if d.icc == nil {
			d.icc = d.bytesOf(p, pipeline.MaxMetaBytes)
		}
	case tXMP:
		// goimgtools addition: the XMP packet.
		if d.xmp == nil {
			d.xmp = d.bytesOf(p, pipeline.MaxMetaBytes)
		}
	case tIPTC:
		// goimgtools addition: the IPTC record, of bytes or (as Photoshop
		// has it) of LONGs, which are the same bytes in words; put in
		// order when a writer swapped the words' bytes; taken out of
		// Photoshop's resources when it is wrapped in them as a JPEG's is
		// (libvips writes a JPEG's so).
		if dt, n, val := d.field(p); d.iptc == nil && (dt == dtUndefined || dt == dtByte || dt == dtLong) {
			if dt == dtLong {
				n *= 4
			}
			var b []byte
			switch {
			case n == 0 || n > pipeline.MaxMetaBytes:
			case n <= uint64(len(val)):
				b = append([]byte(nil), val[:n]...)
			default:
				b, _ = safeReadAt(d.r, n, d.pointer(val))
			}
			if len(b) >= 4 && b[0] != 0x1c && b[3] == 0x1c {
				for i := 0; i+4 <= len(b); i += 4 {
					b[i], b[i+1], b[i+2], b[i+3] = b[i+3], b[i+2], b[i+1], b[i]
				}
			}
			if head := "Photoshop 3.0\x00"; len(b) > len(head) && string(b[:len(head)]) == head {
				b = jpegstream.PhotoshopIptc(b[len(head):])
			}
			if len(b) > 0 && b[0] == 0x1c {
				d.iptc = b
			}
		}
	case tJPEGTables:
		// goimgtools addition: the tables of JPEG compression.
		if d.jpegTables == nil {
			d.jpegTables = d.bytesOf(p, 1<<20)
		}
	case tXResolution:
		// goimgtools addition: the resolution across, a fraction at an
		// offset. One that cannot be read is left out.
		if dt, n, val := d.field(p); dt == dtRational && n >= 1 {
			// In the entry itself in a BigTIFF.
			b := val
			if !d.big {
				var err error
				if b, err = safeReadAt(d.r, 8, d.pointer(val)); err != nil {
					b = nil
				}
			}
			if b != nil {
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
		// (goimgtools: floats are read, see convert.go; setupStored
		// refuses the other formats.)
		val, err := d.ifdUint(p, smallEntryMaxCount)
		if err != nil {
			return 0, err
		}
		d.features[int(tag)] = val
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
	case mCMYK:
		// The four inks as they are; of 16 bits, their high bytes.
		img := dst.(*image.CMYK)
		bps := int(d.bpp) / 8
		n := rMaxX - xmin
		hi := 0
		if d.byteOrder == binary.LittleEndian {
			hi = 1
		}
		for y := ymin; y < rMaxY; y++ {
			off := (y - ymin) * (xmax - xmin) * 4 * bps
			if off+n*4*bps > len(d.buf) {
				return errNoPixels
			}
			o := img.PixOffset(xmin, y)
			if bps == 1 {
				copy(img.Pix[o:o+4*n], d.buf[off:])
				continue
			}
			for i := 0; i < 4*n; i++ {
				img.Pix[o+i] = d.buf[off+2*i+hi]
			}
		}
	case mGray, mGrayInvert:
		if d.grayAlpha {
			return d.decodeGrayAlpha(dst, xmin, ymin, xmax, rMaxX, rMaxY)
		}
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

// decodeGrayAlpha is decode for grey with alpha: each pixel a grey and its
// alpha, into the colour image of its kind.
func (d *decoder) decodeGrayAlpha(dst image.Image, xmin, ymin, xmax, rMaxX, rMaxY int) error {
	bps := int(d.bpp) / 8
	n := rMaxX - xmin
	// grey is the level of a sample: of white as zero, the other way round
	// (from its alpha when it is multiplied by it).
	grey := func(g, a, top uint32) uint32 {
		if d.mode != mGrayInvert {
			return g
		}
		if d.premult {
			return a - min(g, a)
		}
		return top - g
	}
	for y := ymin; y < rMaxY; y++ {
		off := (y - ymin) * (xmax - xmin) * 2 * bps
		if off+n*2*bps > len(d.buf) {
			return errNoPixels
		}
		for x := xmin; x < rMaxX; x++ {
			if bps == 2 {
				a := uint32(d.byteOrder.Uint16(d.buf[off+2:]))
				g := uint16(grey(uint32(d.byteOrder.Uint16(d.buf[off:])), a, 0xffff))
				off += 4
				if img, ok := dst.(*image.RGBA64); ok {
					img.SetRGBA64(x, y, color.RGBA64{g, g, g, uint16(a)})
				} else {
					dst.(*image.NRGBA64).SetNRGBA64(x, y, color.NRGBA64{g, g, g, uint16(a)})
				}
				continue
			}
			a := uint32(d.buf[off+1])
			g := uint8(grey(uint32(d.buf[off]), a, 0xff))
			off += 2
			if img, ok := dst.(*image.RGBA); ok {
				img.SetRGBA(x, y, color.RGBA{g, g, g, uint8(a)})
			} else {
				dst.(*image.NRGBA).SetNRGBA(x, y, color.NRGBA{g, g, g, uint8(a)})
			}
		}
	}
	return nil
}

// maxBytesPerPixel is the maximum possible bytes-per-pixel,
// used for conservative bounds checking.
const maxBytesPerPixel = 8

func newDecoder(r io.ReaderAt) (*decoder, error) { return newDecoderAt(r, -1) }

// newDecoderAt is newDecoder of the directory at ifd, or with ifd < 0 the
// first one, which the header gives.
func newDecoderAt(r io.ReaderAt, ifd int64) (*decoder, error) {
	d := &decoder{
		r:        r,
		features: make(map[int][]uint),
		ifd:      make(map[int][bigIFDLen]byte),
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
	case bigLEHeader:
		d.byteOrder, d.big = binary.LittleEndian, true
	case bigBEHeader:
		d.byteOrder, d.big = binary.BigEndian, true
	default:
		return nil, FormatError("malformed header")
	}

	// The place of the first directory, and how many entries it has: 4
	// bytes and 2, or in a BigTIFF (after the size of an offset, 8, and a
	// zero) 8 and 8.
	ifdOffset, numItems, head := int64(d.byteOrder.Uint32(p[4:8])), uint64(0), int64(2)
	var err error
	if d.big {
		if d.byteOrder.Uint16(p[4:6]) != 8 || d.byteOrder.Uint16(p[6:8]) != 0 {
			return nil, FormatError("malformed header")
		}
		if _, err := d.r.ReadAt(p, 8); err != nil {
			return nil, err
		}
		if ifdOffset = d.pointer(p); ifdOffset < 0 {
			return nil, FormatError("malformed header")
		}
		if ifd >= 0 {
			ifdOffset = ifd
		}
		if _, err := d.r.ReadAt(p, ifdOffset); err != nil {
			return nil, err
		}
		if numItems, head = d.byteOrder.Uint64(p), 8; numItems > 0xffff {
			return nil, FormatError("too many IFD entries")
		}
	} else {
		if ifd >= 0 {
			ifdOffset = ifd
		}
		if _, err := d.r.ReadAt(p[0:2], ifdOffset); err != nil {
			return nil, err
		}
		numItems = uint64(d.byteOrder.Uint16(p[0:2]))
	}

	// All IFD entries are read in one chunk.
	n := d.entryLen()
	p, err = safeReadAt(d.r, uint64(n)*numItems, ifdOffset+head)
	if err != nil {
		return nil, err
	}

	prevTag := -1
	for i := 0; i < len(p); i += n {
		tag, err := d.parseIFD(p[i : i+n])
		if err != nil {
			return nil, err
		}
		if tag <= prevTag {
			return nil, FormatError("tags are not sorted in ascending order")
		}
		prevTag = tag
	}
	d.exif = readExif(d.r, d.byteOrder, p, d.big)
	d.levelInfo(p, ifdOffset+head+int64(len(p)))

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
	if err := d.setupStored(); err != nil {
		return nil, err
	}
	d.bpp = d.firstVal(tBitsPerSample)
	switch d.bpp {
	case 0:
		return nil, FormatError("BitsPerSample must not be 0")
	case 1, 2, 4, 8, 16:
		// Nothing to do, these are accepted by this implementation (2 and
		// 4 bits, of greys and palettes, are a goimgtools addition).
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
	case pWhiteIsZero, pBlackIsZero:
		d.mode = mGray
		if d.firstVal(tPhotometricInterpretation) == pWhiteIsZero {
			d.mode = mGrayInvert
		}
		if d.bpp == 16 {
			d.config.ColorModel = color.Gray16Model
		} else {
			d.config.ColorModel = color.GrayModel
		}
		if len(d.features[tBitsPerSample]) == 2 {
			// goimgtools addition: grey with alpha, of 8 or 16 bits (as
			// Pillow and Photoshop write it); the alpha associated (the
			// grey multiplied by it) when ExtraSamples says so.
			if (d.bpp != 8 && d.bpp != 16) || d.features[tBitsPerSample][1] != d.bpp {
				return nil, UnsupportedError("grey with alpha of this depth")
			}
			d.grayAlpha, d.premult = true, d.firstVal(tExtraSamples) == 1
			switch {
			case d.bpp == 16 && d.premult:
				d.config.ColorModel = color.RGBA64Model
			case d.bpp == 16:
				d.config.ColorModel = color.NRGBA64Model
			case d.premult:
				d.config.ColorModel = color.RGBAModel
			default:
				d.config.ColorModel = color.NRGBAModel
			}
		}
	case pCMYK:
		// goimgtools addition: the four inks, 8 or 16 bits each (16 are
		// read as their high 8); what reads the image turns it into sRGB
		// through its profile.
		if n := len(d.features[tBitsPerSample]); n != 4 || (d.bpp != 8 && d.bpp != 16) {
			return nil, UnsupportedError("CMYK of this kind")
		}
		for _, b := range d.features[tBitsPerSample] {
			if b != d.bpp {
				return nil, FormatError("wrong number of bits for CMYK")
			}
		}
		if v := d.firstVal(tInkSet); v != 0 && v != 1 {
			return nil, UnsupportedError("inks that are not CMYK")
		}
		d.mode = mCMYK
		d.config.ColorModel = color.CMYKModel
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
	if p := d.firstVal(tPhotometricInterpretation); p != pRGB && p != pYCbCr && p != pCMYK && !d.grayAlpha {
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
