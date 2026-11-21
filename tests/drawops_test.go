package tests

import (
	"bytes"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestDrawOps: the shapes and Bandbool by name are the methods'; bad
// arguments are errors.
func TestDrawOps(t *testing.T) {
	base := func() *Image { return Blank(120, 90, color.NRGBA{40, 120, 200, 255}) }
	for _, c := range []struct {
		name string
		args []string
		want *Image
	}{
		{"rect", []string{"10", "12", "50", "30", "fill=#ffcc00", "stroke=black", "width=3", "radius=6"},
			base().Draw(Rect(10, 12, 50, 30).Fill(color.NRGBA{255, 204, 0, 255}).Stroke(color.Black, 3).Rounded(6))},
		{"ellipse", []string{"60", "45", "30", "15", "stroke=#fff", "width=2.5"}, base().Draw(Ellipse(60, 45, 30, 15).Stroke(color.White, 2.5))},
		{"circle", []string{"30", "30", "20", "fill=#ff000080"}, base().Draw(Circle(30, 30, 20).Fill(color.NRGBA{255, 0, 0, 128}))},
		{"polygon", []string{"10", "10", "100", "20", "50", "80", "fill=white"}, base().Draw(Polygon(10, 10, 100, 20, 50, 80).Fill(color.White))},
		{"line", []string{"5", "5", "60", "80", "115", "5"}, base().Draw(Polyline(5, 5, 60, 80, 115, 5).Stroke(color.Black, 1))},
		{"line", []string{"5", "5", "60", "80", "stroke=white", "width=4"}, base().Draw(Line(5, 5, 60, 80).Stroke(color.White, 4))},
		{"bandbool", []string{"eor"}, base().Bandbool(BoolEor)},
	} {
		got, err := base().Do(c.name, c.args...).PNG().ToBytes()
		if err != nil {
			t.Fatalf("%s %v: %v", c.name, c.args, err)
		}
		if want := must(c.want.PNG().ToBytes()); !bytes.Equal(got, want) {
			t.Errorf("%s %v: not the method's picture", c.name, c.args)
		}
	}
	for _, bad := range [][]string{
		{"rect", "1", "2", "3", "4"},                  // nothing to paint
		{"rect", "1", "2", "3", "fill=white"},         // too few numbers
		{"rect", "fill=white", "1", "2", "3", "4"},    // numbers after a setting
		{"circle", "1", "2", "3", "colour=white"},     // no such setting
		{"line", "1", "2", "3", "4", "fill=white"},    // a line has no fill
		{"circle", "1", "2", "3", "fill=nocolour"},    // not a colour
		{"polygon", "1", "2", "3", "4", "fill=white"}, // two points
		{"bandbool", "xor"},                           // not an operation
	} {
		if _, err := Blank(10, 10, color.White).Do(bad[0], bad[1:]...).PNG().ToBytes(); err == nil {
			t.Errorf("%v: no error", bad)
		}
	}
}
