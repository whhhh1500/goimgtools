package goimgtools

import (
	"errors"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Pillow's ImageOps (posterize, solarize, equalize, autocontrast) and
// ImageEnhance (Color, Contrast, Brightness, Sharpness), to the byte. They
// change red, green and blue and keep transparency (Pillow's ImageOps
// refuses images with it).

// Posterize keeps the bits highest bits (1 to 8) of each red, green and
// blue level: fewer bits, fewer levels. Pillow's ImageOps.posterize.
func (m *Image) Posterize(bits int) *Image {
	if bits < 1 || bits > 8 {
		return m.fail(errors.New("goimgtools: Posterize keeps 1 to 8 bits"))
	}
	return m.add(pillowOp{name: "posterize", n: bits})
}

// Solarize inverts each red, green and blue level at or above threshold
// (0 to 256; Pillow's default is 128), as an overexposed photograph.
// Pillow's ImageOps.solarize.
func (m *Image) Solarize(threshold int) *Image {
	if threshold < 0 || threshold > 256 {
		return m.fail(errors.New("goimgtools: Solarize threshold is 0 to 256"))
	}
	return m.add(pillowOp{name: "solarize", n: threshold})
}

// Equalize spreads the levels of each of red, green and blue so that each
// is about as frequent as the others: Pillow's ImageOps.equalize. The
// levels are counted over the whole image first, which is kept meanwhile
// (as Normalize keeps it).
func (m *Image) Equalize() *Image {
	return m.add(pillowOp{name: "equalize"})
}

// AutoContrastOptions are the optional settings of AutoContrast.
type AutoContrastOptions struct {
	// CutoffLow and CutoffHigh are the percentages (0 to 100) of the
	// pixels left out at the dark and at the light end (Pillow's cutoff),
	// so that a few outliers do not hold the stretch back. 0 by default.
	CutoffLow, CutoffHigh float64
	// Ignore are levels (0 to 255) not counted, such as a background
	// (Pillow's ignore).
	Ignore []int
	// PreserveTone stretches the lightness (Pillow's convert("L")) with one
	// table for the three channels, instead of each channel on its own, so
	// that the colours do not shift.
	PreserveTone bool
}

// AutoContrast stretches each of red, green and blue so that its darkest
// level becomes 0 and its lightest 255: Pillow's ImageOps.autocontrast.
// The levels are counted over the whole image first, which is kept
// meanwhile (as Normalize keeps it).
func (m *Image) AutoContrast(opts ...AutoContrastOptions) *Image {
	m.atMostOne("AutoContrast", len(opts))
	o := first(opts)
	o.Ignore = append([]int(nil), o.Ignore...)
	return m.add(pillowOp{name: "autocontrast", ac: o})
}

// Enhancement is what Enhance changes.
type Enhancement int

const (
	// EnhanceColor is the saturation: 0 grey, 1 the image, more more
	// colourful (Pillow's ImageEnhance.Color).
	EnhanceColor Enhancement = iota
	// EnhanceContrast is the contrast: 0 a grey of the mean lightness, 1
	// the image (ImageEnhance.Contrast).
	EnhanceContrast
	// EnhanceBrightness is the brightness: 0 black, 1 the image
	// (ImageEnhance.Brightness).
	EnhanceBrightness
	// EnhanceSharpness is the sharpness: 0 smoothed (Pillow's SMOOTH
	// filter), 1 the image, 2 sharpened (ImageEnhance.Sharpness).
	EnhanceSharpness
)

// Enhance changes the colour, contrast, brightness or sharpness of the
// image by factor, which may be any number, 1 leaving it as it is: each
// level is factor of the way from a degenerate image (grey, a grey of the
// mean lightness, black, smoothed) to the image's, beyond 1 further on.
// Pillow's ImageEnhance, to the byte (as torchvision's ColorJitter and
// adjust_* functions use on images). With EnhanceContrast the image is
// kept whole meanwhile, to measure its mean lightness first.
func (m *Image) Enhance(e Enhancement, factor float64) *Image {
	if e < EnhanceColor || e > EnhanceSharpness {
		return m.fail(errors.New("goimgtools: unknown Enhancement"))
	}
	return m.add(pillowOp{name: "enhance", n: int(e), f: factor})
}

// ImageFilter is one of Pillow's built-in filters, for Filter.
type ImageFilter int

const (
	// FilterBlur is ImageFilter.BLUR: the 5 x 5 ring around the pixel.
	FilterBlur ImageFilter = iota
	// FilterContour is CONTOUR: dark lines on white where the image
	// changes.
	FilterContour
	// FilterDetail is DETAIL: a light sharpening.
	FilterDetail
	// FilterEdgeEnhance and FilterEdgeEnhanceMore are EDGE_ENHANCE and
	// EDGE_ENHANCE_MORE: edges made stronger.
	FilterEdgeEnhance
	FilterEdgeEnhanceMore
	// FilterEmboss is EMBOSS: relief on grey.
	FilterEmboss
	// FilterFindEdges is FIND_EDGES: light edges on black.
	FilterFindEdges
	// FilterSharpen is SHARPEN.
	FilterSharpen
	// FilterSmooth and FilterSmoothMore are SMOOTH and SMOOTH_MORE.
	FilterSmooth
	FilterSmoothMore
)

// Filter applies one of Pillow's built-in filters, as im.filter(f) does,
// to the byte (see ConvolvePillow: the edge pixels stay, and with
// transparency alpha is filtered too). The memory it takes is a few rows.
func (m *Image) Filter(f ImageFilter) *Image {
	if f < FilterBlur || f > FilterSmoothMore {
		return m.fail(errors.New("goimgtools: unknown ImageFilter"))
	}
	return m.add(pillowOp{name: "filter", n: int(f)})
}

var filterNames = map[ImageFilter]string{FilterBlur: "blur", FilterContour: "contour", FilterDetail: "detail",
	FilterEdgeEnhance: "edge-enhance", FilterEdgeEnhanceMore: "edge-enhance-more", FilterEmboss: "emboss",
	FilterFindEdges: "find-edges", FilterSharpen: "sharpen", FilterSmooth: "smooth", FilterSmoothMore: "smooth-more"}

// pillowOp is Posterize, Solarize, Equalize, AutoContrast, Enhance or
// Filter.
type pillowOp struct {
	name string
	n    int
	f    float64
	ac   AutoContrastOptions
}

func (o pillowOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	switch o.name {
	case "posterize":
		return ops.Levels(src, ops.PosterizeLUT(o.n)), nil
	case "solarize":
		return ops.Levels(src, ops.SolarizeLUT(o.n)), nil
	case "equalize":
		return ops.Equalize(src)
	case "autocontrast":
		return ops.AutoContrast(src, o.ac.CutoffLow, o.ac.CutoffHigh, o.ac.Ignore, o.ac.PreserveTone)
	case "filter":
		return ops.PillowFilter(src, o.n)
	}
	return ops.Enhance(src, o.n, o.f)
}

var enhanceNames = map[Enhancement]string{EnhanceColor: "color", EnhanceContrast: "contrast",
	EnhanceBrightness: "brightness", EnhanceSharpness: "sharpness"}

func (o pillowOp) words() []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	switch o.name {
	case "posterize", "solarize":
		return []string{strconv.Itoa(o.n)}
	case "autocontrast":
		mode := "channels"
		if o.ac.PreserveTone {
			mode = "tone"
		}
		w := []string{f(o.ac.CutoffLow), f(o.ac.CutoffHigh), mode}
		for _, v := range o.ac.Ignore {
			w = append(w, strconv.Itoa(v))
		}
		return w
	case "enhance":
		return []string{enhanceNames[Enhancement(o.n)], f(o.f)}
	case "filter":
		return []string{filterNames[ImageFilter(o.n)]}
	}
	return nil
}

var pillowOps = map[string]OperationDef{
	"filter": {Name: "filter", Usage: "NAME", Help: "Filter: one of Pillow's built-in filters: blur, contour, detail, edge-enhance, edge-enhance-more, emboss, find-edges, sharpen, smooth, smooth-more",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			f, err := pick(filterNames, a[0], "filter")
			if err != nil {
				return err
			}
			m.Filter(f)
			return nil
		}},
	"posterize": {Name: "posterize", Usage: "BITS", Help: "Posterize: keep the BITS (1-8) highest bits of each level (Pillow's ImageOps.posterize)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			v, err := argInts(a)
			if err != nil {
				return err
			}
			m.Posterize(v[0])
			return nil
		}},
	"solarize": {Name: "solarize", Usage: "[THRESHOLD]", Help: "Solarize: invert the levels at or above THRESHOLD (128) (Pillow's ImageOps.solarize)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 1); err != nil {
				return err
			}
			t := 128
			if len(a) == 1 {
				v, err := argInts(a)
				if err != nil {
					return err
				}
				t = v[0]
			}
			m.Solarize(t)
			return nil
		}},
	"equalize": {Name: "equalize", Help: "Equalize: spread the levels of each channel evenly (Pillow's ImageOps.equalize)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 0, 0); err != nil {
				return err
			}
			m.Equalize()
			return nil
		}},
	"autocontrast": {Name: "autocontrast", Usage: "[LOW[,HIGH[,channels|tone[,IGNORE...]]]]",
		Help: "AutoContrast: stretch each channel (or with tone the lightness) to black and white, leaving out LOW and HIGH percent of the pixels at either end and the levels IGNORE (Pillow's ImageOps.autocontrast)",
		builtIn: func(m *Image, a []string) error {
			var o AutoContrastOptions
			if len(a) > 0 {
				v, err := argFloats(a[:1])
				if err != nil {
					return err
				}
				o.CutoffLow, o.CutoffHigh = v[0], v[0]
			}
			if len(a) > 1 {
				v, err := argFloats(a[1:2])
				if err != nil {
					return err
				}
				o.CutoffHigh = v[0]
			}
			if len(a) > 2 {
				switch strings.ToLower(a[2]) {
				case "channels":
				case "tone":
					o.PreserveTone = true
				default:
					return errors.New("want channels or tone, got " + strconv.Quote(a[2]))
				}
			}
			if len(a) > 3 {
				v, err := argInts(a[3:])
				if err != nil {
					return err
				}
				o.Ignore = v
			}
			m.AutoContrast(o)
			return nil
		}},
	"enhance": {Name: "enhance", Usage: "color|contrast|brightness|sharpness,FACTOR",
		Help: "Enhance: change the colour, contrast, brightness or sharpness by FACTOR, 1 for none (Pillow's ImageEnhance)",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 2, 2); err != nil {
				return err
			}
			e, err := pick(enhanceNames, a[0], "enhancement")
			if err != nil {
				return err
			}
			v, err := argFloats(a[1:])
			if err != nil {
				return err
			}
			m.Enhance(e, v[0])
			return nil
		}},
}

func init() {
	for n, d := range pillowOps {
		builtInOps[n] = d
	}
}
