package svg

import (
	"bytes"
	"io"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
)

// charsetReader reads a document in the encoding its XML declaration
// names, by the labels of the WHATWG Encoding Standard as browsers read
// them (iso-8859-1 and us-ascii as windows-1252); one unknown, or UTF-16
// (already made UTF-8 if it was), is read as UTF-8.
func charsetReader(label string, in io.Reader) (io.Reader, error) {
	if e := charsetOf(label); e != nil {
		return e.NewDecoder().Reader(in), nil
	}
	return in, nil
}

func charsetOf(label string) encoding.Encoding {
	l := strings.ToLower(strings.TrimSpace(label))
	switch l {
	case "us-ascii", "ascii", "ansi_x3.4-1968", "iso-8859-1", "iso8859-1", "iso_8859-1", "iso88591",
		"latin1", "l1", "cp819", "ibm819", "iso-ir-100", "csisolatin1", "windows-1252", "cp1252", "x-cp1252":
		return charmap.Windows1252
	case "iso-8859-2", "iso8859-2", "iso_8859-2", "latin2", "l2", "csisolatin2":
		return charmap.ISO8859_2
	case "iso-8859-3", "iso8859-3", "iso_8859-3", "latin3", "l3":
		return charmap.ISO8859_3
	case "iso-8859-4", "iso8859-4", "iso_8859-4", "latin4", "l4":
		return charmap.ISO8859_4
	case "iso-8859-5", "iso8859-5", "iso_8859-5", "cyrillic", "csisolatincyrillic":
		return charmap.ISO8859_5
	case "iso-8859-6", "iso8859-6", "iso_8859-6", "arabic", "asmo-708", "ecma-114":
		return charmap.ISO8859_6
	case "iso-8859-7", "iso8859-7", "iso_8859-7", "greek", "greek8", "elot_928", "ecma-118":
		return charmap.ISO8859_7
	case "iso-8859-8", "iso8859-8", "iso_8859-8", "hebrew", "visual", "iso-8859-8-i", "logical":
		return charmap.ISO8859_8
	case "iso-8859-10", "iso8859-10", "latin6", "l6":
		return charmap.ISO8859_10
	case "iso-8859-13", "iso8859-13", "iso_8859-13":
		return charmap.ISO8859_13
	case "iso-8859-14", "iso8859-14":
		return charmap.ISO8859_14
	case "iso-8859-15", "iso8859-15", "iso_8859-15", "latin9", "l9":
		return charmap.ISO8859_15
	case "iso-8859-16":
		return charmap.ISO8859_16
	case "iso-8859-9", "iso8859-9", "iso_8859-9", "latin5", "l5", "windows-1254", "cp1254":
		return charmap.Windows1254
	case "iso-8859-11", "iso8859-11", "tis-620", "windows-874", "dos-874":
		return charmap.Windows874
	case "windows-1250", "cp1250", "x-cp1250":
		return charmap.Windows1250
	case "windows-1251", "cp1251", "x-cp1251":
		return charmap.Windows1251
	case "windows-1253", "cp1253", "x-cp1253":
		return charmap.Windows1253
	case "windows-1255", "cp1255", "x-cp1255":
		return charmap.Windows1255
	case "windows-1256", "cp1256", "x-cp1256":
		return charmap.Windows1256
	case "windows-1257", "cp1257", "x-cp1257":
		return charmap.Windows1257
	case "windows-1258", "cp1258", "x-cp1258":
		return charmap.Windows1258
	case "koi8-r", "koi8_r", "koi8", "koi", "cskoi8r":
		return charmap.KOI8R
	case "koi8-u", "koi8-ru":
		return charmap.KOI8U
	case "macintosh", "mac", "x-mac-roman", "csmacintosh":
		return charmap.Macintosh
	case "x-mac-cyrillic", "x-mac-ukrainian":
		return charmap.MacintoshCyrillic
	case "ibm866", "cp866", "866", "csibm866":
		return charmap.CodePage866
	case "gbk", "gb2312", "gb_2312", "gb_2312-80", "chinese", "csgb2312", "csiso58gb231280", "iso-ir-58", "x-gbk":
		return simplifiedchinese.GBK
	case "gb18030":
		return simplifiedchinese.GB18030
	case "big5", "big5-hkscs", "cn-big5", "csbig5", "x-x-big5":
		return traditionalchinese.Big5
	case "shift_jis", "shift-jis", "sjis", "ms_kanji", "ms932", "windows-31j", "x-sjis", "csshiftjis":
		return japanese.ShiftJIS
	case "euc-jp", "x-euc-jp", "cseucpkdfmtjapanese":
		return japanese.EUCJP
	case "iso-2022-jp", "csiso2022jp":
		return japanese.ISO2022JP
	case "euc-kr", "ks_c_5601-1987", "ks_c_5601-1989", "ksc5601", "ksc_5601", "korean", "windows-949", "cp949",
		"csksc56011987", "iso-ir-149", "cseuckr":
		return korean.EUCKR
	}
	return nil
}

// utf16Order tells whether data is UTF-16, by its byte order mark or by
// the first character (an XML document's is '<'): 1 little-endian, 2
// big-endian, 0 not.
func utf16Order(data []byte) int {
	switch {
	case len(data) < 2:
		return 0
	case data[0] == 0xFF && data[1] == 0xFE, data[0] == '<' && data[1] == 0:
		return 1
	case data[0] == 0xFE && data[1] == 0xFF, data[0] == 0 && data[1] == '<':
		return 2
	}
	return 0
}

// fromUTF16 makes UTF-16 data UTF-8 (its byte order mark dropped).
func fromUTF16(data []byte, order int) []byte {
	if len(data) >= 2 && (data[0] == 0xFF && data[1] == 0xFE || data[0] == 0xFE && data[1] == 0xFF) {
		data = data[2:]
	}
	var out bytes.Buffer
	out.Grow(len(data) / 2)
	var buf [utf8.UTFMax]byte
	for i := 0; i+1 < len(data); i += 2 {
		u := rune(data[i]) | rune(data[i+1])<<8
		if order == 2 {
			u = rune(data[i])<<8 | rune(data[i+1])
		}
		if utf16.IsSurrogate(u) && i+3 < len(data) {
			v := rune(data[i+2]) | rune(data[i+3])<<8
			if order == 2 {
				v = rune(data[i+2])<<8 | rune(data[i+3])
			}
			if r := utf16.DecodeRune(u, v); r != utf8.RuneError {
				u = r
				i += 2
			}
		}
		n := utf8.EncodeRune(buf[:], u)
		out.Write(buf[:n])
	}
	return out.Bytes()
}
