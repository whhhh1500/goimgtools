//go:build !(amd64 || arm64) || purego

package vp8

const parseAsm = false

type asmState struct {
	value uint64
	bits  int64
	rng   uint64
	r     int64
}

func parseBlockAsm(st *asmState, buf *byte, prob *[nBand][nContext][nProb]uint8, blk *[16]int16, first, ctx int, quant uint32) uint8 {
	panic("unreachable")
}
