package goimgtools

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"time"

	"github.com/whhhh1500/goimgtools/internal/gifstream"
	"github.com/whhhh1500/goimgtools/internal/pngstream"
	"github.com/whhhh1500/goimgtools/internal/webpanim"
	"github.com/whhhh1500/goimgtools/internal/webpstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Animated makes the chain apply to every frame of an animated GIF, WebP or
// PNG (APNG) and write an animated GIF, WebP or PNG (APNG). Without it only
// the first frame is used. A still image passes unchanged. An animation that cannot be
// kept, one to another output format, fails with an error wrapping
// errors.ErrUnsupported (without Animated its first frame is used).
//
// Each frame is drawn onto the full picture first (frames often hold only
// the part that changed; a WebP frame is blended or put there as it says,
// as libwebp's animation decoder does, an APNG frame as the APNG
// specification says, composited over as apngdis does), then goes through
// the chain as a complete image.
//
// A WebP holds the first frame whole and every later one as the rectangle
// that changed; a frame that changes nothing lengthens the one before, and
// a GIF of one frame (or of frames all alike) becomes a still WebP.
// WebPOptions apply to every frame (lossy or lossless, Quality, Effort).
// A frame without transparency leaves out what stays as it was in its
// rectangle, transparent there and blended onto the picture: a lossless
// frame the unchanged pixels; a lossy one, to which a pixel a few levels
// off (5 at quality 75, as libwebp's animation encoder counts) has not
// changed, the unchanged blocks of 8x8 or of 4x4 pixels, when the frame is
// smaller so than whole (so a lossy frame that is nowhere further off than
// that lengthens the one before too). With WebPOptions.Mixed the frames of
// a lossy animation are lossless where that is smaller. The frames are
// encoded as they come: what is kept is the picture so far and this frame
// (in working storage) and the frames written so far (in a spill buffer).
// Frames shown for 10ms or less are written as 100ms, as browsers show
// them, and the GIF's loop count becomes a WebP's (a GIF says how often it
// repeats, a WebP how often it plays), as gif2webp converts them. A WebP's
// become a GIF's the other way, and its durations hundredths of a second.
//
// An APNG is written without loss: with one palette for all the frames
// when they have 256 colours or fewer, else as RGB (RGBA with
// transparency); the first frame whole, every later one as the rectangle
// that differs from what it is drawn on (the frame before kept, cleared or
// undone, whichever leaves least to draw, as apngopt chooses), either as it
// is or with the pixels that stay as they were transparent and blended
// over, whichever is smaller. A
// frame that changes nothing lengthens the one before, and frames all
// alike make a still PNG. Every frame is processed before the file is
// written (the colours and the transparency of all of them decide how it
// is stored): the frames are kept in working storage. PNGOptions'
// CompressionLevel applies; Colors does not (an error).
//
// A GIF is written with one palette chosen for all the frames together
// (GIFOptions.Colors and NoDither apply; an animation of no more colours
// than that keeps them exactly), and every frame after the first as the
// rectangle that changed, with the pixels that stay as they were left
// transparent: the file holds only what moves. A frame in which pixels
// turn transparent is written whole, on a cleared picture. A frame that
// changes nothing lengthens the one before (up to a GIF's longest delay,
// 655.35s).
//
// For that every frame is processed before the first GIF frame is
// written. The frames are kept in working storage (a temporary file beyond
// the memory share), and the input is read twice (a plain reader is copied
// to working storage first): once to count the frames, once to process
// them. Memory does not grow with the number of frames; choosing the
// palette takes some 4 to 5MB of it. GIFOptions{FixedPalette: true} writes
// every frame whole with the fixed palette instead, as it goes: nothing is
// stored but the picture so far.
//
// Time grows with the frames: every frame costs as much as a still image
// of that size. So LimitInputPixels counts the pixels of all frames
// together (width x height x frames), and an animation with more is
// refused with an ErrPixelLimit once the frames are counted, before any
// is processed (with FixedPalette, once that many have been).
func (m *Image) Animated() *Image { m.animated = true; return m }

// Loop sets how often an animation that Animated keeps plays: n times, or
// for ever with 0 (sharp's loop). By default it plays as the input says.
// A GIF stores how often it repeats after the first time, a WebP and an
// APNG how often they play: n is the plays in every one.
func (m *Image) Loop(n int) *Image {
	if n < 0 || n > math.MaxUint16 {
		return m.fail(errors.New("goimgtools: Loop must be 0 (for ever) to 65535"))
	}
	m.loopSet, m.loop = true, n
	return m
}

// FrameDelay sets how long each frame of an animation that Animated keeps
// shows (sharp's delay): one duration for every frame, or one for each,
// the last for the frames beyond the list. By default each frame shows as
// long as in the input. A GIF stores hundredths of a second (rounded); 10ms
// or less is written as 100ms in a WebP, as browsers show such frames. A
// frame that changes nothing still lengthens the one before by its delay.
func (m *Image) FrameDelay(d ...time.Duration) *Image {
	if len(d) == 0 {
		return m.fail(errors.New("goimgtools: FrameDelay needs a duration"))
	}
	for _, v := range d {
		if v < 0 || v > maxFrameDelay {
			return m.fail(fmt.Errorf("goimgtools: a frame delay must be 0 to %v", maxFrameDelay))
		}
	}
	m.delays = append([]time.Duration(nil), d...)
	return m
}

// maxFrameDelay is the longest frame a WebP can hold (24 bits of
// milliseconds; a GIF's is 655.35s).
const maxFrameDelay = (1<<24 - 1) * time.Millisecond

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
	// A GIF states how often it repeats after the first time through:
	// LoopCount is one more.
	LoopCount int
}

// Frames goes through the input and reports how many frames it has and how
// long they play. A GIF, a WebP and a PNG (APNG) can have more than one.
//
// Unlike Metadata it reads the whole file, since a GIF does not say in its
// header how many frames follow, and an APNG and a WebP state the length
// of each frame with the frame; the pixels are stepped over, not decoded,
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
		env := m.newEnv()
		fr, err := f.otherFrames(format, env)
		if err != nil {
			f.Close()
			return Frames{}, err
		}
		// A still image, or the first frame, once its header says that it
		// is an image at all.
		src, err := m.decode(env, f, format)
		if err != nil {
			return Frames{}, err
		}
		src.Close()
		f.Close()
		if fr.Count > 1 {
			return fr, nil
		}
		return Frames{Count: 1}, nil
	}
	defer f.Close()
	anim, err := gifstream.OpenAnimation(f.br, m.newEnv())
	if err != nil {
		return Frames{}, f.classify(err)
	}
	defer anim.Close()
	fr.LoopCount = anim.Config().LoopCount
	if fr.LoopCount > 0 {
		fr.LoopCount++ // the first time through and the repeats
	}
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

// otherFrames reads what an animated WebP or PNG (APNG) says of its
// frames: a Count of 0 for a still image or another format. It leaves the
// input to be read again from the start.
func (in *input) otherFrames(format string, env *pipeline.Env) (Frames, error) {
	if format != "webp" && format != "png" {
		return Frames{}, nil
	}
	ra, size, err := in.rewind(env)
	if err != nil {
		return Frames{}, in.classify(err)
	}
	if format == "webp" {
		a, err := webpstream.ReadAnimation(ra, size)
		return Frames{Count: a.Frames, Duration: a.Duration, LoopCount: a.LoopCount}, in.classify(err)
	}
	a, err := pngstream.ReadAnimation(ra, size)
	return Frames{Count: a.Frames, Duration: a.Duration, LoopCount: a.LoopCount}, in.classify(err)
}

// keepsAnimation reports whether Animated can run the chain over the
// frames of input format into output format.
func keepsAnimation(input, output string) bool {
	return (input == "gif" || input == "webp" || input == "png") && (output == "gif" || output == "webp" || output == "png")
}

// refuseAnimation fails when Animated asks for the frames of input format
// in output format and the input really is an animation, which then
// could not be kept: a still image passes, and so its first frame would.
// It leaves the input to be read again from the start.
func (m *Image) refuseAnimation(f *input, input, output string, env *pipeline.Env) error {
	n, err := inputFrames(f, input, env)
	if err != nil || n <= 1 {
		return err
	}
	name := map[string]string{"gif": "an animated GIF", "webp": "an animated WebP", "png": "an animated PNG (APNG)"}[input]
	return fmt.Errorf("goimgtools: the input is %s and %s output cannot hold an animation (GIF, WebP and PNG output can); without Animated its first frame is used: %w",
		name, output, errors.ErrUnsupported)
}

// refuseMaxSize fails when MaxFileSize is set for an animation that would
// be kept: it chooses the quality of a single picture. A GIF of one frame
// passes, to be written as a still image.
func (m *Image) refuseMaxSize(f *input, input string, env *pipeline.Env) error {
	n, err := inputFrames(f, input, env)
	if err != nil || n <= 1 {
		return err
	}
	return fmt.Errorf("goimgtools: MaxFileSize does not apply to an animation; without Animated the first frame is used: %w", errors.ErrUnsupported)
}

// inputFrames tells a still image (1) from an animation (2 or more frames)
// for an input of format input; 0 for a format without frames. It leaves
// the input to be read again from the start.
func inputFrames(f *input, input string, env *pipeline.Env) (int, error) {
	switch input {
	case "gif":
		ra, size, err := f.rewind(env)
		if err != nil {
			return 0, f.classify(err)
		}
		n, err := gifFrames(io.NewSectionReader(ra, 0, size), env, 2)
		return n, f.classify(err)
	case "webp", "png":
		fr, err := f.otherFrames(input, env)
		return fr.Count, err
	}
	return 0, nil
}

// gifFrames counts the frames of the GIF r, stopping at most.
func gifFrames(r io.Reader, env *pipeline.Env, most int) (int, error) {
	anim, err := gifstream.OpenAnimation(bufio.NewReaderSize(r, 64<<10), env)
	if err != nil {
		return 0, err
	}
	defer anim.Close()
	n := 0
	for n < most {
		fr, _, err := anim.NextFrame()
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, err
		}
		fr.Close() // unread: its data is stepped over
		n++
	}
	return n, nil
}

// GIF disposal methods.
const (
	disposalBackground = 2
	disposalPrevious   = 3
)

// animReader is an animation being read, GIF or WebP, frame by frame.
type animReader interface {
	nextFrame() (pipeline.Source, animFrame, error)
	Close() error
}

// animFrame is how a frame goes on the canvas.
type animFrame struct {
	rect     image.Rectangle // on the canvas
	ms       int             // how long the canvas shows with the frame on it
	disposal byte            // disposalBackground, disposalPrevious or 0
	// gif is a GIF frame's: paletted strips in the canvas's coordinates.
	// A WebP or APNG frame's strips are NRGBA, from (0, 0), blended onto
	// the canvas (as libwebp does, or as apngdis does when apng is set) or
	// put there as they are.
	gif   *gifstream.FrameInfo
	blend bool
	apng  bool
}

type gifReader struct{ *gifstream.Animation }

func (g gifReader) nextFrame() (pipeline.Source, animFrame, error) {
	fr, info, err := g.NextFrame()
	if err != nil {
		return nil, animFrame{}, err
	}
	return fr, animFrame{rect: fr.Bounds(), ms: 10 * info.Delay, disposal: info.Disposal, gif: &info}, nil
}

type pngReader struct{ *pngstream.AnimReader }

func (r pngReader) nextFrame() (pipeline.Source, animFrame, error) {
	fr, f, err := r.NextFrame()
	if err != nil {
		return nil, animFrame{}, err
	}
	d := map[byte]byte{1: disposalBackground, 2: disposalPrevious}[f.Dispose]
	src := pipeline.Map(fr, func(*image.NRGBA) error { return nil })
	return src, animFrame{rect: f.Rect, ms: int(f.Delay / time.Millisecond), disposal: d, blend: f.Over, apng: true}, nil
}

type webpReader struct{ *webpstream.AnimReader }

func (r webpReader) nextFrame() (pipeline.Source, animFrame, error) {
	fr, f, err := r.NextFrame()
	if err != nil {
		return nil, animFrame{}, err
	}
	var d byte
	if f.Dispose {
		d = disposalBackground
	}
	return fr, animFrame{rect: f.Rect, ms: int(f.Duration / time.Millisecond), disposal: d, blend: f.Blend}, nil
}

// canvas is the picture an animation has built up so far, as RGBA, or as
// NRGBA (nrgba) for a WebP, whose frames are blended as libwebp blends them.
type canvas struct {
	env      *pipeline.Env
	sc       *pipeline.Scratch
	w, h     int
	row      []uint8
	reserved int64
	nrgba    bool
	// band is the strip the canvas is handed out in, kept from frame to
	// frame; lent while a source has it.
	band *image.RGBA
	lent bool
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

// draw puts a frame on the canvas: a GIF frame's every pixel but the
// transparent ones, a WebP frame's as it says. When the frame is to be
// undone afterwards (disposal "previous"), what it covers is copied to
// saved first.
func (c *canvas) draw(f pipeline.Source, af animFrame, saved *pipeline.Spill) error {
	if af.gif == nil {
		return c.drawNRGBA(f, af, saved)
	}
	info := *af.gif
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

// drawNRGBA puts a WebP or APNG frame (NRGBA strips of its size) on the
// canvas at af.rect, blended or as it is, having copied what it covers to
// saved when that is set.
func (c *canvas) drawNRGBA(f pipeline.Source, af animFrame, saved *pipeline.Spill) error {
	r := af.rect
	if !r.In(image.Rect(0, 0, c.w, c.h)) {
		return errors.New("goimgtools: animation frame outside the picture")
	}
	row := c.row[:r.Dx()*4]
	for {
		s, err := f.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		m, ok := s.(*image.NRGBA)
		if !ok || m.Rect.Min.X != 0 || m.Rect.Dx() != r.Dx() || m.Rect.Min.Y < 0 || m.Rect.Max.Y > r.Dy() {
			return errors.New("goimgtools: animation frame strip of the wrong kind")
		}
		for y := m.Rect.Min.Y; y < m.Rect.Max.Y; y++ {
			src := m.Pix[m.PixOffset(0, y):][:len(row)]
			off := c.off(r.Min.X, r.Min.Y+y)
			if saved != nil || af.blend {
				if _, err := c.sc.ReadAt(row, off); err != nil {
					return err
				}
			}
			if saved != nil {
				if _, err := saved.Write(row); err != nil {
					return err
				}
			}
			if !af.blend {
				if _, err := c.sc.WriteAt(src, off); err != nil {
					return err
				}
				continue
			}
			for x := 0; x < len(row); x += 4 {
				if af.apng {
					blendOver(row[x:x+4:x+4], src[x:x+4:x+4])
				} else {
					blendNonPremult(row[x:x+4:x+4], src[x:x+4:x+4])
				}
			}
			if _, err := c.sc.WriteAt(row, off); err != nil {
				return err
			}
		}
	}
}

// blendNonPremult puts the NRGBA pixel src over dst, as libwebp's animation
// decoder does (anim_decode.c, BlendPixelNonPremult).
func blendNonPremult(dst, src []uint8) {
	sa := uint32(src[3])
	if sa == 0 {
		return
	}
	da := uint32(dst[3])
	// dst_factor_a = dst_a * (255 - src_a) / 255, approximately.
	dfa := (da * (256 - sa)) >> 8
	ba := sa + dfa
	scale := (1 << 24) / ba
	for i := 0; i < 3; i++ {
		dst[i] = uint8(((uint32(src[i])*sa + uint32(dst[i])*dfa) * scale) >> 24)
	}
	dst[3] = uint8(ba)
}

// blendOver composites the NRGBA pixel src over dst, as apngdis (by the
// author of APNG) does.
func blendOver(dst, src []uint8) {
	sa := uint32(src[3])
	switch {
	case sa == 0:
	case sa == 255 || dst[3] == 0:
		copy(dst, src)
	default:
		u := sa * 255
		v := (255 - sa) * uint32(dst[3])
		al := u + v
		for i := 0; i < 3; i++ {
			dst[i] = uint8((uint32(src[i])*u + uint32(dst[i])*v) / al)
		}
		dst[3] = uint8(al / 255)
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
	shared   bool // band is the canvas's, to give back
	n, y     int
	reserved int64
}

func (c *canvas) source() (*canvasSource, error) {
	n := pipeline.StripRows(int64(c.w)*4, c.h)
	need := int64(c.w) * 4 * int64(n)
	if err := c.env.Budget.Reserve(need, "animation strip"); err != nil {
		return nil, err
	}
	s := &canvasSource{c: c, n: n, reserved: need}
	switch {
	case c.band == nil:
		c.band = image.NewRGBA(image.Rect(0, 0, c.w, n))
		fallthrough
	case !c.lent:
		s.band, s.shared, c.lent = c.band, true, true
	default: // a source of the frame before is still open
		s.band = image.NewRGBA(image.Rect(0, 0, c.w, n))
	}
	return s, nil
}

func (s *canvasSource) Env() *pipeline.Env      { return s.c.env }
func (s *canvasSource) Bounds() image.Rectangle { return image.Rect(0, 0, s.c.w, s.c.h) }
func (s *canvasSource) ColorModel() color.Model {
	if s.c.nrgba {
		return color.NRGBAModel
	}
	return color.RGBAModel
}

func (s *canvasSource) Close() error {
	s.c.env.Budget.Release(s.reserved)
	s.reserved = 0
	if s.shared {
		s.shared, s.c.lent = false, false
	}
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
	if s.c.nrgba {
		return &image.NRGBA{Pix: out.Pix, Stride: out.Stride, Rect: out.Rect}, nil
	}
	return &out, nil
}

// writeAnimated runs the chain over every frame of the animated GIF or WebP
// (input) in f and writes them as an animated GIF or WebP (format).
func (m *Image) writeAnimated(w io.Writer, f *input, env *pipeline.Env, input, format string) error {
	var g GIFOptions
	var wo WebPOptions
	var po PNGOptions
	var nl int // WebP near-lossless rounding
	var err error
	fixed := false
	switch format {
	case "webp":
		if wo, err = optionsOf[WebPOptions](m.options, "webp"); err != nil {
			return err
		}
		if wo.NearLossless {
			if nl, err = nearLosslessBits(wo.Quality); err != nil {
				return err
			}
		}
	case "png":
		if po, err = optionsOf[PNGOptions](m.options, "png"); err != nil {
			return err
		}
		if po.Colors != 0 {
			return fmt.Errorf("goimgtools: PNGOptions.Colors does not apply to an animation (an APNG keeps every colour; GIF output chooses a palette for all frames): %w", errors.ErrUnsupported)
		}
		if po.CompressionLevel == 0 {
			po.CompressionLevel = DefaultPNGLevel
		}
	default:
		if g, err = optionsOf[GIFOptions](m.options, "gif"); err != nil {
			return err
		}
		if g.Colors < 0 || g.Colors > 256 {
			return errors.New("goimgtools: GIF colours must be 1 to 256")
		}
		if g.Effort < 0 || g.Effort > 9 {
			return errors.New("goimgtools: GIF effort must be 1 to 9")
		}
		// One colour is no choice to make; the fixed palette handles it.
		fixed = g.FixedPalette || g.Colors == 1
	}
	// The reader, the canvas size and how often the animation plays
	// (0 for ever, -1 when a GIF does not say).
	var anim animReader
	var cw, ch, plays int
	frames := 0
	switch input {
	case "png":
		ra, size, err := f.readerAt(env)
		if err != nil {
			return f.classify(err)
		}
		a, err := pngstream.ReadAnimation(ra, size)
		if err != nil {
			return f.classify(err)
		}
		pa, err := pngstream.OpenAnimation(ra, size, env)
		if err != nil {
			return f.classify(err)
		}
		cw, ch = pa.Config()
		if err := m.framesFit(a.Frames, max(cw, 1), max(ch, 1)); err != nil {
			return err
		}
		frames, plays, anim = a.Frames, a.LoopCount, pngReader{pa}
	case "webp":
		ra, size, err := f.readerAt(env)
		if err != nil {
			return f.classify(err)
		}
		a, err := webpstream.ReadAnimation(ra, size)
		if err != nil {
			return f.classify(err)
		}
		if err := m.framesFit(a.Frames, max(a.Width, 1), max(a.Height, 1)); err != nil {
			return err
		}
		frames = a.Frames
		wa, err := webpstream.OpenAnimation(ra, size, env)
		if err != nil {
			return f.classify(err)
		}
		anim = webpReader{wa}
		cw, ch, plays = wa.Config()
	default:
		r := io.Reader(f.br)
		if format == "gif" && !fixed {
			// The palette is chosen for all frames, so their number is
			// needed before the first is read: the input is read twice.
			ra, size, err := f.readerAt(env)
			if err != nil {
				return f.classify(err)
			}
			if frames, err = m.countFrames(f, io.NewSectionReader(ra, 0, size), env); err != nil {
				return err
			}
			r = bufio.NewReaderSize(io.NewSectionReader(ra, 0, size), 64<<10)
		}
		ga, err := gifstream.OpenAnimation(r, env)
		if err != nil {
			return f.classify(err)
		}
		anim = gifReader{ga}
		cfg := ga.Config()
		cw, ch, plays = cfg.Width, cfg.Height, cfg.LoopCount
		if plays > 0 {
			plays++ // the first time through and the repeats
		}
	}
	defer anim.Close()
	if m.loopSet {
		plays = m.loop
	}
	if cw <= 0 || ch <= 0 {
		return fmt.Errorf("%w: the image has no pixels", ErrFormat)
	}
	if limit := m.pixelLimit(); int64(cw)*int64(ch) > limit {
		return fmt.Errorf("%w: %dx%d is more than the limit of %d pixels (see LimitInputPixels)", ErrPixelLimit, cw, ch, limit)
	}
	cv, err := newCanvas(env, cw, ch)
	if err != nil {
		return err
	}
	cv.nrgba = input != "gif"
	defer cv.close()
	seq := &frameSeq{m: m, f: f, anim: anim, cv: cv, env: env, w: cw, h: ch}
	defer seq.close()
	switch format {
	case "webp":
		return webpanim.Encode(w, env, webpanim.Options{
			LoopCount: webpLoopCount(plays), Lossless: wo.Lossless || wo.NearLossless, Quality: wo.Quality, Effort: wo.Effort,
			NearLossless: nl, Exact: wo.Exact, AlphaQuality: wo.AlphaQuality, Mixed: wo.Mixed,
		}, seq.next)
	case "png":
		return pngstream.EncodeAnimation(w, env, pngstream.AnimOptions{
			LoopCount: webpLoopCount(plays), Level: po.CompressionLevel,
		}, seq.next)
	}
	// A GIF says in hundredths of a second how long a frame shows, and how
	// often the animation repeats after the first time.
	centi := func() (pipeline.Source, int, error) {
		src, ms, err := seq.next()
		return src, (ms + 5) / 10, err
	}
	loop := plays
	if plays > 0 {
		loop = plays - 1
		if loop == 0 {
			loop = -1 // once: no looping extension
		}
	}
	if fixed {
		return m.writeFixedFrames(w, centi, &seq.n, loop, g)
	}
	return gifstream.EncodeAnimation(w, env, gifstream.AnimOptions{
		Frames: frames, LoopCount: loop, Colors: g.Colors, NoDither: g.NoDither, Lossy: g.Lossy, Effort: g.Effort,
	}, centi)
}

// webpLoopCount is how often a WebP animation plays that is to play as an
// animation that plays n times (0 for ever, -1 when a GIF does not say:
// once), as gif2webp converts a GIF's.
func webpLoopCount(n int) int {
	switch {
	case n < 0:
		return 1
	case n == 0:
		return 0
	}
	return min(n, math.MaxUint16)
}

// countFrames counts the frames of the animated GIF r, against the limit
// on pixels.
func (m *Image) countFrames(f *input, r io.Reader, env *pipeline.Env) (int, error) {
	anim, err := gifstream.OpenAnimation(bufio.NewReaderSize(r, 64<<10), env)
	if err != nil {
		return 0, f.classify(err)
	}
	defer anim.Close()
	cfg := anim.Config()
	for n := 0; ; n++ {
		if m.ctx != nil {
			if err := m.ctx.Err(); err != nil {
				return 0, err
			}
		}
		fr, _, err := anim.NextFrame()
		if err == io.EOF {
			return n, nil
		}
		if err != nil {
			return 0, f.classify(err)
		}
		fr.Close() // unread: its data is stepped over
		if err := m.framesFit(n+1, cfg.Width, cfg.Height); err != nil {
			return 0, err
		}
	}
}

// framesFit holds n frames of w x h against the limit on pixels, which
// counts all the frames together: every frame is a whole picture's worth
// of work, and a small file can hold thousands of them.
func (m *Image) framesFit(n, w, h int) error {
	if limit := m.pixelLimit(); int64(n)*int64(w)*int64(h) > limit {
		return fmt.Errorf("%w: the animation has more than %d frames of %dx%d, in all more than the limit of %d pixels (see LimitInputPixels)",
			ErrPixelLimit, n-1, w, h, limit)
	}
	return nil
}

// frameSeq hands out the frames of an animation one by one, each drawn on
// the picture so far and sent through the chain.
type frameSeq struct {
	m    *Image
	f    *input
	anim animReader
	cv   *canvas
	env  *pipeline.Env
	w, h int
	n    int // frames handed out

	// The frame last drawn, to be undone as its disposal says before the
	// next is drawn (by then the chain has read the picture).
	shown    bool
	rect     image.Rectangle
	disposal byte
	saved    *pipeline.Spill
}

func (s *frameSeq) close() {
	if s.saved != nil {
		s.saved.Close()
		s.saved = nil
	}
}

// next returns the next frame after the chain, with how long it shows in
// milliseconds; io.EOF after the last. The caller closes the frame before it
// asks for the next.
func (s *frameSeq) next() (pipeline.Source, int, error) {
	if s.shown {
		s.shown = false
		err := s.cv.dispose(s.rect, s.disposal, s.saved)
		s.close()
		if err != nil {
			return nil, 0, fmt.Errorf("frame %d: %w", s.n, err)
		}
	}
	if s.m.ctx != nil {
		if err := s.m.ctx.Err(); err != nil {
			return nil, 0, err
		}
	}
	fr, info, err := s.anim.nextFrame()
	if err == io.EOF {
		return nil, 0, io.EOF
	}
	if err != nil {
		return nil, 0, s.f.classify(err)
	}
	if err := s.m.framesFit(s.n+1, s.w, s.h); err != nil {
		return nil, 0, err
	}
	s.n++
	var saved *pipeline.Spill
	if info.disposal == disposalPrevious {
		saved = pipeline.NewSpill(s.env)
	}
	// What goes wrong while a frame is drawn is its data being bad,
	// unless the working storage failed.
	err = s.cv.draw(fr, info, saved)
	fr.Close()
	if err := s.f.classify(err); err != nil {
		if saved != nil {
			saved.Close()
		}
		return nil, 0, fmt.Errorf("frame %d: %w", s.n, err)
	}
	s.shown, s.rect, s.disposal, s.saved = true, info.rect, info.disposal, saved
	cs, err := s.cv.source()
	if err != nil {
		return nil, 0, fmt.Errorf("frame %d: %w", s.n, err)
	}
	var src pipeline.Source = cs
	if s.m.ctx != nil {
		src = cancelable{src, s.m.ctx}
	}
	m := s.m
	out, err := applyOps(m.chain, src, &run{exifOrient: 1, maxPixels: m.outputLimit(), hooks: m.hooks}, preShrunk{})
	if err != nil {
		out.Close()
		return nil, 0, fmt.Errorf("frame %d: %w", s.n, err)
	}
	ms := info.ms
	if d := s.m.delays; len(d) > 0 {
		ms = int(d[min(s.n-1, len(d)-1)].Milliseconds())
	}
	return originAtZero{out}, ms, nil
}

// writeFixedFrames writes every frame whole, reduced to the fixed palette.
// Nothing is kept from one frame to the next but the picture so far.
func (m *Image) writeFixedFrames(w io.Writer, next func() (pipeline.Source, int, error), n *int, loop int, g GIFOptions) error {
	colors := g.Colors
	if colors == 0 {
		colors = 256
	}
	pal, transparent, hasTransparent := gifstream.AnimPalette(colors)
	var aw *gifstream.AnimWriter
	var size image.Point
	for {
		src, delay, err := next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		err = func() error {
			defer func() { src.Close() }()
			b := src.Bounds()
			if aw == nil {
				size = b.Size()
				if aw, err = gifstream.NewAnimWriter(w, gifstream.AnimConfig{
					Width: b.Dx(), Height: b.Dy(), LoopCount: loop, BackgroundIndex: transparent, GlobalPalette: pal, Lossy: g.Lossy,
				}); err != nil {
					return err
				}
			} else if b.Size() != size {
				return errors.New("goimgtools: the chain gave frames of different sizes")
			}
			q, err := gifstream.Quantize(src, pal, !g.NoDither)
			if err != nil {
				return err
			}
			src = q
			// Every frame written is the whole picture, so the viewer is
			// told to clear it before the next: transparent areas must not
			// show through.
			return aw.WriteFrame(q, gifstream.FrameInfo{
				Delay: delay, Disposal: disposalBackground, Transparent: hasTransparent, TransparentIndex: transparent,
			})
		}()
		if err != nil {
			return fmt.Errorf("frame %d: %w", *n, err)
		}
	}
	if aw == nil {
		return errors.New("goimgtools: the animation has no frames")
	}
	return aw.Close()
}

// originAtZero moves a source whose bounds do not start at (0, 0) (after
// Crop) to the origin, where a frame covering the whole picture belongs.
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
