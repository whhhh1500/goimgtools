//go:build amd64 && !purego

package resize

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 versions of libvips' reduce, which give the same
// bytes as the Go code. The purego build tag leaves them out.
var useSIMD = cpu.X86.HasAVX2

// reduceVSIMD is the reduce down of reduceRow: chunks times 16 bytes.
//
//go:noescape
func reduceVSIMD(dst *byte, rows **byte, pairs *uint32, npairs, chunks int)

// reduceHSIMD is the reduce across of reduceRow: count pixels.
//
//go:noescape
func reduceHSIMD(dst, row *byte, cols, pidx *int32, pairs *uint32, npairs, count int)
