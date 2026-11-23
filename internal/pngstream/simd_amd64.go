//go:build amd64 && !purego

package pngstream

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 filters, which give the same rows and choices as
// the Go code. The purego build tag leaves them out.
var useSIMD = cpu.X86.HasAVX2

// filterRunSIMD is filterSIMD's filters over chunks times 32 bytes.
//
//go:noescape
func filterRunSIMD(cur, prev, up, sub, avg, paeth *byte, bpp, chunks int, sums *[5]uint64)

// chainsSIMD selects markChainsSIMD.
var chainsSIMD = cpu.X86.HasAVX2

// markChainsSIMD is markChains with AVX2, eight positions at a time (the
// positions before in the chains are gathered), for the n positions from d
// on, n a multiple of 8: their entries in prev are those from ring on,
// without going round its end, 20 bytes can be read at the last eight, and
// two bytes past each entry of prev. mask is the length of prev less one.
//
//go:noescape
func markChainsSIMD(d *byte, prev *uint16, mask, ring, window int, marks *uint8, n int)
