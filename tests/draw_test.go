package tests

import (
	"bytes"
	"image"
	"image/color"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

func drawn(t *testing.T, m *Image) *image.NRGBA {
	t.Helper()
	img, err := m.ToImage()
	if err != nil {
		t.Fatal(err)
	}
	return nrgbaOf(img)
}

// TestDrawShapes: a rectangle on whole pixels paints exactly them; one on
// fractions paints the edge pixels by what they overlap; a circle covers
// its area; a stroke is a ring of its width; a translucent fill blends; the
// painting does not depend on how the image arrives in strips.
func TestDrawShapes(t *testing.T) {
	red := color.NRGBA{200, 20, 30, 255}
	m := drawn(t, Blank(40, 30, color.White).Draw(Rect(5, 6, 10, 8).Fill(red)))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			in := x >= 5 && x < 15 && y >= 6 && y < 14
			want := color.NRGBA{255, 255, 255, 255}
			if in {
				want = red
			}
			if got := m.NRGBAAt(x, y); got != want {
				t.Fatalf("pixel %d,%d: %v, want %v", x, y, got, want)
			}
		}
	}
	// Fractional edges: black on white, the grey of each pixel its share.
	m = drawn(t, Blank(20, 12, color.White).Draw(Rect(2.25, 3.5, 10.5, 5.75).Fill(color.Black)))
	for y := 0; y < 12; y++ {
		for x := 0; x < 20; x++ {
			ox := math.Max(0, math.Min(float64(x+1), 12.75)-math.Max(float64(x), 2.25))
			oy := math.Max(0, math.Min(float64(y+1), 9.25)-math.Max(float64(y), 3.5))
			want := 255 * (1 - ox*oy)
			if d := float64(m.NRGBAAt(x, y).R) - want; d > 1.5 || d < -1.5 {
				t.Fatalf("pixel %d,%d: %d, want %.1f", x, y, m.NRGBAAt(x, y).R, want)
			}
		}
	}
	// A circle's area; a ring's.
	dark := func(m *image.NRGBA) (s float64) {
		for i := 0; i < len(m.Pix); i += 4 {
			s += float64(255-m.Pix[i]) / 255
		}
		return s
	}
	m = drawn(t, Blank(100, 100, color.White).Draw(Circle(50, 50, 30).Fill(color.Black)))
	if a := dark(m); math.Abs(a-math.Pi*900) > math.Pi*900*0.003 {
		t.Errorf("circle: %.1f, want %.1f", a, math.Pi*900)
	}
	m = drawn(t, Blank(100, 100, color.White).Draw(Circle(50, 50, 30).Stroke(color.Black, 4)))
	if a, want := dark(m), math.Pi*(32*32-28*28); math.Abs(a-want) > want*0.01 {
		t.Errorf("ring: %.1f, want %.1f", a, want)
	}
	// Square corners stay square: the stroke of a rectangle on whole
	// pixels, 2 wide, is exactly the ring of pixels around it.
	m = drawn(t, Blank(30, 30, color.White).Draw(Rect(10, 10, 10, 10).Stroke(color.Black, 2)))
	for y := 0; y < 30; y++ {
		for x := 0; x < 30; x++ {
			ring := x >= 9 && x < 21 && y >= 9 && y < 21 && !(x >= 11 && x < 19 && y >= 11 && y < 19)
			if v := m.NRGBAAt(x, y).R; ring != (v == 0) || !ring && v != 255 {
				t.Fatalf("stroke pixel %d,%d: %d", x, y, v)
			}
		}
	}
	// A line between pixel centres, 1 wide, is a row of pixels.
	m = drawn(t, Blank(20, 10, color.White).Draw(Line(2, 4.5, 15, 4.5).Stroke(color.Black, 1)))
	for x := 0; x < 20; x++ {
		want := uint8(255)
		if x >= 2 && x < 15 {
			want = 0
		}
		if v := m.NRGBAAt(x, 4).R; v != want || m.NRGBAAt(x, 3).R != 255 || m.NRGBAAt(x, 5).R != 255 {
			t.Fatalf("line at %d: %d", x, v)
		}
	}
	// Half-transparent red over half-transparent blue.
	m = drawn(t, Blank(4, 4, color.NRGBA{0, 0, 255, 128}).Draw(Rect(0, 0, 4, 4).Fill(color.NRGBA{255, 0, 0, 128})))
	if got := m.NRGBAAt(1, 1); got.A != 192 || got.R < 168 || got.R > 172 || got.B < 83 || got.B > 87 {
		t.Errorf("blend: %v", got)
	}
	// The same painting whatever the strips.
	shapes := []Shape{Circle(31.3, 22.7, 17.5).Fill(red).Stroke(color.Black, 2.5), Polyline(3, 50, 30, 5, 60, 50).Stroke(color.NRGBA{0, 90, 200, 180}, 4)}
	src := testutil.Images(64, 53)["nrgba"]
	ref := drawn(t, FromImage(src).Draw(shapes...))
	for _, rows := range []int{1, 7, 64} {
		got := drawn(t, FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
			return testutil.ChoppedEnv(src, rows, env), nil
		}).Draw(shapes...))
		if !bytes.Equal(got.Pix, ref.Pix) {
			t.Errorf("strips of %d rows: another picture", rows)
		}
	}
}

// TestDrawErrors: shapes with nothing to paint or with bad sizes.
func TestDrawErrors(t *testing.T) {
	for i, s := range []Shape{
		Rect(0, 0, 10, 10),
		Rect(0, 0, -1, 10).Fill(color.Black),
		Line(0, 0, 5, 5),
		Line(0, 0, 5, 5).Stroke(color.Black, 0),
		Polygon(0, 0, 5, 5).Fill(color.Black),
		Polyline(1).Stroke(color.Black, 1),
		Circle(math.NaN(), 0, 3).Fill(color.Black),
		Rect(0, 0, 4, 4).Rounded(-1).Fill(color.Black),
	} {
		if _, err := Blank(10, 10, color.White).Draw(s).PNG().ToBytes(); err == nil {
			t.Errorf("shape %d: no error", i)
		}
	}
	// Shapes far outside the image change nothing.
	m := drawn(t, Blank(10, 10, color.White).Draw(Circle(-100, -100, 20).Fill(color.Black), Rect(50, 0, 10, 10).Fill(color.Black)))
	for i := 0; i < len(m.Pix); i++ {
		if m.Pix[i] != 255 {
			t.Fatal("a shape outside the image painted on it")
		}
	}
}
