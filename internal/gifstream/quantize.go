package gifstream

import (
	"image"
	"image/color"
	"image/color/palette"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// AnimPalette returns the palette animation frames are reduced to and the
// index of its transparent entry: the Plan 9 palette, the one gif.Encode
// uses, with the entry that has the closest neighbour given up for
// transparency.
func AnimPalette() (color.Palette, byte) {
	pal := append(color.Palette(nil), palette.Plan9...)
	best, bestDist := 0, uint32(1<<32-1)
	for i := range pal {
		ri, gi, bi, _ := pal[i].RGBA()
		for j := range pal {
			if i == j {
				continue
			}
			rj, gj, bj, _ := pal[j].RGBA()
			d := sqDiff(int32(ri), int32(rj)) + sqDiff(int32(gi), int32(gj)) + sqDiff(int32(bi), int32(bj))
			// Prefer the later entry of the closest pair.
			if d < bestDist || (d == bestDist && i > best) {
				best, bestDist = i, d
			}
		}
	}
	pal[best] = color.RGBA{}
	return pal, byte(best)
}

// Quantize returns src as *image.Paletted strips over pal, reduced with
// Floyd-Steinberg dithering one row at a time (the arithmetic Encode uses).
// Its working memory is two rows of quantization error and one strip of
// colour indexes.
func Quantize(src pipeline.Source, pal color.Palette) (pipeline.Source, error) {
	if err := checkPalette(pal); err != nil {
		return nil, err
	}
	b := src.Bounds()
	env := pipeline.EnvOf(src)
	need := 2 * quantErrorBytes(b.Dx())
	if err := env.Budget.Reserve(need, "gif dithering rows"); err != nil {
		return nil, err
	}
	return &quantized{src: src, env: env, pal: pal, d: newDitherer(pal, b.Dx()), reserved: need}, nil
}

type quantized struct {
	src      pipeline.Source
	env      *pipeline.Env
	pal      color.Palette
	d        *ditherer
	band     *image.Paletted
	bandRes  int64
	reserved int64
}

func (q *quantized) Env() *pipeline.Env      { return q.env }
func (q *quantized) Bounds() image.Rectangle { return q.src.Bounds() }
func (q *quantized) ColorModel() color.Model { return q.pal }
func (q *quantized) Opaque() bool            { return pipeline.IsOpaque(q.src) }

func (q *quantized) Close() error {
	q.env.Budget.Release(q.reserved + q.bandRes)
	q.reserved, q.bandRes, q.band = 0, 0, nil
	return q.src.Close()
}

func (q *quantized) Next() (image.Image, error) {
	s, err := q.src.Next()
	if err != nil {
		return nil, err
	}
	r := s.Bounds()
	if q.band == nil || q.band.Rect.Dy() < r.Dy() {
		q.env.Budget.Release(q.bandRes)
		q.bandRes, q.band = 0, nil
		need := int64(r.Dx()) * int64(r.Dy())
		if err := q.env.Budget.Reserve(need, "gif index strip"); err != nil {
			return nil, err
		}
		q.bandRes = need
		q.band = image.NewPaletted(image.Rect(r.Min.X, 0, r.Max.X, r.Dy()), q.pal)
	}
	out := *q.band
	out.Rect = r
	px := pixelFunc(s)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		o := (y - r.Min.Y) * out.Stride
		q.d.row(px, r.Min.X, y, out.Pix[o:o+r.Dx()])
	}
	return &out, nil
}
