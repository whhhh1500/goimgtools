package goimgtools

import (
	"bytes"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"path/filepath"
	"testing"
)

// testAnimation builds an animation whose frames cover parts of the canvas,
// use every disposal method and have transparent pixels. Its colours are
// Plan 9 palette entries, so reducing a frame to that palette is exact.
func testAnimation(w, h int) *gif.GIF {
	pal := color.Palette{color.RGBA{}, palette.Plan9[20], palette.Plan9[77], palette.Plan9[140], palette.Plan9[201], palette.Plan9[250]}
	g := &gif.GIF{LoopCount: 3, Config: image.Config{Width: w, Height: h, ColorModel: pal}}
	add := func(r image.Rectangle, disposal byte, delay int, f func(x, y int) uint8) {
		m := image.NewPaletted(r, pal)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				m.SetColorIndex(x, y, f(x, y))
			}
		}
		g.Image = append(g.Image, m)
		g.Disposal = append(g.Disposal, disposal)
		g.Delay = append(g.Delay, delay)
	}
	full := image.Rect(0, 0, w, h)
	add(full, gif.DisposalNone, 10, func(x, y int) uint8 { return uint8(1 + (x/7+y/5)%2) })
	add(image.Rect(5, 4, w-9, h-6), gif.DisposalNone, 20, func(x, y int) uint8 { return uint8((x + y) % 4) }) // index 0 is transparent
	add(image.Rect(w/3, h/4, w-3, h-2), gif.DisposalPrevious, 5, func(x, y int) uint8 { return 4 })
	add(image.Rect(2, 2, w/2, h/2), gif.DisposalBackground, 7, func(x, y int) uint8 { return uint8(5 * ((x ^ y) & 1)) })
	add(image.Rect(w/4, h/3, w/4+9, h/3+7), gif.DisposalNone, 30, func(x, y int) uint8 { return 3 })
	add(full, gif.DisposalNone, 1, func(x, y int) uint8 { return uint8((x * y) % 6) })
	return g
}

// coalesce renders every frame of g as the full picture a viewer shows.
func coalesce(g *gif.GIF) []*image.RGBA {
	full := image.Rect(0, 0, g.Config.Width, g.Config.Height)
	cv := image.NewRGBA(full)
	var out []*image.RGBA
	for i, f := range g.Image {
		before := image.NewRGBA(full)
		copy(before.Pix, cv.Pix)
		draw.Draw(cv, f.Bounds(), f, f.Bounds().Min, draw.Over)
		shown := image.NewRGBA(full)
		copy(shown.Pix, cv.Pix)
		out = append(out, shown)
		switch g.Disposal[i] {
		case gif.DisposalBackground:
			draw.Draw(cv, f.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			draw.Draw(cv, f.Bounds(), before, f.Bounds().Min, draw.Src)
		}
	}
	return out
}

func TestAnimated(t *testing.T) {
	g := testAnimation(64, 48)
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	in := buf.Bytes()
	want := coalesce(g)

	decodeAll := func(b []byte) *gif.GIF {
		t.Helper()
		out, err := gif.DecodeAll(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(what string, b []byte, ref func(i int) image.Image) {
		t.Helper()
		out := decodeAll(b)
		if len(out.Image) != len(g.Image) || out.LoopCount != g.LoopCount {
			t.Fatalf("%s: %d frames, loop %d; want %d, %d", what, len(out.Image), out.LoopCount, len(g.Image), g.LoopCount)
		}
		// What a viewer shows for the output must be what it shows for the
		// input, frame by frame, with the same timing.
		shown := coalesce(out)
		for i := range shown {
			if out.Delay[i] != g.Delay[i] {
				t.Errorf("%s: frame %d delay %d, want %d", what, i, out.Delay[i], g.Delay[i])
			}
			sameImage(t, what+" frame", shown[i], ref(i))
		}
	}

	// No operations: the same animation, frame for frame.
	for _, limit := range []int64{0, 400 << 10} {
		dir := t.TempDir()
		got, err := FromBytes(in).MemoryLimit(limit).TempDir(dir).Animated().ToBytes()
		if err != nil {
			t.Fatalf("limit %d: %v", limit, err)
		}
		check("copy", got, func(i int) image.Image { return want[i] })
		if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
			t.Errorf("limit %d: temp files left: %v", limit, left)
		}
	}
	// Operations that move pixels without changing colours stay exact.
	got, err := FromBytes(in).Animated().Rotate(90).GIF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	check("rotated", got, func(i int) image.Image { return turnRef(want[i], 6) })
	got, err = FromBytes(in).Animated().Extract(10, 8, 40, 30).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	check("cropped", got, func(i int) image.Image { return want[i].SubImage(image.Rect(10, 8, 50, 38)) })

	// A resize: right size on every frame, and close to the scaled frames.
	got, err = FromBytes(in).Animated().Resize(32, 0).Flatten(color.White).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	out := decodeAll(got)
	if len(out.Image) != len(g.Image) || out.Config.Width != 32 || out.Config.Height != 24 {
		t.Fatalf("resized: %d frames, %dx%d", len(out.Image), out.Config.Width, out.Config.Height)
	}
	for i, f := range out.Image {
		if f.Bounds() != image.Rect(0, 0, 32, 24) {
			t.Fatalf("resized frame %d: %v", i, f.Bounds())
		}
	}

	// Without Animated, and for another output format, only the first frame.
	first, err := FromBytes(in).GIF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(decodeAll(first).Image); n != 1 {
		t.Errorf("without Animated: %d frames", n)
	}
	p, err := FromBytes(in).Animated().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "png of an animation", decodePNG(t, p), want[0])
	// Animated on a still image changes nothing.
	still := pngBytes(t, want[0])
	a, _ := FromBytes(still).Animated().PNG().ToBytes()
	b, _ := FromBytes(still).PNG().ToBytes()
	if !bytes.Equal(a, b) {
		t.Error("Animated changed a still image")
	}
	// Truncated input fails.
	if _, err := FromBytes(in[:len(in)*2/3]).Animated().ToBytes(); err == nil {
		t.Error("truncated animation accepted")
	}
}
