package ops

import (
	"image"
	"math"
)

// The blend modes of an overlay: how its colour mixes with the colour under
// it where both are there, as libvips' composite (so sharp's blend)
// computes them: the PDF blend modes, but with both colours multiplied by
// their alpha, f their blend, and the result
//
//	B' = (1-aB)*A + (1-aA)*B + aA*aB*f(A, B),  aR = aA + aB*(1-aA)
//
// (A the overlay's, B the image's), then divided by aR and cut to 8 bits.
const (
	BlendOver = iota
	BlendMultiply
	BlendScreen
	BlendOverlay
	BlendDarken
	BlendLighten
	BlendColorDodge
	BlendColorBurn
	BlendHardLight
	BlendSoftLight
	BlendDifference
	BlendExclusion
	blendModes
)

// ValidBlend reports whether m is one of the blend modes.
func ValidBlend(m int) bool { return m >= 0 && m < blendModes }

// blendOf returns f for a mode, as libvips' vips_composite_base_blend, on
// colours from 0 to 1 multiplied by alpha (a the overlay's, b the image's).
// Every product is rounded before it is added (the explicit float64
// conversions), so that no processor fuses the two and rounds differently.
func blendOf(mode int) func(a, b float64) float64 {
	switch mode {
	case BlendMultiply:
		return func(a, b float64) float64 { return a * b }
	case BlendScreen:
		return func(a, b float64) float64 { return a + b - float64(a*b) }
	case BlendOverlay:
		return func(a, b float64) float64 {
			if b <= 0.5 {
				return 2 * a * b
			}
			return 1 - float64(2*(1-a)*(1-b))
		}
	case BlendDarken:
		return math.Min
	case BlendLighten:
		return math.Max
	case BlendColorDodge:
		return func(a, b float64) float64 {
			if a < 1 {
				return math.Min(1, b/(1-a))
			}
			return 1
		}
	case BlendColorBurn:
		return func(a, b float64) float64 {
			if a > 0 {
				return 1 - math.Min(1, (1-b)/a)
			}
			return 0
		}
	case BlendHardLight:
		return func(a, b float64) float64 {
			if a < 0.5 {
				return 2 * a * b
			}
			return 1 - float64(2*(1-a)*(1-b))
		}
	case BlendSoftLight:
		return func(a, b float64) float64 {
			g := math.Sqrt(b)
			if b <= 0.25 {
				g = (float64((float64(16*b)-12)*b) + 4) * b
			}
			if a <= 0.5 {
				return b - float64((1-2*a)*b*(1-b))
			}
			return b + float64((2*a-1)*(g-b))
		}
	case BlendDifference:
		return func(a, b float64) float64 { return math.Abs(b - a) }
	case BlendExclusion:
		return func(a, b float64) float64 { return a + b - float64(2*a*b) }
	}
	return func(a, b float64) float64 { return a }
}

// drawBlend draws the part r of top (straight colours; its point sp at
// r.Min) over out (multiplied by alpha) in the given mode, which is not
// BlendOver.
func drawBlend(out *image.RGBA, r image.Rectangle, top *image.NRGBA, sp image.Point, mode int) {
	f := blendOf(mode)
	for y := r.Min.Y; y < r.Max.Y; y++ {
		d := out.Pix[out.PixOffset(r.Min.X, y):][:4*r.Dx()]
		s := top.Pix[top.PixOffset(sp.X, sp.Y+y-r.Min.Y):][:4*r.Dx()]
		for i := 0; i < len(d); i += 4 {
			if s[i+3] == 0 {
				continue // aA = 0 leaves B as it is
			}
			aA, aB := float64(s[i+3])/255, float64(d[i+3])/255
			aR := aA + float64(aB*(1-aA))
			ar := uint8(aR * 255) // cut, as libvips casts
			for c := 0; c < 3; c++ {
				A := float64(s[i+c]) / 255 * aA
				B := float64(d[i+c]) / 255
				v := (float64((1-aB)*A) + float64((1-aA)*B) + float64(aA*aB*f(A, B))) / aR
				cut := uint32(math.Max(0, math.Min(255, v*255)))
				// Kept multiplied by alpha, as the band is.
				d[i+c] = uint8((cut*uint32(ar) + 127) / 255)
			}
			d[i+3] = ar
		}
	}
}
