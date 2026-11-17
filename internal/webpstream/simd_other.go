//go:build !(amd64 || (arm64 && go1.27)) || purego

package webpstream

const useSIMD = false

func yuvRowAsm(dst, y, nu, fu, nv, fv *byte, chunks int) { panic("unreachable") }
