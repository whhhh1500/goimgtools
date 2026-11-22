// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package gifstream is Go's image/gif decoder (go1.27) changed to emit the
// first frame as horizontal strips instead of one full-size image. Header,
// extension, colour table and block parsing are the standard library's code,
// unchanged, so the result is identical to gif.Decode's: the first frame
// only, as *image.Paletted strips with the frame's bounds and palette.
//
// Changes from the standard library are limited to: the streaming entry
// point (Open / Next / Close), readImageDescriptor being split into
// openImage (everything up to the LZW reader), readPixels (the pixel read
// and the palette range check, now per strip) and finish (the end-of-data
// checks), and newImageFromDescriptor returning the frame rectangle instead
// of allocating the frame.
//
// A non-interlaced frame is read row by row straight from the LZW reader.
// An interlaced frame arrives in four passes; its rows are written to a
// pipeline.Spill in arrival order and each output row is then read back from
// its pass, so no width×height buffer is allocated.
package gifstream

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var (
	errNotEnough = errors.New("gif: not enough image data")
	errTooMuch   = errors.New("gif: too much image data")
	errBadPixel  = errors.New("gif: invalid pixel value")
)

// If the io.Reader does not also have ReadByte, then decode will introduce its own buffering.
type reader interface {
	io.Reader
	io.ByteReader
}

// Masks etc.
const (
	// Fields.
	fColorTable         = 1 << 7
	fInterlace          = 1 << 6
	fColorTableBitsMask = 7

	// Graphic control flags.
	gcTransparentColorSet = 1 << 0
	gcDisposalMethodMask  = 7 << 2
)

// Section indicators.
const (
	sExtension       = 0x21
	sImageDescriptor = 0x2C
	sTrailer         = 0x3B
)

// Extensions.
const (
	eText           = 0x01 // Plain Text
	eGraphicControl = 0xF9 // Graphic Control
	eComment        = 0xFE // Comment
	eApplication    = 0xFF // Application
)

func readFull(r io.Reader, b []byte) error {
	_, err := io.ReadFull(r, b)
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

func readByte(r io.ByteReader) (byte, error) {
	b, err := r.ReadByte()
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return b, err
}

// decoder is the type used to decode a GIF file.
type decoder struct {
	r reader

	// Of an animation, from frame to frame (goimgtools): the pixels of
	// the strip of the frame before, once it is closed, and the LZW
	// reader.
	spare []uint8
	lzw   *lzwReader

	// From header.
	vers            string
	width           int
	height          int
	loopCount       int
	delayTime       int
	backgroundIndex byte
	disposalMethod  byte

	// From image descriptor.
	imageFields byte

	// From graphics control.
	transparentIndex    byte
	hasTransparentIndex bool

	// Computed.
	globalColorTable color.Palette

	// Used when decoding.
	tmp [1024]byte // must be at least 768 so we can read color table
}

// blockReader parses the block structure of GIF image data, which comprises
// (n, (n bytes)) blocks, with 1 <= n <= 255. It is the reader given to the
// LZW decoder, which is thus immune to the blocking. After the LZW decoder
// completes, there will be a 0-byte block remaining (0, ()), which is
// consumed when checking that the blockReader is exhausted.
//
// To avoid the allocation of a bufio.Reader for the lzw Reader, blockReader
// implements io.ByteReader and buffers blocks into the decoder's "tmp" buffer.
type blockReader struct {
	d    *decoder
	i, j uint8 // d.tmp[i:j] contains the buffered bytes
	err  error
}

func (b *blockReader) fill() {
	if b.err != nil {
		return
	}
	b.j, b.err = readByte(b.d.r)
	if b.j == 0 && b.err == nil {
		b.err = io.EOF
	}
	if b.err != nil {
		return
	}

	b.i = 0
	b.err = readFull(b.d.r, b.d.tmp[:b.j])
	if b.err != nil {
		b.j = 0
	}
}

func (b *blockReader) ReadByte() (byte, error) {
	if b.i == b.j {
		b.fill()
		if b.err != nil {
			return 0, b.err
		}
	}

	c := b.d.tmp[b.i]
	b.i++
	return c, nil
}

// blockReader must implement io.Reader, but its Read shouldn't ever actually
// be called in practice. The compress/lzw package will only call [blockReader.ReadByte].
func (b *blockReader) Read(p []byte) (int, error) {
	if len(p) == 0 || b.err != nil {
		return 0, b.err
	}
	if b.i == b.j {
		b.fill()
		if b.err != nil {
			return 0, b.err
		}
	}

	n := copy(p, b.d.tmp[b.i:b.j])
	b.i += uint8(n)
	return n, nil
}

// close primarily detects whether or not a block terminator was encountered
// after reading a sequence of data sub-blocks. It allows at most one trailing
// sub-block worth of data. I.e., if some number of bytes exist in one sub-block
// following the end of LZW data, the very next sub-block must be the block
// terminator. If the very end of LZW data happened to fill one sub-block, at
// most one more sub-block of length 1 may exist before the block-terminator.
// These accommodations allow us to support GIFs created by less strict encoders.
// See https://golang.org/issue/16146.
func (b *blockReader) close() error {
	if b.err == io.EOF {
		// A clean block-sequence terminator was encountered while reading.
		return nil
	} else if b.err != nil {
		// Some other error was encountered while reading.
		return b.err
	}

	if b.i == b.j {
		// We reached the end of a sub block reading LZW data. We'll allow at
		// most one more sub block of data with a length of 1 byte.
		b.fill()
		if b.err == io.EOF {
			return nil
		} else if b.err != nil {
			return b.err
		} else if b.j > 1 {
			return errTooMuch
		}
	}

	// Part of a sub-block remains buffered. We expect that the next attempt to
	// buffer a sub-block will reach the block terminator.
	b.fill()
	if b.err == io.EOF {
		return nil
	} else if b.err != nil {
		return b.err
	}

	return errTooMuch
}

// passBufSize is the read-back buffer of one interlace pass.
const passBufSize = 64 << 10

// stream is the first frame of a GIF, decoded strip by strip.
type stream struct {
	d   *decoder
	env *pipeline.Env

	rect       image.Rectangle // frame bounds, as gif.Decode reports them
	palette    color.Palette
	interlaced bool
	opaque     bool

	br   *blockReader
	lzwr io.ReadCloser

	band     *image.Paletted // reusable strip buffer, n rows
	n        int             // rows per strip
	y        int             // next output row, relative to rect.Min.Y
	reserved int64

	// Interlaced frames only.
	spill    *pipeline.Spill
	pass     [4]*pipeline.SeqReader
	passSize [4]int64 // bytes of each pass
	passBuf  [4]int   // read-back buffer size of each pass

	err  error
	done bool
}

// Open reads the GIF header and everything up to the first frame's pixel
// data, and returns a Source that decodes that frame on demand, exactly as
// gif.Decode would. env may be nil.
func Open(r io.Reader, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	d := &decoder{}
	// Add buffering if r does not provide ReadByte.
	if rr, ok := r.(reader); ok {
		d.r = rr
	} else {
		d.r = bufio.NewReader(r)
	}

	d.loopCount = -1

	err := d.readHeaderAndScreenDescriptor()
	if err != nil {
		return nil, err
	}

	for {
		c, err := readByte(d.r)
		if err != nil {
			return nil, fmt.Errorf("gif: reading frames: %v", err)
		}
		switch c {
		case sExtension:
			if err = d.readExtension(); err != nil {
				return nil, err
			}

		case sImageDescriptor:
			return d.openImage(env)

		case sTrailer:
			return nil, fmt.Errorf("gif: missing image data")

		default:
			return nil, fmt.Errorf("gif: unknown block type: 0x%.2x", c)
		}
	}
}

func (d *decoder) readHeaderAndScreenDescriptor() error {
	err := readFull(d.r, d.tmp[:13])
	if err != nil {
		return fmt.Errorf("gif: reading header: %v", err)
	}
	d.vers = string(d.tmp[:6])
	if d.vers != "GIF87a" && d.vers != "GIF89a" {
		return fmt.Errorf("gif: can't recognize format %q", d.vers)
	}
	d.width = int(d.tmp[6]) + int(d.tmp[7])<<8
	d.height = int(d.tmp[8]) + int(d.tmp[9])<<8
	if fields := d.tmp[10]; fields&fColorTable != 0 {
		d.backgroundIndex = d.tmp[11]
		// readColorTable overwrites the contents of d.tmp, but that's OK.
		if d.globalColorTable, err = d.readColorTable(fields); err != nil {
			return err
		}
	}
	// d.tmp[12] is the Pixel Aspect Ratio, which is ignored.
	return nil
}

func (d *decoder) readColorTable(fields byte) (color.Palette, error) {
	n := 1 << (1 + uint(fields&fColorTableBitsMask))
	err := readFull(d.r, d.tmp[:3*n])
	if err != nil {
		return nil, fmt.Errorf("gif: reading color table: %s", err)
	}
	j, p := 0, make(color.Palette, n)
	for i := range p {
		p[i] = color.RGBA{d.tmp[j+0], d.tmp[j+1], d.tmp[j+2], 0xFF}
		j += 3
	}
	return p, nil
}

func (d *decoder) readExtension() error {
	extension, err := readByte(d.r)
	if err != nil {
		return fmt.Errorf("gif: reading extension: %v", err)
	}
	size := 0
	switch extension {
	case eText:
		size = 13
	case eGraphicControl:
		return d.readGraphicControl()
	case eComment:
		// nothing to do but read the data.
	case eApplication:
		b, err := readByte(d.r)
		if err != nil {
			return fmt.Errorf("gif: reading extension: %v", err)
		}
		// The spec requires size be 11, but Adobe sometimes uses 10.
		size = int(b)
	default:
		return fmt.Errorf("gif: unknown extension 0x%.2x", extension)
	}
	if size > 0 {
		if err := readFull(d.r, d.tmp[:size]); err != nil {
			return fmt.Errorf("gif: reading extension: %v", err)
		}
	}

	// Application Extension with "NETSCAPE2.0" as string and 1 in data means
	// this extension defines a loop count.
	if extension == eApplication && string(d.tmp[:size]) == "NETSCAPE2.0" {
		n, err := d.readBlock()
		if err != nil {
			return fmt.Errorf("gif: reading extension: %v", err)
		}
		if n == 0 {
			return nil
		}
		if n == 3 && d.tmp[0] == 1 {
			d.loopCount = int(d.tmp[1]) | int(d.tmp[2])<<8
		}
	}
	for {
		n, err := d.readBlock()
		if err != nil {
			return fmt.Errorf("gif: reading extension: %v", err)
		}
		if n == 0 {
			return nil
		}
	}
}

func (d *decoder) readGraphicControl() error {
	if err := readFull(d.r, d.tmp[:6]); err != nil {
		return fmt.Errorf("gif: can't read graphic control: %s", err)
	}
	if d.tmp[0] != 4 {
		return fmt.Errorf("gif: invalid graphic control extension block size: %d", d.tmp[0])
	}
	flags := d.tmp[1]
	d.disposalMethod = (flags & gcDisposalMethodMask) >> 2
	d.delayTime = int(d.tmp[2]) | int(d.tmp[3])<<8
	if flags&gcTransparentColorSet != 0 {
		d.transparentIndex = d.tmp[4]
		d.hasTransparentIndex = true
	}
	if d.tmp[5] != 0 {
		return fmt.Errorf("gif: invalid graphic control extension block terminator: %d", d.tmp[5])
	}
	return nil
}

// openImage is the first half of the standard library's readImageDescriptor:
// the descriptor, the colour table, the transparent index and the LZW
// reader. The pixels are read later, by Next.
func (d *decoder) openImage(env *pipeline.Env) (pipeline.Source, error) {
	rect, err := d.newImageFromDescriptor()
	if err != nil {
		return nil, err
	}
	var palette color.Palette
	useLocalColorTable := d.imageFields&fColorTable != 0
	if useLocalColorTable {
		palette, err = d.readColorTable(d.imageFields)
		if err != nil {
			return nil, err
		}
	} else {
		if d.globalColorTable == nil {
			return nil, errors.New("gif: no color table")
		}
		palette = d.globalColorTable
	}
	if d.hasTransparentIndex {
		if !useLocalColorTable {
			// Clone the global color table.
			palette = append(color.Palette(nil), d.globalColorTable...)
		}
		if ti := int(d.transparentIndex); ti < len(palette) {
			palette[ti] = color.RGBA{}
		} else {
			// The transparentIndex is out of range, which is an error
			// according to the spec, but Firefox and Google Chrome
			// seem OK with this, so we enlarge the palette with
			// transparent colors. See golang.org/issue/15059.
			p := make(color.Palette, ti+1)
			copy(p, palette)
			for i := len(palette); i < len(p); i++ {
				p[i] = color.RGBA{}
			}
			palette = p
		}
	}
	litWidth, err := readByte(d.r)
	if err != nil {
		return nil, fmt.Errorf("gif: reading image data: %v", err)
	}
	if litWidth < 2 || litWidth > 8 {
		return nil, fmt.Errorf("gif: pixel size in decode out of range: %d", litWidth)
	}

	s := &stream{
		d:          d,
		env:        env,
		rect:       rect,
		palette:    palette,
		interlaced: d.imageFields&fInterlace != 0,
		opaque:     !d.hasTransparentIndex,
	}
	dx, dy := rect.Dx(), rect.Dy()
	s.n = pipeline.StripRows(int64(dx), dy)
	need := int64(dx) * int64(s.n)
	if s.interlaced {
		for i, pass := range interlacing {
			rows := 0
			if dy > pass.start {
				rows = (dy - pass.start + pass.skip - 1) / pass.skip
			}
			s.passSize[i] = int64(rows) * int64(dx)
			s.passBuf[i] = int(max(1, min(passBufSize, s.passSize[i])))
			need += int64(s.passBuf[i])
		}
	}
	if err := env.Budget.Reserve(need, "gif row buffers"); err != nil {
		return nil, err
	}
	s.reserved = need
	if n := dx * s.n; n > 0 && cap(d.spare) >= n {
		// The strip of the frame before, cleared as a new one is.
		pix := d.spare[:n]
		d.spare = nil
		clear(pix)
		s.band = &image.Paletted{Pix: pix, Stride: dx, Rect: image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+s.n), Palette: palette}
	} else {
		s.band = image.NewPaletted(image.Rect(rect.Min.X, rect.Min.Y, rect.Max.X, rect.Min.Y+s.n), palette)
	}

	// A wonderfully Go-like piece of magic.
	s.br = &blockReader{d: d}
	if d.lzw == nil {
		d.lzw = new(lzwReader)
	}
	d.lzw.reset(s.br, uint(litWidth))
	s.lzwr = d.lzw
	return s, nil
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return s.rect }
func (s *stream) ColorModel() color.Model { return s.palette }

// Opaque reports whether the frame has no transparent colour index.
func (s *stream) Opaque() bool { return s.opaque }

func (s *stream) Close() error {
	var err error
	if s.lzwr != nil {
		s.lzwr.Close()
		s.lzwr = nil
	}
	if s.spill != nil {
		err = s.spill.Close()
		s.spill = nil
	}
	s.pass = [4]*pipeline.SeqReader{}
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	if s.band != nil {
		s.d.spare = s.band.Pix
	}
	s.band = nil
	if s.err == nil {
		s.err = errors.New("goimgtools: gif: closed")
	}
	return err
}

// Next decodes the next strip. The final strip is returned only after the
// checks gif.Decode makes after the last pixel: the end of the LZW stream
// and the block terminator. For an interlaced frame all of the frame's data
// is read and checked on the first call.
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
	r := s.rect
	dx, dy := r.Dx(), r.Dy()
	if dx == 0 || dy == 0 {
		// gif.Decode accepts a frame without pixels (after the same
		// checks); it is reported as one empty strip.
		if err := s.finish(); err != nil {
			return nil, err
		}
		s.done = true
		return pipeline.Relabel(s.band, r), nil
	}
	if s.interlaced && s.spill == nil {
		if err := s.readInterlaced(); err != nil {
			return nil, err
		}
	}
	y0, y1 := s.y, min(s.y+s.n, dy)
	strip := pipeline.Relabel(s.band, image.Rect(r.Min.X, r.Min.Y+y0, r.Max.X, r.Min.Y+y1)).(*image.Paletted)
	if s.interlaced {
		for y := y0; y < y1; y++ {
			if err := s.pass[passOf(y)].ReadFull(strip.Pix[(y-y0)*dx : (y-y0+1)*dx]); err != nil {
				return nil, err
			}
		}
	} else {
		if err := s.readPixels(strip.Pix[:(y1-y0)*dx]); err != nil {
			return nil, err
		}
		if y1 == dy {
			if err := s.finish(); err != nil {
				return nil, err
			}
		}
	}
	s.y = y1
	if y1 == dy {
		s.done = true
	}
	return strip, nil
}

// readPixels reads the next len(pix) pixels of the frame, in file order,
// and checks them against the palette. It is the pixel read and the range
// check of the standard library's readImageDescriptor, applied to a part of
// the frame instead of to all of m.Pix.
func (s *stream) readPixels(pix []uint8) error {
	if err := readFull(s.lzwr, pix); err != nil {
		if err != io.ErrUnexpectedEOF {
			return fmt.Errorf("gif: reading image data: %v", err)
		}
		return errNotEnough
	}

	// Check that the color indexes are inside the palette.
	if len(s.palette) < 256 {
		for _, pixel := range pix {
			if int(pixel) >= len(s.palette) {
				return errBadPixel
			}
		}
	}
	return nil
}

// finish runs the checks gif.Decode performs after the last pixel.
func (s *stream) finish() error {
	d, lzwr, br := s.d, s.lzwr, s.br
	// In theory, both lzwr and br should be exhausted. Reading from them
	// should yield (0, io.EOF).
	//
	// The spec (Appendix F - Compression), says that "An End of
	// Information code... must be the last code output by the encoder
	// for an image". In practice, though, giflib (a widely used C
	// library) does not enforce this, so we also accept lzwr returning
	// io.ErrUnexpectedEOF (meaning that the encoded stream hit io.EOF
	// before the LZW decoder saw an explicit end code), provided that
	// the io.ReadFull call above successfully read len(m.Pix) bytes.
	// See https://golang.org/issue/9856 for an example GIF.
	if n, err := lzwr.Read(d.tmp[256:257]); n != 0 || (err != io.EOF && err != io.ErrUnexpectedEOF) {
		if err != nil {
			return fmt.Errorf("gif: reading image data: %v", err)
		}
		return errTooMuch
	}

	// In practice, some GIFs have an extra byte in the data sub-block
	// stream, which we ignore. See https://golang.org/issue/16146.
	if err := br.close(); err == errTooMuch {
		return errTooMuch
	} else if err != nil {
		return fmt.Errorf("gif: reading image data: %v", err)
	}
	return nil
}

// readInterlaced reads the whole interlaced frame into the spill buffer in
// arrival order (pass after pass), runs the end-of-data checks, and sets up
// one sequential cursor per pass. It replaces the standard library's
// uninterlace, which needs a second full-size buffer.
func (s *stream) readInterlaced() error {
	dx, dy := s.rect.Dx(), s.rect.Dy()
	s.spill = pipeline.NewSpill(s.env)
	s.spill.Expect(int64(dx) * int64(dy))
	for y := 0; y < dy; {
		k := min(s.n, dy-y)
		buf := s.band.Pix[:k*dx]
		if err := s.readPixels(buf); err != nil {
			return err
		}
		if _, err := s.spill.Write(buf); err != nil {
			return err
		}
		y += k
	}
	if err := s.finish(); err != nil {
		return err
	}
	off := int64(0)
	for i := range interlacing {
		end := off + s.passSize[i]
		s.pass[i] = pipeline.NewSeqReader(s.spill, off, end, s.passBuf[i])
		off = end
	}
	return nil
}

func (d *decoder) newImageFromDescriptor() (image.Rectangle, error) {
	if err := readFull(d.r, d.tmp[:9]); err != nil {
		return image.Rectangle{}, fmt.Errorf("gif: can't read image descriptor: %s", err)
	}
	left := int(d.tmp[0]) + int(d.tmp[1])<<8
	top := int(d.tmp[2]) + int(d.tmp[3])<<8
	width := int(d.tmp[4]) + int(d.tmp[5])<<8
	height := int(d.tmp[6]) + int(d.tmp[7])<<8
	d.imageFields = d.tmp[8]

	// The GIF89a spec, Section 20 (Image Descriptor) says: "Each image must
	// fit within the boundaries of the Logical Screen, as defined in the
	// Logical Screen Descriptor."
	//
	// This is conceptually similar to testing
	//	frameBounds := image.Rect(left, top, left+width, top+height)
	//	imageBounds := image.Rect(0, 0, d.width, d.height)
	//	if !frameBounds.In(imageBounds) { etc }
	// but the semantics of the Go image.Rectangle type is that r.In(s) is true
	// whenever r is an empty rectangle, even if r.Min.X > s.Max.X. Here, we
	// want something stricter.
	//
	// Note that, by construction, left >= 0 && top >= 0, so we only have to
	// explicitly compare frameBounds.Max (left+width, top+height) against
	// imageBounds.Max (d.width, d.height) and not frameBounds.Min (left, top)
	// against imageBounds.Min (0, 0).
	if left+width > d.width || top+height > d.height {
		return image.Rectangle{}, errors.New("gif: frame bounds larger than image bounds")
	}
	return image.Rectangle{
		Min: image.Point{left, top},
		Max: image.Point{left + width, top + height},
	}, nil
}

func (d *decoder) readBlock() (int, error) {
	n, err := readByte(d.r)
	if n == 0 || err != nil {
		return 0, err
	}
	if err := readFull(d.r, d.tmp[:n]); err != nil {
		return 0, err
	}
	return int(n), nil
}

// interlaceScan defines the ordering for a pass of the interlace algorithm.
type interlaceScan struct {
	skip, start int
}

// interlacing represents the set of scans in an interlaced GIF image.
var interlacing = []interlaceScan{
	{8, 0}, // Group 1 : Every 8th. row, starting with row 0.
	{8, 4}, // Group 2 : Every 8th. row, starting with row 4.
	{4, 2}, // Group 3 : Every 4th. row, starting with row 2.
	{2, 1}, // Group 4 : Every 2nd. row, starting with row 1.
}

// passOf returns the index in interlacing of the pass that carries row y.
func passOf(y int) int {
	switch {
	case y%8 == 0:
		return 0
	case y%8 == 4:
		return 1
	case y%4 == 2:
		return 2
	}
	return 3
}
