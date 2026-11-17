package tests

import (
	"bytes"
	"fmt"
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
// (0.35) results to the byte, Normalize and Tint within a level,
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
		{"tint", FromBytes(src).Tint(color.RGBA{0x70, 0x42, 0x14, 0xff}), 1},
		{"clahe-20-31-5", FromBytes(src).CLAHE(CLAHEOptions{Width: 20, Height: 31, MaxSlope: 5}), 0},
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
	// (The product rounded before the sum, as the library does it on every
	// processor: without float64 Go may fuse the two on some.)
	check("Linear", FromImage(m).Linear(1.7, -40.5), func(v uint8) uint8 { return uint8(math.Max(0, math.Min(255, float64(1.7*float64(v))-40.5))) })
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
		{"CLAHE 0x5", FromImage(m).CLAHE(CLAHEOptions{Width: 0, Height: 5})},
		{"CLAHE 4097", FromImage(m).CLAHE(CLAHEOptions{Width: 4097, Height: 5})},
		{"CLAHE slope 101", FromImage(m).CLAHE(CLAHEOptions{Width: 5, Height: 5, MaxSlope: 101})},
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

// TestBlendModes: every blend mode of Composite within a level of sharp's
// composite blend (libvips), on a 24x16 patch of the Pillow source with a
// translucent overlay over it; the reference holds the twelve results one
// under the other.
func TestBlendModes(t *testing.T) {
	src := sourcePNG(t, "pillow/source.png")
	over := FromBytes(sourcePNG(t, "sharp/blend-overlay.png"))
	ref := readPNG(t, "sharp/blend.png")
	for mode := BlendOver; mode <= BlendExclusion; mode++ {
		out, err := FromBytes(src).Extract(40, 30, 24, 16).Composite(over, CompositeOptions{Blend: mode}).PNG().ToBytes()
		if err != nil {
			t.Fatalf("%v: %v", mode, err)
		}
		want := ref.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(image.Rect(0, 16*int(mode), 24, 16*int(mode)+16))
		if maxd, mean, _ := channelDiff(t, decodePNG(t, out), want); maxd > 1 {
			t.Errorf("%v: differs from sharp by up to %d (mean %.3f)", mode, maxd, mean)
		}
	}
	if s := BlendColorDodge.String(); s != "color-dodge" {
		t.Errorf("BlendColorDodge is %q", s)
	}
	if _, err := FromBytes(src).Composite(over, CompositeOptions{Blend: BlendSaturate + 1}).PNG().ToBytes(); err == nil {
		t.Error("an unknown blend mode: no error")
	}
	// The Porter-Duff modes, and add and saturate, on a base with
	// transparency, the overlay over part of it only (the modes that clear
	// the image where the overlay is not do so). The pixels are kept
	// multiplied by alpha in 8 bits where libvips keeps them in floating
	// point: what shows of a colour (times its alpha) is within 2 levels of
	// sharp's, alpha within 1.
	rgba := sourcePNG(t, "pillow/median-rgba-source.png")
	pd := nrgbaOf(readPNG(t, "sharp/blend-pd.png"))
	for mode := BlendClear; mode <= BlendSaturate; mode++ {
		got := nrgbaOf(decodePNG(t, must(FromBytes(rgba).Extract(40, 30, 24, 16).Composite(over.Clone().Extract(0, 0, 16, 10),
			CompositeOptions{Left: 5, Top: 3, Blend: mode}).PNG().ToBytes())))
		row := int(mode - BlendClear)
		for i := 0; i < len(got.Pix); i += 4 {
			w := pd.Pix[row*16*pd.Stride+i:]
			if d := int(got.Pix[i+3]) - int(w[3]); d < -1 || d > 1 {
				t.Fatalf("%v: pixel %d alpha %d, sharp's %d", mode, i/4, got.Pix[i+3], w[3])
			}
			for c := 0; c < 3; c++ {
				if d := (int(got.Pix[i+c]) - int(w[c])) * int(w[3]) / 255; d < -2 || d > 2 {
					t.Fatalf("%v: pixel %d %v, sharp's %v", mode, i/4, got.Pix[i:i+4], w[:4])
				}
			}
		}
	}
	if s := BlendDestIn.String(); s != "dest-in" {
		t.Errorf("BlendDestIn is %q", s)
	}
}

// TestCoverPositionAndSmartCrop: FitCover keeps the part Position names
// (the middle rounded up, as sharp), FitContain puts the image there, and
// the smart crops find what stands out: the detailed side for entropy, a
// patch of skin tone and colour for attention.
func TestCoverPositionAndSmartCrop(t *testing.T) {
	// 300x100: a flat grey field with a noisy patch at x 220..280.
	m := image.NewNRGBA(image.Rect(0, 0, 300, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 300; x++ {
			c := color.NRGBA{128, 128, 128, 255}
			if x >= 220 && x < 280 {
				v := uint8((x*7919 + y*104729) % 251)
				c = color.NRGBA{v, v ^ 0x55, v * 3, 255}
			}
			m.SetNRGBA(x, y, c)
		}
	}
	// g: every column different (for finding where a crop starts).
	g := image.NewNRGBA(m.Rect)
	for y := 0; y < 100; y++ {
		for x := 0; x < 300; x++ {
			g.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(x / 256 * 100), uint8(y), 255})
		}
	}
	pick := func(o ResizeOptions) image.Image {
		t.Helper()
		o.Fit = FitCover
		out, err := FromImage(g).Resize(100, 100, o).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		return decodePNG(t, out)
	}
	// Where a crop of 100 starts: the column of m its column 0 shows.
	start := func(got image.Image) int {
		for x0 := 0; x0 <= 200; x0++ {
			same := true
			for y := 0; y < 100 && same; y += 7 {
				for x := 0; x < 100 && same; x += 3 {
					same = color.NRGBAModel.Convert(got.At(x, y)) == g.NRGBAAt(x0+x, y)
				}
			}
			if same {
				return x0
			}
		}
		return -1
	}
	for _, c := range []struct {
		o    ResizeOptions
		want int
	}{
		{ResizeOptions{}, 100},
		{ResizeOptions{Position: West}, 0},
		{ResizeOptions{Position: SouthEast}, 200},
		{ResizeOptions{Position: North}, 100},
	} {
		if got := start(pick(c.o)); got != c.want {
			t.Errorf("%+v: crop from %d, want %d", c.o, got, c.want)
		}
	}
	// Entropy: the crop holds the noisy patch.
	e := decodePNG(t, must(FromImage(m).Resize(100, 100, ResizeOptions{Fit: FitCover, Interesting: InterestingEntropy}).PNG().ToBytes()))
	noisy := 0
	for x := 0; x < 100; x++ {
		if p := color.NRGBAModel.Convert(e.At(x, 50)).(color.NRGBA); p != (color.NRGBA{128, 128, 128, 255}) {
			noisy++
		}
	}
	if noisy < 55 {
		t.Errorf("entropy crop holds %d columns of the patch of 60", noisy)
	}
	// Attention: a skin-toned patch on the left of a dull picture.
	a := image.NewNRGBA(image.Rect(0, 0, 400, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 400; x++ {
			c := color.NRGBA{60, 70, 80, 255}
			if x >= 40 && x < 90 && y >= 30 && y < 70 {
				c = color.NRGBA{224, 172, 140, 255}
			}
			a.SetNRGBA(x, y, c)
		}
	}
	out := decodePNG(t, must(FromImage(a).Resize(100, 100, ResizeOptions{Fit: FitCover, Interesting: InterestingAttention}).PNG().ToBytes()))
	skin := 0
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			if p := color.NRGBAModel.Convert(out.At(x, y)).(color.NRGBA); p.R > 200 {
				skin++
			}
		}
	}
	if skin < 1500 {
		t.Errorf("attention crop shows %d skin pixels of 2000", skin)
	}
	// FitContain puts the image at Position.
	got := decodePNG(t, must(FromImage(m).Resize(400, 200, ResizeOptions{Fit: FitContain, Position: SouthEast, Background: color.White}).PNG().ToBytes()))
	if p := color.NRGBAModel.Convert(got.At(399, 199)).(color.NRGBA); p == (color.NRGBA{255, 255, 255, 255}) {
		t.Error("FitContain SouthEast: the bottom-right corner is background")
	}
	if p := color.NRGBAModel.Convert(got.At(0, 0)).(color.NRGBA); p != (color.NRGBA{255, 255, 255, 255}) {
		t.Errorf("FitContain SouthEast: the top-left corner is %v, not background", p)
	}
	// By name, and the words of the step.
	var words []string
	FromImage(m).Do("resize", "100", "100", "cover", "cubic", "entropy").Around(func(s Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		words = s.Args
		return run(in)
	}).PNG().ToBytes()
	if len(words) != 5 || words[4] != "entropy" {
		t.Errorf("resize words %v", words)
	}
	for _, o := range []ResizeOptions{{Fit: FitCover, Position: CustomPosition}, {Fit: FitCover, Interesting: 3}, {Fit: FitCover, Interesting: -1}} {
		if _, err := FromImage(m).Resize(100, 100, o).PNG().ToBytes(); err == nil {
			t.Errorf("%+v: no error", o)
		}
	}
}

// TestChannelOps: Recomb and Unflatten give sharp 0.35's results to the
// byte (with a 3 x 3 and a 4 x 4 matrix, on images with and without
// transparency, and after Threshold); ExtractChannel and RemoveAlpha give
// the levels of the channel and the colours as stored.
func TestChannelOps(t *testing.T) {
	rgb, rgba := sourcePNG(t, "pillow/source.png"), sourcePNG(t, "pillow/median-rgba-source.png")
	sepia := [][]float64{{0.393, 0.769, 0.189}, {0.349, 0.686, 0.168}, {0.272, 0.534, 0.131}}
	for _, c := range []struct {
		name string
		m    *Image
	}{
		{"recomb", FromBytes(rgb).Recomb(sepia)},
		{"recomb-alpha", FromBytes(rgba).Recomb([][]float64{{0.9, 0.1, 0, 0}, {0, 0.7, 0.5, -0.1}, {0.2, 0, 0.8, 0}, {0.3, 0.3, 0, 0.6}})},
		{"unflatten-alpha", FromBytes(rgba).Unflatten()},
		{"threshold-unflatten", FromBytes(rgb).Threshold(200).Unflatten()},
	} {
		got, err := c.m.ToImage()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if maxd, mean, _ := channelDiff(t, got, readPNG(t, "sharp/"+c.name+".png")); maxd > 0 {
			t.Errorf("%s: differs from sharp by up to %d (mean %.3f)", c.name, maxd, mean)
		}
	}
	// Unflatten of an opaque image is written with its transparency.
	if p, ok := decodePNG(t, must(FromBytes(rgb).Unflatten().PNG().ToBytes())).(*image.NRGBA); !ok || p.Opaque() {
		t.Errorf("Unflatten: written as %T, opaque", p)
	}

	src := decodePNG(t, rgba)
	at := func(x, y int) color.NRGBA { return color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA) }
	for ch, name := range []string{"red", "green", "blue", "alpha"} {
		out := decodePNG(t, must(FromBytes(rgba).ExtractChannel(Channel(ch)).PNG().ToBytes()))
		g, ok := out.(*image.Gray)
		if !ok {
			t.Fatalf("ExtractChannel(%s): %T, want a greyscale image", name, out)
		}
		for y := 0; y < 103; y += 5 {
			for x := 0; x < 150; x += 3 {
				p := at(x, y)
				if want := [4]uint8{p.R, p.G, p.B, p.A}[ch]; g.GrayAt(x, y).Y != want {
					t.Fatalf("ExtractChannel(%s) at %d,%d: %d, want %d", name, x, y, g.GrayAt(x, y).Y, want)
				}
			}
		}
	}
	if g := decodePNG(t, must(FromBytes(rgb).ExtractChannel(ChannelAlpha).PNG().ToBytes())).(*image.Gray); g.GrayAt(7, 7).Y != 255 {
		t.Errorf("the alpha of an opaque image: %d", g.GrayAt(7, 7).Y)
	}
	out, err := FromBytes(rgba).RemoveAlpha().ToImage()
	if err != nil {
		t.Fatal(err)
	}
	for y := 0; y < 103; y += 4 {
		for x := 0; x < 150; x += 3 {
			p, q := at(x, y), color.NRGBAModel.Convert(out.At(x, y)).(color.NRGBA)
			if q != (color.NRGBA{p.R, p.G, p.B, 255}) {
				t.Fatalf("RemoveAlpha at %d,%d: %v, want %v opaque", x, y, q, p)
			}
		}
	}
	if _, ok := decodePNG(t, must(FromBytes(rgba).RemoveAlpha().PNG().ToBytes())).(*image.RGBA); !ok {
		t.Errorf("RemoveAlpha is written with an alpha channel")
	}

	// By name, and the words of the steps.
	var words [][]string
	FromBytes(rgb).Do("recomb", "1", "0", "0", "0", "1", "0", "0", "0", "0.5").Do("extract-channel", "blue").Do("unflatten").Do("remove-alpha").
		Around(func(s Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			words = append(words, append([]string{s.Name}, s.Args...))
			return run(in)
		}).PNG().ToBytes()
	if fmt.Sprint(words) != "[[recomb 1 0 0 0 1 0 0 0 0.5] [extract-channel blue] [unflatten] [remove-alpha]]" {
		t.Errorf("steps %v", words)
	}
	for _, m := range []*Image{
		FromBytes(rgb).Recomb([][]float64{{1, 0}, {0, 1}}),
		FromBytes(rgb).Recomb([][]float64{{1, 0, 0}, {0, 1, 0}, {0, 1}}),
		FromBytes(rgb).Recomb([][]float64{{1, 0, 0}, {0, 1, 0}, {0, 0, math.NaN()}}),
		FromBytes(rgb).ExtractChannel(4),
		FromBytes(rgb).Do("recomb", "1", "2"),
		FromBytes(rgb).Do("extract-channel", "cyan"),
	} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Error("no error")
		}
	}
}

// TestLinearLight: resizing in linear light keeps every level through a
// resize that only repeats pixels, averages light (a fine black and white
// pattern becomes the grey of half the light, not the middle level), and
// keeps a translucent colour.
func TestLinearLight(t *testing.T) {
	// Every level, in colour and in grey, repeated twice over.
	c := image.NewNRGBA(image.Rect(0, 0, 256, 3))
	g := image.NewGray(image.Rect(0, 0, 256, 3))
	for y := 0; y < 3; y++ {
		for x := 0; x < 256; x++ {
			c.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(255 - x), uint8(x * 7), 255})
			g.SetGray(x, y, color.Gray{uint8(x)})
		}
	}
	for _, src := range []image.Image{c, g} {
		out, err := FromImage(src).Resize(512, 6, ResizeOptions{Fit: FitFill, Kernel: KernelNearest, LinearLight: true}).ToImage()
		if err != nil {
			t.Fatal(err)
		}
		for x := 0; x < 512; x++ {
			if got, want := color.NRGBAModel.Convert(out.At(x, 3)), color.NRGBAModel.Convert(src.At(x/2, 1)); got != want {
				t.Fatalf("%T: level at %d is %v, want %v", src, x, got, want)
			}
		}
	}

	// A pattern of black and white pixels, halved: 188 is the sRGB level
	// of half the light of white; without linear light, about 128.
	checker := image.NewNRGBA(image.Rect(0, 0, 64, 64))
	grey := image.NewGray(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			v := uint8(255 * ((x + y) % 2))
			checker.SetNRGBA(x, y, color.NRGBA{v, v, v, 255})
			grey.SetGray(x, y, color.Gray{v})
		}
	}
	for _, src := range []image.Image{checker, grey} {
		for _, threads := range []int{1, 4} {
			for _, k := range []Kernel{KernelLinear, KernelCubic, KernelLanczos3} {
				out, err := FromImage(src).Threads(threads).Resize(32, 32, ResizeOptions{Kernel: k, LinearLight: true}).ToImage()
				if err != nil {
					t.Fatal(err)
				}
				p := color.NRGBAModel.Convert(out.At(16, 16)).(color.NRGBA)
				if p.R < 186 || p.R > 189 || p.R != p.G || p.G != p.B {
					t.Errorf("%T, kernel %v, %d threads: %v, want about 188", src, k, threads, p)
				}
				plain := color.NRGBAModel.Convert(must2(FromImage(src).Resize(32, 32, ResizeOptions{Kernel: k}).ToImage()).At(16, 16)).(color.NRGBA)
				if plain.R < 120 || plain.R > 135 {
					t.Errorf("%T, kernel %v without linear light: %v", src, k, plain)
				}
			}
		}
	}

	// A translucent colour stays itself; what is clear stays clear.
	tr := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for y := 0; y < 40; y++ {
		for x := 20; x < 40; x++ {
			tr.SetNRGBA(x, y, color.NRGBA{200, 60, 30, 128})
		}
	}
	out, err := FromImage(tr).Resize(20, 20, ResizeOptions{LinearLight: true}).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	if p := color.NRGBAModel.Convert(out.At(2, 10)).(color.NRGBA); p.A != 0 {
		t.Errorf("clear area: %v", p)
	}
	if p := color.NRGBAModel.Convert(out.At(16, 10)).(color.NRGBA); p.A != 128 || absDiff(p.R, 200) > 2 || absDiff(p.G, 60) > 2 || absDiff(p.B, 30) > 2 {
		t.Errorf("translucent area: %v, want about {200 60 30 128}", p)
	}

	// The words of the step, and by name.
	var words []string
	FromImage(c).Do("resize", "100", "0", "inside", "cubic", "center", "linear").Around(func(s Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		words = s.Args
		return run(in)
	}).PNG().ToBytes()
	if fmt.Sprint(words) != "[100 0 inside cubic center linear]" {
		t.Errorf("resize words %v", words)
	}
	if _, err := FromImage(c).Do("resize", "100", "0", "inside", "cubic", "center", "gamma").PNG().ToBytes(); err == nil {
		t.Error("an unknown LIGHT word: no error")
	}
}

func must2(img image.Image, err error) image.Image {
	if err != nil {
		panic(err)
	}
	return img
}

func absDiff(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestSharpOptions: the options of Negate, Threshold, Modulate and
// Grayscale that sharp 0.35 has: negate with alpha and threshold per
// channel are its results to the byte (255 - v, and 255 where v is at
// least the level); grayscale linear its greyscale to the byte; modulate
// with lightness within a level.
func TestSharpOptions(t *testing.T) {
	rgb, rgba := sourcePNG(t, "pillow/source.png"), sourcePNG(t, "pillow/median-rgba-source.png")
	src := decodePNG(t, rgba)
	out := decodePNG(t, must(FromBytes(rgba).Negate(NegateOptions{Alpha: true}).PNG().ToBytes()))
	opaque := decodePNG(t, rgb)
	per := decodePNG(t, must(FromBytes(rgb).Threshold(120, ThresholdOptions{PerChannel: true}).PNG().ToBytes()))
	for y := 0; y < 103; y++ {
		for x := 0; x < 150; x++ {
			p, q := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA), color.NRGBAModel.Convert(out.At(x, y)).(color.NRGBA)
			if want := (color.NRGBA{255 - p.R, 255 - p.G, 255 - p.B, 255 - p.A}); q != want && want.A != 0 {
				t.Fatalf("negate alpha at %d,%d: %v, want %v", x, y, q, want)
			}
			o, r := color.NRGBAModel.Convert(opaque.At(x, y)).(color.NRGBA), color.NRGBAModel.Convert(per.At(x, y)).(color.NRGBA)
			th := func(v uint8) uint8 {
				if v >= 120 {
					return 255
				}
				return 0
			}
			if want := (color.NRGBA{th(o.R), th(o.G), th(o.B), 255}); r != want {
				t.Fatalf("threshold per channel at %d,%d: %v, want %v", x, y, r, want)
			}
		}
	}
	// An image without transparency has none to negate.
	if p := color.NRGBAModel.Convert(decodePNG(t, must(FromBytes(rgb).Negate(NegateOptions{Alpha: true}).PNG().ToBytes())).At(5, 5)).(color.NRGBA); p.A != 255 {
		t.Errorf("negate alpha of an opaque image: %v", p)
	}
	for _, c := range []struct {
		name string
		m    *Image
		max  int
	}{
		{"greyscale", FromBytes(rgb).Grayscale(GrayscaleOptions{Linear: true}), 0},
		{"modulate-all", FromBytes(rgb).Modulate(0.8, 1.3, 30, ModulateOptions{Lightness: -10}), 1},
	} {
		got := decodePNG(t, must(c.m.PNG().ToBytes()))
		if maxd, mean, _ := channelDiff(t, got, readPNG(t, "sharp/"+c.name+".png")); maxd > c.max {
			t.Errorf("%s: differs from sharp by up to %d (mean %.3f), want at most %d", c.name, maxd, mean, c.max)
		}
	}
	// By name, and the words of the steps.
	var words [][]string
	FromBytes(rgb).Do("negate", "alpha").Do("threshold", "90", "channels").Do("modulate", "1", "1", "0", "5").Do("grayscale", "linear").
		Around(func(s Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			words = append(words, append([]string{s.Name}, s.Args...))
			return run(in)
		}).PNG().ToBytes()
	if fmt.Sprint(words) != "[[negate alpha] [threshold 90 channels] [modulate 1 1 0 5] [grayscale linear]]" {
		t.Errorf("steps %v", words)
	}
	for _, m := range []*Image{FromBytes(rgb).Do("negate", "all"), FromBytes(rgb).Do("threshold", "9", "each"), FromBytes(rgb).Do("grayscale", "dark"),
		FromBytes(rgb).Modulate(1, 1, 0, ModulateOptions{Lightness: math.NaN()})} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Error("no error")
		}
	}
}

// TestMorphology: Dilate and Erode are sharp's dilate and erode to the
// byte (the AND and OR of the bits of the square), on a photo, on black
// and white after Threshold, with transparency, and with a square wider
// than the image; Rank is Pillow's RankFilter, MinFilter and MaxFilter.
func TestMorphology(t *testing.T) {
	rgb, rgba := sourcePNG(t, "pillow/source.png"), sourcePNG(t, "pillow/median-rgba-source.png")
	for _, c := range []struct {
		name string
		m    *Image
	}{
		{"sharp/dilate-1", FromBytes(rgb).Dilate(1)},
		{"sharp/erode-3", FromBytes(rgb).Erode(3)},
		{"sharp/threshold-dilate-2", FromBytes(rgb).Threshold(128).Dilate(2)},
		{"sharp/threshold-erode-1", FromBytes(rgb).Threshold(128).Erode(1)},
		{"sharp/dilate-rgba-1", FromBytes(rgba).Dilate(1)},
		{"sharp/erode-rgba-200", FromBytes(rgba).Erode(200)},
		{"pillow/min-3", FromBytes(rgb).Rank(3, 0)},
		{"pillow/max-5", FromBytes(rgb).Rank(5, 24)},
		{"pillow/rank-rgba-5-7", FromBytes(rgba).Rank(5, 7)},
	} {
		got := decodePNG(t, must(c.m.PNG().ToBytes()))
		if maxd, mean, _ := channelDiff(t, got, readPNG(t, c.name+".png")); maxd != 0 {
			t.Errorf("%s: differs by up to %d (mean %.3f)", c.name, maxd, mean)
		}
	}
	// On black and white, dilate is the darkest of the square: a white
	// image with one black pixel gets a black square of 2·width+1.
	dot := image.NewGray(image.Rect(0, 0, 40, 30))
	for i := range dot.Pix {
		dot.Pix[i] = 255
	}
	dot.Pix[dot.PixOffset(20, 10)] = 0
	got := decodePNG(t, must(FromImage(dot).Dilate(3).PNG().ToBytes()))
	for y := 0; y < 30; y++ {
		for x := 0; x < 40; x++ {
			black := x >= 17 && x <= 23 && y >= 7 && y <= 13
			if v := color.GrayModel.Convert(got.At(x, y)).(color.Gray).Y; (v == 0) != black {
				t.Fatalf("dilate of a dot at %d,%d: %d", x, y, v)
			}
		}
	}
	// By name, and the words of the steps.
	var words [][]string
	FromBytes(rgb).Do("dilate").Do("erode", "4").Do("rank", "3", "8").
		Around(func(s Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			words = append(words, append([]string{s.Name}, s.Args...))
			return run(in)
		}).PNG().ToBytes()
	if fmt.Sprint(words) != "[[dilate 1] [erode 4] [rank 3 8]]" {
		t.Errorf("steps %v", words)
	}
	for _, m := range []*Image{FromBytes(rgb).Dilate(0), FromBytes(rgb).Erode(65537), FromBytes(rgb).Rank(4, 0), FromBytes(rgb).Rank(3, 9),
		FromBytes(rgb).Rank(3, -1), FromBytes(rgb).Rank(101, 0), FromBytes(rgb).Do("dilate", "x")} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Error("no error")
		}
	}
}

// TestEnsureAlpha: sharp's ensureAlpha, to the byte: the opacity times
// 255 with the fraction dropped, on an image without transparency; one
// with transparency keeps it.
func TestEnsureAlpha(t *testing.T) {
	rgb, rgba := sourcePNG(t, "pillow/source.png"), sourcePNG(t, "pillow/median-rgba-source.png")
	src := decodePNG(t, rgb)
	for alpha, want := range map[float64]uint8{0: 0, 0.25: 63, 0.3: 76, 0.5: 127, 0.999: 254, 1: 255} {
		got := decodePNG(t, must(FromBytes(rgb).EnsureAlpha(alpha).PNG().ToBytes()))
		for _, p := range [][2]int{{0, 0}, {77, 50}, {149, 102}} {
			c, s := color.NRGBAModel.Convert(got.At(p[0], p[1])).(color.NRGBA), color.NRGBAModel.Convert(src.At(p[0], p[1])).(color.NRGBA)
			if want != 0 && (c.R != s.R || c.G != s.G || c.B != s.B) || c.A != want {
				t.Errorf("EnsureAlpha(%v) at %v: %v, want the colour of %v with alpha %d", alpha, p, c, s, want)
			}
		}
	}
	if a, b := must(FromBytes(rgba).EnsureAlpha(0.5).PNG().ToBytes()), must(FromBytes(rgba).PNG().ToBytes()); !bytes.Equal(a, b) {
		t.Error("an image with transparency changed")
	}
	var words [][]string
	FromBytes(rgb).Do("ensure-alpha").Do("ensure-alpha", "0.4").
		Around(func(s Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			words = append(words, append([]string{s.Name}, s.Args...))
			return run(in)
		}).PNG().ToBytes()
	if fmt.Sprint(words) != "[[ensure-alpha 1] [ensure-alpha 0.4]]" {
		t.Errorf("steps %v", words)
	}
	for _, m := range []*Image{FromBytes(rgb).EnsureAlpha(-0.1), FromBytes(rgb).EnsureAlpha(1.01), FromBytes(rgb).EnsureAlpha(math.NaN())} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Error("no error")
		}
	}
}
