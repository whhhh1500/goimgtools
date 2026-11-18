//go:build amd64 && !purego

package resize

// planeSIMD selects the AVX2 passes of planeScaler, which give the same
// bytes as the Go code.
var planeSIMD = useSIMD

// planeHSIMD is groups times 8 samples of planeScaler's pass across, each
// with its 16 weights (zero past the kernel's) in w16.
//
//go:noescape
func planeHSIMD(out *int16, row *byte, start *int32, w16 *int16, groups int)

// planeVSIMD is chunks times 16 samples of planeScaler's pass down.
//
//go:noescape
func planeVSIMD(out *byte, rows **int16, pairs *uint32, npairs, chunks int)
