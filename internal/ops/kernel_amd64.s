// The passes of BlurLibvips (blurkernel.go) with AVX2, eight 32-bit sums
// at a time: the same uint32 arithmetic as the Go code, wrapping where it
// wraps, so every byte is the same.

//go:build amd64 && !purego

#include "textflag.h"

// func kernelPrefixSIMD(pre *uint32, row *byte, n int)
TEXT ·kernelPrefixSIMD(SB), NOSPLIT, $0-24
	MOVQ pre+0(FP), DI
	MOVQ row+8(FP), SI
	MOVQ n+16(FP), CX
	VMOVDQU (DI), X0
kploop:
	VPMOVZXBD (SI), X1
	VPADDD X1, X0, X0
	VMOVDQU X0, 16(DI)
	ADDQ $4, SI
	ADDQ $16, DI
	DECQ CX
	JNZ kploop
	RET

// func kernelHSIMD(dst *byte, t *uint32, hiOff, loOff *int64, wt *uint32, nb, n int, half, shift uint32, alpha uint64)
TEXT ·kernelHSIMD(SB), NOSPLIT, $0-72
	MOVQ dst+0(FP), DI
	MOVQ t+8(FP), SI
	MOVQ hiOff+16(FP), R8
	MOVQ loOff+24(FP), R9
	MOVQ wt+32(FP), R10
	MOVQ nb+40(FP), R11
	MOVQ n+48(FP), CX
	MOVL half+56(FP), AX
	VMOVD AX, X14
	VPBROADCASTD X14, Y14
	MOVL shift+60(FP), AX
	VMOVD AX, X15
	MOVL $255, AX
	VMOVD AX, X13
	VPBROADCASTD X13, Y13
	MOVQ alpha+64(FP), AX
	VMOVQ AX, X12
khloop:
	VPXOR Y0, Y0, Y0
	XORQ BX, BX
	MOVQ R10, R12
khbox:
	MOVQ (R8)(BX*8), R13
	MOVQ (R9)(BX*8), AX
	VMOVDQU (SI)(R13*1), Y1
	VPSUBD (SI)(AX*1), Y1, Y1
	VPMULLD (R12), Y1, Y1
	VPADDD Y1, Y0, Y0
	ADDQ $32, R12
	INCQ BX
	CMPQ BX, R11
	JLT khbox
	VPADDD Y14, Y0, Y0
	VPSRLD X15, Y0, Y0
	VPMINUD Y13, Y0, Y0
	VPACKUSDW Y0, Y0, Y0
	VPACKUSWB Y0, Y0, Y0
	VEXTRACTI128 $1, Y0, X1
	VPUNPCKLDQ X1, X0, X0
	VPOR X12, X0, X0
	VMOVQ X0, (DI)
	ADDQ $8, DI
	ADDQ $32, SI
	SUBQ $8, CX
	JNZ khloop
	VZEROUPPER
	RET

// func kernelVSIMD(out *byte, sum *uint32, in, gone **byte, wt *uint32, nb, n, off int, half, shift uint32, alpha uint64)
TEXT ·kernelVSIMD(SB), NOSPLIT, $0-80
	MOVQ out+0(FP), DI
	MOVQ sum+8(FP), SI
	MOVQ in+16(FP), R8
	MOVQ gone+24(FP), R9
	MOVQ wt+32(FP), R10
	MOVQ nb+40(FP), R11
	MOVQ n+48(FP), CX
	MOVQ off+56(FP), DX
	MOVL half+64(FP), AX
	VMOVD AX, X14
	VPBROADCASTD X14, Y14
	MOVL shift+68(FP), AX
	VMOVD AX, X15
	MOVL $255, AX
	VMOVD AX, X13
	VPBROADCASTD X13, Y13
	MOVQ alpha+72(FP), AX
	VMOVQ AX, X12
kvloop:
	// The row out of the sum, then the sum moved down a row.
	VMOVDQU (SI), Y0
	VPADDD Y14, Y0, Y2
	VPSRLD X15, Y2, Y2
	VPMINUD Y13, Y2, Y2
	VPACKUSDW Y2, Y2, Y2
	VPACKUSWB Y2, Y2, Y2
	VEXTRACTI128 $1, Y2, X3
	VPUNPCKLDQ X3, X2, X2
	VPOR X12, X2, X2
	VMOVQ X2, (DI)
	XORQ BX, BX
	MOVQ R10, R12
kvbox:
	MOVQ (R8)(BX*8), R13
	MOVQ (R9)(BX*8), AX
	VPMOVZXBD (R13)(DX*1), Y1
	VPMOVZXBD (AX)(DX*1), Y3
	VPSUBD Y3, Y1, Y1
	VPMULLD (R12), Y1, Y1
	VPADDD Y1, Y0, Y0
	ADDQ $32, R12
	INCQ BX
	CMPQ BX, R11
	JLT kvbox
	VMOVDQU Y0, (SI)
	ADDQ $8, DI
	ADDQ $32, SI
	ADDQ $8, DX
	SUBQ $8, CX
	JNZ kvloop
	VZEROUPPER
	RET
