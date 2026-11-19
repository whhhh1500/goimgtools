package watermark

import (
	"math"
	"slices"
	"sync/atomic"
)

// WorkingSide is the most pixels along the shorter side, and twice that
// along the longer, of the picture Detect is given: a larger one is to be
// reduced first (by averaging), which keeps the mark.
const WorkingSide = 1024

const (
	sub = 2       // samples of a folded tile per chip, each way
	ns  = sub * N // samples of a folded tile along a side
)

// Parallel runs fn(0), fn(1), …, fn(n-1), each once, at the same time as
// far as it can, and returns when all have returned (pipeline.Env's
// Parallel). Detect's result is the same with any.
type Parallel func(n int, fn func(i int))

// Detect looks for a mark made with the key in the luminance of a picture
// (w x h values, 0 to 255, rows top to bottom) and returns its payload.
// par, if not nil, shares the work out; the result does not depend on it.
func Detect(lum []float64, w, h int, k Key, par Parallel) ([]byte, bool) {
	if w < 32 || h < 32 {
		return nil, false
	}
	p := newPatterns(k)
	// The detectors keep par as it is: nil, they work alone and share their
	// scratch.
	d := &detector{k: k, p: p, w: w, h: h, par: par}
	par = d.parallel
	// The whole picture's residual first: the windows below take theirs
	// from it.
	d.residual(lum)
	// The tiling is looked for in the whole picture and in its middle,
	// half as wide and high: in a photograph, perspective changes the
	// tile's size and angle across the picture, which spreads the
	// reference's peaks over the whole and much less over the middle.
	type found struct {
		c  candidate
		in *detector
	}
	// within searches the windows of half the width and height at these
	// places, each in a detector of its own, and returns the best of what
	// they find, by the spectrum's score.
	cw, ch := w/2, h/2
	within := func(at [][2]int, n int) []found {
		searched := make([][]found, len(at))
		if cw >= 128 && ch >= 128 {
			par(len(at), func(i int) {
				dw := &detector{k: k, p: p, w: cw, h: ch, par: d.par}
				dw.prepare(d.window(at[i][1], at[i][0], ch, cw))
				for _, c := range dw.search() {
					searched[i] = append(searched[i], found{c, dw})
				}
				// Only its spectrum is wanted from here on (polish, lsq).
				dw.magMax = nil
			})
		}
		var all []found
		for _, s := range searched {
			all = append(all, s...)
		}
		byZ(all, func(f found) float64 { return f.c.z })
		return all[:min(n, len(all))]
	}
	var whole, middle []found
	par(2, func(i int) {
		if i == 0 {
			d.prepare(d.r)
			for _, c := range d.search() {
				whole = append(whole, found{c, d})
			}
			return
		}
		middle = within([][2]int{{w / 4, h / 4}}, 4)
	})
	cands := append(whole, middle...)
	byZ(cands, func(f found) float64 { return f.c.z })
	// What the candidates share is worked out before they are shared out.
	d.classOf()
	d.syncFFT(0)
	d.syncFFT(1)
	// The quick way for every candidate: the tiling from the spectrum,
	// placed by the sync pattern, its halves and regions. The first by
	// rank whose payload checks is the answer: candidates are taken in
	// order, and none is begun after a better-ranked one has been read.
	type placed struct {
		B       mat2
		m       mapping
		z       float64
		ok      bool // placed well enough to try (z of 4.5 or more)
		payload []byte
		read    bool
	}
	var tried []placed
	// quickly places cands (those ranked from on, quickly: see quickFrom)
	// and reads them.
	quickly := func(cands []found, from int) ([]byte, bool) {
		out := make([]placed, len(cands))
		var first atomic.Int64
		first.Store(int64(len(cands)))
		par(len(cands), func(rank int) {
			if int64(rank) > first.Load() {
				return
			}
			f := cands[rank]
			dd := d.worker()
			dd.quick = rank >= from
			B := f.in.polish(f.c)
			if b2, ok := f.in.lsq(B); ok {
				B = b2
			}
			det := float64(B[0][0]*B[1][1]) - float64(B[0][1]*B[1][0])
			if !(math.Abs(det) > 1e-12) {
				return
			}
			// The tile's sides in pixels: the columns of the inverse of B's
			// transpose.
			uy, ux := B[1][1]/det, -B[1][0]/det
			vy, vx := -B[0][1]/det, B[0][0]/det
			if math.Hypot(uy, ux) > 2*float64(max(w, h)) || math.Hypot(vy, vx) > 2*float64(max(w, h)) {
				return
			}
			m, z := dd.align(B)
			if z < 4.5 {
				return
			}
			o := placed{B: B, m: m, z: z, ok: true}
			o.payload, o.read = dd.decode(m)
			out[rank] = o
			if o.read {
				for cur := first.Load(); int64(rank) < cur && !first.CompareAndSwap(cur, int64(rank)); cur = first.Load() {
				}
			}
		})
		for _, o := range out {
			if o.read {
				return o.payload, true
			}
			if o.ok {
				tried = append(tried, o)
			}
		}
		return nil, false
	}
	if payload, ok := quickly(cands, quickFrom); ok {
		return payload, true
	}
	// The slower ways for the two tilings the sync pattern liked best,
	// where it was found at all (a wrong tiling scores about 4): the
	// whole mapping adjusted for the sync pattern, and a photograph's
	// perspective from the tile's size around the middle; in that order.
	slices.SortStableFunc(tried, func(a, b placed) int {
		if a.z > b.z {
			return -1
		}
		if a.z < b.z {
			return 1
		}
		return 0
	})
	var best []placed
	for _, t := range tried[:min(2, len(tried))] {
		if t.z < 6 {
			break
		}
		best = append(best, t)
	}
	type result struct {
		payload []byte
		read    bool
	}
	slow := make([]result, 2*len(best))
	par(len(slow), func(j int) {
		t, dd := best[j/2], d.worker()
		if j%2 == 0 {
			// Read whatever its score: adjusted to a fraction of a pixel, the
			// payload stands out more even where the sync pattern hardly
			// does (0.3 to 1 dB on the Kodak photographs turned, resized or
			// untouched). (Measured with 3 keys on the 24 Kodak photographs at
			// Strength 0.75 after 18 attacks: 1188 readings of 1296 against
			// 1184 when read only with the sync pattern 0.5 higher.)
			m2, _ := dd.optimize(t.m)
			slow[j].payload, slow[j].read = dd.decode(m2)
		} else if mp, ok := dd.perspective(t.B); ok {
			if m3, z3 := dd.optimize(dd.orient(mp)); z3 >= 4.5 {
				slow[j].payload, slow[j].read = dd.decode(m3)
			}
		}
	})
	for _, r := range slow {
		if r.read {
			return r.payload, true
		}
	}
	// Last, in the four corners, half as wide and high, for a mark that
	// is left in only part of the picture (a screenshot whose flat parts
	// were compressed away, the sharper part of a photograph): the best four
	// tilings found there, placed quickly. (Measured with the 22 attacks on 5
	// pictures with 3 keys: 276 marks of 330 read against 271, the screen
	// mark 293 against 288, most of them photographs of a screen. More of the
	// corners' tilings, or the best placed region by region too, read no more
	// and took longer where nothing is found; searched with the rest at first,
	// the corners took a marked picture 76% longer to read.)
	corners := [][2]int{{0, 0}, {w - cw, 0}, {0, h - ch}, {w - cw, h - ch}}
	if payload, ok := quickly(within(corners, 4), 0); ok {
		return payload, true
	}
	// Last, in a picture of fewer than three tiles along its shorter side,
	// where the spectrum hardly shows the tiling, the tilings a crop of a
	// small picture keeps: the tile of each smallest chip (see
	// nativePeriods), not turned. (Measured with 3 keys on the 24 Kodak
	// photographs marked at Strength 0.75 and cropped to 40% and 25% of
	// each side: 115 of 144 against 104; crops of 136 x 100 and 128 x 96,
	// under a tile, of 4 pictures of 512 x 384 at Strength 0.75 and 1, as
	// they are and after JPEG 90, 48 of 48 against 22. With other keys,
	// none of 864 attacked photographs; reading the 24 photographs
	// unmarked took 2% longer.)
	var native []float64
	for _, p := range nativePeriods {
		if float64(min(w, h)) < 3*p {
			native = append(native, p)
		}
	}
	got := make([][]byte, len(native))
	par(len(native), func(j int) {
		dd, p := d.worker(), native[j]
		m, z := dd.align(mat2{{1 / p, 0}, {0, 1 / p}})
		if z < 4.5 {
			return
		}
		payload, ok := dd.decode(m)
		if !ok {
			m2, _ := dd.optimize(m)
			payload, ok = dd.decode(m2)
		}
		if ok {
			got[j] = payload
		}
	})
	for _, payload := range got {
		if payload != nil {
			return payload, true
		}
	}
	return nil, false
}

// nativePeriods are the tile's sides, in pixels, with the smallest chips
// a mark is made with: of a pixel (Blind on a small busy picture, see
// smallSide), and each kind's minChip.
var nativePeriods = [...]float64{N, N * kinds[Blind].minChip, N * kinds[Screen].minChip}

// byZ sorts v by score, best first, ties kept in order.
func byZ[T any](v []T, z func(T) float64) {
	slices.SortStableFunc(v, func(a, b T) int {
		za, zb := z(a), z(b)
		if za > zb {
			return -1
		}
		if za < zb {
			return 1
		}
		return 0
	})
}

type detector struct {
	k      Key
	p      *patterns
	w, h   int
	r      []float64 // the picture, whitened and evened out
	hp, wp int       // the size of its spectrum (powers of two)
	mag    []float32 // its spectrum's magnitude over its median
	magMax []float32 // the largest of mag over 3 x 3 bins
	magBox []float32 // the mean of mag over 3 x 3 bins
	syncFT [2][]complex64
	// quick: align leaves out the regions when the whole picture
	// scores low.
	quick bool
	// Scratch of fold and corr, which are called a few thousand times.
	foldCnt []float64
	corrBuf []complex64
	class   []uint8  // see classOf
	par     Parallel // nil: one thing at a time
}

// parallel runs fn(0) to fn(n-1) with d's Parallel, or one after another.
func (d *detector) parallel(n int, fn func(i int)) {
	if d.par == nil {
		for i := range n {
			fn(i)
		}
		return
	}
	d.par(n, fn)
}

// worker is d for one goroutine: what d has worked out is shared, read
// only, and the scratch is its own. Without a Parallel it is d.
func (d *detector) worker() *detector {
	if d.par == nil {
		return d
	}
	c := *d
	c.foldCnt, c.corrBuf = nil, nil
	return &c
}

// evenRadius is the radius of the window evenOut evens the picture out
// over, evenFloor the share of the median of its local sizes added to each
// one, and whitenRadius the radius of the bins spectrum divides the
// spectrum by. A small window with little of a floor lets the flat parts
// of a damaged copy count fully, and a smoother spectrum keeps the
// reference's peaks standing out. (Measured with the picture whitened as a
// whole, with the 22 attacks on 5 pictures with 3 keys, against 8, 1 and
// 3: 271 marks of 330 read against 244, with 3 other keys 271 against 243,
// the screen mark 288 against 279. Floors of 0.125 and 0.5: 273 and 264;
// with whitenRadius 3, windows of 2, 4, 8 and 16: 264, 263, 259 and 239;
// with a window of 4, whitenRadius 5, 10 and 15: 271, 257 and 240. On the
// 24 Kodak photographs, not used to choose them, after 18 attacks: 400
// readings of 432 against 393, crops to 40% 18 against 14; the blind mark
// at Strength 0.6 320 against 289. Whitened block by block, windows of 2
// to 8 and floors of 0.125 to 0.5 kept the payload's SNR within 0.2 dB.)
const (
	evenRadius   = 3
	evenFloor    = 0.25
	whitenRadius = 7
)

// prepare works out the spectrum the tiling is looked for in: that of the
// residual (see residual), whitened.
func (d *detector) prepare(r []float64) {
	mag := d.spectrum(r)
	if m := float32(median(mag)); m > 0 {
		for i := range mag {
			mag[i] /= m
		}
	}
	d.mag = mag
	d.magMax = maxWrap3(mag, d.wp, d.hp)
	d.magBox = boxWrap3(mag, d.wp, d.hp)
}

// residual works out what the tiles are folded from (r), and the spectrum
// is taken of: the picture whitened block by block (localWhiten), evened
// out, and whitened block by block again. A photograph's texture runs
// different ways in different places, which a block's own spectrum takes
// out and the whole picture's does not; a photograph of a screen's moiré,
// strong at a few frequencies, it takes down to the rest. (Measured with
// 3 keys on the 24 Kodak photographs marked at Strength 0.75, after the 18
// attacks made with Pillow: 1246 readings of 1296 against 1217 with the
// picture whitened as a whole, cropped to 25% 42 against 30, to 40% 62
// against 51, WebP 60 72 against 67, made 70% and JPEG 80 72 against 71;
// on 5 other pictures, among them a screenshot and a photograph mostly of
// a smooth background, after 13 attacks, 159 against 154, none fewer. With
// the mapping found, blocks of 16, 64 and 128 kept less of the payload, as
// did the picture whitened as a whole first. The spectrum of the picture
// whitened as a whole, with the tiles folded from this, read 1239 and 155.)
func (d *detector) residual(lum []float64) {
	r := d.localWhiten(lum, localFloorFirst)
	d.evenOut(r)
	d.r = d.localWhiten(r, localFloorSecond)
}

// window is the residual of the h x w window at (y, x), as the whole
// picture's: block by block, it is the window's own but near its edges.
func (d *detector) window(y, x, h, w int) []float64 {
	o := make([]float64, w*h)
	for j := range h {
		copy(o[j*w:], d.r[(y+j)*d.w+x:][:w])
	}
	return o
}

// evenOut divides each place by the size of the picture around it, so
// that flat areas, where the whitened picture is mostly the mark, count as
// much as busy ones.
func (d *detector) evenOut(r []float64) {
	loc := make([]float32, len(r))
	for i, v := range r {
		loc[i] = float32(v * v)
	}
	boxClampInPlace(loc, d.w, d.h, evenRadius)
	for i, v := range loc {
		loc[i] = float32(math.Sqrt(float64(v)))
	}
	med := median(loc)
	for i := range r {
		// Nothing to even out where the picture is flat all round.
		if s := float64(loc[i]) + float64(evenFloor*med); s > 0 {
			r[i] /= s
		} else {
			r[i] = 0
		}
	}
}

// localBlock is the side of the blocks localWhiten works on (a power of
// two), and localFloorFirst and localFloorSecond its floor in residual's
// first pass and second. Without a floor a flat part's little noise, all
// that JPEG 30 leaves of a smooth background, is made as loud as the rest;
// too much of one, and the blocks are whitened alike. (Measured as in
// residual, with the spectrum of the picture whitened as a whole: floors
// of 0, 0.25, 2, 4 and 8 in both passes read 156, 157, 154, 153 and 154 of
// the 5 pictures and 1236, 1239, 1240, 1237 and 1231 of the Kodak
// photographs, all with fewer after JPEG 30 or WebP 60 than the picture
// whitened as a whole; 0.5 and 4, 1 and 8, 2 and 4 and 0.5 and 8 156,
// 155, 154 and 154 and 1239 to 1240, none fewer. With the spectrum of
// this, 0.25 and 1 read 165 and 1254, but one fewer after JPEG 30 and
// after WebP 60; 0.5 and 2 163 and 1250, the same.)
const (
	localBlock       = 32
	localFloorFirst  = 1.0
	localFloorSecond = 4.0
)

// localWhiten returns y whitened block by block: blocks of localBlock,
// overlapping by half, each windowed by a sine on the way in and again on
// the way out (the products sum to one), its mean taken out and its
// spectrum divided by its own mean magnitude over 3 x 3 bins plus floor
// times that mean over the blocks (Welch's method, over every other row of
// them); the picture mirrored past its edges. Two blocks side by side go
// through one complex transform, as its real and imaginary parts. Rows of
// blocks two apart do not overlap: the even ones, then the odd ones, are
// added in, so the sum is the same however the work is shared out.
func (d *detector) localWhiten(y []float64, floor float64) []float64 {
	w, h := d.w, d.h
	const bs, hop, n = localBlock, localBlock / 2, localBlock * localBlock
	var win [bs]float64
	for i := range win {
		win[i] = math.Sin(float64(math.Pi*(float64(i)+0.5)) / bs)
	}
	plan := newFFTPlan(bs)
	rows, cols := (h+hop-1)/hop+1, (w+hop-1)/hop+1
	type scratch struct {
		a        []complex64
		col      []complex64
		blk      [2][n]float32
		sm       [2][n]float32
		mag, tmp [n]float32
	}
	newScratch := func() *scratch {
		return &scratch{a: make([]complex64, n), col: make([]complex64, bs)}
	}
	// load transforms blocks c and c+1 of the row of blocks from by (none
	// past the last), their means out, windowed, and leaves in s.sm their
	// smoothed magnitudes: the two spectra, each twice its size, are X[f] =
	// Z[f] + conj(Z[-f]) and Y[f] = -i (Z[f] - conj(Z[-f])).
	load := func(s *scratch, by, c int) {
		for p := range 2 {
			if c+p == cols {
				clear(s.blk[p][:])
				continue
			}
			bx := (c+p)*hop - hop
			var mean float64
			for j := range bs {
				src := y[mirror(by+j, h)*w:][:w]
				for i := range bs {
					v := src[mirror(bx+i, w)]
					s.blk[p][j*bs+i] = float32(v)
					mean += v
				}
			}
			mean = float64(mean / n)
			for j := range bs {
				for i := range bs {
					v := float64(s.blk[p][j*bs+i]) - mean
					s.blk[p][j*bs+i] = float32(float64(float64(v*win[j]) * win[i]))
				}
			}
		}
		for i := range s.a {
			s.a[i] = complex(s.blk[0][i], s.blk[1][i])
		}
		fft32(s.a, s.col, plan, false)
		for p := range 2 {
			for f := range s.a {
				g := (bs-f/bs)%bs*bs + (bs-f%bs)%bs
				zr, zi, gr, gi := real(s.a[f]), imag(s.a[f]), real(s.a[g]), imag(s.a[g])
				re, im := zr+gr, zi-gi
				if p == 1 {
					re, im = zi+gi, gr-zr
				}
				s.mag[f] = float32(math.Sqrt(float64(float32(re*re) + float32(im*im))))
			}
			box3Wrap(&s.sm[p], &s.mag, &s.tmp)
		}
	}
	// The mean of the smoothed magnitudes, over the even rows of blocks
	// (Welch's method), summed row by row and then in order.
	var base [n]float32
	if floor > 0 {
		half := (rows + 1) / 2
		sums := make([][n]float64, half)
		counts := make([]int, half)
		d.parallel(half, func(k int) {
			s := newScratch()
			for c := 0; c < cols; c += 2 {
				load(s, 2*k*hop-hop, c)
				for p := range min(2, cols-c) {
					for f, v := range s.sm[p] {
						sums[k][f] += float64(v)
					}
					counts[k]++
				}
			}
		})
		var total [n]float64
		cnt := 0
		for k := range half {
			for f, v := range sums[k] {
				total[f] += v
			}
			cnt += counts[k]
		}
		for f := range base {
			base[f] = float32(float64(floor*total[f]) / float64(cnt))
		}
	}
	out := make([]float64, w*h)
	for parity := range 2 {
		d.parallel((rows-parity+1)/2, func(k int) {
			by := (2*k+parity)*hop - hop
			s := newScratch()
			a := s.a
			for c := 0; c < cols; c += 2 {
				load(s, by, c)
				// Each divided by its own (the division keeps each one's
				// symmetry, so they come back apart), as X + i Y.
				for f := range a {
					g := (bs-f/bs)%bs*bs + (bs-f%bs)%bs
					zr, zi, gr, gi := real(a[f]), imag(a[f]), real(a[g]), imag(a[g])
					sx, sy := s.sm[0][f]+base[f]+1e-9, s.sm[1][f]+base[f]+1e-9
					xr, xi, yr, yi := (zr+gr)/sx, (zi-gi)/sx, (zi+gi)/sy, (gr-zr)/sy
					s.blk[0][f], s.blk[1][f] = xr-yi, xi+yr
				}
				for f := range a {
					a[f] = complex(s.blk[0][f], s.blk[1][f])
				}
				a[0] = 0
				fft32(a, s.col, plan, true)
				for p := range 2 {
					if c+p == cols {
						continue
					}
					bx := (c+p)*hop - hop
					for j := range bs {
						yy := by + j
						if yy < 0 || yy >= h {
							continue
						}
						row := out[yy*w:][:w]
						for i := range bs {
							if xx := bx + i; xx >= 0 && xx < w {
								v := real(a[j*bs+i])
								if p == 1 {
									v = imag(a[j*bs+i])
								}
								row[xx] += float64(float64(float64(v)*win[j]) * win[i])
							}
						}
					}
				}
			}
		})
	}
	return out
}

// fft32 is the 2-D DFT of a localBlock x localBlock array, in place, by
// plan (of localBlock), the columns through col; inverse without the 1/n.
func fft32(a, col []complex64, plan *fftPlan, inverse bool) {
	const bs = localBlock
	for y := range bs {
		plan.transform(a[y*bs:], inverse)
	}
	for x := range bs {
		for y := range bs {
			col[y] = a[y*bs+x]
		}
		plan.transform(col, inverse)
		for y := range bs {
			a[y*bs+x] = col[y]
		}
	}
}

// box3Wrap is the mean of v over 3 x 3 around each place of a localBlock x
// localBlock array, wrapping round, by way of tmp.
func box3Wrap(dst, v, tmp *[localBlock * localBlock]float32) {
	const bs = localBlock
	for y := range bs {
		r := v[y*bs:][:bs]
		t := tmp[y*bs:][:bs]
		t[0] = r[bs-1] + r[0] + r[1]
		for x := 1; x < bs-1; x++ {
			t[x] = r[x-1] + r[x] + r[x+1]
		}
		t[bs-1] = r[bs-2] + r[bs-1] + r[0]
	}
	for y := range bs {
		u, m, l := tmp[(y+bs-1)%bs*bs:][:bs], tmp[y*bs:][:bs], tmp[(y+1)%bs*bs:][:bs]
		o := dst[y*bs:][:bs]
		for x := range bs {
			o[x] = (u[x] + m[x] + l[x]) / 9
		}
	}
}

// mirror brings i into [0, n) by reflecting it at the ends (n > 0).
func mirror(i, n int) int {
	for i < 0 || i >= n {
		if i < 0 {
			i = -i - 1
		}
		if i >= n {
			i = 2*n - 1 - i
		}
	}
	return i
}

// spectrum returns the magnitude of y's spectrum (hp x wp, y windowed and
// padded to powers of two) over its mean around each frequency, 15 x 15
// (whitened).
func (d *detector) spectrum(y []float64) []float32 {
	w, h := d.w, d.h
	d.hp, d.wp = pow2(h), pow2(w)
	var mean float64
	for _, v := range y {
		mean += v
	}
	mean /= float64(len(y))
	ty, tx := tukey(h), tukey(w)
	a := make([]complex64, d.hp*d.wp)
	for j := range h {
		for i := range w {
			a[j*d.wp+i] = complex(float32(float64((y[j*w+i]-mean)*ty[j])*tx[i]), 0)
		}
	}
	fft2Par(a, d.hp, d.wp, false, d.par)
	white := make([]float32, len(a))
	for i, v := range a {
		re, im := real(v), imag(v)
		white[i] = float32(math.Sqrt(float64(float32(re*re) + float32(im*im))))
	}
	a = nil
	sm := boxWrap(white, d.wp, d.hp, whitenRadius)
	for i := range white {
		white[i] /= sm[i] + 1e-9
	}
	white[0] = 0
	return white
}

// tukey is a window that is flat but for 5% at each end.
func tukey(n int) []float64 {
	t := make([]float64, n)
	m := n / 20
	for i := range t {
		t[i] = 1
		if i < m {
			t[i] = 0.5 * (1 - math.Cos(math.Pi*float64(i)/float64(m)))
		} else if i >= n-m {
			t[i] = 0.5 * (1 - math.Cos(math.Pi*float64(n-1-i)/float64(m)))
		}
	}
	return t
}

// candidate is a size and angle of the tiling.
type candidate struct {
	z, period, angle, flip float64
}

// search scores every period (log steps of 1.2%) and angle (0.4 degree
// steps), the tiling mirrored or not, by the summed spectrum at the
// reference's frequencies, and returns the four best apart from each
// other. (The spectrum as it is, interpolated, rather than its largest
// over 3 x 3 bins, read 4 more marks of 330 with the 22 attacks on 5
// pictures with 3 keys, the screen mark 5 more, but 3 fewer of the 24 Kodak
// photographs of a screen made with Pillow, whose peaks spread more.)
func (d *detector) search() []candidate {
	maxK := 0.0
	for _, f := range d.p.ref {
		maxK = max(maxK, math.Hypot(float64(f[0]), float64(f[1])))
	}
	// The smallest tile looked for puts the reference's highest frequency
	// at 0.4 cycles a pixel; in a small picture, at the Nyquist frequency,
	// for the tiles of chips of a pixel (smallSide) made smaller still.
	// (Measured on the Kodak pictures made 448 x 299 and then reduced to
	// 75%: 23 of 24 read against 6. In a large picture the smaller tiles
	// only add false ones to place: a picture without a mark took 21% longer
	// to search.)
	cycles := 2.5
	if min(d.w, d.h) < 400 {
		cycles = 2.0
	}
	pmin, pmax := max(16, cycles*maxK), 1.2*float64(min(d.w, d.h))
	var periods []float64
	for lp := math.Log(pmin); lp < math.Log(pmax); lp += 0.012 {
		periods = append(periods, math.Exp(lp))
	}
	const na = 450
	var out []candidate
	for _, flip := range [2]float64{1, -1} {
		sc := make([]float64, len(periods)*na)
		for ai := range na {
			s, c := math.Sincos(float64(ai) * math.Pi / na)
			for pi, p := range periods {
				var v float64
				for _, f := range d.p.ref {
					ky, kx := flip*float64(f[0]), float64(f[1])
					fy := (float64(c*ky) + float64(s*kx)) / p
					fx := (float64(c*kx) - float64(s*ky)) / p
					v += sample(d.magMax, d.wp, d.hp, fy, fx)
				}
				sc[pi*na+ai] = v
			}
		}
		var mean, sq float64
		for _, v := range sc {
			mean += v
		}
		mean = float64(mean / float64(len(sc)))
		for _, v := range sc {
			sq += float64((v - mean) * (v - mean))
		}
		std := math.Sqrt(sq/float64(len(sc))) + 1e-12
		for range 4 {
			best := -1
			for i, v := range sc {
				if !math.IsInf(v, -1) && (best < 0 || v > sc[best]) {
					best = i
				}
			}
			if best < 0 {
				break
			}
			pi, ai := best/na, best%na
			out = append(out, candidate{z: (sc[best] - mean) / std, period: periods[pi], angle: float64(ai) * math.Pi / na, flip: flip})
			for p := max(0, pi-4); p <= min(len(periods)-1, pi+4); p++ {
				for a := ai - 6; a <= ai+6; a++ {
					sc[p*na+mod(a, na)] = math.Inf(-1)
				}
			}
		}
	}
	slices.SortStableFunc(out, func(a, b candidate) int {
		if a.z > b.z {
			return -1
		}
		if a.z < b.z {
			return 1
		}
		return 0
	})
	return out[:min(4, len(out))]
}

// mat2 maps a reference frequency (ky, kx), in cycles per tile, to one in
// the picture (fy, fx), in cycles per pixel.
type mat2 [2][2]float64

func (B mat2) apply(ky, kx float64) (float64, float64) {
	return float64(B[0][0]*ky) + float64(B[0][1]*kx), float64(B[1][0]*ky) + float64(B[1][1]*kx)
}

func (d *detector) score(m []float32, B mat2) float64 {
	var v float64
	for _, f := range d.p.ref {
		fy, fx := B.apply(float64(f[0]), float64(f[1]))
		v += sample(m, d.wp, d.hp, fy, fx)
	}
	return v
}

// polish improves a candidate by coordinate ascent of the reference's
// summed spectrum, lightly smoothed.
func (d *detector) polish(c candidate) mat2 {
	s, co := math.Sincos(c.angle)
	B := mat2{{co * c.flip / c.period, s / c.period}, {-s * c.flip / c.period, co / c.period}}
	cur := d.score(d.magBox, B)
	scale := max(math.Abs(B[0][0]), math.Abs(B[0][1]), math.Abs(B[1][0]), math.Abs(B[1][1]))
	for _, step := range [...]float64{0.004, 0.0015, 0.0005, 0.00015} {
		for improved := true; improved; {
			improved = false
			for i := range 2 {
				for j := range 2 {
					for _, sgn := range [2]float64{1, -1} {
						T := B
						T[i][j] += float64(sgn * step * scale)
						if v := d.score(d.magBox, T); v > cur {
							B, cur, improved = T, v, true
						}
					}
				}
			}
		}
	}
	return B
}

// lsq fits B by least squares to the reference peaks found within two
// bins of where B puts them, each to a fraction of a bin; those under
// twice the background are left out.
func (d *detector) lsq(B mat2) (mat2, bool) {
	hp, wp := d.hp, d.wp
	at := func(y, x int) float64 { return float64(d.mag[mod(y, hp)*wp+mod(x, wp)]) }
	for range 3 {
		var kk [2][2]float64 // sum w k k^T
		var kf [2][2]float64 // sum w k f^T
		n := 0
		for _, f := range d.p.ref {
			ky, kx := float64(f[0]), float64(f[1])
			fy, fx := B.apply(ky, kx)
			iy, ix := int(math.Round(fy*float64(hp))), int(math.Round(fx*float64(wp)))
			best, py, px := -1.0, 0, 0
			for dy := -2; dy <= 2; dy++ {
				for dx := -2; dx <= 2; dx++ {
					if v := at(iy+dy, ix+dx); v > best {
						best, py, px = v, iy+dy, ix+dx
					}
				}
			}
			if best < 2 {
				continue
			}
			sy := parabola(best, at(py-1, px), at(py+1, px))
			sx := parabola(best, at(py, px-1), at(py, px+1))
			gy, gx := (float64(py)+sy)/float64(hp), (float64(px)+sx)/float64(wp)
			wt := math.Sqrt(best)
			k := [2]float64{ky, kx}
			g := [2]float64{gy, gx}
			for a := range 2 {
				for b := range 2 {
					kk[a][b] += float64(float64(wt*k[a]) * k[b])
					kf[a][b] += float64(float64(wt*k[a]) * g[b])
				}
			}
			n++
		}
		if n < 6 {
			return B, false
		}
		det := float64(kk[0][0]*kk[1][1]) - float64(kk[0][1]*kk[1][0])
		if math.Abs(det) < 1e-12 {
			return B, false
		}
		// Bt = kk^-1 kf; B is its transpose.
		inv := [2][2]float64{{kk[1][1] / det, -kk[0][1] / det}, {-kk[1][0] / det, kk[0][0] / det}}
		for a := range 2 {
			for b := range 2 {
				B[b][a] = float64(inv[a][0]*kf[0][b]) + float64(inv[a][1]*kf[1][b])
			}
		}
	}
	return B, true
}

// parabola is where the peak of a parabola through three samples is,
// relative to the middle one.
func parabola(c, l, r float64) float64 {
	den := l - float64(2*c) + r
	if den == 0 {
		return 0
	}
	return 0.5 * (l - r) / den
}

// sample is m (rows of w) at a frequency, bilinear, wrapping.
func sample(m []float32, w, h int, fy, fx float64) float64 {
	y, x := float64(fy*float64(h)), float64(fx*float64(w))
	y0, x0 := math.Floor(y), math.Floor(x)
	dy, dx := y-y0, x-x0
	iy, ix := mod(int(y0), h), mod(int(x0), w)
	iy1, ix1 := iy+1, ix+1
	if iy1 == h {
		iy1 = 0
	}
	if ix1 == w {
		ix1 = 0
	}
	a := float64(float64(m[iy*w+ix])*(1-dx)) + float64(float64(m[iy*w+ix1])*dx)
	b := float64(float64(m[iy1*w+ix])*(1-dx)) + float64(float64(m[iy1*w+ix1])*dx)
	return float64(a*(1-dy)) + float64(b*dy)
}

// median is that of up to 200000 values of v, evenly spread.
func median[T float32 | float64](v []T) float64 {
	step := max(1, len(v)/200000)
	s := make([]T, 0, len(v)/step+1)
	for i := 0; i < len(v); i += step {
		s = append(s, v[i])
	}
	slices.Sort(s)
	return float64(s[len(s)/2])
}
