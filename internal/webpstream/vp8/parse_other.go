//go:build !(amd64 || arm64) || purego

package vp8

const parseAsm = false

func parseBlockAsm(r *partition, prob *[nBand][nContext][nProb]uint8, blk *[16]int16, first, ctx int, quant uint32) uint8 {
	panic("unreachable")
}
