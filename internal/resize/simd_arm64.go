//go:build arm64 && go1.27 && !purego

package resize

// useSIMD selects the NEON versions of libvips' reduce, which give the same
// bytes as the Go code (Go 1.27 or later; not with the purego build tag).
var useSIMD = true

// reduceVSIMD is the reduce down of reduceRow: chunks times 16 bytes.
//
//go:noescape
func reduceVSIMD(dst *byte, rows **byte, pairs *uint32, npairs, chunks int)

// reduceHSIMD is the reduce across of reduceRow: count pixels.
//
//go:noescape
func reduceHSIMD(dst, row *byte, cols, pidx *int32, pairs *uint32, npairs, count int)
