package tests

import (
	"crypto/sha256"
	"fmt"
	"image/color"
	"runtime"
	"testing"

	. "github.com/whhhh1500/goimgtools"
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
		{"blur kernel", photo.Clone().Blur(6, BlurOptions{Method: BlurLibvips}), "bd79e44679602cf7"},
		{"blur kernel translucent", clear.Clone().Blur(1.7, BlurOptions{Method: BlurLibvips, MinAmplitude: 0.01}), "70237cb818d8b9ec"},
		{"unsharp mask", photo.Clone().Sharpen(SharpenOptions{Sigma: 1.5, Amount: 1.7, Threshold: 2}), "39a6d26dcb229159"},
		{"mild sharpen", photo.Clone().Sharpen(), "1656909198e7d9c8"},
		{"sharpen lightness", photo.Clone().Sharpen(SharpenOptions{Sigma: 1.2, Method: SharpenLibvips}), "d31a17aef6543a56"},
		{"sharpen lightness translucent", clear.Clone().Sharpen(SharpenOptions{Sigma: 0.9, Method: SharpenLibvips}), "010012faaa37a465"},
		{"rotate", photo.Clone().RotateBy(23.5, color.White), "6a31d1ab774afdea"},
		{"rotate translucent", clear.Clone().RotateBy(-140, color.Transparent), "1ff52d2f8da42763"},
		{"extend, trim", photo.Clone().Extend(3, 4, 5, 6, color.NRGBA{9, 8, 7, 255}).Trim(10), "d1998b95dee39434"},
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
		{"resize libvips", photo.Clone().Resize(97, 0, ResizeOptions{Kernel: KernelLanczos3, Method: ResizeLibvips}), "0021404ad205d433"},
		{"resize libvips translucent", clear.Clone().Resize(400, 50, ResizeOptions{Fit: FitFill, Kernel: KernelMKS2021, Method: ResizeLibvips}), "75485f3a5472db7f"},
		{"enhance", photo.Clone().Enhance(EnhanceSharpness, 1.6).Enhance(EnhanceColor, 1.3).Enhance(EnhanceContrast, 0.8), "22429f5f874bed97"},
		{"pillow filters", photo.Clone().Filter(FilterSmoothMore).Filter(FilterEdgeEnhance), "9339e0ee44175ebb"},
		{"pillow filter translucent", clear.Clone().Filter(FilterBlur), "c43147e9dc7a1044"},
		{"autocontrast", photo.Clone().AutoContrast(AutoContrastOptions{CutoffLow: 1.5, CutoffHigh: 2.5}), "1a0f7d7e646a9784"},
		{"resize pillow", photo.Clone().Resize(97, 0, ResizeOptions{Kernel: KernelLanczos3, Method: ResizePillow}), "a507ca964967b586"},
		{"resize pillow translucent", clear.Clone().Resize(400, 50, ResizeOptions{Fit: FitFill, Kernel: KernelHamming, Method: ResizePillow}), "e37043c622b45c1a"},
		{"resize libvips contain", clear.Clone().Resize(60, 60, ResizeOptions{Fit: FitContain, Kernel: KernelCubic,
			Background: color.NRGBA{200, 10, 90, 99}, Method: ResizeLibvips}), "9ff49d5906aebc1c"},
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
		{"jpeg", func() ([]byte, error) { return photo.Clone().JPEG(83).ToBytes() }, "17e7e54b996696d4"},
		{"jpeg progressive", func() ([]byte, error) {
			return photo.Clone().JPEG(83, JPEGOptions{Progressive: true, Optimize: true}).ToBytes()
		}, "fa220cd8e057ac2d"},
		{"jpeg trellis", func() ([]byte, error) {
			return photo.Clone().JPEG(77, JPEGOptions{Optimize: true, Trellis: true}).ToBytes()
		}, "8b4c90a04ea104ff"},
		{"png text level 7", func() ([]byte, error) {
			return FromImage(textLike(320, 200)).PNG(PNGOptions{CompressionLevel: 7}).ToBytes()
		}, byFlate("d83e8dfe83edf793", "ee8dde29d4265e21")},
		// Levels 8 and 9 have a compressor of their own: one checksum
		// for every Go version.
		{"png text level 8", func() ([]byte, error) {
			return FromImage(textLike(320, 200)).PNG(PNGOptions{CompressionLevel: 8}).ToBytes()
		}, "e47f67cef1a4891b"},
		{"png level 9", func() ([]byte, error) {
			return photo.Clone().PNG(PNGOptions{CompressionLevel: 9}).ToBytes()
		}, "61817650c4bc5347"},
		{"gif lossy", func() ([]byte, error) { return photo.Clone().GIF(GIFOptions{Lossy: 24}).ToBytes() }, "7fcd09eb9c1eea96"},
		{"jpeg jpegli tables", func() ([]byte, error) {
			return photo.Clone().JPEG(77, JPEGOptions{QuantTable: QuantTableJpegli, Progressive: true}).ToBytes()
		}, "da2d555832c82e5f"},
		{"jpeg 4:4:4 progressive", func() ([]byte, error) {
			return photo.Clone().JPEG(83, JPEGOptions{Progressive: true, ChromaSubsampling: Chroma444}).ToBytes()
		}, "0fbd533c18adfad7"},
		{"jpeg 4:2:2 trellis", func() ([]byte, error) {
			return photo.Clone().JPEG(77, JPEGOptions{Optimize: true, Trellis: true, ChromaSubsampling: Chroma422}).ToBytes()
		}, "12036896ee7978f7"},
		{"jpeg quant table 3", func() ([]byte, error) {
			return photo.Clone().JPEG(77, JPEGOptions{QuantTable: QuantTableImageMagick}).ToBytes()
		}, "39b0657868638dd1"},
		{"png", func() ([]byte, error) { return clear.Clone().PNG().ToBytes() }, byFlate("7d6848e3147d24ec", "2a1b227d06566024")},
		{"png 40 colours", func() ([]byte, error) { return photo.Clone().PNG(PNGOptions{Colors: 40}).ToBytes() }, byFlate("a22a7198a696d7a8", "4b7205232df6241f")},
		{"webp", func() ([]byte, error) { return photo.Clone().WebP(WebPOptions{Quality: 70}).ToBytes() }, "3fd53ba37ca99645"},
		{"webp lossless", func() ([]byte, error) { return clear.Clone().WebP(WebPOptions{Lossless: true}).ToBytes() }, "9d7241e10fec8b83"},
		// Transparency: sharp YUV of the colours, the transparent area
		// made flat, the alpha plane coded by the lossless encoder.
		{"webp alpha", func() ([]byte, error) { return clear.Clone().WebP(WebPOptions{Quality: 70}).ToBytes() }, "bd4f113e1fb01ed1"},
		{"gif", func() ([]byte, error) { return photo.Clone().GIF().ToBytes() }, "88be7f34f4dddfe6"},
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

// byFlate picks the checksum of a PNG file by the standard library's
// compress/flate, which Go 1.27 rewrote: the same pixels compress to other
// bytes with Go 1.26, where the default level is also 6 instead of 7. (A
// development version counts as the newer one.)
func byFlate(go127, go126 string) string {
	var minor int
	if _, err := fmt.Sscanf(runtime.Version(), "go1.%d", &minor); err == nil && minor < 27 {
		return go126
	}
	return go127
}
