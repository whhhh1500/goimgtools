// The 4x4 transforms, quantizer and squared error of dct.go and frame.go
// with NEON, every result that of the Go code. The forward transform keeps
// 16-bit lanes where the Go code's values fit them (differences of 8-bit
// samples), with 32-bit products; the inverse works in 32-bit lanes, as
// the Go code does, wrapping where it wraps.

//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// FDCT is the forward transform of the 4x4 difference of the blocks at
// R0 (stride R1) and R2 (stride R3): coefficients 0-7 in V0, 8-15 in
// V1. The differences in 16-bit lanes, columns 0 | 1 and 2 | 3 by two
// unzips, pass 1 (rows), the rows of tmp by two more, pass 2
// (columns), with 32-bit products; (a3 != 0) added as 1 - (a3 == 0).
#define FDCT \
	VLD1 (R0), V0.S[0]; \
	ADD R1, R0, R0; \
	VLD1 (R0), V0.S[1]; \
	ADD R1, R0, R0; \
	VLD1 (R0), V0.S[2]; \
	ADD R1, R0, R0; \
	VLD1 (R0), V0.S[3]; \
	VLD1 (R2), V1.S[0]; \
	ADD R3, R2, R2; \
	VLD1 (R2), V1.S[1]; \
	ADD R3, R2, R2; \
	VLD1 (R2), V1.S[2]; \
	ADD R3, R2, R2; \
	VLD1 (R2), V1.S[3]; \
	MOVD $5352, R5; \
	VDUP R5, V20.H8; \
	MOVD $2217, R5; \
	VDUP R5, V21.H8; \
	MOVD $-5352, R5; \
	VDUP R5, V22.H8; \
	MOVD $1812, R5; \
	VDUP R5, V23.S4; \
	MOVD $937, R5; \
	VDUP R5, V24.S4; \
	MOVD $77536, R5; \
	VDUP R5, V25.S4; \
	MOVD $51000, R5; \
	VDUP R5, V26.S4; \
	MOVD $7, R5; \
	VDUP R5, V27.H8; \
	VEOR V31.B16, V31.B16, V31.B16; \
	VUXTL V0.B8, V2.H8; \
	VUXTL2 V0.B16, V3.H8; \
	VUXTL V1.B8, V4.H8; \
	VUXTL2 V1.B16, V5.H8; \
	VSUB V4.H8, V2.H8, V2.H8; \
	VSUB V5.H8, V3.H8, V3.H8; \
	VUZP1 V3.H8, V2.H8, V4.H8; \
	VUZP2 V3.H8, V2.H8, V5.H8; \
	VUZP1 V5.H8, V4.H8, V6.H8; \
	VUZP2 V5.H8, V4.H8, V7.H8; \
	VEXT $8, V7.B16, V7.B16, V8.B16; \
	VADD V8.H8, V6.H8, V9.H8; \
	VSUB V8.H8, V6.H8, V10.H8; \
	VEXT $8, V9.B16, V9.B16, V11.B16; \
	VADD V11.H8, V9.H8, V12.H8; \
	VSUB V11.H8, V9.H8, V13.H8; \
	VSHL $3, V12.H8, V12.H8; \
	VSHL $3, V13.H8, V13.H8; \
	VSMULL V20.H4, V10.H4, V14.S4; \
	VSMLAL2 V21.H8, V10.H8, V14.S4; \
	VSMULL V21.H4, V10.H4, V15.S4; \
	VSMLAL2 V22.H8, V10.H8, V15.S4; \
	VADD V23.S4, V14.S4, V14.S4; \
	VADD V24.S4, V15.S4, V15.S4; \
	VSSHR $9, V14.S4, V14.S4; \
	VSSHR $9, V15.S4, V15.S4; \
	VXTN V14.S4, V16.H4; \
	VXTN V15.S4, V17.H4; \
	VZIP1 V16.D2, V12.D2, V18.D2; \
	VZIP1 V17.D2, V13.D2, V19.D2; \
	VUZP1 V19.H8, V18.H8, V4.H8; \
	VUZP2 V19.H8, V18.H8, V5.H8; \
	VUZP1 V5.H8, V4.H8, V6.H8; \
	VUZP2 V5.H8, V4.H8, V7.H8; \
	VEXT $8, V7.B16, V7.B16, V8.B16; \
	VADD V8.H8, V6.H8, V9.H8; \
	VSUB V8.H8, V6.H8, V10.H8; \
	VEXT $8, V9.B16, V9.B16, V11.B16; \
	VADD V27.H8, V9.H8, V9.H8; \
	VADD V11.H8, V9.H8, V12.H8; \
	VSUB V11.H8, V9.H8, V13.H8; \
	VSSHR $4, V12.H8, V12.H8; \
	VSSHR $4, V13.H8, V13.H8; \
	VSMULL V20.H4, V10.H4, V14.S4; \
	VSMLAL2 V21.H8, V10.H8, V14.S4; \
	VSMULL V21.H4, V10.H4, V15.S4; \
	VSMLAL2 V22.H8, V10.H8, V15.S4; \
	VADD V25.S4, V14.S4, V14.S4; \
	VADD V26.S4, V15.S4, V15.S4; \
	VSSHR $16, V14.S4, V14.S4; \
	VSSHR $16, V15.S4, V15.S4; \
	VXTN V14.S4, V16.H4; \
	VXTN V15.S4, V17.H4; \
	VCMEQ V31.H4, V10.H4, V28.H4; \
	VADD V28.H4, V16.H4, V16.H4; \
	VZIP1 V16.D2, V12.D2, V0.D2; \
	VZIP1 V17.D2, V13.D2, V1.D2

// func fdctSIMD(src *byte, sStride int, ref *byte, rStride int, out *[16]int16)
TEXT ·fdctSIMD(SB), NOSPLIT, $0-40
	MOVD src+0(FP), R0
	MOVD sStride+8(FP), R1
	MOVD ref+16(FP), R2
	MOVD rStride+24(FP), R3
	MOVD out+32(FP), R4
	FDCT
	VST1 [V0.H8, V1.H8], (R4)
	RET

DATA histW<>+0(SB)/8, $0x8040201008040201
DATA histW<>+8(SB)/8, $0x8040201008040201
GLOBL histW<>(SB), RODATA|NOPTR, $16

// func histBinsSIMD(src *byte, sStride int, ref *byte, rStride int, bins *[16]uint8) uint32
TEXT ·histBinsSIMD(SB), NOSPLIT, $0-44
	MOVD src+0(FP), R0
	MOVD sStride+8(FP), R1
	MOVD ref+16(FP), R2
	MOVD rStride+24(FP), R3
	MOVD bins+32(FP), R4
	FDCT
	VABS V0.H8, V0.H8
	VABS V1.H8, V1.H8
	VUSHR $3, V0.H8, V0.H8
	VUSHR $3, V1.H8, V1.H8
	MOVD $31, R5
	VDUP R5, V2.H8
	VSMIN V2.H8, V0.H8, V0.H8
	VSMIN V2.H8, V1.H8, V1.H8
	VXTN V0.H8, V3.B8
	VXTN2 V1.H8, V3.B16
	VST1 [V3.B16], (R4)
	// Bit k for each bin k that is 0, then turned over.
	VEOR V4.B16, V4.B16, V4.B16
	VCMEQ V4.B16, V3.B16, V4.B16
	MOVD $histW<>(SB), R6
	VLD1 (R6), [V5.B16]
	VAND V5.B16, V4.B16, V4.B16
	VUADDLV V4.B8, V6
	VEXT $8, V4.B16, V4.B16, V7.B16
	VUADDLV V7.B8, V8
	VMOV V6.H[0], R6
	VMOV V8.H[0], R7
	ORR R7<<8, R6, R6
	EOR $0xffff, R6, R6
	MOVW R6, ret+40(FP)
	RET

// func idctAddSIMD(coef *[16]int16, ref *byte, rStride int, dst *byte, dStride int)
TEXT ·idctAddSIMD(SB), NOSPLIT, $0-40
	MOVD coef+0(FP), R0
	MOVD ref+8(FP), R1
	MOVD rStride+16(FP), R2
	MOVD dst+24(FP), R3
	MOVD dStride+32(FP), R4
	MOVD $85627, R5
	VDUP R5, V28.S4 // c1
	MOVD $35468, R5
	VDUP R5, V29.S4 // c2
	MOVD $4, R5
	VDUP R5, V27.S4
	VLD1 (R0), [V0.H8, V1.H8]
	// Pass 1: rows of coefficients, lanes the columns i.
	VSXTL V0.H4, V2.S4  // c0
	VSXTL2 V0.H8, V3.S4 // c4
	VSXTL V1.H4, V4.S4  // c8
	VSXTL2 V1.H8, V5.S4 // c12
	VADD V4.S4, V2.S4, V6.S4 // a
	VSUB V4.S4, V2.S4, V7.S4 // b
	VMUL V29.S4, V3.S4, V8.S4
	VMUL V28.S4, V5.S4, V9.S4
	VSSHR $16, V8.S4, V8.S4
	VSSHR $16, V9.S4, V9.S4
	VSUB V9.S4, V8.S4, V10.S4 // c
	VMUL V28.S4, V3.S4, V8.S4
	VMUL V29.S4, V5.S4, V9.S4
	VSSHR $16, V8.S4, V8.S4
	VSSHR $16, V9.S4, V9.S4
	VADD V9.S4, V8.S4, V11.S4 // d
	VADD V11.S4, V6.S4, V12.S4
	VADD V10.S4, V7.S4, V13.S4
	VSUB V10.S4, V7.S4, V14.S4
	VSUB V11.S4, V6.S4, V15.S4
	// Transposed: lanes j of m[0][j], ..., m[3][j].
	VTRN1 V13.S4, V12.S4, V16.S4
	VTRN2 V13.S4, V12.S4, V17.S4
	VTRN1 V15.S4, V14.S4, V18.S4
	VTRN2 V15.S4, V14.S4, V19.S4
	VTRN1 V18.D2, V16.D2, V12.D2
	VTRN2 V18.D2, V16.D2, V14.D2
	VTRN1 V19.D2, V17.D2, V13.D2
	VTRN2 V19.D2, V17.D2, V15.D2
	// Pass 2.
	VADD V27.S4, V12.S4, V12.S4
	VADD V14.S4, V12.S4, V6.S4 // a
	VSUB V14.S4, V12.S4, V7.S4 // b
	VMUL V29.S4, V13.S4, V8.S4
	VMUL V28.S4, V15.S4, V9.S4
	VSSHR $16, V8.S4, V8.S4
	VSSHR $16, V9.S4, V9.S4
	VSUB V9.S4, V8.S4, V10.S4 // c
	VMUL V28.S4, V13.S4, V8.S4
	VMUL V29.S4, V15.S4, V9.S4
	VSSHR $16, V8.S4, V8.S4
	VSSHR $16, V9.S4, V9.S4
	VADD V9.S4, V8.S4, V11.S4 // d
	VADD V11.S4, V6.S4, V12.S4
	VADD V10.S4, V7.S4, V13.S4
	VSUB V10.S4, V7.S4, V14.S4
	VSUB V11.S4, V6.S4, V15.S4
	VSSHR $3, V12.S4, V12.S4
	VSSHR $3, V13.S4, V13.S4
	VSSHR $3, V14.S4, V14.S4
	VSSHR $3, V15.S4, V15.S4
	// Transposed: the rows of the residual.
	VTRN1 V13.S4, V12.S4, V16.S4
	VTRN2 V13.S4, V12.S4, V17.S4
	VTRN1 V15.S4, V14.S4, V18.S4
	VTRN2 V15.S4, V14.S4, V19.S4
	VTRN1 V18.D2, V16.D2, V12.D2
	VTRN2 V18.D2, V16.D2, V14.D2
	VTRN1 V19.D2, V17.D2, V13.D2
	VTRN2 V19.D2, V17.D2, V15.D2
	// Plus ref, then saturated to 16 and to 8 bits: clipped as clip8 does.
	VLD1 (R1), V0.S[0]
	ADD R2, R1, R1
	VLD1 (R1), V0.S[1]
	ADD R2, R1, R1
	VLD1 (R1), V0.S[2]
	ADD R2, R1, R1
	VLD1 (R1), V0.S[3]
	VUXTL V0.B8, V1.H8
	VUXTL2 V0.B16, V2.H8
	VUXTL V1.H4, V3.S4
	VUXTL2 V1.H8, V4.S4
	VUXTL V2.H4, V5.S4
	VUXTL2 V2.H8, V6.S4
	VADD V3.S4, V12.S4, V12.S4
	VADD V4.S4, V13.S4, V13.S4
	VADD V5.S4, V14.S4, V14.S4
	VADD V6.S4, V15.S4, V15.S4
	VSQXTN V12.S4, V16.H4
	VSQXTN2 V13.S4, V16.H8
	VSQXTN V14.S4, V17.H4
	VSQXTN2 V15.S4, V17.H8
	VSQXTUN V16.H8, V18.B8
	VSQXTUN V17.H8, V19.B8
	VST1 V18.S[0], (R3)
	ADD R4, R3, R3
	VST1 V18.S[1], (R3)
	ADD R4, R3, R3
	VST1 V19.S[0], (R3)
	ADD R4, R3, R3
	VST1 V19.S[1], (R3)
	RET

// func sse4SIMD(a *byte, aStride int, b *byte, bStride int) int
TEXT ·sse4SIMD(SB), NOSPLIT, $0-40
	MOVD a+0(FP), R0
	MOVD aStride+8(FP), R1
	MOVD b+16(FP), R2
	MOVD bStride+24(FP), R3
	VLD1 (R0), V0.S[0]
	ADD R1, R0, R0
	VLD1 (R0), V0.S[1]
	ADD R1, R0, R0
	VLD1 (R0), V0.S[2]
	ADD R1, R0, R0
	VLD1 (R0), V0.S[3]
	VLD1 (R2), V1.S[0]
	ADD R3, R2, R2
	VLD1 (R2), V1.S[1]
	ADD R3, R2, R2
	VLD1 (R2), V1.S[2]
	ADD R3, R2, R2
	VLD1 (R2), V1.S[3]
	VUXTL V0.B8, V2.H8
	VUXTL2 V0.B16, V3.H8
	VUXTL V1.B8, V4.H8
	VUXTL2 V1.B16, V5.H8
	VSUB V4.H8, V2.H8, V2.H8
	VSUB V5.H8, V3.H8, V3.H8
	VSMULL V2.H4, V2.H4, V6.S4
	VSMLAL2 V2.H8, V2.H8, V6.S4
	VSMLAL V3.H4, V3.H4, V6.S4
	VSMLAL2 V3.H8, V3.H8, V6.S4
	VADDV V6.S4, V7
	VMOV V7.S[0], R4
	MOVD R4, ret+32(FP)
	RET

// The levels in scan order: bytes of the raster positions in the two
// registers of levels.
DATA qzTab<>+0(SB)/8, $0x1110090803020100
DATA qzTab<>+8(SB)/8, $0x0d0c070605040b0a
DATA qzTab<>+16(SB)/8, $0x15141b1a19181312
DATA qzTab<>+24(SB)/8, $0x1f1e1d1c17160f0e
GLOBL qzTab<>(SB), RODATA|NOPTR, $32

// func quantizeSIMD(in *[16]int16, v *quantVec, lv, deq *[16]int16) int
TEXT ·quantizeSIMD(SB), NOSPLIT, $0-40
	MOVD in+0(FP), R0
	MOVD v+8(FP), R1
	MOVD lv+16(FP), R2
	MOVD deq+24(FP), R3
	VLD1 (R0), [V0.H8, V1.H8]
	VLD1.P 32(R1), [V20.H8, V21.H8] // q
	VLD1.P 32(R1), [V22.H8, V23.H8] // iq
	VLD1 (R1), [V24.S4, V25.S4]     // biases: coefficient 0, then AC
	VSSHR $15, V0.H8, V2.H8 // signs
	VSSHR $15, V1.H8, V3.H8
	VABS V0.H8, V4.H8
	VABS V1.H8, V5.H8
	VUMULL V22.H4, V4.H4, V8.S4
	VUMULL2 V22.H8, V4.H8, V9.S4
	VUMULL V23.H4, V5.H4, V10.S4
	VUMULL2 V23.H8, V5.H8, V11.S4
	VADD V24.S4, V8.S4, V8.S4
	VADD V25.S4, V9.S4, V9.S4
	VADD V25.S4, V10.S4, V10.S4
	VADD V25.S4, V11.S4, V11.S4
	VUSHR $17, V8.S4, V8.S4
	VUSHR $17, V9.S4, V9.S4
	VUSHR $17, V10.S4, V10.S4
	VUSHR $17, V11.S4, V11.S4
	VXTN V8.S4, V14.H4
	VXTN2 V9.S4, V14.H8
	VXTN V10.S4, V15.H4
	VXTN2 V11.S4, V15.H8
	MOVD $2047, R5
	VDUP R5, V16.H8
	VSMIN V16.H8, V14.H8, V14.H8
	VSMIN V16.H8, V15.H8, V15.H8
	VEOR V2.B16, V14.B16, V14.B16
	VEOR V3.B16, V15.B16, V15.B16
	VSUB V2.H8, V14.H8, V14.H8 // levels, raster order
	VSUB V3.H8, V15.H8, V15.H8
	VMUL V20.H8, V14.H8, V17.H8
	VMUL V21.H8, V15.H8, V18.H8
	VST1 [V17.H8, V18.H8], (R3)
	MOVD $qzTab<>(SB), R6
	VLD1 (R6), [V26.B16, V27.B16]
	VTBL V26.B16, [V14.B16, V15.B16], V28.B16
	VTBL V27.B16, [V14.B16, V15.B16], V29.B16
	VST1 [V28.H8, V29.H8], (R2)
	// The last level that is not zero.
	VEOR V31.B16, V31.B16, V31.B16
	VCMEQ V31.H8, V28.H8, V28.H8
	VCMEQ V31.H8, V29.H8, V29.H8
	VXTN V28.H8, V30.B8
	VXTN2 V29.H8, V30.B16
	VMOV V30.D[1], R7
	MVN R7, R7
	CBZ R7, low
	CLZ R7, R7
	MOVD $127, R8
	SUB R7, R8, R8
	LSR $3, R8, R8
	MOVD R8, ret+32(FP)
	RET
low:
	VMOV V30.D[0], R7
	MVN R7, R7
	CBZ R7, none
	CLZ R7, R7
	MOVD $63, R8
	SUB R7, R8, R8
	LSR $3, R8, R8
	MOVD R8, ret+32(FP)
	RET
none:
	MOVD $-1, R8
	MOVD R8, ret+32(FP)
	RET
