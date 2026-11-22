package svg

import (
	"unicode"

	"github.com/whhhh1500/goimgtools/internal/bidi"
	"github.com/whhhh1500/goimgtools/internal/shape"
	"github.com/whhhh1500/goimgtools/internal/textsrc"
)

// svgShape is how a character of shaped text is drawn: the first of a
// cluster (the characters its font draws together) has the cluster's
// glyphs, in the units of the font's em square and in the order they are
// seen; the others draw nothing.
type svgShape struct {
	font   *textsrc.Font
	head   bool
	glyphs []shape.Glyph
}

// cursive reports whether r is of a script whose letters join (Arabic and
// those that join as it does), which letter spacing does not part.
func cursive(r rune) bool {
	return r >= 0x0600 && unicode.In(r, unicode.Arabic, unicode.Syriac, unicode.Nko, unicode.Mongolian, unicode.Mandaic,
		unicode.Adlam, unicode.Hanifi_Rohingya)
}

// chunkEnd is where the chunk of characters from s ends: at the next one
// placed absolutely, or on another path.
func chunkEnd(chars []tchar, s int) int {
	e := s + 1
	for e < len(chars) && chars[e].x == nil && chars[e].y == nil && chars[e].tctx.path == chars[s].tctx.path {
		e++
	}
	return e
}

// shapeChars shapes the characters of scripts that join, reorder or stack
// by the tables of their fonts, in their logical order: a run of one font,
// one level and no absolute place after its first character at a time,
// across elements (a cluster is painted as its first character).
func (b *builder) shapeChars(chars []tchar) {
	rs := make([]rune, len(chars))
	for i, c := range chars {
		rs[i] = c.r
	}
	need := textsrc.NeedsShaping(rs)
	if need == nil {
		return
	}
	// The levels of the characters, each chunk a paragraph of its own.
	levels := make([]int8, len(chars))
	for s := 0; s < len(chars); {
		e := chunkEnd(chars, s)
		if seg := rs[s:e]; bidi.HasRTL(seg) {
			copy(levels[s:e], bidi.Levels(seg, chars[s].st.rtl))
		}
		s = e
	}
	fonts := make([]*textsrc.Font, len(chars))
	sets := make([]*textsrc.Set, len(chars))
	for i := range chars {
		if !need[i] {
			continue
		}
		c := &chars[i]
		sets[i] = b.fonts(c.family, c.face)
		if sets[i] == nil {
			need[i] = false
			continue
		}
		// A mark, or a character drawn as nothing, in the font of the one
		// before it when it has it.
		if i > 0 && need[i-1] && sets[i-1] == sets[i] && fonts[i-1] != nil && (bidi.Ignorable(c.r) || textsrc.IsMark(c.r) && fonts[i-1].Has(c.r)) {
			fonts[i] = fonts[i-1]
			continue
		}
		f, err := sets[i].PickFor(rs, i)
		if err != nil || f == nil {
			need[i] = false
			continue
		}
		fonts[i] = f
	}
	var sh shape.Shaper
	for i := 0; i < len(chars); {
		if !need[i] {
			i++
			continue
		}
		first := &chars[i]
		j := i + 1
		for j < len(chars) && need[j] && fonts[j] == fonts[i] && levels[j] == levels[i] {
			c := &chars[j]
			// Placed absolutely, a character starts a chunk of its own (as
			// in Chrome); moved by dx, dy or turned, it is shaped with the
			// others, its cluster moved and turned with its first character.
			if c.tctx.path != first.tctx.path || c.x != nil || c.y != nil {
				break
			}
			j++
		}
		face, err := sets[i].ShapingFace(fonts[i])
		if err != nil || face == nil {
			i = j
			continue
		}
		gs, ok := sh.Shape(face, rs, i, j, levels[i]%2 == 1)
		if !ok {
			i = j
			continue
		}
		for k := 0; k < len(gs); {
			c := gs[k].Cluster
			e := k
			for e < len(gs) && gs[e].Cluster == c {
				e++
			}
			if c >= i && c < j {
				if s := chars[c].sh; s != nil && s.head {
					s.glyphs = append(s.glyphs, gs[k:e]...)
				} else {
					chars[c].sh = &svgShape{font: fonts[i], head: true, glyphs: append([]shape.Glyph(nil), gs[k:e]...)}
				}
			}
			k = e
		}
		for k := i; k < j; k++ {
			if chars[k].sh == nil {
				chars[k].sh = &svgShape{font: fonts[i]}
			}
		}
		i = j
	}
}
