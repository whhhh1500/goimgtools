//go:build amd64 && !purego

package vp8enc

import "golang.org/x/sys/cpu"

// useAVX2 selects the AVX2 SSIM sums of the loop filter search, which give
// the same numbers as the Go code. The purego build tag leaves them out.
var useAVX2 = cpu.X86.HasAVX2

// ssimSumsAVX2 sets y, yy and xy to winSums16 of b, b*b and a*b, a and b
// 16x16 planes.
//
//go:noescape
func ssimSumsAVX2(a, b *byte, y, yy, xy *[100]uint32)
