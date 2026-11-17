//go:build amd64 && !purego

package vp8

import "golang.org/x/sys/cpu"

// useAVX2 selects the AVX2 version of the normal loop filter, which gives
// the same bytes as the Go code. The purego build tag leaves it out.
var useAVX2 = cpu.X86.HasAVX2

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

// AVX2 reports whether InnerH and InnerV may be called: the inner-edge
// loop filter (four pixels) with AVX2, for the encoder's filter search.
var AVX2 = useAVX2

// InnerH is filterH4 and InnerV filterV4, for package vp8enc.
func InnerH(p, p2 *byte, stride, level, ilevel, hlevel int) {
	filterH4(p, p2, stride, level, ilevel, hlevel)
}
func InnerV(p, p2 *byte, stride, level, ilevel, hlevel int) {
	filterV4(p, p2, stride, level, ilevel, hlevel)
}
