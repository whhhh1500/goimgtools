//go:build !(amd64 || (arm64 && go1.27)) || purego

package pngstream

var useSIMD = false

func filterRunSIMD(cur, prev, up, sub, avg, paeth *byte, bpp, chunks int, sums *[5]uint64) {
	panic("unreachable")
}
