package goimgtools

import (
	"errors"
	"image/color"
	"math"

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
// a level lower). For a JPEG it is the brightness the file stores. An
// image without transparency becomes a one-channel greyscale image, which
// a JPEG or PNG then stores as such, smaller; transparency is kept.
func (m *Image) Grayscale() *Image { return m.add(grayscaleOp{}) }

// Modulate changes brightness and saturation by a factor and turns the
// hue by a number of degrees, the way sharp does: in the LCh colour space,
// so that colours keep their character (Modulate(1, 0, 0) gives the
// perceived lightness in grey, not an average of red, green and blue).
// Modulate(1, 1, 0) leaves the image as it is; Modulate(1.2, 0.8, 0) is a
// fifth brighter and less vivid; Modulate(1, 1, 180) gives the opposite
// colours. Transparency is kept. The result is within one level of
// sharp's (0.35) on every pixel.
func (m *Image) Modulate(brightness, saturation, hue float64) *Image {
	for _, v := range []float64{brightness, saturation, hue} {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return m.fail(errors.New("goimgtools: Modulate takes finite numbers"))
		}
	}
	if brightness < 0 || saturation < 0 {
		return m.fail(errors.New("goimgtools: Modulate brightness and saturation are factors, at least 0"))
	}
	return m.add(modulateOp{brightness, saturation, hue})
}

// Blur blurs the image with a Gaussian of standard deviation sigma pixels,
// above 0 and at most 1000 (as sharp). It is Pillow's GaussianBlur: three
// box blurs across and three down, so the time it takes does not grow with
// sigma, and for an image without transparency the result is Pillow's to
// the byte. The memory it takes grows with sigma times the width of the
// image, and goes to a temporary file beyond its share of the limit.
// Transparent pixels lend no colour to their neighbours.
func (m *Image) Blur(sigma float64) *Image {
	if !(sigma > 0) || sigma > ops.MaxSigma {
		return m.fail(errors.New("goimgtools: Blur sigma must be above 0 and at most 1000"))
	}
	return m.add(blurOp{sigma})
}

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
}

// Sharpen makes the image crisper. Without options it is sharp's mild
// sharpen, a 3x3 kernel, cheap and the usual choice after making an image
// smaller. With a Sigma it is an unsharp mask (see SharpenOptions): for an
// image without transparency the result is that of Pillow's UnsharpMask,
// to the byte (Amount counts in whole percent, as there).
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
	if o.Sigma > 0 && o.Amount == 0 {
		o.Amount = 1.5
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
	return m.add(extendOp{top, right, bottom, left, background})
}

// Trim cuts away the edges that are the colour of the top-left pixel: the
// rows and columns at the edges in which no pixel differs from it by more
// than threshold (0 to 255) in red, green, blue or alpha. 10, as sharp
// uses, also catches the slight noise of a JPEG; 0 takes only exactly that
// colour. An image of one colour is left as it is.
//
// Which edges go is only known once the whole image has been seen: it is
// kept in working storage (a temporary file beyond its share of the limit)
// and read once more. For the same reason OutputSize reads the pixels when
// the recipe has a Trim.
func (m *Image) Trim(threshold float64) *Image {
	if !(threshold >= 0 && threshold <= 255) {
		return m.fail(errors.New("goimgtools: Trim threshold must be 0 to 255"))
	}
	return m.add(trimOp{threshold})
}

// RotateBy turns the image clockwise by any number of degrees (negative:
// anticlockwise). The canvas grows to hold the whole turned image; the
// corners are the background colour, nil for black, color.Transparent for
// none. The pixels are interpolated bilinearly. A multiple of 90 degrees
// is a plain quarter or half turn instead, exact, as Rotate does it.
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

type grayscaleOp struct{}

func (grayscaleOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Grayscale(src), nil
}

type modulateOp struct{ brightness, saturation, hue float64 }

func (o modulateOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Modulate(src, o.brightness, o.saturation, o.hue)
}

type blurOp struct{ sigma float64 }

func (o blurOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Blur(src, o.sigma)
}

type sharpenOp struct{ o SharpenOptions }

func (o sharpenOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	return ops.Sharpen(src, o.o.Sigma, int(math.Round(o.o.Amount*100)), o.o.Threshold)
}

type extendOp struct {
	top, right, bottom, left int
	background               color.Color
}

func (o extendOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	b := src.Bounds()
	if err := r.fits(b.Dx()+o.left+o.right, b.Dy()+o.top+o.bottom); err != nil || int64(b.Dx())+int64(o.left)+int64(o.right) > 1<<24 || int64(b.Dy())+int64(o.top)+int64(o.bottom) > 1<<24 {
		if err == nil {
			err = errors.New("goimgtools: Extend makes the image too large")
		}
		return nil, err
	}
	return ops.Extend(src, o.left, o.top, o.right, o.bottom, o.background)
}

type trimOp struct{ threshold float64 }

func (o trimOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	return ops.Trim(src, o.threshold)
}

type rotateAnyOp struct {
	deg        float64
	background color.Color
}

func (o rotateAnyOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	return ops.RotateAny(src, o.deg, o.background)
}
