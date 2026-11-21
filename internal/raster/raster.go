// Package raster fills paths with anti-aliasing, any band of rows at a
// time, in integer arithmetic: FreeType's smooth rasterizer (ftgrays.c),
// which accumulates for each pixel the signed area its edges cover and how
// far they cross it vertically, then sweeps each row. Coordinates are in
// 1/256 of a pixel. Where an edge crosses a row or a column of pixels is
// worked out from its two ends, never by stepping along it, so the
// coverage of a pixel is the same whatever band it is rendered in, and on
// every processor. The fill rule is non-zero.
//
// A path whose contours overlap (the pieces of a stroke) can instead be
// covered by their union, as cairo's and Skia's scan converters do: each
// row is cut at the ends of its edges into bands every edge crosses whole,
// the edges of a band are put in order across it, and only those where the
// winding turns from nothing to something, or back, are added up, so that
// where pieces overlap they count once, partly covered pixels too. A band
// two edges cross in is halved until they do not, or sampled across its
// middle when it is thin.
package raster

import (
	"cmp"
	"math"
	"slices"
	"sync"
)

// one is a pixel in the units of the coordinates.
const one = 256

// pt is a point in 1/256 of a pixel.
type pt struct{ x, y int64 }

// Path is a set of closed contours.
type Path struct {
	contours               [][]pt
	spans                  [][2]int64 // the least and greatest y of each contour
	minX, minY, maxX, maxY int64
	// Union covers a pixel by the union of the contours, exactly, rather
	// than by adding up the area each covers, which counts twice the part
	// of a pixel two of them cover: for the overlapping pieces of a stroke.
	Union bool
	// EvenOdd, with Union, covers what an odd number of contours wind
	// around, rather than what any number but none does.
	EvenOdd bool
	// Its edges by their tops, made when the path is first rendered, for
	// a row to look only at those that cross it: each the contour and the
	// edge's place in it, contour<<32 | place (8 bytes an edge), which is
	// also their order in the path.
	byTopOnce  sync.Once
	byTop      []uint64
	byTopCount int // the contours then
}

// edgeEnds are the ends of edge e (contour<<32 | place), as stored.
func (p *Path) edgeEnds(e uint64) (a, b pt) {
	ct := p.contours[e>>32]
	i := int(e & 0xffffffff)
	return ct[i], ct[(i+1)%len(ct)]
}

// edgeOrder is the edges that are not level, in the order of their tops
// (and, for equal tops, theirs in the path).
func (p *Path) edgeOrder() []uint64 {
	build := func() []uint64 {
		var order []uint64
		for k, ct := range p.contours {
			for i := range ct {
				if ct[i].y != ct[(i+1)%len(ct)].y {
					order = append(order, uint64(k)<<32|uint64(i))
				}
			}
		}
		topOf := func(e uint64) int64 { a, b := p.edgeEnds(e); return min(a.y, b.y) }
		slices.SortStableFunc(order, func(a, b uint64) int { return cmp.Compare(topOf(a), topOf(b)) })
		return order
	}
	p.byTopOnce.Do(func() { p.byTop, p.byTopCount = build(), len(p.contours) })
	if p.byTopCount != len(p.contours) {
		return build() // contours added after it was first rendered
	}
	return p.byTop
}

// Add adds a closed contour through the points (x, y pairs, in pixels).
// Its direction counts: contours of the same direction add up, one of the
// other direction takes away (a hole).
func (p *Path) Add(xy []float64) {
	if len(xy) < 6 {
		return
	}
	c := make([]pt, 0, len(xy)/2)
	var span [2]int64
	for i := 0; i+1 < len(xy); i += 2 {
		q := pt{fix(xy[i]), fix(xy[i+1])}
		if len(p.contours) == 0 && len(c) == 0 {
			p.minX, p.maxX, p.minY, p.maxY = q.x, q.x, q.y, q.y
		}
		if len(c) == 0 {
			span = [2]int64{q.y, q.y}
		}
		p.minX, p.maxX = min(p.minX, q.x), max(p.maxX, q.x)
		p.minY, p.maxY = min(p.minY, q.y), max(p.maxY, q.y)
		span = [2]int64{min(span[0], q.y), max(span[1], q.y)}
		c = append(c, q)
	}
	p.contours = append(p.contours, c)
	p.spans = append(p.spans, span)
}

// fix is v pixels in 1/256 of a pixel, rounded, held where it cannot
// overflow.
func fix(v float64) int64 {
	const lim = 1 << 28 // a million pixels out: products of two stay in 64 bits
	if math.IsNaN(v) {
		return 0
	}
	return int64(math.Round(math.Max(-lim, math.Min(lim, float64(v*one)))))
}

// Empty reports whether the path has nothing to fill.
func (p *Path) Empty() bool { return len(p.contours) == 0 }

// Bounds is the rectangle of pixels the path can touch: x0, y0 included,
// x1, y1 not.
func (p *Path) Bounds() (x0, y0, x1, y1 int) {
	if p.Empty() {
		return 0, 0, 0, 0
	}
	return int(floorDiv(p.minX, one)), int(floorDiv(p.minY, one)), int(floorDiv(p.maxX, one)) + 1, int(floorDiv(p.maxY, one)) + 1
}

// BoundsAt is the rectangle of pixels the path moved by (dx, dy) pixels
// can touch, as RenderAt draws it.
func (p *Path) BoundsAt(dx, dy float64) (x0, y0, x1, y1 int) {
	if p.Empty() {
		return 0, 0, 0, 0
	}
	ox, oy := fix(dx), fix(dy)
	return int(floorDiv(p.minX+ox, one)), int(floorDiv(p.minY+oy, one)), int(floorDiv(p.maxX+ox, one)) + 1, int(floorDiv(p.maxY+oy, one)) + 1
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

// lerp is the coordinate u at which a line through (u0, v0) and (u1, v1)
// is at v: u0 + (u1-u0)*(v-v0)/(v1-v0), rounded to the nearest (halves
// up). v1 != v0.
func lerp(u0, v0, u1, v1, v int64) int64 {
	num := (u1 - u0) * (v - v0)
	den := v1 - v0
	if den < 0 {
		num, den = -num, -den
	}
	return u0 + floorDiv(2*num+den, 2*den)
}

// Cells is the scratch of a rendering, reused from one to the next.
type Cells struct {
	cover, area []int64
	edges       []edge
	events      []int64
	act         []int32
	rows        []uint64 // the edges that cross the row, in their order in the path
}

// Render writes the coverage of the path, 0 to 255, of the pixels of rows
// y0 to y0+h-1 and columns x0 to x0+w-1 into dst, stride bytes a row.
func (p *Path) Render(dst []uint8, stride, x0, y0, w, h int, c *Cells) {
	p.render(dst, stride, x0, y0, w, h, 0, 0, c)
}

// RenderAt is Render of the path moved by (dx, dy) pixels (to the nearest
// 1/256): one outline drawn in many places.
func (p *Path) RenderAt(dst []uint8, stride, x0, y0, w, h int, dx, dy float64, c *Cells) {
	p.render(dst, stride, x0, y0, w, h, fix(dx), fix(dy), c)
}

// render renders the path moved by (ox, oy), in 1/256 of a pixel.
func (p *Path) render(dst []uint8, stride, x0, y0, w, h int, ox, oy int64, c *Cells) {
	for r := 0; r < h; r++ {
		clear(dst[r*stride : r*stride+w])
	}
	if p.Empty() || w <= 0 || h <= 0 {
		return
	}
	if cap(c.cover) < w {
		c.cover, c.area = make([]int64, w), make([]int64, w)
	}
	cover, area := c.cover[:w], c.area[:w]
	lo, hi := int64(x0)*one, int64(x0+w)*one
	// The edges crossing the row, kept going down: those whose top is
	// above its bottom come in, those whose bottom is above its top go; in
	// their order in the path, as they were added up before.
	order := p.edgeOrder()
	act, next := c.rows[:0], 0
	for r := 0; r < h; r++ {
		y := int64(y0 + r)
		top, bot := y*one, (y+1)*one
		if bot <= p.minY+oy || top >= p.maxY+oy || hi <= p.minX+ox || lo > p.maxX+ox {
			continue
		}
		for next < len(order) {
			e := order[next]
			a, b := p.edgeEnds(e)
			if min(a.y, b.y)+oy >= bot {
				break
			}
			next++
			if max(a.y, b.y)+oy <= top {
				continue // above this row, and so all below it
			}
			at, _ := slices.BinarySearch(act, e)
			act = slices.Insert(act, at, e)
		}
		kept := act[:0]
		for _, e := range act {
			if a, b := p.edgeEnds(e); max(a.y, b.y)+oy > top {
				kept = append(kept, e)
			}
		}
		act = kept
		clear(cover)
		clear(area)
		var left int64 // the cover of what lies left of x0
		if p.Union {
			left = p.unionRow(cover, area, int64(x0), lo, hi, top, bot, ox, oy, act, c)
		} else {
			for _, e := range act {
				{
					a, b := p.edgeEnds(e)
					a.x, a.y, b.x, b.y = a.x+ox, a.y+oy, b.x+ox, b.y+oy
					if a.y == b.y {
						continue
					}
					ya, yb := max(min(a.y, b.y), top), min(max(a.y, b.y), bot)
					if ya >= yb {
						continue
					}
					// The piece of the edge in this row, in its direction.
					y1, y2 := ya, yb
					if a.y > b.y {
						y1, y2 = yb, ya
					}
					x1, x2 := a.x, b.x
					if y1 != a.y {
						x1 = lerp(a.x, a.y, b.x, b.y, y1)
					}
					if y2 != b.y {
						x2 = lerp(a.x, a.y, b.x, b.y, y2)
					}
					left += row(cover, area, int64(x0), lo, hi, x1, y1-top, x2, y2-top)
				}
			}
		}
		// Sweep: the cover of the edges to the left runs on to the right.
		acc := left * 2 * one
		out := dst[r*stride : r*stride+w]
		for x := range out {
			acc += cover[x] * 2 * one
			v := acc - area[x]
			if v < 0 {
				v = -v
			}
			out[x] = uint8(min(255, v>>9))
		}
	}
	c.rows = act
}

// row adds an edge piece within one row of pixels, from (x1, fy1) to (x2,
// fy2) (fy from the row's top), to the cells of the columns from lo to hi
// (in 1/256 of a pixel; pixel x0 is index 0). It returns the cover of what
// lies left of the columns, which counts for every pixel of the row.
func row(cover, area []int64, x0, lo, hi, x1, fy1, x2, fy2 int64) (left int64) {
	// add adds a part that lies within one column.
	add := func(ax, ay, bx, by int64) {
		dy := by - ay
		switch {
		case dy == 0:
		case max(ax, bx) <= lo:
			left += dy
		case min(ax, bx) >= hi:
		default:
			cx := floorDiv(min(ax, bx), one)
			cover[cx-x0] += dy
			area[cx-x0] += (ax + bx - 2*cx*one) * dy
		}
	}
	px, py := x1, fy1
	switch {
	case x2 > x1:
		for c := (floorDiv(x1, one) + 1) * one; c < x2; c += one {
			cy := lerp(fy1, x1, fy2, x2, c)
			add(px, py, c, cy)
			px, py = c, cy
		}
	case x2 < x1:
		for c := floorDiv(x1-1, one) * one; c > x2; c -= one {
			cy := lerp(fy1, x1, fy2, x2, c)
			add(px, py, c, cy)
			px, py = c, cy
		}
	}
	add(px, py, x2, fy2)
	return left
}

// edge is an edge of a Union path, top end first.
type edge struct {
	ax, ay, bx, by int64
	dir            int64 // 1 if it runs down, -1 up
	t, b           int64 // where it is at the top and the bottom of a band
}

// at is where the edge is at y, ay <= y <= by.
func (e *edge) at(y int64) int64 {
	switch y {
	case e.ay:
		return e.ax
	case e.by:
		return e.bx
	}
	return lerp(e.ax, e.ay, e.bx, e.by, y)
}

const (
	maxCuts  = 32  // more ends of edges in a row than this: cut it evenly
	evenCuts = 16  // into this many bands, each sampled where edges end in it
	minBand  = 4   // a band two edges cross in is halved down to this height
	maxBands = 256 // and no more bands than this in a row
)

// unionRow adds the boundary of the union of the contours, moved by (ox,
// oy), within the row from top to bot to the cells, and returns the cover
// of what lies left of the columns.
func (p *Path) unionRow(cover, area []int64, x0, lo, hi, top, bot, ox, oy int64, act []uint64, c *Cells) (left int64) {
	es, ev := c.edges[:0], c.events[:0]
	for _, k := range act {
		{
			a, b := p.edgeEnds(k)
			a.x, a.y, b.x, b.y = a.x+ox, a.y+oy, b.x+ox, b.y+oy
			e := edge{ax: a.x, ay: a.y, bx: b.x, by: b.y, dir: 1}
			if a.y > b.y {
				e = edge{ax: b.x, ay: b.y, bx: a.x, by: a.y, dir: -1}
			}
			if e.ay == e.by || e.by <= top || e.ay >= bot {
				continue
			}
			if e.ay > top {
				ev = append(ev, e.ay)
			}
			if e.by < bot {
				ev = append(ev, e.by)
			}
			es = append(es, e)
		}
	}
	c.edges, c.events = es, ev
	if len(es) == 0 {
		return 0
	}
	u := union{es: es, cover: cover, area: area, x0: x0, lo: lo, hi: hi, top: top, c: c, budget: maxBands, evenOdd: p.EvenOdd}
	if len(ev) <= maxCuts {
		slices.Sort(ev)
		ev = slices.Compact(ev)
		y := top
		for _, e := range ev {
			u.band(y, e)
			y = e
		}
		u.band(y, bot)
	} else {
		for k := int64(0); k < evenCuts; k++ {
			u.band(top+k*one/evenCuts, top+(k+1)*one/evenCuts)
		}
	}
	return u.left
}

// union is the rendering of a row of a Union path.
type union struct {
	es              []edge
	cover, area     []int64
	x0, lo, hi, top int64
	c               *Cells
	budget          int
	left            int64
	evenOdd         bool
}

// inside reports whether winding w is inside the path.
func (u *union) inside(w int64) bool {
	if u.evenOdd {
		return w&1 != 0
	}
	return w != 0
}

// band adds the boundary of the union between ya and yb.
func (u *union) band(ya, yb int64) {
	act := u.c.act[:0]
	whole := true
	for i := range u.es {
		e := &u.es[i]
		if e.by <= ya || e.ay >= yb {
			continue
		}
		if e.ay > ya || e.by < yb {
			whole = false
		}
		act = append(act, int32(i))
	}
	u.c.act = act
	u.budget--
	if len(act) == 0 {
		return
	}
	es := u.es
	if whole {
		for _, i := range act {
			e := &es[i]
			e.t, e.b = e.at(ya), e.at(yb)
		}
		slices.SortFunc(act, func(i, j int32) int {
			a, b := &es[i], &es[j]
			return cmp.Or(cmp.Compare(a.t+a.b, b.t+b.b), cmp.Compare(a.t, b.t), cmp.Compare(i, j))
		})
		ordered := true
		for k := 1; k < len(act); k++ {
			if es[act[k]].t < es[act[k-1]].t || es[act[k]].b < es[act[k-1]].b {
				ordered = false
				break
			}
		}
		if ordered {
			// The edges where the winding starts, running down (covering
			// what lies right of them), and where it stops, running up.
			w := int64(0)
			for _, i := range act {
				e := &es[i]
				was := w
				w += e.dir
				switch {
				case !u.inside(was) && u.inside(w):
					u.left += row(u.cover, u.area, u.x0, u.lo, u.hi, e.t, ya-u.top, e.b, yb-u.top)
				case u.inside(was) && !u.inside(w):
					u.left += row(u.cover, u.area, u.x0, u.lo, u.hi, e.b, yb-u.top, e.t, ya-u.top)
				}
			}
			return
		}
		if yb-ya > minBand && u.budget > 1 {
			m := ya + (yb-ya)/2
			u.band(ya, m)
			u.band(m, yb)
			return
		}
	}
	// Sampled across its middle: the spans there, as wide as they are and
	// as high as the band.
	ym := ya + (yb-ya)/2
	act = act[:0]
	for _, i := range u.c.act {
		e := &es[i]
		if e.ay <= ym && e.by > ym {
			e.t = e.at(ym)
			act = append(act, i)
		}
	}
	slices.SortFunc(act, func(i, j int32) int {
		return cmp.Or(cmp.Compare(es[i].t, es[j].t), cmp.Compare(i, j))
	})
	w := int64(0)
	for _, i := range act {
		e := &es[i]
		was := w
		w += e.dir
		switch {
		case !u.inside(was) && u.inside(w):
			u.left += row(u.cover, u.area, u.x0, u.lo, u.hi, e.t, ya-u.top, e.t, yb-u.top)
		case u.inside(was) && !u.inside(w):
			u.left += row(u.cover, u.area, u.x0, u.lo, u.hi, e.t, yb-u.top, e.t, ya-u.top)
		}
	}
}
