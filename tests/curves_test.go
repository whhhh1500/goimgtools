package tests

import (
	"bytes"
	"image"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestCurves: the curve goes through its points, flat beyond them, and
// rises or falls between them as they do (no overshoot); no points is no
// change, a falling line is Negate; the curves of single channels come
// after the one of all three; transparency is kept; by name, and the words
// of the steps, give the same image.
func TestCurves(t *testing.T) {
	// A ramp of every level, as red, green and blue, alpha 200.
	ramp := image.NewNRGBA(image.Rect(0, 0, 256, 1))
	for v := 0; v < 256; v++ {
		copy(ramp.Pix[4*v:], []byte{byte(v), byte(v), byte(v), 200})
	}
	levels := func(points [][2]uint8, o ...CurvesOptions) []byte {
		t.Helper()
		return nrgbaOf(decodePNG(t, must(FromImage(ramp).Curves(points, o...).PNG().ToBytes()))).Pix
	}
	if got := levels(nil); !bytes.Equal(got, ramp.Pix) {
		t.Error("no points changed the levels")
	}
	neg := nrgbaOf(decodePNG(t, must(FromImage(ramp).Negate().PNG().ToBytes()))).Pix
	if got := levels([][2]uint8{{0, 255}, {255, 0}}); !bytes.Equal(got, neg) {
		t.Error("a falling line is not Negate")
	}
	pts := [][2]uint8{{30, 10}, {64, 40}, {192, 220}, {230, 250}}
	got := levels(pts)
	for i, p := range pts {
		if v := got[4*int(p[0])]; v != p[1] {
			t.Errorf("point %d: level %d becomes %d", i, p[0], v)
		}
	}
	for v := 0; v < 256; v++ {
		r := got[4*v]
		switch {
		case got[4*v+3] != 200:
			t.Fatalf("alpha changed at %d", v)
		case v < 30 && r != 10, v > 230 && r != 250:
			t.Fatalf("not flat beyond the points: %d becomes %d", v, r)
		case v > 0 && r < got[4*(v-1)]:
			t.Fatalf("the curve falls at %d (%d after %d)", v, r, got[4*(v-1)])
		}
	}
	// The red curve, after the curve of all three: a straight line to
	// half, so red is about half of what the first curve gives.
	red := [][2]uint8{{0, 0}, {255, 128}}
	both := levels(pts, CurvesOptions{Red: red})
	for v := 0; v < 256; v++ {
		want := float64(got[4*v]) * 128 / 255
		if both[4*v+1] != got[4*v+1] || float64(both[4*v]) < want-0.51 || float64(both[4*v]) > want+0.51 {
			t.Fatalf("red curve at %d: %v from %v", v, both[4*v:4*v+3], got[4*v:4*v+3])
		}
	}
	// By name.
	in := sourcePNG(t, "pillow/source.png")
	want := must(FromBytes(in).Curves(pts, CurvesOptions{Red: red}).PNG().ToBytes())
	var words []string
	byName := must(FromBytes(in).Do("curves", "30:10", "64:40", "192:220", "230:250", "red=0:0/255:128").Around(func(s Step, src pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		words = append([]string{s.Name}, s.Args...)
		return run(src)
	}).PNG().ToBytes())
	if !bytes.Equal(byName, want) || !bytes.Equal(must(FromBytes(in).Do(words[0], words[1:]...).PNG().ToBytes()), want) {
		t.Errorf("curves by name, words %v: another image", words)
	}
	for _, args := range [][]string{{"1:2", "1:3"}, {"300:2"}, {"cyan=0:0/255:255"}, {"red=0:0", "red=1:1"}, {"x"}} {
		if _, err := FromBytes(in).Do("curves", args...).PNG().ToBytes(); err == nil {
			t.Errorf("curves %v accepted", args)
		}
	}
}
