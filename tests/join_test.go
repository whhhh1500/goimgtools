package tests

import (
	"bytes"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestJoin: each image lands in its cell, as large as the largest, at the
// alignment asked for, with the gaps and the room around it in the
// background colour (transparent without one); one row without Across;
// many images within a small memory limit; bad arguments are errors.
func TestJoin(t *testing.T) {
	a := noisyPhoto(30, 20, false)
	b := noisyPhoto(12, 25, false)
	c := noisyPhoto(18, 9, true)
	imgs := []*Image{FromImage(a), FromImage(b), FromImage(c)}
	check := func(got *image.NRGBA, src *image.NRGBA, x0, y0 int) {
		t.Helper()
		for y := 0; y < src.Rect.Dy(); y++ {
			for x := 0; x < src.Rect.Dx(); x++ {
				if g, w := got.NRGBAAt(x0+x, y0+y), src.NRGBAAt(x, y); g != w && !(g.A == 0 && w.A == 0) {
					t.Fatalf("pixel %d,%d of the image at %d,%d: %v, want %v", x, y, x0, y0, g, w)
				}
			}
		}
	}
	// Two across, gap 4, centred, grey: cells of 30x25.
	grey := color.NRGBA{90, 90, 90, 255}
	m := drawn(t, Join(imgs, JoinOptions{Across: 2, Gap: 4, Background: grey, Align: Center}))
	if m.Rect.Dx() != 30*2+4 || m.Rect.Dy() != 25*2+4 {
		t.Fatalf("grid %v", m.Rect)
	}
	check(m, a, 0, 2)
	check(m, b, 34+9, 0)
	check(m, c, 6, 29+8)
	for _, p := range []image.Point{{0, 0}, {31, 10}, {40, 26}, {60, 50}} {
		if m.NRGBAAt(p.X, p.Y) != grey {
			t.Errorf("background at %v: %v", p, m.NRGBAAt(p.X, p.Y))
		}
	}
	// One row, top-left, transparent.
	m = drawn(t, Join(imgs))
	if m.Rect.Dx() != 90 || m.Rect.Dy() != 25 {
		t.Fatalf("row %v", m.Rect)
	}
	check(m, a, 0, 0)
	check(m, b, 30, 0)
	check(m, c, 60, 0)
	if m.NRGBAAt(5, 22).A != 0 {
		t.Error("the room under a smaller image is not transparent")
	}
	if info, err := Join(imgs).Metadata(); err != nil || info.Format != "join" || info.Width != 90 {
		t.Errorf("Metadata %+v, %v", info, err)
	}
	// Forty images of 300x200, eight at a time, 2.4 megapixels in all,
	// within 6MB.
	var many []*Image
	for i := 0; i < 40; i++ {
		many = append(many, Blank(300, 200, color.NRGBA{uint8(i * 6), 100, 200, 255}))
	}
	if _, err := Join(many, JoinOptions{Across: 8}).MemoryLimit(6 << 20).PNG().ToBytes(); err != nil {
		t.Errorf("forty images in 6MB: %v", err)
	}
	for i, bad := range []*Image{Join(nil), Join(imgs, JoinOptions{Across: -1}), Join(imgs, JoinOptions{Gap: -2}),
		Join(imgs, JoinOptions{Align: CustomPosition}), Join([]*Image{nil}), Join([]*Image{FromBytes([]byte("not an image"))}),
		Join(imgs, JoinOptions{}, JoinOptions{})} {
		if _, err := bad.PNG().ToBytes(); err == nil {
			t.Errorf("case %d: no error", i)
		}
	}
}

// TestJoinFromReader: an image that can be read only once has its place
// in the grid like any other.
func TestJoinFromReader(t *testing.T) {
	a, _ := Blank(30, 20, color.NRGBA{200, 0, 0, 255}).PNG().ToBytes()
	b, _ := Blank(10, 40, color.NRGBA{0, 0, 200, 255}).PNG().ToBytes()
	want, err := Join([]*Image{FromBytes(a), FromBytes(b), FromBytes(a)}, JoinOptions{Across: 2}).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	got, err := Join([]*Image{FromBytes(a), FromReader(bytes.NewReader(b)), FromReader(bytes.NewReader(a))}, JoinOptions{Across: 2}).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Error("another grid with FromReader images")
	}
}
