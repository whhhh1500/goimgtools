package tests

import (
	"image"
	"image/color"
	stddraw "image/draw"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestCompositePlacement covers the overlay position by ratio, the choice
// of anchor, and turning the overlay around a pivot.
func TestCompositePlacement(t *testing.T) {
	const W, H = 301, 200
	base := image.NewRGBA(image.Rect(0, 0, W, H))
	stddraw.Draw(base, base.Bounds(), image.NewUniform(color.RGBA{30, 60, 90, 255}), image.Point{}, stddraw.Src)
	in := pngBytes(t, base)
	// A logo that is not symmetric in any way: a gradient with one corner marked.
	const lw, lh = 40, 20
	logo := image.NewRGBA(image.Rect(0, 0, lw, lh))
	for y := 0; y < lh; y++ {
		for x := 0; x < lw; x++ {
			logo.SetRGBA(x, y, color.RGBA{uint8(100 + 3*x), uint8(250 - 6*y), 200, 255})
		}
	}
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			logo.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
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
	// placed is the base with over drawn with its top-left corner at (x, y).
	placed := func(over image.Image, x, y int) image.Image {
		ref := image.NewRGBA(base.Bounds())
		copy(ref.Pix, base.Pix)
		ob := over.Bounds()
		stddraw.Draw(ref, ob.Sub(ob.Min).Add(image.Pt(x, y)), over, ob.Min, stddraw.Over)
		return ref
	}
	round := func(v float64) int { return int(math.Floor(v + 0.5)) }

	// The logo's top-left corner a third of the way across and down.
	sameImage(t, "ratio", run(CompositeOptions{LeftRatio: 1.0 / 3, TopRatio: 1.0 / 3}),
		placed(logo, round(W/3.0), round(H/3.0)))
	// Ratio and pixels add up; the ratio is counted from the gravity point.
	sameImage(t, "ratio from the far corner", run(CompositeOptions{Gravity: SouthEast, LeftRatio: -0.25, TopRatio: -0.5, Left: -3, Top: 2}),
		placed(logo, round(W-0.25*W-3-lw), round(H-0.5*H+2-lh)))
	// The logo's centre on the centre of the image.
	sameImage(t, "centre on centre", run(CompositeOptions{LeftRatio: 0.5, TopRatio: 0.5, Anchor: Center}),
		placed(logo, round(W/2.0-lw/2.0), round(H/2.0-lh/2.0)))
	// Each named anchor, and a point given in pixels, on the pixel (120, 80).
	for a := NorthWest; a <= SouthEast; a++ {
		i := int(a - NorthWest)
		sameImage(t, "anchor", run(CompositeOptions{Left: 120, Top: 80, Anchor: a}),
			placed(logo, 120-lw*(i%3)/2, 80-lh*(i/3)/2))
	}
	sameImage(t, "anchor point", run(CompositeOptions{Left: 120, Top: 80, Anchor: CustomPosition, AnchorX: 7, AnchorY: 13}),
		placed(logo, 113, 67))
	// The same nine names serve the image, the anchor and the pivot; and
	// leaving Gravity out means the top-left corner.
	sameImage(t, "gravity given", run(CompositeOptions{Gravity: NorthWest, Left: 9, Top: 4}), run(CompositeOptions{Left: 9, Top: 4}))
	sameImage(t, "anchor like gravity", run(CompositeOptions{Gravity: SouthEast, Anchor: SouthEast, Left: -5, Top: -6}),
		run(CompositeOptions{Gravity: SouthEast, Left: -5, Top: -6}))
	sameImage(t, "pivot like anchor", run(CompositeOptions{Left: 150, Top: 100, Anchor: Center, Pivot: Center, Angle: 90}),
		run(CompositeOptions{Left: 150, Top: 100, Anchor: Center, Angle: 90}))
	// Gravity alone keeps meaning what it did.
	sameImage(t, "gravity", run(CompositeOptions{Gravity: SouthEast, Left: -5, Top: -6}), placed(logo, W-lw-5, H-lh-6))

	// Quarter and half turns land on whole pixels, so they are exact: the
	// turned logo, placed where the turn leaves it.
	turns := []struct {
		name   string
		o      CompositeOptions
		orient int
		x, y   int
	}{
		// Around the anchor (top-left corner at 150,100): the logo hangs
		// below-left of it after a quarter turn clockwise.
		{"90 around the corner", CompositeOptions{Left: 150, Top: 100, Angle: 90}, 6, 150 - lh, 100},
		{"180 around the corner", CompositeOptions{Left: 150, Top: 100, Angle: 180}, 3, 150 - lw, 100 - lh},
		{"270 around the corner", CompositeOptions{Left: 150, Top: 100, Angle: -90}, 8, 150, 100 - lw},
		// Around its own centre the logo stays centred where it was.
		{"90 around the centre", CompositeOptions{Left: 150, Top: 100, Angle: 90, Pivot: Center}, 6, 150 + lw/2 - lh/2, 100 + lh/2 - lw/2},
		{"180 around the centre", CompositeOptions{Left: 150, Top: 100, Angle: 540, Pivot: Center}, 3, 150, 100},
		// Around the bottom-right corner, and around a point in pixels.
		{"90 around the far corner", CompositeOptions{Left: 150, Top: 100, Angle: 90, Pivot: SouthEast}, 6, 150 + lw, 100 + lh - lw},
		{"180 around a point", CompositeOptions{Left: 150, Top: 100, Angle: 180, Pivot: CustomPosition, PivotX: 10, PivotY: 4}, 3, 150 + 20 - lw, 100 + 8 - lh},
		// Anchor and pivot together: centre of the logo on (150,100), turned in place.
		{"centred and turned", CompositeOptions{Left: 150, Top: 100, Anchor: Center, Angle: 90}, 6, 150 - lh/2, 100 - lw/2},
	}
	for _, c := range turns {
		got := run(c.o)
		want := placed(turnRef(logo, c.orient), c.x, c.y)
		if d := meanDiff(got, want); d > 0.02 {
			t.Errorf("%s: differs from the turned logo at (%d,%d) by %.3f levels on average", c.name, c.x, c.y, d)
		}
	}
	// A full turn is no turn.
	sameImage(t, "360", run(CompositeOptions{Left: 150, Top: 100, Angle: 360}), placed(logo, 150, 100))

	// Any other angle: the pivot stays put, about as many pixels change as
	// the logo has, and all of them near the pivot.
	got := run(CompositeOptions{Left: 150, Top: 100, Anchor: Center, Angle: 30})
	if r, g, b, _ := got.At(150, 100).RGBA(); r>>8 < 120 || g>>8 < 150 || b>>8 != 200 {
		t.Errorf("30 degrees: pixel at the pivot is %v, not the logo's centre colour", got.At(150, 100))
	}
	changed := 0
	for y := 0; y < H; y++ {
		for x := 0; x < W; x++ {
			if got.At(x, y) != base.At(x, y) {
				changed++
				if dx, dy := float64(x-150), float64(y-100); math.Hypot(dx, dy) > math.Hypot(lw/2, lh/2)+2 {
					t.Fatalf("30 degrees: pixel (%d,%d) changed, too far from the pivot", x, y)
				}
			}
		}
	}
	if changed < lw*lh*9/10 || changed > lw*lh*12/10 {
		t.Errorf("30 degrees: %d pixels changed, the logo has %d", changed, lw*lh)
	}
	// A turned logo that sticks out of the image is clipped, not an error.
	run(CompositeOptions{Gravity: SouthEast, Angle: 45})
	run(CompositeOptions{Left: -500, Top: -500, Angle: 10})

	for _, bad := range []CompositeOptions{
		{Anchor: 99}, {Angle: 10, Pivot: 99}, {Gravity: 12}, {Gravity: CustomPosition}, {Angle: math.NaN()}, {LeftRatio: math.Inf(1)},
	} {
		if _, err := FromBytes(in).Composite(FromBytes(logoPNG), bad).PNG().ToBytes(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}

// TestCompositeSizeByShare: WidthRatio and HeightRatio size the overlay by
// the image it goes on, so one setting gives the same look on images of
// every size; bad ratios are errors.
func TestCompositeSizeByShare(t *testing.T) {
	logo := image.NewRGBA(image.Rect(0, 0, 100, 50))
	stddraw.Draw(logo, logo.Bounds(), image.NewUniform(color.RGBA{255, 0, 0, 255}), image.Point{}, stddraw.Src)
	logoPNG := pngBytes(t, logo)
	// The red box: the extent of the pixels that are clearly red.
	redBox := func(m image.Image) image.Rectangle {
		var r image.Rectangle
		b := m.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				c := color.RGBAModel.Convert(m.At(x, y)).(color.RGBA)
				if c.R > 200 && c.G < 60 {
					r = r.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
		return r
	}
	for _, c := range []struct {
		w, h   int
		o      CompositeOptions
		lw, lh int
	}{
		{1000, 600, CompositeOptions{WidthRatio: 0.2}, 200, 100},
		{3000, 1800, CompositeOptions{WidthRatio: 0.2}, 600, 300},
		{1000, 600, CompositeOptions{HeightRatio: 0.5}, 600, 300},
		{1000, 600, CompositeOptions{WidthRatio: 0.5, HeightRatio: 0.1}, 120, 60}, // the height decides
		{1000, 600, CompositeOptions{WidthRatio: 0.05, HeightRatio: 0.5}, 50, 25}, // the width decides
		{400, 300, CompositeOptions{WidthRatio: 0.2, Gravity: Center, Tile: true}, 80, 40},
	} {
		base := image.NewRGBA(image.Rect(0, 0, c.w, c.h))
		stddraw.Draw(base, base.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
		out, err := FromImage(base).Composite(FromBytes(logoPNG), c.o).PNG().ToBytes()
		if err != nil {
			t.Fatalf("%dx%d %+v: %v", c.w, c.h, c.o, err)
		}
		got := decodePNG(t, out)
		if got.Bounds().Size() != image.Pt(c.w, c.h) {
			t.Fatalf("%+v: the image became %v", c.o, got.Bounds().Size())
		}
		r := redBox(got)
		if c.o.Tile {
			// Tiled: the first copy is at the centre; the copies cover the image.
			if r != got.Bounds() {
				t.Errorf("tiled %+v: red over %v, want all of %v", c.o, r, got.Bounds())
			}
			continue
		}
		if d := r.Dx() - c.lw; d < -1 || d > 1 || r.Dy()-c.lh < -1 || r.Dy()-c.lh > 1 || r.Min != image.Pt(0, 0) {
			t.Errorf("%dx%d %+v: the overlay is %v, want %dx%d at the top left", c.w, c.h, c.o, r, c.lw, c.lh)
		}
	}
	base := FromImage(image.NewRGBA(image.Rect(0, 0, 50, 50)))
	for _, bad := range []CompositeOptions{{WidthRatio: -0.1}, {HeightRatio: math.NaN()}, {WidthRatio: 11}, {WidthRatio: math.Inf(1)}} {
		if _, err := base.Clone().Composite(FromBytes(logoPNG), bad).PNG().ToBytes(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	// A tiny share of a small image is still a pixel of overlay, not an error.
	if _, err := base.Clone().Composite(FromBytes(logoPNG), CompositeOptions{WidthRatio: 0.001}).PNG().ToBytes(); err != nil {
		t.Errorf("a very small share: %v", err)
	}
}
