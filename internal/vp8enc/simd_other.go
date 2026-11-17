//go:build !amd64 || purego

package vp8enc

const useSIMD = false

func ssimSumsSIMD(a, b *byte, y, yy, xy *[100]uint32) { panic("unreachable") }
