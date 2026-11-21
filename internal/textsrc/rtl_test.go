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
