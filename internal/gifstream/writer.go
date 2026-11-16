// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// This file is Go's image/gif encoder (go1.27) changed to take its pixels
// row by row. The header, colour table, graphic control, image descriptor
// and sub-block code is the standard library's; writeImageBlock is split
// into beginImage (everything up to the LZW writer), the row writes and
// endImage, and the *image.Paletted the standard library builds first is
// replaced by rows produced on the fly (see dither.go).

package gifstream

import (
	"bufio"
	"bytes"
	"compress/lzw"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"io"
	"math/bits"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Graphic control extension fields.
const (
	gcLabel     = 0xF9
	gcBlockSize = 0x04
)

func log2(x int) int {
	if x < 2 {
		return 0
	}
	return bits.Len(uint(x-1)) - 1
}

// writer is a buffered writer.
type writer interface {
	Flush() error
	io.Writer
	io.ByteWriter
}

// encoder encodes images to the GIF format.
type encoder struct {
	// w is the writer to write to. err is the first error encountered during
	// writing. All attempted writes after the first error become no-ops.
	w   writer
	err error
	// The logical screen, in place of the standard library's GIF value.
	width, height int
	globalPalette color.Palette
	// globalCT is the size in bytes of the global color table.
	globalCT int
	// buf is a scratch buffer. It must be at least 256 for the blockWriter.
	buf              [256]byte
	globalColorTable [3 * 256]byte
	localColorTable  [3 * 256]byte
	// lzww is reused from frame to frame.
	lzww *lzw.Writer
	// lossy is the colour difference the lossy LZW encoder accepts; 0
	// for lossless LZW (compress/lzw). lz is that encoder.
	lossy int
	lz    *lossyLZW
}

func newEncoder(w io.Writer, width, height int, global color.Palette) *encoder {
	e := &encoder{width: width, height: height, globalPalette: global}
	if ww, ok := w.(writer); ok {
		e.w = ww
	} else {
		e.w = bufio.NewWriter(w)
	}
	return e
}

// blockWriter writes the block structure of GIF image data, which
// comprises (n, (n bytes)) blocks, with 1 <= n <= 255. It is the
// writer given to the LZW encoder, which is thus immune to the
// blocking.
type blockWriter struct {
	e *encoder
}

func (b blockWriter) setup() {
	b.e.buf[0] = 0
}

func (b blockWriter) Flush() error {
	return b.e.err
}

func (b blockWriter) WriteByte(c byte) error {
	if b.e.err != nil {
		return b.e.err
	}

	// Append c to buffered sub-block.
	b.e.buf[0]++
	b.e.buf[b.e.buf[0]] = c
	if b.e.buf[0] < 255 {
		return nil
	}

	// Flush block
	b.e.write(b.e.buf[:256])
	b.e.buf[0] = 0
	return b.e.err
}

// blockWriter must be an io.Writer for lzw.NewWriter, but this is never
// actually called.
func (b blockWriter) Write(data []byte) (int, error) {
	for i, c := range data {
		if err := b.WriteByte(c); err != nil {
			return i, err
		}
	}
	return len(data), nil
}

func (b blockWriter) close() {
	// Write the block terminator (0x00), either by itself, or along with a
	// pending sub-block.
	if b.e.buf[0] == 0 {
		b.e.writeByte(0)
	} else {
		n := uint(b.e.buf[0])
		b.e.buf[n+1] = 0
		b.e.write(b.e.buf[:n+2])
	}
	b.e.flush()
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

func putUint16(b []byte, v uint16) {
	b[0], b[1] = byte(v), byte(v>>8)
}

// writeHeader writes the header, the logical screen descriptor, the global
// color table and, when loopCount >= 0, the looping extension.
func (e *encoder) writeHeader(backgroundIndex byte, loopCount int) {
	if e.err != nil {
		return
	}
	_, e.err = io.WriteString(e.w, "GIF89a")
	if e.err != nil {
		return
	}

	// Logical screen width and height.
	putUint16(e.buf[0:2], uint16(e.width))
	putUint16(e.buf[2:4], uint16(e.height))
	e.write(e.buf[:4])

	if p := e.globalPalette; len(p) > 0 {
		paddedSize := log2(len(p)) // Size of Global Color Table: 2^(1+n).
		e.buf[0] = fColorTable | uint8(paddedSize)
		e.buf[1] = backgroundIndex
		e.buf[2] = 0x00 // Pixel Aspect Ratio.
		e.write(e.buf[:3])
		var err error
		e.globalCT, err = encodeColorTable(e.globalColorTable[:], p, paddedSize)
		if err != nil && e.err == nil {
			e.err = err
			return
		}
		e.write(e.globalColorTable[:e.globalCT])
	} else {
		// All frames have a local color table, so a global color table
		// is not needed.
		e.buf[0] = 0x00
		e.buf[1] = 0x00 // Background Color Index.
		e.buf[2] = 0x00 // Pixel Aspect Ratio.
		e.write(e.buf[:3])
	}

	// Add animation info if necessary.
	if loopCount >= 0 {
		e.buf[0] = 0x21 // Extension Introducer.
		e.buf[1] = 0xff // Application Label.
		e.buf[2] = 0x0b // Block Size.
		e.write(e.buf[:3])
		_, err := io.WriteString(e.w, "NETSCAPE2.0") // Application Identifier.
		if err != nil && e.err == nil {
			e.err = err
			return
		}
		e.buf[0] = 0x03 // Block Size.
		e.buf[1] = 0x01 // Sub-block Index.
		putUint16(e.buf[2:4], uint16(loopCount))
		e.buf[4] = 0x00 // Block Terminator.
		e.write(e.buf[:5])
	}
}

func encodeColorTable(dst []byte, p color.Palette, size int) (int, error) {
	if uint(size) >= 8 {
		return 0, errors.New("gif: cannot encode color table with more than 256 entries")
	}
	for i, c := range p {
		if c == nil {
			return 0, errors.New("gif: cannot encode color table with nil entries")
		}
		var r, g, b uint8
		// It is most likely that the palette is full of color.RGBAs, so they
		// get a fast path.
		if rgba, ok := c.(color.RGBA); ok {
			r, g, b = rgba.R, rgba.G, rgba.B
		} else {
			rr, gg, bb, _ := c.RGBA()
			r, g, b = uint8(rr>>8), uint8(gg>>8), uint8(bb>>8)
		}
		dst[3*i+0] = r
		dst[3*i+1] = g
		dst[3*i+2] = b
	}
	n := 1 << (size + 1)
	if n > len(p) {
		// Pad with black.
		clear(dst[3*len(p) : 3*n])
	}
	return 3 * n, nil
}

func (e *encoder) colorTablesMatch(localLen, transparentIndex int) bool {
	localSize := 3 * localLen
	if transparentIndex >= 0 {
		trOff := 3 * transparentIndex
		return bytes.Equal(e.globalColorTable[:trOff], e.localColorTable[:trOff]) &&
			bytes.Equal(e.globalColorTable[trOff+3:localSize], e.localColorTable[trOff+3:localSize])
	}
	return bytes.Equal(e.globalColorTable[:localSize], e.localColorTable[:localSize])
}

// transparentIndexOf is the standard library's choice of a frame's
// transparent index: the first fully transparent palette entry, or -1.
func transparentIndexOf(p color.Palette) (int, error) {
	for i, c := range p {
		if c == nil {
			return 0, errors.New("gif: cannot encode color table with nil entries")
		}
		if _, _, _, a := c.RGBA(); a == 0 {
			return i, nil
		}
	}
	return -1, nil
}

// beginImage is the first half of the standard library's writeImageBlock:
// the graphic control extension, the image descriptor, the color table and
// the LZW writer. The caller writes b.Dx()*b.Dy() color indexes to e.lzww
// and then calls endImage.
func (e *encoder) beginImage(pal color.Palette, b image.Rectangle, delay int, disposal byte, transparentIndex int) {
	if e.err != nil {
		return
	}

	if len(pal) == 0 {
		e.err = errors.New("gif: cannot encode image block with empty palette")
		return
	}

	if b.Min.X < 0 || b.Max.X >= 1<<16 || b.Min.Y < 0 || b.Max.Y >= 1<<16 {
		e.err = errors.New("gif: image block is too large to encode")
		return
	}
	if !b.In(image.Rectangle{Max: image.Point{e.width, e.height}}) {
		e.err = errors.New("gif: image block is out of bounds")
		return
	}

	if delay > 0 || disposal != 0 || transparentIndex != -1 {
		e.buf[0] = sExtension  // Extension Introducer.
		e.buf[1] = gcLabel     // Graphic Control Label.
		e.buf[2] = gcBlockSize // Block Size.
		if transparentIndex != -1 {
			e.buf[3] = 0x01 | disposal<<2
		} else {
			e.buf[3] = 0x00 | disposal<<2
		}
		putUint16(e.buf[4:6], uint16(delay)) // Delay Time (1/100ths of a second)

		// Transparent color index.
		if transparentIndex != -1 {
			e.buf[6] = uint8(transparentIndex)
		} else {
			e.buf[6] = 0x00
		}
		e.buf[7] = 0x00 // Block Terminator.
		e.write(e.buf[:8])
	}
	e.buf[0] = sImageDescriptor
	putUint16(e.buf[1:3], uint16(b.Min.X))
	putUint16(e.buf[3:5], uint16(b.Min.Y))
	putUint16(e.buf[5:7], uint16(b.Dx()))
	putUint16(e.buf[7:9], uint16(b.Dy()))
	e.write(e.buf[:9])

	// To determine whether or not this frame's palette is the same as the
	// global palette, we can check a couple things. First, do they actually
	// point to the same []color.Color? If so, they are equal so long as the
	// frame's palette is not longer than the global palette...
	paddedSize := log2(len(pal)) // Size of Local Color Table: 2^(1+n).
	if gp := e.globalPalette; len(gp) > 0 && len(pal) <= len(gp) && &gp[0] == &pal[0] {
		e.writeByte(0) // Use the global color table.
	} else {
		ct, err := encodeColorTable(e.localColorTable[:], pal, paddedSize)
		if err != nil {
			if e.err == nil {
				e.err = err
			}
			return
		}
		// This frame's palette is not the very same slice as the global
		// palette, but it might be a copy, possibly with one value turned into
		// transparency by DecodeAll.
		if ct <= e.globalCT && e.colorTablesMatch(len(pal), transparentIndex) {
			e.writeByte(0) // Use the global color table.
		} else {
			// Use a local color table.
			e.writeByte(fColorTable | uint8(paddedSize))
			e.write(e.localColorTable[:ct])
		}
	}

	litWidth := paddedSize + 1
	if litWidth < 2 {
		litWidth = 2
	}
	e.writeByte(uint8(litWidth)) // LZW Minimum Code Size.

	bw := blockWriter{e: e}
	bw.setup()
	if e.lossy > 0 {
		if e.lz == nil {
			e.lz = new(lossyLZW)
		}
		e.lz.reset(bw, uint(litWidth), pal, transparentIndex, e.lossy)
		return
	}
	if e.lzww == nil {
		e.lzww = lzw.NewWriter(bw, lzw.LSB, litWidth).(*lzw.Writer)
	} else {
		e.lzww.Reset(bw, lzw.LSB, litWidth)
	}
}

// writePixels compresses the next color indexes of the frame.
func (e *encoder) writePixels(pix []byte) {
	if e.err != nil {
		return
	}
	if e.lossy > 0 {
		e.lz.write(pix)
		return
	}
	_, e.err = e.lzww.Write(pix)
}

// endImage is the end of the standard library's writeImageBlock.
func (e *encoder) endImage() {
	if e.err != nil {
		return
	}
	if e.lossy > 0 {
		e.lz.close()
	} else {
		e.lzww.Close() // flush to bw
	}
	blockWriter{e: e}.close() // flush to e.w
}

// Options are the encoding parameters of Encode.
type Options struct {
	// NumColors is the maximum number of colors used in the image, 1 to
	// 256. Zero means 256.
	NumColors int
	// NoDither maps an image that is not paletted to the nearest entries
	// of the Plan 9 palette instead of dithering it (gif.Encode with
	// draw.Src as its Drawer).
	NoDither bool
	// Lossy is the largest colour difference (distance in RGB, 0-255 a
	// channel) the LZW encoder accepts to lengthen a string (see
	// lossy.go); 0 keeps every pixel.
	Lossy int
}

// checkPalette rejects what cannot be written or looked up without a panic.
func checkPalette(p color.Palette) error {
	if len(p) == 0 {
		return errors.New("gif: cannot encode image block with empty palette")
	}
	for _, c := range p {
		if c == nil {
			return errors.New("gif: cannot encode color table with nil entries")
		}
	}
	return nil
}

// samePalette reports whether two strips of one image carry the same
// palette. Palettes with the same colors are the same palette.
func samePalette(a, b color.Palette) bool {
	if len(a) != len(b) {
		return false
	}
	if len(a) == 0 || &a[0] == &b[0] {
		return true
	}
	for i := range a {
		if a[i] == nil || b[i] == nil {
			return false
		}
		r0, g0, b0, a0 := a[i].RGBA()
		r1, g1, b1, a1 := b[i].RGBA()
		if r0 != r1 || g0 != g1 || b0 != b1 || a0 != a1 {
			return false
		}
	}
	return true
}

var errPaletteChanged = errors.New("goimgtools: gif: palette changed between strips")

// checkStrip verifies that strip continues the image b at row y.
func checkStrip(strip image.Image, b image.Rectangle, y int) error {
	if strip == nil {
		return errors.New("goimgtools: gif: source returned a nil strip")
	}
	sb := strip.Bounds()
	if sb.Min.X != b.Min.X || sb.Max.X != b.Max.X || sb.Min.Y != y || sb.Max.Y < y || sb.Max.Y > b.Max.Y {
		return fmt.Errorf("goimgtools: gif: strip %v does not continue image %v at row %d", sb, b, y)
	}
	if p, ok := strip.(*image.Paletted); ok {
		dx, dy := sb.Dx(), sb.Dy()
		if p.Stride < dx || (dy > 0 && len(p.Pix) < (dy-1)*p.Stride+dx) {
			return errors.New("goimgtools: gif: paletted strip has too few pixels")
		}
	}
	return nil
}

// nextStrip reads the next strip; the source ending here is an error.
func nextStrip(src pipeline.Source) (image.Image, error) {
	m, err := src.Next()
	if err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return m, err
}

// Encode writes src to w as a single-image GIF. The bytes are those the
// standard library's gif.Encode writes for the same image with
// &gif.Options{NumColors: o.NumColors}:
//
//   - *image.Paletted strips whose palette has at most NumColors entries
//     are written as they are, with that palette;
//   - everything else is reduced to the first NumColors colors of the
//     Plan 9 palette with Floyd-Steinberg dithering, one row at a time.
//
// As in the standard library the image is moved to (0, 0). Working memory
// is two rows of quantization error and one row of color indexes (nothing
// for paletted strips written as they are), reserved from the source's
// budget, plus the fixed LZW state.
//
// It does not close src. If it returns an error, what was written to w is
// not a valid image and must be discarded.
func Encode(w io.Writer, src pipeline.Source, o *Options) error {
	// Check for bounds and size restrictions.
	b := src.Bounds()
	dx, dy := b.Dx(), b.Dy()
	if dx <= 0 || dy <= 0 {
		return fmt.Errorf("goimgtools: gif: invalid image size %dx%d", dx, dy)
	}
	if dx >= 1<<16 || dy >= 1<<16 {
		return fmt.Errorf("goimgtools: a GIF holds at most 65535 pixels a side, the image is %dx%d", dx, dy)
	}
	numColors := 256
	if o != nil && o.NumColors != 0 {
		if o.NumColors < 1 || o.NumColors > 256 {
			return errors.New("goimgtools: gif: NumColors must be 1 to 256")
		}
		numColors = o.NumColors
	}
	env := pipeline.EnvOf(src)

	strip, err := nextStrip(src)
	if err != nil {
		return err
	}
	if err := checkStrip(strip, b, b.Min.Y); err != nil {
		return err
	}

	// The three cases of gif.Encode: a paletted image, an image of another
	// type whose color model is a palette, and the rest. The first two are
	// written with their palette when it is small enough.
	const (
		direct  = iota // *image.Paletted strips, written as they are
		convert        // color model is a palette: nearest entry per pixel
		dither         // Plan 9 palette, Floyd-Steinberg
	)
	mode := dither
	var pal color.Palette
	if pm, ok := strip.(*image.Paletted); ok {
		if len(pm.Palette) <= numColors {
			mode, pal = direct, pm.Palette
		}
	} else if cp, ok := src.ColorModel().(color.Palette); ok && len(cp) <= numColors {
		mode, pal = convert, cp
	}
	if mode == dither {
		pal = palette.Plan9[:numColors]
	}
	if err := checkPalette(pal); err != nil {
		return err
	}

	var (
		fs  *ditherer
		row []byte
	)
	if mode != direct {
		need := int64(dx)
		if mode == dither {
			need += 2 * quantErrorBytes(dx)
		}
		if err := env.Budget.Reserve(need, "gif encoder rows"); err != nil {
			return err
		}
		defer env.Budget.Release(need)
		row = make([]byte, dx)
		if mode == dither {
			fs = newDitherer(pal, dx)
			fs.plain = o != nil && o.NoDither
		}
	}

	// When calling Encode instead of EncodeAll, the single-frame image is
	// translated such that its top-left corner is (0, 0), so that the single
	// frame completely fills the overall GIF's bounds.
	e := newEncoder(w, dx, dy, pal)
	if o != nil {
		if o.Lossy < 0 || o.Lossy > 255 {
			return fmt.Errorf("goimgtools: gif: lossy must be 0 to 255, got %d", o.Lossy)
		}
		e.lossy = o.Lossy
	}
	e.writeHeader(0, -1)
	transparentIndex, err := transparentIndexOf(pal)
	if err != nil {
		return err
	}
	e.beginImage(pal, image.Rect(0, 0, dx, dy), 0, 0, transparentIndex)

	var stripPal color.Palette // palette of the paletted strips, when dithering
	for y := b.Min.Y; ; {
		sb := strip.Bounds()
		pm, _ := strip.(*image.Paletted)
		switch mode {
		case direct:
			if pm == nil {
				return fmt.Errorf("goimgtools: gif: strip type changed from *image.Paletted to %T", strip)
			}
			if !samePalette(pm.Palette, pal) {
				return errPaletteChanged
			}
			if pm.Stride == dx {
				e.writePixels(pm.Pix[:dx*sb.Dy()])
			} else {
				for i, n := 0, sb.Dy(); n > 0; i, n = i+pm.Stride, n-1 {
					e.writePixels(pm.Pix[i : i+dx])
				}
			}
		case convert:
			for ; y < sb.Max.Y; y++ {
				for x := range row {
					row[x] = uint8(pal.Index(pal.Convert(strip.At(b.Min.X+x, y))))
				}
				e.writePixels(row)
			}
		case dither:
			if pm != nil {
				// At must not be asked for a color that does not exist.
				if stripPal == nil {
					if err := checkPalette(pm.Palette); err != nil {
						return err
					}
					stripPal = pm.Palette
				} else if !samePalette(pm.Palette, stripPal) {
					return errPaletteChanged
				}
				if len(stripPal) < 256 {
					for i, n := 0, sb.Dy(); n > 0; i, n = i+pm.Stride, n-1 {
						for _, v := range pm.Pix[i : i+dx] {
							if int(v) >= len(stripPal) {
								return errors.New("goimgtools: gif: pixel value outside the palette")
							}
						}
					}
				}
			}
			px := pixelFunc(strip)
			for ; y < sb.Max.Y; y++ {
				fs.row(px, b.Min.X, y, row)
				e.writePixels(row)
			}
		}
		if e.err != nil {
			return e.err
		}
		y = sb.Max.Y
		if y == b.Max.Y {
			break
		}
		if strip, err = nextStrip(src); err != nil {
			return err
		}
		if err := checkStrip(strip, b, y); err != nil {
			return err
		}
	}
	e.endImage()
	e.writeByte(sTrailer)
	e.flush()
	return e.err
}
