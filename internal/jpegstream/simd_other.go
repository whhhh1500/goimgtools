//go:build !amd64 || purego

package jpegstream

const useAVX2 = false

func islowAVX2(b *block, dst *byte, stride int) { panic("unreachable") }

func yccH2V2AVX2(dst, y, cbN, cbF, crN, crF *byte, chunks int) { panic("unreachable") }

func yccH2V1AVX2(dst, y, cb, cr *byte, chunks int) { panic("unreachable") }

func yccRowAVX2(dst, y, cb, cr *byte, chunks int) { panic("unreachable") }

func fdctQuantAVX2(b *block, half *[blockSize]int32, recip *[blockSize]uint32, out *[blockSize]int16) {
	panic("unreachable")
}

func rgbaBlockAVX2(pix *byte, stride int, y, cb, cr *block) { panic("unreachable") }

func scaleAVX2(dst *block, src *[4]block) { panic("unreachable") }
