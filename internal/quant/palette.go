package quant

import (
	"image/color"
	"slices"
	"sort"
)

// box is a set of points pts[lo:hi] during the median cut.
type box struct {
	lo, hi int
	w      float64    // weight (pixels)
	sum    [4]float64 // weighted coordinate sums
	vr     [4]float64 // weighted squared deviation from the mean, per axis
	sse    float64    // the same over all axes: the error of one colour for the box
}

func newBox(pts []bin, lo, hi int) box {
	b := box{lo: lo, hi: hi}
	var s2 [4]float64
	for i := lo; i < hi; i++ {
		p := &pts[i]
		n := float64(p.n)
		b.w += n
		for c := 0; c < 4; c++ {
			v := float64(p.v[c])
			b.sum[c] += n * v
			s2[c] += n * v * v
		}
	}
	for c := 0; c < 4; c++ {
		b.vr[c] = max(0, s2[c]-b.sum[c]*b.sum[c]/b.w)
		b.sse += b.vr[c]
	}
	return b
}

// split cuts b in two along the axis it varies most on, at the place that
// leaves the smallest total error (not at the median: that is what makes
// the cut variance-minimising, as in Wu's quantizer).
func split(pts []bin, b box) (box, box, bool) {
	axis := 0
	for c := 1; c < 4; c++ {
		if b.vr[c] > b.vr[axis] {
			axis = c
		}
	}
	if b.hi-b.lo < 2 || b.vr[axis] <= 0 {
		return b, b, false
	}
	part := pts[b.lo:b.hi]
	slices.SortFunc(part, func(x, y bin) int {
		if d := int(x.v[axis]) - int(y.v[axis]); d != 0 {
			return d
		}
		// A total order, so the result does not depend on the sort.
		for c := 0; c < 4; c++ {
			if d := int(x.v[c]) - int(y.v[c]); d != 0 {
				return d
			}
		}
		return int(x.n) - int(y.n)
	})
	var w1 float64
	var s1 [4]float64
	cut, best := -1, -1.0
	for i := 0; i < len(part)-1; i++ {
		p := &part[i]
		n := float64(p.n)
		w1 += n
		w2 := b.w - w1
		score := 0.0
		for c := 0; c < 4; c++ {
			s1[c] += n * float64(p.v[c])
			s2 := b.sum[c] - s1[c]
			score += s1[c]*s1[c]/w1 + s2*s2/w2
		}
		if score > best {
			best, cut = score, i+1
		}
	}
	return newBox(pts, b.lo, b.lo+cut), newBox(pts, b.lo+cut, b.hi), true
}

// medianCut returns up to k centres for the points: the box with the
// largest error is cut until there are k boxes. With opaque > 0 the first
// opaque points are kept apart from the rest, and the centres of their
// boxes come first; their number is returned.
func medianCut(pts []bin, opaque, k int) ([][4]float64, int) {
	var boxes []box
	if opaque > 0 && opaque < len(pts) {
		boxes = []box{newBox(pts, 0, opaque), newBox(pts, opaque, len(pts))}
	} else {
		boxes = []box{newBox(pts, 0, len(pts))}
	}
	for len(boxes) < k {
		bi := -1
		for i := range boxes {
			if boxes[i].hi-boxes[i].lo >= 2 && boxes[i].sse > 0 && (bi < 0 || boxes[i].sse > boxes[bi].sse) {
				bi = i
			}
		}
		if bi < 0 {
			break
		}
		x, y, ok := split(pts, boxes[bi])
		if !ok {
			boxes[bi].sse = 0
			continue
		}
		boxes[bi] = x
		boxes = append(boxes, y)
	}
	sort.SliceStable(boxes, func(i, j int) bool { return boxes[i].lo < boxes[j].lo })
	cent := make([][4]float64, len(boxes))
	n := 0
	for i, b := range boxes {
		for c := 0; c < 4; c++ {
			cent[i][c] = b.sum[c] / b.w
		}
		if b.lo < opaque {
			n++
		}
	}
	return cent, n
}

// kmeansPasses bounds the refinement; it usually stops earlier.
const kmeansPasses = 16

// refine moves the centres with k-means over the points: every point goes
// to its nearest centre, every centre to the mean of its points. It returns
// the centres that still have points.
func refine(pts []bin, cent [][4]float64) [][4]float64 {
	k := len(cent)
	if k == 0 {
		return cent
	}
	ic := make([][4]int32, k)
	w := make([]float64, k)
	sum := make([][4]float64, k)
	for pass := 0; pass < kmeansPasses; pass++ {
		for i := range cent {
			for c := 0; c < 4; c++ {
				ic[i][c] = int32(cent[i][c] + 0.5)
			}
		}
		clear(w)
		clear(sum)
		last := 0
		for i := range pts {
			p := &pts[i]
			v0, v1, v2, v3 := p.v[0], p.v[1], p.v[2], p.v[3]
			q := &ic[last]
			best := sq(v0-q[0]) + sq(v1-q[1]) + sq(v2-q[2]) + sq(v3-q[3])
			bi := last
			for j := range ic {
				q := &ic[j]
				d := sq(v0-q[0]) + sq(v1-q[1])
				if d > best {
					continue
				}
				d += sq(v2-q[2]) + sq(v3-q[3])
				if d < best || (d == best && j < bi) {
					best, bi = d, j
				}
			}
			last = bi
			n := float64(p.n)
			w[bi] += n
			s := &sum[bi]
			s[0] += n * float64(v0)
			s[1] += n * float64(v1)
			s[2] += n * float64(v2)
			s[3] += n * float64(v3)
		}
		moved := 0.0
		for i := range cent {
			if w[i] == 0 {
				continue
			}
			for c := 0; c < 4; c++ {
				v := sum[i][c] / w[i]
				moved = max(moved, v-cent[i][c], cent[i][c]-v)
				cent[i][c] = v
			}
		}
		if moved < 1 {
			break
		}
	}
	out := cent[:0]
	for i := range cent {
		if w[i] != 0 {
			out = append(out, cent[i])
		}
	}
	return out
}

func sq(d int32) int32 { return d * d }

// toNRGBA rounds a centre to an 8-bit colour.
func toNRGBA(c [4]float64) color.NRGBA {
	a := c[3] / alphaScale
	if a >= 254.5 {
		a = 255
	}
	f := 255 / (a * colorScale)
	ch := func(v float64) uint8 { return uint8(min(255, max(0, v*f+0.5))) }
	return color.NRGBA{ch(c[0]), ch(c[1]), ch(c[2]), uint8(min(255, max(1, a+0.5)))}
}

// centres chooses up to k palette colours for the points, as coordinates.
// The first opaque points are those of the opaque pixels.
//
// Opaque pixels must stay opaque: a solid area that lets a little of the
// background through is a worse error than its size in colour terms says.
// So when the image has both kinds (and k allows it), the opaque and the
// translucent points are clustered apart: the first solid of the centres
// returned belong to the opaque pixels alone. With solid == 0 all centres
// serve all pixels.
func centres(pts []bin, opaque, k int) (cent [][4]float64, solid int) {
	if len(pts) == 0 || k <= 0 {
		return nil, 0
	}
	if k < 2 && opaque < len(pts) {
		opaque = 0 // one colour for everything
	}
	cent, solid = medianCut(pts, opaque, k)
	a := refine(pts[:opaque], cent[:solid:solid])
	b := refine(pts[opaque:], cent[solid:])
	return append(a, b...), len(a)
}

// finish rounds the centres to 8-bit colours; transparent adds the fully
// transparent entry. Entries are ordered by alpha, so the PNG tRNS chunk
// stops at the last one that is not opaque.
func finish(cent [][4]float64, transparent bool) []color.NRGBA {
	var pal []color.NRGBA
	seen := map[color.NRGBA]bool{}
	if transparent {
		pal = append(pal, color.NRGBA{})
	}
	for _, c := range cent {
		if n := toNRGBA(c); !seen[n] {
			seen[n] = true
			pal = append(pal, n)
		}
	}
	sort.SliceStable(pal, func(i, j int) bool { return pal[i].A < pal[j].A })
	return pal
}

// exactPalette is the palette of an image with few colours: its colours,
// in an order that depends on nothing but the colours.
func exactPalette(keys []uint32) []color.NRGBA {
	pal := make([]color.NRGBA, len(keys))
	for i, k := range keys {
		pal[i] = color.NRGBA{uint8(k), uint8(k >> 8), uint8(k >> 16), uint8(k >> 24)}
	}
	sort.Slice(pal, func(i, j int) bool {
		p, q := pal[i], pal[j]
		if p.A != q.A {
			return p.A < q.A
		}
		return uint32(p.R)<<16|uint32(p.G)<<8|uint32(p.B) < uint32(q.R)<<16|uint32(q.G)<<8|uint32(q.B)
	})
	return pal
}
