package tests

import (
	"bytes"
	"errors"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
)

// TestMinSSIM: the file is the one of the lowest quality (colours, for
// PNG) that stays at least as alike as asked, found by trying every one up
// to 100 when no quality was asked for; when even the quality asked for
// falls short, that; the errors.
func TestMinSSIM(t *testing.T) {
	photo := pngBytes(t, noisyPhoto(160, 120, false))
	lowest := func(top int, enc func(q int) *Image, target float64) []byte {
		for q := 1; q <= top; q++ {
			b := must(enc(q).ToBytes())
			if s, err := inspect.SSIM(FromBytes(photo), FromBytes(b)); err == nil && s >= target {
				return b
			}
		}
		return must(enc(top).ToBytes())
	}
	for _, c := range []struct {
		name   string
		target float64
		top    int
		enc    func(q int) *Image
		got    *Image
	}{
		{"jpeg", 0.95, 100, func(q int) *Image { return FromBytes(photo).JPEG(JPEGOptions{Quality: q}) }, FromBytes(photo).JPEG().MinSSIM(0.95)},
		{"webp", 0.97, 100, func(q int) *Image { return FromBytes(photo).WebP(WebPOptions{Quality: q}) }, FromBytes(photo).WebP().MinSSIM(0.97)},
		{"png", 0.9, 256, func(q int) *Image {
			if q < 2 {
				q = 2
			}
			return FromBytes(photo).PNG(PNGOptions{Colors: q})
		}, FromBytes(photo).PNG(PNGOptions{Colors: 256}).MinSSIM(0.9)},
	} {
		got := must(c.got.ToBytes())
		want := lowest(c.top, c.enc, c.target)
		if !bytes.Equal(got, want) {
			s, _ := inspect.SSIM(FromBytes(photo), FromBytes(got))
			t.Errorf("%s: %d bytes (SSIM %.4f), the lowest that reaches %v is %d bytes", c.name, len(got), s, c.target, len(want))
		}
	}
	// Out of reach: the quality asked for.
	if got, want := must(FromBytes(photo).JPEG(JPEGOptions{Quality: 70}).MinSSIM(0.99999).ToBytes()), must(FromBytes(photo).JPEG(JPEGOptions{Quality: 70}).ToBytes()); !bytes.Equal(got, want) {
		t.Error("out of reach: not the quality asked for")
	}
	// Under a small memory limit the same file: each of the eight or so
	// measurements gives back what it took (it used to keep the strips of
	// both pictures reserved, and the later ones found no room).
	big := noisyPhoto(1600, 1200, false)
	want := must(FromImage(big).JPEG().MinSSIM(0.97).ToBytes())
	if got, err := FromImage(big).MemoryLimit(2 << 20).JPEG().MinSSIM(0.97).ToBytes(); err != nil || !bytes.Equal(got, want) {
		t.Errorf("under a limit of 2MB: %d bytes, %v; without, %d bytes", len(got), err, len(want))
	}
	for i, bad := range []*Image{FromBytes(photo).JPEG().MinSSIM(0), FromBytes(photo).JPEG().MinSSIM(1.5),
		FromBytes(photo).JPEG().MinSSIM(0.9).MaxFileSize(10000), FromBytes(photo).WebP(WebPOptions{Lossless: true}).MinSSIM(0.9),
		FromBytes(encodeAll(t, testAnimation(32, 24))).Animated().GIF().MinSSIM(0.9)} {
		if _, err := bad.ToBytes(); err == nil {
			t.Errorf("case %d: no error", i)
		} else if i == 4 && !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("animation: %v", err)
		}
	}
}
