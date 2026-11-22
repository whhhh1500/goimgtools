package svg

import (
	"math"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/trig"
)

// vertex is a point of a path where a marker goes, and the directions of
// the path coming in and going out (angles in degrees).
type vertex struct {
	x, y    float64
	in, out float64
	hasIn   bool
	hasOut  bool
	start   bool // the first vertex
	end     bool // the last
}

func angleOf(dx, dy float64) float64 {
	return float64(trig.Atan2(dy, dx) * 180 / math.Pi)
}

// vertices lists the vertices of a path with their directions.
func vertices(segs []seg) []vertex {
	var out []vertex
	var cx, cy, sx, sy float64
	startIdx := -1
	// The direction a segment leaves its start and reaches its end.
	dirs := func(s seg, x0, y0 float64) (float64, float64, float64, float64) {
		var pts [][2]float64
		switch s.cmd {
		case cmdLine:
			pts = [][2]float64{{x0, y0}, s.pts[0]}
		case cmdQuad:
			pts = [][2]float64{{x0, y0}, s.pts[0], s.pts[1]}
		case cmdCubic:
			pts = [][2]float64{{x0, y0}, s.pts[0], s.pts[1], s.pts[2]}
		}
		// The first control point unlike the start, the last unlike the end.
		ax, ay, bx, by := 0.0, 0.0, 0.0, 0.0
		for i := 1; i < len(pts); i++ {
			if pts[i] != pts[0] {
				ax, ay = pts[i][0]-pts[0][0], pts[i][1]-pts[0][1]
				break
			}
		}
		last := pts[len(pts)-1]
		for i := len(pts) - 2; i >= 0; i-- {
			if pts[i] != last {
				bx, by = last[0]-pts[i][0], last[1]-pts[i][1]
				break
			}
		}
		return ax, ay, bx, by
	}
	for _, s := range segs {
		switch s.cmd {
		case cmdMove:
			cx, cy = s.pts[0][0], s.pts[0][1]
			sx, sy = cx, cy
			out = append(out, vertex{x: cx, y: cy})
			startIdx = len(out) - 1
		case cmdLine, cmdQuad, cmdCubic:
			if len(out) == 0 {
				out = append(out, vertex{x: cx, y: cy})
				startIdx = 0
			}
			ax, ay, bx, by := dirs(s, cx, cy)
			v := &out[len(out)-1]
			v.out, v.hasOut = angleOf(ax, ay), ax != 0 || ay != 0
			end := s.pts[0]
			switch s.cmd {
			case cmdQuad:
				end = s.pts[1]
			case cmdCubic:
				end = s.pts[2]
			}
			cx, cy = end[0], end[1]
			out = append(out, vertex{x: cx, y: cy, in: angleOf(bx, by), hasIn: bx != 0 || by != 0})
		case cmdClose:
			if len(out) == 0 || startIdx < 0 {
				continue
			}
			// The closing line, then the subpath's start joins its end.
			if cx != sx || cy != sy {
				v := &out[len(out)-1]
				v.out, v.hasOut = angleOf(sx-cx, sy-cy), true
				out = append(out, vertex{x: sx, y: sy, in: angleOf(sx-cx, sy-cy), hasIn: true})
			}
			last := &out[len(out)-1]
			first := &out[startIdx]
			if first.hasOut {
				last.out, last.hasOut = first.out, true
			}
			if last.hasIn {
				first.in, first.hasIn = last.in, true
			}
			cx, cy = sx, sy
		}
	}
	if len(out) > 0 {
		out[0].start = true
		out[len(out)-1].end = true
	}
	return out
}

// bisect is the angle halfway between in and out, the shorter way.
func bisect(in, out float64) float64 {
	d := out - in
	for d > 180 {
		d -= 360
	}
	for d < -180 {
		d += 360
	}
	return in + float64(d/2)
}

// markers draws the markers of a path's start, middle and end vertices.
func (b *builder) markers(n *node, segs []seg, m matrix, st style, vp viewport, cl *clip) {
	ref := func(prop string) *node {
		v, ok := n.prop(prop)
		if !ok {
			if v, ok = n.prop("marker"); !ok {
				return nil
			}
		}
		v = strings.TrimSpace(v)
		if !strings.HasPrefix(v, "url(") {
			return nil
		}
		end := strings.IndexByte(v, ')')
		if end < 0 {
			return nil
		}
		t := b.ids[strings.TrimPrefix(strings.Trim(strings.TrimSpace(v[4:end]), `"'`), "#")]
		if t == nil || t.name != "marker" {
			return nil
		}
		return t
	}
	start, mid, end := ref("marker-start"), ref("marker-mid"), ref("marker-end")
	if start == nil && mid == nil && end == nil {
		return
	}
	// The shape is the context of the paints of its markers.
	if !b.opts.contextPaint {
		b.drawMarkers(segs, m, st, vp, cl, start, mid, end)
		return
	}
	b.withContext(n, st, func() []seg { return segs }, m, vp, func() { b.drawMarkers(segs, m, st, vp, cl, start, mid, end) })
}

func (b *builder) drawMarkers(segs []seg, m matrix, st style, vp viewport, cl *clip, start, mid, end *node) {
	vs := vertices(segs)
	for i, v := range vs {
		var mk *node
		switch {
		case v.start:
			mk = start
		case v.end:
			mk = end
		default:
			mk = mid
		}
		if i == len(vs)-1 && !v.start {
			mk = end
		}
		if mk != nil && !b.marking[mk] {
			b.marker(mk, v, m, st, vp, cl)
		}
		if b.err != nil {
			return
		}
	}
}

// marker draws one marker at a vertex.
func (b *builder) marker(mk *node, v vertex, m matrix, st style, vp viewport, cl *clip) {
	b.marking[mk] = true
	defer delete(b.marking, mk)
	angle := 0.0
	switch o := strings.TrimSpace(mk.attrs["orient"]); o {
	case "auto", "auto-start-reverse":
		switch {
		case v.hasIn && v.hasOut:
			angle = bisect(v.in, v.out)
		case v.hasOut:
			angle = v.out
		case v.hasIn:
			angle = v.in
		}
		if o == "auto-start-reverse" && v.start {
			angle += 180
		}
	default:
		if a, ok := cssAngle(o); ok { // in degrees, or with a unit of CSS
			angle = a
		}
	}
	mw, okW := parseLength(attrOr(mk, "markerWidth", "3"), st.fontSize, vp, axisX)
	mh, okH := parseLength(attrOr(mk, "markerHeight", "3"), st.fontSize, vp, axisY)
	if !okW || !okH || mw <= 0 || mh <= 0 {
		return
	}
	sn, cs := trig.SinCosDeg(angle)
	mm := m.mul(matrix{cs, sn, -sn, cs, v.x, v.y})
	if mk.attrs["markerUnits"] != "userSpaceOnUse" {
		mm = mm.mul(matrix{st.strokeWidth, 0, 0, st.strokeWidth, 0, 0})
	}
	refX, _ := parseLength(mk.attrs["refX"], st.fontSize, vp, axisX)
	refY, _ := parseLength(mk.attrs["refY"], st.fontSize, vp, axisY)
	inner := viewport{mw, mh}
	cm := identity
	if vb, ok := viewBox(mk); ok {
		cm = viewBoxTransform(vb, mk.attrs["preserveAspectRatio"], mw, mh)
		inner = viewport{vb[2], vb[3]}
	}
	// The reference point, in the content's units, goes on the vertex.
	rx, ry := cm.apply(refX, refY)
	mm = mm.mul(matrix{1, 0, 0, 1, -rx, -ry})
	if mm.det() == 0 {
		return
	}
	mst := computeStyle(mk, initialStyle(), vp)
	var cut *clip
	if ov, _ := mk.prop("overflow"); ov != "visible" && ov != "auto" {
		p := fillPath(flatten(rectSegs(0, 0, mw, mh, 0, 0), mm), mm, false)
		cut = chainClip(&clip{paths: nil, bounds: pathBounds(p, b.full)}, nil)
		cut.paths = append(cut.paths, p)
	}
	b.group(mk, mst, cut, cl, func(c *clip) { b.children(mk, mm.mul(cm), mst, inner, c) })
}
