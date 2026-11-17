//go:build !(amd64 || (arm64 && go1.27)) || purego

package vp8enc

const useSIMD = false

func ssimSumsSIMD(a, b *byte, y, yy, xy *[100]uint32) { panic("unreachable") }

func fdctSIMD(src *byte, sStride int, ref *byte, rStride int, out *[16]int16) {
	panic("unreachable")
}

func sse4SIMD(a *byte, aStride int, b *byte, bStride int) int { panic("unreachable") }

func quantizeSIMD(in *[16]int16, v *quantVec, lv, deq *[16]int16) int { panic("unreachable") }

func histBinsSIMD(src *byte, sStride int, ref *byte, rStride int, bins *[16]uint8) uint32 {
	panic("unreachable")
}
