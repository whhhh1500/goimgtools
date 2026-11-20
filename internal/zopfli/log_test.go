package zopfli

import (
	"math"
	"math/rand/v2"
	"runtime"
	"testing"
)

// TestLog: zlog and zlog2 give math.Log's and math.Log2's results where
// those are not fused (amd64, whose math.Log is assembly, and 386), for
// the counts Zopfli takes logarithms of and for normal doubles at random
// (amd64's math.Log does not normalize subnormal ones first).
func TestLog(t *testing.T) {
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "386" {
		t.Skip("math.Log may be fused here")
	}
	check := func(x float64) {
		if g, w := zlog(x), math.Log(x); g != w && !(math.IsNaN(g) && math.IsNaN(w)) {
			t.Fatalf("zlog(%v) = %v, math.Log %v", x, g, w)
		}
		if g, w := zlog2(x), math.Log2(x); g != w && !(math.IsNaN(g) && math.IsNaN(w)) {
			t.Fatalf("zlog2(%v) = %v, math.Log2 %v", x, g, w)
		}
	}
	n := 1 << 22
	if testing.Short() {
		n = 1 << 16
	}
	for i := 0; i <= n; i++ {
		check(float64(i))
	}
	rng := rand.New(rand.NewPCG(1, 2))
	for range n {
		if x := math.Float64frombits(rng.Uint64() &^ (1 << 63)); x >= 0x1p-1022 {
			check(x)
		}
		check(rng.Float64() * 1e7)
	}
	for _, x := range []float64{-1, math.Inf(1), math.NaN(), 0x1p-1022, math.MaxFloat64, 0.5, 1, 2} {
		check(x)
	}
}
