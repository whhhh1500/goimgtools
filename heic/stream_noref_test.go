//go:build !((amd64 && !(noasm && amd64.v3)) || (arm64 && !noasm))

package heic

// wholeSame: here gen2brain/h265 converts the colours in Go, which may fuse
// a multiplication and an addition and round differently from the stream:
// the stream is held to its sums alone.
const wholeSame = false
