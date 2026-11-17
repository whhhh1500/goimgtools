// The passes of the default scaler (resize.go's feed) with AVX2: the four
// channels of a pixel in the lanes of one register, each multiplied and
// then added (never fused) in the order of the Go code, so every sum is
// the same to the bit.

//go:build amd64 && !purego

#include "textflag.h"

DATA scOne<>+0(SB)/8, $0x3ff0000000000000
GLOBL scOne<>(SB), RODATA|NOPTR, $8

// func hpassSIMD(tmp, row *[4]float64, s0 int, sources *source, contribs *contrib, n int, ca bool)
TEXT ·hpassSIMD(SB), NOSPLIT, $0-49
	MOVQ tmp+0(FP), DI
	MOVQ row+8(FP), SI
	MOVQ s0+16(FP), AX
	SHLQ $5, AX
	SUBQ AX, SI
	MOVQ sources+24(FP), R8
	MOVQ contribs+32(FP), R9
	MOVQ n+40(FP), CX
	MOVBQZX ca+48(FP), DX
	VBROADCASTSD scOne<>(SB), Y15
	TESTQ CX, CX
	JLE hdone
hloop:
	MOVLQSX 0(R8), R10
	MOVLQSX 4(R8), R11
	VXORPD Y0, Y0, Y0
	CMPQ R10, R11
	JGE hsum
	MOVQ R10, R12
	SHLQ $4, R12
	ADDQ R9, R12
htap:
	MOVLQSX 0(R12), R13
	SHLQ $5, R13
	VBROADCASTSD 8(R12), Y1
	VMULPD (SI)(R13*1), Y1, Y2
	VADDPD Y2, Y0, Y0
	ADDQ $16, R12
	INCQ R10
	CMPQ R10, R11
	JLT htap
hsum:
	VBROADCASTSD 16(R8), Y1
	VMULPD Y1, Y0, Y0
	TESTQ DX, DX
	JZ hstore
	VBLENDPD $8, Y15, Y0, Y0
hstore:
	VMOVUPD Y0, (DI)
	ADDQ $32, DI
	ADDQ $24, R8
	DECQ CX
	JNZ hloop
hdone:
	VZEROUPPER
	RET

// func vpassSIMD(acc, tmp *[4]float64, n int, w float64)
TEXT ·vpassSIMD(SB), NOSPLIT, $0-32
	MOVQ acc+0(FP), DI
	MOVQ tmp+8(FP), SI
	MOVQ n+16(FP), CX
	VBROADCASTSD w+24(FP), Y1
	CMPQ CX, $2
	JLT vone
vtwo:
	VMULPD (SI), Y1, Y2
	VMULPD 32(SI), Y1, Y3
	VADDPD (DI), Y2, Y2
	VADDPD 32(DI), Y3, Y3
	VMOVUPD Y2, (DI)
	VMOVUPD Y3, 32(DI)
	ADDQ $64, SI
	ADDQ $64, DI
	SUBQ $2, CX
	CMPQ CX, $2
	JGE vtwo
vone:
	TESTQ CX, CX
	JLE vdone
	VMULPD (SI), Y1, Y2
	VADDPD (DI), Y2, Y2
	VMOVUPD Y2, (DI)
vdone:
	VZEROUPPER
	RET

// func vpassGraySIMD(acc, tmp *float64, n int, w float64)
TEXT ·vpassGraySIMD(SB), NOSPLIT, $0-32
	MOVQ acc+0(FP), DI
	MOVQ tmp+8(FP), SI
	MOVQ n+16(FP), CX
	VBROADCASTSD w+24(FP), Y1
	CMPQ CX, $4
	JLT gtail
gfour:
	VMULPD (SI), Y1, Y2
	VADDPD (DI), Y2, Y2
	VMOVUPD Y2, (DI)
	ADDQ $32, SI
	ADDQ $32, DI
	SUBQ $4, CX
	CMPQ CX, $4
	JGE gfour
gtail:
	TESTQ CX, CX
	JLE gdone
	VMULSD (SI), X1, X2
	VADDSD (DI), X2, X2
	VMOVSD X2, (DI)
	ADDQ $8, SI
	ADDQ $8, DI
	DECQ CX
	JMP gtail
gdone:
	VZEROUPPER
	RET

// func rgbaRowSIMD(out *[4]float64, pix *byte, n int)
TEXT ·rgbaRowSIMD(SB), NOSPLIT, $0-24
	MOVQ out+0(FP), DI
	MOVQ pix+8(FP), SI
	MOVQ n+16(FP), CX
	CMPQ CX, $2
	JLT rone
rtwo:
	VPMOVZXBD (SI), Y0
	VPSLLD $8, Y0, Y1
	VPOR Y1, Y0, Y0
	VCVTDQ2PD X0, Y2
	VEXTRACTI128 $1, Y0, X3
	VCVTDQ2PD X3, Y3
	VMOVUPD Y2, (DI)
	VMOVUPD Y3, 32(DI)
	ADDQ $8, SI
	ADDQ $64, DI
	SUBQ $2, CX
	CMPQ CX, $2
	JGE rtwo
rone:
	TESTQ CX, CX
	JLE rdone
	VPMOVZXBD (SI), X0
	VPSLLD $8, X0, X1
	VPOR X1, X0, X0
	VCVTDQ2PD X0, Y2
	VMOVUPD Y2, (DI)
rdone:
	VZEROUPPER
	RET
