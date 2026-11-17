//go:build arm64 && go1.27 && !purego

package pngstream

// useSIMD selects the NEON filters, which give the same rows and choices as
// the Go code (Go 1.27 or later; not with the purego build tag).
var useSIMD = true

// filterRunSIMD is filterSIMD's filters over chunks times 32 bytes.
//
//go:noescape
func filterRunSIMD(cur, prev, up, sub, avg, paeth *byte, bpp, chunks int, sums *[5]uint64)
