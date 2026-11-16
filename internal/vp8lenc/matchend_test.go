package vp8lenc

import (
	"math/rand/v2"
	"testing"
)

// TestMatchAt checks the remembered ends of matches against comparing the
// pixels: the tokenizer's searches go forward through the stream, at
// distances that repeat, with limits that vary (the buffer's end, the
// longest match), on data with long repeats and short ones.
func TestMatchAt(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	for round := 0; round < 20; round++ {
		n := 2000 + r.IntN(20000)
		buf := make([]uint32, n)
		for i := 0; i < n; {
			switch k := r.IntN(4); {
			case k == 0 && i > 0: // a run
				l := min(r.IntN(3000), n-i)
				for j := 0; j < l; j++ {
					buf[i+j] = buf[i-1]
				}
				i += max(l, 1)
			case k == 1 && i > 100: // a copy from back
				d := 1 + r.IntN(min(i, 900))
				l := min(r.IntN(2000), n-i)
				for j := 0; j < l; j++ {
					buf[i+j] = buf[i+j-d]
				}
				i += max(l, 1)
			default:
				buf[i] = uint32(r.IntN(3))
				i++
			}
		}
		tk := &tokenizer{buf: buf, n: n, ends: make([]matchEnd, matchEnds)}
		for p := 1; p < n; p += 1 + r.IntN(3) {
			for k := 0; k < 4; k++ {
				d := 1 + r.IntN(min(p, 40))
				if k == 0 {
					d = 1 + r.IntN(min(p, 1000))
				}
				limit := min(maxMatch, n-p)
				if r.IntN(4) == 0 {
					limit = min(limit, 1+r.IntN(50))
				}
				if got, want := tk.matchAt(p, d, limit), matchLen(buf, p-d, p, limit); got != want {
					t.Fatalf("round %d: at %d, distance %d, limit %d: %d, want %d", round, p, d, limit, got, want)
				}
			}
		}
	}
}
