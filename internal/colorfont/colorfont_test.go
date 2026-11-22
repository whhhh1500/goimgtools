package colorfont

import (
	"bytes"
	"image/color"
	"os"
	"testing"

	"golang.org/x/image/font/sfnt"
)

// The test font (made for these tests): ✨ in three layers, red, blue and
// the text's colour; 👨 and 👩 in two each; A in one colour.
const testFont = "../../tests/testdata/fonts/ColourTest.ttf"

func TestLayers(t *testing.T) {
	data, err := os.ReadFile(testFont)
	if err != nil {
		t.Fatal(err)
	}
	f, err := Open(bytes.NewReader(data), 0)
	if err != nil || f == nil {
		t.Fatalf("%v %v", f, err)
	}
	sf, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var b sfnt.Buffer
	id := func(r rune) uint16 {
		g, _ := sf.GlyphIndex(&b, r)
		return uint16(g)
	}
	ls := f.Layers(id('✨'), nil)
	if len(ls) != 3 || ls[0].Color != (color.NRGBA{255, 0, 0, 255}) || ls[1].Color != (color.NRGBA{0, 0, 255, 255}) || !ls[2].Foreground {
		t.Errorf("✨: %+v", ls)
	}
	if ls := f.Layers(id('A'), nil); len(ls) != 0 {
		t.Errorf("A: %+v", ls)
	}
	if f.Bytes <= 0 || f.Bytes > int64(len(data)) {
		t.Errorf("%d bytes held", f.Bytes)
	}
	// A font without colour layers, and what is not a font.
	if f, err := Open(bytes.NewReader([]byte("\x00\x01\x00\x00\x00\x00\x00\x00\x00\x00\x00\x00")), 0); f != nil || err != nil {
		t.Errorf("no tables: %v %v", f, err)
	}
	for _, bad := range [][]byte{nil, data[:40], data[:len(data)/2]} {
		if f, _ := Open(bytes.NewReader(bad), 0); f != nil {
			f.Layers(id('✨'), nil)
		}
	}
	var none *Font
	if ls := none.Layers(1, nil); len(ls) != 0 {
		t.Error("nil font")
	}
}
