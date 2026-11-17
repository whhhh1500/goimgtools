//go:build amd64 && !purego

package vp8

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 version of the normal loop filter, which gives
// the same bytes as the Go code. The purego build tag leaves it out.
var useSIMD = cpu.X86.HasAVX2

// filterH6 and filterH4 are filter246 of an edge between rows (six or four
// pixels): 8 pixels at p, 8 at p2, rows stride apart. filterV6 and
// filterV4 are those of an edge between columns: 8 rows from p, 8 from p2.
//
//go:noescape
func filterH6(p, p2 *byte, stride, level, ilevel, hlevel int)

//go:noescape
func filterH4(p, p2 *byte, stride, level, ilevel, hlevel int)

//go:noescape
func filterV6(p, p2 *byte, stride, level, ilevel, hlevel int)

//go:noescape
func filterV4(p, p2 *byte, stride, level, ilevel, hlevel int)

// SIMD reports whether InnerH and InnerV may be called: the inner-edge
// loop filter (four pixels) with AVX2, for the encoder's filter search.
var SIMD = useSIMD

// InnerH is filterH4 and InnerV filterV4, for package vp8enc.
func InnerH(p, p2 *byte, stride, level, ilevel, hlevel int) {
	filterH4(p, p2, stride, level, ilevel, hlevel)
}
func InnerV(p, p2 *byte, stride, level, ilevel, hlevel int) {
	filterV4(p, p2, stride, level, ilevel, hlevel)
}

// idctAddAsm adds the inverse transform of coef to the 4x4 block at ref
// into the one at dst (inverseDCT4's arithmetic; they may be the same).
//
//go:noescape
func idctAddAsm(coef *[16]int16, ref *byte, rStride int, dst *byte, dStride int)

// IDCTAdd is idctAddAsm, for package vp8enc: only where SIMD is true.
func IDCTAdd(coef *[16]int16, ref *byte, rStride int, dst *byte, dStride int) {
	idctAddAsm(coef, ref, rStride, dst, dStride)
}

// dcAddAsm adds dc to the 4x4 block at dst, clipped (inverseDCT4DCOnly);
// dcAdd8Asm adds d0, d1, d2 and d3 to the four 4x4 blocks of the 8x8 one
// at dst (inverseDCT8DCOnly).
//
//go:noescape
func dcAddAsm(dst *byte, stride, dc int)

//go:noescape
func dcAdd8Asm(dst *byte, stride, d0, d1, d2, d3 int)

// lf8 selects the loop filter of lf_amd64.s, after libwebp's SSE2 code (8-bit
// lanes, the inner edges of the luma in one call), for normalFilterSIMD.
const lf8 = true

// lfRows16 filters the macroblock edge above row p (16 pixels), lfCols16
// the one left of p (16 rows); lfRows16i and lfCols16i the three inner
// edges of the macroblock at p; lfRows8, lfCols8, lfRows8i and lfCols8i
// those of the 8x8 chroma blocks at u and v together. thresh, ithresh and
// hev are filter246's level, ilevel and hlevel.
//
//go:noescape
func lfRows16(p *byte, stride, thresh, ithresh, hev int)

//go:noescape
func lfCols16(p *byte, stride, thresh, ithresh, hev int)

//go:noescape
func lfRows16i(p *byte, stride, thresh, ithresh, hev int)

//go:noescape
func lfCols16i(p *byte, stride, thresh, ithresh, hev int)

//go:noescape
func lfRows8(u, v *byte, stride, thresh, ithresh, hev int)

//go:noescape
func lfCols8(u, v *byte, stride, thresh, ithresh, hev int)

//go:noescape
func lfRows8i(u, v *byte, stride, thresh, ithresh, hev int)

//go:noescape
func lfCols8i(u, v *byte, stride, thresh, ithresh, hev int)
