package goimgtools

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"time"

	"github.com/whhhh1500/goimgtools/internal/gifstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Animated makes the chain apply to every frame of an animated GIF and
// write an animated GIF. Without it only the first frame is used. It takes
// effect when the input is a GIF and the output is a GIF; otherwise it is
// ignored.
//
// Each frame is drawn onto the full picture first (GIF frames often hold
// only the part that changed), then goes through the chain as a complete
// image, and is written as a full frame reduced to a fixed 256-colour
// palette with dithering. The picture between frames is kept in working
// storage: a temporary file when it does not fit in the memory limit.
// Memory does not grow with the number of frames.
//
// Time does: every frame costs as much as a still image of that size. So
// LimitInputPixels counts the pixels of all frames together (width x
// height x frames), and an animation with more is refused with an
// ErrPixelLimit once that many have been processed.
func (m *Image) Animated() *Image { m.animated = true; return m }

// Frames is what Image.Frames reports about an input that may be an
// animation.
type Frames struct {
	// Count is the number of frames: 1 for a still image.
	Count int
	// Duration is how long the frames are shown one after the other, once
	// through. Zero for a still image.
	Duration time.Duration
	// LoopCount is how often an animation plays: 0 for ever, n > 0 for n
	// times, -1 when the file does not say (viewers then play it once).
	LoopCount int
}

// Frames goes through the input and reports how many frames it has and how
// long they play. Only a GIF can have more than one.
//
// Unlike Metadata it reads the whole file, since a GIF does not say in its
// header how many frames follow; the pixels are stepped over, not decoded,
// so it costs the reading and little else. It does not depend on Animated.
func (m *Image) Frames() (fr Frames, err error) {
	defer guard(&err)
	if m.err != nil {
		return Frames{}, m.err
	}
	f, format, err := m.openInput()
	if err != nil {
		return Frames{}, err
	}
	if format != "gif" {
		// A still image, once its header says that it is an image at all.
		src, err := m.decode(m.newEnv(), f, format)
		if err != nil {
			return Frames{}, err
		}
		src.Close()
		f.Close()
		return Frames{Count: 1}, nil
	}
	defer f.Close()
	anim, err := gifstream.OpenAnimation(f.br, m.newEnv())
	if err != nil {
		return Frames{}, f.classify(err)
	}
	defer anim.Close()
	fr.LoopCount = anim.Config().LoopCount
	for {
		if m.ctx != nil {
			if err := m.ctx.Err(); err != nil {
				return Frames{}, err
			}
		}
		frame, info, err := anim.NextFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			return Frames{}, f.classify(err)
		}
		frame.Close() // unread: its data is stepped over
		fr.Count++
		fr.Duration += time.Duration(info.Delay) * 10 * time.Millisecond
	}
	if fr.Count == 0 {
		return Frames{}, fmt.Errorf("%w: the GIF has no frames", ErrFormat)
	}
	return fr, nil
}

// GIF disposal methods.
const (
	disposalBackground = 2
	disposalPrevious   = 3
)

// canvas is the picture an animation has built up so far, as RGBA.
type canvas struct {
	env      *pipeline.Env
	sc       *pipeline.Scratch
	w, h     int
	row      []uint8
	reserved int64
}

func newCanvas(env *pipeline.Env, w, h int) (*canvas, error) {
	need := int64(w) * 4
	if err := env.Budget.Reserve(need, "animation row"); err != nil {
		return nil, err
	}
	sc, err := pipeline.NewScratch(env, int64(w)*int64(h)*4)
	if err != nil {
		env.Budget.Release(need)
		return nil, err
	}
	return &canvas{env: env, sc: sc, w: w, h: h, row: make([]uint8, w*4), reserved: need}, nil
}

func (c *canvas) close() {
	if c.sc != nil {
		c.sc.Close()
		c.sc = nil
	}
	c.env.Budget.Release(c.reserved)
	c.reserved = 0
}

func (c *canvas) off(x, y int) int64 { return (int64(y)*int64(c.w) + int64(x)) * 4 }

// draw puts a frame on the canvas: every pixel but the transparent ones.
// When the frame is to be undone afterwards (disposal "previous"), what it
// covers is copied to saved first.
func (c *canvas) draw(f pipeline.Source, info gifstream.FrameInfo, saved *pipeline.Spill) error {
	clip := image.Rect(0, 0, c.w, c.h)
	for {
		s, err := f.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p, ok := s.(*image.Paletted)
		if !ok {
			return errors.New("goimgtools: animation frame is not paletted")
		}
		var lut [256][4]uint8
		var have [256]bool
		for i, col := range p.Palette {
			if i > 255 {
				break
			}
			r, g, b, a := col.RGBA()
			lut[i], have[i] = [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}, a != 0
		}
		if info.Transparent {
			have[info.TransparentIndex] = false
		}
		r := p.Rect.Intersect(clip)
		if r.Empty() {
			continue
		}
		row := c.row[:r.Dx()*4]
		for y := r.Min.Y; y < r.Max.Y; y++ {
			if _, err := c.sc.ReadAt(row, c.off(r.Min.X, y)); err != nil {
				return err
			}
			if saved != nil {
				if _, err := saved.Write(row); err != nil {
					return err
				}
			}
			pix := p.Pix[p.PixOffset(r.Min.X, y):][:r.Dx()]
			for x, idx := range pix {
				if have[idx] {
					copy(row[4*x:4*x+4], lut[idx][:])
				}
			}
			if _, err := c.sc.WriteAt(row, c.off(r.Min.X, y)); err != nil {
				return err
			}
		}
	}
}

// dispose undoes or clears the area r of the frame just shown, as its
// disposal method says.
func (c *canvas) dispose(r image.Rectangle, disposal byte, saved *pipeline.Spill) error {
	r = r.Intersect(image.Rect(0, 0, c.w, c.h))
	if r.Empty() || (disposal != disposalBackground && disposal != disposalPrevious) {
		return nil
	}
	row := c.row[:r.Dx()*4]
	clear(row)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		if disposal == disposalPrevious {
			if _, err := saved.ReadAt(row, int64(y-r.Min.Y)*int64(len(row))); err != nil {
				return err
			}
		}
		if _, err := c.sc.WriteAt(row, c.off(r.Min.X, y)); err != nil {
			return err
		}
	}
	return nil
}

// canvasSource streams the canvas as it stands.
type canvasSource struct {
	c        *canvas
	band     *image.RGBA
	n, y     int
	reserved int64
}

func (c *canvas) source() (*canvasSource, error) {
	n := pipeline.StripRows(int64(c.w)*4, c.h)
	need := int64(c.w) * 4 * int64(n)
	if err := c.env.Budget.Reserve(need, "animation strip"); err != nil {
		return nil, err
	}
	return &canvasSource{c: c, band: image.NewRGBA(image.Rect(0, 0, c.w, n)), n: n, reserved: need}, nil
}

func (s *canvasSource) Env() *pipeline.Env      { return s.c.env }
func (s *canvasSource) Bounds() image.Rectangle { return image.Rect(0, 0, s.c.w, s.c.h) }
func (s *canvasSource) ColorModel() color.Model { return color.RGBAModel }

func (s *canvasSource) Close() error {
	s.c.env.Budget.Release(s.reserved)
	s.reserved = 0
	return nil
}

func (s *canvasSource) Next() (image.Image, error) {
	if s.y >= s.c.h {
		return nil, io.EOF
	}
	y1 := min(s.y+s.n, s.c.h)
	out := *s.band
	out.Rect = image.Rect(0, s.y, s.c.w, y1)
	if _, err := s.c.sc.ReadAt(out.Pix[:(y1-s.y)*out.Stride], s.c.off(0, s.y)); err != nil {
		return nil, err
	}
	s.y = y1
	return &out, nil
}

// writeAnimated runs the chain over every frame of the animated GIF in f.
func (m *Image) writeAnimated(w io.Writer, f *input, env *pipeline.Env) error {
	anim, err := gifstream.OpenAnimation(f.br, env)
	if err != nil {
		return f.classify(err)
	}
	defer anim.Close()
	cfg := anim.Config()
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return fmt.Errorf("%w: the image has no pixels", ErrFormat)
	}
	if limit := m.pixelLimit(); int64(cfg.Width)*int64(cfg.Height) > limit {
		return fmt.Errorf("%w: %dx%d is more than the limit of %d pixels (see LimitInputPixels)", ErrPixelLimit, cfg.Width, cfg.Height, limit)
	}
	cv, err := newCanvas(env, cfg.Width, cfg.Height)
	if err != nil {
		return err
	}
	defer cv.close()
	pal, transparent := gifstream.AnimPalette()
	var aw *gifstream.AnimWriter
	var size image.Point
	for n := 0; ; n++ {
		if m.ctx != nil {
			if err := m.ctx.Err(); err != nil {
				return err
			}
		}
		fr, info, err := anim.NextFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			return f.classify(err)
		}
		// The limit on pixels holds for all the frames together: every
		// frame is a whole picture's worth of work, and a small file can
		// hold thousands of them.
		if limit := m.pixelLimit(); int64(n+1)*int64(cfg.Width)*int64(cfg.Height) > limit {
			return fmt.Errorf("%w: the animation has more than %d frames of %dx%d, in all more than the limit of %d pixels (see LimitInputPixels)",
				ErrPixelLimit, n, cfg.Width, cfg.Height, limit)
		}
		rect := fr.Bounds()
		var saved *pipeline.Spill
		if info.Disposal == disposalPrevious {
			saved = pipeline.NewSpill(env)
		}
		// What goes wrong while a frame is drawn is its data being bad,
		// unless the working storage failed.
		err = f.classify(cv.draw(fr, info, saved))
		if err == nil {
			err = m.writeFrame(w, cv, &aw, &size, pal, transparent, cfg.LoopCount, info.Delay)
		}
		if err == nil {
			err = cv.dispose(rect, info.Disposal, saved)
		}
		if saved != nil {
			saved.Close()
		}
		if err != nil {
			return fmt.Errorf("frame %d: %w", n+1, err)
		}
	}
	if aw == nil {
		return errors.New("goimgtools: the animation has no frames")
	}
	return aw.Close()
}

// writeFrame sends the canvas through the chain and writes the result as
// one full frame.
func (m *Image) writeFrame(w io.Writer, cv *canvas, aw **gifstream.AnimWriter, size *image.Point,
	pal color.Palette, transparent byte, loop, delay int) error {
	cs, err := cv.source()
	if err != nil {
		return err
	}
	var src pipeline.Source = cs
	if m.ctx != nil {
		src = cancelable{src, m.ctx}
	}
	defer func() { src.Close() }()
	if src, err = applyOps(m.chain, src, &run{exifOrient: 1, maxPixels: m.outputLimit(), hooks: m.hooks}, preShrunk{}); err != nil {
		return err
	}
	b := src.Bounds()
	if *aw == nil {
		*size = b.Size()
		*aw, err = gifstream.NewAnimWriter(w, gifstream.AnimConfig{
			Width: b.Dx(), Height: b.Dy(), LoopCount: loop, BackgroundIndex: transparent, GlobalPalette: pal,
		})
		if err != nil {
			return err
		}
	} else if b.Size() != *size {
		return errors.New("goimgtools: the chain gave frames of different sizes")
	}
	q, err := gifstream.Quantize(originAtZero{src}, pal)
	if err != nil {
		return err
	}
	src = q
	// Every frame written is the whole picture, so the viewer is told to
	// clear it before the next: transparent areas must not show through.
	return (*aw).WriteFrame(q, gifstream.FrameInfo{
		Delay: delay, Disposal: disposalBackground, Transparent: true, TransparentIndex: transparent,
	})
}

// originAtZero moves a source whose bounds do not start at (0, 0) (after
// Extract) to the origin, where a frame covering the whole picture belongs.
type originAtZero struct{ pipeline.Source }

func (o originAtZero) Env() *pipeline.Env { return pipeline.EnvOf(o.Source) }
func (o originAtZero) Opaque() bool       { return pipeline.IsOpaque(o.Source) }

func (o originAtZero) Bounds() image.Rectangle {
	b := o.Source.Bounds()
	return b.Sub(b.Min)
}

func (o originAtZero) Next() (image.Image, error) {
	s, err := o.Source.Next()
	if err != nil {
		return nil, err
	}
	min := o.Source.Bounds().Min
	if min == (image.Point{}) {
		return s, nil
	}
	return pipeline.Relabel(s, s.Bounds().Sub(min)), nil
}
