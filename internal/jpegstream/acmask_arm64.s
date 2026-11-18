// The mask of acFirst (writer_prog.go) with NEON: which coefficients of a
// block are not zero once the low al bits of their absolute values are
// dropped, as the Go loop computes it. |c| >> al is not zero when |c| is
// at least k = 1<<al, which is when c + k - 1, as an unsigned 16-bit
// value, is above 2k - 2: no absolute value is needed.

//go:build arm64 && go1.27 && !purego

#include "textflag.h"

// func acMaskAsm(w *byte, al uint) uint64
//
// Bit k is set when coefficient k of the block at w (64 little-endian
// int16) is nonzero after |c| >> al.
TEXT ·acMaskAsm(SB), NOSPLIT, $0-24
	MOVD w+0(FP), R0
	MOVD al+8(FP), R1
	MOVD $1, R2
	LSL R1, R2, R2
	SUB $1, R2, R3
	ADD R3, R3, R4
	VDUP R3, V16.H8 // k - 1
	VDUP R4, V17.H8 // 2k - 2
	MOVD $0x8040201008040201, R5
	VMOV R5, V18.D[0]
	VMOV R5, V18.D[1]
	VLD1.P 64(R0), [V0.H8, V1.H8, V2.H8, V3.H8]
	VLD1 (R0), [V4.H8, V5.H8, V6.H8, V7.H8]
	VADD V16.H8, V0.H8, V0.H8
	VADD V16.H8, V1.H8, V1.H8
	VADD V16.H8, V2.H8, V2.H8
	VADD V16.H8, V3.H8, V3.H8
	VADD V16.H8, V4.H8, V4.H8
	VADD V16.H8, V5.H8, V5.H8
	VADD V16.H8, V6.H8, V6.H8
	VADD V16.H8, V7.H8, V7.H8
	VCMHI V17.H8, V0.H8, V0.H8
	VCMHI V17.H8, V1.H8, V1.H8
	VCMHI V17.H8, V2.H8, V2.H8
	VCMHI V17.H8, V3.H8, V3.H8
	VCMHI V17.H8, V4.H8, V4.H8
	VCMHI V17.H8, V5.H8, V5.H8
	VCMHI V17.H8, V6.H8, V6.H8
	VCMHI V17.H8, V7.H8, V7.H8
	// A byte per coefficient, 0xff where it is not zero.
	VXTN V0.H8, V8.B8
	VXTN2 V1.H8, V8.B16
	VXTN V2.H8, V9.B8
	VXTN2 V3.H8, V9.B16
	VXTN V4.H8, V10.B8
	VXTN2 V5.H8, V10.B16
	VXTN V6.H8, V11.B8
	VXTN2 V7.H8, V11.B16
	// Each byte keeps its bit of the eight of its group, and pairwise sums
	// gather a group's bits into one byte: byte i of the result is the
	// mask of coefficients 8i to 8i+7.
	VAND V18.B16, V8.B16, V8.B16
	VAND V18.B16, V9.B16, V9.B16
	VAND V18.B16, V10.B16, V10.B16
	VAND V18.B16, V11.B16, V11.B16
	VADDP V9.B16, V8.B16, V12.B16
	VADDP V11.B16, V10.B16, V13.B16
	VADDP V13.B16, V12.B16, V14.B16
	VADDP V14.B16, V14.B16, V15.B16
	VMOV V15.D[0], R0
	MOVD R0, ret+16(FP)
	RET
