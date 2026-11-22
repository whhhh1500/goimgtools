package textsrc

import (
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// FuzzLayout: any text, in the shaping test font, the colour one and the built-in one, at
// any width, lays out without a panic; every line's glyphs go left to
// right and the line is as wide as where its last one ends.
func FuzzLayout(f *testing.F) {
	for _, s := range []string{
		"नमस्ते दुनिया", "क्षत्रिय हिन्दी", "◌ि ◌ा 1ा", "कि​र्क­प्र",
		"abc אבג (def) 123", "⁧abc⁩ ‮तेन‬", "á̂b‍‌",
		"مرحبا لا إله", "لَا", string(rune(0xFEFF)) + "‏⁦", "ภาษาไทย", "", "A ✨" + string(rune(0xFE0F)) + " 👨" + string(rune(0x200D)) + "👩 👨" + string(rune(0xFE0E)) + " 1" + string(rune(0x20E3)),
	} {
		f.Add(s, uint16(0))
		f.Add(s, uint16(60))
	}
	font := []string{"../../tests/testdata/fonts/NotoSansDevanagari-subset.ttf", "../../tests/testdata/fonts/ColourTest.ttf", BuiltIn}
	f.Fuzz(func(t *testing.T, text string, width uint16) {
		if len(text) > 400 {
			return
		}
		src, err := New(text, Options{Size: 20, Fonts: font, Width: int(width % 600)}, pipeline.NewEnv(0, ""))
		if err != nil {
			return // no text to draw, or an image too large
		}
		s := src.(*source)
		for _, l := range s.lines {
			for i, g := range l.glyphs {
				if i > 0 && g.x < l.glyphs[i-1].x {
					t.Fatalf("%q: glyph %d left of the one before", text, i)
				}
			}
			if n := len(l.glyphs); n > 0 && l.width != l.glyphs0End(n, s) {
				t.Fatalf("%q: line %v wide, its last glyph ends at %v", text, l.width, l.glyphs0End(n, s))
			}
		}
		for {
			if _, err := src.Next(); err != nil {
				break
			}
		}
		src.Close()
	})
}
