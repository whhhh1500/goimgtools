package textsrc

import (
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestRightToLeft: a line of Hebrew, with a number, and one of Arabic are
// laid out in the order they are seen, left to right, the Arabic letters
// in their joined forms, each glyph to the right of the one before.
func TestRightToLeft(t *testing.T) {
	for _, c := range []struct {
		text string
		want []rune
	}{
		{"שלום 123", []rune("123 םולש")},
		{"abc אבג", []rune("abc גבא")},
		// سلام, shaped by its font: its clusters seen right to left.
		{"سلام", []rune("مالس")},
	} {
		src, err := New(c.text, Options{Size: 20}, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		s := src.(*source)
		if len(s.lines) != 1 {
			t.Fatalf("%q: %d lines", c.text, len(s.lines))
		}
		if c.text == "سلام" && !s.set.fonts[s.lines[0].glyphs[0].font].has(&s.set.buf, 'س') {
			t.Log("no font with Arabic")
			src.Close()
			continue
		}
		var got []rune
		for i, g := range s.lines[0].glyphs {
			got = append(got, g.r)
			if i > 0 && g.x < s.lines[0].glyphs[i-1].x {
				t.Errorf("%q: glyph %d at %v, left of the one before", c.text, i, g.x)
			}
			if g.sh != nil && g.sh.head && len(g.sh.glyphs) == 0 {
				t.Errorf("%q: cluster %d has no glyph", c.text, i)
			}
		}
		if string(got) != string(c.want) {
			t.Errorf("%q: laid out %q, want %q", c.text, string(got), string(c.want))
		}
		src.Close()
	}
}

// TestShaped: Devanagari in a font with its tables is shaped: a cluster of
// several characters is drawn by its first, the conjunct's glyph is not a
// letter's own, and a line breaks only between clusters.
func TestShaped(t *testing.T) {
	font := []string{"../../tests/testdata/fonts/NotoSansDevanagari-subset.ttf"}
	src, err := New("नमस्ते", Options{Size: 40, Fonts: font}, pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	s := src.(*source)
	gs := s.lines[0].glyphs
	heads, drawn := 0, 0
	for i, g := range gs {
		if g.sh == nil {
			t.Fatalf("character %d not shaped", i)
		}
		if g.sh.head {
			heads++
			drawn += len(g.sh.glyphs)
		}
	}
	// न, म, the half form of स, then ते: four clusters, five glyphs.
	if len(gs) != 6 || heads != 4 || drawn != 5 || gs[3].sh.head || gs[5].sh.head {
		t.Errorf("%d characters, %d clusters, %d glyphs", len(gs), heads, drawn)
	}
	if id, _ := s.set.fonts[0].f.GlyphIndex(&s.set.buf, 'स'); gs[2].sh.glyphs[0].id == id {
		t.Error("स् drawn as स")
	}
	width := s.lines[0].width
	src.Close()
	// At a width of three clusters, the line breaks before the one that
	// does not fit, never inside a cluster.
	src, err = New("नमस्ते नमस्ते", Options{Size: 40, Fonts: font, Width: width.Ceil()}, pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range src.(*source).lines {
		if g := l.glyphs[0]; g.sh != nil && !g.sh.head {
			t.Error("a line starts inside a cluster")
		}
	}
	if n := len(src.(*source).lines); n != 2 {
		t.Errorf("%d lines", n)
	}
	src.Close()
	// A zero width space between shaped words is still a place to break.
	src, err = New("नमस्ते​नमस्ते", Options{Size: 40, Fonts: font, Width: (width * 3 / 2).Ceil()}, pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatal(err)
	}
	if ls := src.(*source).lines; len(ls) != 2 || len(ls[0].glyphs) != 7 {
		t.Errorf("at a zero width space: %d lines, the first of %d characters", len(ls), len(ls[0].glyphs))
	}
	src.Close()
}

// TestArabicForms: where a font cannot shape, Arabic letters take their
// presentation forms, as before shaping; the alef of a lam-alef ligature
// is drawn as nothing.
func TestArabicForms(t *testing.T) {
	rs := []rune("سلام")
	arabicForms(rs, 0, len(rs))
	if want := []rune{0xFEB3, 0xFEFC, 0xFEFF, 0xFEE1}; string(rs) != string(want) {
		t.Errorf("%U, want %U", rs, want)
	}
}

// TestIgnorable: characters drawn as nothing (joiners, marks of direction,
// variation selectors, isolates, tags) take no room and open no font; a
// right-to-left mark still turns the order of what is neutral around it.
func TestIgnorable(t *testing.T) {
	width := func(text string) (int, int) {
		src, err := New(text, Options{Size: 30}, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		s := src.(*source)
		return s.lines[0].width.Round(), len(s.set.fonts)
	}
	plain, fonts := width("ab")
	for _, r := range []rune{0x200B, 0x200C, 0x200D, 0x200E, 0x200F, 0x00AD, 0xFEFF, 0xFE0F, 0x2066, 0x2069, 0x061C, 0xE0001} {
		if w, n := width("a" + string(r) + "b"); w != plain || n != fonts {
			t.Errorf("U+%04X: width %d with %d fonts, want %d with %d", r, w, n, plain, fonts)
		}
	}
}

// TestBreakAtInvisible: a line may break at a zero width space, and at a
// soft hyphen, which then shows as a hyphen.
func TestBreakAtInvisible(t *testing.T) {
	for _, c := range []struct {
		text string
		room string // the width of the line: room for it, no more (in any font)
		want []string
	}{
		{"aaaa​aaaa", "aaaa", []string{"aaaa​", "aaaa"}},
		{"aaaa­bbbb", "aaaa-", []string{"aaaa-", "bbbb"}},
		// Where the line does not break, the soft hyphen shows nothing.
		{"aa­b", "aaaa-", []string{"aa­b"}},
	} {
		probe, err := New(c.room, Options{Size: 30}, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		w := probe.(*source).lines[0].width.Ceil()
		probe.Close()
		src, err := New(c.text, Options{Size: 30, Width: w}, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, l := range src.(*source).lines {
			var rs []rune
			for _, g := range l.glyphs {
				rs = append(rs, g.r)
			}
			got = append(got, string(rs))
		}
		src.Close()
		if len(got) != len(c.want) {
			t.Errorf("%q: lines %q, want %q", c.text, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%q: lines %q, want %q", c.text, got, c.want)
				break
			}
		}
	}
}
