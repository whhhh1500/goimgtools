//go:build amd64 && !purego

package deflate

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 code, which computes what the Go code does. The
// purego build tag leaves it out.
var useSIMD = cpu.X86.HasAVX2

// minPackedCostSIMD is minPackedCost of the n numbers at lc and at ct, n at
// least 4.
//
//go:noescape
func minPackedCostSIMD(lc, ct *uint32, n int) uint32

// slideSIMD is btMatchfinder.slide for the n positions at p, n a multiple
// of 32.
//
//go:noescape
func slideSIMD(p *int16, n int)

// dpAsm selects simplePositionsAsm, simplePositions in assembly: the same
// costs and choices in half the instructions (the Go compiler keeps the
// loop's values in memory and checks every index).
var dpAsm = true

//go:noescape
func simplePositionsAsm(cache *lzMatch, costToEnd, items *uint32, dp *dpTables, i, cp int, best, baseCost uint32) (ni, ncp int, nbest uint32)
