package tests

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"testing/iotest"

	"golang.org/x/image/bmp"
	"golang.org/x/image/tiff"
	"golang.org/x/image/webp"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// noPanic fails the test when err is a bug that was caught, not an error
// the library reported.
func noPanic(t *testing.T, what string, err error) {
	t.Helper()
	var pe *pipeline.PanicError
	if errors.As(err, &pe) {
		t.Fatalf("%s: panic: %v\n%s", what, pe.Value, pe.Stack)
	}
}

// decodeAny decodes a file this package wrote with the reference decoder
// of its format.
func decodeAny(t *testing.T, what string, b []byte) image.Image {
	t.Helper()
	var m image.Image
	var err error
	switch {
	case bytes.HasPrefix(b, []byte("\xff\xd8")):
		m, err = jpeg.Decode(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		m, err = png.Decode(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("GIF8")):
		m, err = gif.Decode(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("BM")):
		m, err = bmp.Decode(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("RIFF")):
		m, err = webp.Decode(bytes.NewReader(b))
	default:
		m, err = tiff.Decode(bytes.NewReader(b))
	}
	if err != nil {
		t.Fatalf("%s: the reference decoder rejects the file: %v", what, err)
	}
	return m
}

// outputs are all the ways a file can be written.
var outputs = map[string]func(*Image) *Image{
	"jpeg":             func(m *Image) *Image { return m.JPEG(80) },
	"jpeg optimized":   func(m *Image) *Image { return m.JPEG(80, JPEGOptions{Optimize: true}) },
	"jpeg progressive": func(m *Image) *Image { return m.JPEG(80, JPEGOptions{Progressive: true}) },
	"png":              func(m *Image) *Image { return m.PNG() },
	"png 16 colours":   func(m *Image) *Image { return m.PNG(PNGOptions{Colors: 16}) },
	"png 2 colours":    func(m *Image) *Image { return m.PNG(PNGOptions{Colors: 2, NoDither: true}) },
	"gif":              func(m *Image) *Image { return m.GIF() },
	"gif fixed":        func(m *Image) *Image { return m.GIF(GIFOptions{FixedPalette: true}) },
	"bmp":              func(m *Image) *Image { return m.BMP() },
	"tiff":             func(m *Image) *Image { return m.TIFF() },
	"tiff deflate":     func(m *Image) *Image { return m.TIFF(TIFFOptions{Compression: TIFFDeflate}) },
	"webp":             func(m *Image) *Image { return m.WebP() },
	"webp lossless":    func(m *Image) *Image { return m.WebP(WebPOptions{Lossless: true}) },
}

// shapes are the sizes at the small end and the awkward ones just around
// the block sizes of the formats (8 and 16 for JPEG and WebP).
var shapes = []image.Point{
	{1, 1}, {1, 2}, {2, 1}, {2, 2}, {3, 3}, {1, 17}, {17, 1}, {7, 9}, {8, 8}, {9, 7},
	{15, 17}, {16, 16}, {17, 15}, {1, 300}, {300, 1}, {33, 2}, {2, 33},
}

// TestSmallestImages writes and reads back images of the smallest and most
// awkward sizes in every format, as every pixel type.
func TestSmallestImages(t *testing.T) {
	for _, size := range shapes {
		for name, m := range testutil.Images(size.X, size.Y) {
			for out, choose := range outputs {
				what := fmt.Sprintf("%s %dx%d as %s", name, size.X, size.Y, out)
				b, err := choose(FromImage(m)).ToBytes()
				noPanic(t, what, err)
				if err != nil {
					t.Errorf("%s: %v", what, err)
					continue
				}
				if got := decodeAny(t, what, b).Bounds().Size(); got != size {
					t.Errorf("%s: the file is %v", what, got)
				}
				// And read by this package again, into another format.
				back, err := FromBytes(b).PNG().ToBytes()
				noPanic(t, what+", read back", err)
				if err != nil {
					t.Errorf("%s, read back: %v", what, err)
				} else if got := decodePNG(t, back).Bounds().Size(); got != size {
					t.Errorf("%s, read back: %v", what, got)
				}
			}
		}
	}
}

// TestSmallestThroughOperations puts the smallest images through every
// operation: nothing may fail, hang or come out at a wrong size.
func TestSmallestThroughOperations(t *testing.T) {
	logo := FromImage(testutil.Images(5, 3)["nrgba"])
	type op struct {
		name string
		do   func(m *Image, w, h int) *Image
		size func(w, h int) (int, int)
	}
	same := func(w, h int) (int, int) { return w, h }
	ops := []op{
		{"to 1x1", func(m *Image, w, h int) *Image { return m.Resize(1, 1, ResizeOptions{Fit: FitFill}) }, func(w, h int) (int, int) { return 1, 1 }},
		{"to 40 wide", func(m *Image, w, h int) *Image { return m.Resize(40, 0) }, func(w, h int) (int, int) { return 40, max(1, int(math.Round(float64(h)*40/float64(w)))) }},
		{"to 37x41 fill", func(m *Image, w, h int) *Image { return m.Resize(37, 41, ResizeOptions{Fit: FitFill}) }, func(w, h int) (int, int) { return 37, 41 }},
		{"cover 5x5", func(m *Image, w, h int) *Image { return m.Resize(5, 5, ResizeOptions{Fit: FitCover}) }, func(w, h int) (int, int) { return 5, 5 }},
		{"contain 9x4", func(m *Image, w, h int) *Image { return m.Resize(9, 4, ResizeOptions{Fit: FitContain}) }, func(w, h int) (int, int) { return 9, 4 }},
		{"rotate 90", func(m *Image, w, h int) *Image { return m.Rotate(90) }, func(w, h int) (int, int) { return h, w }},
		{"rotate 180", func(m *Image, w, h int) *Image { return m.Rotate(180) }, same},
		{"rotate 270, then to 3 high", func(m *Image, w, h int) *Image { return m.Rotate(270).Resize(0, 3) }, func(w, h int) (int, int) { return max(1, int(math.Round(float64(h)*3/float64(w)))), 3 }},
		{"auto-orient", func(m *Image, w, h int) *Image { return m.Rotate() }, same},
		{"flip, flop", func(m *Image, w, h int) *Image { return m.Flip().Flop() }, same},
		{"last pixel", func(m *Image, w, h int) *Image { return m.Extract(w-1, h-1, 1, 1) }, func(w, h int) (int, int) { return 1, 1 }},
		{"flatten", func(m *Image, w, h int) *Image { return m.Flatten(color.White) }, same},
		{"logo larger than the image", func(m *Image, w, h int) *Image { return m.Composite(logo, CompositeOptions{Gravity: Center}) }, same},
		{"logo turned and tiled", func(m *Image, w, h int) *Image {
			return m.Composite(logo, CompositeOptions{Gravity: SouthEast, Angle: 33, Opacity: 0.5, Tile: true})
		}, same},
		{"text", func(m *Image, w, h int) *Image {
			return m.Composite(Text("Ag", TextOptions{Size: 9, StrokeWidth: 1, ShadowColor: color.Black, ShadowBlur: 2}), CompositeOptions{LeftRatio: 0.5, TopRatio: 0.5, Anchor: Center})
		}, same},
		{"own operation", func(m *Image, w, h int) *Image { return m.Pixels(func(*image.NRGBA) error { return nil }) }, same},
		{"grayscale", func(m *Image, w, h int) *Image { return m.Grayscale() }, same},
		{"modulate", func(m *Image, w, h int) *Image { return m.Modulate(1.3, 0.2, 77) }, same},
		{"blur", func(m *Image, w, h int) *Image { return m.Blur(1.5) }, same},
		{"blur wider than the image", func(m *Image, w, h int) *Image { return m.Blur(400) }, same},
		{"mild sharpen", func(m *Image, w, h int) *Image { return m.Sharpen() }, same},
		{"unsharp mask", func(m *Image, w, h int) *Image { return m.Sharpen(SharpenOptions{Sigma: 3, Amount: 2, Threshold: 2}) }, same},
		{"extend", func(m *Image, w, h int) *Image { return m.Extend(1, 2, 3, 4, color.Transparent) }, func(w, h int) (int, int) { return w + 6, h + 4 }},
		{"levels", func(m *Image, w, h int) *Image { return m.Negate().Linear(1.1, -3).Gamma(0.8).Threshold(90) }, same},
		{"tint, normalize", func(m *Image, w, h int) *Image { return m.Tint(color.RGBA{9, 99, 199, 255}).Normalize() }, same},
		{"median", func(m *Image, w, h int) *Image { return m.Median(3) }, same},
		{"median larger than the image", func(m *Image, w, h int) *Image { return m.Median(99) }, same},
		{"convolve", func(m *Image, w, h int) *Image {
			return m.Convolve(Convolution{Width: 5, Height: 3, Weights: []float64{1, 0, -1, 2, 0, -2, 1, 0, -1, 1, 2, 3, 4, 5, 6}, Offset: 100})
		}, same},
		{"extend, trim", func(m *Image, w, h int) *Image { return m.Extend(1, 2, 3, 4, color.RGBA{1, 2, 3, 255}).Trim(0) }, nil},
		{"turn 33 degrees", func(m *Image, w, h int) *Image { return m.RotateBy(33, nil) }, func(w, h int) (int, int) {
			s, c := math.Sincos(33 * math.Pi / 180)
			return int(math.Ceil(float64(w)*c + float64(h)*s - 1e-9)), int(math.Ceil(float64(w)*s + float64(h)*c - 1e-9))
		}},
	}
	kernels := []Kernel{KernelCubic, KernelNearest, KernelLinear, KernelMitchell, KernelLanczos2, KernelLanczos3}
	for _, size := range shapes {
		for _, name := range []string{"nrgba", "gray", "paletted", "ycbcr420", "rgba-opaque"} {
			m := testutil.Images(size.X, size.Y)[name]
			for _, o := range ops {
				for _, threads := range []int{1, 3, 6} {
					what := fmt.Sprintf("%s %dx%d, %s, %d threads", name, size.X, size.Y, o.name, threads)
					b, err := o.do(FromImage(m), size.X, size.Y).Threads(threads).PNG().ToBytes()
					noPanic(t, what, err)
					if err != nil {
						t.Errorf("%s: %v", what, err)
						continue
					}
					if o.size == nil {
						continue // depends on the pixels
					}
					w, h := o.size(size.X, size.Y)
					if got := decodePNG(t, b).Bounds().Size(); got != image.Pt(w, h) {
						t.Errorf("%s: %v, want %dx%d", what, got, w, h)
					}
				}
			}
			for _, k := range kernels {
				for _, to := range []image.Point{{1, 1}, {3, 2}, {64, 48}} {
					what := fmt.Sprintf("%s %dx%d to %v with kernel %d", name, size.X, size.Y, to, k)
					b, err := FromImage(m).Resize(to.X, to.Y, ResizeOptions{Fit: FitFill, Kernel: k}).PNG().ToBytes()
					noPanic(t, what, err)
					if err != nil {
						t.Errorf("%s: %v", what, err)
					} else if got := decodePNG(t, b).Bounds().Size(); got != to {
						t.Errorf("%s: %v", what, got)
					}
				}
			}
		}
	}
}

// stripe is an input of any size that costs nothing to make: every row is
// the same grey ramp.
func stripe(w, h int) *Image {
	return FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
		return &stripes{w: w, h: h, env: env}, nil
	})
}

type stripes struct {
	w, h, y  int
	env      *pipeline.Env
	buf      []uint8
	reserved int64
}

func (s *stripes) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *stripes) ColorModel() color.Model { return color.GrayModel }
func (s *stripes) Env() *pipeline.Env      { return s.env }
func (s *stripes) Opaque() bool            { return true }

func (s *stripes) Close() error {
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
	return nil
}

func (s *stripes) Next() (image.Image, error) {
	if s.y >= s.h {
		return nil, io.EOF
	}
	rows := pipeline.StripRows(int64(s.w), s.h)
	if s.buf == nil {
		need := int64(s.w) * int64(rows)
		if err := s.env.Budget.Reserve(need, "stripe"); err != nil {
			return nil, err
		}
		s.reserved, s.buf = need, make([]uint8, need)
		for i := range s.buf {
			s.buf[i] = uint8((i % s.w) * 255 / s.w)
		}
	}
	y1 := min(s.y+rows, s.h)
	out := &image.Gray{Pix: s.buf[:(y1-s.y)*s.w], Stride: s.w, Rect: image.Rect(0, s.y, s.w, y1)}
	s.y = y1
	return out, nil
}

// TestLargestSides: every format at the largest width and height it can
// hold, and one pixel beyond, where the format has a limit. Beyond it the
// result must be an error that says so, never a file that cannot be read.
func TestLargestSides(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	cases := []struct {
		name  string
		max   int // the largest side the format holds; 0 for no limit worth testing
		write func(*Image) *Image
	}{
		{"jpeg", 65535, func(m *Image) *Image { return m.JPEG(60) }},
		{"jpeg progressive", 65535, func(m *Image) *Image { return m.JPEG(60, JPEGOptions{Progressive: true}) }},
		{"gif", 65535, func(m *Image) *Image { return m.GIF(GIFOptions{FixedPalette: true}) }},
		{"gif adaptive", 65535, func(m *Image) *Image { return m.GIF() }},
		{"webp", 16383, func(m *Image) *Image { return m.WebP() }},
		{"webp lossless", 16384, func(m *Image) *Image { return m.WebP(WebPOptions{Lossless: true}) }},
		{"png", 0, func(m *Image) *Image { return m.PNG() }},
		{"bmp", 0, func(m *Image) *Image { return m.BMP() }},
		{"tiff", 0, func(m *Image) *Image { return m.TIFF(TIFFOptions{Compression: TIFFDeflate}) }},
	}
	for _, c := range cases {
		sides := []int{c.max}
		if c.max == 0 {
			sides = []int{70000}
		}
		for _, side := range sides {
			for _, size := range []image.Point{{side, 3}, {3, side}} {
				what := fmt.Sprintf("%s %dx%d", c.name, size.X, size.Y)
				b, err := c.write(stripe(size.X, size.Y)).ToBytes()
				noPanic(t, what, err)
				if err != nil {
					t.Errorf("%s: %v", what, err)
					continue
				}
				if cfgSize := configSize(t, what, b); cfgSize != size {
					t.Errorf("%s: the file says %v", what, cfgSize)
				}
				// Read back by this package, to a small picture.
				small, err := FromBytes(b).Resize(64, 64, ResizeOptions{Fit: FitFill}).PNG().ToBytes()
				noPanic(t, what+", read back", err)
				if err != nil {
					t.Errorf("%s, read back: %v", what, err)
				} else if decodePNG(t, small).Bounds().Size() != image.Pt(64, 64) {
					t.Errorf("%s, read back: wrong size", what)
				}
			}
		}
		if c.max == 0 {
			continue
		}
		for _, size := range []image.Point{{c.max + 1, 3}, {3, c.max + 1}} {
			what := fmt.Sprintf("%s %dx%d", c.name, size.X, size.Y)
			var w countingWriter
			err := c.write(stripe(size.X, size.Y)).ToWriter(&w)
			noPanic(t, what, err)
			if err == nil {
				t.Errorf("%s: written (%d bytes), though the format cannot hold it", what, w.n)
			} else if errors.Is(err, ErrFormat) || errors.Is(err, ErrMemoryLimit) {
				t.Errorf("%s: wrong kind of error: %v", what, err)
			} else if !strings.Contains(err.Error(), fmt.Sprint(c.max)) {
				t.Errorf("%s: the error does not name the limit: %v", what, err)
			}
		}
	}
}

func configSize(t *testing.T, what string, b []byte) image.Point {
	t.Helper()
	var c image.Config
	var err error
	switch {
	case bytes.HasPrefix(b, []byte("\xff\xd8")):
		c, err = jpeg.DecodeConfig(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("\x89PNG")):
		c, err = png.DecodeConfig(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("GIF8")):
		c, err = gif.DecodeConfig(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("BM")):
		c, err = bmp.DecodeConfig(bytes.NewReader(b))
	case bytes.HasPrefix(b, []byte("RIFF")):
		c, err = webp.DecodeConfig(bytes.NewReader(b))
	default:
		c, err = tiff.DecodeConfig(bytes.NewReader(b))
	}
	if err != nil {
		t.Fatalf("%s: the reference decoder rejects the file: %v", what, err)
	}
	return image.Pt(c.Width, c.Height)
}

// TestWideAndTallWithinMemory: an image that is very wide or very tall goes
// through the usual chain within the default memory limit, or is refused
// with ErrMemoryLimit; what it reserves never goes above the limit.
func TestWideAndTallWithinMemory(t *testing.T) {
	if testing.Short() {
		t.Skip("long")
	}
	for _, size := range []image.Point{{60000, 40}, {40, 60000}, {16000, 16000}} {
		for name, chain := range map[string]func(*Image) *Image{
			"resize":            func(m *Image) *Image { return m.Resize(800, 800, ResizeOptions{Fit: FitInside}) },
			"rotate and resize": func(m *Image) *Image { return m.Rotate(90).Resize(800, 800) },
			"rotate":            func(m *Image) *Image { return m.Rotate(90) },
			"watermark": func(m *Image) *Image {
				return m.Composite(Text("limit", TextOptions{Size: 20}), CompositeOptions{Gravity: SouthEast})
			},
		} {
			if size.X == 16000 && name != "resize" {
				continue // 256 megapixels through a full-size rotation takes minutes
			}
			what := fmt.Sprintf("%dx%d, %s", size.X, size.Y, name)
			var env *pipeline.Env
			in := FromSource(func(e *pipeline.Env) (pipeline.Source, error) {
				env = e
				return &stripes{w: size.X, h: size.Y, env: e}, nil
			})
			var w countingWriter
			err := chain(in).TempDir(t.TempDir()).JPEG(70).ToWriter(&w)
			noPanic(t, what, err)
			if err != nil && !errors.Is(err, ErrMemoryLimit) {
				t.Errorf("%s: %v", what, err)
			}
			if peak := env.Budget.Peak(); peak > DefaultMemoryLimit {
				t.Errorf("%s: %d bytes reserved at the peak, the limit is %d", what, peak, DefaultMemoryLimit)
			}
			if used := env.Budget.Used(); used != 0 {
				t.Errorf("%s: %d bytes still reserved after the run", what, used)
			}
			t.Logf("%s: peak %dKB, %v", what, env.Budget.Peak()>>10, err)
		}
	}
}

// TestArgumentExtremes: values at and beyond the ends of what each setting
// takes give a result or an error, never a panic or a hang.
func TestArgumentExtremes(t *testing.T) {
	in := pngBytes(t, testutil.Images(64, 48)["nrgba"])
	logo := FromImage(testutil.Images(20, 10)["nrgba"])
	inf, nan := math.Inf(1), math.NaN()
	big := math.MaxInt32
	type c struct {
		name string
		m    func() *Image
		ok   bool // whether it must succeed
	}
	base := func() *Image { return FromBytes(in) }
	cases := []c{
		{"quality 1", func() *Image { return base().JPEG(1) }, true},
		{"quality 100", func() *Image { return base().JPEG(100) }, true},
		{"quality 101", func() *Image { return base().JPEG(101) }, false},
		{"quality -1", func() *Image { return base().JPEG(-1) }, false},
		{"webp quality 1", func() *Image { return base().WebP(WebPOptions{Quality: 1}) }, true},
		{"webp quality 100", func() *Image { return base().WebP(WebPOptions{Quality: 100}) }, true},
		{"webp quality 101", func() *Image { return base().WebP(WebPOptions{Quality: 101}) }, false},
		{"webp quality -1", func() *Image { return base().WebP(WebPOptions{Quality: -1}) }, false},
		{"webp effort 9", func() *Image { return base().WebP(WebPOptions{Lossless: true, Effort: 9}) }, true},
		{"webp effort 99", func() *Image { return base().WebP(WebPOptions{Lossless: true, Effort: 99}) }, false},
		{"webp effort -1", func() *Image { return base().WebP(WebPOptions{Lossless: true, Effort: -1}) }, false},
		{"png level 9", func() *Image { return base().PNG(PNGOptions{CompressionLevel: 9}) }, true},
		{"png level 10", func() *Image { return base().PNG(PNGOptions{CompressionLevel: 10}) }, false},
		{"png level -1", func() *Image { return base().PNG(PNGOptions{CompressionLevel: -1}) }, false},
		{"png 256 colours", func() *Image { return base().PNG(PNGOptions{Colors: 256}) }, true},
		{"png 257 colours", func() *Image { return base().PNG(PNGOptions{Colors: 257}) }, false},
		{"png 1 colour", func() *Image { return base().PNG(PNGOptions{Colors: 1}) }, false},
		{"png -1 colours", func() *Image { return base().PNG(PNGOptions{Colors: -1}) }, false},
		{"gif 1 colour", func() *Image { return base().GIF(GIFOptions{Colors: 1}) }, true},
		{"gif 257 colours", func() *Image { return base().GIF(GIFOptions{Colors: 257}) }, false},
		{"gif -1 colours", func() *Image { return base().GIF(GIFOptions{Colors: -1}) }, false},
		{"tiff compression 9", func() *Image { return base().TIFF(TIFFOptions{Compression: 9}) }, false},
		{"resize 0x0", func() *Image { return base().Resize(0, 0) }, false},
		{"resize -1", func() *Image { return base().Resize(-1, 10) }, false},
		{"resize to MaxInt32", func() *Image { return base().Resize(big, 0) }, false},
		{"resize to MaxInt32 x MaxInt32", func() *Image { return base().Resize(big, big, ResizeOptions{Fit: FitFill}) }, false},
		{"resize unknown fit", func() *Image { return base().Resize(10, 10, ResizeOptions{Fit: 99}) }, false},
		{"resize unknown kernel", func() *Image { return base().Resize(10, 10, ResizeOptions{Kernel: 99}) }, false},
		{"no enlargement", func() *Image { return base().Resize(big, 0, ResizeOptions{WithoutEnlargement: true}) }, true},
		{"rotate 45", func() *Image { return base().Rotate(45) }, false},
		{"rotate -90", func() *Image { return base().Rotate(-90) }, true},
		{"rotate MinInt", func() *Image { return base().Rotate(math.MinInt) }, false},
		{"rotate 360000", func() *Image { return base().Rotate(360000) }, true},
		{"rotate twice", func() *Image { return base().Rotate(90, 180) }, false},
		{"extract nothing", func() *Image { return base().Extract(0, 0, 0, 0) }, false},
		{"extract negative", func() *Image { return base().Extract(-1, 0, 10, 10) }, false},
		{"extract overflow", func() *Image { return base().Extract(math.MaxInt, math.MaxInt, math.MaxInt, math.MaxInt) }, false},
		{"extract all", func() *Image { return base().Extract(0, 0, 64, 48) }, true},
		{"opacity 2", func() *Image { return base().Composite(logo, CompositeOptions{Opacity: 2}) }, false},
		{"opacity -1", func() *Image { return base().Composite(logo, CompositeOptions{Opacity: -1}) }, false},
		{"opacity NaN", func() *Image { return base().Composite(logo, CompositeOptions{Opacity: nan}) }, false},
		{"angle 1e12", func() *Image { return base().Composite(logo, CompositeOptions{Angle: 1e12}) }, true},
		{"angle Inf", func() *Image { return base().Composite(logo, CompositeOptions{Angle: inf}) }, false},
		{"ratio 1e300", func() *Image { return base().Composite(logo, CompositeOptions{LeftRatio: 1e300, TopRatio: -1e300}) }, true},
		{"ratio 1e300 turned", func() *Image {
			return base().Composite(logo, CompositeOptions{LeftRatio: 1e300, TopRatio: -1e300, Angle: 30})
		}, true},
		{"offset MaxInt", func() *Image { return base().Composite(logo, CompositeOptions{Left: math.MaxInt, Top: math.MinInt}) }, true},
		{"offset MaxInt from the far corner", func() *Image {
			return base().Composite(logo, CompositeOptions{Gravity: SouthEast, Left: math.MaxInt, Top: math.MaxInt})
		}, true},
		{"offset MaxInt tiled", func() *Image {
			return base().Composite(logo, CompositeOptions{Left: math.MaxInt, Top: math.MinInt, Tile: true})
		}, true},
		{"anchor 1e300", func() *Image {
			return base().Composite(logo, CompositeOptions{Anchor: CustomPosition, AnchorX: 1e300, AnchorY: -1e300})
		}, true},
		{"pivot 1e300", func() *Image {
			return base().Composite(logo, CompositeOptions{Angle: 10, Pivot: CustomPosition, PivotX: 1e300, PivotY: 1e300})
		}, true},
		{"tile gap MaxInt", func() *Image {
			return base().Composite(logo, CompositeOptions{Tile: true, TileGapX: math.MaxInt, TileGapY: math.MaxInt})
		}, true},
		{"tile gap MinInt", func() *Image {
			return base().Composite(logo, CompositeOptions{Tile: true, TileGapX: math.MinInt, TileGapY: math.MinInt})
		}, false},
		{"tile gap -19 (one pixel apart)", func() *Image {
			return base().Composite(logo, CompositeOptions{Tile: true, TileGapX: -19, TileGapY: -9})
		}, true},
		{"gravity 99", func() *Image { return base().Composite(logo, CompositeOptions{Gravity: 99}) }, false},
		{"memory limit 1", func() *Image { return base().MemoryLimit(1) }, false},
		{"memory limit -1", func() *Image { return base().MemoryLimit(-1) }, true},
		{"memory limit MaxInt64", func() *Image { return base().MemoryLimit(math.MaxInt64) }, true},
		{"threads -1", func() *Image { return base().Threads(-1) }, true},
		{"threads 1000", func() *Image { return base().Threads(1000) }, true},
		{"pixel limit -1", func() *Image { return base().LimitInputPixels(-1) }, true},
		{"pixel limit 1", func() *Image { return base().LimitInputPixels(1) }, false},
		{"pixel limit exact", func() *Image { return base().LimitInputPixels(64 * 48) }, true},
		{"pixel limit one short", func() *Image { return base().LimitInputPixels(64*48 - 1) }, false},
		{"max file size 1", func() *Image { return base().JPEG(80).MaxFileSize(1) }, false},
		{"max file size MaxInt64", func() *Image { return base().JPEG(80).MaxFileSize(math.MaxInt64) }, true},
		{"max file size -1", func() *Image { return base().JPEG(80).MaxFileSize(-1) }, false},
		{"temp dir that does not exist", func() *Image { return base().TempDir(filepath.Join(t.TempDir(), "no", "such")) }, true},
		{"text empty", func() *Image { return Text("") }, false},
		{"text of spaces", func() *Image { return Text("   \n\n  ") }, false},
		{"text size 1", func() *Image { return Text("x", TextOptions{Size: 1}) }, true},
		{"text size 4096", func() *Image { return Text("x", TextOptions{Size: 4096}).MemoryLimit(1 << 30) }, true},
		{"text size 0.01", func() *Image { return Text("x", TextOptions{Size: 0.01}) }, false},
		{"text size -5", func() *Image { return Text("x", TextOptions{Size: -5}) }, false},
		{"text size NaN", func() *Image { return Text("x", TextOptions{Size: nan}) }, false},
		{"text size Inf", func() *Image { return Text("x", TextOptions{Size: inf}) }, false},
		{"text size 1e9", func() *Image { return Text("x", TextOptions{Size: 1e9}) }, false},
		{"text width 1", func() *Image { return Text("some words to wrap", TextOptions{Width: 1}) }, true},
		{"text width -1", func() *Image { return Text("x", TextOptions{Width: -1}) }, false},
		{"text padding -1", func() *Image { return Text("x", TextOptions{Padding: -1}) }, false},
		{"text padding 1e9", func() *Image { return Text("x", TextOptions{Padding: 1e9}) }, false},
		{"text line spacing 0.5", func() *Image { return Text("a\nb", TextOptions{LineSpacing: 0.5}) }, true},
		{"text line spacing 0.01", func() *Image { return Text("a\nb", TextOptions{LineSpacing: 0.01}) }, false},
		{"text line spacing -1", func() *Image { return Text("a\nb", TextOptions{LineSpacing: -1}) }, false},
		{"text line spacing NaN", func() *Image { return Text("a\nb", TextOptions{LineSpacing: nan}) }, false},
		{"text stroke -1", func() *Image { return Text("x", TextOptions{StrokeWidth: -1}) }, false},
		{"text stroke NaN", func() *Image { return Text("x", TextOptions{StrokeWidth: nan}) }, false},
		{"text stroke 1e6", func() *Image { return Text("x", TextOptions{StrokeWidth: 1e6}) }, false},
		{"text shadow blur -1", func() *Image { return Text("x", TextOptions{ShadowColor: color.Black, ShadowBlur: -1}) }, false},
		{"text shadow blur 1e9", func() *Image { return Text("x", TextOptions{ShadowColor: color.Black, ShadowBlur: 1e9}) }, false},
		{"text shadow offset MaxInt", func() *Image {
			return Text("x", TextOptions{ShadowColor: color.Black, ShadowX: math.MaxInt, ShadowY: math.MinInt})
		}, false},
		{"text of control characters", func() *Image { return Text("a\x00b\tc\r\nd\x7f\u200b\ufeff") }, true},
		{"text that is not UTF-8", func() *Image { return Text("\xff\xfe\xfd abc \xc0\x80") }, true},
		{"text without a glyph", func() *Image { return Text("\U0001F600\U000E0001￿") }, true},
		{"text of 20000 characters, wrapped", func() *Image { return Text(strings.Repeat("word ", 4000), TextOptions{Width: 300, Size: 8}) }, true},
		{"text of one 5000-character word", func() *Image { return Text(strings.Repeat("W", 5000), TextOptions{Width: 100, Size: 8}) }, true},
		{"text font that does not exist", func() *Image { return Text("x", TextOptions{Fonts: []string{"/no/such/font.ttf"}}) }, false},
		{"text font data that is no font", func() *Image { return Text("x", TextOptions{FontData: [][]byte{[]byte("not a font")}}) }, false},
		{"text two options", func() *Image { return Text("x", TextOptions{}, TextOptions{}) }, false},
		{"blur 0.01", func() *Image { return base().Blur(0.01) }, true},
		{"blur 1000", func() *Image { return base().Blur(1000) }, true},
		{"blur 1000.1", func() *Image { return base().Blur(1000.1) }, false},
		{"blur 0", func() *Image { return base().Blur(0) }, false},
		{"blur NaN", func() *Image { return base().Blur(nan) }, false},
		{"sharpen sigma 1000", func() *Image { return base().Sharpen(SharpenOptions{Sigma: 1000}) }, true},
		{"sharpen amount 100", func() *Image { return base().Sharpen(SharpenOptions{Sigma: 1, Amount: 100}) }, true},
		{"sharpen amount 101", func() *Image { return base().Sharpen(SharpenOptions{Sigma: 1, Amount: 101}) }, false},
		{"sharpen amount Inf", func() *Image { return base().Sharpen(SharpenOptions{Sigma: 1, Amount: inf}) }, false},
		{"sharpen threshold 255", func() *Image { return base().Sharpen(SharpenOptions{Sigma: 1, Threshold: 255}) }, true},
		{"modulate 0, 0", func() *Image { return base().Modulate(0, 0, 0) }, true},
		{"jpeg quant table 8", func() *Image { return base().JPEG(80, JPEGOptions{QuantTable: 8}) }, true},
		{"gif lossy 255", func() *Image { return base().GIF(GIFOptions{Lossy: 255}) }, true},
		{"gif lossy 256", func() *Image { return base().GIF(GIFOptions{Lossy: 256}) }, false},
		{"jpeg quant table 9", func() *Image { return base().JPEG(80, JPEGOptions{QuantTable: 9}) }, true},
		{"jpeg quant table 10", func() *Image { return base().JPEG(80, JPEGOptions{QuantTable: 10}) }, false},
		{"jpeg quant table -1", func() *Image { return base().JPEG(80, JPEGOptions{QuantTable: -1}) }, false},
		{"modulate 1e6", func() *Image { return base().Modulate(1e6, 1e6, 1e9) }, true},
		{"modulate Inf", func() *Image { return base().Modulate(inf, 1, 0) }, false},
		{"extend 0", func() *Image { return base().Extend(0, 0, 0, 0, nil) }, true},
		{"extend MaxInt", func() *Image { return base().Extend(math.MaxInt, 0, 0, 0, nil) }, false},
		{"extend MaxInt32 both", func() *Image { return base().Extend(0, math.MaxInt32, 0, math.MaxInt32, nil) }, false},
		{"trim 255", func() *Image { return base().Trim(255) }, true},
		{"linear 1e300", func() *Image { return base().Linear(1e300, -1e300) }, true},
		{"linear NaN", func() *Image { return base().Linear(nan, 0) }, false},
		{"gamma 100", func() *Image { return base().Gamma(100) }, true},
		{"gamma 1e-300", func() *Image { return base().Gamma(1e-300) }, true},
		{"gamma -1", func() *Image { return base().Gamma(-1) }, false},
		{"threshold 0", func() *Image { return base().Threshold(0) }, true},
		{"threshold 255", func() *Image { return base().Threshold(255) }, true},
		{"normalize 0, 100", func() *Image { return base().Normalize(NormalizeOptions{Lower: 0, Upper: 100}) }, true},
		{"normalize 99.99, 100", func() *Image { return base().Normalize(NormalizeOptions{Lower: 99.99, Upper: 100}) }, true},
		{"normalize NaN", func() *Image { return base().Normalize(NormalizeOptions{Lower: nan, Upper: 1}) }, false},
		{"median 99", func() *Image { return base().Median(99) }, true},
		{"median 3", func() *Image { return base().Median(3) }, true},
		{"median 0", func() *Image { return base().Median(0) }, false},
		{"convolve 99x99", func() *Image {
			return base().Convolve(Convolution{Width: 99, Height: 99, Weights: make([]float64, 99*99)})
		}, true},
		{"convolve 101x1", func() *Image {
			return base().Convolve(Convolution{Width: 101, Height: 1, Weights: make([]float64, 101)})
		}, false},
		{"convolve scale Inf", func() *Image {
			return base().Convolve(Convolution{Width: 1, Height: 1, Weights: []float64{1}, Scale: inf})
		}, false},
		{"convolve weights 1e300", func() *Image {
			return base().Convolve(Convolution{Width: 3, Height: 1, Weights: []float64{1e300, -1e300, 1e300}})
		}, true},
		{"trim Inf", func() *Image { return base().Trim(inf) }, false},
		{"turn 1e300 degrees", func() *Image { return base().RotateBy(1e300, nil) }, true},
		{"turn 1e-300 degrees", func() *Image { return base().RotateBy(1e-300, nil) }, true},
		{"turn -0.0000001 degrees", func() *Image { return base().RotateBy(-1e-7, color.Transparent) }, true},
		{"turn NaN", func() *Image { return base().RotateBy(nan, nil) }, false},
		{"watermark 10 times the width", func() *Image { return base().Composite(base(), CompositeOptions{WidthRatio: 10}) }, true},
		{"watermark a millionth of the height", func() *Image { return base().Composite(base(), CompositeOptions{HeightRatio: 1e-6}) }, true},
		{"watermark ratio 10.5", func() *Image { return base().Composite(base(), CompositeOptions{WidthRatio: 10.5}) }, false},
		{"watermark ratio NaN", func() *Image { return base().Composite(base(), CompositeOptions{HeightRatio: nan}) }, false},
		{"watermark ratio -1", func() *Image { return base().Composite(base(), CompositeOptions{WidthRatio: -1}) }, false},
	}
	for _, c := range cases {
		done := make(chan error, 1)
		go func() {
			_, err := c.m().ToBytes()
			done <- err
		}()
		err := <-done
		noPanic(t, c.name, err)
		switch {
		case c.ok && err != nil:
			t.Errorf("%s: %v", c.name, err)
		case !c.ok && err == nil:
			t.Errorf("%s: accepted", c.name)
		case !c.ok && errors.Is(err, ErrFormat):
			t.Errorf("%s: a wrong argument reported as a bad image: %v", c.name, err)
		}
	}
}

// TestAwkwardReaders: inputs that arrive a byte at a time, in halves, with
// their error together with the last data, or not at all.
func TestAwkwardReaders(t *testing.T) {
	files := realFiles(t)
	for name, data := range files {
		want, err := FromBytes(data).PNG().ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for rname, r := range map[string]io.Reader{
			"one byte at a time": iotest.OneByteReader(bytes.NewReader(data)),
			"half reads":         iotest.HalfReader(bytes.NewReader(data)),
			"data with EOF":      iotest.DataErrReader(bytes.NewReader(data)),
		} {
			got, err := FromReader(r).PNG().ToBytes()
			noPanic(t, name+", "+rname, err)
			if err != nil || !bytes.Equal(got, want) {
				t.Errorf("%s, %s: differs from reading the bytes (%v)", name, rname, err)
			}
		}
		// A reader that times out is a failure of the reading, not a bad
		// image. (A file that arrived whole in the first read is complete:
		// then there is nothing to report.)
		got, err := FromReader(iotest.TimeoutReader(bytes.NewReader(data))).PNG().ToBytes()
		noPanic(t, name+", timeout", err)
		if err == nil && !bytes.Equal(got, want) {
			t.Errorf("%s, a reader that times out: no error, and another picture", name)
		} else if err != nil && (!errors.Is(err, iotest.ErrTimeout) || errors.Is(err, ErrFormat)) {
			t.Errorf("%s, a reader that times out: %v", name, err)
		}
		// A reader that never gives anything.
		_, err = FromReader(stuck{}).PNG().ToBytes()
		noPanic(t, name+", stuck", err)
		if !errors.Is(err, io.ErrNoProgress) || errors.Is(err, ErrFormat) {
			t.Errorf("a reader that makes no progress: %v", err)
		}
	}
}

// TestReaderFailsLate: a read error far into a large file, in every format.
func TestReaderFailsLate(t *testing.T) {
	src := photoLike(700, 500)
	for name, choose := range outputs {
		data, err := choose(FromImage(src)).ToBytes()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		// (Not the very last bytes: a GIF's first frame is complete
		// before its trailer.)
		for _, at := range []int{len(data) / 3, len(data) * 9 / 10} {
			boom := errors.New("connection reset")
			_, err = FromReader(&brokenAfter{data: data, n: at, err: boom}).PNG().ToBytes()
			noPanic(t, name, err)
			if !errors.Is(err, boom) || errors.Is(err, ErrFormat) {
				t.Errorf("%s, read error after %d of %d bytes: %v", name, at, len(data), err)
			}
		}
	}
}

type stuck struct{}

func (stuck) Read([]byte) (int, error) { return 0, nil }

// TestFilesAtTheEdges: the file system cases a program meets.
func TestFilesAtTheEdges(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "in.png")
	data := pngBytes(t, testutil.Images(120, 90)["rgba-opaque"])
	if err := os.WriteFile(in, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Writing over the input.
	if err := Open(in).Resize(60, 0).ToFile(in); err != nil {
		t.Fatalf("in place: %v", err)
	}
	if info, err := Open(in).Metadata(); err != nil || info.Width != 60 {
		t.Fatalf("in place: %+v, %v", info, err)
	}
	// Writing over an existing file, and a failed run leaving it alone.
	out := filepath.Join(dir, "out.jpg")
	os.WriteFile(out, []byte("old"), 0o644)
	if err := Open(in).ToFile(out); err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(out)
	if err := FromBytes(data[:len(data)/2]).ToFile(out); !errors.Is(err, ErrFormat) {
		t.Fatalf("truncated input: %v", err)
	}
	if now, _ := os.ReadFile(out); !bytes.Equal(now, good) {
		t.Error("a failed run changed the existing output file")
	}
	for name, c := range map[string]struct {
		m    *Image
		path string
	}{
		"input does not exist":      {Open(filepath.Join(dir, "none.png")), out},
		"input is a directory":      {Open(dir), out},
		"input is empty":            {FromBytes(nil), out},
		"input of one byte":         {FromBytes([]byte{0xff}), out},
		"output directory missing":  {Open(in), filepath.Join(dir, "no", "such", "out.png")},
		"output is a directory":     {Open(in), dir},
		"output without extension":  {Text("x"), filepath.Join(dir, "plain")},
		"output extension unknown":  {Open(in), filepath.Join(dir, "x.heic")},
		"output name is empty":      {Open(in), ""},
		"output name has a NUL":     {Open(in), filepath.Join(dir, "a\x00b.png")},
		"output name is very long":  {Open(in), filepath.Join(dir, strings.Repeat("n", 5000)+".png")},
		"no input at all":           {&Image{}, out},
		"overlay does not exist":    {Open(in).Composite(Open(filepath.Join(dir, "none.png"))), out},
		"overlay is not an image":   {Open(in).Composite(FromBytes([]byte("not an image at all"))), out},
		"overlay without any input": {Open(in).Composite(&Image{}), out},
	} {
		err := c.m.ToFile(c.path)
		noPanic(t, name, err)
		if name == "output without extension" {
			if err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Nothing but the files written on purpose is left.
	names, _ := filepath.Glob(filepath.Join(dir, "*"))
	if len(names) != 3 {
		t.Errorf("left in the directory: %v", names)
	}
}

// TestAnimationPixelLimit: the pixel limit counts every frame of an
// animation, so that a small file with thousands of frames cannot keep a
// run busy for minutes.
func TestAnimationPixelLimit(t *testing.T) {
	g := testAnimation(60, 40) // 6 frames
	var buf bytes.Buffer
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatal(err)
	}
	in := buf.Bytes()
	if _, err := FromBytes(in).Animated().LimitInputPixels(60 * 40 * 6).GIF().ToBytes(); err != nil {
		t.Errorf("six frames within a limit of six frames: %v", err)
	}
	_, err := FromBytes(in).Animated().LimitInputPixels(60*40*6 - 1).GIF().ToBytes()
	if !errors.Is(err, ErrPixelLimit) || !strings.Contains(err.Error(), "frames") {
		t.Errorf("six frames with a limit one pixel short: %v", err)
	}
	// Without Animated only the first frame counts.
	if _, err := FromBytes(in).LimitInputPixels(60 * 40).GIF().ToBytes(); err != nil {
		t.Errorf("first frame only: %v", err)
	}
	// A file of a few kilobytes with many frames on a large canvas: refused
	// after the frames the limit allows.
	many := &gif.GIF{Config: image.Config{Width: 2000, Height: 2000}}
	for i := 0; i < 400; i++ {
		many.Image = append(many.Image, image.NewPaletted(image.Rect(i%5, i%7, i%5+1, i%7+1), color.Palette{color.Black, color.White}))
		many.Delay = append(many.Delay, 1)
	}
	buf.Reset()
	if err := gif.EncodeAll(&buf, many); err != nil {
		t.Fatal(err)
	}
	// (Counted in frames that went through the chain, not in seconds: a
	// slow machine is not a failure.) With a palette chosen for all frames
	// they are counted first, so none is; with the fixed palette, those the
	// limit allows.
	for _, c := range []struct {
		opts GIFOptions
		want int64
	}{{GIFOptions{}, 0}, {GIFOptions{FixedPalette: true}, 3}} {
		var made, closed, rows atomic.Int64
		_, err = FromBytes(buf.Bytes()).Animated().LimitInputPixels(2000 * 2000 * 3).
			Pipe(closeCounter{&made, &closed, &rows}).GIF(c.opts).ToBytes()
		if !errors.Is(err, ErrPixelLimit) {
			t.Errorf("%+v: 400 frames of 2000x2000 in %d bytes with a limit of three: %v", c.opts, buf.Len(), err)
		}
		if made.Load() != c.want || closed.Load() != c.want {
			t.Errorf("%+v: %d frames were processed (%d closed) before the limit of three stopped the run, want %d", c.opts, made.Load(), closed.Load(), c.want)
		}
	}
}

// TestOutputPermissions: a result is a file like any other the program
// writes, readable by whoever may read those; not one that only its owner
// can read, as the temporary file it is written to would be. (Where the
// system has such permissions.)
func TestOutputPermissions(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("no permission bits to compare")
	}
	dir := t.TempDir()
	in := pngBytes(t, testutil.Images(40, 30)["rgba-opaque"])
	// What a file the program creates in the ordinary way gets.
	plain := filepath.Join(dir, "plain")
	f, err := os.OpenFile(plain, os.O_WRONLY|os.O_CREATE, 0o666)
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	want, _ := os.Stat(plain)
	if want.Mode().Perm()&0o044 == 0 {
		t.Skipf("the umask leaves new files private (%v): nothing to tell apart", want.Mode().Perm())
	}
	out := filepath.Join(dir, "out.jpg")
	if err := FromBytes(in).ToFile(out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Stat(out); got.Mode().Perm() != want.Mode().Perm() {
		t.Errorf("a new result has permissions %v, an ordinary new file %v", got.Mode().Perm(), want.Mode().Perm())
	}
	// Replacing a file keeps what that file allowed.
	if err := os.Chmod(out, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := FromBytes(in).Resize(20, 0).ToFile(out); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.Stat(out); got.Mode().Perm() != 0o600 {
		t.Errorf("a replaced private file now has permissions %v", got.Mode().Perm())
	}
	// A failed run leaves neither a result nor its unfinished file.
	bad := filepath.Join(dir, "bad.jpg")
	if err := FromBytes(in[:len(in)/2]).ToFile(bad); err == nil {
		t.Fatal("a truncated input was accepted")
	}
	if names, _ := filepath.Glob(filepath.Join(dir, "*")); len(names) != 2 {
		t.Errorf("left in the directory: %v", names)
	}
	if hidden, _ := filepath.Glob(filepath.Join(dir, ".goimgtools-*")); len(hidden) != 0 {
		t.Errorf("unfinished files left: %v", hidden)
	}
}
