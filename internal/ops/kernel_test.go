package ops

import (
	"bytes"
	"image"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestBlurLibvipsSIMD: the SIMD passes of BlurLibvips give the bytes of
// the Go code: widths that leave no, some and all bytes to the Go code,
// rows wide enough to be split between helpers, sigmas from a mask of one
// pixel to one of many boxes, opaque images and ones with alpha, the rows
// in memory and on disk.
func TestBlurLibvipsSIMD(t *testing.T) {
	if !useKernelSIMD {
		t.Skip("no SIMD")
	}
	defer func() { useKernelSIMD = true }()
	rng := rand.New(rand.NewPCG(5, 6))
	onDisk := false
	sizes := [][2]int{{1, 1}, {1, 9}, {2, 3}, {3, 7}, {5, 4}, {17, 13}, {64, 40}, {1201, 23}}
	for _, size := range sizes {
		w, h := size[0], size[1]
		smooth := rng.IntN(2) == 0
		alpha := image.NewNRGBA(image.Rect(0, 0, w, h))
		solid := image.NewRGBA(image.Rect(0, 0, w, h))
		for i := range alpha.Pix {
			v := uint8(rng.IntN(256))
			if smooth {
				v = uint8((i/4%w)*255/max(w, 1)) ^ uint8(rng.IntN(8))
			}
			alpha.Pix[i], solid.Pix[i] = v, v
			if i%4 == 3 {
				solid.Pix[i] = 255
			}
		}
		for _, m := range []image.Image{alpha, solid} {
			for _, sigma := range []float64{0.3, 1, 3, 7.5, 21} {
				for _, mode := range []int{0, 1, 2} {
					run := func(simd bool) []uint8 {
						useKernelSIMD = simd
						env := pipeline.NewEnv(0, "")
						switch mode {
						case 1:
							env.SetHelpers(3)
						case 2:
							env = pipeline.NewEnv(512<<10, t.TempDir())
						}
						src := testutil.ChoppedEnv(m, 7, env)
						if _, isRGBA := m.(*image.RGBA); isRGBA {
							src = opaque{src}
						}
						b, err := BlurLibvips(src, sigma, MinAmplitude)
						if err != nil {
							t.Fatal(err)
						}
						got := collect(t, b, env)
						if f, is := b.(*filterStage); is && f.onDisk {
							onDisk = true
						}
						out := image.NewNRGBA(got.Bounds())
						for y := 0; y < h; y++ {
							for x := 0; x < w; x++ {
								out.Set(x, y, got.At(x, y))
							}
						}
						return out.Pix
					}
					want, got := run(false), run(true)
					if !bytes.Equal(want, got) {
						t.Fatalf("%dx%d %T sigma %v mode %d: the SIMD passes differ", w, h, m, sigma, mode)
					}
				}
			}
		}
	}
	if !onDisk {
		t.Error("no rows were kept on disk")
	}
}
