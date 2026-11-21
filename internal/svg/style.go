package svg

import (
	"image/color"
	"math"
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

// compound is a simple selector: a type, an id and classes.
type compound struct {
	tag     string // "" for any
	id      string
	classes []string
}

func (c compound) matches(n *node) bool {
	if c.tag != "" && c.tag != n.name {
		return false
	}
	if c.id != "" && n.attrs["id"] != c.id {
		return false
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
// (pseudo-classes, attribute selectors), which then matches nothing.
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
		end := strings.IndexAny(src, " >\t\n\r")
		if end < 0 {
			end = len(src)
		}
		tok := src[:end]
		src = src[end:]
		var c compound
		for len(tok) > 0 {
			k := strings.IndexAny(tok[1:], ".#") + 1
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
			case ':', '[':
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
	for _, d := range strings.Split(src, ";") {
		k, v, ok := strings.Cut(d, ":")
		if !ok {
			continue
		}
		k = strings.ToLower(strings.TrimSpace(k))
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
	var css strings.Builder
	var walk func(n *node)
	walk = func(n *node) {
		if n.name == "style" {
			css.WriteString(n.text)
			css.WriteByte('\n')
		}
		for _, c := range n.children {
			walk(c)
		}
	}
	walk(root)
	if css.Len() == 0 {
		return
	}
	src := stripComments(css.String())
	type rule struct {
		sels  []selector
		decls [][3]string
	}
	var rules []rule
	for len(src) > 0 {
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
			continue // media queries, font faces and the like are not used
		}
		var r rule
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

// prop is the specified value of a property of an element: its style
// attribute, then the style sheets, then its presentation attribute (an
// important sheet declaration beats the style attribute).
func (n *node) prop(name string) (string, bool) {
	sheet, hasSheet := n.sheet[name]
	if hasSheet && sheet.important {
		return sheet.value, true
	}
	if st, ok := n.attrs["style"]; ok {
		var found string
		var ok bool
		for _, d := range declarations(st) {
			if d[0] == name {
				found, ok = d[1], true
			}
		}
		if ok {
			return found, true
		}
	}
	if hasSheet {
		return sheet.value, true
	}
	v, ok := n.attrs[name]
	return strings.TrimSpace(v), ok
}

// Kinds of paint.
const (
	paintNone = iota
	paintColor
	paintURL
	paintCurrent
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
	}
	if strings.HasPrefix(s, "url(") {
		end := strings.IndexByte(s, ')')
		if end < 0 {
			return paint{}, false
		}
		id := strings.Trim(strings.TrimSpace(s[4:end]), `"'`)
		p := paint{kind: paintURL, url: strings.TrimPrefix(id, "#")}
		if rest := strings.TrimSpace(s[end+1:]); rest != "" {
			if f, ok := parsePaint(rest); ok {
				p.fallback = &f
			}
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
	strokeWidth                float64
	lineCap, lineJoin          int
	miterLimit                 float64
	dash                       []float64
	dashOffset                 float64
	color                      color.NRGBA
	visible                    bool
	fontSize                   float64
	strokeFirst                bool // paint-order: the stroke before the fill
	// Of text: font-family, -weight and -style, text-anchor,
	// letter-spacing, word-spacing and dominant-baseline.
	fontFamily       string
	bold, italic     bool
	anchor           int
	letterSp, wordSp float64
	baseline         int
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
	get := func(name string) (string, bool) {
		v, ok := n.prop(name)
		if ok && strings.Contains(v, "var(") {
			v, ok = resolveVars(v, s.vars, 0)
		}
		if ok && strings.TrimSpace(v) == "inherit" {
			return "", false
		}
		return v, ok
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
		switch strings.TrimSpace(v) {
		case "bold", "bolder", "600", "700", "800", "900":
			s.bold = true
		case "normal", "lighter", "100", "200", "300", "400", "500":
			s.bold = false
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
	if v, ok := get("opacity"); ok {
		s.opacity = opacityValue(v, 1)
	}
	if v, ok := get("fill-rule"); ok {
		s.evenOdd = strings.TrimSpace(v) == "evenodd"
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
		// The parts named come first, the others after in their usual
		// order (fill, stroke, markers).
		fill, stroke := -1, -1
		for i, f := range strings.Fields(v) {
			switch f {
			case "fill":
				fill = i
			case "stroke":
				stroke = i
			}
		}
		s.strokeFirst = stroke >= 0 && (fill < 0 || stroke < fill)
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
	if v, ok := get("stop-color"); ok {
		if strings.EqualFold(strings.TrimSpace(v), "currentcolor") {
			s.stopColor = s.color
		} else if c, ok := csscolor.Parse(v); ok {
			s.stopColor = c
		}
	}
	if v, ok := get("stop-opacity"); ok {
		s.stopOpacity = opacityValue(v, 1)
	}
	return s
}

// splitList splits a list of numbers by commas and spaces.
func splitList(s string) []string {
	return strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r' })
}
