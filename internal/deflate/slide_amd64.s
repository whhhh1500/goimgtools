//go:build amd64 && !purego

#include "textflag.h"

// func slideSIMD(p *int16, n int)
//
// The slide of n positions at p, n a multiple of 32: 32768 less, but no
// less than -32768, which is no position.
TEXT ·slideSIMD(SB), NOSPLIT, $0-16
	MOVQ     p+0(FP), DI
	MOVQ     n+8(FP), CX
	VPCMPEQW Y1, Y1, Y1
	VPSLLW   $15, Y1, Y1 // -32768

loop:
	VMOVDQU (DI), Y0
	VMOVDQU 32(DI), Y2
	VPADDSW Y1, Y0, Y0
	VPADDSW Y1, Y2, Y2
	VMOVDQU Y0, (DI)
	VMOVDQU Y2, 32(DI)
	ADDQ    $64, DI
	SUBQ    $32, CX
	JGT     loop
	VZEROUPPER
	RET
