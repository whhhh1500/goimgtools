package tests

import (
	"bytes"
	"encoding/hex"
	"image"
	"image/color"
	stddraw "image/draw"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/image/draw"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

func pngBytes(t *testing.T, m image.Image) []byte {
	var buf bytes.Buffer
	if err := png.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jpegBytes(m image.Image, q int) []byte {
	var buf bytes.Buffer
	jpeg.Encode(&buf, m, &jpeg.Options{Quality: q})
	return buf.Bytes()
}

func scaled(m image.Image, w, h int) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), m, m.Bounds(), draw.Src, nil)
	return dst
}

// TestChain runs shrink + white background + watermark + JPEG and requires
// the same bytes as the same steps done with the standard library on whole
// images.
func TestChain(t *testing.T) {
	imgs := testutil.Images(613, 457)
	logo := testutil.Images(90, 40)["nrgba"]
	logoPNG := pngBytes(t, logo)
	for _, name := range []string{"nrgba", "rgba-opaque", "gray", "paletted", "nrgba64"} {
		in := pngBytes(t, imgs[name])
		got, err := FromBytes(in).
			Resize(200, 0).
			Flatten(color.White).
			Composite(FromBytes(logoPNG).Resize(45, 0), CompositeOptions{Gravity: SouthEast, Left: -7, Top: -5}).
			JPEG(85).
			ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// Reference. 613x457 -> 200x149; logo 90x40 -> 45x20.
		dec, _ := png.Decode(bytes.NewReader(in))
		logoDec, _ := png.Decode(bytes.NewReader(logoPNG))
		small := scaled(dec, 200, 149)
		ref := image.NewRGBA(small.Bounds())
		stddraw.Draw(ref, ref.Bounds(), image.NewUniform(color.White), image.Point{}, stddraw.Src)
		stddraw.Draw(ref, ref.Bounds(), small, image.Point{}, stddraw.Over)
		wm := scaled(logoDec, 45, 20)
		at := image.Rect(0, 0, 45, 20).Add(image.Pt(200-45-7, 149-20-5))
		stddraw.Draw(ref, at, wm, image.Point{}, stddraw.Over)
		if !bytes.Equal(got, jpegBytes(ref, 85)) {
			t.Errorf("%s: output differs from the reference", name)
		}
	}
}

func TestExtractAndFit(t *testing.T) {
	m := testutil.Images(613, 457)["nrgba"].(*image.NRGBA)
	in := pngBytes(t, m)

	got, err := FromBytes(in).Extract(100, 50, 301, 207).JPEG(90).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, jpegBytes(m.SubImage(image.Rect(100, 50, 401, 257)), 90)) {
		t.Error("extract differs from the reference")
	}

	// Cover: 613x457 into 100x100 -> scale to 134x100, crop 17 px each side.
	got, err = FromBytes(in).Resize(100, 100, ResizeOptions{Fit: FitCover}).JPEG(90).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, jpegBytes(scaled(m, 134, 100).SubImage(image.Rect(17, 0, 117, 100)), 90)) {
		t.Error("cover differs from the reference")
	}
	// Without options the image is fitted inside the box, nothing cut off:
	// 613x457 into 100x100 -> 100x75.
	got, err = FromBytes(in).Resize(100, 100).JPEG(90).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, jpegBytes(scaled(m, 100, 75), 90)) {
		t.Error("the default fit is not FitInside")
	}

	for _, c := range []struct {
		w, h   int
		o      ResizeOptions
		ww, wh int
	}{
		{300, 300, ResizeOptions{}, 300, 224},
		{300, 300, ResizeOptions{Fit: FitInside}, 300, 224},
		{300, 300, ResizeOptions{Fit: FitCover}, 300, 300},
		{300, 300, ResizeOptions{Fit: FitContain}, 300, 300},
		{300, 300, ResizeOptions{Fit: FitOutside}, 402, 300},
		{300, 300, ResizeOptions{Fit: FitFill}, 300, 300},
		{0, 100, ResizeOptions{}, 134, 100},
		{2000, 2000, ResizeOptions{Fit: FitInside, WithoutEnlargement: true}, 613, 457},
		{2000, 100, ResizeOptions{Fit: FitInside, WithoutEnlargement: true}, 134, 100},
	} {
		recipe := FromBytes(in).Resize(c.w, c.h, c.o)
		w, h, err := recipe.OutputSize()
		if err != nil {
			t.Fatal(err)
		}
		if w != c.ww || h != c.wh {
			t.Errorf("Resize(%d,%d,%+v) = %dx%d, want %dx%d", c.w, c.h, c.o, w, h, c.ww, c.wh)
		}
		// The size announced is the size written.
		out, err := recipe.PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if got := decodePNG(t, out).Bounds().Size(); got != image.Pt(c.ww, c.wh) {
			t.Errorf("Resize(%d,%d,%+v) wrote %v, OutputSize said %dx%d", c.w, c.h, c.o, got, w, h)
		}
		// Metadata is about the input, whatever the recipe does to it.
		info, err := recipe.Metadata()
		if err != nil || info.Width != 613 || info.Height != 457 || info.Format != "png" || !info.HasAlpha {
			t.Errorf("Metadata of a recipe with a resize: %+v, %v", info, err)
		}
	}
}

func TestToFileAndErrors(t *testing.T) {
	dir := t.TempDir()
	in := pngBytes(t, testutil.Images(120, 300)["nrgba"])
	src := filepath.Join(dir, "in.png")
	os.WriteFile(src, in, 0o644)
	out := filepath.Join(dir, "out.jpg")
	if err := Open(src).Resize(60, 0).ToFile(out); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeJPEG(bytes.NewReader(must(os.ReadFile(out)))); err != nil {
		t.Fatal(err)
	}
	// A truncated input must leave no output file and no temp file.
	os.WriteFile(src, in[:len(in)-40], 0o644)
	bad := filepath.Join(dir, "bad.jpg")
	if err := Open(src).ToFile(bad); err == nil {
		t.Fatal("truncated input: no error")
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 2 {
		t.Fatalf("leftover files: %v", names)
	}
	// A limit too small for one strip is reported as ErrMemoryLimit.
	if err := FromBytes(in).MemoryLimit(1 << 10).JPEG(0).ToWriter(&bytes.Buffer{}); err == nil {
		t.Fatal("tiny memory limit: no error")
	}
	if _, err := FromBytes([]byte("hello, not an image")).Metadata(); err == nil {
		t.Fatal("garbage input: no error")
	}
}

func must(b []byte, err error) []byte {
	if err != nil {
		panic(err)
	}
	return b
}

func TestInputLimits(t *testing.T) {
	in := pngBytes(t, testutil.Images(120, 300)["gray"])
	if _, err := FromBytes(in).LimitInputPixels(120*300 - 1).JPEG(0).ToBytes(); err == nil {
		t.Error("image above the pixel limit: no error")
	}
	if _, err := FromBytes(in).LimitInputPixels(120 * 300).JPEG(0).ToBytes(); err != nil {
		t.Errorf("image at the pixel limit: %v", err)
	}
	// A GIF whose frame is empty decodes with the standard library; here it
	// is reported instead of producing an empty file.
	empty, _ := hex.DecodeString("47494638396101000100800000000000ffffff2c00000000000000000002012c003b")
	if _, err := gif.Decode(bytes.NewReader(empty)); err != nil {
		t.Fatalf("the empty GIF is not valid for the standard library: %v", err)
	}
	if _, err := FromBytes(empty).JPEG(0).ToBytes(); err == nil {
		t.Error("empty GIF frame: no error")
	}
}

// defaultThreads is what the tests have set with SetDefaultThreads.
var defaultThreads = 1

// TestThreads repeats the tests above with the decoder, and then the encoder
// too, on their own goroutines: every result must be what one thread gives.
func TestThreads(t *testing.T) {
	defer func() { SetDefaultThreads(1); defaultThreads = 1 }()
	for _, n := range []int{2, 3} {
		SetDefaultThreads(n)
		defaultThreads = n
		TestChain(t)
		TestExtractAndFit(t)
		TestToFileAndErrors(t)
		TestInputLimits(t)
		TestEveryFormat(t)
		TestFormatsThroughChain(t)
		TestPNGOutput(t)
		TestMemoryLimitAndDisk(t)
		TestNotImages(t)
	}
}

// TestFastShrinkOnLoad: shrinking a JPEG while decoding gives the same size
// and nearly the same picture as resizing the full image, and is left out
// where it does not apply.
func TestFastShrinkOnLoad(t *testing.T) {
	// A smooth picture, so the difference measured is the method's and not
	// noise being averaged differently.
	smooth := func(w, h int) *image.RGBA {
		m := image.NewRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				m.SetRGBA(x, y, color.RGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + y) * 255 / (w + h)), 255})
			}
		}
		return m
	}
	decode := func(b []byte) image.Image {
		m, err := decodeJPEG(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	fast := ResizeOptions{Fit: FitInside, FastShrinkOnLoad: true}
	slow := ResizeOptions{Fit: FitInside}
	for _, size := range [][2]int{{640, 480}, {641, 479}, {1001, 333}, {97, 1203}} {
		for _, gray := range []bool{false, true} {
			var src image.Image = smooth(size[0], size[1])
			if gray {
				g := image.NewGray(src.Bounds())
				stddraw.Draw(g, g.Bounds(), src, image.Point{}, stddraw.Src)
				src = g
			}
			in := jpegBytes(src, 90)
			for _, box := range []int{300, 150, 61, 20} { // factors 2, 4, 8 and beyond
				a, err := FromBytes(in).Resize(box, box, fast).JPEG(95).ToBytes()
				if err != nil {
					t.Fatal(err)
				}
				b, err := FromBytes(in).Resize(box, box, slow).JPEG(95).ToBytes()
				if err != nil {
					t.Fatal(err)
				}
				ma, mb := decode(a), decode(b)
				if ma.Bounds() != mb.Bounds() {
					t.Fatalf("%v box %d: size %v with the option, %v without", size, box, ma.Bounds(), mb.Bounds())
				}
				if bytes.Equal(a, b) {
					t.Errorf("%v box %d: the option changed nothing", size, box)
				}
				var sum, n int
				r := ma.Bounds()
				for y := r.Min.Y; y < r.Max.Y; y++ {
					for x := r.Min.X; x < r.Max.X; x++ {
						r1, g1, b1, _ := ma.At(x, y).RGBA()
						r2, g2, b2, _ := mb.At(x, y).RGBA()
						for _, d := range []int{int(r1>>8) - int(r2>>8), int(g1>>8) - int(g2>>8), int(b1>>8) - int(b2>>8)} {
							sum += max(d, -d)
							n++
						}
					}
				}
				// Shrunk to 2 pixels across, the picture whose planes are
				// scaled differs by 2.2 levels (1.75 with an RGBA scale).
				limit := 2.0
				if r.Dx() < 4 {
					limit = 2.5
				}
				if mean := float64(sum) / float64(n); mean > limit {
					t.Errorf("%v gray=%v box %d: mean difference %.2f levels", size, gray, box, mean)
				}
			}
		}
	}
	in := jpegBytes(smooth(640, 480), 90)
	same := func(what string, a, b *Image) {
		t.Helper()
		x, err := a.JPEG(90).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		y, err := b.JPEG(90).ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(x, y) {
			t.Errorf("%s: the option changed the output", what)
		}
	}
	// Less than a 2× reduction; not the first step; not a JPEG.
	same("small reduction", FromBytes(in).Resize(400, 400, fast), FromBytes(in).Resize(400, 400, slow))
	same("after a crop", FromBytes(in).Extract(1, 1, 600, 400).Resize(100, 100, fast), FromBytes(in).Extract(1, 1, 600, 400).Resize(100, 100, slow))
	p := pngBytes(t, smooth(640, 480))
	same("png input", FromBytes(p).Resize(100, 100, fast), FromBytes(p).Resize(100, 100, slow))
}

// TestFastShrinkOnLoadWebP: a lossy WebP without alpha reduced to a third
// or less is decoded at twice the size of the result (libwebp's scaling):
// the same size, pixels close to those without the option. A lossless one,
// one with alpha and a smaller reduction are left alone.
func TestFastShrinkOnLoadWebP(t *testing.T) {
	smooth := func(w, h int, alpha bool) *image.NRGBA {
		m := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				a := uint8(255)
				if alpha {
					a = uint8(x * 255 / w)
				}
				m.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + y) * 255 / (w + h)), a})
			}
		}
		return m
	}
	fast := ResizeOptions{Fit: FitInside, FastShrinkOnLoad: true}
	slow := ResizeOptions{Fit: FitInside}
	for _, size := range [][2]int{{640, 480}, {641, 479}, {1001, 333}, {97, 1203}} {
		in := must(FromImage(smooth(size[0], size[1], false)).WebP(WebPOptions{Quality: 90}).ToBytes())
		for _, box := range []int{200, 150, 61, 20} {
			ma := decodePNG(t, must(FromBytes(in).Resize(box, box, fast).PNG().ToBytes()))
			mb := decodePNG(t, must(FromBytes(in).Resize(box, box, slow).PNG().ToBytes()))
			if ma.Bounds() != mb.Bounds() {
				t.Fatalf("%v box %d: size %v with the option, %v without", size, box, ma.Bounds(), mb.Bounds())
			}
			var sum, n int
			r := ma.Bounds()
			for y := r.Min.Y; y < r.Max.Y; y++ {
				for x := r.Min.X; x < r.Max.X; x++ {
					r1, g1, b1, _ := ma.At(x, y).RGBA()
					r2, g2, b2, _ := mb.At(x, y).RGBA()
					for _, d := range []int{int(r1>>8) - int(r2>>8), int(g1>>8) - int(g2>>8), int(b1>>8) - int(b2>>8)} {
						sum += max(d, -d)
						n++
					}
				}
			}
			if sum == 0 {
				t.Errorf("%v box %d: the option changed nothing", size, box)
			}
			if mean := float64(sum) / float64(n); mean > 1.5 {
				t.Errorf("%v box %d: mean difference %.2f levels", size, box, mean)
			}
		}
	}
	same := func(what string, in []byte, box int) {
		t.Helper()
		a := must(FromBytes(in).Resize(box, box, fast).PNG().ToBytes())
		b := must(FromBytes(in).Resize(box, box, slow).PNG().ToBytes())
		if !bytes.Equal(a, b) {
			t.Errorf("%s: the option changed the output", what)
		}
	}
	same("lossless", must(FromImage(smooth(640, 480, false)).WebP(WebPOptions{Lossless: true}).ToBytes()), 100)
	same("alpha", must(FromImage(smooth(640, 480, true)).WebP(WebPOptions{Quality: 90}).ToBytes()), 100)
	same("small reduction", must(FromImage(smooth(640, 480, false)).WebP(WebPOptions{Quality: 90}).ToBytes()), 300)
}
