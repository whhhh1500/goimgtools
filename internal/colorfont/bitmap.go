package colorfont

import (
	"encoding/binary"
	"errors"
	"io"

	"github.com/whhhh1500/goimgtools/internal/otdir"
)

// Bitmaps is the colour pictures of a font's glyphs, as PNG files at a few
// sizes (strikes): CBLC and CBDT (as Noto Color Emoji) or sbix (as Apple
// Color Emoji). Only the index is held; a picture is read from the file
// when its glyph is drawn.
type Bitmaps struct {
	r io.ReaderAt
	// CBLC and CBDT.
	cblc    []byte
	cbdt    otdir.Table
	strikes []cbStrike
	// sbix.
	sbix      otdir.Table
	sbStrikes []sbStrike
	numGlyphs int
	Bytes     int64 // the memory it holds
}

type cbStrike struct {
	arrayOff, num int // the index subtables
	first, last   uint16
	ppem          int
}

type sbStrike struct {
	off  int64 // from the start of sbix
	ppem int
}

// Bitmap is the picture of a glyph.
type Bitmap struct {
	PNG []byte
	// Left and Top are where its top left corner is from the glyph's
	// origin (Top up), Width and Height its size, in pixels of its strike
	// (0 for Width and Height: as the PNG says).
	Left, Top, Width, Height int
	PPEM                     int // the size of its strike
}

// MaxPicture is the most a picture of a glyph may hold.
const MaxPicture = 4 << 20

// OpenBitmaps reads the index of the colour pictures of font number index
// of a font or collection file: nil and no error for a font without them.
func OpenBitmaps(r io.ReaderAt, index int) (*Bitmaps, error) {
	dir, err := otdir.Read(r, index)
	if err != nil {
		return nil, err
	}
	be := binary.BigEndian
	b := &Bitmaps{r: r}
	if t, ok := dir.Tables["CBDT"]; ok {
		cblc, err := dir.ReadTable(r, "CBLC", MaxBytes)
		if err != nil || cblc == nil {
			return nil, err
		}
		if len(cblc) < 8 {
			return nil, errors.New("colorfont: CBLC too short")
		}
		n := int(be.Uint32(cblc[4:]))
		if 8+48*n > len(cblc) {
			return nil, errors.New("colorfont: CBLC sizes beyond the table")
		}
		for i := 0; i < n; i++ {
			s := cblc[8+48*i:]
			st := cbStrike{arrayOff: int(be.Uint32(s)), num: int(be.Uint32(s[8:])), first: be.Uint16(s[40:]), last: be.Uint16(s[42:]), ppem: int(s[45])}
			if st.arrayOff+8*st.num > len(cblc) || st.ppem == 0 {
				continue
			}
			b.strikes = append(b.strikes, st)
		}
		if len(b.strikes) == 0 {
			return nil, nil
		}
		b.cblc, b.cbdt = cblc, t
		b.Bytes = int64(len(cblc))
		return b, nil
	}
	if t, ok := dir.Tables["sbix"]; ok {
		maxp, err := dir.ReadTable(r, "maxp", 64)
		if err != nil || len(maxp) < 6 {
			return nil, errors.New("colorfont: maxp unreadable")
		}
		b.numGlyphs = int(be.Uint16(maxp[4:]))
		hdr := make([]byte, 8)
		if _, err := r.ReadAt(hdr, t.Offset); err != nil {
			return nil, err
		}
		n := int(be.Uint32(hdr[4:]))
		if n > 64 || 8+4*int64(n) > t.Length {
			return nil, errors.New("colorfont: sbix strikes beyond the table")
		}
		offs := make([]byte, 4*n)
		if _, err := r.ReadAt(offs, t.Offset+8); err != nil {
			return nil, err
		}
		for i := 0; i < n; i++ {
			off := int64(be.Uint32(offs[4*i:]))
			sh := make([]byte, 4)
			if off+4+4*int64(b.numGlyphs+1) > t.Length {
				continue
			}
			if _, err := r.ReadAt(sh, t.Offset+off); err != nil {
				return nil, err
			}
			if ppem := int(be.Uint16(sh)); ppem > 0 {
				b.sbStrikes = append(b.sbStrikes, sbStrike{off: off, ppem: ppem})
			}
		}
		if len(b.sbStrikes) == 0 {
			return nil, nil
		}
		b.sbix = t
		b.Bytes = 64 * int64(len(b.sbStrikes))
		return b, nil
	}
	return nil, nil
}

// pick is the strike for a size: the smallest at least as large, else the
// largest.
func pick(ppems []int, ppem float64) int {
	best := -1
	for i, p := range ppems {
		switch {
		case best < 0:
			best = i
		case float64(p) >= ppem && (float64(ppems[best]) < ppem || p < ppems[best]):
			best = i
		case float64(ppems[best]) < ppem && p > ppems[best]:
			best = i
		}
	}
	return best
}

// Glyph is the picture of glyph g for text of ppem pixels; ok is false for
// a glyph without one.
func (b *Bitmaps) Glyph(g uint16, ppem float64) (bm Bitmap, ok bool) {
	if b == nil {
		return bm, false
	}
	if b.cblc != nil {
		var ppems []int
		var at []int
		for i, s := range b.strikes {
			if g >= s.first && g <= s.last {
				ppems, at = append(ppems, s.ppem), append(at, i)
			}
		}
		if i := pick(ppems, ppem); i >= 0 {
			return b.cbGlyph(b.strikes[at[i]], g)
		}
		return bm, false
	}
	ppems := make([]int, len(b.sbStrikes))
	for i, s := range b.sbStrikes {
		ppems[i] = s.ppem
	}
	if i := pick(ppems, ppem); i >= 0 {
		return b.sbGlyph(b.sbStrikes[i], g, 0)
	}
	return bm, false
}

// Has reports whether glyph g has a picture, from the index alone.
func (b *Bitmaps) Has(g uint16) bool {
	if b == nil {
		return false
	}
	be := binary.BigEndian
	if b.cblc != nil {
		for _, s := range b.strikes {
			if g < s.first || g > s.last {
				continue
			}
			for k := 0; k < s.num; k++ {
				rec := b.cblc[s.arrayOff+8*k:]
				if g >= be.Uint16(rec) && g <= be.Uint16(rec[2:]) {
					return true
				}
			}
		}
		return false
	}
	if int(g) >= b.numGlyphs || len(b.sbStrikes) == 0 {
		return false
	}
	o := make([]byte, 8)
	s := b.sbStrikes[0]
	if _, err := b.r.ReadAt(o, b.sbix.Offset+s.off+4+4*int64(g)); err != nil {
		return false
	}
	return be.Uint32(o[4:]) > be.Uint32(o)
}

// cbGlyph reads glyph g of a strike of CBLC and CBDT.
func (b *Bitmaps) cbGlyph(s cbStrike, g uint16) (bm Bitmap, ok bool) {
	be := binary.BigEndian
	c := b.cblc
	for k := 0; k < s.num; k++ {
		rec := c[s.arrayOff+8*k:]
		first, last := be.Uint16(rec), be.Uint16(rec[2:])
		if g < first || g > last {
			continue
		}
		sub := s.arrayOff + int(be.Uint32(rec[4:]))
		if sub+8 > len(c) {
			return bm, false
		}
		indexFormat, imageFormat := be.Uint16(c[sub:]), be.Uint16(c[sub+2:])
		dataOff := int64(be.Uint32(c[sub+4:]))
		i := int(g - first)
		var off, end int64
		var metrics []byte // big metrics, from the index (image format 19)
		switch indexFormat {
		case 1:
			p := sub + 8 + 4*i
			if p+8 > len(c) {
				return bm, false
			}
			off, end = int64(be.Uint32(c[p:])), int64(be.Uint32(c[p+4:]))
		case 3:
			p := sub + 8 + 2*i
			if p+4 > len(c) {
				return bm, false
			}
			off, end = int64(be.Uint16(c[p:])), int64(be.Uint16(c[p+2:]))
		case 2:
			if sub+20 > len(c) {
				return bm, false
			}
			size := int64(be.Uint32(c[sub+8:]))
			metrics = c[sub+12 : sub+20]
			off, end = int64(i)*size, int64(i+1)*size
		case 4, 5:
			var n, ids int
			var size int64
			if indexFormat == 4 {
				if sub+12 > len(c) {
					return bm, false
				}
				n, ids = int(be.Uint32(c[sub+8:])), sub+12
			} else {
				if sub+24 > len(c) {
					return bm, false
				}
				size = int64(be.Uint32(c[sub+8:]))
				metrics = c[sub+12 : sub+20]
				n, ids = int(be.Uint32(c[sub+20:])), sub+24
			}
			found := false
			for j := 0; j < n; j++ {
				if indexFormat == 4 {
					p := ids + 4*j
					if p+8 > len(c) {
						return bm, false
					}
					if be.Uint16(c[p:]) == g {
						off, end, found = int64(be.Uint16(c[p+2:])), int64(be.Uint16(c[p+6:])), true
						break
					}
				} else {
					p := ids + 2*j
					if p+2 > len(c) {
						return bm, false
					}
					if be.Uint16(c[p:]) == g {
						off, end, found = int64(j)*size, int64(j+1)*size, true
						break
					}
				}
			}
			if !found {
				return bm, false
			}
		default:
			return bm, false
		}
		n := end - off
		if n <= 0 || n > MaxPicture || dataOff+end > b.cbdt.Length {
			return bm, false
		}
		data := make([]byte, n)
		if _, err := b.r.ReadAt(data, b.cbdt.Offset+dataOff+off); err != nil {
			return bm, false
		}
		bm.PPEM = s.ppem
		switch imageFormat {
		case 17: // small metrics, then the PNG
			if len(data) < 9 {
				return bm, false
			}
			bm.Height, bm.Width = int(data[0]), int(data[1])
			bm.Left, bm.Top = int(int8(data[2])), int(int8(data[3]))
			data = data[5:]
		case 18: // big metrics, then the PNG
			if len(data) < 12 {
				return bm, false
			}
			bm.Height, bm.Width = int(data[0]), int(data[1])
			bm.Left, bm.Top = int(int8(data[2])), int(int8(data[3]))
			data = data[8:]
		case 19: // the PNG; its metrics in the index
			if metrics == nil || len(data) < 4 {
				return bm, false
			}
			bm.Height, bm.Width = int(metrics[0]), int(metrics[1])
			bm.Left, bm.Top = int(int8(metrics[2])), int(int8(metrics[3]))
		default:
			return bm, false
		}
		l := int64(be.Uint32(data))
		if l <= 0 || 4+l > int64(len(data)) {
			return bm, false
		}
		bm.PNG = data[4 : 4+l]
		return bm, true
	}
	return bm, false
}

// sbGlyph reads glyph g of a strike of sbix; depth counts the "dupe"
// glyphs followed.
func (b *Bitmaps) sbGlyph(s sbStrike, g uint16, depth int) (bm Bitmap, ok bool) {
	if int(g) >= b.numGlyphs || depth > 2 {
		return bm, false
	}
	be := binary.BigEndian
	o := make([]byte, 8)
	if _, err := b.r.ReadAt(o, b.sbix.Offset+s.off+4+4*int64(g)); err != nil {
		return bm, false
	}
	start, end := int64(be.Uint32(o)), int64(be.Uint32(o[4:]))
	n := end - start
	if n < 8 || n > MaxPicture || s.off+end > b.sbix.Length {
		return bm, false
	}
	data := make([]byte, n)
	if _, err := b.r.ReadAt(data, b.sbix.Offset+s.off+start); err != nil {
		return bm, false
	}
	x, y := int(int16(be.Uint16(data))), int(int16(be.Uint16(data[2:])))
	switch string(data[4:8]) {
	case "png ":
	case "dupe":
		if len(data) < 10 {
			return bm, false
		}
		return b.sbGlyph(s, be.Uint16(data[8:]), depth+1)
	default:
		return bm, false
	}
	png := data[8:]
	// The size of the picture, from its header; its bottom left corner is
	// at (x, y) from the origin.
	if len(png) < 24 || string(png[12:16]) != "IHDR" {
		return bm, false
	}
	w, h := int(be.Uint32(png[16:])), int(be.Uint32(png[20:]))
	if w <= 0 || h <= 0 || w > 4096 || h > 4096 {
		return bm, false
	}
	return Bitmap{PNG: png, Left: x, Top: y + h, Width: w, Height: h, PPEM: s.ppem}, true
}
