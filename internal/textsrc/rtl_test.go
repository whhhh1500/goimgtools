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
		// سلام: meem isolated, lam-alef final, seen initial, seen left to right.
		{"سلام", []rune{0xFEE1, 0xFEFC, 0xFEB3}},
	} {
		src, err := New(c.text, Options{Size: 20}, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		s := src.(*source)
		if len(s.lines) != 1 {
			t.Fatalf("%q: %d lines", c.text, len(s.lines))
		}
		var got []rune
		for i, g := range s.lines[0].glyphs {
			got = append(got, g.r)
			if i > 0 && g.x < s.lines[0].glyphs[i-1].x {
				t.Errorf("%q: glyph %d at %v, left of the one before", c.text, i, g.x)
			}
		}
		if string(got) != string(c.want) {
			t.Errorf("%q: laid out %q, want %q", c.text, string(got), string(c.want))
		}
		src.Close()
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
		want []string
	}{
		{"aaaa​bbbb", []string{"aaaa​", "bbbb"}},
		{"aaaa­bbbb", []string{"aaaa-", "bbbb"}},
		// Where the line does not break, the soft hyphen shows nothing.
		{"aa­b", []string{"aa­b"}},
	} {
		probe, err := New("aaaab", Options{Size: 30}, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		w := probe.(*source).lines[0].width.Ceil() // room for four letters and a hyphen, not eight
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
