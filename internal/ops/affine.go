package ops

import (
	"errors"
	"image"
	"image/color"
	"math"
	"math/big"

	"github.com/whhhh1500/goimgtools/internal/resize"
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
//   - With bicubic interpolation (Catmull-Rom, a window of 4 pixels, one
//     before the point) the border is two pixels above and to the left and
//     three below and to the right, the point is a pixel further in, and
//     the window's place is rounded to 1/64 of a pixel: the weights are
//     libvips' table of 65, times 4096 and dropped to whole numbers without
//     transparency (each row's sum plus 2048 shifted right by 12, then the
//     same down the rows, kept within 0 to 255); with it in double
//     precision, each row's sum and the result kept in single precision.
//     Nearest takes the pixel the point is in (a border of one pixel).
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

// Interpolations of Affine.
const (
	AffineBilinear = iota
	AffineNearest
	AffineBicubic
)

type affineMap struct {
	interp             int
	ia, ib, ic, id     float64 // the inverse matrix
	idx, idy, odx, ody float64
	left, top          int  // the output box's corner
	scaleOnly          bool // libvips works out whole rows
	float              bool // the single-precision path
	bg                 [4]uint8
}

// Affine moves src by the matrix (a, b; c, d) and the offsets, as sharp's
// affine (see affineMap), interpolating as interp says; the corners left
// are bg. The result is RGBA without transparency, NRGBA with.
func Affine(src pipeline.Source, a, b, c, d, idx, idy, odx, ody float64, bg color.Color, interp int) (pipeline.Source, error) {
	if interp < AffineBilinear || interp > AffineBicubic {
		return nil, errors.New("goimgtools: unknown Affine interpolation")
	}
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
	m := &affineMap{interp: interp, ia: tmp * d, ib: -tmp * b, ic: -tmp * c, id: tmp * a, idx: idx - 1, idy: idy - 1, odx: odx, ody: ody,
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
	var nal [256]float32
	for a := range nal {
		nal[a] = float32(float64(a) / 255)
	}
	// The window offset: pixels the window reaches before the point.
	wo := 0
	if m.interp == AffineBicubic {
		wo = 1
	}
	// The pixel at (x, y) of the source with its border, or the
	// background's (t.pixel, with the tile last read kept at hand: the
	// samples of neighbouring output pixels mostly come from it).
	lastTi := -1
	var lastTile []uint8
	at := func(x, y int) ([4]uint8, error) {
		sx, sy := x-wo-1, y-wo-1
		if sx < 0 || sy < 0 || sx >= t.sw || sy >= t.sh {
			return m.bg, nil
		}
		if ti := (sy/tileSide)*t.tilesX + sx/tileSide; ti != lastTi {
			tl, err := c.tile(t.sc, ti)
			if err != nil {
				return m.bg, err
			}
			lastTi, lastTile = ti, tl
		}
		o := ((sy%tileSide)*tileSide + sx%tileSide) * 4
		p := lastTile[o : o+4 : o+4]
		return [4]uint8{p[0], p[1], p[2], p[3]}, nil
	}
	// window sets q to the 4 x 4 pixels from (x, y) on (at's coordinates),
	// read together when they are in one tile.
	window := func(x, y int, q *[4][4][4]uint8) error {
		if sx, sy := x-wo-1, y-wo-1; sx >= 0 && sy >= 0 && sx+3 < t.sw && sy+3 < t.sh &&
			sx%tileSide <= tileSide-4 && sy%tileSide <= tileSide-4 {
			if ti := (sy/tileSide)*t.tilesX + sx/tileSide; ti != lastTi {
				tl, err := c.tile(t.sc, ti)
				if err != nil {
					return err
				}
				lastTi, lastTile = ti, tl
			}
			o := ((sy%tileSide)*tileSide + sx%tileSide) * 4
			for j := range q {
				p := lastTile[o+4*tileSide*j : o+4*tileSide*j+16]
				q[j] = [4][4]uint8{[4]uint8(p[0:4]), [4]uint8(p[4:8]), [4]uint8(p[8:12]), [4]uint8(p[12:16])}
			}
			return nil
		}
		for j := range q {
			for i := range q[j] {
				var err error
				if q[j][i], err = at(x+i, y+j); err != nil {
					return err
				}
			}
		}
		return nil
	}
	// pre is a channel of a pixel premultiplied in single precision.
	pre := func(q [4]uint8, ch int) float32 {
		if ch == 3 {
			return float32(q[3])
		}
		return float32(float32(q[ch]) * nal[q[3]])
	}
	for oy := out.Rect.Min.Y; oy < out.Rect.Max.Y; oy++ {
		dst := out.Pix[(oy-out.Rect.Min.Y)*out.Stride:][:4*ow]
		oyc := float64(oy+m.top) - m.ody
		for le := x0 / tile * tile; le < x1; le += tile {
			oxc := float64(le+m.left) - m.odx
			ix := float64(m.ia*oxc) + float64(m.ib*oyc) - m.idx + float64(wo)
			iy := float64(m.ic*oxc) + float64(m.id*oyc) - m.idy + float64(wo)
			for ox := le; ox < min(le+tile, ow); ox, ix, iy = ox+1, ix+m.ia, iy+m.ic {
				if ox < x0 || ox >= x1 {
					continue
				}
				d := dst[4*ox : 4*ox+4 : 4*ox+4]
				if fx, fy := math.Floor(ix)-float64(wo), math.Floor(iy)-float64(wo); !(fx >= 0 && fx <= fw && fy >= 0 && fy <= fh) {
					if m.float {
						t.unpremultiply(d, [4]float32{float32(m.bg[0]), float32(m.bg[1]), float32(m.bg[2]), float32(m.bg[3])})
					} else {
						d[0], d[1], d[2], d[3] = m.bg[0], m.bg[1], m.bg[2], 255
					}
					continue
				}
				xi, yi := int(ix), int(iy)
				switch m.interp {
				case AffineNearest:
					q, err := at(xi, yi)
					if err != nil {
						return err
					}
					if !m.float {
						d[0], d[1], d[2], d[3] = q[0], q[1], q[2], 255
						continue
					}
					t.unpremultiply(d, [4]float32{pre(q, 0), pre(q, 1), pre(q, 2), pre(q, 3)})
					continue
				case AffineBicubic:
					if err := t.bicubic(d, ix, iy, xi, yi, window, pre); err != nil {
						return err
					}
					continue
				}
				var q [4][4]uint8
				if sx, sy := xi-wo-1, yi-wo-1; sx >= 0 && sy >= 0 && sx+1 < t.sw && sy+1 < t.sh &&
					(sx+1)%tileSide != 0 && (sy+1)%tileSide != 0 {
					// The four in one tile: read together.
					if ti := (sy/tileSide)*t.tilesX + sx/tileSide; ti != lastTi {
						tl, err := c.tile(t.sc, ti)
						if err != nil {
							return err
						}
						lastTi, lastTile = ti, tl
					}
					o := ((sy%tileSide)*tileSide + sx%tileSide) * 4
					p := lastTile[o : o+4*tileSide+8]
					q[0], q[1] = [4]uint8(p[0:4]), [4]uint8(p[4:8])
					q[2], q[3] = [4]uint8(p[4*tileSide:4*tileSide+4]), [4]uint8(p[4*tileSide+4:4*tileSide+8])
				} else {
					for k := range q {
						var err error
						if q[k], err = at(xi+k%2, yi+k/2); err != nil {
							return err
						}
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
						s += float64(w[k] * float64(pre(q[k], ch)))
					}
					v[ch] = float32(s)
				}
				t.unpremultiply(d, v)
			}
		}
	}
	return nil
}

// bicubic interpolates the 4 x 4 pixels about (ix, iy), whose whole parts
// are xi, yi, into d, as libvips' bicubic does (see affineMap).
func (t *rotated) bicubic(d []uint8, ix, iy float64, xi, yi int, window func(x, y int, q *[4][4][4]uint8) error, pre func([4]uint8, int) float32) error {
	tx := (int(float64(ix*64)*2)&127 + 1) >> 1
	ty := (int(float64(iy*64)*2)&127 + 1) >> 1
	var q [4][4][4]uint8 // rows, columns, channels
	if err := window(xi-1, yi-1, &q); err != nil {
		return err
	}
	if !t.aff.float {
		cx, cy := resize.Catmull.I[tx], resize.Catmull.I[ty]
		for ch := 0; ch < 3; ch++ {
			var r [4]int
			for j := range q {
				r[j] = (cx[0]*int(q[j][0][ch]) + cx[1]*int(q[j][1][ch]) + cx[2]*int(q[j][2][ch]) + cx[3]*int(q[j][3][ch]) + 2048) >> 12
			}
			v := (cy[0]*r[0] + cy[1]*r[1] + cy[2]*r[2] + cy[3]*r[3] + 2048) >> 12
			d[ch] = uint8(max(0, min(v, 255)))
		}
		d[3] = 255
		return nil
	}
	cx, cy := resize.Catmull.F[tx], resize.Catmull.F[ty]
	var v [4]float32
	for ch := 0; ch < 4; ch++ {
		var r [4]float32
		for j := range q {
			r[j] = float32(float64(cx[0]*float64(pre(q[j][0], ch))) + float64(cx[1]*float64(pre(q[j][1], ch))) +
				float64(cx[2]*float64(pre(q[j][2], ch))) + float64(cx[3]*float64(pre(q[j][3], ch))))
		}
		v[ch] = float32(float64(cy[0]*float64(r[0])) + float64(cy[1]*float64(r[1])) + float64(cy[2]*float64(r[2])) + float64(cy[3]*float64(r[3])))
	}
	t.unpremultiply(d, v)
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
