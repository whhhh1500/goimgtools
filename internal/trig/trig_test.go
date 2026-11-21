package trig

import (
	"math"
	"testing"
)

// TestCosPi: within a few ulps of math.Cos (of the angle brought into
// [0, pi] first: math.Pi*k/n itself is off by more for large k/n).
func TestCosPi(t *testing.T) {
	for n := 1; n <= 200; n++ {
		for k := -3 * n; k <= 3*n; k++ {
			r := ((k % (2 * n)) + 2*n) % (2 * n)
			if r > n {
				r = 2*n - r
			}
			want := math.Cos(math.Pi * float64(r) / float64(n))
			if got := CosPi(k, n); math.Abs(got-want) > 1e-15 {
				t.Fatalf("cos(pi*%d/%d) = %v, want %v", k, n, got, want)
			}
		}
	}
}

// TestSinPi: sin from cos.
func TestSinPi(t *testing.T) {
	for n := 1; n <= 100; n++ {
		for k := 0; k <= 2*n; k++ {
			if d := SinPi(k, n) - math.Sin(math.Pi*float64(k)/float64(n)); d > 1e-14 || d < -1e-14 {
				t.Fatalf("sin(pi*%d/%d) off by %g", k, n, d)
			}
		}
	}
}
