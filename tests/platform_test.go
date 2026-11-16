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
		{"unsharp mask", photo.Clone().Sharpen(SharpenOptions{Sigma: 1.5, Amount: 1.7, Threshold: 2}), "39a6d26dcb229159"},
		{"mild sharpen", photo.Clone().Sharpen(), "1656909198e7d9c8"},
		{"rotate", photo.Clone().RotateBy(23.5, color.White), "fca06d93d78470fa"},
		{"rotate translucent", clear.Clone().RotateBy(-140, color.Transparent), "971c60e10bf30ca6"},
		{"extend, trim", photo.Clone().Extend(3, 4, 5, 6, color.NRGBA{9, 8, 7, 255}).Trim(10), "d1998b95dee39434"},
		{"resize cubic", photo.Clone().Resize(117, 0), "5cdeb0a2e42b9640"},
		{"resize lanczos up", clear.Clone().Resize(400, 0, ResizeOptions{Kernel: KernelLanczos3}), "04647b41febc98fb"},
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
		{"jpeg", func() ([]byte, error) { return photo.Clone().JPEG(83).ToBytes() }, "72272b88c25cc018"},
		{"jpeg progressive", func() ([]byte, error) {
			return photo.Clone().JPEG(83, JPEGOptions{Progressive: true, Optimize: true}).ToBytes()
		}, "15fdeecd80749ec2"},
		{"jpeg trellis", func() ([]byte, error) {
			return photo.Clone().JPEG(77, JPEGOptions{Optimize: true, Trellis: true}).ToBytes()
		}, "1e6064dbc5840d3a"},
		{"png text level 7", func() ([]byte, error) {
			return FromImage(textLike(320, 200)).PNG(PNGOptions{CompressionLevel: 7}).ToBytes()
		}, byFlate("d83e8dfe83edf793", "ee8dde29d4265e21")},
		{"gif lossy", func() ([]byte, error) { return photo.Clone().GIF(GIFOptions{Lossy: 24}).ToBytes() }, "0d853b8290dead48"},
		{"jpeg jpegli tables", func() ([]byte, error) {
			return photo.Clone().JPEG(77, JPEGOptions{QuantTable: QuantTableJpegli, Progressive: true}).ToBytes()
		}, "2bab35f640292b20"},
		{"jpeg quant table 3", func() ([]byte, error) {
			return photo.Clone().JPEG(77, JPEGOptions{QuantTable: QuantTableImageMagick}).ToBytes()
		}, "32485d8a9a2c7f60"},
		{"png", func() ([]byte, error) { return clear.Clone().PNG().ToBytes() }, byFlate("7d6848e3147d24ec", "2a1b227d06566024")},
		{"png 40 colours", func() ([]byte, error) { return photo.Clone().PNG(PNGOptions{Colors: 40}).ToBytes() }, byFlate("844bdcb3467662a1", "81d23e2c5193b24f")},
		{"webp", func() ([]byte, error) { return photo.Clone().WebP(WebPOptions{Quality: 70}).ToBytes() }, "ee1381f7db2666f2"},
		{"webp lossless", func() ([]byte, error) { return clear.Clone().WebP(WebPOptions{Lossless: true}).ToBytes() }, "c090f32b061d1cc5"},
		{"gif", func() ([]byte, error) { return photo.Clone().GIF().ToBytes() }, "55ae5c4b96926b04"},
		{"gif animation", func() ([]byte, error) {
			return FromBytes(animationBytes(t)).Animated().Resize(90, 0).GIF(GIFOptions{Colors: 50}).ToBytes()
		}, "12fc9505fd4a347d"},
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
