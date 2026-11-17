package ops

import (
	"image"
	"image/color"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// CMYKToSRGB turns the CMYK strips of src into sRGB through the CMYK
// colour profile t was made from, as browsers and sharp show such an
// image; the result has no profile (Meta leaves it out). Strips of other
// types pass through. With helpers in the pipeline the rows of each strip
// are shared out, as for ToSRGB.
func CMYKToSRGB(src pipeline.Source, t *iccprof.Transform) pipeline.Source {
	return &cmykToSRGB{src: src, env: pipeline.EnvOf(src), t: t}
}

type cmykToSRGB struct {
	src      pipeline.Source
	env      *pipeline.Env
	t        *iccprof.Transform
	buf      []uint8
	reserved int64
}

func (c *cmykToSRGB) Bounds() image.Rectangle { return c.src.Bounds() }
func (c *cmykToSRGB) ColorModel() color.Model { return color.NRGBAModel }
func (c *cmykToSRGB) Env() *pipeline.Env      { return c.env }
func (c *cmykToSRGB) Opaque() bool            { return true }

func (c *cmykToSRGB) Meta() pipeline.Meta {
	m := pipeline.MetaOf(c.src)
	m.ICC = nil
	return m
}

func (c *cmykToSRGB) Close() error {
	c.env.Budget.Release(c.reserved)
	c.reserved, c.buf = 0, nil
	return c.src.Close()
}

func (c *cmykToSRGB) Next() (image.Image, error) {
	s, err := c.src.Next()
	if err != nil {
		return nil, err
	}
	m, ok := s.(*image.CMYK)
	if !ok {
		return s, nil
	}
	r := m.Rect
	w, rows := r.Dx(), r.Dy()
	need := 4 * int64(w) * int64(rows)
	if int64(len(c.buf)) < need {
		c.env.Budget.Release(c.reserved)
		c.reserved, c.buf = 0, nil
		if err := c.env.Budget.Reserve(need, "cmyk to srgb strip"); err != nil {
			return nil, err
		}
		c.reserved, c.buf = need, make([]uint8, need)
	}
	out := &image.NRGBA{Pix: c.buf[:need], Stride: 4 * w, Rect: r}
	parts := max(1, min(c.env.Helpers()+1, rows, rows*w/(8<<10)))
	c.env.Parallel(parts, func(k int) {
		for y := k * rows / parts; y < (k+1)*rows/parts; y++ {
			in := m.Pix[y*m.Stride:][:4*w]
			d := out.Pix[y*out.Stride:][:4*w]
			var last [4]uint8
			var lr, lg, lb uint8
			have := false
			for x := 0; x < 4*w; x += 4 {
				p := [4]uint8{in[x], in[x+1], in[x+2], in[x+3]}
				if !have || p != last {
					lr, lg, lb = c.t.ConvertCMYK(p[0], p[1], p[2], p[3])
					last, have = p, true
				}
				d[x], d[x+1], d[x+2], d[x+3] = lr, lg, lb, 0xff
			}
		}
	})
	return out, nil
}
