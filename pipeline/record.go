package pipeline

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
)

// Recording is a whole image kept in a Spill (a temporary file beyond its
// memory share) so that it can be read more than once: an encoder may have
// to try several settings on the same pixels. It replays exactly the strips
// it was given, so what is made from a replay is what would have been made
// from the source itself.
type Recording struct {
	env    *Env
	spill  *Spill
	bounds image.Rectangle
	model  color.Model
	opaque bool
	proto  image.Image       // the strip type, with the last palette seen
	rects  []image.Rectangle // every strip, in order
	rows   int               // the tallest strip
}

// plane is one plane of a strip: rows of n bytes, stride apart.
type plane struct {
	pix       []uint8
	stride, n int
	rows      int
}

// planes lists the pixel planes of strip m in a fixed order.
func planes(m image.Image) ([]plane, error) {
	r := m.Bounds()
	if pix, stride, bpp, pr, ok := Pix(m); ok {
		off := (r.Min.Y-pr.Min.Y)*stride + (r.Min.X-pr.Min.X)*bpp
		return []plane{{pix[off:], stride, r.Dx() * bpp, r.Dy()}}, nil
	}
	var y *image.YCbCr
	var alpha []plane
	switch p := m.(type) {
	case *image.YCbCr:
		y = p
	case *image.NYCbCrA:
		y = &p.YCbCr
		alpha = []plane{{p.A[p.AOffset(r.Min.X, r.Min.Y):], p.AStride, r.Dx(), r.Dy()}}
	default:
		return nil, fmt.Errorf("goimgtools: cannot record %T strips", m)
	}
	cx0, cx1 := chromaCols(r, y.SubsampleRatio)
	crows := r.Dy()
	switch y.SubsampleRatio {
	case image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio410:
		crows = (r.Max.Y+1)/2 - r.Min.Y/2
	}
	co := y.COffset(r.Min.X, r.Min.Y)
	out := []plane{
		{y.Y[y.YOffset(r.Min.X, r.Min.Y):], y.YStride, r.Dx(), r.Dy()},
		{y.Cb[co:], y.CStride, cx1 - cx0, crows},
		{y.Cr[co:], y.CStride, cx1 - cx0, crows},
	}
	return append(out, alpha...), nil
}

// Record reads src to its end into a Recording. It does not close src.
func Record(src Source) (*Recording, error) {
	env := EnvOf(src)
	r := &Recording{env: env, spill: NewSpill(env), bounds: src.Bounds(), model: src.ColorModel(), opaque: IsOpaque(src)}
	for {
		m, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			r.Close()
			return nil, err
		}
		if r.proto != nil && !sameType(r.proto, m) {
			r.Close()
			return nil, errors.New("goimgtools: source changed pixel type")
		}
		ps, err := planes(m)
		if err != nil {
			r.Close()
			return nil, err
		}
		if r.proto == nil {
			// The first strip says how many bytes a row takes, and with that
			// how large the whole image will be: a large one then goes to
			// disk at once instead of filling its memory share first.
			var n int64
			for _, p := range ps {
				n += int64(p.rows) * int64(p.n)
			}
			r.spill.Expect(n * int64(r.bounds.Dy()) / int64(max(1, m.Bounds().Dy())))
		}
		for _, p := range ps {
			for y := 0; y < p.rows; y++ {
				if _, err := r.spill.Write(p.pix[y*p.stride : y*p.stride+p.n]); err != nil {
					r.Close()
					return nil, err
				}
			}
		}
		r.proto = m // the latest strip carries the latest palette
		r.rects = append(r.rects, m.Bounds())
		r.rows = max(r.rows, m.Bounds().Dy())
	}
	if r.proto == nil {
		r.Close()
		return nil, io.ErrUnexpectedEOF
	}
	return r, nil
}

// Close frees the recording. Sources made from it must not be used after.
func (r *Recording) Close() {
	if r.spill != nil {
		r.spill.Close()
		r.spill = nil
	}
}

// Source returns a new reader of the recorded image, from its first strip.
// Closing it frees only its own strip buffer.
func (r *Recording) Source() (Source, error) {
	// The band is as tall as the tallest strip, plus a row so that a
	// vertically subsampled strip starting on an odd row still fits.
	full := image.Rect(r.bounds.Min.X, 0, r.bounds.Max.X, r.rows+1)
	need := BandBytes(r.proto, full) + recordReadBuf
	if err := r.env.Budget.Reserve(need, "replay strip"); err != nil {
		return nil, err
	}
	return &replay{rec: r, band: NewBand(r.proto, full), reserved: need,
		rd: NewSeqReader(r.spill, 0, r.spill.Size(), recordReadBuf)}, nil
}

const recordReadBuf = 64 << 10

type replay struct {
	rec      *Recording
	band     image.Image
	rd       *SeqReader
	i        int
	reserved int64
}

func (p *replay) Env() *Env               { return p.rec.env }
func (p *replay) Bounds() image.Rectangle { return p.rec.bounds }
func (p *replay) ColorModel() color.Model { return p.rec.model }
func (p *replay) Opaque() bool            { return p.rec.opaque }

func (p *replay) Close() error {
	p.rec.env.Budget.Release(p.reserved)
	p.reserved, p.band = 0, nil
	return nil
}

func (p *replay) Next() (image.Image, error) {
	if p.band == nil {
		return nil, errors.New("goimgtools: replay closed")
	}
	if p.i >= len(p.rec.rects) {
		return nil, io.EOF
	}
	rect := p.rec.rects[p.i]
	p.i++
	// Rows of the band are relabelled by an even amount so that chroma
	// rows keep their pairing.
	base := rect.Min.Y &^ 1
	view := Relabel(p.band, image.Rect(rect.Min.X, base, rect.Max.X, base+p.band.Bounds().Dy())).(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(rect)
	if pal, ok := view.(*image.Paletted); ok {
		pal.Palette = p.rec.proto.(*image.Paletted).Palette
	}
	ps, err := planes(view)
	if err != nil {
		return nil, err
	}
	for _, pl := range ps {
		for y := 0; y < pl.rows; y++ {
			if err := p.rd.ReadFull(pl.pix[y*pl.stride : y*pl.stride+pl.n]); err != nil {
				return nil, err
			}
		}
	}
	return view, nil
}
