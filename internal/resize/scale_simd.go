//go:build (amd64 || (arm64 && go1.27)) && !purego

package resize

// hpassSIMD is the horizontal pass of feed for n destination pixels: tmp[x]
// is the sum, over the contributions of sources[x] in their order, of
// row[coord-s0] times the weight, times invTotalWeightFFFF; its alpha is 1
// when ca.
//
//go:noescape
func hpassSIMD(tmp, row *[4]float64, s0 int, sources *source, contribs *contrib, n int, ca bool)

// vpassSIMD adds tmp[x] times w to acc[x] for the n pixels.
//
//go:noescape
func vpassSIMD(acc, tmp *[4]float64, n int, w float64)

// vpassGraySIMD adds tmp[x] times w to acc[x] for the n samples.
//
//go:noescape
func vpassGraySIMD(acc, tmp *float64, n int, w float64)

// rgbaRowSIMD is readRow of n pixels of an RGBA image at pix.
//
//go:noescape
func rgbaRowSIMD(out *[4]float64, pix *byte, n int)
