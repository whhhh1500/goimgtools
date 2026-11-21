// Package trig computes the cosines and sines of whole fractions of pi,
// cos(pi*k/n), the same on every processor: by symmetry from polynomials on
// [0, pi/4] with every product rounded before it is added, where math.Cos
// may be fused or use the processor's instructions.
package trig

import "math"

// CosPi is cos(pi*k/n) for whole k and n > 0, by symmetry from a polynomial
// on [0, pi/4] (Cephes' coefficients, as Go's math package has them) with
// every product rounded before it is added.
func CosPi(k, n int) float64 {
	k %= 2 * n
	if k < 0 {
		k += 2 * n
	}
	if k > n {
		k = 2*n - k // cos(pi*k/n) = cos(2pi - pi*k/n)
	}
	sign := 1.0
	if 2*k > n {
		k, sign = n-k, -1 // cos(pi - x) = -cos(x)
	}
	// Now 0 <= pi*k/n <= pi/2.
	if 4*k > n {
		// cos(x) = sin(pi/2 - x), the argument below pi/4.
		return sign * sinPoly(float64(math.Pi*float64(n-2*k))/float64(2*n))
	}
	return sign * cosPoly(float64(math.Pi*float64(k))/float64(n))
}

// sinPoly and cosPoly are sin and cos of x in [0, pi/4].
func sinPoly(x float64) float64 {
	z := float64(x * x)
	p := 1.58962301576546568060e-10
	for _, c := range [...]float64{-2.50507477628578072866e-8, 2.75573136213857245213e-6, -1.98412698295895385996e-4, 8.33333333332211858878e-3, -1.66666666666666307295e-1} {
		p = float64(p*z) + c
	}
	return x + float64(float64(x*z)*p)
}

func cosPoly(x float64) float64 {
	z := float64(x * x)
	p := -1.13585365213876817300e-11
	for _, c := range [...]float64{2.08757008419747316778e-9, -2.75573141792967388112e-7, 2.48015872888517045348e-5, -1.38888888888730564116e-3, 4.16666666666665929218e-2} {
		p = float64(p*z) + c
	}
	return 1.0 - float64(0.5*z) + float64(float64(z*z)*p)
}

// SinPi is sin(pi*k/n), n > 0: cos(pi*(n-2k)/(2n)).
func SinPi(k, n int) float64 { return CosPi(n-2*k, 2*n) }

// SinCos is sin and cos of x radians, |x| below a million, by Go's
// octant reduction (Cody and Waite's, pi/4 in three parts) and the
// polynomials above, every product rounded before it is added.
func SinCos(x float64) (sin, cos float64) {
	const (
		pi4a = 7.85398125648498535156e-1
		pi4b = 3.77489470793079817668e-8
		pi4c = 2.69515142907905952645e-15
	)
	if math.IsNaN(x) || math.IsInf(x, 0) || math.Abs(x) > 1e6 {
		return math.NaN(), math.NaN()
	}
	sinSign, cosSign := false, false
	if x < 0 {
		x, sinSign = -x, true
	}
	j := uint64(float64(x * (4 / math.Pi)))
	y := float64(j)
	if j&1 == 1 {
		j++
		y++
	}
	j &= 7
	z := ((x - float64(y*pi4a)) - float64(y*pi4b)) - float64(y*pi4c)
	if j > 3 {
		j -= 4
		sinSign, cosSign = !sinSign, !cosSign
	}
	if j > 1 {
		cosSign = !cosSign
	}
	s, c := sinPoly(z), cosPoly(z)
	if j == 1 || j == 2 {
		s, c = c, s
	}
	if sinSign {
		s = -s
	}
	if cosSign {
		c = -c
	}
	return s, c
}

// SinCosDeg is sin and cos of d degrees, exact at the multiples of 90.
func SinCosDeg(d float64) (sin, cos float64) {
	d = math.Mod(d, 360)
	if d < 0 {
		d += 360
	}
	switch d {
	case 0:
		return 0, 1
	case 90:
		return 1, 0
	case 180:
		return 0, -1
	case 270:
		return -1, 0
	}
	return SinCos(float64(d * (math.Pi / 180)))
}

// Atan2 is the angle of (x, y), as math.Atan2, by Go's portable series
// with every product rounded before it is added.
func Atan2(y, x float64) float64 {
	switch {
	case math.IsNaN(x) || math.IsNaN(y):
		return math.NaN()
	case y == 0:
		if x >= 0 && !math.Signbit(x) {
			return math.Copysign(0, y)
		}
		return math.Copysign(math.Pi, y)
	case x == 0:
		return math.Copysign(math.Pi/2, y)
	case math.IsInf(x, 0) || math.IsInf(y, 0):
		return math.Atan2(y, x) // exact quarter values
	}
	q := atan(y / x)
	if x < 0 {
		if q <= 0 {
			return q + math.Pi
		}
		return q - math.Pi
	}
	return q
}

func atan(x float64) float64 {
	if x == 0 {
		return x
	}
	if x > 0 {
		return satan(x)
	}
	return -satan(-x)
}

func satan(x float64) float64 {
	const (
		morebits = 6.123233995736765886130e-17
		tan3pio8 = 2.41421356237309504880
	)
	if x <= 0.66 {
		return xatan(x)
	}
	if x > tan3pio8 {
		return math.Pi/2 - xatan(1/x) + morebits
	}
	return math.Pi/4 + xatan((x-1)/(x+1)) + float64(0.5*morebits)
}

func xatan(x float64) float64 {
	const (
		p0 = -8.750608600031904122785e-01
		p1 = -1.615753718733365076637e+01
		p2 = -7.500855792314704667340e+01
		p3 = -1.228866684490136173410e+02
		p4 = -6.485021904942025371773e+01
		q0 = +2.485846490142306297962e+01
		q1 = +1.650270098316988542046e+02
		q2 = +4.328810604912902668951e+02
		q3 = +4.853903996359136964868e+02
		q4 = +1.945506571482613964425e+02
	)
	z := float64(x * x)
	num := float64(float64(float64(float64(float64(float64(float64(p0*z)+p1)*z)+p2)*z)+p3)*z) + p4
	den := float64(float64(float64(float64(float64(float64(float64(float64(z+q0)*z)+q1)*z)+q2)*z)+q3)*z) + q4
	z = float64(z*num) / den
	return float64(x*z) + x
}

// Pow is x to the power y for x >= 0, the same on every processor (math.Pow
// is the processor's own on some): exp(y ln x), the logarithm from the
// series of atanh after taking out the exponent, the exponential from its
// Taylor series after taking out the powers of two. Relative error about
// 1e-15.
func Pow(x, y float64) float64 {
	switch {
	case y == 0:
		return 1
	case math.IsNaN(x) || math.IsNaN(y) || x < 0:
		return math.NaN()
	case x == 0:
		if y > 0 {
			return 0
		}
		return math.Inf(1)
	case x == 1:
		return 1
	}
	return exp(float64(y * ln(x)))
}

const ln2 = 0.693147180559945309417232121458176568

// ln is the natural logarithm of x > 0.
func ln(x float64) float64 {
	m, e := math.Frexp(x) // x = m 2^e, m in [0.5, 1)
	if m < math.Sqrt2/2 {
		m *= 2
		e--
	}
	// ln m = 2 atanh(s), s = (m-1)/(m+1), |s| < 0.172
	s := (m - 1) / (m + 1)
	s2 := float64(s * s)
	sum, term := 0.0, s
	for k := 1; k < 40; k += 2 {
		sum += term / float64(k)
		term = float64(term * s2)
		if math.Abs(term) < 1e-18 {
			break
		}
	}
	return float64(float64(e)*ln2) + float64(2*sum)
}

// exp is e to the power t.
func exp(t float64) float64 {
	if t > 709 {
		return math.Inf(1)
	}
	if t < -745 {
		return 0
	}
	k := math.Round(t / ln2)
	r := t - float64(k*ln2) // |r| <= ln2/2
	sum, term := 1.0, 1.0
	for n := 1; n < 30; n++ {
		term = float64(term*r) / float64(n)
		sum += term
		if math.Abs(term) < 1e-18 {
			break
		}
	}
	return math.Ldexp(sum, int(k))
}
