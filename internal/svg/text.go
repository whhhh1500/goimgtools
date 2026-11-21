package svg

import (
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/whhhh1500/goimgtools/internal/textsrc"
	"github.com/whhhh1500/goimgtools/internal/trig"
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
	rot        float64 // rotate, degrees
	rotSet     bool
	anchor     int // start, middle, end
	whitespace bool
	tctx       *textCtx
}

// textCtx is what a text element or tspan passes on to its characters and
// those of the tspans inside it.
type textCtx struct {
	letter, word float64 // letter-spacing, word-spacing
	shift        float64 // the baseline-shift of it and those around it, up
	baseline     int     // dominant-baseline
	decos        []*decoration
	path         *textPathInfo
}

// A decoration (text-decoration) of an element: the lines it draws under,
// over or through the characters in it, in its paint.
type decoration struct {
	node                 *node
	st                   style
	under, over, through bool
}

// textPathInfo is the path of a textPath, flattened, and where its text
// starts along it.
type textPathInfo struct {
	xy     []float64 // the points, in the text's user space
	cum    []float64 // the length up to each
	start  float64
	anchor int
}

// The baselines dominant-baseline can put on the text's y.
const (
	baseAlphabetic = iota
	baseMiddle
	baseCentral
	baseHanging
	baseMathematical
	baseTop
	baseBottom
)

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
	try := func(f string) bool {
		for _, name := range []string{f + face, f} {
			if fo, err := textsrc.Open(name); err == nil {
				fo.Close()
				specs = append(specs, name)
				return true
			}
		}
		return false
	}
	// The families named, the generic ones as the fonts browsers use for
	// them; after them all, as CSS says, the default, serif.
	generic := false
	for _, f := range strings.Split(family, ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		if names, ok := genericFonts[strings.ToLower(f)]; ok {
			for _, n := range names {
				if try(n) {
					generic = true
					break
				}
			}
			if generic {
				break // the list ends at a generic family
			}
			continue
		}
		if f != "" {
			try(f)
		}
	}
	if !generic {
		for _, n := range genericFonts["serif"] {
			if try(n) {
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

// genericFonts are the fonts the generic families of CSS stand for, as
// browsers choose them, the first installed of each list.
var genericFonts = map[string][]string{
	"serif":      {"Times New Roman", "Times", "Liberation Serif", "DejaVu Serif", "Noto Serif", "Tinos"},
	"sans-serif": {"Arial", "Helvetica", "Liberation Sans", "DejaVu Sans", "Noto Sans", "Arimo"},
	"system-ui":  {"Segoe UI", "San Francisco", "Helvetica Neue", "Cantarell", "Ubuntu", "DejaVu Sans", "Arial"},
	"monospace":  {"Consolas", "Courier New", "Menlo", "Courier", "Liberation Mono", "DejaVu Sans Mono", "Noto Sans Mono", "Cousine"},
	"cursive":    {"Comic Sans MS", "Apple Chancery", "URW Chancery L", "Comic Neue"},
	"fantasy":    {"Impact", "Papyrus", "Luminari"},
}

// spacing reads letter-spacing or word-spacing: normal is none.
func spacing(v string, st style, vp viewport) (float64, bool) {
	v = strings.TrimSpace(v)
	if v == "normal" {
		return 0, true
	}
	return parseLength(v, st.fontSize, vp, axisX)
}

// baselineOf reads dominant-baseline or alignment-baseline.
func baselineOf(v string) (int, bool) {
	switch strings.TrimSpace(v) {
	case "auto", "alphabetic", "baseline", "use-script", "no-change", "reset-size":
		return baseAlphabetic, true
	case "middle":
		return baseMiddle, true
	case "central":
		return baseCentral, true
	case "hanging":
		return baseHanging, true
	case "mathematical":
		return baseMathematical, true
	case "text-before-edge", "text-top", "before-edge":
		return baseTop, true
	case "text-after-edge", "text-bottom", "after-edge", "ideographic":
		return baseBottom, true
	}
	return 0, false
}

// pathFor flattens the path a textPath refers to, in the text's user
// space.
func (b *builder) pathFor(k *node, st style, vp viewport) *textPathInfo {
	href, ok := k.attr("href")
	if !ok {
		href, ok = k.attr("xlink:href")
	}
	href = strings.TrimSpace(href)
	if !ok || !strings.HasPrefix(href, "#") {
		return nil
	}
	t := b.ids[href[1:]]
	if t == nil {
		return nil
	}
	switch t.name {
	case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon":
	default:
		return nil
	}
	tm := identity
	if v, ok := t.attr("transform"); ok {
		if m, ok := parseTransform(v); ok {
			tm = m
		}
	}
	segs := shapeSegs(t, computeStyle(t, st, vp), vp)
	ps, ok := flattenLimit(segs, tm, maxPoints/16)
	if !ok || len(ps) == 0 {
		return nil
	}
	// The first subpath: text runs along one.
	q := ps[0]
	info := &textPathInfo{}
	for i := 0; i+1 < len(q.xy); i += 2 {
		x, y := tm.apply(q.xy[i], q.xy[i+1])
		l := 0.0
		if n := len(info.xy); n > 0 {
			l = info.cum[len(info.cum)-1] + hypot(x-info.xy[n-2], y-info.xy[n-1])
		}
		info.xy = append(info.xy, x, y)
		info.cum = append(info.cum, l)
	}
	if q.closed && len(info.xy) >= 4 {
		x, y := info.xy[0], info.xy[1]
		info.cum = append(info.cum, info.cum[len(info.cum)-1]+hypot(x-info.xy[len(info.xy)-2], y-info.xy[len(info.xy)-1]))
		info.xy = append(info.xy, x, y)
	}
	if len(info.cum) < 2 || !(info.cum[len(info.cum)-1] > 0) {
		return nil
	}
	total := info.cum[len(info.cum)-1]
	if v := strings.TrimSpace(k.attrs["startOffset"]); v != "" {
		if strings.HasSuffix(v, "%") {
			if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
				info.start = float64(f / 100 * total)
			}
		} else if f, ok := parseLength(v, st.fontSize, vp, axisX); ok {
			info.start = f
		}
	}
	return info
}

// at is the point of the path at distance s along it and its direction;
// false beyond its ends.
func (p *textPathInfo) at(s float64) (x, y, ux, uy float64, ok bool) {
	n := len(p.cum)
	if s < 0 || s > p.cum[n-1] {
		return 0, 0, 0, 0, false
	}
	lo, hi := 0, n-1
	for hi-lo > 1 {
		mid := (lo + hi) / 2
		if p.cum[mid] <= s {
			lo = mid
		} else {
			hi = mid
		}
	}
	// A segment of some length around s.
	for hi < n-1 && p.cum[hi] == p.cum[lo] {
		hi++
	}
	x0, y0, x1, y1 := p.xy[2*lo], p.xy[2*lo+1], p.xy[2*hi], p.xy[2*hi+1]
	l := p.cum[hi] - p.cum[lo]
	if !(l > 0) {
		return x0, y0, 1, 0, true
	}
	t := (s - p.cum[lo]) / l
	return x0 + float64(t*(x1-x0)), y0 + float64(t*(y1-y0)), (x1 - x0) / l, (y1 - y0) / l, true
}

// text draws a text element: its characters laid out along a line (or a
// path) from the positions given, each glyph's outline filled and stroked
// as its style says, with the lines of its decorations.
func (b *builder) text(n *node, m matrix, st style, vp viewport, own, outer *clip) {
	var chars []tchar
	var walk func(k *node, kst style, ctx textCtx)
	walk = func(k *node, kst style, ctx textCtx) {
		// The inherited properties of text come with the style (from the
		// element's ancestors too); these do not inherit.
		family, anchor := kst.fontFamily, kst.anchor
		face := ""
		if kst.bold {
			face += " Bold"
		}
		if kst.italic {
			face += " Italic"
		}
		ctx.letter, ctx.word, ctx.baseline = kst.letterSp, kst.wordSp, kst.baseline
		if v, ok := k.prop("alignment-baseline"); ok && k != n {
			if bl, ok := baselineOf(v); ok {
				ctx.baseline = bl
			}
		}
		if v, ok := k.prop("baseline-shift"); ok && k != n {
			switch v = strings.TrimSpace(v); v {
			case "baseline":
			case "sub":
				ctx.shift -= float64(0.2 * kst.fontSize)
			case "super":
				ctx.shift += float64(0.4 * kst.fontSize)
			default:
				if strings.HasSuffix(v, "%") {
					if f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64); err == nil {
						ctx.shift += float64(f / 100 * kst.fontSize)
					}
				} else if f, ok := parseLength(v, kst.fontSize, vp, axisY); ok {
					ctx.shift += f
				}
			}
		}
		if v, ok := k.prop("text-decoration"); ok {
			d := &decoration{node: k, st: kst}
			for _, w := range strings.Fields(v) {
				switch w {
				case "underline":
					d.under = true
				case "overline":
					d.over = true
				case "line-through":
					d.through = true
				}
			}
			if d.under || d.over || d.through {
				d.st.opacity = 1
				ctx.decos = append(ctx.decos[:len(ctx.decos):len(ctx.decos)], d)
			}
		}
		if k.name == "textPath" {
			ctx.path = b.pathFor(k, kst, vp)
			if ctx.path == nil {
				return // a path that is not there: its text is not drawn
			}
			ctx.path.anchor = anchor
		}
		xs := lengths(k.attrs["x"], kst, vp, axisX)
		ys := lengths(k.attrs["y"], kst, vp, axisY)
		dxs := lengths(k.attrs["dx"], kst, vp, axisX)
		dys := lengths(k.attrs["dy"], kst, vp, axisY)
		rots, _ := numbers(k.attrs["rotate"])
		first := len(chars)
		kctx := ctx
		addText := func(s string) {
			for _, r := range s {
				chars = append(chars, tchar{r: r, st: kst, node: k, family: family, face: face, anchor: anchor, tctx: &kctx})
			}
		}
		// The character data and the tspans, in order.
		for _, c := range k.children {
			if c.name == "#text" {
				addText(c.text)
				continue
			}
			if c.name == "tspan" || c.name == "a" || c.name == "textPath" && ctx.path == nil {
				cst := computeStyle(c, kst, vp)
				if cst.display {
					walk(c, cst, ctx)
				}
			}
		}
		// The element's positions go to its characters in order, but for
		// those an inner tspan places itself; its last rotation goes on to
		// the rest.
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
			if len(rots) > 0 && !chars[i].rotSet {
				chars[i].rot, chars[i].rotSet = rots[min(j, len(rots)-1)], true
			}
		}
	}
	st.opacity = 1 // the group's, see element
	walk(n, st, textCtx{})
	chars = collapseSpace(chars, n.attrs["xml:space"] == "preserve")
	if len(chars) == 0 {
		return
	}
	// Lay out: chunks start where a character is placed absolutely, and
	// where a path starts.
	type placed struct {
		c       *tchar
		f       *textsrc.Font
		gi      sfnt.GlyphIndex
		x, y    float64 // its pen (along the path, from its start, on one)
		adv     float64 // its advance, spacing included
		chunk   int
		sx      float64 // how much it is stretched across
		base    float64 // how far down its baseline is put to meet y
		end     float64 // its chunk's end so far
		under   float64 // the decorations' place and thickness, down from the baseline
		thick   float64
		over    float64
		through float64
	}
	var out []placed
	var buf sfnt.Buffer
	penX, penY := 0.0, 0.0
	chunk := 0
	var chunkStart []float64
	var chunkPath []*textPathInfo
	var prev sfnt.GlyphIndex
	var prevFont *textsrc.Font
	for i := range chars {
		c := &chars[i]
		path := c.tctx.path
		newPath := path != nil && (i == 0 || chars[i-1].tctx.path != path)
		if c.x != nil || c.y != nil || i == 0 || newPath || path == nil && i > 0 && chars[i-1].tctx.path != nil {
			if i > 0 {
				chunk++
			}
			if newPath {
				penX, penY = 0, 0
			}
			if c.x != nil && path == nil {
				penX = *c.x
			}
			if c.y != nil && path == nil {
				penY = *c.y
			}
			chunkStart = append(chunkStart, penX)
			chunkPath = append(chunkPath, path)
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
		p := placed{c: c, f: f, gi: gi, x: penX, y: penY, chunk: chunk, sx: 1}
		// The font's lines, in its units of 1/64.
		u := float64(scale / 64)
		if mt, err := sf.Metrics(&buf, upem, font.HintingNone); err == nil {
			asc, desc, xh := float64(mt.Ascent), float64(mt.Descent), float64(mt.XHeight)
			if !(xh > 0) {
				xh = float64(asc / 2)
			}
			switch c.tctx.baseline {
			case baseMiddle:
				p.base = float64(xh*u) / 2
			case baseCentral:
				p.base = float64((asc-desc)*u) / 2
			case baseHanging:
				p.base = float64(0.8 * float64(asc*u))
			case baseMathematical:
				p.base = float64(asc*u) / 2
			case baseTop:
				p.base = float64(asc * u)
			case baseBottom:
				p.base = -float64(desc * u)
			}
			p.over = -float64(asc * u)
			p.through = -float64(xh*u) / 2 // the top of the line, where fonts put their strikeout about
		}
		p.thick = float64(c.st.fontSize / 14)
		p.under = float64(c.st.fontSize / 10)
		if post := sf.PostTable(); post != nil && post.UnderlineThickness > 0 {
			p.thick = float64(float64(post.UnderlineThickness) * scale)
			p.under = -float64(float64(post.UnderlinePosition) * scale)
		}
		adv := 0.0
		if a, err := sf.GlyphAdvance(&buf, gi, upem, font.HintingNone); err == nil {
			adv = float64(float64(float64(a)/64) * scale)
		}
		adv += c.tctx.letter
		if c.r == ' ' {
			adv += c.tctx.word
		}
		p.adv = adv
		penX += adv
		p.end = penX
		out = append(out, p)
		prev, prevFont = gi, f
	}
	// textLength: the advances of the text element made to add up to it,
	// by spacing or by stretching the glyphs too.
	if v, ok := n.attrs["textLength"]; ok && len(out) > 0 && out[0].chunk == out[len(out)-1].chunk {
		if l, ok := parseLength(v, st.fontSize, vp, axisX); ok && l > 0 {
			w := out[len(out)-1].end - chunkStart[out[0].chunk]
			if w > 0 {
				stretch := strings.TrimSpace(n.attrs["lengthAdjust"]) == "spacingAndGlyphs"
				k := l / w
				delta := (l - w) / float64(len(out))
				start := chunkStart[out[0].chunk]
				for i := range out {
					p := &out[i]
					if stretch {
						p.x = start + float64((p.x-start)*k)
						p.adv = float64(p.adv * k)
						p.sx = k
					} else {
						p.x += float64(float64(i) * delta)
						p.adv += delta
					}
					p.end = p.x + p.adv
				}
			}
		}
	}
	// Anchor each chunk: its width is from its start to its last pen.
	ends := map[int]float64{}
	anchors := map[int]int{}
	for _, p := range out {
		ends[p.chunk] = p.end
		if _, ok := anchors[p.chunk]; !ok {
			anchors[p.chunk] = p.c.anchor
		}
	}
	// Each glyph's frame: where its origin goes and its axes, across and
	// down.
	type frame struct{ ox, oy, ux, uy, vx, vy float64 }
	frames := make([]frame, len(out))
	drawn := make([]bool, len(out))
	for i, p := range out {
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
		down := p.base - p.c.tctx.shift // the baseline, down from the pen
		ux, uy := p.sx, 0.0
		vx, vy := 0.0, 1.0
		if p.c.rotSet && p.c.rot != 0 {
			s, c := trig.SinCosDeg(p.c.rot)
			ux, uy, vx, vy = float64(p.sx*c), float64(p.sx*s), -s, c
		}
		if path := chunkPath[p.chunk]; path != nil {
			// The glyph's middle on the path, turned with it; beyond its
			// ends it is not drawn.
			mid := path.start + p.x + shift + float64(p.adv/2) // a product by 1/2: not to be fused
			x, y, tx, ty, ok := path.at(mid)
			if !ok {
				continue
			}
			nx, ny := -ty, tx
			// Its own rotation turns it within the path's frame.
			fux, fuy := float64(ux*tx)+float64(uy*nx), float64(ux*ty)+float64(uy*ny)
			fvx, fvy := float64(vx*tx)+float64(vy*nx), float64(vx*ty)+float64(vy*ny)
			half := p.adv / 2
			frames[i] = frame{
				ox: x - float64(tx*half) + float64(nx*float64(down+p.y)),
				oy: y - float64(ty*half) + float64(ny*float64(down+p.y)),
				ux: fux, uy: fuy, vx: fvx, vy: fvy,
			}
		} else {
			frames[i] = frame{ox: p.x + shift, oy: p.y + down, ux: ux, uy: uy, vx: vx, vy: vy}
		}
		drawn[i] = true
	}
	apply := func(fr frame, gx, gy float64) [2]float64 {
		return [2]float64{fr.ox + float64(fr.ux*gx) + float64(fr.vx*gy), fr.oy + float64(fr.uy*gx) + float64(fr.vy*gy)}
	}
	// Decorations: a band along each glyph under one, those of a chunk
	// along a straight line joined into one.
	decoLines := func(through bool) {
		type key struct {
			d    *decoration
			kind int
		}
		type band struct {
			x0, x1, y, thick float64
			chunk            int
			open             bool
		}
		segsOf := map[*decoration][]seg{}
		var order []*decoration
		bands := map[key]*band{}
		var keys []key
		quad := func(d *decoration, q [4][2]float64) {
			if _, ok := segsOf[d]; !ok {
				order = append(order, d)
			}
			segsOf[d] = append(segsOf[d], seg{cmd: cmdMove, pts: [3][2]float64{q[0]}}, seg{cmd: cmdLine, pts: [3][2]float64{q[1]}},
				seg{cmd: cmdLine, pts: [3][2]float64{q[2]}}, seg{cmd: cmdLine, pts: [3][2]float64{q[3]}}, seg{cmd: cmdClose})
		}
		flushBand := func(k key, bd *band) {
			if bd.open {
				quad(k.d, [4][2]float64{{bd.x0, bd.y}, {bd.x1, bd.y}, {bd.x1, bd.y + bd.thick}, {bd.x0, bd.y + bd.thick}})
				bd.open = false
			}
		}
		for i, p := range out {
			if !drawn[i] {
				continue
			}
			fr := frames[i]
			straight := chunkPath[p.chunk] == nil && !(p.c.rotSet && p.c.rot != 0)
			for _, d := range p.c.tctx.decos {
				for kind, on := range [3]bool{d.under && !through, d.over && !through, d.through && through} {
					if !on {
						continue
					}
					top := [3]float64{p.under, p.over, p.through}[kind]
					if !straight {
						w := p.adv / p.sx
						quad(d, [4][2]float64{apply(fr, 0, top), apply(fr, w, top), apply(fr, w, top+p.thick), apply(fr, 0, top+p.thick)})
						continue
					}
					k := key{d, kind}
					y, x0, x1 := fr.oy+top, fr.ox, fr.ox+p.adv
					bd := bands[k]
					if bd == nil {
						bd = &band{}
						bands[k] = bd
						keys = append(keys, k)
					} else if bd.open && bd.chunk == p.chunk && bd.y == y && bd.thick == p.thick {
						bd.x1 = x1
						continue
					}
					flushBand(k, bd)
					*bd = band{x0: x0, x1: x1, y: y, thick: p.thick, chunk: p.chunk, open: true}
				}
			}
		}
		for _, k := range keys {
			flushBand(k, bands[k])
		}
		for _, d := range order {
			b.shape(d.node, segsOf[d], m, d.st, vp, own, outer)
		}
	}
	decoLines(false)
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
	for i, p := range out {
		if !drawn[i] {
			continue
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
		fr := frames[i]
		pt := func(q fixed.Point26_6) [2]float64 {
			return apply(fr, float64(float64(q.X)*scale), float64(float64(q.Y)*scale))
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
	decoLines(true)
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
