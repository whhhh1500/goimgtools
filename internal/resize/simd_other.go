//go:build !(amd64 || (arm64 && go1.27)) || purego

package resize

var useSIMD = false

func reduceVSIMD(dst *byte, rows **byte, pairs *uint32, npairs, chunks int) { panic("unreachable") }

func reduceHSIMD(dst, row *byte, cols, pidx *int32, pairs *uint32, npairs, count int) {
	panic("unreachable")
}

func hpassSIMD(tmp, row *[4]float64, s0 int, sources *source, contribs *contrib, n int, ca bool) {
	panic("unreachable")
}

func vpassSIMD(acc, tmp *[4]float64, n int, w float64) { panic("unreachable") }

func vpassGraySIMD(acc, tmp *float64, n int, w float64) { panic("unreachable") }

func rgbaRowSIMD(out *[4]float64, pix *byte, n int) { panic("unreachable") }
