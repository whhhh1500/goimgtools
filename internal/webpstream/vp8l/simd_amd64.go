//go:build amd64 && !purego

package vp8l

import "golang.org/x/sys/cpu"

// predSIMD selects the inverse predictors of pred_amd64.s (AVX), which give
// the bytes of the Go code. The purego build tag leaves them out.
var predSIMD = cpu.X86.HasAVX2

// predAdd0 to predAdd12 undo predictor 0 to 12 (not 5, 6, 7) for n pixels,
// a multiple of 4 and at least 4, at cur, under the row at top (see
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

//go:noescape
func predAdd10(cur, top *byte, n int)

//go:noescape
func predAdd11(cur, top *byte, n int)

//go:noescape
func predAdd12(cur, top *byte, n int)

// predAdd holds them by mode; nil where the Go code does the work.
var predAdd = [16]func(cur, top *byte, n int){
	0: predAdd0, 1: predAdd1, 2: predAdd2, 3: predAdd3, 4: predAdd4,
	8: predAdd8, 9: predAdd9, 10: predAdd10, 11: predAdd11, 12: predAdd12,
}
