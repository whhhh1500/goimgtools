package deflate

import (
	"bytes"
	"math/rand/v2"
	"testing"
)

// TestMinPackedCostSIMD: the vector code gives the minimum the Go code
// gives, for every number of sums up to 300 and wherever the least is.
func TestMinPackedCostSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(31, 32))
	for n := 4; n <= 300; n++ {
		for range 20 {
			lc, ct := make([]uint32, n), make([]uint32, n)
			for i := range lc {
				lc[i], ct[i] = rng.Uint32(), rng.Uint32()
				if rng.IntN(3) == 0 {
					lc[i], ct[i] = 1<<31+uint32(rng.IntN(1000)), 1<<30+uint32(rng.IntN(1000))
				}
			}
			// The least, alone, at a place of its own.
			at := rng.IntN(n)
			lc[at], ct[at] = uint32(rng.IntN(3)), uint32(rng.IntN(3))
			if got, want := minPackedCostSIMD(&lc[0], &ct[0], n), minPackedCostGo(lc, ct); got != want {
				t.Fatalf("%d sums, the least at %d: %d, want %d", n, at, got, want)
			}
		}
	}
}

// TestSlideSIMD: the vector code slides the matchfinder's positions as the
// Go code does.
func TestSlideSIMD(t *testing.T) {
	if !useSIMD {
		t.Skip("no SIMD")
	}
	rng := rand.New(rand.NewPCG(33, 34))
	a, b := new(btMatchfinder), new(btMatchfinder)
	fill := func(p []int16) {
		for i := range p {
			switch rng.IntN(4) {
			case 0:
				p[i] = int16(rng.IntN(1 << 16)) // any
			case 1:
				p[i] = []int16{-32768, -32767, -1, 0, 1, 32767}[rng.IntN(6)]
			default:
				p[i] = int16(rng.IntN(1 << 15)) // a position of the window
			}
		}
	}
	for i := range a.hash3 {
		fill(a.hash3[i][:])
	}
	fill(a.hash4[:])
	fill(a.child[:])
	*b = *a
	a.slide()
	b.slideGo()
	if *a != *b {
		t.Fatal("the slide differs from the Go code's")
	}
}

// residuals are bytes like the filtered rows of a photograph: small
// differences either side of 0, with here and there a stretch seen before,
// so that most positions have a match or two of 3 to 6 bytes.
func residuals(n int, seed uint64) []byte {
	rng := rand.New(rand.NewPCG(seed, 7))
	d := make([]byte, 0, n)
	for len(d) < n {
		if len(d) > 100 && rng.IntN(40) == 0 {
			from := len(d) - 1 - rng.IntN(min(len(d)-1, 30000))
			d = append(d, d[from:min(len(d), from+3+rng.IntN(9))]...)
			continue
		}
		d = append(d, byte(int(rng.NormFloat64()*2.5)))
	}
	return d[:n]
}

// TestAssemblySame: with the assembly and the vector code the compressor
// writes what it writes with the Go code alone.
func TestAssemblySame(t *testing.T) {
	if !useSIMD && !dpAsm {
		t.Skip("no assembly")
	}
	with := [2]bool{useSIMD, dpAsm}
	defer func() { useSIMD, dpAsm = with[0], with[1] }()
	nine := Level12
	nine.NoSkip, nine.SplitByCost, nine.SplitPasses, nine.EntropyCosts, nine.BlockLen, nine.CachePerBlock = true, true, 2, true, 256<<10, 3
	nine.FineSplit, nine.MoreCodes = true, true
	eight := Level10
	eight.SplitByCost, eight.SplitPasses, eight.EntropyCosts, eight.BlockLen, eight.CachePerBlock = true, 1, true, 128<<10, 3
	eight.FineSplit, eight.MoreCodes = true, true
	s := samples()
	data := map[string][]byte{
		"photo": residuals(400<<10, 1), "photo2": residuals(70<<10, 2), "mixed": s["mixed"], "rows": s["rows"],
		"text": s["text"][:150000], "zeros": s["zeros"][:300000], "random": s["random"][:100000], "short": s["short"],
		"both": append(residuals(200<<10, 3), s["rows"][:150000]...),
	}
	for _, p := range []Params{nine, eight, Level10, Level12} {
		for name, d := range data {
			var out [2][]byte
			for k := range out {
				useSIMD, dpAsm = with[0] && k == 0, with[1] && k == 0
				out[k] = New(p).Compress(nil, d, 0, true)
			}
			if !bytes.Equal(out[0], out[1]) {
				t.Fatalf("%s (depth %d, split %v): %d bytes with the assembly, %d without", name, p.SearchDepth, p.SplitByCost, len(out[0]), len(out[1]))
			}
			if got := inflate(t, nil, out[0]); !bytes.Equal(got, d) {
				t.Fatalf("%s: round trip differs", name)
			}
		}
	}
}

// randomParse fills c.items with a parse of n bytes: literals whose values
// change along the block, and matches, more of them in some stretches.
func randomParse(c *Compressor, n int, rng *rand.Rand) {
	for i := 0; i < n; {
		zone := i * 6 / n
		if l := 3 + rng.IntN(20); rng.IntN(3+2*(zone%3)) == 0 && i+l <= n {
			c.items[i] = uint32(l)<<optimumLenShift | uint32(1+rng.IntN(1<<(4+2*zone)))
			i += l
			continue
		}
		c.items[i] = uint32(40*zone + rng.IntN(20+30*(zone%2)))
		i++
	}
}

// TestFindMinimumSame: the split point and its cost are those found by
// counting the symbols of each part of each point anew, as findMinimum
// first did.
func TestFindMinimumSame(t *testing.T) {
	// findMinimum as it was.
	old := func(c *Compressor, a, b int) (int, uint32) {
		const num = 9
		lo, hi := a+minSplitBlock, b-minSplitBlock
		best, bestCost := -1, uint32(1<<32-1)
		last := uint32(1<<32 - 1)
		for hi-lo > num {
			var ps [num]int
			var vs [num]uint32
			bi := 0
			for i := range ps {
				ps[i] = c.nextItem(a, lo+(i+1)*((hi-lo)/(num+1)), b)
				vs[i] = c.rangeCost(a, ps[i]) + c.rangeCost(ps[i], b)
				if vs[i] < vs[bi] {
					bi = i
				}
			}
			if vs[bi] > last {
				break
			}
			best, bestCost, last = ps[bi], vs[bi], vs[bi]
			nlo, nhi := lo, hi
			if bi > 0 {
				nlo = ps[bi-1]
			}
			if bi < num-1 {
				nhi = ps[bi+1]
			}
			if nhi-nlo >= hi-lo {
				break
			}
			lo, hi = nlo, nhi
		}
		return best, bestCost
	}
	p := Level12
	p.SplitByCost, p.FineSplit, p.BlockLen = true, true, 256<<10
	c := New(p)
	rng := rand.New(rand.NewPCG(41, 42))
	for round := range 6 {
		n := []int{3000, 20000, 100000, 262144, 5000, 262000}[round]
		randomParse(c, n, rng)
		c.markItems(n)
		// Ranges that start and end at items.
		for range 40 {
			a := c.nextItem(0, rng.IntN(n), n)
			b := c.nextItem(0, a+rng.IntN(n-a+1), n)
			if rng.IntN(4) == 0 {
				a, b = 0, n
			}
			gp, gc := c.findMinimum(a, b)
			wp, wc := old(c, a, b)
			if gp != wp || gc != wc {
				t.Fatalf("%d bytes, [%d, %d): point %d cost %d, want %d and %d", n, a, b, gp, gc, wp, wc)
			}
		}
	}
}

// TestSideCountsSet: counts set at once are the counts added one by one.
func TestSideCountsSet(t *testing.T) {
	rng := rand.New(rand.NewPCG(43, 44))
	var f freqs
	var one sideCounts
	for range 20000 {
		if rng.IntN(4) == 0 {
			sym := rng.IntN(numOffsetSyms)
			f.offset[sym]++
			one.add(sym, true, 1)
		} else {
			sym := rng.IntN(numLitlenSyms) % (20 + rng.IntN(260))
			f.litlen[sym]++
			one.add(sym, false, 1)
		}
	}
	var all sideCounts
	all.set(&f)
	if all != one {
		t.Fatal("the counts set at once differ from those added one by one")
	}
	if all.entropy() != one.entropy() {
		t.Fatal("the entropy differs")
	}
}

// TestSimplePositionsAsm: the assembly works out the positions the Go code
// does, with the same costs and items, whatever their matches: none to
// five, of every set of lengths from 3 to 8, at any offsets.
func TestSimplePositionsAsm(t *testing.T) {
	if !dpAsm {
		t.Skip("no assembly")
	}
	rng := rand.New(rand.NewPCG(51, 52))
	const n = 5000
	p := Params{BlockLen: n, CachePerBlock: 6}
	rebased := 0
	for round := range 40 {
		var cs [2]*Compressor
		for k := range cs {
			cs[k] = New(p)
		}
		c := cs[0]
		// The cache: for each position its matches, each longer than the
		// one before, then its entry.
		cp := 0
		for i := 0; i < n; i++ {
			matches := 0
			for l := 3; l <= 8 && matches < 5; l++ {
				if rng.IntN(1+round%4) == 0 {
					c.cache[cp] = lzMatch{uint16(l), uint16(1 + rng.IntN(1<<(1+rng.IntN(15))))}
					cp++
					matches++
				}
			}
			c.cache[cp] = lzMatch{uint16(matches), uint16(rng.IntN(256))}
			cp++
		}
		for i := range c.dp.literal {
			c.dp.literal[i] = uint32(16 + rng.IntN(200))
		}
		for i := range c.dp.slot {
			c.dp.slot[i] = uint32(rng.IntN(500))
		}
		for l := minMatch; l <= maxMatch; l++ {
			c.dp.length[l] = uint32(rng.IntN(300))<<packedCostShift | uint32(l)
		}
		// The cost so far, and those to the end past the block near it, as
		// they are in the middle of a block; in some rounds the cost soon
		// needs rebasing, in others the base is not 0.
		i, best, baseCost := n, uint32(5000+rng.IntN(100000)), uint32(0)
		switch round % 5 {
		case 0:
			best = rebaseThreshold - 3000
		case 1:
			baseCost = uint32(rng.IntN(1 << 20))
		}
		for i := range c.costToEnd {
			c.costToEnd[i] = (best - uint32(rng.IntN(2000))) << packedCostShift
		}
		copy(cs[1].cache, c.cache)
		copy(cs[1].costToEnd, c.costToEnd)
		cs[1].dp = c.dp
		// From the end back: as far as each goes, then one position by
		// hand where they stop.
		for i > 0 {
			gi, gcp, gbest := simplePositionsAsm(&cs[1].cache[0], &cs[1].costToEnd[0], &cs[1].items[0], &cs[1].dp, i, cp, best, baseCost)
			wi, wcp, wbest := c.simplePositions(i, cp, best, baseCost)
			if gi != wi || gcp != wcp || gbest != wbest {
				t.Fatalf("round %d from %d (entry %d): stops at %d (entry %d) with %d, want %d (entry %d) with %d", round, i, cp, gi, gcp, gbest, wi, wcp, wbest)
			}
			i, cp, best = wi, wcp, wbest
			if best >= rebaseThreshold {
				best = rebaseThreshold - 2000
				rebased++
			} else if i > 0 {
				i--
				cp -= 1 + int(c.cache[cp-1].length)
				best += uint32(20 + rng.IntN(100))
				for _, x := range cs {
					x.costToEnd[i], x.items[i] = (best+baseCost)<<packedCostShift, uint32(i)
				}
			}
		}
		for k := range c.items[:n] {
			if cs[1].items[k] != c.items[k] || cs[1].costToEnd[k] != c.costToEnd[k] {
				t.Fatalf("round %d, position %d: item %#x and cost %#x, want %#x and %#x", round, k, cs[1].items[k], cs[1].costToEnd[k], c.items[k], c.costToEnd[k])
			}
		}
	}
	if rebased < 10 {
		t.Fatalf("the cost needed rebasing %d times: too few to tell", rebased)
	}
}
