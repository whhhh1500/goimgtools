//go:build amd64 && !purego

#include "textflag.h"

// The four bytes at each of the first four of 16 bytes.
DATA quads<>+0(SB)/8, $0x0403020103020100
DATA quads<>+8(SB)/8, $0x0605040305040302
GLOBL quads<>(SB), RODATA|NOPTR, $16

DATA iota<>+0(SB)/8, $0x0000000100000000
DATA iota<>+8(SB)/8, $0x0000000300000002
DATA iota<>+16(SB)/8, $0x0000000500000004
DATA iota<>+24(SB)/8, $0x0000000700000006
GLOBL iota<>(SB), RODATA|NOPTR, $32

// func markChainsSIMD(d *byte, prev *uint16, mask, ring, window int, marks *uint8, n int)
//
// markChains, as it is in Go (see there), eight positions at a time, for the
// n positions from d on, n a multiple of 8: their entries in prev are those
// from ring on, without going round its end, and 20 bytes can be read at the
// last eight. The positions before in the chains and what prev has of them
// are gathered; prev is read two bytes past the entry of the place asked
// for.
//
// Y0 is the positions' four bytes, Y1 and Y7 how far back the first two of
// their chains are, Y2 their places in prev; Y8 is set anew for each gather,
// which clears it.
TEXT ·markChainsSIMD(SB), NOSPLIT, $0-56
	MOVQ         d+0(FP), SI
	MOVQ         prev+8(FP), DI
	MOVQ         ring+24(FP), R9
	MOVQ         marks+40(FP), R11
	MOVQ         n+48(FP), CX
	VMOVDQU      iota<>(SB), Y15
	VPBROADCASTD mask+16(FP), Y14
	VPBROADCASTD window+32(FP), Y13
	VPCMPEQD     Y12, Y12, Y12
	VPSRLD       $8, Y12, Y12     // the three bytes
	VPCMPEQD     Y11, Y11, Y11
	VPSRLD       $16, Y11, Y11    // an entry of prev
	VPXOR        Y10, Y10, Y10
	VPBROADCASTD ring+24(FP), Y2
	VPADDD       Y15, Y2, Y2
	VPCMPEQD     Y9, Y9, Y9
	VPSRLD       $31, Y9, Y9
	VPSLLD       $3, Y9, Y9       // eight

loop:
	VMOVDQU     (SI), X0
	VMOVDQU     4(SI), X3
	VPSHUFB     quads<>(SB), X0, X0
	VPSHUFB     quads<>(SB), X3, X3
	VINSERTI128 $1, X3, Y0, Y0
	VPMOVZXWD   (DI)(R9*2), Y1

	// The first of each chain: its three bytes against the position's, and
	// how far the next is from it.
	VPSUBD     Y1, Y15, Y3
	VPCMPEQD   Y8, Y8, Y8
	VPGATHERDD Y8, (SI)(Y3*1), Y4
	VPXOR      Y0, Y4, Y4
	VPAND      Y12, Y4, Y4
	VPCMPEQD   Y10, Y4, Y4        // the same three bytes
	VPSUBD     Y1, Y2, Y5
	VPAND      Y14, Y5, Y5
	VPCMPEQD   Y8, Y8, Y8
	VPGATHERDD Y8, (DI)(Y5*2), Y6
	VPAND      Y11, Y6, Y6

	// The second, if there is one within the window; else the position.
	VPADDD   Y1, Y6, Y7
	VPCMPGTD Y7, Y13, Y5
	VPAND    Y5, Y7, Y7
	VPCMPEQD Y10, Y6, Y5
	VPANDN   Y7, Y5, Y7
	VPSUBD     Y7, Y15, Y3
	VPCMPEQD   Y8, Y8, Y8
	VPGATHERDD Y8, (SI)(Y3*1), Y5
	VPXOR      Y0, Y5, Y5
	VPAND      Y12, Y5, Y5
	VPCMPEQD   Y10, Y5, Y5        // the same three bytes
	VPSUBD     Y7, Y2, Y3
	VPAND      Y14, Y3, Y3
	VPCMPEQD   Y8, Y8, Y8
	VPGATHERDD Y8, (DI)(Y3*2), Y6
	VPAND      Y11, Y6, Y6

	// A third within the window (4), and the second's three bytes the
	// position's (2): either only with a second. The first's (1), with a
	// first.
	VPADDD   Y7, Y6, Y3
	VPCMPGTD Y3, Y13, Y3
	VPCMPEQD Y10, Y6, Y6
	VPANDN   Y3, Y6, Y3
	VPCMPEQD Y10, Y7, Y6
	VPANDN   Y3, Y6, Y3
	VPANDN   Y5, Y6, Y5
	VPCMPEQD Y10, Y1, Y6
	VPANDN   Y4, Y6, Y4
	VPSRLD   $31, Y3, Y3
	VPSRLD   $31, Y5, Y5
	VPSRLD   $31, Y4, Y4
	VPSLLD   $2, Y3, Y3
	VPADDD   Y5, Y5, Y5
	VPOR     Y4, Y3, Y3
	VPOR     Y5, Y3, Y3

	// A byte for each.
	VPACKUSDW    Y3, Y3, Y3
	VPACKUSWB    Y3, Y3, Y3
	VEXTRACTI128 $1, Y3, X4
	VMOVD        X3, (R11)
	VMOVD        X4, 4(R11)

	VPADDD Y9, Y2, Y2
	ADDQ   $8, SI
	ADDQ   $8, R9
	ADDQ   $8, R11
	SUBQ   $8, CX
	JNZ    loop
	VZEROUPPER
	RET
