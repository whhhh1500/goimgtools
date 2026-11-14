package jpegstream

import (
	"math/rand"
	"testing"
)

// TestDivBy: dividing by multiplication gives what the division gives, for
// every divisor a quantization table can hold and every coefficient far
// beyond what a transform can produce.
func TestDivBy(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for q := int32(1); q <= 255; q++ {
		b := 8 * q
		m := recip(b)
		check := func(a int32) {
			if got, want := divBy(a, b, m), div(a, b); got != want {
				t.Fatalf("%d / %d = %d, want %d", a, b, got, want)
			}
		}
		// Around every multiple of half the divisor, where rounding turns.
		for k := int32(-600); k <= 600; k++ {
			for d := int32(-2); d <= 2; d++ {
				check(k*b/2 + d)
			}
		}
		for _, a := range []int32{0, 1, -1, 1 << 19, -(1 << 19), 1<<20 - 1, -(1<<20 - 1)} {
			check(a)
		}
		for i := 0; i < 20000; i++ {
			check(r.Int31n(1<<21) - 1<<20)
		}
	}
	// Every coefficient value for the divisors of the extremes.
	for _, q := range []int32{1, 2, 3, 16, 99, 254, 255} {
		b := 8 * q
		m := recip(b)
		for a := int32(-(1 << 20)); a <= 1<<20; a++ {
			if divBy(a, b, m) != div(a, b) {
				t.Fatalf("%d / %d", a, b)
			}
		}
	}
}
