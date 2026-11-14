package quant

import (
	"image/color"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// searcher finds the palette entry nearest to a point, exactly, without
// looking at every entry.
//
// The space is divided into cells 8 levels wide along the colour axes, in
// slabs 6.4 levels thick along alpha, plus one slab for "exactly opaque",
// where every pixel of an opaque image lies. For a cell, only some entries
// can be the nearest one of any point in it: those whose smallest possible
// distance to the cell is not above the smallest of the entries' largest
// possible distances, and that are not further away than one other entry
// throughout the cell. That list (a handful of entries for a 256 colour
// palette) is computed the first time a pixel falls in the cell and kept;
// a search then compares the pixel with the list only. Among equally near
// entries the lowest index wins, as in a search of the whole palette.
//
// Memory: the cells of each slab in use (128KB for the opaque one; the
// translucent ones are smaller, since premultiplied colours cannot exceed
// alpha) and the lists, in chunks of 64KB up to poolChunks of them, all
// from the budget and only as needed. When the budget has no room, the
// cells concerned are searched in full instead; the result is the same.
type searcher struct {
	pal      [][4]int32
	slabs    [alphaSlabs + 1][]uint32 // per cell: list position<<9 | length; 0 = not computed
	side     [alphaSlabs + 1]int32    // cells per colour axis
	denied   [alphaSlabs + 1]bool
	pool     [][]uint8
	poolFull bool
	budget   *pipeline.Budget
	reserved int64
}

const (
	cellShift  = 7 // cells of 128 units
	alphaSlabs = maxA>>cellShift + 1
	fullSide   = maxC>>cellShift + 1
	poolChunk  = 1 << 16
	poolChunks = 32
)

func newSearcher(pal [][4]int32, budget *pipeline.Budget) *searcher {
	s := &searcher{pal: pal, budget: budget}
	for sl := range s.side {
		s.side[sl] = fullSide
		if sl < alphaSlabs {
			// The largest colour coordinate a pixel of this slab can have.
			top := min(int32(sl+1)<<cellShift-1, maxA-1) * colorScale / alphaScale
			s.side[sl] = min(fullSide, top>>cellShift+1)
		}
	}
	return s
}

func (s *searcher) release() {
	s.budget.Release(s.reserved)
	s.reserved = 0
	s.pool = nil
	s.slabs = [alphaSlabs + 1][]uint32{}
}

// brute searches the whole palette.
func (s *searcher) brute(t0, t1, t2, t3 int32) int {
	best, bi := int32(1<<31-1), 0
	for j := range s.pal {
		q := &s.pal[j]
		d := sq(t0-q[0]) + sq(t1-q[1])
		if d >= best {
			continue
		}
		if d += sq(t2-q[2]) + sq(t3-q[3]); d < best {
			best, bi = d, j
		}
	}
	return bi
}

// nearest returns the index of the entry nearest to the point, whose
// coordinates must be within [0, maxC] and [0, maxA].
func (s *searcher) nearest(t0, t1, t2, t3 int32) int {
	sl := alphaSlabs
	if t3 != maxA {
		sl = int(t3 >> cellShift)
	}
	n := s.side[sl]
	slab := s.slabs[sl]
	if slab == nil {
		if s.denied[sl] {
			return s.brute(t0, t1, t2, t3)
		}
		size := int64(n) * int64(n) * int64(n) * 4
		if s.budget.Reserve(size, "palette search cells") != nil {
			s.denied[sl] = true
			return s.brute(t0, t1, t2, t3)
		}
		s.reserved += size
		slab = make([]uint32, n*n*n)
		s.slabs[sl] = slab
	}
	// Dithering can push a colour past what its alpha allows; such points
	// share the last cell of their axis.
	c0, c1, c2 := min(t0>>cellShift, n-1), min(t1>>cellShift, n-1), min(t2>>cellShift, n-1)
	ci := (c0*n+c1)*n + c2
	v := slab[ci]
	if v == 0 {
		if s.poolFull {
			return s.brute(t0, t1, t2, t3)
		}
		if v = s.fill(sl, [3]int32{c0, c1, c2}); v == 0 {
			return s.brute(t0, t1, t2, t3)
		}
		slab[ci] = v
	}
	pos := v >> 9
	list := s.pool[pos>>16][pos&(poolChunk-1):][:v&511]
	best, bi := int32(1<<31-1), 0
	for _, j := range list {
		q := &s.pal[j]
		d := sq(t0-q[0]) + sq(t1-q[1])
		if d >= best {
			continue
		}
		if d += sq(t2-q[2]) + sq(t3-q[3]); d < best {
			best, bi = d, int(j)
		}
	}
	return bi
}

// fill computes the list of cell c of slab sl and returns its reference,
// or 0 when there is no room to keep it.
func (s *searcher) fill(sl int, c [3]int32) uint32 {
	var lo, hi [4]int32
	n := s.side[sl]
	for i := 0; i < 3; i++ {
		lo[i] = c[i] << cellShift
		hi[i] = lo[i] + 1<<cellShift - 1
		if c[i] == n-1 {
			hi[i] = maxC
		}
	}
	if sl == alphaSlabs {
		lo[3], hi[3] = maxA, maxA
	} else {
		lo[3] = int32(sl) << cellShift
		hi[3] = min(lo[3]+1<<cellShift-1, maxA-1)
	}
	var near [256]int32
	bound := int32(1<<31 - 1)
	for j := range s.pal {
		q := &s.pal[j]
		var dn, df int32
		for i := 0; i < 4; i++ {
			a, b := q[i]-lo[i], hi[i]-q[i]
			if a < 0 {
				dn += a * a
			} else if b < 0 {
				dn += b * b
			}
			df += sq(max(a, -a, b, -b))
		}
		near[j] = dn
		bound = min(bound, df)
	}
	// Of those, drop the ones that are further than the entry nearest to
	// the middle of the cell at every point of the cell. Being nearer to
	// one point than to another is a half-space, so it holds for the whole
	// cell if it holds where the difference of the squared distances,
	// which is linear, is largest.
	var list [256]uint8
	k, mid, midDist := 0, 0, int32(1<<31-1)
	for j := range s.pal {
		if near[j] > bound {
			continue
		}
		list[k] = uint8(j)
		k++
		q := &s.pal[j]
		var d int32
		for i := 0; i < 4; i++ {
			d += sq(q[i] - (lo[i]+hi[i])/2)
		}
		if d < midDist {
			mid, midDist = j, d
		}
	}
	m := &s.pal[mid]
	n2 := k
	k = 0
	for _, j := range list[:n2] {
		q := &s.pal[j]
		// d(x,m) - d(x,q) = |m|^2 - |q|^2 + 2x.(q-m), largest at the corner
		// that follows the signs of q-m.
		var worst int64
		for i := 0; i < 4; i++ {
			x := lo[i]
			if q[i] > m[i] {
				x = hi[i]
			}
			worst += int64(m[i])*int64(m[i]) - int64(q[i])*int64(q[i]) + 2*int64(x)*int64(q[i]-m[i])
		}
		if worst >= 0 || int(j) == mid {
			list[k] = j
			k++
		}
	}
	if len(s.pool) == 0 || len(s.pool[len(s.pool)-1])+k > poolChunk {
		if len(s.pool) == poolChunks || s.budget.Reserve(poolChunk, "palette search lists") != nil {
			s.poolFull = true
			return 0
		}
		s.reserved += poolChunk
		s.pool = append(s.pool, make([]uint8, 0, poolChunk))
	}
	last := &s.pool[len(s.pool)-1]
	pos := uint32(len(s.pool)-1)<<16 | uint32(len(*last))
	*last = append(*last, list[:k]...)
	return pos<<9 | uint32(k)
}

// mapper turns NRGBA rows into rows of palette indexes.
type mapper struct {
	s        *searcher
	solid    *searcher // over the opaque entries, when there are others too
	first    int       // index of the first opaque entry
	pal      [][4]int32
	dither   bool
	trans    uint8      // index of the fully transparent entry, if any
	limit    int32      // largest error passed on to a pixel, per channel
	cur, nxt [][4]int32 // Floyd-Steinberg error of this row and the next, times 16
}

// errorRowBytes is the size of one row of quantization error.
func errorRowBytes(w int) int64 { return (int64(w) + 2) * 16 }

func newMapper(pal []color.NRGBA, w int, dither bool, budget *pipeline.Budget) *mapper {
	m := &mapper{dither: dither, pal: make([][4]int32, len(pal))}
	for i, c := range pal {
		m.pal[i] = coords(c.R, c.G, c.B, c.A)
		if c.A == 0 {
			m.trans = uint8(i)
		}
	}
	m.s = newSearcher(m.pal, budget)
	// The palette is ordered by alpha, so its opaque entries are the last.
	for m.first < len(pal) && pal[m.first].A != 255 {
		m.first++
	}
	if m.first > 0 && m.first < len(pal) {
		m.solid = newSearcher(m.pal[m.first:], budget)
	}
	if dither {
		m.cur = make([][4]int32, w+2)
		m.nxt = make([][4]int32, w+2)
		// The error handed to a pixel is limited so that colours the
		// palette cannot reach (outside its hull) do not pile up error and
		// smear it over the following pixels. How much is needed depends
		// on how coarse the palette is: twice the largest distance between
		// an entry and its nearest neighbour, at least 16 levels.
		var far int64
		for i := range m.pal {
			near := int64(-1)
			for j := range m.pal {
				if i == j {
					continue
				}
				var d int64
				for c := 0; c < 4; c++ {
					d += int64(sq(m.pal[i][c] - m.pal[j][c]))
				}
				if near < 0 || d < near {
					near = d
				}
			}
			far = max(far, near)
		}
		l := int32(0)
		for int64(l)*int64(l) < 4*far && l < maxA {
			l += colorScale
		}
		m.limit = max(l, 16*colorScale)
	}
	return m
}

func (m *mapper) release() {
	m.s.release()
	if m.solid != nil {
		m.solid.release()
	}
}

// nearest returns the entry for a point. Opaque pixels (a3 is the pixel's
// own alpha coordinate) only get opaque entries when the palette has any.
func (m *mapper) nearest(t0, t1, t2, t3, a3 int32) int {
	if a3 == maxA && m.solid != nil {
		return m.first + m.solid.nearest(t0, t1, t2, maxA)
	}
	return m.s.nearest(t0, t1, t2, t3)
}

func clampTo(v, lo, hi int32) int32 { return min(max(v, lo), hi) }

// activity measures, per pixel of row cur, how busy the image is around
// it: the second differences along the row and across the rows above and
// below, summed over the channels, in 8-bit levels. It is zero on flat
// areas and on even gradients, where banding shows and dithering is needed,
// and large on texture, noise and edges.
func activity(act []uint16, prev, cur, next []byte) {
	w := len(act)
	for x := 0; x < w; x++ {
		i := 4 * x
		l, r := max(i-4, 0), min(i+4, 4*w-4)
		var a int
		for c := 0; c < 4; c++ {
			v := 2 * int(cur[i+c])
			h := v - int(cur[l+c]) - int(cur[r+c])
			v -= int(prev[i+c]) + int(next[i+c])
			a += max(h, -h) + max(v, -v)
		}
		act[x] = uint16(a)
	}
}

// Dithering trades banding for noise. Where the image itself is busy the
// noise buys nothing: texture hides the rounding error as well as it would
// hide banding. So a pixel passes on all of its error only while the error
// (summed over the channels) is at least twice the activity around it
// beyond calmActivity, and proportionally less otherwise, down to an
// eighth. pngquant's "dither map" serves the same purpose.
const (
	calmActivity = 6
	errorPerAct  = 8 // sixteenths of the error passed on, per error/activity
	leastDither  = 2 // sixteenths
)

// row maps one row. y is the row's number from the top of the image: with
// dithering, rows are walked alternately left to right and right to left
// (serpentine), which keeps the error from drifting one way, and act is
// the activity of the row. Rows must be given in order.
func (m *mapper) row(src, dst []byte, act []uint16, y int) {
	w := len(dst)
	if !m.dither {
		prev, pi := uint32(0), -1
		for x := 0; x < w; x++ {
			p := src[4*x : 4*x+4 : 4*x+4]
			key := pixKey(p)
			if pi < 0 || key != prev {
				t := coords(p[0], p[1], p[2], p[3])
				prev, pi = key, m.nearest(t[0], t[1], t[2], t[3], t[3])
			}
			dst[x] = uint8(pi)
		}
		return
	}
	cur, nxt, limit := m.cur, m.nxt, m.limit
	x, dir := 0, 1
	if y&1 == 1 {
		x, dir = w-1, -1
	}
	for i := 0; i < w; i, x = i+1, x+dir {
		p := src[4*x : 4*x+4 : 4*x+4]
		if p[3] == 0 {
			// Fully transparent stays fully transparent; the error that
			// reached this pixel is dropped.
			dst[x] = m.trans
			continue
		}
		t := coords(p[0], p[1], p[2], p[3])
		e := &cur[x+1]
		t0 := clampTo(t[0]+clampTo(e[0]/16, -limit, limit), 0, maxC)
		t1 := clampTo(t[1]+clampTo(e[1]/16, -limit, limit), 0, maxC)
		t2 := clampTo(t[2]+clampTo(e[2]/16, -limit, limit), 0, maxC)
		t3 := t[3]
		if t3 != maxA {
			// Opaque pixels are not handed alpha error, so that solid
			// areas next to soft edges stay solid.
			t3 = clampTo(t3+clampTo(e[3]/16, -limit, limit), 0, maxA)
		}
		j := m.nearest(t0, t1, t2, t3, t[3])
		dst[x] = uint8(j)
		q := &m.pal[j]
		d0, d1, d2, d3 := t0-q[0], t1-q[1], t2-q[2], t3-q[3]
		if a := int32(act[x]) - calmActivity; a > 0 {
			ds := max(d0, -d0) + max(d1, -d1) + max(d2, -d2) + max(d3, -d3)
			if s := max(ds*errorPerAct/(a*colorScale), leastDither); s < 16 {
				d0, d1, d2, d3 = d0*s/16, d1*s/16, d2*s/16, d3*s/16
			}
		}
		f := &cur[x+1+dir]
		f[0] += d0 * 7
		f[1] += d1 * 7
		f[2] += d2 * 7
		f[3] += d3 * 7
		f = &nxt[x+1-dir]
		f[0] += d0 * 3
		f[1] += d1 * 3
		f[2] += d2 * 3
		f[3] += d3 * 3
		f = &nxt[x+1]
		f[0] += d0 * 5
		f[1] += d1 * 5
		f[2] += d2 * 5
		f[3] += d3 * 5
		f = &nxt[x+1+dir]
		f[0] += d0
		f[1] += d1
		f[2] += d2
		f[3] += d3
	}
	m.cur, m.nxt = nxt, cur
	clear(m.nxt)
}
