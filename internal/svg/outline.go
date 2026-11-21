package svg

import (
	"math"

	"github.com/whhhh1500/goimgtools/internal/raster"
	"github.com/whhhh1500/goimgtools/internal/trig"
)

// tolerance is how far, in device pixels, a flattened curve may stray from
// the curve.
const tolerance = 0.05

// poly is a subpath flattened into points, in user space.
type poly struct {
	xy     []float64 // x, y pairs
	corner []bool    // per point: a vertex of the path (joined as the stroke says) or inside a curve
	closed bool
}

// maxPoints bounds the points a path flattens into.
const maxPoints = 4 << 20

// hypot is the length of (x, y), the same on every processor (math.Hypot
// is not).
func hypot(x, y float64) float64 { return math.Sqrt(float64(x*x) + float64(y*y)) }

// maxScale is the most a transform stretches a length.
func maxScale(m matrix) float64 {
	p := float64(m.a*m.a) + float64(m.b*m.b)
	q := float64(m.c*m.c) + float64(m.d*m.d)
	r := float64(m.a*m.c) + float64(m.b*m.d)
	d := p - q
	return math.Sqrt((p + q + math.Sqrt(float64(d*d)+float64(4*float64(r*r)))) / 2)
}

// flatten turns path segments into polylines, close enough to the curves
// under the transform m.
func flatten(segs []seg, m matrix) []poly {
	ps, _ := flattenLimit(segs, m, maxPoints)
	return ps
}

// flattenLimit is flatten making at most limit points; ok is false when it
// stopped there.
func flattenLimit(segs []seg, m matrix, limit int) (_ []poly, ok bool) {
	tol := tolerance / max(maxScale(m), 1e-12)
	var out []poly
	var cur *poly
	var cx, cy float64
	total := 0
	start := func(x, y float64) {
		out = append(out, poly{xy: []float64{x, y}, corner: []bool{true}})
		cur = &out[len(out)-1]
		cx, cy = x, y
	}
	add := func(x, y float64, corner bool) {
		if cur == nil {
			start(cx, cy)
		}
		if total >= limit {
			return
		}
		total++
		cur.xy = append(cur.xy, x, y)
		cur.corner = append(cur.corner, corner)
		cx, cy = x, y
	}
	for _, s := range segs {
		switch s.cmd {
		case cmdMove:
			start(s.pts[0][0], s.pts[0][1])
		case cmdLine:
			add(s.pts[0][0], s.pts[0][1], true)
		case cmdQuad:
			x0, y0 := cx, cy
			x1, y1 := s.pts[0][0], s.pts[0][1]
			x2, y2 := s.pts[1][0], s.pts[1][1]
			dd := hypot(x0-2*x1+x2, y0-2*y1+y2)
			n := steps(float64(0.25*dd), tol)
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				a, b, c := float64(u*u), float64(2*float64(u*t)), float64(t*t)
				add(float64(a*x0)+float64(b*x1)+float64(c*x2), float64(a*y0)+float64(b*y1)+float64(c*y2), i == n)
			}
		case cmdCubic:
			x0, y0 := cx, cy
			x1, y1 := s.pts[0][0], s.pts[0][1]
			x2, y2 := s.pts[1][0], s.pts[1][1]
			x3, y3 := s.pts[2][0], s.pts[2][1]
			dd := math.Max(hypot(x0-2*x1+x2, y0-2*y1+y2), hypot(x1-2*x2+x3, y1-2*y2+y3))
			n := steps(float64(0.75*dd), tol)
			for i := 1; i <= n; i++ {
				t := float64(i) / float64(n)
				u := 1 - t
				a, b := float64(float64(u*u)*u), float64(3*float64(float64(u*u)*t))
				c, d := float64(3*float64(float64(u*t)*t)), float64(float64(t*t)*t)
				add(float64(a*x0)+float64(b*x1)+float64(c*x2)+float64(d*x3),
					float64(a*y0)+float64(b*y1)+float64(c*y2)+float64(d*y3), i == n)
			}
		case cmdClose:
			if cur != nil {
				cur.closed = true
				x, y := cur.xy[0], cur.xy[1]
				cur = nil
				cx, cy = x, y
			}
		}
		if total >= limit {
			return out, false
		}
	}
	return out, true
}

// steps is how many lines a curve whose second differences reach k (times
// the formula's constant) needs: Wang's formula.
func steps(k, tol float64) int {
	n := math.Ceil(math.Sqrt(k / tol))
	if !(n >= 1) {
		return 1
	}
	return int(min(n, 4096))
}

// fillPath is the outline of the polylines for filling, in device space.
func fillPath(ps []poly, m matrix, evenOdd bool) *raster.Path {
	p := &raster.Path{Union: true, EvenOdd: evenOdd}
	for _, q := range ps {
		if len(q.xy) < 6 {
			continue
		}
		xy := make([]float64, len(q.xy))
		for i := 0; i < len(q.xy); i += 2 {
			xy[i], xy[i+1] = m.apply(q.xy[i], q.xy[i+1])
		}
		p.Add(xy)
	}
	return p
}

// stroker builds the outline of a stroke as pieces in user space (a
// rectangle along each segment, a join at each vertex, a cap at each end)
// that are put into device space all turning the same way, to be covered
// by their union.
type stroker struct {
	m          matrix
	hw         float64 // half the width
	cap, join  int
	miterLimit float64
	path       *raster.Path
	arcSteps   int     // sides of a quarter of a round join or cap
	scale      float64 // the most the matrix stretches
	points     int     // the points of the pieces so far
	limit      int     // the most it may make
}

func newStroker(m matrix, width float64, cap, join int, miterLimit float64) *stroker {
	s := &stroker{m: m, hw: width / 2, cap: cap, join: join, miterLimit: miterLimit, path: &raster.Path{Union: true}}
	s.scale = maxScale(m)
	r := s.hw * s.scale
	s.arcSteps = min(64, max(2, int(math.Ceil(float64(math.Pi/2)/math.Sqrt(float64(0.1/max(r, 0.1)))))))
	s.limit = maxScenePts
	return s
}

// piece adds a polygon of user space, turned to run clockwise in device
// space.
func (s *stroker) piece(xy ...float64) {
	if s.points += len(xy) / 2; s.points > s.limit {
		return
	}
	d := make([]float64, len(xy))
	for i := 0; i < len(xy); i += 2 {
		d[i], d[i+1] = s.m.apply(xy[i], xy[i+1])
	}
	var a float64
	n := len(d) / 2
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		a += float64(d[2*i]*d[2*j+1]) - float64(d[2*j]*d[2*i+1])
	}
	if a < 0 {
		for i, j := 0, len(d)-2; i < j; i, j = i+2, j-2 {
			d[i], d[j] = d[j], d[i]
			d[i+1], d[j+1] = d[j+1], d[i+1]
		}
	}
	if a != 0 {
		s.path.Add(d)
	}
}

// disc adds a circle of radius hw at (x, y).
func (s *stroker) disc(x, y float64) {
	n := 4 * s.arcSteps
	xy := make([]float64, 0, 2*n)
	for i := 0; i < n; i++ {
		xy = append(xy, x+float64(s.hw*trig.CosPi(2*i, n)), y+float64(s.hw*trig.SinPi(2*i, n)))
	}
	s.piece(xy...)
}

// stroke adds the stroke of one polyline.
func (s *stroker) stroke(q poly) {
	// Points that repeat the one before them are dropped.
	xy, corner := q.xy[:2:2], q.corner[:1:1]
	for i := 2; i < len(q.xy); i += 2 {
		if q.xy[i] != xy[len(xy)-2] || q.xy[i+1] != xy[len(xy)-1] {
			xy = append(xy, q.xy[i], q.xy[i+1])
			corner = append(corner, q.corner[i/2])
		} else if q.corner[i/2] {
			corner[len(corner)-1] = true
		}
	}
	closed := q.closed
	if closed && len(xy) >= 4 && xy[0] == xy[len(xy)-2] && xy[1] == xy[len(xy)-1] {
		xy, corner = xy[:len(xy)-2], corner[:len(corner)-1]
	}
	n := len(xy) / 2
	if n == 1 {
		// A lone point: a round cap is a dot, a square one a square.
		x, y := xy[0], xy[1]
		switch s.cap {
		case capRound:
			s.disc(x, y)
		case capSquare:
			h := s.hw
			s.piece(x-h, y-h, x+h, y-h, x+h, y+h, x-h, y+h)
		}
		return
	}
	segs := n - 1
	if closed {
		segs = n
	}
	pt := func(i int) (float64, float64) { i %= n; return xy[2*i], xy[2*i+1] }
	length := func(i int) float64 {
		x0, y0 := pt(i)
		x1, y1 := pt(i + 1)
		return hypot(x1-x0, y1-y0)
	}
	unit := func(i int) (float64, float64) {
		x0, y0 := pt(i)
		x1, y1 := pt(i + 1)
		l := hypot(x1-x0, y1-y0)
		return (x1 - x0) / l, (y1 - y0) / l
	}
	h := s.hw
	// turn is how the line turns at vertex i, from segment i-1 to i: the
	// cross and the dot products of their directions.
	turn := func(i int) (cross, dot float64) {
		ax, ay := unit((i - 1 + n) % n)
		bx, by := unit(i)
		return float64(ax*by) - float64(ay*bx), float64(ax*bx) + float64(ay*by)
	}
	// joinAt is the join at vertex i.
	joinAt := func(i int) int {
		_, dot := turn(i)
		join := s.join
		if !corner[i%n] {
			join = joinBevel // inside a curve the turn is slight
		} else if join != joinBevel {
			// A turn so slight that the round or the miter would reach less
			// than a twentieth of a pixel past the bevel, as where the curves
			// of a circle meet: a bevel, of 3 points (a disc takes dozens).
			c := math.Sqrt(math.Max(0, float64((1+dot)/2))) // cos(half the turn)
			if float64(float64(h*s.scale)*float64(1-c)) < 0.05 {
				join = joinBevel
			}
		}
		return join
	}
	// joinPiece adds the join at vertex i.
	joinPiece := func(i int) {
		x, y := pt(i)
		ax, ay := unit((i - 1 + n) % n)
		bx, by := unit(i)
		cross := float64(ax*by) - float64(ay*bx)
		dot := float64(ax*bx) + float64(ay*by)
		if cross == 0 && dot > 0 {
			return // straight on
		}
		join := joinAt(i)
		if join == joinRound {
			s.disc(x, y)
			return
		}
		// The outer side: where the turn opens.
		sgn := 1.0
		if cross > 0 {
			sgn = -1
		}
		pax, pay := x+float64(sgn*float64(-ay*h)), y+float64(sgn*float64(ax*h))
		pbx, pby := x+float64(sgn*float64(-by*h)), y+float64(sgn*float64(bx*h))
		if join == joinMiter {
			// The miter's length over the width: 1 / sin(half the angle
			// between the segments).
			cosT := -dot
			sinHalf := math.Sqrt(math.Max(0, (1-cosT)/2))
			if sinHalf > 0 && 1/sinHalf <= s.miterLimit {
				// The tip: along the bisector of the two outer normals.
				mx, my := float64((pax+pbx)/2)-x, float64((pay+pby)/2)-y
				ml := hypot(mx, my)
				if ml > 0 {
					k := h / sinHalf / ml
					s.piece(x, y, pax, pay, x+float64(mx*k), y+float64(my*k), pbx, pby)
					return
				}
			}
		}
		s.piece(x, y, pax, pay, pbx, pby)
	}
	// ends is where segment i begins and ends, square caps lengthening
	// the first and the last of an open line.
	ends := func(i int) (x0, y0, x1, y1 float64) {
		x0, y0 = pt(i)
		x1, y1 = pt(i + 1)
		if !closed && s.cap == capSquare {
			ux, uy := unit(i)
			if i == 0 {
				x0, y0 = x0-float64(ux*h), y0-float64(uy*h)
			}
			if i == segs-1 {
				x1, y1 = x1+float64(ux*h), y1+float64(uy*h)
			}
		}
		return x0, y0, x1, y1
	}
	// rect adds the rectangle along segment i.
	rect := func(i int) {
		x0, y0, x1, y1 := ends(i)
		ux, uy := unit(i)
		nx, ny := float64(-uy*h), float64(ux*h)
		s.piece(x0+nx, y0+ny, x1+nx, y1+ny, x1-nx, y1-ny, x0-nx, y0-ny)
	}
	// smooth reports whether the line goes on at vertex i with a bevel (or
	// straight on), turning a quarter at most.
	smooth := func(i int) bool {
		cross, dot := turn(i)
		return dot >= 0 && (cross == 0 || joinAt(i) == joinBevel)
	}
	// A closed line is gone round from a vertex with a join of its own,
	// if it has one.
	start := 0
	if closed {
		for i := 0; i < n; i++ {
			if !smooth(i) {
				start = i
				break
			}
		}
	}
	seg := func(k int) int { return (start + k) % n }
	// run adds the segments seg(a) to seg(b), joined by bevels: the union
	// of their rectangles and bevels is the polygon of the offsets of the
	// line, the outer side of each turn bevelled and the inner side meeting
	// at the miter, as long as on each side every segment is longer than
	// what the rectangles of its neighbours reach into it past their
	// vertices (h sin(the turn), more than the h tan(half the turn) the
	// miter is set back by), so that is what is added, a piece in place of
	// two for each segment.
	run := func(a, b int) {
		if a == b {
			rect(seg(a))
			return
		}
		x0, y0, _, _ := ends(seg(a))
		ux, uy := unit(seg(a))
		nx, ny := float64(-uy*h), float64(ux*h)
		left := []float64{x0 + nx, y0 + ny}
		right := []float64{x0 - nx, y0 - ny}
		var inL, inR float64 // how far each side of the segment begins past its vertex
		ok := true
		for k := a + 1; k <= b; k++ {
			v := seg(k)
			px, py, pnx, pny := ux, uy, nx, ny
			ux, uy = unit(v)
			nx, ny = float64(-uy*h), float64(ux*h)
			cross := float64(px*uy) - float64(py*ux)
			dot := float64(px*ux) + float64(py*uy)
			x, y := pt(v)
			// The inner side meets at the miter; on that side each
			// rectangle reaches h sin(the turn) into the other segment.
			mx, my := (pnx+nx)/(1+dot), (pny+ny)/(1+dot)
			t := float64(h * math.Abs(cross))
			var tL, tR float64
			switch {
			case cross > 0: // turning to the left: the left side is inside
				tL = t
				left = append(left, x+mx, y+my)
				right = append(right, x-pnx, y-pny, x-nx, y-ny)
			case cross < 0:
				tR = t
				left = append(left, x+pnx, y+pny, x+nx, y+ny)
				right = append(right, x-mx, y-my)
			default:
				left = append(left, x+nx, y+ny)
				right = append(right, x-nx, y-ny)
			}
			l := length(seg(k - 1))
			ok = ok && inL+tL < l && inR+tR < l
			inL, inR = tL, tR
		}
		l := length(seg(b))
		ok = ok && inL < l && inR < l
		if !ok {
			for k := a; k <= b; k++ {
				rect(seg(k))
				if k > a {
					joinPiece(seg(k))
				}
			}
			return
		}
		_, _, x1, y1 := ends(seg(b))
		left = append(left, x1+nx, y1+ny, x1-nx, y1-ny)
		for i := len(right) - 2; i >= 0; i -= 2 {
			left = append(left, right[i], right[i+1])
		}
		s.piece(left...)
	}
	// Runs of segments, each within a quarter turn of its first, joined
	// at their ends.
	for k := 0; k < segs; k++ {
		a := k
		ax, ay := unit(seg(a))
		for k+1 < segs && smooth(seg(k+1)) {
			bx, by := unit(seg(k + 1))
			if float64(ax*bx)+float64(ay*by) < 0.1 {
				break
			}
			k++
		}
		run(a, k)
		if k+1 < segs {
			joinPiece(seg(k + 1))
		}
	}
	if closed {
		joinPiece(start)
	}
	if !closed && s.cap == capRound {
		x, y := pt(0)
		s.disc(x, y)
		x, y = pt(n - 1)
		s.disc(x, y)
	}
}

// dashes cuts the polylines into the dashes of the pattern, offset by off;
// false if that takes more than limit dashes and gaps.
func dashes(ps []poly, pattern []float64, off float64, limit int) ([]poly, bool) {
	var total float64
	for _, d := range pattern {
		total += d
	}
	if !(total > 0) {
		return ps, true
	}
	var out []poly
	pieces := 0
	for _, q := range ps {
		xy := q.xy
		if q.closed && len(xy) >= 4 {
			xy = append(append([]float64(nil), xy...), xy[0], xy[1])
		}
		// Where in the pattern the line starts.
		pos := math.Mod(off, total)
		if pos < 0 {
			pos += total
		}
		k := 0
		for pos >= pattern[k] {
			pos -= pattern[k]
			k = (k + 1) % len(pattern)
		}
		left := pattern[k] - pos // of the current dash or gap
		on := k%2 == 0
		var cur *poly
		if on {
			out = append(out, poly{xy: []float64{xy[0], xy[1]}, corner: []bool{true}})
			cur = &out[len(out)-1]
		}
		for i := 2; i+1 < len(xy); i += 2 {
			x0, y0, x1, y1 := xy[i-2], xy[i-1], xy[i], xy[i+1]
			l := hypot(x1-x0, y1-y0)
			done := 0.0
			for l-done > left {
				if pieces++; pieces > limit {
					return nil, false
				}
				done += left
				t := done / l
				x, y := x0+float64(t*(x1-x0)), y0+float64(t*(y1-y0))
				if on {
					cur.xy = append(cur.xy, x, y)
					cur.corner = append(cur.corner, false)
					cur = nil
				} else {
					out = append(out, poly{xy: []float64{x, y}, corner: []bool{true}})
					cur = &out[len(out)-1]
				}
				on = !on
				k = (k + 1) % len(pattern)
				left = pattern[k]
			}
			left -= l - done
			if on {
				cur.xy = append(cur.xy, x1, y1)
				cur.corner = append(cur.corner, q.corner[(i/2)%len(q.corner)])
			}
		}
	}
	// Dashes of no length are dots (for round and square caps).
	return out, true
}

// deviceLines is the polylines put into device space by m.
func deviceLines(ps []poly, m matrix) []poly {
	out := make([]poly, len(ps))
	for i, q := range ps {
		xy := make([]float64, len(q.xy))
		for k := 0; k+1 < len(xy); k += 2 {
			xy[k], xy[k+1] = m.apply(q.xy[k], q.xy[k+1])
		}
		out[i] = poly{xy: xy, corner: q.corner, closed: q.closed}
	}
	return out
}
