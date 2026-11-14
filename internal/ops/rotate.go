package ops

import (
	"image"
	"math"

	"golang.org/x/image/draw"
	"golang.org/x/image/math/f64"
)

// RotateOverlay turns the image top by deg degrees clockwise around its
// point (px, py) (in pixels from its top-left corner; any point, also one
// outside it) and returns the turned picture together with where its
// top-left corner goes when that point is to sit at (cx, cy) of the image
// underneath. cx and cy may fall between pixels: the fraction is worked into
// the resampling.
//
// The result is as large as the turned picture's bounding box, transparent
// where top does not reach, with soft edges.
func RotateOverlay(top image.Image, deg, px, py, cx, cy float64) (*image.RGBA, int, int) {
	tb := top.Bounds()
	w, h := float64(tb.Dx()), float64(tb.Dy())
	sin, cos := math.Sincos(deg * math.Pi / 180)
	// With y pointing down, a clockwise turn takes (x, y) to
	// (x cos - y sin, x sin + y cos).
	at := func(u, v float64) (float64, float64) {
		u, v = u-px, v-py
		return cx + u*cos - v*sin, cy + u*sin + v*cos
	}
	minX, minY := math.Inf(1), math.Inf(1)
	maxX, maxY := math.Inf(-1), math.Inf(-1)
	// One transparent pixel around top takes part, so its edges come out
	// smooth instead of jagged.
	for _, c := range [][2]float64{{-1, -1}, {w + 1, -1}, {-1, h + 1}, {w + 1, h + 1}} {
		x, y := at(c[0], c[1])
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	x0, y0 := int(math.Floor(minX)), int(math.Floor(minY))
	x1, y1 := int(math.Ceil(maxX)), int(math.Ceil(maxY))

	padded := image.NewRGBA(image.Rect(0, 0, tb.Dx()+2, tb.Dy()+2))
	draw.Draw(padded, tb.Sub(tb.Min).Add(image.Pt(1, 1)), top, tb.Min, draw.Src)

	out := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	// A point (sx, sy) of padded is point (sx-1, sy-1) of top.
	ox, oy := at(-1, -1)
	m := f64.Aff3{
		cos, -sin, ox - float64(x0),
		sin, cos, oy - float64(y0),
	}
	draw.CatmullRom.Transform(out, m, padded, padded.Bounds(), draw.Src, nil)
	return out, x0, y0
}
