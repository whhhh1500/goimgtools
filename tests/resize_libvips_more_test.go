package tests

import (
	"bytes"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestResizeLibvipsOptions: by name the same; what ResizeLibvips does not
// take is refused; the steps say the method.
func TestResizeLibvipsOptions(t *testing.T) {
	rgb := sourcePNG(t, "pillow/source.png")
	a := must(FromBytes(rgb).Resize(60, 41, ResizeOptions{Fit: FitFill, Kernel: KernelLanczos3, Method: ResizeLibvips}).PNG().ToBytes())
	b := must(FromBytes(rgb).Do("resize", "60", "41", "fill", "lanczos3", "center", "srgb", "libvips").PNG().ToBytes())
	if !bytes.Equal(a, b) {
		t.Error("by name: another image")
	}
	var steps []Step
	seen := func(step Step, in pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
		steps = append(steps, step)
		return run(in)
	}
	c := must(FromBytes(rgb).Around(seen).Resize(60, 41, ResizeOptions{Fit: FitFill, Kernel: KernelLanczos3, Method: ResizeLibvips}).PNG().ToBytes())
	if len(steps) != 1 || len(steps[0].Args) != 7 || steps[0].Args[6] != "libvips" || !bytes.Equal(a, c) {
		t.Errorf("steps: %+v", steps)
	}
	p := must(FromBytes(rgb).Resize(60, 41, ResizeOptions{Fit: FitFill, Kernel: KernelHamming, Method: ResizePillow}).PNG().ToBytes())
	if q := must(FromBytes(rgb).Do("resize", "60", "41", "fill", "hamming", "center", "srgb", "pillow").PNG().ToBytes()); !bytes.Equal(p, q) {
		t.Error("pillow by name: another image")
	}
	// Box and Hamming with every method.
	for _, k := range []Kernel{KernelBox, KernelHamming} {
		for _, m := range []ResizeMethod{ResizeDraw, ResizeLibvips, ResizePillow} {
			for _, sz := range [][2]int{{40, 30}, {400, 300}} {
				g := decodePNG(t, must(FromBytes(rgb).Resize(sz[0], sz[1], ResizeOptions{Fit: FitFill, Kernel: k, Method: m}).PNG().ToBytes()))
				if b := g.Bounds(); b.Dx() != sz[0] || b.Dy() != sz[1] {
					t.Errorf("kernel %d method %d: %v", k, m, b)
				}
			}
		}
	}
	for _, o := range []ResizeOptions{{Method: 3}, {Method: -1}, {Method: ResizeLibvips, LinearLight: true}, {Method: ResizePillow, LinearLight: true}, {Kernel: 10}} {
		if _, err := FromBytes(rgb).Resize(60, 41, o).PNG().ToBytes(); err == nil {
			t.Errorf("Resize(%+v) accepted", o)
		}
	}
	if _, err := FromBytes(rgb).Do("resize", "60", "41", "fill", "cubic", "center", "srgb", "magick").PNG().ToBytes(); err == nil {
		t.Error("an unknown method word accepted")
	}
	// A filter of one's own, and the extremes: no panic, the sizes asked.
	own := &Filter{Support: 1.5, At: func(x float64) float64 { return max(0, 1.5-max(x, -x)) }}
	for _, c := range []struct {
		w, h int
		o    ResizeOptions
	}{
		{40, 30, ResizeOptions{Fit: FitFill, Filter: own, Method: ResizePillow}},
		{400, 300, ResizeOptions{Fit: FitFill, Filter: own, Method: ResizePillow}},
		{1, 1, ResizeOptions{Fit: FitFill, Kernel: KernelMKS2021, Method: ResizePillow}},
		{40, 30, ResizeOptions{Fit: FitFill, Filter: own}},
		{400, 300, ResizeOptions{Fit: FitFill, Filter: own}},
		{1, 1, ResizeOptions{Fit: FitFill, Kernel: KernelMKS2021}},
		{1, 500, ResizeOptions{Fit: FitFill, Kernel: KernelLanczos3}},
		{3000, 1, ResizeOptions{Fit: FitFill, Kernel: KernelNearest}},
	} {
		if c.o.Method == ResizeDraw {
			c.o.Method = ResizeLibvips
		}
		m := decodePNG(t, must(FromBytes(rgb).Resize(c.w, c.h, c.o).PNG().ToBytes()))
		if b := m.Bounds(); b.Dx() != c.w || b.Dy() != c.h {
			t.Errorf("%dx%d: %v", c.w, c.h, b)
		}
	}
	// A grey image stays grey.
	if g := decodePNG(t, must(FromBytes(sourcePNG(t, "pillow/gray.png")).Resize(60, 41, ResizeOptions{Fit: FitFill, Method: ResizeLibvips}).PNG().ToBytes())); g.ColorModel() != color.GrayModel {
		t.Errorf("grey: %T", g)
	}
	// A memory limit far below the image: an error or the same image.
	small, err := FromBytes(rgb).MemoryLimit(16<<10).Resize(60, 41, ResizeOptions{Fit: FitFill, Kernel: KernelLanczos3, Method: ResizeLibvips}).PNG().ToBytes()
	if err == nil && !bytes.Equal(small, a) {
		t.Error("in little memory: another image")
	}
}

// TestExtendTranslucent: a border keeps the colours of translucent pixels
// as they are (an image with transparency is padded in NRGBA), and the
// colour of the border too.
func TestExtendTranslucent(t *testing.T) {
	src := testutil.Images(37, 23)["nrgba"]
	bg := color.NRGBA{200, 10, 90, 99}
	got := decodePNG(t, must(FromImage(src).Extend(3, 4, 5, 6, bg).PNG().ToBytes()))
	n := toNRGBA(got)
	s := toNRGBA(src)
	for y := 0; y < 23; y++ {
		for x := 0; x < 37; x++ {
			if n.NRGBAAt(x+6, y+3) != s.NRGBAAt(x, y) {
				t.Fatalf("(%d, %d): %v, was %v", x, y, n.NRGBAAt(x+6, y+3), s.NRGBAAt(x, y))
			}
		}
	}
	if c := n.NRGBAAt(0, 0); c != bg {
		t.Errorf("border %v, want %v", c, bg)
	}
}

// TestFastShrinkUnusualSampling: a JPEG whose components are sampled in
// ways the decoder does not reduce itself (Go's test files) is still shrunk
// on load, the decoded YCbCr averaged, with either method.
func TestFastShrinkUnusualSampling(t *testing.T) {
	data, err := os.ReadFile(filepath.Join(runtime.GOROOT(), "src", "image", "testdata", "video-001.q50.121121.jpeg"))
	if err != nil {
		t.Skip(err)
	}
	for _, m := range []ResizeMethod{ResizeDraw, ResizeLibvips} {
		out, err := FromBytes(data).Resize(30, 0, ResizeOptions{FastShrinkOnLoad: true, Method: m}).PNG().ToBytes()
		if err != nil {
			t.Fatalf("method %d: %v", m, err)
		}
		if b := decodePNG(t, out).Bounds(); b.Dx() != 30 {
			t.Errorf("method %d: %v", m, b)
		}
	}
}

// TestResizeLibvipsWebP: with FastShrinkOnLoad sharp has libwebp scale a
// WebP while decoding it (by the smaller of the two reductions, each side
// rounded) and resizes the rest: lossy (the planes each scaled at their
// own size, the loop filter skipped below three quarters), lossy with
// alpha and lossless (scaled premultiplied), odd sizes, every fit and
// kernel. Without FastShrinkOnLoad the WebP is decoded whole. An image
// with transparency padded without a resize keeps its colour as it is
// (sharp multiplies colours by alpha only to resize). The sums are of the
// pixels of sharp 0.35.5 (libvips 8.18.7).
func TestResizeLibvipsWebP(t *testing.T) {
	for _, c := range []struct {
		in   string
		w, h int
		o    ResizeOptions
		sum  string
	}{
		{"sharp/resize-lossy.webp", 240, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "5e5c57e434949efd"},
		{"sharp/resize-lossy.webp", 200, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "973fd23aca5076f4"},
		{"sharp/resize-lossy.webp", 160, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "e0da4955744e8ce0"},
		{"sharp/resize-lossy.webp", 100, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "150fbe75aee3290f"},
		{"sharp/resize-lossy.webp", 61, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "4ac4cb6e4793d946"},
		{"sharp/resize-lossy.webp", 37, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "c18ccecfe50db159"},
		{"sharp/resize-lossy.webp", 7, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "de36b82efb6f1b97"},
		{"sharp/resize-lossy.webp", 0, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "a217c23d4ce24476"},
		{"sharp/resize-lossy.webp", 80, 80, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, FastShrinkOnLoad: true}, "4fb1351a54cb0580"},
		{"sharp/resize-lossy.webp", 80, 80, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, FastShrinkOnLoad: true, Position: NorthWest}, "94f36a0580aa68f6"},
		{"sharp/resize-lossy.webp", 150, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill, FastShrinkOnLoad: true}, "24ef4298bbeed658"},
		{"sharp/resize-lossy.webp", 31, 120, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill, FastShrinkOnLoad: true}, "f29ec40417d70636"},
		{"sharp/resize-lossy.webp", 90, 90, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, FastShrinkOnLoad: true, Background: color.NRGBA{10, 200, 30, 255}}, "5227d111c9e72c58"},
		{"sharp/resize-lossy.webp", 70, 70, ResizeOptions{Kernel: KernelLanczos3, Fit: FitOutside, FastShrinkOnLoad: true}, "8b9856873f5334af"},
		{"sharp/resize-lossy.webp", 400, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true, WithoutEnlargement: true}, "e90088422457c6fe"},
		{"sharp/resize-lossy.webp", 77, 0, ResizeOptions{Kernel: KernelNearest, Fit: FitInside, FastShrinkOnLoad: true}, "3a6e7effa940fe0a"},
		{"sharp/resize-lossy.webp", 77, 0, ResizeOptions{Kernel: KernelCubic, Fit: FitInside, FastShrinkOnLoad: true}, "3a6e7effa940fe0a"},
		{"sharp/resize-lossy.webp", 77, 0, ResizeOptions{Kernel: KernelLinear, Fit: FitInside, FastShrinkOnLoad: true}, "3a6e7effa940fe0a"},
		{"sharp/resize-lossy.webp", 77, 0, ResizeOptions{Kernel: KernelMitchell, Fit: FitInside, FastShrinkOnLoad: true}, "3a6e7effa940fe0a"},
		{"sharp/resize-lossy.webp", 77, 0, ResizeOptions{Kernel: KernelLanczos2, Fit: FitInside, FastShrinkOnLoad: true}, "3a6e7effa940fe0a"},
		{"sharp/resize-lossy.webp", 100, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside}, "a2041bfc1282769d"},
		{"sharp/resize-odd.webp", 240, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "b66fa96e77323f63"},
		{"sharp/resize-odd.webp", 200, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "4a7292264737e5b9"},
		{"sharp/resize-odd.webp", 160, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "9626f3474bff5ee8"},
		{"sharp/resize-odd.webp", 100, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "1fdd79630af377db"},
		{"sharp/resize-odd.webp", 61, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "bb568718ba0f2f6c"},
		{"sharp/resize-odd.webp", 37, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "c30c7232b4d1d243"},
		{"sharp/resize-odd.webp", 7, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "974c768eae597baf"},
		{"sharp/resize-odd.webp", 0, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "e6af80b52d2200df"},
		{"sharp/resize-odd.webp", 80, 80, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, FastShrinkOnLoad: true}, "85da43c6ce44e747"},
		{"sharp/resize-odd.webp", 80, 80, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, FastShrinkOnLoad: true, Position: NorthWest}, "6c96b5280a3c3229"},
		{"sharp/resize-odd.webp", 150, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill, FastShrinkOnLoad: true}, "325361b0b6c7983c"},
		{"sharp/resize-odd.webp", 31, 120, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill, FastShrinkOnLoad: true}, "0a3ed9797849d0f7"},
		{"sharp/resize-odd.webp", 90, 90, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, FastShrinkOnLoad: true, Background: color.NRGBA{10, 200, 30, 255}}, "28d4e56da86d366e"},
		{"sharp/resize-odd.webp", 70, 70, ResizeOptions{Kernel: KernelLanczos3, Fit: FitOutside, FastShrinkOnLoad: true}, "2313e52bca157323"},
		{"sharp/resize-odd.webp", 400, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true, WithoutEnlargement: true}, "181116198308fba9"},
		{"sharp/resize-odd.webp", 77, 0, ResizeOptions{Kernel: KernelNearest, Fit: FitInside, FastShrinkOnLoad: true}, "109e5dc33b9fa5d0"},
		{"sharp/resize-odd.webp", 77, 0, ResizeOptions{Kernel: KernelCubic, Fit: FitInside, FastShrinkOnLoad: true}, "109e5dc33b9fa5d0"},
		{"sharp/resize-odd.webp", 77, 0, ResizeOptions{Kernel: KernelLinear, Fit: FitInside, FastShrinkOnLoad: true}, "109e5dc33b9fa5d0"},
		{"sharp/resize-odd.webp", 77, 0, ResizeOptions{Kernel: KernelMitchell, Fit: FitInside, FastShrinkOnLoad: true}, "109e5dc33b9fa5d0"},
		{"sharp/resize-odd.webp", 77, 0, ResizeOptions{Kernel: KernelLanczos2, Fit: FitInside, FastShrinkOnLoad: true}, "109e5dc33b9fa5d0"},
		{"sharp/resize-odd.webp", 100, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside}, "b3204ab6b1d78050"},
		{"sharp/resize-alpha.webp", 120, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "fe971cf47a2cb76d"},
		{"sharp/resize-alpha.webp", 100, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "8b7b3d7d1ed3eebc"},
		{"sharp/resize-alpha.webp", 61, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "2453a39d83b05944"},
		{"sharp/resize-alpha.webp", 30, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "c45f99e382e77ecc"},
		{"sharp/resize-alpha.webp", 50, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, FastShrinkOnLoad: true}, "c99df190b434e630"},
		{"sharp/resize-alpha.webp", 31, 77, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill, FastShrinkOnLoad: true}, "a14eac1843c43aa2"},
		{"sharp/resize-alpha.webp", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, FastShrinkOnLoad: true, Background: color.NRGBA{10, 200, 30, 128}}, "53c5eb0aceec88c2"},
		{"sharp/resize-alpha.webp", 70, 0, ResizeOptions{Kernel: KernelCubic, Fit: FitInside, FastShrinkOnLoad: true}, "09e5311171aa5ef9"},
		{"sharp/resize-alpha.webp", 70, 0, ResizeOptions{Kernel: KernelNearest, Fit: FitInside, FastShrinkOnLoad: true}, "09e5311171aa5ef9"},
		{"sharp/resize-alpha.webp", 61, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside}, "cb63a5d89b676fd0"},
		{"sharp/resize-lossless.webp", 120, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "59fd3a04a1505794"},
		{"sharp/resize-lossless.webp", 100, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "42cde2d55127dc7f"},
		{"sharp/resize-lossless.webp", 61, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "2ba90ea11ce414bf"},
		{"sharp/resize-lossless.webp", 30, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside, FastShrinkOnLoad: true}, "f62a5e3640b12744"},
		{"sharp/resize-lossless.webp", 50, 50, ResizeOptions{Kernel: KernelLanczos3, Fit: FitCover, FastShrinkOnLoad: true}, "7c78a0bac48d8538"},
		{"sharp/resize-lossless.webp", 31, 77, ResizeOptions{Kernel: KernelLanczos3, Fit: FitFill, FastShrinkOnLoad: true}, "959323560fd1817e"},
		{"sharp/resize-lossless.webp", 60, 60, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, FastShrinkOnLoad: true, Background: color.NRGBA{10, 200, 30, 128}}, "7005f8663d15267a"},
		{"sharp/resize-lossless.webp", 70, 0, ResizeOptions{Kernel: KernelCubic, Fit: FitInside, FastShrinkOnLoad: true}, "c9052ca9e6e4cf9a"},
		{"sharp/resize-lossless.webp", 70, 0, ResizeOptions{Kernel: KernelNearest, Fit: FitInside, FastShrinkOnLoad: true}, "c9052ca9e6e4cf9a"},
		{"sharp/resize-lossless.webp", 61, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitInside}, "03447f0e58b24cbe"},
		{"pillow/median-rgba-source.png", 150, 150, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, Background: color.NRGBA{10, 200, 30, 128}}, "1304f5ab9d834367"},
		{"pillow/median-rgba-source.png", 200, 103, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, Background: color.NRGBA{10, 200, 30, 128}}, "b8ab8a3f9da388af"},
		{"pillow/median-rgba-source.png", 150, 0, ResizeOptions{Kernel: KernelLanczos3, Fit: FitContain, Background: color.NRGBA{10, 200, 30, 128}}, "04598fdf7ccb5e43"},
	} {
		c.o.Method = ResizeLibvips
		for _, threads := range []int{1, 3} {
			out, err := FromBytes(sourcePNG(t, c.in)).Threads(threads).Resize(c.w, c.h, c.o).PNG().ToBytes()
			if err != nil {
				t.Errorf("%s %dx%d %+v: %v", c.in, c.w, c.h, c.o, err)
				break
			}
			if got := rgbaSum(t, decodePNG(t, out)); got != c.sum {
				t.Errorf("%s %dx%d %+v, %d threads: %s, sharp's is %s", c.in, c.w, c.h, c.o, threads, got, c.sum)
				break
			}
		}
	}
}

// TestOutputSizePreShrunk: OutputSize is the size of the result also when
// the decoder reduces the image first (sharp's sizes after a JPEG or WebP
// is shrunk on load can differ by a pixel from those of the full image).
func TestOutputSizePreShrunk(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1001, 667))
	for i := range src.Pix {
		src.Pix[i] = uint8(i * 7)
	}
	for format, in := range map[string][]byte{
		"jpeg": must(FromImage(src).JPEG(90).ToBytes()),
		"webp": must(FromImage(src).WebP().ToBytes()),
	} {
		for _, m := range []ResizeMethod{ResizeDraw, ResizeLibvips, ResizePillow} {
			for _, fast := range []bool{false, true} {
				for _, sz := range [][2]int{{100, 0}, {61, 0}, {0, 83}, {250, 250}} {
					o := ResizeOptions{Method: m, FastShrinkOnLoad: fast}
					w, h, err := FromBytes(in).Resize(sz[0], sz[1], o).OutputSize()
					m, err2 := FromBytes(in).Resize(sz[0], sz[1], o).ToImage()
					if err != nil || err2 != nil || m.Bounds() != image.Rect(0, 0, w, h) {
						t.Errorf("%s %+v %v: OutputSize %dx%d (%v, %v), the image %v", format, o, sz, w, h, err, err2, m.Bounds())
					}
				}
			}
		}
	}
}
