package tests

import (
	"crypto/sha256"
	"fmt"
	"image/color"
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
		}, "cdbc714acfd0afd3"},
		{"png", func() ([]byte, error) { return clear.Clone().PNG().ToBytes() }, "18f1642af3e6c2bf"},
		{"png 40 colours", func() ([]byte, error) { return photo.Clone().PNG(PNGOptions{Colors: 40}).ToBytes() }, "16f7aa295c5109b2"},
		{"webp", func() ([]byte, error) { return photo.Clone().WebP(WebPOptions{Quality: 70}).ToBytes() }, "6ce2fe0efc34b6e7"},
		{"webp lossless", func() ([]byte, error) { return clear.Clone().WebP(WebPOptions{Lossless: true}).ToBytes() }, "998880f023e64fd8"},
		{"gif", func() ([]byte, error) { return photo.Clone().GIF().ToBytes() }, "55ae5c4b96926b04"},
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
