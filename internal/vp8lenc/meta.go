package vp8lenc

import (
	"math"
)

// Huffman groups. One set of prefix codes for a whole image is a compromise
// between its flat and its busy parts. VP8L lets every block of the image
// (the "entropy image") select one of several groups of codes; this file
// decides how many groups there are and which block uses which.
//
// It works on the token stream of pass one, replayed from the spill, so it
// needs no pixels: one histogram per block of the block row being replayed,
// and one histogram and cost table per group.
//
//  1. A first replay sorts the blocks into groups by how many bits per
//     symbol their own statistics need (flat ... busy).
//  2. Further replays move every block to the group whose codes are
//     cheapest for it and rebuild the groups from their new members, the
//     way k-means does.
//
// The histograms of the last replay are exactly those of the symbols each
// group will code, so the size of the result is known before it is written.

// grouper is a symSink that collects block histograms and clusters them.
type grouper struct {
	hbits  uint
	tw, th int
	green  int // size of the green alphabet (with the cache symbols)
	alpha  int // size of all five alphabets laid end to end

	// The block row being replayed: counts per block and unified symbol,
	// and for each block the symbols that occur.
	row     []uint16
	touched [][]uint16
	curRow  int

	groups  []*group
	assign  []uint16 // group per block; noGroup while not known
	last    uint16   // group of the previous block with symbols
	binning bool     // first replay: sort blocks by their own entropy
	binMax  float64  // bits per token that map to the last group
}

const noGroup = 0xffff

type group struct {
	hist  []uint32  // of the members chosen in this replay
	cost  []uint16  // 1/16 bit per symbol, from the previous replay
	total [5]uint64 // symbols per alphabet in hist
}

// The five alphabets in the unified symbol space.
func (g *grouper) offsets() [6]int {
	return [6]int{0, g.green, g.green + 256, g.green + 512, g.green + 768, g.alpha}
}

// grouperScratch is what one grouper after another works in: the
// encoder tries a dozen splits or more, and each would otherwise leave
// megabytes behind for the collector.
type grouperScratch struct {
	row     []uint16
	touched [][]uint16
	hists   [][]uint32
	costs   [][]uint16
}

func newGrouper(width, height int, hbits uint, cacheBits uint, ngroups int, lit1 bool, sc *grouperScratch) *grouper {
	g := &grouper{hbits: hbits, binning: true, last: noGroup}
	g.tw, g.th = (width+1<<hbits-1)>>hbits, (height+1<<hbits-1)>>hbits
	g.green = numLiteral + numLength
	if cacheBits != 0 {
		g.green += 1 << cacheBits
	}
	g.alpha = g.green + 3*256 + numDistance
	if n := g.tw * g.alpha; cap(sc.row) < n {
		sc.row = make([]uint16, n)
	} else {
		sc.row = sc.row[:n]
		clear(sc.row)
	}
	g.row = sc.row
	for len(sc.touched) < g.tw {
		sc.touched = append(sc.touched, nil)
	}
	g.touched = sc.touched[:g.tw]
	for i := range g.touched {
		g.touched[i] = g.touched[i][:0]
	}
	g.assign = make([]uint16, g.tw*g.th) // kept by the split chosen
	for i := 0; i < ngroups; i++ {
		if i == len(sc.hists) {
			sc.hists, sc.costs = append(sc.hists, nil), append(sc.costs, nil)
		}
		if cap(sc.hists[i]) < g.alpha {
			sc.hists[i], sc.costs[i] = make([]uint32, g.alpha), make([]uint16, g.alpha)
		} else {
			sc.hists[i], sc.costs[i] = sc.hists[i][:g.alpha], sc.costs[i][:g.alpha]
			clear(sc.hists[i])
			clear(sc.costs[i])
		}
		g.groups = append(g.groups, &group{hist: sc.hists[i], cost: sc.costs[i]})
	}
	// A block's own entropy tops out near log2 of its size per alphabet,
	// and a colour pixel has three or four alphabets that vary.
	g.binMax = 16
	if lit1 {
		g.binMax = 6
	}
	return g
}

// grouperMem is what a grouper allocates.
func grouperMem(width, height int, hbits uint, cacheBits uint, ngroups int) int64 {
	tw, th := int64((width+1<<hbits-1)>>hbits), int64((height+1<<hbits-1)>>hbits)
	a := int64(numLiteral + numLength + 3*256 + numDistance)
	if cacheBits != 0 {
		a += 1 << cacheBits
	}
	return tw*a*4 + tw*th*2 + int64(ngroups)*a*6
}

func (g *grouper) add(x, y int, sym int) {
	if r := y >> g.hbits; r != g.curRow {
		g.flushRows(r)
	}
	t := x >> g.hbits
	c := &g.row[t*g.alpha+sym]
	if *c == 0 {
		g.touched[t] = append(g.touched[t], uint16(sym))
	}
	*c++
}

func (g *grouper) literal(x, y int, p uint32) {
	g.add(x, y, int(p>>8&0xff))
	g.add(x, y, g.green+int(p>>16&0xff))
	g.add(x, y, g.green+256+int(p&0xff))
	g.add(x, y, g.green+512+int(p>>24))
}

func (g *grouper) cacheHit(x, y int, index uint32) {
	g.add(x, y, numLiteral+numLength+int(index))
}

func (g *grouper) copyRef(x, y, length int, dcode uint32) {
	ls, _, _ := prefixCode(uint32(length))
	ds, _, _ := prefixCode(dcode)
	g.add(x, y, numLiteral+int(ls))
	g.add(x, y, g.green+768+int(ds))
}

// selfBits is the entropy of one block's symbols, and the number of tokens
// (green symbols) among them.
func (g *grouper) selfBits(counts []uint16, syms []uint16) (bits float64, n int) {
	off := g.offsets()
	var tot [5]int
	for _, s := range syms {
		c := int(counts[s])
		a := 0
		for int(s) >= off[a+1] {
			a++
		}
		tot[a] += c
		bits -= float64(c) * math.Log2(float64(c))
	}
	for _, t := range tot {
		if t > 0 {
			bits += float64(t) * math.Log2(float64(t))
		}
	}
	return bits, tot[0]
}

// flushRows assigns the blocks of the current block row to groups and
// moves on to block row next. Blocks no symbol starts in (they lie inside
// long copies) take the group of the block before them, which costs the
// entropy image least.
func (g *grouper) flushRows(next int) {
	for t := 0; t < g.tw; t++ {
		syms := g.touched[t]
		if len(syms) == 0 {
			g.assign[g.curRow*g.tw+t] = g.last
			continue
		}
		counts := g.row[t*g.alpha : (t+1)*g.alpha]
		best := -1
		if g.binning {
			bits, n := g.selfBits(counts, syms)
			best = min(int(bits/float64(n)/g.binMax*float64(len(g.groups))), len(g.groups)-1)
		} else {
			bestCost := uint64(math.MaxUint64)
			for i, gr := range g.groups {
				if gr.cost == nil {
					continue
				}
				var c uint64
				cost := gr.cost
				for _, s := range syms {
					c += uint64(counts[s]) * uint64(cost[s])
				}
				if c < bestCost || best < 0 {
					best, bestCost = i, c
				}
			}
		}
		hist := g.groups[best].hist
		for _, s := range syms {
			hist[s] += uint32(counts[s])
			counts[s] = 0
		}
		g.touched[t] = syms[:0]
		g.last = uint16(best)
		g.assign[g.curRow*g.tw+t] = g.last
	}
	for r := g.curRow + 1; r < next && r < g.th; r++ {
		for t := 0; t < g.tw; t++ {
			g.assign[r*g.tw+t] = g.last
		}
	}
	g.curRow = next
}

// endReplay closes a replay. It reports the number of groups in use.
func (g *grouper) endReplay() int {
	g.flushRows(g.th)
	used := 0
	off := g.offsets()
	for _, gr := range g.groups {
		gr.total = [5]uint64{}
		for a := 0; a < 5; a++ {
			for _, c := range gr.hist[off[a]:off[a+1]] {
				gr.total[a] += uint64(c)
			}
		}
		if gr.total[0] != 0 {
			used++
		}
	}
	return used
}

// beginReplay prepares the next replay: the groups' cost tables come from
// the histograms just collected, which are then cleared. Groups that lost
// all their members are out.
func (g *grouper) beginReplay() {
	g.binning = false
	g.curRow, g.last = 0, noGroup
	off := g.offsets()
	for _, gr := range g.groups {
		if gr.total[0] == 0 {
			gr.cost = nil
			continue
		}
		for a := 0; a < 5; a++ {
			lt := math.Log2(float64(gr.total[a]) + 1)
			for s := off[a]; s < off[a+1]; s++ {
				// A symbol the group has not seen is expensive, not
				// impossible: the code is rebuilt from the new members.
				v := 16 * (lt - math.Log2(float64(gr.hist[s])+0.25))
				gr.cost[s] = uint16(max(1, min(v, 16*24)))
			}
		}
		clear(gr.hist)
	}
}

// result returns the groups in use as histograms, with assign renumbered
// to match and the blocks before the first symbol resolved.
func (g *grouper) result() []*histSet {
	off := g.offsets()
	var sets []*histSet
	renum := make([]uint16, len(g.groups))
	for i, gr := range g.groups {
		if gr.total[0] == 0 {
			continue
		}
		renum[i] = uint16(len(sets))
		h := gr.hist
		sets = append(sets, &histSet{
			green: h[off[0]:off[1]], red: h[off[1]:off[2]], blue: h[off[2]:off[3]],
			alpha: h[off[3]:off[4]], dist: h[off[4]:off[5]],
		})
	}
	first := uint16(0)
	for _, a := range g.assign {
		if a != noGroup {
			first = renum[a]
			break
		}
	}
	for i, a := range g.assign {
		if a == noGroup {
			g.assign[i] = first
		} else {
			g.assign[i] = renum[a]
		}
	}
	return sets
}
