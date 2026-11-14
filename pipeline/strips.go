package pipeline

import (
	"fmt"
	"image"
	"image/color"
	"io"
)

// NewBand allocates an image of the same concrete type (and palette, for
// paletted images) as proto, covering rect. Unknown types become RGBA64,
// which preserves every At().RGBA() value exactly.
func NewBand(proto image.Image, rect image.Rectangle) image.Image {
	switch p := proto.(type) {
	case *image.Gray:
		return image.NewGray(rect)
	case *image.Gray16:
		return image.NewGray16(rect)
	case *image.Alpha:
		return image.NewAlpha(rect)
	case *image.Alpha16:
		return image.NewAlpha16(rect)
	case *image.RGBA:
		return image.NewRGBA(rect)
	case *image.RGBA64:
		return image.NewRGBA64(rect)
	case *image.NRGBA:
		return image.NewNRGBA(rect)
	case *image.NRGBA64:
		return image.NewNRGBA64(rect)
	case *image.CMYK:
		return image.NewCMYK(rect)
	case *image.Paletted:
		return image.NewPaletted(rect, p.Palette)
	case *image.NYCbCrA:
		return image.NewNYCbCrA(rect, p.SubsampleRatio)
	case *image.YCbCr:
		return image.NewYCbCr(rect, p.SubsampleRatio)
	}
	return image.NewRGBA64(rect)
}

// BandBytes estimates the memory NewBand(proto, rect) allocates.
func BandBytes(proto image.Image, rect image.Rectangle) int64 {
	w, h := int64(rect.Dx()), int64(rect.Dy())
	switch p := proto.(type) {
	case *image.Gray, *image.Alpha, *image.Paletted:
		return w * h
	case *image.Gray16, *image.Alpha16:
		return 2 * w * h
	case *image.RGBA, *image.NRGBA, *image.CMYK:
		return 4 * w * h
	case *image.NYCbCrA:
		return 4 * w * (h + 2)
	case *image.YCbCr:
		_ = p
		return 3 * w * (h + 2)
	}
	return 8 * w * h
}

type pixImage struct {
	pix    []uint8
	stride int
	rect   image.Rectangle
	bpp    int
}

func asPix(m image.Image) (pixImage, bool) {
	switch p := m.(type) {
	case *image.Gray:
		return pixImage{p.Pix, p.Stride, p.Rect, 1}, true
	case *image.Gray16:
		return pixImage{p.Pix, p.Stride, p.Rect, 2}, true
	case *image.Alpha:
		return pixImage{p.Pix, p.Stride, p.Rect, 1}, true
	case *image.Alpha16:
		return pixImage{p.Pix, p.Stride, p.Rect, 2}, true
	case *image.RGBA:
		return pixImage{p.Pix, p.Stride, p.Rect, 4}, true
	case *image.RGBA64:
		return pixImage{p.Pix, p.Stride, p.Rect, 8}, true
	case *image.NRGBA:
		return pixImage{p.Pix, p.Stride, p.Rect, 4}, true
	case *image.NRGBA64:
		return pixImage{p.Pix, p.Stride, p.Rect, 8}, true
	case *image.CMYK:
		return pixImage{p.Pix, p.Stride, p.Rect, 4}, true
	case *image.Paletted:
		return pixImage{p.Pix, p.Stride, p.Rect, 1}, true
	}
	return pixImage{}, false
}

func chromaCols(r image.Rectangle, ratio image.YCbCrSubsampleRatio) (x0, x1 int) {
	switch ratio {
	case image.YCbCrSubsampleRatio422, image.YCbCrSubsampleRatio420:
		return r.Min.X / 2, (r.Max.X + 1) / 2
	case image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410:
		return r.Min.X / 4, (r.Max.X + 3) / 4
	}
	return r.Min.X, r.Max.X
}

// CopyRows copies rows [y0, y1) from src into dst. Both must have the same
// concrete type (dst from NewBand(src, ...)) and the same X range, and both
// must contain the rows. For an unknown src type dst is an *image.RGBA64.
func CopyRows(dst, src image.Image, y0, y1 int) {
	if y0 >= y1 {
		return
	}
	if d, ok := asPix(dst); ok {
		if s, ok := asPix(src); ok {
			n := d.rect.Dx() * d.bpp
			for y := y0; y < y1; y++ {
				do := (y - d.rect.Min.Y) * d.stride
				so := (y-s.rect.Min.Y)*s.stride + (d.rect.Min.X-s.rect.Min.X)*s.bpp
				copy(d.pix[do:do+n], s.pix[so:so+n])
			}
			if dp, ok := dst.(*image.Paletted); ok {
				dp.Palette = src.(*image.Paletted).Palette
			}
			return
		}
	}
	switch d := dst.(type) {
	case *image.NYCbCrA:
		s := src.(*image.NYCbCrA)
		copyYCbCr(&d.YCbCr, &s.YCbCr, y0, y1)
		n := d.Rect.Dx()
		for y := y0; y < y1; y++ {
			copy(d.A[d.AOffset(d.Rect.Min.X, y):][:n], s.A[s.AOffset(d.Rect.Min.X, y):][:n])
		}
		return
	case *image.YCbCr:
		copyYCbCr(d, src.(*image.YCbCr), y0, y1)
		return
	case *image.RGBA64:
		for y := y0; y < y1; y++ {
			for x := d.Rect.Min.X; x < d.Rect.Max.X; x++ {
				r, g, b, a := src.At(x, y).RGBA()
				d.SetRGBA64(x, y, color.RGBA64{uint16(r), uint16(g), uint16(b), uint16(a)})
			}
		}
		return
	}
	panic(fmt.Sprintf("goimgtools: CopyRows %T <- %T", dst, src))
}

func copyYCbCr(d, s *image.YCbCr, y0, y1 int) {
	n := d.Rect.Dx()
	cx0, cx1 := chromaCols(d.Rect, d.SubsampleRatio)
	cn := cx1 - cx0
	for y := y0; y < y1; y++ {
		copy(d.Y[d.YOffset(d.Rect.Min.X, y):][:n], s.Y[s.YOffset(d.Rect.Min.X, y):][:n])
		dc, sc := d.COffset(d.Rect.Min.X, y), s.COffset(d.Rect.Min.X, y)
		copy(d.Cb[dc:dc+cn], s.Cb[sc:sc+cn])
		copy(d.Cr[dc:dc+cn], s.Cr[sc:sc+cn])
	}
}

// RowReader hands out rows of a Source in increasing order, pulling strips
// as needed. Row(y) returns the strip that contains y; it stays valid until
// a later call asks for a row beyond it.
type RowReader struct {
	src   Source
	strip image.Image
	err   error
}

// NewRowReader returns a RowReader over src. It does not close src.
func NewRowReader(src Source) *RowReader { return &RowReader{src: src} }

// Row returns the strip containing row y. y must not decrease between calls.
func (r *RowReader) Row(y int) (image.Image, error) {
	for r.strip == nil || y >= r.strip.Bounds().Max.Y {
		if r.err != nil {
			return nil, r.err
		}
		s, err := r.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			r.err = err
			return nil, err
		}
		r.strip = s
	}
	if y < r.strip.Bounds().Min.Y {
		return nil, fmt.Errorf("goimgtools: row %d requested after strip %v", y, r.strip.Bounds())
	}
	return r.strip, nil
}

// Drain consumes any remaining strips so a decoder can finish verifying its
// input; it returns nil once Next reports io.EOF.
func (r *RowReader) Drain() error {
	if r.err != nil {
		return r.err
	}
	for {
		_, err := r.src.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			r.err = err
			return err
		}
	}
}

// Rechunk re-cuts src into strips of exactly n rows (the last may be
// shorter), aligned to src.Bounds().Min.Y, keeping the pixel type. It is for
// consumers such as the JPEG encoder that work in fixed-height bands.
func Rechunk(src Source, n int) (Source, error) {
	return &rechunk{src: src, n: n, rows: NewRowReader(src), env: EnvOf(src)}, nil
}

type rechunk struct {
	src      Source
	n        int
	rows     *RowReader
	y        int
	started  bool
	band     image.Image
	reserved int64
	env      *Env
}

func (c *rechunk) Bounds() image.Rectangle { return c.src.Bounds() }
func (c *rechunk) ColorModel() color.Model { return c.src.ColorModel() }
func (c *rechunk) Env() *Env               { return c.env }

func (c *rechunk) Next() (image.Image, error) {
	b := c.src.Bounds()
	if !c.started {
		c.started, c.y = true, b.Min.Y
	}
	if c.y >= b.Max.Y {
		if err := c.rows.Drain(); err != nil {
			return nil, err
		}
		return nil, io.EOF
	}
	y0, y1 := c.y, min(c.y+c.n, b.Max.Y)
	first, err := c.rows.Row(y0)
	if err != nil {
		return nil, err
	}
	// Fast path: the source strip already is exactly this band.
	if fb := first.Bounds(); fb.Min.Y == y0 && fb.Max.Y == y1 {
		c.y = y1
		if y1 == b.Max.Y {
			if err := c.rows.Drain(); err != nil {
				return nil, err
			}
		}
		return first, nil
	}
	rect := image.Rect(b.Min.X, y0, b.Max.X, y1)
	if c.band == nil || c.band.Bounds().Dy() < y1-y0 || !sameType(c.band, first) {
		c.release()
		full := image.Rect(b.Min.X, y0, b.Max.X, y0+c.n)
		need := BandBytes(first, full)
		if err := c.env.Budget.Reserve(need, "rechunk band"); err != nil {
			return nil, err
		}
		c.reserved = need
		c.band = NewBand(first, full)
	}
	band := subBand(c.band, rect)
	for y := y0; y < y1; {
		s, err := c.rows.Row(y)
		if err != nil {
			return nil, err
		}
		e := min(y1, s.Bounds().Max.Y)
		CopyRows(band, s, y, e)
		y = e
	}
	c.y = y1
	if y1 == b.Max.Y {
		if err := c.rows.Drain(); err != nil {
			return nil, err
		}
	}
	return band, nil
}

func sameType(a, b image.Image) bool { return fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b) }

// subBand re-labels the reusable band buffer to cover rect without copying.
// rect has the buffer's X range and rect.Dy() <= the buffer's height; the
// buffer's first row becomes rect.Min.Y.
func subBand(band image.Image, rect image.Rectangle) image.Image {
	return Relabel(band, rect)
}

// Relabel returns a view of band's buffer whose first row is rect.Min.Y.
// Vertically subsampled YCbCr cannot be shifted by an odd number of rows
// (chroma rows pair up by absolute parity), so a fresh band is returned then.
func Relabel(band image.Image, rect image.Rectangle) image.Image {
	dy := rect.Min.Y - band.Bounds().Min.Y
	switch p := band.(type) {
	case *image.YCbCr:
		if dy%2 != 0 && (p.SubsampleRatio == image.YCbCrSubsampleRatio420 || p.SubsampleRatio == image.YCbCrSubsampleRatio440) {
			return image.NewYCbCr(rect, p.SubsampleRatio)
		}
		q := *p
		q.Rect = rect
		return &q
	case *image.NYCbCrA:
		if dy%2 != 0 && (p.SubsampleRatio == image.YCbCrSubsampleRatio420 || p.SubsampleRatio == image.YCbCrSubsampleRatio440) {
			return image.NewNYCbCrA(rect, p.SubsampleRatio)
		}
		q := *p
		q.Rect = rect
		return &q
	}
	{
		switch p := band.(type) {
		case *image.Gray:
			q := *p
			q.Rect = rect
			return &q
		case *image.Gray16:
			q := *p
			q.Rect = rect
			return &q
		case *image.Alpha:
			q := *p
			q.Rect = rect
			return &q
		case *image.Alpha16:
			q := *p
			q.Rect = rect
			return &q
		case *image.RGBA:
			q := *p
			q.Rect = rect
			return &q
		case *image.RGBA64:
			q := *p
			q.Rect = rect
			return &q
		case *image.NRGBA:
			q := *p
			q.Rect = rect
			return &q
		case *image.NRGBA64:
			q := *p
			q.Rect = rect
			return &q
		case *image.CMYK:
			q := *p
			q.Rect = rect
			return &q
		case *image.Paletted:
			q := *p
			q.Rect = rect
			return &q
		}
	}
	return band
}

// Release gives back the band a Rechunk source holds without closing the
// source under it, for a consumer that is done with it but does not own
// the source.
func (c *rechunk) Release() { c.release() }

func (c *rechunk) release() {
	if c.reserved != 0 {
		c.env.Budget.Release(c.reserved)
		c.reserved = 0
	}
	c.band = nil
}

func (c *rechunk) Close() error {
	c.release()
	return c.src.Close()
}

// FromImage streams an already-decoded image as strips (zero copy where the
// image supports SubImage).
func FromImage(m image.Image, env *Env) Source {
	if env == nil {
		env = NewEnv(0, "")
	}
	return &imageSource{m: m, env: env, y: m.Bounds().Min.Y}
}

type imageSource struct {
	m   image.Image
	env *Env
	y   int
}

func (s *imageSource) Bounds() image.Rectangle { return s.m.Bounds() }
func (s *imageSource) ColorModel() color.Model { return s.m.ColorModel() }
func (s *imageSource) Env() *Env               { return s.env }
func (s *imageSource) Close() error            { s.m = nil; return nil }

func (s *imageSource) Next() (image.Image, error) {
	if s.m == nil {
		return nil, io.EOF
	}
	b := s.m.Bounds()
	if s.y >= b.Max.Y {
		return nil, io.EOF
	}
	n := StripRows(int64(b.Dx())*8, b.Dy())
	r := image.Rect(b.Min.X, s.y, b.Max.X, min(s.y+n, b.Max.Y))
	s.y = r.Max.Y
	if si, ok := s.m.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return si.SubImage(r), nil
	}
	band := NewBand(s.m, r)
	CopyRows(band, s.m, r.Min.Y, r.Max.Y)
	return band, nil
}

// Collect assembles a whole Source into one image (tests and small images).
func Collect(src Source) (image.Image, error) {
	b := src.Bounds()
	var out image.Image
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if out == nil {
			out = NewBand(s, b)
		}
		CopyRows(out, s, s.Bounds().Min.Y, s.Bounds().Max.Y)
	}
	if out == nil {
		return nil, io.ErrUnexpectedEOF
	}
	return out, nil
}
