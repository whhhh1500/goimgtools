//go:build amd64 && !purego

package ops

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 box blur passes, which give the same bytes as
// the Go code. The purego build tag leaves them out.
var useSIMD = cpu.X86.HasAVX2

// hboxRowSIMD is hbox.pass of a row of w pixels for all four channels, or
// for the colours only (the alpha of dst left as it is) when keepAlpha.
//
//go:noescape
func hboxRowSIMD(dst, src *byte, w, ri int, ww, fw uint32, keepAlpha bool)

// vboxRowSIMD is the inner loop of vbox.nextRow for n bytes, n a multiple
// of 8.
//
//go:noescape
func vboxRowSIMD(out, a, b, c *byte, sum *uint32, n int, ww, fw uint32)

// median3SIMD is median3Row for the n pixels (a multiple of 8) from the
// second on, dst at the second pixel's place, the rows r0..r2 at the
// first's; each row must hold the pixel after the last.
//
//go:noescape
func median3SIMD(dst, r0, r1, r2 *byte, n int)
