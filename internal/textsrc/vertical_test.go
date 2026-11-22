package textsrc

import (
	"testing"

	"golang.org/x/image/font/sfnt"
)

// TestVerticalMetrics: a font's vhea and vmtx give a glyph's advance down a
// vertical line and its top bearing; a font without them has none.
func TestVerticalMetrics(t *testing.T) {
	f, err := Open("../../tests/testdata/fonts/VertTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	var buf sfnt.Buffer
	for _, c := range []struct {
		r        rune
		adv, tsb int
	}{{'日', 1000, 300}, {'A', 1000, 300}} {
		gi, err := f.SFNT().GlyphIndex(&buf, c.r)
		if err != nil {
			t.Fatal(err)
		}
		if adv, tsb, ok := f.VerticalMetrics(gi); !ok || adv != c.adv || tsb != c.tsb {
			t.Errorf("%q: %d, %d, %v; want %d, %d", c.r, adv, tsb, ok, c.adv, c.tsb)
		}
	}
	g, err := Open("../../tests/testdata/fonts/MonoTest.ttf")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	if _, _, ok := g.VerticalMetrics(1); ok {
		t.Error("a font without vertical metrics has them")
	}
}
