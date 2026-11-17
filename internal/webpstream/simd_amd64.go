//go:build amd64 && !purego

package webpstream

import "golang.org/x/sys/cpu"

// useSIMD selects the AVX2 version of the conversion of colour, which gives
// the same bytes as the Go code. The purego build tag leaves it out.
var useSIMD = cpu.X86.HasAVX2

// yuvRowAsm is chunks times 32 pixels of yuvRowSIMD: those of 17 chroma
// samples of each of nu, fu, nv and fv, 16 apart.
//
//go:noescape
func yuvRowAsm(dst, y, nu, fu, nv, fv *byte, chunks int)
