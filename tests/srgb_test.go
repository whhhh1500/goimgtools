package tests

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/bmp"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The colour wheel, a Display P3 test profile and what LittleCMS makes of
// the wheel in that profile in sRGB (internal/iccprof/testdata).
func iccData(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "internal", "iccprof", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestToSRGB: an image with a Display P3 profile written as BMP or GIF,
// which cannot carry the profile, or with ToSRGB, comes out converted to
// sRGB (LittleCMS's result to within a level) and without the profile;
// an sRGB profile, one that cannot be read and StripICC change nothing.
func TestToSRGB(t *testing.T) {
	wheel, p3 := iccData(t, "wheel.png"), iccData(t, "p3-table.icc")
	want := decodePNG(t, iccData(t, "p3-table-srgb.png"))
	raw := decodePNG(t, wheel)
	in := withPNGProfile(t, wheel, p3)
	within := func(what string, got image.Image, ref image.Image, limit int) {
		t.Helper()
		if d, _, _ := channelDiff(t, got, ref); d > limit {
			t.Errorf("%s: up to %d levels from the reference", what, d)
		}
	}
	decodeBMP := func(b []byte) image.Image {
		m, err := bmp.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}

	// BMP: converted.
	b, err := FromBytes(in).BMP().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	within("BMP", decodeBMP(b), want, 1)

	// GIF: converted, then reduced to 256 colours; nearer the converted
	// colours than to the raw ones.
	g, err := FromBytes(in).GIF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	gm, err := gif.Decode(bytes.NewReader(g))
	if err != nil {
		t.Fatal(err)
	}
	_, toWant, _ := channelDiff(t, gm, want)
	_, toRaw, _ := channelDiff(t, gm, raw)
	if toWant >= toRaw {
		t.Errorf("GIF: %.2f from the sRGB colours, %.2f from the raw values", toWant, toRaw)
	}

	// ToSRGB with PNG output: converted, the profile left out.
	p, err := FromBytes(in).ToSRGB().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	within("ToSRGB PNG", decodePNG(t, p), want, 1)
	if info, err := FromBytes(p).Metadata(); err != nil || info.HasProfile {
		t.Errorf("ToSRGB PNG: profile still there (%v)", err)
	}
	// Without it, PNG keeps the profile and the values.
	k, _ := FromBytes(in).PNG().ToBytes()
	if info, _ := FromBytes(k).Metadata(); !info.HasProfile {
		t.Error("PNG lost its profile")
	}
	within("PNG with its profile", decodePNG(t, k), raw, 0)

	// Through Stream: converted, and the stream carries no profile.
	src, err := FromBytes(in).ToSRGB().Stream(nil)
	if err != nil {
		t.Fatal(err)
	}
	if icc := pipeline.MetaOf(src).ICC; icc != nil {
		t.Error("Stream with ToSRGB still has the profile")
	}
	sm, err := pipeline.Collect(src)
	src.Close()
	if err != nil {
		t.Fatal(err)
	}
	within("ToSRGB Stream", sm, want, 1)

	// StripICC: the values as they are, taken for sRGB.
	s, _ := FromBytes(in).StripICC().BMP().ToBytes()
	within("StripICC BMP", decodeBMP(s), raw, 0)

	// A profile that is not one to convert from: nothing changes, no error.
	junk := make([]byte, 600)
	for i := range junk {
		junk[i] = byte(i * 31)
	}
	j, err := FromBytes(withPNGProfile(t, wheel, junk)).BMP().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	within("unreadable profile BMP", decodeBMP(j), raw, 0)

	// Transparency is kept.
	clear := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := range clear.Pix {
		clear.Pix[i] = byte(i * 7)
	}
	c, err := FromBytes(withPNGProfile(t, pngBytes(t, clear), p3)).ToSRGB().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	cm := decodePNG(t, c)
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			if a := color.NRGBAModel.Convert(cm.At(x, y)).(color.NRGBA).A; a != clear.NRGBAAt(x, y).A {
				t.Fatalf("pixel (%d,%d): alpha %d, want %d", x, y, a, clear.NRGBAAt(x, y).A)
			}
		}
	}
}
