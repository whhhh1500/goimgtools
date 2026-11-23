//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// func minPackedCostSIMD(lc, ct *uint32, n int) uint32
//
// The least of lc[k]+ct[k] for k < n, n at least 4: four sums at a time,
// and the last four again where n is not a multiple of 4.
TEXT ·minPackedCostSIMD(SB), NOSPLIT, $0-28
	MOVD   lc+0(FP), R0
	MOVD   ct+8(FP), R1
	MOVD   n+16(FP), R2
	VLD1.P 16(R0), [V1.S4]
	VLD1.P 16(R1), [V2.S4]
	VADD   V1.S4, V2.S4, V0.S4
	SUB    $4, R2

loop:
	CMP    $4, R2
	BLT    last
	VLD1.P 16(R0), [V1.S4]
	VLD1.P 16(R1), [V2.S4]
	VADD   V1.S4, V2.S4, V1.S4
	VUMIN  V1.S4, V0.S4, V0.S4
	SUB    $4, R2
	B      loop

last:
	CBZ   R2, done
	SUB   $4, R2             // back to where the last four start
	ADD   R2<<2, R0
	ADD   R2<<2, R1
	VLD1  (R0), [V1.S4]
	VLD1  (R1), [V2.S4]
	VADD  V1.S4, V2.S4, V1.S4
	VUMIN V1.S4, V0.S4, V0.S4

done:
	WORD  $0x6eb1a800        // UMINV S0, V0.4S
	FMOVS F0, R3
	MOVW  R3, ret+24(FP)
	RET
