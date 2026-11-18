//go:build !go1.27

package pngstream

// kflateSame reports that klauspost/compress's flate writes the bytes
// compress/flate writes: not Go 1.26's, which compresses every block.
const kflateSame = false
