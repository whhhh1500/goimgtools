package jpegstream

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"io"
	"math"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestMatchesLibjpeg requires the bytes libjpeg-turbo writes (Pillow
// 12.1.1's Image.save at the same settings, its JFIF segment aside: the
// sums are of its files) for every pixel type (colours with alpha
// multiplied by it), odd sizes and several qualities, with misaligned
// source strips; and the other subsamplings and optimized tables.
func TestMatchesLibjpeg(t *testing.T) {
	sum := func(b []byte) string { return fmt.Sprintf("%x", sha256.Sum256(b))[:16] }
	for _, c := range []struct {
		name string
		w, h int
		sums [5]string
	}{
		{"gray16", 16, 16, [5]string{"62cf86caa6e0bf46", "bc4626390def3caa", "cfeb2de9d591263a", "b0f2270d1c748253", "c774d58170aed973"}},
		{"gray16", 191, 143, [5]string{"6e373426988c3662", "5bc53985da72f6e3", "c74e7db4263fc196", "65aad8cde8a47983", "6efb26976feab670"}},
		{"gray16", 1, 1, [5]string{"fee755a5c4ec4c01", "21943d0b00c4dbfb", "8e6a22f63038178e", "c69e8e6460a5ff7d", "87492e7de2afbbeb"}},
		{"gray16", 33, 8, [5]string{"ab71ed1961a9340f", "54fa6d1902773292", "be1025a9173060e2", "6a48d9f030b86c08", "903887e3b6ca102b"}},
		{"gray16", 64, 257, [5]string{"f5c355a2c1effc3f", "8a0d92d0da3774a9", "b8b1fbff192b9323", "d8df1e9d0b1c8b35", "2536e7251b957906"}},
		{"gray", 16, 16, [5]string{"567341e1a3a64ecc", "d59cbb4435eb35e2", "0d3cba1dbf9ca6c8", "99f4a8733469819a", "847212ab4dc3f4fe"}},
		{"gray", 191, 143, [5]string{"aa5c433805d77601", "faea42793f6c34e2", "952492c188b15756", "c1d25575eb73b160", "917b9095af917629"}},
		{"gray", 1, 1, [5]string{"695c9964251bac95", "e6f20b4a2f50848c", "e031982c1f59812f", "b0a4f94cbbe6fbae", "ff13cc9dab9066bb"}},
		{"gray", 33, 8, [5]string{"59c66b28799aeae7", "f0f5974e0f29d4bd", "c3a0510ccd743cce", "d22f0dee9e8a6bf5", "45d600c7dfe5d726"}},
		{"gray", 64, 257, [5]string{"fe963212753f3524", "7173cbad916b6890", "c32d207e5dd7b6c2", "1257299e05f7adbe", "e930d8361d06cbd8"}},
		{"nrgba64", 16, 16, [5]string{"9dd0536efe0c7ddc", "697bdcc8e0bbdfb1", "a5f188e3bcf41320", "ca3786a81a9bf55d", "9b95fbbe71485fc8"}},
		{"nrgba64", 191, 143, [5]string{"826e7dde596e06ef", "9108f1221223106f", "2a818688fa05b7ff", "1c48a9af4d546974", "b2bcb770f141fec8"}},
		{"nrgba64", 1, 1, [5]string{"fee755a5c4ec4c01", "21943d0b00c4dbfb", "8e6a22f63038178e", "c69e8e6460a5ff7d", "87492e7de2afbbeb"}},
		{"nrgba64", 33, 8, [5]string{"9b58e105cf74aff5", "74208c5a893d55ec", "09e47c2ea7079032", "d1f7b4734d6a9f22", "1ddf359b41da6dfa"}},
		{"nrgba64", 64, 257, [5]string{"7cd3f1607d0c2a70", "b326c8e48f59aab9", "3dd90fe2e5d6b899", "c432157f52f85ca2", "6846cbbb956adea1"}},
		{"nrgba", 16, 16, [5]string{"ddd2a2b4e72d0444", "38b231cea0cfbce8", "85dd80bc7f45b14e", "5fd079c6126b706b", "dde4d384bb3f99ea"}},
		{"nrgba", 191, 143, [5]string{"570234c25b2c9c1b", "6e7a72f0f4baa8f6", "10a6602796e0f4c9", "698b16ba00f344df", "729b750a45df8998"}},
		{"nrgba", 1, 1, [5]string{"fee755a5c4ec4c01", "21943d0b00c4dbfb", "8e6a22f63038178e", "c69e8e6460a5ff7d", "87492e7de2afbbeb"}},
		{"nrgba", 33, 8, [5]string{"7dc2c9026bfddf89", "25db7620e35c55dc", "7ab8d9fc994ce5bb", "320679ff5c5b39b7", "6ad5b616131c8b4f"}},
		{"nrgba", 64, 257, [5]string{"7b6fc43b899ab685", "b5b2fb583df88768", "9e60c294abff09c0", "c2200da4766be2be", "7e513738dfbaacb9"}},
		{"paletted", 16, 16, [5]string{"9380f17aa8128c07", "817e3b62efd4eb05", "9145e9c6298c13d8", "c31230d622be2490", "31c423ee6a74441a"}},
		{"paletted", 191, 143, [5]string{"800f983302c9df9d", "11ad17331a35bb59", "c5a76a0f36394399", "b88fd8398164835a", "2f3154189b476672"}},
		{"paletted", 1, 1, [5]string{"de9a6a5734a43e20", "1416b230953cf6f3", "a33a950cfcd75ce5", "1364efe7d8ccf75e", "4997b5c68238558f"}},
		{"paletted", 33, 8, [5]string{"05eac9211929e761", "8a36f9b7ba0ed898", "832241a250a8a8f9", "ee349b751dd3f631", "35cb46ce46b51a40"}},
		{"paletted", 64, 257, [5]string{"dc40863f73886108", "617f3fd56a9f981a", "2dcc27aef9c6d175", "f26b1675d05e119f", "c218a8c093ec6c70"}},
		{"rgba-opaque", 16, 16, [5]string{"fbdc180e492972cb", "29aa4b740280c5ce", "1c050eb10a96806a", "6f84a72bea1c4a90", "645a81d7df0d2f01"}},
		{"rgba-opaque", 191, 143, [5]string{"85551061e8ceb8b9", "1226d6c285785271", "a80ca82f751c0254", "a55e65ddb14925da", "8617794cc31bc785"}},
		{"rgba-opaque", 1, 1, [5]string{"fee755a5c4ec4c01", "21943d0b00c4dbfb", "8e6a22f63038178e", "c69e8e6460a5ff7d", "87492e7de2afbbeb"}},
		{"rgba-opaque", 33, 8, [5]string{"6d55b61e15436622", "4386612ff9423633", "d5521cb272e4b97d", "f5cdb87f240ef7fc", "33539b464fbd4295"}},
		{"rgba-opaque", 64, 257, [5]string{"b9c7bc8e7dab1afb", "d8329dd608224e4f", "b40256f4061520c7", "7624048c8a1eaeaa", "c3bd66ca8917f1d4"}},
		{"rgba", 16, 16, [5]string{"ddd2a2b4e72d0444", "38b231cea0cfbce8", "85dd80bc7f45b14e", "5fd079c6126b706b", "dde4d384bb3f99ea"}},
		{"rgba", 191, 143, [5]string{"570234c25b2c9c1b", "6e7a72f0f4baa8f6", "10a6602796e0f4c9", "698b16ba00f344df", "729b750a45df8998"}},
		{"rgba", 1, 1, [5]string{"fee755a5c4ec4c01", "21943d0b00c4dbfb", "8e6a22f63038178e", "c69e8e6460a5ff7d", "87492e7de2afbbeb"}},
		{"rgba", 33, 8, [5]string{"7dc2c9026bfddf89", "25db7620e35c55dc", "7ab8d9fc994ce5bb", "320679ff5c5b39b7", "6ad5b616131c8b4f"}},
		{"rgba", 64, 257, [5]string{"7b6fc43b899ab685", "b5b2fb583df88768", "9e60c294abff09c0", "c2200da4766be2be", "7e513738dfbaacb9"}},
	} {
		m := testutil.Images(c.w, c.h)[c.name]
		for i, q := range []int{1, 50, 75, 92, 100} {
			var got bytes.Buffer
			if err := Encode(&got, testutil.Chopped(m, []int{1, 7, 16, 32, 300}[i]), &Options{Quality: q}); err != nil {
				t.Fatalf("%s/%dx%d/q%d: %v", c.name, c.w, c.h, q, err)
			}
			if s := sum(got.Bytes()); s != c.sums[i] {
				t.Errorf("%s/%dx%d/q%d: sum %s, libjpeg's %s", c.name, c.w, c.h, q, s, c.sums[i])
			}
		}
	}
	for _, c := range []struct {
		name string
		o    Options
		sum  string
	}{
		{"rgba-opaque", Options{Quality: 85, Subsampling: Subsample422}, "d5db42b21189f398"},
		{"rgba-opaque", Options{Quality: 85, Subsampling: Subsample444}, "9fdae59acd34c30e"},
		{"rgba-opaque", Options{Quality: 70, Optimize: true}, "29dafc24a8316cdb"},
		{"rgba-opaque", Options{Quality: 95, Optimize: true, Subsampling: Subsample444}, "bd4221b74abb9fe8"},
		{"nrgba", Options{Quality: 85, Subsampling: Subsample422}, "f7602bc1eb6ac2de"},
		{"nrgba", Options{Quality: 85, Subsampling: Subsample444}, "e4853aa6431c72ba"},
		{"nrgba", Options{Quality: 70, Optimize: true}, "541efa625639c682"},
		{"nrgba", Options{Quality: 95, Optimize: true, Subsampling: Subsample444}, "e55227bedc3bc09c"},
		{"paletted", Options{Quality: 85, Subsampling: Subsample422}, "7c1e0875115e41f9"},
		{"paletted", Options{Quality: 85, Subsampling: Subsample444}, "b1f9070313dbae59"},
		{"paletted", Options{Quality: 70, Optimize: true}, "589b1d412776dc52"},
		{"paletted", Options{Quality: 95, Optimize: true, Subsampling: Subsample444}, "0d9ce9f1630de244"},
		{"gray", Options{Quality: 70, Optimize: true}, "c91d71f59ec797d8"},
	} {
		var got bytes.Buffer
		if err := Encode(&got, testutil.Chopped(testutil.Images(191, 143)[c.name], 16), &c.o); err != nil {
			t.Fatal(err)
		}
		if s := sum(got.Bytes()); s != c.sum {
			t.Errorf("%s %+v: sum %s, libjpeg's %s", c.name, c.o, s, c.sum)
		}
	}
	// A source whose bounds do not start at the origin: the same file as
	// the same pixels at the origin.
	full := testutil.Images(200, 160)["rgba"].(*image.RGBA)
	m := full.SubImage(image.Rect(13, 21, 180, 150))
	at0 := image.NewRGBA(image.Rect(0, 0, 167, 129))
	draw.Draw(at0, at0.Rect, m, m.Bounds().Min, draw.Src)
	var want, got bytes.Buffer
	if err := Encode(&want, testutil.Chopped(at0, 16), nil); err != nil {
		t.Fatal(err)
	}
	if err := Encode(&got, testutil.Chopped(m, 5), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Error("sub-image: other bytes than at the origin")
	}
	// YCbCr images whose corner is even, odd or negative, of every
	// subsampling ratio: the chroma of a 4:2:0 one is taken as it is only
	// when its corner is even and not negative. Either way the file is the
	// one of the same Y, Cb and Cr at every pixel (as libjpeg takes them).
	for _, ratio := range []image.YCbCrSubsampleRatio{image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio422,
		image.YCbCrSubsampleRatio440, image.YCbCrSubsampleRatio411, image.YCbCrSubsampleRatio410, image.YCbCrSubsampleRatio444} {
		for _, r := range []image.Rectangle{image.Rect(0, 0, 37, 29), image.Rect(2, 4, 41, 36), image.Rect(3, 5, 40, 34),
			image.Rect(2, 7, 35, 40), image.Rect(-7, -3, 30, 26), image.Rect(-6, -10, 28, 23), image.Rect(-1, 0, 16, 17)} {
			m := image.NewYCbCr(r, ratio)
			for i := range m.Y {
				m.Y[i] = uint8(i * 13)
			}
			for i := range m.Cb {
				m.Cb[i], m.Cr[i] = uint8(i*29), uint8(i*i)
			}
			for _, sub := range []image.Image{m, m.SubImage(image.Rect(r.Min.X+1, r.Min.Y+1, r.Max.X, r.Max.Y))} {
				b := sub.Bounds()
				every := image.NewYCbCr(image.Rect(0, 0, b.Dx(), b.Dy()), image.YCbCrSubsampleRatio444)
				for y := 0; y < b.Dy(); y++ {
					for x := 0; x < b.Dx(); x++ {
						c := sub.At(b.Min.X+x, b.Min.Y+y).(color.YCbCr)
						i := every.YOffset(x, y)
						every.Y[i], every.Cb[i], every.Cr[i] = c.Y, c.Cb, c.Cr
					}
				}
				var want, got bytes.Buffer
				if err := Encode(&want, testutil.Chopped(every, 16), &Options{Quality: 90}); err != nil {
					t.Fatal(err)
				}
				if err := Encode(&got, testutil.Chopped(sub, 3), &Options{Quality: 90}); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(got.Bytes(), want.Bytes()) {
					t.Errorf("YCbCr %v %v: other bytes than from every pixel's values", ratio, b)
				}
			}
		}
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

// TestNonzeros checks the mask of nonzero values against a plain loop, with
// the lane values that matter: 0, 1, -1, the extremes of int16.
func TestNonzeros(t *testing.T) {
	vals := []int16{0, 1, -1, 2, 0x7fff, -0x8000, 0x100, -0x100, 0x4000}
	var zz qblock
	for k := 0; k < 20000; k++ {
		for i := range zz {
			zz[i] = 0
			if (k*31+i*17)%5 != 0 {
				zz[i] = vals[(k*7+i*13+k/9)%len(vals)]
			}
		}
		var want uint64
		for i, v := range zz {
			if v != 0 {
				want |= 1 << i
			}
		}
		if got := nonzeros(&zz); got != want {
			t.Fatalf("%v: %x, want %x", zz, got, want)
		}
	}
}

// TestYcc: ycc is libjpeg's conversion (jccolor.c), its factors made as
// libjpeg makes them, for every colour.
func TestYcc(t *testing.T) {
	fix := func(x float64) int64 { return int64(x*65536 + 0.5) }
	const half, offset = 1 << 15, 128 << 16
	for c := 0; c < 1<<24; c++ {
		r, g, b := int64(c>>16), int64(c>>8&0xff), int64(c&0xff)
		y0 := (fix(0.29900)*r + fix(0.58700)*g + fix(0.11400)*b + half) >> 16
		cb0 := (-fix(0.16874)*r - fix(0.33126)*g + fix(0.50000)*b + offset + half - 1) >> 16
		cr0 := (fix(0.50000)*r - fix(0.41869)*g - fix(0.08131)*b + offset + half - 1) >> 16
		y1, cb1, cr1 := ycc(int32(r), int32(g), int32(b))
		if int64(y1) != y0 || int64(cb1) != cb0 || int64(cr1) != cr0 {
			t.Fatalf("%d %d %d: %d %d %d, want %d %d %d", r, g, b, y1, cb1, cr1, y0, cb0, cr0)
		}
	}
}
