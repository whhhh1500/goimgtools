package watermark

import (
	"math"
	"slices"
)

// mapping takes a pixel (y, x) to tile coordinates (a, b), in tiles: a
// homography on (y, x, 1), an affine map when its last row is (0, 0, 1).
type mapping [3][3]float64

func affine(A mat2, ca, cb float64) mapping {
	return mapping{{A[0][0], A[0][1], ca}, {A[1][0], A[1][1], cb}, {0, 0, 1}}
}

func (m *mapping) at(y, x float64) (float64, float64) {
	a := float64(m[0][0]*y) + float64(m[0][1]*x) + m[0][2]
	b := float64(m[1][0]*y) + float64(m[1][1]*x) + m[1][2]
	c := float64(m[2][0]*y) + float64(m[2][1]*x) + m[2][2]
	return a / c, b / c
}

// rect is a region of the picture, [y0, y1) x [x0, x1).
type rect struct{ y0, x0, y1, x1 int }

// fold averages the whitened picture over every copy of the tile, in ns x
// ns samples, within a region.
func (d *detector) fold(m *mapping, g rect) []float64 {
	acc := make([]float64, ns*ns)
	if d.foldCnt == nil {
		d.foldCnt = make([]float64, ns*ns)
	}
	cnt := d.foldCnt
	clear(cnt)
	for y := g.y0; y < g.y1; y++ {
		row := d.r[y*d.w : y*d.w+d.w]
		for x := g.x0; x < g.x1; x++ {
			a, b := m.at(float64(y)+0.5, float64(x)+0.5)
			ia := int((a - math.Floor(a)) * ns)
			ib := int((b - math.Floor(b)) * ns)
			if ia >= ns {
				ia = ns - 1
			}
			if ib >= ns {
				ib = ns - 1
			}
			if ia < 0 || ib < 0 {
				continue // not a number: a mapping gone wild
			}
			acc[ia*ns+ib] += row[x]
			cnt[ia*ns+ib]++
		}
	}
	for i := range acc {
		if cnt[i] > 0 {
			acc[i] /= cnt[i]
		}
	}
	return acc
}

func (d *detector) all() rect { return rect{0, 0, d.h, d.w} }

// classes is the number of classes the places of a picture are put in, by
// the energy of the whitened picture around them, to be weighed apart.
const classes = 12

// classOf is each place's class (once for a picture): by the energy of
// the whitened picture within 8 pixels, in classes of equal numbers of
// places.
func (d *detector) classOf() []uint8 {
	if d.class != nil {
		return d.class
	}
	e := make([]float32, len(d.r))
	for i, v := range d.r {
		e[i] = float32(v * v)
	}
	boxClampInPlace(e, d.w, d.h, 8)
	s := make([]float32, 0, len(e)/7+1)
	for i := 0; i < len(e); i += 7 {
		s = append(s, e[i])
	}
	slices.Sort(s)
	var edges [classes - 1]float32
	for c := range edges {
		edges[c] = s[(c+1)*len(s)/classes]
	}
	d.class = make([]uint8, len(e))
	for i, v := range e {
		c, _ := slices.BinarySearch(edges[:], v)
		d.class[i] = uint8(c)
	}
	return d.class
}

// foldWeighed is fold over the whole picture with each class of places
// (classOf) weighed by how much of the mark it holds over its noise, both
// measured on what every tile has (the sync pattern and the reference):
// a matched filter across the picture's parts. Where the picture is flat
// JPEG leaves little of the mark, and where it is busy the picture drowns
// it; the places between hold the most. (Measured with the true tiling:
// the payload's SNR up by 1 to 4 dB on photographs saved as PNG or JPEG
// 70, 8 dB on a screenshot.)
func (d *detector) foldWeighed(m *mapping) ([]float64, []float64) {
	class := d.classOf()
	const nn = ns * ns
	sum := make([]float64, classes*nn)
	cnt := make([]float64, classes*nn)
	for y := range d.h {
		row := d.r[y*d.w : y*d.w+d.w]
		cl := class[y*d.w : y*d.w+d.w]
		for x := range d.w {
			a, b := m.at(float64(y)+0.5, float64(x)+0.5)
			ia := int((a - math.Floor(a)) * ns)
			ib := int((b - math.Floor(b)) * ns)
			if ia >= ns {
				ia = ns - 1
			}
			if ib >= ns {
				ib = ns - 1
			}
			if ia < 0 || ib < 0 {
				continue
			}
			i := int(cl[x])*nn + ia*ns + ib
			sum[i] += row[x]
			cnt[i]++
		}
	}
	// What every tile has, as the fold samples it.
	kt := make([]float64, N*N)
	for i := range kt {
		kt[i] = float64(wSync*d.p.sync[i]) + float64(wRef*d.p.refPat[i])
	}
	kn := make([]float64, nn)
	for i := range ns {
		for j := range ns {
			kn[i*ns+j] = field(kt, float64((float64(i)+0.5)/sub)-0.5, float64((float64(j)+0.5)/sub)-0.5)
		}
	}
	// Each class's gain g (the mean of its places is g times what is known,
	// plus the payload and noise) and noise per place v: weight g / v.
	var weight [classes]float64
	found := false
	for c := range classes {
		s, n := sum[c*nn:][:nn], cnt[c*nn:][:nn]
		var mk, kk float64
		for i := range nn {
			if n[i] > 0 {
				mk += float64(s[i] / n[i] * kn[i])
				kk += float64(kn[i] * kn[i])
			}
		}
		if !(kk > 0) {
			continue
		}
		g := mk / kk
		var v float64
		var used int
		for i := range nn {
			if n[i] > 0 {
				e := s[i]/n[i] - float64(g*kn[i])
				v += float64(float64(e*e) * n[i])
				used++
			}
		}
		v /= float64(used)
		if g > 0 && v > 0 {
			weight[c] = g / v
			found = true
		}
	}
	if !found {
		for c := range weight {
			weight[c] = 1
		}
	}
	f := make([]float64, nn)
	cover := make([]float64, nn)
	for i := range nn {
		var s, n float64
		for c := range classes {
			s += float64(weight[c] * sum[c*nn+i])
			n += float64(weight[c] * cnt[c*nn+i])
		}
		if n > 0 {
			f[i] = s / n
		}
		cover[i] = n
	}
	return f, cover
}

// corrRefWeight is how much of the reference the tiles are placed by,
// with the sync pattern, against its share of the tile. More than none
// makes the placing surer (measured: JPEG 50, a photograph's perspective);
// 0.4 did better than all of it, whose peaks repeat a little across the
// tile. (With the reference at 25% of the tile and 10 to 20 cycles, 0.2
// to 2 read about as many: 239 to 248 marks of 330, 0.4 and 1 alike with
// other keys.)
const corrRefWeight = 0.4

// regionsFrom is the z-score of the whole picture's placing below which
// align does not look region by region, for the tilings the spectrum
// likes less (see quickFrom).
const regionsFrom = 5.0

// quickFrom is the rank, by the spectrum's score, from which a tiling is
// placed quickly (see regionsFrom).
const quickFrom = 3

// known is what every tile of a key has: the sync pattern and the
// reference, in their shares of the tile (the reference's weighed by
// corrRefWeight).
func (d *detector) known() []float64 {
	k := make([]float64, N*N)
	for i := range k {
		k[i] = float64(wSync*d.p.sync[i]) + float64(float64(corrRefWeight*wRef)*d.p.refPat[i])
	}
	return k
}

// syncFFT is the conjugate spectrum of what every tile has (known) as a
// folded tile samples it (bilinear between the chips' centres), turned by
// half a turn for o = 1. Placing the tiles by the reference as well as
// the sync pattern uses more than half of the mark's power.
func (d *detector) syncFFT(o int) []complex64 {
	if d.syncFT[o] != nil {
		return d.syncFT[o]
	}
	kn := d.known()
	a := make([]complex64, ns*ns)
	for i := range ns {
		for j := range ns {
			v := field(kn, float64((float64(i)+0.5)/sub)-0.5, float64((float64(j)+0.5)/sub)-0.5)
			if o == 1 {
				a[(ns-1-i)*ns+(ns-1-j)] = complex(float32(v), 0)
			} else {
				a[i*ns+j] = complex(float32(v), 0)
			}
		}
	}
	fft2(a, ns, ns, false)
	for i, v := range a {
		a[i] = complex(real(v), -imag(v))
	}
	d.syncFT[o] = a
	return a
}

// field is a tile of chips at a chip position, bilinear between the
// chips' centres, wrapping.
func field(t []float64, cy, cx float64) float64 {
	fy, fx := math.Floor(cy), math.Floor(cx)
	dy, dx := cy-fy, cx-fx
	y0, x0 := mod(int(fy), N), mod(int(fx), N)
	y1, x1 := mod(y0+1, N), mod(x0+1, N)
	a := float64(t[y0*N+x0]*(1-dx)) + float64(t[y0*N+x1]*dx)
	b := float64(t[y1*N+x0]*(1-dx)) + float64(t[y1*N+x1]*dx)
	return float64(a*(1-dy)) + float64(b*dy)
}

// corr is the circular correlation of a folded tile with the sync pattern,
// as z-scores.
func (d *detector) corr(f []float64, o int) []float64 {
	if d.corrBuf == nil {
		d.corrBuf = make([]complex64, ns*ns)
	}
	a := d.corrBuf
	for i, v := range f {
		a[i] = complex(float32(v), 0)
	}
	fft2(a, ns, ns, false)
	s := d.syncFFT(o)
	for i := range a {
		a[i] = cmul(a[i], s[i])
	}
	fft2(a, ns, ns, true)
	c := make([]float64, ns*ns)
	var mean float64
	for i, v := range a {
		c[i] = float64(real(v))
		mean += c[i]
	}
	mean = float64(mean / float64(len(c)))
	var sq float64
	for _, v := range c {
		sq += float64((v - mean) * (v - mean))
	}
	std := math.Sqrt(sq/float64(len(c))) + 1e-12
	for i := range c {
		c[i] = (c[i] - mean) / std
	}
	return c
}

// peak is the largest correlation, within radius samples (circularly) of
// (ny, nx) when radius > 0.
func peak(c []float64, ny, nx, radius int) (float64, int, int) {
	best, by, bx := math.Inf(-1), 0, 0
	for y := range ns {
		for x := range ns {
			if radius > 0 {
				dy, dx := mod(y-ny+ns/2, ns)-ns/2, mod(x-nx+ns/2, ns)-ns/2
				if dy*dy+dx*dx > radius*radius {
					continue
				}
			}
			if v := c[y*ns+x]; v > best {
				best, by, bx = v, y, x
			}
		}
	}
	return best, by, bx
}

// subpeak is the peak's position to a fraction of a sample.
func subpeak(c []float64, y, x int) (float64, float64) {
	at := func(y, x int) float64 { return c[mod(y, ns)*ns+mod(x, ns)] }
	v := at(y, x)
	return float64(y) + parabola(v, at(y-1, x), at(y+1, x)), float64(x) + parabola(v, at(y, x-1), at(y, x+1))
}

// wrapHalf brings a shift in samples to [-ns/2, ns/2).
func wrapHalf(v float64) float64 {
	return v - float64(ns*math.Floor((v+ns/2)/ns))
}

// align places the tiling: the sync pattern found in the whole picture
// (and which way round), the tile's steps corrected from the shift between
// halves of the picture, then region by region, which fits a homography
// (a photograph's perspective) or an affine map. It returns the mapping
// and the sync pattern's z-score.
func (d *detector) align(B mat2) (mapping, float64) {
	h, w := d.h, d.w
	A := mat2{{B[0][0], B[1][0]}, {B[0][1], B[1][1]}} // tile coordinates per pixel: B transposed
	m := affine(A, 0, 0)
	f := d.fold(&m, d.all())
	z0, y0, x0 := peak(d.corr(f, 0), 0, 0, 0)
	if z1, _, _ := peak(d.corr(f, 1), 0, 0, 0); z1 > z0 {
		// Half a turn: the tile's axes the other way round.
		for i := range 2 {
			for j := range 2 {
				A[i][j] = -A[i][j]
			}
		}
		m = affine(A, 0, 0)
		f = d.fold(&m, d.all())
		z0, y0, x0 = peak(d.corr(f, 0), 0, 0, 0)
	}
	for range 4 {
		var E mat2 // d(tile coordinates)/d(pixel)
		found := false
		for axis := range 2 {
			var r1, r2 rect
			var dist float64
			if axis == 0 {
				r1, r2, dist = rect{0, 0, h / 2, w}, rect{h / 2, 0, h, w}, float64(h)/2
			} else {
				r1, r2, dist = rect{0, 0, h, w / 2}, rect{0, w / 2, h, w}, float64(w)/2
			}
			c1 := d.corr(d.fold(&m, r1), 0)
			za, ya, xa := peak(c1, 0, 0, 0)
			c2 := d.corr(d.fold(&m, r2), 0)
			zb, yb, xb := peak(c2, 0, 0, 0)
			if min(za, zb) < 4.5 {
				continue
			}
			sya, sxa := subpeak(c1, ya, xa)
			syb, sxb := subpeak(c2, yb, xb)
			E[0][axis] = float64(wrapHalf(syb-sya)/ns) / dist
			E[1][axis] = float64(wrapHalf(sxb-sxa)/ns) / dist
			found = true
		}
		if !found {
			break
		}
		for i := range 2 {
			for j := range 2 {
				A[i][j] -= E[i][j]
			}
		}
		m = affine(A, 0, 0)
		f = d.fold(&m, d.all())
		z0, y0, x0 = peak(d.corr(f, 0), 0, 0, 0)
		if max(math.Abs(E[0][0]), math.Abs(E[0][1]), math.Abs(E[1][0]), math.Abs(E[1][1]))*float64(max(h, w)) < 0.1/N {
			break
		}
	}
	sy, sx := subpeak(d.corr(f, 0), y0, x0)
	m = affine(A, -float64(sy/ns), -float64(sx/ns))
	if d.quick && z0 < regionsFrom {
		// Most likely a wrong tiling (about 4): the regions would find
		// nothing, and are most of the time spent on it. (A tiling the
		// spectrum likes best is always looked at region by region: a
		// photograph's perspective can leave the whole picture scoring
		// that low until its regions are placed.)
		return m, z0
	}
	tilePx := math.Sqrt(1 / math.Abs(float64(A[0][0]*A[1][1])-float64(A[0][1]*A[1][0])))
	// Region by region, as long as it helps: regions of about two tiles
	// each way (three when too few of those find the pattern), each
	// placed against the mapping so far.
	z := z0
	for range 4 {
		var m2 mapping
		ok := false
		for _, tiles := range [2]float64{2, 3} {
			if m2, ok = d.regions(&m, max(64, int(tiles*tilePx))); ok {
				break
			}
		}
		if !ok {
			break
		}
		c2 := d.corr(d.fold(&m2, d.all()), 0)
		z2, py, px := peak(c2, 0, 0, 0)
		if z2 <= z+0.05 {
			break
		}
		dy, dx := subpeak(c2, py, px)
		dy, dx = float64(wrapHalf(dy)/ns), float64(wrapHalf(dx)/ns)
		for j := range 3 {
			m2[0][j] -= float64(dy * m2[2][j])
			m2[1][j] -= float64(dx * m2[2][j])
		}
		m, z = m2, z2
	}
	return m, z
}

// regions finds the sync pattern in regions of reg pixels, overlapping by
// half, near where m puts it, and fits a homography to where it is (six
// regions or more) or an affine map (three to five).
func (d *detector) regions(m *mapping, reg int) (mapping, bool) {
	h, w := d.h, d.w
	var grid []rect
	for ry := 0; ry < max(1, h-reg/2); ry += reg / 2 {
		for rx := 0; rx < max(1, w-reg/2); rx += reg / 2 {
			g := rect{ry, rx, min(h, ry+reg), min(w, rx+reg)}
			if 2*(g.y1-g.y0)*(g.x1-g.x0) >= reg*reg {
				grid = append(grid, g)
			}
		}
	}
	type hit struct {
		found    bool
		pts, tgt [2]float64
	}
	hits := make([]hit, len(grid))
	d.parallel(len(grid), func(i int) {
		g, dd := grid[i], d.worker()
		c := dd.corr(dd.fold(m, g), 0)
		zr, py, px := peak(c, 0, 0, ns/6)
		if zr < 5 {
			return
		}
		dy, dx := subpeak(c, py, px)
		cy, cx := float64(g.y0+g.y1)/2, float64(g.x0+g.x1)/2
		a, b := m.at(cy, cx)
		hits[i] = hit{true, [2]float64{cy, cx}, [2]float64{a - float64(wrapHalf(dy)/ns), b - float64(wrapHalf(dx)/ns)}}
	})
	var pts, tgt [][2]float64
	for _, ht := range hits {
		if ht.found {
			pts = append(pts, ht.pts)
			tgt = append(tgt, ht.tgt)
		}
	}
	switch {
	case len(pts) >= 6:
		return fitHomography(pts, tgt)
	case len(pts) >= 3:
		return fitAffine(pts, tgt)
	}
	return mapping{}, false
}

// fitAffine is the affine map, by least squares, taking pts to tgt.
func fitAffine(pts, tgt [][2]float64) (mapping, bool) {
	var ata [3][3]float64
	var atb [3][2]float64
	for i, p := range pts {
		row := [3]float64{p[0], p[1], 1}
		for a := range 3 {
			for b := range 3 {
				ata[a][b] += float64(row[a] * row[b])
			}
			atb[a][0] += float64(row[a] * tgt[i][0])
			atb[a][1] += float64(row[a] * tgt[i][1])
		}
	}
	var m mapping
	for c := range 2 {
		sys := make([][]float64, 3)
		for a := range 3 {
			sys[a] = []float64{ata[a][0], ata[a][1], ata[a][2], atb[a][c]}
		}
		sol, ok := solve(sys)
		if !ok {
			return m, false
		}
		m[c] = [3]float64{sol[0], sol[1], sol[2]}
	}
	m[2] = [3]float64{0, 0, 1}
	return m, true
}

// fitHomography is the homography, by least squares (its last entry 1),
// taking pts to tgt, worked out on coordinates centred and scaled.
func fitHomography(pts, tgt [][2]float64) (mapping, bool) {
	var cy, cx float64
	for _, p := range pts {
		cy += p[0]
		cx += p[1]
	}
	cy /= float64(len(pts))
	cx /= float64(len(pts))
	var sc float64
	for _, p := range pts {
		sc = max(sc, math.Abs(p[0]-cy), math.Abs(p[1]-cx))
	}
	if sc == 0 {
		return mapping{}, false
	}
	var ata [8][8]float64
	var atb [8]float64
	add := func(row [8]float64, v float64) {
		for a := range 8 {
			for b := range 8 {
				ata[a][b] += float64(row[a] * row[b])
			}
			atb[a] += float64(row[a] * v)
		}
	}
	for i, p := range pts {
		y, x := (p[0]-cy)/sc, (p[1]-cx)/sc
		v, u := tgt[i][0], tgt[i][1]
		add([8]float64{y, x, 1, 0, 0, 0, -float64(v * y), -float64(v * x)}, v)
		add([8]float64{0, 0, 0, y, x, 1, -float64(u * y), -float64(u * x)}, u)
	}
	sys := make([][]float64, 8)
	for a := range 8 {
		sys[a] = append(ata[a][:], atb[a])
	}
	h, ok := solve(sys)
	if !ok {
		return mapping{}, false
	}
	// H' acts on ((y - cy)/sc, (x - cx)/sc, 1): fold the centring and
	// scaling into it.
	hp := mapping{{h[0], h[1], h[2]}, {h[3], h[4], h[5]}, {h[6], h[7], 1}}
	var m mapping
	for r := range 3 {
		m[r][0] = hp[r][0] / sc
		m[r][1] = hp[r][1] / sc
		m[r][2] = hp[r][2] - float64(hp[r][0]*cy)/sc - float64(hp[r][1]*cx)/sc
	}
	return m, true
}

// solve solves the linear system of the augmented rows by Gaussian
// elimination with partial pivoting.
func solve(a [][]float64) ([]float64, bool) {
	n := len(a)
	for c := range n {
		p := c
		for r := c + 1; r < n; r++ {
			if math.Abs(a[r][c]) > math.Abs(a[p][c]) {
				p = r
			}
		}
		if math.Abs(a[p][c]) < 1e-300 {
			return nil, false
		}
		a[c], a[p] = a[p], a[c]
		for r := c + 1; r < n; r++ {
			f := a[r][c] / a[c][c]
			for k := c; k <= n; k++ {
				a[r][k] -= float64(f * a[c][k])
			}
		}
	}
	x := make([]float64, n)
	for r := n - 1; r >= 0; r-- {
		s := a[r][n]
		for k := r + 1; k < n; k++ {
			s -= float64(a[r][k] * x[k])
		}
		x[r] = s / a[r][r]
	}
	return x, true
}

// decode reads the payload from the folded picture. What every tile has
// (the sync pattern and the reference) shows how the copy changed the
// mark at each frequency (blurred by resampling and compression): that
// much of it is taken out, and the rest, the payload as changed and
// noise, weighed by the same response (a matched filter), gives each
// coded bit's soft value, for every length of payload until one's check
// matches.
func (d *detector) decode(m mapping) ([]byte, bool) {
	chips := d.payloadChips(m)
	softOf := func(n int) []float64 {
		ncoded := codedLen(n)
		which, sign := spreading(d.k, ncoded)
		soft := make([]float64, ncoded)
		for i, v := range chips {
			soft[which[i]] += float64(v * sign[i])
		}
		return soft
	}
	// The likeliest few sequences of every length (later ones often pass
	// the check when the first does not); the lengths whose best path fits
	// the soft values best (its metric over the size of the soft values)
	// are then searched further. (In 9 of 10 marks read where about half
	// are, the true length fits best.)
	type fit struct {
		n     int
		score float64
	}
	var fits []fit
	var t *trellis
	for n := 1; n <= MaxPayload; n++ {
		soft := softOf(n)
		t = forward(t, soft, 8*(n+4))
		for _, bits := range t.list(listFirst) {
			if payload, ok := unframe(bits, d.k); ok {
				return payload, true
			}
		}
		var sq float64
		for _, v := range soft {
			sq += float64(v * v)
		}
		if sq > 0 {
			fits = append(fits, fit{n, t.best() / math.Sqrt(float64(float64(len(soft))*sq))})
		}
	}
	slices.SortStableFunc(fits, func(a, b fit) int {
		if a.score > b.score {
			return -1
		}
		if a.score < b.score {
			return 1
		}
		return 0
	})
	for _, f := range fits[:min(listLengths, len(fits))] {
		t = forward(t, softOf(f.n), 8*(f.n+4))
		list := t.list(listSize)
		for _, bits := range list[min(listFirst, len(list)):] {
			if payload, ok := unframe(bits, d.k); ok {
				return payload, true
			}
		}
	}
	return nil, false
}

// decode checks listFirst sequences of every payload length, then
// listSize of the listLengths lengths that fit best. Every sequence
// checked is a chance of 2^-32 of taking noise for a mark: with these,
// 32 x 4 + 4 x 12 = 176 for a tiling, about 4 in 100 million.
const (
	listFirst   = 4
	listLengths = 4
	listSize    = 16
)

// payloadChips is the folded picture's chips with what every tile has
// taken out and the rest weighed for the payload (see decode).
func (d *detector) payloadChips(m mapping) []float64 {
	f, cover := d.foldWeighed(&m)
	a := make([]complex64, N*N)
	for cy := range N {
		for cx := range N {
			var s float64
			for i := range sub {
				for j := range sub {
					s += f[(sub*cy+i)*ns+sub*cx+j]
				}
			}
			a[cy*N+cx] = complex(float32(s/(sub*sub)), 0)
		}
	}
	fft2(a, N, N, false)
	kn := make([]complex64, N*N)
	for i := range kn {
		kn[i] = complex(float32(float64(wSync*d.p.sync[i])+float64(wRef*d.p.refPat[i])), 0)
	}
	fft2(kn, N, N, false)
	// The response by distance from the centre of the spectrum: the
	// least-squares gain of what is known there.
	const rings = N
	var num, den [rings]float64
	ring := func(i int) int {
		fy, fx := i/N, i%N
		fy, fx = min(fy, N-fy), min(fx, N-fx)
		return min(rings-1, int(math.Sqrt(float64(fy*fy+fx*fx))+0.5))
	}
	for i := range a {
		c, k := a[i], kn[i]
		r := ring(i)
		num[r] += float64(float64(real(c))*float64(real(k))) + float64(float64(imag(c))*float64(imag(k)))
		den[r] += float64(float64(real(k))*float64(real(k))) + float64(float64(imag(k))*float64(imag(k)))
	}
	var gain [rings]float64
	for r := 1; r < rings; r++ {
		if den[r] > 0 {
			gain[r] = max(0, num[r]/den[r])
		}
	}
	// What is left, the payload as changed and noise, is weighed at each
	// ring by its gain over the power left there: the matched filter for
	// noise that is stronger at some frequencies (JPEG's) than others.
	// (Measured worse, as noisier estimates of the noise alone: the power
	// left less the payload's expected share, and the difference of the
	// folds of alternate tiles; the payload's SNR down by up to 4.6 dB.
	// The noise of each bin over the 5 x 5 or 7 x 7 around it instead of
	// rings read as many after JPEG and WebP.)
	var left [rings]float64
	var bins [rings]int
	for i := range a {
		g := float32(gain[ring(i)])
		re := real(a[i]) - float32(g*real(kn[i]))
		im := imag(a[i]) - float32(g*imag(kn[i]))
		a[i] = complex(re, im)
		r := ring(i)
		left[r] += float64(float64(re)*float64(re)) + float64(float64(im)*float64(im))
		bins[r]++
	}
	var weight [rings]float32
	for r := 1; r < rings; r++ {
		if left[r] > 0 {
			weight[r] = float32(gain[r] / (left[r] / float64(bins[r])))
		}
	}
	for i := range a {
		wt := weight[ring(i)]
		a[i] = complex(real(a[i])*wt, imag(a[i])*wt)
	}
	fft2(a, N, N, true)
	chips := make([]float64, N*N)
	for i, v := range a {
		chips[i] = float64(real(v))
	}
	// Each chip weighed by how much was folded into it: in a crop some
	// chips come from more copies of the tile than others, and are surer.
	// (Measured on crops of the Kodak pictures with 6 keys, 432 crops of
	// 40%, 30% and 25% of each side: 193 marks read against 179.)
	var mean float64
	folded := make([]float64, N*N)
	for cy := range N {
		for cx := range N {
			var s float64
			for i := range sub {
				for j := range sub {
					s += cover[(sub*cy+i)*ns+sub*cx+j]
				}
			}
			folded[cy*N+cx] = s
			mean += s
		}
	}
	mean /= N * N
	if mean > 0 {
		for i := range chips {
			chips[i] *= folded[i] / mean
		}
	}
	return chips
}
