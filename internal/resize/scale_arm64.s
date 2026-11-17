// The passes of the default scaler (resize.go's feed) with NEON: the four
// channels of a pixel in the lanes of two registers, each multiplied and
// then added (never fused) in the order of the Go code, so every sum is
// the same to the bit.

//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// func hpassSIMD(tmp, row *[4]float64, s0 int, sources *source, contribs *contrib, n int, ca bool)
TEXT ·hpassSIMD(SB), NOSPLIT, $0-49
	MOVD tmp+0(FP), R0
	MOVD row+8(FP), R1
	MOVD s0+16(FP), R2
	MOVD sources+24(FP), R3
	MOVD contribs+32(FP), R4
	MOVD n+40(FP), R5
	MOVBU ca+48(FP), R6
	SUB R2<<5, R1, R1
	FMOVD $1.0, F31
	CBZ R5, hdone
hloop:
	MOVW 0(R3), R7
	MOVW 4(R3), R8
	VEOR V0.B16, V0.B16, V0.B16
	VEOR V1.B16, V1.B16, V1.B16
	CMP R8, R7
	BGE hsum
	ADD R7<<4, R4, R9
htap:
	MOVW 0(R9), R10
	ADD R10<<5, R1, R11
	ADD $8, R9, R12
	VLD1R (R12), [V2.D2]
	VLD1 (R11), [V4.D2, V5.D2]
	VFMUL V2.D2, V4.D2, V4.D2
	VFMUL V2.D2, V5.D2, V5.D2
	VFADD V4.D2, V0.D2, V0.D2
	VFADD V5.D2, V1.D2, V1.D2
	ADD $16, R9, R9
	ADD $1, R7, R7
	CMP R8, R7
	BLT htap
hsum:
	ADD $16, R3, R12
	VLD1R (R12), [V2.D2]
	VFMUL V2.D2, V0.D2, V0.D2
	VFMUL V2.D2, V1.D2, V1.D2
	CBZ R6, hstore
	VMOV V31.D[0], V1.D[1]
hstore:
	VST1.P [V0.D2, V1.D2], 32(R0)
	ADD $24, R3, R3
	SUB $1, R5, R5
	CBNZ R5, hloop
hdone:
	RET

// func vpassSIMD(acc, tmp *[4]float64, n int, w float64)
TEXT ·vpassSIMD(SB), NOSPLIT, $0-32
	MOVD acc+0(FP), R0
	MOVD tmp+8(FP), R1
	MOVD n+16(FP), R2
	FMOVD w+24(FP), F7
	VDUP V7.D[0], V7.D2
	CBZ R2, vdone
vloop:
	VLD1.P 32(R1), [V0.D2, V1.D2]
	VLD1 (R0), [V2.D2, V3.D2]
	VFMUL V7.D2, V0.D2, V0.D2
	VFMUL V7.D2, V1.D2, V1.D2
	VFADD V0.D2, V2.D2, V2.D2
	VFADD V1.D2, V3.D2, V3.D2
	VST1.P [V2.D2, V3.D2], 32(R0)
	SUB $1, R2, R2
	CBNZ R2, vloop
vdone:
	RET

// func vpassGraySIMD(acc, tmp *float64, n int, w float64)
TEXT ·vpassGraySIMD(SB), NOSPLIT, $0-32
	MOVD acc+0(FP), R0
	MOVD tmp+8(FP), R1
	MOVD n+16(FP), R2
	FMOVD w+24(FP), F7
	VDUP V7.D[0], V7.D2
	CMP $2, R2
	BLT gtail
gtwo:
	VLD1.P 16(R1), [V0.D2]
	VLD1 (R0), [V2.D2]
	VFMUL V7.D2, V0.D2, V0.D2
	VFADD V0.D2, V2.D2, V2.D2
	VST1.P [V2.D2], 16(R0)
	SUB $2, R2, R2
	CMP $2, R2
	BGE gtwo
gtail:
	CBZ R2, gdone
	FMOVD (R1), F0
	FMOVD (R0), F2
	FMULD F7, F0, F0
	FADDD F0, F2, F2
	FMOVD F2, (R0)
gdone:
	RET

// func rgbaRowSIMD(out *[4]float64, pix *byte, n int)
TEXT ·rgbaRowSIMD(SB), NOSPLIT, $0-24
	MOVD out+0(FP), R0
	MOVD pix+8(FP), R1
	MOVD n+16(FP), R2
	CMP $2, R2
	BLT rone
rtwo:
	VLD1.P 8(R1), [V0.B8]
	VUXTL V0.B8, V0.H8
	VSHL $8, V0.H8, V1.H8
	VORR V1.B16, V0.B16, V0.B16
	VUXTL V0.H4, V2.S4
	VUXTL2 V0.H8, V3.S4
	VUXTL V2.S2, V4.D2
	VUXTL2 V2.S4, V5.D2
	VUXTL V3.S2, V6.D2
	VUXTL2 V3.S4, V7.D2
	VUCVTF V4.D2, V4.D2
	VUCVTF V5.D2, V5.D2
	VUCVTF V6.D2, V6.D2
	VUCVTF V7.D2, V7.D2
	VST1.P [V4.D2, V5.D2, V6.D2, V7.D2], 64(R0)
	SUB $2, R2, R2
	CMP $2, R2
	BGE rtwo
rone:
	CBZ R2, rdone
	VLD1 (R1), V0.S[0]
	VUXTL V0.B8, V0.H8
	VSHL $8, V0.H8, V1.H8
	VORR V1.B16, V0.B16, V0.B16
	VUXTL V0.H4, V2.S4
	VUXTL V2.S2, V4.D2
	VUXTL2 V2.S4, V5.D2
	VUCVTF V4.D2, V4.D2
	VUCVTF V5.D2, V5.D2
	VST1 [V4.D2, V5.D2], (R0)
rdone:
	RET
