package ops

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// SharpenLab is sharp's sharpen with a sigma (libvips' sharpen): the
// lightness (L* in LabS, see labs.go) is blurred with the Gaussian of
// libvips' integer mask cut off at a tenth of its peak, across and then
// down, each pass (sum + scale/2) / scale; the difference of the lightness
// to its blur is mapped by the curve of m1, m2, x1, y2 and y3 (in L* units)
// through libvips' table of 65536 entries and added to the lightness; a*
// and b* stay. With transparency the colours are premultiplied and divided
// again as for BlurLibvips. The result is NRGBA.
func SharpenLab(src pipeline.Source, sigma, m1, m2, x1, y2, y3 float64) (pipeline.Source, error) {
	for _, v := range []float64{sigma, m1, m2, x1, y2, y3} {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return nil, errors.New("goimgtools: sharpen takes finite numbers, at least 0")
		}
	}
	if !(sigma > 0) || sigma > MaxSigma {
		return nil, errors.New("goimgtools: sigma must be above 0 and at most 1000")
	}
	m, r := gaussMask(sigma, 0.1)
	if m == nil {
		return nil, errors.New("goimgtools: sharpen mask too large")
	}
	scale := int32(0)
	for _, v := range m {
		scale += v
	}
	lut := make([]int32, 65536)
	for i := range lut {
		v := float64(i-32767) / 327.67
		var y float64
		switch {
		case v < -x1:
			y = float64((v+x1)*m2) + float64(-x1*m1)
		case v < x1:
			y = v * m1
		default:
			y = float64((v-x1)*m2) + float64(x1*m1)
		}
		y = min(max(y, -y3), y2)
		lut[i] = int32(math.RoundToEven(y * 327.67))
	}
	opaque := pipeline.IsOpaque(src)
	in := pipeline.Map(src, func(*image.NRGBA) error { return nil })
	b := src.Bounds()
	s := &sharpenLab{src: in, env: pipeline.EnvOf(src), w: b.Dx(), h: b.Dy(), r: r, mask: m, scale: scale, lut: lut,
		opaque: opaque, t: labsOnce()}
	s.n = pipeline.StripRows(int64(s.w)*4, s.h)
	ring := min(2*r+s.n+1, s.h)
	need := int64(ring)*int64(s.w)*9 + int64(s.n)*int64(s.w)*4 + int64(s.w)*8
	if err := s.env.Budget.Reserve(need, "sharpen rows"); err != nil {
		return nil, err
	}
	s.reserved = need
	s.rows = make([]labRow, ring)
	for i := range s.rows {
		s.rows[i] = labRow{l: make([]int32, s.w), ab: make([]int16, 2*s.w), hl: make([]int16, s.w), alpha: make([]uint8, s.w)}
	}
	s.out = image.NRGBA{Pix: make([]uint8, s.n*4*s.w), Stride: 4 * s.w}
	return s, nil
}

// gaussMask is libvips' integer Gaussian mask of sigma cut off below
// minAmpl of its peak (see newGaussKernel), and its radius.
func gaussMask(sigma, minAmpl float64) ([]int32, int) {
	sig2 := 2 * sigma * sigma
	maxX := int(min(8*sigma, kernelSanity))
	x := 0
	for ; x < maxX; x++ {
		if math.Exp(-float64(x*x)/sig2) < minAmpl {
			break
		}
	}
	if x >= kernelSanity {
		return nil, 0
	}
	r := max(x-1, 0)
	m := make([]int32, 2*r+1)
	for i := range m {
		d := i - r
		m[i] = int32(math.RoundToEven(20 * math.Exp(-float64(d*d)/sig2)))
	}
	return m, r
}

// labRow is a row in LabS: its lightness, a* and b*, the lightness blurred
// across, and the alpha.
type labRow struct {
	l     []int32
	ab    []int16
	hl    []int16
	alpha []uint8
}

type sharpenLab struct {
	src      pipeline.Source
	env      *pipeline.Env
	w, h, r  int
	mask     []int32
	scale    int32
	lut      []int32
	opaque   bool
	t        *labsTable
	rows     []labRow // row i at i % len
	have     int      // rows read
	strip    *image.NRGBA
	sy       int
	scratch  [][]int32
	out      image.NRGBA
	n, y     int
	reserved int64
}

func (s *sharpenLab) Env() *pipeline.Env      { return s.env }
func (s *sharpenLab) Bounds() image.Rectangle { return s.src.Bounds() }
func (s *sharpenLab) ColorModel() color.Model { return color.NRGBAModel }
func (s *sharpenLab) Opaque() bool            { return s.opaque }

func (s *sharpenLab) Close() error {
	s.env.Budget.Release(s.reserved)
	s.reserved, s.rows = 0, nil
	return s.src.Close()
}

// premul is libvips' premultiplication of a level by alpha, in single
// precision, the fraction dropped.
func premul(v, a uint8) uint8 {
	return uint8(float32(v) * float32(float64(a)/255))
}

// load reads rows into the ring up to row y.
func (s *sharpenLab) load(y int) error {
	for s.have <= y {
		if s.strip == nil || s.have >= s.sy+s.strip.Rect.Dy() {
			m, err := s.src.Next()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return err
			}
			s.strip, s.sy = m.(*image.NRGBA), s.have
		}
		px := s.strip.Pix[(s.have-s.sy)*s.strip.Stride:][:4*s.w]
		row := &s.rows[s.have%len(s.rows)]
		for x := range s.w {
			p := px[4*x : 4*x+4 : 4*x+4]
			r, g, b, a := p[0], p[1], p[2], p[3]
			if !s.opaque && a != 255 {
				r, g, b = premul(r, a), premul(g, a), premul(b, a)
			}
			L, A, B := s.t.toLabS(r, g, b)
			row.l[x], row.ab[2*x], row.ab[2*x+1], row.alpha[x] = L, int16(A), int16(B), a
		}
		// Across: the edge pixels repeated beyond the ends.
		for x := range s.w {
			var sum int32
			for k, wt := range s.mask {
				sum += wt * row.l[min(max(x+k-s.r, 0), s.w-1)]
			}
			row.hl[x] = int16(min(max((sum+s.scale/2)/s.scale, -32768), 32767))
		}
		s.have++
	}
	return nil
}

func (s *sharpenLab) Next() (image.Image, error) {
	if s.y >= s.h {
		return nil, io.EOF
	}
	y0, y1 := s.y, min(s.y+s.n, s.h)
	if err := s.load(min(y1-1+s.r, s.h-1)); err != nil {
		return nil, err
	}
	var div [256]float32
	for a := 1; a < 256; a++ {
		div[a] = float32(255 / float64(a))
	}
	// The rows are independent of one another: with helpers, shared out,
	// each part with a scratch row of its own.
	n := y1 - y0
	parts := max(1, min(s.env.Helpers()+1, n))
	for len(s.scratch) < parts {
		s.scratch = append(s.scratch, make([]int32, s.w))
	}
	s.env.Parallel(parts, func(p int) {
		for y := y0 + p*n/parts; y < y0+(p+1)*n/parts; y++ {
			s.row(y, y0, s.scratch[p], &div)
		}
	})
	out := s.out
	out.Pix = out.Pix[:(y1-y0)*out.Stride]
	b := s.src.Bounds()
	out.Rect = image.Rect(b.Min.X, b.Min.Y+y0, b.Max.X, b.Min.Y+y1)
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
	return &out, nil
}

// row works out row y of the strip that starts at y0, with tmp as scratch.
func (s *sharpenLab) row(y, y0 int, tmp []int32, div *[256]float32) {
	{
		// Down.
		clear(tmp)
		for k, wt := range s.mask {
			hl := s.rows[min(max(y+k-s.r, 0), s.h-1)%len(s.rows)].hl
			for x, v := range hl {
				tmp[x] += wt * int32(v)
			}
		}
		row := &s.rows[y%len(s.rows)]
		dst := s.out.Pix[(y-y0)*s.out.Stride:][:4*s.w]
		for x := range s.w {
			bl := min(max((tmp[x]+s.scale/2)/s.scale, -32768), 32767)
			v1 := row.l[x]
			L := min(max(v1+s.lut[(v1&0x7fff)-(bl&0x7fff)+32768], 0), 32767)
			r, g, b := s.t.fromLabS(L, int32(row.ab[2*x]), int32(row.ab[2*x+1]))
			d := dst[4*x : 4*x+4 : 4*x+4]
			a := row.alpha[x]
			if !s.opaque && a != 255 {
				f := div[a]
				r, g, b = uint8(min(float32(r)*f, 255)), uint8(min(float32(g)*f, 255)), uint8(min(float32(b)*f, 255))
			}
			d[0], d[1], d[2], d[3] = r, g, b, a
		}
	}
}
