package ops

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TrimOptions are the settings of Trim besides the threshold.
type TrimOptions struct {
	// Background, when set, is the colour of the edges (NRGBA); else that
	// of the top-left pixel.
	Background *[4]uint8
	// LineArt compares the pixels as they are; otherwise a 3x3 median of
	// the image is, so that noise does not count, as libvips' find_trim.
	LineArt bool
	// Margin keeps this many pixels around what is found, within the image.
	Margin int
}

// Trim crops away the edges of src that are the colour of its top-left
// pixel (or o.Background), as sharp's trim (libvips' find_trim): rows and
// columns at the edges in which no pixel differs from it by more than
// threshold in any of red, green, blue and alpha (0 to 255, the colours not
// multiplied by alpha; two pixels that are both fully transparent are the
// same), the pixels compared being those of a 3x3 median of the image
// unless o.LineArt. An image that is that colour throughout is left as it
// is.
//
// Which edges go is only known once every pixel has been seen, so unlike
// the other operations Trim reads its input when it is set up: the image
// is recorded in working storage (a temporary file beyond its share of
// the limit) and played back cropped.
func Trim(src pipeline.Source, threshold float64, o TrimOptions) (pipeline.Source, error) {
	if !(threshold >= 0 && threshold <= 255) {
		return nil, errors.New("goimgtools: Trim threshold must be 0 to 255")
	}
	if o.Margin < 0 {
		return nil, errors.New("goimgtools: Trim margin cannot be negative")
	}
	scan := &trimScan{Source: src, limit: int(math.Floor(threshold)), median: !o.LineArt}
	if o.Background != nil {
		scan.bg, scan.haveBG = *o.Background, true
	}
	rec, err := pipeline.Record(scan)
	if err != nil {
		return nil, err
	}
	replay, err := rec.Source()
	if err != nil {
		rec.Close()
		return nil, err
	}
	b := src.Bounds()
	area := b
	if scan.found {
		area = image.Rect(scan.minX, scan.minY, scan.maxX+1, scan.maxY+1)
		area = image.Rect(area.Min.X-o.Margin, area.Min.Y-o.Margin, area.Max.X+o.Margin, area.Max.Y+o.Margin).Intersect(b)
	}
	out, err := Crop(replay, area.Min.X-b.Min.X, area.Min.Y-b.Min.Y, area.Dx(), area.Dy())
	if err != nil {
		replay.Close()
		rec.Close()
		return nil, err
	}
	return &trimmed{Source: out, rec: rec, src: src}, nil
}

// trimmed is the cropped play-back, which owns the recording and the
// source it was made from.
type trimmed struct {
	pipeline.Source
	rec *pipeline.Recording
	src pipeline.Source
}

func (t *trimmed) Opaque() bool { return pipeline.IsOpaque(t.Source) }

func (t *trimmed) Close() error {
	err := t.Source.Close()
	t.rec.Close()
	if serr := t.src.Close(); err == nil {
		err = serr
	}
	return err
}

// trimScan passes the strips of a source on while it finds the smallest
// rectangle that holds every pixel unlike the background. With median, the
// rows looked at are those of the 3x3 median of the image (the edges
// repeated), each made once the row after it has come.
type trimScan struct {
	pipeline.Source
	limit                  int
	bg                     [4]uint8
	haveBG                 bool
	median                 bool
	started, found         bool
	minX, minY, maxX, maxY int
	row                    []uint8
	// With median: the last three rows of the image, the y of the newest,
	// and the median row.
	win  [3][]uint8
	winY int
	mid  []uint8
	minR int // the first row's x
}

func (t *trimScan) Opaque() bool { return pipeline.IsOpaque(t.Source) }

func (t *trimScan) Next() (image.Image, error) {
	s, err := t.Source.Next()
	if err != nil {
		if err == io.EOF && t.median && t.started {
			// The last row: its own neighbour below.
			t.medianRow(t.win[1], t.win[2], t.win[2], t.winY)
		}
		return s, err
	}
	r := s.Bounds()
	w := r.Dx()
	if t.row == nil {
		t.row = make([]uint8, 4*w)
		t.minR = r.Min.X
		if t.median {
			for i := range t.win {
				t.win[i] = make([]uint8, 4*w)
			}
			t.mid = make([]uint8, 4*w)
		}
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		nrgbaRow(t.row, s, y)
		if !t.started {
			t.started = true
			if !t.haveBG {
				copy(t.bg[:], t.row[:4])
			}
			if t.median {
				// The first row: its own neighbour above.
				copy(t.win[1], t.row)
				copy(t.win[2], t.row)
				t.winY = y
				continue
			}
		}
		if t.median {
			// Row y-1 now has both its neighbours.
			t.win[0], t.win[1], t.win[2] = t.win[1], t.win[2], t.win[0]
			copy(t.win[2], t.row)
			t.medianRow(t.win[0], t.win[1], t.win[2], y-1)
			t.winY = y
			continue
		}
		t.look(t.row, r.Min.X, y)
	}
	return s, nil
}

// medianRow looks at the 3x3 median of row y, whose rows above, at and
// below are given.
func (t *trimScan) medianRow(up, at, down []uint8, y int) {
	median3Row([][]uint8{up, at, down}, t.mid, len(at)/4)
	t.look(t.mid, t.minR, y)
}

// look widens the rectangle by what in row y (pixels row, its first at x0)
// differs from the background.
func (t *trimScan) look(row []uint8, x0, y int) {
	w := len(row) / 4
	{
		r := image.Rect(x0, y, x0+w, y+1)
		// Only what can widen the rectangle is looked at: from the left up
		// to its left edge, from the right down to its right edge.
		left, right := w, -1
		stopL, stopR := w, -1
		if t.found {
			stopL, stopR = t.minX-r.Min.X, t.maxX-r.Min.X
		}
		for x := 0; x < stopL; x++ {
			if t.differs(row[4*x : 4*x+4]) {
				left = x
				break
			}
		}
		for x := w - 1; x > stopR && x >= 0; x-- {
			if t.differs(row[4*x : 4*x+4]) {
				right = x
				break
			}
		}
		content := left < w || right >= 0
		if !content && t.found {
			// The row may still hold content between the edges found.
			for x := stopL; x <= stopR; x++ {
				if t.differs(row[4*x : 4*x+4]) {
					content = true
					break
				}
			}
		}
		if !content {
			return
		}
		if !t.found {
			t.found = true
			t.minX, t.maxX, t.minY, t.maxY = r.Min.X+left, r.Min.X+right, y, y
			return
		}
		if left < w {
			t.minX = min(t.minX, r.Min.X+left)
		}
		if right >= 0 {
			t.maxX = max(t.maxX, r.Min.X+right)
		}
		t.maxY = y
	}
}

func (t *trimScan) differs(p []uint8) bool {
	if p[3] == 0 && t.bg[3] == 0 {
		return false
	}
	for c := 0; c < 4; c++ {
		d := int(p[c]) - int(t.bg[c])
		if d > t.limit || -d > t.limit {
			return true
		}
	}
	return false
}

// nrgbaRow writes row y of m as 8-bit colours not multiplied by alpha.
func nrgbaRow(dst []uint8, m image.Image, y int) {
	r := m.Bounds()
	w := r.Dx()
	switch p := m.(type) {
	case *image.NRGBA:
		copy(dst, p.Pix[p.PixOffset(r.Min.X, y):][:4*w])
		return
	case *image.RGBA:
		sp := p.Pix[p.PixOffset(r.Min.X, y):][:4*w]
		for i := 0; i < 4*w; i += 4 {
			a := uint32(sp[i+3])
			switch a {
			case 255:
				copy(dst[i:i+4], sp[i:i+4])
			case 0:
				dst[i], dst[i+1], dst[i+2], dst[i+3] = 0, 0, 0, 0
			default:
				dst[i] = uint8((uint32(sp[i])*255 + a/2) / a)
				dst[i+1] = uint8((uint32(sp[i+1])*255 + a/2) / a)
				dst[i+2] = uint8((uint32(sp[i+2])*255 + a/2) / a)
				dst[i+3] = uint8(a)
			}
		}
		return
	case *image.Gray:
		for x, v := range p.Pix[p.PixOffset(r.Min.X, y):][:w] {
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = v, v, v, 255
		}
		return
	case *image.YCbCr:
		yi := p.YOffset(r.Min.X, y)
		for x := 0; x < w; x++ {
			ci := p.COffset(r.Min.X+x, y)
			dst[4*x], dst[4*x+1], dst[4*x+2] = color.YCbCrToRGB(p.Y[yi+x], p.Cb[ci], p.Cr[ci])
			dst[4*x+3] = 255
		}
		return
	}
	for x := 0; x < w; x++ {
		c := color.NRGBAModel.Convert(m.At(r.Min.X+x, y)).(color.NRGBA)
		dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = c.R, c.G, c.B, c.A
	}
}
