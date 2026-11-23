//go:build amd64 && !purego

#include "textflag.h"

// Where the costs of the offset slots and of the lengths start in dpTables.
#define SLOT 1024
#define LEN 1152

// The cost of the match of length n (its packed cost at LEN+4*n, the cost to
// the end after it at END(DI)(BX*4), BX the position after this one), with
// the offset in DX, R11 and R8 used: R11 is the cost, DX the item.
#define MATCH(n, END) \
	MOVBLZX (R10)(DX*1), R11; \
	MOVL    SLOT(R9)(R11*4), R11; \
	MOVL    END(DI)(BX*4), R8; \
	ADDL    (LEN+4*n)(R9), R8; \
	SHRL    $9, R8; \
	ADDL    R8, R11; \
	ORL     $(n<<23), DX

// R11 or the most there is when the longest match (R12) is shorter than n;
// then the cost (R13) and the item (AX) are R11 and DX if R11 is less.
#define TAKE(n) \
	CMPL    R12, $n; \
	SBBL    R8, R8; \
	ORL     R8, R11; \
	CMPL    R11, R13; \
	CMOVLCS R11, R13; \
	CMOVLCS DX, AX

// func simplePositionsAsm(cache *lzMatch, costToEnd, items *uint32, dp *dpTables, i, cp int, best, baseCost uint32) (ni, ncp int, nbest uint32)
//
// simplePositions, as it is in Go: see there. SI, DI and R8 are the cache,
// the costs to the end and the items; R9 the costs; R10 the slots of the
// offsets; BX the position after the one worked on and CX its cache entry;
// R13 the cost so far.
TEXT ·simplePositionsAsm(SB), NOSPLIT, $0-76
	MOVQ cache+0(FP), SI
	MOVQ costToEnd+8(FP), DI
	MOVQ items+16(FP), R8
	MOVQ dp+24(FP), R9
	MOVQ i+32(FP), BX
	MOVQ cp+40(FP), CX
	MOVL best+48(FP), R13
	MOVQ ·offsetSlotFull(SB), R10

loop:
	TESTQ   BX, BX
	JLE     done
	CMPQ    CX, $3
	JLT     done
	MOVL    -4(SI)(CX*4), AX // the position's entry: how many matches, and its byte
	MOVL    -8(SI)(CX*4), DX // the entry before: the longest match, if there is one
	MOVWLZX AX, R11
	MOVWLZX DX, R12

	// No match, one of length 3, or two of lengths 3 and 4?
	SUBL R11, R12
	SUBL $2, R12   // 0 for those lengths
	MOVL R11, R15
	NEGL R15
	SBBL R15, R15  // all ones with matches
	ANDL R15, R12
	LEAL 1(R11), R14
	SHRL $2, R14   // not 0 with three matches or more
	ORL  R14, R12
	JNZ  second

	SHRL $16, AX           // the byte: the item, until a match costs less
	ADDL (R9)(AX*4), R13

	// The match of length 3 when there are two.
	MOVL    -12(SI)(CX*4), R12
	SHRL    $16, R12
	MOVBLZX (R10)(R12*1), R15
	MOVL    SLOT(R9)(R15*4), R15
	MOVL    8(DI)(BX*4), R14
	ADDL    (LEN+12)(R9), R14
	SHRL    $9, R14
	ADDL    R14, R15
	MOVL    R11, R14
	SHRL    $1, R14
	DECL    R14               // all ones without two matches
	ORL     R14, R15
	ORL     $(3<<23), R12
	CMPL    R15, R13
	CMOVLCS R15, R13
	CMOVLCS R12, AX

	// The longest match: of length 3, or 4 when there are two.
	SHRL    $16, DX
	MOVBLZX (R10)(DX*1), R15
	MOVL    SLOT(R9)(R15*4), R15
	LEAQ    (BX)(R11*1), R14
	MOVL    4(DI)(R14*4), R14
	ADDL    (LEN+8)(R9)(R11*4), R14
	SHRL    $9, R14
	ADDL    R14, R15
	CMPL    R11, $1
	SBBL    R14, R14          // all ones without a match
	ORL     R14, R15
	LEAL    2(R11), R12
	SHLL    $23, R12
	ORL     DX, R12
	CMPL    R15, R13
	CMOVLCS R15, R13
	CMOVLCS R12, AX

	DECQ BX
	SUBQ R11, CX
	DECQ CX

store:
	MOVL R13, R14
	ADDL baseCost+52(FP), R14
	SHLL $9, R14
	MOVL R14, (DI)(BX*4)
	MOVL AX, (R8)(BX*4)
	CMPL R13, $(1<<22) // the cost needs rebasing
	JCS  loop

done:
	MOVQ BX, ni+56(FP)
	MOVQ CX, ncp+64(FP)
	MOVL R13, nbest+72(FP)
	RET

second:
	// Up to four matches, none longer than 6? R12 is the longest, R14 the
	// number of matches less one, R15 the first match's entry.
	LEAL    -1(R11), R14
	CMPL    R14, $4
	JCC     done
	MOVWLZX DX, R12
	CMPL    R12, $6
	JHI     done
	SHRL    $16, AX
	ADDL    (R9)(AX*4), R13
	MOVQ    CX, R15
	SUBQ    R11, R15
	LEAQ    -4(SI)(R15*4), R15

	// Length 3: the first match's offset.
	MOVWLZX 2(R15), DX
	MATCH(3, 8)
	CMPL    R11, R13
	CMOVLCS R11, R13
	CMOVLCS DX, AX

	// Length 4: the second's when the first is of 3.
	MOVWLZX (R15), DX
	CMPL    DX, $4
	SBBL    DX, DX
	NEGL    DX
	CMPL    DX, R14
	CMOVLHI R14, DX
	MOVWLZX 2(R15)(DX*4), DX
	MATCH(4, 12)
	TAKE(4)

	// Length 5: after the matches shorter than 5.
	MOVWLZX (R15), DX
	CMPL    DX, $5
	SBBL    DX, DX
	MOVWLZX 4(R15), R11
	CMPL    R11, $5
	SBBL    R11, R11
	ADDL    R11, DX
	NEGL    DX
	CMPL    DX, R14
	CMOVLHI R14, DX
	MOVWLZX 2(R15)(DX*4), DX
	MATCH(5, 16)
	TAKE(5)

	// Length 6: the longest match's.
	MOVWLZX -6(SI)(CX*4), DX
	MATCH(6, 20)
	TAKE(6)

	DECQ BX
	LEAQ 2(R14), R11
	SUBQ R11, CX
	MOVQ items+16(FP), R8
	JMP  store
