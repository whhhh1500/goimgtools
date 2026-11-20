package vp8lenc

import "math"

// merged is what mergeSets leaves: the sets and the bits each takes
// (trees and symbols), and which of the sets given are left.
type merged struct {
	sets  []*histSet
	bits  []int64
	alive []bool
}

// mergeSets joins Huffman groups while that makes them smaller (step 3 in
// meta.go). k-means fills the number of groups it is given, but every
// group's codes cost a few hundred bytes, which parts of a photograph that
// differ little do not pay back. Like libwebp's greedy combining of
// histograms, the pair whose union is estimated to save the most is joined
// first: the estimate is the cheaper of the two trees (the union needs
// about one of them) less what the symbols lose by sharing codes, by their
// entropy. A join is made only when the union's codes, sized exactly,
// confirm the saving. assign is renumbered to the sets left.
func mergeSets(sets []*histSet, assign []uint16) merged {
	n := len(sets)
	m := merged{bits: make([]int64, n), alive: make([]bool, n)}
	if n == 0 {
		return m
	}
	lens := make([]uint8, len(sets[0].green))
	ent := make([]float64, n)
	for i, s := range sets {
		m.bits[i] = s.bits(lens)
		ent[i] = setEntropy(s)
		m.alive[i] = true
	}
	if n < 2 {
		m.sets = sets
		return m
	}
	bits, alive := m.bits, m.alive
	to := make([]int, n) // the set each one went into
	for i := range to {
		to[i] = i
	}
	gain := make([]float64, n*n) // pair a<b at a*n+b
	rejected := make([]bool, n*n)
	est := func(a, b int) float64 {
		ta, tb := float64(bits[a])-ent[a], float64(bits[b])-ent[b]
		return min(ta, tb) - (pairEntropy(sets[a], sets[b]) - ent[a] - ent[b])
	}
	for a := 0; a < n; a++ {
		for b := a + 1; b < n; b++ {
			gain[a*n+b] = est(a, b)
		}
	}
	var trial *histSet
	joined := false
	for {
		ba, bb, bg := -1, -1, 0.0
		for a := 0; a < n; a++ {
			if !alive[a] {
				continue
			}
			for b := a + 1; b < n; b++ {
				if alive[b] && !rejected[a*n+b] && gain[a*n+b] > bg {
					ba, bb, bg = a, b, gain[a*n+b]
				}
			}
		}
		if ba < 0 {
			break
		}
		if trial == nil {
			trial = newHistSetLike(sets[0])
		}
		sumInto(trial, sets[ba], sets[bb])
		if mb := trial.bits(lens); mb < bits[ba]+bits[bb] {
			copySet(sets[ba], trial)
			bits[ba], ent[ba] = mb, setEntropy(sets[ba])
			alive[bb], to[bb], joined = false, ba, true
			for k := 0; k < n; k++ {
				if alive[k] && k != ba {
					a, b := min(ba, k), max(ba, k)
					gain[a*n+b], rejected[a*n+b] = est(a, b), false
				}
			}
		} else {
			rejected[ba*n+bb] = true
		}
	}
	if !joined {
		m.sets = sets
		return m
	}
	idx := make([]uint16, n)
	var left []int64
	for i, s := range sets {
		if alive[i] {
			idx[i] = uint16(len(m.sets))
			m.sets = append(m.sets, s)
			left = append(left, bits[i])
		}
	}
	m.bits = left
	final := make([]uint16, n)
	for i := range final {
		r := i
		for !alive[r] {
			r = to[r]
		}
		final[i] = idx[r]
	}
	for i, a := range assign {
		assign[i] = final[a]
	}
	return m
}

// mergeMem is what mergeSets allocates for n sets of the grouper's
// alphabets (a symbols in all).
func mergeMem(n int, a int64) int64 {
	return a*5 + int64(n*n)*9 + int64(n)*48
}

func newHistSetLike(s *histSet) *histSet {
	return &histSet{
		green: make([]uint32, len(s.green)), red: make([]uint32, len(s.red)),
		blue: make([]uint32, len(s.blue)), alpha: make([]uint32, len(s.alpha)), dist: make([]uint32, len(s.dist)),
	}
}

func sumInto(dst, a, b *histSet) {
	d, x, y := dst.lists(), a.lists(), b.lists()
	for k := range d {
		for i := range d[k] {
			d[k][i] = x[k][i] + y[k][i]
		}
	}
}

func copySet(dst, src *histSet) {
	d, s := dst.lists(), src.lists()
	for k := range d {
		copy(d[k], s[k])
	}
}

// xlog2Small holds v*log2(v) for the counts most symbols have.
var xlog2Small = func() (t [4096]float64) {
	for v := 1; v < len(t); v++ {
		t[v] = float64(float64(v) * math.Log2(float64(v)))
	}
	return t
}()

func xlog2(v uint32) float64 {
	if v < uint32(len(xlog2Small)) {
		return xlog2Small[v]
	}
	f := float64(v)
	return float64(f * math.Log2(f))
}

// listEntropy is entropy(l), from the table where it can.
func listEntropy(l []uint32) float64 {
	var n uint64
	var s float64
	for _, c := range l {
		if c != 0 {
			n += uint64(c)
			s -= xlog2(c)
		}
	}
	if n == 0 {
		return 0
	}
	f := float64(n)
	return s + float64(f*math.Log2(f))
}

func setEntropy(s *histSet) float64 {
	var e float64
	for _, l := range s.lists() {
		e += listEntropy(l)
	}
	return e
}

// pairEntropy is setEntropy of the sum of a and b.
func pairEntropy(a, b *histSet) float64 {
	var e float64
	x, y := a.lists(), b.lists()
	for k := range x {
		var n uint64
		var s float64
		yk := y[k][:len(x[k])]
		for i, c := range x[k] {
			if v := c + yk[i]; v != 0 {
				n += uint64(v)
				s -= xlog2(v)
			}
		}
		if n != 0 {
			f := float64(n)
			e += s + float64(f*math.Log2(f))
		}
	}
	return e
}
