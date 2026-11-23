package pngstream

import (
	"encoding/binary"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The parse of bruteFilter and of the estimator, as first written: a
// position at a time, its chain followed as soon as it is entered. The tests
// below hold the parse a stretch at a time, and its vector code, to it.

// refChains are the hash chains of all the data so far.
type refChains struct {
	d      []byte
	head   []int
	prev   []int // by position: how far back the one before with its hash is, 0 for none
	bits   uint
	window int
	chain  int
	undo   [][2]int
}

func newRefChains(bits uint, window, chain int) *refChains {
	r := &refChains{head: make([]int, 1<<bits), bits: bits, window: window, chain: chain}
	for i := range r.head {
		r.head[i] = -1
	}
	return r
}

// insert enters position p and returns the one before with its hash.
func (r *refChains) insert(p int, trying bool) int {
	h := binary.LittleEndian.Uint32(r.d[p:]) * 2654435761 >> (32 - r.bits)
	c := r.head[h]
	if trying {
		r.undo = append(r.undo, [2]int{int(h), c})
	}
	r.head[h] = p
	for len(r.prev) <= p {
		r.prev = append(r.prev, 0)
	}
	r.prev[p] = 0
	if c >= 0 && p-c < r.window {
		r.prev[p] = p - c
	}
	return c
}

// parse counts the symbols of the greedy parse of d[from:], and leaves the
// chains as they were when trying.
func (r *refChains) parse(from int, trying bool) (hist [nSyms]int64, extra int64) {
	d, n := r.d, len(r.d)
	r.undo = r.undo[:0]
	for p := from; p < n; {
		if p+4 <= n {
			c := r.insert(p, trying)
			l, at := 0, -1
			for k := 0; k < r.chain && c >= 0 && p-c < r.window; k++ {
				if d[c] == d[p] && d[c+1] == d[p+1] && d[c+2] == d[p+2] {
					if j := matchLen(d, p, c, 3, min(258, n-p)); j > l {
						l, at = j, c
					}
				}
				if r.prev[c] == 0 {
					break
				}
				c -= r.prev[c]
			}
			if at >= 0 {
				ls, lx := lengthCode(l)
				ds, dx := distCode(p - at)
				hist[ls]++
				hist[286+ds]++
				extra += int64(lx + dx)
				for q := p + 1; q < p+l && q+4 <= n; q++ {
					r.insert(q, trying)
				}
				p += l
				continue
			}
		}
		hist[d[p]]++
		p++
	}
	if trying {
		for i := len(r.undo) - 1; i >= 0; i-- {
			r.head[r.undo[i][0]] = r.undo[i][1]
		}
	}
	return hist, extra
}

// testRows are rows of several kinds, of rowBytes each (with the type byte's
// place): noise like a photograph's, flat stretches and runs, the row
// before again with a few changes, rows of zeros, and rows seen long before.
func testRows(rng *rand.Rand, rowBytes, n int) [][]byte {
	rows := make([][]byte, n)
	for y := range rows {
		row := make([]byte, rowBytes-1)
		switch kind := rng.IntN(8); {
		case kind == 0 && y > 0:
			copy(row, rows[y-1])
			for range 1 + rng.IntN(4) {
				row[rng.IntN(len(row))] = byte(rng.IntN(256))
			}
		case kind == 1:
			// zeros
		case kind == 2:
			v := byte(rng.IntN(256))
			for i := range row {
				if rng.IntN(60) == 0 {
					v = byte(rng.IntN(256))
				}
				row[i] = v
			}
		case kind == 3 && y > 8:
			copy(row, rows[rng.IntN(y)])
		default:
			v := 128
			for i := range row {
				v += int(rng.NormFloat64() * 3)
				row[i] = byte(v)
			}
		}
		rows[y] = row
	}
	return rows
}

// TestBruteParseSame: Brute chooses the filters, from the same symbols of
// each candidate, as with the parse a position at a time: rows shorter than
// four bytes and longer than a stretch, enough of them for the window to
// move and the chains to go round, with both lengths of chain.
func TestBruteParseSame(t *testing.T) {
	rng := rand.New(rand.NewPCG(61, 62))
	for _, c := range []struct{ rowBytes, bpp, rows, chain int }{
		{2, 1, 50, 16}, {3, 2, 50, 256}, {4, 3, 60, 16}, {5, 4, 60, 16}, {8, 1, 300, 256}, {61, 3, 400, 16},
		{301, 3, 300, 256}, {1500, 4, 120, 16}, {4096, 1, 40, 16}, {4099, 3, 40, 256}, {4100, 3, 30, 16}, {9001, 3, 30, 256}, {70001, 4, 6, 16},
	} {
		b, err := newBruteFilter(pipeline.NewEnv(0, ""), c.rowBytes, c.chain, false, 0)
		if err != nil {
			t.Fatal(err)
		}
		ref := newRefChains(bruteHashBits, bruteWindow, c.chain)
		var sums [bruteLines - 1][nSyms]int64 // the symbols of the last rows chosen
		var extras [bruteLines - 1]int64
		rows := testRows(rng, c.rowBytes, c.rows)
		prev := make([]byte, c.rowBytes-1)
		cand := make([]byte, c.rowBytes)
		for y, cur := range rows {
			got := b.choose(cur, prev, c.bpp)
			// The parse a position at a time of each candidate after the
			// rows chosen, and the cost of each.
			zero := true
			for _, v := range cur {
				zero = zero && v == 0
			}
			best, bestCost := ftNone, int64(-1)
			var bestHist [nSyms]int64
			var bestExtra int64
			base := len(ref.d)
			for f := ftNone; f <= ftPaeth; f++ {
				if zero && f != ftNone {
					break
				}
				filterRow(cand, cur, prev, c.bpp, f)
				ref.d = append(ref.d[:base], cand...)
				hist, extra := ref.parse(base, true)
				if hist != b.candHist[f] || extra != b.candExtra[f] {
					t.Fatalf("rows of %d, chain %d, row %d, filter %d: the symbols differ from the parse a position at a time (extra bits %d, want %d)", c.rowBytes, c.chain, y, f, b.candExtra[f], extra)
				}
				var total [nSyms]int64
				sumExtra := extra
				for s := range total {
					total[s] = hist[s]
					for k := range sums {
						total[s] += sums[k][s]
					}
				}
				for _, x := range extras {
					sumExtra += x
				}
				total[256]++
				cost := b.huffmanBits(total[:286]) + b.huffmanBits(total[286:]) + sumExtra
				if bestCost < 0 || cost < bestCost {
					best, bestCost, bestHist, bestExtra = f, cost, hist, extra
				}
			}
			if int(got[0]) != best {
				t.Fatalf("rows of %d, chain %d, row %d: filter %d, want %d", c.rowBytes, c.chain, y, got[0], best)
			}
			filterRow(cand, cur, prev, c.bpp, best)
			ref.d = append(ref.d[:base], cand...)
			ref.parse(base, false)
			sums[y%len(sums)], extras[y%len(extras)] = bestHist, bestExtra
			prev = cur
		}
		b.release()
	}
}

// TestEstimateSame: the estimate is that of the parse a position at a time,
// with and without data before, for both estimators.
func TestEstimateSame(t *testing.T) {
	rng := rand.New(rand.NewPCG(63, 64))
	for _, c := range []struct {
		bits   uint
		window int
	}{{estHashBits, estWindow}, {estHashBitsNear, estWindowNear}} {
		e := newEstimator(c.bits, c.window)
		for round := range 60 {
			rowBytes := []int{2, 5, 40, 301, 2000, 9001}[rng.IntN(6)]
			var p []byte
			for _, row := range testRows(rng, rowBytes, 1+rng.IntN(1+90000/rowBytes/(1+round%3*4))) {
				p = append(append(p, byte(rng.IntN(5))), row...)
			}
			for _, start := range []int{0, 1, 3, 4, 5, len(p) / 3, min(len(p), c.window), len(p) - 1, len(p)} {
				if start < 0 || start > len(p) {
					continue
				}
				ref := newRefChains(c.bits, c.window, estChain)
				ref.d = p
				for i := 0; i+4 <= start; i++ {
					ref.insert(i, false)
				}
				hist, extra := ref.parse(start, false)
				hist[256]++
				var h symHeap
				want := h.cost(hist[:286]) + h.cost(hist[286:]) + extra
				if got := e.estimateAfter(p, start); got != want {
					t.Fatalf("window %d, %d bytes of rows of %d after %d: %d bits, want %d", c.window, len(p)-start, rowBytes, start, got, want)
				}
			}
		}
	}
}

// TestMarkChainsSIMD: the vector code marks the positions the Go code marks,
// where the chains go round the end of their table and at the end of the
// data too.
func TestMarkChainsSIMD(t *testing.T) {
	if !chainsSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(65, 66))
	for round := range 300 {
		window := []int{8 << 10, 32 << 10}[round%2]
		ring := 2 * window << (round / 2 % 2)
		prev := make([]uint16, ring, ring+1)
		for i := range prev {
			switch rng.IntN(4) {
			case 0:
				prev[i] = uint16(1 + rng.IntN(window-1))
			case 1:
				prev[i] = uint16(1 + rng.IntN(300))
			}
		}
		at := window + rng.IntN(1000)
		n := rng.IntN(200)
		if round%7 == 0 {
			n = 4096
		}
		// Up to 20 bytes after the last position, or just its four.
		d := make([]byte, at+n+3+rng.IntN(20))
		for i := range d {
			d[i] = byte(rng.IntN(3))
		}
		first := rng.IntN(ring)
		if round%3 == 0 {
			first = ring - rng.IntN(n+10) // the entries go round the end
			first = max(first, 0)
		}
		got, want := make([]uint8, n), make([]uint8, n)
		markChains(d, prev, at, first, window, got)
		markChainsGo(d, prev, at, first, window, want)
		for k := range want {
			if got[k] != want[k] {
				t.Fatalf("round %d (%d positions, the first's entry %d of %d): position %d marked %d, want %d", round, n, first, ring, k, got[k], want[k])
			}
		}
	}
}
