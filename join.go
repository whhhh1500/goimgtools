package goimgtools

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// JoinOptions arrange the images of Join.
type JoinOptions struct {
	// Across is how many images a row of the grid holds; 0 puts them all
	// in one row.
	Across int
	// Gap is the space between the images, in pixels (sharp's shim).
	Gap int
	// Background fills the gaps and the room an image smaller than its
	// cell leaves; nil leaves them transparent.
	Background color.Color
	// Align is where an image smaller than its cell sits in it: the
	// top-left corner by default (DefaultPosition), or another of the nine
	// positions (Center, SouthEast, ...).
	Align Position
}

// Join lays images out in a grid, as sharp's join does (and ImageMagick's
// montage): left to right, Across to a row, each in a cell as large as the
// largest of them (they are not resized: Resize them first for a grid of
// thumbnails). Each image is read through its own recipe, a few rows at a
// time, while its row of the grid is written, so memory holds a strip of
// each image of one row, whatever their size or number; an image that can
// be read only once (FromReader) is opened at the start, to learn its
// size, and held until its row. Without an output format the result is
// written as PNG.
func Join(images []*Image, opts ...JoinOptions) *Image {
	if len(opts) > 1 {
		return &Image{err: errors.New("goimgtools: Join takes at most one JoinOptions")}
	}
	var o JoinOptions
	if len(opts) == 1 {
		o = opts[0]
	}
	switch {
	case len(images) == 0:
		return &Image{err: errors.New("goimgtools: Join needs images")}
	case o.Across < 0 || o.Gap < 0:
		return &Image{err: errors.New("goimgtools: JoinOptions Across and Gap must not be negative")}
	case o.Align != DefaultPosition && (o.Align < NorthWest || o.Align > SouthEast):
		return &Image{err: errors.New("goimgtools: JoinOptions Align must be one of the nine positions")}
	}
	for _, m := range images {
		if m == nil {
			return &Image{err: errors.New("goimgtools: Join was given a nil image")}
		}
	}
	imgs := append([]*Image(nil), images...)
	return &Image{open: func() (*input, error) {
		return &input{kind: "join", gen: func(env *pipeline.Env) (pipeline.Source, error) {
			return newGrid(imgs, o, env)
		}}, nil
	}}
}

// gridSource gives the rows of the grid.
type gridSource struct {
	env        *pipeline.Env
	imgs       []*Image
	sizes      []image.Point
	o          JoinOptions
	bg         color.NRGBA
	cols, rows int
	cw, ch     int // the cell
	b          image.Rectangle
	y          int
	strip      *image.NRGBA
	held       int64
	// The images of the grid row being written, opened when it starts.
	gridRow int
	open    []*gridCell
	// The images that can be read only once (FromReader), opened at the
	// start to learn their size, until their row.
	early  map[int]*gridCell
	closed bool
}

// gridCell is an image of the grid row being written.
type gridCell struct {
	src    pipeline.Source
	closer io.Closer
	cur    image.Image // its strip holding the rows being read
	x, y   int         // where it sits in its cell
	w, h   int
}

func newGrid(imgs []*Image, o JoinOptions, env *pipeline.Env) (*gridSource, error) {
	j := &gridSource{env: env, imgs: imgs, o: o, gridRow: -1}
	ok := false
	defer func() {
		if !ok {
			j.closeEarly()
		}
	}()
	for i, m := range imgs {
		var w, h int
		if m.once {
			src, closer, _, _, err := m.build(env, false)
			if err != nil {
				return nil, fmt.Errorf("goimgtools: Join: image %d: %w", i, err)
			}
			if j.early == nil {
				j.early = map[int]*gridCell{}
			}
			j.early[i] = &gridCell{src: src, closer: closer}
			w, h = src.Bounds().Dx(), src.Bounds().Dy()
		} else {
			var err error
			if w, h, err = m.OutputSize(); err != nil {
				return nil, fmt.Errorf("goimgtools: Join: image %d: %w", i, err)
			}
		}
		j.sizes = append(j.sizes, image.Pt(w, h))
		j.cw, j.ch = max(j.cw, w), max(j.ch, h)
	}
	j.cols = len(imgs)
	if o.Across > 0 {
		j.cols = min(o.Across, len(imgs))
	}
	j.rows = (len(imgs) + j.cols - 1) / j.cols
	w := int64(j.cols)*int64(j.cw) + int64(j.cols-1)*int64(o.Gap)
	h := int64(j.rows)*int64(j.ch) + int64(j.rows-1)*int64(o.Gap)
	if w > 1<<30 || h > 1<<30 {
		return nil, fmt.Errorf("%w: a grid of %dx%d", ErrPixelLimit, w, h)
	}
	j.b = image.Rect(0, 0, int(w), int(h))
	if o.Background != nil {
		j.bg = color.NRGBAModel.Convert(o.Background).(color.NRGBA)
	}
	rows := pipeline.StripRows(4*w, int(h))
	j.held = 4 * w * int64(rows)
	if err := env.Budget.Reserve(j.held, "join strip"); err != nil {
		return nil, err
	}
	j.strip = image.NewNRGBA(image.Rect(0, 0, int(w), rows))
	ok = true
	return j, nil
}

func (j *gridSource) Bounds() image.Rectangle { return j.b }
func (j *gridSource) ColorModel() color.Model { return color.NRGBAModel }
func (j *gridSource) Env() *pipeline.Env      { return j.env }

// startRow opens the images of grid row r, closing those of the row before.
func (j *gridSource) startRow(r int) error {
	j.closeCells()
	j.gridRow = r
	cx, cy := j.o.Align.gridOr(0, 0)
	for c := 0; c < j.cols; c++ {
		i := r*j.cols + c
		if i >= len(j.imgs) {
			break
		}
		var src pipeline.Source
		var closer io.Closer
		if c := j.early[i]; c != nil {
			src, closer = c.src, c.closer
			delete(j.early, i)
		} else {
			var err error
			if src, closer, _, _, err = j.imgs[i].build(j.env, false); err != nil {
				return fmt.Errorf("goimgtools: Join: image %d: %w", i, err)
			}
		}
		sz := j.sizes[i]
		if sb := src.Bounds(); sb.Size() != sz {
			src.Close()
			closer.Close()
			return fmt.Errorf("goimgtools: Join: image %d came out %v, not %v", i, sb.Size(), sz)
		}
		j.open = append(j.open, &gridCell{src: src, closer: closer, w: sz.X, h: sz.Y,
			x: c*(j.cw+j.o.Gap) + cx*(j.cw-sz.X)/2, y: cy * (j.ch - sz.Y) / 2})
	}
	return nil
}

// copyRow copies row y (from its top) of a cell's image into dst as
// non-premultiplied RGBA; rows come in order.
func (c *gridCell) copyRow(dst []uint8, y int) error {
	b := c.src.Bounds()
	abs := b.Min.Y + y
	for c.cur == nil || abs >= c.cur.Bounds().Max.Y {
		s, err := c.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		c.cur = s
	}
	dst = dst[:4*c.w]
	switch s := c.cur.(type) {
	case *image.NRGBA:
		copy(dst, s.Pix[s.PixOffset(b.Min.X, abs):])
	case *image.Gray:
		for x, v := range s.Pix[s.PixOffset(b.Min.X, abs):][:c.w] {
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = v, v, v, 255
		}
	case *image.YCbCr:
		for x := 0; x < c.w; x++ {
			yi, ci := s.YOffset(b.Min.X+x, abs), s.COffset(b.Min.X+x, abs)
			r, g, bl := color.YCbCrToRGB(s.Y[yi], s.Cb[ci], s.Cr[ci])
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = r, g, bl, 255
		}
	default:
		for x := 0; x < c.w; x++ {
			p := color.NRGBAModel.Convert(s.At(b.Min.X+x, abs)).(color.NRGBA)
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = p.R, p.G, p.B, p.A
		}
	}
	return nil
}
func (j *gridSource) Next() (image.Image, error) {
	if j.closed {
		return nil, errors.New("goimgtools: Join: read after Close")
	}
	if j.y >= j.b.Max.Y {
		return nil, io.EOF
	}
	n := min(j.strip.Rect.Dy(), j.b.Max.Y-j.y)
	out := &image.NRGBA{Pix: j.strip.Pix[:4*j.b.Dx()*n], Stride: j.strip.Stride, Rect: image.Rect(0, j.y, j.b.Dx(), j.y+n)}
	bg := [4]uint8{j.bg.R, j.bg.G, j.bg.B, j.bg.A}
	for k := 0; k < n; k++ {
		y := j.y + k
		row := out.Pix[k*out.Stride:][:4*j.b.Dx()]
		for x := 0; x < len(row); x += 4 {
			copy(row[x:x+4], bg[:])
		}
		r, cy := y/(j.ch+j.o.Gap), y%(j.ch+j.o.Gap)
		if cy >= j.ch {
			continue // a gap between rows
		}
		if r != j.gridRow {
			if err := j.startRow(r); err != nil {
				return nil, err
			}
		}
		for _, c := range j.open {
			iy := cy - c.y
			if iy < 0 || iy >= c.h {
				continue
			}
			if err := c.copyRow(row[4*c.x:], iy); err != nil {
				return nil, err
			}
		}
	}
	j.y += n
	return out, nil
}

// closeCells closes the images of the grid row being written.
func (j *gridSource) closeCells() {
	for _, c := range j.open {
		c.src.Close()
		c.closer.Close()
	}
	j.open = nil
}

// closeEarly closes the images opened early whose row never came.
func (j *gridSource) closeEarly() {
	for i, c := range j.early {
		c.src.Close()
		c.closer.Close()
		delete(j.early, i)
	}
}

func (j *gridSource) Close() error {
	if !j.closed {
		j.closed = true
		j.closeCells()
		j.closeEarly()
		j.env.Budget.Release(j.held)
	}
	return nil
}
