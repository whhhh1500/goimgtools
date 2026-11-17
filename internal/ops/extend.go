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
// bottom pixels. The result, with its origin at (0, 0), is RGBA, but NRGBA
// for an NRGBA src that is not opaque or a colour that is not, so that the
// colours of translucent pixels stay as they are.
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
	if (!e.bgOpaque || !pipeline.IsOpaque(src)) && src.ColorModel() == color.NRGBAModel {
		e.straight = true
		e.src = pipeline.Map(src, func(*image.NRGBA) error { return nil })
		n := color.NRGBAModel.Convert(bg).(color.NRGBA)
		e.bgPix = [4]uint8{n.R, n.G, n.B, n.A}
	}
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
	straight  bool     // NRGBA strips
	bgPix     [4]uint8 // the colour, for them
}

func (e *extend) Env() *pipeline.Env      { return e.env }
func (e *extend) Bounds() image.Rectangle { return e.rect }
func (e *extend) ColorModel() color.Model {
	if e.straight {
		return color.NRGBAModel
	}
	return color.RGBAModel
}
func (e *extend) Opaque() bool { return e.bgOpaque && pipeline.IsOpaque(e.src) }
func (e *extend) Close() error { e.band.release(); return e.src.Close() }

// strip is the output strip for r.
func (e *extend) strip(r image.Rectangle) (image.Image, error) {
	if e.straight {
		return e.band.getNRGBA(r)
	}
	return e.band.get(r)
}

// fill paints r of out with the colour.
func (e *extend) fill(out image.Image, r image.Rectangle) {
	if !e.straight {
		draw.Draw(out.(draw.Image), r, e.bg, image.Point{}, draw.Src)
		return
	}
	o := out.(*image.NRGBA)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := o.Pix[o.PixOffset(r.Min.X, y):][:4*r.Dx()]
		for x := 0; x < len(row); x += 4 {
			copy(row[x:x+4], e.bgPix[:])
		}
	}
}

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
		out, err := e.strip(r)
		if err != nil {
			return nil, err
		}
		e.fill(out, r)
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
	out, err := e.strip(r)
	if err != nil {
		return nil, err
	}
	if at.Min.X > 0 {
		e.fill(out, image.Rect(0, r.Min.Y, at.Min.X, r.Max.Y))
	}
	if at.Max.X < r.Max.X {
		e.fill(out, image.Rect(at.Max.X, r.Min.Y, r.Max.X, r.Max.Y))
	}
	if e.straight {
		// NRGBA onto NRGBA, row by row.
		o, n := out.(*image.NRGBA), s.(*image.NRGBA)
		for y := 0; y < at.Dy(); y++ {
			copy(o.Pix[o.PixOffset(at.Min.X, at.Min.Y+y):][:4*at.Dx()], n.Pix[n.PixOffset(sr.Min.X, sr.Min.Y+y):])
		}
	} else {
		draw.Draw(out.(draw.Image), at, s, sr.Min, draw.Src)
	}
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
