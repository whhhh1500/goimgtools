package ops

import (
	"image"
	"image/color"
	"image/draw"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// BinaryAlpha makes every pixel either fully transparent or fully opaque,
// which is all a GIF can store: pixels less than half opaque become
// transparent (and lose their colour), the others keep their colour at
// full opacity. The result is NRGBA. A source known to be opaque is
// returned as it is.
func BinaryAlpha(src pipeline.Source) pipeline.Source {
	if pipeline.IsOpaque(src) {
		return src
	}
	return &binaryAlpha{src: src, env: pipeline.EnvOf(src)}
}

type binaryAlpha struct {
	src      pipeline.Source
	env      *pipeline.Env
	band     *image.NRGBA
	reserved int64
}

func (b *binaryAlpha) Env() *pipeline.Env      { return b.env }
func (b *binaryAlpha) Bounds() image.Rectangle { return b.src.Bounds() }
func (b *binaryAlpha) ColorModel() color.Model { return color.NRGBAModel }

func (b *binaryAlpha) Close() error {
	b.env.Budget.Release(b.reserved)
	b.reserved, b.band = 0, nil
	return b.src.Close()
}

func (b *binaryAlpha) Next() (image.Image, error) {
	s, err := b.src.Next()
	if err != nil {
		return nil, err
	}
	// A paletted strip whose entries are already all or nothing stays as
	// it is, so an image that is a palette already is not taken apart.
	if p, ok := s.(*image.Paletted); ok && binaryPalette(p.Palette) {
		return s, nil
	}
	r := s.Bounds()
	if b.band == nil || b.band.Rect.Dy() < r.Dy() {
		b.env.Budget.Release(b.reserved)
		b.reserved, b.band = 0, nil
		need := int64(r.Dx()) * 4 * int64(r.Dy())
		if err := b.env.Budget.Reserve(need, "alpha strip"); err != nil {
			return nil, err
		}
		b.reserved = need
		b.band = image.NewNRGBA(image.Rect(r.Min.X, 0, r.Max.X, r.Dy()))
	}
	out := *b.band
	out.Rect = r
	// Drawing into NRGBA undoes any premultiplication exactly as the
	// standard library does.
	draw.Draw(&out, r, s, r.Min, draw.Src)
	pix := out.Pix[:r.Dy()*out.Stride]
	for i := 0; i+3 < len(pix); i += 4 {
		if pix[i+3] < 128 {
			pix[i], pix[i+1], pix[i+2], pix[i+3] = 0, 0, 0, 0
		} else {
			pix[i+3] = 255
		}
	}
	return &out, nil
}

// binaryPalette reports whether every entry is fully transparent or fully
// opaque.
func binaryPalette(p color.Palette) bool {
	for _, c := range p {
		if _, _, _, a := c.RGBA(); a != 0 && a != 0xffff {
			return false
		}
	}
	return true
}
