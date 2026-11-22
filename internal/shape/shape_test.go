package shape

import (
	"bytes"
	"os"
	"testing"

	"golang.org/x/image/font/sfnt"
)

// The test font is Noto Sans Devanagari (SIL Open Font License 1.1,
// licenses/LICENSE-OFL-NOTO), cut down to the characters of the texts
// below with all its layout features.
const devanagari = "../../tests/testdata/fonts/NotoSansDevanagari-subset.ttf"

func cmapOf(t *testing.T, data []byte) func(r rune) uint16 {
	f, err := sfnt.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	var b sfnt.Buffer
	return func(r rune) uint16 {
		i, err := f.GlyphIndex(&b, r)
		if err != nil {
			t.Fatal(err)
		}
		return uint16(i)
	}
}

// TestDevanagari: the letters of Devanagari join, reorder and take their
// marks as the font's tables say.
func TestDevanagari(t *testing.T) {
	data, err := os.ReadFile(devanagari)
	if err != nil {
		t.Fatal(err)
	}
	f, err := OpenData(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	gid := cmapOf(t, data)
	var s Shaper
	shape := func(text string) []Glyph {
		rs := []rune(text)
		gs, ok := s.Shape(f, rs, 0, len(rs), false)
		if !ok {
			t.Fatalf("%q not shaped", text)
		}
		return gs
	}
	// स्ते: sa and virama make the half form of sa, ta and the vowel sign e
	// one cluster: five glyphs for six letters.
	if gs := shape("नमस्ते"); len(gs) != 5 || gs[2].Cluster != 2 || gs[2].ID == gid('स') || gs[4].Cluster != 4 || gs[4].XAdvance != 0 {
		t.Errorf("नमस्ते: %+v", gs)
	}
	// कि: the vowel sign i is drawn before ka, in its cluster.
	gs := shape("कि")
	if len(gs) != 2 || gs[1].ID != gid('क') || gs[0].ID == gid('क') || gs[0].Cluster != 0 || gs[1].Cluster != 0 {
		t.Errorf("कि: %+v", gs)
	}
	// दु: da with the vowel sign u is a glyph of its own in this font.
	gs = shape("दु")
	if len(gs) != 1 || gs[0].ID == gid('द') {
		t.Errorf("दु: %+v", gs)
	}
	// Advances are in the units of the em square.
	if gs := shape("0"); len(gs) != 1 || gs[0].XAdvance <= 0 || gs[0].XAdvance > 2048 {
		t.Errorf("0: %+v", gs)
	}
	// Right to left, the glyphs come in the order they are seen.
	if gs := shape("ab"); len(gs) != 2 || gs[0].Cluster != 0 {
		t.Errorf("ab: %+v", gs)
	}
	rs := []rune("ab")
	if gs, _ := s.Shape(f, rs, 0, 2, true); len(gs) != 2 || gs[0].Cluster != 1 {
		t.Errorf("ab right to left: %+v", gs)
	}
}

// TestOpen: only the layout tables are read, counted at three times their
// size; what is not a font fails, without a panic.
func TestOpen(t *testing.T) {
	data, err := os.ReadFile(devanagari)
	if err != nil {
		t.Fatal(err)
	}
	f, err := OpenData(data, 0)
	if err != nil {
		t.Fatal(err)
	}
	if f.Bytes <= 0 || f.Bytes > 3*int64(len(data)) {
		t.Errorf("counted %d bytes for a %d byte font", f.Bytes, len(data))
	}
	for _, bad := range [][]byte{nil, []byte("ttcf\x00\x01\x00\x00\x00\x00\x00\x05"), data[:200], append([]byte("ttcf\x00\x02\x00\x00\x00\x00\x00\x01\x00\x00\x00\x10"), data...)} {
		if _, err := OpenData(bad, 0); err == nil {
			t.Errorf("%d bytes opened", len(bad))
		}
	}
	// A font whose tables are cut short opens or fails, and shapes or
	// fails, without a panic.
	for n := 300; n < len(data); n += 997 {
		cut := append([]byte(nil), data[:n]...)
		if f, err := OpenData(cut, 0); err == nil {
			var s Shaper
			rs := []rune("नमस्ते कि")
			s.Shape(f, rs, 0, len(rs), false)
		}
	}
}

func TestNeeds(t *testing.T) {
	for _, c := range []struct {
		r    rune
		want bool
	}{
		{'a', false}, {'中', false}, {'ж', false}, {'א', false}, {'1', false}, {' ', false},
		{'न', true}, {'ب', true}, {'ก', true}, {'ཀ', true}, {'က', true}, {'ក', true},
		{0x0301, true}, {0x05B7, true}, // marks: acute accent, Hebrew patah
		{0xE001, false}, {0x0378, false}, {0x1F600, false}, // private use (icon fonts), unassigned, emoji
	} {
		if got := Needs(c.r); got != c.want {
			t.Errorf("%q: %v", c.r, got)
		}
	}
}

// TestLigatures: a font whose Latin text makes ligatures by default is
// told from one whose does not.
func TestLigatures(t *testing.T) {
	for _, c := range []struct {
		font string
		want bool
	}{
		{"../../tests/testdata/fonts/LigaTest.ttf", true},
		{"../../tests/testdata/fonts/ColourTest.ttf", false},
		{devanagari, false},
	} {
		data, err := os.ReadFile(c.font)
		if err != nil {
			t.Fatal(err)
		}
		if got := Ligatures(bytes.NewReader(data), 0); got != c.want {
			t.Errorf("%s: %v", c.font, got)
		}
		if Ligatures(bytes.NewReader(data[:40]), 0) {
			t.Errorf("%s cut short: ligatures", c.font)
		}
	}
}