//go:build (amd64 || (arm64 && go1.27)) && !purego

package vp8enc

// fdctSIMD is fdctGo of the 4x4 blocks at src and ref.
//
//go:noescape
func fdctSIMD(src *byte, sStride int, ref *byte, rStride int, out *[16]int16)

// sse4SIMD is sse4Go of the 4x4 blocks at a and b.
//
//go:noescape
func sse4SIMD(a *byte, aStride int, b *byte, bStride int) int

// quantizeSIMD is quantizeGo from the first coefficient on, with the
// quantizer v.
//
//go:noescape
func quantizeSIMD(in *[16]int16, v *quantVec, lv, deq *[16]int16) int

// histBinsSIMD transforms the 4x4 difference of the blocks at src and ref
// as fdctGo does and sets bins to the histogram bin of each coefficient
// (coeffHist.add); it returns a mask of the bins that are not 0.
//
//go:noescape
func histBinsSIMD(src *byte, sStride int, ref *byte, rStride int, bins *[16]uint8) uint32
