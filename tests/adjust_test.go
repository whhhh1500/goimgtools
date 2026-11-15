package tests

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/jpeg"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// readPNG reads a PNG of the test data.
func readPNG(t *testing.T, name string) image.Image {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return decodePNG(t, data)
}

// channelDiff compares the colour channels of two images of the same size
// as 8-bit straight colours: the largest difference, the mean, and the part
// of the values that differ by more than one.
func channelDiff(t *testing.T, got, want image.Image) (maxd int, mean, overOne float64) {
	t.Helper()
	if got.Bounds().Size() != want.Bounds().Size() {
		t.Fatalf("size %v, want %v", got.Bounds().Size(), want.Bounds().Size())
	}
	gb, wb := got.Bounds(), want.Bounds()
	n, over := 0, 0
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			p := color.NRGBAModel.Convert(got.At(gb.Min.X+x, gb.Min.Y+y)).(color.NRGBA)
			q := color.NRGBAModel.Convert(want.At(wb.Min.X+x, wb.Min.Y+y)).(color.NRGBA)
			for _, d := range []int{int(p.R) - int(q.R), int(p.G) - int(q.G), int(p.B) - int(q.B), int(p.A) - int(q.A)} {
				if d < 0 {
					d = -d
				}
				maxd = max(maxd, d)
				mean += float64(d)
				if d > 1 {
					over++
				}
				n++
			}
		}
	}
	return maxd, mean / float64(n), float64(over) / float64(n)
}

// premulDiff is the largest difference between two images of the same size
// in premultiplied 8-bit RGBA, the space the operations compute in.
func premulDiff(t *testing.T, got, want image.Image) int {
	t.Helper()
	if got.Bounds().Size() != want.Bounds().Size() {
		t.Fatalf("size %v, want %v", got.Bounds().Size(), want.Bounds().Size())
	}
	gb, wb := got.Bounds(), want.Bounds()
	worst := 0
	for y := 0; y < wb.Dy(); y++ {
		for x := 0; x < wb.Dx(); x++ {
			p := color.RGBAModel.Convert(got.At(gb.Min.X+x, gb.Min.Y+y)).(color.RGBA)
			q := color.RGBAModel.Convert(want.At(wb.Min.X+x, wb.Min.Y+y)).(color.RGBA)
			worst = max(worst, abs8(p.R, q.R), abs8(p.G, q.G), abs8(p.B, q.B), abs8(p.A, q.A))
		}
	}
	return worst
}

// budgetAfter runs m as a stream with its own Env and reports what was
// still reserved after the stream was read and closed, and the decoded
// result.
func budgetAfter(t *testing.T, m *Image, limit int64, dir string) (image.Image, int64) {
	t.Helper()
	env := pipeline.NewEnv(limit, dir)
	src, err := m.Stream(env)
	if err != nil {
		t.Fatal(err)
	}
	img, err := pipeline.Collect(src)
	if err != nil {
		src.Close()
		t.Fatal(err)
	}
	if err := src.Close(); err != nil {
		t.Fatal(err)
	}
	if peak := env.Budget.Peak(); peak > limit {
		t.Errorf("%d bytes reserved at the peak, the limit is %d", peak, limit)
	}
	return img, env.Budget.Used()
}

// TestGrayscale: every kind of pixel turns into the grey image/color's
// GrayModel gives; an opaque image becomes a one-channel image, a
// transparent one keeps its transparency.
func TestGrayscale(t *testing.T) {
	for name, m := range testutil.Images(61, 37) {
		out, err := FromImage(m).Grayscale().PNG().ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := decodePNG(t, out)
		b := m.Bounds()
		opaque := true
		for y := b.Min.Y; y < b.Max.Y && opaque; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if _, _, _, a := m.At(x, y).RGBA(); a != 0xffff {
					opaque = false
					break
				}
			}
		}
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				src := m.At(b.Min.X+x, b.Min.Y+y)
				var want uint8
				if yc, ok := m.(*image.YCbCr); ok {
					want = yc.Y[yc.YOffset(b.Min.X+x, b.Min.Y+y)] // the stored brightness
				} else {
					n := color.NRGBAModel.Convert(src).(color.NRGBA)
					want, _, _ = color.RGBToYCbCr(n.R, n.G, n.B)
				}
				g := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
				_, _, _, a16 := src.RGBA()
				if g.R != g.G || g.G != g.B {
					t.Fatalf("%s: pixel (%d,%d) is not grey: %v", name, x, y, g)
				}
				// Exact where opaque; translucent colours of 16-bit sources are
				// first brought to 8 bits, which can round the other way.
				if d := int(g.R) - int(want); a16 == 0xffff && d != 0 || a16 != 0 && (d > 1 || d < -1) {
					t.Fatalf("%s: pixel (%d,%d) is %d, want %d", name, x, y, g.R, want)
				}
				if a := uint8(a16 >> 8); g.A != a {
					t.Fatalf("%s: pixel (%d,%d) alpha %d, want %d", name, x, y, g.A, a)
				}
			}
		}
		if _, isGray := got.(*image.Gray); isGray != opaque {
			t.Errorf("%s: opaque %v, written as %T", name, opaque, got)
		}
	}
	// The same as Pillow's convert("L").
	src, err := os.ReadFile(filepath.Join("testdata", "pillow", "source.png"))
	if err != nil {
		t.Fatal(err)
	}
	out, err := FromBytes(src).Grayscale().PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if maxd, mean, _ := channelDiff(t, decodePNG(t, out), readPNG(t, "pillow/gray.png")); maxd != 0 {
		t.Errorf("differs from Pillow by up to %d, %.3f on average", maxd, mean)
	}
	// A JPEG becomes a greyscale JPEG.
	jp, err := FromBytes(jpegBytes(photoLike(160, 120), 90)).Grayscale().JPEG(85).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if c, err := jpeg.DecodeConfig(bytes.NewReader(jp)); err != nil || c.ColorModel != color.GrayModel {
		t.Errorf("grey JPEG: %v, %v", c.ColorModel, err)
	}
}

// labRef converts 8-bit sRGB to L*a*b* (D65) by the formulas, with no
// tables.
func labRef(r, g, b uint8) (L, A, B float64) {
	lin := func(v uint8) float64 {
		c := float64(v) / 255
		if c <= 0.04045 {
			return c / 12.92
		}
		return math.Pow((c+0.055)/1.055, 2.4)
	}
	R, G, Bl := lin(r), lin(g), lin(b)
	X := (0.4124564*R + 0.3575761*G + 0.1804375*Bl) / 0.95047
	Y := 0.2126729*R + 0.7151522*G + 0.0721750*Bl
	Z := (0.0193339*R + 0.1191920*G + 0.9503041*Bl) / 1.08883
	f := func(t float64) float64 {
		if t > 216.0/24389 {
			return math.Cbrt(t)
		}
		return (24389.0/27*t + 16) / 116
	}
	fx, fy, fz := f(X), f(Y), f(Z)
	return 116*fy - 16, 500 * (fx - fy), 200 * (fy - fz)
}

func rgbRef(L, A, B float64) (uint8, uint8, uint8) {
	fy := (L + 16) / 116
	inv := func(v float64) float64 {
		if v > 6.0/29 {
			return v * v * v
		}
		return (116*v - 16) / (24389.0 / 27)
	}
	X, Y, Z := 0.95047*inv(fy+A/500), inv(fy), 1.08883*inv(fy-B/200)
	out := func(v float64) uint8 {
		v = math.Max(0, math.Min(1, v))
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = 1.055*math.Pow(v, 1/2.4) - 0.055
		}
		return uint8(math.Round(v * 255))
	}
	return out(3.2404542*X - 1.5371385*Y - 0.4985314*Z), out(-0.9692660*X + 1.8760108*Y + 0.0415560*Z), out(0.0556434*X - 0.2040259*Y + 1.0572252*Z)
}

// TestModulate: the result is what the formulas of the LCh colour space
// give, computed here without the tables the package uses.
func TestModulate(t *testing.T) {
	src := testutil.Images(97, 61)["nrgba"].(*image.NRGBA)
	in := pngBytes(t, src)
	for _, c := range [][3]float64{{1.2, 0.8, 0}, {1, 1, 120}, {0.5, 2, -45}, {1, 0, 0}, {2, 1.3, 400}} {
		out, err := FromBytes(in).Modulate(c[0], c[1], c[2]).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		got := decodePNG(t, out)
		sin, cos := math.Sincos(c[2] * math.Pi / 180)
		worst := 0
		for y := 0; y < 61; y++ {
			for x := 0; x < 97; x++ {
				p := src.NRGBAAt(x, y)
				g := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
				if g.A != p.A {
					t.Fatalf("%v: alpha changed at (%d,%d)", c, x, y)
				}
				if p.A == 0 {
					continue
				}
				L, A, B := labRef(p.R, p.G, p.B)
				A, B = c[1]*(A*cos-B*sin), c[1]*(A*sin+B*cos)
				r, gg, b := rgbRef(L*c[0], A, B)
				for _, d := range []int{int(g.R) - int(r), int(g.G) - int(gg), int(g.B) - int(b)} {
					worst = max(worst, d, -d)
				}
				if c[1] == 0 && (abs8(g.R, g.G) > 1 || abs8(g.G, g.B) > 1) {
					t.Fatalf("saturation 0: pixel (%d,%d) is not grey: %v", x, y, g)
				}
			}
		}
		if worst > 1 {
			t.Errorf("%v: differs from the formulas by up to %d levels", c, worst)
		}
	}
	// No change leaves the file as it is.
	plain, _ := FromBytes(in).PNG().ToBytes()
	for _, c := range [][3]float64{{1, 1, 0}, {1, 1, 360}, {1, 1, -720}} {
		if same, err := FromBytes(in).Modulate(c[0], c[1], c[2]).PNG().ToBytes(); err != nil || !bytes.Equal(same, plain) {
			t.Errorf("%v changed the image (%v)", c, err)
		}
	}
	for _, bad := range [][3]float64{{-1, 1, 0}, {1, -0.1, 0}, {math.NaN(), 1, 0}, {1, 1, math.Inf(1)}} {
		if _, err := FromBytes(in).Modulate(bad[0], bad[1], bad[2]).ToBytes(); err == nil || errors.Is(err, ErrFormat) {
			t.Errorf("%v: %v", bad, err)
		}
	}
}

func abs8(a, b uint8) int {
	if a > b {
		return int(a - b)
	}
	return int(b - a)
}

// TestBlur: Pillow's GaussianBlur to the byte; no colour leaks out of
// transparent pixels; large blurs keep to the memory limit.
// pillowSource is the image Pillow's references were made from; with hide,
// as an image that does not say it is opaque, so that the filters blur its
// alpha too (all 255, which must come out the same).
func pillowSource(t *testing.T, src []byte, hide bool) *Image {
	if hide {
		return FromImage(struct{ image.Image }{decodePNG(t, src)})
	}
	return FromBytes(src)
}

func TestBlur(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "pillow", "source.png"))
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{"0.7", "2", "10", "40"} {
		var sigma float64
		fmt.Sscan(s, &sigma)
		for _, hide := range []bool{false, true} {
			out, err := pillowSource(t, src, hide).Blur(sigma).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			maxd, mean, _ := channelDiff(t, decodePNG(t, out), readPNG(t, "pillow/blur-"+s+".png"))
			if maxd != 0 {
				t.Errorf("sigma %s (alpha blurred too: %v): differs from Pillow by up to %d, %.3f on average", s, hide, maxd, mean)
			}
			if info, err := FromBytes(out).Metadata(); !hide && (err != nil || info.HasAlpha) {
				t.Errorf("sigma %s: an opaque image came out with alpha (%v)", s, err)
			}
		}
	}

	// A red square on a transparent ground: wherever the blur reaches, the
	// colour is red, only less opaque. (Blurring colours that are not
	// premultiplied would darken the edge towards the black of the
	// transparent pixels.)
	sq := image.NewNRGBA(image.Rect(0, 0, 60, 60))
	stddraw.Draw(sq, image.Rect(20, 20, 40, 40), image.NewUniform(color.NRGBA{255, 0, 0, 255}), image.Point{}, stddraw.Src)
	out, err := FromImage(sq).Blur(4).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	got := decodePNG(t, out)
	for y := 0; y < 60; y++ {
		for x := 0; x < 60; x++ {
			c := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
			if c.A > 8 && (c.R < 240 || c.G > 10 || c.B > 10) {
				t.Fatalf("pixel (%d,%d) = %v: colour leaked from the transparent ground", x, y, c)
			}
		}
	}

	// A wide image with a large blur: under a small limit its rows go to
	// temporary files, with the same result as in memory, and everything
	// is given back.
	wide := photoLike(3000, 120)
	in := pngBytes(t, wide)
	dir := t.TempDir()
	big, used := budgetAfter(t, FromBytes(in).Blur(150), 64<<20, dir)
	small, used2 := budgetAfter(t, FromBytes(in).Blur(150), 2<<20, dir)
	if used != 0 || used2 != 0 {
		t.Errorf("%d and %d bytes still reserved", used, used2)
	}
	sameImage(t, "large blur on disk and in memory", small, big)
	// Blurs whose window is more than a few rows tall, on disk: the rows
	// read back must not be overwritten while a pass still uses them.
	tall := pngBytes(t, photoLike(3000, 300))
	for _, sigma := range []float64{4, 10, 40} {
		big, _ := budgetAfter(t, FromBytes(tall).Blur(sigma), 64<<20, dir)
		small, _ := budgetAfter(t, FromBytes(tall).Blur(sigma), 2<<20, dir)
		sameImage(t, fmt.Sprintf("blur %v on disk and in memory", sigma), small, big)
		big, _ = budgetAfter(t, FromBytes(tall).Sharpen(SharpenOptions{Sigma: sigma}), 64<<20, dir)
		small, _ = budgetAfter(t, FromBytes(tall).Sharpen(SharpenOptions{Sigma: sigma}), 2<<20, dir)
		sameImage(t, fmt.Sprintf("unsharp mask %v on disk and in memory", sigma), small, big)
	}
	big, _ = budgetAfter(t, FromBytes(tall).Sharpen(), 64<<20, dir)
	small, _ = budgetAfter(t, FromBytes(tall).Sharpen(), 2<<20, dir)
	sameImage(t, "mild sharpen on disk and in memory", small, big)
	if n := tempFiles(dir); n != 0 {
		t.Errorf("%d temporary files left", n)
	}

	for _, bad := range []float64{0, -1, 1000.5, math.NaN(), math.Inf(1)} {
		if _, err := FromBytes(src).Blur(bad).ToBytes(); err == nil {
			t.Errorf("Blur(%v) accepted", bad)
		}
	}
}

// TestSharpen: the unsharp mask is Pillow's UnsharpMask to the byte; the
// mild sharpen its 3x3 kernel as computed here.
func TestSharpen(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "pillow", "source.png"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		radius             float64
		percent, threshold int
	}{{2, 150, 3}, {1, 100, 0}, {5, 250, 10}} {
		for _, hide := range []bool{false, true} {
			out, err := pillowSource(t, src, hide).Sharpen(SharpenOptions{Sigma: c.radius, Amount: float64(c.percent) / 100, Threshold: c.threshold}).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			maxd, mean, _ := channelDiff(t, decodePNG(t, out), readPNG(t, fmt.Sprintf("pillow/usm-%v-%d-%d.png", c.radius, c.percent, c.threshold)))
			if maxd != 0 {
				t.Errorf("%+v (alpha blurred too: %v): differs from Pillow by up to %d, %.3f on average", c, hide, maxd, mean)
			}
		}
	}

	// The mild sharpen: -1 around 32, divided by 24, the edges repeated.
	// Both ways: as an opaque image and as one that does not say so.
	m := photoLike(83, 47)
	at := func(x, y, c int) int {
		x, y = max(0, min(82, x)), max(0, min(46, y))
		return int(m.Pix[m.PixOffset(x, y)+c])
	}
	for _, im := range []image.Image{m, struct{ image.Image }{m}} {
		out, err := FromImage(im).Sharpen().PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		got := decodePNG(t, out)
		for y := 0; y < 47; y++ {
			for x := 0; x < 83; x++ {
				g := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
				if g.A != 255 {
					t.Fatalf("mild sharpen at (%d,%d): alpha %d", x, y, g.A)
				}
				for c, v := range []uint8{g.R, g.G, g.B} {
					sum := 32 * at(x, y, c)
					for dy := -1; dy <= 1; dy++ {
						for dx := -1; dx <= 1; dx++ {
							if dx != 0 || dy != 0 {
								sum -= at(x+dx, y+dy, c)
							}
						}
					}
					want := max(0, min(255, int(math.Round(float64(sum)/24))))
					if d := int(v) - want; d > 1 || d < -1 {
						t.Fatalf("mild sharpen at (%d,%d) channel %d: %d, want %d", x, y, c, v, want)
					}
				}
			}
		}
	}

	for _, bad := range []SharpenOptions{{Amount: 2}, {Threshold: 3}, {Sigma: -1}, {Sigma: 2000}, {Sigma: 1, Amount: -1}, {Sigma: 1, Threshold: 256}, {Sigma: math.NaN()}} {
		if _, err := FromBytes(src).Sharpen(bad).ToBytes(); err == nil {
			t.Errorf("%+v accepted", bad)
		}
	}
	if _, err := FromBytes(src).Sharpen(SharpenOptions{}, SharpenOptions{}).ToBytes(); err == nil {
		t.Error("two options accepted")
	}
}

// TestExtend: the border is where it was asked, in its colour.
func TestExtend(t *testing.T) {
	m := testutil.Images(40, 30)["nrgba"]
	for _, bg := range []color.Color{nil, color.White, color.Transparent, color.NRGBA{10, 200, 30, 128}} {
		out, err := FromImage(m).Extend(3, 5, 7, 11, bg).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		want := image.NewNRGBA(image.Rect(0, 0, 40+5+11, 30+3+7))
		fill := bg
		if fill == nil {
			fill = color.Black
		}
		stddraw.Draw(want, want.Bounds(), image.NewUniform(fill), image.Point{}, stddraw.Src)
		stddraw.Draw(want, image.Rect(11, 3, 51, 33), m, image.Point{}, stddraw.Src)
		if d := premulDiff(t, decodePNG(t, out), want); d > 1 {
			t.Errorf("border %v: off by %d", bg, d)
		}
	}
	if _, err := FromImage(m).Extend(0, -1, 0, 0, nil).ToBytes(); err == nil {
		t.Error("a negative border accepted")
	}
	// 40040 x 40030 pixels: beyond the default limit of 16383 x 16383.
	if _, err := FromImage(m).Extend(20000, 20000, 20000, 20000, nil).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("a border beyond the pixel limit: %v", err)
	}
	if w, h, err := FromImage(m).Extend(1, 2, 3, 4, nil).OutputSize(); err != nil || w != 46 || h != 34 {
		t.Errorf("OutputSize %dx%d, %v", w, h, err)
	}
}

// TestTrim: the edges of the top-left colour go, within the threshold.
func TestTrim(t *testing.T) {
	content := photoLike(120, 80)
	page := image.NewRGBA(image.Rect(0, 0, 200, 150))
	stddraw.Draw(page, page.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	stddraw.Draw(page, image.Rect(37, 21, 157, 101), content, image.Point{}, stddraw.Src)

	out, err := FromImage(page).Trim(0).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	sameImage(t, "exact border", decodePNG(t, out), content)
	if w, h, err := FromImage(page).Trim(0).OutputSize(); err != nil || w != 120 || h != 80 {
		t.Errorf("OutputSize %dx%d, %v", w, h, err)
	}

	// A JPEG: the white is no longer exactly white, and its blocks smear
	// the edge a little.
	jp := jpegBytes(page, 90)
	w0, h0, err := FromBytes(jp).Trim(0).OutputSize()
	if err != nil || w0 < 120 || h0 < 80 {
		t.Errorf("JPEG with threshold 0: %dx%d, %v", w0, h0, err)
	}
	w, h, err := FromBytes(jp).Trim(40).OutputSize()
	if err != nil || w < 120 || w > min(w0, 136) || h < 80 || h > min(h0, 96) {
		t.Errorf("JPEG with threshold 40: %dx%d, %v; want about 120x80, and no more than with 0 (%dx%d)", w, h, err, w0, h0)
	}

	// Transparent edges: any colour, as long as it is invisible.
	clear := image.NewNRGBA(image.Rect(0, 0, 50, 40))
	for i := 0; i < len(clear.Pix); i += 4 {
		clear.Pix[i] = uint8(i) // colour under alpha 0
	}
	stddraw.Draw(clear, image.Rect(10, 5, 30, 25), image.NewUniform(color.NRGBA{0, 0, 255, 255}), image.Point{}, stddraw.Src)
	if w, h, err := FromImage(clear).Trim(0).OutputSize(); err != nil || w != 20 || h != 20 {
		t.Errorf("transparent edges: %dx%d, %v", w, h, err)
	}
	// One colour throughout: nothing to cut, nothing cut.
	plain := image.NewGray(image.Rect(0, 0, 30, 20))
	if w, h, err := FromImage(plain).Trim(10).OutputSize(); err != nil || w != 30 || h != 20 {
		t.Errorf("one colour: %dx%d, %v", w, h, err)
	}
	// A single different pixel in the far corner is all that is left.
	plain.SetGray(29, 19, color.Gray{200})
	if w, h, err := FromImage(plain).Trim(10).OutputSize(); err != nil || w != 1 || h != 1 {
		t.Errorf("corner pixel: %dx%d, %v", w, h, err)
	}
	dot := image.NewGray(image.Rect(0, 0, 30, 20))
	dot.SetGray(17, 4, color.Gray{200})
	if w, h, err := FromImage(dot).Trim(10).OutputSize(); err != nil || w != 1 || h != 1 {
		t.Errorf("one pixel: %dx%d, %v", w, h, err)
	}

	// Large, under a small limit: recorded to disk, everything given back.
	dir := t.TempDir()
	bigPage := image.NewRGBA(image.Rect(0, 0, 2000, 1500))
	stddraw.Draw(bigPage, bigPage.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
	stddraw.Draw(bigPage, image.Rect(300, 200, 1800, 1300), photoLike(1500, 1100), image.Point{}, stddraw.Src)
	img, used := budgetAfter(t, FromBytes(pngBytes(t, bigPage)).Trim(0), 2<<20, dir)
	if used != 0 || img.Bounds().Size() != image.Pt(1500, 1100) {
		t.Errorf("large: %v, %d bytes still reserved", img.Bounds().Size(), used)
	}
	if n := tempFiles(dir); n != 0 {
		t.Errorf("%d temporary files left", n)
	}
	for _, bad := range []float64{-1, 256, math.NaN()} {
		if _, err := FromImage(page).Trim(bad).ToBytes(); err == nil {
			t.Errorf("Trim(%v) accepted", bad)
		}
	}
	// A damaged input is found while trimming, and is a bad image.
	if _, err := FromBytes(jp[:len(jp)/2]).Trim(10).ToBytes(); !errors.Is(err, ErrFormat) {
		t.Errorf("damaged input: %v", err)
	}
}

// rotateRef is RotateBy computed directly: every output pixel is the
// bilinear interpolation of the premultiplied source at the point the
// output's centre turned back by the angle reaches, the background beyond
// the source.
func rotateRef(src image.Image, deg float64, bg color.Color) *image.RGBA {
	b := src.Bounds()
	w, h := float64(b.Dx()), float64(b.Dy())
	sin, cos := math.Sincos(deg * math.Pi / 180)
	ow := int(math.Ceil(w*math.Abs(cos) + h*math.Abs(sin) - 1e-9))
	oh := int(math.Ceil(w*math.Abs(sin) + h*math.Abs(cos) - 1e-9))
	out := image.NewRGBA(image.Rect(0, 0, ow, oh))
	br, bgg, bb, ba := bg.RGBA()
	back := [4]float64{float64(br >> 8), float64(bgg >> 8), float64(bb >> 8), float64(ba >> 8)}
	px := func(x, y int) [4]float64 {
		if x < 0 || y < 0 || x >= b.Dx() || y >= b.Dy() {
			return back
		}
		c := color.RGBAModel.Convert(src.At(b.Min.X+x, b.Min.Y+y)).(color.RGBA)
		return [4]float64{float64(c.R), float64(c.G), float64(c.B), float64(c.A)}
	}
	for oy := 0; oy < oh; oy++ {
		for ox := 0; ox < ow; ox++ {
			dx, dy := float64(ox)+0.5-float64(ow)/2, float64(oy)+0.5-float64(oh)/2
			sx := dx*cos + dy*sin + w/2 - 0.5
			sy := -dx*sin + dy*cos + h/2 - 0.5
			x0, y0 := int(math.Floor(sx)), int(math.Floor(sy))
			fx, fy := sx-float64(x0), sy-float64(y0)
			var acc [4]float64
			for k, wgt := range []float64{(1 - fx) * (1 - fy), fx * (1 - fy), (1 - fx) * fy, fx * fy} {
				p := px(x0+k%2, y0+k/2)
				for c := range acc {
					acc[c] += wgt * p[c]
				}
			}
			a := math.Min(255, math.Round(acc[3]))
			out.SetRGBA(ox, oy, color.RGBA{uint8(math.Min(a, math.Round(acc[0]))), uint8(math.Min(a, math.Round(acc[1]))), uint8(math.Min(a, math.Round(acc[2]))), uint8(a)})
		}
	}
	return out
}

// TestRotateBy: any angle, as computed directly; quarter turns exactly as
// Rotate; turned there and back, the picture again.
func TestRotateBy(t *testing.T) {
	src := testutil.Images(73, 41)["nrgba"]
	for _, deg := range []float64{30, -10, 135, 200.5, 359, 0.25} {
		for _, bg := range []color.Color{color.Transparent, color.NRGBA{255, 0, 255, 255}} {
			out, err := FromImage(src).RotateBy(deg, bg).PNG().ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			if d := premulDiff(t, decodePNG(t, out), rotateRef(src, deg, bg)); d > 1 {
				t.Errorf("%v degrees on %v: differs from the direct computation by up to %d", deg, bg, d)
			}
		}
	}
	// Quarter turns are Rotate's, to the byte.
	in := pngBytes(t, src)
	for _, c := range []struct {
		deg float64
		rot int
	}{{90, 90}, {-90, 270}, {180, 180}, {450, 90}, {-270, 90}, {0, 0}, {360, 0}} {
		a, err := FromBytes(in).RotateBy(c.deg, nil).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		b, _ := FromBytes(in).Rotate(c.rot).PNG().ToBytes()
		if !bytes.Equal(a, b) {
			t.Errorf("RotateBy(%v) differs from Rotate(%d)", c.deg, c.rot)
		}
	}
	// There and back: the middle of the picture is what it was.
	photo := photoLike(300, 200)
	back, err := FromImage(photo).RotateBy(25, nil).RotateBy(-25, nil).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	bi := decodePNG(t, back)
	bb := bi.Bounds()
	cx, cy := bb.Dx()/2-100, bb.Dy()/2-66
	mid := image.NewRGBA(image.Rect(0, 0, 200, 133))
	stddraw.Draw(mid, mid.Bounds(), bi, image.Pt(cx, cy), stddraw.Src)
	orig := photo.SubImage(image.Rect(50, 33, 250, 166))
	if _, mean, _ := channelDiff(t, mid, orig); mean > 12 {
		t.Errorf("turned there and back: %.1f levels off on average", mean)
	}
	// Size, an opaque result for an opaque background, the pixel limit.
	if w, h, err := FromImage(photo).RotateBy(30, nil).OutputSize(); err != nil || w != 360 || h != 324 {
		t.Errorf("OutputSize %dx%d, %v; want 360x324", w, h, err)
	}
	opq, _ := FromImage(photo).RotateBy(30, color.White).PNG().ToBytes()
	if info, _ := FromBytes(opq).Metadata(); info.HasAlpha {
		t.Error("an opaque picture on an opaque background came out with alpha")
	}
	if _, err := FromImage(photo).Resize(16000, 0, ResizeOptions{Kernel: KernelNearest}).RotateBy(45, nil).ToBytes(); !errors.Is(err, ErrPixelLimit) {
		t.Errorf("a turn beyond the pixel limit: %v", err)
	}
	for _, bad := range []float64{math.NaN(), math.Inf(-1)} {
		if _, err := FromImage(photo).RotateBy(bad, nil).ToBytes(); err == nil {
			t.Errorf("RotateBy(%v) accepted", bad)
		}
	}
	// Large, under a small limit: tiles on disk, the same pixels as in
	// memory, everything given back.
	dir := t.TempDir()
	large := pngBytes(t, photoLike(1500, 1000))
	inMem, used := budgetAfter(t, FromBytes(large).RotateBy(33, nil), 256<<20, dir)
	onDisk, used2 := budgetAfter(t, FromBytes(large).RotateBy(33, nil), 2<<20, dir)
	if used != 0 || used2 != 0 {
		t.Errorf("%d and %d bytes still reserved", used, used2)
	}
	sameImage(t, "turn on disk and in memory", onDisk, inMem)
	if n := tempFiles(dir); n != 0 {
		t.Errorf("%d temporary files left", n)
	}
}

// TestAdjustTogether: the new operations in one chain with the old ones,
// on an animation and through the command-line tests' formats.
func TestAdjustTogether(t *testing.T) {
	jp := jpegBytes(photoLike(640, 480), 90)
	out, err := FromBytes(jp).Rotate().Trim(10).Resize(320, 0).Extend(8, 8, 8, 8, color.White).
		Modulate(1.05, 1.1, 0).Sharpen().RotateBy(3, color.White).JPEG(85).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if c, err := jpeg.DecodeConfig(bytes.NewReader(out)); err != nil || c.Width < 336 || c.Height < 250 {
		t.Errorf("chain: %+v, %v", c, err)
	}
	for _, threads := range []int{1, 2, 3, 4, 8} {
		again, err := FromBytes(jp).Rotate().Trim(10).Resize(320, 0).Extend(8, 8, 8, 8, color.White).
			Modulate(1.05, 1.1, 0).Sharpen().RotateBy(3, color.White).Threads(threads).JPEG(85).ToBytes()
		if err != nil || !bytes.Equal(again, out) {
			t.Errorf("threads %d: another file (%v)", threads, err)
		}
	}
	if _, err := png.Decode(bytes.NewReader(must(FromBytes(jp).Grayscale().Blur(2).PNG().ToBytes()))); err != nil {
		t.Error(err)
	}
	if !strings.Contains(fmt.Sprint(ErrFormat), "readable") {
		t.Skip()
	}
}
