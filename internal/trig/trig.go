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
