package watermark

import "math"

const (
	// N is the number of chips along each side of the tile.
	N = 64
	// nRef is the number of reference sinusoids.
	nRef = 24
	// refMin and refMax bound the reference frequencies, in cycles per
	// tile.
	refMin, refMax = 5, 18
	// The amplitudes of the reference, the sync pattern and the payload
	// in the tile (each of unit power): 40%, 15% and 45% of its power.
	// (More for the payload, 60% with 25% or 30% for the reference,
	// read no more after JPEG and WebP, and less after a photograph's
	// perspective: the reference places the tiles there.)
	wRef, wSync, wPay = 0.63, 0.39, 0.67
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

// tile is the mark of payload: its chips, of unit power.
func tile(p *patterns, payload []byte, k Key) []float64 {
	coded := encode(frameBits(payload, k))
	which, sign := spreading(k, len(coded))
	t := make([]float64, N*N)
	var power float64
	for i := range t {
		pay := sign[i]
		if coded[which[i]] == 1 {
			pay = -pay
		}
		v := float64(wRef*p.refPat[i]) + float64(wSync*p.sync[i]) + float64(wPay*pay)
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
