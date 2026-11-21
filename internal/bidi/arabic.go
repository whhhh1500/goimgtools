package bidi

// The forms of the letters of Arabic and Persian, from Unicode's Arabic
// Presentation Forms: isolated, final, initial, medial (0: none). A letter
// with initial and medial forms joins on both sides; one without, only to
// the letter before it.
var arabicForms = map[rune][4]rune{
	0x0621: {0xFE80, 0, 0, 0},
	0x0622: {0xFE81, 0xFE82, 0, 0},
	0x0623: {0xFE83, 0xFE84, 0, 0},
	0x0624: {0xFE85, 0xFE86, 0, 0},
	0x0625: {0xFE87, 0xFE88, 0, 0},
	0x0626: {0xFE89, 0xFE8A, 0xFE8B, 0xFE8C},
	0x0627: {0xFE8D, 0xFE8E, 0, 0},
	0x0628: {0xFE8F, 0xFE90, 0xFE91, 0xFE92},
	0x0629: {0xFE93, 0xFE94, 0, 0},
	0x062A: {0xFE95, 0xFE96, 0xFE97, 0xFE98},
	0x062B: {0xFE99, 0xFE9A, 0xFE9B, 0xFE9C},
	0x062C: {0xFE9D, 0xFE9E, 0xFE9F, 0xFEA0},
	0x062D: {0xFEA1, 0xFEA2, 0xFEA3, 0xFEA4},
	0x062E: {0xFEA5, 0xFEA6, 0xFEA7, 0xFEA8},
	0x062F: {0xFEA9, 0xFEAA, 0, 0},
	0x0630: {0xFEAB, 0xFEAC, 0, 0},
	0x0631: {0xFEAD, 0xFEAE, 0, 0},
	0x0632: {0xFEAF, 0xFEB0, 0, 0},
	0x0633: {0xFEB1, 0xFEB2, 0xFEB3, 0xFEB4},
	0x0634: {0xFEB5, 0xFEB6, 0xFEB7, 0xFEB8},
	0x0635: {0xFEB9, 0xFEBA, 0xFEBB, 0xFEBC},
	0x0636: {0xFEBD, 0xFEBE, 0xFEBF, 0xFEC0},
	0x0637: {0xFEC1, 0xFEC2, 0xFEC3, 0xFEC4},
	0x0638: {0xFEC5, 0xFEC6, 0xFEC7, 0xFEC8},
	0x0639: {0xFEC9, 0xFECA, 0xFECB, 0xFECC},
	0x063A: {0xFECD, 0xFECE, 0xFECF, 0xFED0},
	0x0641: {0xFED1, 0xFED2, 0xFED3, 0xFED4},
	0x0642: {0xFED5, 0xFED6, 0xFED7, 0xFED8},
	0x0643: {0xFED9, 0xFEDA, 0xFEDB, 0xFEDC},
	0x0644: {0xFEDD, 0xFEDE, 0xFEDF, 0xFEE0},
	0x0645: {0xFEE1, 0xFEE2, 0xFEE3, 0xFEE4},
	0x0646: {0xFEE5, 0xFEE6, 0xFEE7, 0xFEE8},
	0x0647: {0xFEE9, 0xFEEA, 0xFEEB, 0xFEEC},
	0x0648: {0xFEED, 0xFEEE, 0, 0},
	0x0649: {0xFEEF, 0xFEF0, 0xFBE8, 0xFBE9},
	0x064A: {0xFEF1, 0xFEF2, 0xFEF3, 0xFEF4},
	0x067E: {0xFB56, 0xFB57, 0xFB58, 0xFB59}, // Persian peh
	0x0686: {0xFB7A, 0xFB7B, 0xFB7C, 0xFB7D}, // tcheh
	0x0698: {0xFB8A, 0xFB8B, 0, 0},           // jeh
	0x06A9: {0xFB8E, 0xFB8F, 0xFB90, 0xFB91}, // keheh
	0x06AF: {0xFB92, 0xFB93, 0xFB94, 0xFB95}, // gaf
	0x06CC: {0xFBFC, 0xFBFD, 0xFBFE, 0xFBFF}, // Farsi yeh
}

// lamAlef are the ligatures of lam and an alef: isolated, final.
var lamAlef = map[rune][2]rune{
	0x0622: {0xFEF5, 0xFEF6},
	0x0623: {0xFEF7, 0xFEF8},
	0x0625: {0xFEF9, 0xFEFA},
	0x0627: {0xFEFB, 0xFEFC},
}

// transparent reports whether r, a mark set on a letter, lets the letters
// on either side join.
func transparent(r rune) bool {
	return r >= 0x064B && r <= 0x065F || r == 0x0670 || r >= 0x06D6 && r <= 0x06ED
}

// joinsBoth reports whether r joins to the letters on both sides (tatweel
// too); joinsBefore, to the one before it.
func joinsBoth(r rune) bool {
	if r == 0x0640 {
		return true
	}
	f, ok := arabicForms[r]
	return ok && f[2] != 0
}

func joinsBefore(r rune) bool {
	if r == 0x0640 {
		return true
	}
	f, ok := arabicForms[r]
	return ok && f[1] != 0
}

// Shape puts the letters of Arabic and Persian in rs in the forms their
// neighbours give them, lam and alef as their ligature. keep[i] is the
// place in rs of out[i] (an alef taken into a ligature goes).
func Shape(rs []rune) (out []rune, keep []int) {
	out = make([]rune, 0, len(rs))
	keep = make([]int, 0, len(rs))
	// The letter before and after i, past marks.
	prev := func(i int) rune {
		for j := i - 1; j >= 0; j-- {
			if !transparent(rs[j]) {
				return rs[j]
			}
		}
		return 0
	}
	next := func(i int) (rune, int) {
		for j := i + 1; j < len(rs); j++ {
			if !transparent(rs[j]) {
				return rs[j], j
			}
		}
		return 0, -1
	}
	skip := -1
	for i, r := range rs {
		if i == skip {
			continue
		}
		f, ok := arabicForms[r]
		if !ok {
			out = append(out, r)
			keep = append(keep, i)
			continue
		}
		joinPrev := joinsBoth(prev(i))
		n, at := next(i)
		if r == 0x0644 && at == i+1 {
			if lig, ok := lamAlef[n]; ok {
				form := lig[0]
				if joinPrev {
					form = lig[1]
				}
				out = append(out, form)
				keep = append(keep, i)
				skip = at
				continue
			}
		}
		joinNext := f[2] != 0 && joinsBefore(n)
		form := f[0]
		switch {
		case joinPrev && joinNext && f[3] != 0:
			form = f[3]
		case joinPrev && f[1] != 0:
			form = f[1]
		case joinNext && f[2] != 0:
			form = f[2]
		}
		out = append(out, form)
		keep = append(keep, i)
	}
	return out, keep
}
