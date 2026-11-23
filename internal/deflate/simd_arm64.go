//go:build arm64 && go1.27 && !purego

package deflate

// useSIMD selects the NEON code, which computes what the Go code does (Go
// 1.27 or later; not with the purego build tag).
var useSIMD = true

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

var dpAsm = false

func simplePositionsAsm(cache *lzMatch, costToEnd, items *uint32, dp *dpTables, i, cp int, best, baseCost uint32) (ni, ncp int, nbest uint32) {
	panic("unreachable")
}
