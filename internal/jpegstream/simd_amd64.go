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

// fdctQuant16 is fdct of b and the division of quantizeCoefs (without a
// dead zone) with AVX2 in 16-bit lanes, after libjpeg-turbo's, by the table
// tab (encoder.nat16), the coefficients in zig-zag order; b is not changed.
//
//go:noescape
func fdctQuant16(b *block, tab *[3][blockSize]uint16, out *[blockSize]int16)

// fdctQuant is quantize for useSIMD and no dead zone.
func fdctQuant(e *encoder, b *block, q quantIndex, zz *qblock) {
	fdctQuant16(b, &e.nat16[q], (*[blockSize]int16)(zz))
}

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

// nonzerosSIMD is nonzeros with AVX2.
//
//go:noescape
func nonzerosSIMD(zz *qblock) uint64

// nonzeros returns the mask of the nonzero values of zz: bit k is set when
// zz[k] != 0.
func nonzeros(zz *qblock) uint64 {
	if useSIMD {
		return nonzerosSIMD(zz)
	}
	return nonzerosGo(zz)
}

// acMaskSIMD selects acMaskAsm, acFirst's mask with AVX2.
var acMaskSIMD = useSIMD

// acMaskAsm returns the coefficients of the block at w (64 int16, as
// stored) that are not zero once |c| >> al: bit k for coefficient k.
//
//go:noescape
func acMaskAsm(w *byte, al uint) uint64
