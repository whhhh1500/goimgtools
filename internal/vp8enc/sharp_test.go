package vp8enc

import (
	"bytes"
	"hash/fnv"
	"image"
	"io"
	"math"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// sharpTestImage has gradients and thin saturated strokes, made with
// integers only (so that it is the same on every platform).
func sharpTestImage(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := []uint8{uint8(x * 255 / w), uint8(y * 255 / h), uint8(x * y), 255}
			if (x/3+y/5)%4 == 0 {
				c[0], c[1], c[2] = 230, 20, 40 // red strokes
			} else if (x+2*y)%11 < 2 {
				c[0], c[1], c[2] = 30, 40, 220 // blue ones
			}
			copy(m.Pix[m.PixOffset(x, y):], c)
		}
	}
	return m
}

// TestSharpLibwebp checks sharp YUV against libwebp's: the FNV-64a sums are
// of the Y, U and V planes (one after the other, unpadded) that libwebp
// 1.6.0's SharpYuvConvert gives for these images with kSharpYuvMatrixWebp,
// with its early stop taken out so that it makes its four passes. (On the
// first image libwebp makes four passes anyway.)
func TestSharpLibwebp(t *testing.T) {
	for _, c := range []struct {
		name string
		m    image.Image
		want uint64
	}{
		{"strokes 67x41", sharpTestImage(67, 41), 0xa882b5f6e32428cc},
		{"noise 33x9", testutil.Images(33, 9)["rgba-opaque"], 0x923c16a43b3c67d8},
	} {
		for _, parts := range []int{1, 3, 4} {
			b := c.m.Bounds()
			w, h := b.Dx(), b.Dy()
			uw, uh := (w+1)/2, (h+1)/2
			Y, U, V := make([]uint8, w*h), make([]uint8, uw*uh), make([]uint8, uw*uh)
			env := pipeline.NewEnv(0, "")
			env.SetHelpers(parts - 1)
			s := newSharpYUV(w, env)
			// The columns shared out among parts as on a wide image.
			s.parts = parts
			rows := [2][]uint8{make([]uint8, 4*(w+1)), make([]uint8, 4*(w+1))}
			fill := func(dst []uint8, y int) {
				for x := 0; x < w; x++ {
					r, g, bl, _ := c.m.At(b.Min.X+x, b.Min.Y+y).RGBA()
					dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = uint8(r>>8), uint8(g>>8), uint8(bl>>8), 255
				}
				copy(dst[4*w:4*w+4], dst[4*w-4:4*w])
			}
			put := func(p int) error {
				y1 := make([]uint8, w)
				if 2*p+1 < h {
					y1 = Y[(2*p+1)*w : (2*p+2)*w]
				}
				s.yuv(p, Y[2*p*w:2*p*w+w], y1, U[p*uw:(p+1)*uw], V[p*uw:(p+1)*uw])
				return nil
			}
			for y := 0; y < h; y += 2 {
				fill(rows[0], y)
				if y+1 < h {
					fill(rows[1], y+1)
				} else {
					copy(rows[1], rows[0])
				}
				if err := s.push(rows[0], rows[1], y+2 >= h, put); err != nil {
					t.Fatal(err)
				}
			}
			if s.emitted != uh {
				t.Fatalf("%s: %d pairs given out, want %d", c.name, s.emitted, uh)
			}
			f := fnv.New64a()
			f.Write(Y)
			f.Write(U)
			f.Write(V)
			if got := f.Sum64(); got != c.want {
				t.Errorf("%s, %d parts: sum %016x, want %016x", c.name, parts, got, c.want)
			}
		}
	}
}

// TestSharpStrips checks that sharp YUV, which hands out rows eight behind
// the ones it takes in, gives the same file whatever the strips of the
// source and the number of goroutines, at sizes around a macroblock row.
func TestSharpStrips(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {3, 2}, {5, 7}, {9, 15}, {16, 16}, {17, 17}, {31, 18}, {40, 25}, {33, 40}} {
		m := sharpTestImage(sz[0], sz[1])
		var ref []byte
		for _, n := range []int{1, 2, 3, 16, 100} {
			for _, helpers := range []int{0, 3} {
				env := pipeline.NewEnv(0, t.TempDir())
				env.SetHelpers(helpers)
				data := mustEncode(t, testutil.ChoppedEnv(m, n, env), 80, nil)
				if ref == nil {
					ref = data
				} else if string(ref) != string(data) {
					t.Fatalf("%v: strips of %d rows, %d helpers give different bytes", sz, n, helpers)
				}
			}
		}
		d := decodeX(t, ref)
		if d.w != sz[0] || d.h != sz[1] {
			t.Fatalf("%v: decoded %dx%d", sz, d.w, d.h)
		}
	}
}

// TestSharpColour checks that sharp YUV keeps the brightness of thin
// saturated lines, after a decoder's chroma interpolation, which is what it
// is for: in linear light, the luminance of the decoded image must be much
// closer to the source's than with the plain conversion (which averages
// the colours of each 2x2 block, and so dims a red line on blue, say).
func TestSharpColour(t *testing.T) {
	for kind, m := range []image.Image{strokes(160, 120, false), strokes(160, 120, true)} {
		for _, q := range []int{30, 90} {
			var psnr [2]float64
			for i, dbg := range []*debug{{noSharp: true}, nil} {
				data := mustEncode(t, testutil.Chopped(m, 16), q, dbg)
				psnr[i] = luminancePSNR(m, toRGB(decodeX(t, data)))
			}
			t.Logf("strokes %d q%d: luminance PSNR plain %.2f dB, sharp %.2f dB", kind, q, psnr[0], psnr[1])
			if psnr[1] < psnr[0]+2 {
				t.Errorf("strokes %d q%d: luminance PSNR %.2f dB with sharp YUV, %.2f dB without", kind, q, psnr[1], psnr[0])
			}
		}
	}
}

// strokes draws thin saturated lines over gradients: red ones across and
// down, or blue and yellow diagonals.
func strokes(w, h int, diagonal bool) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			c := []uint8{uint8(40 + x*150/w), uint8(60 + y*120/h), uint8(200 - (x+y)*150/(w+h)), 255}
			switch {
			case !diagonal && (x%7 == 3 || y%9 == 4):
				c[0], c[1], c[2] = 250, 30, 30
			case diagonal && (x/2+y)%6 == 0:
				c[0], c[1], c[2] = 20, 20, 240
			case diagonal && (x+y/2)%7 == 0:
				c[0], c[1], c[2] = 240, 220, 20
			}
			copy(m.Pix[m.PixOffset(x, y):], c)
		}
	}
	return m
}

// luminancePSNR compares the luminance (Rec. 709, in linear light) of
// interleaved RGB to that of m.
func luminancePSNR(m image.Image, rgb []uint8) float64 {
	lum := func(r, g, b uint32) float64 {
		lin := func(v uint32) float64 {
			f := float64(v) / 255
			if f <= 0.04045 {
				return f / 12.92
			}
			return math.Pow((f+0.055)/1.055, 2.4)
		}
		return 0.2126*lin(r) + 0.7152*lin(g) + 0.0722*lin(b)
	}
	b := m.Bounds()
	var sum float64
	i := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, _ := m.At(x, y).RGBA()
			e := lum(r>>8, g>>8, bl>>8) - lum(uint32(rgb[3*i]), uint32(rgb[3*i+1]), uint32(rgb[3*i+2]))
			sum += e * e
			i++
		}
	}
	return 10 * math.Log10(float64(i)/sum)
}

// TestEffort checks the effort levels of lossy output: every level gives
// a file both decoders read, level 5 already does all there is (so 9 is
// the same file), the low levels leave out the slow steps (3 the trellis
// in the choice of 4x4 modes), and levels out of range are refused.
func TestEffort(t *testing.T) {
	m := sharpTestImage(96, 70)
	files := map[int][]byte{}
	for _, effort := range []int{1, 2, 3, 4, 5, 9} {
		var buf bytes.Buffer
		if err := encode(&buf, testutil.Chopped(m, 16), &Options{Quality: 70, Effort: effort}, nil); err != nil {
			t.Fatalf("effort %d: %v", effort, err)
		}
		d := decodeX(t, buf.Bytes())
		if d.w != 96 || d.h != 70 {
			t.Fatalf("effort %d: decoded %dx%d", effort, d.w, d.h)
		}
		files[effort] = buf.Bytes()
	}
	if !bytes.Equal(files[5], files[9]) {
		t.Error("effort 9 differs from effort 5")
	}
	for _, k := range [][2]int{{1, 2}, {2, 3}, {1, 3}, {3, 4}, {3, 5}} {
		if bytes.Equal(files[k[0]], files[k[1]]) {
			t.Errorf("effort %d and %d give the same file", k[0], k[1])
		}
	}
	// The default is 5.
	var def bytes.Buffer
	if err := encode(&def, testutil.Chopped(m, 16), &Options{Quality: 70}, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(def.Bytes(), files[5]) {
		t.Error("the default effort is not 5")
	}
	for _, effort := range []int{-1, 10} {
		if err := encode(io.Discard, testutil.Chopped(m, 16), &Options{Quality: 70, Effort: effort}, nil); err == nil {
			t.Errorf("effort %d: no error", effort)
		}
	}
}
