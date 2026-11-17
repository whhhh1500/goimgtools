//go:build !amd64 || purego

package webpstream

const useSIMD = false

func yuvRowAsm(dst, y, nu, fu, nv, fv *byte, chunks int) { panic("unreachable") }
