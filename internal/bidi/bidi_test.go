package bidi

import (
	"math/rand"
	"slices"
	"strings"
	"testing"
)

// seen is the characters of s in the order Visual gives, those drawn as
// nothing left out.
func seen(s string, rtl bool) string {
	rs := []rune(s)
	order, out := Visual(rs, rtl)
	var v []rune
	for _, i := range order {
		if !Ignorable(out[i]) {
			v = append(v, out[i])
		}
	}
	return string(v)
}

// TestVisual: the order of lines of mixed directions, as the Unicode
// Bidirectional Algorithm has it (checked against all of Unicode 17's
// BidiTest.txt and BidiCharacterTest.txt when it was written).
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
		// Paired brackets (rule N0): a bracketed phrase of left-to-right text
		// at the end of right-to-left text keeps its brackets.
		{"אבג abc(def)", true, "abc(def) גבא"},
		{"אבג (abc) דה", true, "הד (abc) גבא"},
		// An override turns left-to-right letters.
		{"x ‮abc‬ y", false, "x cba y"},
		// A first-strong isolate takes the direction of its first letter,
		// apart from the text around it.
		{"x ⁨אב cd⁩ y", false, "x cd בא y"},
		// A right-to-left mark keeps the neutral before it with the
		// right-to-left text: without it, the ! goes with the paragraph.
		{"אב! abc", false, "בא! abc"},
		{"אב!‏ abc", false, "!בא abc"},
	} {
		if got := seen(c.in, c.rtl); got != c.want {
			t.Errorf("%q (rtl %v): %q, want %q", c.in, c.rtl, got, c.want)
		}
	}
	if !BaseRTL([]rune("123 שלום abc")) || BaseRTL([]rune("abc שלום")) {
		t.Error("BaseRTL: the first strong character")
	}
	if !HasRTL([]rune("a ب")) || HasRTL([]rune("abc 123")) || !HasRTL([]rune("‮abc")) {
		t.Error("HasRTL")
	}
	// P2: what is in an isolate does not decide the paragraph.
	if BaseRTL([]rune("⁧abc⁩ אב")) != true || BaseRTL([]rune("⁨אב⁩ abc")) != false {
		t.Error("BaseRTL: isolates skipped")
	}
}

// TestAnyText: text of every class, explicit marks and brackets in any
// order and depth, gives an order of all its characters.
func TestAnyText(t *testing.T) {
	pool := []rune{'a', 0x05D0, 0x0627, '1', '+', '$', 0x0660, ',', 0x2029, '\t', ' ', '!', 0x200B, 0x0300,
		0x202D, 0x202E, 0x202A, 0x202B, 0x202C, 0x2066, 0x2067, 0x2068, 0x2069, '(', ')', '[', ']', 0x2329, 0x3009}
	rnd := rand.New(rand.NewSource(1))
	for n := 0; n < 20000; n++ {
		rs := make([]rune, 1+rnd.Intn(40))
		for i := range rs {
			rs[i] = pool[rnd.Intn(len(pool))]
		}
		order, out := Visual(rs, n%2 == 0)
		if len(order) != len(rs) || len(out) != len(rs) {
			t.Fatalf("%U: %d places", rs, len(order))
		}
		seen := make([]bool, len(rs))
		for _, k := range order {
			if seen[k] {
				t.Fatalf("%U: %d twice", rs, k)
			}
			seen[k] = true
		}
	}
	// Deeper than the levels and the pairs can go.
	for _, s := range []string{strings.Repeat("‫", 300) + "abc", strings.Repeat("⁧", 300) + "abc", strings.Repeat("(", 300) + "א" + strings.Repeat(")", 300)} {
		if order, _ := Visual([]rune(s), false); len(order) != len([]rune(s)) {
			t.Fatal("deep text")
		}
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
		// A zero width non-joiner parts the letters (می‌خواهم's می), a joiner
		// joins a letter to nothing, other format characters do not part
		// them.
		{"می‌خ", []rune{0xFEE3, 0xFBFD, 0x200C, 0xFEA5}},
		{"ب‍", []rune{0xFE91, 0x200D}},
		{"ب‏ب", []rune{0xFE91, 0x200F, 0xFE90}},
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
