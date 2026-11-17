package tests

import (
	"bytes"
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
	for _, o := range []ResizeOptions{{Method: 2}, {Method: -1}, {Method: ResizeLibvips, LinearLight: true}} {
		if _, err := FromBytes(rgb).Resize(60, 41, o).PNG().ToBytes(); err == nil {
			t.Errorf("Resize(%+v) accepted", o)
		}
	}
	if _, err := FromBytes(rgb).Do("resize", "60", "41", "fill", "cubic", "center", "srgb", "pillow").PNG().ToBytes(); err == nil {
		t.Error("an unknown method word accepted")
	}
	// A filter of one's own, and the extremes: no panic, the sizes asked.
	own := &Filter{Support: 1.5, At: func(x float64) float64 { return max(0, 1.5-max(x, -x)) }}
	for _, c := range []struct {
		w, h int
		o    ResizeOptions
	}{
		{40, 30, ResizeOptions{Fit: FitFill, Filter: own}},
		{400, 300, ResizeOptions{Fit: FitFill, Filter: own}},
		{1, 1, ResizeOptions{Fit: FitFill, Kernel: KernelMKS2021}},
		{1, 500, ResizeOptions{Fit: FitFill, Kernel: KernelLanczos3}},
		{3000, 1, ResizeOptions{Fit: FitFill, Kernel: KernelNearest}},
	} {
		c.o.Method = ResizeLibvips
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
