package gifstream

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/internal/quant"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// AnimOptions are the parameters of EncodeAnimation.
type AnimOptions struct {
	// Frames is how many frames next hands out.
	Frames int
	// LoopCount is as in AnimConfig.
	LoopCount int
	// Colors is the largest palette, 2 to 256; zero means 256. Frames that
	// are written as what changed need one entry for "as before", which
	// is counted in.
	Colors int
	// NoDither maps every pixel to the nearest palette colour.
	NoDither bool
}

// GIF disposal methods.
const (
	disposalNone       = 1
	disposalBackground = 2
)

// EncodeAnimation writes an animated GIF of o.Frames frames, which next
// hands out one at a time: each the whole picture, all of one size, with
// how long it is shown in 1/100 s. EncodeAnimation closes each when it has
// read it, before it asks for the next; after the last it asks once more
// and expects io.EOF.
//
// The frames share one palette, chosen for all of them together (package
// quant; the colours themselves when there are few enough). Pixels less
// than half opaque become transparent. A frame after the first in which no
// pixel turns transparent is written as the rectangle that changed, with
// the pixels that stay as they were transparent ("leave as it is"), which
// is what makes an animation small: only the moving parts are stored.
// Frames in which pixels turn transparent are written whole, on a cleared
// picture.
//
// Every frame is read before anything is written: the frames are kept in
// working storage (a temporary file beyond the memory budget) while the
// palette is chosen, and two more frames while the changes are found.
func EncodeAnimation(w io.Writer, env *pipeline.Env, o AnimOptions, next func() (pipeline.Source, int, error)) error {
	if o.Frames < 1 {
		return errors.New("goimgtools: the animation has no frames")
	}
	if o.Colors != 0 && (o.Colors < 2 || o.Colors > 256) {
		return fmt.Errorf("goimgtools: an animation with a chosen palette takes 2 to 256 colours, not %d", o.Colors)
	}
	first, delay, err := next()
	if err == io.EOF {
		return errors.New("goimgtools: the animation has no frames")
	}
	if err != nil {
		return err
	}
	b := first.Bounds()
	fw, fh := b.Dx(), b.Dy()
	switch {
	case fw <= 0 || fh <= 0:
		err = fmt.Errorf("goimgtools: gif: invalid image size %dx%d", fw, fh)
	case fw >= 1<<16 || fh >= 1<<16:
		err = fmt.Errorf("goimgtools: a GIF holds at most 65535 pixels a side, the image is %dx%d", fw, fh)
	case int64(fh)*int64(o.Frames) > math.MaxInt32 || int64(fw)*4 > math.MaxInt32:
		err = fmt.Errorf("goimgtools: %d frames of %dx%d are too many to choose one palette for; a fixed palette has no such limit", o.Frames, fw, fh)
	}
	if err != nil {
		first.Close()
		return err
	}
	// What is found out about each frame is kept for the second pass.
	need := int64(o.Frames) * infoBytes
	if err := env.Budget.Reserve(need, "animation frame list"); err != nil {
		first.Close()
		return err
	}
	defer env.Budget.Release(need)
	t := &animFrames{env: env, w: fw, h: fh, n: o.Frames, next: next, first: first, firstDelay: delay, k: -1,
		infos: make([]frameInfo, 0, o.Frames)}
	q, err := quant.Quantize(t, &quant.Options{Colors: o.Colors, NoDither: o.NoDither, FrameRows: fh})
	if err != nil {
		t.Close()
		return err
	}
	defer q.Close()
	// The first strip comes once every frame has been read.
	s, err := q.Next()
	if err != nil {
		return err
	}
	p0, ok := s.(*image.Paletted)
	if !ok {
		return fmt.Errorf("goimgtools: gif: quantized strip is %T", s)
	}
	pal := p0.Palette
	ti := -1
	for i, c := range pal {
		if _, _, _, a := c.RGBA(); a == 0 {
			ti = i
		}
	}
	aw, err := NewAnimWriter(w, AnimConfig{Width: fw, Height: fh, LoopCount: o.LoopCount, BackgroundIndex: byte(max(ti, 0)), GlobalPalette: pal})
	if err != nil {
		return err
	}
	if len(t.infos) != o.Frames {
		return io.ErrUnexpectedEOF
	}
	rows := &indexRows{src: q, cur: p0}
	full := image.Rect(0, 0, fw, fh)
	for k, cur := range t.infos {
		// What the frame leaves behind depends on the frame after it: one
		// written whole with transparent pixels needs a clear picture.
		// After the last comes the first again.
		after := frameInfo{transparent: t.infos[0].transparent && o.Frames > 1}
		if k+1 < o.Frames {
			after = t.infos[k+1]
		}
		rect, disposal := cur.rect, byte(disposalNone)
		if !after.diff && after.transparent {
			rect, disposal = full, disposalBackground
		}
		f := &frameRows{rows: rows, top: k * fh, rect: rect, y: rect.Min.Y, pal: pal, env: env}
		info := FrameInfo{Delay: cur.delay, Disposal: disposal, Transparent: ti >= 0, TransparentIndex: byte(max(ti, 0))}
		if err := aw.WriteFrame(f, info); err != nil {
			return fmt.Errorf("frame %d: %w", k+1, err)
		}
	}
	return aw.Close()
}

// frameInfo is what the first pass finds out about a frame.
type frameInfo struct {
	rect        image.Rectangle // what is written of it
	delay       int
	diff        bool // written as what changed from the frame before
	transparent bool // has transparent pixels of its own
}

// infoBytes is about what a frameInfo takes up, counted in the budget.
const infoBytes = 48

// animFrames is the frames one under the other, the image the palette is
// chosen for. A frame written as what changed has its pixels that stay as
// they were made transparent, so that the palette is chosen for what is
// written, and the transparent entry is in it.
type animFrames struct {
	env        *pipeline.Env
	w, h, n    int
	next       func() (pipeline.Source, int, error)
	first      pipeline.Source // frame 0, until it is read
	firstDelay int
	infos      []frameInfo // one per frame read

	k         int  // the frame in cur; -1 before the first
	y         int  // its next row to hand out
	diff      bool // frame k is handed out as what changed from prev
	prev, cur *pipeline.Scratch
	rowP      []byte       // a row of prev
	band      *image.NRGBA // the strip handed out, and a frame's strip converted
	rows      int          // rows per strip handed out
	reserved  int64        // rowP and band
	err       error
}

func (a *animFrames) Env() *pipeline.Env      { return a.env }
func (a *animFrames) Bounds() image.Rectangle { return image.Rect(0, 0, a.w, a.h*a.n) }
func (a *animFrames) ColorModel() color.Model { return color.NRGBAModel }

func (a *animFrames) Close() error {
	if a.first != nil {
		a.first.Close()
		a.first = nil
	}
	a.free()
	return nil
}

// free gives back what the first pass holds.
func (a *animFrames) free() {
	for _, s := range []**pipeline.Scratch{&a.prev, &a.cur} {
		if *s != nil {
			(*s).Close()
			*s = nil
		}
	}
	a.env.Budget.Release(a.reserved)
	a.reserved = 0
	a.rowP, a.band = nil, nil
}

func (a *animFrames) fail(err error) (image.Image, error) {
	a.err = err
	a.free()
	return nil, err
}

func (a *animFrames) Next() (image.Image, error) {
	if a.err != nil {
		return nil, a.err
	}
	if a.k < 0 || a.y >= a.h {
		if a.k == a.n-1 {
			a.free()
			return nil, io.EOF
		}
		if err := a.load(); err != nil {
			return a.fail(err)
		}
	}
	y1 := min(a.y+a.rows, a.h)
	out := *a.band
	out.Rect = image.Rect(0, a.k*a.h+a.y, a.w, a.k*a.h+y1)
	stride := 4 * a.w
	for y := a.y; y < y1; y++ {
		dst := out.Pix[(y-a.y)*stride : (y-a.y+1)*stride]
		if _, err := a.cur.ReadAt(dst, int64(y)*int64(stride)); err != nil {
			return a.fail(err)
		}
		if !a.diff {
			continue
		}
		if _, err := a.prev.ReadAt(a.rowP, int64(y)*int64(stride)); err != nil {
			return a.fail(err)
		}
		for i := 0; i < stride; i += 4 {
			if [4]byte(dst[i:i+4]) == [4]byte(a.rowP[i:i+4]) {
				dst[i], dst[i+1], dst[i+2], dst[i+3] = 0, 0, 0, 0
			}
		}
	}
	a.y = y1
	return &out, nil
}

// load reads the next frame into cur, with the frame before moved to prev,
// and records what it is to be written as.
func (a *animFrames) load() error {
	k := a.k + 1
	if k == 0 {
		a.rows = pipeline.StripRows(int64(a.w)*4, a.h)
		need := int64(a.w)*4 + int64(a.w)*4*int64(a.rows)
		if err := a.env.Budget.Reserve(need, "animation rows"); err != nil {
			return err
		}
		a.reserved = need
		a.rowP = make([]byte, 4*a.w)
		a.band = image.NewNRGBA(image.Rect(0, 0, a.w, a.rows))
		size := int64(a.w) * int64(a.h) * 4
		var err error
		if a.prev, err = pipeline.NewScratch(a.env, size); err != nil {
			return err
		}
		if a.cur, err = pipeline.NewScratch(a.env, size); err != nil {
			return err
		}
	} else {
		a.prev, a.cur = a.cur, a.prev
	}
	var src pipeline.Source
	var delay int
	if k == 0 {
		src, delay, a.first = a.first, a.firstDelay, nil
	} else {
		var err error
		src, delay, err = a.next()
		if err == io.EOF {
			return fmt.Errorf("goimgtools: the animation ended after %d frames, not %d: %w", k, a.n, io.ErrUnexpectedEOF)
		}
		if err != nil {
			return err
		}
	}
	info, err := a.read(src, k)
	src.Close()
	if err != nil {
		return err
	}
	info.delay = delay
	a.infos = append(a.infos, info)
	a.k, a.y, a.diff = k, 0, info.diff
	if k == a.n-1 {
		// The frames were counted: there must be no more.
		s, _, err := a.next()
		if err == nil {
			s.Close()
			err = fmt.Errorf("goimgtools: the animation has more than the %d frames counted", a.n)
		}
		if err != io.EOF {
			return err
		}
	}
	return nil
}

// read stores frame k in cur, its pixels made all or nothing, and compares
// it with prev.
func (a *animFrames) read(src pipeline.Source, k int) (frameInfo, error) {
	b := src.Bounds()
	if b.Dx() != a.w || b.Dy() != a.h {
		return frameInfo{}, errors.New("goimgtools: the chain gave frames of different sizes")
	}
	// So far no pixel turns transparent.
	info := readState{frameInfo: frameInfo{rect: image.Rect(0, 0, a.w, a.h), diff: k > 0}, x0: a.w, y0: a.h}
	y := 0
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return frameInfo{}, err
		}
		r := s.Bounds()
		if r.Min.X != b.Min.X || r.Max.X != b.Max.X || r.Min.Y != b.Min.Y+y || r.Max.Y > b.Max.Y || r.Dy() <= 0 {
			return frameInfo{}, fmt.Errorf("goimgtools: strip %v does not continue frame %v at row %d", r, b, b.Min.Y+y)
		}
		// The strip is converted a band at a time, in the band that is
		// handed out later (it is not in use while a frame is read).
		for by := r.Min.Y; by < r.Max.Y; by += a.rows {
			sub := image.Rect(r.Min.X, by, r.Max.X, min(by+a.rows, r.Max.Y))
			c := *a.band
			c.Rect = sub
			draw.Draw(&c, sub, s, sub.Min, draw.Src)
			if err := a.store(c.Pix[:sub.Dy()*c.Stride], k, &y, &info); err != nil {
				return frameInfo{}, err
			}
		}
	}
	if y != a.h {
		return frameInfo{}, io.ErrUnexpectedEOF
	}
	if info.diff {
		if info.x0 >= info.x1 {
			// Nothing changed: one pixel left as it is.
			info.x0, info.y0, info.x1, info.y1 = 0, 0, 1, 1
		}
		info.rect = image.Rect(info.x0, info.y0, info.x1, info.y1)
	}
	return info.frameInfo, nil
}

// readState is what read finds out about a frame as its rows go by.
type readState struct {
	frameInfo
	x0, y0, x1, y1 int // what changed
}

// store makes the rows of pix (the frame's, from row *y on) all or
// nothing, compares them with prev and writes them to cur.
func (a *animFrames) store(pix []byte, k int, y *int, info *readState) error {
	stride := 4 * a.w
	for ; len(pix) > 0; pix, *y = pix[stride:], *y+1 {
		row := pix[:stride]
		for p := 0; p < stride; p += 4 {
			if row[p+3] < 128 {
				row[p], row[p+1], row[p+2], row[p+3] = 0, 0, 0, 0
				info.transparent = true
			} else {
				row[p+3] = 255
			}
		}
		off := int64(*y) * int64(stride)
		if k > 0 {
			if _, err := a.prev.ReadAt(a.rowP, off); err != nil {
				return err
			}
			for p := 0; p < stride; p += 4 {
				if [4]byte(row[p:p+4]) == [4]byte(a.rowP[p:p+4]) {
					continue
				}
				if row[p+3] == 0 {
					info.diff = false // turns transparent: cannot be drawn over what is there
				}
				info.x0, info.x1 = min(info.x0, p/4), max(info.x1, p/4+1)
				info.y0, info.y1 = min(info.y0, *y), *y+1
			}
		}
		if _, err := a.cur.WriteAt(row, off); err != nil {
			return err
		}
	}
	return nil
}

// indexRows hands out the rows of the quantized frames, in order.
type indexRows struct {
	src pipeline.Source
	cur *image.Paletted
}

// take returns rows y to at most y+n of the strip that holds row y.
func (r *indexRows) take(y, n int) (*image.Paletted, error) {
	for r.cur == nil || y >= r.cur.Rect.Max.Y {
		s, err := r.src.Next()
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		if err != nil {
			return nil, err
		}
		p, ok := s.(*image.Paletted)
		if !ok || (r.cur != nil && p.Rect.Min.Y != r.cur.Rect.Max.Y) {
			return nil, errors.New("goimgtools: gif: quantized strips out of order")
		}
		r.cur = p
	}
	if y < r.cur.Rect.Min.Y {
		return nil, errors.New("goimgtools: gif: frame rows asked for out of order")
	}
	y1 := min(y+n, r.cur.Rect.Max.Y)
	return r.cur.SubImage(image.Rect(r.cur.Rect.Min.X, y, r.cur.Rect.Max.X, y1)).(*image.Paletted), nil
}

// frameRows is the rectangle rect of the frame whose first row is row top
// of the quantized frames, as the strips AnimWriter takes.
type frameRows struct {
	rows *indexRows
	top  int
	rect image.Rectangle
	y    int // next row, in the frame
	pal  color.Palette
	env  *pipeline.Env
}

func (f *frameRows) Env() *pipeline.Env      { return f.env }
func (f *frameRows) Bounds() image.Rectangle { return f.rect }
func (f *frameRows) ColorModel() color.Model { return f.pal }
func (f *frameRows) Close() error            { return nil }

func (f *frameRows) Next() (image.Image, error) {
	if f.y >= f.rect.Max.Y {
		return nil, io.EOF
	}
	p, err := f.rows.take(f.top+f.y, f.rect.Max.Y-f.y)
	if err != nil {
		return nil, err
	}
	dy := p.Rect.Dy()
	out := *p.SubImage(image.Rect(f.rect.Min.X, p.Rect.Min.Y, f.rect.Max.X, p.Rect.Max.Y)).(*image.Paletted)
	out.Rect = image.Rect(f.rect.Min.X, f.y, f.rect.Max.X, f.y+dy)
	f.y += dy
	return &out, nil
}
