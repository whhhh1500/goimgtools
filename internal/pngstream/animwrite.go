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
// frame whole, and every later one as the rectangle that differs from what
// it is drawn on, either as it is (blend_op SOURCE) or, when that is
// smaller and the pixels that change are opaque, with the pixels that stay
// as they were made transparent and blended over (blend_op OVER). What a
// frame is drawn on is chosen too, by the dispose_op of the frame before
// (see write).

// apngFrame is a distinct picture of the animation: how long it shows.
type apngFrame struct {
	ms int
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
	if o.Level < 0 || o.Level > MaxLevel {
		return errors.New("goimgtools: PNG compression level must be 1 to 10")
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
			changed = changed.Union(image.Rect(0, y0+y, e.w, y0+y+1))
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
	if err := e.env.Budget.Reserve(apngFrameBytes, "animation frame list"); err != nil {
		return err
	}
	e.frames = append(e.frames, apngFrame{ms: ms})
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
//
// Each frame after the first is drawn on what the frame before leaves,
// which its dispose_op chooses: the picture as it is (NONE), with the
// frame's rectangle cleared (BACKGROUND), or with it as it was before that
// frame (PREVIOUS). For every frame the three are tried, as apngopt tries
// them, each with the rectangle that differs from it and both blend ops,
// and the smallest is kept, which sets the dispose_op of the frame before:
// a frame is written once the next is chosen.
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
	// The pictures the frame before and this one are drawn on (the first
	// on a transparent canvas, all zero).
	size := int64(e.w) * int64(e.h) * 4
	var err error
	e.prev.Close()
	e.cur.Close()
	e.prev, e.cur = nil, nil
	if e.prev, err = pipeline.NewScratch(e.env, size); err != nil {
		return err
	}
	if e.cur, err = pipeline.NewScratch(e.env, size); err != nil {
		return err
	}
	seq := uint32(0)
	var pend apngChoice // the frame chosen, not yet written
	flush := func(dispose byte) error {
		if pend.data == nil {
			return nil
		}
		defer func() { pend.data.Close(); pend.data = nil }()
		if len(e.frames) > 1 {
			var fctl [26]byte
			binary.BigEndian.PutUint32(fctl[0:], seq)
			seq++
			binary.BigEndian.PutUint32(fctl[4:], uint32(pend.rect.Dx()))
			binary.BigEndian.PutUint32(fctl[8:], uint32(pend.rect.Dy()))
			binary.BigEndian.PutUint32(fctl[12:], uint32(pend.rect.Min.X))
			binary.BigEndian.PutUint32(fctl[16:], uint32(pend.rect.Min.Y))
			num, den := apngDelay(pend.ms)
			binary.BigEndian.PutUint16(fctl[20:], num)
			binary.BigEndian.PutUint16(fctl[22:], den)
			fctl[24] = dispose
			if pend.over {
				fctl[25] = 1 // blend_op OVER
			}
			cw.chunk("fcTL", fctl[:])
		}
		return cw.data(pend.data, pend.i > 0, &seq)
	}
	defer func() {
		if pend.data != nil {
			pend.data.Close()
		}
	}()
	for i := range e.frames {
		if cw.err != nil {
			break
		}
		c, err := e.choose(i, mode, pend)
		if err != nil {
			return fmt.Errorf("frame %d: %w", i+1, err)
		}
		if err := flush(c.dispose); err != nil {
			return err
		}
		// What this frame is drawn on, for the PREVIOUS of the next.
		if i > 0 {
			if err := e.keepBase(i, c.dispose, pend.rect); err != nil {
				return err
			}
		}
		pend = c
	}
	if err := flush(0); err != nil {
		return err
	}
	cw.chunk("IEND", nil)
	if cw.err != nil {
		return cw.err
	}
	return bw.Flush()
}

// apngChoice is how frame i is written: its rectangle, blend op and data,
// and the dispose_op of the frame before that it is drawn after.
type apngChoice struct {
	i       int
	ms      int
	rect    image.Rectangle
	over    bool
	dispose byte
	data    *pipeline.Spill
}

// baseRow fills buf with row y of what frame i is drawn on when the frame
// before (whose rectangle is prev) is disposed of by dispose.
func (e *apngEncoder) baseRow(i int, dispose byte, prev image.Rectangle, y int, buf []byte) error {
	if dispose == 2 {
		_, err := e.prev.ReadAt(buf, e.off(0, y))
		return err
	}
	if _, err := e.store.ReadAt(buf, e.pictureAt(i-1, 0, y)); err != nil {
		return err
	}
	if dispose == 1 && y >= prev.Min.Y && y < prev.Max.Y {
		clear(buf[4*prev.Min.X : 4*prev.Max.X])
	}
	return nil
}

// keepBase records what frame i is drawn on (the frame before disposed of
// by dispose), which a PREVIOUS after frame i restores: e.prev then holds
// it, e.cur what frame i-1 was drawn on.
func (e *apngEncoder) keepBase(i int, dispose byte, prev image.Rectangle) error {
	if dispose == 2 {
		// The same picture as the frame before was drawn on.
		return nil
	}
	row, err := e.rowBuf(4 * e.w)
	if err != nil {
		return err
	}
	for y := 0; y < e.h; y++ {
		if err := e.baseRow(i, dispose, prev, y, row); err != nil {
			return err
		}
		if _, err := e.cur.WriteAt(row, e.off(0, y)); err != nil {
			return err
		}
	}
	e.prev, e.cur = e.cur, e.prev
	return nil
}

func (e *apngEncoder) pictureAt(i, x, y int) int64 {
	return int64(i)*int64(e.w)*int64(e.h)*4 + e.off(x, y)
}

// choose works out how frame i is best written, after the frame before
// (pend) is disposed of in each of the three ways.
func (e *apngEncoder) choose(i int, mode apngMode, pend apngChoice) (apngChoice, error) {
	f := e.frames[i]
	if i == 0 {
		data, err := e.encodeRect(0, mode, image.Rect(0, 0, e.w, e.h), nil, e.o.Level)
		return apngChoice{i: 0, ms: f.ms, rect: image.Rect(0, 0, e.w, e.h), data: data}, err
	}
	// The candidates: each disposal with its rectangle; both blend ops
	// when the pixels that change are opaque and the file can be
	// transparent.
	type cand struct {
		dispose byte
		rect    image.Rectangle
		over    bool
	}
	var cands []cand
	canOver := (mode.pal != nil && mode.keep >= 0) || (mode.pal == nil && mode.alpha)
	pic, err := e.rowBuf(8 * e.w)
	if err != nil {
		return apngChoice{}, err
	}
	pic, base := pic[:4*e.w], pic[4*e.w:]
	for _, d := range []byte{0, 1, 2} {
		if d == 2 && i == 1 && pend.rect == image.Rect(0, 0, e.w, e.h) {
			// PREVIOUS after the first frame is BACKGROUND over the whole
			// canvas, tried as BACKGROUND.
			continue
		}
		var rect image.Rectangle
		opaque := true
		for y := 0; y < e.h; y++ {
			if _, err := e.store.ReadAt(pic, e.pictureAt(i, 0, y)); err != nil {
				return apngChoice{}, err
			}
			if err := e.baseRow(i, d, pend.rect, y, base); err != nil {
				return apngChoice{}, err
			}
			if bytes.Equal(pic, base) {
				continue
			}
			x0, x1 := e.w, 0
			for x := 0; x < e.w; x++ {
				if !bytes.Equal(pic[4*x:4*x+4], base[4*x:4*x+4]) {
					x0, x1 = min(x0, x), x+1
					if pic[4*x+3] != 0xff {
						opaque = false
					}
				}
			}
			rect = rect.Union(image.Rect(x0, y, x1, y+1))
		}
		if rect.Empty() {
			// Nothing to draw: one pixel as it is.
			rect = image.Rect(0, 0, 1, 1)
		}
		cands = append(cands, cand{d, rect, false})
		if canOver && opaque {
			cands = append(cands, cand{d, rect, true})
		}
	}
	// Each candidate compressed; from level 8 they are compared at level
	// 7 and only the one chosen is compressed at the level asked for.
	level := e.o.Level
	if level == 0 {
		level = DefaultLevel
	}
	trial := level
	if level >= 8 {
		trial = 7
	}
	var best apngChoice
	for _, c := range cands {
		var keep func(y int, buf []byte) error
		if c.over {
			d := c.dispose
			keep = func(y int, buf []byte) error { return e.baseRow(i, d, pend.rect, y, buf) }
		}
		data, err := e.encodeRect(i, mode, c.rect, keep, trial)
		if err != nil {
			if best.data != nil {
				best.data.Close()
			}
			return apngChoice{}, err
		}
		if best.data == nil || data.Size() < best.data.Size() {
			if best.data != nil {
				best.data.Close()
			}
			best = apngChoice{i: i, ms: f.ms, rect: c.rect, over: c.over, dispose: c.dispose, data: data}
		} else {
			data.Close()
		}
	}
	if trial != level {
		best.data.Close()
		var keep func(y int, buf []byte) error
		if best.over {
			d := best.dispose
			keep = func(y int, buf []byte) error { return e.baseRow(i, d, pend.rect, y, buf) }
		}
		if best.data, err = e.encodeRect(i, mode, best.rect, keep, level); err != nil {
			return apngChoice{}, err
		}
	}
	return best, nil
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

// encodeRect compresses rect of picture i at level, with the pixels that
// are as in what it is drawn on (keep's rows) made transparent when keep
// is set, and returns the zlib stream (what the IDAT chunks hold).
func (e *apngEncoder) encodeRect(i int, mode apngMode, rect image.Rectangle, keep func(y int, buf []byte) error, level int) (*pipeline.Spill, error) {
	src := &apngRect{e: e, i: i, rect: rect, keep: keep, mode: mode,
		rows: pipeline.StripRows(4*int64(rect.Dx()), rect.Dy())}
	defer src.Close()
	file := pipeline.NewSpill(e.env)
	defer file.Close()
	if err := Encode(file, src, &Options{Level: level, alpha: mode.pal == nil && mode.alpha}); err != nil {
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

// apngRect is a rectangle of a kept picture as strips: NRGBA, or paletted
// with the animation's palette. With keep, the pixels that are as in what
// the frame is drawn on are transparent.
type apngRect struct {
	e        *apngEncoder
	i        int
	rect     image.Rectangle
	keep     func(y int, buf []byte) error
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

func (s *apngRect) Next() (image.Image, error) {
	h := s.rect.Dy()
	if s.y >= h {
		return nil, io.EOF
	}
	w := s.rect.Dx()
	stride := 4 * w
	if s.band == nil {
		need := int64(stride) * int64(s.rows)
		if s.keep != nil {
			need += 4 * int64(s.e.w)
		}
		if s.mode.pal != nil {
			need += int64(w) * int64(s.rows)
		}
		if err := s.e.env.Budget.Reserve(need, "animation strip"); err != nil {
			return nil, err
		}
		s.reserved = need
		s.band = make([]byte, stride*s.rows)
		if s.keep != nil {
			s.before = make([]byte, 4*s.e.w)
		}
		if s.mode.pal != nil {
			s.idx = make([]byte, w*s.rows)
		}
	}
	n := min(s.rows, h-s.y)
	for r := 0; r < n; r++ {
		row := s.band[r*stride:][:stride]
		y := s.rect.Min.Y + s.y + r
		if _, err := s.e.store.ReadAt(row, s.e.pictureAt(s.i, s.rect.Min.X, y)); err != nil {
			return nil, err
		}
		if s.keep != nil {
			if err := s.keep(y, s.before); err != nil {
				return nil, err
			}
			before := s.before[4*s.rect.Min.X:][:stride]
			for x := 0; x < stride; x += 4 {
				if bytes.Equal(row[x:x+4], before[x:x+4]) {
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
