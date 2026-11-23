//go:build !(amd64 || (arm64 && go1.27)) || purego

package deflate

var useSIMD = false

func minPackedCostSIMD(lc, ct *uint32, n int) uint32 { panic("unreachable") }

func slideSIMD(p *int16, n int) { panic("unreachable") }

var dpAsm = false

func simplePositionsAsm(cache *lzMatch, costToEnd, items *uint32, dp *dpTables, i, cp int, best, baseCost uint32) (ni, ncp int, nbest uint32) {
	panic("unreachable")
}
