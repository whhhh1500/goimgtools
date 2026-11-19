package inspect

import (
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/watermark"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// ReadWatermark looks for a mark hidden with Image.InvisibleWatermark and
// the same key, and returns its bytes; false when there is none, or it is
// too damaged to read. What is returned is practically never a wrong mark:
// each of the six thousand or so readings a search tries at most is checked
// against a 32-bit CRC of the key and the bytes (about one chance in seven
// hundred thousand for a picture without a mark). The original is not
// needed.
//
// The picture may have been saved again, resized, cropped (it must keep
// about two of the mark's repeats each way), turned by any angle, made
// lighter or darker, screenshotted, and, for WatermarkScreen, photographed
// off a screen: the mark's own reference tells its size and angle, and
// where its tiles start, region by region for a photograph's perspective.
// Crop a photograph to the screen first for the best chance.
//
// A picture larger than 1024 pixels on its shorter side (or 2048 on its
// longer) is reduced first. For one of that size the search takes under a
// second when the mark is there (about five when it is not, or is badly
// damaged: more ways are tried, in more places, and a picture in colour is
// read again with its brightness less what its colour explains) and about
// 90MB (105 when nothing is found). It shares its work with the cores img
// may use (Threads, SetDefaultThreads), finding the same: with 8, about 0.5
// and 2 seconds. The operations of img, if it has any, are run first.
func ReadWatermark(img *goimgtools.Image, key string) ([]byte, bool, error) {
	if img == nil {
		return nil, false, errors.New("goimgtools/inspect: ReadWatermark needs an image")
	}
	w, h, err := img.Clone().OutputSize()
	if err != nil {
		return nil, false, err
	}
	m := img.Clone()
	if s := min(1, float64(watermark.WorkingSide)/float64(min(w, h)), float64(2*watermark.WorkingSide)/float64(max(w, h))); s < 1 {
		m.Resize(max(1, int(float64(float64(w)*s)+0.5)), max(1, int(float64(float64(h)*s)+0.5)), goimgtools.ResizeOptions{
			Fit: goimgtools.FitFill, Kernel: goimgtools.KernelBox, Method: goimgtools.MethodPillow, FastShrinkOnLoad: true})
	}
	src, err := m.Stream(nil)
	if err != nil {
		return nil, false, err
	}
	defer func() { src.Close() }()
	// The brightness, and the colour of a picture that has any, are taken
	// from the strips as they come, held against the memory limit; the
	// picture itself is not kept.
	b := src.Bounds()
	lw, lh := b.Dx(), b.Dy()
	env := pipeline.EnvOf(src)
	colour := src.ColorModel() != color.GrayModel && src.ColorModel() != color.Gray16Model
	need := int64(8) * int64(lw) * int64(lh)
	if colour {
		need *= 2
	}
	if err := env.Budget.Reserve(need, "the brightness of the picture (ReadWatermark)"); err != nil {
		return nil, false, err
	}
	defer env.Budget.Release(need)
	l := make([]float64, lw*lh)
	var cb, cr []float32
	if colour {
		cb, cr = make([]float32, lw*lh), make([]float32, lw*lh)
	}
	for {
		strip, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, false, err
		}
		sb := strip.Bounds()
		o := (sb.Min.Y - b.Min.Y) * lw
		if colour {
			luma(l[o:], cb[o:], cr[o:], lw, strip)
		} else {
			luma(l[o:], nil, nil, lw, strip)
		}
	}
	// The search shares its work with the cores the image may use
	// (Threads, SetDefaultThreads); what it finds does not depend on them.
	var par watermark.Parallel
	if env.Helpers() > 0 {
		par = env.Parallel
	}
	k := watermark.NewKey(key)
	payload, ok := watermark.Detect(l, lw, lh, k, par)
	// Not found, the brightness is read again less what its colour explains
	// (see watermark.CleanChroma): more is read cropped and turned, less
	// after a camera's white balance, so both are tried.
	if !ok && colour && watermark.CleanChroma(l, cb, cr, lw, lh) {
		payload, ok = watermark.Detect(l, lw, lh, k, par)
	}
	return payload, ok, nil
}

// luma writes the brightness of each pixel of pic, 0 to 255, of the
// colours as they are (transparency left aside), to l, in rows of w, and,
// when cb and cr are not nil, blue and red less the brightness to them.
func luma(l []float64, cb, cr []float32, w int, pic image.Image) {
	b := pic.Bounds()
	h := b.Dy()
	put := func(i int, r, g, bl float64) {
		v := float64(0.299*r) + float64(0.587*g) + float64(0.114*bl)
		l[i] = v
		if cb != nil {
			cb[i], cr[i] = float32(bl-v), float32(r-v)
		}
	}
	switch p := pic.(type) {
	case *image.NRGBA:
		for y := range h {
			row := p.Pix[y*p.Stride:]
			for x := range w {
				put(y*w+x, float64(row[4*x]), float64(row[4*x+1]), float64(row[4*x+2]))
			}
		}
	case *image.Gray:
		for y := range h {
			for x := range w {
				l[y*w+x] = float64(p.Pix[y*p.Stride+x])
				if cb != nil {
					cb[y*w+x], cr[y*w+x] = 0, 0
				}
			}
		}
	default:
		for y := range h {
			for x := range w {
				r, g, bl, a := pic.At(b.Min.X+x, b.Min.Y+y).RGBA()
				if a > 0 && a < 0xffff {
					// Back to the colours as they are.
					r, g, bl = r*0xffff/a, g*0xffff/a, bl*0xffff/a
				}
				put(y*w+x, float64(r)/257, float64(g)/257, float64(bl)/257)
			}
		}
	}
}
