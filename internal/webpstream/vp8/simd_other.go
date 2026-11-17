//go:build !(amd64 || (arm64 && go1.27)) || purego

package vp8

const useSIMD = false

func filterH6(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterH4(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterV6(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterV4(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }

const SIMD = false

func InnerH(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func InnerV(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }

func idctAddAsm(coef *[16]int16, ref *byte, rStride int, dst *byte, dStride int) {
	panic("unreachable")
}

// IDCTAdd is not to be called without SIMD.
func IDCTAdd(coef *[16]int16, ref *byte, rStride int, dst *byte, dStride int) {
	panic("unreachable")
}

func dcAddAsm(dst *byte, stride, dc int)              { panic("unreachable") }
func dcAdd8Asm(dst *byte, stride, d0, d1, d2, d3 int) { panic("unreachable") }

const lf8 = false

func lfRows16(p *byte, stride, thresh, ithresh, hev int)    { panic("unreachable") }
func lfCols16(p *byte, stride, thresh, ithresh, hev int)    { panic("unreachable") }
func lfRows16i(p *byte, stride, thresh, ithresh, hev int)   { panic("unreachable") }
func lfCols16i(p *byte, stride, thresh, ithresh, hev int)   { panic("unreachable") }
func lfRows8(u, v *byte, stride, thresh, ithresh, hev int)  { panic("unreachable") }
func lfCols8(u, v *byte, stride, thresh, ithresh, hev int)  { panic("unreachable") }
func lfRows8i(u, v *byte, stride, thresh, ithresh, hev int) { panic("unreachable") }
func lfCols8i(u, v *byte, stride, thresh, ithresh, hev int) { panic("unreachable") }
