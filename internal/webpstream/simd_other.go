//go:build !amd64 || purego

package webpstream

const useAVX2 = false

func yuvRowAVX2(dst, y, nu, fu, nv, fv *byte, chunks int) { panic("unreachable") }
