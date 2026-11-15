//go:build go1.27

package pngstream

// DefaultLevel is the compression level used when none is given. Go 1.27's
// compress/flate finds far fewer repeats at level 6 than zlib does, and
// level 7 makes smooth images about five times smaller.
const DefaultLevel = 7
