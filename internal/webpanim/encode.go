// Package webpanim writes animated WebP files: the frames of an animation,
// each a whole picture, as ANMF chunks of lossy (VP8) or lossless (VP8L)
// images.
//
// The first frame is written whole; every later one only as the rectangle
// that changed since the frame before, and a frame that changes nothing
// lengthens the one before instead (frames all alike make a still image). When a lossless frame has no
// transparent pixels, the pixels of its rectangle that stay as they were
// are made transparent and the frame is blended onto the picture: they
// cost next to nothing. The frames are encoded one at a time, as they
// come; the two pictures compared (the frame before and this one) are kept
// in working storage, and the encoded frames in a spill buffer until the
// size of the file is known.
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
	// frames otherwise; Effort is the encoder's in either case.
	Lossless bool
	Quality  int
	Effort   int
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
	env       *pipeline.Env
	o         Options
	w, h      int
	prev, cur *pipeline.Scratch // the frame before, this one (NRGBA)
	data      *pipeline.Spill   // the image chunks of the frames
	frames    []frame
	alpha     bool // some frame has transparent pixels
	rows      []byte
	reserved  int64
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
	e := &encoder{env: env, o: o, w: b.Dx(), h: b.Dy()}
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
	var changed image.Rectangle
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
			changed = changed.Union(image.Rect(x0, y0+y, x1, y0+y+1))
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

	if !first && changed.Empty() {
		// The same picture: the frame before stays longer.
		f := &e.frames[len(e.frames)-1]
		f.ms = min(f.ms+ms, maxDuration)
		return nil
	}
	rect := image.Rect(0, 0, e.w, e.h)
	if !first {
		// A frame starts at even coordinates.
		rect = image.Rect(changed.Min.X&^1, changed.Min.Y&^1, changed.Max.X, changed.Max.Y)
	}
	blend := !first && opaque && e.o.Lossless
	e.alpha = e.alpha || !opaque || blend
	if err := e.env.Budget.Reserve(frameBytes, "animation frame list"); err != nil {
		return err
	}
	e.frames = append(e.frames, frame{rect: rect, ms: min(ms, maxDuration), blend: blend})
	if err := e.encode(&e.frames[len(e.frames)-1], opaque); err != nil {
		return err
	}
	e.prev, e.cur = e.cur, e.prev
	return nil
}

// encode encodes the rectangle of f from the picture just read and puts
// its image chunks in the spill buffer.
func (e *encoder) encode(f *frame, opaque bool) error {
	src := &rectSource{e: e, rect: f.rect, blend: f.blend, opaque: opaque && !f.blend,
		rows: pipeline.StripRows(4*int64(f.rect.Dx()), f.rect.Dy())}
	defer src.Close()
	file := pipeline.NewSpill(e.env)
	defer file.Close()
	var err error
	if e.o.Lossless {
		err = vp8lenc.Encode(file, src, &vp8lenc.Options{Effort: e.o.Effort, Clean: !e.o.Exact, NearLossless: e.o.NearLossless})
	} else {
		err = vp8enc.Encode(file, src, &vp8enc.Options{Quality: e.o.Quality, Effort: e.o.Effort, Exact: e.o.Exact, AlphaQuality: e.o.AlphaQuality})
	}
	if err != nil {
		return err
	}
	// The encoder wrote a whole file: RIFF, maybe VP8X, then the image
	// chunks, which are what a frame holds.
	f.off = e.data.Size()
	var hdr [8]byte
	for pos := int64(12); pos+8 <= file.Size(); {
		if _, err := file.ReadAt(hdr[:], pos); err != nil {
			return err
		}
		n := int64(binary.LittleEndian.Uint32(hdr[4:]))
		whole := 8 + n + n&1
		if id := string(hdr[:4]); id == "ALPH" || id == "VP8 " || id == "VP8L" {
			if _, err := io.Copy(e.data, io.NewSectionReader(file, pos, whole)); err != nil {
				return err
			}
		}
		pos += whole
	}
	f.n = e.data.Size() - f.off
	if f.n == 0 {
		return errors.New("goimgtools: webp: the encoder wrote no image")
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
// With blend, the pixels that are as in the frame before are transparent.
type rectSource struct {
	e        *encoder
	rect     image.Rectangle
	blend    bool
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
	if s.band == nil {
		need := int64(stride) * int64(s.rows)
		if s.blend {
			need += int64(stride)
		}
		if err := s.e.env.Budget.Reserve(need, "animation strip"); err != nil {
			return nil, err
		}
		s.reserved = need
		s.band = make([]byte, stride*s.rows)
		if s.blend {
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
		if !s.blend {
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
	m := &image.NRGBA{Pix: s.band[:n*stride], Stride: stride, Rect: image.Rect(0, s.y, s.rect.Dx(), s.y+n)}
	s.y += n
	return m, nil
}
