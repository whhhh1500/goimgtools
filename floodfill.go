package goimgtools

import (
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// FloodFillOptions are the optional settings of FloodFill.
type FloodFillOptions struct {
	// Threshold is how far a pixel may be from the colour of the starting
	// point and still be filled: the sum of the differences of its red,
	// green, blue and alpha, 0 (the same colour only) to 1020, as
	// Pillow's thresh.
	Threshold int
	// Border, when set, changes what is filled: every pixel reached from
	// the starting point without crossing this colour (or the fill
	// colour), whatever its own, as Pillow's border.
	Border color.Color
}

// FloodFill paints with colour c the area of one colour around the point
// (x, y): the pixels joined to it side by side (not corner to corner) whose
// colour is that of the point, or within a threshold of it, as Pillow's
// ImageDraw.floodfill fills them, to the pixel. The colour replaces what
// was there (its alpha too). A point that already has the colour (within
// the threshold) changes nothing.
//
// The area can be anywhere, so the image is kept in working storage (a
// temporary file beyond its share of the memory limit) and two bits a
// pixel in memory: 6MB for 24 megapixels.
func (m *Image) FloodFill(x, y int, c color.Color, opts ...FloodFillOptions) *Image {
	var o FloodFillOptions
	if len(opts) > 1 {
		return m.fail(errors.New("goimgtools: FloodFill takes one FloodFillOptions"))
	}
	if len(opts) == 1 {
		o = opts[0]
	}
	if c == nil {
		return m.fail(errors.New("goimgtools: FloodFill needs a colour"))
	}
	if x < 0 || y < 0 {
		return m.fail(errors.New("goimgtools: FloodFill starts at a point of the image"))
	}
	if o.Threshold < 0 || o.Threshold > 4*255 {
		return m.fail(errors.New("goimgtools: FloodFill's threshold is 0 to 1020"))
	}
	op := floodOp{x: x, y: y, c: color.NRGBAModel.Convert(c).(color.NRGBA), thresh: o.Threshold}
	if o.Border != nil {
		b := color.NRGBAModel.Convert(o.Border).(color.NRGBA)
		op.border = &b
	}
	return m.add(op)
}

type floodOp struct {
	x, y   int
	c      color.NRGBA
	thresh int
	border *color.NRGBA
}

func (o floodOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	b := src.Bounds()
	if o.x >= b.Dx() || o.y >= b.Dy() {
		return nil, errors.New("goimgtools: FloodFill starts at a point of the image")
	}
	if r.sizeOnly {
		return src, nil
	}
	opaque := pipeline.IsOpaque(src) && o.c.A == 0xff
	return &flooded{src: pipeline.Map(src, func(*image.NRGBA) error { return nil }), env: pipeline.EnvOf(src), b: b, o: o, opaque: opaque}, nil
}

// flooded is the image with the area filled: the whole of it is read into
// working storage, the area found, and the rows handed out from there.
type flooded struct {
	src    pipeline.Source
	env    *pipeline.Env
	b      image.Rectangle
	o      floodOp
	opaque bool

	sc       *pipeline.Scratch
	fill     []uint8 // a bit for each pixel: it is filled
	strip    *image.NRGBA
	rows     int
	y        int
	reserved int64
	loaded   bool
}

func (f *flooded) Env() *pipeline.Env      { return f.env }
func (f *flooded) Bounds() image.Rectangle { return f.b }
func (f *flooded) ColorModel() color.Model { return color.NRGBAModel }
func (f *flooded) Opaque() bool            { return f.opaque }
func (f *flooded) Meta() pipeline.Meta     { return pipeline.MetaOf(f.src) }

func (f *flooded) Close() error {
	if f.sc != nil {
		f.sc.Close()
		f.sc = nil
	}
	f.env.Budget.Release(f.reserved)
	f.reserved, f.fill, f.strip = 0, nil, nil
	return f.src.Close()
}

// load reads the image into working storage and finds the area.
func (f *flooded) load() error {
	w, h := f.b.Dx(), f.b.Dy()
	rowBytes := 4 * int64(w)
	bitRow := (w + 7) / 8
	f.rows = pipeline.StripRows(rowBytes, h)
	need := 2*int64(bitRow)*int64(h) + rowBytes*int64(f.rows)
	if err := f.env.Budget.Reserve(need, "flood fill"); err != nil {
		return err
	}
	f.reserved = need
	var err error
	if f.sc, err = pipeline.NewScratch(f.env, rowBytes*int64(h)); err != nil {
		return err
	}
	y := 0
	for {
		s, err := f.src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		n := s.(*image.NRGBA)
		for sy := n.Rect.Min.Y; sy < n.Rect.Max.Y; sy++ {
			if y >= h {
				return io.ErrUnexpectedEOF
			}
			if _, err := f.sc.WriteAt(n.Pix[n.PixOffset(n.Rect.Min.X, sy):][:rowBytes], int64(y)*rowBytes); err != nil {
				return err
			}
			y++
		}
	}
	if y != h {
		return io.ErrUnexpectedEOF
	}
	f.strip = image.NewNRGBA(image.Rect(0, 0, w, f.rows))
	f.fill = make([]uint8, bitRow*h)
	f.loaded = true

	// The colour at the starting point.
	row := f.strip.Pix[:rowBytes]
	if _, err := f.sc.ReadAt(row, int64(f.o.y)*rowBytes); err != nil {
		return err
	}
	bg := [4]uint8(row[4*f.o.x:])
	c := [4]uint8{f.o.c.R, f.o.c.G, f.o.c.B, f.o.c.A}
	diff := func(p, q [4]uint8) (d int) {
		for i := range p {
			d += max(int(p[i]), int(q[i])) - min(int(p[i]), int(q[i]))
		}
		return d
	}
	if diff(c, bg) <= f.o.thresh {
		return nil // it has the colour already
	}
	// may: a bit for each pixel that is filled when the area reaches it.
	may := make([]uint8, bitRow*h)
	for y0 := 0; y0 < h; y0 += f.rows {
		n := min(f.rows, h-y0)
		buf := f.strip.Pix[:int64(n)*rowBytes]
		if _, err := f.sc.ReadAt(buf, int64(y0)*rowBytes); err != nil {
			return err
		}
		for j := 0; j < n; j++ {
			px := buf[int64(j)*rowBytes:][:rowBytes]
			bits := may[(y0+j)*bitRow:][:bitRow]
			for x := 0; x < w; x++ {
				p := [4]uint8(px[4*x:])
				ok := false
				if f.o.border == nil {
					ok = diff(p, bg) <= f.o.thresh
				} else {
					bd := *f.o.border
					ok = p != c && p != [4]uint8{bd.R, bd.G, bd.B, bd.A}
				}
				if ok {
					bits[x>>3] |= 1 << (x & 7)
				}
			}
		}
	}
	may[f.o.y*bitRow+f.o.x>>3] |= 1 << (f.o.x & 7) // the starting point always is
	f.fill[f.o.y*bitRow+f.o.x>>3] |= 1 << (f.o.x & 7)

	// grow lets the area into row r from the rows beside it and along
	// the row, and tells whether the row changed.
	grow := func(r int) bool {
		cur, can := f.fill[r*bitRow:][:bitRow], may[r*bitRow:][:bitRow]
		changed := false
		for i := range cur {
			v := cur[i]
			if r > 0 {
				v |= f.fill[(r-1)*bitRow+i]
			}
			if r < h-1 {
				v |= f.fill[(r+1)*bitRow+i]
			}
			if v &= can[i]; v != cur[i] {
				cur[i], changed = v, true
			}
		}
		// Along the row: each run of pixels that may be filled is, once
		// one of them is.
		for x := 0; x < w; {
			if can[x>>3] == 0 {
				x = (x | 7) + 1
				continue
			}
			if can[x>>3]>>(x&7)&1 == 0 {
				x++
				continue
			}
			start, any, all := x, false, true
			for ; x < w && can[x>>3]>>(x&7)&1 != 0; x++ {
				if cur[x>>3]>>(x&7)&1 != 0 {
					any = true
				} else {
					all = false
				}
			}
			if any && !all {
				for i := start; i < x; i++ {
					cur[i>>3] |= 1 << (i & 7)
				}
				changed = true
			}
		}
		return changed
	}
	// Down and up over the rows the area has reached, and on while it
	// spreads, until nothing changes.
	lo, hi := f.o.y, f.o.y
	for again := true; again; {
		again = false
		for r := lo; r < h; r++ {
			if grow(r) {
				again, hi = true, max(hi, r)
			} else if r > hi {
				break
			}
		}
		for r := hi; r >= 0; r-- {
			if grow(r) {
				again, lo = true, min(lo, r)
			} else if r < lo {
				break
			}
		}
	}
	return nil
}

func (f *flooded) Next() (image.Image, error) {
	if !f.loaded {
		if err := f.load(); err != nil {
			return nil, err
		}
	}
	w, h := f.b.Dx(), f.b.Dy()
	if f.y >= h {
		return nil, io.EOF
	}
	n := min(f.rows, h-f.y)
	rowBytes := 4 * int64(w)
	buf := f.strip.Pix[:int64(n)*rowBytes]
	if _, err := f.sc.ReadAt(buf, int64(f.y)*rowBytes); err != nil {
		return nil, err
	}
	bitRow := (w + 7) / 8
	c := [4]uint8{f.o.c.R, f.o.c.G, f.o.c.B, f.o.c.A}
	for j := 0; j < n; j++ {
		bits := f.fill[(f.y+j)*bitRow:][:bitRow]
		px := buf[int64(j)*rowBytes:][:rowBytes]
		for i, v := range bits {
			if v == 0 {
				continue
			}
			for k := 0; k < 8 && 8*i+k < w; k++ {
				if v>>k&1 != 0 {
					copy(px[4*(8*i+k):], c[:])
				}
			}
		}
	}
	out := &image.NRGBA{Pix: buf, Stride: int(rowBytes), Rect: image.Rect(f.b.Min.X, f.b.Min.Y+f.y, f.b.Max.X, f.b.Min.Y+f.y+n)}
	f.y += n
	return out, nil
}
