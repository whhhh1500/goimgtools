package ops

import (
	"errors"
	"image"
	"image/color"
	"math"
	"sync"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ---- grayscale ----

// Grayscale turns src into shades of grey. The grey of a pixel is the
// Rec. 601 luma of its colours, as image/color's GrayModel computes it and
// as JPEG stores brightness; for a JPEG (YCbCr strips) it is the stored
// brightness itself. An opaque source gives *image.Gray strips; one that
// can be transparent gives NRGBA strips with red, green and blue equal and
// the transparency kept.
func Grayscale(src pipeline.Source) pipeline.Source {
	if !pipeline.IsOpaque(src) {
		return pipeline.Map(src, func(strip *image.NRGBA) error {
			for i := 0; i < len(strip.Pix); i += 4 {
				p := strip.Pix[i : i+4 : i+4]
				y := luma(p[0], p[1], p[2])
				p[0], p[1], p[2] = y, y, y
			}
			return nil
		})
	}
	if src.ColorModel() == color.GrayModel {
		return src
	}
	env := pipeline.EnvOf(src)
	return &grayscale{src: src, env: env}
}

// luma is color.GrayModel for 8-bit colours.
func luma(r, g, b uint8) uint8 {
	return uint8((19595*uint32(r)*0x101 + 38470*uint32(g)*0x101 + 7471*uint32(b)*0x101 + 1<<15) >> 24)
}

type grayscale struct {
	src      pipeline.Source
	env      *pipeline.Env
	buf      []uint8
	reserved int64
	lut      [256]uint8 // the palette of a paletted source, as grey
	lutFor   color.Palette
}

func (g *grayscale) Env() *pipeline.Env      { return g.env }
func (g *grayscale) Bounds() image.Rectangle { return g.src.Bounds() }
func (g *grayscale) ColorModel() color.Model { return color.GrayModel }
func (g *grayscale) Opaque() bool            { return true }

func (g *grayscale) Close() error {
	g.env.Budget.Release(g.reserved)
	g.reserved, g.buf = 0, nil
	return g.src.Close()
}

func (g *grayscale) Next() (image.Image, error) {
	s, err := g.src.Next()
	if err != nil {
		return nil, err
	}
	r := s.Bounds()
	if gs, ok := s.(*image.Gray); ok {
		return gs, nil
	}
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
		switch p := s.(type) {
		case *image.YCbCr:
			copy(d, p.Y[p.YOffset(r.Min.X, y):][:w])
		case *image.NYCbCrA:
			copy(d, p.Y[p.YOffset(r.Min.X, y):][:w])
		case *image.Gray16:
			sp := p.Pix[p.PixOffset(r.Min.X, y):][:2*w]
			for x := range d {
				d[x] = sp[2*x]
			}
		case *image.RGBA:
			sp := p.Pix[p.PixOffset(r.Min.X, y):][:4*w]
			for x := range d {
				d[x] = luma(sp[4*x], sp[4*x+1], sp[4*x+2])
			}
		case *image.NRGBA:
			sp := p.Pix[p.PixOffset(r.Min.X, y):][:4*w]
			for x := range d {
				d[x] = luma(sp[4*x], sp[4*x+1], sp[4*x+2])
			}
		case *image.Paletted:
			if len(g.lutFor) != len(p.Palette) || len(p.Palette) > 0 && &g.lutFor[0] != &p.Palette[0] {
				g.lutFor = p.Palette
				for i, c := range p.Palette {
					if i > 255 {
						break
					}
					g.lut[i] = color.GrayModel.Convert(c).(color.Gray).Y
				}
			}
			for x, v := range p.Pix[p.PixOffset(r.Min.X, y):][:w] {
				d[x] = g.lut[v]
			}
		default:
			for x := range d {
				d[x] = color.GrayModel.Convert(s.At(r.Min.X+x, y)).(color.Gray).Y
			}
		}
	}
	return out, nil
}

// ---- modulate ----

// Modulate changes the brightness, saturation and hue of src the way sharp
// (libvips) does: in the LCh colour space (CIE L*a*b* under D65 in polar
// form), L is multiplied by brightness, C by saturation, and hue degrees
// are added to h. Transparency is kept. 1, 1, 0 leaves the image as it is.
func Modulate(src pipeline.Source, brightness, saturation, hue float64) (pipeline.Source, error) {
	for _, v := range []float64{brightness, saturation, hue} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("goimgtools: Modulate takes finite numbers")
		}
	}
	if brightness < 0 || saturation < 0 {
		return nil, errors.New("goimgtools: Modulate brightness and saturation are factors, at least 0")
	}
	if brightness == 1 && saturation == 1 && math.Mod(hue, 360) == 0 {
		return src, nil
	}
	t := labTables()
	sin, cos := math.Sincos(hue * math.Pi / 180)
	// Hue turns (a, b) about the grey axis; saturation scales it.
	ka, kb := float32(saturation*cos), float32(saturation*sin)
	br := float32(brightness)
	return pipeline.Map(src, func(strip *image.NRGBA) error {
		var lastIn, lastOut [3]uint8
		have := false
		for i := 0; i < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			if p[3] == 0 {
				continue
			}
			in := [3]uint8{p[0], p[1], p[2]}
			if have && in == lastIn {
				p[0], p[1], p[2] = lastOut[0], lastOut[1], lastOut[2]
				continue
			}
			L, a, b := t.toLab(p[0], p[1], p[2])
			L *= br
			a, b = a*ka-b*kb, a*kb+b*ka
			p[0], p[1], p[2] = t.fromLab(L, a, b)
			lastIn, lastOut, have = in, [3]uint8{p[0], p[1], p[2]}, true
		}
		return nil
	}), nil
}

// labTable holds what converting between 8-bit sRGB and L*a*b* is made
// fast with.
type labTable struct {
	linear [256]float32 // sRGB level to linear light
	// cbrt is f(t) of the L*a*b* definition on [0, 1] in cbrtSteps
	// steps, to interpolate between.
	cbrt [cbrtSteps + 2]float32
	// gamma is linear light on [0, 1] in gammaSteps steps to sRGB levels.
	gamma [gammaSteps + 1]uint8
}

const (
	cbrtSteps  = 4096
	gammaSteps = 1 << 14
	// D65 white.
	whiteX, whiteZ = 0.95047, 1.08883
	labEpsilon     = 216.0 / 24389 // (6/29)^3
	labKappa       = 24389.0 / 27
)

var labOnce = sync.OnceValue(func() *labTable {
	t := &labTable{}
	for i := range t.linear {
		v := float64(i) / 255
		if v <= 0.04045 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		t.linear[i] = float32(v)
	}
	for i := range t.cbrt {
		t.cbrt[i] = float32(labF(float64(i) / cbrtSteps))
	}
	for i := range t.gamma {
		v := float64(i) / gammaSteps
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		t.gamma[i] = uint8(math.Round(math.Max(0, math.Min(1, v)) * 255))
	}
	return t
})

func labTables() *labTable { return labOnce() }

func labF(t float64) float64 {
	if t > labEpsilon {
		return math.Cbrt(t)
	}
	return (labKappa*t + 16) / 116
}

// f is labF, from the table where t is within it.
func (t *labTable) f(v float32) float32 {
	if v < 0 {
		v = 0
	}
	if v >= 1 {
		return float32(labF(float64(v)))
	}
	x := v * cbrtSteps
	i := int(x)
	frac := x - float32(i)
	return t.cbrt[i] + (t.cbrt[i+1]-t.cbrt[i])*frac
}

func fInv(v float32) float32 {
	if v > 6.0/29 {
		return v * v * v
	}
	return (116*v - 16) / labKappa
}

func (t *labTable) toLab(r8, g8, b8 uint8) (L, a, b float32) {
	r, g, bl := t.linear[r8], t.linear[g8], t.linear[b8]
	x := (0.4124564*r + 0.3575761*g + 0.1804375*bl) / whiteX
	y := 0.2126729*r + 0.7151522*g + 0.0721750*bl
	z := (0.0193339*r + 0.1191920*g + 0.9503041*bl) / whiteZ
	fx, fy, fz := t.f(x), t.f(y), t.f(z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

func (t *labTable) fromLab(L, a, b float32) (uint8, uint8, uint8) {
	fy := (L + 16) / 116
	x := whiteX * fInv(fy+a/500)
	y := fInv(fy)
	z := whiteZ * fInv(fy-b/200)
	r := 3.2404542*x - 1.5371385*y - 0.4985314*z
	g := -0.9692660*x + 1.8760108*y + 0.0415560*z
	bl := 0.0556434*x - 0.2040259*y + 1.0572252*z
	return t.level(r), t.level(g), t.level(bl)
}

// level turns linear light into an sRGB level, clipping what is outside
// the gamut.
func (t *labTable) level(v float32) uint8 {
	if !(v > 0) {
		return 0
	}
	if v >= 1 {
		return 255
	}
	return t.gamma[int(v*gammaSteps+0.5)]
}
