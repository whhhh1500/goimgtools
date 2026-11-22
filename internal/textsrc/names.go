package textsrc

import (
	"encoding/binary"
	"io"
	"strings"
	"unicode/utf16"

	"github.com/whhhh1500/goimgtools/internal/otdir"
)

// englishNames reads a font's family, subfamily and full names from its
// name table in English: those of Windows in US English, else of the Mac
// in English, else the first of Unicode or Windows. Fonts name their
// styles in many languages ("Negreta", "Fett", "粗体"), and in a table
// sorted by language some come before English; golang.org/x/image's
// sfnt.Name takes the first. ok is false when the table cannot be read.
func englishNames(r io.ReaderAt, index int) (family, sub, full string, ok bool) {
	dir, err := otdir.Read(r, index)
	if err != nil {
		return "", "", "", false
	}
	t, err := dir.ReadTable(r, "name", 1<<20)
	if err != nil || len(t) < 6 {
		return "", "", "", false
	}
	count := int(binary.BigEndian.Uint16(t[2:]))
	strs := int(binary.BigEndian.Uint16(t[4:]))
	if 6+12*count > len(t) {
		count = (len(t) - 6) / 12
	}
	// Each name by how well it fits: 3 Windows US English, 2 Mac English,
	// 1 Unicode or other Windows; 0 none.
	var best [3]int
	var got [3]string
	for i := 0; i < count; i++ {
		rec := t[6+12*i:]
		platform, encoding := binary.BigEndian.Uint16(rec), binary.BigEndian.Uint16(rec[2:])
		lang, id := binary.BigEndian.Uint16(rec[4:]), binary.BigEndian.Uint16(rec[6:])
		length, off := int(binary.BigEndian.Uint16(rec[8:])), int(binary.BigEndian.Uint16(rec[10:]))
		var k int
		switch id {
		case 1:
			k = 0
		case 2:
			k = 1
		case 4:
			k = 2
		default:
			continue
		}
		rank := 0
		switch {
		case platform == 3 && (encoding == 1 || encoding == 10) && lang == 0x0409:
			rank = 3
		case platform == 1 && encoding == 0 && lang == 0:
			rank = 2
		case platform == 0 || platform == 3 && (encoding == 1 || encoding == 10):
			rank = 1
		}
		if rank <= best[k] || strs+off+length > len(t) {
			continue
		}
		b := t[strs+off : strs+off+length]
		var s string
		if platform == 1 {
			s = macRoman(b)
		} else {
			u := make([]uint16, len(b)/2)
			for j := range u {
				u[j] = binary.BigEndian.Uint16(b[2*j:])
			}
			s = string(utf16.Decode(u))
		}
		if s = strings.TrimSpace(s); s != "" {
			best[k], got[k] = rank, s
		}
	}
	return got[0], got[1], got[2], best[0] > 0 || best[2] > 0
}

// macRoman decodes the Mac's Roman encoding: ASCII as it is, the rest as
// the code page has it.
func macRoman(b []byte) string {
	var sb strings.Builder
	for _, c := range b {
		if c < 0x80 {
			sb.WriteByte(c)
		} else {
			sb.WriteRune(macRomanHigh[c-0x80])
		}
	}
	return sb.String()
}

var macRomanHigh = [128]rune{
	'Ä', 'Å', 'Ç', 'É', 'Ñ', 'Ö', 'Ü', 'á', 'à', 'â', 'ä', 'ã', 'å', 'ç', 'é', 'è',
	'ê', 'ë', 'í', 'ì', 'î', 'ï', 'ñ', 'ó', 'ò', 'ô', 'ö', 'õ', 'ú', 'ù', 'û', 'ü',
	'†', '°', '¢', '£', '§', '•', '¶', 'ß', '®', '©', '™', '´', '¨', '≠', 'Æ', 'Ø',
	'∞', '±', '≤', '≥', '¥', 'µ', '∂', '∑', '∏', 'π', '∫', 'ª', 'º', 'Ω', 'æ', 'ø',
	'¿', '¡', '¬', '√', 'ƒ', '≈', '∆', '«', '»', '…', ' ', 'À', 'Ã', 'Õ', 'Œ', 'œ',
	'–', '—', '“', '”', '‘', '’', '÷', '◊', 'ÿ', 'Ÿ', '⁄', '€', '‹', '›', 'ﬁ', 'ﬂ',
	'‡', '·', '‚', '„', '‰', 'Â', 'Ê', 'Á', 'Ë', 'È', 'Í', 'Î', 'Ï', 'Ì', 'Ó', 'Ô',
	'', 'Ò', 'Ú', 'Û', 'Ù', 'ı', 'ˆ', '˜', '¯', '˘', '˙', '˚', '¸', '˝', '˛', 'ˇ',
}
