package svg

import (
	"image/color"
	"math"
	"strconv"
	"strings"
)

// shader gives the colour of a paint at a pixel, premultiplied.
type shader struct {
	solid   [4]uint8
	grad    *gradient
	img     *sampler
	tile    *tileSampler
	opacity float64 // of a picture's or a pattern's pixels; 0 is 1
}

const lutSize = 1024

type gradient struct {
	radial bool
	inv    matrix // device to gradient space
	// Linear: from (x1, y1) along (dx, dy), t = dot / len2.
	x1, y1, dx, dy, len2 float64
	// Radial: the focal circle to the end circle.
	cx, cy, r, fx, fy, fr float64
	spread                int
	lut                   [lutSize][4]uint8
}

const (
	spreadPad = iota
	spreadReflect
	spreadRepeat
)

func solidShader(c color.NRGBA, opacity float64) *shader {
	a := float64(float64(c.A) * opacity)
	if a <= 0 {
		return nil
	}
	return &shader{solid: premul(float64(c.R), float64(c.G), float64(c.B), a)}
}

// premul is a colour on 0 to 255 with alpha a, premultiplied and rounded.
func premul(r, g, b, a float64) [4]uint8 {
	k := a / 255
	return [4]uint8{uint8(math.Round(float64(r * k))), uint8(math.Round(float64(g * k))), uint8(math.Round(float64(b * k))), uint8(math.Round(a))}
}

// faded is the shader with every colour more transparent by o.
func (s *shader) faded(o float64) *shader {
	f := func(c [4]uint8) [4]uint8 {
		var out [4]uint8
		for i, v := range c {
			out[i] = uint8(math.Round(float64(float64(v) * o)))
		}
		return out
	}
	t := *s
	t.solid = f(s.solid)
	if s.img != nil || s.tile != nil {
		t.opacity = o
		if s.opacity > 0 {
			t.opacity = float64(s.opacity * o)
		}
	}
	if s.grad != nil {
		g := *s.grad
		for i := range g.lut {
			g.lut[i] = f(g.lut[i])
		}
		t.grad = &g
	}
	return &t
}

// at is the colour at the centre of device pixel (x, y).
func (s *shader) at(x, y int) [4]uint8 {
	switch {
	case s.img != nil || s.tile != nil:
		var c [4]uint8
		if s.img != nil {
			c = s.img.at(float64(x)+0.5, float64(y)+0.5)
		} else {
			c = s.tile.at(float64(x)+0.5, float64(y)+0.5)
		}
		if s.opacity > 0 && s.opacity < 1 {
			k := uint32(float64(s.opacity*255) + 0.5)
			for i := range c {
				c[i] = uint8(mul255(uint32(c[i]), k))
			}
		}
		return c
	case s.grad != nil:
		return s.grad.at(float64(x)+0.5, float64(y)+0.5)
	}
	return s.solid
}

func (g *gradient) at(px, py float64) [4]uint8 {
	x, y := g.inv.apply(px, py)
	var t float64
	if !g.radial {
		t = (float64((x-g.x1)*g.dx) + float64((y-g.y1)*g.dy)) / g.len2
	} else {
		dx, dy, dr := g.cx-g.fx, g.cy-g.fy, g.r-g.fr
		px, py := x-g.fx, y-g.fy
		a := float64(dx*dx) + float64(dy*dy) - float64(dr*dr)
		b := float64(px*dx) + float64(py*dy) + float64(g.fr*dr)
		c := float64(px*px) + float64(py*py) - float64(g.fr*g.fr)
		if dx == 0 && dy == 0 && math.Abs(dr) <= float64(g.r/(1<<15)) {
			// The two circles one: the first colour within, the last
			// without (as Skia draws it for Chrome).
			if c < 0 {
				return g.lut[0]
			}
			return g.lut[lutSize-1]
		}
		if math.Abs(a) < 1e-12 {
			if b == 0 {
				return [4]uint8{}
			}
			t = c / (2 * b)
		} else {
			disc := float64(b*b) - float64(a*c)
			if disc < 0 {
				return [4]uint8{}
			}
			sq := math.Sqrt(disc)
			t1, t2 := (b+sq)/a, (b-sq)/a
			t = math.Max(t1, t2)
			if g.fr+float64(t*dr) < 0 {
				t = math.Min(t1, t2)
				if g.fr+float64(t*dr) < 0 {
					return [4]uint8{}
				}
			}
		}
	}
	switch g.spread {
	case spreadRepeat:
		t -= math.Floor(t)
	case spreadReflect:
		t = math.Mod(math.Abs(t), 2)
		if t > 1 {
			t = 2 - t
		}
	}
	if !(t >= 0) {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return g.lut[int(math.Round(float64(t*(lutSize-1))))]
}

// paintContext is the element whose fill and stroke context-fill and
// context-stroke take (SVG 2): the shape a marker is on, a use element;
// its paints, resolved, painted in its own space over its own bounding
// box.
type paintContext struct {
	fill, stroke contextPaint
}

type contextPaint struct {
	p    paint
	n    *node
	st   style
	segs func() []seg // its outline (a use element's: its bounding box), made when a paint server needs it
	m    matrix
	vp   viewport
}

// withContext draws with n as the context element.
func (b *builder) withContext(n *node, st style, segs func() []seg, m matrix, vp viewport, draw func()) {
	outer := b.ctx
	c := &paintContext{
		fill:   contextPaint{st.fill, n, st, segs, m, vp},
		stroke: contextPaint{st.stroke, n, st, segs, m, vp},
	}
	// Its own paints of the context of an outer element are that
	// element's, as it paints them (none without one).
	for _, cp := range []*contextPaint{&c.fill, &c.stroke} {
		switch {
		case cp.p.kind != paintContextFill && cp.p.kind != paintContextStroke:
		case outer == nil:
			cp.p = paint{kind: paintNone}
		case cp.p.kind == paintContextFill:
			*cp = outer.fill
		default:
			*cp = outer.stroke
		}
	}
	b.ctx = c
	defer func() { b.ctx = outer }()
	draw()
}

// shader resolves a fill or a stroke of an element into what colours its
// pixels; nil for none.
func (b *builder) shader(n *node, p paint, opacity float64, st style, segs []seg, m matrix, vp viewport) *shader {
	switch p.kind {
	case paintNone:
		return nil
	case paintContextFill, paintContextStroke:
		c := b.ctx
		if c == nil {
			return nil
		}
		q := c.fill
		if p.kind == paintContextStroke {
			q = c.stroke
		}
		if q.p.kind == paintContextFill || q.p.kind == paintContextStroke {
			return nil
		}
		b.ctx = nil // its paint servers are not in a context of their own
		defer func() { b.ctx = c }()
		var segs []seg
		if q.p.kind == paintURL {
			segs = q.segs()
		}
		return b.shader(q.n, q.p, opacity, q.st, segs, q.m, q.vp)
	case paintCurrent:
		return solidShader(st.color, opacity)
	case paintColor:
		return solidShader(p.c, opacity)
	}
	t := b.ids[p.url]
	if t != nil && t.name == "pattern" {
		if sh := b.pattern(t, opacity, st, segs, m, vp); sh != nil || b.err != nil {
			return sh
		}
		if p.fallback != nil && b.boxless(t, "patternUnits", segs) {
			return b.shader(n, *p.fallback, opacity, st, segs, m, vp)
		}
		return nil
	}
	if t == nil || t.name != "linearGradient" && t.name != "radialGradient" {
		if p.fallback != nil {
			return b.shader(n, *p.fallback, opacity, st, segs, m, vp)
		}
		return nil
	}
	sh := b.gradient(t, opacity, st, segs, m, vp)
	if sh == nil && p.fallback != nil && (!b.gradientHasStops(t) || b.boxless(t, "gradientUnits", segs)) {
		// A gradient without stops, or of a bounding box's units on a
		// shape whose box is flat (a straight line): the fallback, as
		// Chrome paints it.
		return b.shader(n, *p.fallback, opacity, st, segs, m, vp)
	}
	return sh
}

// boxless reports whether a paint server is in a bounding box's units (by
// attr, of it or the servers it refers to) and the shape's box has no
// width or no height.
func (b *builder) boxless(t *node, attr string, segs []seg) bool {
	for k, i := t, 0; k != nil && i < 8; k, i = b.href(k), i+1 {
		if v, ok := k.attrs[attr]; ok {
			if strings.TrimSpace(v) == "userSpaceOnUse" {
				return false
			}
			break
		}
	}
	bx, ok := segsBBox(segs)
	return !ok || bx[2] <= 0 || bx[3] <= 0
}

// gradientHasStops reports whether a gradient, or one it refers to, has a
// stop.
func (b *builder) gradientHasStops(g *node) bool {
	for k, i := g, 0; k != nil && i < 8; k, i = b.href(k), i+1 {
		if k.name != "linearGradient" && k.name != "radialGradient" {
			return false
		}
		for _, c := range k.children {
			if c.name == "stop" {
				return true
			}
		}
	}
	return false
}

// chainTransform is the first transform of the paint servers of a chain
// (one and those it refers to) that is one: a value that does not parse is
// passed over, as is one that flattens everything (as resvg; Chrome draws
// a gradient of such a transform as nothing, which resvg's suite leaves
// undefined).
func chainTransform(chain []*node, name string) (matrix, bool) {
	for _, c := range chain {
		if v, ok := c.attrs[name]; ok {
			if tm, ok := parseTransform(v); ok {
				return tm, tm.det() != 0
			}
		}
	}
	return identity, false
}

// gradient builds the shader of a gradient element for a shape.
func (b *builder) gradient(g *node, opacity float64, st style, segs []seg, m matrix, vp viewport) *shader {
	// Attributes and stops come from the gradients it refers to when it
	// has none of its own.
	chain := []*node{g}
	for k := g; len(chain) < 8; {
		next := b.href(k)
		if next == nil || next.name != "linearGradient" && next.name != "radialGradient" {
			break
		}
		cycle := false
		for _, c := range chain {
			cycle = cycle || c == next
		}
		if cycle {
			break
		}
		chain = append(chain, next)
		k = next
	}
	attr := func(name string) (string, bool) {
		for _, c := range chain {
			// The geometry of the other kind of gradient is not its own.
			switch name {
			case "x1", "y1", "x2", "y2":
				if c.name != "linearGradient" {
					continue
				}
			case "cx", "cy", "r", "fx", "fy", "fr":
				if c.name != "radialGradient" {
					continue
				}
			}
			if v, ok := c.attrs[name]; ok {
				return v, true
			}
		}
		return "", false
	}
	var stops []*node
	var stopsOf *node
	for _, c := range chain {
		for _, k := range c.children {
			if k.name == "stop" {
				stops = append(stops, k)
			}
		}
		if len(stops) > 0 {
			stopsOf = c
			break
		}
	}
	if len(stops) == 0 {
		return nil
	}
	type stopVal struct {
		off float64
		c   color.NRGBA
		a   float64
	}
	var sv []stopVal
	// The stops' style is that of where the gradient is (its color for
	// currentColor among them), not of what it paints.
	gst := b.styleOf(stopsOf, vp)
	last := 0.0
	for _, k := range stops {
		ks := computeStyle(k, gst, vp)
		off := 0.0
		if v, ok := k.attrs["offset"]; ok {
			off = opacityValue(v, 0)
		}
		off = math.Max(off, last)
		last = off
		sv = append(sv, stopVal{off, ks.stopColor, float64(float64(ks.stopColor.A)/255) * ks.stopOpacity})
	}
	if len(sv) == 1 {
		c := sv[0].c
		c.A = 255
		return solidShader(c, float64(sv[0].a*opacity))
	}
	gr := &gradient{radial: g.name == "radialGradient"}
	if v, ok := attr("spreadMethod"); ok {
		switch strings.TrimSpace(v) {
		case "reflect":
			gr.spread = spreadReflect
		case "repeat":
			gr.spread = spreadRepeat
		}
	}
	bboxUnits := true
	if v, ok := attr("gradientUnits"); ok && strings.TrimSpace(v) == "userSpaceOnUse" {
		bboxUnits = false
	}
	gm := m
	if bboxUnits {
		bx, ok := segsBBox(segs)
		if !ok || bx[2] <= 0 || bx[3] <= 0 {
			return nil
		}
		gm = gm.mul(matrix{bx[2], 0, 0, bx[3], bx[0], bx[1]})
	}
	if tm, ok := chainTransform(chain, "gradientTransform"); ok {
		bw, bh := vp.w, vp.h
		if bboxUnits {
			bw, bh = 1, 1
		}
		gm = gm.mul(aboutOrigin(g, tm, bw, bh, st.fontSize, vp, [2]float64{}))
	}
	inv, ok := gm.invert()
	if !ok {
		return nil
	}
	gr.inv = inv
	coord := func(name, def string, axis int) float64 {
		v, ok := attr(name)
		if _, valid := parseLength(strings.TrimSuffix(strings.TrimSpace(v), "%"), 0, vp, axis); !ok || !valid {
			v = def // not a length: the default, not the referenced one's (as Chrome)
		}
		if bboxUnits {
			v = strings.TrimSpace(v)
			if strings.HasSuffix(v, "%") {
				f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
				if err != nil {
					return 0
				}
				return f / 100
			}
			f, _ := parseLength(v, st.fontSize, viewport{1, 1}, axis)
			return f
		}
		f, _ := parseLength(v, st.fontSize, vp, axis)
		return f
	}
	if !gr.radial {
		gr.x1, gr.y1 = coord("x1", "0%", axisX), coord("y1", "0%", axisY)
		x2, y2 := coord("x2", "100%", axisX), coord("y2", "0%", axisY)
		gr.dx, gr.dy = x2-gr.x1, y2-gr.y1
		gr.len2 = float64(gr.dx*gr.dx) + float64(gr.dy*gr.dy)
		if gr.len2 == 0 {
			// The last stop's colour all over.
			s := sv[len(sv)-1]
			c := s.c
			c.A = 255
			return solidShader(c, float64(s.a*opacity))
		}
	} else {
		gr.cx, gr.cy, gr.r = coord("cx", "50%", axisX), coord("cy", "50%", axisY), coord("r", "50%", axisDiag)
		gr.fx, gr.fy = gr.cx, gr.cy
		if _, ok := attr("fx"); ok {
			gr.fx = coord("fx", "50%", axisX)
		}
		if _, ok := attr("fy"); ok {
			gr.fy = coord("fy", "50%", axisY)
		}
		gr.fr = math.Max(0, coord("fr", "0%", axisDiag))
		if !(gr.r > 0) {
			s := sv[len(sv)-1]
			c := s.c
			c.A = 255
			return solidShader(c, float64(s.a*opacity))
		}
		// A focal point outside the circle stays there, the gradient a cone
		// from the focal circle to the end circle, not drawn outside it
		// (SVG 2, as Chrome; SVG 1.1 brought the point inside).
	}
	// The colours along the gradient, interpolated between stops
	// unpremultiplied (in linear light by color-interpolation, inherited
	// where the gradient is), then premultiplied.
	linear := false
	for p := g; p != nil; p = p.parent {
		if v, ok := p.prop("color-interpolation"); ok && strings.TrimSpace(v) != "inherit" {
			linear = strings.EqualFold(strings.TrimSpace(v), "linearRGB")
			break
		}
	}
	k := 0
	for i := range gr.lut {
		t := float64(i) / (lutSize - 1)
		for k+1 < len(sv) && sv[k+1].off < t {
			k++
		}
		var r, g2, bl, a float64
		switch {
		case t <= sv[0].off:
			s := sv[0]
			r, g2, bl, a = float64(s.c.R), float64(s.c.G), float64(s.c.B), s.a
		case k+1 >= len(sv):
			s := sv[len(sv)-1]
			r, g2, bl, a = float64(s.c.R), float64(s.c.G), float64(s.c.B), s.a
		default:
			s0, s1 := sv[k], sv[k+1]
			f := 0.0
			if s1.off > s0.off {
				f = (t - s0.off) / (s1.off - s0.off)
			}
			lerp := func(u, v float64) float64 { return u + float64((v-u)*f) }
			if linear {
				// In linear light, back in the sRGB curve.
				ch := func(u, v uint8) float64 {
					return float64(fromLinear(float32(lerp(float64(toLinear[u]), float64(toLinear[v])))) * 255)
				}
				r, g2, bl = ch(s0.c.R, s1.c.R), ch(s0.c.G, s1.c.G), ch(s0.c.B, s1.c.B)
			} else {
				r, g2, bl = lerp(float64(s0.c.R), float64(s1.c.R)), lerp(float64(s0.c.G), float64(s1.c.G)), lerp(float64(s0.c.B), float64(s1.c.B))
			}
			a = lerp(s0.a, s1.a)
		}
		gr.lut[i] = premul(r, g2, bl, float64(float64(a*opacity)*255))
	}
	return &shader{grad: gr}
}
