//go:build arm64 && go1.27 && !purego

package vp8

// useSIMD selects the NEON version of the normal loop filter, which gives
// the same bytes as the Go code (Go 1.27 or later; not with the purego
// build tag).
var useSIMD = true

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

// lf8 is false: arm64 keeps the NEON filter of filter_arm64.s.
const lf8 = false

func lfRows16(p *byte, stride, thresh, ithresh, hev int)    { panic("unreachable") }
func lfCols16(p *byte, stride, thresh, ithresh, hev int)    { panic("unreachable") }
func lfRows16i(p *byte, stride, thresh, ithresh, hev int)   { panic("unreachable") }
func lfCols16i(p *byte, stride, thresh, ithresh, hev int)   { panic("unreachable") }
func lfRows8(u, v *byte, stride, thresh, ithresh, hev int)  { panic("unreachable") }
func lfCols8(u, v *byte, stride, thresh, ithresh, hev int)  { panic("unreachable") }
func lfRows8i(u, v *byte, stride, thresh, ithresh, hev int) { panic("unreachable") }
func lfCols8i(u, v *byte, stride, thresh, ithresh, hev int) { panic("unreachable") }
