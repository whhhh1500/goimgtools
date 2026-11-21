package svg

import (
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/whhhh1500/goimgtools/internal/textsrc"
)

// tchar is a character of a text element, with what places and paints it.
type tchar struct {
	r          rune
	st         style
	node       *node // the text or tspan it is in
	family     string
	face       string // "", " Bold", " Italic" or " Bold Italic"
	x, y       *float64
	dx, dy     float64
	dxSet      bool
	dySet      bool
	anchor     int // start, middle, end
	whitespace bool
}

const (
	anchorStart = iota
	anchorMiddle
	anchorEnd
)

// fonts gives the font set of a font-family list in a face (bold,
// italic): each family's font of that face when it is installed, else its
// regular one.
func (b *builder) fonts(family, face string) *textsrc.Set {
	key := family + "\x00" + face
	if s, ok := b.fontSets[key]; ok {
		return s
	}
	var specs []string
	for _, f := range strings.Split(family, ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		switch strings.ToLower(f) {
		case "", "serif", "sans-serif", "monospace", "cursive", "fantasy", "system-ui":
			continue // the default fonts stand for these
		}
		for _, name := range []string{f + face, f} {
			if fo, err := textsrc.Open(name); err == nil {
				fo.Close()
				specs = append(specs, name)
				break
			}
		}
	}
	s, err := textsrc.Load(specs, nil, b.reserve)
	if err != nil {
		s = nil
	}
	b.fontSets[key] = s
	return s
}

// text draws a text element: its characters laid out along a line from
// the positions given, each glyph's outline filled and stroked as its
// style says.
func (b *builder) text(n *node, m matrix, st style, vp viewport, own, outer *clip) {
	var chars []tchar
	var walk func(k *node, kst style, family string, bold, italic bool, anchor int)
	walk = func(k *node, kst style, family string, bold, italic bool, anchor int) {
		if v, ok := k.prop("font-family"); ok && strings.TrimSpace(v) != "inherit" {
			family = v
		}
		if v, ok := k.prop("font-weight"); ok {
			switch w := strings.TrimSpace(v); w {
			case "bold", "bolder", "600", "700", "800", "900":
				bold = true
			case "normal", "lighter", "100", "200", "300", "400", "500":
				bold = false
			}
		}
		if v, ok := k.prop("font-style"); ok {
			switch strings.TrimSpace(v) {
			case "italic", "oblique":
				italic = true
			case "normal":
				italic = false
			}
		}
		face := ""
		if bold {
			face += " Bold"
		}
		if italic {
			face += " Italic"
		}
		if v, ok := k.prop("text-anchor"); ok {
			switch strings.TrimSpace(v) {
			case "middle":
				anchor = anchorMiddle
			case "end":
				anchor = anchorEnd
			case "start":
				anchor = anchorStart
			}
		}
		xs := lengths(k.attrs["x"], kst, vp, axisX)
		ys := lengths(k.attrs["y"], kst, vp, axisY)
		dxs := lengths(k.attrs["dx"], kst, vp, axisX)
		dys := lengths(k.attrs["dy"], kst, vp, axisY)
		first := len(chars)
		addText := func(s string) {
			for _, r := range s {
				chars = append(chars, tchar{r: r, st: kst, node: k, family: family, face: face, anchor: anchor})
			}
		}
		// The character data and the tspans, in order.
		for _, c := range k.children {
			if c.name == "#text" {
				addText(c.text)
				continue
			}
			if c.name == "tspan" || c.name == "a" {
				cst := computeStyle(c, kst, vp)
				if cst.display {
					walk(c, cst, family, bold, italic, anchor)
				}
			}
		}
		// The element's positions go to its characters in order, but for
		// those an inner tspan places itself.
		for i := first; i < len(chars); i++ {
			j := i - first
			if j < len(xs) && chars[i].x == nil {
				chars[i].x = &xs[j]
			}
			if j < len(ys) && chars[i].y == nil {
				chars[i].y = &ys[j]
			}
			if j < len(dxs) && !chars[i].dxSet {
				chars[i].dx, chars[i].dxSet = dxs[j], true
			}
			if j < len(dys) && !chars[i].dySet {
				chars[i].dy, chars[i].dySet = dys[j], true
			}
		}
	}
	st.opacity = 1 // the group's, see element
	walk(n, st, "", false, false, anchorStart)
	chars = collapseSpace(chars, n.attrs["xml:space"] == "preserve")
	if len(chars) == 0 {
		return
	}
	// Lay out: chunks start where a character is placed absolutely.
	type placed struct {
		c      *tchar
		f      *textsrc.Font
		gi     sfnt.GlyphIndex
		x, y   float64
		chunk  int
		height float64
	}
	var out []placed
	var buf sfnt.Buffer
	penX, penY := 0.0, 0.0
	chunk := 0
	var chunkStart []float64
	var prev sfnt.GlyphIndex
	var prevFont *textsrc.Font
	for i := range chars {
		c := &chars[i]
		if c.x != nil || c.y != nil || i == 0 {
			if i > 0 {
				chunk++
			}
			if c.x != nil {
				penX = *c.x
			}
			if c.y != nil {
				penY = *c.y
			}
			chunkStart = append(chunkStart, penX)
			prevFont = nil
		}
		penX += c.dx
		penY += c.dy
		set := b.fonts(c.family, c.face)
		if set == nil {
			continue
		}
		f, err := set.Pick(c.r)
		if err != nil || f == nil {
			continue
		}
		sf := f.SFNT()
		upem := fixed.Int26_6(sf.UnitsPerEm()) << 6
		gi, err := sf.GlyphIndex(&buf, c.r)
		if err != nil {
			continue
		}
		scale := c.st.fontSize / float64(sf.UnitsPerEm())
		if prevFont == f {
			if k, err := sf.Kern(&buf, prev, gi, upem, font.HintingNone); err == nil {
				penX += float64(float64(float64(k)/64) * scale)
			}
		}
		out = append(out, placed{c: c, f: f, gi: gi, x: penX, y: penY, chunk: chunk})
		adv, err := sf.GlyphAdvance(&buf, gi, upem, font.HintingNone)
		if err == nil {
			penX += float64(float64(float64(adv)/64) * scale)
		}
		if chunk < len(chunkStart) {
			out[len(out)-1].height = penX // the chunk's end so far
		}
		prev, prevFont = gi, f
	}
	// Anchor each chunk: its width is from its start to its last pen.
	ends := map[int]float64{}
	anchors := map[int]int{}
	for _, p := range out {
		ends[p.chunk] = p.height
		if _, ok := anchors[p.chunk]; !ok {
			anchors[p.chunk] = p.c.anchor
		}
	}
	// Outlines, a run of characters of the same element at a time.
	var runSegs []seg
	var runNode *node
	var runStyle style
	flush := func() {
		if len(runSegs) > 0 && runNode != nil {
			b.shape(runNode, runSegs, m, runStyle, vp, own, outer)
		}
		runSegs, runNode = nil, nil
	}
	for _, p := range out {
		shift := 0.0
		if p.chunk < len(chunkStart) {
			w := ends[p.chunk] - chunkStart[p.chunk]
			switch anchors[p.chunk] {
			case anchorMiddle:
				shift = -w / 2
			case anchorEnd:
				shift = -w
			}
		}
		if p.c.node != runNode {
			flush()
			runNode, runStyle = p.c.node, p.c.st
		}
		sf := p.f.SFNT()
		upem := fixed.Int26_6(sf.UnitsPerEm()) << 6
		gsegs, err := sf.LoadGlyph(&buf, p.gi, upem, nil)
		if err != nil {
			continue
		}
		scale := p.c.st.fontSize / float64(sf.UnitsPerEm()) / 64
		ox, oy := p.x+shift, p.y
		pt := func(q fixed.Point26_6) [2]float64 {
			return [2]float64{ox + float64(float64(q.X)*scale), oy + float64(float64(q.Y)*scale)}
		}
		for _, g := range gsegs {
			switch g.Op {
			case sfnt.SegmentOpMoveTo:
				if len(runSegs) > 0 && runSegs[len(runSegs)-1].cmd != cmdClose {
					runSegs = append(runSegs, seg{cmd: cmdClose})
				}
				runSegs = append(runSegs, seg{cmd: cmdMove, pts: [3][2]float64{pt(g.Args[0])}})
			case sfnt.SegmentOpLineTo:
				runSegs = append(runSegs, seg{cmd: cmdLine, pts: [3][2]float64{pt(g.Args[0])}})
			case sfnt.SegmentOpQuadTo:
				runSegs = append(runSegs, seg{cmd: cmdQuad, pts: [3][2]float64{pt(g.Args[0]), pt(g.Args[1])}})
			case sfnt.SegmentOpCubeTo:
				runSegs = append(runSegs, seg{cmd: cmdCubic, pts: [3][2]float64{pt(g.Args[0]), pt(g.Args[1]), pt(g.Args[2])}})
			}
		}
		if len(runSegs) > 0 && runSegs[len(runSegs)-1].cmd != cmdClose {
			runSegs = append(runSegs, seg{cmd: cmdClose})
		}
		if len(runSegs) > maxSegs {
			flush()
		}
	}
	flush()
}

// lengths reads a list of lengths.
func lengths(s string, st style, vp viewport, axis int) []float64 {
	var out []float64
	for _, f := range splitList(s) {
		if v, ok := parseLength(f, st.fontSize, vp, axis); ok {
			out = append(out, v)
		}
	}
	return out
}

// collapseSpace applies SVG's default white space handling: newlines
// dropped, tabs made spaces, runs of spaces made one, leading and trailing
// ones dropped (the positions of the characters dropped go to the next).
func collapseSpace(cs []tchar, preserve bool) []tchar {
	out := cs[:0:0]
	for _, c := range cs {
		switch c.r {
		case '\n', '\r':
			if preserve {
				c.r = ' '
			} else {
				continue
			}
		case '\t':
			c.r = ' '
		}
		if !preserve && c.r == ' ' && (len(out) == 0 || out[len(out)-1].r == ' ') {
			continue
		}
		out = append(out, c)
	}
	if !preserve {
		for len(out) > 0 && out[len(out)-1].r == ' ' {
			out = out[:len(out)-1]
		}
	}
	return out
}
