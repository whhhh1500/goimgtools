package textsrc

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"unicode"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
	"golang.org/x/image/vector"

	"github.com/whhhh1500/goimgtools/internal/bidi"
	"github.com/whhhh1500/goimgtools/internal/colorfont"
	"github.com/whhhh1500/goimgtools/internal/resize"
	"github.com/whhhh1500/goimgtools/internal/shape"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// sglyph is a glyph of a shaped cluster: its index in its font, and where
// it is drawn from the cluster's pen (y down).
type sglyph struct {
	id     sfnt.GlyphIndex
	dx, dy fixed.Int26_6
}

// shaped is how a character of shaped text is laid out: the first of a
// cluster (the characters the font draws together) carries its glyphs and
// its advance, the others nothing.
type shaped struct {
	font   int
	head   bool
	glyphs []sglyph
	adv    fixed.Int26_6
}

// layoutFace is the shaping tables of font i, opened (and counted in the
// budget) the first time; nil when the font has none that can be read.
func (s *Set) layoutFace(i int) (*shape.Face, error) {
	f := s.fonts[i]
	if f.shapeTried {
		return f.shaping, nil
	}
	f.shapeTried = true
	var face *shape.Face
	var err error
	switch {
	case f.file != nil:
		face, err = shape.Open(f.file, f.Info.Index)
	case f.data != nil:
		face, err = shape.OpenData(f.data, 0)
	default:
		return nil, nil
	}
	if err != nil {
		return nil, nil // laid out letter by letter
	}
	if err := s.reserve(face.Bytes); err != nil {
		return nil, err
	}
	s.held += face.Bytes
	f.shaping = face
	return face, nil
}

// isMark reports whether r is a mark set on the letter before it: one that
// takes no room (Mn, Me) or one that does (Mc, as the vowel signs of
// India's scripts after their consonant or a dotted circle).
func isMark(r rune) bool {
	return r >= 0x0300 && unicode.In(r, unicode.Mn, unicode.Me, unicode.Mc)
}

// colourFont is the colour layers of font i, read (and counted in the
// budget) the first time; nil for a font without them.
func (s *Set) colourFont(i int) (*colorfont.Font, error) {
	f := s.fonts[i]
	if f.colourTried {
		return f.colour, nil
	}
	f.colourTried = true
	var r io.ReaderAt
	index := f.Info.Index
	switch {
	case f.file != nil:
		r = f.file
	case f.data != nil:
		r, index = bytes.NewReader(f.data), 0
	default:
		return nil, nil
	}
	// Layers (COLR) or pictures (CBDT, sbix); a font that cannot be read
	// for them is drawn in one colour.
	c, err := colorfont.Open(r, index)
	if err != nil {
		c = nil
	}
	var bm *colorfont.Bitmaps
	if c == nil {
		if bm, err = colorfont.OpenBitmaps(r, index); err != nil {
			bm = nil
		}
	}
	var n int64
	if c != nil {
		n += c.Bytes
	}
	if bm != nil {
		n += bm.Bytes
	}
	if n > 0 {
		if err := s.reserve(n); err != nil {
			return nil, err
		}
		s.held += n
	}
	f.colour, f.bitmaps = c, bm
	return c, nil
}

// ColourLayers appends the colour layers of glyph id of f, a font of the
// set, to buf: none for a glyph drawn in one colour.
func (s *Set) ColourLayers(f *Font, id sfnt.GlyphIndex, buf []colorfont.Layer) ([]colorfont.Layer, error) {
	for i, g := range s.fonts {
		if g == f {
			c, err := s.colourFont(i)
			if err != nil {
				return buf, err
			}
			return c.Layers(uint16(id), buf), nil
		}
	}
	return buf, nil
}

// Picture is the colour picture of glyph id of f, a font of the set (a PNG
// of a bitmap colour font), for text of ppem pixels; ok is false for a
// glyph without one.
func (s *Set) Picture(f *Font, id sfnt.GlyphIndex, ppem float64) (bm colorfont.Bitmap, ok bool, err error) {
	for i, g := range s.fonts {
		if g == f {
			if _, err := s.colourFont(i); err != nil {
				return bm, false, err
			}
			bm, ok = g.bitmaps.Glyph(uint16(id), ppem)
			return bm, ok, nil
		}
	}
	return bm, false, nil
}

// ShapingFace is the shaping tables of f, a font of the set, read (and
// counted in the budget) the first time; nil when it has none that can be
// read.
func (s *Set) ShapingFace(f *Font) (*shape.Face, error) {
	for i, g := range s.fonts {
		if g == f {
			return s.layoutFace(i)
		}
	}
	return nil, nil
}

// IsMark reports whether r is a mark set on the letter before it.
func IsMark(r rune) bool { return isMark(r) }

// NeedsShaping reports which characters of a text are shaped by the tables
// of their font: those of scripts that join, reorder or stack, a mark with
// its letter, and the characters drawn as nothing between two shaped ones.
// nil when none is.
func NeedsShaping(rs []rune) []bool {
	n := len(rs)
	need := make([]bool, n)
	any := false
	for i, r := range rs {
		if shape.Needs(r) && !bidi.Ignorable(r) {
			need[i], any = true, true
			// A mark is shaped with its letter.
			if isMark(r) {
				j := i - 1
				for j >= 0 && (isMark(rs[j]) || bidi.Ignorable(rs[j])) {
					j--
				}
				for ; j >= 0 && j < i; j++ {
					need[j] = true
				}
			}
		}
	}
	// An emoji with what joins or modifies it (a zero width joiner, a
	// variation selector, a skin tone, a keycap, tags), and the two
	// regional indicators of a flag, are shaped together: the font's tables
	// make one glyph of the sequence.
	for i := 1; i < n; i++ {
		r := rs[i]
		switch {
		case (r == 0x200D || r == 0xFE0F || r == 0x20E3 || r >= 0x1F3FB && r <= 0x1F3FF || r >= 0xE0020 && r <= 0xE007F) && EmojiBase(rs[i-1]):
			need[i-1], need[i], any = true, true, true
			if r == 0x200D && i+1 < n {
				need[i+1] = true
			}
		case r >= 0x1F1E6 && r <= 0x1F1FF && rs[i-1] >= 0x1F1E6 && rs[i-1] <= 0x1F1FF:
			need[i-1], need[i], any = true, true, true
		}
	}
	if !any {
		return nil
	}
	// Characters drawn as nothing between two that are shaped go with
	// them (the joiners and non-joiners of Persian and of India's scripts),
	// but for the zero width space and the soft hyphen, where a line may
	// break (Thai marks its words with zero width spaces).
	between := func(r rune) bool { return bidi.Ignorable(r) && r != 0x200B && r != 0x00AD }
	for i := 0; i < n; i++ {
		if !between(rs[i]) || i == 0 || !need[i-1] {
			continue
		}
		j := i
		for j < n && between(rs[j]) {
			j++
		}
		if j < n && need[j] {
			for k := i; k < j; k++ {
				need[k] = true
			}
		}
		i = j - 1
	}
	return need
}

// shapeParagraph shapes the parts of a paragraph whose script needs it
// (NeedsShaping), each run of one font and one level at a time; out[i] is
// how character i is laid out, nil for one laid out letter by letter.
// Arabic letters of a run its font cannot shape are put in their
// presentation forms, as without shaping (rs is changed).
func (s *source) shapeParagraph(rs []rune, levels []int8, size float64) ([]*shaped, error) {
	n := len(rs)
	need := NeedsShaping(rs)
	if need == nil {
		return nil, nil
	}
	// The font of each: a mark or a character drawn as nothing in that of
	// the character before it, when it has it.
	fonts := make([]int, n)
	for i, r := range rs {
		if !need[i] {
			continue
		}
		if i > 0 && need[i-1] && (bidi.Ignorable(r) || isMark(r) && s.set.fonts[fonts[i-1]].has(&s.set.buf, r)) {
			fonts[i] = fonts[i-1]
			continue
		}
		fi, err := s.set.pickFor(rs, i)
		if err != nil {
			return nil, err
		}
		fonts[i] = fi
	}
	out := make([]*shaped, n)
	ppem := fixed.Int26_6(0.5 + (size * 72 * 64 / 72)) // as the faces have it
	var sh shape.Shaper
	for i := 0; i < n; {
		if !need[i] {
			i++
			continue
		}
		j := i + 1
		for j < n && need[j] && fonts[j] == fonts[i] && (levels == nil || levels[j] == levels[i]) {
			j++
		}
		rtl := levels != nil && levels[i]%2 == 1
		face, err := s.set.layoutFace(fonts[i])
		if err != nil {
			return nil, err
		}
		var gs []shape.Glyph
		ok := face != nil
		if ok {
			gs, ok = sh.Shape(face, rs, i, j, rtl)
		}
		if !ok {
			arabicForms(rs, i, j)
			i = j
			continue
		}
		upem := s.set.fonts[fonts[i]].f.UnitsPerEm()
		px := func(v int32) fixed.Int26_6 { return scaleUnits(v, ppem, upem) }
		// The clusters, each from the character of its value to that of
		// the next larger one (the glyphs come in the order they are seen:
		// their clusters rise left to right, fall right to left).
		for k := 0; k < len(gs); {
			c := gs[k].Cluster
			e := k
			var pen fixed.Int26_6
			sc := &shaped{font: fonts[i], head: true}
			for e < len(gs) && gs[e].Cluster == c {
				g := gs[e]
				sc.glyphs = append(sc.glyphs, sglyph{id: sfnt.GlyphIndex(g.ID), dx: pen + px(g.XOffset), dy: -px(g.YOffset)})
				pen += px(g.XAdvance)
				e++
			}
			sc.adv = pen
			switch {
			case c < i || c >= j:
			case out[c] == nil:
				out[c] = sc
			default: // glyphs of a cluster apart (not from HarfBuzz's own clusters): drawn after the others
				for _, g := range sc.glyphs {
					g.dx += out[c].adv
					out[c].glyphs = append(out[c].glyphs, g)
				}
				out[c].adv += sc.adv
			}
			k = e
		}
		// The others of each cluster draw nothing.
		for k := i; k < j; k++ {
			if out[k] == nil {
				out[k] = &shaped{font: fonts[i]}
			}
		}
		i = j
	}
	return out, nil
}

// scaleUnits turns v units of the em square into pixels at ppem, rounded
// as sfnt rounds the advances of the letters laid out one by one.
func scaleUnits(v int32, ppem fixed.Int26_6, upem sfnt.Units) fixed.Int26_6 {
	x := int64(v) * int64(ppem)
	u := int64(upem)
	if x >= 0 {
		x += u / 2
	} else {
		x -= u / 2
	}
	return fixed.Int26_6(x / u)
}

// arabicForms puts the Arabic letters of rs[from:to] in their presentation
// forms, joined as their neighbours in rs have them; an alef taken into a
// lam-alef ligature becomes a zero width no-break space, which is drawn as
// nothing.
func arabicForms(rs []rune, from, to int) {
	out, keep := bidi.Shape(rs)
	forms := make([]rune, len(rs))
	for i := range forms {
		forms[i] = 0xFEFF
	}
	for j, k := range keep {
		forms[k] = out[j]
	}
	copy(rs[from:to], forms[from:to])
}

// hasColour reports whether a line has a colour glyph.
func (s *source) hasColour(l line) bool {
	for _, g := range l.glyphs {
		c, bm := s.set.fonts[g.font].colour, s.set.fonts[g.font].bitmaps
		if c == nil && bm == nil {
			continue
		}
		colour := func(id uint16) bool {
			s.layers = c.Layers(id, s.layers[:0])
			return len(s.layers) > 0 || bm.Has(id)
		}
		if g.sh != nil {
			for _, sg := range g.sh.glyphs {
				if colour(uint16(sg.id)) {
					return true
				}
			}
			continue
		}
		if id, err := s.set.fonts[g.font].f.GlyphIndex(&s.set.buf, g.r); err == nil {
			if colour(uint16(id)) {
				return true
			}
		}
	}
	return false
}

// drawColour draws glyph id of font fi at dot as its colour layers, if it
// has them (false if not): each layer in its colour, its alpha times that
// of the text's colour, or in the text's colour for a layer of it; when
// src is not the text's colour (the shape of the text, for its outline
// and shadow), every layer in src.
func (s *source) drawColour(dst draw.Image, src image.Image, fi int, id sfnt.GlyphIndex, dot fixed.Point26_6) bool {
	c := s.set.fonts[fi].colour // read at layout
	if c == nil {
		return s.drawPicture(dst, src, fi, id, dot)
	}
	s.layers = c.Layers(uint16(id), s.layers[:0])
	if len(s.layers) == 0 {
		return false
	}
	text := src == image.Image(s.fg)
	_, _, _, fa := s.fg.C.RGBA()
	for _, l := range s.layers {
		lsrc := src
		if text && !l.Foreground {
			col := l.Color
			col.A = uint8((uint32(col.A)*fa + 0x7fff) / 0xffff)
			lsrc = image.NewUniform(col)
		}
		s.drawGlyph(dst, lsrc, fi, sfnt.GlyphIndex(l.Glyph), dot, s.size)
	}
	return true
}

// drawPicture draws the colour picture of glyph id of font fi (a PNG of a
// bitmap colour font) at dot, if it has one (false if not): scaled from its
// strike to the size of the text (as Pillow's Lanczos filter), its alpha
// times that of the text's colour; when src is not the text's colour (the
// shape of the text), its alpha as the shape.
func (s *source) drawPicture(dst draw.Image, src image.Image, fi int, id sfnt.GlyphIndex, dot fixed.Point26_6) bool {
	bm, ok := s.set.fonts[fi].bitmaps.Glyph(uint16(id), s.size)
	if !ok {
		return false
	}
	pic := s.picture(bm)
	if pic == nil {
		return false
	}
	scale := s.size / float64(bm.PPEM)
	w, h := bm.Width, bm.Height
	if w == 0 || h == 0 {
		w, h = pic.Bounds().Dx(), pic.Bounds().Dy()
	}
	dw, dh := max(1, int(math.Round(float64(float64(w)*scale)))), max(1, int(math.Round(float64(float64(h)*scale))))
	x0 := int(math.Round(float64(float64(dot.X)/64) + float64(float64(bm.Left)*scale)))
	y0 := int(math.Round(float64(float64(dot.Y)/64) - float64(float64(bm.Top)*scale)))
	scaled := pic
	if dw != pic.Bounds().Dx() || dh != pic.Bounds().Dy() {
		// Its memory is in what each font is counted as: the buffers its
		// glyphs are drawn in, which grow with the square of the size.
		r, err := resize.Pillow(pipeline.FromImage(pic, pipeline.NewEnv(0, "")), dw, dh, resize.PillowLanczos, resize.PillowOptions{})
		if err != nil {
			return false
		}
		img, err := pipeline.Collect(r)
		r.Close()
		if err != nil {
			return false
		}
		scaled = img
	}
	rect := image.Rect(x0, y0, x0+dw, y0+dh)
	sp := scaled.Bounds().Min
	if src == image.Image(s.fg) {
		_, _, _, fa := s.fg.C.RGBA()
		draw.DrawMask(dst, rect, scaled, sp, image.NewUniform(color.Alpha16{A: uint16(fa)}), image.Point{}, draw.Over)
	} else {
		draw.DrawMask(dst, rect, src, image.Point{}, scaled, sp, draw.Over)
	}
	return true
}

// picture decodes the PNG of a glyph's picture: nil if it is not one, or
// one larger than its strike says.
func (s *source) picture(bm colorfont.Bitmap) image.Image {
	cfg, err := png.DecodeConfig(bytes.NewReader(bm.PNG))
	if err != nil || cfg.Width > 1024 || cfg.Height > 1024 {
		return nil
	}
	pic, err := png.Decode(bytes.NewReader(bm.PNG))
	if err != nil {
		return nil
	}
	return pic
}

// drawGlyph draws glyph id of font fi with its origin at dot, as the
// font's face draws a character (the same rasterization).
func (s *source) drawGlyph(dst draw.Image, src image.Image, fi int, id sfnt.GlyphIndex, dot fixed.Point26_6, size float64) {
	ppem := fixed.Int26_6(0.5 + (size * 72 * 64 / 72))
	segments, err := s.set.fonts[fi].f.LoadGlyph(&s.set.buf, id, ppem, nil)
	if err != nil {
		return
	}
	dBounds := segments.Bounds().Add(dot)
	var dr image.Rectangle
	dr.Min.X = dBounds.Min.X.Floor()
	dr.Min.Y = dBounds.Min.Y.Floor()
	dr.Max.X = dBounds.Max.X.Ceil()
	dr.Max.Y = dBounds.Max.Y.Ceil()
	width, height := dr.Dx(), dr.Dy()
	if width <= 0 || height <= 0 {
		return
	}
	biasX := dot.X - fixed.Int26_6(dr.Min.X<<6)
	biasY := dot.Y - fixed.Int26_6(dr.Min.Y<<6)
	if cap(s.gmask.Pix) < width*height {
		s.gmask.Pix = make([]uint8, 2*width*height)
	}
	s.gmask.Pix = s.gmask.Pix[:width*height]
	s.gmask.Stride = width
	s.gmask.Rect = image.Rect(0, 0, width, height)
	if s.grast == nil {
		s.grast = &vector.Rasterizer{}
	}
	r := s.grast
	r.Reset(width, height)
	r.DrawOp = draw.Src
	p := func(q fixed.Point26_6) (float32, float32) {
		return float32(q.X+biasX) / 64, float32(q.Y+biasY) / 64
	}
	for _, seg := range segments {
		switch seg.Op {
		case sfnt.SegmentOpMoveTo:
			r.MoveTo(p(seg.Args[0]))
		case sfnt.SegmentOpLineTo:
			r.LineTo(p(seg.Args[0]))
		case sfnt.SegmentOpQuadTo:
			x1, y1 := p(seg.Args[0])
			x2, y2 := p(seg.Args[1])
			r.QuadTo(x1, y1, x2, y2)
		case sfnt.SegmentOpCubeTo:
			x1, y1 := p(seg.Args[0])
			x2, y2 := p(seg.Args[1])
			x3, y3 := p(seg.Args[2])
			r.CubeTo(x1, y1, x2, y2, x3, y3)
		}
	}
	r.Draw(&s.gmask, s.gmask.Bounds(), image.Opaque, image.Point{})
	draw.DrawMask(dst, dr, src, image.Point{}, &s.gmask, image.Point{}, draw.Over)
}
