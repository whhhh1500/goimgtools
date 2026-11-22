package svg

import (
	"image/color"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/csscolor"
)

// decl is a declaration of a style sheet that applies to an element.
type decl struct {
	value     string
	important bool
	spec      int // specificity: ids, classes, types, 100 apart
	order     int // its place in the sheets
}

// beats reports whether d takes precedence over e.
func (d decl) beats(e decl) bool {
	if d.important != e.important {
		return d.important
	}
	if d.spec != e.spec {
		return d.spec > e.spec
	}
	return d.order > e.order
}

// compound is a simple selector: a type, an id, classes and attributes.
type compound struct {
	tag     string // "" for any
	id      string
	classes []string
	attrs   []attrSel
	root    bool // :root, the document's element
}

// attrSel is an attribute selector: [name], or [name op value] with op
// one of = ~= |= ^= $= *=, its value compared in any case with i.
type attrSel struct {
	name, op, value string
	fold            bool
}

func (a attrSel) matches(n *node) bool {
	v, ok := n.attrs[a.name]
	if !ok {
		return false
	}
	want := a.value
	if a.fold {
		v, want = strings.ToLower(v), strings.ToLower(want)
	}
	switch a.op {
	case "":
		return true
	case "=":
		return v == want
	case "~=":
		for _, f := range strings.Fields(v) {
			if f == want {
				return true
			}
		}
		return false
	case "|=":
		return v == want || strings.HasPrefix(v, want+"-")
	case "^=":
		return want != "" && strings.HasPrefix(v, want)
	case "$=":
		return want != "" && strings.HasSuffix(v, want)
	case "*=":
		return want != "" && strings.Contains(v, want)
	}
	return false
}

// parseAttrSel reads what is between the brackets of an attribute
// selector.
func parseAttrSel(s string) (attrSel, bool) {
	s = strings.TrimSpace(s)
	i := strings.IndexAny(s, "=~|^$*")
	if i < 0 {
		return attrSel{name: s}, s != "" && !strings.ContainsAny(s, " \t\"'")
	}
	a := attrSel{name: strings.TrimSpace(s[:i])}
	rest := s[i:]
	if rest[0] == '=' {
		a.op, rest = "=", rest[1:]
	} else if len(rest) > 1 && rest[1] == '=' {
		a.op, rest = rest[:2], rest[2:]
	} else {
		return a, false
	}
	rest = strings.TrimSpace(rest)
	if len(rest) > 0 && (rest[0] == '"' || rest[0] == '\'') {
		end := strings.IndexByte(rest[1:], rest[0])
		if end < 0 {
			return a, false
		}
		a.value, rest = rest[1:1+end], strings.TrimSpace(rest[2+end:])
	} else {
		f := strings.Fields(rest)
		if len(f) == 0 {
			return a, false
		}
		a.value, rest = f[0], strings.TrimSpace(strings.TrimPrefix(rest, f[0]))
	}
	switch strings.ToLower(rest) {
	case "":
	case "i":
		a.fold = true
	case "s":
	default:
		return a, false
	}
	return a, a.name != "" && !strings.ContainsAny(a.name, " \t\"'")
}

func (c compound) matches(n *node) bool {
	if c.tag != "" && c.tag != n.name {
		return false
	}
	if c.root && n.parent != nil {
		return false
	}
	if c.id != "" && n.attrs["id"] != c.id {
		return false
	}
	for _, a := range c.attrs {
		if !a.matches(n) {
			return false
		}
	}
	if len(c.classes) > 0 {
		have := strings.Fields(n.attrs["class"])
		for _, want := range c.classes {
			found := false
			for _, h := range have {
				if h == want {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		}
	}
	return true
}

// selector is a chain of compounds joined by descendant (' ') or child
// ('>') combinators; the last is the element styled.
type selector struct {
	parts []compound
	combs []byte // combs[i] joins parts[i] and parts[i+1]
	spec  int
}

func (s selector) matches(n *node) bool {
	return s.matchFrom(n, len(s.parts)-1)
}

func (s selector) matchFrom(n *node, i int) bool {
	if !s.parts[i].matches(n) {
		return false
	}
	if i == 0 {
		return true
	}
	if s.combs[i-1] == '>' {
		return n.parent != nil && s.matchFrom(n.parent, i-1)
	}
	for p := n.parent; p != nil; p = p.parent {
		if s.matchFrom(p, i-1) {
			return true
		}
	}
	return false
}

// parseSelector reads one selector; ok is false for what is not supported
// (pseudo-classes but :root, sibling combinators), which then matches
// nothing.
func parseSelector(src string) (selector, bool) {
	var s selector
	src = strings.TrimSpace(src)
	var comb byte = 0
	for len(src) > 0 {
		if src[0] == ' ' || src[0] == '>' || src[0] == '\t' || src[0] == '\n' || src[0] == '\r' {
			if src[0] == '>' {
				comb = '>'
			} else if comb == 0 {
				comb = ' '
			}
			src = src[1:]
			continue
		}
		// The compound: to a space or '>' outside brackets and quotes.
		end, depth := len(src), 0
		var quote byte
	scan:
		for i := 0; i < len(src); i++ {
			switch ch := src[i]; {
			case quote != 0:
				if ch == quote {
					quote = 0
				}
			case ch == '"' || ch == '\'':
				quote = ch
			case ch == '[':
				depth++
			case ch == ']':
				depth--
			case depth <= 0 && (ch == ' ' || ch == '>' || ch == '\t' || ch == '\n' || ch == '\r'):
				end = i
				break scan
			}
		}
		tok := src[:end]
		src = src[end:]
		var c compound
		for len(tok) > 0 {
			if tok[0] == '[' {
				close, q := -1, byte(0)
				for i := 1; i < len(tok) && close < 0; i++ {
					switch {
					case q != 0:
						if tok[i] == q {
							q = 0
						}
					case tok[i] == '"' || tok[i] == '\'':
						q = tok[i]
					case tok[i] == ']':
						close = i
					}
				}
				if close < 0 {
					return s, false
				}
				a, ok := parseAttrSel(tok[1:close])
				if !ok {
					return s, false
				}
				c.attrs = append(c.attrs, a)
				s.spec += 100 // as a class
				tok = tok[close+1:]
				continue
			}
			k := strings.IndexAny(tok[1:], ".#[:") + 1
			if k == 0 {
				k = len(tok)
			}
			part := tok[:k]
			tok = tok[k:]
			switch part[0] {
			case '.':
				c.classes = append(c.classes, part[1:])
				s.spec += 100
			case '#':
				c.id = part[1:]
				s.spec += 10000
			case ':':
				if part == ":root" {
					c.root = true
					s.spec += 100 // a pseudo-class counts as a class
					continue
				}
				return s, false
			default:
				if strings.ContainsAny(part, ":[]()") {
					return s, false
				}
				if part != "*" {
					c.tag = part
					s.spec++
				}
			}
		}
		if len(s.parts) > 0 {
			if comb == 0 {
				comb = ' '
			}
			s.combs = append(s.combs, comb)
		}
		s.parts = append(s.parts, c)
		comb = 0
	}
	return s, len(s.parts) > 0
}

// declarations reads "a: b; c: d !important".
func declarations(src string) [][3]string {
	var out [][3]string
	if strings.Contains(src, "/*") {
		src = stripComments(src)
	}
	for _, d := range strings.Split(src, ";") {
		k, v, ok := strings.Cut(d, ":")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if !strings.HasPrefix(k, "--") {
			k = strings.ToLower(k) // custom properties keep their case
		}
		v = strings.TrimSpace(v)
		imp := ""
		if i := strings.Index(strings.ToLower(v), "!important"); i >= 0 {
			v, imp = strings.TrimSpace(v[:i]), "!"
		}
		if k != "" && v != "" {
			out = append(out, [3]string{k, v, imp})
		}
	}
	return out
}

// applySheets matches the rules of every style element of the document to
// its elements.
func applySheets(root *node) {
	// Each style element is a sheet of its own (what is broken in one does
	// not spill into the next), their rules in the document's order.
	var sheets []string
	var walk func(n *node)
	walk = func(n *node) {
		if n.name == "style" && isCSS(n.attrs["type"]) && n.text != "" {
			sheets = append(sheets, n.text)
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	if len(sheets) == 0 {
		return
	}
	var rules []cssRule
	for _, sheet := range sheets {
		rules = append(rules, parseRules(stripComments(sheet))...)
	}
	order := 0
	var apply func(n *node)
	apply = func(n *node) {
		for _, r := range rules {
			best := -1
			for _, s := range r.sels {
				if s.spec > best && s.matches(n) {
					best = s.spec
				}
			}
			if best < 0 {
				continue
			}
			for _, d := range r.decls {
				order++
				nd := decl{value: d[1], important: d[2] != "", spec: best, order: order}
				if n.sheet == nil {
					n.sheet = map[string]decl{}
				}
				if old, ok := n.sheet[d[0]]; !ok || nd.beats(old) {
					n.sheet[d[0]] = nd
				}
			}
		}
		for _, c := range n.children {
			apply(c)
		}
	}
	apply(root)
}

// isCSS reports whether a style element's type is CSS: none, or text/css
// (with parameters, in any case).
func isCSS(typ string) bool {
	t, _, _ := strings.Cut(typ, ";")
	t = strings.TrimSpace(t)
	return t == "" || strings.EqualFold(t, "text/css")
}

// mediaMatches evaluates a media query list for a screen in the light
// colour scheme, with no reduced motion: its media types, those features,
// "not", "and" and commas; any other feature (sizes among them) does not
// hold.
func mediaMatches(q string) bool {
	for _, one := range strings.Split(strings.ToLower(q), ",") {
		words := strings.Fields(strings.NewReplacer("(", " ( ", ")", " ) ").Replace(one))
		not := len(words) > 0 && words[0] == "not"
		if not || len(words) > 0 && words[0] == "only" {
			words = words[1:]
		}
		ok := true
		for i := 0; i < len(words); i++ {
			w := words[i]
			switch {
			case w == "and":
			case w == "(":
				// A feature: up to its parenthesis.
				j := i + 1
				for j < len(words) && words[j] != ")" {
					j++
				}
				feat := strings.Join(words[i+1:min(j, len(words))], " ")
				ok = ok && mediaFeature(feat)
				i = j
			default: // a media type
				ok = ok && (w == "all" || w == "screen")
			}
		}
		if ok != not {
			return true
		}
	}
	return false
}

func mediaFeature(f string) bool {
	name, value, _ := strings.Cut(f, ":")
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	switch name {
	case "prefers-color-scheme":
		return value == "light"
	case "prefers-reduced-motion", "prefers-reduced-transparency", "prefers-contrast":
		return value == "no-preference"
	case "color":
		return true
	}
	return false
}

// cssRule is a rule of a style sheet: its selectors and declarations.
type cssRule struct {
	sels  []selector
	decls [][3]string
}

// parseRules reads the rules of a style sheet (its comments gone); at-rules
// are passed over.
func parseRules(src string) []cssRule {
	var rules []cssRule
	for len(src) > 0 {
		// At-rules without a block (@import, @charset, @namespace) end at
		// their semicolon.
		if t := strings.TrimLeft(src, " \t\r\n"); strings.HasPrefix(t, "@") {
			semi, brace := strings.IndexByte(t, ';'), strings.IndexByte(t, '{')
			if semi >= 0 && (brace < 0 || semi < brace) {
				src = t[semi+1:]
				continue
			}
		}
		open := strings.IndexByte(src, '{')
		if open < 0 {
			break
		}
		head := strings.TrimSpace(src[:open])
		// The block, with any nested one (an at-rule's).
		depth, end := 0, -1
		for i := open; i < len(src); i++ {
			if src[i] == '{' {
				depth++
			} else if src[i] == '}' {
				depth--
				if depth == 0 {
					end = i
					break
				}
			}
		}
		if end < 0 {
			break
		}
		body := src[open+1 : end]
		src = src[end+1:]
		if strings.HasPrefix(head, "@") {
			// The rules of a media query that holds for a picture drawn as
			// a browser shows it on a screen in its light scheme; other
			// at-rules (font faces and the like) are not used.
			if q, ok := strings.CutPrefix(head, "@media"); ok && mediaMatches(q) {
				rules = append(rules, parseRules(body)...)
			}
			continue
		}
		var r cssRule
		for _, part := range strings.Split(head, ",") {
			if s, ok := parseSelector(part); ok {
				r.sels = append(r.sels, s)
			}
		}
		r.decls = declarations(body)
		if len(r.sels) > 0 && len(r.decls) > 0 {
			rules = append(rules, r)
		}
	}
	return rules
}

func stripComments(s string) string {
	var b strings.Builder
	for {
		i := strings.Index(s, "/*")
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		j := strings.Index(s[i+2:], "*/")
		if j < 0 {
			return b.String()
		}
		s = s[i+2+j+2:]
	}
}

// styleValue is the value of the last declaration of a property in a style
// attribute, as declarations reads them, found without making them all
// (an element's properties are each looked up).
func styleValue(st, name string) (string, bool) {
	if strings.Contains(st, "/*") {
		st = stripComments(st)
	}
	custom := strings.HasPrefix(name, "--")
	var found string
	ok := false
	for len(st) > 0 {
		d := st
		if i := strings.IndexByte(st, ';'); i >= 0 {
			d, st = st[:i], st[i+1:]
		} else {
			st = ""
		}
		k, v, has := strings.Cut(d, ":")
		if !has {
			continue
		}
		k = strings.TrimSpace(k)
		if custom || strings.HasPrefix(k, "--") {
			if k != name {
				continue
			}
		} else if !strings.EqualFold(k, name) {
			continue
		}
		v = strings.TrimSpace(v)
		if i := strings.Index(strings.ToLower(v), "!important"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		if v != "" {
			found, ok = v, true
		}
	}
	return found, ok
}

// prop is the specified value of a property of an element: its style
// attribute, then the style sheets, then its presentation attribute (an
// important sheet declaration beats the style attribute).
func (n *node) prop(name string) (string, bool) {
	sheet, hasSheet := n.sheet[name]
	if hasSheet && sheet.important {
		return sheet.value, true
	}
	if st, ok := n.attrs["style"]; ok {
		if v, ok := styleValue(st, name); ok {
			return v, true
		}
	}
	if hasSheet {
		return sheet.value, true
	}
	v, ok := n.attrs[name]
	return strings.TrimSpace(v), ok
}

// cssProp is the value of a property that is not a presentation attribute
// (mix-blend-mode, isolation): from the style attribute or style sheets
// only.
func (n *node) cssProp(name string) (string, bool) {
	sheet, hasSheet := n.sheet[name]
	if hasSheet && sheet.important {
		return sheet.value, true
	}
	if st, ok := n.attrs["style"]; ok {
		if v, ok := styleValue(st, name); ok {
			return v, true
		}
	}
	return sheet.value, hasSheet
}

// parsePaintOrder reads paint-order: normal, or fill, stroke and markers
// each at most once, those named first and the others after in their
// usual order; anything else is not one (and leaves it as it was).
func parsePaintOrder(v string) ([3]string, bool) {
	order := [3]string{"fill", "stroke", "markers"}
	f := strings.Fields(strings.ToLower(v))
	if len(f) == 1 && f[0] == "normal" {
		return order, true
	}
	if len(f) == 0 || len(f) > 3 {
		return order, false
	}
	var out [3]string
	n := 0
	for _, w := range f {
		if w != "fill" && w != "stroke" && w != "markers" || slices.Contains(out[:n], w) {
			return order, false
		}
		out[n] = w
		n++
	}
	for _, w := range order {
		if !slices.Contains(out[:n], w) {
			out[n] = w
			n++
		}
	}
	return out, true
}

// rootBackground is the colour of the background or background-color
// property of the root (CSS only: from its style sheets, then its style
// attribute, then sheets' !important declarations; the later of the two
// properties wins within each).
func rootBackground(n *node) (color.NRGBA, bool) {
	var c color.NRGBA
	found := false
	take := func(name, v string) {
		switch name {
		case "background-color":
			if k, ok := csscolor.Parse(strings.TrimSpace(v)); ok {
				c, found = k, true
			}
		case "background":
			// The shorthand: its colour, if it names one; else none.
			c, found = color.NRGBA{}, false
			for _, t := range cssTokens(v) {
				if k, ok := csscolor.Parse(t); ok {
					c, found = k, true
				}
			}
		}
	}
	sheet := func(important bool) {
		a, okA := n.sheet["background"]
		b, okB := n.sheet["background-color"]
		okA = okA && a.important == important
		okB = okB && b.important == important
		if okA && okB && !a.beats(b) {
			take("background", a.value)
			take("background-color", b.value)
			return
		}
		if okB {
			take("background-color", b.value)
		}
		if okA {
			take("background", a.value)
		}
	}
	sheet(false)
	if st, ok := n.attrs["style"]; ok {
		for _, d := range declarations(st) {
			take(d[0], d[1])
		}
	}
	sheet(true)
	return c, found && c.A > 0
}

// cssTokens splits a CSS value at the spaces outside parentheses.
func cssTokens(v string) []string {
	var out []string
	depth, start := 0, -1
	for i := 0; i <= len(v); i++ {
		if i == len(v) || depth == 0 && (v[i] == ' ' || v[i] == '\t' || v[i] == '\n' || v[i] == '\r') {
			if start >= 0 {
				out = append(out, v[start:i])
				start = -1
			}
			continue
		}
		if start < 0 {
			start = i
		}
		switch v[i] {
		case '(':
			depth++
		case ')':
			depth = max(0, depth-1)
		}
	}
	return out
}

// Kinds of paint.
const (
	paintNone = iota
	paintColor
	paintURL
	paintCurrent
	paintContextFill   // context-fill (SVG 2): the fill of a marker's shape, of a use element
	paintContextStroke // context-stroke: its stroke
)

type paint struct {
	kind     int
	c        color.NRGBA
	url      string
	fallback *paint // after a url that does not resolve
}

// parsePaint reads a fill or stroke.
func parsePaint(s string) (paint, bool) {
	s = strings.TrimSpace(s)
	switch strings.ToLower(s) {
	case "none":
		return paint{kind: paintNone}, true
	case "currentcolor":
		return paint{kind: paintCurrent}, true
	case "context-fill":
		return paint{kind: paintContextFill}, true
	case "context-stroke":
		return paint{kind: paintContextStroke}, true
	}
	if strings.HasPrefix(s, "url(") {
		end := strings.IndexByte(s, ')')
		if end < 0 {
			return paint{}, false
		}
		id := strings.Trim(strings.TrimSpace(s[4:end]), `"'`)
		p := paint{kind: paintURL, url: strings.TrimPrefix(id, "#")}
		if rest := strings.TrimSpace(s[end+1:]); rest != "" {
			f, ok := parsePaint(rest)
			if !ok || f.kind != paintNone && f.kind != paintColor && f.kind != paintCurrent {
				// A fallback that is not a colour or none (a colour with an
				// icc-color): the whole value is invalid (as Chrome and
				// resvg).
				return paint{}, false
			}
			p.fallback = &f
		}
		return p, true
	}
	if c, ok := csscolor.Parse(s); ok {
		return paint{kind: paintColor, c: c}, true
	}
	return paint{}, false
}

// Line caps and joins.
const (
	capButt = iota
	capRound
	capSquare
	joinMiter = iota - 3
	joinRound
	joinBevel
)

// style is the computed style of an element.
type style struct {
	fill, stroke               paint
	fillOpacity, strokeOpacity float64
	evenOdd                    bool
	clipEvenOdd                bool // clip-rule
	blend                      int  // mix-blend-mode
	isolate                    bool // isolation: isolate
	strokeWidth                float64
	lineCap, lineJoin          int
	miterLimit                 float64
	dash                       []float64
	dashOffset                 float64
	color                      color.NRGBA
	visible                    bool
	fontSize                   float64
	strokeFirst                bool // paint-order: the stroke before the fill
	markersEarly               int8 // and the markers: 0 after both, 1 between, 2 before both
	// Of text: font-family, -weight and -style, text-anchor,
	// letter-spacing, word-spacing and dominant-baseline.
	fontFamily       string
	bold, italic     bool
	weight           int  // font-weight, 1 to 1000; 0 is 400
	whitePre         bool // white-space keeps spaces and line feeds (of HTML in a foreignObject)
	noWrap           bool // and does not wrap lines to fit (nowrap, pre)
	anchor           int
	letterSp, wordSp float64
	baseline         int
	rtl              bool // direction: rtl
	vertical         bool // writing-mode: a vertical line (tb, tb-rl, vertical-rl, vertical-lr)
	orient           int  // text-orientation in a vertical line: orientMixed, orientUpright, orientSideways
	// CSS custom properties (--name), inherited; shared until an element
	// declares its own.
	vars map[string]string
	// Not inherited:
	opacity          float64
	display          bool
	stopColor        color.NRGBA
	stopOpacity      float64
	nonScalingStroke bool // vector-effect: non-scaling-stroke
}

func initialStyle() style {
	return style{fill: paint{kind: paintColor, c: color.NRGBA{0, 0, 0, 255}}, stroke: paint{kind: paintNone},
		fillOpacity: 1, strokeOpacity: 1, strokeWidth: 1, miterLimit: 4, color: color.NRGBA{0, 0, 0, 255},
		visible: true, fontSize: 16, opacity: 1, display: true, stopColor: color.NRGBA{0, 0, 0, 255}, stopOpacity: 1}
}

// parseFloatOr reads a number, or def.
func parseFloatOr(s string, def float64) float64 {
	if v, err := strconv.ParseFloat(strings.TrimSpace(s), 64); err == nil {
		return v
	}
	return def
}

// resolveVars puts in each var(--name[, fallback]) of v the custom
// property's value, or else its fallback; false when one has neither (the
// property is then as if not given).
func resolveVars(v string, vars map[string]string, depth int) (string, bool) {
	if depth > 16 {
		return "", false // custom properties that refer to each other
	}
	var out strings.Builder
	for {
		i := strings.Index(v, "var(")
		if i < 0 {
			out.WriteString(v)
			return out.String(), true
		}
		out.WriteString(v[:i])
		// Its closing parenthesis.
		level, j := 0, i+4
		for ; j < len(v); j++ {
			if v[j] == '(' {
				level++
			} else if v[j] == ')' {
				if level == 0 {
					break
				}
				level--
			}
		}
		if j >= len(v) {
			return "", false
		}
		name, fallback, hasFallback := strings.Cut(v[i+4:j], ",")
		val, ok := vars[strings.TrimSpace(name)]
		if !ok {
			if !hasFallback {
				return "", false
			}
			val = strings.TrimSpace(fallback)
		}
		if strings.Contains(val, "var(") {
			if val, ok = resolveVars(val, vars, depth+1); !ok {
				return "", false
			}
		}
		out.WriteString(val)
		v = v[j+1:]
	}
}

// opacityValue reads a number or a percentage, clamped to 0 to 1.
func opacityValue(s string, def float64) float64 {
	s = strings.TrimSpace(s)
	pct := strings.HasSuffix(s, "%")
	v, err := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
	if err != nil || math.IsNaN(v) {
		return def
	}
	if pct {
		v /= 100
	}
	return math.Max(0, math.Min(1, v))
}

// computeStyle is the style of n given its parent's; vp is the viewport,
// for lengths in percentages.
func computeStyle(n *node, parent style, vp viewport) style {
	s := parent
	s.opacity, s.display, s.stopColor, s.stopOpacity, s.nonScalingStroke = 1, true, color.NRGBA{0, 0, 0, 255}, 1, false
	s.blend, s.isolate = blendNormal, false
	// The custom properties it declares, in the style sheets and its style
	// attribute (which wins).
	var own [][2]string
	for k, d := range n.sheet {
		if strings.HasPrefix(k, "--") {
			own = append(own, [2]string{k, d.value})
		}
	}
	if st, ok := n.attrs["style"]; ok && strings.Contains(st, "--") {
		for _, d := range declarations(st) {
			if strings.HasPrefix(d[0], "--") {
				own = append(own, [2]string{d[0], d[1]})
			}
		}
	}
	if len(own) > 0 {
		vars := make(map[string]string, len(parent.vars)+len(own))
		for k, v := range parent.vars {
			vars[k] = v
		}
		for _, kv := range own {
			vars[kv[0]] = strings.TrimSpace(kv[1])
		}
		s.vars = vars
	}
	raw := func(name string) (string, bool) {
		v, ok := n.prop(name)
		if ok && strings.Contains(v, "var(") {
			v, ok = resolveVars(v, s.vars, 0)
		}
		return v, ok
	}
	get := func(name string) (string, bool) {
		v, ok := raw(name)
		if ok && strings.TrimSpace(v) == "inherit" {
			return "", false
		}
		return v, ok
	}
	// inherit, for a property that is not inherited: the parent's.
	inherit := func(v string) bool { return strings.TrimSpace(v) == "inherit" }
	// The font shorthand: [style] [variant] [weight] size[/line-height]
	// family, its parts as their own properties (which, given, win).
	if v, ok := get("font"); ok {
		fields := strings.Fields(v)
		for i, f := range fields {
			lf := strings.ToLower(f)
			switch lf {
			case "italic", "oblique":
				s.italic = true
				continue
			case "bold", "bolder", "600", "700", "800", "900":
				s.weight = weightValue(lf, parent.weight)
				s.bold = true
				continue
			case "lighter", "100", "200", "300", "400", "500":
				s.weight = weightValue(lf, parent.weight)
				s.bold = false
				continue
			case "normal", "small-caps":
				continue
			}
			size, _, _ := strings.Cut(f, "/")
			if fs, ok := parseLength(size, parent.fontSize, vp, axisDiag); ok && fs > 0 {
				if strings.HasSuffix(size, "%") {
					fs = float64(parseFloatOr(strings.TrimSuffix(size, "%"), 100) / 100 * parent.fontSize)
				}
				s.fontSize = fs
				if i+1 < len(fields) {
					s.fontFamily = strings.Join(fields[i+1:], " ")
				}
			}
			break
		}
	}
	if v, ok := get("font-size"); ok {
		// A percentage or em of the parent's size; the keywords of CSS.
		v = strings.TrimSpace(v)
		f, ok := 0.0, false
		switch v {
		case "xx-small":
			f, ok = 9, true
		case "x-small":
			f, ok = 10, true
		case "small":
			f, ok = 13, true
		case "medium":
			f, ok = 16, true
		case "large":
			f, ok = 18, true
		case "x-large":
			f, ok = 24, true
		case "xx-large":
			f, ok = 32, true
		case "xxx-large":
			f, ok = 48, true
		case "larger":
			f, ok = float64(parent.fontSize*1.2), true
		case "smaller":
			f, ok = parent.fontSize/1.2, true
		default:
			if pct, found := strings.CutSuffix(v, "%"); found {
				if p, err := strconv.ParseFloat(strings.TrimSpace(pct), 64); err == nil {
					f, ok = float64(p/100*parent.fontSize), true
				}
			} else {
				f, ok = parseLength(v, parent.fontSize, vp, axisDiag)
			}
		}
		if ok && f > 0 && f < 1e6 {
			s.fontSize = f
		}
	}
	if v, ok := get("font-family"); ok {
		s.fontFamily = v
	}
	if v, ok := get("font-weight"); ok {
		if w := weightValue(strings.ToLower(strings.TrimSpace(v)), parent.weight); w > 0 {
			s.weight, s.bold = w, w >= 600
		}
	}
	if v, ok := get("font-style"); ok {
		switch strings.TrimSpace(v) {
		case "italic", "oblique":
			s.italic = true
		case "normal":
			s.italic = false
		}
	}
	if v, ok := get("text-anchor"); ok {
		switch strings.TrimSpace(v) {
		case "middle":
			s.anchor = anchorMiddle
		case "end":
			s.anchor = anchorEnd
		case "start":
			s.anchor = anchorStart
		}
	}
	if v, ok := get("letter-spacing"); ok {
		if f, ok := spacing(v, s, vp); ok {
			s.letterSp = f
		}
	}
	if v, ok := get("word-spacing"); ok {
		if f, ok := spacing(v, s, vp); ok {
			s.wordSp = f
		}
	}
	if v, ok := get("dominant-baseline"); ok {
		if bl, ok := baselineOf(v); ok {
			s.baseline = bl
		}
	}
	if v, ok := get("writing-mode"); ok {
		switch strings.TrimSpace(v) {
		case "tb", "tb-rl", "vertical-rl", "vertical-lr":
			s.vertical = true
		case "lr", "lr-tb", "rl", "rl-tb", "horizontal-tb":
			s.vertical = false
		}
	}
	if v, ok := get("text-orientation"); ok {
		switch strings.TrimSpace(v) {
		case "mixed":
			s.orient = orientMixed
		case "upright":
			s.orient = orientUpright
		case "sideways", "sideways-right":
			s.orient = orientSideways
		}
	}
	if v, ok := get("direction"); ok {
		switch strings.TrimSpace(v) {
		case "rtl":
			s.rtl = true
		case "ltr":
			s.rtl = false
		}
	}
	if v, ok := get("color"); ok {
		if c, ok := csscolor.Parse(v); ok {
			s.color = c
		}
	}
	if v, ok := get("fill"); ok {
		if p, ok := parsePaint(v); ok {
			s.fill = p
		}
	}
	if v, ok := get("stroke"); ok {
		if p, ok := parsePaint(v); ok {
			s.stroke = p
		}
	}
	if v, ok := get("fill-opacity"); ok {
		s.fillOpacity = opacityValue(v, s.fillOpacity)
	}
	if v, ok := get("stroke-opacity"); ok {
		s.strokeOpacity = opacityValue(v, s.strokeOpacity)
	}
	if v, ok := raw("opacity"); ok {
		if inherit(v) {
			s.opacity = parent.opacity
		} else {
			s.opacity = opacityValue(v, 1)
		}
	}
	if v, ok := get("fill-rule"); ok {
		s.evenOdd = strings.TrimSpace(v) == "evenodd"
	}
	if v, ok := get("clip-rule"); ok {
		s.clipEvenOdd = strings.TrimSpace(v) == "evenodd"
	}
	if v, ok := n.cssProp("mix-blend-mode"); ok {
		if m, ok := parseBlend(v); ok {
			s.blend = m
		}
	}
	if v, ok := n.cssProp("isolation"); ok {
		s.isolate = strings.TrimSpace(v) == "isolate"
	}
	if v, ok := get("stroke-width"); ok {
		if f, ok := parseLength(v, s.fontSize, vp, axisDiag); ok && f >= 0 {
			s.strokeWidth = f
		}
	}
	if v, ok := get("stroke-linecap"); ok {
		switch strings.TrimSpace(v) {
		case "butt":
			s.lineCap = capButt
		case "round":
			s.lineCap = capRound
		case "square":
			s.lineCap = capSquare
		}
	}
	if v, ok := get("paint-order"); ok {
		if order, ok := parsePaintOrder(v); ok {
			s.strokeFirst = order[0] == "stroke" || order[0] == "markers" && order[1] == "stroke"
			s.markersEarly = int8(2 - slices.Index(order[:], "markers"))
		}
	}
	if v, ok := n.prop("vector-effect"); ok {
		s.nonScalingStroke = strings.TrimSpace(v) == "non-scaling-stroke"
	}
	if v, ok := get("stroke-linejoin"); ok {
		switch strings.TrimSpace(v) {
		case "miter", "miter-clip", "arcs":
			s.lineJoin = joinMiter
		case "round":
			s.lineJoin = joinRound
		case "bevel":
			s.lineJoin = joinBevel
		}
	}
	if v, ok := get("stroke-miterlimit"); ok {
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f >= 1 {
			s.miterLimit = f
		}
	}
	if v, ok := get("stroke-dasharray"); ok {
		s.dash = nil
		if t := strings.TrimSpace(v); t != "none" {
			var d []float64
			valid := true
			sum := 0.0
			for _, f := range splitList(t) {
				x, ok := parseLength(f, s.fontSize, vp, axisDiag)
				if !ok || x < 0 {
					valid = false
					break
				}
				d = append(d, x)
				sum += x
			}
			if valid && sum > 0 {
				if len(d)%2 == 1 {
					d = append(d, d...)
				}
				s.dash = d
			}
		}
	}
	if v, ok := get("stroke-dashoffset"); ok {
		if f, ok := parseLength(v, s.fontSize, vp, axisDiag); ok {
			s.dashOffset = f
		}
	}
	if v, ok := get("visibility"); ok {
		t := strings.TrimSpace(v)
		s.visible = t != "hidden" && t != "collapse"
	}
	if v, ok := get("display"); ok {
		s.display = strings.TrimSpace(v) != "none"
	}
	if v, ok := raw("stop-color"); ok {
		if inherit(v) {
			s.stopColor = parent.stopColor
		} else if strings.EqualFold(strings.TrimSpace(v), "currentcolor") {
			s.stopColor = s.color
		} else if c, ok := csscolor.Parse(v); ok {
			s.stopColor = c
		}
	}
	if v, ok := raw("stop-opacity"); ok {
		if inherit(v) {
			s.stopOpacity = parent.stopOpacity
		} else {
			s.stopOpacity = opacityValue(v, 1)
		}
	}
	return s
}

// splitList splits a list of numbers by commas and spaces.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
}

// weightValue is a font-weight: a number from 1 to 1000 or a keyword,
// bolder and lighter by the parent's weight as CSS has them; 0 for none.
func weightValue(v string, parent int) int {
	if parent <= 0 {
		parent = 400
	}
	switch v {
	case "normal":
		return 400
	case "bold":
		return 700
	case "bolder":
		switch {
		case parent < 350:
			return 400
		case parent < 550:
			return 700
		case parent < 900:
			return 900
		}
		return parent
	case "lighter":
		switch {
		case parent < 100:
			return parent
		case parent < 550:
			return 100
		case parent < 750:
			return 400
		}
		return 700
	}
	if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 1 && f <= 1000 {
		return int(f)
	}
	return 0
}
