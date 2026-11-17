package tests

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// collectWith runs m as a stream whose stages may share their work with
// helpers, and returns the result and what was still reserved after it.
func collectWith(t *testing.T, m *Image, limit int64, helpers int, dir string) (*image.RGBA, int64) {
	t.Helper()
	env := pipeline.NewEnv(limit, dir)
	env.SetHelpers(helpers)
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
	out := image.NewRGBA(img.Bounds())
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out, env.Budget.Used()
}

// TestSameWithHelpers: the operations that share their work give the same
// pixels, to the byte, however many helpers they have; also when their
// rows are kept in a temporary file; and they give back all they took.
func TestSameWithHelpers(t *testing.T) {
	images := map[string][]byte{
		"photo 700x300":       pngBytes(t, photoLike(700, 300)),
		"wide 3000x40":        pngBytes(t, photoLike(3000, 40)),
		"tall 300x1000":       pngBytes(t, photoLike(300, 1000)),
		"translucent 400x150": pngBytes(t, testutil.Images(400, 150)["nrgba"]),
		"narrow 9x200":        pngBytes(t, photoLike(9, 200)),
	}
	ops := map[string]func(*Image) *Image{
		"blur 0.7":     func(m *Image) *Image { return m.Blur(0.7) },
		"blur 3":       func(m *Image) *Image { return m.Blur(3) },
		"blur 40":      func(m *Image) *Image { return m.Blur(40) },
		"blur 600":     func(m *Image) *Image { return m.Blur(600) },
		"unsharp 2":    func(m *Image) *Image { return m.Sharpen(SharpenOptions{Sigma: 2, Amount: 1.2, Threshold: 2}) },
		"unsharp 25":   func(m *Image) *Image { return m.Sharpen(SharpenOptions{Sigma: 25}) },
		"mild sharpen": func(m *Image) *Image { return m.Sharpen() },
		"modulate":     func(m *Image) *Image { return m.Modulate(1.1, 1.4, 33) },
		"median 5":     func(m *Image) *Image { return m.Median(5) },
		"resize in linear light": func(m *Image) *Image {
			return m.Resize(1100, 37, ResizeOptions{Fit: FitFill, LinearLight: true})
		},
		"clahe": func(m *Image) *Image { return m.CLAHE(CLAHEOptions{Width: 17, Height: 12}) },
		"convolve": func(m *Image) *Image {
			return m.Convolve(Convolution{Width: 3, Height: 3, Weights: []float64{1, 2, 1, 2, 4, 2, 1, 2, 1}})
		},
		"normalize, tint": func(m *Image) *Image { return m.Normalize().Tint(color.RGBA{200, 100, 0, 255}) },
		"channels": func(m *Image) *Image {
			return m.Recomb([][]float64{{0.4, 0.6, 0}, {0, 1, 0}, {0.2, 0.2, 0.6}}).Unflatten().ExtractChannel(ChannelAlpha)
		},
		"rotate 30":        func(m *Image) *Image { return m.RotateBy(30, color.White) },
		"rotate -77.7":     func(m *Image) *Image { return m.RotateBy(-77.7, color.Transparent) },
		"blur, rotate, sh": func(m *Image) *Image { return m.Blur(1.5).RotateBy(12, nil).Sharpen() },
	}
	dir := t.TempDir()
	for iname, in := range images {
		for oname, op := range ops {
			want, used := collectWith(t, op(FromBytes(in)), 256<<20, 0, dir)
			if used != 0 {
				t.Errorf("%s, %s: %d bytes still reserved", iname, oname, used)
			}
			for _, c := range []struct {
				helpers int
				limit   int64
			}{{1, 256 << 20}, {3, 256 << 20}, {7, 256 << 20}, {3, 6 << 20}} {
				got, used := collectWith(t, op(FromBytes(in)), c.limit, c.helpers, dir)
				if !bytes.Equal(got.Pix, want.Pix) || got.Rect != want.Rect {
					t.Errorf("%s, %s, %d helpers, limit %dMB: not the pixels of one thread", iname, oname, c.helpers, c.limit>>20)
				}
				if used != 0 {
					t.Errorf("%s, %s, %d helpers: %d bytes still reserved", iname, oname, c.helpers, used)
				}
			}
		}
	}
	if n := tempFiles(dir); n != 0 {
		t.Errorf("%d temporary files left", n)
	}

	// Through the public setting: the same file with 1 and with 8 threads.
	in := images["photo 700x300"]
	var first []byte
	for _, threads := range []int{1, 4, 8} {
		out, err := FromBytes(in).Blur(2).Sharpen(SharpenOptions{Sigma: 1}).Modulate(1, 1.2, 10).RotateBy(5, color.White).
			Threads(threads).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = out
		} else if !bytes.Equal(out, first) {
			t.Errorf("%d threads: a different file than with 1", threads)
		}
	}
	_ = fmt.Sprint
}
