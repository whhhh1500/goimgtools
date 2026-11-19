//go:build !go1.27

package pngstream

// gflateSame reports that internal/gflate, Go 1.27's compress/flate,
// writes the bytes the compress/flate built with writes: not Go 1.26's,
// which compresses every block anew, as png.Encode compresses its data.
const gflateSame = false
