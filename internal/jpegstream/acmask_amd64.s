// The mask of acFirst (writer_prog.go) with AVX2: which coefficients of a
// block are not zero once the low al bits of their absolute values are
// dropped, as the Go loop computes it.

//go:build amd64 && !purego

#include "textflag.h"

// func acMaskAsm(w *byte, al uint) uint64
//
// Bit k is set when coefficient k of the block at w (64 little-endian
// int16) is nonzero after |c| >> al.
TEXT ·acMaskAsm(SB), NOSPLIT, $0-24
	MOVQ w+0(FP), SI
	MOVQ al+8(FP), AX
	VMOVQ AX, X4
	VPABSW 0(SI), Y0
	VPABSW 32(SI), Y1
	VPABSW 64(SI), Y2
	VPABSW 96(SI), Y3
	VPSRLW X4, Y0, Y0
	VPSRLW X4, Y1, Y1
	VPSRLW X4, Y2, Y2
	VPSRLW X4, Y3, Y3
	VPXOR Y5, Y5, Y5
	VPCMPEQW Y5, Y0, Y0
	VPCMPEQW Y5, Y1, Y1
	VPCMPEQW Y5, Y2, Y2
	VPCMPEQW Y5, Y3, Y3
	// Bytes in each lane: a lane of the first register, then of the second.
	VPACKSSWB Y1, Y0, Y0
	VPACKSSWB Y3, Y2, Y2
	VPERMQ $0xd8, Y0, Y0
	VPERMQ $0xd8, Y2, Y2
	VPMOVMSKB Y0, AX
	VPMOVMSKB Y2, DX
	SHLQ $32, DX
	ORQ DX, AX
	NOTQ AX
	MOVQ AX, ret+16(FP)
	VZEROUPPER
	RET
