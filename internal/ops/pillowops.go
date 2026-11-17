package ops

import (
	"errors"
	"image"
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
		// The image blended with Pillow's SMOOTH of it (alpha as it is).
		var k [9]float32
		for i, v := range pillowFilters[8].k {
			k[i] = float32(v) / 13
		}
		return newKernelStage(src, 3, k[:], 0, false, f, true)
	}
	return nil, errors.New("goimgtools: unknown Enhancement")
}
