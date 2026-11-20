package vp8lenc

import (
	"math/rand/v2"
	"slices"
	"testing"
)

func testSet() *histSet {
	return &histSet{
		green: make([]uint32, numLiteral+numLength), red: make([]uint32, 256),
		blue: make([]uint32, 256), alpha: make([]uint32, 256), dist: make([]uint32, numDistance),
	}
}

// noisy fills s with n pixels whose green, red and blue spread around
// centre with the given width; alpha is opaque.
func noisy(s *histSet, r *rand.Rand, n int, centre, width float64) {
	for i := 0; i < n; i++ {
		for _, l := range [][]uint32{s.green[:256], s.red, s.blue} {
			v := int(centre + r.NormFloat64()*width)
			l[min(max(v, 0), 255)]++
		}
		s.alpha[255]++
	}
}

func setsBits(sets []*histSet) (bits int64) {
	for _, s := range sets {
		_, b := s.build()
		bits += b
	}
	return bits
}

func setsSum(sets []*histSet) *histSet {
	t := testSet()
	for _, s := range sets {
		sumInto(t, t, s)
	}
	return t
}

func TestMergeSets(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	// a and c come from the same distribution, b from one far away; d is
	// like a but small. a, c and d are worth joining, b is not.
	a, b, c, d := testSet(), testSet(), testSet(), testSet()
	noisy(a, r, 4000, 128, 6)
	noisy(b, r, 4000, 30, 2)
	noisy(c, r, 4000, 128, 6)
	noisy(d, r, 300, 128, 6)
	sets := []*histSet{a, b, c, d}
	before, sum := setsBits(sets), setsSum(sets)
	assign := []uint16{0, 1, 2, 3, 3, 2, 1, 0}
	m := mergeSets(slices.Clone(sets), assign)
	out, alive := m.sets, m.alive
	if len(out) != 2 || !slices.Equal(alive, []bool{true, true, false, false}) {
		t.Fatalf("%d sets left, alive %v; want a+c+d and b", len(out), alive)
	}
	if want := []uint16{0, 1, 0, 0, 0, 0, 1, 0}; !slices.Equal(assign, want) {
		t.Errorf("assign %v, want %v", assign, want)
	}
	after := setsBits(out)
	if after >= before {
		t.Errorf("%d bits after joining, %d before", after, before)
	}
	var reported int64
	for i, b := range m.bits {
		reported += b
		if _, want := out[i].build(); b != want {
			t.Errorf("set %d: %d bits reported, its codes take %d", i, b, want)
		}
	}
	if reported != after {
		t.Errorf("%d bits reported, %d", reported, after)
	}
	got := setsSum(out)
	for k, l := range got.lists() {
		if !slices.Equal(l, sum.lists()[k]) {
			t.Fatalf("alphabet %d: the symbols are not those given", k)
		}
	}

	// Large groups that differ are left as they are.
	e, f := testSet(), testSet()
	noisy(e, r, 50000, 100, 3)
	noisy(f, r, 50000, 160, 3)
	assign = []uint16{1, 0}
	if m := mergeSets([]*histSet{e, f}, assign); len(m.sets) != 2 || len(m.bits) != 2 || !slices.Equal(assign, []uint16{1, 0}) {
		t.Errorf("%d sets left, assign %v", len(m.sets), assign)
	}
}

// TestBestBits: bestBits counts what bestCode does, on histograms of every
// kind: empty, one or two symbols, runs of equal counts, sparse and dense.
func TestBestBits(t *testing.T) {
	r := rand.New(rand.NewPCG(3, 4))
	lens := make([]uint8, 2400)
	for i := 0; i < 3000; i++ {
		n := []int{40, 256, 280, 1304, 2328}[r.IntN(5)]
		h := make([]uint32, n)
		switch k := r.IntN(6); k {
		case 0: // nothing
		case 1, 2: // one or two symbols
			for j := 0; j < k; j++ {
				h[r.IntN(n)] = uint32(1 + r.IntN(1000))
			}
		case 3: // runs of equal counts
			for j := 0; j < n; {
				run, v := 1+r.IntN(20), uint32(r.IntN(3)*r.IntN(50))
				for ; run > 0 && j < n; run-- {
					h[j] = v
					j++
				}
			}
		default: // sparse or dense, skewed
			p := r.Float64()
			for j := range h {
				if r.Float64() < p {
					h[j] = uint32(r.ExpFloat64() * float64(1+r.IntN(5000)))
				}
			}
		}
		_, want := bestCode(h)
		if got := bestBits(h, lens[:n]); got != want {
			t.Fatalf("histogram %d (%d symbols): %d bits, bestCode %d", i, n, got, want)
		}
	}
}
