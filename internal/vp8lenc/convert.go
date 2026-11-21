package vp8lenc

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// rowReader pulls strips from the source and hands out one row at a time as
// non-premultiplied 0xAARRGGBB pixels: exactly what color.NRGBAModel makes
// of each source pixel, which is also what a decoder returns.
type rowReader struct {
	src      pipeline.Source
	b        image.Rectangle
	strip    image.Image
	y        int    // next row, absolute
	gray     bool   // every strip must be Gray or Gray16
	alphaAnd uint32 // AND of all pixels seen: its top byte stays 0xff while opaque
	pal      [256]uint32
	npal     int
	clean    bool // fully transparent pixels become transparent black
}

func newRowReader(src pipeline.Source) (*rowReader, error) {
	r := &rowReader{src: src, b: src.Bounds(), alphaAnd: 0xffffffff}
	r.y = r.b.Min.Y
	if err := r.pull(); err != nil {
		return nil, err
	}
	r.gray = isGray(r.strip)
	return r, nil
}

func isGray(m image.Image) bool {
	switch m.(type) {
	case *image.Gray, *image.Gray16:
		return true
	}
	return false
}

// pull fetches the strip that starts at row r.y.
func (r *rowReader) pull() error {
	s, err := r.src.Next()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}
	if s == nil {
		return errors.New("goimgtools: vp8l: source returned a nil strip")
	}
	sb := s.Bounds()
	if sb.Min.Y != r.y || sb.Max.Y <= sb.Min.Y || sb.Max.Y > r.b.Max.Y ||
		sb.Min.X > r.b.Min.X || sb.Max.X < r.b.Max.X {
		return fmt.Errorf("goimgtools: vp8l: strip %v does not continue image %v at row %d", sb, r.b, r.y)
	}
	if r.gray && !isGray(s) {
		return errors.New("goimgtools: vp8l: source changed pixel type")
	}
	if p, ok := s.(*image.Paletted); ok {
		r.npal = min(len(p.Palette), 256)
		for i, c := range p.Palette[:r.npal] {
			r.pal[i] = nrgbaOf(c)
		}
	}
	r.strip = s
	return nil
}

// drain checks that the source ends after the last row.
func (r *rowReader) drain() error {
	s, err := r.src.Next()
	if err == io.EOF {
		return nil
	}
	if err != nil {
		return err
	}
	if s == nil {
		return errors.New("goimgtools: vp8l: source returned a nil strip")
	}
	return fmt.Errorf("goimgtools: vp8l: strip %v after the last row of %v", s.Bounds(), r.b)
}

// nrgba16 un-premultiplies a 16-bit colour the way color.NRGBAModel does
// and keeps the high bytes.
func nrgba16(r, g, b, a uint32) uint32 {
	if a == 0xffff {
		return 0xff000000 | uint32(uint8(r>>8))<<16 | uint32(uint8(g>>8))<<8 | uint32(uint8(b>>8))
	}
	if a == 0 {
		return 0
	}
	r = (r * 0xffff) / a
	g = (g * 0xffff) / a
	b = (b * 0xffff) / a
	return uint32(uint8(a>>8))<<24 | uint32(uint8(r>>8))<<16 | uint32(uint8(g>>8))<<8 | uint32(uint8(b>>8))
}

func nrgbaOf(c color.Color) uint32 {
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	return uint32(n.A)<<24 | uint32(n.R)<<16 | uint32(n.G)<<8 | uint32(n.B)
}

func be16(p []uint8) uint32 { return uint32(p[0])<<8 | uint32(p[1]) }

// next converts the next row into dst, which is one image row long.
func (r *rowReader) next(dst []uint32) error {
	if r.y >= r.b.Max.Y {
		return io.ErrUnexpectedEOF
	}
	if r.y >= r.strip.Bounds().Max.Y {
		if err := r.pull(); err != nil {
			return err
		}
	}
	x0, y, w := r.b.Min.X, r.y, len(dst)
	switch m := r.strip.(type) {
	case *image.Gray:
		p := m.Pix[m.PixOffset(x0, y):][:w]
		for i, v := range p {
			dst[i] = 0xff000000 | uint32(v)*0x010101
		}
	case *image.Gray16:
		p := m.Pix[m.PixOffset(x0, y):][:2*w]
		for i := range dst {
			dst[i] = 0xff000000 | uint32(p[2*i])*0x010101
		}
	case *image.NRGBA:
		p := m.Pix[m.PixOffset(x0, y):][:4*w]
		for i := range dst {
			q := p[4*i : 4*i+4 : 4*i+4]
			dst[i] = uint32(q[3])<<24 | uint32(q[0])<<16 | uint32(q[1])<<8 | uint32(q[2])
		}
	case *image.RGBA:
		p := m.Pix[m.PixOffset(x0, y):][:4*w]
		for i := range dst {
			q := p[4*i : 4*i+4 : 4*i+4]
			switch a := uint32(q[3]); a {
			case 0xff:
				dst[i] = 0xff000000 | uint32(q[0])<<16 | uint32(q[1])<<8 | uint32(q[2])
			case 0:
				dst[i] = 0
			default:
				dst[i] = nrgba16(uint32(q[0])*0x101, uint32(q[1])*0x101, uint32(q[2])*0x101, a*0x101)
			}
		}
	case *image.NRGBA64:
		p := m.Pix[m.PixOffset(x0, y):][:8*w]
		for i := range dst {
			q := p[8*i : 8*i+8 : 8*i+8]
			a := be16(q[6:])
			// color.NRGBA64.RGBA premultiplies before the model divides again.
			dst[i] = nrgba16(be16(q[0:])*a/0xffff, be16(q[2:])*a/0xffff, be16(q[4:])*a/0xffff, a)
		}
	case *image.RGBA64:
		p := m.Pix[m.PixOffset(x0, y):][:8*w]
		for i := range dst {
			q := p[8*i : 8*i+8 : 8*i+8]
			dst[i] = nrgba16(be16(q[0:]), be16(q[2:]), be16(q[4:]), be16(q[6:]))
		}
	case *image.Alpha:
		p := m.Pix[m.PixOffset(x0, y):][:w]
		for i, a := range p {
			if a == 0 {
				dst[i] = 0
			} else {
				dst[i] = uint32(a)<<24 | 0xffffff
			}
		}
	case *image.Alpha16:
		p := m.Pix[m.PixOffset(x0, y):][:2*w]
		for i := range dst {
			a := be16(p[2*i:])
			dst[i] = nrgba16(a, a, a, a)
		}
	case *image.Paletted:
		p := m.Pix[m.PixOffset(x0, y):][:w]
		top := uint8(0)
		for i, v := range p {
			dst[i] = r.pal[v]
			top = max(top, v)
		}
		if int(top) >= r.npal {
			return fmt.Errorf("goimgtools: vp8l: palette index %d out of range (%d colours)", top, r.npal)
		}
	case *image.YCbCr:
		yy := m.Y[m.YOffset(x0, y):][:w]
		for i := range dst {
			c := m.COffset(x0+i, y)
			R, G, B := color.YCbCrToRGB(yy[i], m.Cb[c], m.Cr[c])
			dst[i] = 0xff000000 | uint32(R)<<16 | uint32(G)<<8 | uint32(B)
		}
	case *image.NYCbCrA:
		yy := m.Y[m.YOffset(x0, y):][:w]
		aa := m.A[m.AOffset(x0, y):][:w]
		for i := range dst {
			c := m.COffset(x0+i, y)
			if aa[i] == 0xff {
				R, G, B := color.YCbCrToRGB(yy[i], m.Cb[c], m.Cr[c])
				dst[i] = 0xff000000 | uint32(R)<<16 | uint32(G)<<8 | uint32(B)
			} else {
				dst[i] = nrgbaOf(color.NYCbCrA{YCbCr: color.YCbCr{Y: yy[i], Cb: m.Cb[c], Cr: m.Cr[c]}, A: aa[i]})
			}
		}
	case *image.CMYK:
		p := m.Pix[m.PixOffset(x0, y):][:4*w]
		for i := range dst {
			q := p[4*i : 4*i+4 : 4*i+4]
			R, G, B := color.CMYKToRGB(q[0], q[1], q[2], q[3])
			dst[i] = 0xff000000 | uint32(R)<<16 | uint32(G)<<8 | uint32(B)
		}
	default:
		for i := range dst {
			dst[i] = nrgbaOf(m.At(x0+i, y))
		}
	}
	and := r.alphaAnd
	for _, v := range dst {
		and &= v
	}
	r.alphaAnd = and
	if r.clean && and>>24 != 0xff {
		for i, v := range dst {
			if v>>24 == 0 {
				dst[i] = 0
			}
		}
	}
	r.y++
	return nil
}

// opaque reports whether every pixel read so far had alpha 255.
func (r *rowReader) opaque() bool { return r.alphaAnd>>24 == 0xff }
