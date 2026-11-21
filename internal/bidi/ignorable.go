package bidi

// Ignorable reports whether r is drawn as nothing and takes no room: the
// characters Unicode calls Default_Ignorable_Code_Point (joiners, marks of
// direction and isolates, variation selectors, tags, the soft hyphen where
// no line breaks at it), as HarfBuzz hides them: not the Hangul fillers,
// which fonts draw as spaces, nor the shorthand format controls.
func Ignorable(r rune) bool {
	switch {
	case r < 0xAD:
		return false
	case r == 0x00AD, r == 0x034F, r == 0x061C:
		return true
	case r >= 0x17B4 && r <= 0x17B5, r >= 0x180B && r <= 0x180F:
		return true
	case r >= 0x200B && r <= 0x200F, r >= 0x202A && r <= 0x202E, r >= 0x2060 && r <= 0x206F:
		return true
	case r >= 0xFE00 && r <= 0xFE0F, r == 0xFEFF, r >= 0xFFF0 && r <= 0xFFF8:
		return true
	case r >= 0x1D173 && r <= 0x1D17A, r >= 0xE0000 && r <= 0xE0FFF:
		return true
	}
	return false
}
