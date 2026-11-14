package pipeline

import (
	"image"
	"image/color"
	"image/draw"
)

// Map returns src with fn applied to every strip. A strip reaches fn as
// 8-bit NRGBA whatever the pixels of src are (red, green, blue, alpha, one
// byte each, colour not multiplied by alpha), with strip.Rect telling which
// rows of the image it holds. fn changes the pixels in place; the changed
// strip is what the stage hands on. An error of fn ends the run with it.
//
// The size of the image stays as it is. The strip is a buffer of the stage
// (one strip of memory, counted in the budget): fn must not keep it.
//
// The result counts as opaque when src does. A fn that makes pixels of an
// opaque image transparent says so by reading WithAlpha(src) instead.
func Map(src Source, fn func(strip *image.NRGBA) error) Source {
	return &mapped{src: src, env: EnvOf(src), fn: fn, opaque: IsOpaque(src)}
}

type mapped struct {
	src      Source
	env      *Env
	fn       func(strip *image.NRGBA) error
	opaque   bool
	buf      []uint8
	reserved int64
	out      image.NRGBA
	// lut is the palette of a paletted source as NRGBA, made again when
	// the palette changes.
	lut    [256][4]uint8
	lutFor color.Palette
}

func (m *mapped) Bounds() image.Rectangle { return m.src.Bounds() }
func (m *mapped) ColorModel() color.Model { return color.NRGBAModel }
func (m *mapped) Env() *Env               { return m.env }
func (m *mapped) Opaque() bool            { return m.opaque }

func (m *mapped) Close() error {
	m.env.Budget.Release(m.reserved)
	m.reserved, m.buf = 0, nil
	return m.src.Close()
}

func (m *mapped) Next() (image.Image, error) {
	s, err := m.src.Next()
	if err != nil {
		return nil, err
	}
	r := s.Bounds()
	need := int64(r.Dx()) * 4 * int64(r.Dy())
	if int64(len(m.buf)) < need {
		m.env.Budget.Release(m.reserved)
		m.reserved, m.buf = 0, nil
		if err := m.env.Budget.Reserve(need, "strip buffer"); err != nil {
			return nil, err
		}
		m.reserved, m.buf = need, make([]uint8, need)
	}
	m.out = image.NRGBA{Pix: m.buf[:need], Stride: r.Dx() * 4, Rect: r}
	m.fill(s)
	if err := m.fn(&m.out); err != nil {
		return nil, err
	}
	return &m.out, nil
}

// fill converts strip s into m.out.
func (m *mapped) fill(s image.Image) {
	dst, r := &m.out, s.Bounds()
	w := r.Dx()
	switch p := s.(type) {
	case *image.NRGBA:
		for y := r.Min.Y; y < r.Max.Y; y++ {
			copy(dst.Pix[(y-r.Min.Y)*dst.Stride:][:w*4], p.Pix[p.PixOffset(r.Min.X, y):])
		}
		return
	case *image.Paletted:
		if !samePalette(m.lutFor, p.Palette) {
			m.lutFor = p.Palette
			for i, c := range p.Palette {
				if i > 255 {
					break
				}
				n := color.NRGBAModel.Convert(c).(color.NRGBA)
				m.lut[i] = [4]uint8{n.R, n.G, n.B, n.A}
			}
		}
		for y := r.Min.Y; y < r.Max.Y; y++ {
			d := dst.Pix[(y-r.Min.Y)*dst.Stride:][:w*4]
			for x, idx := range p.Pix[p.PixOffset(r.Min.X, y):][:w] {
				copy(d[4*x:4*x+4], m.lut[idx][:])
			}
		}
		return
	case *image.NYCbCrA:
		for y := r.Min.Y; y < r.Max.Y; y++ {
			d := dst.Pix[(y-r.Min.Y)*dst.Stride:][:w*4]
			yi, ai := p.YOffset(r.Min.X, y), p.AOffset(r.Min.X, y)
			for x := 0; x < w; x++ {
				ci := p.COffset(r.Min.X+x, y)
				d[4*x], d[4*x+1], d[4*x+2] = color.YCbCrToRGB(p.Y[yi+x], p.Cb[ci], p.Cr[ci])
				d[4*x+3] = p.A[ai+x]
			}
		}
		return
	case *image.NRGBA64:
		for y := r.Min.Y; y < r.Max.Y; y++ {
			d := dst.Pix[(y-r.Min.Y)*dst.Stride:][:w*4]
			sp := p.Pix[p.PixOffset(r.Min.X, y):][:w*8]
			for x := 0; x < w; x++ {
				d[4*x], d[4*x+1], d[4*x+2], d[4*x+3] = sp[8*x], sp[8*x+2], sp[8*x+4], sp[8*x+6]
			}
		}
		return
	}
	// The types that cannot hold transparency, and every strip of a
	// source known to be opaque: there colour multiplied by alpha (what
	// the fast paths of image/draw write) is the colour itself.
	_, isRGBA := s.(*image.RGBA)
	_, isRGBA64 := s.(*image.RGBA64)
	switch s.(type) {
	case *image.YCbCr, *image.Gray, *image.Gray16, *image.CMYK:
		m.opaqueFill(s)
		return
	}
	if m.opaque && (isRGBA || isRGBA64) {
		m.opaqueFill(s)
		return
	}
	if p, ok := s.(*image.RGBA); ok {
		for y := r.Min.Y; y < r.Max.Y; y++ {
			d := dst.Pix[(y-r.Min.Y)*dst.Stride:][:w*4]
			sp := p.Pix[p.PixOffset(r.Min.X, y):][:w*4]
			for x := 0; x < w*4; x += 4 {
				switch a := uint32(sp[x+3]); a {
				case 255:
					copy(d[x:x+4], sp[x:x+4])
				case 0:
					d[x], d[x+1], d[x+2], d[x+3] = 0, 0, 0, 0
				default:
					// As color.NRGBAModel converts it.
					a16 := a * 0x101
					d[x] = uint8((uint32(sp[x]) * 0x101 * 0xffff / a16) >> 8)
					d[x+1] = uint8((uint32(sp[x+1]) * 0x101 * 0xffff / a16) >> 8)
					d[x+2] = uint8((uint32(sp[x+2]) * 0x101 * 0xffff / a16) >> 8)
					d[x+3] = uint8(a)
				}
			}
		}
		return
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		d := dst.Pix[(y-r.Min.Y)*dst.Stride:][:w*4]
		for x := 0; x < w; x++ {
			n := color.NRGBAModel.Convert(s.At(r.Min.X+x, y)).(color.NRGBA)
			d[4*x], d[4*x+1], d[4*x+2], d[4*x+3] = n.R, n.G, n.B, n.A
		}
	}
}

// opaqueFill converts an opaque strip with the fast paths of image/draw.
func (m *mapped) opaqueFill(s image.Image) {
	r := s.Bounds()
	draw.Draw(&image.RGBA{Pix: m.out.Pix, Stride: m.out.Stride, Rect: r}, r, s, r.Min, draw.Src)
}

func samePalette(a, b color.Palette) bool {
	return len(a) == len(b) && (len(a) == 0 || &a[0] == &b[0])
}

// WithAlpha returns src as a Source that is not known to be opaque: what
// reads it will look for transparent pixels instead of taking their absence
// for granted. It is for a stage that makes pixels of an opaque image
// transparent.
func WithAlpha(src Source) Source { return withAlpha{src} }

type withAlpha struct{ Source }
