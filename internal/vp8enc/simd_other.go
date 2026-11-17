//go:build !amd64 || purego

package vp8enc

const useAVX2 = false

func ssimSumsAVX2(a, b *byte, y, yy, xy *[100]uint32) { panic("unreachable") }
