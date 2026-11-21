package tests

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestBlank: every pixel is the colour asked for, opaque or not; Metadata
// says what it is; the strips are filled again for every row (an operation
// that changes them in place sees the colour each time); a canvas takes
// other images and text; a large one is written within a small memory
// limit; bad sizes and too many pixels are errors.
func TestBlank(t *testing.T) {
	for _, c := range []color.Color{color.NRGBA{200, 30, 90, 255}, color.NRGBA{10, 220, 40, 100}, color.Transparent, color.Gray{77}} {
		b, err := Blank(37, 1000, c).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		m, err := png.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		want := color.NRGBAModel.Convert(c).(color.NRGBA)
		if want.A == 0 {
			want = color.NRGBA{}
		}
		n := nrgbaOf(m)
		if n.Rect.Dx() != 37 || n.Rect.Dy() != 1000 {
			t.Fatalf("%v: size %v", c, n.Rect)
		}
		for i := 0; i < len(n.Pix); i += 4 {
			if got := (color.NRGBA{n.Pix[i], n.Pix[i+1], n.Pix[i+2], n.Pix[i+3]}); got != want {
				t.Fatalf("%v: pixel %d is %v", c, i/4, got)
			}
		}
		info, err := Blank(37, 1000, c).Metadata()
		if err != nil || info.Format != "blank" || info.Width != 37 || info.Height != 1000 || info.HasAlpha != (want.A != 255) {
			t.Errorf("%v: Metadata %+v, %v", c, info, err)
		}
	}
	// An operation that changes the strips in place.
	b, err := Blank(5, 3000, color.Gray{100}).Pixels(func(s *image.NRGBA) error {
		for i := 0; i < len(s.Pix); i += 4 {
			s.Pix[i] += 10
		}
		return nil
	}).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	m := nrgbaOf(decodePNG(t, b))
	for i := 0; i < len(m.Pix); i += 4 {
		if m.Pix[i] != 110 || m.Pix[i+1] != 100 {
			t.Fatalf("pixel %d is %v", i/4, m.Pix[i:i+4])
		}
	}
	// A canvas for a picture and text.
	pic := pngBytes(t, noisyPhoto(60, 40, false))
	if _, err := Blank(200, 100, color.White).Composite(FromBytes(pic), CompositeOptions{Gravity: Center}).
		Composite(Text("Hello", TextOptions{Size: 20})).JPEG().ToBytes(); err != nil {
		t.Fatal(err)
	}
	// Large, in little memory.
	if _, err := Blank(6000, 4000, color.White).MemoryLimit(4 << 20).JPEG().ToBytes(); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []*Image{Blank(0, 10, color.White), Blank(10, -1, color.White), Blank(10, 10, nil)} {
		if _, err := bad.PNG().ToBytes(); err == nil {
			t.Error("no error")
		}
	}
	if _, err := Blank(20000, 20000, color.White).PNG().ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("20000x20000: %v", err)
	}
}
