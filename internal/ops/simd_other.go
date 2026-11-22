//go:build !(amd64 || (arm64 && go1.27)) || purego

package ops

var useSIMD = false

var usePrefix = false

func hboxPrefixSIMD(pre *uint32, pad *byte, n int) { panic("unreachable") }

func hboxSumSIMD(dst, pad *byte, pre *uint32, w, ri int, ww, fw uint32, keepAlpha bool) {
	panic("unreachable")
}

func hboxRowSIMD(dst, src *byte, w, ri int, ww, fw uint32, keepAlpha bool) { panic("unreachable") }

func vboxRowSIMD(out, a, b, c *byte, sum *uint32, n int, ww, fw uint32) { panic("unreachable") }

func median3SIMD(dst, r0, r1, r2 *byte, n int) { panic("unreachable") }
