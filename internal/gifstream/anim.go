package gifstream

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// AnimConfig is what an animated GIF states once, ahead of its frames.
type AnimConfig struct {
	// Width and Height are the size of the logical screen (the canvas).
	Width, Height int
	// LoopCount is how often the animation restarts: 0 for ever, n > 0 for
	// n times, -1 for a file without a looping extension (shown once).
	LoopCount int
	// BackgroundIndex is the background color's index in GlobalPalette.
	BackgroundIndex byte
	// GlobalPalette is the global color table; nil when there is none and
	// every frame carries its own.
	GlobalPalette color.Palette
	// Lossy is as in Options.
	Lossy int
}

// FrameInfo is what goes with one frame of an animation besides its pixels.
type FrameInfo struct {
	// Delay is how long the frame is shown, in 1/100 s.
	Delay int
	// Disposal is what happens to the frame before the next one is drawn:
	// gif.DisposalNone, gif.DisposalBackground, gif.DisposalPrevious or 0.
	Disposal byte
	// Transparent says that TransparentIndex is set. Animation always
	// reports it. AnimWriter, when it is false, does what gif.EncodeAll
	// does: the first fully transparent palette entry, if any, becomes the
	// transparent index.
	Transparent      bool
	TransparentIndex byte
	// ClearPolicy, when writing, from 1, keeps coding with the LZW
	// dictionary once it is full instead of clearing it, until the codes
	// get shorter (see deferLZW and clearPolicies); lossless only.
	ClearPolicy int
}

// Animation reads an animated GIF one frame at a time. It is the standard
// library's gif.DecodeAll with the frames handed out as they are reached
// instead of collected, so memory does not grow with the number of frames:
// what is held is the current frame's strip (and its spill, if interlaced).
type Animation struct {
	d      *decoder
	env    *pipeline.Env
	cur    *frame
	frames int
	// atImage: an image descriptor's introducer has been read.
	// atEnd: the trailer has been read.
	atImage, atEnd bool
	err            error
}

// frame is one frame's Source. It records how the consumer left it, which
// decides how NextFrame gets to the frame after it.
type frame struct {
	*stream
	closed bool
	failed error
}

func (f *frame) Next() (image.Image, error) {
	m, err := f.stream.Next()
	if err != nil && err != io.EOF && !f.closed {
		f.failed = err
	}
	return m, err
}

func (f *frame) Close() error {
	f.closed = true
	return f.stream.Close()
}

// OpenAnimation reads the header and everything up to the first frame's
// image descriptor. A file without a frame is an error, as it is for
// gif.DecodeAll. env may be nil. r is not closed.
func OpenAnimation(r io.Reader, env *pipeline.Env) (*Animation, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	d := &decoder{}
	// Add buffering if r does not provide ReadByte.
	if rr, ok := r.(reader); ok {
		d.r = rr
	} else {
		d.r = bufio.NewReader(r)
	}

	d.loopCount = -1

	if err := d.readHeaderAndScreenDescriptor(); err != nil {
		return nil, err
	}
	a := &Animation{d: d, env: env}
	if err := a.scan(); err != nil {
		return nil, err
	}
	return a, nil
}

// scan is the block loop of the standard library's decode, up to the next
// image descriptor or the trailer.
func (a *Animation) scan() error {
	d := a.d
	for {
		c, err := readByte(d.r)
		if err != nil {
			return fmt.Errorf("gif: reading frames: %v", err)
		}
		switch c {
		case sExtension:
			if err = d.readExtension(); err != nil {
				return err
			}

		case sImageDescriptor:
			a.atImage = true
			return nil

		case sTrailer:
			if a.frames == 0 {
				return fmt.Errorf("gif: missing image data")
			}
			a.atEnd = true
			return nil

		default:
			return fmt.Errorf("gif: unknown block type: 0x%.2x", c)
		}
	}
}

// Config returns the logical screen, the global palette and the loop
// count. The loop count is the one read so far: encoders put the looping
// extension ahead of the first frame, where OpenAnimation has seen it, but
// a file may carry it later, so it is final only once NextFrame has
// returned io.EOF. The palette must not be modified.
func (a *Animation) Config() AnimConfig {
	return AnimConfig{
		Width:           a.d.width,
		Height:          a.d.height,
		LoopCount:       a.d.loopCount,
		BackgroundIndex: a.d.backgroundIndex,
		GlobalPalette:   a.d.globalColorTable,
	}
}

// NextFrame returns the next frame: a Source of *image.Paletted strips
// that cover the frame's rectangle on the canvas (Bounds), with the palette
// gif.DecodeAll gives that frame (ColorModel), and the frame's delay,
// disposal method and transparent index. An interlaced frame comes out in
// row order. After the last frame it returns io.EOF.
//
// The previous frame's Source is finished first: what the consumer did not
// read of it is decoded and checked, unless the consumer closed it, in
// which case the rest of its data is skipped unchecked. NextFrame closes
// that Source; its strips are no longer valid.
//
// An error (from NextFrame or from a frame's Next) means gif.DecodeAll
// rejects the file; frames handed out before it must be discarded.
func (a *Animation) NextFrame() (pipeline.Source, FrameInfo, error) {
	if a.err != nil {
		return nil, FrameInfo{}, a.err
	}
	src, info, err := a.nextFrame()
	if err != nil {
		if err != io.EOF {
			a.err = err
		}
		return nil, FrameInfo{}, err
	}
	return src, info, nil
}

func (a *Animation) nextFrame() (pipeline.Source, FrameInfo, error) {
	d := a.d
	if a.cur != nil {
		if err := a.finishFrame(); err != nil {
			return nil, FrameInfo{}, err
		}
	}
	if !a.atImage && !a.atEnd {
		if err := a.scan(); err != nil {
			return nil, FrameInfo{}, err
		}
	}
	if a.atEnd {
		return nil, FrameInfo{}, io.EOF
	}
	a.atImage = false

	src, err := d.openImage(a.env)
	if err != nil {
		return nil, FrameInfo{}, err
	}
	info := FrameInfo{
		Delay:    d.delayTime,
		Disposal: d.disposalMethod,
	}
	if d.hasTransparentIndex {
		info.Transparent, info.TransparentIndex = true, d.transparentIndex
	}
	// The GIF89a spec, Section 23 (Graphic Control Extension) says:
	// "The scope of this extension is the first graphic rendering block
	// to follow." We therefore reset the GCE fields to zero.
	d.delayTime = 0
	d.hasTransparentIndex = false

	a.cur = &frame{stream: src.(*stream)}
	a.frames++
	return a.cur, info, nil
}

// finishFrame moves the input past the current frame's data and releases
// the frame's buffers.
func (a *Animation) finishFrame() error {
	f := a.cur
	a.cur = nil
	defer f.stream.Close()
	switch {
	case f.failed != nil:
		return f.failed
	case f.done:
		return nil
	case f.closed:
		// Abandoned: step over the remaining data sub-blocks.
		for f.br.err == nil {
			f.br.fill()
		}
		if f.br.err != io.EOF {
			return fmt.Errorf("gif: reading image data: %v", f.br.err)
		}
		return nil
	}
	for {
		_, err := f.stream.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Close releases the current frame's buffers. It does not close the
// reader given to OpenAnimation.
func (a *Animation) Close() error {
	var err error
	if a.cur != nil {
		err = a.cur.stream.Close()
		a.cur = nil
	}
	if a.err == nil {
		a.err = errors.New("goimgtools: gif: animation closed")
	}
	return err
}

// AnimWriter writes an animated GIF one frame at a time, with the standard
// library's encoder: for the same canvas and frames the bytes are those of
// gif.EncodeAll, with one exception. gif.EncodeAll leaves the looping
// extension out of a file with a single frame; AnimWriter cannot know how
// many frames will follow and writes it whenever LoopCount >= 0.
//
// Frames go straight from the source's strips into the LZW writer; the
// writer holds no pixels.
type AnimWriter struct {
	e      *encoder
	frames int
	closed bool
	err    error
	// probe codes a frame to count its bytes (codedSize): one encoder for
	// every trial, its LZW tables used again.
	probe   *encoder
	counted byteCounter
}

// NewAnimWriter checks cfg and writes the header, the logical screen
// descriptor, the global color table and the looping extension.
func NewAnimWriter(w io.Writer, cfg AnimConfig) (*AnimWriter, error) {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("goimgtools: gif: invalid image size %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.Width >= 1<<16 || cfg.Height >= 1<<16 {
		return nil, fmt.Errorf("goimgtools: a GIF holds at most 65535 pixels a side, the image is %dx%d", cfg.Width, cfg.Height)
	}
	if cfg.LoopCount >= 1<<16 {
		return nil, errors.New("goimgtools: gif: loop count must be less than 65536")
	}
	if len(cfg.GlobalPalette) > 256 {
		return nil, errors.New("gif: cannot encode color table with more than 256 entries")
	}
	if len(cfg.GlobalPalette) > 0 {
		if err := checkPalette(cfg.GlobalPalette); err != nil {
			return nil, err
		}
	}
	if cfg.Lossy < 0 || cfg.Lossy > 255 {
		return nil, fmt.Errorf("goimgtools: gif: lossy must be 0 to 255, got %d", cfg.Lossy)
	}
	e := newEncoder(w, cfg.Width, cfg.Height, cfg.GlobalPalette)
	e.lossy = cfg.Lossy
	e.writeHeader(cfg.BackgroundIndex, cfg.LoopCount)
	if e.err != nil {
		return nil, e.err
	}
	return &AnimWriter{e: e}, nil
}

// WriteFrame appends one frame. src yields *image.Paletted strips, all
// with the same palette; src.Bounds() is the frame's rectangle on the
// canvas and must lie inside it. A frame whose palette is the global one
// (the same slice, or the same colors apart from the transparent entry)
// refers to the global color table; any other gets a local color table.
//
// It does not close src. After an error the output is not a valid image
// and the writer accepts no more frames.
func (aw *AnimWriter) WriteFrame(src pipeline.Source, info FrameInfo) error {
	if aw.err != nil {
		return aw.err
	}
	if aw.closed {
		return errors.New("goimgtools: gif: WriteFrame after Close")
	}
	if err := aw.writeFrame(src, info); err != nil {
		aw.err = err
		return err
	}
	aw.frames++
	return nil
}

func (aw *AnimWriter) writeFrame(src pipeline.Source, info FrameInfo) error {
	e := aw.e
	if info.Delay < 0 || info.Delay >= 1<<16 {
		return errors.New("goimgtools: gif: frame delay must be 0 to 65535")
	}
	if info.Disposal > 7 {
		return errors.New("goimgtools: gif: disposal method must be 0 to 7")
	}
	b := src.Bounds()
	if b.Min.X < 0 || b.Max.X >= 1<<16 || b.Min.Y < 0 || b.Max.Y >= 1<<16 {
		return errors.New("gif: image block is too large to encode")
	}
	if !b.In(image.Rectangle{Max: image.Point{e.width, e.height}}) {
		return errors.New("gif: image block is out of bounds")
	}
	dx := b.Dx()

	// The color table is written ahead of the pixels, so the first strip
	// is needed now. A frame without pixels may have no strip at all.
	var pal color.Palette
	strip, err := src.Next()
	switch {
	case err == io.EOF:
		if !b.Empty() {
			return io.ErrUnexpectedEOF
		}
		cp, ok := src.ColorModel().(color.Palette)
		if !ok {
			return fmt.Errorf("goimgtools: gif: frame color model is %T, not a palette", src.ColorModel())
		}
		pal = cp
	case err != nil:
		return err
	default:
		if err := checkStrip(strip, b, b.Min.Y); err != nil {
			return err
		}
		pm, ok := strip.(*image.Paletted)
		if !ok {
			return fmt.Errorf("goimgtools: gif: frame strip is %T, not *image.Paletted", strip)
		}
		pal = pm.Palette
	}
	if err := checkPalette(pal); err != nil {
		return err
	}
	if len(pal) > 256 {
		return errors.New("gif: cannot encode color table with more than 256 entries")
	}
	transparentIndex := int(info.TransparentIndex)
	if !info.Transparent {
		if transparentIndex, err = transparentIndexOf(pal); err != nil {
			return err
		}
	} else if transparentIndex >= len(pal) {
		return errors.New("goimgtools: gif: transparent index outside the palette")
	}

	e.clearPolicy = info.ClearPolicy
	e.beginImage(pal, b, info.Delay, info.Disposal, transparentIndex)
	for y := b.Min.Y; strip != nil; {
		sb := strip.Bounds()
		pm, ok := strip.(*image.Paletted)
		if !ok {
			return fmt.Errorf("goimgtools: gif: frame strip is %T, not *image.Paletted", strip)
		}
		if !samePalette(pm.Palette, pal) {
			return errPaletteChanged
		}
		if pm.Stride == dx {
			e.writePixels(pm.Pix[:dx*sb.Dy()])
		} else {
			for i, n := 0, sb.Dy(); n > 0; i, n = i+pm.Stride, n-1 {
				e.writePixels(pm.Pix[i : i+dx])
			}
		}
		if e.err != nil {
			return e.err
		}
		y = sb.Max.Y
		if y == b.Max.Y {
			break
		}
		if strip, err = nextStrip(src); err != nil {
			return err
		}
		if err := checkStrip(strip, b, y); err != nil {
			return err
		}
	}
	e.endImage()
	return e.err
}

// Close writes the trailer. An animation needs at least one frame. It does
// not close the underlying writer.
func (aw *AnimWriter) Close() error {
	if aw.closed {
		return aw.err
	}
	aw.closed = true
	if aw.err != nil {
		return aw.err
	}
	if aw.frames == 0 {
		aw.err = errors.New("gif: must provide at least one image")
		return aw.err
	}
	aw.e.writeByte(sTrailer)
	aw.e.flush()
	aw.err = aw.e.err
	return aw.err
}
