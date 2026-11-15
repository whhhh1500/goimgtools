package quant

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"math/rand"
	"os"
	"sort"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/pngstream"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// collect reads a quantized source to its end, checking every strip.
func collect(t *testing.T, src pipeline.Source, colors int) *image.Paletted {
	t.Helper()
	b := src.Bounds()
	var out *image.Paletted
	y := b.Min.Y
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		p, ok := s.(*image.Paletted)
		if !ok {
			t.Fatalf("strip is %T, want *image.Paletted", s)
		}
		if len(p.Palette) == 0 || len(p.Palette) > colors {
			t.Fatalf("palette has %d entries, want 1..%d", len(p.Palette), colors)
		}
		r := p.Bounds()
		if r.Min.X != b.Min.X || r.Max.X != b.Max.X || r.Min.Y != y || r.Max.Y > b.Max.Y || r.Dy() < 1 {
			t.Fatalf("strip %v does not continue %v at row %d", r, b, y)
		}
		y = r.Max.Y
		if out == nil {
			out = image.NewPaletted(b, p.Palette)
		} else if !samePalette(out.Palette, p.Palette) {
			t.Fatalf("palette changed between strips")
		}
		for i := range p.Pix[:r.Dy()*p.Stride] {
			if int(p.Pix[i]) >= len(p.Palette) {
				t.Fatalf("index %d outside palette of %d", p.Pix[i], len(p.Palette))
			}
		}
		pipeline.CopyRows(out, p, r.Min.Y, r.Max.Y)
	}
	if y != b.Max.Y {
		t.Fatalf("strips end at row %d, want %d", y, b.Max.Y)
	}
	if _, err := src.Next(); err != io.EOF {
		t.Fatalf("Next after the end: %v, want io.EOF", err)
	}
	return out
}

func samePalette(a, b color.Palette) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func tempFiles(t *testing.T, dir string) int {
	t.Helper()
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	return len(es)
}

// nrgbaAt is the 8-bit non-premultiplied colour of a pixel.
func nrgbaAt(m image.Image, x, y int) color.NRGBA {
	return color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
}

// psnr compares a quantized image with its source over the colour channels
// as seen over a black and over a white background.
func psnr(src image.Image, q *image.Paletted) float64 {
	b := src.Bounds()
	var se float64
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			s, d := nrgbaAt(src, x, y), q.Palette[q.ColorIndexAt(x, y)].(color.NRGBA)
			sc, dc := [3]uint8{s.R, s.G, s.B}, [3]uint8{d.R, d.G, d.B}
			for c := 0; c < 3; c++ {
				sp, dp := float64(sc[c])*float64(s.A)/255, float64(dc[c])*float64(d.A)/255
				se += (sp - dp) * (sp - dp)
				sp, dp = sp+255-float64(s.A), dp+255-float64(d.A)
				se += (sp - dp) * (sp - dp)
			}
		}
	}
	mse := se / float64(6*b.Dx()*b.Dy())
	if mse == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/mse)
}

func TestAllPixelTypes(t *testing.T) {
	images := testutil.Images(97, 61)
	// One with an origin away from (0,0).
	shifted := image.NewNRGBA(image.Rect(5, 7, 5+97, 7+61))
	copy(shifted.Pix, images["nrgba"].(*image.NRGBA).Pix)
	images["nrgba-shifted"] = shifted
	names := make([]string, 0, len(images))
	for name := range images {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		m := images[name]
		for _, colors := range []int{2, 16, 256} {
			for _, noDither := range []bool{false, true} {
				var ref *image.Paletted
				for _, n := range []int{1, 7, 16, 500} {
					dir := t.TempDir()
					// A limit under which the pixels go to a temporary file.
					env := pipeline.NewEnv(4<<20, dir)
					q, err := Quantize(testutil.ChoppedEnv(m, n, env), &Options{Colors: colors, NoDither: noDither})
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
					if q.Bounds() != m.Bounds() {
						t.Fatalf("%s: bounds %v, want %v", name, q.Bounds(), m.Bounds())
					}
					got := collect(t, q, colors)
					if cm, ok := q.ColorModel().(color.Palette); !ok || !samePalette(cm, got.Palette) {
						t.Errorf("%s: ColorModel is not the palette of the strips", name)
					}
					if err := q.Close(); err != nil {
						t.Fatalf("%s: Close: %v", name, err)
					}
					if err := q.Close(); err != nil {
						t.Fatalf("%s: second Close: %v", name, err)
					}
					if u := env.Budget.Used(); u != 0 {
						t.Errorf("%s colors=%d n=%d: %d bytes still reserved", name, colors, n, u)
					}
					if k := tempFiles(t, dir); k != 0 {
						t.Errorf("%s colors=%d n=%d: %d temporary files left", name, colors, n, k)
					}
					if ref == nil {
						ref = got
						continue
					}
					if !samePalette(ref.Palette, got.Palette) || !bytes.Equal(ref.Pix, got.Pix) {
						t.Errorf("%s colors=%d dither=%v: strips of %d rows give a different result", name, colors, !noDither, n)
					}
				}
			}
		}
	}
}

func TestFewColoursExact(t *testing.T) {
	cols := []color.NRGBA{{255, 0, 0, 255}, {0, 200, 30, 255}, {10, 20, 30, 128}, {250, 250, 250, 1}, {7, 7, 7, 255}}
	m := image.NewNRGBA(image.Rect(3, 4, 83, 64))
	r := rand.New(rand.NewSource(1))
	for i := 0; i < len(m.Pix); i += 4 {
		c := cols[r.Intn(len(cols))]
		m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = c.R, c.G, c.B, c.A
	}
	for _, colors := range []int{5, 16, 256} {
		env := pipeline.NewEnv(0, t.TempDir())
		q, err := Quantize(testutil.ChoppedEnv(m, 9, env), &Options{Colors: colors})
		if err != nil {
			t.Fatal(err)
		}
		got := collect(t, q, colors)
		if len(got.Palette) != len(cols) {
			t.Errorf("colors=%d: palette of %d entries, want %d", colors, len(got.Palette), len(cols))
		}
		b := m.Bounds()
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				if g, w := got.Palette[got.ColorIndexAt(x, y)], m.NRGBAAt(x, y); g != color.Color(w) {
					t.Fatalf("colors=%d: pixel (%d,%d) is %v, want %v", colors, x, y, g, w)
				}
			}
		}
		q.Close()
		if env.Budget.Used() != 0 {
			t.Errorf("%d bytes still reserved", env.Budget.Used())
		}
	}
	// One colour more than allowed: no longer exact, but still within the limit.
	q, err := Quantize(testutil.Chopped(m, 9), &Options{Colors: 4})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, q, 4)
	q.Close()
}

func TestPalettedPassThrough(t *testing.T) {
	m := testutil.Images(40, 30)["paletted"].(*image.Paletted)
	env := pipeline.NewEnv(0, t.TempDir())
	src := testutil.ChoppedEnv(m, 7, env)
	want := testutil.Chopped(m, 7)
	q, err := Quantize(src, nil)
	if err != nil {
		t.Fatal(err)
	}
	for {
		s, err := q.Next()
		w, werr := want.Next()
		if err != werr {
			t.Fatalf("Next: %v, want %v", err, werr)
		}
		if err == io.EOF {
			break
		}
		p, ok := s.(*image.Paletted)
		wp := w.(*image.Paletted)
		if !ok || p.Rect != wp.Rect || &p.Pix[0] != &wp.Pix[0] || !samePalette(p.Palette, m.Palette) {
			t.Fatalf("strip %v was not passed through untouched", s.Bounds())
		}
	}
	if cm, ok := q.ColorModel().(color.Palette); !ok || !samePalette(cm, m.Palette) {
		t.Errorf("ColorModel is not the source palette")
	}
	q.Close()
	if env.Budget.Peak() != 0 {
		t.Errorf("pass-through reserved %d bytes", env.Budget.Peak())
	}
	// With fewer colours allowed than it has, it is reduced.
	q, err = Quantize(testutil.Chopped(m, 7), &Options{Colors: 16})
	if err != nil {
		t.Fatal(err)
	}
	collect(t, q, 16)
	q.Close()
}

// gradient is a smooth image with far more than 256 colours.
func gradient(w, h int) *image.RGBA {
	m := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			m.SetRGBA(x, y, color.RGBA{uint8(40 + x*200/w), uint8(60 + y*180/h), uint8(250 - (x+y)*200/(w+h)), 255})
		}
	}
	return m
}

func quantizeImage(t *testing.T, m image.Image, o *Options) *image.Paletted {
	t.Helper()
	q, err := Quantize(testutil.ChoppedEnv(m, 16, pipeline.NewEnv(0, t.TempDir())), o)
	if err != nil {
		t.Fatal(err)
	}
	defer q.Close()
	colors := 256
	if o != nil && o.Colors != 0 {
		colors = o.Colors
	}
	return collect(t, q, colors)
}

// blockError is the mean squared difference between the 8x8 block averages
// of the source and of the quantized image: the error that is left when
// looking from a distance, which is where banding shows and dither noise
// does not.
func blockError(src image.Image, q *image.Paletted, b image.Rectangle) float64 {
	var se float64
	n := 0
	for y := b.Min.Y; y+8 <= b.Max.Y; y += 8 {
		for x := b.Min.X; x+8 <= b.Max.X; x += 8 {
			var d [3]float64
			for j := 0; j < 8; j++ {
				for i := 0; i < 8; i++ {
					s, p := nrgbaAt(src, x+i, y+j), q.Palette[q.ColorIndexAt(x+i, y+j)].(color.NRGBA)
					d[0] += float64(s.R) - float64(p.R)
					d[1] += float64(s.G) - float64(p.G)
					d[2] += float64(s.B) - float64(p.B)
				}
			}
			for c := 0; c < 3; c++ {
				se += d[c] / 64 * d[c] / 64
			}
			n += 3
		}
	}
	return se / float64(n)
}

func TestGradientQuality(t *testing.T) {
	m := gradient(320, 240)
	dith := quantizeImage(t, m, nil)
	flat := quantizeImage(t, m, &Options{NoDither: true})
	pd, pf := psnr(m, dith), psnr(m, flat)
	t.Logf("256 colours: PSNR %.2f dB dithered, %.2f dB not", pd, pf)
	// A uniform 6x7x6 colour cube (252 colours) gets about 30.5 dB without
	// dithering on this image; an adaptive palette must do clearly better.
	if pf < 36 {
		t.Errorf("PSNR without dithering %.2f dB, want at least 36", pf)
	}
	if pd < 33 {
		t.Errorf("PSNR with dithering %.2f dB, want at least 33", pd)
	}
	if pd > pf {
		t.Errorf("dithering raised the PSNR (%.2f > %.2f): it should cost a little", pd, pf)
	}
	if bytes.Equal(dith.Pix, flat.Pix) {
		t.Errorf("dithering changed nothing")
	}
	// With few colours banding is strong, and dithering must remove nearly
	// all of it where the palette allows: in the middle of the image. (The
	// colours along its border lie outside the hull of a 16 colour palette
	// and cannot be mixed from it; there dithering only helps in part.)
	dith = quantizeImage(t, m, &Options{Colors: 16})
	flat = quantizeImage(t, m, &Options{Colors: 16, NoDither: true})
	mid := image.Rect(80, 56, 240, 184)
	bd, bf := blockError(m, dith, mid), blockError(m, flat, mid)
	ad, af := blockError(m, dith, m.Bounds()), blockError(m, flat, m.Bounds())
	t.Logf("16 colours: block error %.2f dithered, %.2f not (whole image %.2f and %.2f); PSNR %.2f and %.2f dB",
		bd, bf, ad, af, psnr(m, dith), psnr(m, flat))
	if bd > bf/20 {
		t.Errorf("block error with dithering %.2f, want under a twentieth of %.2f", bd, bf)
	}
	if ad > af/2 {
		t.Errorf("block error of the whole image with dithering %.2f, want under half of %.2f", ad, af)
	}
}

// TestCloseShades: an image of a few hundred close shades (a sky, a soft
// radial gradient) gets its palette from the colours themselves, not from
// the bins of the histogram, which are wider than the steps between them:
// it comes out nearly as it was, and dithering adds no noise to it. (With
// the bins it was 47 dB, and dithering made the file 15 times larger.)
func TestCloseShades(t *testing.T) {
	sky := image.NewRGBA(image.Rect(0, 0, 400, 1080))
	for y := 0; y < 1080; y++ {
		v := float64(y) / 1079
		for x := 0; x < 400; x++ {
			sky.SetRGBA(x, y, color.RGBA{uint8(40 + 120*v), uint8(90 + 110*v), uint8(200 + 50*v), 255})
		}
	}
	radial := image.NewRGBA(image.Rect(0, 0, 500, 500))
	for y := 0; y < 500; y++ {
		for x := 0; x < 500; x++ {
			r := min(1, math.Hypot(float64(x-250), float64(y-250))/354)
			radial.SetRGBA(x, y, color.RGBA{uint8(250 - 200*r), uint8(200 - 150*r), uint8(120 + 100*r), 255})
		}
	}
	for name, m := range map[string]*image.RGBA{"sky": sky, "radial": radial} {
		dith := quantizeImage(t, m, nil)
		flat := quantizeImage(t, m, &Options{NoDither: true})
		pd, pf := psnr(m, dith), psnr(m, flat)
		t.Logf("%s: %.1f dB dithered, %.1f dB not", name, pd, pf)
		if pd < 60 || pf < 60 {
			t.Errorf("%s: %.1f dB dithered, %.1f dB not; want at least 60", name, pd, pf)
		}
		// Dithering may change a pixel only where the palette lacks its
		// colour: few of them.
		changed := 0
		for i := range dith.Pix {
			if dith.Pix[i] != flat.Pix[i] {
				changed++
			}
		}
		if changed*20 > len(dith.Pix) {
			t.Errorf("%s: dithering changed %d of %d pixels", name, changed, len(dith.Pix))
		}
	}
}

// softShape is a colourful disc with a soft edge on a fully transparent
// background, whose hidden colour varies.
func softShape(w, h int) *image.NRGBA {
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	cx, cy, rad := float64(w)/2, float64(h)/2, float64(min(w, h))/2-4
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			a := (rad - d) / (rad / 3) // 0 outside, 1 from two thirds of the radius inwards
			a = min(1, max(0, a))
			m.SetNRGBA(x, y, color.NRGBA{uint8(x * 255 / w), uint8(y * 255 / h), uint8((x + 2*y) % 256), uint8(a*255 + 0.5)})
		}
	}
	return m
}

func TestTransparency(t *testing.T) {
	m := softShape(200, 160)
	for _, o := range []*Options{nil, {NoDither: true}, {Colors: 16}} {
		got := quantizeImage(t, m, o)
		levels := map[uint8]bool{}
		var solid, solidKept int
		for y := 0; y < 160; y++ {
			for x := 0; x < 200; x++ {
				s, c := m.NRGBAAt(x, y), got.Palette[got.ColorIndexAt(x, y)].(color.NRGBA)
				if s.A == 0 && c != (color.NRGBA{}) {
					t.Fatalf("%+v: transparent pixel (%d,%d) became %v", o, x, y, c)
				}
				if s.A == 255 {
					solid++
					if c.A == 255 {
						solidKept++
					}
				}
				levels[c.A] = true
			}
		}
		want := 12
		if o != nil && o.Colors == 16 {
			want = 3
		}
		if len(levels) < want {
			t.Errorf("%+v: %d alpha levels in use, want at least %d", o, len(levels), want)
		}
		if solidKept < solid*99/100 {
			t.Errorf("%+v: only %d of %d opaque pixels stayed opaque", o, solidKept, solid)
		}
		if got.Palette[0] != color.Color(color.NRGBA{}) {
			t.Errorf("%+v: the transparent entry is not first: %v", o, got.Palette[0])
		}
	}

	// A wholly transparent image is one transparent entry.
	got := quantizeImage(t, image.NewNRGBA(image.Rect(0, 0, 30, 20)), nil)
	if len(got.Palette) != 1 || got.Palette[0] != color.Color(color.NRGBA{}) {
		t.Errorf("transparent image: palette %v", got.Palette)
	}

	// An opaque image in a pixel type that could hold transparency gets a
	// palette without any, with and without dithering, at any size.
	opaque := image.NewNRGBA(image.Rect(0, 0, 200, 160))
	for i := 0; i < len(opaque.Pix); i += 4 {
		copy(opaque.Pix[i:], m.Pix[i:i+3])
		opaque.Pix[i+3] = 255
	}
	for _, o := range []*Options{nil, {NoDither: true}, {Colors: 2}, {Colors: 16}} {
		for _, c := range quantizeImage(t, opaque, o).Palette {
			if c.(color.NRGBA).A != 255 {
				t.Errorf("%+v: opaque image got palette entry %v", o, c)
			}
		}
	}
}

func TestEncodePNG(t *testing.T) {
	for name, m := range map[string]image.Image{"alpha": softShape(150, 90), "opaque": gradient(150, 90)} {
		for _, colors := range []int{2, 4, 16, 256} {
			want := quantizeImage(t, m, &Options{Colors: colors})
			q, err := Quantize(testutil.Chopped(m, 5), &Options{Colors: colors})
			if err != nil {
				t.Fatal(err)
			}
			var buf bytes.Buffer
			if err := pngstream.Encode(&buf, q, nil); err != nil {
				t.Fatalf("%s %d: Encode: %v", name, colors, err)
			}
			q.Close()
			if has := bytes.Contains(buf.Bytes(), []byte("tRNS")); has != (name == "alpha") {
				t.Errorf("%s %d: tRNS chunk present: %v", name, colors, has)
			}
			dec, err := png.Decode(&buf)
			if err != nil {
				t.Fatalf("%s %d: Decode: %v", name, colors, err)
			}
			p, ok := dec.(*image.Paletted)
			if !ok {
				t.Fatalf("%s %d: decoded as %T", name, colors, dec)
			}
			if len(p.Palette) != len(want.Palette) {
				t.Fatalf("%s %d: decoded palette has %d entries, want %d", name, colors, len(p.Palette), len(want.Palette))
			}
			for i := range p.Palette {
				if color.NRGBAModel.Convert(p.Palette[i]) != want.Palette[i] {
					t.Fatalf("%s %d: palette entry %d is %v, want %v", name, colors, i, p.Palette[i], want.Palette[i])
				}
			}
			for y := 0; y < 90; y++ {
				for x := 0; x < 150; x++ {
					if p.ColorIndexAt(x, y) != want.ColorIndexAt(x, y) {
						t.Fatalf("%s %d: index at (%d,%d) differs", name, colors, x, y)
					}
				}
			}
		}
	}
}

func TestSmallBudget(t *testing.T) {
	const limit = 3 << 20
	w, h := 2000, 1500
	if testing.Short() {
		w, h = 2000, 300
	}
	opaque := image.NewRGBA(image.Rect(0, 0, w, h))
	alpha := image.NewNRGBA(image.Rect(0, 0, w, h))
	r := rand.New(rand.NewSource(3))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			n := uint8(r.Intn(24))
			c := color.RGBA{uint8(x*255/w) + n, uint8(y*255/h) + n, uint8((x+y)/8) + n, 255}
			opaque.SetRGBA(x, y, c)
			alpha.SetNRGBA(x, y, color.NRGBA{c.R, c.G, c.B, uint8(x * 300 / w)})
		}
	}
	for name, m := range map[string]image.Image{"opaque": opaque, "alpha": alpha} {
		for _, noDither := range []bool{false, true} {
			dir := t.TempDir()
			env := pipeline.NewEnv(limit, dir)
			q, err := Quantize(testutil.ChoppedEnv(m, 64, env), &Options{NoDither: noDither})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := q.Next(); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if tempFiles(t, dir) != 1 {
				t.Errorf("%s: %d temporary files while running, want 1", name, tempFiles(t, dir))
			}
			rows := 0
			for {
				s, err := q.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("%s: %v", name, err)
				}
				rows += s.Bounds().Dy()
			}
			if tempFiles(t, dir) != 0 {
				t.Errorf("%s: temporary file still there after the last strip", name)
			}
			q.Close()
			if p := env.Budget.Peak(); p > limit {
				t.Errorf("%s: peak %d above the limit %d", name, p, limit)
			} else {
				t.Logf("%s dither=%v: peak %.2fMB of %dMB", name, !noDither, float64(p)/(1<<20), limit>>20)
			}
			if u := env.Budget.Used(); u != 0 {
				t.Errorf("%s: %d bytes still reserved", name, u)
			}
		}
	}

	// A budget too small even for the statistics is an error, not a panic.
	env := pipeline.NewEnv(1<<20, t.TempDir())
	q, err := Quantize(testutil.ChoppedEnv(gradient(64, 64), 8, env), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Next(); !errors.Is(err, pipeline.ErrMemoryLimit) {
		t.Errorf("1MB limit: %v, want ErrMemoryLimit", err)
	}
	q.Close()
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved", env.Budget.Used())
	}
}

// broken is a source that fails, or ends, after a number of strips.
type broken struct {
	pipeline.Source
	left   int
	err    error
	closed int
}

func (b *broken) Next() (image.Image, error) {
	if b.left == 0 {
		return nil, b.err
	}
	b.left--
	return b.Source.Next()
}

func (b *broken) Env() *pipeline.Env { return pipeline.EnvOf(b.Source) }
func (b *broken) Close() error       { b.closed++; return b.Source.Close() }

type emptySource struct{ pipeline.Source }

func (emptySource) Bounds() image.Rectangle { return image.Rect(3, 3, 3, 9) }

func TestErrors(t *testing.T) {
	m := gradient(64, 48)
	for _, colors := range []int{-1, 1, 257, 1000} {
		if _, err := Quantize(testutil.Chopped(m, 8), &Options{Colors: colors}); err == nil {
			t.Errorf("Colors=%d: no error", colors)
		}
	}
	if _, err := Quantize(emptySource{testutil.Chopped(m, 8)}, nil); err == nil {
		t.Errorf("empty image: no error")
	}

	boom := errors.New("boom")
	for _, tc := range []struct {
		name string
		left int
		err  error
		want error
	}{
		{"fails at once", 0, boom, boom},
		{"fails mid-way", 3, boom, boom},
		{"ends at once", 0, io.EOF, io.ErrUnexpectedEOF},
		{"ends early", 3, io.EOF, io.ErrUnexpectedEOF},
	} {
		dir := t.TempDir()
		env := pipeline.NewEnv(4<<20, dir)
		src := &broken{Source: testutil.ChoppedEnv(m, 8, env), left: tc.left, err: tc.err}
		q, err := Quantize(src, nil)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 2; i++ {
			if _, err := q.Next(); !errors.Is(err, tc.want) {
				t.Errorf("%s: Next: %v, want %v", tc.name, err, tc.want)
			}
		}
		if env.Budget.Used() != 0 || tempFiles(t, dir) != 0 {
			t.Errorf("%s: %d bytes and %d files left after the error", tc.name, env.Budget.Used(), tempFiles(t, dir))
		}
		q.Close()
		q.Close()
		if src.closed != 1 {
			t.Errorf("%s: source closed %d times", tc.name, src.closed)
		}
	}

	// Closing early, and reading after Close.
	dir := t.TempDir()
	env := pipeline.NewEnv(4<<20, dir)
	src := &broken{Source: testutil.ChoppedEnv(m, 8, env), left: -1}
	q, _ := Quantize(src, nil)
	if _, err := q.Next(); err != nil {
		t.Fatal(err)
	}
	if err := q.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := q.Next(); err == nil || err == io.EOF {
		t.Errorf("Next after Close: %v", err)
	}
	if env.Budget.Used() != 0 || tempFiles(t, dir) != 0 || src.closed != 1 {
		t.Errorf("after Close: %d bytes, %d files, source closed %d times", env.Budget.Used(), tempFiles(t, dir), src.closed)
	}
}

// The cell lists must give the entry a search of the whole palette gives.
func TestNearestIsExact(t *testing.T) {
	r := rand.New(rand.NewSource(5))
	for _, n := range []int{1, 2, 7, 64, 256} {
		for _, opaque := range []bool{true, false} {
			pal := make([][4]int32, n)
			for i := range pal {
				a := uint8(255)
				if !opaque {
					a = uint8(r.Intn(256))
				}
				pal[i] = coords(uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), a)
			}
			for _, limit := range []int64{0, 200 << 10} { // the second leaves no room for most cells
				b := pipeline.NewBudget(limit)
				s := newSearcher(pal, b)
				for i := 0; i < 200000; i++ {
					t0, t1, t2, t3 := r.Int31n(maxC+1), r.Int31n(maxC+1), r.Int31n(maxC+1), r.Int31n(maxA+1)
					if opaque || i%4 == 0 {
						t3 = maxA
					}
					if i%3 == 0 { // near an entry, where ties are
						p := pal[r.Intn(n)]
						t0, t1, t2 = clampTo(p[0]+r.Int31n(5)-2, 0, maxC), clampTo(p[1]+r.Int31n(5)-2, 0, maxC), p[2]
					}
					if got, want := s.nearest(t0, t1, t2, t3), s.brute(t0, t1, t2, t3); got != want {
						t.Fatalf("n=%d opaque=%v: nearest(%d,%d,%d,%d) = %d, want %d", n, opaque, t0, t1, t2, t3, got, want)
					}
				}
				s.release()
				if b.Used() != 0 {
					t.Fatalf("searcher left %d bytes reserved", b.Used())
				}
			}
		}
	}
}
