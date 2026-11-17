// inverseDCT4DCOnly and inverseDCT8DCOnly (idct.go) with SSE4.1 (in the
// AVX encoding): the DC added in 16-bit lanes and saturated to 8 bits,
// which clips as clip8 does: every byte that of the Go code.

//go:build amd64 && !purego

#include "textflag.h"

// func dcAddAsm(dst *byte, stride, dc int)
TEXT ·dcAddAsm(SB), NOSPLIT, $0-24
	MOVQ dst+0(FP), DI
	MOVQ stride+8(FP), BX
	MOVQ dc+16(FP), AX
	VMOVD AX, X2
	VPBROADCASTW X2, X2
	LEAQ (DI)(BX*2), R8
	VMOVD (DI), X0
	VPINSRD $1, (DI)(BX*1), X0, X0
	VMOVD (R8), X1
	VPINSRD $1, (R8)(BX*1), X1, X1
	VPMOVZXBW X0, X0
	VPMOVZXBW X1, X1
	VPADDW X2, X0, X0
	VPADDW X2, X1, X1
	VPACKUSWB X1, X0, X0
	VMOVD X0, (DI)
	VPEXTRD $1, X0, (DI)(BX*1)
	VPEXTRD $2, X0, (R8)
	VPEXTRD $3, X0, (R8)(BX*1)
	RET

// func dcAdd8Asm(dst *byte, stride, d0, d1, d2, d3 int)
//
// The four 4x4 blocks of an 8x8 one: d0 top left, d1 top right, d2 bottom
// left, d3 bottom right.
TEXT ·dcAdd8Asm(SB), NOSPLIT, $0-48
	MOVQ dst+0(FP), DI
	MOVQ stride+8(FP), BX
	MOVQ d0+16(FP), AX
	VMOVD AX, X4
	VPBROADCASTW X4, X4
	MOVQ d1+24(FP), AX
	VMOVD AX, X5
	VPBROADCASTW X5, X5
	VPBLENDW $0xf0, X5, X4, X4
	MOVQ d2+32(FP), AX
	VMOVD AX, X5
	VPBROADCASTW X5, X5
	MOVQ d3+40(FP), AX
	VMOVD AX, X6
	VPBROADCASTW X6, X6
	VPBLENDW $0xf0, X6, X5, X5
	LEAQ (BX)(BX*2), DX
	LEAQ (DI)(BX*4), R8
	VPMOVZXBW (DI), X0
	VPMOVZXBW (DI)(BX*1), X1
	VPMOVZXBW (DI)(BX*2), X2
	VPMOVZXBW (DI)(DX*1), X3
	VPADDW X4, X0, X0
	VPADDW X4, X1, X1
	VPADDW X4, X2, X2
	VPADDW X4, X3, X3
	VPACKUSWB X1, X0, X0
	VPACKUSWB X3, X2, X2
	VMOVQ X0, (DI)
	VPEXTRQ $1, X0, (DI)(BX*1)
	VMOVQ X2, (DI)(BX*2)
	VPEXTRQ $1, X2, (DI)(DX*1)
	VPMOVZXBW (R8), X0
	VPMOVZXBW (R8)(BX*1), X1
	VPMOVZXBW (R8)(BX*2), X2
	VPMOVZXBW (R8)(DX*1), X3
	VPADDW X5, X0, X0
	VPADDW X5, X1, X1
	VPADDW X5, X2, X2
	VPADDW X5, X3, X3
	VPACKUSWB X1, X0, X0
	VPACKUSWB X3, X2, X2
	VMOVQ X0, (R8)
	VPEXTRQ $1, X0, (R8)(BX*1)
	VMOVQ X2, (R8)(BX*2)
	VPEXTRQ $1, X2, (R8)(DX*1)
	RET
