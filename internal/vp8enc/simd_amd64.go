//go:build amd64 && !purego

package vp8enc

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 SSIM sums of the loop filter search, which give
// the same numbers as the Go code. The purego build tag leaves them out.
var useSIMD = cpu.X86.HasAVX2

// ssimSumsSIMD sets y, yy and xy to winSums16 of b, b*b and a*b, a and b
// 16x16 planes.
//
//go:noescape
func ssimSumsSIMD(a, b *byte, y, yy, xy *[100]uint32)
