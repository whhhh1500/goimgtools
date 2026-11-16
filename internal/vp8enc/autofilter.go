package vp8enc

// The loop filter level of each segment is chosen as libwebp's autofilter
// (cwebp -af; src/enc/filter_enc.c) chooses it: while the macroblocks are
// coded, the reconstruction of each is filtered at levels around the
// segment's starting level, and how like the source it then is, by SSIM,
// is added up per segment and level; each segment gets the level that came
// out best. Only the edges inside a macroblock are filtered, as libwebp
// does: those between macroblocks would need the neighbours below and to
// the right, not yet coded.
//
// The SSIM sums are integers, computed separably (the window weights are a
// product of the same weights across and down), which gives the same
// values as libwebp's sums over each window; each row's totals are added
// to the frame's in row order, so the levels do not depend on how many
// rows were coded at once.

const lfLevels = 64

// mbFilterBytes is the memory of measuring macroblocks, per row coded at
// once.
const mbFilterBytes = 3*384 + 3*200*4 + 160*4 + 3*100*4 + 256*4 + 384 + 4*lfLevels*8

// filterStats are SSIM totals per segment and loop filter level.
type filterStats [numSegments][lfLevels]float64

func (f *filterStats) add(o *filterStats) {
	for s := range f {
		for l := range f[s] {
			f[s][l] += o[s][l]
		}
	}
}

// best gives each segment the level whose total beats no filtering by the
// most, and by at least a hundred-thousandth (libwebp's
// VP8AdjustFilterStrength). A segment no macroblock was measured in keeps
// its level.
func (f *filterStats) best(segs []segment) {
	for s := range segs {
		if f[s][0] == 0 {
			continue
		}
		level, v := 0, 1.00001*f[s][0]
		for l := 1; l < lfLevels; l++ {
			if f[s][l] > v {
				level, v = l, f[s][l]
			}
		}
		segs[s].filter = level
	}
}

// mbFilter is the scratch of measuring a macroblock.
type mbFilter struct {
	rec, flt [384]uint8 // reconstruction, and it filtered: Y 16x16, U, V 8x8
	prev     [384]uint8 // the last filtered one measured
	src      [3]planeSums
	tmp      [16 * 10]uint32
	xy, yy   [100]uint32
	y        [100]uint32
	prod     [256]uint32
}

// planeSums are the weighted sums of the source over each window of a
// plane.
type planeSums struct {
	x, xx [100]uint32
}

// windowWeights are the total weights of the windows of each plane: those
// near the edges of a chroma block lose the weights beyond it.
var windowWeights = func() (w [3][100]uint32) {
	for p, pl := range mbPlanes {
		k := 0
		for cy := pl.lo; cy < pl.hi; cy++ {
			for cx := pl.lo; cx < pl.hi; cx++ {
				w[p][k] = kernelSum(cx, pl.n) * kernelSum(cy, pl.n)
				k++
			}
		}
	}
	return w
}()

// planes: offset in rec, size, and the window centres lo..hi-1 (libwebp's
// GetMBSSIM: centres 3..12 in luma, 1..6 in chroma).
var mbPlanes = [3]struct{ off, n, lo, hi int }{{0, 16, 3, 13}, {256, 8, 1, 7}, {320, 8, 1, 7}}

var ssimK = [7]uint32{1, 2, 3, 4, 3, 2, 1}

// measure adds the SSIM of the macroblock, with the reconstruction in
// rec, at each level tried to the totals of segment seg.
func (m *mbFilter) measure(src *[384]uint8, sg *segment, seg int, st *filterStats) {
	for p, pl := range mbPlanes {
		s := &m.src[p]
		v := src[pl.off : pl.off+pl.n*pl.n]
		for i, x := range v {
			m.prod[i] = uint32(x)
		}
		sepSums(m.prod[:len(v)], pl.n, pl.lo, pl.hi, m.tmp[:], s.x[:])
		for i, x := range v {
			m.prod[i] = uint32(x) * uint32(x)
		}
		sepSums(m.prod[:len(v)], pl.n, pl.lo, pl.hi, m.tmp[:], s.xx[:])
	}
	base := m.ssim(src, &m.rec)
	st[seg][0] += base
	// Levels within the segment's quantizer index of its starting level,
	// four apart.
	level0, d := sg.filter, sg.q
	step := 1
	if 2*d >= 4 {
		step = 4
	}
	// A level that changes nothing, or the same as the level before, has
	// the same SSIM.
	last, lastV := false, 0.0
	for l := level0 - d; l <= level0+d; l += step {
		if l <= 0 || l >= lfLevels {
			continue
		}
		m.flt = m.rec
		switch {
		case !filterInner(&m.flt, l):
			st[seg][l] += base
			last = false
		case last && m.flt == m.prev:
			st[seg][l] += lastV
		default:
			lastV = m.ssim(src, &m.flt)
			st[seg][l] += lastV
			m.prev, last = m.flt, true
		}
	}
}

// ssim sums the SSIM of every window of the three planes of rec against
// src (whose own sums are in m.src).
func (m *mbFilter) ssim(src, rec *[384]uint8) float64 {
	total := 0.0
	for p, pl := range mbPlanes {
		s := &m.src[p]
		a := src[pl.off : pl.off+pl.n*pl.n]
		b := rec[pl.off : pl.off+pl.n*pl.n]
		n2 := len(a)
		for i, y := range b {
			m.prod[i] = uint32(y)
		}
		sepSums(m.prod[:n2], pl.n, pl.lo, pl.hi, m.tmp[:], m.y[:])
		for i, y := range b {
			m.prod[i] = uint32(y) * uint32(y)
		}
		sepSums(m.prod[:n2], pl.n, pl.lo, pl.hi, m.tmp[:], m.yy[:])
		for i, y := range b {
			m.prod[i] = uint32(y) * uint32(a[i])
		}
		sepSums(m.prod[:n2], pl.n, pl.lo, pl.hi, m.tmp[:], m.xy[:])
		k := (pl.hi - pl.lo) * (pl.hi - pl.lo)
		w := &windowWeights[p]
		for i := 0; i < k; i++ {
			total += ssimOf(s.x[i], m.y[i], s.xx[i], m.xy[i], m.yy[i], w[i])
		}
	}
	return total
}

// kernelSum is the sum of the weights of a window centred at c that lie
// within 0..n-1.
func kernelSum(c, n int) uint32 {
	var s uint32
	for d := max(0, c-3); d <= min(n-1, c+3); d++ {
		s += ssimK[d-c+3]
	}
	return s
}

// sepSums sums plane v (n x n) under the window weights for each centre
// (cx, cy) in lo..hi-1, clipping the windows at the plane's edges, into
// out, row by row: across each row, then down each column of that.
func sepSums(v []uint32, n, lo, hi int, tmp, out []uint32) {
	m := hi - lo
	for r := 0; r < n; r++ {
		tri7(v[r*n:r*n+n], lo, hi, tmp[r*m:r*m+m])
	}
	var col [16]uint32
	var res [10]uint32
	for cx := 0; cx < m; cx++ {
		for r := 0; r < n; r++ {
			col[r] = tmp[r*m+cx]
		}
		tri7(col[:n], lo, hi, res[:m])
		for cy := range m {
			out[cy*m+cx] = res[cy]
		}
	}
}

// tri7 sums v (at most 16 values, zero beyond) under the weights 1, 2, 3,
// 4, 3, 2, 1 centred at lo..hi-1. The weights are two boxes of four, one
// after the other, so it takes running sums of four twice.
func tri7(v []uint32, lo, hi int, out []uint32) {
	var p [22]uint32 // three zeros, v, three zeros
	n := copy(p[3:19], v)
	var b [19]uint32 // b[i] = p[i] + ... + p[i+3]
	s := p[0] + p[1] + p[2] + p[3]
	b[0] = s
	for i := 1; i <= n+2; i++ {
		s += p[i+3] - p[i-1]
		b[i] = s
	}
	for c := lo; c < hi; c++ {
		out[c-lo] = b[c] + b[c+1] + b[c+2] + b[c+3]
	}
}

// ssimOf is libwebp's SSIMCalculation: the SSIM of a window from its
// weighted sums and its total weight n.
func ssimOf(xm, ym, xxm, xym, yym, n uint32) float64 {
	w2 := uint64(n) * uint64(n)
	c1, c2, c3 := 20*w2, 60*w2, 64*w2
	xmxm, ymym := uint64(xm)*uint64(xm), uint64(ym)*uint64(ym)
	if xmxm+ymym < c3 {
		return 1 // too dark to matter
	}
	xmym := int64(xm) * int64(ym)
	sxy := int64(xym)*int64(n) - xmym
	sxx := uint64(xxm)*uint64(n) - xmxm
	syy := uint64(yym)*uint64(n) - ymym
	numS := (2*uint64(max(sxy, 0)) + c2) >> 8
	denS := (sxx + syy + c2) >> 8
	fnum := (2*uint64(xmym) + c1) * numS
	fden := (xmxm + ymym + c1) * denS
	return float64(fnum) / float64(fden)
}

// filterInner applies the normal loop filter at level (sharpness 0) to
// the edges inside the macroblock in b, as a decoder does, and reports
// whether any sample changed.
func filterInner(b *[384]uint8, level int) bool {
	ilevel := max(level, 1)
	thresh2 := 2*(2*level+ilevel) + 1
	hev := 0
	if level >= 40 {
		hev = 2
	} else if level >= 15 {
		hev = 1
	}
	changed := false
	// Across the vertical edges, then the horizontal ones.
	for x := 4; x < 16; x += 4 {
		changed = loop24(b[:256], x, 1, 16, 16, thresh2, ilevel, hev) || changed
	}
	changed = loop24(b[256:320], 4, 1, 8, 8, thresh2, ilevel, hev) || changed
	changed = loop24(b[320:], 4, 1, 8, 8, thresh2, ilevel, hev) || changed
	for y := 4; y < 16; y += 4 {
		changed = loop24(b[:256], 16*y, 16, 1, 16, thresh2, ilevel, hev) || changed
	}
	changed = loop24(b[256:320], 32, 8, 1, 8, thresh2, ilevel, hev) || changed
	changed = loop24(b[320:], 32, 8, 1, 8, thresh2, ilevel, hev) || changed
	return changed
}

// loop24 filters size positions along an edge (libwebp's FilterLoop24):
// p is the first sample after the edge, hs the step across it, vs the
// step along it.
func loop24(b []uint8, p, hs, vs, size, thresh2, ilevel, hev int) bool {
	changed := false
	for ; size > 0; size, p = size-1, p+vs {
		p3, p2, p1, p0 := int(b[p-4*hs]), int(b[p-3*hs]), int(b[p-2*hs]), int(b[p-hs])
		q0, q1, q2, q3 := int(b[p]), int(b[p+hs]), int(b[p+2*hs]), int(b[p+3*hs])
		if 4*abs(p0-q0)+abs(p1-q1) > thresh2 {
			continue
		}
		if abs(p3-p2) > ilevel || abs(p2-p1) > ilevel || abs(p1-p0) > ilevel ||
			abs(q3-q2) > ilevel || abs(q2-q1) > ilevel || abs(q1-q0) > ilevel {
			continue
		}
		if abs(p1-p0) > hev || abs(q1-q0) > hev {
			a := 3*(q0-p0) + sclip1(p1-q1)
			a1, a2 := sclip2((a+4)>>3), sclip2((a+3)>>3)
			b[p-hs], b[p] = clip1(p0+a2), clip1(q0-a1)
		} else {
			a := 3 * (q0 - p0)
			a1, a2 := sclip2((a+4)>>3), sclip2((a+3)>>3)
			a3 := (a1 + 1) >> 1
			b[p-2*hs], b[p-hs], b[p], b[p+hs] = clip1(p1+a3), clip1(p0+a2), clip1(q0-a1), clip1(q1-a3)
		}
		changed = changed || b[p-hs] != uint8(p0) || b[p] != uint8(q0) || b[p-2*hs] != uint8(p1) || b[p+hs] != uint8(q1)
	}
	return changed
}

func sclip1(v int) int  { return min(max(v, -128), 127) }
func sclip2(v int) int  { return min(max(v, -16), 15) }
func clip1(v int) uint8 { return uint8(min(max(v, 0), 255)) }
