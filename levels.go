package goimgtools

import (
	"errors"
	"image/color"
	"math"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The operations on levels and neighbourhoods, after sharp's: Negate,
// Linear, Gamma, Threshold, Tint, Normalize, Median and Convolve. They
// work a few rows at a time, except Normalize, which counts the whole
// image first.

// Negate turns every colour into its opposite: each red, green and blue
// level v becomes 255 - v. Transparency is kept (sharp's negate with alpha
// false, to the byte).
func (m *Image) Negate() *Image { return m.add(levelsOp{name: "negate"}) }

// Linear changes every red, green and blue level v to a*v + b, kept within
// 0 to 255 with its fraction dropped, as sharp's linear (to the byte): a
// above 1 raises the contrast about level 0, b brightens or darkens.
// Linear(1.2, -25) is more contrast about the middle grey. Transparency is
// kept.
func (m *Image) Linear(a, b float64) *Image {
	if _, err := ops.LinearLUT(a, b); err != nil {
		return m.fail(err)
	}
	return m.add(levelsOp{name: "linear", a: a, b: b})
}

// Gamma changes every red, green and blue level v to 255 * (v/255)^(1/g),
// as ImageMagick's -gamma: g above 1 brightens the middle levels, below 1
// darkens them; black and white stay. g is above 0 and at most 100.
// Transparency is kept.
func (m *Image) Gamma(g float64) *Image {
	if !(g > 0 && g <= 100) {
		return m.fail(errors.New("goimgtools: Gamma must be above 0 and at most 100"))
	}
	return m.add(levelsOp{name: "gamma", a: g})
}

// Threshold makes the image black and white, as sharp's threshold (to the
// byte, for an image without transparency): pixels whose grey is level or
// more become white, the others black; the grey is that of their linear
// light, as sharp's (not the brightness Grayscale works out). level is 0
// to 255, sharp's default 128. An image without transparency becomes a
// one-channel greyscale image; transparency is kept (sharp thresholds it
// too).
func (m *Image) Threshold(level int) *Image {
	if level < 0 || level > 255 {
		return m.fail(errors.New("goimgtools: Threshold level must be 0 to 255"))
	}
	return m.add(levelsOp{name: "threshold", a: float64(level)})
}

// Tint gives the image the colour c and keeps the brightness of its
// pixels, as sharp's tint: it is made grey, and each grey becomes c at
// that lightness, less of it towards black and white (which stay). Use it
// for sepia: Tint(color.RGBA{112, 66, 20, 255}). Transparency is kept;
// the alpha of c is not used. The result is within a level or two of
// sharp's.
func (m *Image) Tint(c color.Color) *Image {
	if c == nil {
		return m.fail(errors.New("goimgtools: Tint needs a colour"))
	}
	return m.add(tintOp{c})
}

// NormalizeOptions are the optional settings of Normalize.
type NormalizeOptions struct {
	// Lower and Upper are the percentiles of lightness stretched to black
	// and to white: the lightness below which Lower percent of the pixels
	// lie becomes black, the one above which 100-Upper percent lie white.
	// Both 0 means sharp's defaults, 1 and 99; otherwise 0 <= Lower <
	// Upper <= 100 (0 and 100: the darkest and lightest pixel).
	Lower, Upper float64
}

// Normalize stretches the lightness of the image to the full range, as
// sharp's normalise: in the L*a*b* colour space the lightness is spread
// so that the darkest pixels (but the darkest percent, by default) become
// black and the lightest white, the colours keeping their hue and
// saturation. An image whose lightness spans a level or less is left as
// it is. Transparency is kept.
//
// The lightness is counted over the whole image first: the image is kept
// in working storage (a temporary file beyond its share of the limit) and
// read once more. For the same reason Normalize is done on every frame of
// an animation on its own.
func (m *Image) Normalize(opts ...NormalizeOptions) *Image {
	m.atMostOne("Normalize", len(opts))
	o := first(opts)
	if o.Lower == 0 && o.Upper == 0 {
		o.Lower, o.Upper = 1, 99
	}
	if !(o.Lower >= 0 && o.Lower < o.Upper && o.Upper <= 100) {
		return m.fail(errors.New("goimgtools: Normalize wants 0 <= Lower < Upper <= 100"))
	}
	return m.add(normalizeOp{o})
}

// Median gives each pixel the middle value of the size x size square
// around it, channel by channel, removing specks of noise while keeping
// edges: Pillow's MedianFilter, to the byte (the square reaches past the
// edges onto copies of the edge pixels). size is odd, 3 to 99; sharp's
// default is 3. Transparency is filtered as a channel too, as Pillow does.
// The memory it takes is size rows of the image.
func (m *Image) Median(size int) *Image {
	if size < 3 || size > ops.MaxWindow || size%2 == 0 {
		return m.fail(errors.New("goimgtools: Median size must be odd, 3 to 99"))
	}
	return m.add(medianOp{size})
}

// Convolution is a weighted window for Convolve.
type Convolution struct {
	// Width and Height are the sides of the window, odd, 1 to 99.
	Width, Height int
	// Weights are Width x Height weights, row by row, the first on the
	// top-left pixel of the window.
	Weights []float64
	// Scale divides the weighted sum: when 0, the sum of the weights (1
	// if they sum to 0), as sharp's default.
	Scale float64
	// Offset is added after.
	Offset float64
}

// Convolve gives each pixel the weighted sum of the window around it (see
// Convolution), divided by the scale, plus the offset, rounded and kept
// within 0 to 255, for red, green and blue each, as sharp's convolve:
// {3, 3, []float64{0, -1, 0, -1, 5, -1, 0, -1, 0}, 0, 0} sharpens,
// {3, 3, []float64{-1, -1, -1, -1, 8, -1, -1, -1, -1}, 1, 0} finds edges.
// The window reaches past the edges onto copies of the edge pixels. The
// colours are taken as they are, not multiplied by alpha; transparency is
// kept. The memory it takes is Height rows of the image.
func (m *Image) Convolve(c Convolution) *Image {
	if c.Width < 1 || c.Height < 1 || c.Width > ops.MaxWindow || c.Height > ops.MaxWindow || c.Width%2 == 0 || c.Height%2 == 0 {
		return m.fail(errors.New("goimgtools: Convolve sides must be odd, 1 to 99"))
	}
	if len(c.Weights) != c.Width*c.Height {
		return m.fail(errors.New("goimgtools: Convolve wants Width x Height weights"))
	}
	sum := 0.0
	for _, v := range append(c.Weights[:len(c.Weights):len(c.Weights)], c.Scale, c.Offset) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return m.fail(errors.New("goimgtools: Convolve takes finite numbers"))
		}
	}
	for _, v := range c.Weights {
		sum += v
	}
	if c.Scale == 0 {
		c.Scale = sum
		if sum == 0 {
			c.Scale = 1
		}
	}
	c.Weights = append([]float64(nil), c.Weights...)
	return m.add(convolveOp{c})
}

// ---- the operations ----

// levelsOp is an operation done with a table of levels.
type levelsOp struct {
	name string
	a, b float64
}

func (o levelsOp) lut() *[256]uint8 {
	switch o.name {
	case "negate":
		return ops.NegateLUT()
	case "linear":
		t, _ := ops.LinearLUT(o.a, o.b)
		return t
	}
	t, _ := ops.GammaLUT(o.a)
	return t
}

func (o levelsOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	if o.name == "threshold" {
		return ops.Threshold(src, int(o.a)), nil
	}
	return ops.Levels(src, o.lut()), nil
}

func (o levelsOp) words() []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	switch o.name {
	case "linear":
		return []string{f(o.a), f(o.b)}
	case "gamma", "threshold":
		return []string{f(o.a)}
	}
	return nil
}

type tintOp struct{ c color.Color }

func (o tintOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Tint(src, o.c), nil
}

type normalizeOp struct{ o NormalizeOptions }

func (o normalizeOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Normalize(src, o.o.Lower, o.o.Upper)
}

type medianOp struct{ size int }

func (o medianOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Median(src, o.size)
}

type convolveOp struct{ c Convolution }

func (o convolveOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Convolve(src, o.c.Width, o.c.Height, o.c.Weights, o.c.Scale, o.c.Offset)
}

func (o convolveOp) words() []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	w := []string{strconv.Itoa(o.c.Width), strconv.Itoa(o.c.Height)}
	for _, v := range o.c.Weights {
		w = append(w, f(v))
	}
	return append(w, f(o.c.Scale), f(o.c.Offset))
}

// levelOps are the named operations of this file (see builtInOps).
var levelOps = map[string]OperationDef{
	"negate": {Name: "negate", Help: "Negate: every colour its opposite",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 0); err != nil {
				return err
			}
			m.Negate()
			return nil
		}},
	"linear": {Name: "linear", Usage: "A,B", Help: "Linear: every level v becomes A*v+B",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 2, 2); err != nil {
				return err
			}
			v, err := argFloats(a)
			if err != nil {
				return err
			}
			m.Linear(v[0], v[1])
			return nil
		}},
	"gamma": {Name: "gamma", Usage: "G", Help: "Gamma: above 1 brightens the middle levels, below 1 darkens them",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			v, err := argFloats(a)
			if err != nil {
				return err
			}
			m.Gamma(v[0])
			return nil
		}},
	"threshold": {Name: "threshold", Usage: "[LEVEL]", Help: "Threshold: black and white, white from brightness LEVEL (128 by default)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 1); err != nil {
				return err
			}
			level := 128
			if len(a) == 1 {
				v, err := argInts(a)
				if err != nil {
					return err
				}
				level = v[0]
			}
			m.Threshold(level)
			return nil
		}},
	"tint": {Name: "tint", Usage: "COLOR", Help: "Tint: the colour COLOR at the brightness of each pixel (#704214 for sepia)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			c, err := ParseColor(a[0])
			if err != nil {
				return err
			}
			m.Tint(c)
			return nil
		}},
	"normalize": {Name: "normalize", Usage: "[LOWER,UPPER]", Help: "Normalize: stretch the lightness, percentiles LOWER and UPPER (1 and 99) to black and white",
		builtIn: func(m *Image, a []string) error {
			if len(a) != 0 && len(a) != 2 {
				return wantArgs(a, 2, 2)
			}
			var o NormalizeOptions
			if len(a) == 2 {
				v, err := argFloats(a)
				if err != nil {
					return err
				}
				o.Lower, o.Upper = v[0], v[1]
				if o.Lower == 0 && o.Upper == 0 {
					return errors.New("want 0 <= LOWER < UPPER <= 100")
				}
			}
			m.Normalize(o)
			return nil
		}},
	"median": {Name: "median", Usage: "[SIZE]", Help: "Median: the middle value of the SIZE x SIZE square (3 by default)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 1); err != nil {
				return err
			}
			size := 3
			if len(a) == 1 {
				v, err := argInts(a)
				if err != nil {
					return err
				}
				size = v[0]
			}
			m.Median(size)
			return nil
		}},
	"convolve": {Name: "convolve", Usage: "W,H,WEIGHTS...[,SCALE[,OFFSET]]", Help: "Convolve: a W x H window of weights, row by row",
		builtIn: func(m *Image, a []string) error {
			if len(a) < 3 {
				return wantArgs(a, 3, 3)
			}
			wh, err := argInts(a[:2])
			if err != nil {
				return err
			}
			n := wh[0] * wh[1]
			if wh[0] < 1 || wh[1] < 1 || n > ops.MaxWindow*ops.MaxWindow || len(a) < 2+n || len(a) > 4+n {
				return errors.New("want W,H, then W x H weights, then SCALE and OFFSET if any")
			}
			v, err := argFloats(a[2:])
			if err != nil {
				return err
			}
			c := Convolution{Width: wh[0], Height: wh[1], Weights: v[:n]}
			if len(v) > n {
				c.Scale = v[n]
			}
			if len(v) > n+1 {
				c.Offset = v[n+1]
			}
			m.Convolve(c)
			return nil
		}},
}

func init() {
	for n, d := range levelOps {
		builtInOps[n] = d
	}
}
