package raster

import (
	"bytes"
	"math"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/trig"
)

func render(p *Path, x0, y0, w, h int) []uint8 {
	dst := make([]uint8, w*h)
	var c Cells
	p.Render(dst, w, x0, y0, w, h, &c)
	return dst
}

// TestRectangle: each pixel covers what it overlaps of a rectangle with
// fractional edges, to within a level; either direction of the contour.
func TestRectangle(t *testing.T) {
	const x0, y0, x1, y1 = 2.3, 1.75, 9.6, 6.125
	for _, xy := range [][]float64{{x0, y0, x1, y0, x1, y1, x0, y1}, {x0, y0, x0, y1, x1, y1, x1, y0}} {
		var p Path
		p.Add(xy)
		got := render(&p, 0, 0, 12, 8)
		for y := 0; y < 8; y++ {
			for x := 0; x < 12; x++ {
				ox := math.Max(0, math.Min(float64(x+1), x1)-math.Max(float64(x), x0))
				oy := math.Max(0, math.Min(float64(y+1), y1)-math.Max(float64(y), y0))
				want := ox * oy * 255
				if d := float64(got[y*12+x]) - want; d > 1.01 || d < -1.01 {
					t.Fatalf("pixel %d,%d: %d, want %.1f", x, y, got[y*12+x], want)
				}
			}
		}
	}
}

// circle is a circle as a polygon of n sides.
func circle(cx, cy, r float64, n int, reverse bool) []float64 {
	var xy []float64
	for i := 0; i < n; i++ {
		k := i
		if reverse {
			k = n - i
		}
		xy = append(xy, cx+r*trig.CosPi(2*k, n), cy+r*trig.SinPi(2*k, n))
	}
	return xy
}

// TestBands: a picture rendered a row, a few rows, or all at a time, and
// for a part of its columns, is the same.
func TestBands(t *testing.T) {
	var p Path
	p.Add(circle(20.3, 15.7, 13.2, 97, false))
	p.Add([]float64{-5, 3, 40.5, 9.25, 7, 31.8}) // a triangle across it, partly outside
	whole := render(&p, 0, 0, 41, 33)
	var c Cells
	for _, band := range []int{1, 3, 7} {
		got := make([]uint8, 41*33)
		for y := 0; y < 33; y += band {
			h := min(band, 33-y)
			p.Render(got[y*41:], 41, 0, y, 41, h, &c)
		}
		if !bytes.Equal(got, whole) {
			t.Fatalf("in bands of %d rows: another picture", band)
		}
	}
	// Columns 10 to 29 only.
	part := make([]uint8, 20*33)
	p.Render(part, 20, 10, 0, 20, 33, &c)
	for y := 0; y < 33; y++ {
		if !bytes.Equal(part[y*20:(y+1)*20], whole[y*41+10:y*41+30]) {
			t.Fatalf("row %d of columns 10 to 29 differs", y)
		}
	}
}

// TestArea: a circle covers its area, and a ring (a reversed circle
// inside) the difference.
func TestArea(t *testing.T) {
	sum := func(b []uint8) (s float64) {
		for _, v := range b {
			s += float64(v)
		}
		return s / 255
	}
	var p Path
	p.Add(circle(50, 50, 30, 400, false))
	if a := sum(render(&p, 0, 0, 100, 100)); math.Abs(a-math.Pi*900)/(math.Pi*900) > 0.003 {
		t.Errorf("circle: %.1f, want %.1f", a, math.Pi*900)
	}
	p.Add(circle(50, 50, 20, 400, true))
	want := math.Pi * (900 - 400)
	got := render(&p, 0, 0, 100, 100)
	if a := sum(got); math.Abs(a-want)/want > 0.004 {
		t.Errorf("ring: %.1f, want %.1f", a, want)
	}
	if got[50*100+50] != 0 || got[50*100+25] != 255 {
		t.Errorf("ring: centre %d, on it %d", got[50*100+50], got[50*100+25])
	}
	// The same direction twice is still full, not more.
	var q Path
	q.Add([]float64{0, 0, 4, 0, 4, 4, 0, 4})
	q.Add([]float64{0, 0, 4, 0, 4, 4, 0, 4})
	if b := render(&q, 0, 0, 4, 4); b[5] != 255 {
		t.Errorf("twice: %d", b[5])
	}
}

// TestFarOut: coordinates far outside are held and do not overflow.
func TestFarOut(t *testing.T) {
	var p Path
	p.Add([]float64{-1e300, -1e300, 1e300, -1e300, 1e300, 1e300, math.NaN(), 1e300})
	b := render(&p, 0, 0, 8, 8)
	for _, v := range b {
		if v != 255 && v != 0 {
			t.Fatalf("coverage %d far inside a huge shape", v)
		}
	}
}
