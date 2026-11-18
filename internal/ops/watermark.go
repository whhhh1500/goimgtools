package ops

import (
	"image/color"

	"github.com/whhhh1500/goimgtools/internal/watermark"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Watermark adds an invisible mark to src, row by row (see package
// watermark), as 8-bit NRGBA; transparency is kept, and a grey picture
// stays grey (the mark moves red, green and blue alike).
func Watermark(src pipeline.Source, payload []byte, k watermark.Key, o watermark.Options) (pipeline.Source, error) {
	b := src.Bounds()
	m, err := watermark.NewMarker(payload, k, o, b.Dx(), b.Dy())
	if err != nil {
		return nil, err
	}
	grey := src.ColorModel() == color.GrayModel || src.ColorModel() == color.Gray16Model
	out, err := newWindowAt(src, m.Radius(), m.NewRow)
	if err != nil {
		return nil, err
	}
	if grey {
		return Grayscale(out), nil
	}
	return out, nil
}
