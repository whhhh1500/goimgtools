package vp8lenc

import (
	"cmp"
	"slices"
	"sync"
)

const (
	numLiteral  = 256
	numLength   = 24
	numDistance = 40
	maxCodeLen  = 15
)

// code is one prefix code. lens are the canonical code lengths written in
// the bitstream; enc[s] packs what to emit for symbol s: the bit-reversed
// code in the low 16 bits and its length above. A code with a single used
// symbol is read by the decoder without consuming any bits, so its enc
// length is zero.
type code struct {
	lens []uint8
	enc  []uint32
	used int
}

type leaf struct {
	cnt uint32
	sym int32
}

// lengthScratch is what codeLengths works in; a pool keeps it, as codes
// are made by the hundred while the image is split into groups.
type lengthScratch struct {
	leaves        []leaf
	weight        []uint64
	parent, depth []int32
}

var lengthPool = sync.Pool{New: func() any { return new(lengthScratch) }}

// codeLengths fills lens with length-limited Huffman code lengths for hist.
func codeLengths(hist []uint32, maxLen int, lens []uint8) {
	sc := lengthPool.Get().(*lengthScratch)
	defer lengthPool.Put(sc)
	leaves := sc.leaves[:0]
	defer func() { sc.leaves = leaves[:0] }()
	for s, c := range hist {
		lens[s] = 0
		if c != 0 {
			leaves = append(leaves, leaf{c, int32(s)})
		}
	}
	n := len(leaves)
	switch n {
	case 0:
		return
	case 1:
		lens[leaves[0].sym] = 1
		return
	}
	slices.SortFunc(leaves, func(a, b leaf) int {
		if a.cnt != b.cnt {
			return cmp.Compare(a.cnt, b.cnt)
		}
		return cmp.Compare(a.sym, b.sym)
	})
	// Two-queue Huffman construction: nodes [0,n) are the sorted leaves,
	// [n,2n-1) the internal nodes in creation (hence weight) order.
	if cap(sc.weight) < 2*n-1 {
		sc.weight = make([]uint64, 2*n-1)
		sc.parent = make([]int32, 2*n-1)
		sc.depth = make([]int32, 2*n-1)
	}
	weight, parent := sc.weight[:2*n-1], sc.parent[:2*n-1]
	for i, l := range leaves {
		weight[i] = uint64(l.cnt)
	}
	i, j := 0, n
	for k := n; k < 2*n-1; k++ {
		var a, b int
		if i < n && (j >= k || weight[i] <= weight[j]) {
			a = i
			i++
		} else {
			a = j
			j++
		}
		if i < n && (j >= k || weight[i] <= weight[j]) {
			b = i
			i++
		} else {
			b = j
			j++
		}
		weight[k] = weight[a] + weight[b]
		parent[a], parent[b] = int32(k), int32(k)
	}
	depth := sc.depth[:2*n-1]
	depth[2*n-2] = 0
	for k := 2*n - 3; k >= 0; k-- {
		depth[k] = depth[parent[k]] + 1
	}
	// Count the leaves per length, clamp to maxLen and repair the Kraft sum
	// by lengthening the cheapest codes.
	var count [maxCodeLen + 1]int
	for k := 0; k < n; k++ {
		count[min(int(depth[k]), maxLen)]++
	}
	total := 0
	for l := 1; l <= maxLen; l++ {
		total += count[l] << (maxLen - l)
	}
	for total > 1<<maxLen {
		count[maxLen]--
		for l := maxLen - 1; l > 0; l-- {
			if count[l] > 0 {
				count[l]--
				count[l+1] += 2
				break
			}
		}
		total--
	}
	// The rarest symbols get the longest codes.
	k := 0
	for l := maxLen; l >= 1; l-- {
		for c := count[l]; c > 0; c-- {
			lens[leaves[k].sym] = uint8(l)
			k++
		}
	}
}

func reverseBits(v uint32, n uint) uint32 {
	var r uint32
	for ; n > 0; n-- {
		r = r<<1 | v&1
		v >>= 1
	}
	return r
}

// newCode builds the canonical code for hist.
func newCode(hist []uint32, maxLen int) *code {
	lens := make([]uint8, len(hist))
	codeLengths(hist, maxLen, lens)
	return codeOf(lens)
}

// codeOf is the canonical code with the code lengths lens, which it keeps.
func codeOf(lens []uint8) *code {
	c := &code{lens: lens, enc: make([]uint32, len(lens))}
	var count [maxCodeLen + 1]uint32
	for _, l := range c.lens {
		if l != 0 {
			count[l]++
			c.used++
		}
	}
	if c.used <= 1 {
		return c
	}
	var next [maxCodeLen + 1]uint32
	cur := uint32(0)
	for l := 1; l <= maxCodeLen; l++ {
		cur = (cur + count[l-1]) << 1
		next[l] = cur
	}
	for s, l := range c.lens {
		if l != 0 {
			c.enc[s] = reverseBits(next[l], uint(l)) | uint32(l)<<16
			next[l]++
		}
	}
	return c
}

// cost is the number of bits spent coding hist with this code.
func (c *code) cost(hist []uint32) int64 {
	if c.used <= 1 {
		return 0
	}
	var n int64
	for s, h := range hist {
		n += int64(h) * int64(c.lens[s])
	}
	return n
}

func (c *code) put(bw *bitWriter, s uint32) {
	e := c.enc[s]
	bw.put(e&0xffff, uint(e>>16))
}

// codeLengthOrder is the order in which the lengths of the code-length code
// are stored.
var codeLengthOrder = [19]uint8{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

type clToken struct {
	sym, extra, nbits uint8
}

// clTokens run-length encodes a sequence of code lengths: 16 repeats the
// last non-zero length (8 at the start) 3-6 times, 17 and 18 repeat zero
// 3-10 and 11-138 times.
func clTokens(lens []uint8, dst []clToken) []clToken {
	prev := uint8(8)
	for i := 0; i < len(lens); {
		v := lens[i]
		run := 1
		for i+run < len(lens) && lens[i+run] == v {
			run++
		}
		i += run
		if v == 0 {
			for run > 0 {
				switch {
				case run >= 11:
					r := min(run, 138)
					dst = append(dst, clToken{18, uint8(r - 11), 7})
					run -= r
				case run >= 3:
					dst = append(dst, clToken{17, uint8(run - 3), 3})
					run = 0
				default:
					dst = append(dst, clToken{0, 0, 0})
					run--
				}
			}
			continue
		}
		if v != prev {
			dst = append(dst, clToken{v, 0, 0})
			prev = v
			run--
		}
		for run >= 3 {
			r := min(run, 6)
			dst = append(dst, clToken{16, uint8(r - 3), 2})
			run -= r
		}
		for ; run > 0; run-- {
			dst = append(dst, clToken{v, 0, 0})
		}
	}
	return dst
}

// writeTree stores the code in the bitstream (section 6.2.1): the simple
// form for one or two symbols below 256, the normal form otherwise.
func writeTree(bw *bitWriter, c *code) {
	if c.used <= 2 {
		var syms [2]uint32
		n := 0
		for s, l := range c.lens {
			if l != 0 {
				syms[n] = uint32(s)
				n++
			}
		}
		if n == 0 || syms[n-1] < 256 {
			bw.put(1, 1)
			if n == 0 {
				n = 1 // an unused tree: a single symbol 0
			}
			bw.put(uint32(n-1), 1)
			if syms[0] < 2 {
				bw.put(0, 1)
				bw.put(syms[0], 1)
			} else {
				bw.put(1, 1)
				bw.put(syms[0], 8)
			}
			if n == 2 {
				bw.put(syms[1], 8)
			}
			return
		}
	}
	bw.put(0, 1)
	toks := clTokens(c.lens, make([]clToken, 0, 64))
	// The zeros at the end can be left out, the number of tokens written
	// given instead (max_symbol), when that is shorter (as libwebp does,
	// src/enc/vp8l_enc.c: StoreFullHuffmanCode).
	if trim := trimTokens(toks); trim < len(toks) {
		writeLengths(bw, toks[:trim], true)
		return
	}
	writeLengths(bw, toks, false)
}

// trimTokens is how many of toks writeTree writes: all of them, or fewer
// when leaving out the zeros at the end and counting the rest is shorter.
func trimTokens(toks []clToken) int {
	trim := len(toks)
	for trim > 2 && (toks[trim-1].sym == 0 || toks[trim-1].sym >= 17) {
		trim--
	}
	if trim < len(toks) && lengthsBits(toks[:trim], true) < lengthsBits(toks, false) {
		return trim
	}
	return len(toks)
}

// lengthsBits is the size writeLengths gives toks.
func lengthsBits(toks []clToken, counted bool) int64 {
	var hist [19]uint32
	for _, t := range toks {
		hist[t.sym]++
	}
	var lens [19]uint8
	codeLengths(hist[:], 7, lens[:])
	used, n := 0, 4
	for i, s := range codeLengthOrder {
		if lens[s] != 0 {
			used++
			n = max(n, i+1)
		}
	}
	bits := int64(4 + 3*n + 1)
	if counted {
		m := uint32(len(toks) - 2)
		pairs := 1
		for m >= 1<<(2*pairs) {
			pairs++
		}
		bits += 3 + 2*int64(pairs)
	}
	for _, t := range toks {
		if used > 1 { // a code of one symbol takes no bits
			bits += int64(lens[t.sym])
		}
		bits += int64(t.nbits)
	}
	return bits
}

// treeScratch holds what sizing a tree needs, so that the many trees
// priced while the image is split into groups leave no garbage.
type treeScratch struct {
	toks []clToken
	even []uint32
	good []bool
	lens []uint8
}

var treePool = sync.Pool{New: func() any { return new(treeScratch) }}

// treeSize is the size writeTree gives a code with the lengths lens.
func treeSize(lens []uint8, sc *treeScratch) int64 {
	used := 0
	var syms [2]int
	for s, l := range lens {
		if l != 0 {
			if used < 2 {
				syms[used] = s
			}
			used++
		}
	}
	if used <= 2 && (used == 0 || syms[used-1] < 256) {
		bits := int64(3) // simple, the number of symbols, the size of the first
		if syms[0] < 2 {
			bits++
		} else {
			bits += 8
		}
		if used == 2 {
			bits += 8
		}
		return bits
	}
	sc.toks = clTokens(lens, sc.toks[:0])
	trim := trimTokens(sc.toks)
	return 1 + lengthsBits(sc.toks[:trim], trim < len(sc.toks))
}

// writeLengths writes the code lengths of a code in the normal form, as
// the tokens toks, which are all of them or (counted) the first ones.
func writeLengths(bw *bitWriter, toks []clToken, counted bool) {
	var hist [19]uint32
	for _, t := range toks {
		hist[t.sym]++
	}
	cl := newCode(hist[:], 7)
	n := 4
	for i, s := range codeLengthOrder {
		if cl.lens[s] != 0 {
			n = max(n, i+1)
		}
	}
	bw.put(uint32(n-4), 4)
	for _, s := range codeLengthOrder[:n] {
		bw.put(uint32(cl.lens[s]), 3)
	}
	if counted {
		// max_symbol: 2 + a number of 2 to 16 bits, in pairs.
		m := uint32(len(toks) - 2)
		pairs := uint(1)
		for m >= 1<<(2*pairs) {
			pairs++
		}
		bw.put(1, 1)
		bw.put(uint32(pairs-1), 3)
		bw.put(m, 2*pairs)
	} else {
		bw.put(0, 1) // every code length is coded
	}
	for _, t := range toks {
		cl.put(bw, uint32(t.sym))
		if t.nbits != 0 {
			bw.put(uint32(t.extra), uint(t.nbits))
		}
	}
}

// treeBits is the size writeTree gives the code.
func treeBits(c *code) int64 {
	sc := treePool.Get().(*treeScratch)
	defer treePool.Put(sc)
	return treeSize(c.lens, sc)
}
