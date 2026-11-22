package svg

import (
	"fmt"
	"image/color"
	"math"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/whhhh1500/goimgtools/internal/raster"
)

// The HTML a foreignObject holds (mermaid's labels are such) is drawn as
// its text: blocks and line breaks make its lines, each laid out as SVG
// text in the fonts and colours its CSS gives (the color property, as
// HTML has it), aligned by text-align, the lines stacked by their heights
// and the margins of paragraphs and headings, within the foreignObject's
// box. Lines are not wrapped where they do not fit.

// htmlBlocks are the elements of HTML laid out as blocks.
var htmlBlocks = map[string]bool{
	"div": true, "p": true, "h1": true, "h2": true, "h3": true, "h4": true, "h5": true, "h6": true,
	"ul": true, "ol": true, "li": true, "section": true, "article": true, "header": true, "footer": true,
	"blockquote": true, "pre": true, "table": true, "tr": true, "center": true, "dl": true, "dt": true,
	"dd": true, "figure": true, "nav": true, "main": true, "aside": true, "address": true, "form": true,
	"body": true, "html": true, "hr": true, "figcaption": true, "caption": true,
}

// htmlHeading is the size (in em) and margins (in em) browsers give h1 to h6.
var htmlHeading = map[string][2]float64{
	"h1": {2, 0.67}, "h2": {1.5, 0.83}, "h3": {1.17, 1}, "h4": {1, 1.33}, "h5": {0.83, 1.67}, "h6": {0.67, 2.33},
}

type hrun struct {
	text  string
	st    style
	lh    string // its line-height, as specified
	deco  string
	bg    color.NRGBA // the background of the inline element it is in (alpha 0: none)
	index int         // the run of the unwrapped line it comes from
}

// hbox is the background of a block: from its top to its bottom, across
// the box.
type hbox struct {
	top, bottom float64
	c           color.NRGBA
}

type hline struct {
	runs  []hrun
	align string
	top   float64
	st    style // the block's, for the line's height when it has no text
	lh    string
}

// foreignObject draws the text of the HTML a foreignObject holds.
func (b *builder) foreignObject(n *node, m matrix, st style, vp viewport, own, outer *clip) {
	x, _ := parseLength(n.attrs["x"], st.fontSize, vp, axisX)
	y, _ := parseLength(n.attrs["y"], st.fontSize, vp, axisY)
	w, okW := parseLength(attrOr(n, "width", "0"), st.fontSize, vp, axisX)
	h, okH := parseLength(attrOr(n, "height", "0"), st.fontSize, vp, axisY)
	if !okW || !okH || !(w > 0) || !(h > 0) {
		return // nothing shows through a box of no size
	}
	lines, svgs, boxes := b.htmlLines(n, st, vp, viewport{w, h})
	if len(lines) == 0 && len(svgs) == 0 && len(boxes) == 0 {
		return
	}
	// The lines as a text element of tspans, one placed at each line's
	// start.
	text := &node{name: "text", attrs: map[string]string{"xml:space": "preserve"}, parent: n}
	for _, ln := range lines {
		_, baseline := b.lineBox(ln)
		bx, anchor := x, "start"
		switch ln.align {
		case "center", "-webkit-center":
			bx, anchor = x+float64(w/2), "middle"
		case "right", "end", "-webkit-right":
			bx, anchor = x+w, "end"
		}
		for i, r := range ln.runs {
			t := &node{name: "tspan", attrs: runAttrs(r), parent: text}
			if i == 0 {
				t.attrs["x"] = fmtNum(bx)
				t.attrs["y"] = fmtNum(y + ln.top + baseline)
			}
			t.attrs["text-anchor"] = anchor
			t.children = []*node{{name: "#text", text: r.text, parent: t, attrs: map[string]string{}}}
			text.children = append(text.children, t)
		}
	}
	tst := st
	tst.fill, tst.stroke = paint{kind: paintColor, c: st.color}, paint{kind: paintNone}
	tst.baseline = baseAlphabetic // SVG's dominant-baseline is not HTML's
	// What overflows the box is clipped (unless overflow says otherwise).
	rect := own
	if ov, _ := n.prop("overflow"); ov != "visible" && ov != "auto" {
		p := fillPath(flatten(rectSegs(x, y, w, h, 0, 0), m), m, false)
		rect = chainClip(&clip{paths: []*raster.Path{p}, bounds: pathBounds(p, b.full)}, own)
	}
	// The backgrounds of inline elements cover their text's cells: the
	// text measured where it is laid out.
	cells := map[*node][4]float64{}
	runBg := map[*node]color.NRGBA{} // the tspans of runs with a background
	i := 0
	for _, ln := range lines {
		for _, r := range ln.runs {
			if r.bg.A > 0 {
				runBg[text.children[i]] = r.bg
			}
			i++
		}
	}
	if len(runBg) > 0 {
		outerMeasure := b.measureText
		b.measureText = func(k *node, cell [4][2]float64) {
			if _, ok := runBg[k]; !ok {
				return
			}
			bx, ok := cells[k]
			if !ok {
				bx = [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
			}
			for _, p := range cell {
				bx = [4]float64{math.Min(bx[0], p[0]), math.Min(bx[1], p[1]), math.Max(bx[2], p[0]), math.Max(bx[3], p[1])}
			}
			cells[k] = bx
		}
		b.text(text, identity, tst, vp, nil, nil)
		b.measureText = outerMeasure
	}
	fillRect := func(x0, y0, x1, y1 float64, col color.NRGBA, c *clip) {
		if !(x1 > x0) || !(y1 > y0) {
			return
		}
		bst := st
		bst.fill, bst.fillOpacity = paint{kind: paintColor, c: col}, 1
		bst.stroke, bst.opacity, bst.visible = paint{kind: paintNone}, 1, true
		b.shape(n, rectSegs(x0, y0, x1-x0, y1-y0, 0, 0), m, bst, vp, nil, c)
	}
	b.group(n, st, rect, outer, func(c *clip) {
		for _, bx := range boxes {
			fillRect(x, y+bx.top, x+w, y+bx.bottom, bx.c, c)
		}
		for _, t := range text.children {
			if col, ok := runBg[t]; ok {
				if bx, ok := cells[t]; ok {
					fillRect(bx[0], bx[1], bx[2], bx[3], col, c)
				}
			}
		}
		for _, sv := range svgs {
			// Its place in the box; its own x and y are not used there.
			sm := m.mul(matrix{1, 0, 0, 1, x, y + sv.top})
			sst := computeStyle(sv.node, st, vp)
			b.viewportInto(sv.node, sm, sst, 0, 0, sv.w, sv.h, nil, c, func(mm matrix, inner viewport, cc *clip) {
				b.children(sv.node, mm, sst, inner, cc)
			})
		}
		if len(text.children) > 0 {
			b.text(text, m, tst, vp, nil, c)
		}
	})
}

// hsvg is an SVG document in HTML: where its block is, and its size.
type hsvg struct {
	node      *node
	top, w, h float64
}

// htmlHidden are the elements of HTML browsers do not show.
var htmlHidden = map[string]bool{
	"style": true, "script": true, "head": true, "title": true, "meta": true, "link": true,
	"template": true, "noscript": true, "base": true,
}

// htmlLines lays out the blocks and lines of the HTML under n, in a box\n// of a width (its height not bounding them), and the SVG documents in it.
func (b *builder) htmlLines(n *node, st style, vp, box viewport) ([]hline, []hsvg, []hbox) {
	var lines []hline
	var svgs []hsvg
	var boxes []hbox
	var cur *hline
	y, margin := 0.0, 0.0 // the bottom of the lines so far; the margin pending below them
	closeLine := func(force bool) {
		if cur == nil {
			return
		}
		// A line's spaces at its end go (as those at its start never came).
		for len(cur.runs) > 0 {
			last := &cur.runs[len(cur.runs)-1]
			if !strings.HasSuffix(last.text, " ") || last.st.whitePre {
				break
			}
			last.text = strings.TrimRight(last.text, " ")
			if last.text == "" {
				cur.runs = cur.runs[:len(cur.runs)-1]
				continue
			}
			break
		}
		if len(cur.runs) > 0 || force {
			for _, ln := range b.wrapLine(*cur, box.w) {
				y += margin
				margin = 0
				ln.top = y
				height, _ := b.lineBox(ln)
				y += height
				lines = append(lines, ln)
			}
		}
		cur = nil
	}
	open := func(kst style, align, lh string) {
		if cur == nil {
			cur = &hline{align: align, st: kst, lh: lh}
		}
	}
	var walk func(k *node, kst style, align, lh, deco string, bg color.NRGBA, depth int)
	walk = func(k *node, kst style, align, lh, deco string, bg color.NRGBA, depth int) {
		if depth > 64 {
			return
		}
		for _, c := range k.children {
			if c.name == "#text" {
				t := c.text
				if kst.whitePre && !kst.noWrap && strings.TrimSpace(t) == "" && strings.ContainsAny(t, "\n\r") {
					continue // the indentation of the markup, not shown (as Chrome)
				}
				if !kst.whitePre {
					t = collapseSpace(t)
				}
				if t == "" {
					continue
				}
				// Line feeds that are kept break lines.
				parts := []string{t}
				if kst.whitePre {
					parts = strings.Split(strings.ReplaceAll(t, "\r\n", "\n"), "\n")
				}
				for i, p := range parts {
					if i > 0 {
						open(kst, align, lh)
						closeLine(true)
					}
					if p == "" {
						continue
					}
					if cur == nil || len(cur.runs) == 0 {
						if !kst.whitePre {
							p = strings.TrimLeft(p, " ")
						}
						if p == "" {
							continue
						}
					} else if !kst.whitePre && strings.HasSuffix(cur.runs[len(cur.runs)-1].text, " ") {
						p = strings.TrimLeft(p, " ")
					}
					open(kst, align, lh)
					cur.runs = append(cur.runs, hrun{text: p, st: kst, lh: lh, deco: deco, bg: bg})
				}
				continue
			}
			name := strings.ToLower(c.name)
			if htmlHidden[name] {
				continue
			}
			cst := computeStyle(c, kst, vp)
			if !cst.display {
				continue
			}
			if name == "svg" {
				// An SVG document in the HTML: a block of its own size (its
				// width that of the box by default, its height by its
				// viewBox's shape), drawn as SVG.
				closeLine(false)
				y += margin
				margin = 0
				fw, fh := box.w, 150.0
				if v, ok := c.attrs["width"]; ok {
					if l, ok := parseLength(v, cst.fontSize, box, axisX); ok && l > 0 {
						fw = l
					}
				}
				// A percentage of the height of blocks whose height is their
				// content's is none (as CSS has it): the viewBox's shape.
				if v, ok := c.attrs["height"]; ok && !strings.HasSuffix(strings.TrimSpace(v), "%") {
					if l, ok := parseLength(v, cst.fontSize, box, axisY); ok && l > 0 {
						fh = l
					}
				} else if vb, ok := viewBox(c); ok && vb[2] > 0 {
					fh = float64(fw * vb[3] / vb[2])
				}
				svgs = append(svgs, hsvg{node: c, top: y, w: fw, h: fh})
				y += fh
				continue
			}
			cst = htmlDefaults(c, name, cst, kst)
			calign, clh, cdeco := align, lh, deco
			if v, ok := c.prop("text-align"); ok && strings.TrimSpace(v) != "inherit" {
				calign = strings.ToLower(strings.TrimSpace(v))
			}
			if v, ok := c.prop("line-height"); ok && strings.TrimSpace(v) != "inherit" {
				clh = lineHeightPx(v, cst.fontSize, vp)
			}
			if v, ok := c.prop("text-decoration"); ok {
				cdeco = strings.TrimSpace(v)
			} else if name == "u" || name == "ins" {
				cdeco = "underline"
			} else if name == "s" || name == "strike" || name == "del" {
				cdeco = "line-through"
			}
			if name == "br" {
				open(kst, align, lh)
				closeLine(true)
				continue
			}
			block := htmlBlocks[name]
			if v, ok := c.prop("display"); ok {
				switch strings.TrimSpace(v) {
				case "inline", "contents":
					block = false
				default: // block, inline-block, table-cell, flex, list-item and the like
					block = true
				}
			}
			own, hasBg := rootBackground(c) // background-color and background are not inherited
			if !block {
				cbg := bg
				if hasBg {
					cbg = own
				}
				walk(c, cst, calign, clh, cdeco, cbg, depth+1)
				continue
			}
			mTop, mBottom := htmlMargins(c, name, cst, vp)
			closeLine(false)
			margin = math.Max(margin, mTop)
			if hasBg {
				// Its top where its content starts (its margin above it).
				y += margin
				margin = 0
			}
			top := y
			walk(c, cst, calign, clh, cdeco, color.NRGBA{}, depth+1)
			closeLine(false)
			if hasBg {
				boxes = append(boxes, hbox{top: top, bottom: y, c: own})
			}
			margin = math.Max(margin, mBottom)
		}
	}
	// text-align, line-height and white-space inherit (into the
	// foreignObject from the SVG elements it is in, too).
	inherited := func(name string) (string, bool) {
		for p := n; p != nil; p = p.parent {
			if v, ok := p.prop(name); ok && strings.TrimSpace(v) != "inherit" {
				return strings.TrimSpace(v), true
			}
		}
		return "", false
	}
	align, _ := inherited("text-align")
	align = strings.ToLower(align)
	lh := "normal"
	if v, ok := inherited("line-height"); ok {
		lh = lineHeightPx(v, st.fontSize, vp)
	}
	if v, ok := inherited("white-space"); ok {
		st.whitePre, st.noWrap = whiteSpace(v)
	}
	walk(n, st, align, lh, "", color.NRGBA{}, 0)
	closeLine(false)
	return lines, svgs, boxes
}

// lineHeightPx is a line-height as it is inherited: a percentage or a
// length in em of the size of the element it is given on, as pixels; a
// number (a factor of each element's size) or normal as they are.
func lineHeightPx(v string, fontSize float64, vp viewport) string {
	v = strings.TrimSpace(v)
	if p, ok := strings.CutSuffix(v, "%"); ok {
		return fmtNum(float64(parseFloatOr(p, 0)/100*fontSize)) + "px"
	}
	if strings.HasSuffix(v, "em") {
		if f, ok := parseLength(v, fontSize, vp, axisY); ok {
			return fmtNum(f) + "px"
		}
	}
	return v
}

// htmlDefaults gives an element of HTML what browsers give it before its
// own CSS: the weight of strong and b, the slant of em and i, the
// monospace of code, the sizes and weights of headings.
func htmlDefaults(k *node, name string, st, parent style) style {
	_, hasWeight := k.prop("font-weight")
	_, hasStyle := k.prop("font-style")
	_, hasSize := k.prop("font-size")
	_, hasFamily := k.prop("font-family")
	switch name {
	case "strong", "b", "th":
		if !hasWeight {
			st.weight, st.bold = weightValue("bolder", parent.weight), true
		}
	case "em", "i", "cite", "var", "dfn":
		if !hasStyle {
			st.italic = true
		}
	case "code", "kbd", "samp", "tt", "pre":
		if !hasFamily {
			st.fontFamily = "monospace"
		}
		if !hasSize {
			st.fontSize = float64(parent.fontSize * 13 / 16) // as browsers size their monospace
		}
	case "small":
		if !hasSize {
			st.fontSize = float64(parent.fontSize / 1.2)
		}
	case "big":
		if !hasSize {
			st.fontSize = float64(parent.fontSize * 1.2)
		}
	}
	if hd, ok := htmlHeading[name]; ok {
		if !hasSize {
			st.fontSize = float64(parent.fontSize * hd[0])
		}
		if !hasWeight {
			st.weight, st.bold = 700, true
		}
	}
	if name == "pre" {
		st.whitePre, st.noWrap = true, true
	}
	if v, ok := k.prop("white-space"); ok {
		st.whitePre, st.noWrap = whiteSpace(v)
	}
	return st
}

// whiteSpace reads white-space: whether spaces and line feeds are kept,
// and whether lines are not wrapped to fit.
func whiteSpace(v string) (pre, noWrap bool) {
	switch strings.TrimSpace(v) {
	case "pre":
		return true, true
	case "pre-wrap", "break-spaces", "pre-line":
		return true, false
	case "nowrap":
		return false, true
	}
	return false, false
}

// htmlMargins are the top and bottom margins of a block: its CSS's, else
// those browsers give paragraphs (1em), headings and lists.
func htmlMargins(k *node, name string, st style, vp viewport) (top, bottom float64) {
	switch name {
	case "p", "ul", "ol", "dl", "blockquote", "pre", "figure":
		top, bottom = st.fontSize, st.fontSize
	}
	if hd, ok := htmlHeading[name]; ok {
		top, bottom = float64(hd[1]*st.fontSize), float64(hd[1]*st.fontSize)
	}
	length := func(v string) (float64, bool) {
		v = strings.TrimSpace(v)
		if v == "auto" {
			return 0, true
		}
		f, ok := parseLength(v, st.fontSize, vp, axisY)
		return math.Max(0, f), ok
	}
	if v, ok := k.prop("margin"); ok {
		f := strings.Fields(v)
		if len(f) > 0 {
			if t, ok := length(f[0]); ok {
				top = t
				bottom = t
			}
			if len(f) >= 3 {
				if bt, ok := length(f[2]); ok {
					bottom = bt
				}
			}
		}
	}
	if v, ok := k.prop("margin-top"); ok {
		if t, ok := length(v); ok {
			top = t
		}
	}
	if v, ok := k.prop("margin-bottom"); ok {
		if t, ok := length(v); ok {
			bottom = t
		}
	}
	return top, bottom
}

// lineBox is the height of a line and its baseline down from its top: the
// tallest of its runs' (and its block's) line heights, each run's text
// set in the middle of its own (its half-leading above it).
func (b *builder) lineBox(ln hline) (height, baseline float64) {
	measure := func(st style, lh, text string) (float64, float64) {
		asc, desc, gap := b.fontLines(st, text)
		var l float64
		switch {
		case lh == "" || lh == "normal":
			l = asc + desc + gap
		case strings.HasSuffix(lh, "px"):
			l = parseFloatOr(strings.TrimSuffix(lh, "px"), 0)
		default:
			if f, err := strconv.ParseFloat(lh, 64); err == nil {
				l = float64(f * st.fontSize)
			} else if f, ok := parseLength(lh, st.fontSize, viewport{}, axisY); ok {
				l = f
			} else {
				l = asc + desc + gap
			}
		}
		return l, float64((l-(asc+desc))/2) + asc
	}
	height, baseline = measure(ln.st, ln.lh, "")
	for _, r := range ln.runs {
		l, bl := measure(r.st, r.lh, r.text)
		height, baseline = math.Max(height, l), math.Max(baseline, bl)
	}
	return height, baseline
}

// fontLines are the ascent, descent and line gap of a style's first font
// at its size, each rounded to the pixel (as Chrome lays out lines), and
// of the fonts that draw text's characters (a fallback font's taller
// lines make the line taller, as in Chrome); a guess from the size
// without fonts.
func (b *builder) fontLines(st style, text string) (asc, desc, gap float64) {
	set := b.fonts(st.fontFamily, faceOf(st.weight, st.italic))
	if set == nil || set.First() == nil {
		return math.Round(float64(0.9 * st.fontSize)), math.Round(float64(0.25 * st.fontSize)), 0
	}
	var buf sfnt.Buffer
	seen := map[*sfnt.Font]bool{}
	take := func(sf *sfnt.Font) {
		if seen[sf] {
			return
		}
		seen[sf] = true
		upem := fixed.Int26_6(sf.UnitsPerEm()) << 6
		mt, err := sf.Metrics(&buf, upem, font.HintingNone)
		if err != nil {
			return
		}
		u := float64(st.fontSize / float64(sf.UnitsPerEm()) / 64)
		asc = math.Max(asc, math.Round(float64(float64(mt.Ascent)*u)))
		desc = math.Max(desc, math.Round(float64(float64(mt.Descent)*u)))
		gap = math.Max(gap, math.Round(float64(float64(mt.Height-mt.Ascent-mt.Descent)*u)))
	}
	take(set.First().SFNT())
	rs := []rune(text)
	for i, r := range rs {
		if r == ' ' || r < 0x20 {
			continue
		}
		if f, err := set.PickFor(rs, i); err == nil && f != nil {
			take(f.SFNT())
		}
	}
	if asc == 0 && desc == 0 {
		return math.Round(float64(0.9 * st.fontSize)), math.Round(float64(0.25 * st.fontSize)), 0
	}
	return asc, desc, gap
}

// collapseSpace makes each run of white space one space, as HTML shows it.
func collapseSpace(s string) string {
	var sb strings.Builder
	space := false
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == '\f' {
			space = true
			continue
		}
		if space {
			sb.WriteByte(' ')
			space = false
		}
		sb.WriteRune(r)
	}
	if space {
		sb.WriteByte(' ')
	}
	return sb.String()
}

// runAttrs are the attributes of the tspan of a run: its font and colour.
func runAttrs(r hrun) map[string]string {
	st := r.st
	a := map[string]string{
		"font-family": st.fontFamily,
		"font-size":   fmtNum(st.fontSize),
		"font-weight": strconv.Itoa(max(1, st.weight)),
		"fill":        fmt.Sprintf("rgb(%d,%d,%d)", st.color.R, st.color.G, st.color.B),
		"stroke":      "none",
	}
	if st.weight == 0 {
		a["font-weight"] = "400"
	}
	// SVG's own properties do not reach HTML: its opacity of fill is its
	// colour's alone.
	a["fill-opacity"] = fmtNum(float64(st.color.A) / 255)
	if !st.visible {
		a["visibility"] = "hidden" // its place kept, nothing drawn
	}
	if st.italic {
		a["font-style"] = "italic"
	} else {
		a["font-style"] = "normal"
	}
	if r.deco != "" && r.deco != "none" {
		a["text-decoration"] = r.deco
	}
	return a
}

func fmtNum(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

// wrapLine breaks a line wider than width at its spaces, as many words on
// each line as fit (a word wider than the width alone on its line), where
// its white-space lets lines wrap.
func (b *builder) wrapLine(ln hline, width float64) []hline {
	if !(width > 0) || len(ln.runs) == 0 {
		return []hline{ln}
	}
	wraps := false
	total := 0.0
	for _, r := range ln.runs {
		wraps = wraps || !r.st.noWrap
		total += b.textWidth(r, r.text)
	}
	if !wraps || total <= width+0.5 {
		return []hline{ln}
	}
	// The pieces between the places a line may break, each of the run it
	// is in; a run's start is such a place only if its text and the one
	// before allow a break there.
	type part struct {
		run  int
		text string
	}
	var units [][]part
	var prev rune
	for ri, r := range ln.runs {
		for i, word := range splitWords(r.text) {
			first, _ := utf8.DecodeRuneInString(word)
			if len(units) == 0 || i > 0 || !r.st.noWrap && breakBetween(prev, first) {
				units = append(units, nil)
			}
			units[len(units)-1] = append(units[len(units)-1], part{ri, word})
			prev, _ = utf8.DecodeLastRuneInString(word)
		}
	}
	var out []hline
	cur := hline{align: ln.align, st: ln.st, lh: ln.lh}
	w := 0.0
	for _, u := range units {
		uw := 0.0 // its width, the spaces at its end left out
		for j, p := range u {
			t := p.text
			if j == len(u)-1 {
				t = strings.TrimRight(t, " ")
			}
			uw += b.textWidth(ln.runs[p.run], t)
		}
		if w+uw > width+0.5 && len(cur.runs) > 0 && !ln.runs[u[0].run].st.noWrap {
			// A new line: the spaces at the old one's end and the new one's
			// start go.
			last := &cur.runs[len(cur.runs)-1]
			last.text = strings.TrimRight(last.text, " ")
			out = append(out, cur)
			cur, w = hline{align: ln.align, st: ln.st, lh: ln.lh}, 0
			u[0].text = strings.TrimLeft(u[0].text, " ")
		}
		for _, p := range u {
			if p.text == "" {
				continue
			}
			if n := len(cur.runs); n > 0 && runIndex(cur, p.run) {
				cur.runs[n-1].text += p.text
			} else {
				nr := ln.runs[p.run]
				nr.text, nr.index = p.text, p.run
				cur.runs = append(cur.runs, nr)
			}
			w += b.textWidth(ln.runs[p.run], p.text)
		}
	}
	if len(cur.runs) > 0 {
		out = append(out, cur)
	}
	return out
}

func runIndex(l hline, ri int) bool { return l.runs[len(l.runs)-1].index == ri }

// splitWords splits text where lines may break (breakBetween): "a b-c  d"
// is "a ", "b-", "c  ", "d"; Chinese, Japanese and Korean, between their
// characters.
func splitWords(s string) []string {
	var out []string
	start := 0
	var prev, prev2 rune
	for i, r := range s {
		if i > start && breakBetween(prev, r) && !(prev == '-' && !isLetter(prev2)) {
			out = append(out, s[start:i])
			start = i
		}
		prev2, prev = prev, r
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

// breakBetween reports whether a line may break between two characters,
// as UAX #14 has it for the text of labels: after spaces, after a hyphen
// before a letter, and before or after a character of Chinese, Japanese
// or Korean (not before closing punctuation, nor after opening).
func breakBetween(a, b rune) bool {
	switch {
	case a == 0 || b == 0 || b == ' ':
		return false
	case a == ' ':
		return true
	case a == '-':
		return isLetter(b)
	case closingPunct(b) || openingPunct(a):
		return false
	}
	return wideScript(a) || wideScript(b)
}

func isLetter(r rune) bool { return r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' }

// wideScript reports whether r is of the scripts written without spaces
// between words that lines break between any two characters of.
func wideScript(r rune) bool {
	return r >= 0x2E80 && r <= 0x9FFF || r >= 0xAC00 && r <= 0xD7AF || r >= 0xF900 && r <= 0xFAFF ||
		r >= 0xFF00 && r <= 0xFF60 || r >= 0x20000 && r <= 0x3FFFF
}

func closingPunct(r rune) bool {
	return strings.ContainsRune("，。、！？：；）」』》〉】〕｝”’…‥・ー々ゝゞヽヾぁぃぅぇぉっゃゅょゎゕゖァィゥェォッャュョヮヵヶ,.!?:;)]}％", r)
}

func openingPunct(r rune) bool { return strings.ContainsRune("（「『《〈【〔｛“‘([{", r) }

// textWidth is the advance of a run's text as it is laid out.
func (b *builder) textWidth(r hrun, s string) float64 {
	if s == "" {
		return 0
	}
	t := &node{name: "text", attrs: map[string]string{"xml:space": "preserve"}}
	ts := &node{name: "tspan", attrs: runAttrs(r), parent: t}
	ts.children = []*node{{name: "#text", text: s, parent: ts, attrs: map[string]string{}}}
	t.children = []*node{ts}
	minX, maxX := math.Inf(1), math.Inf(-1)
	outer := b.measureText
	b.measureText = func(_ *node, cell [4][2]float64) {
		for _, p := range cell {
			minX, maxX = math.Min(minX, p[0]), math.Max(maxX, p[0])
		}
	}
	b.text(t, identity, r.st, viewport{}, nil, nil)
	b.measureText = outer
	if maxX < minX {
		return 0
	}
	return maxX - minX
}
