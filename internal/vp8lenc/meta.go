package vp8lenc

import (
	"math"
	"slices"
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
//  3. Groups whose codes cost more than they save are joined (merge.go),
//     and if any were, one more replay lets every block choose again
//     among the groups left, as libwebp's remapping does after its
//     combining; the groups are then joined again where that pays.
//
// The histograms of the last replay are exactly those of the symbols each
// group will code, so the size of the result is known before it is written.
//
// What a block holds does not change from one replay to the next, only the
// group it goes to: the first replay keeps every block's symbols and their
// counts, when the budget has room, and the replays after it go through
// those (rerun) instead of the tokens, to the same result.

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

	// With keep, the symbols of every block, kept by the first replay in
	// chunks of blkChunk words: block b's are
	// blks[blkAt[b]>>16][blkAt[b]&0xffff:][:blkLen[b]], each a word of
	// count<<12 | symbol, or for a count above 15 the symbol and then the
	// count. dec is a block's, decoded (symbol<<16 | count).
	keep   bool
	chunk  int // words in a chunk: blkChunk, or fewer for a small image
	chunks int // chunks at most, as keepMem counts them
	blks   [][]uint16
	blkAt  []uint32
	blkLen []uint16
	dec    []uint32
	// With par, the replays from the kept symbols choose on several cores
	// (workers at most at once): choice and decs (a dec for each) are
	// theirs.
	par     func(n int, fn func(int))
	workers int
	choice  []uint16
	decs    chan []uint32
}

// blkChunk is the number of words in a chunk of blks; a block's (two for
// each symbol of the five alphabets at most) fit in one.
const blkChunk = 1 << 16

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

// With keep, the first replay keeps the blocks' symbols, of which there
// are words at most (see keepMem).
func newGrouper(width, height int, hbits uint, cacheBits uint, ngroups int, lit1 bool, sc *grouperScratch, keep bool, words int64) *grouper {
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
	if keep && g.alpha <= blkMaxWords/2 {
		g.keep = true
		g.chunk, g.chunks = keepChunks(words)
		g.blkAt, g.blkLen = make([]uint32, g.tw*g.th), make([]uint16, g.tw*g.th)
	}
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
	return tw*a*4 + tw*th*2 + int64(ngroups)*a*6 + mergeMem(ngroups, a)
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
		bits -= float64(float64(c) * math.Log2(float64(c)))
	}
	for _, t := range tot {
		if t > 0 {
			bits += float64(float64(t) * math.Log2(float64(t)))
		}
	}
	return bits, tot[0]
}

// flushRows assigns the blocks of the current block row to groups and
// moves on to block row next. Blocks no symbol starts in (they lie inside
// long copies) take the group of the block before them, which costs the
// entropy image least.
func (g *grouper) flushRows(next int) {
	if g.curRow >= g.th {
		return // rerun
	}
	for t := 0; t < g.tw; t++ {
		syms := g.touched[t]
		if len(syms) == 0 {
			g.assign[g.curRow*g.tw+t] = g.last
			continue
		}
		counts := g.row[t*g.alpha : (t+1)*g.alpha]
		if g.binning && g.keep {
			g.keepBlock(g.curRow*g.tw+t, counts, syms)
		}
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

// keepMem is the most memory a grouper given keep takes for the symbols
// of its blocks, of which there are words at most (a symbol's word, and
// for counts above 15 a second one, stand for at least one occurrence).
// A chunk is started when a block's words do not fit in the last one,
// which leaves fewer than blkMaxWords unused.
func keepMem(width, height int, hbits uint, words int64, workers int) int64 {
	blocks := int64((width+1<<hbits-1)>>hbits) * int64((height+1<<hbits-1)>>hbits)
	c, n := keepChunks(words)
	m := blocks*6 + int64(n)*int64(c)*2 + blkMaxWords*2
	if workers > 1 {
		m += blocks*2 + int64(workers)*blkMaxWords*2 // see grouper.par
	}
	return m
}

// keepChunks is the size of the chunks for words, blkChunk or for fewer
// words one chunk that holds them all, and how many there can be.
func keepChunks(words int64) (size, n int) {
	words = max(words, 1)
	c := min(words+blkMaxWords, blkChunk)
	return int(c), int((words + c - blkMaxWords - 1) / (c - blkMaxWords))
}

// blkMaxWords is the most words a block's symbols take: two for each
// symbol, with the five alphabets (keep is refused for larger ones).
const blkMaxWords = 2 << 12

// keepBlock adds block b's symbols to blks (or stops keeping them, should
// they be more than the caller was told).
func (g *grouper) keepBlock(b int, counts []uint16, syms []uint16) {
	n := len(g.blks)
	if n == 0 || len(g.blks[n-1])+2*len(syms) > g.chunk {
		if n == g.chunks {
			g.keep, g.blks, g.blkAt, g.blkLen = false, nil, nil, nil
			return
		}
		g.blks = append(g.blks, make([]uint16, 0, g.chunk))
		n++
	}
	c := g.blks[n-1]
	at := len(c)
	for _, s := range syms {
		if k := counts[s]; k < 16 {
			c = append(c, k<<12|s)
		} else {
			c = append(c, s, k)
		}
	}
	g.blkAt[b], g.blkLen[b] = uint32(n-1)<<16|uint32(at), uint16(len(c)-at)
	g.blks[n-1] = c
	g.dec = slices.Grow(g.dec, len(syms))
}

// stored reports whether the first replay is over and kept every block's
// symbols.
func (g *grouper) stored() bool { return g.keep && !g.binning }

// rerun is a replay (after beginReplay) from the symbols the first one
// kept: every block goes to the group whose codes are cheapest for it, as
// in flushRows.
//
// With par, the blocks choose their groups on several cores (each choice
// depends on the block and the cost tables only), the groups are then
// filled one block after the other.
func (g *grouper) rerun() {
	nb := g.tw * g.th
	if g.par != nil && nb >= 2*rerunPart {
		if cap(g.choice) < nb {
			g.choice = make([]uint16, nb)
		}
		choice := g.choice[:nb]
		parts := (nb + rerunPart - 1) / rerunPart
		if g.decs == nil {
			g.decs = make(chan []uint32, g.workers)
			for range g.workers {
				g.decs <- make([]uint32, 0, cap(g.dec))
			}
		}
		g.par(parts, func(k int) {
			dec := <-g.decs
			defer func() { g.decs <- dec }()
			for b := k * rerunPart; b < min(nb, (k+1)*rerunPart); b++ {
				if g.blkLen[b] == 0 {
					choice[b] = noGroup
					continue
				}
				var ents []uint32
				ents, dec = g.decode(b, dec)
				choice[b] = uint16(g.cheapest(ents))
			}
		})
		for b, c := range choice {
			if c == noGroup {
				g.assign[b] = g.last
				continue
			}
			var ents []uint32
			ents, g.dec = g.decode(b, g.dec)
			hist := g.groups[c].hist
			for _, e := range ents {
				hist[e>>16] += e & 0xffff
			}
			g.last = c
			g.assign[b] = c
		}
		g.curRow = g.th
		return
	}
	for b := 0; b < nb; b++ {
		if g.blkLen[b] == 0 {
			g.assign[b] = g.last
			continue
		}
		var ents []uint32
		ents, g.dec = g.decode(b, g.dec)
		best := g.cheapest(ents)
		hist := g.groups[best].hist
		for _, e := range ents {
			hist[e>>16] += e & 0xffff
		}
		g.last = uint16(best)
		g.assign[b] = g.last
	}
	g.curRow = g.th
}

// rerunPart is how many blocks a core takes at a time in rerun.
const rerunPart = 512

// decode is block b's symbols (symbol<<16 | count), in buf.
func (g *grouper) decode(b int, buf []uint32) (ents, grown []uint32) {
	at := g.blkAt[b]
	ents = buf[:0]
	words := g.blks[at>>16][at&0xffff:][:g.blkLen[b]]
	for i := 0; i < len(words); i++ {
		s, k := words[i]&0xfff, words[i]>>12
		if k == 0 {
			i++
			k = words[i]
		}
		ents = append(ents, uint32(s)<<16|uint32(k))
	}
	return ents, ents
}

// cheapest is the first of the groups whose codes cost the least for ents.
func (g *grouper) cheapest(ents []uint32) int {
	best, bestCost := -1, uint64(math.MaxUint64)
	for i, gr := range g.groups {
		if gr.cost == nil {
			continue
		}
		var c uint64
		cost := gr.cost
		for _, e := range ents {
			c += uint64(e&0xffff) * uint64(cost[e>>16])
		}
		if c < bestCost || best < 0 {
			best, bestCost = i, c
		}
	}
	return best
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

// retain prepares another replay after mergeSets: alive tells, in the order
// result returned them, which of the groups in use are left; their
// histograms hold what was joined into them.
func (g *grouper) retain(alive []bool) {
	off := g.offsets()
	j := 0
	for _, gr := range g.groups {
		if gr.total[0] == 0 {
			continue
		}
		gr.total = [5]uint64{}
		if !alive[j] {
			clear(gr.hist)
		} else {
			for a := 0; a < 5; a++ {
				for _, c := range gr.hist[off[a]:off[a+1]] {
					gr.total[a] += uint64(c)
				}
			}
		}
		j++
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
