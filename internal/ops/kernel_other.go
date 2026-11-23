//go:build !amd64 || purego

package ops

var useKernelSIMD = false

func kernelPrefixSIMD(pre *uint32, row *byte, n int) { panic("unreachable") }

func kernelHSIMD(dst *byte, t *uint32, hiOff, loOff *int64, wt *uint32, nb, n int, half, shift uint32, alpha uint64) {
	panic("unreachable")
}

func kernelVSIMD(out *byte, sum *uint32, in, gone **byte, wt *uint32, nb, n, off int, half, shift uint32, alpha uint64) {
	panic("unreachable")
}
