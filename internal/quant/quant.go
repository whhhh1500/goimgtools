// Package quant reduces a true-colour image to a paletted one with a palette
// chosen for that image and dithering: the lossy "PNG compression" of
// pngquant and TinyPNG.
//
// The palette can only be chosen once every pixel has been seen, so the
// image is read twice. The first pass stores the pixels in a pipeline.Spill (a
// temporary file when they do not fit in the memory budget) and counts
// colours in a histogram of fixed size; the second replays them row by row.
// Working memory does not grow with the image area.
//
// How it works:
//
//   - statistics: a fixed 2.5MB histogram with exact bin means (hist.go),
//     in which smooth areas count for more than busy ones;
//   - palette: the box with the largest squared error is cut where the cut
//     leaves the least error, until there are enough boxes; the box means
//     are refined with k-means over the histogram and then over a sample
//     of the stored pixels (palette.go, polish);
//   - colour distance: Euclidean over alpha-premultiplied channels and
//     alpha, so fully transparent pixels are one colour; opaque pixels only
//     ever get opaque palette entries (when the palette has room for both
//     kinds);
//   - mapping: an exact nearest-entry search through per-cell candidate
//     lists, and serpentine Floyd-Steinberg error diffusion that is turned
//     down where the image is busy (nearest.go).
//
// The ideas are those described for pngquant / libimagequant (premultiplied
// alpha, error-driven median cut, k-means refinement, weighting by local
// smoothness, a dither map) and for Wu's quantizer (cutting where the
// variance left is least). Only the ideas: the code is written for this
// package and none of theirs was read into it or translated.
package quant

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the quantization parameters.
type Options struct {
	// Colors is the largest palette, 2 to 256. Zero means 256.
	Colors int
	// NoDither turns Floyd-Steinberg error diffusion off: every pixel then
	// simply gets the nearest palette colour. The zero value dithers.
	NoDither bool
}

// Quantize returns src as *image.Paletted strips over a palette of at most
// o.Colors entries chosen for this image. The entries are color.NRGBA, so
// transparency is kept: fully transparent pixels stay fully transparent
// (they share one entry, whatever colour they had), and an image without
// transparency gets a palette without any.
//
// A source that already is paletted with no more than o.Colors entries is
// passed through as it is. An image with no more than o.Colors different
// colours keeps exactly those, undithered. Anything else is reduced to an
// adaptive palette and, unless o.NoDither is set, dithered.
//
// The result does not depend on how src is cut into strips.
//
// Nothing is read until the first Next, which reads all of src. Closing the
// result closes src.
func Quantize(src pipeline.Source, o *Options) (pipeline.Source, error) {
	colors, dither := 256, true
	if o != nil {
		if o.Colors != 0 {
			colors = o.Colors
		}
		dither = !o.NoDither
	}
	if colors < 2 || colors > 256 {
		return nil, fmt.Errorf("goimgtools: palette size must be 2 to 256, not %d", colors)
	}
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return nil, fmt.Errorf("goimgtools: cannot quantize an empty image (%dx%d)", b.Dx(), b.Dy())
	}
	if int64(b.Dx()) > (1<<31-1)/16 {
		return nil, fmt.Errorf("goimgtools: image too wide to quantize (%d)", b.Dx())
	}
	return &quantized{src: src, env: pipeline.EnvOf(src), bounds: b, colors: colors, dither: dither}, nil
}

const (
	stateNew    = iota
	statePass   // source strips are handed on untouched
	stateReplay // pass 2
	stateDone   // io.EOF returned
	stateFailed // err is set
)

const readBufBytes = 64 << 10

type quantized struct {
	src    pipeline.Source
	env    *pipeline.Env
	bounds image.Rectangle
	colors int
	dither bool

	state  int
	err    error
	closed bool

	reserved int64 // everything below, except what spill and mapper account for
	spill    *pipeline.Spill
	rd       *pipeline.SeqReader
	row      []byte   // one NRGBA row
	above    []byte   // dithering: the row before it
	below    []byte   // and the row after it
	act      []uint16 // and how busy the image is at each of its pixels
	pal      color.Palette
	exact    *distinct // set when the image keeps its own colours
	remap    []uint8   // exact: position in distinct.keys to palette index
	m        *mapper
	pix      []uint8 // the output strip
	n        int     // rows per output strip
	y        int     // next output row, from 0
}

func (q *quantized) Env() *pipeline.Env      { return q.env }
func (q *quantized) Bounds() image.Rectangle { return q.bounds }
func (q *quantized) Opaque() bool            { return pipeline.IsOpaque(q.src) }

// ColorModel is the palette once the first Next has chosen it.
func (q *quantized) ColorModel() color.Model {
	switch {
	case q.state == statePass:
		return q.src.ColorModel()
	case q.pal != nil:
		return q.pal
	}
	return color.NRGBAModel
}

func (q *quantized) reserve(n int64, what string) error {
	if err := q.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	q.reserved += n
	return nil
}

// free gives back every buffer and removes the temporary file.
func (q *quantized) free() {
	if q.spill != nil {
		q.spill.Close()
		q.spill = nil
	}
	if q.m != nil {
		q.m.release()
		q.m = nil
	}
	q.env.Budget.Release(q.reserved)
	q.reserved = 0
	q.rd, q.row, q.above, q.below, q.act, q.pix, q.exact, q.remap = nil, nil, nil, nil, nil, nil, nil, nil
}

func (q *quantized) Close() error {
	if q.closed {
		return nil
	}
	q.closed = true
	q.free()
	if q.state != stateFailed {
		q.state, q.err = stateFailed, errors.New("goimgtools: source closed")
	}
	return q.src.Close()
}

func (q *quantized) fail(err error) (image.Image, error) {
	q.free()
	q.state, q.err = stateFailed, err
	return nil, err
}

func (q *quantized) Next() (image.Image, error) {
	switch q.state {
	case stateFailed:
		return nil, q.err
	case stateDone:
		return nil, io.EOF
	case statePass:
		return q.src.Next()
	case stateNew:
		first, err := q.load()
		if err != nil {
			return q.fail(err)
		}
		if q.state == statePass {
			return first, nil
		}
	}
	h := q.bounds.Dy()
	if q.y >= h {
		q.free()
		q.state = stateDone
		return nil, io.EOF
	}
	w := q.bounds.Dx()
	y1 := min(q.y+q.n, h)
	for y := q.y; y < y1; y++ {
		dst := q.pix[(y-q.y)*w : (y-q.y+1)*w]
		if q.act != nil {
			// Dithering looks at the rows above and below: q.row is row y,
			// q.above row y-1, q.below row y+1.
			if y == 0 {
				if err := q.rd.ReadFull(q.row); err != nil {
					return q.fail(err)
				}
			} else {
				q.above, q.row, q.below = q.row, q.below, q.above
			}
			above, below := q.above, q.below
			if y == 0 {
				above = q.row
			}
			if y == h-1 {
				below = q.row
			} else if err := q.rd.ReadFull(below); err != nil {
				return q.fail(err)
			}
			activity(q.act, above, q.row, below)
			q.m.row(q.row, dst, q.act, y)
			continue
		}
		if err := q.rd.ReadFull(q.row); err != nil {
			return q.fail(err)
		}
		if q.exact != nil {
			prev, pi := uint32(0), -1
			for x := range dst {
				key := pixKey(q.row[4*x : 4*x+4])
				if pi < 0 || key != prev {
					prev, pi = key, int(q.remap[q.exact.index(key)])
				}
				dst[x] = uint8(pi)
			}
		} else {
			q.m.row(q.row, dst, nil, y)
		}
	}
	r := image.Rect(q.bounds.Min.X, q.bounds.Min.Y+q.y, q.bounds.Max.X, q.bounds.Min.Y+y1)
	q.y = y1
	return &image.Paletted{Pix: q.pix[:(r.Dy())*w], Stride: w, Rect: r, Palette: q.pal}, nil
}

// load is pass 1: it reads the whole source into the spill, counts colours,
// chooses the palette and prepares pass 2. For a source that needs no work
// it sets statePass and returns the first strip instead.
func (q *quantized) load() (image.Image, error) {
	b := q.bounds
	w, h := b.Dx(), b.Dy()
	var (
		bins       []bin
		d          = &distinct{limit: min(q.colors, distinctMax)}
		e          *exactHist
		exactFreed bool
		y          = b.Min.Y
	)
	for {
		s, err := q.src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if s == nil {
			return nil, errors.New("goimgtools: source returned no strip")
		}
		if bins == nil {
			if p, ok := s.(*image.Paletted); ok && len(p.Palette) <= q.colors {
				q.state = statePass
				return s, nil
			}
			if err := q.reserve(numBins*binBytes+exactBytes, "colour histogram"); err != nil {
				return nil, err
			}
			e = newExactHist()
			if err := q.reserve(int64(w)*4, "quantizer row"); err != nil {
				return nil, err
			}
			bins = make([]bin, numBins)
			q.row = make([]byte, w*4)
			q.spill = pipeline.NewSpill(q.env)
			q.spill.Expect(int64(w) * 4 * int64(h))
		}
		r := s.Bounds()
		if r.Min.X != b.Min.X || r.Max.X != b.Max.X || r.Min.Y != y || r.Max.Y > b.Max.Y || r.Dy() <= 0 {
			return nil, fmt.Errorf("goimgtools: strip %v does not continue image %v at row %d", r, b, y)
		}
		conv := rowFunc(s, b.Min.X)
		for ; y < r.Max.Y; y++ {
			conv(q.row, y)
			addRow(bins, d, e, q.row)
			if _, err := q.spill.Write(q.row); err != nil {
				return nil, err
			}
		}
		if e != nil && e.over && !exactFreed {
			// Too many colours to count exactly: the table is gone.
			exactFreed = true
			q.env.Budget.Release(exactBytes)
			q.reserved -= exactBytes
		}
	}
	if bins == nil || y != b.Max.Y {
		return nil, io.ErrUnexpectedEOF
	}

	var (
		pal         []color.NRGBA
		cent        [][4]float64 // palette colours as coordinates
		solid       int          // how many of them, the first, are for opaque pixels only
		transparent bool
	)
	if !d.over {
		pal = exactPalette(d.keys)
		q.exact = d
		q.remap = make([]uint8, len(pal))
		for i, c := range pal {
			q.remap[d.index(uint32(c.R)|uint32(c.G)<<8|uint32(c.B)<<16|uint32(c.A)<<24)] = uint8(i)
		}
	} else {
		k := q.colors
		transparent = bins[binTransparent].n != 0
		if transparent {
			k--
		}
		var pts []bin
		var opaque int
		if e.over {
			pts, opaque = points(bins)
		} else {
			pts, opaque = exactPoints(bins, e)
		}
		cent, solid = centres(pts, opaque, k)
	}
	// The histogram is not needed any more; what follows gets its room.
	bins, e = nil, nil
	held := int64(numBins * binBytes)
	if !exactFreed {
		held += exactBytes
	}
	q.env.Budget.Release(held)
	q.reserved -= held
	if q.exact == nil {
		if err := q.polish(cent, solid); err != nil {
			return nil, err
		}
		pal = finish(cent, transparent)
	}

	q.pal = make(color.Palette, len(pal))
	for i, c := range pal {
		q.pal[i] = c
	}
	q.n = pipeline.StripRows(int64(w), h)
	need := int64(w)*int64(q.n) + readBufBytes
	if q.exact == nil && q.dither {
		need += 2*errorRowBytes(w) + 10*int64(w)
	}
	if err := q.reserve(need, "quantizer strip"); err != nil {
		return nil, err
	}
	q.pix = make([]uint8, w*q.n)
	q.rd = pipeline.NewSeqReader(q.spill, 0, q.spill.Size(), readBufBytes)
	if q.exact == nil {
		if q.dither {
			q.above, q.below, q.act = make([]byte, 4*w), make([]byte, 4*w), make([]uint16, w)
		}
		q.m = newMapper(pal, w, q.dither, q.env.Budget)
	}
	q.state = stateReplay
	return nil, nil
}

const (
	polishPasses = 3
	polishPixels = 2 << 20 // about how many pixels a pass looks at
	polishPrior  = 16      // weight of the centre a pass starts from
)

// polish moves the centres, which were fitted to the histogram bins, closer
// to the pixels themselves with a few k-means passes over stored rows: all
// of them for an image of up to two megapixels, evenly spaced ones
// otherwise. The bins are 4 to 16 levels wide, and an image of a few close
// shades (a soft gradient) has palette colours closer together than that.
func (q *quantized) polish(cent [][4]float64, solid int) error {
	if len(cent) == 0 {
		return nil
	}
	w, h := q.bounds.Dx(), q.bounds.Dy()
	step := max(1, (w*h+polishPixels-1)/polishPixels)
	ic := make([][4]int32, len(cent))
	wt := make([]float64, len(cent))
	sum := make([][4]float64, len(cent))
	for pass := 0; pass < polishPasses; pass++ {
		for i := range cent {
			for c := 0; c < 4; c++ {
				ic[i][c] = int32(cent[i][c] + 0.5)
			}
		}
		clear(wt)
		clear(sum)
		// Opaque pixels go to the first solid centres, the others to the
		// rest (see centres).
		rest := newSearcher(ic[solid:], q.env.Budget)
		first := rest
		if solid > 0 {
			first = newSearcher(ic[:solid], q.env.Budget)
		}
		done := func() {
			rest.release()
			first.release()
		}
		for y := step / 2; y < h; y += step {
			if _, err := q.spill.ReadAt(q.row, int64(y)*int64(len(q.row))); err != nil {
				done()
				return err
			}
			prev, pi := uint32(0), -1
			var t [4]int32
			for i := 0; i < len(q.row); i += 4 {
				p := q.row[i : i+4 : i+4]
				if p[3] == 0 {
					continue
				}
				if key := pixKey(p); pi < 0 || key != prev {
					t = coords(p[0], p[1], p[2], p[3])
					prev = key
					if p[3] == 255 && solid > 0 {
						pi = first.nearest(t[0], t[1], t[2], t[3])
					} else {
						pi = solid + rest.nearest(t[0], t[1], t[2], t[3])
					}
				}
				n := float64(smoothWeight(q.row, i))
				wt[pi] += n
				a := &sum[pi]
				a[0] += n * float64(t[0])
				a[1] += n * float64(t[1])
				a[2] += n * float64(t[2])
				a[3] += n * float64(t[3])
			}
		}
		done()
		for i := range cent {
			for c := 0; c < 4; c++ {
				cent[i][c] = (sum[i][c] + polishPrior*cent[i][c]) / (wt[i] + polishPrior)
			}
		}
	}
	return nil
}
