//go:build (amd64 || (arm64 && go1.27)) && !purego

package vp8enc

// filterPairsSIMD is the loop of filterRow over n pairs (a multiple of 8):
// a and b at the first pair's chroma, wy and out at its first sample.
//
//go:noescape
func filterPairsSIMD(a, b *int16, wy, out *uint16, n int)

// updateBySIMD is the correction of W of sharpYUV.pass over n samples (a
// multiple of 16).
//
//go:noescape
func updateBySIMD(by, ty, y *uint16, n int)

// updateUVSIMD is the correction of chroma of sharpYUV.pass over n samples
// (a multiple of 16).
//
//go:noescape
func updateUVSIMD(cur, tuv, uv *int16, n int)
