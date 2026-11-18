// The two passes of planeScaler.feed (planes.go) with AVX2: the products of
// the 14-bit weights and the samples, in pairs of 16-bit multiplications,
// summed in 32 bits with the rounding, shifted and kept within the range
// of the result; whole numbers, so every value is that of the Go code.

//go:build amd64 && !purego

#include "textflag.h"

DATA plane_hround<>+0(SB)/4, $128
GLOBL plane_hround<>(SB), RODATA|NOPTR, $4
DATA plane_vround<>+0(SB)/4, $524288
GLOBL plane_vround<>(SB), RODATA|NOPTR, $4

// One destination sample of the pass across: the 16 bytes of the row from
// its first source sample, as words, times its 16 weights, in pairs.
#define HTAP(i, R) \
	MOVL (i*4)(BX), AX; \
	VPMOVZXBW (SI)(AX*1), R; \
	VPMADDWD (i*32)(DI), R, R

// func planeHSIMD(out *int16, row *byte, start *int32, w16 *int16, groups int)
//
// groups times 8 samples of the pass across: out[x] is the sum of the 16
// bytes of row from start[x] times w16[16x:16x+16], plus 128, shifted down
// by 8 and kept within int16.
TEXT ·planeHSIMD(SB), NOSPLIT, $0-40
	MOVQ out+0(FP), R8
	MOVQ row+8(FP), SI
	MOVQ start+16(FP), BX
	MOVQ w16+24(FP), DI
	MOVQ groups+32(FP), CX
	VPBROADCASTD plane_hround<>(SB), Y15
hloop:
	HTAP(0, Y0)
	HTAP(1, Y1)
	HTAP(2, Y2)
	HTAP(3, Y3)
	HTAP(4, Y4)
	HTAP(5, Y5)
	HTAP(6, Y6)
	HTAP(7, Y7)
	// Each register holds 8 partial sums of one sample; add them up, the
	// samples in order: the halves of each 128-bit lane, then the lanes.
	VPHADDD Y1, Y0, Y8
	VPHADDD Y3, Y2, Y9
	VPHADDD Y5, Y4, Y10
	VPHADDD Y7, Y6, Y11
	VPHADDD Y9, Y8, Y8
	VPHADDD Y11, Y10, Y10
	VPERM2I128 $0x20, Y10, Y8, Y12
	VPERM2I128 $0x31, Y10, Y8, Y13
	VPADDD Y13, Y12, Y12
	VPADDD Y15, Y12, Y12
	VPSRAD $8, Y12, Y12
	VEXTRACTI128 $1, Y12, X13
	VPACKSSDW X13, X12, X12
	VMOVDQU X12, (R8)
	ADDQ $32, BX
	ADDQ $256, DI
	ADDQ $16, R8
	DECQ CX
	JNZ hloop
	VZEROUPPER
	RET

// func planeVSIMD(out *byte, rows **int16, pairs *uint32, npairs, chunks int)
//
// chunks times 16 bytes of out: the sum over the rows (two to a pair, a
// pair's weights in the low and high halves of its uint32) of the row's
// sample times its weight, plus 1<<19, shifted down by 20 and kept within 0
// to 255.
TEXT ·planeVSIMD(SB), NOSPLIT, $0-40
	MOVQ out+0(FP), DI
	MOVQ rows+8(FP), SI
	MOVQ pairs+16(FP), DX
	MOVQ npairs+24(FP), R9
	MOVQ chunks+32(FP), CX
	VPBROADCASTD plane_vround<>(SB), Y14
	XORQ BX, BX
vchunk:
	VPXOR Y0, Y0, Y0
	VPXOR Y1, Y1, Y1
	MOVQ SI, R13
	MOVQ DX, R8
	MOVQ R9, R10
vpair:
	MOVQ (R13), R11
	MOVQ 8(R13), R12
	VMOVDQU (R11)(BX*1), Y2
	VMOVDQU (R12)(BX*1), Y3
	VPBROADCASTD (R8), Y4
	VPUNPCKLWD Y3, Y2, Y5
	VPUNPCKHWD Y3, Y2, Y6
	VPMADDWD Y4, Y5, Y5
	VPMADDWD Y4, Y6, Y6
	VPADDD Y5, Y0, Y0
	VPADDD Y6, Y1, Y1
	ADDQ $16, R13
	ADDQ $4, R8
	DECQ R10
	JNZ vpair
	// Y0 has samples 0-3 and 8-11, Y1 4-7 and 12-15: packing puts them in
	// order within each lane, and the lanes' low halves together.
	VPADDD Y14, Y0, Y0
	VPADDD Y14, Y1, Y1
	VPSRAD $20, Y0, Y0
	VPSRAD $20, Y1, Y1
	VPACKSSDW Y1, Y0, Y0
	VPACKUSWB Y0, Y0, Y0
	VPERMQ $0x08, Y0, Y0
	VMOVDQU X0, (DI)
	ADDQ $16, DI
	ADDQ $32, BX
	DECQ CX
	JNZ vchunk
	VZEROUPPER
	RET
