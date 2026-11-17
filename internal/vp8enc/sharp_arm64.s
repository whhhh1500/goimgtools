// The plain loops of sharp YUV (sharp.go) with NEON, every sample that of
// the Go code: the 9-3-3-1 upsampling of filterRow in 32-bit lanes, and
// the corrections of W (clipped) and chroma (wrapping as int16 does) in
// 16-bit lanes, where their sums fit.

//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// func filterPairsSIMD(a, b *int16, wy, out *uint16, n int)
TEXT ·filterPairsSIMD(SB), NOSPLIT, $0-40
	MOVD a+0(FP), R0
	MOVD b+8(FP), R1
	MOVD wy+16(FP), R2
	MOVD out+24(FP), R3
	MOVD n+32(FP), R4
	MOVW $1023, R5
	VDUP R5, V30.S4
	MOVW $8, R5
	VDUP R5, V29.S4
	VEOR V31.B16, V31.B16, V31.B16
	LSR $2, R4, R4
	CBZ R4, fdone
floop:
	// Four pairs: a0, a1, b0, b1 widened to 32 bits.
	VLD1 (R0), [V0.H4]
	ADD $2, R0, R6
	VLD1 (R6), [V1.H4]
	VLD1 (R1), [V2.H4]
	ADD $2, R1, R6
	VLD1 (R6), [V3.H4]
	VSXTL V0.H4, V0.S4
	VSXTL V1.H4, V1.S4
	VSXTL V2.H4, V2.S4
	VSXTL V3.H4, V3.S4
	// v0 = (9*a0 + 3*(a1+b0) + b1 + 8) >> 4
	VSHL $3, V0.S4, V4.S4
	VADD V0.S4, V4.S4, V4.S4
	VADD V2.S4, V1.S4, V5.S4
	VSHL $1, V5.S4, V6.S4
	VADD V6.S4, V5.S4, V5.S4
	VADD V5.S4, V4.S4, V4.S4
	VADD V3.S4, V4.S4, V4.S4
	VADD V29.S4, V4.S4, V4.S4
	VSSHR $4, V4.S4, V4.S4
	// v1 = (9*a1 + 3*(a0+b1) + b0 + 8) >> 4
	VSHL $3, V1.S4, V5.S4
	VADD V1.S4, V5.S4, V5.S4
	VADD V3.S4, V0.S4, V6.S4
	VSHL $1, V6.S4, V7.S4
	VADD V7.S4, V6.S4, V6.S4
	VADD V6.S4, V5.S4, V5.S4
	VADD V2.S4, V5.S4, V5.S4
	VADD V29.S4, V5.S4, V5.S4
	VSSHR $4, V5.S4, V5.S4
	VZIP1 V5.S4, V4.S4, V6.S4
	VZIP2 V5.S4, V4.S4, V7.S4
	VLD1 (R2), [V8.H8]
	VUXTL V8.H4, V9.S4
	VUXTL2 V8.H8, V10.S4
	VADD V9.S4, V6.S4, V6.S4
	VADD V10.S4, V7.S4, V7.S4
	VSMAX V31.S4, V6.S4, V6.S4
	VSMAX V31.S4, V7.S4, V7.S4
	VSMIN V30.S4, V6.S4, V6.S4
	VSMIN V30.S4, V7.S4, V7.S4
	VXTN V6.S4, V11.H4
	VXTN2 V7.S4, V11.H8
	VST1 [V11.H8], (R3)
	ADD $8, R0, R0
	ADD $8, R1, R1
	ADD $16, R2, R2
	ADD $16, R3, R3
	SUB $1, R4, R4
	CBNZ R4, floop
fdone:
	RET

// func updateBySIMD(by, ty, y *uint16, n int)
TEXT ·updateBySIMD(SB), NOSPLIT, $0-32
	MOVD by+0(FP), R0
	MOVD ty+8(FP), R1
	MOVD y+16(FP), R2
	MOVD n+24(FP), R3
	MOVW $1023, R5
	VDUP R5, V30.H8
	VEOR V31.B16, V31.B16, V31.B16
	LSR $3, R3, R3
	CBZ R3, bdone
bloop:
	VLD1 (R0), [V0.H8]
	VLD1.P 16(R1), [V1.H8]
	VLD1.P 16(R2), [V2.H8]
	VADD V1.H8, V0.H8, V0.H8
	VSUB V2.H8, V0.H8, V0.H8
	VSMAX V31.H8, V0.H8, V0.H8
	VSMIN V30.H8, V0.H8, V0.H8
	VST1.P [V0.H8], 16(R0)
	SUB $1, R3, R3
	CBNZ R3, bloop
bdone:
	RET

// func updateUVSIMD(cur, tuv, uv *int16, n int)
TEXT ·updateUVSIMD(SB), NOSPLIT, $0-32
	MOVD cur+0(FP), R0
	MOVD tuv+8(FP), R1
	MOVD uv+16(FP), R2
	MOVD n+24(FP), R3
	LSR $3, R3, R3
	CBZ R3, udone
uloop:
	VLD1 (R0), [V0.H8]
	VLD1.P 16(R1), [V1.H8]
	VLD1.P 16(R2), [V2.H8]
	VSUB V2.H8, V1.H8, V1.H8
	VADD V1.H8, V0.H8, V0.H8
	VST1.P [V0.H8], 16(R0)
	SUB $1, R3, R3
	CBNZ R3, uloop
udone:
	RET
