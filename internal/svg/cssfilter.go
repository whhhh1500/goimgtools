package svg

import (
	"math"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/csscolor"
)

// The filter functions of CSS (filter: blur(2px) grayscale(1)) are drawn
// through a filter element made of the primitives the Filter Effects
// specification gives each, one after the other, a url() among them
// taking its filter's primitives; computed in sRGB, as browsers compute
// these functions, over the element's box and as far around it as the
// functions reach.

// cssMargin is the attribute of such a filter that holds how far its
// functions reach beyond the element's box, in user units.
const cssMargin = "\x00margin"

// cssFrac holds how far, as a fraction of the box, beyond it.
const cssFrac = "\x00frac"

// cssFilter is the filter of a list of filter functions, or nil for a list
// that is not one (no filter, as CSS takes an invalid value).
func (b *builder) cssFilter(n *node, v string, st style, vp viewport) *node {
	if f, ok := b.cssFilters[n]; ok {
		return f
	}
	f := b.makeCSSFilter(v, st, vp)
	if b.cssFilters == nil {
		b.cssFilters = map[*node]*node{}
	}
	b.cssFilters[n] = f
	return f
}

func (b *builder) makeCSSFilter(v string, st style, vp viewport) *node {
	f := &node{name: "filter", attrs: map[string]string{"filterUnits": "userSpaceOnUse", "color-interpolation-filters": "sRGB"}}
	in := "SourceGraphic"
	margin := 0.0
	frac := 0.0 // and as a fraction of the box (a url's filter's region)
	step := 0
	add := func(p *node) {
		p.parent = f
		f.children = append(f.children, p)
	}
	prim := func(name string, attrs map[string]string, kids ...*node) *node {
		p := &node{name: name, attrs: attrs}
		for _, k := range kids {
			k.parent = p
			p.children = append(p.children, k)
		}
		return p
	}
	next := func() string {
		step++
		return "\x00f" + strconv.Itoa(step)
	}
	amount := func(args []string, def float64) (float64, bool) {
		if len(args) == 0 {
			return def, true
		}
		if len(args) > 1 {
			return 0, false
		}
		a := strings.TrimSpace(args[0])
		if p, ok := strings.CutSuffix(a, "%"); ok {
			x, err := strconv.ParseFloat(p, 64)
			return x / 100, err == nil && x >= 0 && !math.IsInf(x, 0)
		}
		x, err := strconv.ParseFloat(a, 64)
		return x, err == nil && x >= 0 && !math.IsInf(x, 0)
	}
	length := func(s string) (float64, bool) {
		s = strings.TrimSpace(s)
		// A number without a unit is pixels (as SVG's attributes take
		// lengths, and Chrome these); a percentage is none.
		if strings.HasSuffix(s, "%") {
			return 0, false
		}
		l, ok := parseLength(s, st.fontSize, vp, axisDiag)
		return l, ok && !math.IsNaN(l) && !math.IsInf(l, 0)
	}
	num := func(x float64) string { return strconv.FormatFloat(x, 'g', -1, 64) }
	transfer := func(out string, funcs ...*node) {
		add(prim("feComponentTransfer", map[string]string{"in": in, "result": out}, funcs...))
	}
	rgb := func(attrs map[string]string) []*node {
		var out []*node
		for _, c := range []string{"feFuncR", "feFuncG", "feFuncB"} {
			a := map[string]string{}
			for k, v := range attrs {
				a[k] = v
			}
			out = append(out, &node{name: c, attrs: a})
		}
		return out
	}
	matrix := func(out string, m [20]float64) {
		vals := make([]string, 20)
		for i, x := range m {
			vals[i] = num(x)
		}
		add(prim("feColorMatrix", map[string]string{"in": in, "type": "matrix", "values": strings.Join(vals, " "), "result": out}))
	}
	s := strings.TrimSpace(v)
	for s != "" {
		open := strings.IndexByte(s, '(')
		if open <= 0 {
			return nil
		}
		name := strings.ToLower(strings.TrimSpace(s[:open]))
		// The arguments, to the parenthesis that closes them (a colour's
		// own parentheses within them).
		depth, end := 0, -1
		for i := open; i < len(s); i++ {
			switch s[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					end = i
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return nil
		}
		inner := strings.TrimSpace(s[open+1 : end])
		s = strings.TrimSpace(s[end+1:])
		// The arguments, apart by spaces; a comma between them makes the
		// list none (as Chrome reads it), one in a colour is the colour's.
		depth = 0
		for i := 0; i < len(inner); i++ {
			switch inner[i] {
			case '(':
				depth++
			case ')':
				depth--
			case ',':
				if depth == 0 {
					return nil
				}
			}
		}
		var args []string
		if inner != "" {
			args = cssTokens(inner)
		}
		out := next()
		switch name {
		case "url":
			id := strings.TrimPrefix(strings.Trim(inner, `"' `), "#")
			t := b.ids[id]
			if t == nil || t.name != "filter" {
				continue // a filter that is not there: none
			}
			t = b.mergedFilter(t)
			if t.attrs["filterUnits"] != "userSpaceOnUse" {
				fr := func(name, def string) float64 {
					v := strings.TrimSpace(attrOr(t, name, def))
					if p, ok := strings.CutSuffix(v, "%"); ok {
						x, _ := strconv.ParseFloat(p, 64)
						return x / 100
					}
					x, _ := strconv.ParseFloat(v, 64)
					return x
				}
				x, y, w, h := fr("x", "-10%"), fr("y", "-10%"), fr("width", "120%"), fr("height", "120%")
				frac = math.Max(frac, math.Max(math.Max(-x, x+w-1), math.Max(-y, y+h-1)))
			}
			// Its primitives, their results renamed apart, the element's
			// picture in them the result so far.
			prefix := out + "_"
			last := in
			first := true
			for _, p := range t.children {
				if !drawnPrimitive(p.name) {
					continue
				}
				cp := cloneRefs(p, prefix, in, first)
				first = false
				if _, ok := cp.attrs["result"]; !ok {
					cp.attrs["result"] = prefix + "\x00last" + strconv.Itoa(len(f.children))
				}
				last = cp.attrs["result"]
				add(cp)
			}
			if last == in {
				continue
			}
			out = last
			if t.attrs["filterUnits"] == "userSpaceOnUse" {
				// Its result is cut to its region, as it is when it is drawn
				// on its own.
				crop := map[string]string{"in": last, "result": out + "_r"}
				for _, a := range []string{"x", "y", "width", "height"} {
					if v, ok := t.attrs[a]; ok {
						crop[a] = v
					}
				}
				add(prim("feOffset", crop))
				out = out + "_r"
			}
		case "blur":
			r := 0.0
			if len(args) > 1 {
				return nil
			}
			if len(args) == 1 {
				var ok bool
				if r, ok = length(args[0]); !ok || r < 0 {
					return nil
				}
			}
			add(prim("feGaussianBlur", map[string]string{"in": in, "stdDeviation": num(r), "result": out}))
			margin += float64(3 * r)
		case "brightness":
			a, ok := amount(args, 1)
			if !ok {
				return nil
			}
			transfer(out, rgb(map[string]string{"type": "linear", "slope": num(a)})...)
		case "contrast":
			a, ok := amount(args, 1)
			if !ok {
				return nil
			}
			transfer(out, rgb(map[string]string{"type": "linear", "slope": num(a), "intercept": num(0.5 - float64(0.5*a))})...)
		case "invert":
			a, ok := amount(args, 1)
			if !ok {
				return nil
			}
			a = math.Min(a, 1)
			transfer(out, rgb(map[string]string{"type": "table", "tableValues": num(a) + " " + num(1-a)})...)
		case "opacity":
			a, ok := amount(args, 1)
			if !ok {
				return nil
			}
			a = math.Min(a, 1)
			transfer(out, &node{name: "feFuncA", attrs: map[string]string{"type": "table", "tableValues": "0 " + num(a)}})
		case "saturate":
			a, ok := amount(args, 1)
			if !ok {
				return nil
			}
			add(prim("feColorMatrix", map[string]string{"in": in, "type": "saturate", "values": num(a), "result": out}))
		case "grayscale":
			a, ok := amount(args, 1)
			if !ok {
				return nil
			}
			k := 1 - math.Min(a, 1)
			matrix(out, [20]float64{
				0.2126 + float64(0.7874*k), 0.7152 - float64(0.7152*k), 0.0722 - float64(0.0722*k), 0, 0,
				0.2126 - float64(0.2126*k), 0.7152 + float64(0.2848*k), 0.0722 - float64(0.0722*k), 0, 0,
				0.2126 - float64(0.2126*k), 0.7152 - float64(0.7152*k), 0.0722 + float64(0.9278*k), 0, 0,
				0, 0, 0, 1, 0})
		case "sepia":
			a, ok := amount(args, 1)
			if !ok {
				return nil
			}
			k := 1 - math.Min(a, 1)
			matrix(out, [20]float64{
				0.393 + float64(0.607*k), 0.769 - float64(0.769*k), 0.189 - float64(0.189*k), 0, 0,
				0.349 - float64(0.349*k), 0.686 + float64(0.314*k), 0.168 - float64(0.168*k), 0, 0,
				0.272 - float64(0.272*k), 0.534 - float64(0.534*k), 0.131 + float64(0.869*k), 0, 0,
				0, 0, 0, 1, 0})
		case "hue-rotate":
			deg := 0.0
			if len(args) > 1 {
				return nil
			}
			if len(args) == 1 {
				// An angle has a unit, but 0 (Chrome takes no other).
				a := strings.ToLower(strings.TrimSpace(args[0]))
				var ok bool
				if deg, ok = cssAngle(a); !ok || a != "0" && strings.TrimLeft(a, "+-.0123456789eE") == "" {
					return nil
				}
			}
			add(prim("feColorMatrix", map[string]string{"in": in, "type": "hueRotate", "values": num(deg), "result": out}))
		case "drop-shadow":
			// A colour, before or after two or three lengths.
			var lens []float64
			col, hasCol, colLast := "", false, false
			for _, a := range args {
				if l, ok := length(a); ok {
					if colLast {
						return nil // a colour between lengths
					}
					lens = append(lens, l)
					continue
				}
				if hasCol {
					return nil
				}
				colLast = len(lens) > 0
				if _, ok := csscolor.Parse(a); !ok && !strings.EqualFold(a, "currentcolor") {
					return nil
				}
				col, hasCol = a, true
			}
			if len(lens) < 2 || len(lens) > 3 || len(lens) == 3 && lens[2] < 0 {
				return nil
			}
			if !hasCol || strings.EqualFold(col, "currentcolor") {
				col = colorString(st.color)
			}
			sd := 0.0 // the third length is the blur's standard deviation (as Chrome)
			if len(lens) == 3 {
				sd = lens[2]
			}
			blur, flood, shadow := out+"_b", out+"_f", out+"_s"
			add(prim("feGaussianBlur", map[string]string{"in": in, "stdDeviation": num(sd), "result": blur}))
			add(prim("feOffset", map[string]string{"in": blur, "dx": num(lens[0]), "dy": num(lens[1]), "result": blur + "o"}))
			add(prim("feFlood", map[string]string{"flood-color": col, "result": flood}))
			add(prim("feComposite", map[string]string{"in": flood, "in2": blur + "o", "operator": "in", "result": shadow}))
			add(prim("feMerge", map[string]string{"result": out},
				&node{name: "feMergeNode", attrs: map[string]string{"in": shadow}},
				&node{name: "feMergeNode", attrs: map[string]string{"in": in}}))
			margin += float64(3*sd) + math.Max(math.Abs(lens[0]), math.Abs(lens[1]))
		default:
			return nil
		}
		in = out
	}
	if len(f.children) == 0 {
		return nil
	}
	f.attrs[cssMargin] = strconv.FormatFloat(margin, 'g', -1, 64)
	f.attrs[cssFrac] = strconv.FormatFloat(frac, 'g', -1, 64)
	return f
}

// cloneRefs copies a primitive (and the nodes in it) with its results and
// inputs renamed by a prefix, SourceGraphic standing for source (and the
// input of the first primitive that names none).
func cloneRefs(p *node, prefix, source string, first bool) *node {
	cp := &node{name: p.name, attrs: make(map[string]string, len(p.attrs)), parent: p.parent, sheet: p.sheet}
	for k, v := range p.attrs {
		cp.attrs[k] = v
	}
	for _, a := range []string{"in", "in2", "result"} {
		v, ok := cp.attrs[a]
		if !ok {
			continue
		}
		switch strings.TrimSpace(v) {
		case "SourceGraphic":
			cp.attrs[a] = source
		case "SourceAlpha", "BackgroundImage", "BackgroundAlpha", "FillPaint", "StrokePaint":
		default:
			cp.attrs[a] = prefix + strings.TrimSpace(v)
		}
	}
	if _, ok := cp.attrs["in"]; !ok && first && a2(p.name) {
		cp.attrs["in"] = source // the first primitive's input: the picture so far
	}
	for _, c := range p.children {
		cc := cloneRefs(c, prefix, source, false)
		cc.parent = cp
		cp.children = append(cp.children, cc)
	}
	return cp
}

// a2 reports whether a primitive takes an input (in).
func a2(name string) bool {
	switch name {
	case "feFlood", "feImage", "feTurbulence", "feMergeNode":
		return false
	}
	return true
}

func colorString(c interface{ RGBA() (r, g, b, a uint32) }) string {
	r, g, bl, a := c.RGBA()
	if a == 0 {
		return "transparent"
	}
	// Unpremultiplied, as CSS writes colours.
	un := func(v uint32) int { return int(math.Round(float64(float64(v) * 255 / float64(a)))) }
	return "rgba(" + strconv.Itoa(un(r)) + "," + strconv.Itoa(un(g)) + "," + strconv.Itoa(un(bl)) + "," + strconv.FormatFloat(float64(a)/65535, 'g', 4, 64) + ")"
}
