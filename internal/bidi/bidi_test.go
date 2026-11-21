package bidi

import (
	"slices"
	"testing"
)

// seen is the characters of s in the order Visual gives.
func seen(s string, rtl bool) string {
	rs := []rune(s)
	order, out := Visual(rs, rtl)
	var v []rune
	for _, i := range order {
		v = append(v, out[i])
	}
	return string(v)
}

// TestVisual: the order of UAX #9's examples without embeddings.
func TestVisual(t *testing.T) {
	for _, c := range []struct {
		in   string
		rtl  bool
		want string
	}{
		{"abc", false, "abc"},
		{"שלום", false, "םולש"},
		{"שלום", true, "םולש"},
		// Numbers keep their order within right-to-left text.
		{"abc שלום 123", false, "abc 123 םולש"},
		{"שלום 123 abc", true, "abc 123 םולש"},
		// A neutral between two right-to-left runs goes with them.
		{"a אב גד b", false, "a דג בא b"},
		// Brackets are mirrored where right to left.
		{"(שלום)", true, "(םולש)"},
		// Arabic digits after Arabic letters.
		{"عدد ١٢٣", true, "١٢٣ ددع"},
	} {
		if got := seen(c.in, c.rtl); got != c.want {
			t.Errorf("%q (rtl %v): %q, want %q", c.in, c.rtl, got, c.want)
		}
	}
	if !BaseRTL([]rune("123 שלום abc")) || BaseRTL([]rune("abc שלום")) {
		t.Error("BaseRTL: the first strong character")
	}
	if !HasRTL([]rune("a ب")) || HasRTL([]rune("abc 123")) {
		t.Error("HasRTL")
	}
}

// TestShape: Arabic letters take the forms of their place; lam and alef
// make their ligature.
func TestShape(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []rune
	}{
		// سلام: seen initial, lam-alef final, meem isolated.
		{"سلام", []rune{0xFEB3, 0xFEFC, 0xFEE1}},
		// بيت: beh initial, yeh medial, teh final.
		{"بيت", []rune{0xFE91, 0xFEF4, 0xFE96}},
		// دار: dal isolated (it joins to the letter before only), alef isolated, reh isolated.
		{"دار", []rune{0xFEA9, 0xFE8D, 0xFEAD}},
		// A mark between two letters does not part them: بَب.
		{"بَب", []rune{0xFE91, 0x064E, 0xFE90}},
		// Persian: پدر.
		{"پدر", []rune{0xFB58, 0xFEAA, 0xFEAD}},
		{"abc", []rune("abc")},
	} {
		got, keep := Shape([]rune(c.in))
		if !slices.Equal(got, c.want) {
			t.Errorf("%q: %U, want %U", c.in, got, c.want)
		}
		if len(keep) != len(got) {
			t.Errorf("%q: %d places for %d characters", c.in, len(keep), len(got))
		}
	}
}
