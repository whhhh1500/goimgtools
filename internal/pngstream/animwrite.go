package pngstream

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/color"
	"io"
	"math"
	"sort"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// AnimOptions are the settings of an animated PNG (APNG).
type AnimOptions struct {
	// LoopCount is how often the animation plays: 0 for ever.
	LoopCount int
	// Level is the compression level, as Options.Level.
	Level int
}

// An animated PNG is written in two passes. The first reads the frames,
// each a whole picture, keeps every one that differs from the one before
// in working storage (a frame like the one before lengthens it instead),
// and notes whether any pixel is transparent and, up to 256, which colours
// there are. The second writes the file: with a palette when the colours
// fit in one (one palette for all frames), else as RGB or RGBA; the first
// frame whole, and every later one as the rectangle that changed, either
// as it is (blend_op SOURCE) or, when that is smaller and the pixels that
// changed are opaque, with the pixels that stay as they were made
// transparent and blended over the picture (blend_op OVER). The frames
// are never disposed of: each is drawn on the one before.

// apngFrame is a distinct picture of the animation: how long it shows, the
// rectangle that changed since the picture before, and whether every pixel
// that changed is opaque (so that it can be blended over).
type apngFrame struct {
	ms           int
	rect         image.Rectangle
	opaqueChange bool
}

type apngEncoder struct {
	env       *pipeline.Env
	o         AnimOptions
	w, h      int
	store     *pipeline.Spill   // the pictures, NRGBA, one after another
	prev, cur *pipeline.Scratch // the picture before, this one (NRGBA)
	frames    []apngFrame
	opaque    bool
	colors    map[uint32]int // up to 256 colours and how often each is used; nil past that
	rows      []byte
	reserved  int64
}

// apngFrameBytes is what a frame's entry in the list is counted as.
const apngFrameBytes = 64

// EncodeAnimation writes the frames next hands out as an animated PNG: each
// a whole picture, all of the same size, with how long it shows in
// milliseconds; io.EOF after the last. Frames that are all alike make a
// still PNG. Nothing is written to w before the last frame has been read;
// if EncodeAnimation fails, what it wrote must be discarded.
func EncodeAnimation(w io.Writer, env *pipeline.Env, o AnimOptions, next func() (pipeline.Source, int, error)) error {
	if o.LoopCount < 0 || int64(o.LoopCount) > math.MaxInt32 {
		return fmt.Errorf("goimgtools: an APNG plays 0 (for ever) to %d times, not %d", math.MaxInt32, o.LoopCount)
	}
	if o.Level < 0 || o.Level > 9 {
		return errors.New("goimgtools: PNG compression level must be 1 to 9")
	}
	src, ms, err := next()
	if err == io.EOF {
		return errors.New("goimgtools: the animation has no frames")
	}
	if err != nil {
		return err
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 || int64(b.Dx()) > math.MaxInt32 || int64(b.Dy()) > math.MaxInt32 {
		src.Close()
		return FormatError(fmt.Sprintf("invalid image size: %dx%d", b.Dx(), b.Dy()))
	}
	e := &apngEncoder{env: env, o: o, w: b.Dx(), h: b.Dy(), opaque: true, colors: map[uint32]int{}}
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
	e.store = pipeline.NewSpill(env)
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

func (e *apngEncoder) close() {
	for _, s := range []*pipeline.Scratch{e.prev, e.cur} {
		if s != nil {
			s.Close()
		}
	}
	if e.store != nil {
		e.store.Close()
	}
	e.env.Budget.Release(e.reserved + int64(len(e.frames))*apngFrameBytes)
	e.prev, e.cur, e.store, e.frames, e.rows, e.reserved = nil, nil, nil, nil, nil, 0
}

// rowBuf returns a buffer of n bytes, counted in the budget.
func (e *apngEncoder) rowBuf(n int) ([]byte, error) {
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

func (e *apngEncoder) off(x, y int) int64 { return (int64(y)*int64(e.w) + int64(x)) * 4 }

// add reads the frame src (closing it) and keeps it, unless it is the
// picture before, which then shows longer.
func (e *apngEncoder) add(src pipeline.Source, ms int) error {
	if src.Bounds().Size() != image.Pt(e.w, e.h) {
		src.Close()
		return errors.New("goimgtools: the chain gave frames of different sizes")
	}
	ms = max(ms, 0)
	first := len(e.frames) == 0
	var changed image.Rectangle
	opaqueChange := true
	stride := 4 * e.w
	last, lastN := uint32(0), -1 // the colour counted last, a run of it
	m := pipeline.Map(src, func(s *image.NRGBA) error {
		y0, n := s.Rect.Min.Y, s.Rect.Dy()
		for y := 0; y < n; y++ {
			row := s.Pix[y*s.Stride:][:stride]
			for i := 0; i < len(row); i += 4 {
				a := row[i+3]
				if a != 0xff {
					e.opaque = false
					if a == 0 {
						// What a transparent pixel would be is of no
						// account: all are the same.
						row[i], row[i+1], row[i+2] = 0, 0, 0
					}
				}
				if e.colors == nil {
					continue
				}
				c := binary.BigEndian.Uint32(row[i:])
				if c == last && lastN >= 0 {
					lastN++
					continue
				}
				if lastN > 0 {
					e.colors[last] += lastN
				}
				last, lastN = c, 1
				if _, ok := e.colors[c]; !ok && len(e.colors) == 256 {
					e.colors = nil
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
			x0, x1 := e.w, 0
			for x := 0; x < e.w; x++ {
				if !bytes.Equal(row[4*x:4*x+4], before[4*x:4*x+4]) {
					x0, x1 = min(x0, x), x+1
					if row[4*x+3] != 0xff {
						opaqueChange = false
					}
				}
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
	if e.colors != nil && lastN > 0 {
		e.colors[last] += lastN
	}
	if !first && changed.Empty() {
		// The same picture: the frame before stays longer.
		f := &e.frames[len(e.frames)-1]
		f.ms = min(f.ms+ms, math.MaxInt32)
		return nil
	}
	if first {
		changed = image.Rect(0, 0, e.w, e.h)
	}
	if err := e.env.Budget.Reserve(apngFrameBytes, "animation frame list"); err != nil {
		return err
	}
	e.frames = append(e.frames, apngFrame{ms: ms, rect: changed, opaqueChange: opaqueChange && !first})
	// The picture is kept for the second pass.
	row, err := e.rowBuf(stride)
	if err != nil {
		return err
	}
	for y := 0; y < e.h; y++ {
		if _, err := e.cur.ReadAt(row, e.off(0, y)); err != nil {
			return err
		}
		if _, err := e.store.Write(row); err != nil {
			return err
		}
	}
	e.prev, e.cur = e.cur, e.prev
	return nil
}

// apngMode is how the pictures are written: with a palette (and index
// keep, transparent, to blend over; -1 without one) or as RGB or RGBA.
type apngMode struct {
	pal   color.Palette
	index map[uint32]uint8
	keep  int
	alpha bool
}

func (e *apngEncoder) mode() apngMode {
	if e.colors == nil {
		return apngMode{alpha: !e.opaque, keep: -1}
	}
	// The palette: the transparent colour first (the tRNS chunk then
	// holds one entry), then by how often each is used.
	type entry struct {
		c uint32
		n int
	}
	list := make([]entry, 0, len(e.colors)+1)
	hasClear := false
	for c, n := range e.colors {
		list = append(list, entry{c, n})
		hasClear = hasClear || c == 0
	}
	if !hasClear && len(list) < 256 && len(e.frames) > 1 {
		// An index to blend over with, if there is room.
		list = append(list, entry{0, 0})
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if (a.c == 0) != (b.c == 0) {
			return a.c == 0
		}
		if a.n != b.n {
			return a.n > b.n
		}
		return a.c < b.c
	})
	m := apngMode{index: make(map[uint32]uint8, len(list)), keep: -1}
	for i, en := range list {
		m.pal = append(m.pal, color.NRGBA{uint8(en.c >> 24), uint8(en.c >> 16), uint8(en.c >> 8), uint8(en.c)})
		m.index[en.c] = uint8(i)
		if en.c == 0 {
			m.keep = i
		}
	}
	return m
}

// write writes the file from the pictures kept.
func (e *apngEncoder) write(w io.Writer) error {
	mode := e.mode()
	bw := bufio.NewWriterSize(w, 64<<10)
	cw := &chunkWriter{w: bw}
	cw.raw(pngHeader)
	var ihdr [13]byte
	binary.BigEndian.PutUint32(ihdr[0:], uint32(e.w))
	binary.BigEndian.PutUint32(ihdr[4:], uint32(e.h))
	switch {
	case mode.pal != nil:
		ihdr[8] = paletteDepth(len(mode.pal))
		ihdr[9] = ctPaletted
	case mode.alpha:
		ihdr[8], ihdr[9] = 8, ctTrueColorAlpha
	default:
		ihdr[8], ihdr[9] = 8, ctTrueColor
	}
	cw.chunk("IHDR", ihdr[:])
	if len(e.frames) > 1 {
		var actl [8]byte
		binary.BigEndian.PutUint32(actl[0:], uint32(len(e.frames)))
		binary.BigEndian.PutUint32(actl[4:], uint32(e.o.LoopCount))
		cw.chunk("acTL", actl[:])
	}
	if mode.pal != nil {
		var plte []byte
		last := -1
		for i, c := range mode.pal {
			n := c.(color.NRGBA)
			plte = append(plte, n.R, n.G, n.B)
			if n.A != 0xff {
				last = i
			}
		}
		cw.chunk("PLTE", plte)
		if last >= 0 {
			trns := make([]byte, last+1)
			for i := range trns {
				trns[i] = mode.pal[i].(color.NRGBA).A
			}
			cw.chunk("tRNS", trns)
		}
	}
	seq := uint32(0)
	for i := range e.frames {
		if cw.err != nil {
			break
		}
		f := &e.frames[i]
		data, over, err := e.encodeFrame(i, mode)
		if err != nil {
			return fmt.Errorf("frame %d: %w", i+1, err)
		}
		if len(e.frames) > 1 {
			var fctl [26]byte
			binary.BigEndian.PutUint32(fctl[0:], seq)
			seq++
			binary.BigEndian.PutUint32(fctl[4:], uint32(f.rect.Dx()))
			binary.BigEndian.PutUint32(fctl[8:], uint32(f.rect.Dy()))
			binary.BigEndian.PutUint32(fctl[12:], uint32(f.rect.Min.X))
			binary.BigEndian.PutUint32(fctl[16:], uint32(f.rect.Min.Y))
			num, den := apngDelay(f.ms)
			binary.BigEndian.PutUint16(fctl[20:], num)
			binary.BigEndian.PutUint16(fctl[22:], den)
			fctl[24] = 0 // dispose_op NONE: the next frame is drawn on this one
			if over {
				fctl[25] = 1 // blend_op OVER
			}
			cw.chunk("fcTL", fctl[:])
		}
		err = cw.data(data, i > 0, &seq)
		data.Close()
		if err != nil {
			return err
		}
	}
	cw.chunk("IEND", nil)
	if cw.err != nil {
		return cw.err
	}
	return bw.Flush()
}

// apngDelay is how long a frame shows as a fraction of a second: in
// thousandths up to 65.535s, then in hundredths and tenths, and at most
// 6553.5s.
func apngDelay(ms int) (uint16, uint16) {
	for _, d := range []struct{ div, den int }{{1, 1000}, {10, 100}, {100, 10}} {
		if v := (ms + d.div/2) / d.div; v <= math.MaxUint16 {
			return uint16(v), uint16(d.den)
		}
	}
	return math.MaxUint16, 10
}

// paletteDepth is the bit depth of a palette of n colours.
func paletteDepth(n int) uint8 {
	switch {
	case n <= 2:
		return 1
	case n <= 4:
		return 2
	case n <= 16:
		return 4
	}
	return 8
}

// encodeFrame compresses frame i's rectangle: as it is, or blended over
// with the pixels that stay transparent, whichever is smaller (when it can
// be blended). It returns the zlib stream and whether it blends.
func (e *apngEncoder) encodeFrame(i int, mode apngMode) (*pipeline.Spill, bool, error) {
	f := e.frames[i]
	src, err := e.encodeRect(i, mode, false)
	if err != nil || !f.opaqueChange || (mode.pal != nil && mode.keep < 0) || (mode.pal == nil && !mode.alpha) {
		return src, false, err
	}
	over, err := e.encodeRect(i, mode, true)
	if err != nil {
		src.Close()
		return nil, false, err
	}
	if over.Size() < src.Size() {
		src.Close()
		return over, true, nil
	}
	over.Close()
	return src, false, nil
}

// encodeRect compresses the rectangle of frame i, with the pixels as in
// the picture before made transparent when keep is set, and returns the
// zlib stream (what the IDAT chunks hold).
func (e *apngEncoder) encodeRect(i int, mode apngMode, keep bool) (*pipeline.Spill, error) {
	f := e.frames[i]
	src := &apngRect{e: e, i: i, rect: f.rect, keep: keep, mode: mode,
		rows: pipeline.StripRows(4*int64(f.rect.Dx()), f.rect.Dy())}
	defer src.Close()
	file := pipeline.NewSpill(e.env)
	defer file.Close()
	if err := Encode(file, src, &Options{Level: e.o.Level, alpha: mode.pal == nil && mode.alpha}); err != nil {
		return nil, err
	}
	// The encoder wrote a whole file: what its IDAT chunks hold is the
	// frame's data.
	out := pipeline.NewSpill(e.env)
	var hdr [8]byte
	for pos := int64(len(pngHeader)); pos+12 <= file.Size(); {
		if _, err := file.ReadAt(hdr[:], pos); err != nil {
			out.Close()
			return nil, err
		}
		n := int64(binary.BigEndian.Uint32(hdr[:4]))
		if string(hdr[4:]) == "IDAT" {
			if _, err := io.Copy(out, io.NewSectionReader(file, pos+8, n)); err != nil {
				out.Close()
				return nil, err
			}
		}
		pos += 12 + n
	}
	if out.Size() == 0 {
		out.Close()
		return nil, errors.New("goimgtools: png: the encoder wrote no image")
	}
	return out, nil
}

// apngRect is the rectangle of a kept picture as strips: NRGBA, or
// paletted with the animation's palette. With keep, the pixels that are as
// in the picture before are transparent.
type apngRect struct {
	e        *apngEncoder
	i        int
	rect     image.Rectangle
	keep     bool
	mode     apngMode
	rows     int
	y        int
	band     []byte
	idx      []byte
	before   []byte
	reserved int64
}

func (s *apngRect) Bounds() image.Rectangle { return image.Rect(0, 0, s.rect.Dx(), s.rect.Dy()) }
func (s *apngRect) Env() *pipeline.Env      { return s.e.env }
func (s *apngRect) Opaque() bool            { return s.mode.pal == nil && !s.mode.alpha }

func (s *apngRect) ColorModel() color.Model {
	if s.mode.pal != nil {
		return s.mode.pal
	}
	return color.NRGBAModel
}

func (s *apngRect) Close() error {
	s.e.env.Budget.Release(s.reserved)
	s.reserved, s.band, s.idx, s.before = 0, nil, nil, nil
	return nil
}

// at is where row y of picture i is kept.
func (s *apngRect) at(i, y int) int64 {
	return int64(i)*int64(s.e.w)*int64(s.e.h)*4 + s.e.off(s.rect.Min.X, y)
}

func (s *apngRect) Next() (image.Image, error) {
	h := s.rect.Dy()
	if s.y >= h {
		return nil, io.EOF
	}
	w := s.rect.Dx()
	stride := 4 * w
	if s.band == nil {
		need := int64(stride) * int64(s.rows)
		if s.keep {
			need += int64(stride)
		}
		if s.mode.pal != nil {
			need += int64(w) * int64(s.rows)
		}
		if err := s.e.env.Budget.Reserve(need, "animation strip"); err != nil {
			return nil, err
		}
		s.reserved = need
		s.band = make([]byte, stride*s.rows)
		if s.keep {
			s.before = make([]byte, stride)
		}
		if s.mode.pal != nil {
			s.idx = make([]byte, w*s.rows)
		}
	}
	n := min(s.rows, h-s.y)
	for r := 0; r < n; r++ {
		row := s.band[r*stride:][:stride]
		y := s.rect.Min.Y + s.y + r
		if _, err := s.e.store.ReadAt(row, s.at(s.i, y)); err != nil {
			return nil, err
		}
		if s.keep {
			if _, err := s.e.store.ReadAt(s.before, s.at(s.i-1, y)); err != nil {
				return nil, err
			}
			for x := 0; x < stride; x += 4 {
				if bytes.Equal(row[x:x+4], s.before[x:x+4]) {
					clear(row[x : x+4])
				}
			}
		}
		if s.mode.pal != nil {
			ix := s.idx[r*w:][:w]
			for x := range ix {
				ix[x] = s.mode.index[binary.BigEndian.Uint32(row[4*x:])]
			}
		}
	}
	rect := image.Rect(0, s.y, w, s.y+n)
	s.y += n
	if s.mode.pal != nil {
		return &image.Paletted{Pix: s.idx[:n*w], Stride: w, Rect: rect, Palette: s.mode.pal}, nil
	}
	return &image.NRGBA{Pix: s.band[:n*stride], Stride: stride, Rect: rect}, nil
}

// chunkWriter writes PNG chunks, remembering the first error.
type chunkWriter struct {
	w   io.Writer
	err error
}

func (c *chunkWriter) raw(s string) {
	if c.err == nil {
		_, c.err = io.WriteString(c.w, s)
	}
}

func (c *chunkWriter) chunk(name string, data []byte) {
	if c.err != nil {
		return
	}
	var hdr [8]byte
	binary.BigEndian.PutUint32(hdr[:4], uint32(len(data)))
	copy(hdr[4:], name)
	crc := crc32.NewIEEE()
	crc.Write(hdr[4:])
	crc.Write(data)
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], crc.Sum32())
	for _, b := range [][]byte{hdr[:], data, sum[:]} {
		if _, c.err = c.w.Write(b); c.err != nil {
			return
		}
	}
}

// data writes a frame's zlib stream in IDAT chunks, or in fdAT chunks
// (each with the next sequence number) for a frame after the first.
func (c *chunkWriter) data(s *pipeline.Spill, fdat bool, seq *uint32) error {
	const piece = 1 << 20
	buf := make([]byte, 4+min(piece, s.Size()))
	for off := int64(0); off < s.Size() && c.err == nil; {
		n := min(piece, s.Size()-off)
		body := buf[:n]
		name := "IDAT"
		if fdat {
			body = buf[:4+n]
			binary.BigEndian.PutUint32(body, *seq)
			*seq++
			name = "fdAT"
		}
		if _, err := s.ReadAt(body[len(body)-int(n):], off); err != nil && err != io.EOF {
			return err
		}
		c.chunk(name, body)
		off += n
	}
	return c.err
}
