//go:build !go1.27

package pngstream

// DefaultLevel is the compression level used when none is given. With Go
// 1.26's compress/flate, level 7 is only 1 to 1.5% smaller than level 6
// and takes half as long again.
const DefaultLevel = 6
