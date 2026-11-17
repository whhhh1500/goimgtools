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
	// The Porter-Duff operators, and add and saturate, as libvips has them.
	BlendClear
	BlendSource
	BlendIn
	BlendOut
	BlendAtop
	BlendDest
	BlendDestOver
	BlendDestIn
	BlendDestOut
	BlendDestAtop
	BlendXor
	BlendAdd
	BlendSaturate
	blendModes
)

// consumes reports a mode in which the image is gone where the overlay is
// not (its alpha 0 makes the result's 0): there the image turns
// transparent.
func consumes(mode int) bool {
	switch mode {
	case BlendClear, BlendSource, BlendIn, BlendOut, BlendDestIn, BlendDestAtop:
		return true
	}
	return false
}

// porterDuff works out a Porter-Duff mode (or add, saturate) as libvips'
// vips_composite_base_blend: from the overlay's colours A and the image's B,
// multiplied by their alphas aA and aB, the result's alpha and colours
// (multiplied by it); false for a mode that is not one of them.
func porterDuff(mode int, A, B *[3]float64, aA, aB float64) (float64, bool) {
	var aR float64
	switch mode {
	case BlendClear:
		*B = [3]float64{}
		return 0, true
	case BlendSource:
		*B = *A
		return aA, true
	case BlendIn, BlendOut:
		if mode == BlendIn {
			aR = float64(aA * aB)
		} else {
			aR = float64(aA * (1 - aB))
		}
		if aA != 0 {
			for c := range B {
				B[c] = float64(A[c]*aR) / aA
			}
		}
	case BlendAtop:
		aR = aB
		for c := range B {
			B[c] = A[c] + float64((1-aA)*B[c])
		}
	case BlendDest:
		aR = aB
	case BlendDestOver:
		aR = aB + float64(aA*(1-aB))
		for c := range B {
			B[c] = B[c] + float64((1-aB)*A[c])
		}
	case BlendDestIn, BlendDestOut:
		if mode == BlendDestIn {
			aR = float64(aA * aB)
		} else {
			aR = float64((1 - aA) * aB)
		}
		if aB != 0 {
			for c := range B {
				B[c] *= aR / aB
			}
		}
	case BlendDestAtop:
		aR = aA
		for c := range B {
			B[c] = float64((1-aB)*A[c]) + B[c]
		}
	case BlendXor:
		aR = aA + aB - float64(2*aA*aB)
		for c := range B {
			B[c] = float64((1-aB)*A[c]) + float64((1-aA)*B[c])
		}
	case BlendAdd:
		aR = math.Min(1, aA+aB)
		for c := range B {
			B[c] = A[c] + B[c]
		}
	case BlendSaturate:
		aR = math.Min(1, aA+aB)
		t := math.Min(aA, 1-aB)
		for c := range B {
			B[c] = float64(t*A[c]) + B[c]
		}
	default:
		return 0, false
	}
	return aR, true
}

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
// r.Min) over base into out (both multiplied by alpha; base may be out) in
// the given mode, which is not BlendOver.
func drawBlend(out, base *image.RGBA, r image.Rectangle, top *image.NRGBA, sp image.Point, mode int) {
	if _, pd := porterDuff(mode, &[3]float64{}, &[3]float64{}, 0, 0); pd {
		drawPorterDuff(out, base, r, top, sp, mode)
		return
	}
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

// drawPorterDuff is drawBlend for the Porter-Duff modes (and add,
// saturate): every pixel of r, transparent ones of the overlay too.
func drawPorterDuff(out, base *image.RGBA, r image.Rectangle, top *image.NRGBA, sp image.Point, mode int) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		d := out.Pix[out.PixOffset(r.Min.X, y):][:4*r.Dx()]
		b := base.Pix[base.PixOffset(r.Min.X, y):][:4*r.Dx()]
		s := top.Pix[top.PixOffset(sp.X, sp.Y+y-r.Min.Y):][:4*r.Dx()]
		for i := 0; i < len(d); i += 4 {
			aA, aB := float64(s[i+3])/255, float64(b[i+3])/255
			var A, B [3]float64
			for c := 0; c < 3; c++ {
				A[c] = float64(s[i+c]) / 255 * aA
				B[c] = float64(b[i+c]) / 255
			}
			aR, _ := porterDuff(mode, &A, &B, aA, aB)
			ar := uint8(math.Max(0, math.Min(255, aR*255))) // cut, as libvips casts
			for c := 0; c < 3; c++ {
				var v float64
				if aR > 0 {
					v = B[c] / aR
				}
				cut := uint32(math.Max(0, math.Min(255, v*255)))
				d[i+c] = uint8((cut*uint32(ar) + 127) / 255)
			}
			d[i+3] = ar
		}
	}
}
