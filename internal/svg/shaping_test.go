package svg

import (
	"fmt"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/textsrc"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// testFont is Noto Sans Devanagari cut down to a few words, with the
// dotted circle a vowel sign without its consonant is drawn on.
const testFont = "../../tests/testdata/fonts/NotoSansDevanagari-subset.ttf"

// inkWidth draws a document and gives the width of what it inks, and the
// memory left reserved after it is closed.
func inkWidth(t *testing.T, doc string, limit int64) (int, int64, error) {
	t.Helper()
	env := pipeline.NewEnv(limit, "")
	s, err := Open([]byte(doc), env)
	if err != nil {
		return 0, env.Budget.Used(), err
	}
	img, err := pipeline.Collect(s)
	s.Close()
	if err != nil {
		return 0, env.Budget.Used(), err
	}
	b := img.Bounds()
	x0, x1 := b.Max.X, b.Min.X
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a > 0x8000 {
				x0, x1 = min(x0, x), max(x1, x+1)
			}
		}
	}
	return max(0, x1-x0), env.Budget.Used(), nil
}

func textDoc(attrs, text string) string {
	return fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" width="400" height="80"><text x="10" y="55" font-size="36" font-family="'%s'" %s>%s</text></svg>`, testFont, attrs, text)
}

// TestShapedPlacing: characters moved by dx or turned by rotate are
// shaped with their neighbours (their cluster moves with its first): the
// conjunct of स्त stays one, and no vowel sign is drawn on a dotted
// circle of its own.
func TestShapedPlacing(t *testing.T) {
	plain, _, err := inkWidth(t, textDoc("", "नमस्ते कि"), 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		attrs string
		extra int // the most dx adds
	}{
		{`dx="0 4 4 4 4 4 4 4 4"`, 32},
		{`rotate="0 2 4 6 8 10 12 14 16"`, 8},
		{`dy="0 0 0 0 3"`, 0},
	} {
		w, _, err := inkWidth(t, textDoc(c.attrs, "नमस्ते कि"), 0)
		if err != nil {
			t.Fatal(err)
		}
		if w > plain+c.extra+2 {
			t.Errorf("%s: %d pixels wide, %d without (clusters parted)", c.attrs, w, plain)
		}
	}
	// A vowel sign that takes room, after a dotted circle: one circle.
	one, _, _ := inkWidth(t, textDoc("", "◌"), 0)
	two, _, _ := inkWidth(t, textDoc("", "◌ा"), 0)
	if two > 2*one {
		t.Errorf("◌ा: %d pixels wide, ◌ alone %d (a second circle)", two, one)
	}
}

// TestShapedBudget: what text and its shaping reserve is given back, at
// any limit, the document drawn or not.
func TestShapedBudget(t *testing.T) {
	doc := `<svg xmlns="http://www.w3.org/2000/svg" width="300" height="120">
<text x="10" y="40" font-size="30" font-family="'` + testFont + `', serif">नमस्ते क्षत्रिय</text>
<text x="10" y="90" font-size="30" font-family="serif" font-weight="bold">कि ◌ा abc</text>
</svg>`
	for limit := int64(1 << 19); limit <= 8<<20; limit += 1 << 18 {
		if _, left, _ := inkWidth(t, doc, limit); left != 0 {
			t.Fatalf("limit %d: %d bytes left reserved", limit, left)
		}
	}
}

// TestColourGlyph: a colour glyph (as a colour emoji) is filled layer by
// layer in its colours, a layer of the text's colour in the text's fill;
// emoji joined by a zero width joiner are the one glyph the font makes.
func TestColourGlyph(t *testing.T) {
	doc := func(text string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="200" height="80"><text x="10" y="60" font-size="50" fill="#0a141e" font-family="'../../tests/testdata/fonts/ColourTest.ttf'">` + text + `</text></svg>`
	}
	env := pipeline.NewEnv(0, "")
	s, err := Open([]byte(doc("✨")), env)
	if err != nil {
		t.Fatal(err)
	}
	img, err := pipeline.Collect(s)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	count := map[[3]uint32]int{}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := img.At(x, y).RGBA()
			if a == 0xffff {
				count[[3]uint32{r >> 8, g >> 8, bl >> 8}]++
			}
		}
	}
	for _, c := range [][3]uint32{{255, 0, 0}, {0, 0, 255}, {10, 20, 30}} {
		if count[c] < 20 {
			t.Errorf("✨: %d pixels of %v", count[c], c)
		}
	}
	one, _, _ := inkWidth(t, doc("👨"), 0)
	joined, _, _ := inkWidth(t, doc("👨‍👩"), 0)
	two, _, _ := inkWidth(t, doc("👨👩"), 0)
	if joined >= two || joined < one {
		t.Errorf("widths: one %d, joined %d, two %d", one, joined, two)
	}
}

// TestLetterSpacingJoined: letter spacing does not part Arabic letters
// (as Chrome): Arabic with it is as wide as without.
func TestLetterSpacingJoined(t *testing.T) {
	set, err := textsrc.Load([]string{"Segoe UI"}, nil, func(int64) error { return nil })
	if err != nil {
		t.Skip("no Segoe UI")
	}
	set.Close(nil)
	doc := func(ls string) string {
		return `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="80"><text x="10" y="55" font-size="36" font-family="Segoe UI" letter-spacing="` + ls + `">مرحبا</text></svg>`
	}
	a, _, _ := inkWidth(t, doc("0"), 0)
	b, _, _ := inkWidth(t, doc("6"), 0)
	if a == 0 || b != a {
		t.Errorf("مرحبا: %d pixels wide spaced, %d not", b, a)
	}
}

// TestColourPicture: a glyph of a bitmap colour font is its picture,
// scaled to the font size.
func TestColourPicture(t *testing.T) {
	doc := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="80"><text x="10" y="60" font-size="40" font-family="'../../tests/testdata/fonts/SbixTest.ttf'">✨</text></svg>`
	s, err := Open([]byte(doc), pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	img, err := pipeline.Collect(s)
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	// 40 by 40 pixels from (10, 24): red to x 30, blue after.
	at := func(x, y int) [4]uint32 {
		r, g, b, a := img.At(x, y).RGBA()
		return [4]uint32{r >> 8, g >> 8, b >> 8, a >> 8}
	}
	if c := at(20, 40); c != [4]uint32{255, 0, 0, 255} {
		t.Errorf("left: %v", c)
	}
	if c := at(40, 40); c != [4]uint32{0, 0, 255, 255} {
		t.Errorf("right: %v", c)
	}
	if c := at(60, 40); c[3] != 0 {
		t.Errorf("beyond: %v", c)
	}
}
