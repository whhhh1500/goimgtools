package ops

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Pillow's ImageOps and ImageEnhance (Pillow 12), worked out here from what
// they do, to the byte, on the red, green and blue of each pixel (Pillow's
// ImageOps refuses transparency; here alpha stays as it is).

// PosterizeLUT is ImageOps.posterize's table: the bits highest bits kept.
func PosterizeLUT(bits int) *[256]uint8 {
	var t [256]uint8
	mask := ^(1<<(8-bits) - 1)
	for i := range t {
		t[i] = uint8(i & mask)
	}
	return &t
}

// SolarizeLUT is ImageOps.solarize's table: the levels at or above
// threshold inverted.
func SolarizeLUT(threshold int) *[256]uint8 {
	var t [256]uint8
	for i := range t {
		if i < threshold {
			t[i] = uint8(i)
		} else {
			t[i] = uint8(255 - i)
		}
	}
	return &t
}

// counted is src kept whole (pipeline.Record) with the histograms of its
// red, green and blue and of its lightness (Pillow's convert("L")), and
// the source that replays it, which keep calls on what it makes of the
// replay, so that closing that closes the recording too.
func counted(src pipeline.Source) (hist [3][256]int64, light [256]int64, replay pipeline.Source, keep func(pipeline.Source) pipeline.Source, err error) {
	c := pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			hist[0][p[0]]++
			hist[1][p[1]]++
			hist[2][p[2]]++
			light[pillowLuma(p[0], p[1], p[2])]++
		}
		return nil
	})
	rec, err := pipeline.Record(c)
	if err != nil {
		return hist, light, nil, nil, err
	}
	replay, err = rec.Source()
	if err != nil {
		rec.Close()
		return hist, light, nil, nil, err
	}
	keep = func(out pipeline.Source) pipeline.Source { return &recorded{Source: out, rec: rec, src: c} }
	return hist, light, replay, keep, nil
}

// pillowLuma is Pillow's L of an RGB pixel: ITU-R 601, in 16-bit fixed
// point, rounded.
func pillowLuma(r, g, b uint8) uint8 {
	return uint8((19595*uint32(r) + 38470*uint32(g) + 7471*uint32(b) + 1<<15) >> 16)
}

// channelLevels applies a table to each of red, green and blue.
func channelLevels(src pipeline.Source, t *[3][256]uint8) pipeline.Source {
	if t[0] == t[1] && t[1] == t[2] {
		return Levels(src, &t[0])
	}
	l := *t
	return pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			p[0], p[1], p[2] = l[0][p[0]], l[1][p[1]], l[2][p[2]]
		}
		return nil
	})
}

// Equalize is ImageOps.equalize: for each channel the table that spreads
// its histogram, worked out in whole numbers as Pillow does.
func Equalize(src pipeline.Source) (pipeline.Source, error) {
	hist, _, replay, keep, err := counted(src)
	if err != nil {
		return nil, err
	}
	var t [3][256]uint8
	for c := range t {
		h := &hist[c]
		var sum, last int64
		n := 0
		for _, v := range h {
			if v != 0 {
				sum += v
				last = v
				n++
			}
		}
		step := int64(0)
		if n > 1 {
			step = (sum - last) / 255
		}
		if step == 0 {
			for i := range t[c] {
				t[c][i] = uint8(i)
			}
			continue
		}
		acc := step / 2
		for i := range t[c] {
			t[c][i] = uint8(min(acc/step, 255))
			acc += h[i]
		}
	}
	return keep(channelLevels(replay, &t)), nil
}

// pyFloorDiv is Python's float floor division a // b (CPython's
// float_divmod), for b > 0.
func pyFloorDiv(a, b float64) float64 {
	mod := math.Mod(a, b)
	div := (a - mod) / b
	if mod != 0 && mod < 0 {
		div--
	}
	if div == 0 {
		return 0
	}
	f := math.Floor(div)
	if div-f > 0.5 {
		f++
	}
	return f
}

// AutoContrast is ImageOps.autocontrast: each channel (or with tone the
// lightness, one table for all three) stretched from its lowest to its
// highest level after leaving out low and high percent of the pixels at
// either end and the levels of ignore.
func AutoContrast(src pipeline.Source, low, high float64, ignore []int, tone bool) (pipeline.Source, error) {
	for _, v := range []float64{low, high} {
		if !(v >= 0 && v <= 100) {
			return nil, errors.New("goimgtools: AutoContrast cutoffs are percentages, 0 to 100")
		}
	}
	for _, v := range ignore {
		if v < 0 || v > 255 {
			return nil, errors.New("goimgtools: AutoContrast ignores levels 0 to 255")
		}
	}
	hist, light, replay, keep, err := counted(src)
	if err != nil {
		return nil, err
	}
	layers := hist[:]
	if tone {
		layers = [][256]int64{light}
	}
	var t [3][256]uint8
	for c, h := range layers {
		for _, v := range ignore {
			h[v] = 0
		}
		if low != 0 || high != 0 {
			var n int64
			for _, v := range h {
				n += v
			}
			cut := int64(pyFloorDiv(float64(n)*low, 100))
			for lo := 0; lo < 256; lo++ {
				if cut > h[lo] {
					cut -= h[lo]
					h[lo] = 0
				} else {
					h[lo] -= cut
					cut = 0
				}
				if cut <= 0 {
					break
				}
			}
			cut = int64(pyFloorDiv(float64(n)*high, 100))
			for hi := 255; hi >= 0; hi-- {
				if cut > h[hi] {
					cut -= h[hi]
					h[hi] = 0
				} else {
					h[hi] -= cut
					cut = 0
				}
				if cut <= 0 {
					break
				}
			}
		}
		lo, hi := 0, 255
		for lo < 255 && h[lo] == 0 {
			lo++
		}
		for hi > 0 && h[hi] == 0 {
			hi--
		}
		if hi <= lo {
			for i := range t[c] {
				t[c][i] = uint8(i)
			}
			continue
		}
		scale := 255.0 / float64(hi-lo)
		offset := float64(-float64(lo) * scale)
		for i := range t[c] {
			v := int(float64(float64(i)*scale) + offset)
			t[c][i] = uint8(max(0, min(v, 255)))
		}
	}
	if tone {
		t[1], t[2] = t[0], t[0]
	}
	return keep(channelLevels(replay, &t)), nil
}

// Enhancements of Enhance.
const (
	EnhanceColor = iota
	EnhanceContrast
	EnhanceBrightness
	EnhanceSharpness
)

// blend is Image.blend(d, c, f) of one level: f of the way from d to c, in
// single precision, the fraction dropped (kept within 0 to 255 outside 0 to
// 1).
func blend(d, c uint8, f float32) uint8 {
	switch {
	case f == 0:
		return d
	case f == 1:
		return c
	}
	t := float32(float32(d) + float32(f*float32(int32(c)-int32(d))))
	if f >= 0 && f <= 1 {
		return uint8(t)
	}
	if t <= 0 {
		return 0
	}
	if t >= 255 {
		return 255
	}
	return uint8(t)
}

// Enhance is ImageEnhance's Color, Contrast, Brightness or Sharpness with
// factor: each level factor of the way from the degenerate image's (the
// lightness; a grey of the mean lightness; black; the image smoothed) to
// the image's.
func Enhance(src pipeline.Source, kind int, factor float64) (pipeline.Source, error) {
	if math.IsNaN(factor) || math.IsInf(factor, 0) {
		return nil, errors.New("goimgtools: Enhance takes a finite factor")
	}
	f := float32(factor)
	switch kind {
	case EnhanceColor:
		return pipeline.Map(src, func(strip *image.NRGBA) error {
			for i := 0; i+3 < len(strip.Pix); i += 4 {
				p := strip.Pix[i : i+4 : i+4]
				l := pillowLuma(p[0], p[1], p[2])
				p[0], p[1], p[2] = blend(l, p[0], f), blend(l, p[1], f), blend(l, p[2], f)
			}
			return nil
		}), nil
	case EnhanceBrightness:
		var t [256]uint8
		for i := range t {
			t[i] = blend(0, uint8(i), f)
		}
		return Levels(src, &t), nil
	case EnhanceContrast:
		_, light, replay, keep, err := counted(src)
		if err != nil {
			return nil, err
		}
		// ImageStat's mean, a sum of floats over the count, rounded.
		var sum float64
		var n int64
		for i, v := range light {
			sum += float64(int64(i) * v)
			n += v
		}
		mean := uint8(0)
		if n > 0 {
			mean = uint8(int(sum/float64(n) + 0.5))
		}
		var t [256]uint8
		for i := range t {
			t[i] = blend(mean, uint8(i), f)
		}
		return keep(Levels(replay, &t)), nil
	case EnhanceSharpness:
		if f == 1 {
			return src, nil
		}
		in := pipeline.Map(src, func(*image.NRGBA) error { return nil })
		b := src.Bounds()
		s := &smoothed{src: in, env: pipeline.EnvOf(src), w: b.Dx(), h: b.Dy(), f: f, opaque: pipeline.IsOpaque(src),
			gray: src.ColorModel() == color.GrayModel}
		s.n = pipeline.StripRows(int64(s.w)*4, s.h)
		need := int64(s.n+3) * int64(s.w) * 4
		if s.gray {
			need += int64(s.n) * int64(s.w)
		}
		if err := s.env.Budget.Reserve(need, "sharpness rows"); err != nil {
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
	return nil, errors.New("goimgtools: unknown Enhancement")
}

// smoothed is ImageEnhance.Sharpness: the image blended with Pillow's
// SMOOTH filter of it (3×3, 1 around 5, over 13, in single precision plus
// a half, the fraction dropped; the edge rows and columns as they are).
type smoothed struct {
	src      pipeline.Source
	env      *pipeline.Env
	w, h, n  int
	f        float32
	opaque   bool
	gray     bool
	reserved int64
	rows     [3][]uint8 // rows y-1, y, y+1 at index (row+1) % 3... see row
	strip    *image.NRGBA
	sy, next int
	read     int // source rows read
	out      image.NRGBA
	gout     image.Gray
	y        int
}

func (s *smoothed) Env() *pipeline.Env      { return s.env }
func (s *smoothed) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *smoothed) Opaque() bool            { return s.opaque }
func (s *smoothed) ColorModel() color.Model {
	if s.gray {
		return color.GrayModel
	}
	return color.NRGBAModel
}

func (s *smoothed) Close() error {
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
	return s.src.Close()
}

// readRow puts source row y (the next) into the ring.
func (s *smoothed) readRow() error {
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
	copy(s.rows[y%3], s.strip.Pix[(y-s.sy)*s.strip.Stride:][:4*s.w])
	s.read++
	return nil
}

// The SMOOTH kernel, each weight over 13 in single precision.
var smoothK = [3]float32{float32(1) / 13, float32(5) / 13, float32(1) / 13}

func (s *smoothed) Next() (image.Image, error) {
	if s.y >= s.h {
		return nil, io.EOF
	}
	y0, y1 := s.y, min(s.y+s.n, s.h)
	small := s.w < 3 || s.h < 3
	for y := y0; y < y1; y++ {
		for s.read < min(y+2, s.h) {
			if err := s.readRow(); err != nil {
				return nil, err
			}
		}
		d := s.out.Pix[(y-y0)*s.out.Stride:][:4*s.w]
		cur := s.rows[y%3]
		copy(d, cur)
		if !small && y > 0 && y < s.h-1 {
			up, down := s.rows[(y-1)%3], s.rows[(y+1)%3]
			k0, k1 := smoothK[0], smoothK[1]
			for x := 1; x < s.w-1; x++ {
				for c := 0; c < 3; c++ {
					i := 4*x + c
					// Pillow's KERNEL1x3 of the row below, this one and
					// the row above, added to the offset in that order.
					row := func(r []uint8, mid float32) float32 {
						return float32(float32(float32(float32(r[i-4])*k0)+float32(float32(r[i])*mid)) + float32(float32(r[i+4])*k0))
					}
					ss := float32(0.5)
					ss = float32(ss + row(down, k0))
					ss = float32(ss + row(cur, k1))
					ss = float32(ss + row(up, k0))
					var v uint8
					switch {
					case ss <= 0:
						v = 0
					case ss >= 255:
						v = 255
					default:
						v = uint8(ss)
					}
					d[i] = blend(v, cur[i], s.f)
				}
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
