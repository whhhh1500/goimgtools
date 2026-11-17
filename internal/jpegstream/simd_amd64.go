//go:build amd64 && !purego

package jpegstream

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 versions of the inverse DCT and of the
// conversion of colour, which give the same bytes as the Go code. The
// purego build tag leaves them out.
var useSIMD = cpu.X86.HasAVX2

// islowSIMD is islowStore with AVX2; dst must hold 8 rows of 8 bytes,
// stride apart.
//
//go:noescape
func islowSIMD(b *block, dst *byte, stride int)

// yccH2V2SIMD is chunks times 16 chroma samples of yccH2V2 with AVX2: the
// samples before and after each are read, 32 pixels written.
//
//go:noescape
func yccH2V2SIMD(dst, y, cbN, cbF, crN, crF *byte, chunks int)

// yccH2V1SIMD is chunks times 16 chroma samples of yccH2V1 with AVX2.
//
//go:noescape
func yccH2V1SIMD(dst, y, cb, cr *byte, chunks int)

// yccRowSIMD is chunks times 16 pixels of yccRow with AVX2.
//
//go:noescape
func yccRowSIMD(dst, y, cb, cr *byte, chunks int)

// fdctQuantSIMD is fdct of b and the division of quantizeCoefs (without a
// dead zone) with AVX2, the coefficients in natural order; b is not
// changed.
//
//go:noescape
func fdctQuantSIMD(b *block, half *[blockSize]int32, recip *[blockSize]uint32, out *[blockSize]int16)

// rgbaBlockSIMD is rgbaToYCbCr of a whole 8x8 block, pix its first pixel,
// with AVX2.
//
//go:noescape
func rgbaBlockSIMD(pix *byte, stride int, y, cb, cr *block)

// scaleSIMD is scale with AVX2.
//
//go:noescape
func scaleSIMD(dst *block, src *[4]block)

// idct4SIMD is idct4Store with AVX2; dst must hold 4 rows of 4 bytes,
// stride apart.
//
//go:noescape
func idct4SIMD(b *block, dst *byte, stride int)
