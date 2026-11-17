// The 4x4 transforms, quantizer and squared error of dct.go and frame.go
// with AVX2, every result that of the Go code. The forward transform
// follows libwebp's FTransform_SSE2 (its 16-bit steps cannot overflow for
// differences of 8-bit samples); the inverse works in 32-bit lanes, as the
// Go code does, wrapping where it wraps.

//go:build amd64 && !purego

#include "textflag.h"

DATA fdK88p<>+0(SB)/8, $0x0008000800080008
DATA fdK88p<>+8(SB)/8, $0x0008000800080008
GLOBL fdK88p<>(SB), RODATA|NOPTR, $16
DATA fdK88m<>+0(SB)/8, $0xfff80008fff80008
DATA fdK88m<>+8(SB)/8, $0xfff80008fff80008
GLOBL fdK88m<>(SB), RODATA|NOPTR, $16
// (a3, a2) pairs: a3*5352 + a2*2217 and a3*2217 - a2*5352.
DATA fdK1p<>+0(SB)/8, $0x08a914e808a914e8
DATA fdK1p<>+8(SB)/8, $0x08a914e808a914e8
GLOBL fdK1p<>(SB), RODATA|NOPTR, $16
DATA fdK1m<>+0(SB)/8, $0xeb1808a9eb1808a9
DATA fdK1m<>+8(SB)/8, $0xeb1808a9eb1808a9
GLOBL fdK1m<>(SB), RODATA|NOPTR, $16
// (a2, a3) pairs: a2*2217 + a3*5352 and a3*2217 - a2*5352.
DATA fdK2p<>+0(SB)/8, $0x14e808a914e808a9
DATA fdK2p<>+8(SB)/8, $0x14e808a914e808a9
GLOBL fdK2p<>(SB), RODATA|NOPTR, $16
DATA fdK2m<>+0(SB)/8, $0x08a9eb1808a9eb18
DATA fdK2m<>+8(SB)/8, $0x08a9eb1808a9eb18
GLOBL fdK2m<>(SB), RODATA|NOPTR, $16
DATA fd1812<>+0(SB)/8, $0x0000071400000714
DATA fd1812<>+8(SB)/8, $0x0000071400000714
GLOBL fd1812<>(SB), RODATA|NOPTR, $16
DATA fd937<>+0(SB)/8, $0x000003a9000003a9
DATA fd937<>+8(SB)/8, $0x000003a9000003a9
GLOBL fd937<>(SB), RODATA|NOPTR, $16
// 12000 plus one in the upper half: (a3 != 0) added as 1 - (a3 == 0).
DATA fd12000<>+0(SB)/8, $0x00012ee000012ee0
DATA fd12000<>+8(SB)/8, $0x00012ee000012ee0
GLOBL fd12000<>(SB), RODATA|NOPTR, $16
DATA fd51000<>+0(SB)/8, $0x0000c7380000c738
DATA fd51000<>+8(SB)/8, $0x0000c7380000c738
GLOBL fd51000<>(SB), RODATA|NOPTR, $16
DATA fd7<>+0(SB)/8, $0x0007000700070007
DATA fd7<>+8(SB)/8, $0x0007000700070007
GLOBL fd7<>(SB), RODATA|NOPTR, $16

// FDCT is the forward transform of the 4x4 difference of the blocks at
// SI (stride AX) and DI (stride BX), following libwebp's
// FTransform_SSE2: coefficients 0-7 in X7, 8-15 in X8.
#define FDCT \
	VMOVD (SI), X0; \
	VPINSRD $1, (SI)(AX*1), X0, X0; \
	LEAQ (SI)(AX*2), SI; \
	VPINSRD $2, (SI), X0, X0; \
	VPINSRD $3, (SI)(AX*1), X0, X0; \
	VMOVD (DI), X1; \
	VPINSRD $1, (DI)(BX*1), X1, X1; \
	LEAQ (DI)(BX*2), DI; \
	VPINSRD $2, (DI), X1, X1; \
	VPINSRD $3, (DI)(BX*1), X1, X1; \
	VPMOVZXBW X0, Y0; \
	VPMOVZXBW X1, Y1; \
	VPSUBW Y1, Y0, Y0; \
	VPSHUFD $0xd8, Y0, Y0; \
	VEXTRACTI128 $1, Y0, X1; \
	VPSHUFHW $0xb1, X0, X0; \
	VPSHUFHW $0xb1, X1, X1; \
	VPUNPCKLQDQ X1, X0, X2; \
	VPUNPCKHQDQ X1, X0, X3; \
	VPADDW X3, X2, X4; \
	VPSUBW X3, X2, X5; \
	VPMADDWD fdK88p<>(SB), X4, X6; \
	VPMADDWD fdK88m<>(SB), X4, X7; \
	VPMADDWD fdK1p<>(SB), X5, X8; \
	VPMADDWD fdK1m<>(SB), X5, X9; \
	VPADDD fd1812<>(SB), X8, X8; \
	VPADDD fd937<>(SB), X9, X9; \
	VPSRAD $9, X8, X8; \
	VPSRAD $9, X9, X9; \
	VPACKSSDW X7, X6, X6; \
	VPACKSSDW X9, X8, X8; \
	VPUNPCKLWD X8, X6, X7; \
	VPUNPCKHWD X8, X6, X9; \
	VPUNPCKHDQ X9, X7, X10; \
	VPUNPCKLDQ X9, X7, X0; \
	VPSHUFD $0x4e, X10, X1; \
	VPSUBW X1, X0, X2; \
	VPUNPCKHQDQ X2, X2, X3; \
	VPUNPCKLWD X2, X3, X4; \
	VPMADDWD fdK2p<>(SB), X4, X5; \
	VPMADDWD fdK2m<>(SB), X4, X6; \
	VPADDD fd12000<>(SB), X5, X5; \
	VPADDD fd51000<>(SB), X6, X6; \
	VPSRAD $16, X5, X5; \
	VPSRAD $16, X6, X6; \
	VPACKSSDW X5, X5, X5; \
	VPACKSSDW X6, X6, X6; \
	VPXOR X7, X7, X7; \
	VPCMPEQW X7, X2, X7; \
	VPADDW X7, X5, X5; \
	VPADDW X1, X0, X2; \
	VPADDW fd7<>(SB), X2, X3; \
	VPUNPCKHQDQ X2, X2, X4; \
	VPADDW X4, X3, X7; \
	VPSUBW X4, X3, X8; \
	VPSRAW $4, X7, X7; \
	VPSRAW $4, X8, X8; \
	VPUNPCKLQDQ X5, X7, X7; \
	VPUNPCKLQDQ X6, X8, X8

// func fdctSIMD(src *byte, sStride int, ref *byte, rStride int, out *[16]int16)
TEXT ·fdctSIMD(SB), NOSPLIT, $0-40
	MOVQ src+0(FP), SI
	MOVQ sStride+8(FP), AX
	MOVQ ref+16(FP), DI
	MOVQ rStride+24(FP), BX
	MOVQ out+32(FP), DX
	FDCT
	VMOVDQU X7, (DX)
	VMOVDQU X8, 16(DX)
	VZEROUPPER
	RET

DATA fd31<>+0(SB)/8, $0x001f001f001f001f
DATA fd31<>+8(SB)/8, $0x001f001f001f001f
GLOBL fd31<>(SB), RODATA|NOPTR, $16

// func histBinsSIMD(src *byte, sStride int, ref *byte, rStride int, bins *[16]uint8) uint32
TEXT ·histBinsSIMD(SB), NOSPLIT, $0-44
	MOVQ src+0(FP), SI
	MOVQ sStride+8(FP), AX
	MOVQ ref+16(FP), DI
	MOVQ rStride+24(FP), BX
	MOVQ bins+32(FP), DX
	FDCT
	VPABSW X7, X7
	VPABSW X8, X8
	VPSRLW $3, X7, X7
	VPSRLW $3, X8, X8
	VPMINUW fd31<>(SB), X7, X7
	VPMINUW fd31<>(SB), X8, X8
	VPACKUSWB X8, X7, X7
	VMOVDQU X7, (DX)
	VPXOR X9, X9, X9
	VPCMPEQB X9, X7, X9
	VPMOVMSKB X9, AX
	XORL $0xffff, AX
	MOVL AX, ret+40(FP)
	VZEROUPPER
	RET

// c2, c2, c2, c2 | c1, c1, c1, c1 and the other way round (dct.go's idctAdd).
DATA idK21<>+0(SB)/8, $0x00008a8c00008a8c
DATA idK21<>+8(SB)/8, $0x00008a8c00008a8c
DATA idK21<>+16(SB)/8, $0x00014e7b00014e7b
DATA idK21<>+24(SB)/8, $0x00014e7b00014e7b
GLOBL idK21<>(SB), RODATA|NOPTR, $32
DATA idK12<>+0(SB)/8, $0x00014e7b00014e7b
DATA idK12<>+8(SB)/8, $0x00014e7b00014e7b
DATA idK12<>+16(SB)/8, $0x00008a8c00008a8c
DATA idK12<>+24(SB)/8, $0x00008a8c00008a8c
GLOBL idK12<>(SB), RODATA|NOPTR, $32
DATA id4<>+0(SB)/8, $0x0000000400000004
DATA id4<>+8(SB)/8, $0x0000000400000004
GLOBL id4<>(SB), RODATA|NOPTR, $16

// 16-bit constants of the narrow inverse transform: 35468 as an int16
// (35468 - 65536) and 20091, so that mulhi(x, k) + x is (x*35468)>>16 and
// (x*85627)>>16 exactly.
DATA idM21<>+0(SB)/8, $0x8a8c8a8c8a8c8a8c
DATA idM21<>+8(SB)/8, $0x4e7b4e7b4e7b4e7b
GLOBL idM21<>(SB), RODATA|NOPTR, $16
DATA idM12<>+0(SB)/8, $0x4e7b4e7b4e7b4e7b
DATA idM12<>+8(SB)/8, $0x8a8c8a8c8a8c8a8c
GLOBL idM12<>(SB), RODATA|NOPTR, $16
DATA id2048<>+0(SB)/8, $0x0800080008000800
DATA id2048<>+8(SB)/8, $0x0800080008000800
GLOBL id2048<>(SB), RODATA|NOPTR, $16
DATA id4w<>+0(SB)/8, $0x0004000400040004
DATA id4w<>+8(SB)/8, $0x0004000400040004
GLOBL id4w<>(SB), RODATA|NOPTR, $16

// func idctAddSIMD(coef *[16]int16, ref *byte, rStride int, dst *byte, dStride int)
TEXT ·idctAddSIMD(SB), NOSPLIT, $0-40
	MOVQ coef+0(FP), SI
	MOVQ ref+8(FP), DI
	MOVQ rStride+16(FP), BX
	MOVQ dst+24(FP), R8
	MOVQ dStride+32(FP), R9
	// Coefficients of at most 2048 (what the encoder's blocks have but
	// in corner cases) go through 16-bit lanes, which cannot overflow then
	// (the sums of pass 1 stay within 7882, those of pass 2 within 30335),
	// with mulhi(x, k) + x for the products; others through the 32-bit
	// lanes further on, which wrap where the Go code wraps.
	VMOVDQU (SI), X0
	VMOVDQU 16(SI), X1
	VPABSW X0, X2
	VPABSW X1, X3
	VPMAXUW X3, X2, X2
	VPSUBUSW id2048<>(SB), X2, X2
	VPTEST X2, X2
	JNZ wide
	// Pass 1: rows 0 | 1 and 2 | 3, the lanes the columns i.
	VPADDW X1, X0, X2      // a
	VPSUBW X1, X0, X3      // b
	VPUNPCKHQDQ X1, X0, X4 // c4 | c12
	VPMULHW idM21<>(SB), X4, X5
	VPMULHW idM12<>(SB), X4, X6
	VPADDW X4, X5, X5      // (c4*c2)>>16 | (c12*c1)>>16
	VPADDW X4, X6, X6      // (c4*c1)>>16 | (c12*c2)>>16
	VPSHUFD $0x4e, X5, X7
	VPSUBW X7, X5, X5      // c
	VPSHUFD $0x4e, X6, X7
	VPADDW X7, X6, X6      // d
	VPADDW X6, X2, X8
	VPADDW X5, X3, X9
	VPSUBW X5, X3, X10
	VPSUBW X6, X2, X11
	VPUNPCKLWD X9, X8, X8
	VPUNPCKLWD X11, X10, X10
	VPUNPCKLDQ X10, X8, X0 // m[0][j] | m[1][j]
	VPUNPCKHDQ X10, X8, X1 // m[2][j] | m[3][j]
	// Pass 2.
	VPADDW X1, X0, X2
	VPSUBW X1, X0, X3
	VPADDW id4w<>(SB), X2, X2 // a
	VPADDW id4w<>(SB), X3, X3 // b
	VPUNPCKHQDQ X1, X0, X4
	VPMULHW idM21<>(SB), X4, X5
	VPMULHW idM12<>(SB), X4, X6
	VPADDW X4, X5, X5
	VPADDW X4, X6, X6
	VPSHUFD $0x4e, X5, X7
	VPSUBW X7, X5, X5 // c
	VPSHUFD $0x4e, X6, X7
	VPADDW X7, X6, X6 // d
	VPADDW X6, X2, X8
	VPADDW X5, X3, X9
	VPSUBW X5, X3, X10
	VPSUBW X6, X2, X11
	VPSRAW $3, X8, X8
	VPSRAW $3, X9, X9
	VPSRAW $3, X10, X10
	VPSRAW $3, X11, X11
	VPUNPCKLWD X9, X8, X8
	VPUNPCKLWD X11, X10, X10
	VPUNPCKLDQ X10, X8, X0 // rows 0 | 1
	VPUNPCKHDQ X10, X8, X1 // rows 2 | 3
	VMOVD (DI), X4
	VPINSRD $1, (DI)(BX*1), X4, X4
	LEAQ (DI)(BX*2), DI
	VPINSRD $2, (DI), X4, X4
	VPINSRD $3, (DI)(BX*1), X4, X4
	VPMOVZXBW X4, X5
	VPSRLDQ $8, X4, X4
	VPMOVZXBW X4, X4
	VPADDW X5, X0, X0
	VPADDW X4, X1, X1
	VPACKUSWB X1, X0, X0
	VMOVD X0, (R8)
	VPEXTRD $1, X0, (R8)(R9*1)
	LEAQ (R8)(R9*2), R8
	VPEXTRD $2, X0, (R8)
	VPEXTRD $3, X0, (R8)(R9*1)
	RET
wide:
	VMOVDQU idK21<>(SB), Y14
	VMOVDQU idK12<>(SB), Y15
	// Pass 1: rows of coefficients, lanes the columns i.
	VPMOVSXWD 0(SI), X0
	VPMOVSXWD 16(SI), X2
	VPMOVSXWD 8(SI), Y1
	VPMOVSXWD 24(SI), X3
	VINSERTI128 $1, X3, Y1, Y1 // c4 | c12
	VPADDD X2, X0, X4          // a
	VPSUBD X2, X0, X5          // b
	VPMULLD Y14, Y1, Y6        // c4*c2 | c12*c1
	VPMULLD Y15, Y1, Y7        // c4*c1 | c12*c2
	VPSRAD $16, Y6, Y6
	VPSRAD $16, Y7, Y7
	VEXTRACTI128 $1, Y6, X8
	VEXTRACTI128 $1, Y7, X9
	VPSUBD X8, X6, X6 // c
	VPADDD X9, X7, X7 // d
	VPADDD X7, X4, X0 // m[i][0]
	VPADDD X6, X5, X1 // m[i][1]
	VPSUBD X6, X5, X2 // m[i][2]
	VPSUBD X7, X4, X3 // m[i][3]
	// Transposed: lanes j of m[0][j], ..., m[3][j].
	VPUNPCKLDQ X1, X0, X4
	VPUNPCKHDQ X1, X0, X5
	VPUNPCKLDQ X3, X2, X6
	VPUNPCKHDQ X3, X2, X7
	VPUNPCKLQDQ X6, X4, X0
	VPUNPCKHQDQ X6, X4, X1
	VPUNPCKLQDQ X7, X5, X2
	VPUNPCKHQDQ X7, X5, X3
	// Pass 2.
	VPADDD id4<>(SB), X0, X0
	VPADDD X2, X0, X4 // a
	VPSUBD X2, X0, X5 // b
	VINSERTI128 $1, X3, Y1, Y1
	VPMULLD Y14, Y1, Y6
	VPMULLD Y15, Y1, Y7
	VPSRAD $16, Y6, Y6
	VPSRAD $16, Y7, Y7
	VEXTRACTI128 $1, Y6, X8
	VEXTRACTI128 $1, Y7, X9
	VPSUBD X8, X6, X6 // c
	VPADDD X9, X7, X7 // d
	VPADDD X7, X4, X0
	VPADDD X6, X5, X1
	VPSUBD X6, X5, X2
	VPSUBD X7, X4, X3
	VPSRAD $3, X0, X0
	VPSRAD $3, X1, X1
	VPSRAD $3, X2, X2
	VPSRAD $3, X3, X3
	// Transposed: the rows of the residual.
	VPUNPCKLDQ X1, X0, X4
	VPUNPCKHDQ X1, X0, X5
	VPUNPCKLDQ X3, X2, X6
	VPUNPCKHDQ X3, X2, X7
	VPUNPCKLQDQ X6, X4, X0
	VPUNPCKHQDQ X6, X4, X1
	VPUNPCKLQDQ X7, X5, X2
	VPUNPCKHQDQ X7, X5, X3
	// Plus ref, clipped to 0..255: saturated to 16 bits, added with
	// saturation, then saturated to 8 bits, which clips as clip8 does.
	VPACKSSDW X1, X0, X0
	VPACKSSDW X3, X2, X2
	VMOVD (DI), X4
	VPINSRD $1, (DI)(BX*1), X4, X4
	LEAQ (DI)(BX*2), DI
	VPINSRD $2, (DI), X4, X4
	VPINSRD $3, (DI)(BX*1), X4, X4
	VPMOVZXBW X4, X5
	VPSRLDQ $8, X4, X4
	VPMOVZXBW X4, X4
	VPADDSW X5, X0, X0
	VPADDSW X4, X2, X2
	VPACKUSWB X2, X0, X0
	VMOVD X0, (R8)
	VPEXTRD $1, X0, (R8)(R9*1)
	LEAQ (R8)(R9*2), R8
	VPEXTRD $2, X0, (R8)
	VPEXTRD $3, X0, (R8)(R9*1)
	VZEROUPPER
	RET

// func sse4SIMD(a *byte, aStride int, b *byte, bStride int) int
TEXT ·sse4SIMD(SB), NOSPLIT, $0-40
	MOVQ a+0(FP), SI
	MOVQ aStride+8(FP), AX
	MOVQ b+16(FP), DI
	MOVQ bStride+24(FP), BX
	VMOVD (SI), X0
	VPINSRD $1, (SI)(AX*1), X0, X0
	LEAQ (SI)(AX*2), SI
	VPINSRD $2, (SI), X0, X0
	VPINSRD $3, (SI)(AX*1), X0, X0
	VMOVD (DI), X1
	VPINSRD $1, (DI)(BX*1), X1, X1
	LEAQ (DI)(BX*2), DI
	VPINSRD $2, (DI), X1, X1
	VPINSRD $3, (DI)(BX*1), X1, X1
	VPMOVZXBW X0, Y0
	VPMOVZXBW X1, Y1
	VPSUBW Y1, Y0, Y0
	VPMADDWD Y0, Y0, Y0
	VEXTRACTI128 $1, Y0, X1
	VPADDD X1, X0, X0
	VPSHUFD $0x4e, X0, X1
	VPADDD X1, X0, X0
	VPSHUFD $0xb1, X0, X1
	VPADDD X1, X0, X0
	VMOVD X0, AX
	MOVQ AX, ret+32(FP)
	VZEROUPPER
	RET

// The levels in scan order: bytes of the raster positions, from this lane
// and from the other one (the lanes swapped first).
DATA qzA<>+0(SB)/8, $0x8080090803020100
DATA qzA<>+8(SB)/8, $0x0d0c070605040b0a
DATA qzA<>+16(SB)/8, $0x05040b0a09080302
DATA qzA<>+24(SB)/8, $0x0f0e0d0c07068080
GLOBL qzA<>(SB), RODATA|NOPTR, $32
DATA qzB<>+0(SB)/8, $0x0100808080808080
DATA qzB<>+8(SB)/8, $0x8080808080808080
DATA qzB<>+16(SB)/8, $0x8080808080808080
DATA qzB<>+24(SB)/8, $0x8080808080800f0e
GLOBL qzB<>(SB), RODATA|NOPTR, $32
DATA qMax<>+0(SB)/8, $0x07ff07ff07ff07ff
DATA qMax<>+8(SB)/8, $0x07ff07ff07ff07ff
DATA qMax<>+16(SB)/8, $0x07ff07ff07ff07ff
DATA qMax<>+24(SB)/8, $0x07ff07ff07ff07ff
GLOBL qMax<>(SB), RODATA|NOPTR, $32

// func quantizeSIMD(in *[16]int16, v *quantVec, lv, deq *[16]int16) int
TEXT ·quantizeSIMD(SB), NOSPLIT, $0-40
	MOVQ in+0(FP), SI
	MOVQ v+8(FP), DI
	MOVQ lv+16(FP), R8
	MOVQ deq+24(FP), R9
	VMOVDQU (SI), Y0
	VPSRAW $15, Y0, Y1 // sign
	VPABSW Y0, Y2
	VMOVDQU 32(DI), Y3 // iq
	VPMULLW Y3, Y2, Y4
	VPMULHUW Y3, Y2, Y5
	VPUNPCKLWD Y5, Y4, Y6 // |c|*iq of 0-3, 8-11
	VPUNPCKHWD Y5, Y4, Y7 // of 4-7, 12-15
	VPADDD 64(DI), Y6, Y6
	VPADDD 96(DI), Y7, Y7
	VPSRLD $17, Y6, Y6
	VPSRLD $17, Y7, Y7
	VPACKSSDW Y7, Y6, Y6
	VPMINSW qMax<>(SB), Y6, Y6
	VPXOR Y1, Y6, Y6
	VPSUBW Y1, Y6, Y6     // levels, raster order
	VPMULLW (DI), Y6, Y7
	VMOVDQU Y7, (R9)
	VPSHUFB qzA<>(SB), Y6, Y8
	VPERMQ $0x4e, Y6, Y9
	VPSHUFB qzB<>(SB), Y9, Y9
	VPOR Y9, Y8, Y8
	VMOVDQU Y8, (R8)
	VPXOR Y10, Y10, Y10
	VPCMPEQW Y10, Y8, Y8
	VPMOVMSKB Y8, AX
	NOTL AX
	MOVQ $-1, BX
	BSRL AX, CX
	JZ done
	SHRL $1, CX
	MOVQ CX, BX
done:
	MOVQ BX, ret+32(FP)
	VZEROUPPER
	RET
