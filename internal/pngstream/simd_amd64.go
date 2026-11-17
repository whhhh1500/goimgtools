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
