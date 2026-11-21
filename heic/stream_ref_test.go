//go:build (amd64 && !(noasm && amd64.v3)) || (arm64 && !noasm)

package heic

// wholeSame: here gen2brain/h265 converts the colours with SIMD, or in Go
// without fusing any multiplication and addition, which gives the pixels
// the stream's conversion gives on every processor: the stream is compared
// with its whole decoding.
const wholeSame = true
