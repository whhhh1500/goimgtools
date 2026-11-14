package webpstream

import (
	"image"
	"image/color"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// A lossy WebP stores luma as 16 to 235 and chroma as 16 to 240 (the
// "limited range" of video), where a JPEG uses all of 0 to 255. Go's
// image.YCbCr, and everything that turns it into RGB, assumes the JPEG
// range. golang.org/x/image/webp hands the stored values out unchanged, so
// its pictures come out with too little contrast: measured against libwebp
// on a 6000x4000 photo, 6 to 8 levels off per channel on average and up to
// 33. FullRange stretches the samples to the range Go expects.

var lumaFull, chromaFull [256]uint8

func init() {
	clamp := func(v int) uint8 {
		return uint8(max(0, min(255, v)))
	}
	for i := range lumaFull {
		// Rounded (x-16)*255/219 and (x-128)*255/224+128.
		lumaFull[i] = clamp(((i-16)*255*2 + 219) / (2 * 219))
		c := (i - 128) * 255
		if c >= 0 {
			c = (c*2 + 224) / (2 * 224)
		} else {
			c = -((-c*2 + 224) / (2 * 224))
		}
		chromaFull[i] = clamp(c + 128)
	}
}

// FullRange returns src with the samples of its YCbCr strips (and the
// YCbCr part of NYCbCrA strips) stretched from WebP's limited range to the
// full range, so that they mean the colours libwebp decodes. Strips of
// other types (a lossless image's NRGBA) pass through.
func FullRange(src pipeline.Source) pipeline.Source {
	if m := src.ColorModel(); m != color.YCbCrModel && m != color.NYCbCrAModel {
		return src
	}
	return &fullRange{src: src, env: pipeline.EnvOf(src)}
}

type fullRange struct {
	src      pipeline.Source
	env      *pipeline.Env
	band     image.Image
	reserved int64
}

func (f *fullRange) Env() *pipeline.Env      { return f.env }
func (f *fullRange) Bounds() image.Rectangle { return f.src.Bounds() }
func (f *fullRange) ColorModel() color.Model { return f.src.ColorModel() }
func (f *fullRange) Opaque() bool            { return pipeline.IsOpaque(f.src) }
func (f *fullRange) Meta() pipeline.Meta     { return pipeline.MetaOf(f.src) }

func (f *fullRange) Close() error {
	f.env.Budget.Release(f.reserved)
	f.reserved, f.band = 0, nil
	return f.src.Close()
}

func mapPlane(dst, src []uint8, lut *[256]uint8) {
	for i, v := range src[:len(dst)] {
		dst[i] = lut[v]
	}
}

func (f *fullRange) Next() (image.Image, error) {
	s, err := f.src.Next()
	if err != nil {
		return nil, err
	}
	var y *image.YCbCr
	switch p := s.(type) {
	case *image.YCbCr:
		y = p
	case *image.NYCbCrA:
		y = &p.YCbCr
	default:
		return s, nil
	}
	r := s.Bounds()
	if f.band == nil || f.band.Bounds().Dy() < r.Dy() {
		f.env.Budget.Release(f.reserved)
		f.reserved, f.band = 0, nil
		full := image.Rect(r.Min.X, 0, r.Max.X, r.Dy())
		need := pipeline.BandBytes(s, full)
		if err := f.env.Budget.Reserve(need, "webp colour range strip"); err != nil {
			return nil, err
		}
		f.reserved, f.band = need, pipeline.NewBand(s, full)
	}
	out := pipeline.Relabel(f.band, r)
	// The rows are copied plane by plane through the tables.
	var oy *image.YCbCr
	switch p := out.(type) {
	case *image.YCbCr:
		oy = p
	case *image.NYCbCrA:
		oy = &p.YCbCr
		src := s.(*image.NYCbCrA)
		for row := r.Min.Y; row < r.Max.Y; row++ {
			copy(p.A[p.AOffset(r.Min.X, row):][:r.Dx()], src.A[src.AOffset(r.Min.X, row):][:r.Dx()])
		}
	}
	for row := r.Min.Y; row < r.Max.Y; row++ {
		mapPlane(oy.Y[oy.YOffset(r.Min.X, row):][:r.Dx()], y.Y[y.YOffset(r.Min.X, row):], &lumaFull)
	}
	// 4:2:0: one chroma row for two rows of the image.
	cw := (r.Max.X+1)/2 - r.Min.X/2
	for row := r.Min.Y / 2; row < (r.Max.Y+1)/2; row++ {
		do, so := oy.COffset(r.Min.X, 2*row), y.COffset(r.Min.X, 2*row)
		if 2*row < r.Min.Y { // the strip starts on an odd row
			do, so = oy.COffset(r.Min.X, r.Min.Y), y.COffset(r.Min.X, r.Min.Y)
		}
		mapPlane(oy.Cb[do:do+cw], y.Cb[so:], &chromaFull)
		mapPlane(oy.Cr[do:do+cw], y.Cr[so:], &chromaFull)
	}
	return out, nil
}
