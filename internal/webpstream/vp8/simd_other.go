//go:build !amd64 || purego

package vp8

const useAVX2 = false

func filterH6(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterH4(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterV6(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
func filterV4(p, p2 *byte, stride, level, ilevel, hlevel int) { panic("unreachable") }
