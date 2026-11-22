package colorfont

import (
	"bytes"
	"image/png"
	"os"
	"testing"

	"golang.org/x/image/font/sfnt"
)

func glyphOf(t *testing.T, data []byte, r rune) uint16 {
	t.Helper()
	f, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var b sfnt.Buffer
	g, err := f.GlyphIndex(&b, r)
	if err != nil || g == 0 {
		t.Fatalf("%q: no glyph", r)
	}
	return uint16(g)
}

// TestCBDT: the pictures of Noto Color Emoji (cut down to 😀 and ❤): PNGs
// of its one strike, of 109 pixels, with their metrics.
func TestCBDT(t *testing.T) {
	data, err := os.ReadFile("../../tests/testdata/fonts/NotoColorEmoji-subset.ttf")
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenBitmaps(bytes.NewReader(data), 0)
	if err != nil || b == nil {
		t.Fatalf("%v %v", b, err)
	}
	g := glyphOf(t, data, '😀')
	if !b.Has(g) || b.Has(g+50) {
		t.Error("Has")
	}
	bm, ok := b.Glyph(g, 40)
	if !ok || bm.PPEM != 109 || bm.Width < 100 || bm.Height < 100 || bm.Top <= 0 {
		t.Fatalf("😀: %+v %v", bm, ok)
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(bm.PNG))
	if err != nil || cfg.Width != bm.Width || cfg.Height != bm.Height {
		t.Errorf("😀's PNG: %+v %v", cfg, err)
	}
	if _, ok := b.Glyph(g+50, 40); ok {
		t.Error("a glyph beyond the font has a picture")
	}
	// Cut short: no panic.
	for n := 100; n < len(data); n += 700 {
		if b, err := OpenBitmaps(bytes.NewReader(data[:n]), 0); err == nil && b != nil {
			b.Glyph(g, 40)
		}
	}
}

// TestSbix: the picture of an sbix font (made for these tests): 20 by 20
// pixels at 20 pixels, its bottom left corner 2 pixels under the baseline.
func TestSbix(t *testing.T) {
	data, err := os.ReadFile("../../tests/testdata/fonts/SbixTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	b, err := OpenBitmaps(bytes.NewReader(data), 0)
	if err != nil || b == nil {
		t.Fatalf("%v %v", b, err)
	}
	g := glyphOf(t, data, '✨')
	bm, ok := b.Glyph(g, 60)
	if !ok || bm.PPEM != 20 || bm.Width != 20 || bm.Height != 20 || bm.Left != 0 || bm.Top != 18 {
		t.Fatalf("✨: %+v %v", bm, ok)
	}
	if !b.Has(g) || b.Has(1) {
		t.Error("Has")
	}
}

// TestPickStrike: the smallest strike at least as large as the text, else
// the largest.
func TestPickStrike(t *testing.T) {
	for _, c := range []struct {
		ppems []int
		ppem  float64
		want  int
	}{
		{[]int{20, 40, 160}, 30, 1},
		{[]int{160, 20, 40}, 41, 0},
		{[]int{20, 40}, 100, 1},
		{[]int{20, 40}, 5, 0},
		{nil, 5, -1},
	} {
		if got := pick(c.ppems, c.ppem); got != c.want {
			t.Errorf("%v at %v: %d, want %d", c.ppems, c.ppem, got, c.want)
		}
	}
}
