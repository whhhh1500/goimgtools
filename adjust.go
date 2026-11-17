package goimgtools

import (
	"cmp"
	"errors"
	"fmt"
	"image/color"
	"math"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The operations that change how an image looks rather than where its
// pixels are, and those that change its canvas: Grayscale, Modulate, Blur,
// Sharpen, Extend, Trim and RotateBy. Like the others they work a few rows
// at a time, except Trim and RotateBy, which have to see the whole image
// first and keep it in working storage.

// Grayscale turns the image into shades of grey: the Rec. 601 brightness
// of each pixel, rounded to the nearest level, as Pillow's convert("L"),
// image/color's RGBToYCbCr and JPEG encoders compute it (Pillow's result
// to the byte; image/color's GrayModel drops a fraction instead and can be
// a level lower); of a JPEG, Pillow's to the byte too, as it is decoded as
// Pillow decodes it. An image without transparency becomes a one-channel greyscale image, which
// a JPEG or PNG then stores as such, smaller; transparency is kept. See
// GrayscaleOptions.Linear for sharp's grey.
func (m *Image) Grayscale(opts ...GrayscaleOptions) *Image {
	m.atMostOne("Grayscale", len(opts))
	return m.add(grayscaleOp{first(opts).Linear})
}

// GrayscaleOptions are the optional settings of Grayscale.
type GrayscaleOptions struct {
	// Linear gives each pixel the grey of the luminance of its linear
	// light (Rec. 709 weights), as sharp's greyscale (libvips' B_W), to the
	// byte: reds and blues come out darker and greens lighter than with
	// Rec. 601 brightness, closer to how light they look.
	Linear bool
}

// ModulateOptions are the optional settings of Modulate.
type ModulateOptions struct {
	// Lightness is added to the lightness (L* of L*a*b*, 0 to 100) after
	// it is multiplied by the brightness, as sharp's lightness: 10 lightens
	// the darkest pixels as much as the others.
	Lightness float64
}

// Modulate changes brightness and saturation by a factor and turns the
// hue by a number of degrees, the way sharp does: in the LCh colour space,
// so that colours keep their character (Modulate(1, 0, 0) gives the
// perceived lightness in grey, not an average of red, green and blue).
// Modulate(1, 1, 0) leaves the image as it is; Modulate(1.2, 0.8, 0) is a
// fifth brighter and less vivid; Modulate(1, 1, 180) gives the opposite
// colours. Transparency is kept. The result is within one level of
// sharp's (0.35) on every pixel.
func (m *Image) Modulate(brightness, saturation, hue float64, opts ...ModulateOptions) *Image {
	m.atMostOne("Modulate", len(opts))
	lightness := first(opts).Lightness
	for _, v := range []float64{brightness, saturation, hue, lightness} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return m.fail(errors.New("goimgtools: Modulate takes finite numbers"))
		}
	}
	if brightness < 0 || saturation < 0 {
		return m.fail(errors.New("goimgtools: Modulate brightness and saturation are factors, at least 0"))
	}
	return m.add(modulateOp{brightness, saturation, hue, lightness})
}

// Blur blurs the image with a Gaussian of standard deviation sigma pixels,
// above 0 and at most 1000 (as sharp). By default it is Pillow's
// GaussianBlur, and for an image without transparency the result is
// Pillow's to the byte; BlurOptions.Method BlurKernel makes it sharp's, to
// the byte. Either way the time it takes hardly grows with sigma, the
// memory it takes grows with sigma times the width of the image and goes
// to a temporary file beyond its share of the limit, and transparent
// pixels lend no colour to their neighbours.
func (m *Image) Blur(sigma float64, opts ...BlurOptions) *Image {
	m.atMostOne("Blur", len(opts))
	if !(sigma > 0) || sigma > ops.MaxSigma {
		return m.fail(errors.New("goimgtools: Blur sigma must be above 0 and at most 1000"))
	}
	o := first(opts)
	switch {
	case o.Method != BlurBoxes && o.Method != BlurKernel:
		return m.fail(fmt.Errorf("goimgtools: unknown BlurMethod %d", o.Method))
	case o.MinAmplitude != 0 && o.Method != BlurKernel:
		return m.fail(errors.New("goimgtools: Blur MinAmplitude is for BlurKernel"))
	case o.MinAmplitude != 0 && !(o.MinAmplitude >= 0.001 && o.MinAmplitude <= 1):
		return m.fail(errors.New("goimgtools: Blur MinAmplitude must be 0.001 to 1"))
	}
	return m.add(blurOp{sigma, o})
}

// BlurOptions are the optional settings of Blur.
type BlurOptions struct {
	// Method is the way the Gaussian is worked out: BlurBoxes, Pillow's
	// (the default), or BlurKernel, sharp's.
	Method BlurMethod
	// MinAmplitude is where BlurKernel cuts the Gaussian off: where it
	// falls below this fraction of its peak, 0.001 to 1, as sharp's
	// minAmplitude; 0 means sharp's default, 0.2. Smaller makes the edges
	// of the blur smoother and it a little slower.
	MinAmplitude float64
}

// BlurMethod is a way of working out a Gaussian blur.
type BlurMethod int

const (
	// BlurBoxes is Pillow's GaussianBlur: three box blurs across and three
	// down, of the width that comes closest to the Gaussian, each rounded
	// to 8 bits. For an image without transparency the result is Pillow's
	// to the byte.
	BlurBoxes BlurMethod = iota
	// BlurKernel is sharp's blur (libvips' gaussblur at its default
	// integer precision): the Gaussian itself, its weights twentieths of
	// its peak rounded and cut off at MinAmplitude, across and then down,
	// each rounded to 8 bits. The result is sharp's to the byte,
	// transparency included. It blurs less than BlurBoxes at the same
	// sigma, the mask being cut off at 1.8 sigma by default.
	BlurKernel
)

// SharpenOptions are the optional settings of Sharpen.
type SharpenOptions struct {
	// Sigma, when set, makes Sharpen an unsharp mask of this radius (the
	// standard deviation of its blur, in pixels, above 0 and at most
	// 1000), as Pillow's ImageFilter.UnsharpMask. Without it Sharpen is
	// sharp's fast, mild sharpen.
	Sigma float64
	// Amount is how strongly edges are lifted, as a factor: 1.5 (the
	// default) adds one and a half times the difference to the blur.
	Amount float64
	// Threshold leaves differences of up to this much alone (0 to 255), so
	// that flat areas do not get grainy. The default is 0: every difference
	// is sharpened. Pillow's UnsharpMask uses 3.
	Threshold int
	// Method is the unsharp mask a Sigma makes: SharpenChannels (the
	// default), Pillow's UnsharpMask, which sharpens red, green and blue
	// each with Amount and Threshold; or SharpenLightness, sharp's sharpen
	// with a sigma, which sharpens the lightness alone (L* of L*a*b*), so
	// that colours do not shift, by its Curve.
	Method SharpenMethod
	// Curve is how SharpenLightness turns the difference of the lightness
	// to its blur into the change it makes; nil is sharp's default
	// (DefaultSharpenCurve).
	Curve *SharpenCurve
}

// SharpenMethod is a way Sharpen sharpens with a Sigma.
type SharpenMethod int

const (
	// SharpenChannels is Pillow's UnsharpMask: red, green and blue each
	// sharpened by Amount where they differ from their blur by more than
	// Threshold.
	SharpenChannels SharpenMethod = iota
	// SharpenLightness is sharp's sharpen with a sigma (libvips' sharpen):
	// the lightness alone, by SharpenCurve, colours unchanged. The result
	// is sharp's, within a level on a few pixels (where sharp's own result
	// depends on the system's C library).
	SharpenLightness
)

// SharpenCurve is how SharpenLightness maps the difference of a pixel's
// lightness to its blur (in L* units, 0 to 100) to the lightness it adds:
// Flat times the difference up to JaggedFrom either way, Jagged times what
// lies beyond, at most MaxBrighten and MaxDarken in all. The fields are
// sharp's m1, m2, x1, y2 and y3, each at least 0.
type SharpenCurve struct {
	Flat, Jagged, JaggedFrom, MaxBrighten, MaxDarken float64
}

// DefaultSharpenCurve is sharp's: flat areas sharpened by 1, jagged ones
// (differences of more than 2) by 2, at most 10 lighter and 20 darker.
var DefaultSharpenCurve = SharpenCurve{Flat: 1, Jagged: 2, JaggedFrom: 2, MaxBrighten: 10, MaxDarken: 20}

// Sharpen makes the image crisper. Without options it is sharp's mild
// sharpen, a 3x3 kernel, cheap and the usual choice after making an image
// smaller. With a Sigma it is an unsharp mask (see SharpenOptions): for an
// image without transparency the result is that of Pillow's UnsharpMask,
// to the byte (Amount counts in whole percent, as there); with Method
// SharpenLightness, sharp's sharpen with a sigma.
func (m *Image) Sharpen(opts ...SharpenOptions) *Image {
	m.atMostOne("Sharpen", len(opts))
	o := first(opts)
	if o.Sigma != 0 && (!(o.Sigma > 0) || o.Sigma > ops.MaxSigma) {
		return m.fail(errors.New("goimgtools: Sharpen sigma must be above 0 and at most 1000"))
	}
	if o.Sigma == 0 && (o.Amount != 0 || o.Threshold != 0) {
		return m.fail(errors.New("goimgtools: Sharpen Amount and Threshold go with a Sigma"))
	}
	if !(o.Amount >= 0) || o.Amount > 100 {
		return m.fail(errors.New("goimgtools: Sharpen Amount must be 0 to 100"))
	}
	if o.Threshold < 0 || o.Threshold > 255 {
		return m.fail(errors.New("goimgtools: Sharpen Threshold must be 0 to 255"))
	}
	switch o.Method {
	case SharpenChannels:
		if o.Curve != nil {
			return m.fail(errors.New("goimgtools: Sharpen Curve goes with SharpenLightness"))
		}
		if o.Sigma > 0 && o.Amount == 0 {
			o.Amount = 1.5
		}
	case SharpenLightness:
		if o.Sigma == 0 || o.Amount != 0 || o.Threshold != 0 {
			return m.fail(errors.New("goimgtools: SharpenLightness takes a Sigma and a Curve, not Amount and Threshold"))
		}
		if o.Curve == nil {
			c := DefaultSharpenCurve
			o.Curve = &c
		}
		c := *o.Curve
		o.Curve = &c // the caller's may change
		for _, v := range []float64{c.Flat, c.Jagged, c.JaggedFrom, c.MaxBrighten, c.MaxDarken} {
			if !(v >= 0) || math.IsInf(v, 0) {
				return m.fail(errors.New("goimgtools: SharpenCurve takes finite numbers, at least 0"))
			}
		}
	default:
		return m.fail(fmt.Errorf("goimgtools: unknown SharpenMethod %d", o.Method))
	}
	return m.add(sharpenOp{o})
}

// Extend adds a border of the given widths (top, right, bottom, left, in
// pixels, as CSS writes them) in the background colour around the image;
// nil is black, color.Transparent a transparent border.
func (m *Image) Extend(top, right, bottom, left int, background color.Color) *Image {
	if top < 0 || right < 0 || bottom < 0 || left < 0 {
		return m.fail(errors.New("goimgtools: Extend takes widths of 0 and more"))
	}
	if background == nil {
		background = color.Black
	}
	return m.add(extendOp{top: top, right: right, bottom: bottom, left: left, background: background})
}

// Extension is how ExtendWith fills a border from the image.
type Extension int

const (
	// ExtendCopy repeats the pixels at the edges.
	ExtendCopy Extension = iota
	// ExtendRepeat repeats the image, as tiles.
	ExtendRepeat
	// ExtendMirror mirrors the image at its edges (the edge pixel taken
	// twice), and again further out.
	ExtendMirror
)

// ExtendWith adds a border of the given widths (top, right, bottom, left,
// in pixels) filled from the image itself, as sharp's extend with
// extendWith copy, repeat or mirror (to the byte): see Extension. A
// border wider than the image goes on repeating or mirroring it. A top or
// bottom border that repeats or mirrors the image keeps the image in
// working storage (a temporary file beyond its share of the limit).
func (m *Image) ExtendWith(top, right, bottom, left int, how Extension) *Image {
	if top < 0 || right < 0 || bottom < 0 || left < 0 {
		return m.fail(errors.New("goimgtools: ExtendWith takes widths of 0 and more"))
	}
	if how < ExtendCopy || how > ExtendMirror {
		return m.fail(errors.New("goimgtools: unknown Extension"))
	}
	return m.add(extendOp{top: top, right: right, bottom: bottom, left: left, with: how, from: true})
}

// TrimOptions are the optional settings of Trim.
type TrimOptions struct {
	// Background is the colour of the edges to cut away; nil: that of the
	// top-left pixel.
	Background color.Color
	// LineArt compares the pixels as they are. By default the image is
	// first smoothed with a 3x3 median filter (only to find the edges; the
	// pixels kept are not changed), so that noise, JPEG artefacts and
	// specks of dust in the edges do not stop the trim, as sharp does; that
	// filter also removes lines one pixel thin, which a drawing (line art)
	// may be made of: LineArt keeps them.
	LineArt bool
	// Margin keeps this many pixels of the edges around what is found
	// (within the image).
	Margin int
}

// Trim cuts away the edges that are the colour of the top-left pixel (or
// TrimOptions.Background), as sharp's trim (libvips' find_trim): the rows
// and columns at the edges in which no pixel differs from it by more than
// threshold (0 to 255) in red, green, blue or alpha, after a 3x3 median
// filter unless TrimOptions.LineArt (see there). 10, sharp's default, also
// passes over the slight noise of a JPEG; 0 takes only exactly that
// colour. An image of one colour is left as it is.
//
// Which edges go is only known once the whole image has been seen: it is
// kept in working storage (a temporary file beyond its share of the limit)
// and read once more. For the same reason OutputSize reads the pixels when
// the recipe has a Trim.
func (m *Image) Trim(threshold float64, opts ...TrimOptions) *Image {
	m.atMostOne("Trim", len(opts))
	if !(threshold >= 0 && threshold <= 255) {
		return m.fail(errors.New("goimgtools: Trim threshold must be 0 to 255"))
	}
	o := first(opts)
	if o.Margin < 0 {
		return m.fail(errors.New("goimgtools: Trim Margin cannot be negative"))
	}
	return m.add(trimOp{threshold, o})
}

// RotateBy turns the image clockwise by any number of degrees (negative:
// anticlockwise), as sharp's rotate, to the byte, with and without
// transparency: an Affine by the matrix (cos, -sin; sin, cos), its sine
// and cosine rounded as libvips' are. The canvas is the box around the
// turned image (each side rounded to the nearest pixel); the corners are
// the background colour, nil for black, color.Transparent for none. The
// pixels are interpolated bilinearly. A multiple of 90 degrees is a plain
// quarter or half turn instead, exact, as Rotate does it (and sharp).
//
// The whole image is kept in working storage first (a temporary file beyond
// its share of the limit), as for a quarter turn.
func (m *Image) RotateBy(degrees float64, background color.Color) *Image {
	if math.IsNaN(degrees) || math.IsInf(degrees, 0) {
		return m.fail(errors.New("goimgtools: RotateBy takes a finite angle"))
	}
	turn := math.Mod(degrees, 360)
	if turn < 0 {
		turn += 360
	}
	for _, q := range []struct {
		deg float64
		o   ops.Orientation
	}{{0, ops.Upright}, {90, ops.Rotate90}, {180, ops.Rotate180}, {270, ops.Rotate270}, {360, ops.Upright}} {
		if turn == q.deg {
			return m.add(turnOp{o: q.o})
		}
	}
	if background == nil {
		background = color.Black
	}
	return m.add(rotateAnyOp{degrees, background})
}

// ---- the operations ----

type grayscaleOp struct{ linear bool }

func (o grayscaleOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	if o.linear {
		return ops.GrayscaleLinear(src), nil
	}
	return ops.Grayscale(src), nil
}

type modulateOp struct{ brightness, saturation, hue, lightness float64 }

func (o modulateOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Modulate(src, o.brightness, o.saturation, o.hue, o.lightness)
}

type blurOp struct {
	sigma float64
	o     BlurOptions
}

func (o blurOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	if o.o.Method == BlurKernel {
		return ops.BlurKernel(src, o.sigma, cmp.Or(o.o.MinAmplitude, ops.MinAmplitude))
	}
	return ops.Blur(src, o.sigma)
}

type sharpenOp struct{ o SharpenOptions }

func (o sharpenOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	if o.o.Method == SharpenLightness {
		c := o.o.Curve
		return ops.SharpenLab(src, o.o.Sigma, c.Flat, c.Jagged, c.JaggedFrom, c.MaxBrighten, c.MaxDarken)
	}
	return ops.Sharpen(src, o.o.Sigma, int(math.Round(o.o.Amount*100)), o.o.Threshold)
}

type extendOp struct {
	top, right, bottom, left int
	background               color.Color
	// from: filled from the image (ExtendWith), as with says.
	from bool
	with Extension
}

func (o extendOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	b := src.Bounds()
	if err := r.fits(b.Dx()+o.left+o.right, b.Dy()+o.top+o.bottom); err != nil || int64(b.Dx())+int64(o.left)+int64(o.right) > 1<<24 || int64(b.Dy())+int64(o.top)+int64(o.bottom) > 1<<24 {
		if err == nil {
			err = errors.New("goimgtools: Extend makes the image too large")
		}
		return nil, err
	}
	if o.from && !r.sizeOnly {
		return ops.ExtendWith(src, o.left, o.top, o.right, o.bottom, int(o.with))
	}
	bg := o.background
	if bg == nil {
		bg = color.Black
	}
	return ops.Extend(src, o.left, o.top, o.right, o.bottom, bg)
}

type trimOp struct {
	threshold float64
	o         TrimOptions
}

func (o trimOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	t := ops.TrimOptions{LineArt: o.o.LineArt, Margin: o.o.Margin}
	if o.o.Background != nil {
		c := color.NRGBAModel.Convert(o.o.Background).(color.NRGBA)
		t.Background = &[4]uint8{c.R, c.G, c.B, c.A}
	}
	return ops.Trim(src, o.threshold, t)
}

// words are the arguments of the trim step: THRESHOLD, then BACKGROUND
// (a colour or auto), MARGIN and lineart as far as they are set.
func (o trimOp) words() []string {
	w := []string{strconv.FormatFloat(o.threshold, 'g', -1, 64)}
	if o.o.Background == nil && o.o.Margin == 0 && !o.o.LineArt {
		return w
	}
	bg := "auto"
	if o.o.Background != nil {
		bg = colorWord(o.o.Background)
	}
	w = append(w, bg, strconv.Itoa(o.o.Margin))
	if o.o.LineArt {
		w = append(w, "lineart")
	}
	return w
}

// AffineOptions are the optional settings of Affine.
type AffineOptions struct {
	// Background fills what the moved image leaves of its canvas; nil is
	// black.
	Background color.Color
	// InputX and InputY move the image before the matrix is applied,
	// OutputX and OutputY after it, in pixels (sharp's idx, idy, odx and
	// ody); the canvas stays the same.
	InputX, InputY, OutputX, OutputY float64
	// Interpolator works out the colours between pixels: bilinear, sharp's
	// default, or nearest or bicubic, also sharp's to the byte.
	Interpolator Interpolator
}

// Interpolator is how Affine works out the colour at a point between
// pixels.
type Interpolator int

const (
	// InterpolateBilinear weighs the four pixels around the point.
	InterpolateBilinear Interpolator = iota
	// InterpolateNearest takes the pixel the point falls in: blocky, no
	// new colours.
	InterpolateNearest
	// InterpolateBicubic weighs the sixteen pixels around the point
	// (Catmull-Rom): sharper than bilinear, with a little ringing.
	InterpolateBicubic
)

var interpolatorNames = map[Interpolator]string{InterpolateBilinear: "bilinear", InterpolateNearest: "nearest", InterpolateBicubic: "bicubic"}

// Affine moves the pixels of the image by the matrix (a b; c d): the point
// (x, y) goes to (a·x + b·y, c·x + d·y). It is sharp's affine with its
// default bilinear interpolation, to the byte, with and without
// transparency. The canvas is the box around the moved image: Affine(1, 0,
// 0, 1) leaves the image as it is, Affine(-1, 0, 0, 1) mirrors it, (cos θ,
// -sin θ, sin θ, cos θ) turns it, Affine(1, 0.3, 0, 1) slants it and
// Affine(1.5, 0, 0, 1.5) enlarges it (Resize does that better). The matrix
// must not be singular. As sharp, the matrix takes the corners of pixels,
// not their centres.
//
// The whole image is kept in working storage first (a temporary file
// beyond its share of the limit), as for RotateBy.
func (m *Image) Affine(a, b, c, d float64, opts ...AffineOptions) *Image {
	m.atMostOne("Affine", len(opts))
	o := first(opts)
	for _, v := range []float64{a, b, c, d, o.InputX, o.InputY, o.OutputX, o.OutputY} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return m.fail(errors.New("goimgtools: Affine takes finite numbers"))
		}
	}
	if o.Background == nil {
		o.Background = color.Black
	}
	if _, ok := interpolatorNames[o.Interpolator]; !ok {
		return m.fail(fmt.Errorf("goimgtools: unknown Interpolator %d", o.Interpolator))
	}
	return m.add(affineOp{[4]float64{a, b, c, d}, o})
}

type affineOp struct {
	m [4]float64
	o AffineOptions
}

func (o affineOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	a, b, c, d := o.m[0], o.m[1], o.m[2], o.m[3]
	p := o.o
	if a == 1 && b == 0 && c == 0 && d == 1 && p.InputX == 0 && p.InputY == 0 && p.OutputX == 0 && p.OutputY == 0 {
		return src, nil // libvips copies the image
	}
	interp := map[Interpolator]int{InterpolateBilinear: ops.AffineBilinear, InterpolateNearest: ops.AffineNearest,
		InterpolateBicubic: ops.AffineBicubic}[p.Interpolator]
	return ops.Affine(src, a, b, c, d, p.InputX, p.InputY, p.OutputX, p.OutputY, p.Background, interp)
}

func (o affineOp) words() []string {
	f := func(v float64) string { return strconv.FormatFloat(v, 'g', -1, 64) }
	w := []string{f(o.m[0]), f(o.m[1]), f(o.m[2]), f(o.m[3]), colorWord(o.o.Background)}
	if p := o.o; p.InputX != 0 || p.InputY != 0 || p.OutputX != 0 || p.OutputY != 0 {
		w = append(w, f(p.InputX), f(p.InputY), f(p.OutputX), f(p.OutputY))
	}
	if o.o.Interpolator != InterpolateBilinear {
		w = append(w, interpolatorNames[o.o.Interpolator])
	}
	return w
}

type rotateAnyOp struct {
	deg        float64
	background color.Color
}

func (o rotateAnyOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	sin, cos := ops.SinCosDegrees(o.deg)
	return ops.Affine(src, cos, -sin, sin, cos, 0, 0, 0, 0, o.background, ops.AffineBilinear)
}
