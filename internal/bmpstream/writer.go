// Copyright 2013 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package bmpstream

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"math"
	"reflect"

	"github.com/whhhh1500/goimgtools/pipeline"
)

type header struct {
	sigBM           [2]byte
	fileSize        uint32
	resverved       [2]uint16
	pixOffset       uint32
	dibHeaderSize   uint32
	width           uint32
	height          uint32
	colorPlane      uint16
	bpp             uint16
	compression     uint32
	imageSize       uint32
	xPixelsPerMeter uint32
	yPixelsPerMeter uint32
	colorUse        uint32
	colorImportant  uint32
}

var (
	errZeroSize = errors.New("bmp: zero-size image")
	errTooLarge = errors.New("bmp: image too large")
	errStrip    = errors.New("bmp: source strip out of place")
	errType     = errors.New("bmp: source changed pixel type")
)

// The pixel layouts the encoder tells apart, as golang.org/x/image/bmp does.
const (
	kGeneric = iota // anything else: 24 bit through At().RGBA()
	kGray
	kPaletted
	kRGBA
	kNRGBA
)

// outChunk is how many bytes of rows are read back from the spill at a time.
const outChunk = 256 << 10

// stripReader pulls the strips of a source and checks that they are where
// the Source contract says they are, so the row loops cannot run off a
// buffer.
type stripReader struct {
	src pipeline.Source
	b   image.Rectangle
	y   int
	typ reflect.Type
}

// next returns the next strip, io.EOF after the last row, and
// io.ErrUnexpectedEOF when the source ends before that.
func (r *stripReader) next() (image.Image, error) {
	m, err := r.src.Next()
	if err != nil {
		if err == io.EOF && r.y < r.b.Max.Y {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	if m == nil {
		return nil, errStrip
	}
	sb := m.Bounds()
	if sb.Min.X != r.b.Min.X || sb.Max.X != r.b.Max.X || sb.Min.Y != r.y || sb.Max.Y < sb.Min.Y || sb.Max.Y > r.b.Max.Y {
		return nil, errStrip
	}
	if t := reflect.TypeOf(m); r.typ == nil {
		r.typ = t
	} else if t != r.typ {
		return nil, errType
	}
	if pix, stride, bpp := pixOf(m); bpp != 0 && sb.Dy() > 0 {
		if stride < sb.Dx()*bpp || len(pix) < (sb.Dy()-1)*stride+sb.Dx()*bpp {
			return nil, errStrip
		}
	}
	r.y = sb.Max.Y
	return m, nil
}

func pixOf(m image.Image) (pix []uint8, stride, bpp int) {
	switch p := m.(type) {
	case *image.Gray:
		return p.Pix, p.Stride, 1
	case *image.Paletted:
		return p.Pix, p.Stride, 1
	case *image.RGBA:
		return p.Pix, p.Stride, 4
	case *image.NRGBA:
		return p.Pix, p.Stride, 4
	}
	return nil, 0, 0
}

// bgrRow writes one row of 4-byte pixels as 24-bit BGR. dst may be the start
// of the buffer src is in, at or before src.
func bgrRow(dst, src []byte) {
	off := 0
	for i := 0; i+3 < len(src); i += 4 {
		r, g, b := src[i+0], src[i+1], src[i+2]
		dst[off+2] = r
		dst[off+1] = g
		dst[off+0] = b
		off += 3
	}
}

// bgraRow writes one row of premultiplied RGBA pixels as 32-bit BGRA with
// straight alpha. dst may be src.
func bgraRow(dst, src []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		r, g, b := src[i+0], src[i+1], src[i+2]
		a := uint32(src[i+3])
		if a == 0 {
			dst[i+2] = 0
			dst[i+1] = 0
			dst[i+0] = 0
			dst[i+3] = 0
			continue
		} else if a == 0xff {
			dst[i+2] = r
			dst[i+1] = g
			dst[i+0] = b
			dst[i+3] = 0xff
			continue
		}
		dst[i+2] = uint8(((uint32(r) * 0xffff) / a) >> 8)
		dst[i+1] = uint8(((uint32(g) * 0xffff) / a) >> 8)
		dst[i+0] = uint8(((uint32(b) * 0xffff) / a) >> 8)
		dst[i+3] = uint8(a)
	}
}

// bgraStraightRow writes one row of NRGBA pixels as 32-bit BGRA. dst may be src.
func bgraStraightRow(dst, src []byte) {
	for i := 0; i+3 < len(src); i += 4 {
		r, b := src[i+0], src[i+2]
		dst[i+2] = r
		dst[i+1] = src[i+1]
		dst[i+0] = b
		dst[i+3] = src[i+3]
	}
}

// genericRow writes row y of m as 24-bit BGR, the way the reference
// encoder's At() loop does. RGBA64At is by contract the same value as
// At().RGBA() without the interface allocation.
func genericRow(buf []byte, m image.Image, y int) {
	b := m.Bounds()
	off := 0
	if rm, ok := m.(image.RGBA64Image); ok {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := rm.RGBA64At(x, y)
			buf[off+2] = byte(c.R >> 8)
			buf[off+1] = byte(c.G >> 8)
			buf[off+0] = byte(c.B >> 8)
			off += 3
		}
		return
	}
	for x := b.Min.X; x < b.Max.X; x++ {
		r, g, b, _ := m.At(x, y).RGBA()
		buf[off+2] = byte(r >> 8)
		buf[off+1] = byte(g >> 8)
		buf[off+0] = byte(b >> 8)
		off += 3
	}
}

func rowOpaque(row []byte) bool {
	for i := 3; i < len(row); i += 4 {
		if row[i] != 0xff {
			return false
		}
	}
	return true
}

// Encode writes src to w in BMP format. It is the golang.org/x/image/bmp
// encoder reading strips, and the bytes are the ones bmp.Encode writes for
// the same image: 8 bit with a palette for Gray and Paletted strips, 24 or
// 32 bit for RGBA and NRGBA strips depending on whether every pixel is
// opaque, and 24 bit through At().RGBA() for every other pixel type.
//
// A BMP stores its rows bottom-up while a Source delivers them top-down, so
// the rows are collected in a pipeline.Spill (a temporary file when they do not
// fit in the memory budget) and written out in reverse. RGBA and NRGBA
// pixels are spooled as they are and their alpha is checked on the way in,
// unless the source says it is opaque (pipeline.IsOpaque); everything else is
// spooled in its final BMP row layout.
//
// It does not close src. If it returns an error, what was written to w is
// not a valid image and must be discarded. Unlike bmp.Encode, an image with
// no pixels is an error.
func Encode(w io.Writer, src pipeline.Source) error {
	b := src.Bounds()
	dx, dy := b.Dx(), b.Dy()
	if dx <= 0 || dy <= 0 {
		return errZeroSize
	}
	// fits reports whether rows of step bytes keep the file size in 32 bits.
	fits := func(step int64) bool {
		return step <= math.MaxInt32 && 14+40+1024+step*int64(dy) <= math.MaxUint32
	}
	env := pipeline.EnvOf(src)
	rd := &stripReader{src: src, b: b, y: b.Min.Y}
	first, err := rd.next()
	if err != nil {
		return err
	}

	kind := kGeneric
	switch first.(type) {
	case *image.Gray:
		kind = kGray
	case *image.Paletted:
		kind = kPaletted
	case *image.RGBA:
		kind = kRGBA
	case *image.NRGBA:
		kind = kNRGBA
	}
	step64 := (3*int64(dx) + 3) &^ 3
	if kind == kGray || kind == kPaletted {
		step64 = (int64(dx) + 3) &^ 3
	}
	if !fits(step64) {
		return errTooLarge
	}
	step := int(step64)
	// raw: the pixels are spooled untouched, because the row layout depends
	// on their alpha, which is only known once all of them have been seen.
	raw := (kind == kRGBA || kind == kNRGBA) && !pipeline.IsOpaque(src)
	in := step // bytes per spooled row
	if raw {
		if !fits(4 * int64(dx)) {
			return errTooLarge
		}
		in = 4 * dx
	}

	sp := pipeline.NewSpill(env)
	defer sp.Close()
	sp.Expect(int64(in) * int64(dy))

	var row []byte
	if !raw {
		if err := env.Budget.Reserve(int64(in), "bmp row"); err != nil {
			return err
		}
		defer env.Budget.Release(int64(in))
		row = make([]byte, in)
	}

	opaque := true
	var pal color.Palette
	for m := first; ; {
		sb := m.Bounds()
		pix, stride, _ := pixOf(m)
		if p, ok := m.(*image.Paletted); ok && len(p.Palette) > len(pal) {
			pal = p.Palette
		}
		for y := 0; y < sb.Dy(); y++ {
			var out []byte
			switch kind {
			case kGray, kPaletted:
				out = pix[y*stride : y*stride+dx]
				if dx < step {
					copy(row, out) // the padding stays zero
					out = row
				}
			case kRGBA, kNRGBA:
				out = pix[y*stride : y*stride+4*dx]
				if raw {
					opaque = opaque && rowOpaque(out)
				} else {
					bgrRow(row, out)
					out = row
				}
			default:
				genericRow(row, m, sb.Min.Y+y)
				out = row
			}
			if _, err := sp.Write(out); err != nil {
				return err
			}
		}
		if m, err = rd.next(); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
	}

	h := &header{
		sigBM:         [2]byte{'B', 'M'},
		fileSize:      14 + 40,
		pixOffset:     14 + 40,
		dibHeaderSize: 40,
		width:         uint32(dx),
		height:        uint32(dy),
		colorPlane:    1,
		bpp:           24,
	}
	var palette []byte
	switch kind {
	case kGray:
		palette = make([]byte, 1024)
		for i := 0; i < 256; i++ {
			palette[i*4+0] = uint8(i)
			palette[i*4+1] = uint8(i)
			palette[i*4+2] = uint8(i)
			palette[i*4+3] = 0xFF
		}
		h.bpp = 8
	case kPaletted:
		palette = make([]byte, 1024)
		for i := 0; i < len(pal) && i < 256; i++ {
			r, g, b, _ := pal[i].RGBA()
			palette[i*4+0] = uint8(b >> 8)
			palette[i*4+1] = uint8(g >> 8)
			palette[i*4+2] = uint8(r >> 8)
			palette[i*4+3] = 0xFF
		}
		h.bpp = 8
	case kRGBA, kNRGBA:
		if raw && !opaque {
			step = 4 * dx
			h.bpp = 32
		}
	}
	h.imageSize = uint32(int64(dy) * int64(step))
	h.fileSize += uint32(len(palette)) + h.imageSize
	h.pixOffset += uint32(len(palette))

	if err := binary.Write(w, binary.LittleEndian, h); err != nil {
		return err
	}
	if palette != nil {
		if _, err := w.Write(palette); err != nil {
			return err
		}
	}

	// Read the rows back a chunk at a time, last chunk first. Each chunk is
	// brought into its final layout in place, its rows are reversed, and it
	// goes out in one Write.
	n := max(1, min(dy, outChunk/in))
	need := int64(n)*int64(in) + int64(step)
	if err := env.Budget.Reserve(need, "bmp output rows"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	chunk, tmp := make([]byte, n*in), make([]byte, step)
	for y1 := dy; y1 > 0; {
		y0 := max(0, y1-n)
		k := y1 - y0
		if _, err := sp.ReadAt(chunk[:k*in], int64(y0)*int64(in)); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		if raw {
			for j := 0; j < k; j++ {
				s, d := chunk[j*in:(j+1)*in], chunk[j*step:(j+1)*step]
				switch {
				case opaque:
					bgrRow(d, s)
					clear(d[3*dx:])
				case kind == kRGBA:
					bgraRow(d, s)
				default:
					bgraStraightRow(d, s)
				}
			}
		}
		for i, j := 0, k-1; i < j; i, j = i+1, j-1 {
			a, c := chunk[i*step:(i+1)*step], chunk[j*step:(j+1)*step]
			copy(tmp, a)
			copy(a, c)
			copy(c, tmp)
		}
		if _, err := w.Write(chunk[:k*step]); err != nil {
			return err
		}
		y1 = y0
	}
	return nil
}
