// Package shape shapes text by the tables of its font, as HarfBuzz does
// (through its port to Go in github.com/go-text/typesetting): the letters
// of scripts that join, reorder or stack (Arabic, the scripts of India,
// Thai, Tibetan, Myanmar, Khmer, ...) put in the forms and places the font
// gives them, marks set on their letters, ligatures made.
//
// A font is read for it through a table directory of its own that lists
// only the tables shaping needs, so its outlines, pictures and other
// tables are never read: a collection of Chinese fonts that would take
// 40MB to parse whole takes under 1MB. A font with variations has its
// outlines and their variations read too, to be drawn at an instance.
package shape

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"sort"
	"unicode"

	"github.com/go-text/typesetting/font"
	ot "github.com/go-text/typesetting/font/opentype"
	"github.com/go-text/typesetting/harfbuzz"
	"github.com/go-text/typesetting/language"

	"github.com/whhhh1500/goimgtools/internal/otdir"
)

// tables are those read for shaping: the character map, the advances and
// what the font's layout features are made of (OpenType's and Apple's).
var tables = map[string]bool{
	"cmap": true, "head": true, "hhea": true, "maxp": true, "hmtx": true, "OS/2": true, "post": true,
	"GSUB": true, "GPOS": true, "GDEF": true, "kern": true,
	"morx": true, "kerx": true, "trak": true, "ankr": true, "feat": true,
}

// MaxBytes is the most a font's shaping tables may hold; a font with more
// is not shaped (no font of the systems this was tried on comes near).
const MaxBytes = 16 << 20

// variationTables are read as well for a font with variations (fvar): its
// outlines, their variations and those of its advances, so that it can be
// drawn at another instance than its default.
var variationTables = map[string]bool{
	"fvar": true, "avar": true, "gvar": true, "glyf": true, "loca": true, "HVAR": true, "MVAR": true, "CFF2": true,
}

// Face is the shaping tables of a font.
type Face struct {
	font     *harfbuzz.Font
	variable bool // with variations, and their outlines
	// Bytes is the memory it is counted as: three times its tables, as
	// measured (a parsed table takes up to 2.5 times its bytes).
	Bytes int64
}

// view is a font made of the tables shaping needs: a table directory at
// its start, then the tables, read from where they are in the file.
type view struct {
	r    io.ReaderAt
	head []byte
	segs []segment
	size int64
	pos  int64
}

type segment struct{ at, n, from int64 }

func (v *view) ReadAt(p []byte, off int64) (int, error) {
	n := 0
	for n < len(p) {
		o := off + int64(n)
		switch {
		case o < int64(len(v.head)):
			n += copy(p[n:], v.head[o:])
		default:
			i := sort.Search(len(v.segs), func(i int) bool { return v.segs[i].at+v.segs[i].n > o })
			if i == len(v.segs) {
				return n, io.EOF
			}
			s := v.segs[i]
			if o < s.at { // the padding before a table
				k := min(int64(len(p)-n), s.at-o)
				clear(p[n : n+int(k)])
				n += int(k)
				continue
			}
			k := min(int64(len(p)-n), s.at+s.n-o)
			m, err := v.r.ReadAt(p[n:n+int(k)], s.from+o-s.at)
			n += m
			if err != nil && m < int(k) {
				return n, err
			}
		}
	}
	return n, nil
}

func (v *view) Read(p []byte) (int, error) {
	n, err := v.ReadAt(p, v.pos)
	v.pos += int64(n)
	return n, err
}

func (v *view) Seek(off int64, whence int) (int64, error) {
	switch whence {
	case io.SeekStart:
		v.pos = off
	case io.SeekCurrent:
		v.pos += off
	case io.SeekEnd:
		v.pos = v.size + off
	}
	return v.pos, nil
}

// Open reads the shaping tables of font number index of a font or
// collection file. err is set for a font that cannot be shaped with.
func Open(r io.ReaderAt, index int) (f *Face, err error) {
	defer func() {
		if recover() != nil {
			f, err = nil, errors.New("shape: the font's tables cannot be read")
		}
	}()
	dir, err := otdir.Read(r, index)
	if err != nil {
		return nil, err
	}
	pick := func(variable bool) (keep []otdir.Table, total int64) {
		for tag, t := range dir.Tables {
			if tables[tag] || variable && variationTables[tag] {
				keep = append(keep, t)
				total += t.Length
			}
		}
		return keep, total
	}
	_, variable := dir.Tables["fvar"]
	keep, total := pick(variable)
	if variable && total > MaxBytes {
		// Its outlines too large: shaped at its default instance.
		variable = false
		keep, total = pick(false)
	}
	if total > MaxBytes {
		return nil, errors.New("shape: the font's tables are too large")
	}
	sort.Slice(keep, func(i, j int) bool { return string(keep[i].Record[:4]) < string(keep[j].Record[:4]) })
	v := &view{r: r, head: make([]byte, 12+16*len(keep))}
	copy(v.head, dir.Version[:])
	binary.BigEndian.PutUint16(v.head[4:], uint16(len(keep)))
	at := (int64(len(v.head)) + 3) &^ 3
	for i, t := range keep {
		rec := v.head[12+16*i:]
		copy(rec, t.Record[:8]) // tag and checksum
		binary.BigEndian.PutUint32(rec[8:], uint32(at))
		binary.BigEndian.PutUint32(rec[12:], uint32(t.Length))
		v.segs = append(v.segs, segment{at: at, n: t.Length, from: t.Offset})
		at += (t.Length + 3) &^ 3
	}
	v.size = at
	ld, err := ot.NewLoader(v)
	if err != nil {
		return nil, err
	}
	ft, err := font.NewFont(ld)
	if err != nil {
		return nil, err
	}
	_, glyf := dir.Tables["glyf"]
	_, gvar := dir.Tables["gvar"]
	_, cff2 := dir.Tables["CFF2"]
	return &Face{font: harfbuzz.NewFont(font.NewFace(ft)), variable: variable && (glyf && gvar || cff2), Bytes: 3 * total}, nil
}

// Ligatures reports whether font number index of a font or collection file
// makes ligatures or contextual forms of Latin text by default: its GSUB
// table has liga, clig, calt or rlig among the features of the default
// language of the script latn, or of DFLT. Only the lists of the table are
// read.
func Ligatures(r io.ReaderAt, index int) bool {
	dir, err := otdir.Read(r, index)
	if err != nil {
		return false
	}
	t, ok := dir.Tables["GSUB"]
	if !ok || t.Length < 10 {
		return false
	}
	be := binary.BigEndian
	at := func(off int64, n int) []byte {
		if off < 0 || n < 0 || off+int64(n) > t.Length {
			return nil
		}
		b := make([]byte, n)
		if _, err := r.ReadAt(b, t.Offset+off); err != nil {
			return nil
		}
		return b
	}
	hdr := at(0, 10)
	if hdr == nil {
		return false
	}
	scripts, features := int64(be.Uint16(hdr[4:])), int64(be.Uint16(hdr[6:]))
	sc := at(scripts, 2)
	if sc == nil {
		return false
	}
	n := int(be.Uint16(sc))
	recs := at(scripts+2, 6*n)
	fc := at(features, 2)
	if recs == nil || fc == nil {
		return false
	}
	nf := int(be.Uint16(fc))
	for i := 0; i < n; i++ {
		tag := string(recs[6*i : 6*i+4])
		if tag != "latn" && tag != "DFLT" {
			continue
		}
		script := scripts + int64(be.Uint16(recs[6*i+4:]))
		d := at(script, 2)
		if d == nil || be.Uint16(d) == 0 {
			continue
		}
		lang := script + int64(be.Uint16(d))
		lh := at(lang, 6)
		if lh == nil {
			continue
		}
		k := int(be.Uint16(lh[4:]))
		idx := at(lang+6, 2*k)
		if idx == nil {
			continue
		}
		for j := 0; j < k; j++ {
			fi := int(be.Uint16(idx[2*j:]))
			if fi >= nf {
				continue
			}
			rec := at(features+2+6*int64(fi), 4)
			switch string(rec) {
			case "liga", "clig", "calt", "rlig":
				return true
			}
		}
	}
	return false
}

// Variable reports whether the face has variations (an fvar table) and the
// outlines to draw them (TrueType's with gvar, or CFF2).
func (f *Face) Variable() bool { return f.variable }

// Variation is the value of an axis of a font with variations, by its tag
// (four letters), in its design units.
type Variation struct {
	Tag   string
	Value float32
}

// SetVariations puts the face at an instance of its font: the axes named at
// their values (held within their ranges), the others at their defaults;
// tags the font has no axis of are passed over. Shaping, Outline and
// Advance then take it.
func (f *Face) SetVariations(vs []Variation) {
	if !f.variable {
		return
	}
	defer func() { recover() }() // tables that cannot be read: the instance stays
	var list []font.Variation
	for _, v := range vs {
		if len(v.Tag) == 4 {
			list = append(list, font.Variation{Tag: ot.MustNewTag(v.Tag), Value: v.Value})
		}
	}
	f.font.Face().SetVariations(list)
}

// Segment operations of an outline.
const (
	MoveTo = iota
	LineTo
	QuadTo
	CubeTo
)

// Segment is a piece of an outline: a move, a line or a quadratic or cubic
// curve to its last point, in font units, y up.
type Segment struct {
	Op   int
	Args [3][2]float32
}

// Outline appends the outline of glyph gid at the face's instance to segs;
// ok is false when it cannot be read.
func (f *Face) Outline(gid uint16, segs []Segment) (_ []Segment, ok bool) {
	defer func() {
		if recover() != nil {
			ok = false
		}
	}()
	o, ok := f.font.Face().GlyphDataOutline(font.GID(gid))
	if !ok {
		return segs, false
	}
	for _, s := range o.Segments {
		var g Segment
		switch s.Op {
		case ot.SegmentOpMoveTo:
			g.Op = MoveTo
		case ot.SegmentOpLineTo:
			g.Op = LineTo
		case ot.SegmentOpQuadTo:
			g.Op = QuadTo
		default:
			g.Op = CubeTo
		}
		for i, a := range s.Args {
			g.Args[i] = [2]float32{a.X, a.Y}
		}
		segs = append(segs, g)
	}
	return segs, true
}

// Advance is how far glyph gid moves the pen at the face's instance, in
// font units.
func (f *Face) Advance(gid uint16) (adv float32) {
	defer func() {
		if recover() != nil {
			adv = 0
		}
	}()
	return f.font.Face().HorizontalAdvance(font.GID(gid))
}

// OpenData is Open for a font held in memory.
func OpenData(data []byte, index int) (*Face, error) {
	return Open(bytes.NewReader(data), index)
}

// Glyph is a glyph of shaped text, in the units of the font's em square,
// y up.
type Glyph struct {
	ID uint16
	// Cluster is the place in the text of the first character of the
	// cluster the glyph is of: the characters that make it, which are
	// drawn and placed together.
	Cluster int
	// XAdvance is how far it moves the pen; XOffset, YOffset where it is
	// drawn from the pen.
	XAdvance, XOffset, YOffset int32
}

var undetermined = language.NewLanguage("und")

// Shaper shapes text; its buffers are kept from one text to the next.
type Shaper struct {
	buf *harfbuzz.Buffer
}

// Shape shapes rs[start:end], of one script and font, the rest of rs being
// its context; right to left when rtl. The glyphs come in the order they
// are seen, left to right. ok is false when the font's tables could not
// shape it.
func (s *Shaper) Shape(f *Face, rs []rune, start, end int, rtl bool) (gs []Glyph, ok bool) {
	defer func() {
		if recover() != nil {
			gs, ok = nil, false
		}
	}()
	if s.buf == nil {
		s.buf = harfbuzz.NewBuffer()
	} else {
		s.buf.Clear()
	}
	s.buf.AddRunes(rs, start, end-start)
	// A language of its own, so the guess does not take the process's from
	// its environment: the same text is shaped the same anywhere.
	s.buf.Props.Language = undetermined
	s.buf.GuessSegmentProperties()
	s.buf.Props.Direction = harfbuzz.LeftToRight
	if rtl {
		s.buf.Props.Direction = harfbuzz.RightToLeft
	}
	s.buf.Shape(f.font, nil)
	gs = make([]Glyph, len(s.buf.Info))
	for i, in := range s.buf.Info {
		p := s.buf.Pos[i]
		gs[i] = Glyph{ID: uint16(in.Glyph), Cluster: in.Cluster, XAdvance: p.XAdvance, XOffset: p.XOffset, YOffset: p.YOffset}
	}
	return gs, true
}

// Needs reports whether r is shaped by the tables of its font: a letter of
// a script whose letters join, reorder or stack, or a mark set on a letter
// (which the font places). Latin, Greek, Cyrillic, Chinese, Japanese,
// Korean and the like are laid out letter by letter, as without shaping.
func Needs(r rune) bool {
	switch {
	case r < 0x0300:
		return false
	case r >= 0x2E80 && r <= 0xA4CF, r >= 0xAC00 && r <= 0xD7FF, r >= 0xF900 && r <= 0xFAFF, r >= 0xFF00 && r <= 0xFFEF, r >= 0x20000:
		return unicode.Is(unicode.Mn, r)
	}
	return unicode.In(r, unicode.Mn, unicode.Me) || unicode.In(r, complexScripts...)
}

// complexScripts are those whose letters join, reorder or stack: those
// HarfBuzz has a shaper of their own for.
var complexScripts = []*unicode.RangeTable{
	// Arabic and the scripts that join as it does.
	unicode.Arabic, unicode.Syriac, unicode.Thaana, unicode.Nko, unicode.Mandaic, unicode.Mongolian,
	unicode.Phags_Pa, unicode.Manichaean, unicode.Psalter_Pahlavi, unicode.Adlam, unicode.Hanifi_Rohingya,
	unicode.Sogdian, unicode.Old_Uyghur,
	// India's.
	unicode.Devanagari, unicode.Bengali, unicode.Gurmukhi, unicode.Gujarati, unicode.Oriya, unicode.Tamil,
	unicode.Telugu, unicode.Kannada, unicode.Malayalam,
	// Those of South-East Asia, the Himalayas and the others HarfBuzz's
	// universal shaper is for.
	unicode.Sinhala, unicode.Thai, unicode.Lao, unicode.Tibetan, unicode.Myanmar, unicode.Khmer,
	unicode.Balinese, unicode.Batak, unicode.Brahmi, unicode.Buginese, unicode.Buhid, unicode.Chakma,
	unicode.Cham, unicode.Dives_Akuru, unicode.Dogra, unicode.Grantha, unicode.Gunjala_Gondi, unicode.Hanunoo,
	unicode.Javanese, unicode.Kaithi, unicode.Kayah_Li, unicode.Kharoshthi, unicode.Khojki, unicode.Khudawadi,
	unicode.Lepcha, unicode.Limbu, unicode.Mahajani, unicode.Makasar, unicode.Marchen, unicode.Masaram_Gondi,
	unicode.Meetei_Mayek, unicode.Modi, unicode.Nandinagari, unicode.New_Tai_Lue, unicode.Newa, unicode.Rejang,
	unicode.Saurashtra, unicode.Sharada, unicode.Siddham, unicode.Soyombo, unicode.Sundanese,
	unicode.Syloti_Nagri, unicode.Tagalog, unicode.Tagbanwa, unicode.Tai_Tham, unicode.Tai_Viet,
	unicode.Takri, unicode.Tirhuta, unicode.Zanabazar_Square, unicode.Ahom, unicode.Bhaiksuki,
}
