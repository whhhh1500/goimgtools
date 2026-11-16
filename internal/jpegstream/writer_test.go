package jpegstream

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"math"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestMatchesStdlib requires the same bytes as jpeg.Encode for every pixel
// type, odd sizes and several qualities, with misaligned source strips.
func TestMatchesStdlib(t *testing.T) {
	for _, sz := range [][2]int{{191, 143}, {16, 16}, {1, 1}, {33, 8}, {64, 257}} {
		for name, m := range testutil.Images(sz[0], sz[1]) {
			for i, q := range []int{1, 50, 75, 92, 100} {
				var want, got bytes.Buffer
				if err := jpeg.Encode(&want, m, &jpeg.Options{Quality: q}); err != nil {
					t.Fatal(err)
				}
				src := testutil.Chopped(m, []int{1, 7, 16, 32, 300}[i])
				if err := Encode(&got, src, &Options{Quality: q}); err != nil {
					t.Fatalf("%s/%v/q%d: %v", name, sz, q, err)
				}
				if !bytes.Equal(got.Bytes(), want.Bytes()) {
					t.Errorf("%s/%v/q%d: bytes differ from jpeg.Encode", name, sz, q)
				}
			}
		}
	}
	// A source whose bounds do not start at the origin.
	full := testutil.Images(200, 160)["rgba"].(*image.RGBA)
	m := full.SubImage(image.Rect(13, 21, 180, 150))
	var want, got bytes.Buffer
	jpeg.Encode(&want, m, nil)
	if err := Encode(&got, testutil.Chopped(m, 5), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Error("sub-image: bytes differ from jpeg.Encode")
	}
}

// TestEncodeGivesBackItsBand: Encode does not close the source, but what it
// reserved for itself is released when it returns.
func TestEncodeGivesBackItsBand(t *testing.T) {
	for name, m := range testutil.Images(640, 48) {
		env := pipeline.NewEnv(0, "")
		if err := Encode(io.Discard, testutil.ChoppedEnv(m, 7, env), &Options{Quality: 80}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if env.Budget.Used() != 0 {
			t.Errorf("%s: %d bytes still reserved after Encode", name, env.Budget.Used())
		}
	}
}

// TestSameWithHelpers: with helpers sharing the transform of each band,
// every kind of output (plain, optimized tables, progressive) is the same
// to the byte as without, for every pixel type, and the band buffer is
// given back.
func TestSameWithHelpers(t *testing.T) {
	for _, sz := range [][2]int{{700, 97}, {17, 33}, {1, 1}, {4000, 20}} {
		for name, m := range testutil.Images(sz[0], sz[1]) {
			for _, o := range []Options{{Quality: 83}, {Quality: 60, Optimize: true}, {Quality: 90, Progressive: true},
				{Quality: 75, Trellis: true}, {Quality: 85, Optimize: true, Trellis: true}, {Quality: 50, Progressive: true, Trellis: true}} {
				var want bytes.Buffer
				if err := Encode(&want, testutil.Chopped(m, 16), &o); err != nil {
					t.Fatal(err)
				}
				for _, helpers := range []int{1, 3, 7} {
					env := pipeline.NewEnv(0, t.TempDir())
					env.SetHelpers(helpers)
					var got bytes.Buffer
					if err := Encode(&got, testutil.ChoppedEnv(m, 16, env), &o); err != nil {
						t.Fatalf("%s/%v/%+v/%d helpers: %v", name, sz, o, helpers, err)
					}
					if !bytes.Equal(got.Bytes(), want.Bytes()) {
						t.Errorf("%s/%v/%+v/%d helpers: other bytes than without helpers", name, sz, o, helpers)
					}
					if u := env.Budget.Used(); u != 0 {
						t.Errorf("%s/%v/%+v/%d helpers: %d bytes still reserved", name, sz, o, helpers, u)
					}
				}
			}
		}
	}
}

// TestTrellis: trellis quantization writes files every decoder reads, for
// every pixel type and size, that are smaller than plain rounding on a
// photograph-like image at nearly the same fidelity.
func TestTrellis(t *testing.T) {
	for _, sz := range [][2]int{{1, 1}, {17, 9}, {191, 143}, {64, 257}} {
		for name, m := range testutil.Images(sz[0], sz[1]) {
			for _, o := range []Options{{Quality: 1, Trellis: true}, {Quality: 75, Trellis: true}, {Quality: 100, Trellis: true, Optimize: true}, {Quality: 90, Trellis: true, Progressive: true}} {
				var buf bytes.Buffer
				if err := Encode(&buf, testutil.Chopped(m, 7), &o); err != nil {
					t.Fatalf("%s/%v/%+v: %v", name, sz, o, err)
				}
				d, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
				if err != nil || d.Bounds().Size() != m.Bounds().Size() {
					t.Fatalf("%s/%v/%+v: decoded %v, %v", name, sz, o, d, err)
				}
			}
		}
	}
	// A smooth picture with fine texture, as photographs are.
	m := image.NewRGBA(image.Rect(0, 0, 512, 384))
	for y := 0; y < 384; y++ {
		for x := 0; x < 512; x++ {
			n := uint8((x*7 + y*13 + x*y/7) % 23)
			m.Pix[m.PixOffset(x, y)+0] = uint8(40+x*180/512) + n
			m.Pix[m.PixOffset(x, y)+1] = uint8(60+y*150/384) + n/2
			m.Pix[m.PixOffset(x, y)+2] = uint8(128+((x/40+y/30)%2)*60) - n
			m.Pix[m.PixOffset(x, y)+3] = 255
		}
	}
	psnr := func(b []byte) float64 {
		d, err := jpeg.Decode(bytes.NewReader(b))
		if err != nil {
			t.Fatal(err)
		}
		var se float64
		for y := 0; y < 384; y++ {
			for x := 0; x < 512; x++ {
				r, g, bl, _ := d.At(x, y).RGBA()
				p := m.Pix[m.PixOffset(x, y):]
				for c, v := range []uint32{r >> 8, g >> 8, bl >> 8} {
					e := float64(v) - float64(p[c])
					se += e * e
				}
			}
		}
		return 10 * math.Log10(255*255/(se/(512*384*3)))
	}
	for _, q := range []int{60, 80, 92} {
		var plain, tr bytes.Buffer
		Encode(&plain, testutil.Chopped(m, 16), &Options{Quality: q, Optimize: true})
		Encode(&tr, testutil.Chopped(m, 16), &Options{Quality: q, Optimize: true, Trellis: true})
		pp, pt := psnr(plain.Bytes()), psnr(tr.Bytes())
		t.Logf("q%d: plain %d bytes %.2f dB, trellis %d bytes %.2f dB", q, plain.Len(), pp, tr.Len(), pt)
		if tr.Len() >= plain.Len() || pt < pp-1.5 {
			t.Errorf("q%d: trellis %d bytes %.2f dB against plain %d bytes %.2f dB", q, tr.Len(), pt, plain.Len(), pp)
		}
	}
}

// TestDeadZone: with the dead zone of jpegli, an AC coefficient 0.55 of a
// step from zero is dropped and one 0.6 of a step away kept; without it,
// both round to one step. The DC coefficient is never dropped.
func TestDeadZone(t *testing.T) {
	var e encoder
	for i := range e.quant {
		for j := range e.quant[i] {
			e.quant[i][j] = 10
			e.recip[i][j] = recip(8 * 10)
		}
	}
	var b block
	b[0] = 44             // 0.55 of a step (the DCT gives 8 times)
	b[unzig[1]] = 44      // 0.55
	b[unzig[2]] = -48     // -0.6
	b[unzig[3]] = 48 * 10 // 6 steps
	for _, c := range []struct {
		dz   *[2]int64
		want [4]int16
	}{{nil, [4]int16{1, 1, -1, 6}}, {&jpegliDeadZone, [4]int16{1, 0, -1, 6}}} {
		e.deadZone = c.dz
		var zz qblock
		e.quantizeCoefs(&b, quantIndexLuminance, &zz)
		if got := [4]int16{zz[0], zz[1], zz[2], zz[3]}; got != c.want {
			t.Errorf("dead zone %v: %v, want %v", c.dz != nil, got, c.want)
		}
	}
}
