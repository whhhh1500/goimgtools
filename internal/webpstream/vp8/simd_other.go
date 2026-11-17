//go:build !amd64 || purego

package vp8

const useSIMD = false

func filterH6(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterH4(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterV6(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterV4(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }

const SIMD = false

func InnerH(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func InnerV(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
