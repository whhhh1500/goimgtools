//go:build arm64 && go1.27 && !purego

package webpstream

// useSIMD selects the NEON version of the conversion of colour, which
// gives the same bytes as the Go code (Go 1.27 or later; not with the
// purego build tag).
var useSIMD = true

// yuvRowAsm is chunks times 32 pixels of yuvRowSIMD.
//
//go:noescape
func yuvRowAsm(dst, y, nu, fu, nv, fv *byte, chunks int)
