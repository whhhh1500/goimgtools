package tests

import (
	"crypto/sha256"
	"fmt"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestSameOnEveryProcessor: the pixels and files the operations make are
// the same on every processor and system, to the byte. The checksums were
// taken on x86-64; the automatic tests run this on ARM64, PowerPC, s390x,
// RISC-V, LoongArch, MIPS and 32-bit ARM as well. (Floating point is where
// they could differ: Go may fuse a multiplication and an addition on some
// processors and not on others.)
func TestSameOnEveryProcessor(t *testing.T) {
	photo := FromImage(photoLike(301, 203))
	clear := FromImage(testutil.Images(157, 93)["nrgba"])
	cases := []struct {
		name string
		m    *Image
		want string
	}{
		{"grayscale", photo.Clone().Grayscale(), "b8323bd9172be2d8"},
		{"modulate", photo.Clone().Modulate(1.2, 0.7, 40), "17cf3627ead83f21"},
		{"modulate translucent", clear.Clone().Modulate(0.8, 1.5, -100), "d1db2d724879a571"},
		{"blur", photo.Clone().Blur(2.5), "abae7d0078b5ff7c"},
		{"blur translucent", clear.Clone().Blur(1.2), "89ee0ebd631eb210"},
		{"affine", photo.Clone().Affine(0.9, 0.35, -0.2, 1.1), "67598c716d7e9a1d"},
		{"affine translucent", clear.Clone().Affine(0.8, -0.4, 0.3, 1.2, AffineOptions{Background: color.NRGBA{9, 99, 199, 128}}), "c29f9c57b9c451e0"},
		{"affine bicubic", photo.Clone().Affine(0.9, 0.35, -0.2, 1.1, AffineOptions{Interpolator: InterpolateBicubic}), "105b41db60612f2c"},
		{"affine bicubic translucent", clear.Clone().Affine(0.8, -0.4, 0.3, 1.2, AffineOptions{Interpolator: InterpolateBicubic}), "5c43553bc4eb0a91"},
		{"blur kernel", photo.Clone().Blur(6, BlurOptions{Method: MethodLibvips}), "bd79e44679602cf7"},
		{"blur kernel translucent", clear.Clone().Blur(1.7, BlurOptions{Method: MethodLibvips, MinAmplitude: 0.01}), "70237cb818d8b9ec"},
		{"unsharp mask", photo.Clone().Sharpen(SharpenOptions{Sigma: 1.5, Amount: 1.7, Threshold: 2}), "39a6d26dcb229159"},
		{"mild sharpen", photo.Clone().Sharpen(), "1656909198e7d9c8"},
		{"sharpen lightness", photo.Clone().Sharpen(SharpenOptions{Sigma: 1.2, Method: MethodLibvips}), "d31a17aef6543a56"},
		{"sharpen lightness translucent", clear.Clone().Sharpen(SharpenOptions{Sigma: 0.9, Method: MethodLibvips}), "010012faaa37a465"},
		{"rotate", photo.Clone().Rotate(23.5, RotateOptions{Background: color.White}), "6a31d1ab774afdea"},
		{"rotate translucent", clear.Clone().Rotate(-140, RotateOptions{Background: color.Transparent}), "1ff52d2f8da42763"},
		{"extend, trim", photo.Clone().Extend(3, 4, 5, 6, ExtendOptions{Background: color.NRGBA{9, 8, 7, 255}}).Trim(10), "d1998b95dee39434"},
		{"resize cubic", photo.Clone().Resize(117, 0), "5cdeb0a2e42b9640"},
		{"resize lanczos up", clear.Clone().Resize(400, 0, ResizeOptions{Kernel: KernelLanczos3}), "04647b41febc98fb"},
		{"resize in linear light", photo.Clone().Resize(127, 0, ResizeOptions{LinearLight: true, Kernel: KernelLanczos3}), "5b9f6206ffcfbd15"},
		{"resize translucent in linear light", clear.Clone().Resize(0, 61, ResizeOptions{LinearLight: true}), "5cc4e087e1f0a142"},
		{"resize mitchell", photo.Clone().Resize(173, 0, ResizeOptions{Kernel: KernelMitchell}), "ea6251e9cde30067"},
		{"levels", photo.Clone().Linear(1.13, -7.5).Gamma(1.7), "1f410fb5a23f02a4"},
		{"threshold, unflatten", photo.Clone().Threshold(140).Unflatten(), "ce1e2bb0dbd829dc"},
		{"tint, normalize", photo.Clone().Tint(color.RGBA{112, 66, 20, 255}).Normalize(NormalizeOptions{Lower: 3, Upper: 97}), "8a06fc35de2b3332"},
		{"recomb translucent", clear.Clone().Recomb([][]float64{{0.31, 0.62, 0.07, 0}, {0.2, 0.5, 0.3, 0.1}, {0.11, 0.37, 0.53, 0}, {0.1, 0, 0.2, 0.75}}), "8d4169cfe265aa86"},
		{"convolve fractions", photo.Clone().Convolve(Convolution{Width: 3, Height: 3, Weights: []float64{0.1, -0.7, 0.13, -0.6, 3.9, -0.55, 0.12, -0.8, 0.17}, Offset: 1.3}), "5a1ecbad1e425874"},
		{"median, clahe", photo.Clone().Median(3).CLAHE(CLAHEOptions{Width: 21, Height: 17, MaxSlope: 4}), "a33a6f98d121924f"},
		{"blend modes", photo.Clone().Composite(clear.Clone(), CompositeOptions{Blend: BlendSoftLight}).
			Composite(clear.Clone().Flop(), CompositeOptions{Blend: BlendOverlay, Gravity: SouthEast}).
			Composite(clear.Clone().Flip(), CompositeOptions{Blend: BlendHardLight, Gravity: NorthEast, Opacity: 0.6}).
			Composite(clear.Clone(), CompositeOptions{Blend: BlendExclusion, Gravity: SouthWest}), "d1267b1174a7247d"},
		{"overlay by ratio, turned", photo.Clone().Composite(clear.Clone(), CompositeOptions{LeftRatio: 0.37, TopRatio: 0.21, Angle: 17}), "2da34ec182e1e722"},
		{"attention crop", photo.Clone().Resize(120, 140, ResizeOptions{Fit: FitCover, Interesting: InterestingAttention}), "689cfd7a72fd4c9e"},
		{"entropy crop", photo.Clone().Resize(150, 60, ResizeOptions{Fit: FitCover, Interesting: InterestingEntropy}), "d5a7e04db11ef0a0"},
		{"alpha channel", clear.Clone().ExtractChannel(ChannelAlpha), "e880988a7dc509ad"},
		{"draw", photo.Clone().Draw(Circle(150.3, 101.7, 60.2).Fill(color.NRGBA{200, 30, 30, 140}).Stroke(color.Black, 3.5),
			Rect(20.25, 30.5, 90, 50).Rounded(12.5).Stroke(color.White, 2), Polyline(5, 190, 100, 120, 290, 195).Stroke(color.NRGBA{0, 90, 220, 255}, 6)), "698852c92a589ce3"},
		{"resize libvips", photo.Clone().Resize(97, 0, ResizeOptions{Kernel: KernelLanczos3, Method: MethodLibvips}), "0021404ad205d433"},
		{"resize libvips translucent", clear.Clone().Resize(400, 50, ResizeOptions{Fit: FitFill, Kernel: KernelMKS2021, Method: MethodLibvips}), "75485f3a5472db7f"},
		{"enhance", photo.Clone().Enhance(EnhanceSharpness, 1.6).Enhance(EnhanceColor, 1.3).Enhance(EnhanceContrast, 0.8), "22429f5f874bed97"},
		{"pillow filters", photo.Clone().Filter(FilterSmoothMore).Filter(FilterEdgeEnhance), "9339e0ee44175ebb"},
		{"pillow filter translucent", clear.Clone().Filter(FilterBlur), "c43147e9dc7a1044"},
		{"autocontrast", photo.Clone().AutoContrast(AutoContrastOptions{CutoffLow: 1.5, CutoffHigh: 2.5}), "1a0f7d7e646a9784"},
		{"resize pillow", photo.Clone().Resize(97, 0, ResizeOptions{Kernel: KernelLanczos3, Method: MethodPillow}), "a507ca964967b586"},
		{"resize pillow translucent", clear.Clone().Resize(400, 50, ResizeOptions{Fit: FitFill, Kernel: KernelHamming, Method: MethodPillow}), "e37043c622b45c1a"},
		{"resize libvips contain", clear.Clone().Resize(60, 60, ResizeOptions{Fit: FitContain, Kernel: KernelCubic,
			Background: color.NRGBA{200, 10, 90, 99}, Method: MethodLibvips}), "9ff49d5906aebc1c"},
		{"svg", FromBytes([]byte(platformSVG)).Resize(211, 0), "8bcc505bc7c70c99"},
	}
	for _, c := range cases {
		env := pipeline.NewEnv(0, t.TempDir())
		src, err := c.m.Stream(env)
		if err != nil {
			t.Fatal(err)
		}
		img, err := pipeline.Collect(src)
		src.Close()
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		b := img.Bounds()
		fmt.Fprint(h, b)
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, a := img.At(x, y).RGBA()
				fmt.Fprint(h, r, g, bl, a)
			}
		}
		if got := fmt.Sprintf("%x", h.Sum(nil))[:16]; got != c.want {
			t.Errorf("%s: pixels %s, want %s", c.name, got, c.want)
		}
	}
	files := []struct {
		name string
		do   func() ([]byte, error)
		want string
	}{
		{"jpeg", func() ([]byte, error) { return photo.Clone().JPEG(JPEGOptions{Quality: 83}).ToBytes() }, "17e7e54b996696d4"},
		{"jpeg progressive", func() ([]byte, error) {
			return photo.Clone().JPEG(JPEGOptions{Quality: 83, Progressive: true, Optimize: true}).ToBytes()
		}, "fa220cd8e057ac2d"},
		{"jpeg trellis", func() ([]byte, error) {
			return photo.Clone().JPEG(JPEGOptions{Quality: 77, Optimize: true, Trellis: true}).ToBytes()
		}, "8b4c90a04ea104ff"},
		// Levels 7 to 10 have a compressor of their own: one checksum for
		// every Go version (level 10's Zopfli takes its logarithms with its own
		// function, as math.Log may be fused on some processors).
		{"png text level 7", func() ([]byte, error) {
			return FromImage(textLike(320, 200)).PNG(PNGOptions{CompressionLevel: 7}).ToBytes()
		}, "418404b13eac9f66"},
		{"png text level 8", func() ([]byte, error) {
			return FromImage(textLike(320, 200)).PNG(PNGOptions{CompressionLevel: 8}).ToBytes()
		}, "d357dc37a756f138"},
		{"png level 9", func() ([]byte, error) {
			return photo.Clone().PNG(PNGOptions{CompressionLevel: 9}).ToBytes()
		}, "cf4bd56ab990a358"},
		{"png level 10", func() ([]byte, error) {
			return photo.Clone().Resize(150, 0).PNG(PNGOptions{CompressionLevel: 10}).ToBytes()
		}, "60bda50b4ab91abe"},
		{"gif lossy", func() ([]byte, error) { return photo.Clone().GIF(GIFOptions{Lossy: 24}).ToBytes() }, "7fcd09eb9c1eea96"},
		{"jpeg jpegli tables", func() ([]byte, error) {
			return photo.Clone().JPEG(JPEGOptions{Quality: 77, QuantTable: QuantTableJpegli, Progressive: true}).ToBytes()
		}, "da2d555832c82e5f"},
		{"jpeg 4:4:4 progressive", func() ([]byte, error) {
			return photo.Clone().JPEG(JPEGOptions{Quality: 83, Progressive: true, ChromaSubsampling: Chroma444}).ToBytes()
		}, "0fbd533c18adfad7"},
		{"jpeg 4:2:2 trellis", func() ([]byte, error) {
			return photo.Clone().JPEG(JPEGOptions{Quality: 77, Optimize: true, Trellis: true, ChromaSubsampling: Chroma422}).ToBytes()
		}, "12036896ee7978f7"},
		// The curve over white pixels in float32.
		{"jpeg overshoot deringing", func() ([]byte, error) {
			return FromImage(textLike(320, 200)).JPEG(JPEGOptions{Quality: 60, OvershootDeringing: true}).ToBytes()
		}, "4ffc6e9031741e03"},
		{"jpeg quant table 3", func() ([]byte, error) {
			return photo.Clone().JPEG(JPEGOptions{Quality: 77, QuantTable: QuantTableImageMagick}).ToBytes()
		}, "39b0657868638dd1"},
		{"png", func() ([]byte, error) { return clear.Clone().PNG().ToBytes() }, "28793aaceffed8bc"},
		{"png 40 colours", func() ([]byte, error) { return photo.Clone().PNG(PNGOptions{Colors: 40}).ToBytes() }, "a22a7198a696d7a8"},
		{"webp", func() ([]byte, error) { return photo.Clone().WebP(WebPOptions{Quality: 70}).ToBytes() }, "3fd53ba37ca99645"},
		{"webp lossless", func() ([]byte, error) { return clear.Clone().WebP(WebPOptions{Lossless: true}).ToBytes() }, "c9e3f3c7f3205db8"},
		{"webp near-lossless", func() ([]byte, error) {
			return photo.Clone().WebP(WebPOptions{NearLossless: true, Quality: 60}).ToBytes()
		}, "817507837fbd19a6"},
		// The alpha plane reduced to fewer levels by k-means in float64.
		{"webp alpha quality", func() ([]byte, error) {
			return clear.Clone().WebP(WebPOptions{Quality: 70, AlphaQuality: 40}).ToBytes()
		}, "cbfe1ae15f59979d"},
		{"invisible watermark", func() ([]byte, error) {
			return photo.Clone().InvisibleWatermark([]byte("mark"), InvisibleWatermarkOptions{Key: "k", Kind: WatermarkScreen, Strength: 1.3}).BMP().ToBytes()
		}, "56d395e855d48b6b"},
		// Transparency: sharp YUV of the colours, the transparent area
		// made flat, the alpha plane coded by the lossless encoder.
		{"webp alpha", func() ([]byte, error) { return clear.Clone().WebP(WebPOptions{Quality: 70}).ToBytes() }, "bd4f113e1fb01ed1"},
		{"gif", func() ([]byte, error) { return photo.Clone().GIF().ToBytes() }, "88be7f34f4dddfe6"},
		// Cosines and the sRGB curve of their own: the same placeholders
		// everywhere.
		{"blurhash", func() ([]byte, error) {
			s, err := inspect.BlurHash(photo.Clone(), 5, 4)
			return []byte(s), err
		}, "d28e56cb71c3aa27"},
		{"thumbhash", func() ([]byte, error) { return inspect.ThumbHash(clear.Clone()) }, "fd2197c16d62bb42"},
		{"ssim", func() ([]byte, error) {
			s, err := inspect.SSIM(photo.Clone(), photo.Clone().Blur(1.3))
			return []byte(fmt.Sprintf("%.17g", s)), err
		}, "9a2732cf4d680163"},
		{"gif animation", func() ([]byte, error) {
			return FromBytes(animationBytes(t)).Animated().Resize(90, 0).GIF(GIFOptions{Colors: 50}).ToBytes()
		}, "57b8955b8f3855b5"},
		{"display p3 to srgb", func() ([]byte, error) {
			return FromBytes(withPNGProfile(t, iccData(t, "wheel.png"), iccData(t, "p3-para.icc"))).BMP().ToBytes()
		}, "fbab28fd9e3fd62a"},
	}
	for _, f := range files {
		b, err := f.do()
		if err != nil {
			t.Fatal(err)
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(b))[:16]; got != f.want {
			t.Errorf("%s: file %s, want %s", f.name, got, f.want)
		}
	}
}

// animationBytes is an animation with more colours than a GIF palette holds.
func animationBytes(t *testing.T) []byte {
	return encodeAll(t, movingAnimation(120, 90, 5, true))
}

// platformSVG draws arcs, curves, turns, gradients, dashes, joins, a clip
// and a mask: all of SVG's floating point.
const platformSVG = `<svg xmlns="http://www.w3.org/2000/svg" xmlns:xlink="http://www.w3.org/1999/xlink" width="157" height="113" viewBox="0 0 100 72">
  <defs>
    <radialGradient id="r" fx="0.3" fy="0.3" spreadMethod="reflect" r="0.4"><stop offset="0" stop-color="#fff"/><stop offset="1" stop-color="#a30" stop-opacity="0.8"/></radialGradient>
    <linearGradient id="l" gradientTransform="rotate(33)"><stop offset="0.2" stop-color="teal"/><stop offset="0.9" stop-color="gold"/></linearGradient>
    <clipPath id="k"><circle cx="70" cy="30" r="22"/></clipPath>
    <mask id="m"><rect width="100" height="72" fill="#888"/></mask>
    <filter id="f"><feDropShadow dx="1.5" dy="2" stdDeviation="1.7" flood-color="navy"/><feColorMatrix type="hueRotate" values="33"/></filter>
  </defs>
  <rect x="3.3" y="2.7" width="40" height="30" rx="5" fill="url(#r)" stroke="#036" stroke-width="1.5" filter="url(#f)"/>
  <g transform="rotate(17.5 70 30) skewX(7)" clip-path="url(#k)" opacity="0.8">
    <path d="M50 10 c10-5 30 5 40 0 s0 30-10 40 a20 15 30 0 1-30-10z" fill="url(#l)" stroke="black" stroke-width="3"/>
  </g>
  <polyline points="5,60 20,40 35,62 50,41" fill="none" stroke="purple" stroke-width="4" stroke-dasharray="7 3" stroke-linecap="round" stroke-linejoin="round"/>
  <ellipse cx="80" cy="60" rx="15" ry="8" fill="green" mask="url(#m)" transform="rotate(-21 80 60)"/>
</svg>`
