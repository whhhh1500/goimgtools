package svg

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"unicode"

	"github.com/whhhh1500/goimgtools/internal/bidi"
	"github.com/whhhh1500/goimgtools/internal/colorfont"
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

// glyphPicture draws the picture of a glyph of a bitmap colour font: its
// pixels to the glyph's frame (fm, from user space to the device) at
// (gx, gy), scaled from its strike to the font size, sampled as an image
// element's are.
func (b *builder) glyphPicture(n *node, bm colorfont.Bitmap, fm matrix, gx, gy, fontSize float64, st style, own, outer *clip) {
	cfg, err := png.DecodeConfig(bytes.NewReader(bm.PNG))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width > 1024 || cfg.Height > 1024 || bm.PPEM <= 0 {
		return
	}
	px := int64(cfg.Width) * int64(cfg.Height)
	if b.imagePixels+px > maxImagePixels {
		return
	}
	if err := b.reserve(4 * px); err != nil {
		b.err = err
		return
	}
	b.sc.imageBytes += 4 * px
	b.imagePixels += px
	img, err := png.Decode(bytes.NewReader(bm.PNG))
	if err != nil {
		return
	}
	pic := image.NewNRGBA(img.Bounds())
	draw.Draw(pic, pic.Rect, img, img.Bounds().Min, draw.Src)
	w, h := bm.Width, bm.Height
	if w == 0 || h == 0 {
		w, h = cfg.Width, cfg.Height
	}
	// A pixel of the strike is k units of the text; the picture's top left
	// corner is at (Left, Top) pixels from the glyph's origin, Top up.
	k := fontSize / float64(bm.PPEM)
	sm := matrix{float64(k*float64(w)) / float64(cfg.Width), 0, 0, float64(k*float64(h)) / float64(cfg.Height),
		gx + float64(k*float64(bm.Left)), gy - float64(k*float64(bm.Top))}
	pm := fm.mul(sm)
	inv, ok := pm.invert()
	if !ok {
		return
	}
	area := fillPath(flatten(rectSegs(0, 0, float64(cfg.Width), float64(cfg.Height), 0, 0), pm), pm, false)
	sh := &shader{img: &sampler{img: pic, inv: inv}}
	if st.fillOpacity < 1 {
		sh.opacity = st.fillOpacity
	}
	it := item{op: opShape, path: area, paint: sh, bounds: pathBounds(area, b.full)}
	c := chainClip(own, outer)
	it.clip = c
	if c != nil {
		it.bounds = it.bounds.Intersect(c.bounds)
	}
	b.add(it)
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
	// The text of a font that makes ligatures of Latin text is shaped too
	// (as Calibri's ffi), but for spaces.
	for i := range chars {
		c := &chars[i]
		if need != nil && need[i] || !textsrc.Ligated(c.r) {
			continue
		}
		set := b.fonts(c.family, c.face)
		if set == nil {
			continue
		}
		if f, err := set.PickFor(rs, i); err == nil && f != nil && set.Ligates(f) {
			if need == nil {
				need = make([]bool, len(chars))
			}
			need[i] = true
		}
	}
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
