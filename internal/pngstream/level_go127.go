//go:build go1.27

package pngstream

// kflateSame reports that klauspost/compress's flate writes the bytes
// compress/flate writes (Go 1.27's was ported from it), so that it can
// compress data of more than one block (see zBlocks).
const kflateSame = true
