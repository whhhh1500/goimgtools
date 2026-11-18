//go:build arm64 && go1.27 && !purego

package vp8l

// predSIMD selects the inverse predictors of pred_arm64.s (NEON, Go 1.27 or
// later), which give the bytes of the Go code. The purego build tag leaves
// them out.
var predSIMD = true

// predAdd0 to predAdd9 undo predictor 0 to 4, 8 and 9 for n pixels, a
// multiple of 4 and at least 4, at cur, under the row at top (see
// predictRun).
//
//go:noescape
func predAdd0(cur, top *byte, n int)

//go:noescape
func predAdd1(cur, top *byte, n int)

//go:noescape
func predAdd2(cur, top *byte, n int)

//go:noescape
func predAdd3(cur, top *byte, n int)

//go:noescape
func predAdd4(cur, top *byte, n int)

//go:noescape
func predAdd8(cur, top *byte, n int)

//go:noescape
func predAdd9(cur, top *byte, n int)

// predAdd holds them by mode; nil where the Go code does the work.
var predAdd = [16]func(cur, top *byte, n int){
	0: predAdd0, 1: predAdd1, 2: predAdd2, 3: predAdd3, 4: predAdd4,
	8: predAdd8, 9: predAdd9,
}
