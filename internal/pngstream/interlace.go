package pngstream

import (
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Adam7 interlacing stores the image as seven reduced images, one after the
// other, so no row of the picture is complete until the last pass. The
// standard library decodes each pass into its own image and merges them into
// a full-size one. Here every pass is decoded row by row with the same code
// (readRow) and its pixel rows are appended to a pipeline.Spill, which is a
// temporary file when the image does not fit in the memory budget. Output
// row y is then assembled from the passes that hold pixels of that row, each
// read front to back.

const adam7ReadBuf = 16 << 10

type adam7 struct {
	spill  *pipeline.Spill
	rowImg image.Image // one row, the type of the output strips
	pix    []uint8     // rowImg's pixel bytes
	bpp    int         // bytes per pixel in pix
	pw     [7]int      // pass widths in pixels; 0 for an empty pass
	rd     [7]*pipeline.SeqReader
	buf    []uint8
	crFull []uint8
	prFull []uint8
	loaded bool
}

// adam7Extra is the extra working memory an interlaced image needs: a row
// image, a row buffer and the seven read buffers.
func adam7Extra(proto image.Image, width int) int64 {
	return 2*pipeline.BandBytes(proto, image.Rect(0, 0, width, 1)) + 7*adam7ReadBuf
}

func pixOf(m image.Image) ([]uint8, int) {
	switch p := m.(type) {
	case *image.Gray:
		return p.Pix, 1
	case *image.Gray16:
		return p.Pix, 2
	case *image.RGBA:
		return p.Pix, 4
	case *image.RGBA64:
		return p.Pix, 8
	case *image.NRGBA:
		return p.Pix, 4
	case *image.NRGBA64:
		return p.Pix, 8
	case *image.Paletted:
		return p.Pix, 1
	}
	return nil, 0
}

func newAdam7(s *stream, proto image.Image) *adam7 {
	a := &adam7{spill: pipeline.NewSpill(s.env), crFull: s.cr, prFull: s.pr}
	a.rowImg = s.newImage(image.Rect(0, 0, s.d.width, 1))
	a.pix, a.bpp = pixOf(a.rowImg)
	a.buf = make([]uint8, s.d.width*a.bpp)
	return a
}

// load decodes all seven passes into the spill and runs the end-of-stream
// checks, so the image is known to be valid before any strip is returned.
func (a *adam7) load(s *stream) error {
	d := s.d
	bits := s.bitsPerPixel()
	var offs [8]int64
	// The seven passes together hold every pixel once.
	a.spill.Expect(int64(d.width) * int64(d.height) * int64(a.bpp))
	for pass, p := range interlacing {
		offs[pass] = a.spill.Size()
		// Add the multiplication factor and subtract one, effectively rounding up.
		pw := (d.width - p.xOffset + p.xFactor - 1) / p.xFactor
		ph := (d.height - p.yOffset + p.yFactor - 1) / p.yFactor
		// A pass with zero width or height has no data, not even filter bytes.
		if pw == 0 || ph == 0 {
			continue
		}
		a.pw[pass] = pw
		rowSize := 1 + (bits*pw+7)/8
		s.rw = pw
		s.cr, s.pr = a.crFull[:rowSize], a.prFull[:rowSize]
		clear(s.cr)
		clear(s.pr)
		for y := 0; y < ph; y++ {
			if err := s.readRow(a.rowImg, 0); err != nil {
				return err
			}
			if _, err := a.spill.Write(a.pix[:pw*a.bpp]); err != nil {
				return err
			}
		}
	}
	offs[7] = a.spill.Size()
	if err := s.finish(); err != nil {
		return err
	}
	for pass := range a.rd {
		if a.pw[pass] != 0 {
			a.rd[pass] = pipeline.NewSeqReader(a.spill, offs[pass], offs[pass+1], adam7ReadBuf)
		}
	}
	// A paletted image's palette can grow while decoding (indexes past the
	// PLTE chunk); every strip carries the final palette, as the standard
	// library's merged image does.
	if p, ok := a.rowImg.(*image.Paletted); ok {
		s.band.(*image.Paletted).Palette = p.Palette
	}
	a.loaded = true
	return nil
}

// next fills strip (rows y0 to y1) from the stored passes.
func (a *adam7) next(s *stream, strip image.Image, y0, y1 int) error {
	if !a.loaded {
		if err := a.load(s); err != nil {
			return err
		}
	}
	if p, ok := strip.(*image.Paletted); ok {
		p.Palette = s.band.(*image.Paletted).Palette
	}
	dst, bpp := pixOf(strip)
	stride := s.d.width * bpp
	switch m := strip.(type) {
	case *image.Gray:
		stride = m.Stride
	case *image.Gray16:
		stride = m.Stride
	case *image.RGBA:
		stride = m.Stride
	case *image.RGBA64:
		stride = m.Stride
	case *image.NRGBA:
		stride = m.Stride
	case *image.NRGBA64:
		stride = m.Stride
	case *image.Paletted:
		stride = m.Stride
	}
	for y := y0; y < y1; y++ {
		row := dst[(y-y0)*stride:]
		for pass, p := range interlacing {
			pw := a.pw[pass]
			if pw == 0 || y < p.yOffset || (y-p.yOffset)%p.yFactor != 0 {
				continue
			}
			src := a.buf[:pw*bpp]
			if err := a.rd[pass].ReadFull(src); err != nil {
				return err
			}
			o, step := p.xOffset*bpp, p.xFactor*bpp
			for i := 0; i < len(src); i += bpp {
				copy(row[o:o+bpp], src[i:i+bpp])
				o += step
			}
		}
	}
	return nil
}
