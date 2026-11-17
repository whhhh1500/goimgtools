//go:build !(amd64 || (arm64 && go1.27)) || purego

package jpegstream

const useSIMD = false

func islowSIMD(b *block, dst *byte, stride int) { panic("unreachable") }

func yccH2V2SIMD(dst, y, cbN, cbF, crN, crF *byte, chunks int) { panic("unreachable") }

func yccH2V1SIMD(dst, y, cb, cr *byte, chunks int) { panic("unreachable") }

func yccRowSIMD(dst, y, cb, cr *byte, chunks int) { panic("unreachable") }

func fdctQuantSIMD(b *block, half *[blockSize]int32, recip *[blockSize]uint32, out *[blockSize]int16) {
	panic("unreachable")
}

func rgbaBlockSIMD(pix *byte, stride int, y, cb, cr *block) { panic("unreachable") }

func scaleSIMD(dst *block, src *[4]block) { panic("unreachable") }

func idct4SIMD(b *block, dst *byte, stride int) { panic("unreachable") }
