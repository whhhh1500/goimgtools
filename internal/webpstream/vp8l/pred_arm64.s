// The inverse predictors 0 to 4, 8 and 9 of predictRun (transform.go) for
// 4 pixels at a time with NEON, after libwebp's SSE2 code (see
// pred_amd64.s): every byte that of the Go code. Average2 is VUHADD, the
// halving add that rounds down.
//
// Each function undoes the prediction of n pixels (a multiple of 4, at
// least 4) at cur, the row above at top; cur[-4:0] is the pixel to their
// left, top[-4:0] the one above it.

//go:build arm64 && go1.27 && !purego

#include "textflag.h"

#define ARGS \
	MOVD cur+0(FP), R0; \
	MOVD top+8(FP), R1; \
	MOVD n+16(FP), R2; \
	LSR $2, R2, R2

// func predAdd0(cur, top *byte, n int)
TEXT ·predAdd0(SB), NOSPLIT, $0-24
	ARGS
	MOVD $0xff000000, R3
	VDUP R3, V1.S4
loop0:
	VLD1 (R0), [V0.B16]
	VADD V1.B16, V0.B16, V0.B16
	VST1.P [V0.B16], 16(R0)
	SUB $1, R2, R2
	CBNZ R2, loop0
	RET

// func predAdd1(cur, top *byte, n int)
//
// L: the prefix sums of the four pixels, plus the pixel to their left.
TEXT ·predAdd1(SB), NOSPLIT, $0-24
	ARGS
	VEOR V3.B16, V3.B16, V3.B16
	SUB $4, R0, R4
	VLD1 (R4), V1.S[0]
	VDUP V1.S[0], V1.S4
loop1:
	VLD1 (R0), [V0.B16]
	VEXT $12, V0.B16, V3.B16, V2.B16 // 0 | a | b | c
	VADD V2.B16, V0.B16, V0.B16
	VEXT $8, V0.B16, V3.B16, V2.B16 // 0 | 0 | a | a+b
	VADD V2.B16, V0.B16, V0.B16
	VADD V1.B16, V0.B16, V0.B16
	VST1.P [V0.B16], 16(R0)
	VDUP V0.S[3], V1.S4
	SUB $1, R2, R2
	CBNZ R2, loop1
	RET

// Four pixels of a predictor from the row above only, at R1: T, TR (R1
// moved on by 4) or TL (back by 4).
#define ADD_TOP \
	VLD1 (R0), [V0.B16]; \
	VLD1.P 16(R1), [V1.B16]; \
	VADD V1.B16, V0.B16, V0.B16; \
	VST1.P [V0.B16], 16(R0)

// func predAdd2(cur, top *byte, n int)
TEXT ·predAdd2(SB), NOSPLIT, $0-24
	ARGS
loop2:
	ADD_TOP
	SUB $1, R2, R2
	CBNZ R2, loop2
	RET

// func predAdd3(cur, top *byte, n int)
TEXT ·predAdd3(SB), NOSPLIT, $0-24
	ARGS
	ADD $4, R1, R1
loop3:
	ADD_TOP
	SUB $1, R2, R2
	CBNZ R2, loop3
	RET

// func predAdd4(cur, top *byte, n int)
TEXT ·predAdd4(SB), NOSPLIT, $0-24
	ARGS
	SUB $4, R1, R1
loop4:
	ADD_TOP
	SUB $1, R2, R2
	CBNZ R2, loop4
	RET

// Four pixels of Average2 of the pixels of the row above at R1 and R1+4:
// TL and T (R1 back by 4), T and TR.
#define AVG_TOP \
	ADD $4, R1, R4; \
	VLD1.P 16(R1), [V1.B16]; \
	VLD1 (R4), [V2.B16]; \
	VUHADD V2.B16, V1.B16, V1.B16; \
	VLD1 (R0), [V0.B16]; \
	VADD V1.B16, V0.B16, V0.B16; \
	VST1.P [V0.B16], 16(R0)

// func predAdd8(cur, top *byte, n int)
TEXT ·predAdd8(SB), NOSPLIT, $0-24
	ARGS
	SUB $4, R1, R1
loop8:
	AVG_TOP
	SUB $1, R2, R2
	CBNZ R2, loop8
	RET

// func predAdd9(cur, top *byte, n int)
TEXT ·predAdd9(SB), NOSPLIT, $0-24
	ARGS
loop9:
	AVG_TOP
	SUB $1, R2, R2
	CBNZ R2, loop9
	RET
