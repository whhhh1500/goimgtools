//go:build !(amd64 || (arm64 && go1.27)) || purego

package vp8enc

const useSIMD = false

func ssimSumsSIMD(a, b *byte, y, yy, xy *[100]uint32) { panic("unreachable") }
