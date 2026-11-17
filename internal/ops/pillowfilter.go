package ops

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// PillowKernel is Pillow's ImageFilter.Kernel (Filter.c's ImagingFilter3x3
// and 5x5), to the byte: for a size × size kernel (3 or 5), each weight
// over scale in single precision, a pixel is offset plus a half plus the
// rows of the kernel times the rows of the window, the first row of the
// kernel on the row size/2 below and the last on the row above (each row's
// products added left to right), in single precision, kept within 0 to
// 255 with the fraction dropped; the size/2 rows and columns at the edges
// stay as they are, and an image smaller than the kernel is left as it is.
// With transparency alpha is filtered too, as Pillow filters every band.
func PillowKernel(src pipeline.Source, size int, kernel []float64, scale, offset float64) (pipeline.Source, error) {
	if (size != 3 && size != 5) || len(kernel) != size*size {
		return nil, errors.New("goimgtools: a Pillow kernel is 3 x 3 or 5 x 5")
	}
	for _, v := range append(kernel[:len(kernel):len(kernel)], scale, offset) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("goimgtools: Convolve takes finite numbers")
		}
	}
	if scale == 0 {
		return nil, errors.New("goimgtools: a Pillow kernel's scale is not 0")
	}
	k := make([]float32, len(kernel))
	for i, v := range kernel {
		k[i] = float32(v) / float32(scale)
	}
	return newKernelStage(src, size, k, float32(offset), !pipeline.IsOpaque(src), 0, false)
}

// kernelStage is PillowKernel, or with blend ImageEnhance.Sharpness: the
// filtered image (SMOOTH, alpha as it is) blended to the image by f.
type kernelStage struct {
	src      pipeline.Source
	env      *pipeline.Env
	w, h, n  int
	size     int
	k        []float32
	offset   float32 // with the half added
	chans    int     // 3, or 4 when alpha is filtered
	blend    bool
	f        float32
	opaque   bool
	gray     bool
	reserved int64
	rows     [5][]uint8 // row y at y % 5
	strip    *image.NRGBA
	sy, next int
	read     int
	out      image.NRGBA
	gout     image.Gray
	y        int
}

func newKernelStage(src pipeline.Source, size int, k []float32, offset float32, alpha bool, f float32, blend bool) (pipeline.Source, error) {
	in := pipeline.Map(src, func(*image.NRGBA) error { return nil })
	b := src.Bounds()
	s := &kernelStage{src: in, env: pipeline.EnvOf(src), w: b.Dx(), h: b.Dy(), size: size, k: k,
		offset: float32(offset + 0.5), chans: 3, blend: blend, f: f, opaque: pipeline.IsOpaque(src),
		gray: src.ColorModel() == color.GrayModel}
	if alpha {
		s.chans = 4
	}
	s.n = pipeline.StripRows(int64(s.w)*4, s.h)
	need := int64(s.n+5) * int64(s.w) * 4
	if s.gray {
		need += int64(s.n) * int64(s.w)
	}
	if err := s.env.Budget.Reserve(need, "filter rows"); err != nil {
		in.Close()
		return nil, err
	}
	s.reserved = need
	for i := range s.rows {
		s.rows[i] = make([]uint8, 4*s.w)
	}
	s.out = image.NRGBA{Pix: make([]uint8, s.n*4*s.w), Stride: 4 * s.w}
	if s.gray {
		s.gout = image.Gray{Pix: make([]uint8, s.n*s.w), Stride: s.w}
	}
	return s, nil
}

func (s *kernelStage) Env() *pipeline.Env      { return s.env }
func (s *kernelStage) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *kernelStage) Opaque() bool            { return s.opaque }
func (s *kernelStage) ColorModel() color.Model {
	if s.gray {
		return color.GrayModel
	}
	return color.NRGBAModel
}

func (s *kernelStage) Close() error {
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
	return s.src.Close()
}

// readRow puts the next source row into the ring.
func (s *kernelStage) readRow() error {
	y := s.read
	for s.strip == nil || y >= s.sy+s.strip.Rect.Dy() {
		m, err := s.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		s.strip, s.sy = m.(*image.NRGBA), s.next
		s.next += s.strip.Rect.Dy()
	}
	copy(s.rows[y%5], s.strip.Pix[(y-s.sy)*s.strip.Stride:][:4*s.w])
	s.read++
	return nil
}

func (s *kernelStage) Next() (image.Image, error) {
	if s.y >= s.h {
		return nil, io.EOF
	}
	y0, y1 := s.y, min(s.y+s.n, s.h)
	r := s.size / 2
	small := s.w < s.size || s.h < s.size
	for y := y0; y < y1; y++ {
		for s.read < min(y+r+1, s.h) {
			if err := s.readRow(); err != nil {
				return nil, err
			}
		}
		d := s.out.Pix[(y-y0)*s.out.Stride:][:4*s.w]
		cur := s.rows[y%5]
		copy(d, cur)
		if !small && y >= r && y < s.h-r {
			if s.size == 3 {
				s.row3(d, cur, s.rows[(y+1)%5], s.rows[(y-1)%5])
			} else {
				s.row5(d, s.rows[(y+2)%5], s.rows[(y+1)%5], cur, s.rows[(y-1)%5], s.rows[(y-2)%5])
			}
		}
		if s.gray {
			g := s.gout.Pix[(y-y0)*s.gout.Stride:][:s.w]
			for x := range g {
				g[x] = d[4*x]
			}
		}
	}
	s.y = y1
	if s.y == s.h {
		for {
			if _, err := s.src.Next(); err != nil {
				if err == io.EOF {
					break
				}
				return nil, err
			}
		}
	}
	if s.gray {
		g := s.gout
		g.Pix = g.Pix[:(y1-y0)*g.Stride]
		g.Rect = image.Rect(0, y0, s.w, y1)
		return &g, nil
	}
	o := s.out
	o.Pix = o.Pix[:(y1-y0)*o.Stride]
	o.Rect = image.Rect(0, y0, s.w, y1)
	return &o, nil
}

// put stores the sum ss of channel i of d (clip8 of Filter.c), blended to
// the image's level c for Sharpness.
func (s *kernelStage) put(d []uint8, i int, ss float32, c uint8) {
	var v uint8
	switch {
	case ss <= 0:
		v = 0
	case ss >= 255:
		v = 255
	default:
		v = uint8(ss)
	}
	if s.blend {
		v = blend(v, c, s.f)
	}
	d[i] = v
}

// row3 is a row of the 3 × 3 filter: below is the row the first row of the
// kernel weighs.
func (s *kernelStage) row3(d, cur, below, above []uint8) {
	k := s.k
	for x := 1; x < s.w-1; x++ {
		for c := 0; c < s.chans; c++ {
			i := 4*x + c
			row := func(r []uint8, k []float32) float32 {
				return float32(float32(float32(float32(r[i-4])*k[0])+float32(float32(r[i])*k[1])) + float32(float32(r[i+4])*k[2]))
			}
			ss := s.offset
			ss = float32(ss + row(below, k[0:3]))
			ss = float32(ss + row(cur, k[3:6]))
			ss = float32(ss + row(above, k[6:9]))
			s.put(d, i, ss, cur[i])
		}
	}
}

// row5 is a row of the 5 × 5 filter, the rows from two below to two above.
func (s *kernelStage) row5(d, b2, b1, cur, a1, a2 []uint8) {
	k := s.k
	for x := 2; x < s.w-2; x++ {
		for c := 0; c < s.chans; c++ {
			i := 4*x + c
			row := func(r []uint8, k []float32) float32 {
				t := float32(float32(float32(r[i-8])*k[0]) + float32(float32(r[i-4])*k[1]))
				t = float32(t + float32(float32(r[i])*k[2]))
				t = float32(t + float32(float32(r[i+4])*k[3]))
				return float32(t + float32(float32(r[i+8])*k[4]))
			}
			ss := s.offset
			ss = float32(ss + row(b2, k[0:5]))
			ss = float32(ss + row(b1, k[5:10]))
			ss = float32(ss + row(cur, k[10:15]))
			ss = float32(ss + row(a1, k[15:20]))
			ss = float32(ss + row(a2, k[20:25]))
			s.put(d, i, ss, cur[i])
		}
	}
}

// Pillow's built-in filters (ImageFilter): size, scale, offset, kernel.
var pillowFilters = [...]struct {
	size          int
	scale, offset float64
	k             []float64
}{
	{5, 16, 0, []float64{1, 1, 1, 1, 1, 1, 0, 0, 0, 1, 1, 0, 0, 0, 1, 1, 0, 0, 0, 1, 1, 1, 1, 1, 1}},   // BLUR
	{3, 1, 255, []float64{-1, -1, -1, -1, 8, -1, -1, -1, -1}},                                          // CONTOUR
	{3, 6, 0, []float64{0, -1, 0, -1, 10, -1, 0, -1, 0}},                                               // DETAIL
	{3, 2, 0, []float64{-1, -1, -1, -1, 10, -1, -1, -1, -1}},                                           // EDGE_ENHANCE
	{3, 1, 0, []float64{-1, -1, -1, -1, 9, -1, -1, -1, -1}},                                            // EDGE_ENHANCE_MORE
	{3, 1, 128, []float64{-1, 0, 0, 0, 1, 0, 0, 0, 0}},                                                 // EMBOSS
	{3, 1, 0, []float64{-1, -1, -1, -1, 8, -1, -1, -1, -1}},                                            // FIND_EDGES
	{3, 16, 0, []float64{-2, -2, -2, -2, 32, -2, -2, -2, -2}},                                          // SHARPEN
	{3, 13, 0, []float64{1, 1, 1, 1, 5, 1, 1, 1, 1}},                                                   // SMOOTH
	{5, 100, 0, []float64{1, 1, 1, 1, 1, 1, 5, 5, 5, 1, 1, 5, 44, 5, 1, 1, 5, 5, 5, 1, 1, 1, 1, 1, 1}}, // SMOOTH_MORE
}

// PillowFilters is how many built-in filters PillowFilter knows.
const PillowFilters = len(pillowFilters)

// PillowFilter is Pillow's built-in filter i (ImageFilter's BLUR, CONTOUR,
// DETAIL, EDGE_ENHANCE, EDGE_ENHANCE_MORE, EMBOSS, FIND_EDGES, SHARPEN,
// SMOOTH, SMOOTH_MORE, in that order).
func PillowFilter(src pipeline.Source, i int) (pipeline.Source, error) {
	if i < 0 || i >= len(pillowFilters) {
		return nil, errors.New("goimgtools: unknown filter")
	}
	f := pillowFilters[i]
	return PillowKernel(src, f.size, f.k, f.scale, f.offset)
}
