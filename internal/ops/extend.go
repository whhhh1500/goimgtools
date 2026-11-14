package ops

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Extend adds a border of the given colour around src: left, top, right and
// bottom pixels. The result is RGBA with its origin at (0, 0).
func Extend(src pipeline.Source, left, top, right, bottom int, bg color.Color) (pipeline.Source, error) {
	if left < 0 || top < 0 || right < 0 || bottom < 0 {
		return nil, errors.New("goimgtools: a border cannot be negative")
	}
	b := src.Bounds()
	w, h := b.Dx()+left+right, b.Dy()+top+bottom
	if w > 1<<24 || h > 1<<24 {
		return nil, errors.New("goimgtools: border makes the image too large")
	}
	env := pipeline.EnvOf(src)
	_, _, _, a := bg.RGBA()
	e := &extend{src: src, env: env, bg: image.NewUniform(bg), band: rgbaBand{env: env},
		rect: image.Rect(0, 0, w, h), left: left, top: top, bgOpaque: a == 0xffff}
	e.n = pipeline.StripRows(int64(w)*4, h)
	return e, nil
}

type extend struct {
	src       pipeline.Source
	env       *pipeline.Env
	bg        *image.Uniform
	band      rgbaBand
	rect      image.Rectangle
	left, top int
	n         int // rows per border strip
	y         int // next output row
	srcDone   bool
	bgOpaque  bool
}

func (e *extend) Env() *pipeline.Env      { return e.env }
func (e *extend) Bounds() image.Rectangle { return e.rect }
func (e *extend) ColorModel() color.Model { return color.RGBAModel }
func (e *extend) Opaque() bool            { return e.bgOpaque && pipeline.IsOpaque(e.src) }
func (e *extend) Close() error            { e.band.release(); return e.src.Close() }

func (e *extend) Next() (image.Image, error) {
	if e.y >= e.rect.Max.Y {
		return nil, io.EOF
	}
	sb := e.src.Bounds()
	bottomStart := e.top + sb.Dy()
	if e.y < e.top || e.y >= bottomStart {
		// The source is read to its end before the bottom border and the
		// final strip, so the decoder has verified its input.
		if e.y >= bottomStart && !e.srcDone {
			if _, err := e.src.Next(); err != io.EOF {
				if err == nil {
					err = errors.New("goimgtools: source has more rows than its bounds")
				}
				return nil, err
			}
			e.srcDone = true
		}
		limit := e.rect.Max.Y
		if e.y < e.top {
			limit = e.top
		}
		r := image.Rect(0, e.y, e.rect.Max.X, min(e.y+e.n, limit))
		out, err := e.band.get(r)
		if err != nil {
			return nil, err
		}
		draw.Draw(out, r, e.bg, image.Point{}, draw.Src)
		e.y = r.Max.Y
		return out, nil
	}
	s, err := e.src.Next()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	sr := s.Bounds()
	at := sr.Sub(sb.Min).Add(image.Pt(e.left, e.top)) // where the strip lands
	r := image.Rect(0, at.Min.Y, e.rect.Max.X, at.Max.Y)
	out, err := e.band.get(r)
	if err != nil {
		return nil, err
	}
	if at.Min.X > 0 {
		draw.Draw(out, image.Rect(0, r.Min.Y, at.Min.X, r.Max.Y), e.bg, image.Point{}, draw.Src)
	}
	if at.Max.X < r.Max.X {
		draw.Draw(out, image.Rect(at.Max.X, r.Min.Y, r.Max.X, r.Max.Y), e.bg, image.Point{}, draw.Src)
	}
	draw.Draw(out, at, s, sr.Min, draw.Src)
	e.y = r.Max.Y
	if e.y == bottomStart && bottomStart == e.rect.Max.Y {
		// No bottom border: this is the final strip, drain the source now.
		if _, err := e.src.Next(); err != io.EOF {
			if err == nil {
				err = errors.New("goimgtools: source has more rows than its bounds")
			}
			return nil, err
		}
		e.srcDone = true
	}
	return out, nil
}
