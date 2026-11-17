//go:build (amd64 || arm64) && !purego

package vp8

// parseAsm selects parseBlockAsm, the token loop of parseResiduals4 in
// assembly (plain integer instructions: no CPU feature needed).
const parseAsm = true

// asmTables holds the tables of parseBlockAsm: renormShift (from 0),
// renormRange (from 256), 33*bands[n], the offset of prob[bands[n]] (from
// 512), and zigzag (from 529).
var asmTables = func() (t [2*256 + 17 + 16]uint8) {
	copy(t[:], renormShift[:])
	copy(t[256:], renormRange[:])
	for n, b := range bands {
		t[512+n] = 33 * b
	}
	copy(t[529:], zigzag[:])
	return
}()

// parseBlockAsm is the token loop of parseResiduals4 from position first
// (0 or 1) with the given context, reading r (its fields at the offsets
// go_asm.h gives) when its window holds asmMargin bytes from r.r on, and
// otherwise returning asmTooNear without reading. quant is the DC factor |
// the AC factor << 16. It returns parseResiduals4's result: the position
// after the last coefficient.
//
//go:noescape
func parseBlockAsm(r *partition, prob *[nBand][nContext][nProb]uint8, blk *[16]int16, first, ctx int, quant uint32) uint8
