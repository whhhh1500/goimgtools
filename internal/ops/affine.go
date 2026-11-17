package ops

import (
	"errors"
	"image"
	"image/color"
	"math"
	"math/big"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Affine is sharp's affine (libvips 8.17's affine with its bilinear
// interpolator), worked out here from what libvips does, to the byte:
//
//   - The output is the box around the corners of the source moved by the
//     matrix (each side rounded half away from zero), without the offsets.
//   - Output pixel (x, y) takes the source at the inverse matrix times
//     (x + left - odx, y + top - ody), less (idx, idy): the corners of
//     pixels, not their centres. libvips works out that point afresh at
//     the start of each tile of 128 columns (each whole row when the matrix
//     only scales) and adds the inverse matrix's first column for each
//     column after, so the same is done here, in the same order.
//   - The source has a border of the background, a pixel above and to the
//     left and two below and to the right; a point whose floor is outside
//     0..width, 0..height is the background.
//   - Without transparency the four pixels about the point are weighted in
//     12-bit fixed point: X and Y the fractions times 4096, dropped to
//     whole numbers, c4 = Y·X>>12, c2 = (4096-Y)·X>>12, c3 = Y-c4,
//     c1 = 4096-Y-c2, and the sum plus 2048 shifted right by 12.
//   - With transparency (or a background that is not opaque, which sharp
//     gives an image an alpha channel for) the source is premultiplied in
//     single precision, the four pixels weighted in double precision, the
//     sum kept in single precision (the background put in as it is, not
//     premultiplied), divided again in single precision (0 where alpha is
//     below 0.01) and dropped to whole numbers.

// affineTile is the width of the tiles libvips works out a transform in.
const affineTile = 128

type affineMap struct {
	ia, ib, ic, id     float64 // the inverse matrix
	idx, idy, odx, ody float64
	left, top          int  // the output box's corner
	scaleOnly          bool // libvips works out whole rows
	float              bool // the single-precision path
	bg                 [4]uint8
}

// Affine moves src by the matrix (a, b; c, d) and the offsets, as sharp's
// affine (see affineMap); the corners left are bg. The result is RGBA
// without transparency, NRGBA with.
func Affine(src pipeline.Source, a, b, c, d, idx, idy, odx, ody float64, bg color.Color) (pipeline.Source, error) {
	for _, v := range []float64{a, b, c, d, idx, idy, odx, ody} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("goimgtools: Affine takes finite numbers")
		}
	}
	det := float64(a*d) - float64(b*c)
	if math.Abs(det) < 0x1p-1021 { // twice the smallest normal number, as libvips
		return nil, errors.New("goimgtools: Affine matrix is singular")
	}
	tmp := 1 / det
	m := &affineMap{ia: tmp * d, ib: -tmp * b, ic: -tmp * c, id: tmp * a, idx: idx - 1, idy: idy - 1, odx: odx, ody: ody,
		scaleOnly: b == 0 && c == 0}
	sb := src.Bounds()
	w, h := float64(sb.Dx()), float64(sb.Dy())
	xs := [4]float64{0, float64(b * h), float64(a * w), float64(a*w) + float64(b*h)}
	ys := [4]float64{0, float64(d * h), float64(c * w), float64(c*w) + float64(d*h)}
	l, r, t, bo := xs[0], xs[0], ys[0], ys[0]
	for i := 1; i < 4; i++ {
		l, r = min(l, xs[i]), max(r, xs[i])
		t, bo = min(t, ys[i]), max(bo, ys[i])
	}
	round := func(v float64) int {
		if v > 0 {
			return int(v + 0.5)
		}
		return int(v - 0.5)
	}
	m.left, m.top = round(l), round(t)
	ow, oh := round(r-l), round(bo-t)
	if ow < 1 || oh < 1 || ow > 1<<24 || oh > 1<<24 {
		return nil, errors.New("goimgtools: Affine would make an image of no pixels, or too large")
	}
	n := color.NRGBAModel.Convert(bg).(color.NRGBA)
	m.bg = [4]uint8{n.R, n.G, n.B, n.A}
	opaque := pipeline.IsOpaque(src)
	m.float = !opaque || n.A != 255
	env := pipeline.EnvOf(src)
	return &rotated{
		src: pipeline.Map(src, func(*image.NRGBA) error { return nil }), env: env,
		rect:     image.Rect(0, 0, ow, oh),
		bgOpaque: n.A == 255,
		band:     rgbaBand{env: env},
		aff:      m,
		span:     math.Abs(m.ia) + math.Abs(m.ic),
	}, nil
}

// renderAffine works out columns x0 to x1 of the rows of out through
// cache c (see affineMap).
func (t *rotated) renderAffine(out *image.RGBA, x0, x1 int, c *tileCache) error {
	m := t.aff
	ow := t.rect.Dx()
	fw, fh := float64(t.sw), float64(t.sh)
	tile := affineTile
	if m.scaleOnly {
		tile = ow
	}
	// The premultiplied single-precision pixel at (x, y) of the source
	// with its border, or the background's.
	var nal [256]float32
	for a := range nal {
		nal[a] = float32(float64(a) / 255)
	}
	at := func(x, y int) ([4]uint8, error) {
		p, err := t.pixel(c, x-1, y-1)
		if err != nil || p == nil {
			return m.bg, err
		}
		return [4]uint8{p[0], p[1], p[2], p[3]}, nil
	}
	for oy := out.Rect.Min.Y; oy < out.Rect.Max.Y; oy++ {
		dst := out.Pix[(oy-out.Rect.Min.Y)*out.Stride:][:4*ow]
		oyc := float64(oy+m.top) - m.ody
		for le := x0 / tile * tile; le < x1; le += tile {
			oxc := float64(le+m.left) - m.odx
			ix := float64(m.ia*oxc) + float64(m.ib*oyc) - m.idx
			iy := float64(m.ic*oxc) + float64(m.id*oyc) - m.idy
			for ox := le; ox < min(le+tile, ow); ox, ix, iy = ox+1, ix+m.ia, iy+m.ic {
				if ox < x0 || ox >= x1 {
					continue
				}
				d := dst[4*ox : 4*ox+4 : 4*ox+4]
				if fx, fy := math.Floor(ix), math.Floor(iy); !(fx >= 0 && fx <= fw && fy >= 0 && fy <= fh) {
					if m.float {
						t.unpremultiply(d, [4]float32{float32(m.bg[0]), float32(m.bg[1]), float32(m.bg[2]), float32(m.bg[3])})
					} else {
						d[0], d[1], d[2], d[3] = m.bg[0], m.bg[1], m.bg[2], 255
					}
					continue
				}
				xi, yi := int(ix), int(iy)
				var q [4][4]uint8
				for k := range q {
					var err error
					if q[k], err = at(xi+k%2, yi+k/2); err != nil {
						return err
					}
				}
				if !m.float {
					X, Y := int(float64(ix-float64(xi))*4096), int(float64(iy-float64(yi))*4096)
					Yd := 4096 - Y
					c4, c2 := Y*X>>12, Yd*X>>12
					c3, c1 := Y-c4, Yd-c2
					for ch := 0; ch < 3; ch++ {
						d[ch] = uint8((c1*int(q[0][ch]) + c2*int(q[1][ch]) + c3*int(q[2][ch]) + c4*int(q[3][ch]) + 2048) >> 12)
					}
					d[3] = 255
					continue
				}
				X, Y := ix-float64(xi), iy-float64(yi)
				Xd, Yd := 1-X, 1-Y
				w := [4]float64{float64(Xd * Yd), float64(X * Yd), float64(Xd * Y), float64(X * Y)}
				var v [4]float32
				for ch := 0; ch < 4; ch++ {
					var s float64
					for k := range q {
						p := float32(q[k][ch])
						if ch < 3 {
							p = float32(p * nal[q[k][3]])
						}
						s += float64(w[k] * float64(p))
					}
					v[ch] = float32(s)
				}
				t.unpremultiply(d, v)
			}
		}
	}
	return nil
}

// unpremultiply puts the single-precision premultiplied v into d as libvips
// gives it back: divided by alpha (nothing below 0.01) and dropped to whole
// numbers.
func (t *rotated) unpremultiply(d []uint8, v [4]float32) {
	a := float64(v[3])
	var f float32
	if math.Abs(a) >= 0.01 {
		f = float32(255 / a)
	}
	for ch := 0; ch < 3; ch++ {
		d[ch] = castLevel(float64(f * v[ch]))
	}
	d[3] = castLevel(max(0, min(a, 255)))
}

// castLevel is libvips' cast of a float to 8 bits: kept within 0 to 255,
// the fraction dropped.
func castLevel(v float64) uint8 {
	return uint8(max(0, min(v, 255)))
}

// SinCosDegrees is the sine and cosine libvips works out for a turn by
// degrees (sharp's rotate): of ((degrees / 360) · 2) · π in double
// precision, each rounded to the nearest double, as a correctly rounded C
// library gives them. (math.Sincos can be a unit in the last place off,
// which at angles such as 30 degrees moves the box around the turned image
// by a pixel.) Angles beyond ±1e7 degrees, which libvips refuses, are
// taken modulo 360 first.
func SinCosDegrees(degrees float64) (sin, cos float64) {
	if math.Abs(degrees) > 1e7 {
		degrees = math.Mod(degrees, 360)
	}
	rad := float64(float64(degrees/360)*2) * math.Pi
	const prec = 320
	newF := func() *big.Float { return new(big.Float).SetPrec(prec) }
	x := newF().SetFloat64(rad)
	half := newF().Quo(bigPi, big.NewFloat(2))
	q, _ := newF().Quo(x, half).Float64()
	k := math.Round(q)
	r := newF().Sub(x, newF().Mul(half, newF().SetFloat64(k)))
	// The Taylor series of the sine and cosine at r, |r| about π/4 at most.
	r2 := newF().Mul(r, r)
	s, c := newF().Set(r), newF().SetInt64(1)
	ts, tc := newF().Set(r), newF().SetInt64(1)
	for n := int64(1); n < 400; n++ {
		ts.Neg(ts.Quo(ts.Mul(ts, r2), newF().SetInt64(2*n*(2*n+1))))
		tc.Neg(tc.Quo(tc.Mul(tc, r2), newF().SetInt64((2*n-1)*(2*n))))
		s.Add(s, ts)
		c.Add(c, tc)
		if tc.Sign() == 0 || tc.MantExp(nil) < -prec {
			break
		}
	}
	// Back to the quadrant of x.
	switch int64(math.Mod(k, 4)+4) % 4 {
	case 1:
		s, c = c, s.Neg(s)
	case 2:
		s, c = s.Neg(s), c.Neg(c)
	case 3:
		s, c = c.Neg(c), s
	}
	sin, _ = s.Float64()
	cos, _ = c.Float64()
	return sin, cos
}

// bigPi is π to 330 bits.
var bigPi, _, _ = big.ParseFloat("3.14159265358979323846264338327950288419716939937510582097494459230781640628620899862803482534211706798214808651", 10, 330, big.ToNearestEven)
