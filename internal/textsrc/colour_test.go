package textsrc

import (
	"image"
	"image/color"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

const colourFont = "../../tests/testdata/fonts/ColourTest.ttf"

// renderText renders text and gives the image.
func renderText(t *testing.T, text string, o Options) *image.RGBA {
	t.Helper()
	src, err := New(text, o, pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	img, err := pipeline.Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	return img.(*image.RGBA)
}

// colours counts the opaque colours of an image.
func colours(img *image.RGBA) map[color.RGBA]int {
	out := map[color.RGBA]int{}
	for y := img.Rect.Min.Y; y < img.Rect.Max.Y; y++ {
		for x := img.Rect.Min.X; x < img.Rect.Max.X; x++ {
			if c := img.RGBAAt(x, y); c.A == 255 {
				out[c]++
			}
		}
	}
	return out
}

// TestColourGlyphs: a colour glyph is drawn in its layers' colours, a layer
// of the text's colour in it; a glyph without layers in the text's colour.
func TestColourGlyphs(t *testing.T) {
	fg := color.RGBA{10, 20, 30, 255}
	cs := colours(renderText(t, "✨", Options{Size: 60, Fonts: []string{colourFont}, Color: fg}))
	for _, want := range []color.RGBA{{255, 0, 0, 255}, {0, 0, 255, 255}, fg} {
		if cs[want] < 20 {
			t.Errorf("✨: %d pixels of %v (%v)", cs[want], want, cs)
		}
	}
	cs = colours(renderText(t, "A", Options{Size: 60, Fonts: []string{colourFont}, Color: fg}))
	if len(cs) != 1 || cs[fg] == 0 {
		t.Errorf("A: %v", cs)
	}
	// Half transparent text: its colour glyphs too.
	img := renderText(t, "✨", Options{Size: 60, Fonts: []string{colourFont}, Color: color.NRGBA{10, 20, 30, 128}})
	if cs := colours(img); len(cs) != 0 {
		t.Errorf("half transparent ✨ has opaque pixels: %v", cs)
	}
}

// TestEmojiSequence: an emoji joined to another by a zero width joiner is
// drawn as the one glyph the font makes of the two.
func TestEmojiSequence(t *testing.T) {
	one := renderText(t, "👨", Options{Size: 50, Fonts: []string{colourFont}})
	two := renderText(t, "👨👩", Options{Size: 50, Fonts: []string{colourFont}})
	joined := renderText(t, "👨‍👩", Options{Size: 50, Fonts: []string{colourFont}})
	if w1, w2, wj := one.Rect.Dx(), two.Rect.Dx(), joined.Rect.Dx(); wj != w1 || w2 <= w1 {
		t.Errorf("widths: one %d, two %d, joined %d", w1, w2, wj)
	}
}

// TestColourFontFirst: a colour emoji is drawn from the first font that
// has it in colour, though a font before it has it in one colour; asked
// for as text (U+FE0E), or a character that is not an emoji, from the
// first that has it.
func TestColourFontFirst(t *testing.T) {
	fonts := []string{"../../tests/testdata/fonts/MonoTest.ttf", colourFont}
	for _, c := range []struct {
		text, want string
	}{
		{"✨\uFE0F", "Colour Test"},
		{"✨\uFE0E", "Mono Test"},
		{"✨", "Colour Test"}, // an emoji by default (Emoji_Presentation)
		{"A", "Mono Test"},
		{"👨", "Colour Test"},
		{"👨\uFE0E", "Mono Test"},
	} {
		src, err := New(c.text, Options{Size: 20, Fonts: fonts}, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		s := src.(*source)
		if f := s.set.fonts[s.lines[0].glyphs[0].font].Info.Family; f != c.want {
			t.Errorf("%+q from %s, want %s", c.text, f, c.want)
		}
		src.Close()
	}
}

// TestColourPictures: glyphs of bitmap colour fonts (sbix, CBDT) are their
// pictures, scaled to the size of the text.
func TestColourPictures(t *testing.T) {
	cs := colours(renderText(t, "✨", Options{Size: 40, Fonts: []string{"../../tests/testdata/fonts/SbixTest.ttf"}}))
	// 20 by 20 pixels at 20 pixels, twice as large: half red, half blue.
	for _, want := range []color.RGBA{{255, 0, 0, 255}, {0, 0, 255, 255}} {
		if cs[want] < 500 {
			t.Errorf("sbix ✨: %d pixels of %v", cs[want], want)
		}
	}
	img := renderText(t, "😀", Options{Size: 40, Fonts: []string{"../../tests/testdata/fonts/NotoColorEmoji-subset.ttf"}})
	n := 0
	for _, c := range colours(img) {
		n += c
	}
	if n < 600 {
		t.Errorf("CBDT 😀: %d opaque pixels", n)
	}
}

// TestLatinLigatures: the Latin text of a font that makes ligatures by
// default is shaped: f and i are its ligature; a space still parts words.
func TestLatinLigatures(t *testing.T) {
	src, err := New("fi fif", Options{Size: 20, Fonts: []string{"../../tests/testdata/fonts/LigaTest.ttf"}}, pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	gs := src.(*source).lines[0].glyphs
	if len(gs) != 6 || gs[0].sh == nil || !gs[0].sh.head || len(gs[0].sh.glyphs) != 1 || gs[1].sh == nil || gs[1].sh.head || gs[2].sh != nil {
		t.Errorf("fi fif: %+v", gs)
	}
}
