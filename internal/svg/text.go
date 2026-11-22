package svg

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"golang.org/x/image/font"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/whhhh1500/goimgtools/internal/bidi"
	"github.com/whhhh1500/goimgtools/internal/colorfont"
	"github.com/whhhh1500/goimgtools/internal/shape"
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
	ly         float64 // where it is across the line: y and the dy before it, added up in the order of the text
	dxSet      bool
	dySet      bool
	rot        float64 // rotate, degrees
	rotSet     bool
	anchor     int // start, middle, end
	whitespace bool
	tctx       *textCtx
	sh         *svgShape // shaped by its font's tables
}

// textCtx is what a text element or tspan passes on to its characters and
// those of the tspans inside it.
type textCtx struct {
	letter, word float64 // letter-spacing, word-spacing
	shift        float64 // the baseline-shift of it and those around it, up
	baseline     int     // dominant-baseline
	decos        []*decoration
	path         *textPathInfo
	preserve     bool // xml:space="preserve", of it or what it is in
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

// The orientations of text-orientation.
const (
	orientMixed = iota
	orientUpright
	orientSideways
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
		// The usual files of the fonts generic families stand for, without
		// a look through all the system's fonts: for their regular and bold
		// weights (the others are looked for by name).
		if paths, ok := knownFontFiles[f]; ok && (faceWeight(face) == 400 || faceWeight(face) == 700) {
			for _, p := range paths[faceIndex(face)] {
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					specs = append(specs, p)
					return true
				}
			}
		}
		// The family's faces by the weights CSS tries for the one asked
		// for, italic and then upright; else the family's regular face.
		w, italic := parseFace(face)
		for _, it := range []bool{italic, false}[:1+b2i(italic)] {
			for _, cw := range weightOrder(w) {
				for _, wn := range weightNames[cw] {
					name := f
					if wn != "" {
						name += " " + wn
					}
					if it {
						name += " Italic"
					}
					if fo, err := textsrc.Open(name); err == nil {
						fo.Close()
						specs = append(specs, name)
						return true
					}
				}
			}
		}
		if fo, err := textsrc.Open(f); err == nil {
			fo.Close()
			specs = append(specs, f)
			return true
		}
		return false
	}
	// The families named, the generic ones as the fonts browsers use for
	// them, those of other systems as what browsers put for them
	// (Helvetica: Arial); after them all, as CSS says, the default, serif.
	generic := false
	for _, f := range strings.Split(family, ",") {
		f = strings.Trim(strings.TrimSpace(f), `"'`)
		if f != "" && try(f) {
			continue
		}
		lf := strings.ToLower(f)
		if g, ok := fontAliases[lf]; ok {
			lf = g
		}
		if names, ok := genericFonts[lf]; ok {
			for _, n := range names {
				if try(n) {
					generic = true
					break
				}
			}
			if generic {
				break // the list ends at a generic family
			}
		}
	}
	if !generic {
		for _, n := range genericFonts["serif"] {
			if try(n) {
				break
			}
		}
	}
	// What the set reserves is given back when it closes; if it fails to
	// load, here.
	var got int64
	s, err := textsrc.Load(specs, nil, func(n int64) error {
		if err := b.reserve(n); err != nil {
			return err
		}
		got += n
		return nil
	})
	if err != nil {
		b.release(got)
		s = nil
	} else {
		s.DrawsPaints() // the colour glyphs of COLR version 1 are drawn here
	}
	b.fontSets[key] = s
	return s
}

// rightToLeft lays out text of right-to-left scripts: Arabic letters
// joined (lam and alef as their ligature, the alef's character going), and
// each chunk (from a character placed absolutely, or a path, to the next)
// holding such text put in the order it is seen, its direction that of
// the direction property, its place given to the character seen first.
func rightToLeft(chars []tchar) []tchar {
	rs := make([]rune, len(chars))
	for i, c := range chars {
		rs[i] = c.r
	}
	if !bidi.HasRTL(rs) {
		return chars
	}
	// Letters its font did not shape take their presentation forms.
	forms, keep := bidi.Shape(rs)
	form := make([]rune, len(rs))
	for i := range form {
		form[i] = -1 // an alef taken into a lam-alef ligature
	}
	for i, k := range keep {
		form[k] = forms[i]
	}
	out := make([]tchar, 0, len(chars))
	for k, c := range chars {
		if c.sh == nil {
			if form[k] < 0 {
				continue
			}
			c.r = form[k]
		}
		out = append(out, c)
	}
	for s := 0; s < len(out); {
		e := chunkEnd(out, s)
		seg := make([]rune, e-s)
		for i := range seg {
			seg[i] = out[s+i].r
		}
		if bidi.HasRTL(seg) {
			order, mirrored := bidi.Visual(seg, out[s].st.rtl)
			was := append([]tchar(nil), out[s:e]...)
			first := was[0]
			for i, k := range order {
				c := was[k]
				if c.sh == nil { // shaped glyphs are mirrored by the shaping
					c.r = mirrored[k]
				}
				// The chunk's place, and the shift its first character
				// carries (a tspan's dy down to a next line), go to the
				// character seen first: the whole chunk moves by them.
				c.x, c.y = nil, nil
				if k == 0 {
					c.dx, c.dy = 0, 0
				}
				if i == 0 {
					c.x, c.y = first.x, first.y
					c.dx, c.dy = first.dx, first.dy
				}
				out[s+i] = c
			}
		}
		s = e
	}
	return out
}

// fontHalfHeight is half the height (ascent and descent) of the first font
// of a style's family at its size; 0.56 of the size without one.
func (b *builder) fontHalfHeight(st style, face string) float64 {
	set := b.fonts(st.fontFamily, face)
	if set == nil || set.First() == nil {
		return float64(0.56 * st.fontSize)
	}
	sf := set.First().SFNT()
	var buf sfnt.Buffer
	upem := fixed.Int26_6(sf.UnitsPerEm()) << 6
	mt, err := sf.Metrics(&buf, upem, font.HintingNone)
	if err != nil {
		return float64(0.56 * st.fontSize)
	}
	u := st.fontSize / float64(sf.UnitsPerEm()) / 64
	return float64(float64(mt.Ascent+mt.Descent)*u) / 2
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

// fontAliases are families of other systems, and what browsers draw them
// with where they are not installed.
var fontAliases = map[string]string{
	"helvetica": "sans-serif", "helvetica neue": "sans-serif", "arial": "sans-serif", "verdana": "sans-serif",
	"lucida grande": "sans-serif", "geneva": "sans-serif", "tahoma": "sans-serif", "segoe ui": "system-ui",
	"-apple-system": "system-ui", "blinkmacsystemfont": "system-ui",
	"times": "serif", "times new roman": "serif", "georgia": "serif", "palatino": "serif",
	"courier": "monospace", "courier new": "monospace", "monaco": "monospace", "menlo": "monospace", "consolas": "monospace",
}

// knownFontFiles are where Windows keeps the fonts generic families stand
// for, by face: regular, bold, italic, bold italic.
var knownFontFiles = func() map[string][4][]string {
	if runtime.GOOS != "windows" {
		return nil
	}
	dir := filepath.Join(os.Getenv("WINDIR"), "Fonts")
	f := func(names ...string) [4][]string {
		var out [4][]string
		for i, n := range names {
			out[i] = []string{filepath.Join(dir, n)}
		}
		return out
	}
	return map[string][4][]string{
		"Times New Roman": f("times.ttf", "timesbd.ttf", "timesi.ttf", "timesbi.ttf"),
		"Arial":           f("arial.ttf", "arialbd.ttf", "ariali.ttf", "arialbi.ttf"),
		"Consolas":        f("consola.ttf", "consolab.ttf", "consolai.ttf", "consolaz.ttf"),
		"Courier New":     f("cour.ttf", "courbd.ttf", "couri.ttf", "courbi.ttf"),
		"Segoe UI":        f("segoeui.ttf", "segoeuib.ttf", "segoeuii.ttf", "segoeuiz.ttf"),
		"Comic Sans MS":   f("comic.ttf", "comicbd.ttf", "comici.ttf", "comicz.ttf"),
		"Impact":          f("impact.ttf", "impact.ttf", "impact.ttf", "impact.ttf"),
	}
}()

// faceIndex is the place of a face among regular, bold, italic and bold
// italic.
func faceIndex(face string) int {
	w, italic := parseFace(face)
	i := 0
	if w >= 600 {
		i = 1
	}
	if italic {
		i += 2
	}
	return i
}

// faceOf is the face text asks for, by its weight (1 to 1000; 0 is 400)
// and whether it is italic: "600" or "600 Italic".
func faceOf(weight int, italic bool) string {
	if weight <= 0 {
		weight = 400
	}
	f := strconv.Itoa(weight)
	if italic {
		f += " Italic"
	}
	return f
}

func faceWeight(face string) int {
	w, _ := parseFace(face)
	return w
}

// parseFace reads a face of faceOf.
func parseFace(face string) (weight int, italic bool) {
	w, italic := strings.CutSuffix(face, " Italic")
	weight, err := strconv.Atoi(w)
	if err != nil || weight <= 0 {
		weight = 400
	}
	return weight, italic
}

// The weights of the faces of families, by the names their fonts give
// them.
var (
	weightLadder = []int{100, 200, 300, 350, 400, 500, 600, 700, 800, 900}
	weightNames  = map[int][]string{
		100: {"Thin", "Hairline"},
		200: {"ExtraLight", "Extra Light", "UltraLight", "Ultra Light"},
		300: {"Light"},
		350: {"Semilight", "SemiLight", "Semi Light"},
		400: {""},
		500: {"Medium"},
		600: {"Semibold", "SemiBold", "Semi Bold", "Demibold", "DemiBold", "Demi Bold"},
		700: {"Bold"},
		800: {"ExtraBold", "Extra Bold", "UltraBold", "Ultra Bold"},
		900: {"Black", "Heavy"},
	}
)

// weightOrder is the order the weights of a family are tried in for one
// asked for, as CSS matches fonts: from 400 to 500, those from it up to
// 500, then those below it, then those above 500; below 400, those down
// from it, then those above; above 500, those up from it, then below.
func weightOrder(w int) []int {
	var out []int
	up := func(from, to int) {
		for _, l := range weightLadder {
			if l >= from && l <= to {
				out = append(out, l)
			}
		}
	}
	down := func(from, to int) {
		for i := len(weightLadder) - 1; i >= 0; i-- {
			if l := weightLadder[i]; l <= from && l >= to {
				out = append(out, l)
			}
		}
	}
	switch {
	case w >= 400 && w <= 500:
		up(w, 500)
		down(w-1, 0)
		up(501, 1000)
	case w < 400:
		down(w, 0)
		up(w+1, 1000)
	default:
		up(w, 1000)
		down(w-1, 0)
	}
	return out
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
// space, as finely as the text's transform m to the device needs.
func (b *builder) pathFor(k *node, m matrix, st style, vp viewport) *textPathInfo {
	tm := identity
	var segs []seg
	// SVG 2's path attribute (what of it parses, to its first error) comes
	// before a reference, as Chrome takes it.
	if d, ok := k.attrs["path"]; ok {
		segs = parsePath(d)
	}
	if len(segs) == 0 {
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
		if m, has := transformOf(t, st.fontSize, vp); has {
			tm = aboutOrigin(t, m, vp.w, vp.h, st.fontSize, vp, [2]float64{})
		}
		segs = shapeSegs(t, computeStyle(t, st, vp), vp)
	}
	ps, ok := flattenLimit(segs, m.mul(tm), maxPoints/16)
	if !ok || len(ps) == 0 {
		return nil
	}
	// Its subpaths one after another: the move from one to the next adds no
	// length (as Chrome runs text along them all).
	info := &textPathInfo{}
	for _, q := range ps {
		first := len(info.xy)
		for i := 0; i+1 < len(q.xy); i += 2 {
			x, y := tm.apply(q.xy[i], q.xy[i+1])
			l := 0.0
			if n := len(info.xy); n > 0 {
				l = info.cum[len(info.cum)-1]
				if n > first {
					l += hypot(x-info.xy[n-2], y-info.xy[n-1])
				}
			}
			info.xy = append(info.xy, x, y)
			info.cum = append(info.cum, l)
		}
		if q.closed && len(info.xy)-first >= 4 {
			x, y := info.xy[first], info.xy[first+1]
			info.cum = append(info.cum, info.cum[len(info.cum)-1]+hypot(x-info.xy[len(info.xy)-2], y-info.xy[len(info.xy)-1]))
			info.xy = append(info.xy, x, y)
		}
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
	preserve := n.attrs["xml:space"] == "preserve" // the text's: its spaces at the end
	// A vertical line (the text element's writing-mode, as Chrome) is laid
	// out as a horizontal one, in a frame turned a quarter clockwise (its x
	// the user space's y, its y the user space's x reversed): the glyphs of
	// most scripts lie on their side, those set upright (Chinese, Japanese,
	// Korean) are turned back and stand in the middle of the line. Not on a
	// path.
	vertical := st.vertical && !hasTextPath(n, 0)
	var walk func(k *node, kst style, ctx textCtx)
	walk = func(k *node, kst style, ctx textCtx) {
		if v, ok := k.attrs["xml:space"]; ok {
			ctx.preserve = strings.TrimSpace(v) == "preserve" // for it and what it holds
		}
		// The inherited properties of text come with the style (from the
		// element's ancestors too); these do not inherit.
		family, anchor := kst.fontFamily, kst.anchor
		face := faceOf(kst.weight, kst.italic)

		ctx.letter, ctx.word, ctx.baseline = kst.letterSp, kst.wordSp, kst.baseline
		if v, ok := k.prop("alignment-baseline"); ok { // on text too, as Chrome (D3's charts put it there)
			if bl, ok := baselineOf(v); ok {
				ctx.baseline = bl
			}
		}
		if v, ok := k.prop("baseline-shift"); ok && k != n {
			switch v = strings.TrimSpace(v); v {
			case "baseline":
			case "sub", "super":
				// Half the height of the element's font (ascent and
				// descent), down or up, as Chrome shifts them.
				half := b.fontHalfHeight(kst, face)
				if v == "sub" {
					half = -half
				}
				ctx.shift += half
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
			ctx.path = b.pathFor(k, m, kst, vp)
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
		if k.name == "textPath" {
			xs, ys, dxs, dys, rots = nil, nil, nil, nil, nil // not attributes of a textPath (Chrome passes them over)
		}
		if vertical {
			// Laid out across: along the line goes down, across it right.
			neg := func(v []float64) []float64 {
				out := make([]float64, len(v))
				for i, f := range v {
					out[i] = -f
				}
				return out
			}
			xs, ys, dxs, dys = ys, neg(xs), dys, neg(dxs)
		}
		first := len(chars)
		kctx := ctx
		// White space is collapsed as the characters are added (newlines
		// dropped, tabs made spaces, a space after a space or at the start
		// dropped), so that the positions go to the characters that stay.
		addText := func(s string) {
			for _, r := range s {
				switch r {
				case '\n', '\r':
					if !kctx.preserve {
						continue
					}
					r = ' '
				case '\t':
					r = ' '
				}
				if !kctx.preserve && r == ' ' && (len(chars) == 0 || chars[len(chars)-1].r == ' ') {
					continue
				}
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
	if !preserve {
		// The spaces at the end.
		for len(chars) > 0 && chars[len(chars)-1].r == ' ' {
			chars = chars[:len(chars)-1]
		}
	}
	if len(chars) == 0 {
		return
	}
	b.shapeChars(chars)
	// Each character's place across the line: y and dy added up in the
	// order of the text (as SVG lays it out, and Chrome), before right-to-
	// left runs are put in the order they are seen: a part of an Arabic
	// word a tspan moves down keeps its letters.
	ly := 0.0
	for i := range chars {
		c := &chars[i]
		path := c.tctx.path
		if path != nil && (i == 0 || chars[i-1].tctx.path != path) {
			ly = 0
		}
		if c.y != nil && path == nil {
			ly = *c.y
		}
		ly += c.dy
		c.ly = ly
	}
	chars = rightToLeft(chars)
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
		desc    float64 // the descent, down from the baseline (the bottom of its cell)
		// A glyph of a shaped cluster: where it is drawn from the pen
		// (y down); extra for those after the cluster's first, which share
		// its pen, advance and frame.
		gx, gy float64
		extra  bool
		// In a vertical line, a glyph that stands upright: its width
		// across, and how far down from its pen its baseline is.
		upright    bool
		hadv, vorg float64
		// Of a font with variations: the face it is drawn from, at the
		// instance of its style.
		vface *shape.Face
		vars  []shape.Variation
	}
	var out, extra []placed
	var buf sfnt.Buffer
	penX, penY := 0.0, 0.0
	chunk := 0
	var chunkStart, chunkFirst []float64
	var chunkPath []*textPathInfo
	var prev sfnt.GlyphIndex
	var prevFont *textsrc.Font
	// The characters as laid out, for the variation selectors that ask for
	// an emoji in colour or as text.
	visual := make([]rune, len(chars))
	for i, c := range chars {
		visual[i] = c.r
	}
	for i := range chars {
		c := &chars[i]
		path := c.tctx.path
		newPath := path != nil && (i == 0 || chars[i-1].tctx.path != path)
		newChunk := c.x != nil || c.y != nil || i == 0 || newPath || path == nil && i > 0 && chars[i-1].tctx.path != nil
		if newChunk {
			if i > 0 {
				chunk++
			}
			if newPath {
				penX = 0
			}
			if c.x != nil {
				// On a path, an absolute x is the distance along it (as
				// SVG 2 lays text on paths out, and Chrome).
				penX = *c.x
			}
			chunkStart = append(chunkStart, penX)
			chunkPath = append(chunkPath, path)
			prevFont = nil
		}
		penX += c.dx
		penY = c.ly
		if newChunk {
			chunkFirst = append(chunkFirst, penX) // its first character's place, its dx in it
		}
		if c.sh != nil && !c.sh.head {
			continue // drawn with the first of its cluster
		}
		if c.sh == nil && bidi.Ignorable(c.r) {
			continue // drawn as nothing, taking no room
		}
		set := b.fonts(c.family, c.face)
		if set == nil {
			continue
		}
		var f *textsrc.Font
		if c.sh != nil {
			f = c.sh.font
		} else if f, _ = set.PickFor(visual, i); f == nil {
			continue
		}
		sf := f.SFNT()
		upem := fixed.Int26_6(sf.UnitsPerEm()) << 6
		scale := c.st.fontSize / float64(sf.UnitsPerEm())
		var gi sfnt.GlyphIndex
		up := vertical && c.sh == nil && (c.st.orient == orientUpright || c.st.orient == orientMixed && upright(c.r))
		if c.sh != nil {
			if len(c.sh.glyphs) == 0 {
				continue
			}
			gi = sfnt.GlyphIndex(c.sh.glyphs[0].ID)
		} else {
			var err error
			if gi, err = sf.GlyphIndex(&buf, c.r); err != nil {
				continue
			}
			if prevFont == f && !up {
				if k, err := sf.Kern(&buf, prev, gi, upem, font.HintingNone); err == nil {
					penX += float64(float64(float64(k)/64) * scale)
				}
			}
		}
		p := placed{c: c, f: f, gi: gi, x: penX, y: penY, chunk: chunk, sx: 1}
		if f.Variable() {
			// Drawn at the instance of its style (its outlines and
			// advances); a face that cannot be read, at its default.
			if vf, err := set.ShapingFace(f); err == nil && vf != nil && vf.Variable() {
				p.vface, p.vars = vf, instance(c.st)
				vf.SetVariations(p.vars)
			}
		}
		// The lines of the set's first font, in its units of 1/64, whatever
		// font draws the character (as Chrome: a line of mixed scripts keeps
		// one baseline and one underline).
		msf, mupem, mscale := sf, upem, scale
		if first := set.First(); first != nil {
			msf = first.SFNT()
			mupem = fixed.Int26_6(msf.UnitsPerEm()) << 6
			mscale = c.st.fontSize / float64(msf.UnitsPerEm())
		}
		u := float64(mscale / 64)
		if mt, err := msf.Metrics(&buf, mupem, font.HintingNone); err == nil {
			asc, desc, xh := float64(mt.Ascent), float64(mt.Descent), float64(mt.XHeight)
			if !(xh > 0) {
				xh = float64(asc / 2)
			}
			bl := c.tctx.baseline
			if vertical && bl == baseAlphabetic {
				bl = baseCentral // a vertical line's (as Chrome)
			}
			switch bl {
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
			p.desc = float64(desc * u)
			// The line-through's middle a third of the ascent up (as Chrome).
			p.through = -float64(asc*u) / 3
		}
		p.thick = float64(c.st.fontSize / 14)
		p.under = float64(c.st.fontSize / 10)
		if post := msf.PostTable(); post != nil && post.UnderlineThickness > 0 {
			p.thick = float64(float64(post.UnderlineThickness) * mscale)
			p.under = -float64(float64(post.UnderlinePosition) * mscale)
		}
		p.through -= float64(p.thick / 2) // its top
		adv := 0.0
		if c.sh != nil {
			// The cluster's advance, its glyphs placed from its pen.
			pen := int32(0)
			for k, g := range c.sh.glyphs {
				if k > 0 {
					q := p
					q.gi, q.extra = sfnt.GlyphIndex(g.ID), true
					q.gx = float64(float64(pen+g.XOffset) * scale)
					q.gy = -float64(float64(g.YOffset) * scale)
					extra = append(extra, q)
				} else {
					p.gx = float64(float64(g.XOffset) * scale)
					p.gy = -float64(float64(g.YOffset) * scale)
				}
				pen += g.XAdvance
			}
			adv = float64(float64(pen) * scale)
		} else if p.vface != nil {
			adv = float64(float64(p.vface.Advance(uint16(gi))) * scale)
		} else if a, err := sf.GlyphAdvance(&buf, gi, upem, font.HintingNone); err == nil {
			adv = float64(float64(float64(a)/64) * scale)
		}
		if up {
			// Upright: its advance down the line and its baseline as the
			// font's vertical metrics give them (the top of its outline its
			// top bearing down from the pen, as HarfBuzz places it for
			// Chrome); a font without them, its height and ascent.
			p.upright, p.hadv = true, adv
			if va, tsb, ok := f.VerticalMetrics(gi); ok {
				adv = float64(float64(va) * scale)
				if bnd, _, err := sf.GlyphBounds(&buf, gi, upem, font.HintingNone); err == nil {
					p.vorg = float64(float64(float64(tsb)-float64(float64(bnd.Min.Y)/64)) * scale)
				}
			} else if mt, err := sf.Metrics(&buf, upem, font.HintingNone); err == nil {
				adv = float64(float64(float64(mt.Ascent+mt.Descent)/64) * scale)
				p.vorg = float64(float64(float64(mt.Ascent)/64) * scale)
			}
		}
		// Letter spacing parts no letters that join (as Chrome: nor what
		// is drawn as nothing).
		if !cursive(c.r) && !bidi.Ignorable(c.r) {
			adv += c.tctx.letter
		}
		if c.r == ' ' {
			adv += c.tctx.word
		}
		p.adv = adv
		penX += adv
		p.end = penX
		out = append(out, p)
		for _, q := range extra {
			q.adv, q.end = p.adv, p.end
			out = append(out, q)
		}
		extra = extra[:0]
		prev, prevFont = gi, f
		if c.sh != nil {
			prevFont = nil // kerned by the tables
		}
	}
	// textLength: the advances of an element's characters (the text's, a
	// tspan's) made to add up to it, by spacing or by stretching the glyphs
	// too, what follows in the chunk moved with its end; inner elements
	// first.
	var withLength []*node
	var collect func(k *node, depth int)
	collect = func(k *node, depth int) {
		if depth > 64 {
			return
		}
		for _, c := range k.children {
			if c.name == "tspan" || c.name == "a" || c.name == "textPath" {
				collect(c, depth+1)
			}
		}
		if _, ok := k.attrs["textLength"]; ok {
			withLength = append(withLength, k)
		}
	}
	collect(n, 0)
	within := func(k, e *node) bool {
		for ; k != nil; k = k.parent {
			if k == e {
				return true
			}
			if k == n {
				return false
			}
		}
		return false
	}
	for _, e := range withLength {
		i0, i1 := -1, -1
		for j := range out {
			if within(out[j].c.node, e) {
				if i0 < 0 {
					i0 = j
				}
				i1 = j
			}
		}
		if i0 < 0 || out[i0].chunk != out[i1].chunk {
			continue
		}
		l, ok := parseLength(e.attrs["textLength"], st.fontSize, vp, axisX)
		if !ok || !(l > 0) {
			continue
		}
		start := out[i0].x
		w := out[i1].end - start
		if !(w > 0) {
			continue
		}
		stretch := strings.TrimSpace(e.attrs["lengthAdjust"]) == "spacingAndGlyphs"
		k := l / w
		// Spaced, the extra between the characters (as Chrome), a shaped
		// cluster's glyphs moving with it.
		clusters := 0
		for _, p := range out[i0 : i1+1] {
			if !p.extra {
				clusters++
			}
		}
		delta := 0.0
		if clusters > 1 {
			delta = (l - w) / float64(clusters-1)
		}
		oldEnd := out[i1].end
		i := -1
		for j := i0; j <= i1; j++ {
			p := &out[j]
			if p.extra {
				h := out[j-1]
				p.x, p.adv, p.sx, p.end = h.x, h.adv, h.sx, h.end
				continue
			}
			i++
			if stretch {
				p.x = start + float64((p.x-start)*k)
				p.adv = float64(p.adv * k)
				p.sx = k
			} else {
				p.x += float64(float64(i) * delta)
				if i < clusters-1 {
					p.adv += delta
				}
			}
			p.end = p.x + p.adv
		}
		moved := out[i1].end - oldEnd
		for j := i1 + 1; j < len(out) && out[j].chunk == out[i1].chunk; j++ {
			out[j].x += moved
			out[j].end += moved
		}
	} // Anchor each chunk by the extent of its glyphs (the least and the
	// greatest of their pens and pens plus advances: a dx that sends one
	// back counts, as SVG 2 anchors text and Chrome).
	lo, hi := map[int]float64{}, map[int]float64{}
	anchors := map[int]int{}
	for _, p := range out {
		a, z := math.Min(p.x, p.x+p.adv), math.Max(p.x, p.x+p.adv)
		if v, ok := lo[p.chunk]; !ok || a < v {
			lo[p.chunk] = a
		}
		if v, ok := hi[p.chunk]; !ok || z > v {
			hi[p.chunk] = z
		}
		if _, ok := anchors[p.chunk]; !ok {
			a := p.c.anchor
			if p.c.st.rtl {
				// Right to left, the start is the right end.
				switch a {
				case anchorStart:
					a = anchorEnd
				case anchorEnd:
					a = anchorStart
				}
			}
			anchors[p.chunk] = a
		}
	}
	// Each glyph's frame: where its origin goes and its axes, across and
	// down.
	type frame struct{ ox, oy, ux, uy, vx, vy float64 }
	frames := make([]frame, len(out))
	// The frames the glyphs are drawn in: those of the line but for upright
	// glyphs in a vertical one.
	gframes := frames
	if vertical {
		gframes = make([]frame, len(out))
	}
	drawn := make([]bool, len(out))
	for i, p := range out {
		shift := 0.0
		if p.chunk < len(chunkStart) {
			x0 := chunkFirst[p.chunk]
			switch anchors[p.chunk] {
			case anchorMiddle:
				shift = x0 - float64((lo[p.chunk]+hi[p.chunk])/2)
			case anchorEnd:
				shift = x0 - hi[p.chunk]
			default:
				shift = x0 - lo[p.chunk]
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
		if vertical {
			gframes[i] = frames[i]
			if p.upright {
				// Standing, turned back a quarter (by its rotation too), in
				// the middle of the line across, its pen at its top.
				s, c := 0.0, 1.0
				if p.c.rotSet && p.c.rot != 0 {
					s, c = trig.SinCosDeg(p.c.rot)
				}
				gframes[i] = frame{ox: p.x + shift + p.vorg, oy: p.y - p.c.tctx.shift + float64(p.hadv/2), ux: s, uy: -c, vx: c, vy: s}
			}
		}
		drawn[i] = true
	}
	// user is a point of the frames in the text's user space.
	user := func(x, y float64) [2]float64 {
		if vertical {
			return [2]float64{-y, x}
		}
		return [2]float64{x, y}
	}
	apply := func(fr frame, gx, gy float64) [2]float64 {
		return user(fr.ox+float64(fr.ux*gx)+float64(fr.vx*gy), fr.oy+float64(fr.uy*gx)+float64(fr.vy*gy))
	}
	if b.measureText != nil {
		// Measured, not drawn: each character's cell, its advance across
		// and its font's ascent to descent down (the box Chrome gives text).
		for i, p := range out {
			if drawn[i] && !p.extra {
				fr := frames[i]
				b.measureText(p.c.node, [4][2]float64{apply(fr, 0, p.over), apply(fr, p.adv, p.over), apply(fr, 0, p.desc), apply(fr, p.adv, p.desc)})
			}
		}
		return
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
				quad(k.d, [4][2]float64{user(bd.x0, bd.y), user(bd.x1, bd.y), user(bd.x1, bd.y+bd.thick), user(bd.x0, bd.y+bd.thick)})
				bd.open = false
			}
		}
		for i, p := range out {
			if !drawn[i] || p.extra {
				continue
			}
			fr := frames[i]
			straight := chunkPath[p.chunk] == nil && !(p.c.rotSet && p.c.rot != 0)
			for _, d := range p.c.tctx.decos {
				for kind, on := range [3]bool{d.under && !through, d.over && !through, d.through && through} {
					if !on {
						continue
					}
					// The overline stands on the ascent line (as Chrome draws it).
					top := [3]float64{p.under, p.over - p.thick, p.through}[kind]
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
	var vsegs []shape.Segment // an outline of a font with variations
	var runSegs []seg
	var runNode *node
	var layers []colorfont.Layer
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
		scale := p.c.st.fontSize / float64(sf.UnitsPerEm()) / 64
		fr := gframes[i]
		pt := func(q fixed.Point26_6) [2]float64 {
			return apply(fr, p.gx+float64(float64(q.X)*scale), p.gy+float64(float64(q.Y)*scale))
		}
		// outline appends the outline of glyph gi to segs.
		outline := func(segs []seg, gi sfnt.GlyphIndex) []seg {
			if p.vface != nil {
				// At the instance (set again for each: drawing a run may
				// draw a pattern of text of its face): in font units, y up.
				p.vface.SetVariations(p.vars)
				var ok bool
				if vsegs, ok = p.vface.Outline(uint16(gi), vsegs[:0]); ok {
					k := p.c.st.fontSize / float64(sf.UnitsPerEm())
					at := func(a [2]float32) [2]float64 {
						return apply(fr, p.gx+float64(float64(a[0])*k), p.gy-float64(float64(a[1])*k))
					}
					for _, g := range vsegs {
						switch g.Op {
						case shape.MoveTo:
							if len(segs) > 0 && segs[len(segs)-1].cmd != cmdClose {
								segs = append(segs, seg{cmd: cmdClose})
							}
							segs = append(segs, seg{cmd: cmdMove, pts: [3][2]float64{at(g.Args[0])}})
						case shape.LineTo:
							segs = append(segs, seg{cmd: cmdLine, pts: [3][2]float64{at(g.Args[0])}})
						case shape.QuadTo:
							segs = append(segs, seg{cmd: cmdQuad, pts: [3][2]float64{at(g.Args[0]), at(g.Args[1])}})
						default:
							segs = append(segs, seg{cmd: cmdCubic, pts: [3][2]float64{at(g.Args[0]), at(g.Args[1]), at(g.Args[2])}})
						}
					}
					if len(segs) > 0 && segs[len(segs)-1].cmd != cmdClose {
						segs = append(segs, seg{cmd: cmdClose})
					}
					return segs
				}
			}
			gsegs, err := sf.LoadGlyph(&buf, gi, upem, nil)
			if err != nil {
				return segs
			}
			for _, g := range gsegs {
				switch g.Op {
				case sfnt.SegmentOpMoveTo:
					if len(segs) > 0 && segs[len(segs)-1].cmd != cmdClose {
						segs = append(segs, seg{cmd: cmdClose})
					}
					segs = append(segs, seg{cmd: cmdMove, pts: [3][2]float64{pt(g.Args[0])}})
				case sfnt.SegmentOpLineTo:
					segs = append(segs, seg{cmd: cmdLine, pts: [3][2]float64{pt(g.Args[0])}})
				case sfnt.SegmentOpQuadTo:
					segs = append(segs, seg{cmd: cmdQuad, pts: [3][2]float64{pt(g.Args[0]), pt(g.Args[1])}})
				case sfnt.SegmentOpCubeTo:
					segs = append(segs, seg{cmd: cmdCubic, pts: [3][2]float64{pt(g.Args[0]), pt(g.Args[1]), pt(g.Args[2])}})
				}
			}
			if len(segs) > 0 && segs[len(segs)-1].cmd != cmdClose {
				segs = append(segs, seg{cmd: cmdClose})
			}
			return segs
		}
		// A colour glyph of COLR version 1: its paint graph.
		if set := b.fonts(p.c.family, p.c.face); set != nil {
			if cf, err := set.ColourFont(p.f); err != nil {
				b.err = err
			} else if cf.HasPaint(uint16(p.gi)) {
				flush()
				k := p.c.st.fontSize / float64(sf.UnitsPerEm())
				fm := matrix{fr.ux, fr.uy, fr.vx, fr.vy, fr.ox, fr.oy}
				if vertical {
					fm = matrix{0, 1, -1, 0, 0, 0}.mul(fm)
				}
				b.colrGlyph(p.c.node, cf, p.f, p.gi, m.mul(fm).mul(matrix{k, 0, 0, -k, p.gx, p.gy}), p.c.st, own, outer)
				continue
			}
		}
		// A colour glyph (as a colour emoji): its layers, each filled in its
		// colour (a layer of the text's colour as the text is), unstroked.
		if set := b.fonts(p.c.family, p.c.face); set != nil {
			var err error
			if layers, err = set.ColourLayers(p.f, p.gi, layers[:0]); err != nil {
				b.err = err
			}
		}
		// A glyph of a bitmap colour font: its picture.
		if set := b.fonts(p.c.family, p.c.face); set != nil && len(layers) == 0 {
			ppem := float64(p.c.st.fontSize * math.Sqrt(math.Abs(m.det())))
			bm, ok, err := set.Picture(p.f, p.gi, ppem)
			if err != nil {
				b.err = err
			}
			if ok {
				flush()
				fm := matrix{fr.ux, fr.uy, fr.vx, fr.vy, fr.ox, fr.oy}
				if vertical {
					fm = matrix{0, 1, -1, 0, 0, 0}.mul(fm) // the frame turned into the user space
				}
				b.glyphPicture(p.c.node, bm, m.mul(fm), p.gx, p.gy, p.c.st.fontSize, p.c.st, own, outer)
				continue
			}
		}
		if len(layers) > 0 {
			flush()
			for _, l := range layers {
				lst := p.c.st
				if !l.Foreground {
					lst.fill = paint{kind: paintColor, c: l.Color}
				}
				lst.stroke = paint{kind: paintNone}
				if segs := outline(nil, sfnt.GlyphIndex(l.Glyph)); len(segs) > 0 {
					b.shape(p.c.node, segs, m, lst, vp, own, outer)
				}
			}
			continue
		}
		runSegs = outline(runSegs, p.gi)
		if len(runSegs) > maxSegs {
			flush()
		}
	}
	flush()
	decoLines(true)
}

// hasTextPath reports whether text holds a textPath.
func hasTextPath(k *node, depth int) bool {
	if depth > 64 {
		return false
	}
	for _, c := range k.children {
		if c.name == "textPath" || (c.name == "tspan" || c.name == "a") && hasTextPath(c, depth+1) {
			return true
		}
	}
	return false
}

// upright reports whether a character stands upright in a vertical line
// (text-orientation: mixed): those Unicode's vertical orientation property
// (UAX #50) gives U or Tu, Tr in the main: the scripts of China, Japan and
// Korea, their symbols and punctuation, full width forms, emoji.
func upright(r rune) bool {
	switch r {
	case 0x3008, 0x3009, 0x300A, 0x300B, 0x300C, 0x300D, 0x300E, 0x300F, 0x3010, 0x3011, 0x3014, 0x3015, 0x3016, 0x3017,
		0x3018, 0x3019, 0x301A, 0x301B, 0x301C, 0x3030, 0x30A0, 0x30FC, 0xFF08, 0xFF09, 0xFF0D, 0xFF1A, 0xFF1B, 0xFF1C,
		0xFF1D, 0xFF1E, 0xFF3B, 0xFF3D, 0xFF3F, 0xFF5B, 0xFF5C, 0xFF5D, 0xFF5E, 0xFF5F, 0xFF60, 0xFFE3:
		// Brackets, dashes and the long vowel mark (Tr): their vertical
		// forms are about the horizontal ones on their side.
		return false
	}
	switch {
	case r < 0x1100:
		return r == 0xA7 || r == 0xA9 || r == 0xAE || r == 0xB1 || r == 0xBC || r == 0xBD || r == 0xBE || r == 0xD7 || r == 0xF7
	case r <= 0x11FF, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF,   // CJK radicals to Yi
		r >= 0xA960 && r <= 0xA97F,   // Hangul Jamo extended A
		r >= 0xAC00 && r <= 0xD7FF,   // Hangul syllables, Jamo extended B
		r >= 0xE000 && r <= 0xFAFF,   // private use, CJK compatibility ideographs
		r >= 0xFE10 && r <= 0xFE1F,   // vertical forms
		r >= 0xFE30 && r <= 0xFE4F,   // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFFEF,   // half and full width forms
		r >= 0x1F000 && r <= 0x1FAFF, // symbols and emoji
		r >= 0x20000 && r <= 0x3FFFF: // CJK ideographs, extensions B on
		return true
	}
	return false
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

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
