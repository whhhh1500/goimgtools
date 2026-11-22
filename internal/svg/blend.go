package svg

import (
	"math"
	"strings"
)

// The blend modes of mix-blend-mode and feBlend, as the W3C's Compositing
// and Blending Level 1 defines them.
const (
	blendNormal = iota
	blendMultiply
	blendScreen
	blendOverlay
	blendDarken
	blendLighten
	blendColorDodge
	blendColorBurn
	blendHardLight
	blendSoftLight
	blendDifference
	blendExclusion
	blendHue
	blendSaturation
	blendColor
	blendLuminosity
	blendPlusLighter
)

var blendNames = map[string]int{
	"normal": blendNormal, "multiply": blendMultiply, "screen": blendScreen, "overlay": blendOverlay,
	"darken": blendDarken, "lighten": blendLighten, "color-dodge": blendColorDodge, "color-burn": blendColorBurn,
	"hard-light": blendHardLight, "soft-light": blendSoftLight, "difference": blendDifference,
	"exclusion": blendExclusion, "hue": blendHue, "saturation": blendSaturation, "color": blendColor,
	"luminosity": blendLuminosity, "plus-lighter": blendPlusLighter,
}

// parseBlend reads a blend mode; ok is false for one unknown.
func parseBlend(s string) (int, bool) {
	m, ok := blendNames[strings.ToLower(strings.TrimSpace(s))]
	return m, ok
}

// blendPixel puts s over d with a blend mode, both premultiplied, from 0
// to 1: co = cs(1 - ab) + cb(1 - as) + as ab B(Cb, Cs), ao = as + ab(1 - as).
func blendPixel(mode int, s, d [4]float32) [4]float32 {
	as, ab := s[3], d[3]
	if mode == blendPlusLighter {
		var o [4]float32
		for q := 0; q < 4; q++ {
			o[q] = min(1, s[q]+d[q])
		}
		return o
	}
	var o [4]float32
	o[3] = as + float32(ab*(1-as))
	if as <= 0 {
		return d
	}
	var cs, cb [3]float32 // unpremultiplied
	for q := 0; q < 3; q++ {
		cs[q] = s[q] / as
		if ab > 0 {
			cb[q] = d[q] / ab
		}
	}
	var bl [3]float32
	if mode >= blendHue {
		bl = blendNonSeparable(mode, cb, cs)
	} else {
		for q := 0; q < 3; q++ {
			bl[q] = blendSeparable(mode, cb[q], cs[q])
		}
	}
	for q := 0; q < 3; q++ {
		o[q] = float32(s[q]*(1-ab)) + float32(d[q]*(1-as)) + float32(float32(as*ab)*bl[q])
	}
	return o
}

// blendSeparable is B(Cb, Cs) of a mode that blends each channel apart.
func blendSeparable(mode int, cb, cs float32) float32 {
	switch mode {
	case blendMultiply:
		return float32(cb * cs)
	case blendScreen:
		return cb + cs - float32(cb*cs)
	case blendOverlay:
		return blendSeparable(blendHardLight, cs, cb)
	case blendDarken:
		return min(cb, cs)
	case blendLighten:
		return max(cb, cs)
	case blendColorDodge:
		switch {
		case cb == 0:
			return 0
		case cs >= 1:
			return 1
		}
		return min(1, cb/(1-cs))
	case blendColorBurn:
		switch {
		case cb >= 1:
			return 1
		case cs <= 0:
			return 0
		}
		return 1 - min(1, (1-cb)/cs)
	case blendHardLight:
		if cs <= 0.5 {
			return float32(cb * float32(2*cs))
		}
		t := float32(2*cs) - 1
		return cb + t - float32(cb*t)
	case blendSoftLight:
		if cs <= 0.5 {
			return cb - float32(float32(1-float32(2*cs))*float32(cb*(1-cb)))
		}
		var dd float32
		if cb <= 0.25 {
			dd = float32(float32(float32(float32(16*cb)-12)*cb)+4) * cb
		} else {
			dd = sqrt32(cb)
		}
		return cb + float32(float32(float32(2*cs)-1)*(dd-cb))
	case blendDifference:
		if cb > cs {
			return cb - cs
		}
		return cs - cb
	case blendExclusion:
		return cb + cs - float32(float32(2*cb)*cs)
	}
	return cs // normal
}

func sqrt32(x float32) float32 {
	if x <= 0 {
		return 0
	}
	return float32(math.Sqrt(float64(x)))
}

func lum(c [3]float32) float32 {
	return float32(0.3*c[0]) + float32(0.59*c[1]) + float32(0.11*c[2])
}

func clipColor(c [3]float32) [3]float32 {
	l := lum(c)
	n := min(c[0], c[1], c[2])
	x := max(c[0], c[1], c[2])
	if n < 0 && l != n {
		for q := range c {
			c[q] = l + float32(float32(c[q]-l)*l)/(l-n)
		}
	}
	if x > 1 && x != l {
		for q := range c {
			c[q] = l + float32(float32(c[q]-l)*(1-l))/(x-l)
		}
	}
	return c
}

func setLum(c [3]float32, l float32) [3]float32 {
	d := l - lum(c)
	for q := range c {
		c[q] += d
	}
	return clipColor(c)
}

func sat(c [3]float32) float32 { return max(c[0], c[1], c[2]) - min(c[0], c[1], c[2]) }

func setSat(c [3]float32, s float32) [3]float32 {
	// The largest channel to s, the smallest to 0, the middle between.
	i := [3]int{0, 1, 2}
	if c[i[0]] > c[i[1]] {
		i[0], i[1] = i[1], i[0]
	}
	if c[i[1]] > c[i[2]] {
		i[1], i[2] = i[2], i[1]
	}
	if c[i[0]] > c[i[1]] {
		i[0], i[1] = i[1], i[0]
	}
	lo, mid, hi := i[0], i[1], i[2]
	var o [3]float32
	if c[hi] > c[lo] {
		o[mid] = float32(float32(c[mid]-c[lo])*s) / (c[hi] - c[lo])
		o[hi] = s
	}
	return o
}

// blendNonSeparable is B(Cb, Cs) of hue, saturation, color and luminosity.
func blendNonSeparable(mode int, cb, cs [3]float32) [3]float32 {
	switch mode {
	case blendHue:
		return setLum(setSat(cs, sat(cb)), lum(cb))
	case blendSaturation:
		return setLum(setSat(cb, sat(cs)), lum(cb))
	case blendColor:
		return setLum(cs, lum(cb))
	}
	return setLum(cb, lum(cs)) // luminosity
}
