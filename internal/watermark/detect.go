package watermark

import (
	"math"
	"slices"
)

// WorkingSide is the most pixels along the shorter side, and twice that
// along the longer, of the picture Detect is given: a larger one is to be
// reduced first (by averaging), which keeps the mark.
const WorkingSide = 1024

const (
	sub = 2       // samples of a folded tile per chip, each way
	ns  = sub * N // samples of a folded tile along a side
)

// Detect looks for a mark made with the key in the luminance of a picture
// (w x h values, 0 to 255, rows top to bottom) and returns its payload.
func Detect(lum []float64, w, h int, k Key) ([]byte, bool) {
	if w < 32 || h < 32 {
		return nil, false
	}
	p := newPatterns(k)
	d := &detector{k: k, p: p, w: w, h: h}
	d.prepare(lum)
	// The tiling is looked for in the whole picture and in its middle,
	// half as wide and high: in a photograph, perspective changes the
	// tile's size and angle across the picture, which spreads the
	// reference's peaks over the whole and much less over the middle.
	type found struct {
		c  candidate
		in *detector
	}
	var cands []found
	for _, c := range d.search() {
		cands = append(cands, found{c, d})
	}
	if cw, ch := w/2, h/2; cw >= 128 && ch >= 128 {
		mid := make([]float64, cw*ch)
		for y := range ch {
			copy(mid[y*cw:], lum[(y+h/4)*w+w/4:][:cw])
		}
		dm := &detector{k: k, p: p, w: cw, h: ch}
		dm.prepare(mid)
		for _, c := range dm.search() {
			cands = append(cands, found{c, dm})
		}
	}
	slices.SortStableFunc(cands, func(a, b found) int {
		if a.c.z > b.c.z {
			return -1
		}
		if a.c.z < b.c.z {
			return 1
		}
		return 0
	})
	// The quick way for every candidate: the tiling from the spectrum,
	// placed by the sync pattern, its halves and regions.
	type placed struct {
		B mat2
		m mapping
		z float64
	}
	var tried []placed
	for rank, f := range cands {
		d.quick = rank >= quickFrom
		B := f.in.polish(f.c)
		if b2, ok := f.in.lsq(B); ok {
			B = b2
		}
		det := float64(B[0][0]*B[1][1]) - float64(B[0][1]*B[1][0])
		if !(math.Abs(det) > 1e-12) {
			continue
		}
		// The tile's sides in pixels: the columns of the inverse of B's
		// transpose.
		uy, ux := B[1][1]/det, -B[1][0]/det
		vy, vx := -B[0][1]/det, B[0][0]/det
		if math.Hypot(uy, ux) > 2*float64(max(w, h)) || math.Hypot(vy, vx) > 2*float64(max(w, h)) {
			continue
		}
		m, z := d.align(B)
		if z < 4.5 {
			continue
		}
		if payload, ok := d.decode(m); ok {
			return payload, true
		}
		tried = append(tried, placed{B, m, z})
	}
	// The slower ways for the two tilings the sync pattern liked best,
	// where it was found at all (a wrong tiling scores about 4): the
	// whole mapping adjusted for the sync pattern, and a photograph's
	// perspective from the tile's size around the middle.
	slices.SortStableFunc(tried, func(a, b placed) int {
		if a.z > b.z {
			return -1
		}
		if a.z < b.z {
			return 1
		}
		return 0
	})
	for _, t := range tried[:min(2, len(tried))] {
		if t.z < 6 {
			break
		}
		if m2, z2 := d.optimize(t.m); z2 > t.z+0.5 {
			if payload, ok := d.decode(m2); ok {
				return payload, true
			}
		}
		if mp, ok := d.perspective(lum, t.B); ok {
			if m3, z3 := d.optimize(d.orient(mp)); z3 >= 4.5 {
				if payload, ok := d.decode(m3); ok {
					return payload, true
				}
			}
		}
	}
	return nil, false
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
	class   []uint8 // see classOf
}

// prepare whitens the picture (its spectrum flattened), evens out its
// parts (each place over the size of the picture around it, so that flat
// areas, where the whitened picture is mostly the mark, count as much as
// busy ones), and whitens it again.
func (d *detector) prepare(lum []float64) {
	r1, _ := d.whiten(lum)
	sq := make([]float64, len(r1))
	for i, v := range r1 {
		sq[i] = v * v
	}
	loc := boxClamp(sq, d.w, d.h, 8)
	for i, v := range loc {
		loc[i] = math.Sqrt(v)
	}
	med := median(loc)
	for i := range r1 {
		r1[i] /= loc[i] + med
	}
	var mag []float32
	d.r, mag = d.whiten(r1)
	if m := float32(median(mag)); m > 0 {
		for i := range mag {
			mag[i] /= m
		}
	}
	d.mag = mag
	d.magMax = maxWrap3(mag, d.wp, d.hp)
	d.magBox = boxWrap3(mag, d.wp, d.hp)
}

// whiten returns y with its spectrum divided by the spectrum's local mean
// magnitude, and the magnitude of the whitened spectrum (hp x wp).
func (d *detector) whiten(y []float64) ([]float64, []float32) {
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
	fft2(a, d.hp, d.wp, false)
	white := make([]float32, len(a))
	for i, v := range a {
		re, im := real(v), imag(v)
		white[i] = float32(math.Sqrt(float64(float32(re*re) + float32(im*im))))
	}
	sm := boxWrap(white, d.wp, d.hp, 3)
	for i := range a {
		s := sm[i] + 1e-9
		a[i] = complex(real(a[i])/s, imag(a[i])/s)
		white[i] /= s
	}
	sm = nil
	a[0], white[0] = 0, 0
	fft2(a, d.hp, d.wp, true)
	r := make([]float64, w*h)
	for j := range h {
		for i := range w {
			r[j*w+i] = float64(real(a[j*d.wp+i]))
		}
	}
	return r, white
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
// other.
func (d *detector) search() []candidate {
	maxK := 0.0
	for _, f := range d.p.ref {
		maxK = max(maxK, math.Hypot(float64(f[0]), float64(f[1])))
	}
	pmin, pmax := max(16, 2.5*maxK), 1.2*float64(min(d.w, d.h))
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
