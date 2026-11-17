package ops

import (
	"errors"
	"image"
	"image/color"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ---- levels: a table for the red, green and blue levels ----

// Levels maps the red, green and blue levels of src through lut, leaving
// transparency as it is (the colours are taken as they are, not
// multiplied by alpha). An opaque greyscale source stays greyscale.
func Levels(src pipeline.Source, lut *[256]uint8) pipeline.Source {
	t := *lut
	if pipeline.IsOpaque(src) && src.ColorModel() == color.GrayModel {
		return &grayLevels{src: src, env: pipeline.EnvOf(src), lut: t}
	}
	return pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			p[0], p[1], p[2] = t[p[0]], t[p[1]], t[p[2]]
		}
		return nil
	})
}

// NegateLUT turns every level into its opposite.
func NegateLUT() *[256]uint8 {
	var t [256]uint8
	for i := range t {
		t[i] = 255 - uint8(i)
	}
	return &t
}

// LinearLUT is a*v + b kept within 0 to 255, its fraction dropped, as
// libvips' linear to 8 bits (sharp's linear).
func LinearLUT(a, b float64) (*[256]uint8, error) {
	if math.IsNaN(a) || math.IsInf(a, 0) || math.IsNaN(b) || math.IsInf(b, 0) {
		return nil, errors.New("goimgtools: Linear takes finite numbers")
	}
	var t [256]uint8
	for i := range t {
		// The product rounded before b is added, on every processor.
		t[i] = uint8(math.Max(0, math.Min(255, float64(a*float64(i))+b)))
	}
	return &t, nil
}

// GammaLUT is 255 * (v/255)^(1/g), as ImageMagick's -gamma: above 1
// brightens the middle levels, below darkens them, black and white stay.
func GammaLUT(g float64) (*[256]uint8, error) {
	if !(g > 0) || math.IsInf(g, 0) {
		return nil, errors.New("goimgtools: Gamma must be above 0")
	}
	var t [256]uint8
	for i := range t {
		t[i] = clampLevel(255 * math.Pow(float64(i)/255, 1/g))
	}
	return &t, nil
}

// Threshold makes src black and white, as sharp's threshold: pixels whose
// grey (the brightness of their linear light, as libvips' B_W) is level or
// more become white, the others black. An opaque source gives
// *image.Gray strips; transparency is kept.
func Threshold(src pipeline.Source, level int) pipeline.Source {
	t := labTables()
	var lut [256]uint8
	for i := max(0, min(level, 256)); i < 256; i++ {
		lut[i] = 255
	}
	if !pipeline.IsOpaque(src) {
		return pipeline.Map(src, func(strip *image.NRGBA) error {
			for i := 0; i+3 < len(strip.Pix); i += 4 {
				p := strip.Pix[i : i+4 : i+4]
				v := lut[t.grey(p[0], p[1], p[2])]
				p[0], p[1], p[2] = v, v, v
			}
			return nil
		})
	}
	return &grayOf{src: pipeline.Map(src, func(*image.NRGBA) error { return nil }), env: pipeline.EnvOf(src),
		f: func(r, g, b uint8) uint8 { return lut[t.grey(r, g, b)] }}
}

// grayOf makes *image.Gray strips of the NRGBA strips of a source, each
// pixel's grey f of its colour, or its channel ch when f is nil.
type grayOf struct {
	src      pipeline.Source
	env      *pipeline.Env
	f        func(r, g, b uint8) uint8
	ch       int
	buf      []uint8
	reserved int64
}

func (g *grayOf) Env() *pipeline.Env      { return g.env }
func (g *grayOf) Bounds() image.Rectangle { return g.src.Bounds() }
func (g *grayOf) ColorModel() color.Model { return color.GrayModel }
func (g *grayOf) Opaque() bool            { return true }

func (g *grayOf) Close() error {
	g.env.Budget.Release(g.reserved)
	g.reserved, g.buf = 0, nil
	return g.src.Close()
}

func (g *grayOf) Next() (image.Image, error) {
	s, err := g.src.Next()
	if err != nil {
		return nil, err
	}
	n := s.(*image.NRGBA)
	r := n.Rect
	need := int64(r.Dx()) * int64(r.Dy())
	if int64(len(g.buf)) < need {
		g.env.Budget.Release(g.reserved)
		g.reserved, g.buf = 0, nil
		if err := g.env.Budget.Reserve(need, "grey strip"); err != nil {
			return nil, err
		}
		g.reserved, g.buf = need, make([]uint8, need)
	}
	out := &image.Gray{Pix: g.buf[:need], Stride: r.Dx(), Rect: r}
	for y := 0; y < r.Dy(); y++ {
		sp := n.Pix[y*n.Stride:][:4*r.Dx()]
		d := out.Pix[y*r.Dx():][:r.Dx()]
		if g.f == nil {
			for x := range d {
				d[x] = sp[4*x+g.ch]
			}
			continue
		}
		for x := range d {
			d[x] = g.f(sp[4*x], sp[4*x+1], sp[4*x+2])
		}
	}
	return out, nil
}

func clampLevel(v float64) uint8 {
	return uint8(math.Round(math.Max(0, math.Min(255, v))))
}

// grayLevels is Levels for an opaque greyscale source.
type grayLevels struct {
	src      pipeline.Source
	env      *pipeline.Env
	lut      [256]uint8
	buf      []uint8
	reserved int64
}

func (g *grayLevels) Env() *pipeline.Env      { return g.env }
func (g *grayLevels) Bounds() image.Rectangle { return g.src.Bounds() }
func (g *grayLevels) ColorModel() color.Model { return color.GrayModel }
func (g *grayLevels) Opaque() bool            { return true }

func (g *grayLevels) Close() error {
	g.env.Budget.Release(g.reserved)
	g.reserved, g.buf = 0, nil
	return g.src.Close()
}

func (g *grayLevels) Next() (image.Image, error) {
	s, err := g.src.Next()
	if err != nil {
		return nil, err
	}
	r := s.Bounds()
	w := r.Dx()
	need := int64(w) * int64(r.Dy())
	if int64(len(g.buf)) < need {
		g.env.Budget.Release(g.reserved)
		g.reserved, g.buf = 0, nil
		if err := g.env.Budget.Reserve(need, "grey strip"); err != nil {
			return nil, err
		}
		g.reserved, g.buf = need, make([]uint8, need)
	}
	out := &image.Gray{Pix: g.buf[:need], Stride: w, Rect: r}
	for y := r.Min.Y; y < r.Max.Y; y++ {
		d := out.Pix[(y-r.Min.Y)*w:][:w]
		if gs, ok := s.(*image.Gray); ok {
			for x, v := range gs.Pix[gs.PixOffset(r.Min.X, y):][:w] {
				d[x] = g.lut[v]
			}
			continue
		}
		for x := range d {
			d[x] = g.lut[color.GrayModel.Convert(s.At(r.Min.X+x, y)).(color.Gray).Y]
		}
	}
	return out, nil
}

// ---- tint ----

// Tint keeps the brightness of each pixel and gives it the colour c, as
// sharp's tint does: the image is made grey (the brightness of its linear
// light, as libvips' B_W), and each grey level becomes the colour with the
// L* of that grey and a* and b* those of c, weighted 1 - 4(L-0.5)^2 (L*
// from 0 to 1) so that black and white stay. Transparency is kept.
func Tint(src pipeline.Source, c color.Color) pipeline.Source {
	t := labTables()
	n := color.NRGBAModel.Convert(c).(color.NRGBA)
	_, ta, tb := t.toLab(n.R, n.G, n.B)
	var lut [256][3]uint8
	for v := range lut {
		L, _, _ := t.toLab(uint8(v), uint8(v), uint8(v))
		l := float64(L) / 100
		w := float32(1 - float64(4*(l-0.5)*(l-0.5)))
		lut[v][0], lut[v][1], lut[v][2] = t.fromLab(L, w*ta, w*tb)
	}
	return pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			o := &lut[t.grey(p[0], p[1], p[2])]
			p[0], p[1], p[2] = o[0], o[1], o[2]
		}
		return nil
	})
}

// GreyRow sets dst[i] to libvips' 8-bit B_W (sharp's greyscale) of the
// colour of pixel i of the NRGBA pixels pix, to the byte (see grey).
func GreyRow(dst, pix []uint8) {
	t := labTables()
	for i := range dst {
		p := pix[4*i : 4*i+3 : 4*i+3]
		dst[i] = t.grey(p[0], p[1], p[2])
	}
}

// grey is libvips' 8-bit B_W of an sRGB colour, to the byte (checked
// on 65536 colours against sharp's greyscale): the luminance of its linear
// light (Rec. 709 weights, the products in double precision as libvips
// makes them) in single precision, made a level by interpolating between
// the levels of the steps of 1/255 and rounding half to even (adding 2^23
// in single precision rounds so). Every product is rounded before it is
// added (see toLab).
func (t *labTable) grey(r, g, b uint8) uint8 {
	y := float32(t.wr[r] + t.wg[g] + t.wb[b])
	y = min(max(y, 0), 1)
	yf := float32(y * 255)
	yi := min(int(yf), 254)
	v := float32(float32(t.bw[yi+1]-t.bw[yi]) * (yf - float32(yi)))
	return uint8(int32(float32(float32(float32(t.bw[yi])+v)+0x1p23)) - 0x1p23)
}

// ---- normalize ----

// Normalize stretches the lightness of src to the full range, as sharp's
// normalise does: in the L*a*b* colour space, the L* below which lower
// percent of the pixels lie becomes black and the L* above which 100-upper
// percent lie white, a* and b* left as they are; the lightness levels are
// whole numbers 0 to 100 as libvips counts them. An image whose lightness
// spans a level or less is left as it is. The whole image is read first,
// to count, and kept in working storage (a temporary file beyond its share
// of the limit).
func Normalize(src pipeline.Source, lower, upper float64) (pipeline.Source, error) {
	if !(lower >= 0 && lower < upper && upper <= 100) {
		return nil, errors.New("goimgtools: Normalize wants 0 <= lower < upper <= 100")
	}
	t := labTables()
	scan := &lightness{t: t}
	counted := pipeline.Map(src, func(strip *image.NRGBA) error {
		scan.count(strip.Pix)
		return nil
	})
	rec, err := pipeline.Record(counted)
	if err != nil {
		return nil, err
	}
	replay, err := rec.Source()
	if err != nil {
		rec.Close()
		return nil, err
	}
	lo, hi := scan.min, scan.max
	if lower > 0 {
		lo = scan.percent(lower)
	}
	if upper < 100 {
		hi = scan.percent(upper)
	}
	if scan.n == 0 {
		lo, hi = 0, 0
	}
	out := pipeline.Source(replay)
	if hi-lo > 1 {
		f := float32(100.0 / float64(hi-lo))
		a := -float32(lo) * f
		out = pipeline.Map(replay, func(strip *image.NRGBA) error {
			var lastIn, lastOut [3]uint8
			have := false
			for i := 0; i+3 < len(strip.Pix); i += 4 {
				p := strip.Pix[i : i+4 : i+4]
				in := [3]uint8{p[0], p[1], p[2]}
				if have && in == lastIn {
					p[0], p[1], p[2] = lastOut[0], lastOut[1], lastOut[2]
					continue
				}
				L, la, lb := t.toLab(p[0], p[1], p[2])
				p[0], p[1], p[2] = t.fromLab(float32(L*f)+a, la, lb)
				lastIn, lastOut, have = in, [3]uint8{p[0], p[1], p[2]}, true
			}
			return nil
		})
	}
	return &recorded{Source: out, rec: rec, src: counted}, nil
}

// lightness counts the L* of pixels as whole levels (libvips' histogram of
// the float band), with the smallest and largest.
type lightness struct {
	t        *labTable
	hist     [101]int64
	n        int64
	min, max int
}

func (s *lightness) count(pix []uint8) {
	var last [3]uint8
	lastL, have := 0, false
	for i := 0; i+3 < len(pix); i += 4 {
		c := [3]uint8{pix[i], pix[i+1], pix[i+2]}
		if !have || c != last {
			L, _, _ := s.t.toLab(c[0], c[1], c[2])
			lastL, last = max(0, min(100, int(L))), c
			if s.n == 0 && !have {
				s.min, s.max = lastL, lastL
			}
			have = true
			s.min, s.max = min(s.min, lastL), max(s.max, lastL)
		}
		s.hist[lastL]++
		s.n++
	}
}

// percent is libvips' vips_percent on the lightness: its histogram has a
// bin for each level from 0 to the largest (x of them), the running counts
// are scaled to x-1 and cut to whole numbers (vips_hist_norm), and the
// result is the first level whose count is above pct percent of x, or x
// when none is (so 99 can give one level more than the largest). (Checked
// against vips percent at 13 percentages on three images.)
func (s *lightness) percent(pct float64) int {
	x := s.max + 1
	want := pct * float64(x) / 100
	var cum int64
	for v := 0; v < x; v++ {
		cum += s.hist[v]
		if float64(cum*int64(x-1)/s.n) > want {
			return v
		}
	}
	return x
}

// recorded is a replay of a recording, which it closes with the source the
// recording was made from.
type recorded struct {
	pipeline.Source
	rec *pipeline.Recording
	src pipeline.Source
}

func (r *recorded) Opaque() bool { return pipeline.IsOpaque(r.Source) }

func (r *recorded) Close() error {
	err := r.Source.Close()
	r.rec.Close()
	if e := r.src.Close(); err == nil {
		err = e
	}
	return err
}
