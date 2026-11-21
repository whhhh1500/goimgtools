package goimgtools

import (
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Blank is an image of width x height pixels all of colour c, as sharp's
// create and Pillow's Image.new make one: a canvas to Composite pictures
// and text onto, a background, a placeholder. A translucent colour (or
// color.Transparent) gives an image with transparency. Its rows are made
// as they are asked for, so it takes no memory of its size. Without an
// output format the result is written as PNG.
func Blank(width, height int, c color.Color) *Image {
	if width < 1 || height < 1 {
		return &Image{err: errors.New("goimgtools: Blank needs a width and a height of at least 1")}
	}
	if c == nil {
		return &Image{err: errors.New("goimgtools: Blank needs a colour")}
	}
	return &Image{open: func() (*input, error) {
		return &input{kind: "blank", gen: func(env *pipeline.Env) (pipeline.Source, error) {
			return newBlank(width, height, c, env)
		}}, nil
	}}
}

// blankSource gives strips of one colour.
type blankSource struct {
	env    *pipeline.Env
	b      image.Rectangle
	c      color.NRGBA
	rows   int
	y      int
	strip  *image.NRGBA
	held   int64
	closed bool
}

func newBlank(w, h int, c color.Color, env *pipeline.Env) (*blankSource, error) {
	s := &blankSource{env: env, b: image.Rect(0, 0, w, h), c: color.NRGBAModel.Convert(c).(color.NRGBA)}
	s.rows = pipeline.StripRows(4*int64(w), h)
	s.held = 4 * int64(w) * int64(s.rows)
	if err := env.Budget.Reserve(s.held, "blank strip"); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *blankSource) Bounds() image.Rectangle { return s.b }
func (s *blankSource) ColorModel() color.Model { return color.NRGBAModel }
func (s *blankSource) Env() *pipeline.Env      { return s.env }
func (s *blankSource) Opaque() bool            { return s.c.A == 0xff }

func (s *blankSource) Next() (image.Image, error) {
	if s.y >= s.b.Max.Y {
		return nil, io.EOF
	}
	n := min(s.rows, s.b.Max.Y-s.y)
	if s.strip == nil {
		s.strip = image.NewNRGBA(image.Rect(0, 0, s.b.Dx(), s.rows))
	}
	// Filled every time: the strip is the caller's to change until the
	// next one.
	pix := s.strip.Pix[:4*s.b.Dx()*n]
	p := [4]uint8{s.c.R, s.c.G, s.c.B, s.c.A}
	copy(pix, p[:])
	for k := 4; k < len(pix); k *= 2 {
		copy(pix[k:], pix[:k])
	}
	st := &image.NRGBA{Pix: pix, Stride: s.strip.Stride, Rect: image.Rect(0, s.y, s.b.Dx(), s.y+n)}
	s.y += n
	return st, nil
}

func (s *blankSource) Close() error {
	if !s.closed {
		s.closed = true
		s.env.Budget.Release(s.held)
	}
	return nil
}
