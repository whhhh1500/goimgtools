//go:build go1.27

package pngstream

// gflateSame reports that internal/gflate, Go 1.27's compress/flate,
// writes the bytes the compress/flate built with writes, so that it can
// compress data of more than one block (see zBlocks).
const gflateSame = true
