package resize

import (
	"math"

	"golang.org/x/image/draw"
)

// Kernels besides the two golang.org/x/image/draw provides (BiLinear and
// CatmullRom). They plug into the same weight computation.
var (
	// Nearest is a box half a pixel wide on each side: the nearest pixel
	// when enlarging, the plain average of the covered pixels when
	// shrinking.
	//
	// The support is a hair over one half: with exactly one half, an output
	// pixel centred on the border between two source pixels would be within
	// reach of neither and get no value at all. It averages the two.
	Nearest = &draw.Kernel{Support: 0.5 + 1e-9, At: func(t float64) float64 { return 1 }}

	// Mitchell is the Mitchell-Netravali cubic with B = C = 1/3.
	Mitchell = &draw.Kernel{Support: 2, At: func(t float64) float64 {
		const b, c = 1.0 / 3, 1.0 / 3
		if t < 1 {
			return (float64((12-9*b-6*c)*t*t*t) + float64((-18+12*b+6*c)*t*t) + (6 - 2*b)) / 6
		}
		return (float64((-b-6*c)*t*t*t) + float64((6*b+30*c)*t*t) + float64((-12*b-48*c)*t) + (8*b + 24*c)) / 6
	}}

	Lanczos2 = lanczos(2)
	Lanczos3 = lanczos(3)
)

func lanczos(a float64) *draw.Kernel {
	return &draw.Kernel{Support: a, At: func(t float64) float64 {
		if t == 0 {
			return 1
		}
		x := math.Pi * t
		return a * math.Sin(x) * math.Sin(x/a) / (x * x)
	}}
}
