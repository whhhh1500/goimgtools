package tests

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/color/palette"
	"image/draw"
	"image/gif"
	"math"
	"path/filepath"
	"testing"

	. "github.com/whhhh1500/goimgtools"
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

	// No operations: the same animation, frame for frame, with the
	// palette chosen for it and with the fixed one (which needs less
	// memory: choosing a palette takes some 3MB).
	for _, c := range []struct {
		limit int64
		opts  GIFOptions
	}{{0, GIFOptions{}}, {4 << 20, GIFOptions{}}, {0, GIFOptions{FixedPalette: true}}, {400 << 10, GIFOptions{FixedPalette: true}}} {
		dir := t.TempDir()
		got, err := FromBytes(in).MemoryLimit(c.limit).TempDir(dir).Animated().GIF(c.opts).ToBytes()
		if err != nil {
			t.Fatalf("limit %d %+v: %v", c.limit, c.opts, err)
		}
		check("copy", got, func(i int) image.Image { return want[i] })
		if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
			t.Errorf("limit %d: temp files left: %v", c.limit, left)
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
		if !f.Bounds().In(image.Rect(0, 0, 32, 24)) {
			t.Fatalf("resized frame %d: %v", i, f.Bounds())
		}
	}

	// Without Animated, only the first frame. With it, an output format
	// that holds a single picture is refused rather than given that.
	first, err := FromBytes(in).GIF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(decodeAll(first).Image); n != 1 {
		t.Errorf("without Animated: %d frames", n)
	}
	p, err := FromBytes(in).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "png of an animation", decodePNG(t, p), want[0])
	for _, out := range []*Image{FromBytes(in).Animated().TIFF(), FromBytes(in).Animated().JPEG(80), FromReader(bytes.NewReader(in)).Animated().BMP()} {
		if _, err := out.ToBytes(); !errors.Is(err, errors.ErrUnsupported) || errors.Is(err, ErrFormat) {
			t.Errorf("animation to a still format: %v", err)
		}
	}
	// One frame is no animation: Animated passes it, from a file or a
	// plain reader (which is read twice).
	one := &gif.GIF{Image: g.Image[:1], Delay: g.Delay[:1], Config: g.Config}
	var ob bytes.Buffer
	if err := gif.EncodeAll(&ob, one); err != nil {
		t.Fatal(err)
	}
	for _, m := range []*Image{FromBytes(ob.Bytes()), FromReader(bytes.NewReader(ob.Bytes()))} {
		p, err := m.Animated().PNG().ToBytes()
		if err != nil {
			t.Fatalf("one frame: %v", err)
		}
		sameImage(t, "one frame", decodePNG(t, p), want[0])
	}
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

// movingAnimation makes an opaque animation of n full frames over a smooth
// background of many colours, in which only a small square moves (the
// frames are not optimized: each is the whole picture). With local, the
// background's colours shift from frame to frame, each frame with its own
// palette, so that the frames together have far more than 256 colours.
func movingAnimation(w, h, n int, local bool) *gif.GIF {
	g := &gif.GIF{Config: image.Config{Width: w, Height: h}}
	shared := make(color.Palette, 0, 256)
	for i := 0; i < 250; i++ {
		shared = append(shared, color.RGBA{uint8(i), uint8(255 - i), uint8(i / 2), 255})
	}
	shared = append(shared, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255})
	g.Config.ColorModel = shared
	for k := 0; k < n; k++ {
		pal := shared
		if local {
			pal = make(color.Palette, 0, 256)
			for i := 0; i < 254; i++ {
				pal = append(pal, color.RGBA{uint8(i), uint8(255 - i), uint8((i + 37*k) % 256), 255})
			}
			pal = append(pal, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 0, 255, 255})
		}
		m := image.NewPaletted(image.Rect(0, 0, w, h), pal)
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				m.Pix[y*m.Stride+x] = uint8((x + y) * 249 / (w + h))
			}
		}
		sq := image.Rect(4+3*k, h/2, 14+3*k, h/2+10)
		for y := sq.Min.Y; y < sq.Max.Y; y++ {
			for x := sq.Min.X; x < sq.Max.X; x++ {
				m.Pix[y*m.Stride+x] = uint8(len(pal) - 2 + (x/3)%2)
			}
		}
		g.Image = append(g.Image, m)
		g.Delay = append(g.Delay, 4+k)
		g.Disposal = append(g.Disposal, gif.DisposalNone)
	}
	return g
}

func encodeAll(t *testing.T, g *gif.GIF) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func decodeGIF(t *testing.T, b []byte) *gif.GIF {
	t.Helper()
	g, err := gif.DecodeAll(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// TestAnimatedChangedParts: an animation is written with one palette, and
// the frames after the first as the part that changed; no colour is lost
// when there are few enough, and the file is smaller than the frames whole.
func TestAnimatedChangedParts(t *testing.T) {
	g := movingAnimation(160, 120, 8, false)
	in := encodeAll(t, g)
	want := coalesce(g)
	out, err := FromBytes(in).Animated().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	o := decodeGIF(t, out)
	if len(o.Image) != len(g.Image) {
		t.Fatalf("%d frames, want %d", len(o.Image), len(g.Image))
	}
	for i, f := range coalesce(o) {
		sameImage(t, fmt.Sprintf("frame %d", i), f, want[i])
		if o.Delay[i] != g.Delay[i] {
			t.Errorf("frame %d: delay %d, want %d", i, o.Delay[i], g.Delay[i])
		}
	}
	global := o.Config.ColorModel.(color.Palette)
	for i, f := range o.Image {
		if len(f.Palette) != len(global) {
			t.Errorf("frame %d has a palette of its own", i)
		}
		if i == 0 {
			continue
		}
		// The square moves 3 pixels a frame: what changed is where it was
		// and where it is.
		changed := image.Rect(4+3*(i-1), 60, 14+3*i, 70)
		if f.Bounds() != changed {
			t.Errorf("frame %d written as %v, want %v", i, f.Bounds(), changed)
		}
	}
	fixed, err := FromBytes(in).Animated().GIF(GIFOptions{FixedPalette: true}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) >= len(in) || len(out)*3 >= len(fixed) {
		t.Errorf("%d bytes, from %d bytes in and %d with the fixed palette", len(out), len(in), len(fixed))
	}
}

// quantDist is the colour distance of the quantizer between two opaque
// colours: libimagequant's, each channel to the power 0.5499/0.45455 and
// weighted, red 0.5, green 1, blue 0.45.
func quantDist(a, b color.RGBA) float64 {
	g := func(v uint8) float64 { return 255 * math.Pow(float64(v)/255, 0.5499/0.45455) }
	d := 0.0
	for i, w := range [3]float64{0.5, 1, 0.45} {
		x, y := [3]uint8{a.R, a.G, a.B}[i], [3]uint8{b.R, b.G, b.B}[i]
		e := w * (g(x) - g(y))
		d += e * e
	}
	return d
}

// rgbDist is the squared distance between two colours, that of the
// standard library's mapping to a palette (the fixed palette's).
func rgbDist(a, b color.RGBA) float64 {
	d := func(x, y uint8) float64 { e := float64(x) - float64(y); return e * e }
	return d(a.R, b.R) + d(a.G, b.G) + d(a.B, b.B)
}

// nearestDist is the distance (by dist) from c to the nearest opaque entry
// of pal.
func nearestDist(c color.RGBA, pal color.Palette, dist func(a, b color.RGBA) float64) float64 {
	best := -1.0
	for _, p := range pal {
		q := color.RGBAModel.Convert(p).(color.RGBA)
		if q.A != 255 {
			continue
		}
		if d := dist(c, q); best < 0 || d < best {
			best = d
		}
	}
	return best
}

// TestAnimatedColours: frames with more colours together than a GIF holds
// get one palette of at most the colours asked for; without dithering
// every pixel is the nearest of them; with dithering the frames still are
// written as what changed; the fixed palette honours both options too.
func TestAnimatedColours(t *testing.T) {
	g := movingAnimation(120, 90, 6, true)
	in := encodeAll(t, g)
	want := coalesce(g)
	for _, c := range []GIFOptions{{}, {Colors: 64}, {Colors: 20, NoDither: true}, {Colors: 2}, {FixedPalette: true, Colors: 32},
		{FixedPalette: true, NoDither: true}} {
		out, err := FromBytes(in).Animated().GIF(c).ToBytes()
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		o := decodeGIF(t, out)
		if c == (GIFOptions{}) {
			// Every frame has at most 256 colours: each is written with a
			// palette of its own, every pixel as it is.
			shown := coalesce(o)
			for i := range shown {
				if !bytes.Equal(shown[i].Pix, want[i].Pix) {
					t.Errorf("frame %d: pixels changed", i)
				}
				if len(o.Image[i].Palette) > 256 {
					t.Errorf("frame %d: a palette of %d", i, len(o.Image[i].Palette))
				}
			}
			continue
		}
		global := o.Config.ColorModel.(color.Palette)
		n := c.Colors
		if n == 0 {
			n = 256
		}
		// The table is padded to a power of two: count what the pixels use.
		used := map[uint8]bool{}
		for _, f := range o.Image {
			for _, v := range f.Pix {
				used[v] = true
			}
		}
		if len(used) > n || len(global) > 256 {
			t.Errorf("%+v: %d colours used, table of %d", c, len(used), len(global))
		}
		// The entries in use (the padding is not the quantizer's).
		var real color.Palette
		for v := range used {
			real = append(real, global[v])
		}
		shown := coalesce(o)
		for i, f := range o.Image {
			if len(f.Palette) != len(global) {
				t.Errorf("%+v: frame %d has a palette of its own", c, i)
			}
			if c.NoDither {
				dist := quantDist
				if c.FixedPalette {
					dist = rgbDist
				}
				for p := 0; p < len(shown[i].Pix); p += 4 {
					src := color.RGBA{want[i].Pix[p], want[i].Pix[p+1], want[i].Pix[p+2], 255}
					got := color.RGBA{shown[i].Pix[p], shown[i].Pix[p+1], shown[i].Pix[p+2], shown[i].Pix[p+3]}
					// The quantizer works in fixed point, a sixteenth of a level
					// of green: a tie or near tie can go either way.
					if got.A != 255 || dist(src, got) > nearestDist(src, real, dist)*1.01+0.5 {
						t.Fatalf("%+v: frame %d pixel %d is %v for %v, not the nearest colour", c, i, p/4, got, src)
					}
				}
			}
			// The background's colours change every frame, so only the
			// fixed palette's whole frames and the first are full; the
			// others are full too, as everything changed. What matters is
			// that no frame is left with stale pixels: the picture shown
			// is close to the source.
			if psnr := framePSNR(shown[i], want[i]); psnr < 20 && c.Colors != 2 && !(c.FixedPalette && c.Colors != 0) {
				t.Errorf("%+v: frame %d at %.1f dB", c, i, psnr)
			}
		}
	}
	// Dithering does not spoil finding what changed: frames that differ in
	// a square are written as that square.
	g = movingAnimation(120, 90, 6, false)
	for i := range g.Image { // more colours than the palette asked for
		g.Image[i].Palette = append(color.Palette(nil), g.Image[i].Palette...)
	}
	out, err := FromBytes(encodeAll(t, g)).Animated().GIF(GIFOptions{Colors: 16}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	o := decodeGIF(t, out)
	for i, f := range o.Image[1:] {
		if changed := image.Rect(4+3*i, 45, 14+3*(i+1), 55); f.Bounds() != changed {
			t.Errorf("16 colours, dithered: frame %d written as %v, want %v", i+1, f.Bounds(), changed)
		}
	}
	if _, err := FromBytes(in).Animated().GIF(GIFOptions{Colors: 300}).ToBytes(); err == nil {
		t.Error("300 colours accepted")
	}
}

func framePSNR(a, b *image.RGBA) float64 {
	var se float64
	for i := range a.Pix {
		d := float64(a.Pix[i]) - float64(b.Pix[i])
		se += d * d
	}
	if se == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255*float64(len(a.Pix))/se)
}

// TestAnimatedTransparency: a frame in which pixels turn transparent is
// written whole on a cleared picture, and an animation whose first frame
// has transparent pixels clears the picture after its last, before it
// starts again.
func TestAnimatedTransparency(t *testing.T) {
	pal := color.Palette{color.RGBA{}, color.RGBA{200, 30, 30, 255}, color.RGBA{30, 200, 30, 255}}
	g := &gif.GIF{Config: image.Config{Width: 40, Height: 30, ColorModel: pal}}
	add := func(f func(x, y int) uint8) {
		m := image.NewPaletted(image.Rect(0, 0, 40, 30), pal)
		for y := 0; y < 30; y++ {
			for x := 0; x < 40; x++ {
				m.Pix[y*m.Stride+x] = f(x, y)
			}
		}
		g.Image = append(g.Image, m)
		g.Delay = append(g.Delay, 5)
		g.Disposal = append(g.Disposal, gif.DisposalBackground)
	}
	add(func(x, y int) uint8 { return uint8(min(x/10, 2)) }) // transparent on the left
	add(func(x, y int) uint8 { return 1 })                   // all opaque
	add(func(x, y int) uint8 { return uint8(1 + (x+y)%2) })  // opaque, changed
	add(func(x, y int) uint8 { return uint8((x / 5) % 3) })  // turns transparent in places
	add(func(x, y int) uint8 { return uint8(1 + (x/5)%2) })  // opaque again
	want := coalesce(g)
	out, err := FromBytes(encodeAll(t, g)).Animated().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	o := decodeGIF(t, out)
	for i, f := range coalesce(o) {
		sameImage(t, fmt.Sprintf("frame %d", i), f, want[i])
	}
	full := image.Rect(0, 0, 40, 30)
	// Frame 3 turns pixels transparent: frame 2 clears the picture.
	if o.Image[3].Bounds() != full || o.Disposal[2] != gif.DisposalBackground || o.Image[2].Bounds() != full {
		t.Errorf("frame 3 %v after frame 2 %v disposal %d", o.Image[3].Bounds(), o.Image[2].Bounds(), o.Disposal[2])
	}
	if last := len(o.Image) - 1; o.Disposal[last] != gif.DisposalBackground || o.Image[last].Bounds() != full {
		t.Errorf("last frame %v disposal %d: the first frame has transparent pixels", o.Image[last].Bounds(), o.Disposal[last])
	}
}

// TestAnimatedEverywhere: the file is the same whatever the memory limit
// (frames kept in temporary files) and the number of threads, and nothing
// is left behind.
func TestAnimatedEverywhere(t *testing.T) {
	in := encodeAll(t, movingAnimation(400, 300, 8, true))
	ref, err := FromBytes(in).Animated().Blur(1).GIF(GIFOptions{Colors: 100}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		limit   int64
		threads int
	}{{6 << 20, 1}, {0, 6}, {8 << 20, 4}} {
		dir := t.TempDir()
		got, err := FromBytes(in).MemoryLimit(c.limit).TempDir(dir).Threads(c.threads).Animated().Blur(1).GIF(GIFOptions{Colors: 100}).ToBytes()
		if err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
		if !bytes.Equal(got, ref) {
			t.Errorf("%+v: another file", c)
		}
		if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
			t.Errorf("%+v: temp files left: %v", c, left)
		}
	}
}
