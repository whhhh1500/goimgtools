package watermark

import (
	"math/rand/v2"
	"slices"
	"testing"
)

// TestFiltersRows: boxWrapRows and maxWrap3Rows, which keep a few rows of
// their first pass, give what boxWrap and maxWrap3 give, to the bit, on
// every size from a single row up (the wrap-round at both ends) and for
// several radii; boxClampInto what boxClamp gives.
func TestFiltersRows(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for _, sz := range [][2]int{{1, 1}, {3, 1}, {1, 5}, {2, 2}, {7, 3}, {5, 8}, {64, 32}, {33, 17}} {
		w, h := sz[0], sz[1]
		v := make([]float32, w*h)
		for i := range v {
			v[i] = float32(rng.Float64()*1000 - 100)
		}
		for _, r := range []int{1, 2, 7} {
			if got, want := boxWrapRows(make([]float32, len(v)), v, w, h, r), boxWrap(v, w, h, r); !slices.Equal(got, want) {
				t.Errorf("%dx%d, radius %d: boxWrapRows differs from boxWrap", w, h, r)
			}
		}
		if got, want := maxWrap3Rows(make([]float32, len(v)), v, w, h), maxWrap3(v, w, h); !slices.Equal(got, want) {
			t.Errorf("%dx%d: maxWrap3Rows differs from maxWrap3", w, h)
		}
		f := make([]float64, w*h)
		for i := range f {
			f[i] = rng.Float64()*1000 - 100
		}
		for _, r := range []int{1, 4} {
			// Into storage longer than needed, as CleanChroma's last strip.
			out, tmp := make([]float64, len(f)+9), make([]float64, len(f)+9)
			if got, want := boxClampInto(out, tmp, f, w, h, r), boxClamp(f, w, h, r); !slices.Equal(got, want) {
				t.Errorf("%dx%d, radius %d: boxClampInto differs from boxClamp", w, h, r)
			}
		}
	}
}
