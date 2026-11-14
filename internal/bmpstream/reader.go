// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-GO file.

// Package bmpstream is the golang.org/x/image/bmp decoder (v0.47.0) changed
// to emit the image as horizontal strips instead of one full-size image.
// Header parsing (decodeConfig) and the byte-to-pixel conversion of each row
// are x/image's code, unchanged, so the image types and pixels are identical
// to bmp.Decode's.
//
// Changes from x/image are limited to: the streaming entry point (Open /
// Next / Close), and decodePaletted, decodeRGB and decodeNRGBA being reduced
// to the body of their row loops (palettedRow, rgbRow, nrgbaRow), which now
// write into the current strip. BMP rows are usually stored bottom-up, so
// the rows of a strip are read from their file offset through an
// io.ReaderAt, one read per strip, instead of sequentially.
package bmpstream

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math/bits"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ErrUnsupported means that the input BMP image uses a valid but unsupported
// feature.
var ErrUnsupported = errors.New("bmp: unsupported BMP image")

var errInvalidPaletteIndex = errors.New("bmp: invalid palette index")

func readUint16(b []byte) uint16 {
	return uint16(b[0]) | uint16(b[1])<<8
}

func readUint32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// mul3 returns (x * y * z), unless at least one argument is negative or if the
// computation overflows the int type. It is a copy of
// golang.org/x/image/internal/safemath.Mul3.
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

// palettedRow converts one row of a 1, 2, 4 or 8 bit-per-pixel BMP image
// from b, the row as stored in the file, to p. It is the body of the row
// loop of x/image's decodePaletted.
func palettedRow(p, b []byte, width, bpp int, palette color.Palette) error {
	byteIndex, bitIndex, mask := 0, 8, byte((1<<bpp)-1)
	for pixIndex := 0; pixIndex < width; pixIndex++ {
		bitIndex -= bpp
		paletteIndex := (b[byteIndex]) >> bitIndex & mask
		if int(paletteIndex) >= len(palette) {
			return errInvalidPaletteIndex
		}
		p[pixIndex] = paletteIndex
		if bitIndex == 0 {
			byteIndex++
			bitIndex = 8
		}
	}
	return nil
}

// rgbRow converts one row of a 24 bit-per-pixel BMP image from b to p. It
// is the body of the row loop of x/image's decodeRGB.
func rgbRow(p, b []byte) {
	for i, j := 0, 0; i < len(p); i, j = i+4, j+3 {
		// BMP images are stored in BGR order rather than RGB order.
		p[i+0] = b[j+2]
		p[i+1] = b[j+1]
		p[i+2] = b[j+0]
		p[i+3] = 0xFF
	}
}

// nrgbaRow converts one row of a 32 bit-per-pixel BMP image from b to p. It
// is the body of the row loop of x/image's decodeNRGBA, which reads the row
// into p and converts it in place.
func nrgbaRow(p, b []byte, allowAlpha bool) {
	copy(p, b)
	for i := 0; i < len(p); i += 4 {
		// BMP images are stored in BGRA order rather than RGBA order.
		p[i+0], p[i+2] = p[i+2], p[i+0]
		if !allowAlpha {
			p[i+3] = 0xFF
		}
	}
}

// stream is a BMP image decoded strip by strip.
type stream struct {
	r   io.ReaderAt
	env *pipeline.Env

	c          image.Config
	bpp        int
	topDown    bool
	allowAlpha bool

	dataOff     int64 // file offset of the first stored row
	bytesPerRow int64 // size of a stored row, padding included

	band     image.Image // reusable strip buffer, n rows
	raw      []byte      // the stored rows of one strip
	n        int         // rows per strip
	y        int         // next output row
	reserved int64

	err  error
	done bool
}

// Open reads the BMP headers from r, a file of size bytes, and returns a
// Source that decodes the pixel rows on demand, exactly as bmp.Decode would.
// A file too short to hold every row is rejected here, before any pixel is
// produced. env may be nil.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	sr := io.NewSectionReader(r, 0, size)
	c, bpp, topDown, allowAlpha, err := decodeConfig(sr)
	if err != nil {
		return nil, err
	}
	// decodeConfig only accepts files whose pixel data follows the headers
	// directly, which is where bmp.Decode goes on reading.
	dataOff, err := sr.Seek(0, io.SeekCurrent)
	if err != nil {
		return nil, err
	}
	s := &stream{r: r, env: env, c: c, bpp: bpp, topDown: topDown, allowAlpha: allowAlpha, dataOff: dataOff}
	width := int64(c.Width)
	var outPerRow int64
	switch bpp {
	case 1, 2, 4, 8:
		pixelsPerByte := int64(8 / bpp)
		// Pad up to ensure each row is 4-bytes aligned.
		s.bytesPerRow = ((width+pixelsPerByte-1)/pixelsPerByte + 3) &^ 3
		outPerRow = width
	case 24:
		// There are 3 bytes per pixel, and each row is 4-byte aligned.
		s.bytesPerRow = (3*width + 3) &^ 3
		outPerRow = 4 * width
	case 32:
		s.bytesPerRow = 4 * width
		outPerRow = 4 * width
	default:
		panic("unreachable")
	}
	// bmp.Decode fails with a read error when a row is missing. The top
	// row of a bottom-up file is the last one stored, so this is checked
	// before anything is decoded.
	if s.bytesPerRow > (size-dataOff)/int64(max(c.Height, 1)) {
		return nil, io.ErrUnexpectedEOF
	}
	s.n = pipeline.StripRows(outPerRow, c.Height)
	need := int64(s.n) * (outPerRow + s.bytesPerRow)
	if err := env.Budget.Reserve(need, "bmp row buffers"); err != nil {
		return nil, err
	}
	s.reserved = need
	full := image.Rect(0, 0, c.Width, min(s.n, c.Height))
	switch bpp {
	case 1, 2, 4, 8:
		s.band = image.NewPaletted(full, c.ColorModel.(color.Palette))
	case 24:
		s.band = image.NewRGBA(full)
	case 32:
		s.band = image.NewNRGBA(full)
	}
	s.raw = make([]byte, int64(full.Dy())*s.bytesPerRow)
	return s, nil
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.c.Width, s.c.Height) }
func (s *stream) ColorModel() color.Model {
	if p, ok := s.band.(*image.Paletted); ok {
		return p.Palette
	}
	return s.band.ColorModel()
}

// Opaque reports whether every pixel is known to be opaque: always, except
// for 32 bit-per-pixel files with a V4 or V5 header, whose alpha channel
// bmp.Decode keeps.
func (s *stream) Opaque() bool { return !s.allowAlpha }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.raw = nil
	if s.err == nil {
		s.err = errors.New("goimgtools: bmp: closed")
	}
	return nil
}

// Next decodes the next strip. bmp.Decode checks nothing after the last
// row, so there is no extra verification before the final strip.
func (s *stream) Next() (image.Image, error) {
	if s.done {
		return nil, io.EOF
	}
	if s.err != nil {
		return nil, s.err
	}
	strip, err := s.next()
	if err != nil {
		s.err = err
		return nil, err
	}
	return strip, nil
}

func (s *stream) next() (image.Image, error) {
	c := s.c
	if c.Width == 0 || c.Height == 0 {
		// bmp.Decode accepts a 0x0 image; it is reported as one empty strip.
		s.done = true
		return s.band, nil
	}
	y0, y1 := s.y, min(s.y+s.n, c.Height)
	// The strip's rows are adjacent in the file, in either row order.
	first := int64(y0)
	if !s.topDown {
		first = int64(c.Height - y1)
	}
	raw := s.raw[:int64(y1-y0)*s.bytesPerRow]
	if n, err := s.r.ReadAt(raw, s.dataOff+first*s.bytesPerRow); n < len(raw) {
		if err == nil || err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	strip := pipeline.Relabel(s.band, image.Rect(0, y0, c.Width, y1))
	for y := y0; y < y1; y++ {
		k := int64(y - y0)
		if !s.topDown {
			k = int64(y1 - 1 - y)
		}
		b := raw[k*s.bytesPerRow : (k+1)*s.bytesPerRow]
		switch m := strip.(type) {
		case *image.Paletted:
			p := m.Pix[(y-y0)*m.Stride : (y-y0)*m.Stride+c.Width]
			if err := palettedRow(p, b, c.Width, s.bpp, m.Palette); err != nil {
				return nil, err
			}
		case *image.RGBA:
			p := m.Pix[(y-y0)*m.Stride : (y-y0)*m.Stride+c.Width*4]
			rgbRow(p, b)
		case *image.NRGBA:
			p := m.Pix[(y-y0)*m.Stride : (y-y0)*m.Stride+c.Width*4]
			nrgbaRow(p, b, s.allowAlpha)
		}
	}
	s.y = y1
	if y1 == c.Height {
		s.done = true
	}
	return strip, nil
}

func decodeConfig(r io.Reader) (config image.Config, bitsPerPixel int, topDown bool, allowAlpha bool, err error) {
	// We only support those BMP images with one of the following DIB headers:
	// - BITMAPINFOHEADER (40 bytes)
	// - BITMAPV4HEADER (108 bytes)
	// - BITMAPV5HEADER (124 bytes)
	const (
		fileHeaderLen   = 14
		infoHeaderLen   = 40
		v4InfoHeaderLen = 108
		v5InfoHeaderLen = 124
	)
	var b [1024]byte
	if _, err := io.ReadFull(r, b[:fileHeaderLen+4]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return image.Config{}, 0, false, false, err
	}
	if string(b[:2]) != "BM" {
		return image.Config{}, 0, false, false, errors.New("bmp: invalid format")
	}
	offset := readUint32(b[10:14])
	infoLen := readUint32(b[14:18])
	if infoLen != infoHeaderLen && infoLen != v4InfoHeaderLen && infoLen != v5InfoHeaderLen {
		return image.Config{}, 0, false, false, ErrUnsupported
	}
	if _, err := io.ReadFull(r, b[fileHeaderLen+4:fileHeaderLen+infoLen]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return image.Config{}, 0, false, false, err
	}
	width := int(int32(readUint32(b[18:22])))
	height := int(int32(readUint32(b[22:26])))
	if height < 0 {
		height, topDown = -height, true
	}
	if width < 0 || height < 0 {
		return image.Config{}, 0, false, false, ErrUnsupported
	}
	if (width == 0) != (height == 0) {
		// We'll take 0x0, but Nx0 or 0xN is suspicious.
		return image.Config{}, 0, false, false, ErrUnsupported
	}
	// Check that the image fits in memory.
	// This conservatively assumes 4 bytes per pixel,
	// rather than using the actual pixel size.
	if _, ok := mul3(width, height, 4); !ok {
		return image.Config{}, 0, false, false, ErrUnsupported
	}
	// We only support 1 plane and 8, 24 or 32 bits per pixel and no
	// compression.
	planes, bpp, compression := readUint16(b[26:28]), readUint16(b[28:30]), readUint32(b[30:34])
	// if compression is set to BI_BITFIELDS, but the bitmask is set to the default bitmask
	// that would be used if compression was set to 0, we can continue as if compression was 0
	if compression == 3 && infoLen > infoHeaderLen &&
		readUint32(b[54:58]) == 0xff0000 && readUint32(b[58:62]) == 0xff00 &&
		readUint32(b[62:66]) == 0xff && readUint32(b[66:70]) == 0xff000000 {
		compression = 0
	}
	if planes != 1 || compression != 0 {
		return image.Config{}, 0, false, false, ErrUnsupported
	}
	switch bpp {
	case 1, 2, 4, 8:
		colorUsed := readUint32(b[46:50])

		if colorUsed == 0 {
			colorUsed = 1 << bpp
		} else if colorUsed > (1 << bpp) {
			return image.Config{}, 0, false, false, ErrUnsupported
		}

		if offset != fileHeaderLen+infoLen+colorUsed*4 {
			return image.Config{}, 0, false, false, ErrUnsupported
		}
		_, err = io.ReadFull(r, b[:colorUsed*4])
		if err != nil {
			return image.Config{}, 0, false, false, err
		}
		pcm := make(color.Palette, colorUsed)
		for i := range pcm {
			// BMP images are stored in BGR order rather than RGB order.
			// Every 4th byte is padding.
			pcm[i] = color.RGBA{b[4*i+2], b[4*i+1], b[4*i+0], 0xFF}
		}
		return image.Config{ColorModel: pcm, Width: width, Height: height}, int(bpp), topDown, false, nil
	case 24:
		if offset != fileHeaderLen+infoLen {
			return image.Config{}, 0, false, false, ErrUnsupported
		}
		return image.Config{ColorModel: color.RGBAModel, Width: width, Height: height}, 24, topDown, false, nil
	case 32:
		if offset != fileHeaderLen+infoLen {
			return image.Config{}, 0, false, false, ErrUnsupported
		}
		// 32 bits per pixel is possibly RGBX (X is padding) or RGBA (A is
		// alpha transparency). However, for BMP images, "Alpha is a
		// poorly-documented and inconsistently-used feature" says
		// https://source.chromium.org/chromium/chromium/src/+/bc0a792d7ebc587190d1a62ccddba10abeea274b:third_party/blink/renderer/platform/image-decoders/bmp/bmp_image_reader.cc;l=621
		//
		// That goes on to say "BITMAPV3HEADER+ have an alpha bitmask in the
		// info header... so we respect it at all times... [For earlier
		// (smaller) headers we] ignore alpha in Windows V3 BMPs except inside
		// ICO files".
		//
		// "Ignore" means to always set alpha to 0xFF (fully opaque):
		// https://source.chromium.org/chromium/chromium/src/+/bc0a792d7ebc587190d1a62ccddba10abeea274b:third_party/blink/renderer/platform/image-decoders/bmp/bmp_image_reader.h;l=272
		//
		// Confusingly, "Windows V3" does not correspond to BITMAPV3HEADER, but
		// instead corresponds to the earlier (smaller) BITMAPINFOHEADER:
		// https://source.chromium.org/chromium/chromium/src/+/bc0a792d7ebc587190d1a62ccddba10abeea274b:third_party/blink/renderer/platform/image-decoders/bmp/bmp_image_reader.cc;l=258
		//
		// This Go package does not support ICO files and the (infoLen >
		// infoHeaderLen) condition distinguishes BITMAPINFOHEADER (40 bytes)
		// vs later (larger) headers.
		allowAlpha = infoLen > infoHeaderLen
		return image.Config{ColorModel: color.RGBAModel, Width: width, Height: height}, 32, topDown, allowAlpha, nil
	}
	return image.Config{}, 0, false, false, ErrUnsupported
}
