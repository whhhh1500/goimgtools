// Package webpanim writes animated WebP files: the frames of an animation,
// each a whole picture, as ANMF chunks of lossy (VP8) or lossless (VP8L)
// images.
//
// The first frame is written whole; every later one only as the rectangle
// that changed since the frame before, and a frame that changes nothing
// lengthens the one before instead (frames all alike make a still image).
// When a frame has no transparent pixels, what stays as it was within its
// rectangle is left out: transparent there, and the frame blended onto the
// picture. A lossless frame leaves out the pixels that are as they were,
// which then cost next to nothing; a lossy one, to which a pixel near
// enough is as it was, whole blocks of them, when that makes it smaller
// (lossy.go). The frames are encoded one at a time, as they come; the two
// pictures compared (what the frames so far show and this one) are kept in
// working storage, and the encoded frames in a spill buffer until the size
// of the file is known.
package webpanim

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/internal/vp8enc"
	"github.com/whhhh1500/goimgtools/internal/vp8lenc"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the settings of an animated WebP.
type Options struct {
	// LoopCount is how often the animation plays: 0 for ever.
	LoopCount int
	// Lossless writes every frame without loss, Quality is that of lossy
	// frames otherwise; Effort is the encoder's in either case. From
	// effort 7 on, every lossy frame is encoded each of the ways it can be
	// written (lossy.go), not only some.
	Lossless bool
	Quality  int
	Effort   int
	// Mixed lets the frames of a lossy animation be lossless where that is
	// smaller: every frame is encoded both ways (libwebp's allow_mixed),
	// which takes several times as long.
	Mixed bool
	// NearLossless is vp8lenc.Options.NearLossless for lossless frames;
	// Exact keeps the colour of transparent pixels (vp8lenc.Options.Clean
	// otherwise, vp8enc.Options.Exact).
	NearLossless int
	Exact        bool
	AlphaQuality int // vp8enc.Options.AlphaQuality for lossy frames
}

// maxSide is the largest canvas side: what a VP8 frame header holds (a
// VP8L one holds one more).
const maxSide = 16383

// maxDuration is the longest a frame can show, in milliseconds (24 bits).
const maxDuration = 1<<24 - 1

// frameBytes is what a frame's entry in the list is counted as.
const frameBytes = 64

// frame is a frame as it goes into the file: where, how long, and where
// its image chunks are in the spill buffer.
type frame struct {
	rect   image.Rectangle
	ms     int
	blend  bool
	off, n int64
}

type encoder struct {
	env  *pipeline.Env
	o    Options
	w, h int
	// The picture the frames are compared with and the frame just read
	// (NRGBA): the frame before, or with lossy frames what each pixel was
	// when it was last coded (lossy.go).
	prev, cur *pipeline.Scratch
	data      *pipeline.Spill // the image chunks of the frames
	frames    []frame
	alpha     bool // some frame has transparent pixels
	rows      []byte
	reserved  int64
	// Lossy frames: how far a channel may be off for a pixel to count as
	// unchanged, and the blocks of the frame's rectangle that are, for
	// each size of block (cells).
	maxDiff int
	still   [len(cells)]blocks
	// Which way of writing a frame won when all were last tried (the
	// plan's kind), for how many frames it is still used alone, and how
	// many that was to begin with; every, when all are tried every time.
	fav, left, stretch int
	every              bool
}

// Encode writes the frames next hands out as an animated WebP: each a
// whole picture, all of the same size, with how long it shows in
// milliseconds; io.EOF after the last. next's caller (Encode) closes each
// frame before it asks for the next. Nothing is written to w before the
// last frame has been encoded; if Encode fails, what it wrote must be
// discarded.
func Encode(w io.Writer, env *pipeline.Env, o Options, next func() (pipeline.Source, int, error)) error {
	if o.LoopCount < 0 || o.LoopCount > math.MaxUint16 {
		return fmt.Errorf("goimgtools: a WebP animation plays 0 (for ever) to %d times, not %d", math.MaxUint16, o.LoopCount)
	}
	src, ms, err := next()
	if err == io.EOF {
		return errors.New("goimgtools: the animation has no frames")
	}
	if err != nil {
		return err
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || b.Dx() > maxSide || b.Dy() > maxSide {
		src.Close()
		return fmt.Errorf("goimgtools: a WebP animation is 1 to %d pixels a side, the frames are %dx%d", maxSide, b.Dx(), b.Dy())
	}
	e := &encoder{env: env, o: o, w: b.Dx(), h: b.Dy(), fav: noPlan, stretch: 1, every: o.Mixed || o.Effort >= 7}
	e.maxDiff = maxDiff(vp8enc.DefaultQuality)
	if q := o.Quality; q >= 1 && q <= 100 { // (another is the encoder's to refuse)
		e.maxDiff = maxDiff(q)
	}
	defer e.close()
	size := int64(e.w) * int64(e.h) * 4
	if e.prev, err = pipeline.NewScratch(env, size); err != nil {
		src.Close()
		return err
	}
	if e.cur, err = pipeline.NewScratch(env, size); err != nil {
		src.Close()
		return err
	}
	e.data = pipeline.NewSpill(env)
	for n := 1; ; n++ {
		if err := e.add(src, ms); err != nil {
			return fmt.Errorf("frame %d: %w", n, err)
		}
		src, ms, err = next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return e.write(w)
}

func (e *encoder) close() {
	for _, s := range []*pipeline.Scratch{e.prev, e.cur} {
		if s != nil {
			s.Close()
		}
	}
	if e.data != nil {
		e.data.Close()
	}
	for i := range e.still {
		e.still[i].release(e.env)
	}
	e.env.Budget.Release(e.reserved + int64(len(e.frames))*frameBytes)
	e.prev, e.cur, e.data, e.frames, e.rows, e.reserved = nil, nil, nil, nil, nil, 0
}

// rowBuf returns a buffer of n bytes, counted in the budget.
func (e *encoder) rowBuf(n int) ([]byte, error) {
	if len(e.rows) < n {
		e.env.Budget.Release(e.reserved)
		e.rows, e.reserved = nil, 0
		if err := e.env.Budget.Reserve(int64(n), "animation rows"); err != nil {
			return nil, err
		}
		e.rows, e.reserved = make([]byte, n), int64(n)
	}
	return e.rows[:n], nil
}

func (e *encoder) off(x, y int) int64 { return (int64(y)*int64(e.w) + int64(x)) * 4 }

// plan is a way of writing a frame: the rectangle that holds what changed,
// whether it is blended onto the picture, what stays as it is transparent,
// and which encoder codes it; for a lossy blended frame, the blocks left
// out.
type plan struct {
	rect     image.Rectangle
	blend    bool
	lossless bool
	still    *blocks
}

// kind tells the ways of writing a frame apart: the side of the blocks
// left out, 0 for none, -1 for a lossless frame.
func (p plan) kind() int {
	switch {
	case p.lossless:
		return -1
	case p.still != nil:
		return p.still.cell
	}
	return 0
}

const (
	noPlan  = -2 // no kind: nothing has won yet
	longest = 8  // the most frames a way that won is used for alone
	// fewStill: a frame leaves blocks out only when at least one in
	// fewStill of its smallest blocks is still. Fewer never paid: of the
	// 669 frames of 13 animations that had any, the 28 with under 2% of
	// them still all came out smaller whole (by up to 4.5%: the mask and
	// the alpha chunk cost more than the blocks), and none of the others
	// had under 5%.
	fewStill = 32
)

// add reads the frame src (closing it), finds what changed since the frame
// before and encodes that.
func (e *encoder) add(src pipeline.Source, ms int) error {
	if src.Bounds().Size() != image.Pt(e.w, e.h) {
		src.Close()
		return errors.New("goimgtools: the chain gave frames of different sizes")
	}
	// Browsers show a frame of 10ms or less for 100ms, as gif2webp
	// writes it.
	if ms <= 10 {
		ms = 100
	}
	first := len(e.frames) == 0
	lossy := !e.o.Lossless
	// What differs from the picture, and what of that is more than near
	// to it (lossy frames leave be what is near enough).
	var differs, changed image.Rectangle
	opaque := true
	stride := 4 * e.w
	m := pipeline.Map(src, func(s *image.NRGBA) error {
		y0, n := s.Rect.Min.Y, s.Rect.Dy()
		for y := 0; y < n; y++ {
			row := s.Pix[y*s.Stride:][:stride]
			for i := 3; i < len(row); i += 4 {
				if a := row[i]; a != 0xff {
					opaque = false
					if a == 0 {
						// What a transparent pixel would be is of no
						// account: all are the same.
						row[i-3], row[i-2], row[i-1] = 0, 0, 0
					}
				}
			}
			if _, err := e.cur.WriteAt(row, e.off(0, y0+y)); err != nil {
				return err
			}
		}
		if first {
			return nil
		}
		before, err := e.rowBuf(stride)
		if err != nil {
			return err
		}
		for y := 0; y < n; y++ {
			row := s.Pix[y*s.Stride:][:stride]
			if _, err := e.prev.ReadAt(before, e.off(0, y0+y)); err != nil {
				return err
			}
			if bytes.Equal(row, before) {
				continue
			}
			x0, x1 := 0, e.w
			for x0 < x1 && bytes.Equal(row[4*x0:4*x0+4], before[4*x0:4*x0+4]) {
				x0++
			}
			for x1 > x0 && bytes.Equal(row[4*x1-4:4*x1], before[4*x1-4:4*x1]) {
				x1--
			}
			differs = differs.Union(image.Rect(x0, y0+y, x1, y0+y+1))
			if !lossy {
				continue
			}
			for x0 < x1 && similar(before[4*x0:4*x0+4], row[4*x0:4*x0+4], e.maxDiff) {
				x0++
			}
			for x1 > x0 && similar(before[4*x1-4:4*x1], row[4*x1-4:4*x1], e.maxDiff) {
				x1--
			}
			if x0 < x1 {
				changed = changed.Union(image.Rect(x0, y0+y, x1, y0+y+1))
			}
		}
		return nil
	})
	for {
		_, err := m.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			m.Close()
			return err
		}
	}
	if err := m.Close(); err != nil {
		return err
	}
	if !lossy {
		changed = differs
	}

	if !first && changed.Empty() {
		// The same picture: the frame before stays longer.
		f := &e.frames[len(e.frames)-1]
		f.ms = min(f.ms+ms, maxDuration)
		return nil
	}
	// The ways of writing the frame. A frame starts at even coordinates.
	// One without transparent pixels can leave out what stays as it is:
	// transparent there, and blended onto the picture. A lossless frame
	// leaves out pixels, a lossy one blocks, the larger or the smaller
	// (when those are still where the larger are not).
	even := func(r image.Rectangle) image.Rectangle {
		if first {
			return image.Rect(0, 0, e.w, e.h)
		}
		return image.Rect(r.Min.X&^1, r.Min.Y&^1, r.Max.X, r.Max.Y)
	}
	var ways [2 + len(cells)]plan
	plans := ways[:0]
	if !lossy || e.o.Mixed {
		plans = append(plans, plan{rect: even(differs), blend: !first && opaque, lossless: true})
	}
	if lossy {
		rect := even(changed)
		plans = append(plans, plan{rect: rect})
		if !first && opaque {
			if err := e.findStill(rect); err != nil {
				return err
			}
			// (Not for a few blocks: see fewStill.)
			for i := len(cells) - 1; i >= 0 && e.still[0].n*fewStill >= e.still[0].w*e.still[0].h; i-- {
				b := &e.still[i]
				if b.n > 0 && (i == len(cells)-1 || b.n > e.still[i+1].sub) {
					plans = append(plans, plan{rect: rect, blend: true, still: b})
				}
			}
		}
	}
	// Each is encoded, and the smallest kept. With several ways, not all
	// are tried for every frame, unless asked for: the one that won is
	// used alone for a while, twice as long each time it wins again.
	probing := false
	if len(plans) > 1 && !e.every {
		k := -1
		for i, p := range plans {
			if p.kind() == e.fav {
				k = i
			}
		}
		if e.left > 0 && k >= 0 {
			plans = plans[k : k+1]
			e.left--
		} else {
			probing = true
		}
	}
	var best plan
	var file *pipeline.Spill
	var size int64
	for _, p := range plans {
		f, n, err := e.encode(p, opaque)
		if err != nil {
			if file != nil {
				file.Close()
			}
			return err
		}
		if file == nil || n < size {
			if file != nil {
				file.Close()
			}
			best, file, size = p, f, n
		} else {
			f.Close()
		}
	}
	defer file.Close()
	if probing {
		e.stretch = min(2*e.stretch, longest)
		if best.kind() != e.fav {
			e.stretch = 1
		}
		e.fav, e.left = best.kind(), e.stretch
	}
	if err := e.env.Budget.Reserve(frameBytes, "animation frame list"); err != nil {
		return err
	}
	e.frames = append(e.frames, frame{rect: best.rect, ms: min(ms, maxDuration), blend: best.blend, off: e.data.Size(), n: size})
	e.alpha = e.alpha || !opaque || best.blend
	if err := imageChunks(file, func(pos, n int64) error {
		_, err := io.Copy(e.data, io.NewSectionReader(file, pos, n))
		return err
	}); err != nil {
		return err
	}
	if first || best.lossless {
		// The frame is the picture now (where it was not coded it is the
		// same already).
		e.prev, e.cur = e.cur, e.prev
		return nil
	}
	return e.keep(best.rect, best.still)
}

// encode encodes the rectangle of p from the picture just read. It returns
// the file the encoder wrote and the size of the image chunks in it.
func (e *encoder) encode(p plan, opaque bool) (*pipeline.Spill, int64, error) {
	src := &rectSource{e: e, rect: p.rect, blend: p.blend, still: p.still, opaque: opaque && !p.blend,
		rows: pipeline.StripRows(4*int64(p.rect.Dx()), p.rect.Dy())}
	defer src.Close()
	// A frame is seldom large: its file starts small, not as a megabyte.
	file := pipeline.NewSpill(e.env)
	file.Expect(int64(p.rect.Dx()) * int64(p.rect.Dy()) / 4)
	var err error
	if p.lossless {
		o := &vp8lenc.Options{Effort: e.o.Effort, Clean: !e.o.Exact}
		if e.o.Lossless {
			o.NearLossless = e.o.NearLossless
		}
		err = vp8lenc.Encode(file, src, o)
	} else {
		o := &vp8enc.Options{Quality: e.o.Quality, Effort: e.o.Effort, Exact: e.o.Exact, AlphaQuality: e.o.AlphaQuality}
		if b := p.still; b != nil {
			// The strips hold whole blocks, for flatten. The transparent
			// pixels are the blocks left out, of no colour to keep, and
			// the alpha plane is the mask of them.
			src.rows = min(max(src.rows/b.cell, 1)*b.cell, p.rect.Dy())
			o.Exact, o.AlphaBlocks = false, b.cell
		}
		err = vp8enc.Encode(file, src, o)
	}
	var size int64
	if err == nil {
		err = imageChunks(file, func(_, n int64) error { size += n; return nil })
	}
	if err == nil && size == 0 {
		err = errors.New("goimgtools: webp: the encoder wrote no image")
	}
	if err != nil {
		file.Close()
		return nil, 0, err
	}
	return file, size, nil
}

// imageChunks calls fn with the place of each image chunk of a WebP file
// (header and padding included): the encoders write a whole file, RIFF,
// maybe VP8X, then the image chunks, which are what a frame holds.
func imageChunks(file *pipeline.Spill, fn func(pos, n int64) error) error {
	var hdr [8]byte
	for pos := int64(12); pos+8 <= file.Size(); {
		if _, err := file.ReadAt(hdr[:], pos); err != nil {
			return err
		}
		n := int64(binary.LittleEndian.Uint32(hdr[4:]))
		whole := 8 + n + n&1
		if id := string(hdr[:4]); id == "ALPH" || id == "VP8 " || id == "VP8L" {
			if err := fn(pos, whole); err != nil {
				return err
			}
		}
		pos += whole
	}
	return nil
}

// write writes the file: the header, then the frames from the spill. A
// single frame (one picture, or frames all alike) is a still image.
func (e *encoder) write(w io.Writer) error {
	if len(e.frames) == 1 {
		return e.writeStill(w)
	}
	total := int64(4 + 8 + 10 + 8 + 6)
	for _, f := range e.frames {
		total += 8 + 16 + f.n
	}
	if total > math.MaxUint32-8 {
		return errors.New("goimgtools: the animation is too large for a WebP file (4GB)")
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	put24 := func(b []byte, v int) { b[0], b[1], b[2] = byte(v), byte(v>>8), byte(v>>16) }
	var b [32]byte
	copy(b[:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(total))
	copy(b[8:], "WEBPVP8X")
	binary.LittleEndian.PutUint32(b[16:], 10)
	b[20] = 1 << 1 // animation
	if e.alpha {
		b[20] |= 1 << 4
	}
	b[21], b[22], b[23] = 0, 0, 0
	put24(b[24:], e.w-1)
	put24(b[27:], e.h-1)
	bw.Write(b[:30])
	// ANIM: a transparent background, the loop count.
	copy(b[:], "ANIM")
	binary.LittleEndian.PutUint32(b[4:], 6)
	clear(b[8:12])
	binary.LittleEndian.PutUint16(b[12:], uint16(e.o.LoopCount))
	bw.Write(b[:14])
	for _, f := range e.frames {
		copy(b[:], "ANMF")
		binary.LittleEndian.PutUint32(b[4:], uint32(16+f.n))
		put24(b[8:], f.rect.Min.X/2)
		put24(b[11:], f.rect.Min.Y/2)
		put24(b[14:], f.rect.Dx()-1)
		put24(b[17:], f.rect.Dy()-1)
		put24(b[20:], f.ms)
		b[23] = 0 // keep the picture under the next frame
		if !f.blend {
			b[23] |= 1 << 1
		}
		bw.Write(b[:24])
		if _, err := io.Copy(bw, io.NewSectionReader(e.data, f.off, f.n)); err != nil {
			return err
		}
	}
	return bw.Flush()
}

// writeStill writes the one frame as a still WebP: its image chunks, after
// a VP8X chunk when an ALPH chunk needs one.
func (e *encoder) writeStill(w io.Writer) error {
	f := e.frames[0]
	var id [4]byte
	if _, err := e.data.ReadAt(id[:], f.off); err != nil {
		return err
	}
	alph := string(id[:]) == "ALPH"
	total := 4 + f.n
	if alph {
		total += 8 + 10
	}
	if total > math.MaxUint32-8 {
		return errors.New("goimgtools: the image is too large for a WebP file (4GB)")
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	var b [30]byte
	copy(b[:], "RIFF")
	binary.LittleEndian.PutUint32(b[4:], uint32(total))
	copy(b[8:], "WEBP")
	n := 12
	if alph {
		copy(b[12:], "VP8X")
		binary.LittleEndian.PutUint32(b[16:], 10)
		b[20] = 1 << 4 // alpha
		b[24], b[25], b[26] = byte(e.w-1), byte((e.w-1)>>8), byte((e.w-1)>>16)
		b[27], b[28], b[29] = byte(e.h-1), byte((e.h-1)>>8), byte((e.h-1)>>16)
		n = 30
	}
	bw.Write(b[:n])
	if _, err := io.Copy(bw, io.NewSectionReader(e.data, f.off, f.n)); err != nil {
		return err
	}
	return bw.Flush()
}

// rectSource is a rectangle of the picture just read, as NRGBA strips.
// With blend, what is as in the picture before is transparent: the pixels
// that are, or the blocks of still.
type rectSource struct {
	e        *encoder
	rect     image.Rectangle
	blend    bool
	still    *blocks
	opaque   bool
	rows     int
	y        int
	band     []byte
	before   []byte
	reserved int64
}

func (s *rectSource) Bounds() image.Rectangle { return image.Rect(0, 0, s.rect.Dx(), s.rect.Dy()) }
func (s *rectSource) ColorModel() color.Model { return color.NRGBAModel }
func (s *rectSource) Env() *pipeline.Env      { return s.e.env }
func (s *rectSource) Opaque() bool            { return s.opaque }

func (s *rectSource) Close() error {
	s.e.env.Budget.Release(s.reserved)
	s.reserved, s.band, s.before = 0, nil, nil
	return nil
}

func (s *rectSource) Next() (image.Image, error) {
	h := s.rect.Dy()
	if s.y >= h {
		return nil, io.EOF
	}
	stride := 4 * s.rect.Dx()
	pixels := s.blend && s.still == nil // the pixels that are as before are left out
	if s.band == nil {
		need := int64(stride) * int64(s.rows)
		if pixels {
			need += int64(stride)
		}
		if err := s.e.env.Budget.Reserve(need, "animation strip"); err != nil {
			return nil, err
		}
		s.reserved = need
		s.band = make([]byte, stride*s.rows)
		if pixels {
			s.before = make([]byte, stride)
		}
	}
	n := min(s.rows, h-s.y)
	for i := 0; i < n; i++ {
		row := s.band[i*stride:][:stride]
		y := s.rect.Min.Y + s.y + i
		if _, err := s.e.cur.ReadAt(row, s.e.off(s.rect.Min.X, y)); err != nil {
			return nil, err
		}
		if !pixels {
			continue
		}
		if _, err := s.e.prev.ReadAt(s.before, s.e.off(s.rect.Min.X, y)); err != nil {
			return nil, err
		}
		for x := 0; x < stride; x += 4 {
			if bytes.Equal(row[x:x+4], s.before[x:x+4]) {
				clear(row[x : x+4])
			}
		}
	}
	if s.still != nil {
		s.still.flatten(s.band, stride, s.y, n)
	}
	m := &image.NRGBA{Pix: s.band[:n*stride], Stride: stride, Rect: image.Rect(0, s.y, s.rect.Dx(), s.y+n)}
	s.y += n
	return m, nil
}
