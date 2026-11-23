//go:build amd64 && !purego

package ops

import "golang.org/x/sys/cpu"

// useKernelSIMD selects the AVX2 passes of BlurLibvips, which give the
// same bytes as the Go code. The purego build tag leaves them out.
var useKernelSIMD = cpu.X86.HasAVX2

// kernelPrefixSIMD carries on the sums of row from pre: pre[4(i+1)+c] =
// pre[4i+c] + row[4i+c] for the n pixels of row.
//
//go:noescape
func kernelPrefixSIMD(pre *uint32, row *byte, n int)

// kernelHSIMD is the weighted sum of the boxes of hkernel.apply and its
// value for n bytes of dst (a multiple of 8, at least 8): box j is the
// sums hiOff[j] bytes past t less those loOff[j] past it, times wt[8j];
// alpha is ORed into each 8 bytes.
//
//go:noescape
func kernelHSIMD(dst *byte, t *uint32, hiOff, loOff *int64, wt *uint32, nb, n int, half, shift uint32, alpha uint64)

// kernelVSIMD is a row of vkernel's batch for n bytes (a multiple of 8, at
// least 8): the value of sum into out, alpha ORed into each 8 bytes, and
// then sum moved by each box j, wt[8j] times in[j] less gone[j], those
// rows read from off on.
//
//go:noescape
func kernelVSIMD(out *byte, sum *uint32, in, gone **byte, wt *uint32, nb, n, off int, half, shift uint32, alpha uint64)
