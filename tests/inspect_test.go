package tests

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math"
	"testing"
	"time"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestVerify: a whole file passes, one that is cut short or is no image
// does not, also where its header alone looks fine.
func TestVerify(t *testing.T) {
	headerOnly := 0
	for name, data := range realFiles(t) {
		if err := inspect.Verify(FromBytes(data)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if err := inspect.Verify(FromReader(bytes.NewReader(data))); err != nil {
			t.Errorf("%s from a reader: %v", name, err)
		}
		short := data[:len(data)/2]
		if _, err := FromBytes(short).Metadata(); err == nil {
			headerOnly++ // the header alone does not show it
		}
		if err := inspect.Verify(FromBytes(short)); !errors.Is(err, ErrFormat) {
			t.Errorf("%s cut in half: %v, want an ErrFormat", name, err)
		}
	}
	if headerOnly == 0 {
		t.Error("no file cut in half passed Metadata: the test shows nothing Verify adds")
	}
	// The frames after the first of an animation are looked at as well.
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, testAnimation(60, 40)); err != nil {
		t.Fatal(err)
	}
	anim := buf.Bytes()
	if err := inspect.Verify(FromBytes(anim)); err != nil {
		t.Errorf("animation: %v", err)
	}
	if err := inspect.Verify(FromBytes(anim[:len(anim)*3/4])); !errors.Is(err, ErrFormat) {
		t.Errorf("animation cut in its later frames: %v, want an ErrFormat", err)
	}
	// What is wrong with the recipe is reported too; nothing is no image.
	in := pngBytes(t, testutil.Images(40, 30)["nrgba"])
	if err := inspect.Verify(FromBytes(in).Extract(0, 0, 500, 500)); err == nil {
		t.Error("a recipe that cannot run was verified")
	}
	if err := inspect.Verify(FromBytes([]byte("not an image"))); !errors.Is(err, ErrFormat) {
		t.Errorf("no image: %v", err)
	}
	if err := inspect.Verify(nil); err == nil {
		t.Error("Verify(nil) accepted")
	}
}

// psnrOf computes the PSNR of two decoded images over red, green and blue,
// the plain way, for Compare to be held against.
func psnrOf(a, b image.Image) (psnr float64, maxDiff int, different int64) {
	r := a.Bounds()
	var sum float64
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			p := color.NRGBAModel.Convert(a.At(r.Min.X+x, r.Min.Y+y)).(color.NRGBA)
			q := color.NRGBAModel.Convert(b.At(b.Bounds().Min.X+x, b.Bounds().Min.Y+y)).(color.NRGBA)
			differs := false
			for _, d := range []int{int(p.R) - int(q.R), int(p.G) - int(q.G), int(p.B) - int(q.B)} {
				if d < 0 {
					d = -d
				}
				if d != 0 {
					differs = true
				}
				maxDiff = max(maxDiff, d)
				sum += float64(d * d)
			}
			if differs {
				different++
			}
		}
	}
	if sum == 0 {
		return math.Inf(1), 0, 0
	}
	return 10 * math.Log10(255*255/(sum/float64(r.Dx()*r.Dy()*3))), maxDiff, different
}

// TestCompare: what Compare reports is what comparing the decoded images
// pixel by pixel gives, across formats.
func TestCompare(t *testing.T) {
	src := photoLike(317, 211)
	orig := pngBytes(t, src)

	// The same picture in every format that keeps every pixel.
	for name, m := range map[string]*Image{
		"bmp":           FromBytes(orig).BMP(),
		"tiff":          FromBytes(orig).TIFF(TIFFOptions{Compression: TIFFDeflate}),
		"webp lossless": FromBytes(orig).WebP(WebPOptions{Lossless: true}),
		"png again":     FromBytes(orig).PNG(PNGOptions{CompressionLevel: 9}),
	} {
		file, err := m.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		d, err := inspect.Compare(FromBytes(orig), FromBytes(file))
		if err != nil || !d.SameSize || !d.Identical || d.DifferentPixels != 0 || d.MaxDiff != 0 || !math.IsInf(d.PSNR, 1) {
			t.Errorf("%s: %+v, %v; want identical", name, d, err)
		}
		if d.String() != "identical" {
			t.Errorf("%s: %q", name, d.String())
		}
	}

	// A lossy copy: the figures of a plain comparison of the decoded images.
	for _, q := range []int{95, 60, 20} {
		jp := jpegBytes(src, q)
		dec, _ := jpeg.Decode(bytes.NewReader(jp))
		wantPSNR, wantMax, wantDiff := psnrOf(src, dec)
		d, err := inspect.Compare(FromBytes(orig), FromBytes(jp))
		if err != nil {
			t.Fatal(err)
		}
		if !d.SameSize || d.Identical || d.Width != 317 || d.Height != 211 {
			t.Errorf("quality %d: %+v", q, d)
		}
		if math.Abs(d.PSNR-wantPSNR) > 1e-9 || d.MaxDiff != wantMax || d.DifferentPixels != wantDiff {
			t.Errorf("quality %d: PSNR %.4f, max %d, %d pixels; a plain comparison gives %.4f, %d, %d",
				q, d.PSNR, d.MaxDiff, d.DifferentPixels, wantPSNR, wantMax, wantDiff)
		}
		// The other way round is the same difference.
		back, err := inspect.Compare(FromBytes(jp), FromBytes(orig))
		if err != nil || back != d {
			t.Errorf("quality %d the other way round: %+v, %v", q, back, err)
		}
		if q == 20 && d.PSNR > 40 {
			t.Errorf("quality 20 measured as %.1f dB", d.PSNR)
		}
	}

	// Two recipes, with a small memory limit for the pair.
	d, err := inspect.Compare(FromBytes(orig).Resize(100, 0).MemoryLimit(1<<20), FromBytes(orig).Resize(100, 0, ResizeOptions{Kernel: KernelLinear}))
	if err != nil || !d.SameSize || d.Identical || d.PSNR < 25 {
		t.Errorf("two kernels: %+v, %v", d, err)
	}

	// Different sizes: said so, and nothing else.
	d, err = inspect.Compare(FromBytes(orig), FromBytes(orig).Resize(100, 0))
	if err != nil || d.SameSize || d.Identical || d.Width != 317 || d.String() != "different sizes" {
		t.Errorf("different sizes: %+v, %v", d, err)
	}

	// Transparency: pixels that are both invisible are equal whatever
	// colour they hold; a difference in alpha alone is a difference.
	a := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	b := image.NewNRGBA(image.Rect(0, 0, 8, 8))
	for i := 0; i < len(a.Pix); i += 4 {
		a.Pix[i], a.Pix[i+1], a.Pix[i+2], a.Pix[i+3] = 200, 100, 50, 0
		b.Pix[i], b.Pix[i+1], b.Pix[i+2], b.Pix[i+3] = 10, 20, 30, 0
	}
	if d, err := inspect.Compare(FromImage(a), FromImage(b)); err != nil || !d.Identical {
		t.Errorf("two invisible images: %+v, %v", d, err)
	}
	b.Pix[3] = 255 // one pixel visible in b
	if d, err := inspect.Compare(FromImage(a), FromImage(b)); err != nil || d.Identical || d.DifferentPixels != 1 || d.MaxDiff != 255 {
		t.Errorf("one pixel made visible: %+v, %v", d, err)
	}

	// Errors of either side are passed on, the second one marked.
	if _, err := inspect.Compare(FromBytes(orig[:len(orig)/2]), FromBytes(orig)); !errors.Is(err, ErrFormat) {
		t.Errorf("first image cut short: %v", err)
	}
	if _, err := inspect.Compare(FromBytes(orig), FromBytes(orig[:len(orig)/2])); !errors.Is(err, ErrFormat) {
		t.Errorf("second image cut short: %v", err)
	}
	if _, err := inspect.Compare(FromBytes(orig), nil); err == nil {
		t.Error("Compare with nil accepted")
	}
}

// TestFrames: the frames of an animation are counted without decoding
// them; everything else has one.
func TestFrames(t *testing.T) {
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, testAnimation(60, 40)); err != nil {
		t.Fatal(err)
	}
	anim := buf.Bytes()
	fr, err := FromBytes(anim).Frames()
	// Six frames with delays of 10, 20, 5, 7, 30 and 1 hundredths; the
	// file repeats it 3 times, so it plays 4 times.
	if err != nil || fr.Count != 6 || fr.Duration != 730*time.Millisecond || fr.LoopCount != 4 {
		t.Errorf("animation: %+v, %v", fr, err)
	}
	// With or without Animated, and the recipe plays no part.
	if again, err := FromBytes(anim).Animated().Resize(10, 0).Frames(); err != nil || again != fr {
		t.Errorf("with a recipe: %+v, %v", again, err)
	}
	for name, data := range realFiles(t) {
		fr, err := FromBytes(data).Frames()
		if err != nil || fr.Count != 1 || fr.Duration != 0 {
			t.Errorf("%s: %+v, %v; want one frame", name, fr, err)
		}
	}
	if fr, err := Text("frames").Frames(); err != nil || fr.Count != 1 {
		t.Errorf("text: %+v, %v", fr, err)
	}
	if _, err := FromBytes(anim[:len(anim)*3/4]).Frames(); !errors.Is(err, ErrFormat) {
		t.Errorf("animation cut short: %v", err)
	}
	if _, err := FromBytes([]byte("GIF89a")).Frames(); !errors.Is(err, ErrFormat) {
		t.Errorf("a GIF of a header only: %v", err)
	}
	if _, err := FromBytes([]byte("nothing")).Frames(); !errors.Is(err, ErrFormat) {
		t.Errorf("no image: %v", err)
	}
	// A memory budget is given back, and no pixel buffer is needed: a
	// limit far below one frame is enough.
	if fr, err := FromBytes(anim).MemoryLimit(64 << 10).Frames(); err != nil || fr.Count != 6 {
		t.Errorf("under 64KB: %+v, %v", fr, err)
	}
}

// TestInfoColours: Metadata says how the pixels are stored.
func TestInfoColours(t *testing.T) {
	imgs := testutil.Images(33, 21)
	for name, want := range map[string]struct {
		color string
		bits  int
		alpha bool
	}{
		"gray": {"gray", 8, false}, "gray16": {"gray", 16, false},
		"nrgba": {"rgb", 8, true}, "rgba-opaque": {"rgb", 8, false}, "nrgba64": {"rgb", 16, true},
		"paletted": {"palette", 8, true}, // its palette has translucent entries
	} {
		info, err := FromBytes(pngBytes(t, imgs[name])).Metadata()
		if err != nil || info.Color != want.color || info.Bits != want.bits || info.HasAlpha != want.alpha {
			t.Errorf("png %s: %s %d-bit alpha=%v (%v), want %s %d-bit alpha=%v", name, info.Color, info.Bits, info.HasAlpha, err, want.color, want.bits, want.alpha)
		}
	}
	// A palette without a transparent entry is opaque.
	solid := image.NewPaletted(image.Rect(0, 0, 9, 9), color.Palette{color.Black, color.White, color.RGBA{200, 0, 0, 255}})
	var buf bytes.Buffer
	png.Encode(&buf, solid)
	if info, err := FromBytes(buf.Bytes()).Metadata(); err != nil || info.Color != "palette" || info.HasAlpha {
		t.Errorf("solid palette: %+v, %v", info, err)
	}
	for name, want := range map[string]string{"jpeg": "ycbcr", "gif": "palette", "webp-lossy": "ycbcr", "webp-lossless": "rgb", "png": "rgb"} {
		info, err := FromBytes(realFiles(t)[name]).Metadata()
		if err != nil || info.Color != want || info.Bits != 8 {
			t.Errorf("%s: %s %d-bit (%v), want %s 8-bit", name, info.Color, info.Bits, err, want)
		}
	}
	gray := jpegBytes(imgs["gray"], 80)
	if info, err := FromBytes(gray).Metadata(); err != nil || info.Color != "gray" || info.Bits != 8 || info.HasAlpha {
		t.Errorf("grey jpeg: %+v, %v", info, err)
	}
	// From memory, and from a stream of the caller's own.
	cmyk := image.NewCMYK(image.Rect(0, 0, 4, 4))
	if info, err := FromImage(cmyk).Metadata(); err != nil || info.Color != "cmyk" {
		t.Errorf("cmyk image: %+v, %v", info, err)
	}
	if info, err := FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
		return &gradient{w: 10, h: 10, env: env}, nil
	}).Metadata(); err != nil || info.Color != "gray" || info.Bits != 8 {
		t.Errorf("own source: %+v, %v", info, err)
	}
}
