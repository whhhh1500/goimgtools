package inspect

import (
	"errors"
	"image"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/watermark"
)

// ReadWatermark looks for a mark hidden with Image.InvisibleWatermark and
// the same key, and returns its bytes; false when there is none, or it is
// too damaged to read. What is returned is practically never a wrong mark:
// each of the thousand or so readings a search tries is checked against a
// 32-bit CRC of the key and the bytes (about one chance in a million for
// a picture without a mark). The original is not needed.
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
// second when the mark is there (about one and a half when it is not, or
// is badly damaged: more ways are tried) and about 80MB. The operations of img, if
// it has any, are run first.
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
	pic, err := m.ToImage()
	if err != nil {
		return nil, false, err
	}
	l, lw, lh := luma(pic)
	payload, ok := watermark.Detect(l, lw, lh, watermark.NewKey(key))
	return payload, ok, nil
}

// luma is the brightness of each pixel, 0 to 255, of the colours as they
// are (transparency left aside).
func luma(pic image.Image) ([]float64, int, int) {
	b := pic.Bounds()
	w, h := b.Dx(), b.Dy()
	l := make([]float64, w*h)
	lum := func(r, g, bl float64) float64 {
		return float64(0.299*r) + float64(0.587*g) + float64(0.114*bl)
	}
	switch p := pic.(type) {
	case *image.NRGBA:
		for y := range h {
			row := p.Pix[(y)*p.Stride:]
			for x := range w {
				l[y*w+x] = lum(float64(row[4*x]), float64(row[4*x+1]), float64(row[4*x+2]))
			}
		}
	case *image.Gray:
		for y := range h {
			for x := range w {
				l[y*w+x] = float64(p.Pix[y*p.Stride+x])
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
				l[y*w+x] = lum(float64(r)/257, float64(g)/257, float64(bl)/257)
			}
		}
	}
	return l, w, h
}
