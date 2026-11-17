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

	// MKS2013 and MKS2021 are Magic Kernel Sharp, after John Costella
	// ("Solving the mystery of Magic Kernel Sharp", 2021), the pieces of
	// each as libvips has them. (Each product is rounded before it is
	// added, so that no processor fuses the two.)
	MKS2013 = &draw.Kernel{Support: 2.5, At: func(t float64) float64 {
		switch {
		case t >= 1.5:
			return float64((t-2.5)*(t-2.5)) / -8
		case t >= 0.5:
			return (float64(4*t*t) - float64(11*t) + 7) / 4
		}
		return 17.0/16 - float64(float64(7*t*t)/4)
	}}
	MKS2021 = &draw.Kernel{Support: 4.5, At: func(t float64) float64 {
		switch {
		case t >= 3.5:
			return (float64(4*t*t) - float64(36*t) + 81) / -1152
		case t >= 2.5:
			return (float64(4*t*t) - float64(27*t) + 45) / 144
		case t >= 1.5:
			return (float64(24*t*t) - float64(113*t) + 130) / -144
		case t >= 0.5:
			return (float64(140*t*t) - float64(379*t) + 239) / 144
		}
		return 577.0/576 - float64(float64(239*t*t)/144)
	}}
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
