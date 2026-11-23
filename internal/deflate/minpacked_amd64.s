//go:build amd64 && !purego

#include "textflag.h"

// func minPackedCostSIMD(lc, ct *uint32, n int) uint32
//
// The least of lc[k]+ct[k] for k < n, n at least 4: eight sums at a time,
// and the last eight again where n is not a multiple of 8; of fewer than
// eight, the first four and the last four.
TEXT ·minPackedCostSIMD(SB), NOSPLIT, $0-28
	MOVQ lc+0(FP), SI
	MOVQ ct+8(FP), DI
	MOVQ n+16(FP), CX
	CMPQ CX, $8
	JGE  wide

	VMOVDQU (SI), X0
	VPADDD  (DI), X0, X0
	VMOVDQU -16(SI)(CX*4), X1
	VPADDD  -16(DI)(CX*4), X1, X1
	VPMINUD X1, X0, X0
	JMP     four

wide:
	VPCMPEQD Y0, Y0, Y0 // the most there is
	SUBQ     $8, CX     // where the last eight start
	XORQ     AX, AX
	CMPQ     AX, CX
	JGE      last

loop:
	VMOVDQU (SI)(AX*4), Y1
	VPADDD  (DI)(AX*4), Y1, Y1
	VPMINUD Y1, Y0, Y0
	ADDQ    $8, AX
	CMPQ    AX, CX
	JLT     loop

last:
	VMOVDQU      (SI)(CX*4), Y1
	VPADDD       (DI)(CX*4), Y1, Y1
	VPMINUD      Y1, Y0, Y0
	VEXTRACTI128 $1, Y0, X1
	VPMINUD      X1, X0, X0

four:
	VPSHUFD $0x4E, X0, X1
	VPMINUD X1, X0, X0
	VPSHUFD $0xB1, X0, X1
	VPMINUD X1, X0, X0
	VMOVD   X0, AX
	VZEROUPPER
	MOVL    AX, ret+24(FP)
	RET
