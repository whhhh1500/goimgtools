// The box blur passes of blur.go (Pillow's BoxBlur) with NEON: across a
// row the four channels of a pixel in the 32-bit lanes of one register,
// down the rows eight columns at a time; the same uint32 arithmetic as
// the Go code, so every byte is the same.

//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// func hboxRowSIMD(dst, src *byte, w, ri int, ww, fw uint32, keepAlpha bool)
TEXT ·hboxRowSIMD(SB), NOSPLIT, $0-41
	MOVD dst+0(FP), R0
	MOVD src+8(FP), R1
	MOVD w+16(FP), R2
	MOVD ri+24(FP), R3
	MOVWU ww+32(FP), R4
	VDUP R4, V13.S4
	MOVWU fw+36(FP), R4
	VDUP R4, V14.S4
	MOVW $0x800000, R4
	VDUP R4, V15.S4
	MOVBU keepAlpha+40(FP), R5
	SUB $1, R2, R6 // last
	// The sum of the window around pixel 0, the ends repeated.
	VEOR V0.B16, V0.B16, V0.B16
	NEG R3, R7
hinit:
	CMP $0, R7
	CSEL LT, ZR, R7, R8
	CMP R6, R8
	CSEL GT, R6, R8, R8
	ADD R8<<2, R1, R9
	VLD1 (R9), V1.S[0]
	VUXTL V1.B8, V1.H8
	VUXTL V1.H4, V1.S4
	VADD V1.S4, V0.S4, V0.S4
	ADD $1, R7, R7
	CMP R3, R7
	BLE hinit
	MOVD $0, R7 // x
hloop:
	// left = x-ri-1, right = x+ri+1, leaving = x-ri, each clamped.
	SUB R3, R7, R8
	SUB $1, R8, R9
	CMP $0, R8
	CSEL LT, ZR, R8, R8
	CMP $0, R9
	CSEL LT, ZR, R9, R9
	ADD R3, R7, R10
	ADD $1, R10, R10
	CMP R6, R10
	CSEL GT, R6, R10, R10
	ADD R9<<2, R1, R11
	VLD1 (R11), V1.S[0]
	ADD R10<<2, R1, R11
	VLD1 (R11), V2.S[0]
	ADD R8<<2, R1, R11
	VLD1 (R11), V3.S[0]
	VUXTL V1.B8, V1.H8
	VUXTL V1.H4, V1.S4
	VUXTL V2.B8, V2.H8
	VUXTL V2.H4, V2.S4
	VUXTL V3.B8, V3.H8
	VUXTL V3.H4, V3.S4
	VMUL V13.S4, V0.S4, V4.S4
	VADD V2.S4, V1.S4, V5.S4
	VMUL V14.S4, V5.S4, V5.S4
	VADD V5.S4, V4.S4, V4.S4
	VADD V15.S4, V4.S4, V4.S4
	VUSHR $24, V4.S4, V4.S4
	VXTN V4.S4, V4.H4
	VXTN V4.H8, V4.B8
	ADD R7<<2, R0, R11
	CBZ R5, hstore
	MOVBU 3(R11), R12
	VMOV R12, V4.B[3]
hstore:
	VST1 V4.S[0], (R11)
	VADD V2.S4, V0.S4, V0.S4
	VSUB V3.S4, V0.S4, V0.S4
	ADD $1, R7, R7
	CMP R2, R7
	BLT hloop
	RET

// func vboxRowSIMD(out, a, b, c *byte, sum *uint32, n int, ww, fw uint32)
TEXT ·vboxRowSIMD(SB), NOSPLIT, $0-56
	MOVD out+0(FP), R0
	MOVD a+8(FP), R1
	MOVD b+16(FP), R2
	MOVD c+24(FP), R3
	MOVD sum+32(FP), R4
	MOVD n+40(FP), R5
	MOVWU ww+48(FP), R6
	VDUP R6, V13.S4
	MOVWU fw+52(FP), R6
	VDUP R6, V14.S4
	MOVW $0x800000, R6
	VDUP R6, V15.S4
	LSR $3, R5, R5
	CBZ R5, vdone
vloop:
	VLD1 (R4), [V20.S4, V21.S4]
	VLD1.P 8(R1), [V1.B8]
	VLD1.P 8(R2), [V2.B8]
	VLD1.P 8(R3), [V3.B8]
	VUXTL V1.B8, V1.H8
	VUXTL V2.B8, V2.H8
	VUXTL V3.B8, V3.H8
	VUXTL2 V1.H8, V9.S4
	VUXTL V1.H4, V1.S4
	VUXTL2 V2.H8, V10.S4
	VUXTL V2.H4, V2.S4
	VUXTL2 V3.H8, V11.S4
	VUXTL V3.H4, V3.S4
	VMUL V13.S4, V20.S4, V4.S4
	VMUL V13.S4, V21.S4, V12.S4
	VADD V2.S4, V1.S4, V5.S4
	VADD V10.S4, V9.S4, V6.S4
	VMUL V14.S4, V5.S4, V5.S4
	VMUL V14.S4, V6.S4, V6.S4
	VADD V5.S4, V4.S4, V4.S4
	VADD V6.S4, V12.S4, V12.S4
	VADD V15.S4, V4.S4, V4.S4
	VADD V15.S4, V12.S4, V12.S4
	VUSHR $24, V4.S4, V4.S4
	VUSHR $24, V12.S4, V12.S4
	VXTN V4.S4, V4.H4
	VXTN2 V12.S4, V4.H8
	VXTN V4.H8, V4.B8
	VST1.P [V4.B8], 8(R0)
	VADD V2.S4, V20.S4, V20.S4
	VADD V10.S4, V21.S4, V21.S4
	VSUB V3.S4, V20.S4, V20.S4
	VSUB V11.S4, V21.S4, V21.S4
	VST1.P [V20.S4, V21.S4], 32(R4)
	SUB $1, R5, R5
	CBNZ R5, vloop
vdone:
	RET

// SORT3 sorts the bytes of A, B and C lane by lane into LO, MID, HI
// (T a temporary).
#define SORT3(A, B, C, LO, MID, HI, T) \
	VUMIN B, A, T; \
	VUMAX B, A, HI; \
	VUMIN C, T, LO; \
	VUMIN C, HI, MID; \
	VUMAX C, HI, HI; \
	VUMAX T, MID, MID

// func median3SIMD(dst, r0, r1, r2 *byte, n int)
TEXT ·median3SIMD(SB), NOSPLIT, $0-40
	MOVD dst+0(FP), R0
	MOVD r0+8(FP), R1
	MOVD r1+16(FP), R2
	MOVD r2+24(FP), R3
	MOVD n+32(FP), R4
	LSR $2, R4, R4
	CBZ R4, mdone
mloop:
	VLD1 (R1), [V0.B16]
	VLD1 (R2), [V1.B16]
	VLD1 (R3), [V2.B16]
	SORT3(V0.B16, V1.B16, V2.B16, V3.B16, V4.B16, V5.B16, V31.B16)
	ADD $4, R1, R5
	ADD $4, R2, R6
	ADD $4, R3, R7
	VLD1 (R5), [V0.B16]
	VLD1 (R6), [V1.B16]
	VLD1 (R7), [V2.B16]
	SORT3(V0.B16, V1.B16, V2.B16, V6.B16, V7.B16, V8.B16, V31.B16)
	ADD $8, R1, R5
	ADD $8, R2, R6
	ADD $8, R3, R7
	VLD1 (R5), [V0.B16]
	VLD1 (R6), [V1.B16]
	VLD1 (R7), [V2.B16]
	SORT3(V0.B16, V1.B16, V2.B16, V9.B16, V10.B16, V11.B16, V31.B16)
	VUMAX V6.B16, V3.B16, V3.B16
	VUMAX V9.B16, V3.B16, V3.B16
	VUMIN V8.B16, V5.B16, V5.B16
	VUMIN V11.B16, V5.B16, V5.B16
	VUMIN V7.B16, V4.B16, V12.B16
	VUMAX V7.B16, V4.B16, V13.B16
	VUMIN V10.B16, V13.B16, V13.B16
	VUMAX V12.B16, V13.B16, V4.B16
	VUMIN V4.B16, V3.B16, V12.B16
	VUMAX V4.B16, V3.B16, V13.B16
	VUMIN V5.B16, V13.B16, V13.B16
	VUMAX V12.B16, V13.B16, V0.B16
	VST1.P [V0.B16], 16(R0)
	ADD $16, R1, R1
	ADD $16, R2, R2
	ADD $16, R3, R3
	SUB $1, R4, R4
	CBNZ R4, mloop
mdone:
	RET
