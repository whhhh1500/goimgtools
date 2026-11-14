// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package pngstream is Go's image/png decoder (go1.27.1) changed to emit the
// image as horizontal strips instead of one full-size image. Chunk parsing,
// filtering and the byte-to-pixel conversion are the standard library's code,
// unchanged, so the pixels are identical to png.Decode's.
//
// Changes from the standard library are limited to: the streaming entry point
// (Open / Next / Close), readRow (the body of readImagePass's row loop, now
// writing into the current strip), finish (the end-of-IDAT checks and the
// remaining chunks), and parseChunk stopping at the first IDAT.
// Interlaced (Adam7) images go through a spill buffer; see interlace.go.
package pngstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/klauspost/compress/zlib"
	"hash"
	"hash/crc32"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Color type, as per the PNG spec.
const (
	ctGrayscale      = 0
	ctTrueColor      = 2
	ctPaletted       = 3
	ctGrayscaleAlpha = 4
	ctTrueColorAlpha = 6
)

// A cb is a combination of color type and bit depth.
const (
	cbInvalid = iota
	cbG1
	cbG2
	cbG4
	cbG8
	cbGA8
	cbTC8
	cbP1
	cbP2
	cbP4
	cbP8
	cbTCA8
	cbG16
	cbGA16
	cbTC16
	cbTCA16
)

func cbPaletted(cb int) bool {
	return cbP1 <= cb && cb <= cbP8
}

func cbTrueColor(cb int) bool {
	return cb == cbTC8 || cb == cbTC16
}

// Filter type, as per the PNG spec.
const (
	ftNone    = 0
	ftSub     = 1
	ftUp      = 2
	ftAverage = 3
	ftPaeth   = 4
	nFilter   = 5
)

// Interlace type.
const (
	itNone  = 0
	itAdam7 = 1
)

// interlaceScan defines the placement and size of a pass for Adam7 interlacing.
type interlaceScan struct {
	xFactor, yFactor, xOffset, yOffset int
}

// interlacing defines Adam7 interlacing, with 7 passes of reduced images.
// See https://www.w3.org/TR/PNG/#8Interlace
var interlacing = []interlaceScan{
	{8, 8, 0, 0},
	{8, 8, 4, 0},
	{4, 8, 0, 4},
	{4, 4, 2, 0},
	{2, 4, 0, 2},
	{2, 2, 1, 0},
	{1, 2, 0, 1},
}

// Decoding stage.
// The PNG specification says that the IHDR, PLTE (if present), tRNS (if
// present), IDAT and IEND chunks must appear in that order. There may be
// multiple IDAT chunks, and IDAT chunks must be sequential (i.e. they may not
// have any other chunks between them).
// https://www.w3.org/TR/PNG/#5ChunkOrdering
const (
	dsStart = iota
	dsSeenIHDR
	dsSeenPLTE
	dsSeentRNS
	dsSeenIDAT
	dsSeenIEND
)

const pngHeader = "\x89PNG\r\n\x1a\n"

type decoder struct {
	r             io.Reader
	img           image.Image
	crc           hash.Hash32
	width, height int
	depth         int
	palette       color.Palette
	cb            int
	stage         int
	idatLength    uint32
	meta          pipeline.Meta
	tmp           [3 * 256]byte
	interlace     int

	// useTransparent and transparent are used for grayscale and truecolor
	// transparency, as opposed to palette transparency.
	useTransparent bool
	transparent    [6]byte
}

// A FormatError reports that the input is not a valid PNG.
type FormatError string

func (e FormatError) Error() string { return "png: invalid format: " + string(e) }

var chunkOrderError = FormatError("chunk out of order")

// An UnsupportedError reports that the input uses a valid but unimplemented PNG feature.
type UnsupportedError string

func (e UnsupportedError) Error() string { return "png: unsupported feature: " + string(e) }

func (d *decoder) parseIHDR(length uint32) error {
	if length != 13 {
		return FormatError("bad IHDR length")
	}
	if _, err := io.ReadFull(d.r, d.tmp[:13]); err != nil {
		return err
	}
	d.crc.Write(d.tmp[:13])
	if d.tmp[10] != 0 {
		return UnsupportedError("compression method")
	}
	if d.tmp[11] != 0 {
		return UnsupportedError("filter method")
	}
	if d.tmp[12] != itNone && d.tmp[12] != itAdam7 {
		return FormatError("invalid interlace method")
	}
	d.interlace = int(d.tmp[12])

	w := int32(binary.BigEndian.Uint32(d.tmp[0:4]))
	h := int32(binary.BigEndian.Uint32(d.tmp[4:8]))
	if w <= 0 || h <= 0 {
		return FormatError("non-positive dimension")
	}
	nPixels64 := int64(w) * int64(h)
	nPixels := int(nPixels64)
	if nPixels64 != int64(nPixels) {
		return UnsupportedError("dimension overflow")
	}
	// There can be up to 8 bytes per pixel, for 16 bits per channel RGBA.
	if nPixels != (nPixels*8)/8 {
		return UnsupportedError("dimension overflow")
	}

	d.cb = cbInvalid
	d.depth = int(d.tmp[8])
	switch d.depth {
	case 1:
		switch d.tmp[9] {
		case ctGrayscale:
			d.cb = cbG1
		case ctPaletted:
			d.cb = cbP1
		}
	case 2:
		switch d.tmp[9] {
		case ctGrayscale:
			d.cb = cbG2
		case ctPaletted:
			d.cb = cbP2
		}
	case 4:
		switch d.tmp[9] {
		case ctGrayscale:
			d.cb = cbG4
		case ctPaletted:
			d.cb = cbP4
		}
	case 8:
		switch d.tmp[9] {
		case ctGrayscale:
			d.cb = cbG8
		case ctTrueColor:
			d.cb = cbTC8
		case ctPaletted:
			d.cb = cbP8
		case ctGrayscaleAlpha:
			d.cb = cbGA8
		case ctTrueColorAlpha:
			d.cb = cbTCA8
		}
	case 16:
		switch d.tmp[9] {
		case ctGrayscale:
			d.cb = cbG16
		case ctTrueColor:
			d.cb = cbTC16
		case ctGrayscaleAlpha:
			d.cb = cbGA16
		case ctTrueColorAlpha:
			d.cb = cbTCA16
		}
	}
	if d.cb == cbInvalid {
		return UnsupportedError(fmt.Sprintf("bit depth %d, color type %d", d.tmp[8], d.tmp[9]))
	}
	d.width, d.height = int(w), int(h)
	return d.verifyChecksum()
}

func (d *decoder) parsePLTE(length uint32) error {
	np := int(length / 3) // The number of palette entries.
	if length%3 != 0 || np <= 0 || np > 256 || np > 1<<uint(d.depth) {
		return FormatError("bad PLTE length")
	}
	n, err := io.ReadFull(d.r, d.tmp[:3*np])
	if err != nil {
		return err
	}
	d.crc.Write(d.tmp[:n])
	switch d.cb {
	case cbP1, cbP2, cbP4, cbP8:
		d.palette = make(color.Palette, 256)
		for i := 0; i < np; i++ {
			d.palette[i] = color.RGBA{d.tmp[3*i+0], d.tmp[3*i+1], d.tmp[3*i+2], 0xff}
		}
		for i := np; i < 256; i++ {
			// Initialize the rest of the palette to opaque black. The spec (section
			// 11.2.3) says that "any out-of-range pixel value found in the image data
			// is an error", but some real-world PNG files have out-of-range pixel
			// values. We fall back to opaque black, the same as libpng 1.5.13;
			// ImageMagick 6.5.7 returns an error.
			d.palette[i] = color.RGBA{0x00, 0x00, 0x00, 0xff}
		}
		d.palette = d.palette[:np]
	case cbTC8, cbTCA8, cbTC16, cbTCA16:
		// As per the PNG spec, a PLTE chunk is optional (and for practical purposes,
		// ignorable) for the ctTrueColor and ctTrueColorAlpha color types (section 4.1.2).
	default:
		return FormatError("PLTE, color type mismatch")
	}
	return d.verifyChecksum()
}

func (d *decoder) parsetRNS(length uint32) error {
	switch d.cb {
	case cbG1, cbG2, cbG4, cbG8, cbG16:
		if length != 2 {
			return FormatError("bad tRNS length")
		}
		n, err := io.ReadFull(d.r, d.tmp[:length])
		if err != nil {
			return err
		}
		d.crc.Write(d.tmp[:n])

		copy(d.transparent[:], d.tmp[:length])
		switch d.cb {
		case cbG1:
			d.transparent[1] *= 0xff
		case cbG2:
			d.transparent[1] *= 0x55
		case cbG4:
			d.transparent[1] *= 0x11
		}
		d.useTransparent = true

	case cbTC8, cbTC16:
		if length != 6 {
			return FormatError("bad tRNS length")
		}
		n, err := io.ReadFull(d.r, d.tmp[:length])
		if err != nil {
			return err
		}
		d.crc.Write(d.tmp[:n])

		copy(d.transparent[:], d.tmp[:length])
		d.useTransparent = true

	case cbP1, cbP2, cbP4, cbP8:
		if length > 256 {
			return FormatError("bad tRNS length")
		}
		n, err := io.ReadFull(d.r, d.tmp[:length])
		if err != nil {
			return err
		}
		d.crc.Write(d.tmp[:n])

		if len(d.palette) < n {
			d.palette = d.palette[:n]
		}
		for i := 0; i < n; i++ {
			rgba := d.palette[i].(color.RGBA)
			d.palette[i] = color.NRGBA{rgba.R, rgba.G, rgba.B, d.tmp[i]}
		}

	default:
		return FormatError("tRNS, color type mismatch")
	}
	return d.verifyChecksum()
}

// Read presents one or more IDAT chunks as one continuous stream (minus the
// intermediate chunk headers and footers). If the PNG data looked like:
//
//	... len0 IDAT xxx crc0 len1 IDAT yy crc1 len2 IEND crc2
//
// then this reader presents xxxyy. For well-formed PNG data, the decoder state
// immediately before the first Read call is that d.r is positioned between the
// first IDAT and xxx, and the decoder state immediately after the last Read
// call is that d.r is positioned between yy and crc1.
func (d *decoder) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for d.idatLength == 0 {
		// We have exhausted an IDAT chunk. Verify the checksum of that chunk.
		if err := d.verifyChecksum(); err != nil {
			return 0, err
		}
		// Read the length and chunk type of the next chunk, and check that
		// it is an IDAT chunk.
		if _, err := io.ReadFull(d.r, d.tmp[:8]); err != nil {
			return 0, err
		}
		d.idatLength = binary.BigEndian.Uint32(d.tmp[:4])
		if string(d.tmp[4:8]) != "IDAT" {
			return 0, FormatError("not enough pixel data")
		}
		d.crc.Reset()
		d.crc.Write(d.tmp[4:8])
	}
	if int(d.idatLength) < 0 {
		return 0, UnsupportedError("IDAT chunk length overflow")
	}
	n, err := d.r.Read(p[:min(len(p), int(d.idatLength))])
	d.crc.Write(p[:n])
	d.idatLength -= uint32(n)
	return n, err
}

// errAtIDAT stops chunk parsing at the first IDAT chunk header.
var errAtIDAT = errors.New("goimgtools: png: at first IDAT")

type stream struct {
	d        *decoder
	env      *pipeline.Env
	zr       io.ReadCloser
	band     image.Image // reusable strip buffer, n rows
	n        int
	y        int
	cr, pr   []uint8
	bpp      int // bytes per pixel, for filtering
	reserved int64
	err      error
	done     bool
	rw       int    // width of the rows readRow decodes (a pass's width when interlaced)
	ilace    *adam7 // non-nil for an interlaced image
}

// Open reads the PNG header chunks up to the first IDAT and returns a Source
// that decodes the pixel rows on demand. env may be nil.
func Open(r io.Reader, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	d := &decoder{r: r, crc: crc32.NewIEEE()}
	if err := d.checkHeader(); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	for {
		err := d.parseChunk(true)
		if err == errAtIDAT {
			break
		}
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
	s := &stream{d: d, env: env, rw: d.width}
	bitsPerPixel := s.bitsPerPixel()
	s.bpp = (bitsPerPixel + 7) / 8
	// The +1 is for the per-row filter type, which is at cr[0].
	rowSize := 1 + (int64(bitsPerPixel)*int64(d.width)+7)/8
	if rowSize != int64(int(rowSize)) {
		return nil, UnsupportedError("dimension overflow")
	}
	proto := s.newImage(image.Rect(0, 0, 1, 1))
	s.n = pipeline.StripRows(pipeline.BandBytes(proto, image.Rect(0, 0, d.width, 1)), d.height)
	full := image.Rect(0, 0, d.width, s.n)
	need := pipeline.BandBytes(proto, full) + 2*rowSize
	if d.interlace == itAdam7 {
		need += adam7Extra(proto, d.width)
	}
	if err := env.Budget.Reserve(need, "png row buffers"); err != nil {
		return nil, err
	}
	s.reserved = need
	s.band = s.newImage(full)
	s.cr = make([]uint8, rowSize)
	s.pr = make([]uint8, rowSize)
	zr, err := zlib.NewReader(d)
	if err != nil {
		s.Close()
		return nil, err
	}
	s.zr = zr
	if d.interlace == itAdam7 {
		s.ilace = newAdam7(s, proto)
	}
	return s, nil
}

func (s *stream) Env() *pipeline.Env { return s.env }

// Meta returns the colour profile and EXIF block found ahead of the pixels.
func (s *stream) Meta() pipeline.Meta     { return s.d.meta }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.d.width, s.d.height) }
func (s *stream) ColorModel() color.Model {
	if p, ok := s.band.(*image.Paletted); ok {
		return p.Palette
	}
	return s.band.ColorModel()
}

func (s *stream) Close() error {
	if s.zr != nil {
		s.zr.Close()
		s.zr = nil
	}
	if s.ilace != nil {
		s.ilace.spill.Close()
	}
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	if s.err == nil {
		s.err = errors.New("goimgtools: png: closed")
	}
	return nil
}

// Next decodes the next strip. The final strip is returned only after the
// zlib checksum, the IDAT CRCs and every remaining chunk up to IEND verify.
func (s *stream) Next() (image.Image, error) {
	if s.done {
		return nil, io.EOF
	}
	if s.err != nil {
		return nil, s.err
	}
	d := s.d
	y0, y1 := s.y, min(s.y+s.n, d.height)
	strip := pipeline.Relabel(s.band, image.Rect(0, y0, d.width, y1))
	if s.ilace != nil {
		if err := s.ilace.next(s, strip, y0, y1); err != nil {
			s.err = err
			return nil, err
		}
		s.y = y1
		s.done = y1 == d.height
		return strip, nil
	}
	for y := y0; y < y1; y++ {
		if err := s.readRow(strip, y); err != nil {
			s.err = err
			return nil, err
		}
	}
	if p, ok := strip.(*image.Paletted); ok {
		s.band.(*image.Paletted).Palette = p.Palette // keep palette growth
	}
	s.y = y1
	if y1 == d.height {
		if err := s.finish(); err != nil {
			s.err = err
			return nil, err
		}
		s.done = true
	}
	return strip, nil
}

func (s *stream) bitsPerPixel() int {
	d := s.d
	switch d.cb {
	case cbG1, cbG2, cbG4, cbG8:
		return d.depth
	case cbGA8:
		return 16
	case cbTC8:
		return 24
	case cbP1, cbP2, cbP4, cbP8:
		return d.depth
	case cbTCA8:
		return 32
	case cbG16:
		return 16
	case cbGA16:
		return 32
	case cbTC16:
		return 48
	case cbTCA16:
		return 64
	}
	return 0
}

// newImage allocates an image of the type readImagePass would use for d.cb.
func (s *stream) newImage(r image.Rectangle) image.Image {
	d := s.d
	switch d.cb {
	case cbG1, cbG2, cbG4, cbG8:
		if d.useTransparent {
			return image.NewNRGBA(r)
		}
		return image.NewGray(r)
	case cbGA8:
		return image.NewNRGBA(r)
	case cbTC8:
		if d.useTransparent {
			return image.NewNRGBA(r)
		}
		return image.NewRGBA(r)
	case cbP1, cbP2, cbP4, cbP8:
		return image.NewPaletted(r, d.palette)
	case cbTCA8:
		return image.NewNRGBA(r)
	case cbG16:
		if d.useTransparent {
			return image.NewNRGBA64(r)
		}
		return image.NewGray16(r)
	case cbGA16:
		return image.NewNRGBA64(r)
	case cbTC16:
		if d.useTransparent {
			return image.NewNRGBA64(r)
		}
		return image.NewRGBA64(r)
	case cbTCA16:
		return image.NewNRGBA64(r)
	}
	return nil
}

// readRow is the body of the standard library's readImagePass row loop,
// writing row y into the strip img.
func (s *stream) readRow(img image.Image, y int) error {
	d := s.d
	width := s.rw
	bytesPerPixel := s.bpp
	gray, _ := img.(*image.Gray)
	rgba, _ := img.(*image.RGBA)
	paletted, _ := img.(*image.Paletted)
	nrgba, _ := img.(*image.NRGBA)
	gray16, _ := img.(*image.Gray16)
	rgba64, _ := img.(*image.RGBA64)
	nrgba64, _ := img.(*image.NRGBA64)
	pixOffset := 0
	switch m := img.(type) {
	case *image.Gray:
		pixOffset = (y - m.Rect.Min.Y) * m.Stride
	case *image.RGBA:
		pixOffset = (y - m.Rect.Min.Y) * m.Stride
	case *image.Paletted:
		pixOffset = (y - m.Rect.Min.Y) * m.Stride
	case *image.NRGBA:
		pixOffset = (y - m.Rect.Min.Y) * m.Stride
	}
	cr, pr := s.cr, s.pr
	r := s.zr
	{
		_, err := io.ReadFull(r, cr)
		if err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return FormatError("not enough pixel data")
			}
			return err
		}

		// Apply the filter.
		cdat := cr[1:]
		pdat := pr[1:]
		switch cr[0] {
		case ftNone:
			// No-op.
		case ftSub:
			for i := bytesPerPixel; i < len(cdat); i++ {
				cdat[i] += cdat[i-bytesPerPixel]
			}
		case ftUp:
			for i, p := range pdat {
				cdat[i] += p
			}
		case ftAverage:
			// The first column has no column to the left of it, so it is a
			// special case. We know that the first column exists because we
			// check above that width != 0, and so len(cdat) != 0.
			for i := 0; i < bytesPerPixel; i++ {
				cdat[i] += pdat[i] / 2
			}
			for i := bytesPerPixel; i < len(cdat); i++ {
				cdat[i] += uint8((int(cdat[i-bytesPerPixel]) + int(pdat[i])) / 2)
			}
		case ftPaeth:
			filterPaeth(cdat, pdat, bytesPerPixel)
		default:
			return FormatError("bad filter type")
		}

		// Convert from bytes to colors.
		switch d.cb {
		case cbG1:
			if d.useTransparent {
				ty := d.transparent[1]
				for x := 0; x < width; x += 8 {
					b := cdat[x/8]
					for x2 := 0; x2 < 8 && x+x2 < width; x2++ {
						ycol := (b >> 7) * 0xff
						acol := uint8(0xff)
						if ycol == ty {
							acol = 0x00
						}
						nrgba.SetNRGBA(x+x2, y, color.NRGBA{ycol, ycol, ycol, acol})
						b <<= 1
					}
				}
			} else {
				for x := 0; x < width; x += 8 {
					b := cdat[x/8]
					for x2 := 0; x2 < 8 && x+x2 < width; x2++ {
						gray.SetGray(x+x2, y, color.Gray{(b >> 7) * 0xff})
						b <<= 1
					}
				}
			}
		case cbG2:
			if d.useTransparent {
				ty := d.transparent[1]
				for x := 0; x < width; x += 4 {
					b := cdat[x/4]
					for x2 := 0; x2 < 4 && x+x2 < width; x2++ {
						ycol := (b >> 6) * 0x55
						acol := uint8(0xff)
						if ycol == ty {
							acol = 0x00
						}
						nrgba.SetNRGBA(x+x2, y, color.NRGBA{ycol, ycol, ycol, acol})
						b <<= 2
					}
				}
			} else {
				for x := 0; x < width; x += 4 {
					b := cdat[x/4]
					for x2 := 0; x2 < 4 && x+x2 < width; x2++ {
						gray.SetGray(x+x2, y, color.Gray{(b >> 6) * 0x55})
						b <<= 2
					}
				}
			}
		case cbG4:
			if d.useTransparent {
				ty := d.transparent[1]
				for x := 0; x < width; x += 2 {
					b := cdat[x/2]
					for x2 := 0; x2 < 2 && x+x2 < width; x2++ {
						ycol := (b >> 4) * 0x11
						acol := uint8(0xff)
						if ycol == ty {
							acol = 0x00
						}
						nrgba.SetNRGBA(x+x2, y, color.NRGBA{ycol, ycol, ycol, acol})
						b <<= 4
					}
				}
			} else {
				for x := 0; x < width; x += 2 {
					b := cdat[x/2]
					for x2 := 0; x2 < 2 && x+x2 < width; x2++ {
						gray.SetGray(x+x2, y, color.Gray{(b >> 4) * 0x11})
						b <<= 4
					}
				}
			}
		case cbG8:
			if d.useTransparent {
				ty := d.transparent[1]
				for x := 0; x < width; x++ {
					ycol := cdat[x]
					acol := uint8(0xff)
					if ycol == ty {
						acol = 0x00
					}
					nrgba.SetNRGBA(x, y, color.NRGBA{ycol, ycol, ycol, acol})
				}
			} else {
				copy(gray.Pix[pixOffset:], cdat)
				pixOffset += gray.Stride
			}
		case cbGA8:
			for x := 0; x < width; x++ {
				ycol := cdat[2*x+0]
				nrgba.SetNRGBA(x, y, color.NRGBA{ycol, ycol, ycol, cdat[2*x+1]})
			}
		case cbTC8:
			if d.useTransparent {
				pix, i, j := nrgba.Pix, pixOffset, 0
				tr, tg, tb := d.transparent[1], d.transparent[3], d.transparent[5]
				for x := 0; x < width; x++ {
					r := cdat[j+0]
					g := cdat[j+1]
					b := cdat[j+2]
					a := uint8(0xff)
					if r == tr && g == tg && b == tb {
						a = 0x00
					}
					pix[i+0] = r
					pix[i+1] = g
					pix[i+2] = b
					pix[i+3] = a
					i += 4
					j += 3
				}
				pixOffset += nrgba.Stride
			} else {
				pix, i, j := rgba.Pix, pixOffset, 0
				for x := 0; x < width; x++ {
					pix[i+0] = cdat[j+0]
					pix[i+1] = cdat[j+1]
					pix[i+2] = cdat[j+2]
					pix[i+3] = 0xff
					i += 4
					j += 3
				}
				pixOffset += rgba.Stride
			}
		case cbP1:
			for x := 0; x < width; x += 8 {
				b := cdat[x/8]
				for x2 := 0; x2 < 8 && x+x2 < width; x2++ {
					idx := b >> 7
					if len(paletted.Palette) <= int(idx) {
						paletted.Palette = paletted.Palette[:int(idx)+1]
					}
					paletted.SetColorIndex(x+x2, y, idx)
					b <<= 1
				}
			}
		case cbP2:
			for x := 0; x < width; x += 4 {
				b := cdat[x/4]
				for x2 := 0; x2 < 4 && x+x2 < width; x2++ {
					idx := b >> 6
					if len(paletted.Palette) <= int(idx) {
						paletted.Palette = paletted.Palette[:int(idx)+1]
					}
					paletted.SetColorIndex(x+x2, y, idx)
					b <<= 2
				}
			}
		case cbP4:
			for x := 0; x < width; x += 2 {
				b := cdat[x/2]
				for x2 := 0; x2 < 2 && x+x2 < width; x2++ {
					idx := b >> 4
					if len(paletted.Palette) <= int(idx) {
						paletted.Palette = paletted.Palette[:int(idx)+1]
					}
					paletted.SetColorIndex(x+x2, y, idx)
					b <<= 4
				}
			}
		case cbP8:
			if len(paletted.Palette) != 256 {
				for x := 0; x < width; x++ {
					if len(paletted.Palette) <= int(cdat[x]) {
						paletted.Palette = paletted.Palette[:int(cdat[x])+1]
					}
				}
			}
			copy(paletted.Pix[pixOffset:], cdat)
			pixOffset += paletted.Stride
		case cbTCA8:
			copy(nrgba.Pix[pixOffset:], cdat)
			pixOffset += nrgba.Stride
		case cbG16:
			if d.useTransparent {
				ty := uint16(d.transparent[0])<<8 | uint16(d.transparent[1])
				for x := 0; x < width; x++ {
					ycol := uint16(cdat[2*x+0])<<8 | uint16(cdat[2*x+1])
					acol := uint16(0xffff)
					if ycol == ty {
						acol = 0x0000
					}
					nrgba64.SetNRGBA64(x, y, color.NRGBA64{ycol, ycol, ycol, acol})
				}
			} else {
				for x := 0; x < width; x++ {
					ycol := uint16(cdat[2*x+0])<<8 | uint16(cdat[2*x+1])
					gray16.SetGray16(x, y, color.Gray16{ycol})
				}
			}
		case cbGA16:
			for x := 0; x < width; x++ {
				ycol := uint16(cdat[4*x+0])<<8 | uint16(cdat[4*x+1])
				acol := uint16(cdat[4*x+2])<<8 | uint16(cdat[4*x+3])
				nrgba64.SetNRGBA64(x, y, color.NRGBA64{ycol, ycol, ycol, acol})
			}
		case cbTC16:
			if d.useTransparent {
				tr := uint16(d.transparent[0])<<8 | uint16(d.transparent[1])
				tg := uint16(d.transparent[2])<<8 | uint16(d.transparent[3])
				tb := uint16(d.transparent[4])<<8 | uint16(d.transparent[5])
				for x := 0; x < width; x++ {
					rcol := uint16(cdat[6*x+0])<<8 | uint16(cdat[6*x+1])
					gcol := uint16(cdat[6*x+2])<<8 | uint16(cdat[6*x+3])
					bcol := uint16(cdat[6*x+4])<<8 | uint16(cdat[6*x+5])
					acol := uint16(0xffff)
					if rcol == tr && gcol == tg && bcol == tb {
						acol = 0x0000
					}
					nrgba64.SetNRGBA64(x, y, color.NRGBA64{rcol, gcol, bcol, acol})
				}
			} else {
				for x := 0; x < width; x++ {
					rcol := uint16(cdat[6*x+0])<<8 | uint16(cdat[6*x+1])
					gcol := uint16(cdat[6*x+2])<<8 | uint16(cdat[6*x+3])
					bcol := uint16(cdat[6*x+4])<<8 | uint16(cdat[6*x+5])
					rgba64.SetRGBA64(x, y, color.RGBA64{rcol, gcol, bcol, 0xffff})
				}
			}
		case cbTCA16:
			for x := 0; x < width; x++ {
				rcol := uint16(cdat[8*x+0])<<8 | uint16(cdat[8*x+1])
				gcol := uint16(cdat[8*x+2])<<8 | uint16(cdat[8*x+3])
				bcol := uint16(cdat[8*x+4])<<8 | uint16(cdat[8*x+5])
				acol := uint16(cdat[8*x+6])<<8 | uint16(cdat[8*x+7])
				nrgba64.SetNRGBA64(x, y, color.NRGBA64{rcol, gcol, bcol, acol})
			}
		}
	}
	// The current row for y is the previous row for y+1.
	s.pr, s.cr = cr, pr
	_, _, _ = gray16, rgba64, nrgba64
	return nil
}

// finish runs the checks png.Decode performs after the last row: zlib EOF
// and checksum, no extra pixel data, the IDAT CRC, then every chunk to IEND.
func (s *stream) finish() error {
	d := s.d
	r := s.zr
	// Check for EOF, to verify the zlib checksum.
	var err error
	n := 0
	for i := 0; n == 0 && err == nil; i++ {
		if i == 100 {
			return io.ErrNoProgress
		}
		n, err = r.Read(d.tmp[:1])
	}
	if err != nil && err != io.EOF {
		return FormatError(err.Error())
	}
	if n != 0 || d.idatLength != 0 {
		return FormatError("too much pixel data")
	}
	if err := d.verifyChecksum(); err != nil { // as parseIDAT does
		return err
	}
	for d.stage != dsSeenIEND {
		if err := d.parseChunk(false); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
	}
	return nil
}

func (d *decoder) parseChunk(atStart bool) error {
	_ = atStart
	// Read the length and chunk type.
	if _, err := io.ReadFull(d.r, d.tmp[:8]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(d.tmp[:4])
	d.crc.Reset()
	d.crc.Write(d.tmp[4:8])

	// Read the chunk data.
	switch string(d.tmp[4:8]) {
	case "IHDR":
		if d.stage != dsStart {
			return chunkOrderError
		}
		d.stage = dsSeenIHDR
		return d.parseIHDR(length)
	case "PLTE":
		if d.stage != dsSeenIHDR {
			return chunkOrderError
		}
		d.stage = dsSeenPLTE
		return d.parsePLTE(length)
	case "tRNS":
		if cbPaletted(d.cb) {
			if d.stage != dsSeenPLTE {
				return chunkOrderError
			}
		} else if cbTrueColor(d.cb) {
			if d.stage != dsSeenIHDR && d.stage != dsSeenPLTE {
				return chunkOrderError
			}
		} else if d.stage != dsSeenIHDR {
			return chunkOrderError
		}
		d.stage = dsSeentRNS
		return d.parsetRNS(length)
	case "iCCP", "eXIf":
		// goimgtools addition: keep the colour profile and the EXIF block
		// (the standard library skips both). Only ahead of the pixels.
		if d.stage >= dsSeenIHDR && d.stage < dsSeenIDAT && length <= pipeline.MaxMetaBytes {
			return d.parseMeta(string(d.tmp[4:8]), length)
		}
	case "IDAT":
		if d.stage < dsSeenIHDR || d.stage > dsSeenIDAT || (d.stage == dsSeenIHDR && cbPaletted(d.cb)) {
			return chunkOrderError
		} else if d.stage == dsSeenIDAT {
			// Ignore trailing zero-length or garbage IDAT chunks.
			//
			// This does not affect valid PNG images that contain multiple IDAT
			// chunks, since the first call to parseIDAT below will consume all
			// consecutive IDAT chunks required for decoding the image.
			break
		}
		d.stage = dsSeenIDAT
		// pngstream: stop here; Next reads the IDAT data via d.Read.
		d.idatLength = length
		return errAtIDAT
	case "IEND":
		if d.stage != dsSeenIDAT {
			return chunkOrderError
		}
		d.stage = dsSeenIEND
		return d.parseIEND(length)
	}
	if length > 0x7fffffff {
		return FormatError(fmt.Sprintf("Bad chunk length: %d", length))
	}
	// Ignore this chunk (of a known length).
	var ignored [4096]byte
	for length > 0 {
		n, err := io.ReadFull(d.r, ignored[:min(len(ignored), int(length))])
		if err != nil {
			return err
		}
		d.crc.Write(ignored[:n])
		length -= uint32(n)
	}
	return d.verifyChecksum()
}

func (d *decoder) verifyChecksum() error {
	if _, err := io.ReadFull(d.r, d.tmp[:4]); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(d.tmp[:4]) != d.crc.Sum32() {
		return FormatError("invalid checksum")
	}
	return nil
}

func (d *decoder) checkHeader() error {
	_, err := io.ReadFull(d.r, d.tmp[:len(pngHeader)])
	if err != nil {
		return err
	}
	if string(d.tmp[:len(pngHeader)]) != pngHeader {
		return FormatError("not a PNG file")
	}
	return nil
}
func (d *decoder) parseIEND(length uint32) error {
	if length != 0 {
		return FormatError("bad IEND length")
	}
	return d.verifyChecksum()
}

// Opaque reports whether the PNG has no alpha channel and no tRNS chunk.
// For a paletted image the tRNS chunk gives single palette entries their
// transparency, so it is opaque when every entry is (a pixel that points
// past the end of the palette is opaque black).
func (s *stream) Opaque() bool {
	switch s.d.cb {
	case cbG1, cbG2, cbG4, cbG8, cbG16, cbTC8, cbTC16:
		return !s.d.useTransparent
	case cbP1, cbP2, cbP4, cbP8:
		for _, c := range s.d.palette {
			if _, _, _, a := c.RGBA(); a != 0xffff {
				return false
			}
		}
		return true
	}
	return false
}

// parseMeta reads an iCCP or eXIf chunk. A profile that does not decompress
// is dropped; it never makes the image fail, as the standard library, which
// skips these chunks, would not fail either.
func (d *decoder) parseMeta(name string, length uint32) error {
	buf := make([]byte, length)
	if _, err := io.ReadFull(d.r, buf); err != nil {
		return err
	}
	d.crc.Write(buf)
	if err := d.verifyChecksum(); err != nil {
		return err
	}
	if name == "eXIf" {
		if d.meta.Exif == nil && len(buf) > 0 {
			d.meta.Exif = buf
		}
		return nil
	}
	// iCCP: profile name, a zero byte, the compression method (0), zlib data.
	i := bytes.IndexByte(buf, 0)
	if d.meta.ICC != nil || i < 1 || i > 79 || i+2 > len(buf) || buf[i+1] != 0 {
		return nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(buf[i+2:]))
	if err != nil {
		return nil
	}
	defer zr.Close()
	icc, err := io.ReadAll(io.LimitReader(zr, pipeline.MaxMetaBytes+1))
	if err == nil && len(icc) > 0 && len(icc) <= pipeline.MaxMetaBytes {
		d.meta.ICC = icc
	}
	return nil
}
