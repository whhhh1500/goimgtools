package ops

import (
	"image"
	"image/color"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// GrayToSRGB maps the grey strips of src through the grey profile t was
// made from to sRGB's greys; the image stays grey. Strips of other types
// pass through.
func GrayToSRGB(src pipeline.Source, t *iccprof.Transform) pipeline.Source {
	g := &graySRGB{src: src, env: pipeline.EnvOf(src)}
	for v := range g.lut {
		g.lut[v], _, _ = t.Convert(uint8(v), uint8(v), uint8(v))
	}
	return g
}

type graySRGB struct {
	src      pipeline.Source
	env      *pipeline.Env
	lut      [256]uint8
	buf      []uint8
	reserved int64
}

func (g *graySRGB) Bounds() image.Rectangle { return g.src.Bounds() }
func (g *graySRGB) ColorModel() color.Model { return color.GrayModel }
func (g *graySRGB) Env() *pipeline.Env      { return g.env }
func (g *graySRGB) Opaque() bool            { return true }
func (g *graySRGB) Meta() pipeline.Meta {
	m := pipeline.MetaOf(g.src)
	m.ICC = nil
	return m
}

func (g *graySRGB) Close() error {
	g.env.Budget.Release(g.reserved)
	g.reserved, g.buf = 0, nil
	return g.src.Close()
}

func (g *graySRGB) Next() (image.Image, error) {
	s, err := g.src.Next()
	if err != nil {
		return nil, err
	}
	m, ok := s.(*image.Gray)
	if !ok {
		return s, nil
	}
	r := m.Rect
	w, rows := r.Dx(), r.Dy()
	need := int64(w) * int64(rows)
	if int64(len(g.buf)) < need {
		g.env.Budget.Release(g.reserved)
		g.reserved, g.buf = 0, nil
		if err := g.env.Budget.Reserve(need, "grey to srgb strip"); err != nil {
			return nil, err
		}
		g.reserved, g.buf = need, make([]uint8, need)
	}
	out := &image.Gray{Pix: g.buf[:need], Stride: w, Rect: r}
	for y := 0; y < rows; y++ {
		d := out.Pix[y*w : y*w+w]
		for x, v := range m.Pix[y*m.Stride:][:w] {
			d[x] = g.lut[v]
		}
	}
	return out, nil
}
