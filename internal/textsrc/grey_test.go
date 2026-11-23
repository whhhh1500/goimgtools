package textsrc

import (
	"image"
	"image/color"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestGreyRows: text whose every colour is grey, on an opaque background,
// comes out as grey rows, the pixels of the RGBA ones; a colour, a colour
// glyph or a background that is not opaque keeps RGBA.
func TestGreyRows(t *testing.T) {
	defer func() { greyRows = true }()
	mono := []string{"../../tests/testdata/fonts/MonoTest.ttf"}
	white := color.Gray{255}
	render := func(text string, o Options, grey bool) (image.Image, color.Model) {
		t.Helper()
		greyRows = grey
		src, err := New(text, o, pipeline.NewEnv(0, ""))
		if err != nil {
			t.Fatal(err)
		}
		defer src.Close()
		m, err := pipeline.Collect(src)
		if err != nil {
			t.Fatal(err)
		}
		return m, src.ColorModel()
	}
	o := Options{Size: 40, Fonts: mono, Color: color.Gray{40}, Background: white, Width: 120,
		StrokeWidth: 2, StrokeColor: color.Gray{200}, ShadowColor: color.Black, ShadowX: 3, ShadowY: 2, ShadowBlur: 2}
	text := "AAA AA A AAAA"
	g, gm := render(text, o, true)
	c, cm := render(text, o, false)
	if _, is := g.(*image.Gray); !is || gm != color.GrayModel || cm != color.RGBAModel {
		t.Fatalf("grey text: %T, %v; without grey rows %v", g, gm, cm)
	}
	if g.Bounds() != c.Bounds() {
		t.Fatalf("bounds %v and %v", g.Bounds(), c.Bounds())
	}
	b := g.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if gr, rgba := g.(*image.Gray).GrayAt(x, y).Y, c.(*image.RGBA).RGBAAt(x, y); rgba != (color.RGBA{gr, gr, gr, 255}) {
				t.Fatalf("(%d,%d): grey %d, RGBA %v", x, y, gr, rgba)
			}
		}
	}
	for name, o := range map[string]Options{
		"coloured text":       {Size: 40, Fonts: mono, Color: color.RGBA{40, 41, 40, 255}, Background: white},
		"coloured outline":    {Size: 40, Fonts: mono, Background: white, StrokeWidth: 1, StrokeColor: color.RGBA{0, 0, 9, 255}},
		"coloured background": {Size: 40, Fonts: mono, Background: color.RGBA{255, 255, 250, 255}},
		"no background":       {Size: 40, Fonts: mono},
		"colour glyph":        {Size: 40, Fonts: []string{colourFont}, Background: white},
	} {
		text := "A"
		if name == "colour glyph" {
			text = "A✨"
		}
		if _, m := render(text, o, true); m != color.RGBAModel {
			t.Errorf("%s: %v", name, m)
		}
	}
}
