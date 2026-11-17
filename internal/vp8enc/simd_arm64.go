//go:build arm64 && go1.27 && !purego

package vp8enc

// useSIMD selects the NEON SSIM sums of the loop filter search, which give
// the same numbers as the Go code (Go 1.27 or later; not with the purego
// build tag).
var useSIMD = true

// ssimSumsSIMD sets y, yy and xy to winSums16 of b, b*b and a*b, a and b
// 16x16 planes.
//
//go:noescape
func ssimSumsSIMD(a, b *byte, y, yy, xy *[100]uint32)
