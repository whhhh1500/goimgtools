// Package ops holds the streaming pixel operations other than scaling:
// crop, flatten onto a background colour, and overlay (watermark).
package ops

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// rgbaBand is a reusable RGBA strip buffer charged to the budget.
type rgbaBand struct {
	env      *pipeline.Env
	band     *image.RGBA
	nband    *image.NRGBA
	reserved int64
}

// get returns an RGBA strip covering r, reusing the buffer.
func (b *rgbaBand) get(r image.Rectangle) (*image.RGBA, error) {
	if b.band == nil || b.band.Rect.Dy() < r.Dy() {
		b.release()
		rows := max(r.Dy(), 16)
		need := int64(r.Dx()) * 4 * int64(rows)
		if err := b.env.Budget.Reserve(need, "strip buffer"); err != nil {
			return nil, err
		}
		b.reserved = need
		b.band = image.NewRGBA(image.Rect(r.Min.X, 0, r.Max.X, rows))
	}
	return pipeline.Relabel(b.band, r).(*image.RGBA), nil
}

// getNRGBA is get for an NRGBA strip.
func (b *rgbaBand) getNRGBA(r image.Rectangle) (*image.NRGBA, error) {
	if b.nband == nil || b.nband.Rect.Dy() < r.Dy() {
		b.release()
		rows := max(r.Dy(), 16)
		need := int64(r.Dx()) * 4 * int64(rows)
		if err := b.env.Budget.Reserve(need, "strip buffer"); err != nil {
			return nil, err
		}
		b.reserved = need
		b.nband = image.NewNRGBA(image.Rect(r.Min.X, 0, r.Max.X, rows))
	}
	return pipeline.Relabel(b.nband, r).(*image.NRGBA), nil
}

func (b *rgbaBand) release() {
	if b.reserved != 0 {
		b.env.Budget.Release(b.reserved)
		b.reserved = 0
	}
	b.band, b.nband = nil, nil
}

// ---- crop ----

type crop struct {
	src      pipeline.Source
	env      *pipeline.Env
	rect     image.Rectangle
	last     image.Image
	reserved int64
	done     bool
}

// Crop keeps rect, given relative to the top-left corner of src. The result
// has its own origin at (0,0)-relative coordinates of rect within src.
func Crop(src pipeline.Source, left, top, width, height int) (pipeline.Source, error) {
	b := src.Bounds()
	r := image.Rect(left, top, left+width, top+height).Add(b.Min)
	if width <= 0 || height <= 0 || !r.In(b) {
		return nil, errors.New("goimgtools: extract area is outside the image")
	}
	return &crop{src: src, env: pipeline.EnvOf(src), rect: r}, nil
}

func (c *crop) Env() *pipeline.Env      { return c.env }
func (c *crop) Bounds() image.Rectangle { return c.rect }
func (c *crop) ColorModel() color.Model { return c.src.ColorModel() }
func (c *crop) Opaque() bool            { return pipeline.IsOpaque(c.src) }

func (c *crop) Close() error {
	if c.reserved != 0 {
		c.env.Budget.Release(c.reserved)
		c.reserved = 0
	}
	return c.src.Close()
}

func (c *crop) Next() (image.Image, error) {
	if c.done {
		return nil, io.EOF
	}
	for {
		s, err := c.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		sb := s.Bounds()
		in := sb.Intersect(c.rect)
		if in.Empty() {
			continue
		}
		final := sb.Max.Y >= c.rect.Max.Y
		sub, ok := s.(interface {
			SubImage(image.Rectangle) image.Image
		})
		var out image.Image
		if ok && !(final && sb.Max.Y < c.src.Bounds().Max.Y) {
			out = sub.SubImage(in)
		} else {
			// The strip must outlive the Next calls that drain the source.
			if c.reserved == 0 {
				need := pipeline.BandBytes(s, in)
				if err := c.env.Budget.Reserve(need, "crop strip"); err != nil {
					return nil, err
				}
				c.reserved = need
			}
			out = pipeline.NewBand(s, in)
			pipeline.CopyRows(out, s, in.Min.Y, in.Max.Y)
		}
		if final {
			// Let the decoder verify the rest of its input.
			for {
				_, err := c.src.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					return nil, err
				}
			}
			c.done = true
		}
		return out, nil
	}
}

// ---- flatten ----

type flatten struct {
	src  pipeline.Source
	env  *pipeline.Env
	bg   *image.Uniform
	band rgbaBand
}

// Flatten composites src over the background colour, removing transparency.
// A source known to be opaque is returned unchanged.
func Flatten(src pipeline.Source, bg color.Color) pipeline.Source {
	if pipeline.IsOpaque(src) {
		return src
	}
	env := pipeline.EnvOf(src)
	r, g, b, _ := bg.RGBA()
	opaque := color.RGBA64{uint16(r), uint16(g), uint16(b), 0xffff}
	return &flatten{src: src, env: env, bg: image.NewUniform(opaque), band: rgbaBand{env: env}}
}

func (f *flatten) Env() *pipeline.Env      { return f.env }
func (f *flatten) Bounds() image.Rectangle { return f.src.Bounds() }
func (f *flatten) ColorModel() color.Model { return color.RGBAModel }
func (f *flatten) Opaque() bool            { return true }
func (f *flatten) Close() error            { f.band.release(); return f.src.Close() }

func (f *flatten) Next() (image.Image, error) {
	s, err := f.src.Next()
	if err != nil {
		return nil, err
	}
	r := s.Bounds()
	out, err := f.band.get(r)
	if err != nil {
		return nil, err
	}
	draw.Draw(out, r, f.bg, image.Point{}, draw.Src)
	draw.Draw(out, r, s, r.Min, draw.Over)
	return out, nil
}

// ---- overlay ----

type overlay struct {
	src      pipeline.Source
	env      *pipeline.Env
	top      image.Image
	at       image.Rectangle // where top lands, in src coordinates
	band     rgbaBand
	reserved int64
	// stepX and stepY, when positive, repeat top that far apart over the
	// whole image, with one copy at "at".
	stepX, stepY int
	// mode is the blend mode (see blend.go); for one but BlendOver, top is
	// an *image.NRGBA.
	mode int
	// base keeps the strip in a mode that consumes the image (start).
	base *image.RGBA
}

// Overlay draws top over src with its top-left corner at (x, y) relative to
// src's top-left corner, in the blend mode given (BlendOver and the others
// of blend.go). top is held in memory whole (it is charged to the budget),
// so it should be small, like a watermark. Parts of top that fall outside
// src are clipped. The result is RGBA.
func Overlay(src pipeline.Source, top image.Image, x, y, mode int) (pipeline.Source, error) {
	if !ValidBlend(mode) {
		return nil, errors.New("goimgtools: unknown blend mode")
	}
	env := pipeline.EnvOf(src)
	need := pipeline.BandBytes(top, top.Bounds())
	if mode != BlendOver {
		if _, ok := top.(*image.NRGBA); !ok {
			need = 4 * int64(top.Bounds().Dx()) * int64(top.Bounds().Dy())
			n := image.NewNRGBA(top.Bounds())
			draw.Draw(n, n.Rect, top, n.Rect.Min, draw.Src)
			top = n
		}
	}
	if err := env.Budget.Reserve(need, "overlay image"); err != nil {
		return nil, err
	}
	b := src.Bounds()
	at := top.Bounds().Sub(top.Bounds().Min).Add(b.Min).Add(image.Pt(x, y))
	return &overlay{src: src, env: env, top: top, at: at, band: rgbaBand{env: env}, reserved: need, mode: mode}, nil
}

// put draws the part in of the copy of top at "at" over base into out.
func (o *overlay) put(out, base *image.RGBA, in, at image.Rectangle) {
	sp := o.top.Bounds().Min.Add(in.Min.Sub(at.Min))
	if o.mode == BlendOver {
		draw.Draw(out, in, o.top, sp, draw.Over)
		return
	}
	drawBlend(out, base, in, o.top.(*image.NRGBA), sp, o.mode)
}

// start fills out with the strip s, or, in a mode that consumes the image
// where the overlay is not, with transparency, the strip then kept in base
// for the parts the overlay covers.
func (o *overlay) start(out *image.RGBA, s image.Image, r image.Rectangle) (*image.RGBA, error) {
	draw.Draw(out, r, s, r.Min, draw.Src)
	if !consumes(o.mode) {
		return out, nil
	}
	if o.base == nil || len(o.base.Pix) < len(out.Pix) {
		need := int64(len(out.Pix))
		if err := o.env.Budget.Reserve(need, "overlay strip"); err != nil {
			return nil, err
		}
		o.reserved += need
		o.base = &image.RGBA{Pix: make([]byte, len(out.Pix))}
	}
	base := &image.RGBA{Pix: o.base.Pix[:len(out.Pix)], Stride: out.Stride, Rect: out.Rect}
	copy(base.Pix, out.Pix)
	clear(out.Pix)
	return base, nil
}

// OverlayTiled is Overlay with top repeated over the whole image: one copy
// has its top-left corner at (x, y) and the others follow every
// top-width + gapX pixels across and top-height + gapY pixels down, in all
// directions. A gap may be negative (copies overlap) as long as the step
// stays positive.
func OverlayTiled(src pipeline.Source, top image.Image, x, y, gapX, gapY, mode int) (pipeline.Source, error) {
	stepX, stepY := top.Bounds().Dx()+gapX, top.Bounds().Dy()+gapY
	if stepX < 1 || stepY < 1 {
		return nil, errors.New("goimgtools: tile gap leaves no room between copies")
	}
	s, err := Overlay(src, top, x, y, mode)
	if err != nil {
		return nil, err
	}
	o := s.(*overlay)
	o.stepX, o.stepY = stepX, stepY
	return o, nil
}

// Fade returns top as RGBA with the opacity of every pixel multiplied by f
// (0 to 1).
func Fade(top image.Image, f float64) *image.RGBA {
	b := top.Bounds()
	out := image.NewRGBA(b)
	draw.Draw(out, b, top, b.Min, draw.Src)
	k := uint32(float64(f*255) + 0.5)
	for i, v := range out.Pix {
		out.Pix[i] = uint8((uint32(v)*k + 127) / 255)
	}
	return out
}

// floorDiv is a / b rounded down (b > 0).
func floorDiv(a, b int) int {
	q := a / b
	if a%b != 0 && a < 0 {
		q--
	}
	return q
}

func (o *overlay) Env() *pipeline.Env      { return o.env }
func (o *overlay) Bounds() image.Rectangle { return o.src.Bounds() }
func (o *overlay) ColorModel() color.Model { return color.RGBAModel }
func (o *overlay) Opaque() bool            { return pipeline.IsOpaque(o.src) }

func (o *overlay) Close() error {
	o.band.release()
	if o.reserved != 0 {
		o.env.Budget.Release(o.reserved)
		o.reserved = 0
	}
	return o.src.Close()
}

func (o *overlay) Next() (image.Image, error) {
	s, err := o.src.Next()
	if err != nil {
		return nil, err
	}
	r := s.Bounds()
	if o.stepX > 0 {
		out, err := o.band.get(r)
		if err != nil {
			return nil, err
		}
		base, err := o.start(out, s, r)
		if err != nil {
			return nil, err
		}
		w, h := o.at.Dx(), o.at.Dy()
		// The copies whose rows and columns reach into this strip.
		j0, j1 := floorDiv(r.Min.Y-o.at.Min.Y-h+1+o.stepY-1, o.stepY), floorDiv(r.Max.Y-1-o.at.Min.Y, o.stepY)
		i0, i1 := floorDiv(r.Min.X-o.at.Min.X-w+1+o.stepX-1, o.stepX), floorDiv(r.Max.X-1-o.at.Min.X, o.stepX)
		for j := j0; j <= j1; j++ {
			for i := i0; i <= i1; i++ {
				at := o.at.Add(image.Pt(i*o.stepX, j*o.stepY))
				if in := r.Intersect(at); !in.Empty() {
					o.put(out, base, in, at)
				}
			}
		}
		return out, nil
	}
	in := r.Intersect(o.at)
	if _, ok := s.(*image.RGBA); ok && in.Empty() && !consumes(o.mode) {
		return s, nil
	}
	out, err := o.band.get(r)
	if err != nil {
		return nil, err
	}
	base, err := o.start(out, s, r)
	if err != nil {
		return nil, err
	}
	if !in.Empty() {
		o.put(out, base, in, o.at)
	}
	return out, nil
}
