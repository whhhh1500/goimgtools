// Package raster fills paths with anti-aliasing, any band of rows at a
// time, in integer arithmetic: FreeType's smooth rasterizer (ftgrays.c),
// which accumulates for each pixel the signed area its edges cover and how
// far they cross it vertically, then sweeps each row. Coordinates are in
// 1/256 of a pixel. Where an edge crosses a row or a column of pixels is
// worked out from its two ends, never by stepping along it, so the
// coverage of a pixel is the same whatever band it is rendered in, and on
// every processor. The fill rule is non-zero.
package raster

import "math"

// one is a pixel in the units of the coordinates.
const one = 256

// pt is a point in 1/256 of a pixel.
type pt struct{ x, y int64 }

// Path is a set of closed contours.
type Path struct {
	contours               [][]pt
	minX, minY, maxX, maxY int64
}

// Add adds a closed contour through the points (x, y pairs, in pixels).
// Its direction counts: contours of the same direction add up, one of the
// other direction takes away (a hole).
func (p *Path) Add(xy []float64) {
	if len(xy) < 6 {
		return
	}
	c := make([]pt, 0, len(xy)/2)
	for i := 0; i+1 < len(xy); i += 2 {
		q := pt{fix(xy[i]), fix(xy[i+1])}
		if len(p.contours) == 0 && len(c) == 0 {
			p.minX, p.maxX, p.minY, p.maxY = q.x, q.x, q.y, q.y
		}
		p.minX, p.maxX = min(p.minX, q.x), max(p.maxX, q.x)
		p.minY, p.maxY = min(p.minY, q.y), max(p.maxY, q.y)
		c = append(c, q)
	}
	p.contours = append(p.contours, c)
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
}

// Render writes the coverage of the path, 0 to 255, of the pixels of rows
// y0 to y0+h-1 and columns x0 to x0+w-1 into dst, stride bytes a row.
func (p *Path) Render(dst []uint8, stride, x0, y0, w, h int, c *Cells) {
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
	for r := 0; r < h; r++ {
		y := int64(y0 + r)
		top, bot := y*one, (y+1)*one
		if bot <= p.minY || top >= p.maxY || hi <= p.minX || lo > p.maxX {
			continue
		}
		clear(cover)
		clear(area)
		var left int64 // the cover of what lies left of x0
		for _, ct := range p.contours {
			for i := range ct {
				a, b := ct[i], ct[(i+1)%len(ct)]
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
