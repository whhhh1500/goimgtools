//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// func slideSIMD(p *int16, n int)
//
// The slide of n positions at p, n a multiple of 32: 32768 less, but no
// less than -32768, which is no position.
TEXT ·slideSIMD(SB), NOSPLIT, $0-16
	MOVD p+0(FP), R0
	MOVD n+8(FP), R1
	MOVD $0x8000, R2
	VDUP R2, V2.H8   // -32768

loop:
	VLD1   (R0), [V0.H8, V1.H8]
	WORD   $0x4e620c00           // SQADD V0.8H, V0.8H, V2.8H
	WORD   $0x4e620c21           // SQADD V1.8H, V1.8H, V2.8H
	VST1.P [V0.H8, V1.H8], 32(R0)
	SUBS   $16, R1
	BGT    loop
	RET
