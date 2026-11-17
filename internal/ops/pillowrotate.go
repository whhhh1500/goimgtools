package ops

// Pillow's affine transform (src/libImaging/Geometry.c, Pillow 12.1.1,
// MIT-CMU licence, see licenses/LICENSE-PILLOW), behind its Image.rotate:
// each output pixel takes the source at the matrix times its centre,
//
//   - nearest: the pixel that point falls in, worked out in 16.16 fixed
//     point when the whole image stays within ±32768 (affine_fixed), in
//     double precision added up along the row otherwise, or, for a matrix
//     that only scales, from a table of columns (ImagingScaleAffine);
//   - bilinear and bicubic (a = -1): the pixels around it, the edges
//     repeated, each channel in double precision, the result dropped to a
//     whole number (and kept within 0 to 255 for bicubic);
//
// a point outside the source giving the fill. An image with transparency
// is worked out with its colours multiplied by alpha for bilinear and
// bicubic (Pillow's RGBa), the fill included, and divided again after.

import (
	"errors"
	"image"
	"image/color"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Pillow filters.
const (
	PillowNearest = iota
	PillowBilinear
	PillowBicubic
)

type pillowMap struct {
	a      [6]float64
	filter int
	bands  int // 3 without transparency (alpha stays 255), 4 with
	premul bool
	fill   [4]uint8
	// fixed: the nearest of affine_fixed, a2 and a5 the start of row 0;
	// scale: that of ImagingScaleAffine, xin the column of each output
	// column (-1 outside).
	fixed        bool
	f0, f1, f3   int64
	f4, f2, f5   int64
	scale        bool
	xin          []int
	rowX, rowY   []float64 // the running start of each output row (float paths)
	nextRowStart float64
	nextRowY     float64
}

// AffinePillow transforms src as Pillow's Image.transform with
// Transform.AFFINE and the matrix a (from output pixel centres to source
// points), to an image of ow x oh, with filter (PillowNearest,
// PillowBilinear or PillowBicubic) and fill for what lies outside (nil:
// zero, transparent black with transparency and black without).
func AffinePillow(src pipeline.Source, a [6]float64, ow, oh, filter int, fill color.Color) (pipeline.Source, error) {
	for _, v := range a {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("goimgtools: the transform takes finite numbers")
		}
	}
	if ow < 1 || oh < 1 || ow > 1<<24 || oh > 1<<24 {
		return nil, errors.New("goimgtools: the transform would make an image of no pixels, or too large")
	}
	if _, paletted := src.ColorModel().(color.Palette); paletted {
		filter = PillowNearest // as Pillow does for P images
	}
	opaque := pipeline.IsOpaque(src)
	m := &pillowMap{a: a, filter: filter, bands: 4}
	if opaque {
		m.bands = 3
	}
	m.premul = !opaque && filter != PillowNearest
	if fill != nil {
		n := color.NRGBAModel.Convert(fill).(color.NRGBA)
		m.fill = [4]uint8{n.R, n.G, n.B, n.A}
	}
	if opaque {
		m.fill[3] = 255
	}
	if filter == PillowNearest {
		switch {
		case a[1] == 0 && a[3] == 0:
			m.scale = true
		default:
			check := func(x, y float64) bool {
				return math.Abs(float64(x*a[0])+float64(y*a[1])+a[2]) < 32768 &&
					math.Abs(float64(x*a[3])+float64(y*a[4])+a[5]) < 32768
			}
			w, h := float64(ow), float64(oh)
			m.fixed = check(0, 0) && check(w, h) && check(0, h) && check(w, 0)
		}
	}
	fix := func(v float64) int64 {
		v = float64(v*65536) + 0.5
		if v < 0 {
			return int64(math.Floor(v))
		}
		return int64(int32(v))
	}
	if m.fixed {
		m.f0, m.f1, m.f3, m.f4 = fix(a[0]), fix(a[1]), fix(a[3]), fix(a[4])
		m.f2 = fix(a[2] + float64(a[0]*0.5) + float64(a[1]*0.5))
		m.f5 = fix(a[5] + float64(a[3]*0.5) + float64(a[4]*0.5))
	}
	if m.scale {
		sw := src.Bounds().Dx()
		m.xin = make([]int, ow)
		xo := a[2] + float64(a[0]*0.5)
		for x := range m.xin {
			xi := coord(xo)
			if xi < 0 || xi >= sw {
				xi = -1
			}
			m.xin[x] = xi
			xo += a[0]
		}
		m.nextRowY = a[5] + float64(a[4]*0.5)
	} else if !m.fixed && filter == PillowNearest {
		m.nextRowStart = a[2] + float64(a[1]*0.5) + float64(a[0]*0.5)
		m.nextRowY = a[5] + float64(a[4]*0.5) + float64(a[3]*0.5)
	}
	env := pipeline.EnvOf(src)
	span := math.Abs(a[0]) + math.Abs(a[3])
	return &rotated{
		src: pipeline.Map(src, func(*image.NRGBA) error { return nil }), env: env,
		rect:     image.Rect(0, 0, ow, oh),
		bgOpaque: m.fill[3] == 255,
		band:     rgbaBand{env: env},
		pil:      m,
		span:     max(span, 1e-9),
	}, nil
}

// coord is Pillow's COORD: -1 for a point left of (or above) the image,
// else its whole part.
func coord(v float64) int {
	if v < 0 {
		return -1
	}
	return int(v)
}

// pfloor is Pillow's FLOOR.
func pfloor(v float64) int {
	if v < 0 {
		return int(math.Floor(v))
	}
	return int(v)
}

// startRows works out the running starts of rows y0 to y1 of the float
// nearest paths, which Pillow adds up row after row.
func (m *pillowMap) startRows(y0, y1 int) {
	if m.fixed || m.filter != PillowNearest {
		return
	}
	m.rowX, m.rowY = m.rowX[:0], m.rowY[:0]
	for y := y0; y < y1; y++ {
		m.rowX = append(m.rowX, m.nextRowStart)
		m.rowY = append(m.rowY, m.nextRowY)
		if m.scale {
			m.nextRowY += m.a[4]
		} else {
			m.nextRowStart += m.a[1]
			m.nextRowY += m.a[4]
		}
	}
}

// pilPremul is Pillow's RGBA to RGBa: colour times alpha over 255,
// rounded as MULDIV255 (alpha 0 makes the colour 0).
func pilPremul(p []uint8) [4]uint8 {
	a := uint32(p[3])
	if a == 255 {
		return [4]uint8{p[0], p[1], p[2], p[3]}
	}
	mul := func(c uint32) uint8 {
		t := c*a + 128
		return uint8((t + t>>8) >> 8)
	}
	return [4]uint8{mul(uint32(p[0])), mul(uint32(p[1])), mul(uint32(p[2])), p[3]}
}

// pilUnpremul is Pillow's RGBa to RGBA.
func pilUnpremul(d []uint8) {
	a := uint32(d[3])
	if a == 0 || a == 255 {
		return
	}
	for i := 0; i < 3; i++ {
		d[i] = uint8(min(255, 255*uint32(d[i])/a))
	}
}

// renderPillow works out columns x0 to x1 of the rows of out through cache
// c (see pillowMap).
func (t *rotated) renderPillow(out *image.RGBA, x0, x1 int, c *tileCache) error {
	m := t.pil
	r := out.Rect
	sample := func(x, y int) ([4]uint8, error) {
		x, y = max(0, min(x, t.sw-1)), max(0, min(y, t.sh-1))
		p, err := t.pixel(c, x, y)
		if err != nil {
			return [4]uint8{}, err
		}
		if m.premul {
			return pilPremul(p), nil
		}
		return [4]uint8{p[0], p[1], p[2], p[3]}, nil
	}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		row := out.Pix[(y-r.Min.Y)*out.Stride:]
		var xx, yy float64
		if m.filter == PillowNearest && !m.fixed {
			xx, yy = m.rowX[y-r.Min.Y], m.rowY[y-r.Min.Y]
			if !m.scale {
				// Added up from the first column, as Pillow does.
				for x := 0; x < x0; x++ {
					xx += m.a[0]
					yy += m.a[3]
				}
			}
		}
		for x := x0; x < x1; x++ {
			d := row[4*x : 4*x+4 : 4*x+4]
			var v [4]uint8
			ok := false
			switch {
			case m.filter == PillowNearest && m.fixed:
				fx := m.f2 + int64(y)*m.f1 + int64(x)*m.f0
				fy := m.f5 + int64(y)*m.f4 + int64(x)*m.f3
				xi, yi := int(int32(fx)>>16), int(int32(fy)>>16)
				if xi >= 0 && xi < t.sw && yi >= 0 && yi < t.sh {
					p, err := t.pixel(c, xi, yi)
					if err != nil {
						return err
					}
					v, ok = [4]uint8{p[0], p[1], p[2], p[3]}, true
				}
			case m.filter == PillowNearest && m.scale:
				yi := coord(yy)
				if xi := m.xin[x]; xi >= 0 && yi >= 0 && yi < t.sh {
					p, err := t.pixel(c, xi, yi)
					if err != nil {
						return err
					}
					v, ok = [4]uint8{p[0], p[1], p[2], p[3]}, true
				}
			case m.filter == PillowNearest:
				xi, yi := coord(xx), coord(yy)
				if xi >= 0 && xi < t.sw && yi >= 0 && yi < t.sh {
					p, err := t.pixel(c, xi, yi)
					if err != nil {
						return err
					}
					v, ok = [4]uint8{p[0], p[1], p[2], p[3]}, true
				}
				xx += m.a[0]
				yy += m.a[3]
			default:
				px, py := float64(x)+0.5, float64(y)+0.5
				xin := float64(m.a[0]*px) + float64(m.a[1]*py) + m.a[2]
				yin := float64(m.a[3]*px) + float64(m.a[4]*py) + m.a[5]
				var err error
				if m.filter == PillowBilinear {
					v, ok, err = t.pillowBilinear(xin, yin, sample)
				} else {
					v, ok, err = t.pillowBicubic(xin, yin, sample)
				}
				if err != nil {
					return err
				}
			}
			if !ok {
				v = m.fill
			}
			if m.bands == 3 {
				v[3] = 255
			}
			copy(d, v[:])
			if m.premul {
				pilUnpremul(d)
			}
		}
	}
	return nil
}

func (t *rotated) pillowBilinear(xin, yin float64, at func(x, y int) ([4]uint8, error)) ([4]uint8, bool, error) {
	if xin < 0 || xin >= float64(t.sw) || yin < 0 || yin >= float64(t.sh) {
		return [4]uint8{}, false, nil
	}
	xin -= 0.5
	yin -= 0.5
	x, y := pfloor(xin), pfloor(yin)
	dx, dy := xin-float64(x), yin-float64(y)
	p00, err := at(x, y)
	if err != nil {
		return [4]uint8{}, false, err
	}
	p01, err := at(x+1, y)
	if err != nil {
		return [4]uint8{}, false, err
	}
	second := y+1 >= 0 && y+1 < t.sh
	var p10, p11 [4]uint8
	if second {
		if p10, err = at(x, y+1); err != nil {
			return [4]uint8{}, false, err
		}
		if p11, err = at(x+1, y+1); err != nil {
			return [4]uint8{}, false, err
		}
	}
	lerp := func(a, b, d float64) float64 { return a + float64((b-a)*d) }
	var v [4]uint8
	for b := 0; b < t.pil.bands; b++ {
		v1 := lerp(float64(p00[b]), float64(p01[b]), dx)
		v2 := v1
		if second {
			v2 = lerp(float64(p10[b]), float64(p11[b]), dx)
		}
		v[b] = uint8(lerp(v1, v2, dy))
	}
	return v, true, nil
}

func (t *rotated) pillowBicubic(xin, yin float64, at func(x, y int) ([4]uint8, error)) ([4]uint8, bool, error) {
	if xin < 0 || xin >= float64(t.sw) || yin < 0 || yin >= float64(t.sh) {
		return [4]uint8{}, false, nil
	}
	xin -= 0.5
	yin -= 0.5
	x, y := pfloor(xin)-1, pfloor(yin)-1
	dx, dy := xin-float64(x+1), yin-float64(y+1)
	cubic := func(v1, v2, v3, v4, d float64) float64 {
		p1 := v2
		p2 := -v1 + v3
		p3 := float64(2*(v1-v2)) + v3 - v4
		p4 := -v1 + v2 - v3 + v4
		return p1 + float64(d*(p2+float64(d*(p3+float64(d*p4)))))
	}
	var rows [4][4][4]uint8
	have := [4]bool{true}
	for j := 0; j < 4; j++ {
		if j > 0 {
			have[j] = y+j >= 0 && y+j < t.sh
			if !have[j] {
				continue
			}
		}
		for i := 0; i < 4; i++ {
			p, err := at(x+i, y+j)
			if err != nil {
				return [4]uint8{}, false, err
			}
			rows[j][i] = p
		}
	}
	var v [4]uint8
	for b := 0; b < t.pil.bands; b++ {
		var r [4]float64
		for j := 0; j < 4; j++ {
			if !have[j] {
				r[j] = r[j-1]
				continue
			}
			q := rows[j]
			r[j] = cubic(float64(q[0][b]), float64(q[1][b]), float64(q[2][b]), float64(q[3][b]), dx)
		}
		s := cubic(r[0], r[1], r[2], r[3], dy)
		switch {
		case s <= 0:
			v[b] = 0
		case s >= 255:
			v[b] = 255
		default:
			v[b] = uint8(s)
		}
	}
	return v, true, nil
}
