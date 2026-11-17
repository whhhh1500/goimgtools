// The inverse transform of idct.go (inverseDCT4, which the encoder's
// reconstruction repeats) with AVX2, every byte that of the Go code: in
// 16-bit lanes when no coefficient is beyond 2048 (where they cannot
// overflow), otherwise in 32-bit lanes, wrapping where the Go code wraps.

//go:build amd64 && !purego

#include "textflag.h"

// c2, c2, c2, c2 | c1, c1, c1, c1 and the other way round (idct.go's inverseDCT4).
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

// func idctAddAsm(coef *[16]int16, ref *byte, rStride int, dst *byte, dStride int)
TEXT ·idctAddAsm(SB), NOSPLIT, $0-40
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
