// The inverse predictors of predictRun (transform.go) for 4 pixels at a
// time, after libwebp's PredictorAdd*_SSE2 (src/dsp/lossless_sse2.c: see
// licenses/LICENSE-LIBWEBP), in the AVX encoding: every byte that of the Go
// code. Each channel is handled on its own (or, for Select, summed over
// the four), so the RGBA order of the bytes here, against libwebp's ARGB
// words, changes nothing.
//
// Each function undoes the prediction of n pixels (a multiple of 4, at
// least 4) at cur, the row above at top; cur[-4:0] is the pixel to their
// left, top[-4:0] the one above it.

//go:build amd64 && !purego

#include "textflag.h"

DATA predOnes<>+0(SB)/8, $0x0101010101010101
DATA predOnes<>+8(SB)/8, $0x0101010101010101
GLOBL predOnes<>(SB), RODATA|NOPTR, $16
DATA predBlack<>+0(SB)/8, $0xff000000ff000000
DATA predBlack<>+8(SB)/8, $0xff000000ff000000
GLOBL predBlack<>(SB), RODATA|NOPTR, $16

// AVG2 sets D to the average of A and B, rounded down, per byte: VPAVGB
// rounds up, less the low bit of A^B. T is a temporary.
#define AVG2(A, B, D, T) \
	VPXOR A, B, T; \
	VPAND predOnes<>(SB), T, T; \
	VPAVGB A, B, D; \
	VPSUBB T, D, D

#define ARGS \
	MOVQ cur+0(FP), SI; \
	MOVQ top+8(FP), DI; \
	MOVQ n+16(FP), CX; \
	SHRQ $2, CX

// func predAdd0(cur, top *byte, n int)
TEXT ·predAdd0(SB), NOSPLIT, $0-24
	ARGS
	VMOVDQU predBlack<>(SB), X1
loop0:
	VMOVDQU (SI), X0
	VPADDB X1, X0, X0
	VMOVDQU X0, (SI)
	ADDQ $16, SI
	DECQ CX
	JNZ loop0
	RET

// func predAdd1(cur, top *byte, n int)
//
// L: the prefix sums of the four pixels, plus the pixel to their left.
TEXT ·predAdd1(SB), NOSPLIT, $0-24
	ARGS
	VMOVD -4(SI), X1
	VPSHUFD $0, X1, X1
loop1:
	VMOVDQU (SI), X0
	VPSLLDQ $4, X0, X2
	VPADDB X2, X0, X0
	VPSLLDQ $8, X0, X2
	VPADDB X2, X0, X0
	VPADDB X1, X0, X0
	VMOVDQU X0, (SI)
	VPSHUFD $0xff, X0, X1
	ADDQ $16, SI
	DECQ CX
	JNZ loop1
	RET

// Four pixels of a predictor from the row above only: T (OFF 0), TR (4),
// TL (-4).
#define ADD_TOP(OFF) \
	VMOVDQU (SI), X0; \
	VMOVDQU OFF(DI), X1; \
	VPADDB X1, X0, X0; \
	VMOVDQU X0, (SI); \
	ADDQ $16, SI; \
	ADDQ $16, DI

// func predAdd2(cur, top *byte, n int)
TEXT ·predAdd2(SB), NOSPLIT, $0-24
	ARGS
loop2:
	ADD_TOP(0)
	DECQ CX
	JNZ loop2
	RET

// func predAdd3(cur, top *byte, n int)
TEXT ·predAdd3(SB), NOSPLIT, $0-24
	ARGS
loop3:
	ADD_TOP(4)
	DECQ CX
	JNZ loop3
	RET

// func predAdd4(cur, top *byte, n int)
TEXT ·predAdd4(SB), NOSPLIT, $0-24
	ARGS
loop4:
	ADD_TOP(-4)
	DECQ CX
	JNZ loop4
	RET

// Four pixels of Average2 of two pixels of the row above: TL and T (8), T
// and TR (9).
#define AVG_TOP(OFFA, OFFB) \
	VMOVDQU OFFA(DI), X1; \
	VMOVDQU OFFB(DI), X2; \
	AVG2(X1, X2, X3, X4); \
	VMOVDQU (SI), X0; \
	VPADDB X3, X0, X0; \
	VMOVDQU X0, (SI); \
	ADDQ $16, SI; \
	ADDQ $16, DI

// func predAdd8(cur, top *byte, n int)
TEXT ·predAdd8(SB), NOSPLIT, $0-24
	ARGS
loop8:
	AVG_TOP(-4, 0)
	DECQ CX
	JNZ loop8
	RET

// func predAdd9(cur, top *byte, n int)
TEXT ·predAdd9(SB), NOSPLIT, $0-24
	ARGS
loop9:
	AVG_TOP(0, 4)
	DECQ CX
	JNZ loop9
	RET

// One pixel of predictor 10: L becomes src + Average2(Average2(L, TL),
// Average2(T, TR)) in X0's low lane, stored at OUT(SI); then the next
// pixel's values move down.
#define PRED10(OUT) \
	AVG2(X0, X2, X5, X6); \
	AVG2(X3, X5, X5, X6); \
	VPADDB X5, X1, X0; \
	VMOVD X0, OUT(SI); \
	VPSRLDQ $4, X3, X3; \
	VPSRLDQ $4, X2, X2; \
	VPSRLDQ $4, X1, X1

// func predAdd10(cur, top *byte, n int)
TEXT ·predAdd10(SB), NOSPLIT, $0-24
	ARGS
	VMOVD -4(SI), X0 // L
loop10:
	VMOVDQU (SI), X1  // src
	VMOVDQU -4(DI), X2 // TL
	VMOVDQU (DI), X3  // T
	VMOVDQU 4(DI), X4 // TR
	AVG2(X3, X4, X3, X6) // Average2(T, TR)
	PRED10(0)
	PRED10(4)
	PRED10(8)
	PRED10(12)
	ADDQ $16, SI
	ADDQ $16, DI
	DECQ CX
	JNZ loop10
	RET

// One pixel of predictor 11: the sum of |L - TL| over the channels (pb)
// against that of |T - TL| (pa, X4): L if pb > pa, else T; then the next
// pixel's values move down.
#define PRED11(OUT) \
	VPUNPCKLDQ X3, X0, X5; \
	VPUNPCKLDQ X3, X2, X6; \
	VPSADBW X6, X5, X5; \
	VPCMPGTD X4, X5, X5; \
	VPAND X5, X0, X6; \
	VPANDN X3, X5, X5; \
	VPOR X6, X5, X5; \
	VPADDB X5, X1, X0; \
	VMOVD X0, OUT(SI); \
	VPSRLDQ $4, X3, X3; \
	VPSRLDQ $4, X2, X2; \
	VPSRLDQ $4, X1, X1; \
	VPSRLDQ $4, X4, X4

// func predAdd11(cur, top *byte, n int)
TEXT ·predAdd11(SB), NOSPLIT, $0-24
	ARGS
	VMOVD -4(SI), X0 // L
loop11:
	VMOVDQU (SI), X1   // src
	VMOVDQU -4(DI), X2 // TL
	VMOVDQU (DI), X3   // T
	// pa: each pixel of T and TL paired with T, whose difference is 0.
	VPUNPCKLDQ X3, X3, X5
	VPUNPCKLDQ X3, X2, X6
	VPSADBW X6, X5, X5
	VPUNPCKHDQ X3, X3, X6
	VPUNPCKHDQ X3, X2, X7
	VPSADBW X7, X6, X6
	VPACKSSDW X6, X5, X4
	PRED11(0)
	PRED11(4)
	PRED11(8)
	PRED11(12)
	ADDQ $16, SI
	ADDQ $16, DI
	DECQ CX
	JNZ loop11
	RET

// One pixel of predictor 12: L (16-bit lanes, X0) plus T - TL (DIFF),
// saturated to a byte, plus src; then the next pixel's src moves down.
#define PRED12(DIFF, OUT) \
	VPADDW DIFF, X0, X5; \
	VPACKUSWB X5, X5, X5; \
	VPADDB X5, X1, X5; \
	VMOVD X5, OUT(SI); \
	VPUNPCKLBW X7, X5, X0; \
	VPSRLDQ $4, X1, X1

// func predAdd12(cur, top *byte, n int)
TEXT ·predAdd12(SB), NOSPLIT, $0-24
	ARGS
	VPXOR X7, X7, X7
	VMOVD -4(SI), X0
	VPUNPCKLBW X7, X0, X0 // L
loop12:
	VMOVDQU (SI), X1   // src
	VMOVDQU (DI), X2   // T
	VMOVDQU -4(DI), X3 // TL
	VPUNPCKLBW X7, X2, X4
	VPUNPCKLBW X7, X3, X5
	VPSUBW X5, X4, X4 // T - TL, pixels 0 and 1
	VPUNPCKHBW X7, X2, X2
	VPUNPCKHBW X7, X3, X3
	VPSUBW X3, X2, X2 // pixels 2 and 3
	PRED12(X4, 0)
	VPSRLDQ $8, X4, X4
	PRED12(X4, 4)
	PRED12(X2, 8)
	VPSRLDQ $8, X2, X2
	PRED12(X2, 12)
	ADDQ $16, SI
	ADDQ $16, DI
	DECQ CX
	JNZ loop12
	RET
