package ops

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Orientation is one of the eight ways to turn or mirror an image: an
// optional transpose (x and y swapped), then an optional left-right mirror,
// then an optional top-bottom mirror.
type Orientation struct {
	Swap, MirrorX, MirrorY bool
}

var (
	Upright   = Orientation{}
	Flop      = Orientation{MirrorX: true}             // mirror left-right
	Flip      = Orientation{MirrorY: true}             // mirror top-bottom
	Rotate90  = Orientation{Swap: true, MirrorX: true} // clockwise
	Rotate180 = Orientation{MirrorX: true, MirrorY: true}
	Rotate270 = Orientation{Swap: true, MirrorY: true}
)

// FromExif returns what must be done to the stored pixels of an image with
// EXIF orientation v (1 to 8) to show it upright.
func FromExif(v int) Orientation {
	switch v {
	case 2:
		return Flop
	case 3:
		return Rotate180
	case 4:
		return Flip
	case 5:
		return Orientation{Swap: true}
	case 6:
		return Rotate90
	case 7:
		return Orientation{Swap: true, MirrorX: true, MirrorY: true}
	case 8:
		return Rotate270
	}
	return Upright
}

// Then returns the orientation equal to applying o and then p.
func (o Orientation) Then(p Orientation) Orientation {
	mx, my := o.MirrorX, o.MirrorY
	if p.Swap { // a transpose turns a left-right mirror into a top-bottom one
		mx, my = my, mx
	}
	return Orientation{Swap: o.Swap != p.Swap, MirrorX: mx != p.MirrorX, MirrorY: my != p.MirrorY}
}

// Size returns the size of a w × h image after o.
func (o Orientation) Size(w, h int) (int, int) {
	if o.Swap {
		return h, w
	}
	return w, h
}

// Orient returns src turned by o. Nothing is read until the first Next.
// Orienting an Orient result that has not started combines the two.
//
// A left-right mirror alone streams row by row. Every other orientation
// needs the last rows first, so the pixels are stored in a pipeline.Scratch (a
// temporary file when they do not fit in the memory budget) and read back
// in the new order. YCbCr and other planar strips become RGBA.
func Orient(src pipeline.Source, o Orientation) pipeline.Source {
	if l, ok := src.(*oriented); ok && l.out == nil && l.err == nil {
		src, o = l.src, l.o.Then(o)
	}
	if o == Upright {
		return src
	}
	b := src.Bounds()
	w, h := o.Size(b.Dx(), b.Dy())
	return &oriented{src: src, env: pipeline.EnvOf(src), o: o, rect: image.Rect(0, 0, w, h)}
}

type oriented struct {
	src  pipeline.Source
	env  *pipeline.Env
	o    Orientation
	rect image.Rectangle
	out  pipeline.Source // set by the first Next
	err  error
}

func (l *oriented) Env() *pipeline.Env      { return l.env }
func (l *oriented) Bounds() image.Rectangle { return l.rect }
func (l *oriented) Opaque() bool            { return pipeline.IsOpaque(l.src) }

func (l *oriented) ColorModel() color.Model {
	if l.out != nil {
		return l.out.ColorModel()
	}
	return l.src.ColorModel()
}

func (l *oriented) Close() error {
	if l.err == nil {
		l.err = errors.New("goimgtools: source closed")
	}
	if l.out != nil {
		return l.out.Close()
	}
	return l.src.Close()
}

func (l *oriented) Next() (image.Image, error) {
	if l.err != nil {
		return nil, l.err
	}
	if l.out == nil {
		p := &pixSource{src: l.src, env: l.env, band: rgbaBand{env: l.env}}
		if l.o == Flop {
			l.out = &mirror{src: p, env: l.env}
		} else {
			l.out = &turned{src: p, env: l.env, o: l.o, rect: l.rect}
		}
	}
	m, err := l.out.Next()
	if err != nil {
		l.err = err
	}
	return m, err
}

// pixSource passes strips through, converting those whose pixels are not
// stored side by side (YCbCr, ...) to RGBA.
type pixSource struct {
	src  pipeline.Source
	env  *pipeline.Env
	band rgbaBand
}

func (p *pixSource) Env() *pipeline.Env      { return p.env }
func (p *pixSource) Bounds() image.Rectangle { return p.src.Bounds() }
func (p *pixSource) ColorModel() color.Model { return p.src.ColorModel() }
func (p *pixSource) Opaque() bool            { return pipeline.IsOpaque(p.src) }
func (p *pixSource) Close() error            { p.band.release(); return p.src.Close() }

func (p *pixSource) Next() (image.Image, error) {
	s, err := p.src.Next()
	if err != nil {
		return nil, err
	}
	if _, _, _, _, ok := pipeline.Pix(s); ok {
		return s, nil
	}
	r := s.Bounds()
	out, err := p.band.get(r)
	if err != nil {
		return nil, err
	}
	draw.Draw(out, r, s, r.Min, draw.Src)
	return out, nil
}

// reverse copies the n pixels of src into dst in the opposite order.
func reverse(dst, src []uint8, n, bpp int) {
	switch bpp {
	case 1:
		for i := 0; i < n; i++ {
			dst[i] = src[n-1-i]
		}
	case 4:
		for i := 0; i < n; i++ {
			d, s := dst[4*i:4*i+4:4*i+4], src[4*(n-1-i):4*(n-1-i)+4:4*(n-1-i)+4]
			d[0], d[1], d[2], d[3] = s[0], s[1], s[2], s[3]
		}
	default:
		for i := 0; i < n; i++ {
			copy(dst[i*bpp:(i+1)*bpp], src[(n-1-i)*bpp:(n-i)*bpp])
		}
	}
}

// mirror reverses every row, strip by strip.
type mirror struct {
	src      *pixSource
	env      *pipeline.Env
	band     image.Image
	reserved int64
}

func (m *mirror) Env() *pipeline.Env { return m.env }
func (m *mirror) Bounds() image.Rectangle {
	b := m.src.Bounds()
	return image.Rect(0, 0, b.Dx(), b.Dy())
}
func (m *mirror) ColorModel() color.Model { return m.src.ColorModel() }
func (m *mirror) Opaque() bool            { return m.src.Opaque() }

func (m *mirror) Close() error {
	m.env.Budget.Release(m.reserved)
	m.reserved, m.band = 0, nil
	return m.src.Close()
}

func (m *mirror) Next() (image.Image, error) {
	s, err := m.src.Next()
	if err != nil {
		return nil, err
	}
	r := s.Bounds()
	if m.band == nil || m.band.Bounds().Dy() < r.Dy() {
		m.env.Budget.Release(m.reserved)
		m.reserved, m.band = 0, nil
		full := image.Rect(0, 0, r.Dx(), r.Dy())
		need := pipeline.BandBytes(s, full)
		if err := m.env.Budget.Reserve(need, "mirror strip"); err != nil {
			return nil, err
		}
		m.reserved, m.band = need, pipeline.NewBand(s, full)
	}
	// Like every turn, the result has its origin at (0, 0).
	top := r.Min.Y - m.src.Bounds().Min.Y
	out := pipeline.Relabel(m.band, image.Rect(0, top, r.Dx(), top+r.Dy()))
	if p, ok := out.(*image.Paletted); ok {
		p.Palette = s.(*image.Paletted).Palette
	}
	sp, ss, bpp, sr, _ := pipeline.Pix(s)
	dp, ds, _, _, _ := pipeline.Pix(out)
	w := r.Dx()
	for y := 0; y < r.Dy(); y++ {
		so := (r.Min.Y+y-sr.Min.Y)*ss + (r.Min.X-sr.Min.X)*bpp
		reverse(dp[y*ds:y*ds+w*bpp], sp[so:so+w*bpp], w, bpp)
	}
	return out, nil
}

// turned stores the whole image, already in its new arrangement, and then
// hands it out in strips.
type turned struct {
	src      *pixSource
	env      *pipeline.Env
	o        Orientation
	rect     image.Rectangle // of the result
	sc       *pipeline.Scratch
	band     image.Image
	bpp      int
	n        int // rows per output strip
	y        int // next output row
	reserved int64
}

func (t *turned) Env() *pipeline.Env      { return t.env }
func (t *turned) Bounds() image.Rectangle { return t.rect }
func (t *turned) ColorModel() color.Model { return t.src.ColorModel() }
func (t *turned) Opaque() bool            { return t.src.Opaque() }

func (t *turned) Close() error {
	if t.sc != nil {
		t.sc.Close()
		t.sc = nil
	}
	t.env.Budget.Release(t.reserved)
	t.reserved, t.band = 0, nil
	return t.src.Close()
}

// load reads every strip of the source into the Scratch, each pixel at the
// place it has in the result.
func (t *turned) load() error {
	sb := t.src.Bounds()
	sw, sh := sb.Dx(), sb.Dy()
	dw := t.rect.Dx()
	var (
		buf    []uint8 // source rows collected for one round of writes
		run    []uint8
		rows   int // rows buf can hold
		have   int // rows in buf
		y0     int // source row of buf[0], relative to sb.Min.Y
		bpp    int
		proto  image.Image
		bufRes int64
	)
	defer func() { t.env.Budget.Release(bufRes) }()
	// flush writes the collected source rows [y0, y0+have) to their place.
	flush := func() error {
		if have == 0 {
			return nil
		}
		if !t.o.Swap {
			// Whole rows, mirrored as needed; only their order changes.
			for i := 0; i < have; i++ {
				row := buf[i*sw*bpp : (i+1)*sw*bpp]
				if t.o.MirrorX {
					reverse(run[:sw*bpp], row, sw, bpp)
					row = run[:sw*bpp]
				}
				dy := y0 + i
				if t.o.MirrorY {
					dy = sh - 1 - dy
				}
				if _, err := t.sc.WriteAt(row, int64(dy)*int64(dw)*int64(bpp)); err != nil {
					return err
				}
			}
			y0, have = y0+have, 0
			return nil
		}
		// Transposed: source column x becomes result row x (or its mirror),
		// and the collected rows are a run of consecutive pixels in it.
		for x := 0; x < sw; x++ {
			for i := 0; i < have; i++ {
				j := i
				if t.o.MirrorX {
					j = have - 1 - i
				}
				copy(run[j*bpp:(j+1)*bpp], buf[(i*sw+x)*bpp:(i*sw+x+1)*bpp])
			}
			dy, dx := x, y0
			if t.o.MirrorY {
				dy = sw - 1 - x
			}
			if t.o.MirrorX {
				dx = sh - y0 - have
			}
			if _, err := t.sc.WriteAt(run[:have*bpp], (int64(dy)*int64(dw)+int64(dx))*int64(bpp)); err != nil {
				return err
			}
		}
		y0, have = y0+have, 0
		return nil
	}
	for {
		s, err := t.src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		pix, stride, b, sr, _ := pipeline.Pix(s)
		if proto == nil {
			proto, bpp, t.bpp = s, b, b
			// The output strip is reserved before the store is made, so
			// the store takes memory only if both still fit.
			t.n = pipeline.StripRows(int64(dw)*int64(bpp), t.rect.Dy())
			need := pipeline.BandBytes(s, image.Rect(0, 0, dw, t.n))
			if err := t.env.Budget.Reserve(need, "rotation strip"); err != nil {
				return err
			}
			t.reserved = need
			if t.sc, err = pipeline.NewScratch(t.env, int64(sw)*int64(sh)*int64(bpp)); err != nil {
				return err
			}
			// Source rows are collected before they are written to their
			// new places. With the store in memory a few rows do. On disk a
			// transposed image is written in runs as long as the rows
			// collected, so there it is as many as an eighth of the limit
			// holds, at most 4MB: fewer, longer writes. (Measured on a 24
			// megapixel image: 8MB 1.10s, 4MB 1.25s, 2MB 1.63s.)
			rowBytes := int64(sw) * int64(bpp)
			room := int64(256 << 10)
			if t.sc.OnDisk() {
				room = min(4<<20, t.env.Budget.Limit()/8)
			}
			rows = int(max(1, min(int64(sh), room/rowBytes)))
			bufRes = rowBytes*int64(rows) + int64(max(sw, rows))*int64(bpp)
			if err := t.env.Budget.Reserve(bufRes, "rotation buffer"); err != nil {
				bufRes = 0
				return err
			}
			buf = make([]uint8, rowBytes*int64(rows))
			run = make([]uint8, max(sw, rows)*bpp)
		} else if b != bpp {
			return errors.New("goimgtools: source changed pixel type")
		}
		if p, ok := s.(*image.Paletted); ok {
			proto = p // the palette may grow; keep the latest
		}
		r := s.Bounds()
		for y := r.Min.Y; y < r.Max.Y; y++ {
			so := (y-sr.Min.Y)*stride + (r.Min.X-sr.Min.X)*bpp
			copy(buf[have*sw*bpp:], pix[so:so+sw*bpp])
			if have++; have == rows {
				if err := flush(); err != nil {
					return err
				}
			}
		}
	}
	if proto == nil || y0+have != sh {
		return io.ErrUnexpectedEOF
	}
	if err := flush(); err != nil {
		return err
	}
	t.band = pipeline.NewBand(proto, image.Rect(0, 0, dw, t.n))
	return nil
}

func (t *turned) Next() (image.Image, error) {
	if t.sc == nil {
		if err := t.load(); err != nil {
			return nil, err
		}
	}
	if t.y >= t.rect.Dy() {
		return nil, io.EOF
	}
	y1 := min(t.y+t.n, t.rect.Dy())
	out := pipeline.Relabel(t.band, image.Rect(0, t.y, t.rect.Dx(), y1))
	pix, stride, bpp, _, _ := pipeline.Pix(out)
	rowBytes := t.rect.Dx() * bpp
	if stride == rowBytes {
		if _, err := t.sc.ReadAt(pix[:(y1-t.y)*rowBytes], int64(t.y)*int64(rowBytes)); err != nil {
			return nil, err
		}
	} else {
		for y := t.y; y < y1; y++ {
			if _, err := t.sc.ReadAt(pix[(y-t.y)*stride:][:rowBytes], int64(y)*int64(rowBytes)); err != nil {
				return nil, err
			}
		}
	}
	t.y = y1
	return out, nil
}
