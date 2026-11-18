package ops

import (
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/internal/watermark"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Watermark adds an invisible mark to src, row by row (see package
// watermark), as 8-bit NRGBA; transparency is kept, and a grey picture
// stays grey (the mark moves red, green and blue alike). Where the mark
// depends on the picture's texture (a small picture's blind mark), the
// picture is first recorded (in working storage, a temporary file beyond
// its share of the limit) while its texture is measured, and played back.
func Watermark(src pipeline.Source, payload []byte, k watermark.Key, o watermark.Options) (pipeline.Source, error) {
	b := src.Bounds()
	if _, err := watermark.NewMarker(payload, k, o, b.Dx(), b.Dy()); err != nil {
		return nil, err
	}
	grey := src.ColorModel() == color.GrayModel || src.ColorModel() == color.Gray16Model
	in := src
	var rec *pipeline.Recording
	if watermark.NeedsTexture(o, b.Dx(), b.Dy()) {
		var err error
		if rec, err = pipeline.Record(src); err != nil {
			return nil, err
		}
		if o.Texture, err = texture(rec, b.Dx()); err != nil {
			rec.Close()
			return nil, err
		}
		if in, err = rec.Source(); err != nil {
			rec.Close()
			return nil, err
		}
	}
	m, err := watermark.NewMarker(payload, k, o, b.Dx(), b.Dy())
	if err != nil {
		return nil, err
	}
	var out pipeline.Source
	out, err = newWindowAt(in, m.Radius(), m.NewRow)
	if err != nil {
		if rec != nil {
			in.Close()
			rec.Close()
		}
		return nil, err
	}
	if rec != nil {
		out = &recorded{Source: out, rec: rec, src: src}
	}
	if grey {
		return Grayscale(out), nil
	}
	return out, nil
}

// texture measures the texture of a recorded picture w pixels wide
// (watermark.TextureMeter) from a play-back of it.
func texture(rec *pipeline.Recording, w int) (float64, error) {
	play, err := rec.Source()
	if err != nil {
		return 0, err
	}
	meter := watermark.NewTextureMeter(w)
	scan := pipeline.Map(play, func(s *image.NRGBA) error {
		for y := range s.Rect.Dy() {
			meter.Row(s.Pix[y*s.Stride:][:4*w])
		}
		return nil
	})
	defer scan.Close()
	for {
		if _, err := scan.Next(); err != nil {
			if err == io.EOF {
				return meter.Mean(), nil
			}
			return 0, err
		}
	}
}
