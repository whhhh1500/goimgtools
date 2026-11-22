package svg

import (
	"math"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/trig"
)

// Every product below is rounded before it is added (float64(a*b)), so
// that no processor fuses them and the picture is the same on all.

// viewport is the size lengths in percentages are taken from.
type viewport struct{ w, h float64 }

// Axes of a length in percentages.
const (
	axisX = iota
	axisY
	axisDiag
)

// parseLength reads a length in user units (pixels): px, pt, pc, mm, cm,
// in, em, ex, or a percentage of the viewport along axis.
func parseLength(s string, fontSize float64, vp viewport, axis int) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	unit := 1.0
	switch {
	case strings.HasSuffix(s, "%"):
		s = s[:len(s)-1]
		switch axis {
		case axisX:
			unit = vp.w / 100
		case axisY:
			unit = vp.h / 100
		default:
			unit = math.Sqrt(float64(float64(vp.w*vp.w)+float64(vp.h*vp.h))/2) / 100
		}
	case len(s) > 2:
		switch s[len(s)-2:] {
		case "px":
			s = s[:len(s)-2]
		case "pt":
			s, unit = s[:len(s)-2], 4.0/3
		case "pc":
			s, unit = s[:len(s)-2], 16
		case "mm":
			s, unit = s[:len(s)-2], 96/25.4
		case "cm":
			s, unit = s[:len(s)-2], 96/2.54
		case "in":
			s, unit = s[:len(s)-2], 96
		case "em":
			s, unit = s[:len(s)-2], fontSize
		case "ex":
			s, unit = s[:len(s)-2], fontSize/2
		}
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return float64(v * unit), true
}

// matrix is an affine transform: (x, y) to (a x + c y + e, b x + d y + f).
type matrix struct{ a, b, c, d, e, f float64 }

var identity = matrix{1, 0, 0, 1, 0, 0}

// mul is m after n: n's transform applied first.
func (m matrix) mul(n matrix) matrix {
	return matrix{
		a: float64(m.a*n.a) + float64(m.c*n.b),
		b: float64(m.b*n.a) + float64(m.d*n.b),
		c: float64(m.a*n.c) + float64(m.c*n.d),
		d: float64(m.b*n.c) + float64(m.d*n.d),
		e: float64(m.a*n.e) + float64(m.c*n.f) + m.e,
		f: float64(m.b*n.e) + float64(m.d*n.f) + m.f,
	}
}

func (m matrix) apply(x, y float64) (float64, float64) {
	return float64(m.a*x) + float64(m.c*y) + m.e, float64(m.b*x) + float64(m.d*y) + m.f
}

func (m matrix) det() float64 { return float64(m.a*m.d) - float64(m.b*m.c) }

func (m matrix) invert() (matrix, bool) {
	d := m.det()
	if d == 0 || math.IsNaN(d) || math.IsInf(d, 0) {
		return identity, false
	}
	return matrix{
		a: m.d / d, b: -m.b / d, c: -m.c / d, d: m.a / d,
		e: (float64(m.c*m.f) - float64(m.d*m.e)) / d,
		f: (float64(m.b*m.e) - float64(m.a*m.f)) / d,
	}, true
}

// scale is how much the transform enlarges lengths, on average.
func (m matrix) scale() float64 { return math.Sqrt(math.Abs(m.det())) }

// aboutOrigin is transform tm of element n about its transform-origin
// (attribute or CSS), its percentages and keywords taken over a box of w
// by h from the origin of user space (the viewport, by transform-box's
// view-box; a unit square in a bounding box's units); def is the origin
// it has without one, as fractions of the box.
func aboutOrigin(n *node, tm matrix, w, h, fontSize float64, vp viewport, def [2]float64) matrix {
	v, ok := n.cssProp("transform-origin")
	if !ok {
		v, ok = n.attrs["transform-origin"]
	}
	x, y := float64(def[0]*w), float64(def[1]*h)
	if ok {
		if ox, oy, ok := parseOrigin(v, w, h, fontSize, vp); ok {
			x, y = ox, oy
		}
	}
	if x == 0 && y == 0 {
		return tm
	}
	return matrix{1, 0, 0, 1, x, y}.mul(tm).mul(matrix{1, 0, 0, 1, -x, -y})
}

// parseOrigin reads a transform-origin: one to three values (a third,
// along z, is ignored), keywords for either axis in either order.
func parseOrigin(v string, w, h, fontSize float64, vp viewport) (x, y float64, ok bool) {
	f := strings.Fields(strings.ToLower(v))
	if len(f) == 0 || len(f) > 3 {
		return 0, 0, false
	}
	xs, ys := "center", "center"
	if len(f) == 1 {
		if f[0] == "top" || f[0] == "bottom" {
			ys = f[0]
		} else {
			xs = f[0]
		}
	} else {
		xs, ys = f[0], f[1]
		if xs == "top" || xs == "bottom" || ys == "left" || ys == "right" {
			xs, ys = ys, xs
		}
	}
	x, okX := originPart(xs, w, "left", "right", fontSize, vp, axisX)
	y, okY := originPart(ys, h, "top", "bottom", fontSize, vp, axisY)
	return x, y, okX && okY
}

func originPart(s string, size float64, lo, hi string, fontSize float64, vp viewport, axis int) (float64, bool) {
	switch s {
	case lo:
		return 0, true
	case "center":
		return size / 2, true
	case hi:
		return size, true
	}
	if p, ok := strings.CutSuffix(s, "%"); ok {
		f, err := strconv.ParseFloat(p, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return float64(f/100) * size, true
	}
	l, ok := parseLength(s, fontSize, vp, axis)
	return l, ok && !math.IsNaN(l) && !math.IsInf(l, 0)
}

// transformOf is an element's transform: the CSS transform property (from
// its style attribute or the style sheets; "none" is none), or else its
// transform attribute. has is false for none (a value of either that does
// not parse is passed over).
func transformOf(n *node, fontSize float64, vp viewport) (matrix, bool) {
	if v, ok := n.cssProp("transform"); ok {
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			return identity, false
		}
		if m, ok := parseCSSTransform(v, fontSize, vp); ok {
			return m, true
		}
	}
	v, has := n.attrs["transform"]
	if !has {
		return identity, false
	}
	m, ok := parseTransform(v)
	if !ok {
		// One that does not parse (units, as CSS has them, among its
		// numbers) is none: the element is drawn as it is, as in Chrome.
		return identity, false
	}
	return m, true
}

// parseCSSTransform reads a transform of CSS: functions apart by spaces,
// their arguments by commas, lengths and angles with units (a number
// without one is taken as pixels or degrees, as in the attribute); of the
// functions of 3D, what they do in the plane.
func parseCSSTransform(s string, fontSize float64, vp viewport) (matrix, bool) {
	m := identity
	s = strings.TrimSpace(s)
	if s == "" {
		return m, false
	}
	for len(s) > 0 {
		open := strings.IndexByte(s, '(')
		end := strings.IndexByte(s, ')')
		if open < 0 || end < open {
			return m, false
		}
		name := strings.ToLower(strings.TrimSpace(s[:open]))
		var args []string
		for _, a := range strings.Split(s[open+1:end], ",") {
			if a = strings.TrimSpace(a); a != "" {
				args = append(args, a)
			}
		}
		s = strings.TrimSpace(s[end+1:])
		n := len(args)
		length := func(i, axis int) (float64, bool) {
			v, ok := parseLength(args[i], fontSize, vp, axis)
			return v, ok && !math.IsNaN(v) && !math.IsInf(v, 0)
		}
		number := func(i int) (float64, bool) {
			v, err := strconv.ParseFloat(args[i], 64)
			return v, err == nil && !math.IsNaN(v) && !math.IsInf(v, 0)
		}
		angle := func(i int) (float64, bool) { return cssAngle(args[i]) }
		var t matrix
		ok := true
		switch {
		case (name == "translate" && (n == 1 || n == 2)) || (name == "translate3d" && n == 3):
			t = identity
			var okY bool
			t.e, ok = length(0, axisX)
			if n >= 2 {
				t.f, okY = length(1, axisY)
				ok = ok && okY
			}
		case name == "translatex" && n == 1:
			t = identity
			t.e, ok = length(0, axisX)
		case name == "translatey" && n == 1:
			t = identity
			t.f, ok = length(0, axisY)
		case (name == "scale" && (n == 1 || n == 2)) || (name == "scale3d" && n == 3):
			var sx, sy float64
			var okY bool
			sx, ok = number(0)
			sy, okY = sx, true
			if n >= 2 {
				sy, okY = number(1)
			}
			ok = ok && okY
			t = matrix{sx, 0, 0, sy, 0, 0}
		case name == "scalex" && n == 1:
			var sx float64
			sx, ok = number(0)
			t = matrix{sx, 0, 0, 1, 0, 0}
		case name == "scaley" && n == 1:
			var sy float64
			sy, ok = number(0)
			t = matrix{1, 0, 0, sy, 0, 0}
		case (name == "rotate" || name == "rotatez") && n == 1:
			var a float64
			a, ok = angle(0)
			sn, cs := trig.SinCosDeg(a)
			t = matrix{cs, sn, -sn, cs, 0, 0}
		case (name == "skew" && (n == 1 || n == 2)) || name == "skewx" && n == 1 || name == "skewy" && n == 1:
			var ax, ay float64
			okX, okY := true, true
			if name == "skewy" {
				ay, okY = angle(0)
			} else {
				ax, okX = angle(0)
				if n == 2 {
					ay, okY = angle(1)
				}
			}
			ok = okX && okY
			sx, cx := trig.SinCosDeg(ax)
			sy, cy := trig.SinCosDeg(ay)
			t = matrix{1, sy / cy, sx / cx, 1, 0, 0}
		case name == "matrix" && n == 6:
			var v [6]float64
			for i := range v {
				var okI bool
				v[i], okI = number(i)
				ok = ok && okI
			}
			t = matrix{v[0], v[1], v[2], v[3], v[4], v[5]}
		case name == "matrix3d" && n == 16:
			var v [16]float64
			for i := range v {
				var okI bool
				v[i], okI = number(i)
				ok = ok && okI
			}
			t = matrix{v[0], v[1], v[4], v[5], v[12], v[13]}
		default:
			return m, false
		}
		if !ok {
			return m, false
		}
		m = m.mul(t)
	}
	return m, true
}

// cssAngle reads an angle of CSS, in degrees.
func cssAngle(s string) (float64, bool) {
	s = strings.ToLower(strings.TrimSpace(s))
	k := 1.0
	for _, u := range []struct {
		unit string
		k    float64
	}{{"deg", 1}, {"grad", 0.9}, {"rad", 180 / math.Pi}, {"turn", 360}} {
		if v, ok := strings.CutSuffix(s, u.unit); ok {
			s, k = v, u.k
			break
		}
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
		return 0, false
	}
	return float64(v * k), true
}

// parseTransform reads a transform list.
func parseTransform(s string) (matrix, bool) {
	m := identity
	s = strings.TrimSpace(s)
	for len(s) > 0 {
		open := strings.IndexByte(s, '(')
		end := strings.IndexByte(s, ')')
		if open < 0 || end < open {
			return m, false
		}
		name := strings.TrimSpace(strings.Trim(s[:open], " ,\t\r\n"))
		args, ok := numbers(s[open+1 : end])
		if !ok {
			return m, false
		}
		s = strings.TrimLeft(s[end+1:], " ,\t\r\n")
		var t matrix
		switch {
		case name == "matrix" && len(args) == 6:
			t = matrix{args[0], args[1], args[2], args[3], args[4], args[5]}
		case name == "translate" && (len(args) == 1 || len(args) == 2):
			t = identity
			t.e = args[0]
			if len(args) == 2 {
				t.f = args[1]
			}
		case name == "scale" && (len(args) == 1 || len(args) == 2):
			sy := args[0]
			if len(args) == 2 {
				sy = args[1]
			}
			t = matrix{args[0], 0, 0, sy, 0, 0}
		case name == "rotate" && (len(args) == 1 || len(args) == 3):
			sn, cs := trig.SinCosDeg(args[0])
			t = matrix{cs, sn, -sn, cs, 0, 0}
			if len(args) == 3 {
				t = matrix{1, 0, 0, 1, args[1], args[2]}.mul(t).mul(matrix{1, 0, 0, 1, -args[1], -args[2]})
			}
		case name == "skewX" && len(args) == 1:
			sn, cs := trig.SinCosDeg(args[0])
			t = matrix{1, 0, sn / cs, 1, 0, 0}
		case name == "skewY" && len(args) == 1:
			sn, cs := trig.SinCosDeg(args[0])
			t = matrix{1, sn / cs, 0, 1, 0, 0}
		default:
			return m, false
		}
		m = m.mul(t)
	}
	return m, true
}

// numbers reads a list of numbers separated by commas or spaces.
func numbers(s string) ([]float64, bool) {
	var out []float64
	p := numParser{s: s}
	for {
		p.skip()
		if p.i >= len(p.s) {
			return out, true
		}
		v, ok := p.number()
		if !ok {
			return nil, false
		}
		out = append(out, v)
	}
}

// numParser reads numbers the way path data writes them: "1-2.5.5e3".
type numParser struct {
	s string
	i int
}

func (p *numParser) skip() {
	for p.i < len(p.s) {
		switch p.s[p.i] {
		case ' ', '\t', '\n', '\r', ',':
			p.i++
		default:
			return
		}
	}
}

func (p *numParser) number() (float64, bool) {
	start := p.i
	i := p.i
	if i < len(p.s) && (p.s[i] == '+' || p.s[i] == '-') {
		i++
	}
	digits, dot := false, false
	for i < len(p.s) {
		c := p.s[i]
		switch {
		case c >= '0' && c <= '9':
			digits = true
		case c == '.' && !dot:
			dot = true
		default:
			goto exp
		}
		i++
	}
exp:
	if !digits {
		return 0, false
	}
	if i < len(p.s) && (p.s[i] == 'e' || p.s[i] == 'E') {
		j := i + 1
		if j < len(p.s) && (p.s[j] == '+' || p.s[j] == '-') {
			j++
		}
		if j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
			for j < len(p.s) && p.s[j] >= '0' && p.s[j] <= '9' {
				j++
			}
			i = j
		}
	}
	v, err := strconv.ParseFloat(p.s[start:i], 64)
	if err != nil || math.IsInf(v, 0) {
		return 0, false
	}
	p.i = i
	return v, true
}

// flag reads an arc flag, a single 0 or 1 that may stand right before the
// next number.
func (p *numParser) flag() (bool, bool) {
	p.skip()
	if p.i < len(p.s) && (p.s[p.i] == '0' || p.s[p.i] == '1') {
		p.i++
		return p.s[p.i-1] == '1', true
	}
	return false, false
}

// Path commands, in user space, absolute; arcs are turned into cubic
// curves.
const (
	cmdMove = iota
	cmdLine
	cmdCubic
	cmdQuad
	cmdClose
)

type seg struct {
	cmd int
	pts [3][2]float64
}

// maxSegs bounds the commands of one path.
const maxSegs = 1 << 20

// parsePath reads path data, as far as it is valid (the rest is ignored,
// as the specification says).
func parsePath(d string) []seg {
	var out []seg
	p := numParser{s: d}
	var cx, cy, sx, sy float64 // current point, subpath start
	var lastCtrl [2]float64    // the last control point, for S and T
	var lastCmd byte           // the last command letter, upper case
	var cmd byte
	for len(out) < maxSegs {
		p.skip()
		if p.i >= len(p.s) {
			break
		}
		c := p.s[p.i]
		if c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' {
			if len(out) == 0 && cmd == 0 && c != 'M' && c != 'm' {
				break // path data starts with a move, or is none
			}
			cmd = c
			p.i++
		} else if cmd == 0 {
			break
		} else if cmd == 'M' {
			cmd = 'L' // pairs after a move are lines
		} else if cmd == 'm' {
			cmd = 'l'
		}
		rel := cmd >= 'a'
		up := cmd &^ 0x20
		ox, oy := 0.0, 0.0
		if rel {
			ox, oy = cx, cy
		}
		read := func(n int) ([]float64, bool) {
			v := make([]float64, n)
			for i := range v {
				p.skip()
				x, ok := p.number()
				if !ok {
					return nil, false
				}
				v[i] = x
			}
			return v, true
		}
		switch up {
		case 'Z':
			out = append(out, seg{cmd: cmdClose})
			cx, cy = sx, sy
			lastCtrl = [2]float64{cx, cy}
			lastCmd = 'Z'
			cmd = 0
			continue
		case 'M', 'L', 'T':
			v, ok := read(2)
			if !ok {
				return out
			}
			x, y := v[0]+ox, v[1]+oy
			switch up {
			case 'M':
				out = append(out, seg{cmd: cmdMove, pts: [3][2]float64{{x, y}}})
				sx, sy = x, y
				lastCtrl = [2]float64{x, y}
			case 'L':
				out = append(out, seg{cmd: cmdLine, pts: [3][2]float64{{x, y}}})
				lastCtrl = [2]float64{x, y}
			case 'T':
				q := [2]float64{cx, cy}
				if lastCmd == 'Q' || lastCmd == 'T' {
					q = [2]float64{2*cx - lastCtrl[0], 2*cy - lastCtrl[1]}
				}
				out = append(out, seg{cmd: cmdQuad, pts: [3][2]float64{q, {x, y}}})
				lastCtrl = q
			}
			cx, cy = x, y
		case 'H', 'V':
			v, ok := read(1)
			if !ok {
				return out
			}
			if up == 'H' {
				cx = v[0] + ox
			} else {
				cy = v[0] + oy
			}
			out = append(out, seg{cmd: cmdLine, pts: [3][2]float64{{cx, cy}}})
			lastCtrl = [2]float64{cx, cy}
		case 'C', 'S', 'Q':
			n := 6
			if up != 'C' {
				n = 4
			}
			v, ok := read(n)
			if !ok {
				return out
			}
			for i := 0; i < n; i += 2 {
				v[i] += ox
				v[i+1] += oy
			}
			switch up {
			case 'C':
				out = append(out, seg{cmd: cmdCubic, pts: [3][2]float64{{v[0], v[1]}, {v[2], v[3]}, {v[4], v[5]}}})
				lastCtrl = [2]float64{v[2], v[3]}
				cx, cy = v[4], v[5]
			case 'S':
				c1 := [2]float64{cx, cy}
				if lastCmd == 'C' || lastCmd == 'S' {
					c1 = [2]float64{2*cx - lastCtrl[0], 2*cy - lastCtrl[1]}
				}
				out = append(out, seg{cmd: cmdCubic, pts: [3][2]float64{c1, {v[0], v[1]}, {v[2], v[3]}}})
				lastCtrl = [2]float64{v[0], v[1]}
				cx, cy = v[2], v[3]
			case 'Q':
				out = append(out, seg{cmd: cmdQuad, pts: [3][2]float64{{v[0], v[1]}, {v[2], v[3]}}})
				lastCtrl = [2]float64{v[0], v[1]}
				cx, cy = v[2], v[3]
			}
		case 'A':
			v, ok := read(3)
			if !ok {
				return out
			}
			large, ok1 := p.flag()
			sweep, ok2 := p.flag()
			end, ok3 := read(2)
			if !ok1 || !ok2 || !ok3 {
				return out
			}
			x, y := end[0]+ox, end[1]+oy
			out = arcToCubics(out, cx, cy, v[0], v[1], v[2], large, sweep, x, y)
			cx, cy = x, y
			lastCtrl = [2]float64{cx, cy}
		default:
			return out
		}
		lastCmd = up
	}
	return out
}

// arcToCubics appends the elliptical arc from (x1, y1) to (x2, y2) as
// cubic curves (SVG's endpoint parameterization, implementation notes
// F.6), at most a quarter turn each.
func arcToCubics(out []seg, x1, y1, rx, ry, angle float64, large, sweep bool, x2, y2 float64) []seg {
	if x1 == x2 && y1 == y2 {
		return out
	}
	rx, ry = math.Abs(rx), math.Abs(ry)
	if rx == 0 || ry == 0 {
		return append(out, seg{cmd: cmdLine, pts: [3][2]float64{{x2, y2}}})
	}
	sn, cs := trig.SinCosDeg(angle)
	dx, dy := (x1-x2)/2, (y1-y2)/2
	x1p := float64(cs*dx) + float64(sn*dy)
	y1p := float64(-sn*dx) + float64(cs*dy)
	// Radii too small are scaled up.
	l := float64(float64(x1p*x1p)/float64(rx*rx)) + float64(float64(y1p*y1p)/float64(ry*ry))
	if l > 1 {
		k := math.Sqrt(l)
		rx, ry = float64(rx*k), float64(ry*k)
	}
	rx2, ry2 := float64(rx*rx), float64(ry*ry)
	num := float64(rx2*ry2) - float64(rx2*float64(y1p*y1p)) - float64(ry2*float64(x1p*x1p))
	den := float64(rx2*float64(y1p*y1p)) + float64(ry2*float64(x1p*x1p))
	coef := 0.0
	if den > 0 && num > 0 {
		coef = math.Sqrt(num / den)
	}
	if large == sweep {
		coef = -coef
	}
	cxp := float64(coef * float64(rx*y1p) / ry)
	cyp := float64(-coef * float64(ry*x1p) / rx)
	cx := float64(cs*cxp) - float64(sn*cyp) + float64((x1+x2)/2)
	cy := float64(sn*cxp) + float64(cs*cyp) + float64((y1+y2)/2)
	t1 := trig.Atan2((y1p-cyp)/ry, (x1p-cxp)/rx)
	t2 := trig.Atan2((-y1p-cyp)/ry, (-x1p-cxp)/rx)
	dt := t2 - t1
	if sweep && dt < 0 {
		dt += 2 * math.Pi
	} else if !sweep && dt > 0 {
		dt -= 2 * math.Pi
	}
	n := int(math.Ceil(math.Abs(dt)/(math.Pi/2) - 1e-9))
	n = max(n, 1)
	step := dt / float64(n)
	// The control points of an arc of step: 4/3 tan(step/4).
	ss, sc := trig.SinCos(step / 4)
	k := float64(4.0/3) * ss / sc
	at := func(t float64) (x, y, dxdt, dydt float64) {
		st, ct := trig.SinCos(t)
		ex, ey := float64(rx*ct), float64(ry*st)
		x = float64(cs*ex) - float64(sn*ey) + cx
		y = float64(sn*ex) + float64(cs*ey) + cy
		ex, ey = float64(-rx*st), float64(ry*ct)
		return x, y, float64(cs*ex) - float64(sn*ey), float64(sn*ex) + float64(cs*ey)
	}
	px, py, pdx, pdy := at(t1)
	for i := 1; i <= n; i++ {
		t := t1 + float64(step*float64(i))
		qx, qy, qdx, qdy := at(t)
		if i == n {
			qx, qy = x2, y2
		}
		out = append(out, seg{cmd: cmdCubic, pts: [3][2]float64{
			{px + float64(k*pdx), py + float64(k*pdy)},
			{qx - float64(k*qdx), qy - float64(k*qdy)},
			{qx, qy}}})
		px, py, pdx, pdy = qx, qy, qdx, qdy
	}
	return out
}
