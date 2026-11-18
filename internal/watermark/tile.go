package watermark

import "math"

const (
	// N is the number of chips along each side of the tile.
	N = 64
	// nRef is the number of reference sinusoids: of the same power in all,
	// fewer stand out more in a damaged copy's spectrum, and are as visible.
	// (The 22 attacks on 5 pictures with 3 keys read, with 32, 24, 20, 16
	// and 12: 233, 239, 245, 245 and 243 marks of 330; the screen mark with
	// 24, 16 and 12: 277, 280 and 280.)
	nRef = 16
	// refMin and refMax bound the reference frequencies, in cycles per
	// tile, and wRef, wSync and wPay are the amplitudes of the reference,
	// the sync pattern and the payload in the tile (each of unit power):
	// 25%, 20% and 55% of its power. The reference's sinusoids are what
	// the eye sees most of the mark (alone, at the same power, SSIMULACRA2
	// 74.0 against 83.9 and 83.5 for the others), the more so the lower
	// their frequency: taking them from 10 to 20 cycles rather than 5 to
	// 18 and giving them 25% rather than 40% let the whole be 12% stronger
	// as visible as before, and the 22 attacks on 5 pictures with 3 keys
	// read 239 marks of 330 against 217 (JPEG 30 12 against 8, WebP 60 9
	// against 5); the screen mark, as visible, 277 against 274. With 16
	// sinusoids (nRef), 8 to 20 cycles read 229 and 238 with two sets of
	// keys against 245 and 240. (With 15%
	// for the reference the photographs of a screen were lost: 220. With
	// the reference at a half or a quarter where the picture is flat,
	// where it shows most, and the whole stronger to be as visible: 219,
	// 200, and a mostly white screenshot was not found at all.)
	refMin, refMax    = 10, 20
	wRef, wSync, wPay = 0.5, 0.447, 0.742
)

// cosTab is cos(2 pi m / 64), written out so that the reference is the
// same on every processor (math.Cos is in assembly on some).
var cosTab = [N]float64{
	1.0, 0.9951847266721969, 0.9807852804032304, 0.9569403357322088,
	0.9238795325112867, 0.881921264348355, 0.8314696123025452,
	0.773010453362737, 0.7071067811865476, 0.6343932841636455,
	0.5555702330196023, 0.4713967368259978, 0.38268343236508984,
	0.29028467725446233, 0.19509032201612833, 0.09801714032956077, 0.0,
	-0.09801714032956077, -0.19509032201612833, -0.29028467725446233,
	-0.38268343236508984, -0.4713967368259978, -0.5555702330196023,
	-0.6343932841636455, -0.7071067811865476, -0.773010453362737,
	-0.8314696123025452, -0.881921264348355, -0.9238795325112867,
	-0.9569403357322088, -0.9807852804032304, -0.9951847266721969, -1.0,
	-0.9951847266721969, -0.9807852804032304, -0.9569403357322088,
	-0.9238795325112867, -0.881921264348355, -0.8314696123025452,
	-0.773010453362737, -0.7071067811865476, -0.6343932841636455,
	-0.5555702330196023, -0.4713967368259978, -0.38268343236508984,
	-0.29028467725446233, -0.19509032201612833, -0.09801714032956077, 0.0,
	0.09801714032956077, 0.19509032201612833, 0.29028467725446233,
	0.38268343236508984, 0.4713967368259978, 0.5555702330196023,
	0.6343932841636455, 0.7071067811865476, 0.773010453362737,
	0.8314696123025452, 0.881921264348355, 0.9238795325112867,
	0.9569403357322088, 0.9807852804032304, 0.9951847266721969,
}

// patterns are what a key's tiles have in common.
type patterns struct {
	ref    [nRef][2]int // (ky, kx) cycles per tile
	phase  [nRef]int    // in 64ths of a turn
	refPat [N * N]float64
	sync   [N * N]float64
}

func newPatterns(k Key) *patterns {
	p := new(patterns)
	r := newRNG(k, "ref")
	for n := 0; n < nRef; {
		ky, kx := r.intn(2*refMax+1)-refMax, r.intn(2*refMax+1)-refMax
		d2 := ky*ky + kx*kx
		ay, ax := abs(ky), abs(kx)
		// Within the band, in the upper half plane (the other half is
		// the same frequency), away from the axes, where pictures have
		// most of their lines, and from the others and their opposites.
		if d2 < refMin*refMin || d2 > refMax*refMax || ky <= 0 || 7*min(ay, ax) <= max(ay, ax) {
			continue
		}
		ok := true
		for _, o := range p.ref[:n] {
			if sq(ky-o[0])+sq(kx-o[1]) < 4 || sq(ky+o[0])+sq(kx+o[1]) < 4 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		p.ref[n] = [2]int{ky, kx}
		p.phase[n] = r.intn(N)
		n++
	}
	var power float64
	for cy := range N {
		for cx := range N {
			var v float64
			for i, f := range p.ref {
				v += cosTab[mod(f[0]*cy+f[1]*cx+p.phase[i], N)]
			}
			p.refPat[cy*N+cx] = v
			power += float64(v * v)
		}
	}
	scale := 1 / math.Sqrt(power/(N*N))
	for i := range p.refPat {
		p.refPat[i] *= scale
	}
	s := newRNG(k, "sync")
	for i := range p.sync {
		p.sync[i] = s.sign()
	}
	shape(p.sync[:])
	return p
}

// spreading assigns each chip a coded bit and a sign, for a payload of
// ncoded coded bits.
func spreading(k Key, ncoded int) (which []int32, sign []float64) {
	r := newRNG(k, "spread")
	perm := make([]int32, N*N)
	for i := range perm {
		perm[i] = int32(i)
	}
	for i := len(perm) - 1; i > 0; i-- {
		j := r.intn(i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	which = make([]int32, N*N)
	for i, c := range perm {
		which[c] = int32(i % ncoded)
	}
	sign = make([]float64, N*N)
	for i := range sign {
		sign[i] = r.sign()
	}
	return which, sign
}

// tile is the mark of payload: its chips, of unit power. (Measured: the
// payload in blocks of 2 x 2 chips, which JPEG and WebP keep better, has
// 1 to 5 dB more SNR after JPEG 30 but is more visible; as visible as now
// (Strength 0.85), the 22 attacks read 74 marks of 110 against 77,
// losing crops and photographs of a screen. Reading the mark back as it
// is made and strengthening the coded bits the picture works against
// reads more with the tiling known, but as visible the 22 attacks on 5
// pictures with 3 keys read 213 to 246 marks of 330 against 244 to 252:
// what is lost is mostly the tiling, whose reference gets a smaller share.)
func tile(p *patterns, payload []byte, k Key) []float64 {
	coded := encode(frameBits(payload, k))
	which, sign := spreading(k, len(coded))
	pay := make([]float64, N*N)
	for i := range pay {
		pay[i] = sign[i]
		if coded[which[i]] == 1 {
			pay[i] = -pay[i]
		}
	}
	shape(pay)
	t := make([]float64, N*N)
	var power float64
	for i := range t {
		v := float64(wRef*p.refPat[i]) + float64(wSync*p.sync[i]) + float64(wPay*pay[i])
		t[i] = v
		power += float64(v * v)
	}
	scale := 1 / math.Sqrt(power/(N*N))
	for i := range t {
		t[i] *= scale
	}
	return t
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func sq(v int) int { return v * v }

func mod(a, n int) int {
	a %= n
	if a < 0 {
		a += n
	}
	return a
}

// shapeWeight is what the spectrum of the sync pattern and the payload is
// weighed by at bin (fy, fx) of the tile: the square root of the frequency
// over 16 cycles a tile (taken as 1 at the lowest). Their chips are drawn
// at random, of a flat spectrum; tilted to the higher frequencies, which
// the eye sees less, they can be stronger as visible, and enough of them
// is left after compression for the matched filter of payloadChips to read.
// (Measured on the 24 Kodak photographs, as visible by SSIMULACRA2, after
// the 22 attacks: 390 marks of 528 read against 358 at Strength 0.7, 452
// against 430 at the default, WebP 60 22 and 24 against 18 and 24, crops
// to 25% 11 against 6; butteraugli 5% to 7% higher. With the weight's power at
// 0.25, 0.35, 0.75, 1 and 1.5: 374, 381, 395, 393 and 395, butteraugli up
// with it; with -0.25 and -0.5, towards the lower frequencies, 314 and
// 219; with the payload alone tilted, 382.)
func shapeWeight(fy, fx int) float64 {
	fy, fx = min(fy, N-fy), min(fx, N-fx)
	return math.Sqrt(math.Sqrt(float64(max(1, fy*fy+fx*fx)))) / 2
}

// shape weighs v, N x N chips repeating, by shapeWeight in frequency, and
// scales the result back to v's power. The transforms are written out
// with cosTab, so that the tile is the same on every processor.
func shape(v []float64) {
	const mask = N - 1
	sin := func(j int) float64 { return cosTab[(j+3*N/4)&mask] } // sin(2 pi j / N)
	var p0 float64
	for _, x := range v {
		p0 += float64(x * x)
	}
	// Forward along the rows, then the columns.
	re, im := make([]float64, N*N), make([]float64, N*N)
	for y := range N {
		row := v[y*N : y*N+N]
		for k := range N {
			var sr, si float64
			for n, x := range row {
				j := (k * n) & mask
				sr += float64(x * cosTab[j])
				si -= float64(x * sin(j))
			}
			re[y*N+k], im[y*N+k] = sr, si
		}
	}
	fre, fim := make([]float64, N*N), make([]float64, N*N)
	for x := range N {
		for k := range N {
			var sr, si float64
			for n := range N {
				j := (k * n) & mask
				c, s := cosTab[j], sin(j)
				a, b := re[n*N+x], im[n*N+x]
				sr += float64(a*c) + float64(b*s)
				si += float64(b*c) - float64(a*s)
			}
			w := shapeWeight(k, x)
			fre[k*N+x], fim[k*N+x] = float64(sr*w), float64(si*w)
		}
	}
	// Back along the columns, then the rows, keeping the real part.
	for x := range N {
		for n := range N {
			var sr, si float64
			for k := range N {
				j := (k * n) & mask
				c, s := cosTab[j], sin(j)
				a, b := fre[k*N+x], fim[k*N+x]
				sr += float64(a*c) - float64(b*s)
				si += float64(b*c) + float64(a*s)
			}
			re[n*N+x], im[n*N+x] = sr, si
		}
	}
	var p1 float64
	for y := range N {
		for n := range N {
			var sr float64
			for k := range N {
				j := (k * n) & mask
				sr += float64(re[y*N+k]*cosTab[j]) - float64(im[y*N+k]*sin(j))
			}
			v[y*N+n] = sr
			p1 += float64(sr * sr)
		}
	}
	scale := math.Sqrt(p0 / p1)
	for i := range v {
		v[i] *= scale
	}
}
