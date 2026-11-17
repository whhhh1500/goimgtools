//go:build !amd64 || purego

package resize

var useAVX2 = false

func reduceVAVX2(dst *byte, rows **byte, pairs *uint32, npairs, chunks int) { panic("unreachable") }

func reduceHAVX2(dst, row *byte, cols, pidx *int32, pairs *uint32, npairs, count int) {
	panic("unreachable")
}
