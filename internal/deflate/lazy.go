package deflate

// The lazy parsing of libdeflate (deflate_compress_lazy_generic,
// lib/deflate_compress.c, levels 5 to 9) with its hash chains
// (lib/hc_matchfinder.h): at each position the longest match is looked
// for along a chain of the earlier positions with the same 4 bytes (and
// one earlier position with the same 3), and before it is taken the next
// position (and with lazy2 the one after) is searched too; a better match
// there makes the current byte a literal. Blocks end where the statistics
// of the data change, as with the near-optimal parsing, and are written by
// flushBlock. Without a dictionary the output is libdeflate's, to the byte.

import (
	"encoding/binary"
	"math/bits"
)

const (
	hcHash3Order = 15
	hcHash4Order = 16
	hcHops       = 4 // positions in a link
	// seqStoreLength is the most matches a block of the lazy parsing
	// holds (libdeflate's SEQ_STORE_LENGTH).
	seqStoreLength = 50000
	hcBytes        = 2<<hcHash3Order + 2<<hcHash4Order + 8*windowSize
)

// Lazy levels 5 to 9 are libdeflate's levels 5 to 9.
var (
	LazyLevel5 = Params{Lazy: 1, SearchDepth: 16, NiceLen: 30, BlockLen: 300000}
	LazyLevel6 = Params{Lazy: 1, SearchDepth: 35, NiceLen: 65, BlockLen: 300000}
	LazyLevel7 = Params{Lazy: 1, SearchDepth: 100, NiceLen: 130, BlockLen: 300000}
	LazyLevel8 = Params{Lazy: 2, SearchDepth: 300, NiceLen: maxMatch, BlockLen: 300000}
	LazyLevel9 = Params{Lazy: 2, SearchDepth: 600, NiceLen: maxMatch, BlockLen: 300000}
)

// hcMatchfinder is libdeflate's hc_matchfinder with multi-hop links: a
// hash table of the last position of each 3-byte sequence, and one of the
// first position of a chain of the positions of each 4-byte sequence,
// newest first. Each position's link holds the next hcHops positions of
// its chain, so that walking a chain waits for a load only every hcHops
// steps; the positions visited are those of single links. Positions are
// 16-bit, relative to a base that moves on by the window (see slide), so
// the tables take 448KB. (32-bit positions, which need no moving on, made
// the tables twice as large and the compression of screenshots twice as
// slow.)
type hcMatchfinder struct {
	hash3 [1 << hcHash3Order]int16
	hash4 [1 << hcHash4Order]int16
	next  [windowSize]uint64
}

func (mf *hcMatchfinder) init() {
	for i := range mf.hash3 {
		mf.hash3[i] = mfInitVal
	}
	for i := range mf.hash4 {
		mf.hash4[i] = mfInitVal
	}
	// The first position entered copies the link of "no position", which
	// is never walked but is read; every other link read was written.
	mf.next[0] = 0x0001000100010001 * (mfInitVal & 0xffff)
}

// slide moves the base on by the window: positions in the window before
// become negative, and older ones none.
func (mf *hcMatchfinder) slide() {
	for i, v := range mf.hash3 {
		mf.hash3[i] = v&^(v>>15) | mfInitVal
	}
	for i, v := range mf.hash4 {
		mf.hash4[i] = v&^(v>>15) | mfInitVal
	}
	const signs = 0x8000800080008000
	for i, x := range mf.next {
		// In each 16-bit lane: v | 0x8000 for v >= 0, else 0x8000.
		m := (x & signs) >> 15 * 0x7fff
		mf.next[i] = (x | signs) &^ m
	}
}

// link is the link of position node (of its window).
func (mf *hcMatchfinder) link(node int16) uint64 {
	return mf.next[int(node)&(windowSize-1)]
}

// longestMatch enters position p of in, whose hashes are in nh (which
// gets those of the next one), and returns the longest match there longer
// than bestLen, up to maxLen (stopping at niceLen or after depth chain
// positions), or bestLen and 0 when there is none. base is that of the
// positions, and moves on when p reaches the end of its window.
func (mf *hcMatchfinder) longestMatch(in []byte, base *int, p, bestLen, maxLen, niceLen, depth int, nh *[2]uint32) (int, int) {
	cur := p - *base
	if cur == windowSize {
		mf.slide()
		*base += windowSize
		cur = 0
	}
	if maxLen < 5 {
		return bestLen, 0
	}
	inBase := *base
	cutoff := int16(cur - windowSize)
	best := p
	h3, h4 := nh[0], nh[1]
	node3, node4 := mf.hash3[h3], mf.hash4[h4]
	mf.hash3[h3] = int16(cur)
	mf.hash4[h4] = int16(cur)
	link := mf.link(node4) // the positions after node4
	mf.next[cur] = link<<16 | uint64(uint16(node4))
	seq := binary.LittleEndian.Uint32(in[p+1:])
	nh[0] = lzHash(seq&0xFFFFFF, hcHash3Order)
	nh[1] = lzHash(seq, hcHash4Order)

	var nextLink uint64
	hops := 0
	advance := func() {
		node4 = int16(link)
		if hops--; hops != 0 {
			link >>= 16
		} else {
			link, hops = nextLink, hcHops
			nextLink = mf.link(int16(link >> (16 * (hcHops - 1))))
		}
	}

	if bestLen < 4 {
		// A match of 3 bytes, then the first of 4 or more.
		if node3 <= cutoff {
			return bestLen, 0
		}
		seq4 := binary.LittleEndian.Uint32(in[p:])
		if bestLen < 3 {
			if m := inBase + int(node3); load24(in, m) == seq4&0xFFFFFF {
				bestLen, best = 3, m
			}
		}
		if node4 <= cutoff {
			return bestLen, p - best
		}
		hops, nextLink = hcHops, mf.link(int16(link>>(16*(hcHops-1))))
		var m int
		for {
			m = inBase + int(node4)
			if binary.LittleEndian.Uint32(in[m:]) == seq4 {
				break
			}
			advance()
			if depth--; node4 <= cutoff || depth == 0 {
				return bestLen, p - best
			}
		}
		best, bestLen = m, extend(in, p, m, 4, maxLen)
		if bestLen >= niceLen {
			return bestLen, p - best
		}
		advance()
		if depth--; node4 <= cutoff || depth == 0 {
			return bestLen, p - best
		}
	} else {
		if node4 <= cutoff || bestLen >= niceLen {
			return bestLen, 0
		}
		hops, nextLink = hcHops, mf.link(int16(link>>(16*(hcHops-1))))
	}
	// Longer matches: a position is extended only when the 4 bytes that
	// would make it longer than the best and its first 4 agree.
	for {
		var m int
		for {
			m = inBase + int(node4)
			if binary.LittleEndian.Uint32(in[m+bestLen-3:]) == binary.LittleEndian.Uint32(in[p+bestLen-3:]) &&
				binary.LittleEndian.Uint32(in[m:]) == binary.LittleEndian.Uint32(in[p:]) {
				break
			}
			advance()
			if depth--; node4 <= cutoff || depth == 0 {
				return bestLen, p - best
			}
		}
		if l := extend(in, p, m, 4, maxLen); l > bestLen {
			bestLen, best = l, m
			if bestLen >= niceLen {
				return bestLen, p - best
			}
		}
		advance()
		if depth--; node4 <= cutoff || depth == 0 {
			return bestLen, p - best
		}
	}
}

// skip enters count positions of in from p, whose hashes are in nh (which
// gets those of p+count), without looking for matches; near the end of in
// it enters none.
func (mf *hcMatchfinder) skip(in []byte, base *int, p, count int, nh *[2]uint32) {
	if count+5 > len(in)-p {
		return
	}
	cur := p - *base
	h3, h4 := nh[0], nh[1]
	for end := p + count; p < end; {
		if cur == windowSize {
			mf.slide()
			*base += windowSize
			cur = 0
		}
		mf.hash3[h3] = int16(cur)
		old := mf.hash4[h4]
		mf.next[cur] = mf.link(old)<<16 | uint64(uint16(old))
		mf.hash4[h4] = int16(cur)
		p++
		cur++
		seq := binary.LittleEndian.Uint32(in[p:])
		h3 = lzHash(seq&0xFFFFFF, hcHash3Order)
		h4 = lzHash(seq, hcHash4Order)
	}
	nh[0], nh[1] = h3, h4
}

// recalculateMinMatchLen is the shortest match worth taking, by the
// literals the block has used so far (those used very rarely left out).
func recalculateMinMatchLen(f *freqs, depth int) int {
	var total uint32
	for _, n := range f.litlen[:numLiterals] {
		total += n
	}
	cutoff := total >> 10
	var used uint32
	for _, n := range f.litlen[:numLiterals] {
		if n > cutoff {
			used++
		}
	}
	return chooseMinMatchLen(used, depth)
}

// compressLazy is compress for the lazy parsing.
func (c *Compressor) compressLazy(in []byte, start int, final bool) {
	inEnd := len(in)
	if n := min(c.p.maxBlockLength(), inEnd-start); len(c.items) < n {
		// More than MaxInput.
		c.items = make([]uint32, n)
	}
	mf := c.hc
	mf.init()
	base := 0
	var nh [2]uint32
	maxLen, niceLen := maxMatch, min(c.p.NiceLen, maxMatch)
	depth := c.p.SearchDepth
	lazy2 := c.p.Lazy == 2
	inNext := 0
	if start > 0 {
		// The dictionary is entered as the bytes a match covers are.
		if inEnd >= 4 {
			seq := binary.LittleEndian.Uint32(in)
			nh = [2]uint32{lzHash(seq&0xFFFFFF, hcHash3Order), lzHash(seq, hcHash4Order)}
		}
		mf.skip(in, &base, 0, start, &nh)
		inNext = start
	}
	adjust := func() {
		if r := inEnd - inNext; r < maxMatch {
			maxLen = r
			niceLen = min(niceLen, maxLen)
		}
	}
	for {
		blockBegin := inNext
		maxBlockEnd := inEnd
		if inEnd-blockBegin >= c.p.BlockLen+minBlockLength {
			maxBlockEnd = blockBegin + c.p.BlockLen
		}
		nextRecalc := inNext + min(inEnd-inNext, 10000)
		c.split = splitStats{}
		c.freqs = freqs{}
		items := c.items
		nseq := 0
		literal := func(p int) {
			b := in[p]
			c.freqs.litlen[b]++
			c.split.observeLiteral(b)
			items[p-blockBegin] = uint32(b)
		}
		match := func(p, length, offset int) {
			c.freqs.litlen[firstLenSym+int(lengthSlot[length])]++
			c.freqs.offset[offsetSlot(uint32(offset))]++
			c.split.observeMatch(length)
			items[p-blockBegin] = uint32(length)<<optimumLenShift | uint32(offset)
			nseq++
		}
		minLen := calculateMinMatchLen(in[inNext:maxBlockEnd], depth)
		for {
			if inNext >= nextRecalc {
				minLen = recalculateMinMatchLen(&c.freqs, depth)
				nextRecalc += min(inEnd-nextRecalc, inNext-blockBegin)
			}
			adjust()
			curLen, curOff := mf.longestMatch(in, &base, inNext, minLen-1, maxLen, niceLen, depth, &nh)
			if curLen < minLen || curLen == minMatch && curOff > 8192 {
				literal(inNext)
				inNext++
			} else {
				inNext++
				for {
					// A match at inNext-1.
					if curLen >= niceLen {
						match(inNext-1, curLen, curOff)
						mf.skip(in, &base, inNext, curLen-1, &nh)
						inNext += curLen - 1
						break
					}
					adjust()
					nextLen, nextOff := mf.longestMatch(in, &base, inNext, curLen-1, maxLen, niceLen, depth>>1, &nh)
					inNext++
					if nextLen >= curLen && 4*(nextLen-curLen)+bits.Len32(uint32(curOff))-bits.Len32(uint32(nextOff)) > 2 {
						literal(inNext - 2)
						curLen, curOff = nextLen, nextOff
						continue
					}
					if !lazy2 {
						match(inNext-2, curLen, curOff)
						mf.skip(in, &base, inNext, curLen-2, &nh)
						inNext += curLen - 2
						break
					}
					adjust()
					nextLen, nextOff = mf.longestMatch(in, &base, inNext, curLen-1, maxLen, niceLen, depth>>2, &nh)
					inNext++
					if nextLen >= curLen && 4*(nextLen-curLen)+bits.Len32(uint32(curOff))-bits.Len32(uint32(nextOff)) > 6 {
						literal(inNext - 3)
						literal(inNext - 2)
						curLen, curOff = nextLen, nextOff
						continue
					}
					match(inNext-3, curLen, curOff)
					if curLen > 3 {
						mf.skip(in, &base, inNext, curLen-3, &nh)
						inNext += curLen - 3
					}
					break
				}
			}
			if inNext >= maxBlockEnd || nseq >= seqStoreLength {
				break
			}
			if c.split.readyToCheck(blockBegin, inNext, inEnd) && c.split.endBlockCheck(uint32(inNext-blockBegin)) {
				break
			}
		}
		c.freqs.litlen[endOfBlock]++
		makeCodes(&c.freqs, &c.codes)
		c.flushBlock(in[blockBegin:inNext], false, final && inNext == inEnd)
		if inNext == inEnd {
			return
		}
	}
}
