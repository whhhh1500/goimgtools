package tests

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

func sourcePNG(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// TestLevelsAgainstSharp: Linear, Threshold and Convolve give sharp's
// (0.35) results to the byte, Normalize within a level, Tint within two,
// on the image Pillow's references are made from.
func TestLevelsAgainstSharp(t *testing.T) {
	src := sourcePNG(t, "pillow/source.png")
	for _, c := range []struct {
		name string
		m    *Image
		max  int
	}{
		{"linear", FromBytes(src).Linear(1.2, -25), 0},
		{"threshold", FromBytes(src).Threshold(128), 0},
		{"convolve", FromBytes(src).Convolve(Convolution{Width: 3, Height: 3, Weights: []float64{0, -1, 0, -1, 5, -1, 0, -1, 0}}), 0},
		{"normalize", FromBytes(src).Normalize(), 1},
		{"normalize-5-95", FromBytes(src).Normalize(NormalizeOptions{Lower: 5, Upper: 95}), 1},
		{"tint", FromBytes(src).Tint(color.RGBA{0x70, 0x42, 0x14, 0xff}), 2},
	} {
		out, err := c.m.PNG().ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if maxd, mean, _ := channelDiff(t, decodePNG(t, out), readPNG(t, "sharp/"+c.name+".png")); maxd > c.max {
			t.Errorf("%s: differs from sharp by up to %d (mean %.3f), want at most %d", c.name, maxd, mean, c.max)
		}
	}
	// Threshold of an image without transparency is one channel.
	out := must(FromBytes(src).Threshold(128).PNG().ToBytes())
	if _, ok := decodePNG(t, out).(*image.Gray); !ok {
		t.Errorf("Threshold: %T, want a greyscale image", decodePNG(t, out))
	}
}

// TestMedian: Pillow's MedianFilter to the byte, for squares of 3, 5 and 9
// and on an image with transparency (its alpha filtered too).
func TestMedian(t *testing.T) {
	for _, c := range []struct {
		src, want string
		size      int
	}{
		{"pillow/source.png", "pillow/median-3.png", 3},
		{"pillow/source.png", "pillow/median-5.png", 5},
		{"pillow/source.png", "pillow/median-9.png", 9},
		{"pillow/median-rgba-source.png", "pillow/median-rgba-3.png", 3},
		{"pillow/median-rgba-source.png", "pillow/median-rgba-5.png", 5},
	} {
		out, err := FromBytes(sourcePNG(t, c.src)).Median(c.size).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if maxd, mean, _ := channelDiff(t, decodePNG(t, out), readPNG(t, c.want)); maxd != 0 {
			t.Errorf("%s: differs from Pillow by up to %d (mean %.3f)", c.want, maxd, mean)
		}
	}
}

// TestLevelFormulas: the level operations on every level, with
// transparency kept, and the edge cases of their arguments.
func TestLevelFormulas(t *testing.T) {
	// A row of every level in red, the opposite in green, a third in blue,
	// alpha falling.
	m := image.NewNRGBA(image.Rect(0, 0, 256, 2))
	for x := 0; x < 256; x++ {
		for y := 0; y < 2; y++ {
			m.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(255 - x), uint8(x / 3), uint8(255 - x/2)})
		}
	}
	check := func(what string, img *Image, f func(v uint8) uint8) {
		t.Helper()
		out, err := img.PNG().ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
		got := decodePNG(t, out)
		for x := 0; x < 256; x++ {
			in := m.NRGBAAt(x, 0)
			p := color.NRGBAModel.Convert(got.At(x, 1)).(color.NRGBA)
			if want := (color.NRGBA{f(in.R), f(in.G), f(in.B), in.A}); p != want {
				t.Fatalf("%s at %d: %v, want %v", what, x, p, want)
			}
		}
	}
	check("Negate", FromImage(m).Negate(), func(v uint8) uint8 { return 255 - v })
	check("Linear", FromImage(m).Linear(1.7, -40.5), func(v uint8) uint8 { return uint8(math.Max(0, math.Min(255, 1.7*float64(v)-40.5))) })
	check("Linear 1, 0", FromImage(m).Linear(1, 0), func(v uint8) uint8 { return v })
	check("Gamma", FromImage(m).Gamma(2.2), func(v uint8) uint8 { return uint8(math.Round(255 * math.Pow(float64(v)/255, 1/2.2))) })
	check("Gamma 1", FromImage(m).Gamma(1), func(v uint8) uint8 { return v })
	check("Negate twice", FromImage(m).Negate().Negate(), func(v uint8) uint8 { return v })
	// Threshold keeps transparency and makes red, green and blue equal,
	// 0 or 255.
	out := decodePNG(t, must(FromImage(m).Threshold(100).PNG().ToBytes()))
	for x := 0; x < 256; x++ {
		p := color.NRGBAModel.Convert(out.At(x, 0)).(color.NRGBA)
		if p.R != p.G || p.G != p.B || p.R != 0 && p.R != 255 || p.A != m.NRGBAAt(x, 0).A {
			t.Fatalf("Threshold at %d: %v", x, p)
		}
	}
	for _, c := range []struct {
		what string
		m    *Image
	}{
		{"Gamma 0", FromImage(m).Gamma(0)},
		{"Gamma NaN", FromImage(m).Gamma(math.NaN())},
		{"Gamma 101", FromImage(m).Gamma(101)},
		{"Linear Inf", FromImage(m).Linear(math.Inf(1), 0)},
		{"Threshold -1", FromImage(m).Threshold(-1)},
		{"Threshold 256", FromImage(m).Threshold(256)},
		{"Tint nil", FromImage(m).Tint(nil)},
		{"Normalize 50, 50", FromImage(m).Normalize(NormalizeOptions{Lower: 50, Upper: 50})},
		{"Normalize 0, 101", FromImage(m).Normalize(NormalizeOptions{Lower: 0, Upper: 101})},
		{"Normalize twice", FromImage(m).Normalize(NormalizeOptions{}, NormalizeOptions{})},
		{"Median 4", FromImage(m).Median(4)},
		{"Median 1", FromImage(m).Median(1)},
		{"Median 101", FromImage(m).Median(101)},
		{"Convolve 2x3", FromImage(m).Convolve(Convolution{Width: 2, Height: 3, Weights: make([]float64, 6)})},
		{"Convolve short", FromImage(m).Convolve(Convolution{Width: 3, Height: 3, Weights: make([]float64, 8)})},
		{"Convolve NaN", FromImage(m).Convolve(Convolution{Width: 1, Height: 1, Weights: []float64{math.NaN()}})},
	} {
		if _, err := c.m.PNG().ToBytes(); err == nil {
			t.Errorf("%s: no error", c.what)
		}
	}
}

// TestTintAndNormalizeKeep: Tint leaves black and white as they are and
// gives greys the hue of the colour; Normalize leaves an image of one
// lightness alone and stretches a dull one to black and white; both keep
// transparency.
func TestTintAndNormalizeKeep(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 3, 1))
	m.SetNRGBA(0, 0, color.NRGBA{0, 0, 0, 255})
	m.SetNRGBA(1, 0, color.NRGBA{255, 255, 255, 200})
	m.SetNRGBA(2, 0, color.NRGBA{128, 128, 128, 255})
	out := decodePNG(t, must(FromImage(m).Tint(color.RGBA{0, 0, 255, 255}).PNG().ToBytes()))
	if p := color.NRGBAModel.Convert(out.At(0, 0)).(color.NRGBA); p != (color.NRGBA{0, 0, 0, 255}) {
		t.Errorf("Tint black: %v", p)
	}
	if p := color.NRGBAModel.Convert(out.At(1, 0)).(color.NRGBA); p != (color.NRGBA{255, 255, 255, 200}) {
		t.Errorf("Tint white: %v", p)
	}
	if p := color.NRGBAModel.Convert(out.At(2, 0)).(color.NRGBA); !(p.B > p.R && p.B > p.G) {
		t.Errorf("Tint grey with blue: %v", p)
	}
	flat := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := range flat.Pix {
		flat.Pix[i] = 120
	}
	got := decodePNG(t, must(FromImage(flat).Normalize().PNG().ToBytes()))
	if maxd, _, _ := channelDiff(t, got, flat); maxd != 0 {
		t.Errorf("Normalize of one lightness changed it by up to %d", maxd)
	}
	dull := image.NewNRGBA(image.Rect(0, 0, 200, 2))
	for x := 0; x < 200; x++ {
		v := uint8(80 + x/2)
		for y := 0; y < 2; y++ {
			dull.SetNRGBA(x, y, color.NRGBA{v, v, v, uint8(55 + x)})
		}
	}
	got = decodePNG(t, must(FromImage(dull).Normalize(NormalizeOptions{Lower: 0, Upper: 100}).PNG().ToBytes()))
	lo := color.NRGBAModel.Convert(got.At(0, 0)).(color.NRGBA)
	hi := color.NRGBAModel.Convert(got.At(199, 0)).(color.NRGBA)
	if lo.R > 5 || hi.R < 250 || lo.A != 55 || hi.A != 254 {
		t.Errorf("Normalize of 80..179: %v .. %v", lo, hi)
	}
}

// TestWindowFiltersStream: Median and Convolve on an image of many strips
// give, strip by strip, what they give on the whole of it at once (one
// strip), and keep to a small memory limit.
func TestWindowFiltersStream(t *testing.T) {
	m := image.NewNRGBA(image.Rect(0, 0, 301, 1203))
	for i := range m.Pix {
		m.Pix[i] = uint8(i*7 + i/1204*13)
	}
	var buf bytes.Buffer
	png.Encode(&buf, m)
	for _, c := range []struct {
		what string
		op   func(*Image) *Image
	}{
		{"median 3", func(i *Image) *Image { return i.Median(3) }},
		{"median 7", func(i *Image) *Image { return i.Median(7) }},
		{"convolve 5x3", func(i *Image) *Image {
			return i.Convolve(Convolution{Width: 5, Height: 3, Weights: []float64{1, 2, 3, 2, 1, 2, 4, 6, 4, 2, 1, 2, 3, 2, 1}, Offset: 3})
		}},
	} {
		// A source of one strip: the whole image read at once.
		whole := decodePNG(t, must(c.op(FromSource(oneStrip(m))).PNG().ToBytes()))
		streamed := decodePNG(t, must(c.op(FromBytes(buf.Bytes())).MemoryLimit(2<<20).PNG().ToBytes()))
		if maxd, _, _ := channelDiff(t, streamed, whole); maxd != 0 {
			t.Errorf("%s: strips and whole differ by up to %d", c.what, maxd)
		}
	}
}

// oneStrip opens a source handing out m as a single strip.
func oneStrip(m image.Image) func(env *pipeline.Env) (pipeline.Source, error) {
	return func(env *pipeline.Env) (pipeline.Source, error) { return &single{m: m, env: env}, nil }
}

type single struct {
	m    image.Image
	env  *pipeline.Env
	done bool
}

func (s *single) Env() *pipeline.Env      { return s.env }
func (s *single) Bounds() image.Rectangle { return s.m.Bounds() }
func (s *single) ColorModel() color.Model { return s.m.ColorModel() }
func (s *single) Close() error            { return nil }
func (s *single) Next() (image.Image, error) {
	if s.done {
		return nil, io.EOF
	}
	s.done = true
	return s.m, nil
}
