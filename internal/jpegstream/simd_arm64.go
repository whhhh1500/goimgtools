//go:build arm64 && go1.27 && !purego

package jpegstream

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
// zz[k] != 0.
func nonzeros(zz *qblock) uint64 { return nonzerosGo(zz) }

const acMaskSIMD = false

func acMaskAsm(w *byte, al uint) uint64 { panic("unreachable") }
