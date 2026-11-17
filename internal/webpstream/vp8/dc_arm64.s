// inverseDCT4DCOnly and inverseDCT8DCOnly (idct.go) with NEON: the DC
// added in 16-bit lanes and saturated to 8 bits, which clips as clip8
// does: every byte that of the Go code.

//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// func dcAddAsm(dst *byte, stride, dc int)
TEXT ·dcAddAsm(SB), NOSPLIT, $0-24
	MOVD dst+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD dc+16(FP), R2
	VDUP R2, V2.H8
	MOVD R0, R3
	VLD1 (R3), V0.S[0]
	ADD R1, R3, R3
	VLD1 (R3), V0.S[1]
	ADD R1, R3, R3
	VLD1 (R3), V0.S[2]
	ADD R1, R3, R3
	VLD1 (R3), V0.S[3]
	VUXTL V0.B8, V1.H8
	VUXTL2 V0.B16, V3.H8
	VADD V2.H8, V1.H8, V1.H8
	VADD V2.H8, V3.H8, V3.H8
	VSQXTUN V1.H8, V4.B8
	VSQXTUN2 V3.H8, V4.B16
	VST1 V4.S[0], (R0)
	ADD R1, R0, R0
	VST1 V4.S[1], (R0)
	ADD R1, R0, R0
	VST1 V4.S[2], (R0)
	ADD R1, R0, R0
	VST1 V4.S[3], (R0)
	RET

// func dcAdd8Asm(dst *byte, stride, d0, d1, d2, d3 int)
//
// The four 4x4 blocks of an 8x8 one: d0 top left, d1 top right, d2 bottom
// left, d3 bottom right.
TEXT ·dcAdd8Asm(SB), NOSPLIT, $0-48
	MOVD dst+0(FP), R0
	MOVD stride+8(FP), R1
	MOVD d0+16(FP), R2
	VDUP R2, V4.H8
	MOVD d1+24(FP), R2
	VDUP R2, V5.H8
	VTRN1 V5.D2, V4.D2, V4.D2
	MOVD d2+32(FP), R2
	VDUP R2, V5.H8
	MOVD d3+40(FP), R2
	VDUP R2, V6.H8
	VTRN1 V6.D2, V5.D2, V5.D2
	MOVD $4, R4
loop:
	MOVD R0, R3
	VLD1 (R3), V0.D[0]
	ADD R1, R3, R3
	VLD1 (R3), V0.D[1]
	VUXTL V0.B8, V1.H8
	VUXTL2 V0.B16, V3.H8
	VADD V4.H8, V1.H8, V1.H8
	VADD V4.H8, V3.H8, V3.H8
	VSQXTUN V1.H8, V6.B8
	VSQXTUN2 V3.H8, V6.B16
	VST1 V6.D[0], (R0)
	ADD R1, R0, R0
	VST1 V6.D[1], (R0)
	ADD R1, R0, R0
	SUB $1, R4, R4
	CMP $2, R4
	BNE same
	VMOV V5.B16, V4.B16
same:
	CBNZ R4, loop
	RET
