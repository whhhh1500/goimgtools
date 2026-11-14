package goimgtools

import (
	"bytes"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/gif"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"

	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestBMPAndTIFFOutput: the files are the ones the x/image encoders write
// for the same pixels, whether chosen by method or by file extension.
func TestBMPAndTIFFOutput(t *testing.T) {
	imgs := testutil.Images(97, 61)
	dir := t.TempDir()
	for _, name := range []string{"nrgba", "rgba-opaque", "gray", "gray16", "paletted", "nrgba64"} {
		in := pngBytes(t, imgs[name])
		dec := decodePNG(t, in)
		enc := func(f func(*bytes.Buffer) error) []byte {
			var b bytes.Buffer
			if err := f(&b); err != nil {
				t.Fatal(err)
			}
			return b.Bytes()
		}
		cases := []struct {
			what string
			got  func() ([]byte, error)
			want []byte
		}{
			{"BMP()", func() ([]byte, error) { return FromBytes(in).BMP().ToBytes() },
				enc(func(b *bytes.Buffer) error { return bmp.Encode(b, dec) })},
			{"TIFF()", func() ([]byte, error) { return FromBytes(in).TIFF().ToBytes() },
				enc(func(b *bytes.Buffer) error { return tiff.Encode(b, dec, nil) })},
			{"TIFF(deflate)", func() ([]byte, error) {
				return FromBytes(in).TIFF(TIFFOptions{Compression: TIFFDeflate}).ToBytes()
			}, enc(func(b *bytes.Buffer) error { return tiff.Encode(b, dec, &tiff.Options{Compression: tiff.Deflate}) })},
			{".bmp", func() ([]byte, error) {
				p := filepath.Join(dir, name+".bmp")
				if err := FromBytes(in).ToFile(p); err != nil {
					return nil, err
				}
				return os.ReadFile(p)
			}, enc(func(b *bytes.Buffer) error { return bmp.Encode(b, dec) })},
			{".tif", func() ([]byte, error) {
				p := filepath.Join(dir, name+".tif")
				if err := FromBytes(in).ToFile(p); err != nil {
					return nil, err
				}
				return os.ReadFile(p)
			}, enc(func(b *bytes.Buffer) error { return tiff.Encode(b, dec, nil) })},
		}
		for _, c := range cases {
			got, err := c.got()
			if err != nil {
				t.Fatalf("%s %s: %v", name, c.what, err)
			}
			if !bytes.Equal(got, c.want) {
				t.Errorf("%s %s: differs from the x/image encoder", name, c.what)
			}
		}
	}
	// After a chain, and back in again: a round trip keeps the pixels.
	// (An opaque image: what BMP does with partial transparency is the
	// x/image encoder's business and is covered by the byte comparison.)
	in := pngBytes(t, imgs["rgba-opaque"])
	for _, f := range []func(*Image) *Image{
		func(m *Image) *Image { return m.BMP() },
		func(m *Image) *Image { return m.TIFF(TIFFOptions{Compression: TIFFDeflate}) },
	} {
		out, err := f(FromBytes(in).Resize(40, 0).Rotate(90)).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		back, err := FromBytes(out).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		want, err := FromBytes(in).Resize(40, 0).Rotate(90).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		sameImage(t, "round trip", decodePNG(t, back), decodePNG(t, want))
	}
	if _, err := FromBytes(in).TIFF(TIFFOptions{Compression: 9}).ToBytes(); err == nil {
		t.Error("unknown TIFF compression accepted")
	}
}

// TestGIFOutput: colours chosen for the image by default, the standard
// library's file with the fixed palette.
func TestGIFOutput(t *testing.T) {
	imgs := testutil.Images(97, 61)
	for _, name := range []string{"nrgba", "rgba-opaque", "gray", "paletted"} {
		in := pngBytes(t, imgs[name])
		dec := decodePNG(t, in)
		for _, colors := range []int{0, 16, 256} {
			got, err := FromBytes(in).GIF(GIFOptions{Colors: colors, FixedPalette: true}).ToBytes()
			if err != nil {
				t.Fatalf("%s %d colours: %v", name, colors, err)
			}
			var want bytes.Buffer
			if err := gif.Encode(&want, dec, &gif.Options{NumColors: colors}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want.Bytes()) {
				t.Errorf("%s %d colours: with the fixed palette the file differs from gif.Encode", name, colors)
			}
		}
	}
	// By default the colours are chosen for the image: far closer to it.
	photo := photoLike(320, 240)
	in0 := pngBytes(t, photo)
	fixed, _ := FromBytes(in0).GIF(GIFOptions{FixedPalette: true}).ToBytes()
	for _, o := range []GIFOptions{{}, {Colors: 64}, {NoDither: true}} {
		got, err := FromBytes(in0).GIF(o).ToBytes()
		if err != nil {
			t.Fatalf("%+v: %v", o, err)
		}
		g, err := gif.Decode(bytes.NewReader(got))
		if err != nil {
			t.Fatal(err)
		}
		limit := 256
		if o.Colors != 0 {
			limit = o.Colors
		}
		if n := len(g.(*image.Paletted).Palette); n > limit {
			t.Errorf("%+v: %d palette entries", o, n)
		}
		f, _ := gif.Decode(bytes.NewReader(fixed))
		if d, df := meanDiff(g, photo), meanDiff(f, photo); d > df/2 {
			t.Errorf("%+v: mean difference %.2f levels, the fixed palette gives %.2f", o, d, df)
		}
	}
	// Transparency is all or nothing: what was mostly transparent is
	// transparent, what was mostly opaque is opaque and keeps its colour.
	soft := image.NewNRGBA(image.Rect(0, 0, 120, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 120; x++ {
			soft.SetNRGBA(x, y, color.NRGBA{200, uint8(4 * y), 30, uint8(x * 255 / 119)})
		}
	}
	got, err := FromBytes(pngBytes(t, soft)).GIF().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	g, err := gif.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		x      int
		opaque bool
	}{{0, false}, {40, false}, {80, true}, {119, true}} {
		r, _, _, a := g.At(c.x, 20).RGBA()
		if (a == 0xffff) != c.opaque || (a != 0 && a != 0xffff) {
			t.Errorf("pixel %d: alpha %#x", c.x, a)
		}
		if c.opaque && (r>>8 < 180 || r>>8 > 220) {
			t.Errorf("pixel %d: red %d, the source has 200", c.x, r>>8)
		}
	}
	// An image that is already a palette with few enough colours is
	// written as it is, either way.
	pal := color.Palette{color.NRGBA{}, color.RGBA{255, 0, 0, 255}, color.RGBA{0, 160, 0, 255}, color.RGBA{0, 0, 255, 255}, color.RGBA{250, 250, 250, 255}}
	flag := image.NewPaletted(image.Rect(0, 0, 60, 40), pal)
	for y := 0; y < 40; y++ {
		for x := 0; x < 60; x++ {
			flag.SetColorIndex(x, y, uint8((x/12+y/10)%5))
		}
	}
	pin := pngBytes(t, flag)
	a, _ := FromBytes(pin).GIF().ToBytes()
	b, _ := FromBytes(pin).GIF(GIFOptions{FixedPalette: true}).ToBytes()
	if !bytes.Equal(a, b) {
		t.Error("a paletted image is written differently with and without the fixed palette")
	}
	if back, err := gif.Decode(bytes.NewReader(a)); err != nil {
		t.Fatal(err)
	} else {
		sameImage(t, "paletted image as GIF", back, flag)
	}
	in := pngBytes(t, imgs["nrgba"])
	p := filepath.Join(t.TempDir(), "out.gif")
	if err := FromBytes(in).Resize(30, 0).ToFile(p); err != nil {
		t.Fatal(err)
	}
	if info, err := Open(p).Metadata(); err != nil || info.Format != "gif" || info.Width != 30 {
		t.Fatalf("written GIF: %+v, %v", info, err)
	}
	if _, err := FromBytes(in).GIF(GIFOptions{Colors: 300}).ToBytes(); err == nil {
		t.Error("300 colours accepted")
	}
}

// TestWebPOutput: lossless keeps every pixel, lossy is close and smaller,
// transparency survives both, and the files read back through this module
// and through the x/image decoder alike.
func TestWebPOutput(t *testing.T) {
	smooth := image.NewNRGBA(image.Rect(0, 0, 240, 160))
	for y := 0; y < 160; y++ {
		for x := 0; x < 240; x++ {
			a := uint8(255)
			if x < 60 {
				a = uint8(x * 4)
			}
			smooth.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y + 40), uint8((x + y) / 2), a})
		}
	}
	opaque := image.NewRGBA(smooth.Bounds())
	stddraw.Draw(opaque, opaque.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	stddraw.Draw(opaque, opaque.Bounds(), smooth, image.Point{}, stddraw.Over)
	// The x/image decoder, with the colour range of lossy images put right.
	xdecode := func(b []byte) image.Image {
		t.Helper()
		m, err := webpFullRange(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	for name, src := range map[string]image.Image{"transparent": smooth, "opaque": opaque} {
		in := pngBytes(t, src)
		// Lossless: exact, also through our own decoder.
		ll, err := FromBytes(in).WebP(WebPOptions{Lossless: true}).ToBytes()
		if err != nil {
			t.Fatalf("%s lossless: %v", name, err)
		}
		sameImage(t, name+" lossless (x/image)", xdecode(ll), src)
		back, err := FromBytes(ll).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		sameImage(t, name+" lossless (own decoder)", decodePNG(t, back), src)
		if info, err := FromBytes(ll).Metadata(); err != nil || info.Format != "webp" || info.Width != 240 {
			t.Errorf("%s lossless: Metadata %+v, %v", name, info, err)
		}
		// Lossy: near the source, better at higher quality, smaller than lossless.
		var last float64 = 1e9
		for _, q := range []int{95, 75, 30} {
			ly, err := FromBytes(in).WebP(WebPOptions{Quality: q}).ToBytes()
			if err != nil {
				t.Fatalf("%s q%d: %v", name, q, err)
			}
			got := xdecode(ly)
			if got.Bounds().Size() != src.Bounds().Size() {
				t.Fatalf("%s q%d: size %v", name, q, got.Bounds().Size())
			}
			// Compare on white, so transparent areas count by what shows.
			flat := image.NewRGBA(src.Bounds())
			stddraw.Draw(flat, flat.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
			stddraw.Draw(flat, flat.Bounds(), got, got.Bounds().Min, stddraw.Over)
			d := meanDiff(flat, opaque)
			if limit := map[int]float64{95: 1, 75: 1.5, 30: 3}[q]; d > limit {
				t.Errorf("%s q%d: mean difference %.2f levels", name, q, d)
			}
			if float64(len(ly)) > last {
				t.Errorf("%s q%d: %d bytes, larger than at the higher quality", name, q, len(ly))
			}
			last = float64(len(ly))
			if name == "transparent" {
				// Transparency is kept exactly.
				for _, p := range []image.Point{{0, 0}, {30, 80}, {59, 159}, {100, 100}} {
					_, _, _, a1 := got.At(p.X, p.Y).RGBA()
					_, _, _, a2 := src.At(p.X, p.Y).RGBA()
					if a1 != a2 {
						t.Errorf("q%d: alpha at %v = %#x, want %#x", q, p, a1, a2)
					}
				}
			}
		}
		// A lossy file read back by this module shows what x/image shows.
		ly, _ := FromBytes(in).WebP().ToBytes()
		back, err = FromBytes(ly).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if d := meanDiff(decodePNG(t, back), xdecode(ly)); name == "opaque" && d > 0.01 {
			t.Errorf("%s: own decoder differs from x/image by %.3f levels", name, d)
		}
	}
	// By extension, after a chain, and under a small memory limit.
	in := pngBytes(t, testutil.Images(613, 457)["rgba-opaque"])
	dir := t.TempDir()
	p := filepath.Join(dir, "out.webp")
	if err := FromBytes(in).Resize(300, 0).MemoryLimit(2 << 20).TempDir(dir).ToFile(p); err != nil {
		t.Fatal(err)
	}
	if info, err := Open(p).Metadata(); err != nil || info.Format != "webp" || info.Width != 300 {
		t.Errorf("out.webp: %+v, %v", info, err)
	}
	if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 1 {
		t.Errorf("temp files left: %v", left)
	}
	for _, bad := range []WebPOptions{{Quality: 101}, {Quality: -3}} {
		if _, err := FromBytes(in).WebP(bad).ToBytes(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
}
