// The box blur passes of blur.go (Pillow's BoxBlur) with AVX2: across a
// row the four channels of a pixel in the 32-bit lanes of one register,
// down the rows eight columns at a time; the same uint32 arithmetic as
// the Go code, so every byte is the same.

//go:build amd64 && !purego

#include "textflag.h"

DATA boxHalf<>+0(SB)/4, $0x00800000
GLOBL boxHalf<>(SB), RODATA|NOPTR, $4

// The low byte of each 32-bit lane, packed into the low four bytes of
// each 128-bit lane.
DATA boxPack<>+0(SB)/8, $0x808080800c080400
DATA boxPack<>+8(SB)/8, $0x8080808080808080
DATA boxPack<>+16(SB)/8, $0x808080800c080400
DATA boxPack<>+24(SB)/8, $0x8080808080808080
GLOBL boxPack<>(SB), RODATA|NOPTR, $32

// func hboxRowSIMD(dst, src *byte, w, ri int, ww, fw uint32, keepAlpha bool)
TEXT ·hboxRowSIMD(SB), NOSPLIT, $0-41
	MOVQ dst+0(FP), DI
	MOVQ src+8(FP), SI
	MOVQ w+16(FP), CX
	MOVQ ri+24(FP), DX
	MOVL ww+32(FP), AX
	VMOVD AX, X13
	VPBROADCASTD X13, X13
	MOVL fw+36(FP), AX
	VMOVD AX, X14
	VPBROADCASTD X14, X14
	VPBROADCASTD boxHalf<>(SB), X15
	MOVBQZX keepAlpha+40(FP), BX
	LEAQ -1(CX), R8 // last
	// The sum of the window around pixel 0, the ends repeated.
	VPXOR X0, X0, X0
	MOVQ DX, R9
	NEGQ R9 // k
hinit:
	MOVQ R9, R10
	XORQ R11, R11
	CMPQ R10, R11
	CMOVQLT R11, R10
	CMPQ R10, R8
	CMOVQGT R8, R10
	VPMOVZXBD (SI)(R10*4), X1
	VPADDD X1, X0, X0
	INCQ R9
	CMPQ R9, DX
	JLE hinit
	XORQ R9, R9 // x
	XORQ R11, R11
hloop:
	// left = x-ri-1, right = x+ri+1, out = x-ri, each clamped.
	MOVQ R9, R10
	SUBQ DX, R10
	LEAQ -1(R10), R12
	CMPQ R12, R11
	CMOVQLT R11, R12
	CMPQ R10, R11
	CMOVQLT R11, R10
	LEAQ 1(R9)(DX*1), R13
	CMPQ R13, R8
	CMOVQGT R8, R13
	VPMOVZXBD (SI)(R12*4), X1 // left
	VPMOVZXBD (SI)(R13*4), X2 // right
	VPMOVZXBD (SI)(R10*4), X3 // leaving
	VPMULLD X13, X0, X4
	VPADDD X2, X1, X5
	VPMULLD X14, X5, X5
	VPADDD X5, X4, X4
	VPADDD X15, X4, X4
	VPSRLD $24, X4, X4
	VPSHUFB boxPack<>(SB), X4, X4
	TESTQ BX, BX
	JZ hstore
	MOVBLZX 3(DI)(R9*4), AX
	VPINSRB $3, AX, X4, X4
hstore:
	VMOVD X4, (DI)(R9*4)
	VPADDD X2, X0, X0
	VPSUBD X3, X0, X0
	INCQ R9
	CMPQ R9, CX
	JLT hloop
	RET

// func vboxRowSIMD(out, a, b, c *byte, sum *uint32, n int, ww, fw uint32)
TEXT ·vboxRowSIMD(SB), NOSPLIT, $0-56
	MOVQ out+0(FP), DI
	MOVQ a+8(FP), R8
	MOVQ b+16(FP), R9
	MOVQ c+24(FP), R10
	MOVQ sum+32(FP), SI
	MOVQ n+40(FP), CX
	MOVL ww+48(FP), AX
	VMOVD AX, X13
	VPBROADCASTD X13, Y13
	MOVL fw+52(FP), AX
	VMOVD AX, X14
	VPBROADCASTD X14, Y14
	VPBROADCASTD boxHalf<>(SB), Y15
	VMOVDQU boxPack<>(SB), Y12
	SHRQ $3, CX
	JZ vdone
vloop:
	VMOVDQU (SI), Y0         // sums
	VPMOVZXBD (R8), Y1       // a
	VPMOVZXBD (R9), Y2       // b
	VPMOVZXBD (R10), Y3      // c
	VPMULLD Y13, Y0, Y4
	VPADDD Y2, Y1, Y5
	VPMULLD Y14, Y5, Y5
	VPADDD Y5, Y4, Y4
	VPADDD Y15, Y4, Y4
	VPSRLD $24, Y4, Y4
	VPSHUFB Y12, Y4, Y4
	VMOVD X4, (DI)
	VEXTRACTI128 $1, Y4, X5
	VMOVD X5, 4(DI)
	VPADDD Y2, Y0, Y0
	VPSUBD Y3, Y0, Y0
	VMOVDQU Y0, (SI)
	ADDQ $32, SI
	ADDQ $8, R8
	ADDQ $8, R9
	ADDQ $8, R10
	ADDQ $8, DI
	DECQ CX
	JNZ vloop
vdone:
	VZEROUPPER
	RET

// SORT3 sorts the bytes of A, B and C lane by lane into LO, MID, HI
// (T a temporary): lo = min(min(a,b),c), mid = max(min(a,b),
// min(max(a,b),c)), hi = max(max(a,b),c).
#define SORT3(A, B, C, LO, MID, HI, T) \
	VPMINUB B, A, T; \
	VPMAXUB B, A, HI; \
	VPMINUB C, T, LO; \
	VPMINUB C, HI, MID; \
	VPMAXUB C, HI, HI; \
	VPMAXUB T, MID, MID

// func median3SIMD(dst, r0, r1, r2 *byte, n int)
TEXT ·median3SIMD(SB), NOSPLIT, $0-40
	MOVQ dst+0(FP), DI
	MOVQ r0+8(FP), R8
	MOVQ r1+16(FP), R9
	MOVQ r2+24(FP), R10
	MOVQ n+32(FP), CX
	SHRQ $3, CX
	JZ mdone
mloop:
	// The columns before, at and after each of eight pixels, sorted.
	VMOVDQU (R8), Y0
	VMOVDQU (R9), Y1
	VMOVDQU (R10), Y2
	SORT3(Y0, Y1, Y2, Y3, Y4, Y5, Y15)
	VMOVDQU 4(R8), Y0
	VMOVDQU 4(R9), Y1
	VMOVDQU 4(R10), Y2
	SORT3(Y0, Y1, Y2, Y6, Y7, Y8, Y15)
	VMOVDQU 8(R8), Y0
	VMOVDQU 8(R9), Y1
	VMOVDQU 8(R10), Y2
	SORT3(Y0, Y1, Y2, Y9, Y10, Y11, Y15)
	// The largest of the smallest, the median of the middles, the
	// smallest of the largest, and their median.
	VPMAXUB Y6, Y3, Y3
	VPMAXUB Y9, Y3, Y3
	VPMINUB Y8, Y5, Y5
	VPMINUB Y11, Y5, Y5
	VPMINUB Y7, Y4, Y12
	VPMAXUB Y7, Y4, Y13
	VPMINUB Y10, Y13, Y13
	VPMAXUB Y12, Y13, Y4
	VPMINUB Y4, Y3, Y12
	VPMAXUB Y4, Y3, Y13
	VPMINUB Y5, Y13, Y13
	VPMAXUB Y12, Y13, Y0
	VMOVDQU Y0, (DI)
	ADDQ $32, R8
	ADDQ $32, R9
	ADDQ $32, R10
	ADDQ $32, DI
	DECQ CX
	JNZ mloop
mdone:
	VZEROUPPER
	RET
