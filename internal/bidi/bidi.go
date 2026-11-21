// Package bidi lays out text of right-to-left scripts: the letters of
// Arabic and Persian joined as their neighbours have them (by the
// presentation forms of Unicode, which most fonts with Arabic hold) and
// the characters of a line put in the order they are seen, by the
// Unicode Bidirectional Algorithm (UAX #9) for text without explicit
// embeddings: its weak and neutral types resolved, the implicit levels,
// and the reordering of rule L2, mirrored brackets in right-to-left runs.
package bidi

import (
	xbidi "golang.org/x/text/unicode/bidi"
)

// HasRTL reports whether text holds a character of a right-to-left
// script.
func HasRTL(rs []rune) bool {
	for _, r := range rs {
		if c := class(r); c == xbidi.R || c == xbidi.AL {
			return true
		}
	}
	return false
}

func class(r rune) xbidi.Class {
	p, _ := xbidi.LookupRune(r)
	return p.Class()
}

// BaseRTL reports whether a paragraph runs right to left: its first strong
// character (rules P2 and P3) is of a right-to-left script.
func BaseRTL(rs []rune) bool {
	for _, r := range rs {
		switch class(r) {
		case xbidi.L:
			return false
		case xbidi.R, xbidi.AL:
			return true
		}
	}
	return false
}

// Visual gives the order in which the characters of a line are seen, left
// to right (order[i] is the place in rs of the i-th seen), and the
// characters, brackets mirrored where a right-to-left run holds them. rtl
// is the paragraph's direction.
func Visual(rs []rune, rtl bool) (order []int, out []rune) {
	n := len(rs)
	order = make([]int, n)
	out = append([]rune(nil), rs...)
	for i := range order {
		order[i] = i
	}
	if n == 0 {
		return order, out
	}
	base := 0
	if rtl {
		base = 1
	}
	t := make([]xbidi.Class, n)
	for i, r := range rs {
		t[i] = class(r)
	}
	sos := xbidi.L
	if rtl {
		sos = xbidi.R
	}
	// W1: a mark takes the type of what it marks.
	for i := range t {
		if t[i] == xbidi.NSM {
			if i == 0 {
				t[i] = sos
			} else {
				t[i] = t[i-1]
			}
		}
	}
	// W2: European digits after Arabic letters are Arabic numbers; W3: Arabic
	// letters are right to left.
	last := sos
	for i := range t {
		switch t[i] {
		case xbidi.L, xbidi.R, xbidi.AL:
			last = t[i]
		case xbidi.EN:
			if last == xbidi.AL {
				t[i] = xbidi.AN
			}
		}
	}
	for i := range t {
		if t[i] == xbidi.AL {
			t[i] = xbidi.R
		}
	}
	// W4: a single separator between two numbers of a kind joins them.
	for i := 1; i+1 < n; i++ {
		switch {
		case t[i] == xbidi.ES && t[i-1] == xbidi.EN && t[i+1] == xbidi.EN:
			t[i] = xbidi.EN
		case t[i] == xbidi.CS && t[i-1] == xbidi.EN && t[i+1] == xbidi.EN:
			t[i] = xbidi.EN
		case t[i] == xbidi.CS && t[i-1] == xbidi.AN && t[i+1] == xbidi.AN:
			t[i] = xbidi.AN
		}
	}
	// W5: terminators next to European numbers are of them.
	for i := 0; i < n; i++ {
		if t[i] != xbidi.ET {
			continue
		}
		j := i
		for j < n && t[j] == xbidi.ET {
			j++
		}
		if (i > 0 && t[i-1] == xbidi.EN) || (j < n && t[j] == xbidi.EN) {
			for k := i; k < j; k++ {
				t[k] = xbidi.EN
			}
		}
		i = j - 1
	}
	// W6: the separators and terminators left are neutral.
	for i := range t {
		switch t[i] {
		case xbidi.ES, xbidi.ET, xbidi.CS:
			t[i] = xbidi.ON
		}
	}
	// W7: European numbers after left-to-right letters are left to right.
	last = sos
	for i := range t {
		switch t[i] {
		case xbidi.L, xbidi.R:
			last = t[i]
		case xbidi.EN:
			if last == xbidi.L {
				t[i] = xbidi.L
			}
		}
	}
	// N1, N2: neutrals take the direction of the strong types on both
	// sides when it is the same (numbers counting as right to left), else
	// the paragraph's.
	strong := func(c xbidi.Class) (xbidi.Class, bool) {
		switch c {
		case xbidi.L:
			return xbidi.L, true
		case xbidi.R, xbidi.EN, xbidi.AN:
			return xbidi.R, true
		}
		return 0, false
	}
	eor := sos
	for i := 0; i < n; i++ {
		if _, ok := strong(t[i]); ok {
			continue
		}
		j := i
		for j < n {
			if _, ok := strong(t[j]); ok {
				break
			}
			j++
		}
		before, after := sos, eor
		if i > 0 {
			before, _ = strong(t[i-1])
		}
		if j < n {
			after, _ = strong(t[j])
		}
		dir := sos
		if before == after {
			dir = before
		}
		for k := i; k < j; k++ {
			t[k] = dir
		}
		i = j - 1
	}
	// I1, I2: the levels.
	lv := make([]int, n)
	most := base
	for i := range t {
		l := base
		if base == 0 {
			switch t[i] {
			case xbidi.R:
				l = 1
			case xbidi.AN, xbidi.EN:
				l = 2
			}
		} else {
			switch t[i] {
			case xbidi.L, xbidi.EN, xbidi.AN:
				l = 2
			}
		}
		lv[i] = l
		most = max(most, l)
	}
	// L1: white space at the end of the line is at the paragraph's level.
	for i := n - 1; i >= 0 && class(rs[i]) == xbidi.WS; i-- {
		lv[i] = base
	}
	// L4: brackets of right-to-left runs mirrored.
	for i, r := range out {
		if lv[i]%2 == 1 {
			out[i] = mirror(r)
		}
	}
	// L2: from the highest level to the lowest odd one, each run of
	// characters at that level or above reversed.
	lowestOdd := base | 1
	for l := most; l >= lowestOdd; l-- {
		for i := 0; i < n; {
			if lv[order[i]] < l {
				i++
				continue
			}
			j := i
			for j < n && lv[order[j]] >= l {
				j++
			}
			for a, b := i, j-1; a < b; a, b = a+1, b-1 {
				order[a], order[b] = order[b], order[a]
			}
			i = j
		}
	}
	return order, out
}

// mirror is the mirrored glyph of a bracket, or r.
func mirror(r rune) rune {
	switch r {
	case '(':
		return ')'
	case ')':
		return '('
	case '[':
		return ']'
	case ']':
		return '['
	case '{':
		return '}'
	case '}':
		return '{'
	case '<':
		return '>'
	case '>':
		return '<'
	case '«':
		return '»'
	case '»':
		return '«'
	case '‹':
		return '›'
	case '›':
		return '‹'
	}
	return r
}
