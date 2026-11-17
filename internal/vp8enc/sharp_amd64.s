// The plain loops of sharp YUV (sharp.go) with AVX2, every sample that of
// the Go code: the 9-3-3-1 upsampling of filterRow in 32-bit lanes, and
// the corrections of W (clipped) and chroma (wrapping as int16 does) in
// 16-bit lanes, where their sums fit.

//go:build amd64 && !purego

#include "textflag.h"

DATA shMax<>+0(SB)/4, $1023
GLOBL shMax<>(SB), RODATA|NOPTR, $4
DATA shEight<>+0(SB)/4, $8
GLOBL shEight<>(SB), RODATA|NOPTR, $4
DATA shMaxW<>+0(SB)/2, $1023
GLOBL shMaxW<>(SB), RODATA|NOPTR, $2

// func filterPairsSIMD(a, b *int16, wy, out *uint16, n int)
TEXT ·filterPairsSIMD(SB), NOSPLIT, $0-40
	MOVQ a+0(FP), SI
	MOVQ b+8(FP), DI
	MOVQ wy+16(FP), R8
	MOVQ out+24(FP), R9
	MOVQ n+32(FP), CX
	VPBROADCASTD shMax<>(SB), Y14
	VPBROADCASTD shEight<>(SB), Y13
	VPXOR Y15, Y15, Y15
	SHRQ $3, CX
	JZ fdone
floop:
	VPMOVSXWD (SI), Y0  // a0
	VPMOVSXWD 2(SI), Y1 // a1
	VPMOVSXWD (DI), Y2  // b0
	VPMOVSXWD 2(DI), Y3 // b1
	// v0 = (9*a0 + 3*(a1+b0) + b1 + 8) >> 4
	VPSLLD $3, Y0, Y4
	VPADDD Y0, Y4, Y4
	VPADDD Y2, Y1, Y5
	VPSLLD $1, Y5, Y6
	VPADDD Y6, Y5, Y5
	VPADDD Y5, Y4, Y4
	VPADDD Y3, Y4, Y4
	VPADDD Y13, Y4, Y4
	VPSRAD $4, Y4, Y4
	// v1 = (9*a1 + 3*(a0+b1) + b0 + 8) >> 4
	VPSLLD $3, Y1, Y5
	VPADDD Y1, Y5, Y5
	VPADDD Y3, Y0, Y6
	VPSLLD $1, Y6, Y7
	VPADDD Y7, Y6, Y6
	VPADDD Y6, Y5, Y5
	VPADDD Y2, Y5, Y5
	VPADDD Y13, Y5, Y5
	VPSRAD $4, Y5, Y5
	// v0, v1 of each pair in turn.
	VPUNPCKLDQ Y5, Y4, Y6
	VPUNPCKHDQ Y5, Y4, Y7
	VPERM2I128 $0x20, Y7, Y6, Y8
	VPERM2I128 $0x31, Y7, Y6, Y9
	VPMOVZXWD (R8), Y10
	VPMOVZXWD 16(R8), Y11
	VPADDD Y10, Y8, Y8
	VPADDD Y11, Y9, Y9
	VPMAXSD Y15, Y8, Y8
	VPMAXSD Y15, Y9, Y9
	VPMINSD Y14, Y8, Y8
	VPMINSD Y14, Y9, Y9
	VPACKUSDW Y9, Y8, Y8
	VPERMQ $0xd8, Y8, Y8
	VMOVDQU Y8, (R9)
	ADDQ $16, SI
	ADDQ $16, DI
	ADDQ $32, R8
	ADDQ $32, R9
	DECQ CX
	JNZ floop
fdone:
	VZEROUPPER
	RET

// func updateBySIMD(by, ty, y *uint16, n int)
TEXT ·updateBySIMD(SB), NOSPLIT, $0-32
	MOVQ by+0(FP), DI
	MOVQ ty+8(FP), SI
	MOVQ y+16(FP), R8
	MOVQ n+24(FP), CX
	VPBROADCASTW shMaxW<>(SB), Y14
	VPXOR Y15, Y15, Y15
	SHRQ $4, CX
	JZ bdone
bloop:
	VMOVDQU (DI), Y0
	VPADDW (SI), Y0, Y0
	VPSUBW (R8), Y0, Y0
	VPMAXSW Y15, Y0, Y0
	VPMINSW Y14, Y0, Y0
	VMOVDQU Y0, (DI)
	ADDQ $32, DI
	ADDQ $32, SI
	ADDQ $32, R8
	DECQ CX
	JNZ bloop
bdone:
	VZEROUPPER
	RET

// func updateUVSIMD(cur, tuv, uv *int16, n int)
TEXT ·updateUVSIMD(SB), NOSPLIT, $0-32
	MOVQ cur+0(FP), DI
	MOVQ tuv+8(FP), SI
	MOVQ uv+16(FP), R8
	MOVQ n+24(FP), CX
	SHRQ $4, CX
	JZ udone
uloop:
	VMOVDQU (SI), Y0
	VPSUBW (R8), Y0, Y0
	VPADDW (DI), Y0, Y0
	VMOVDQU Y0, (DI)
	ADDQ $32, DI
	ADDQ $32, SI
	ADDQ $32, R8
	DECQ CX
	JNZ uloop
udone:
	VZEROUPPER
	RET
