//go:build amd64 && !purego

package webpstream

import "golang.org/x/sys/cpu"

// useAVX2 selects the AVX2 version of the conversion of colour, which gives
// the same bytes as the Go code. The purego build tag leaves it out.
var useAVX2 = cpu.X86.HasAVX2

// yuvRowAVX2 is chunks times 32 pixels of yuvRowSIMD: those of 17 chroma
// samples of each of nu, fu, nv and fv, 16 apart.
//
//go:noescape
func yuvRowAVX2(dst, y, nu, fu, nv, fv *byte, chunks int)
