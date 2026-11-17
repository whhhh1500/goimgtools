package jpegstream

import (
	"bytes"
	"image"
	"io"
	"math"
	"math/rand/v2"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/resize"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// shrunkPlanes returns the planes of a decoded Gray or YCbCr image with their
// sizes.
func shrunkPlanes(m image.Image) (p [][]byte, w, h, cw, ch []int) {
	switch m := m.(type) {
	case *image.Gray:
		b := m.Rect
		return [][]byte{m.Pix}, []int{b.Dx()}, []int{b.Dy()}, []int{m.Stride}, nil
	case *image.YCbCr:
		b := m.Rect
		cb := image.Rect(0, 0, b.Dx(), b.Dy())
		switch m.SubsampleRatio {
		case image.YCbCrSubsampleRatio422:
			cb.Max.X = (cb.Max.X + 1) / 2
		case image.YCbCrSubsampleRatio420:
			cb.Max.X, cb.Max.Y = (cb.Max.X+1)/2, (cb.Max.Y+1)/2
		case image.YCbCrSubsampleRatio440:
			cb.Max.Y = (cb.Max.Y + 1) / 2
		case image.YCbCrSubsampleRatio411:
			cb.Max.X = (cb.Max.X + 3) / 4
		case image.YCbCrSubsampleRatio410:
			cb.Max.X, cb.Max.Y = (cb.Max.X+3)/4, (cb.Max.Y+1)/2
		}
		return [][]byte{m.Y, m.Cb, m.Cr}, []int{b.Dx(), cb.Dx(), cb.Dx()}, []int{b.Dy(), cb.Dy(), cb.Dy()},
			[]int{m.YStride, m.CStride, m.CStride}, nil
	}
	return nil, nil, nil, nil, nil
}

// TestShrinkOnLoad decodes the standard library's test files reduced by 2, 4
// and 8 and compares their YCbCr with the full decode's averaged over cells
// (resize.Shrink), which they match but for rounding, the cells cut by the
// image edge and pixels that the full decode clips.
func TestShrinkOnLoad(t *testing.T) {
	for name, data := range corpus(t) {
		for _, k := range []int{2, 4, 8} {
			full, err := Open(bytes.NewReader(data), nil)
			if err != nil {
				continue
			}
			full.(*stream).KeepYCbCr()
			box, err := resize.Shrink(full, k)
			if err != nil {
				full.Close()
				continue
			}
			want, err := pipeline.Collect(box)
			box.Close()
			if err != nil {
				continue
			}
			src, err := Open(bytes.NewReader(data), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !src.(*stream).ShrinkOnLoad(k) {
				src.Close()
				if _, ok := want.(*image.CMYK); !ok && !src.(*stream).d.flex {
					t.Errorf("%s: ShrinkOnLoad(%d) refused", name, k)
				}
				continue
			}
			src.(*stream).KeepYCbCr()
			if src.Bounds() != want.Bounds() {
				t.Fatalf("%s /%d: bounds %v, want %v", name, k, src.Bounds(), want.Bounds())
			}
			got, err := pipeline.Collect(src)
			src.Close()
			if err != nil {
				t.Fatalf("%s /%d: %v", name, k, err)
			}
			gp, gw, gh, gs, _ := shrunkPlanes(got)
			wp, ww, wh, ws, _ := shrunkPlanes(want)
			if len(gp) == 0 || len(gp) != len(wp) {
				t.Fatalf("%s /%d: %T, want %T", name, k, got, want)
			}
			var n, sum, far, worst int
			for i := range gp {
				if gw[i] != ww[i] || gh[i] != wh[i] {
					t.Fatalf("%s /%d plane %d: %dx%d, want %dx%d", name, k, i, gw[i], gh[i], ww[i], wh[i])
				}
				for y := 0; y < gh[i]; y++ {
					for x := 0; x < gw[i]; x++ {
						d := int(gp[i][y*gs[i]+x]) - int(wp[i][y*ws[i]+x])
						d = max(d, -d)
						n++
						sum += d
						// The last column and row are cells that the image
						// edge cuts: Shrink averages the pixels in the
						// image, the reduced IDCT the whole block.
						if x < gw[i]-1 && y < gh[i]-1 {
							worst = max(worst, d)
							if d > 2 {
								far++
							}
						}
					}
				}
			}
			// Where the full decode clips ringing around black or white,
			// its average is a few levels off the reduced IDCT's; and
			// libjpeg's reduced IDCTs leave out a few high frequencies,
			// which in coarsely quantized detail is more.
			mean := float64(sum) / float64(n)
			if mean > 0.6 || far*50 > n || worst > 16 {
				t.Errorf("%s /%d: mean difference %.3f, %d of %d pixels off by more than 2, at most %d", name, k, mean, far, n, worst)
			}
			t.Logf("%s /%d: mean %.3f, far %d of %d, worst %d", name, k, mean, far, n, worst)
		}
	}
}

// TestReducedIDCT checks the reduced IDCTs against averages of the exact
// (floating-point) IDCT of random blocks.
func TestReducedIDCT(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for iter := 0; iter < 20000; iter++ {
		var b block
		b[0] = rng.Int32N(2041) - 1024
		for i := 1; i < blockSize; i++ {
			if rng.IntN(4) == 0 {
				b[i] = rng.Int32N(401) - 200
			}
		}
		var px [8][8]float64
		for y := range 8 {
			for x := range 8 {
				var s float64
				for v := range 8 {
					for u := range 8 {
						cu, cv := 1.0, 1.0
						if u == 0 {
							cu = 1 / math.Sqrt2
						}
						if v == 0 {
							cv = 1 / math.Sqrt2
						}
						s += cu * cv * float64(b[8*v+u]) *
							math.Cos(float64(2*x+1)*float64(u)*math.Pi/16) *
							math.Cos(float64(2*y+1)*float64(v)*math.Pi/16)
					}
				}
				px[y][x] = s / 4
			}
		}
		for _, n := range []int{4, 2, 1} {
			var dst [64]byte
			c := b
			switch n {
			case 4:
				idct4Store(&c, dst[:], 8)
			case 2:
				idct2Store(&c, dst[:], 8)
			case 1:
				idct1Store(&c, dst[:])
			}
			k := 8 / n
			for y := range n {
				for x := range n {
					var s float64
					for j := range k {
						for i := range k {
							s += px[k*y+j][k*x+i]
						}
					}
					want := math.Round(s/float64(k*k) + 128)
					want = max(0, min(255, want))
					if d := math.Abs(float64(dst[8*y+x]) - want); d > 1 {
						t.Fatalf("%d×%d block %d (%d,%d): got %d, want %v", n, n, iter, x, y, dst[8*y+x], want)
					}
				}
			}
		}
	}
}

// TestShrinkInStep checks that the scans decoded in step give the pixels the
// coefficient store gives, reduced too.
func TestShrinkInStep(t *testing.T) {
	for name, data := range corpus(t) {
		for _, k := range []int{1, 2, 4, 8} {
			var imgs [2]image.Image
			for i, random := range []bool{false, true} {
				src, err := Open(bytes.NewReader(data), nil)
				if err != nil {
					break
				}
				if random {
					WithRandomAccess(src, func() (io.ReaderAt, int64, error) { return bytes.NewReader(data), int64(len(data)), nil })
				}
				if k > 1 && !src.(*stream).ShrinkOnLoad(k) {
					src.Close()
					break
				}
				imgs[i], err = pipeline.Collect(src)
				src.Close()
				if err != nil {
					t.Fatalf("%s /%d random %v: %v", name, k, random, err)
				}
			}
			if imgs[0] != nil && imgs[1] != nil {
				same(t, name, imgs[0], imgs[1])
			}
		}
	}
}
