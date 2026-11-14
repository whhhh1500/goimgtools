package vp8lenc

import (
	"sort"
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

// codeLengths fills lens with length-limited Huffman code lengths for hist.
func codeLengths(hist []uint32, maxLen int, lens []uint8) {
	type leaf struct {
		cnt uint32
		sym int32
	}
	leaves := make([]leaf, 0, 64)
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
	sort.Slice(leaves, func(i, j int) bool {
		if leaves[i].cnt != leaves[j].cnt {
			return leaves[i].cnt < leaves[j].cnt
		}
		return leaves[i].sym < leaves[j].sym
	})
	// Two-queue Huffman construction: nodes [0,n) are the sorted leaves,
	// [n,2n-1) the internal nodes in creation (hence weight) order.
	weight := make([]uint64, 2*n-1)
	parent := make([]int32, 2*n-1)
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
	depth := make([]int32, 2*n-1)
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
	c := &code{lens: make([]uint8, len(hist)), enc: make([]uint32, len(hist))}
	codeLengths(hist, maxLen, c.lens)
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
	bw.put(0, 1) // every code length is coded: no max_symbol
	for _, t := range toks {
		cl.put(bw, uint32(t.sym))
		if t.nbits != 0 {
			bw.put(uint32(t.extra), uint(t.nbits))
		}
	}
}

// treeBits is the size writeTree gives the code.
func treeBits(c *code) int64 {
	var bw bitWriter
	writeTree(&bw, c)
	return bw.bitLen()
}
