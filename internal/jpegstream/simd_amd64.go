//go:build amd64 && !purego

package jpegstream

import "golang.org/x/sys/cpu"

// useAVX2 selects the AVX2 versions of the inverse DCT and of the
// conversion of colour, which give the same bytes as the Go code. The
// purego build tag leaves them out.
var useAVX2 = cpu.X86.HasAVX2

// islowAVX2 is islowStore with AVX2; dst must hold 8 rows of 8 bytes,
// stride apart.
//
//go:noescape
func islowAVX2(b *block, dst *byte, stride int)

// yccH2V2AVX2 is chunks times 16 chroma samples of yccH2V2 with AVX2: the
// samples before and after each are read, 32 pixels written.
//
//go:noescape
func yccH2V2AVX2(dst, y, cbN, cbF, crN, crF *byte, chunks int)

// yccH2V1AVX2 is chunks times 16 chroma samples of yccH2V1 with AVX2.
//
//go:noescape
func yccH2V1AVX2(dst, y, cb, cr *byte, chunks int)

// yccRowAVX2 is chunks times 16 pixels of yccRow with AVX2.
//
//go:noescape
func yccRowAVX2(dst, y, cb, cr *byte, chunks int)
