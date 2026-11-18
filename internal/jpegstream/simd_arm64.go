//go:build arm64 && go1.27 && !purego

package jpegstream

import "unsafe"

// useSIMD selects the NEON versions of the inverse DCT and of the
// conversions of colour, which give the same bytes as the Go code. NEON is
// part of every arm64 processor; the purego build tag leaves them out, and
// Go before 1.27, whose assembler lacks some of the instructions.
var useSIMD = true

//go:noescape
func islowSIMD(b *block, dst *byte, stride int)

//go:noescape
func idct4SIMD(b *block, dst *byte, stride int)

//go:noescape
func yccH2V2SIMD(dst, y, cbN, cbF, crN, crF *byte, chunks int)

//go:noescape
func yccH2V1SIMD(dst, y, cb, cr *byte, chunks int)

//go:noescape
func yccRowSIMD(dst, y, cb, cr *byte, chunks int)

//go:noescape
func fdctQuantSIMD(b *block, half *[blockSize]int32, recip *[blockSize]uint32, out *[blockSize]int16)

//go:noescape
func rgbaBlockSIMD(pix *byte, stride int, y, cb, cr *block)

//go:noescape
func scaleSIMD(dst *block, src *[4]block)

// nonzeros returns the mask of the nonzero values of zz: bit k is set when
// zz[k] != 0 (acMaskAsm with nothing dropped).
func nonzeros(zz *qblock) uint64 { return acMaskAsm((*byte)(unsafe.Pointer(&zz[0])), 0) }

// acMaskSIMD selects acMaskAsm, acFirst's mask with NEON.
const acMaskSIMD = true

// acMaskAsm returns the coefficients of the block at w (64 int16, as
// stored) that are not zero once |c| >> al: bit k for coefficient k.
//
//go:noescape
func acMaskAsm(w *byte, al uint) uint64

// fdctQuant is quantize for useSIMD and no dead zone.
func fdctQuant(e *encoder, b *block, q quantIndex, zz *qblock) {
	fdctQuantSIMD(b, &e.natHalf[q], &e.natRecip[q], (*[blockSize]int16)(zz))
}
