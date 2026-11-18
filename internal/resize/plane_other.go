//go:build !(amd64 || (arm64 && go1.27)) || purego

package resize

var planeSIMD = false

func planeHSIMD(out *int16, row *byte, start *int32, w16 *int16, groups int) {
	panic("unreachable")
}

func planeVSIMD(out *byte, rows **int16, pairs *uint32, npairs, chunks int) {
	panic("unreachable")
}
