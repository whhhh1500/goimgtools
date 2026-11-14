package tests

import (
	"image"
	"image/color"
	stddraw "image/draw"
	"os"
	"testing"

	"golang.org/x/image/font/gofont/goregular"

	. "github.com/whhhh1500/goimgtools"
)

// TestTextOutlineAndShadow checks where outline and shadow put their
// colours, that the image grows to hold them, and that several lines still
// come out as one consistent picture.
func TestTextOutlineAndShadow(t *testing.T) {
	font := [][]byte{goregular.TTF}
	white, black, red := color.RGBA{255, 255, 255, 255}, color.RGBA{0, 0, 0, 255}, color.RGBA{255, 0, 0, 255}
	count := func(m image.Image, c color.RGBA) int {
		n := 0
		b := m.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if color.NRGBAModel.Convert(m.At(x, y)) == (color.NRGBA{c.R, c.G, c.B, c.A}) {
					n++
				}
			}
		}
		return n
	}
	plain := textPNG(t, "Hi", TextOptions{FontData: font, Size: 80, Color: white})
	pw, ph := plain.Bounds().Dx(), plain.Bounds().Dy()

	// An outline: the image grows by its width on every side, the text
	// keeps its colour, and around it there is a ring of the outline colour.
	out := textPNG(t, "Hi", TextOptions{FontData: font, Size: 80, Color: white, StrokeWidth: 4, StrokeColor: black})
	if out.Bounds().Dx() != pw+8 || out.Bounds().Dy() != ph+8 {
		t.Fatalf("outlined: %v, plain %dx%d", out.Bounds().Size(), pw, ph)
	}
	inkPlain, whiteOut, blackOut := count(plain, white), count(out, white), count(out, black)
	if whiteOut < inkPlain*9/10 || whiteOut > inkPlain*11/10 {
		t.Errorf("outlined text has %d white pixels, plain text %d", whiteOut, inkPlain)
	}
	if blackOut < inkPlain/3 {
		t.Errorf("outline has only %d pixels around %d of text", blackOut, inkPlain)
	}
	// Every pixel of the plain text is covered (by text or outline) in the
	// outlined one, moved by the margin; and the outline reaches 4 pixels.
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			_, _, _, a := plain.At(x, y).RGBA()
			_, _, _, b := out.At(x+4, y+4).RGBA()
			if a > 0x8000 && b < 0xff00 {
				t.Fatalf("pixel (%d,%d) of the text is not solid in the outlined image", x, y)
			}
		}
	}
	far := 0
	for y := 0; y < out.Bounds().Dy(); y++ {
		for x := 0; x < out.Bounds().Dx(); x++ {
			if _, _, _, a := out.At(x, y).RGBA(); a == 0 {
				continue
			}
			// How near is the plain text? (Chebyshev distance, enough here.)
			near := false
			for dy := -5; dy <= 5 && !near; dy++ {
				for dx := -5; dx <= 5 && !near; dx++ {
					px, py := x-4+dx, y-4+dy
					if px >= 0 && py >= 0 && px < pw && py < ph {
						_, _, _, a := plain.At(px, py).RGBA()
						near = a > 0
					}
				}
			}
			if !near {
				far++
			}
		}
	}
	if far != 0 {
		t.Errorf("%d outline pixels lie further than 5 pixels from the text", far)
	}
	// A wide outline is as quick to make as a narrow one (the method does
	// not slow down with the width) and still correct in size.
	wide := textPNG(t, "Hi", TextOptions{FontData: font, Size: 80, Color: white, StrokeWidth: 30})
	if wide.Bounds().Dx() != pw+60 {
		t.Errorf("30 pixel outline: width %d", wide.Bounds().Dx())
	}

	// A hard shadow is the text again, in the shadow colour, moved.
	sh := textPNG(t, "Hi", TextOptions{FontData: font, Size: 80, Color: white, ShadowColor: red, ShadowX: 6, ShadowY: 3})
	if sh.Bounds().Dx() != pw+12 || sh.Bounds().Dy() != ph+12 {
		t.Fatalf("shadowed: %v, plain %dx%d", sh.Bounds().Size(), pw, ph)
	}
	miss := 0
	for y := 0; y < ph; y++ {
		for x := 0; x < pw; x++ {
			if _, _, _, a := plain.At(x, y).RGBA(); a != 0xffff {
				continue
			}
			// The shadow of this pixel is at +6,+3 (plus the margin of 6),
			// unless the text itself covers that spot.
			// Either way it is solid, and red unless white text is over it.
			c := color.NRGBAModel.Convert(sh.At(x+6+6, y+6+3)).(color.NRGBA)
			if c.A != 255 || c.R != 255 || c.G != c.B {
				miss++
			}
		}
	}
	if miss != 0 {
		t.Errorf("%d text pixels have neither shadow nor text where the shadow belongs", miss)
	}
	// A soft shadow has pixels of partial opacity; a glow has no offset.
	soft := textPNG(t, "Hi", TextOptions{FontData: font, Size: 80, Color: white, ShadowColor: black, ShadowBlur: 5})
	if soft.Bounds().Dx() != pw+20 {
		t.Errorf("glow: width %d, plain %d", soft.Bounds().Dx(), pw)
	}
	partial := 0
	for y := 0; y < soft.Bounds().Dy(); y++ {
		for x := 0; x < soft.Bounds().Dx(); x++ {
			if _, _, _, a := soft.At(x, y).RGBA(); a > 0x1000 && a < 0xe000 {
				partial++
			}
		}
	}
	if partial < 500 {
		t.Errorf("soft shadow has only %d half-transparent pixels", partial)
	}

	// Several lines: the effects of one line reach into the next, and the
	// picture does not depend on how it is cut into strips. Two lines drawn
	// together equal each line drawn alone, placed one above the other.
	o := TextOptions{FontData: font, Size: 40, Color: white, StrokeWidth: 3, StrokeColor: black, ShadowColor: red, ShadowX: 4, ShadowY: 4, LineSpacing: 0.9}
	both := textPNG(t, "Tyg\nÅjp", o)
	first, second := textPNG(t, "Tyg", o), textPNG(t, "Åjp", o)
	lineH := both.Bounds().Dy() - first.Bounds().Dy()
	ref := image.NewRGBA(image.Rect(0, 0, max(first.Bounds().Dx(), second.Bounds().Dx()), both.Bounds().Dy()))
	stddraw.Draw(ref, first.Bounds(), first, image.Point{}, stddraw.Over)
	stddraw.Draw(ref, second.Bounds().Add(image.Pt(0, lineH)), second, image.Point{}, stddraw.Over)
	if both.Bounds().Size() != ref.Bounds().Size() {
		t.Fatalf("two lines: %v, expected %v", both.Bounds().Size(), ref.Bounds().Size())
	}
	if d := meanDiff(both, ref); d > 0.01 {
		t.Errorf("two lines differ from the two lines drawn separately by %.3f levels", d)
	}
	// Many lines with a reach larger than a line: still the right height.
	tall := textPNG(t, "a\nb\nc\nd\ne\nf", TextOptions{FontData: font, Size: 12, ShadowColor: black, ShadowY: 40})
	one := textPNG(t, "a", TextOptions{FontData: font, Size: 12})
	if tall.Bounds().Dy() != 6*one.Bounds().Dy()+80 {
		t.Errorf("6 lines with a shadow 40 below: height %d, a line is %d", tall.Bounds().Dy(), one.Bounds().Dy())
	}

	for what, bad := range map[string]TextOptions{
		"negative outline": {FontData: font, StrokeWidth: -1},
		"huge outline":     {FontData: font, StrokeWidth: 500},
		"huge blur":        {FontData: font, ShadowColor: black, ShadowBlur: 900},
		"huge offset":      {FontData: font, ShadowColor: black, ShadowX: 5000},
	} {
		if _, err := Text("x", bad).ToBytes(); err == nil {
			t.Errorf("%s accepted", what)
		}
	}

	if out := os.Getenv("GOIMG_FX_OUT"); out != "" {
		base := image.NewRGBA(image.Rect(0, 0, 900, 500))
		for y := 0; y < 500; y++ {
			for x := 0; x < 900; x++ {
				base.SetRGBA(x, y, color.RGBA{uint8(40 + x/5), uint8(90 + y/4), uint8(200 - x/6), 255})
			}
		}
		err := FromBytes(pngBytes(t, base)).
			Composite(Text("© goimgtools 水印", TextOptions{Size: 26, Color: white}),
				CompositeOptions{Tile: true, TileGapX: 60, TileGapY: 50, Angle: -30, Opacity: 0.25, LeftRatio: 0.5, TopRatio: 0.5, Anchor: Center}).
			Composite(Text("描边 Outline", TextOptions{Size: 64, Color: white, StrokeWidth: 4, StrokeColor: color.RGBA{20, 20, 20, 255}}),
				CompositeOptions{Left: 40, Top: 40}).
			Composite(Text("阴影 Shadow", TextOptions{Size: 64, Color: color.RGBA{255, 220, 60, 255}, ShadowColor: color.RGBA{0, 0, 0, 200}, ShadowX: 5, ShadowY: 5, ShadowBlur: 4}),
				CompositeOptions{Left: 40, Top: 170}).
			Composite(Text("发光 Glow", TextOptions{Size: 64, Color: white, ShadowColor: color.RGBA{0, 255, 255, 255}, ShadowBlur: 8}),
				CompositeOptions{Left: 40, Top: 310}).
			ToFile(out)
		if err != nil {
			t.Fatal(err)
		}
	}
}

// TestCompositeOpacityAndTile checks the overall transparency of an overlay
// and its repetition over the image.
func TestCompositeOpacityAndTile(t *testing.T) {
	const W, H = 200, 120
	base := image.NewRGBA(image.Rect(0, 0, W, H))
	stddraw.Draw(base, base.Bounds(), image.NewUniform(color.RGBA{0, 0, 200, 255}), image.Point{}, stddraw.Src)
	in := pngBytes(t, base)
	logo := image.NewRGBA(image.Rect(0, 0, 30, 20))
	for y := 0; y < 20; y++ {
		for x := 0; x < 30; x++ {
			logo.SetRGBA(x, y, color.RGBA{uint8(200 + x), uint8(10 * y), 0, 255})
		}
	}
	logoPNG := pngBytes(t, logo)
	run := func(o CompositeOptions) image.Image {
		t.Helper()
		got, err := FromBytes(in).Composite(FromBytes(logoPNG), o).PNG().ToBytes()
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		return decodePNG(t, got)
	}

	// Opacity: every covered pixel is that share of logo over the base.
	for _, op := range []float64{0.25, 0.5, 1} {
		got := run(CompositeOptions{Left: 50, Top: 40, Opacity: op})
		for y := 0; y < H; y++ {
			for x := 0; x < W; x++ {
				want := base.RGBAAt(x, y)
				if x >= 50 && x < 80 && y >= 40 && y < 60 {
					l := logo.RGBAAt(x-50, y-40)
					mix := func(a, b uint8) uint8 { return uint8(float64(a)*op + float64(b)*(1-op) + 0.5) }
					want = color.RGBA{mix(l.R, want.R), mix(l.G, want.G), mix(l.B, want.B), 255}
				}
				g := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
				if d := max(int(g.R)-int(want.R), int(want.R)-int(g.R), int(g.G)-int(want.G), int(want.G)-int(g.G), int(g.B)-int(want.B), int(want.B)-int(g.B)); d > 2 {
					t.Fatalf("opacity %g: pixel (%d,%d) = %v, want about %v", op, x, y, g, want)
				}
			}
		}
	}
	sameImage(t, "opacity 0 means 1", run(CompositeOptions{Left: 50, Top: 40}), run(CompositeOptions{Left: 50, Top: 40, Opacity: 1}))

	// Tiling: a copy at the given place and one every width+gap, height+gap,
	// cut off at the edges; gaps show the base.
	for _, c := range []struct{ x, y, gx, gy int }{{0, 0, 0, 0}, {50, 40, 10, 5}, {7, 3, 25, 0}, {-5, 115, 3, 9}, {50, 40, -10, -5}} {
		got := run(CompositeOptions{Left: c.x, Top: c.y, Tile: true, TileGapX: c.gx, TileGapY: c.gy})
		ref := image.NewRGBA(base.Bounds())
		copy(ref.Pix, base.Pix)
		sx, sy := 30+c.gx, 20+c.gy
		for j := -20; j <= 20; j++ {
			for i := -20; i <= 20; i++ {
				at := logo.Bounds().Add(image.Pt(c.x+i*sx, c.y+j*sy))
				stddraw.Draw(ref, at, logo, image.Point{}, stddraw.Over)
			}
		}
		sameImage(t, "tiled", got, ref)
	}
	// Tiled, turned and faded at once: copies everywhere, none opaque.
	got := run(CompositeOptions{LeftRatio: 0.5, TopRatio: 0.5, Anchor: Center, Angle: 45, Tile: true, TileGapX: 4, TileGapY: 4, Opacity: 0.5})
	changed := 0
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			g := color.RGBAModel.Convert(got.At(x, y)).(color.RGBA)
			if g != base.RGBAAt(x, y) {
				changed++
				if g.B < 90 { // half of the base's blue always remains
					t.Fatalf("pixel (%d,%d) = %v: the overlay is not half transparent there", x, y, g)
				}
			}
		}
	}
	if changed < W*H/4 {
		t.Errorf("tiled and turned: only %d of %d pixels changed", changed, W*H)
	}

	for _, bad := range []CompositeOptions{{Opacity: -0.1}, {Opacity: 1.5}, {Tile: true, TileGapX: -30}, {Tile: true, TileGapY: -99}} {
		if _, err := FromBytes(in).Composite(FromBytes(logoPNG), bad).PNG().ToBytes(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
