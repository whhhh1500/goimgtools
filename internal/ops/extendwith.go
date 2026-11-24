package ops

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// How ExtendWith fills a border from the image, as libvips'
// VIPS_EXTEND_COPY, REPEAT and MIRROR.
const (
	ExtendCopy   = iota // the edge pixels repeated
	ExtendRepeat        // the image repeated
	ExtendMirror        // the image mirrored, the edge pixel taken twice
)

// ExtendWith adds borders of left, top, right and bottom pixels around src
// filled from src itself (see ExtendCopy, ExtendRepeat, ExtendMirror),
// as sharp's extend with extendWith. Pixels are copied as they are: an
// opaque greyscale source stays greyscale. A border at the top or bottom
// that repeats or mirrors the image needs rows again after later ones: a
// mirrored border no taller than the image needs those of the border only,
// and so does the image repeated below it; they are kept in a store as
// they are read. Otherwise (the image repeated above it, a border taller
// than the image) the whole image is. The store is a temporary file when
// the image is beyond its share of the limit, of the rows kept only.
func ExtendWith(src pipeline.Source, left, top, right, bottom, how int) (pipeline.Source, error) {
	if left < 0 || top < 0 || right < 0 || bottom < 0 {
		return nil, errors.New("goimgtools: a border cannot be negative")
	}
	if how < ExtendCopy || how > ExtendMirror {
		return nil, errors.New("goimgtools: unknown way to extend")
	}
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	w, h := sw+left+right, sh+top+bottom
	if w > 1<<24 || h > 1<<24 {
		return nil, errors.New("goimgtools: border makes the image too large")
	}
	if sw < 1 || sh < 1 {
		return nil, errors.New("goimgtools: nothing to extend")
	}
	env := pipeline.EnvOf(src)
	e := &extendFrom{src: src, env: env, rect: image.Rect(0, 0, w, h), top: top, how: how, last: -1,
		opaque: pipeline.IsOpaque(src), bpp: 4}
	if e.opaque && src.ColorModel() == color.GrayModel {
		e.bpp = 1
	} else {
		// NRGBA strips, made as every stage makes them.
		e.src = pipeline.Map(src, func(*image.NRGBA) error { return nil })
	}
	e.n = pipeline.StripRows(int64(w)*int64(e.bpp), h)
	// The source column of each column, a source row, and a strip.
	need := int64(w)*4 + int64(sw)*int64(e.bpp) + int64(w)*int64(e.n)*int64(e.bpp)
	if err := env.Budget.Reserve(need, "extend rows"); err != nil {
		return nil, err
	}
	e.reserved = need
	e.cols = make([]int32, w)
	for x := range e.cols {
		e.cols[x] = int32(edgeIndex(x-left, sw, how))
	}
	e.row = make([]uint8, sw*e.bpp)
	e.buf = make([]uint8, w*e.n*e.bpp)
	switch {
	case how == ExtendCopy || top == 0 && bottom == 0:
	case how == ExtendMirror && top <= sh && bottom <= sh:
		// Rows top-1 to 0, the image, rows sh-1 to sh-bottom.
		e.edges, e.headN, e.tail0 = true, top, max(top, sh-bottom)
	case how == ExtendRepeat && top == 0 && bottom <= sh:
		// The image, then its rows 0 to bottom-1.
		e.edges, e.headN, e.tail0 = true, bottom, sh
	default:
		e.held = true
	}
	if !e.held {
		e.rows = pipeline.NewRowReader(e.src)
	}
	return e, nil
}

// edgeIndex is the index within 0..n-1 that i (perhaps outside) shows.
func edgeIndex(i, n, how int) int {
	switch how {
	case ExtendCopy:
		return max(0, min(i, n-1))
	case ExtendRepeat:
		if i %= n; i < 0 {
			i += n
		}
		return i
	}
	if i %= 2 * n; i < 0 {
		i += 2 * n
	}
	if i >= n {
		i = 2*n - 1 - i
	}
	return i
}

type extendFrom struct {
	src      pipeline.Source
	env      *pipeline.Env
	rect     image.Rectangle
	top, how int
	bpp      int // 1 for an opaque greyscale source, else 4 (NRGBA)
	opaque   bool
	cols     []int32
	n, y     int
	// A source row as bytes, and which it is.
	row  []uint8
	last int
	buf  []uint8
	// The source is read in order (rows). Where rows are needed again after
	// later ones, either those are kept in a store as they are read (edges:
	// the first headN rows and the rows from tail0 on; read is how many
	// rows have been read), or the whole image is held in one before
	// anything else (held). The store, sc, is made when the first strip is
	// asked for.
	rows               *pipeline.RowReader
	held, edges        bool
	headN, tail0, read int
	sc                 *pipeline.Scratch
	err                error // of reading the source, or of the store
	reserved           int64
}

func (e *extendFrom) Env() *pipeline.Env      { return e.env }
func (e *extendFrom) Bounds() image.Rectangle { return e.rect }
func (e *extendFrom) Opaque() bool            { return e.opaque }

func (e *extendFrom) ColorModel() color.Model {
	if e.bpp == 1 {
		return color.GrayModel
	}
	return color.NRGBAModel
}

func (e *extendFrom) Close() error {
	e.env.Budget.Release(e.reserved)
	e.reserved = 0
	if e.sc != nil {
		e.sc.Close()
		e.sc = nil
	}
	return e.src.Close()
}

// readRow puts row y of strip s (in the source's coordinates) into dst as
// bytes: grey levels, or NRGBA (as the strips are).
func (e *extendFrom) readRow(s image.Image, y int, dst []uint8) {
	sb := e.src.Bounds()
	switch m := s.(type) {
	case *image.NRGBA:
		copy(dst, m.Pix[m.PixOffset(sb.Min.X, y):])
	case *image.Gray:
		copy(dst, m.Pix[m.PixOffset(sb.Min.X, y):])
	default:
		r := image.Rect(0, 0, sb.Dx(), 1)
		draw.Draw(&image.Gray{Pix: dst, Stride: len(dst), Rect: r}, r, s, image.Pt(sb.Min.X, y), draw.Src)
	}
}

// store reads the whole source into the store, row after row.
func (e *extendFrom) store() error {
	sb := e.src.Bounds()
	sc, err := pipeline.NewScratch(e.env, int64(sb.Dx())*int64(sb.Dy())*int64(e.bpp))
	if err != nil {
		return err
	}
	e.sc = sc
	rows := pipeline.NewRowReader(e.src)
	for y := sb.Min.Y; y < sb.Max.Y; y++ {
		s, err := rows.Row(y)
		if err != nil {
			return err
		}
		e.readRow(s, y, e.row)
		if _, err := e.sc.WriteAt(e.row, int64(y-sb.Min.Y)*int64(len(e.row))); err != nil {
			return err
		}
	}
	e.last = -1
	return rows.Drain()
}

// keptAt is the place in the store of source row sy where only the rows
// needed again are kept, -1 for a row that is not.
func (e *extendFrom) keptAt(sy int) int {
	switch {
	case sy < e.headN:
		return sy
	case sy >= e.tail0:
		return e.headN + sy - e.tail0
	}
	return -1
}

// edgeRow makes e.row source row sy where only the rows needed again are
// kept: the next rows are read up to it, each put in the store if it is
// kept; a row read before comes from the store.
func (e *extendFrom) edgeRow(sy int) error {
	n := int64(len(e.row))
	if e.read <= sy {
		for e.read <= sy {
			y := e.src.Bounds().Min.Y + e.read
			s, err := e.rows.Row(y)
			if err != nil {
				return err
			}
			e.readRow(s, y, e.row)
			if k := e.keptAt(e.read); k >= 0 {
				if _, err := e.sc.WriteAt(e.row, int64(k)*n); err != nil {
					return err
				}
			}
			e.read++
		}
		return nil
	}
	k := e.keptAt(sy)
	if k < 0 {
		return fmt.Errorf("goimgtools: extend: row %d is needed again and was not kept", sy)
	}
	_, err := e.sc.ReadAt(e.row, int64(k)*n)
	return err
}

// sourceRow makes e.row source row sy (from the top, 0 on).
func (e *extendFrom) sourceRow(sy int) error {
	if sy == e.last {
		return nil
	}
	e.last = -1 // e.row is no row until the one asked for is in it
	switch {
	case e.edges:
		if err := e.edgeRow(sy); err != nil {
			return err
		}
	case e.sc != nil:
		if _, err := e.sc.ReadAt(e.row, int64(sy)*int64(len(e.row))); err != nil {
			return err
		}
	default:
		y := e.src.Bounds().Min.Y + sy
		s, err := e.rows.Row(y)
		if err != nil {
			return err
		}
		e.readRow(s, y, e.row)
	}
	e.last = sy
	return nil
}

func (e *extendFrom) Next() (image.Image, error) {
	if e.y >= e.rect.Max.Y {
		return nil, io.EOF
	}
	if e.err != nil {
		return nil, e.err
	}
	if e.held && e.sc == nil {
		if e.err = e.store(); e.err != nil {
			return nil, e.err
		}
	}
	if e.edges && e.sc == nil {
		// The rows kept go where the whole image would have gone: in
		// memory when the image could be staged there, else in a temporary
		// file, so that keeping less never takes more memory than keeping
		// all did. The file is asked for as a store too large to be staged
		// in memory; only the rows kept are written to it.
		n := int64(len(e.row))
		kept, whole := int64(e.headN+e.src.Bounds().Dy()-e.tail0)*n, int64(e.src.Bounds().Dy())*n
		if e.env.Budget.TryReserve(whole) {
			e.env.Budget.Release(whole)
		} else {
			kept = max(kept, e.env.Budget.Limit()/pipeline.StagingShare+1)
		}
		if e.sc, e.err = pipeline.NewScratch(e.env, kept); e.err != nil {
			return nil, e.err
		}
	}
	sh := e.src.Bounds().Dy()
	w := e.rect.Dx()
	r := image.Rect(0, e.y, w, min(e.y+e.n, e.rect.Max.Y))
	pix := e.buf[:r.Dx()*r.Dy()*e.bpp]
	for y := r.Min.Y; y < r.Max.Y; y++ {
		if err := e.sourceRow(edgeIndex(y-e.top, sh, e.how)); err != nil {
			e.err = err // and of every strip asked for after this one
			return nil, err
		}
		d := pix[(y-r.Min.Y)*w*e.bpp:][:w*e.bpp]
		if e.bpp == 1 {
			for x, c := range e.cols {
				d[x] = e.row[c]
			}
			continue
		}
		for x, c := range e.cols {
			copy(d[4*x:4*x+4], e.row[4*c:4*c+4])
		}
	}
	e.y = r.Max.Y
	if e.y == e.rect.Max.Y && e.rows != nil {
		// The final strip: the source is read to its end, so that the
		// decoder has verified its input.
		if err := e.rows.Drain(); err != nil {
			return nil, err
		}
	}
	if e.bpp == 1 {
		return &image.Gray{Pix: pix, Stride: w, Rect: r}, nil
	}
	return &image.NRGBA{Pix: pix, Stride: 4 * w, Rect: r}, nil
}
