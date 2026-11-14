package gifstream

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"io"
	"math/rand"
	"sort"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// stdEncode is the reference: gif.Encode with the default quantizer and
// drawer. numColors 0 is our spelling of the default, 256.
func stdEncode(t testing.TB, m image.Image, numColors int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := gif.Encode(&buf, m, &gif.Options{NumColors: numColors}); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// checkEncode encodes m in strips of n rows and requires the reference
// bytes and a clean budget.
func checkEncode(t *testing.T, name string, m image.Image, n, numColors int, want []byte) {
	t.Helper()
	env := pipeline.NewEnv(0, t.TempDir())
	var got bytes.Buffer
	var o *Options
	if numColors != 0 {
		o = &Options{NumColors: numColors}
	}
	if err := Encode(&got, testutil.ChoppedEnv(m, n, env), o); err != nil {
		t.Errorf("%s strips=%d colors=%d: %v", name, n, numColors, err)
		return
	}
	if !bytes.Equal(got.Bytes(), want) {
		t.Errorf("%s strips=%d colors=%d: %d bytes differ from the %d of gif.Encode",
			name, n, numColors, got.Len(), len(want))
	}
	if env.Budget.Used() != 0 {
		t.Errorf("%s strips=%d colors=%d: %d bytes still reserved", name, n, numColors, env.Budget.Used())
	}
}

func sortedNames(imgs map[string]image.Image) []string {
	var names []string
	for name := range imgs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// TestEncodeMatchesStdlib requires the bytes of gif.Encode for every pixel
// type, strip height and palette size.
func TestEncodeMatchesStdlib(t *testing.T) {
	imgs := testutil.Images(67, 131)
	// Paletted images that are written as they are: few colors, a
	// transparent entry, a palette that is not a power of two.
	imgs["paletted-16"] = testImage(image.Rect(0, 0, 67, 131), testPalette(16, false, 3), 1)
	imgs["paletted-2"] = testImage(image.Rect(0, 0, 67, 131), testPalette(2, false, 4), 2)
	imgs["paletted-1"] = testImage(image.Rect(0, 0, 67, 131), testPalette(1, false, 4), 2)
	imgs["paletted-37-transparent"] = testImage(image.Rect(0, 0, 67, 131), testPalette(37, true, 5), 3)
	imgs["paletted-256-opaque"] = testImage(image.Rect(0, 0, 67, 131), testPalette(256, false, 6), 4)
	for _, name := range sortedNames(imgs) {
		m := imgs[name]
		for _, numColors := range []int{0, 2, 16, 256} {
			want := stdEncode(t, m, numColors)
			for _, n := range []int{1, 7, 16, 500} {
				checkEncode(t, name, m, n, numColors, want)
			}
		}
	}
}

type subImager interface {
	SubImage(image.Rectangle) image.Image
}

// TestEncodeSubImage covers sources whose bounds do not start at (0, 0)
// and whose strips have a stride wider than the image.
func TestEncodeSubImage(t *testing.T) {
	r := image.Rect(13, 21, 60, 117)
	for _, name := range []string{"nrgba", "rgba", "paletted", "gray", "ycbcr420", "ycbcr444", "nrgba64"} {
		m := testutil.Images(67, 131)[name].(subImager).SubImage(r)
		if m.Bounds() != r {
			t.Fatalf("%s: bounds %v", name, m.Bounds())
		}
		for _, numColors := range []int{0, 16} {
			want := stdEncode(t, m, numColors)
			for _, n := range []int{1, 7, 500} {
				checkEncode(t, "sub-"+name, m, n, numColors, want)
			}
		}
	}
	small := testImage(image.Rect(0, 0, 67, 131), testPalette(16, true, 3), 1).SubImage(r)
	want := stdEncode(t, small, 0)
	for _, n := range []int{1, 7, 500} {
		checkEncode(t, "sub-paletted-16", small, n, 0, want)
	}
}

func TestEncodeWide(t *testing.T) {
	rnd := rand.New(rand.NewSource(11))
	rgba := image.NewRGBA(image.Rect(0, 0, 2000, 40))
	for i := range rgba.Pix {
		rgba.Pix[i] = uint8(i/97) + uint8(rnd.Intn(50))
		if i%4 == 3 {
			rgba.Pix[i] = 0xff
		}
	}
	want := stdEncode(t, rgba, 0)
	for _, n := range []int{1, 16, 500} {
		checkEncode(t, "wide-rgba", rgba, n, 0, want)
	}
	pm := testImage(image.Rect(0, 0, 2000, 40), testPalette(256, true, 9), 5)
	want = stdEncode(t, pm, 0)
	for _, n := range []int{1, 16, 500} {
		checkEncode(t, "wide-paletted", pm, n, 0, want)
	}
}

// hidden hides an image's concrete type, leaving At, Bounds and ColorModel.
type hidden struct{ image.Image }

func (h hidden) SubImage(r image.Rectangle) image.Image {
	return hidden{h.Image.(subImager).SubImage(r)}
}

// TestEncodeOtherTypes covers gif.Encode's two remaining paths: an image
// of an unknown type, and one that is not an *image.Paletted but whose
// color model is a palette.
func TestEncodeOtherTypes(t *testing.T) {
	imgs := testutil.Images(67, 131)
	cases := map[string]image.Image{
		"hidden-nrgba":       hidden{imgs["nrgba"]},
		"hidden-paletted":    hidden{imgs["paletted"]},
		"hidden-paletted-16": hidden{testImage(image.Rect(0, 0, 67, 131), testPalette(16, true, 3), 1)},
	}
	for _, name := range sortedNames(cases) {
		m := cases[name]
		for _, numColors := range []int{0, 16, 5} {
			want := stdEncode(t, m, numColors)
			for _, n := range []int{1, 7, 500} {
				checkEncode(t, name, m, n, numColors, want)
			}
		}
	}
}

// TestEncodeBigPalette: a palette of more than 256 entries cannot be
// written; gif.Encode dithers such an image, and so do we.
func TestEncodeBigPalette(t *testing.T) {
	pal := make(color.Palette, 300)
	for i := range pal {
		pal[i] = color.RGBA{uint8(i), uint8(i * 3), uint8(i / 2), 0xff}
	}
	m := testImage(image.Rect(0, 0, 40, 30), pal[:200], 1)
	m.Palette = pal
	checkEncode(t, "palette-300", m, 7, 0, stdEncode(t, m, 0))
}

// fakeSource yields the given strips, then err (io.EOF when nil).
type fakeSource struct {
	bounds image.Rectangle
	model  color.Model
	strips []image.Image
	err    error
	env    *pipeline.Env
}

func (f *fakeSource) Env() *pipeline.Env      { return f.env }
func (f *fakeSource) Bounds() image.Rectangle { return f.bounds }
func (f *fakeSource) ColorModel() color.Model { return f.model }
func (f *fakeSource) Close() error            { return nil }
func (f *fakeSource) Next() (image.Image, error) {
	if len(f.strips) == 0 {
		if f.err != nil {
			return nil, f.err
		}
		return nil, io.EOF
	}
	s := f.strips[0]
	f.strips = f.strips[1:]
	return s, nil
}

type failWriter struct{ n int }

var errWrite = errors.New("write failed")

func (w *failWriter) Write(p []byte) (int, error) {
	if w.n -= len(p); w.n < 0 {
		return 0, errWrite
	}
	return len(p), nil
}

func TestEncodeErrors(t *testing.T) {
	env := pipeline.NewEnv(0, t.TempDir())
	rgba := image.NewRGBA(image.Rect(0, 0, 20, 10))
	top := rgba.SubImage(image.Rect(0, 0, 20, 4))
	rest := rgba.SubImage(image.Rect(0, 4, 20, 10))
	pal := testPalette(16, false, 1)
	pm := testImage(image.Rect(0, 0, 20, 10), pal, 1)
	pmTop := pm.SubImage(image.Rect(0, 0, 20, 4))
	pmRest := pm.SubImage(image.Rect(0, 4, 20, 10)).(*image.Paletted)
	otherPal := *pmRest
	otherPal.Palette = testPalette(16, false, 2)
	longerPal := *pmRest
	longerPal.Palette = testPalette(32, false, 1)
	big := testImage(image.Rect(0, 0, 20, 10), testPalette(200, false, 1), 1) // dithered at 16 colors
	bigTop := big.SubImage(image.Rect(0, 0, 20, 4))
	bigRest := *big.SubImage(image.Rect(0, 4, 20, 10)).(*image.Paletted)
	bigRest.Palette = testPalette(200, false, 2)
	outside := testImage(image.Rect(0, 0, 20, 10), testPalette(200, false, 1), 1)
	outside.Palette = outside.Palette[:100]
	nilEntry := testImage(image.Rect(0, 0, 20, 10), testPalette(16, false, 1), 1)
	nilEntry.Palette[3] = nil
	nilEntryBig := testImage(image.Rect(0, 0, 20, 10), testPalette(200, false, 1), 1)
	nilEntryBig.Palette[3] = nil
	empty := image.NewPaletted(image.Rect(0, 0, 20, 10), nil)
	short := *pm
	short.Pix = short.Pix[:50]
	errSrc := errors.New("source failed")

	cases := []struct {
		name string
		src  *fakeSource
		o    *Options
		want error // nil: any error
	}{
		{"zero width", &fakeSource{bounds: image.Rect(5, 5, 5, 9)}, nil, nil},
		{"zero height", &fakeSource{bounds: image.Rect(0, 0, 9, 0)}, nil, nil},
		{"too wide", &fakeSource{bounds: image.Rect(0, 0, 65536, 1)}, nil, nil},
		{"too tall", &fakeSource{bounds: image.Rect(0, 0, 1, 70000)}, nil, nil},
		{"negative colors", &fakeSource{bounds: rgba.Rect, strips: []image.Image{rgba}}, &Options{NumColors: -1}, nil},
		{"too many colors", &fakeSource{bounds: rgba.Rect, strips: []image.Image{rgba}}, &Options{NumColors: 257}, nil},
		{"no strips", &fakeSource{bounds: rgba.Rect}, nil, io.ErrUnexpectedEOF},
		{"ends early", &fakeSource{bounds: rgba.Rect, strips: []image.Image{top}}, nil, io.ErrUnexpectedEOF},
		{"ends early paletted", &fakeSource{bounds: pm.Rect, strips: []image.Image{pmTop}}, nil, io.ErrUnexpectedEOF},
		{"source fails first", &fakeSource{bounds: rgba.Rect, err: errSrc}, nil, errSrc},
		{"source fails later", &fakeSource{bounds: rgba.Rect, strips: []image.Image{top}, err: errSrc}, nil, errSrc},
		{"gap", &fakeSource{bounds: rgba.Rect, strips: []image.Image{rest}}, nil, nil},
		{"strip repeated", &fakeSource{bounds: rgba.Rect, strips: []image.Image{top, top}}, nil, nil},
		{"strip too wide", &fakeSource{bounds: image.Rect(0, 0, 10, 10), strips: []image.Image{rgba}}, nil, nil},
		{"strip too tall", &fakeSource{bounds: image.Rect(0, 0, 20, 5), strips: []image.Image{rgba}}, nil, nil},
		{"nil strip", &fakeSource{bounds: rgba.Rect, strips: []image.Image{nil}}, nil, nil},
		{"palette changes", &fakeSource{bounds: pm.Rect, strips: []image.Image{pmTop, &otherPal}}, nil, errPaletteChanged},
		{"palette grows", &fakeSource{bounds: pm.Rect, strips: []image.Image{pmTop, &longerPal}}, nil, errPaletteChanged},
		{"palette changes while dithering", &fakeSource{bounds: big.Rect, strips: []image.Image{bigTop, &bigRest}}, &Options{NumColors: 16}, errPaletteChanged},
		{"type changes", &fakeSource{bounds: pm.Rect, strips: []image.Image{pmTop, rest}}, nil, nil},
		{"pixel outside palette", &fakeSource{bounds: outside.Rect, strips: []image.Image{outside}}, &Options{NumColors: 16}, nil},
		{"pixel outside color table", &fakeSource{bounds: pm.Rect, strips: []image.Image{func() image.Image {
			m := testImage(image.Rect(0, 0, 20, 10), testPalette(16, false, 1), 1)
			m.Pix[77] = 200
			return m
		}()}}, nil, nil},
		{"nil palette entry", &fakeSource{bounds: nilEntry.Rect, strips: []image.Image{nilEntry}}, nil, nil},
		{"nil palette entry while dithering", &fakeSource{bounds: nilEntryBig.Rect, strips: []image.Image{nilEntryBig}}, &Options{NumColors: 16}, nil},
		{"nil entry in color model", &fakeSource{bounds: rgba.Rect, model: nilEntry.Palette, strips: []image.Image{rgba}}, nil, nil},
		{"empty palette", &fakeSource{bounds: empty.Rect, strips: []image.Image{empty}}, nil, nil},
		{"empty color model", &fakeSource{bounds: rgba.Rect, model: color.Palette{}, strips: []image.Image{rgba}}, nil, nil},
		{"short pix", &fakeSource{bounds: pm.Rect, strips: []image.Image{&short}}, nil, nil},
	}
	for _, c := range cases {
		c.src.env = env
		var buf bytes.Buffer
		err := func() (err error) {
			defer func() {
				if r := recover(); r != nil {
					err = nil
					t.Errorf("%s: panic: %v", c.name, r)
				}
			}()
			return Encode(&buf, c.src, c.o)
		}()
		if err == nil {
			t.Errorf("%s: no error", c.name)
		} else if c.want != nil && !errors.Is(err, c.want) {
			t.Errorf("%s: error %v, want %v", c.name, err, c.want)
		}
		if env.Budget.Used() != 0 {
			t.Fatalf("%s: %d bytes still reserved", c.name, env.Budget.Used())
		}
	}

	// A writer that fails, at every point of the output.
	m := testutil.Images(67, 131)["nrgba"]
	size := len(stdEncode(t, m, 0))
	for _, n := range []int{0, 5, 100, 5000, size - 1} {
		err := Encode(&failWriter{n: n}, testutil.ChoppedEnv(m, 7, env), nil)
		if !errors.Is(err, errWrite) {
			t.Errorf("writer failing after %d bytes: %v", n, err)
		}
	}
	if err := Encode(&failWriter{n: size}, testutil.ChoppedEnv(m, 7, env), nil); err != nil {
		t.Errorf("writer with room for everything: %v", err)
	}
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved", env.Budget.Used())
	}
}

// TestEncodeMemory: dithering reserves two error rows and an index row,
// and a paletted image nothing at all; neither depends on the height.
func TestEncodeMemory(t *testing.T) {
	const w, h = 300, 2000
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := range rgba.Pix {
		rgba.Pix[i] = uint8(i / 7)
	}
	env := pipeline.NewEnv(0, t.TempDir())
	if err := Encode(io.Discard, testutil.ChoppedEnv(rgba, 16, env), nil); err != nil {
		t.Fatal(err)
	}
	if want := int64(2*(w+2)*16 + w); env.Budget.Peak() != want {
		t.Errorf("dithering reserved %d bytes at its peak, want %d", env.Budget.Peak(), want)
	}

	env = pipeline.NewEnv(0, t.TempDir())
	pm := testImage(image.Rect(0, 0, w, h), testPalette(256, false, 1), 1)
	if err := Encode(io.Discard, testutil.ChoppedEnv(pm, 16, env), nil); err != nil {
		t.Fatal(err)
	}
	if env.Budget.Peak() != 0 {
		t.Errorf("paletted image reserved %d bytes", env.Budget.Peak())
	}

	// A budget too small for the rows is an error, not an allocation.
	env = pipeline.NewEnv(4000, t.TempDir())
	err := Encode(io.Discard, testutil.ChoppedEnv(rgba, 16, env), nil)
	if !errors.Is(err, pipeline.ErrMemoryLimit) {
		t.Errorf("budget of 4000 bytes: %v", err)
	}
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved", env.Budget.Used())
	}
}

// TestEncodeDoesNotClose: the source stays open.
func TestEncodeDoesNotClose(t *testing.T) {
	m := testutil.Images(20, 10)["rgba"]
	src := &closeCounter{Source: testutil.Chopped(m, 3)}
	if err := Encode(io.Discard, src, nil); err != nil {
		t.Fatal(err)
	}
	if src.closed != 0 {
		t.Errorf("Encode closed the source")
	}
}

type closeCounter struct {
	pipeline.Source
	closed int
}

func (c *closeCounter) Close() error {
	c.closed++
	return c.Source.Close()
}

func BenchmarkEncodeDither(b *testing.B) {
	m := testutil.Images(400, 300)["nrgba"]
	for _, name := range []string{"stream", "stdlib"} {
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var err error
				if name == "stream" {
					err = Encode(io.Discard, testutil.Chopped(m, 16), nil)
				} else {
					err = gif.Encode(io.Discard, m, nil)
				}
				if err != nil {
					b.Fatal(fmt.Sprint(err))
				}
			}
		})
	}
}
