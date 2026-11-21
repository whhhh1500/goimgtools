package inspect

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"math"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/trig"
)

// Placeholders: short codes of a picture's colours that a page shows,
// blurred, while the image itself loads. Both are computed from the
// picture made upright by its EXIF orientation and reduced to fit 100x100
// pixels (ThumbHash requires that; BlurHash's low frequencies hardly change
// by it), so a 24 megapixel JPEG takes a fraction of a second and a few
// megabytes, and in arithmetic that is the same on every processor: the
// cosines and the sRGB curve come from tables and polynomials of this
// package, not from math.Cos and math.Pow, and no multiplication is fused
// with an addition.

// thumbnail is the picture reduced to fit 100x100, as non-premultiplied
// RGBA.
func thumbnail(img *goimgtools.Image, flatten bool) (*image.NRGBA, error) {
	m := img.Clone().AutoOrient().
		Resize(100, 100, goimgtools.ResizeOptions{Kernel: goimgtools.KernelBox, FastShrinkOnLoad: true, WithoutEnlargement: true})
	if flatten {
		m = m.Flatten(color.White)
	}
	small, err := m.ToImage()
	if err != nil {
		return nil, err
	}
	b := small.Bounds()
	if b.Dx() < 1 || b.Dy() < 1 || b.Dx() > 100 || b.Dy() > 100 {
		return nil, fmt.Errorf("goimgtools/inspect: the image came out as %dx%d", b.Dx(), b.Dy())
	}
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			n.Set(x, y, small.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return n, nil
}

// BlurHash returns the BlurHash (blurha.sh) of an image: a string of 6 to
// 164 characters that a page decodes into a blurred picture of it, shown
// while the image loads. x and y are its components across and down, 1 to
// 9: more show more detail in a longer string (4 by 3 is usual, 30
// characters). A transparent picture is laid on white first. The method is
// that of the reference (woltapp/blurhash): each component the mean of the
// pixels' linear light weighted by a product of cosines, the first in
// sRGB, the others relative to the largest, in base 83.
func BlurHash(img *goimgtools.Image, x, y int) (string, error) {
	if img == nil {
		return "", errors.New("goimgtools/inspect: BlurHash needs an image")
	}
	if x < 1 || x > 9 || y < 1 || y > 9 {
		return "", errors.New("goimgtools/inspect: BlurHash components must be 1 to 9")
	}
	m, err := thumbnail(img, true)
	if err != nil {
		return "", err
	}
	return blurHash(m, x, y), nil
}

// blurHash is BlurHash of the pixels of m.
func blurHash(m *image.NRGBA, cx, cy int) string {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	factors := make([][3]float64, 0, cx*cy)
	for j := 0; j < cy; j++ {
		for i := 0; i < cx; i++ {
			norm := 2.0
			if i == 0 && j == 0 {
				norm = 1
			}
			var f [3]float64
			for x := 0; x < w; x++ {
				bx := float64(norm * trig.CosPi(i*x, w))
				for y := 0; y < h; y++ {
					basis := float64(bx * trig.CosPi(j*y, h))
					p := m.Pix[y*m.Stride+4*x:]
					f[0] += float64(basis * srgbLinear[p[0]])
					f[1] += float64(basis * srgbLinear[p[1]])
					f[2] += float64(basis * srgbLinear[p[2]])
				}
			}
			scale := 1 / float64(w*h)
			factors = append(factors, [3]float64{float64(f[0] * scale), float64(f[1] * scale), float64(f[2] * scale)})
		}
	}
	out := base83(nil, (cx-1)+(cy-1)*9, 1)
	maximum := 1.0
	if len(factors) > 1 {
		actual := 0.0
		for _, f := range factors[1:] {
			actual = max(actual, math.Abs(f[0]), math.Abs(f[1]), math.Abs(f[2]))
		}
		q := int(math.Max(0, math.Min(82, math.Floor(float64(actual*166)-0.5))))
		maximum = float64(q+1) / 166
		out = base83(out, q, 1)
	} else {
		out = base83(out, 0, 1)
	}
	dc := factors[0]
	out = base83(out, linearToSRGB(dc[0])<<16|linearToSRGB(dc[1])<<8|linearToSRGB(dc[2]), 4)
	for _, f := range factors[1:] {
		q := func(v float64) int {
			r := v / maximum
			s := math.Copysign(math.Sqrt(math.Abs(r)), r)
			return int(math.Max(0, math.Min(18, math.Floor(float64(s*9)+9.5))))
		}
		out = base83(out, q(f[0])*19*19+q(f[1])*19+q(f[2]), 2)
	}
	return string(out)
}

// linearToSRGB is the 8-bit sRGB value of linear light v, rounded.
func linearToSRGB(v float64) int {
	lo, hi := 0, len(srgbUp)
	for lo < hi {
		mid := (lo + hi) / 2
		if srgbUp[mid] <= v {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

const base83Chars = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz#$%*+,-.:;=?@[]^_{|}~"

// base83 appends v in n base-83 digits.
func base83(out []byte, v, n int) []byte {
	for i := n - 1; i >= 0; i-- {
		d := v
		for range i {
			d /= 83
		}
		out = append(out, base83Chars[d%83])
	}
	return out
}

// ThumbHash returns the ThumbHash (evanw.github.io/thumbhash) of an image:
// 5 to 25 bytes from which a page draws a blurred picture of it while the
// image loads, with its transparency and its proportions, more faithfully
// than a BlurHash of the same length. The method is the reference's: the
// picture laid on its own mean colour where it is transparent, turned
// into light and two colour differences (and alpha), each reduced to a
// few cosine components, quantized to 4 bits.
func ThumbHash(img *goimgtools.Image) ([]byte, error) {
	if img == nil {
		return nil, errors.New("goimgtools/inspect: ThumbHash needs an image")
	}
	m, err := thumbnail(img, false)
	if err != nil {
		return nil, err
	}
	return thumbHash(m), nil
}

// round is JavaScript's Math.round, which the reference uses: halves up.
func round(v float64) int { return int(math.Floor(v + 0.5)) }

// thumbHash is ThumbHash of the pixels of m (at most 100x100).
func thumbHash(m *image.NRGBA) []byte {
	w, h := m.Rect.Dx(), m.Rect.Dy()
	n := w * h
	var avgR, avgG, avgB, avgA float64
	for i := 0; i < n; i++ {
		p := m.Pix[(i/w)*m.Stride+4*(i%w):]
		alpha := float64(p[3]) / 255
		avgR += float64(alpha / 255 * float64(p[0]))
		avgG += float64(alpha / 255 * float64(p[1]))
		avgB += float64(alpha / 255 * float64(p[2]))
		avgA += alpha
	}
	if avgA > 0 {
		avgR, avgG, avgB = avgR/avgA, avgG/avgA, avgB/avgA
	}
	hasAlpha := avgA < float64(n)
	lLimit := 7.0
	if hasAlpha {
		lLimit = 5 // fewer bits of light when there is alpha
	}
	big := float64(max(w, h))
	lx := max(1, round(float64(lLimit*float64(w))/big))
	ly := max(1, round(float64(lLimit*float64(h))/big))
	l, pp, q, a := make([]float64, n), make([]float64, n), make([]float64, n), make([]float64, n)
	for i := 0; i < n; i++ {
		p := m.Pix[(i/w)*m.Stride+4*(i%w):]
		alpha := float64(p[3]) / 255
		r := float64(avgR*(1-alpha)) + float64(alpha/255*float64(p[0]))
		g := float64(avgG*(1-alpha)) + float64(alpha/255*float64(p[1]))
		b := float64(avgB*(1-alpha)) + float64(alpha/255*float64(p[2]))
		l[i] = (r + g + b) / 3
		pp[i] = float64((r+g)/2) - b
		q[i] = r - g
		a[i] = alpha
	}
	encode := func(ch []float64, nx, ny int) (dc float64, ac []float64, scale float64) {
		fx := make([]float64, w)
		for cy := 0; cy < ny; cy++ {
			for cx := 0; cx*ny < nx*(ny-cy); cx++ {
				f := 0.0
				for x := 0; x < w; x++ {
					fx[x] = trig.CosPi(cx*(2*x+1), 2*w)
				}
				for y := 0; y < h; y++ {
					fy := trig.CosPi(cy*(2*y+1), 2*h)
					for x := 0; x < w; x++ {
						f += float64(float64(ch[x+y*w]*fx[x]) * fy)
					}
				}
				f /= float64(w * h)
				if cx > 0 || cy > 0 {
					ac = append(ac, f)
					scale = max(scale, math.Abs(f))
				} else {
					dc = f
				}
			}
		}
		if scale > 0 {
			for i := range ac {
				ac[i] = 0.5 + float64(0.5/scale*ac[i])
			}
		}
		return dc, ac, scale
	}
	lDC, lAC, lScale := encode(l, max(3, lx), max(3, ly))
	pDC, pAC, pScale := encode(pp, 3, 3)
	qDC, qAC, qScale := encode(q, 3, 3)
	var aDC, aScale float64
	var aAC []float64
	if hasAlpha {
		aDC, aAC, aScale = encode(a, 5, 5)
	}
	landscape := w > h
	b2i := func(v bool) int {
		if v {
			return 1
		}
		return 0
	}
	header24 := round(float64(63*lDC)) | round(31.5+float64(31.5*pDC))<<6 | round(31.5+float64(31.5*qDC))<<12 |
		round(float64(31*lScale))<<18 | b2i(hasAlpha)<<23
	side := lx
	if landscape {
		side = ly
	}
	header16 := side | round(float64(63*pScale))<<3 | round(float64(63*qScale))<<9 | b2i(landscape)<<15
	hash := []byte{byte(header24), byte(header24 >> 8), byte(header24 >> 16), byte(header16), byte(header16 >> 8)}
	start := 5
	if hasAlpha {
		hash = append(hash, byte(round(float64(15*aDC))|round(float64(15*aScale))<<4))
		start = 6
	}
	acs := [][]float64{lAC, pAC, qAC}
	if hasAlpha {
		acs = append(acs, aAC)
	}
	idx := 0
	for _, ac := range acs {
		for _, f := range ac {
			for len(hash) <= start+idx>>1 {
				hash = append(hash, 0)
			}
			hash[start+idx>>1] |= byte(round(float64(15*f)) << ((idx & 1) << 2))
			idx++
		}
	}
	return hash
}
