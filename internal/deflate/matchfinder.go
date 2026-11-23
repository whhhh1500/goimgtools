package deflate

import (
	"encoding/binary"
	"math/bits"
)

// btMatchfinder finds matches with binary trees, as libdeflate's
// lib/bt_matchfinder.h: a hash table of the last two positions for each
// 3-byte sequence, for matches of length 3, and for each 4-byte sequence
// a binary tree of the positions that start with it, sorted by what
// follows, for longer matches. Searching the tree for the current position
// also re-roots it there, so the tree keeps the most recent positions near
// the root.
//
// Positions are 16-bit, relative to a base that moves on by the window
// size every window (see slide), so the tables take 512KB.
type btMatchfinder struct {
	hash3 [1 << hash3Order][2]int16
	hash4 [1 << hash4Order]int16
	child [2 * windowSize]int16 // left and right child of each position, by position mod windowSize
}

const (
	hash3Order = 16
	hash4Order = 16
	mfInitVal  = -windowSize // no position
	// mfRequiredBytes is how many bytes the matchfinder reads at a
	// position.
	mfRequiredBytes  = 5
	matchfinderBytes = 2*2<<hash3Order + 2<<hash4Order + 2*2*windowSize
)

// lzMatch is a match, or in the match cache a position's entry: its number
// of matches in length and its byte in offset.
type lzMatch struct {
	length uint16
	offset uint16
}

func (mf *btMatchfinder) init() {
	for i := range mf.hash3 {
		mf.hash3[i] = [2]int16{mfInitVal, mfInitVal}
	}
	for i := range mf.hash4 {
		mf.hash4[i] = mfInitVal
	}
	// The children need no clearing: a position's are set when it is
	// entered, before they can be reached.
}

// slide moves the base on by the window: positions in the window before
// become negative, and older ones none.
func (mf *btMatchfinder) slide() {
	if useSIMD {
		slideSIMD(&mf.hash3[0][0], 2*len(mf.hash3))
		slideSIMD(&mf.hash4[0], len(mf.hash4))
		slideSIMD(&mf.child[0], len(mf.child))
		return
	}
	mf.slideGo()
}

func (mf *btMatchfinder) slideGo() {
	// v - windowSize for v >= 0 (setting the sign bit), else none.
	rebase := func(t []int16) {
		for i, v := range t {
			t[i] = v&^(v>>15) | mfInitVal
		}
	}
	for i := range mf.hash3 {
		rebase(mf.hash3[i][:])
	}
	rebase(mf.hash4[:])
	rebase(mf.child[:])
}

func lzHash(seq uint32, order uint) uint32 {
	return seq * 0x1E35A7BD >> (32 - order)
}

// extend returns how far, up to maxLen, in[p:] and in[m:] agree, knowing
// that they do for l bytes.
func extend(in []byte, p, m, l, maxLen int) int {
	for l+8 <= maxLen {
		if x := binary.LittleEndian.Uint64(in[p+l:]) ^ binary.LittleEndian.Uint64(in[m+l:]); x != 0 {
			return l + bits.TrailingZeros64(x)>>3
		}
		l += 8
	}
	for l < maxLen && in[p+l] == in[m+l] {
		l++
	}
	return l
}

func load24(in []byte, i int) uint32 {
	return uint32(in[i]) | uint32(in[i+1])<<8 | uint32(in[i+2])<<16
}

// advance enters the position cur (relative to base) of in, and when
// record is set appends its matches to out, each longer than the one
// before, and returns out. maxLen is at least mfRequiredBytes, niceLen no
// more than maxLen, and the hashes of the position are in nh, which gets
// those of the next one.
func (mf *btMatchfinder) advance(in []byte, base, cur, maxLen, niceLen, maxDepth int, nh *[2]uint32, out []lzMatch, record bool) []lzMatch {
	p := base + cur
	cutoff := cur - windowSize
	seq := binary.LittleEndian.Uint32(in[p+1:])
	h3, h4 := nh[0], nh[1]
	nh[0] = lzHash(seq&0xFFFFFF, hash3Order)
	nh[1] = lzHash(seq, hash4Order)

	node := int(mf.hash3[h3][0])
	node2 := int(mf.hash3[h3][1])
	mf.hash3[h3] = [2]int16{int16(cur), int16(node)}
	if record && node > cutoff {
		seq3 := load24(in, p)
		if seq3 == load24(in, base+node) {
			out = append(out, lzMatch{3, uint16(cur - node)})
		} else if node2 > cutoff && seq3 == load24(in, base+node2) {
			out = append(out, lzMatch{3, uint16(cur - node2)})
		}
	}

	node = int(mf.hash4[h4])
	mf.hash4[h4] = int16(cur)
	lt := 2 * (cur & (windowSize - 1)) // where the next smaller node goes
	gt := lt + 1                       // and the next larger
	if node <= cutoff {
		mf.child[lt], mf.child[gt] = mfInitVal, mfInitVal
		return out
	}
	bestLt, bestGt, l := 0, 0, 0
	bestLen := 3
	for depth := maxDepth; ; {
		m := base + node
		if in[m+l] == in[p+l] {
			l = extend(in, p, m, l+1, maxLen)
			if !record || l > bestLen {
				if record {
					bestLen = l
					out = append(out, lzMatch{uint16(l), uint16(cur - node)})
				}
				if l >= niceLen {
					// The node is taken out of the tree: its children
					// are the current position's.
					n := 2 * (node & (windowSize - 1))
					mf.child[lt], mf.child[gt] = mf.child[n], mf.child[n+1]
					return out
				}
			}
		}
		n := 2 * (node & (windowSize - 1))
		if in[m+l] < in[p+l] {
			mf.child[lt] = int16(node)
			lt = n + 1
			node = int(mf.child[lt])
			bestLt = l
			l = min(l, bestGt)
		} else {
			mf.child[gt] = int16(node)
			gt = n
			node = int(mf.child[gt])
			bestGt = l
			l = min(l, bestLt)
		}
		depth--
		if node <= cutoff || depth == 0 {
			mf.child[lt], mf.child[gt] = mfInitVal, mfInitVal
			return out
		}
	}
}
