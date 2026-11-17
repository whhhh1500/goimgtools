package ops

import (
	"math"
	"sync"
)

// The conversions between 8-bit sRGB and libvips' LabS (L*a*b* in 16-bit
// integers: L* times 327.67, a* and b* times 256), as libvips 8.17 does
// them, step by step in the same precision: sRGB to linear light from its
// table, to XYZ (times 100) in single precision, to L*a*b* through its
// table of cube roots, to LabS with the fraction dropped; and back through
// XYZ in double precision, linear light in single precision, and its table
// of 8-bit sRGB levels, interpolated and rounded to even. The cube roots
// are rounded correctly (libvips takes them from the C library's cbrtf,
// which on some systems is a unit in the last place off: there the result
// can be a level apart on a few pixels).

type labsTable struct {
	v2Y  [256]float32 // sRGB level to linear light
	y2v  [257]int32   // linear light (in 255 steps) to sRGB level
	cbrt []float32    // f(t) of L*a*b* at i/100000
}

const labsQuant = 100000

var labsOnce = sync.OnceValue(func() *labsTable {
	t := &labsTable{cbrt: make([]float32, labsQuant)}
	powf := func(x, y float32) float32 { return float32(math.Pow(float64(x), float64(y))) }
	for i := range 256 {
		f := float32(i) / 255
		var v float32
		if f <= 0.0031308 {
			v = 12.92 * f
		} else {
			v = float32(float32(1.0+0.055)*powf(f, float32(1)/float32(2.4))) - 0.055
		}
		t.y2v[i] = int32(math.RoundToEven(float64(float32(255 * v))))
		if f <= 0.04045 {
			t.v2Y[i] = f / 12.92
		} else {
			t.v2Y[i] = powf((f+0.055)/float32(1+0.055), 2.4)
		}
	}
	t.y2v[256] = t.y2v[255]
	for i := range t.cbrt {
		y := float32(float64(i) / labsQuant)
		if y < 0.008856 {
			t.cbrt[i] = float32(7.787*y) + float32(16.0/116.0)
		} else {
			t.cbrt[i] = float32(math.Cbrt(float64(y)))
		}
	}
	return t
})

// cb is the cube root of v/white from the table, interpolated.
func (t *labsTable) cb(v float32, white float64) float32 {
	n := float32(float64(float32(labsQuant*v)) / white)
	i := min(max(int(n), 0), labsQuant-2)
	f := n - float32(i)
	return t.cbrt[i] + float32(f*(t.cbrt[i+1]-t.cbrt[i]))
}

// toLabS converts an sRGB pixel to LabS.
func (t *labsTable) toLabS(r, g, b uint8) (L, A, B int32) {
	R := float32(float64(t.v2Y[r]) * 100)
	G := float32(float64(t.v2Y[g]) * 100)
	Bl := float32(float64(t.v2Y[b]) * 100)
	x := float32(float32(0.4124*R)+float32(0.3576*G)) + float32(0.1805*Bl)
	y := float32(float32(0.2126*R)+float32(0.7152*G)) + float32(0.0722*Bl)
	z := float32(float32(0.0193*R)+float32(0.1192*G)) + float32(0.9505*Bl)
	cbx, cby, cbz := t.cb(x, 95.0470), t.cb(y, 100.0), t.cb(z, 108.8827)
	l := float32(116*cby) - 16
	a := float32(500 * (cbx - cby))
	bb := float32(200 * (cby - cbz))
	L = int32(max(0, min(float64(l)*(32767.0/100.0), 32767)))
	A = int32(max(-32768, min(float64(a)*256, 32767)))
	B = int32(max(-32768, min(float64(bb)*256, 32767)))
	return L, A, B
}

// fromLabS converts LabS to an sRGB pixel.
func (t *labsTable) fromLabS(Ls, As, Bs int32) (uint8, uint8, uint8) {
	const x0, y0, z0 = 95.0470, 100.0, 108.8827
	L := float64(float32(float64(Ls) / (32767.0 / 100.0)))
	a := float64(float32(float64(As) / 256))
	b := float64(float32(float64(Bs) / 256))
	var cby float64
	var Y float32
	if L < 8 {
		Y = float32(float64(L*y0) / 903.3)
		cby = float64(7.787*(float64(Y)/y0)) + 16.0/116.0
	} else {
		cby = (L + 16) / 116
		Y = float32(float64(float64(y0*cby)*cby) * cby)
	}
	cube := func(tmp, white float64) float32 {
		if tmp < 0.2069 {
			return float32(float64(white*(tmp-0.13793)) / 7.787)
		}
		return float32(float64(float64(white*tmp)*tmp) * tmp)
	}
	X := cube(float64(a/500)+cby, x0)
	Z := cube(cby-float64(b/200), z0)
	X = float32(float64(X) / 100)
	Y = float32(float64(Y) / 100)
	Z = float32(float64(Z) / 100)
	R := float32(float32(3.240625*X)+float32(-1.537208*Y)) + float32(-0.498629*Z)
	G := float32(float32(-0.968931*X)+float32(1.875756*Y)) + float32(0.041518*Z)
	Bl := float32(float32(0.055710*X)+float32(-0.204021*Y)) + float32(1.056996*Z)
	return t.level(R), t.level(G), t.level(Bl)
}

// level is libvips' scRGB to 8-bit sRGB: the table interpolated, rounded
// to even.
func (t *labsTable) level(v float32) uint8 {
	if v != v { // NaN
		return 0
	}
	yf := float32(v * 255)
	yf = max(0, min(yf, 255))
	yi := int(yf)
	r := float32(t.y2v[yi]) + float32(float32(t.y2v[yi+1]-t.y2v[yi])*(yf-float32(yi)))
	// r is within 0 to 255: adding 2^23 rounds it to a whole number, half
	// to even, as rintf.
	return uint8(float32(r+0x1p23) - 0x1p23)
}
