//go:build amd64 && !purego

package resize

import "golang.org/x/sys/cpu"

// useAVX2 selects the AVX2 versions of libvips' reduce, which give the same
// bytes as the Go code. The purego build tag leaves them out.
var useAVX2 = cpu.X86.HasAVX2

// reduceVAVX2 is the reduce down of reduceRow: chunks times 16 bytes.
//
//go:noescape
func reduceVAVX2(dst *byte, rows **byte, pairs *uint32, npairs, chunks int)

// reduceHAVX2 is the reduce across of reduceRow: count pixels.
//
//go:noescape
func reduceHAVX2(dst, row *byte, cols, pidx *int32, pairs *uint32, npairs, count int)
