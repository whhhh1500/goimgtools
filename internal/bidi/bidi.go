// Package bidi lays out text of right-to-left scripts: the letters of
// Arabic and Persian joined as their neighbours have them (by the
// presentation forms of Unicode, which most fonts with Arabic hold) and
// the characters of a line put in the order they are seen, by the whole of
// the Unicode Bidirectional Algorithm (UAX #9: explicit embeddings,
// overrides and isolates, paired brackets), mirrored glyphs in
// right-to-left runs.
package bidi

import (
	"slices"

	xbidi "golang.org/x/text/unicode/bidi"
)

func class(r rune) Class {
	p, _ := xbidi.LookupRune(r)
	return p.Class()
}

// HasRTL reports whether text holds a character of a right-to-left script,
// or a mark that turns text right to left (an embedding, override or
// isolate of that direction).
func HasRTL(rs []rune) bool {
	for _, r := range rs {
		switch class(r) {
		case R, AL, RLE, RLO, RLI:
			return true
		}
	}
	return false
}

// BaseRTL reports whether a paragraph runs right to left: its first strong
// character, outside isolates (rules P2 and P3), is of a right-to-left
// script.
func BaseRTL(rs []rune) bool {
	if len(rs) == 0 {
		return false
	}
	p := &paragraph{initialTypes: classes(rs)}
	p.resultTypes = p.initialTypes
	p.determineMatchingIsolates()
	return p.determineParagraphEmbeddingLevel(0, p.Len()) == 1
}

// classes are the bidirectional classes of rs, a paragraph separator
// before the end taken as white space.
func classes(rs []rune) []Class {
	t := make([]Class, len(rs))
	for i, r := range rs {
		if t[i] = class(r); t[i] == B && i < len(rs)-1 {
			t[i] = WS
		}
	}
	return t
}

// Levels resolves the embedding levels of a paragraph (all its rules but
// those of lines): even for left to right, odd for right to left. rtl is
// its direction.
func Levels(rs []rune, rtl bool) []int8 {
	if len(rs) == 0 {
		return nil
	}
	types := classes(rs)
	pairTypes := make([]bracketType, len(rs))
	pairValues := make([]rune, len(rs))
	for i, r := range rs {
		if types[i] != ON {
			continue // only neutral brackets pair
		}
		// Brackets canonically equivalent pair as one.
		switch r {
		case 0x2329:
			r = 0x3008
		case 0x232A:
			r = 0x3009
		}
		if b, ok := bracketOf(r); ok {
			if b.open {
				pairTypes[i], pairValues[i] = bpOpen, r
			} else {
				pairTypes[i], pairValues[i] = bpClose, b.pair
			}
		}
	}
	lvl := level(0)
	if rtl {
		lvl = 1
	}
	p := newParagraph(types, pairTypes, pairValues, lvl)
	out := make([]int8, len(rs))
	for i, l := range p.resultLevels {
		out[i] = int8(l)
	}
	return out
}

// Reorder gives the order in which the characters of a line are seen, left
// to right (order[i] is the place in rs of the i-th seen), and the
// characters, mirrored where a right-to-left run holds them: rules L1, L2
// and L4. lv are the levels Levels gave the line's characters, rtl the
// paragraph's direction.
func Reorder(rs []rune, lv []int8, rtl bool) (order []int, out []rune) {
	n := len(rs)
	order = make([]int, n)
	for i := range order {
		order[i] = i
	}
	out = append([]rune(nil), rs...)
	if n == 0 {
		return order, out
	}
	base := int8(0)
	if rtl {
		base = 1
	}
	levels := append([]int8(nil), lv...)
	// L1: separators at the paragraph's level, and the white space (and
	// marks of format) before them and at the end of the line.
	end := true
	for i := n - 1; i >= 0; i-- {
		switch c := class(rs[i]); {
		case c == S || c == B:
			levels[i] = base
			end = true
		case end && isWhitespace(c):
			levels[i] = base
		default:
			end = false
		}
	}
	// L4: mirrored glyphs where right to left.
	most, lowestOdd := int8(0), int8(maxDepth+2)
	for i, l := range levels {
		if l%2 == 1 {
			out[i] = mirror(rs[i])
			lowestOdd = min(lowestOdd, l)
		}
		most = max(most, l)
	}
	// L2: from the highest level to the lowest odd one, each run of
	// characters at that level or above reversed.
	for l := most; l >= lowestOdd; l-- {
		for i := 0; i < n; {
			if levels[order[i]] < l {
				i++
				continue
			}
			j := i
			for j < n && levels[order[j]] >= l {
				j++
			}
			slices.Reverse(order[i:j])
			i = j
		}
	}
	return order, out
}

// Visual is the order of the characters of a line that is a paragraph of
// its own, rtl its direction, and the characters, as Reorder gives them.
func Visual(rs []rune, rtl bool) (order []int, out []rune) {
	return Reorder(rs, Levels(rs, rtl), rtl)
}

type bracket struct {
	r, pair rune
	open    bool
}

func bracketOf(r rune) (bracket, bool) {
	if r < 0x28 {
		return bracket{}, false
	}
	i, ok := slices.BinarySearchFunc(bracketTable[:], r, func(b bracket, r rune) int { return int(b.r - r) })
	if !ok {
		return bracket{}, false
	}
	return bracketTable[i], true
}

// mirror is the character whose glyph is that of r mirrored, or r.
func mirror(r rune) rune {
	if r < 0x28 {
		return r
	}
	i, ok := slices.BinarySearchFunc(mirrorTable[:], r, func(m [2]rune, r rune) int { return int(m[0] - r) })
	if !ok {
		return r
	}
	return mirrorTable[i][1]
}
