//go:build !amd64 || purego

package resize

var useSIMD = false

func reduceVSIMD(dst *byte, rows **byte, pairs *uint32, npairs, chunks int) { panic("unreachable") }

func reduceHSIMD(dst, row *byte, cols, pidx *int32, pairs *uint32, npairs, count int) {
	panic("unreachable")
}
