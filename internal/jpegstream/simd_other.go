//go:build !amd64 || purego

package jpegstream

const useAVX2 = false

func islowAVX2(b *block, dst *byte, stride int) { panic("unreachable") }

func yccH2V2AVX2(dst, y, cbN, cbF, crN, crF *byte, chunks int) { panic("unreachable") }

func yccH2V1AVX2(dst, y, cb, cr *byte, chunks int) { panic("unreachable") }

func yccRowAVX2(dst, y, cb, cr *byte, chunks int) { panic("unreachable") }
