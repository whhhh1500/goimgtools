package tiffstream

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math/rand"
	"os"
	"sort"
	"testing"

	"golang.org/x/image/tiff"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// wtImages is testutil.Images plus the pixel types it leaves out.
func wtImages(w, h int) map[string]image.Image {
	ms := testutil.Images(w, h)
	rect := image.Rect(0, 0, w, h)
	rgba64, cmyk := image.NewRGBA64(rect), image.NewCMYK(rect)
	draw.Draw(rgba64, rect, ms["nrgba64"], image.Point{}, draw.Src)
	draw.Draw(cmyk, rect, ms["rgba-opaque"], image.Point{}, draw.Src)
	ms["rgba64"], ms["cmyk"] = rgba64, cmyk
	return ms
}

func wtNames(ms map[string]image.Image) []string {
	var names []string
	for name := range ms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func wtSubImage(m image.Image, r image.Rectangle) image.Image {
	return m.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(r)
}

// wtOptions pairs each option set with the reference encoder's equivalent.
var wtOptions = []struct {
	name string
	o    *Options
	ref  *tiff.Options
}{
	{"nil", nil, nil},
	{"none", &Options{Compression: Uncompressed}, &tiff.Options{Compression: tiff.Uncompressed}},
	{"none+pred", &Options{Predictor: true}, &tiff.Options{Predictor: true}},
	{"deflate", &Options{Compression: Deflate}, &tiff.Options{Compression: tiff.Deflate}},
	// x/image applies the predictor only to LZW, which it cannot write:
	// with Deflate its file is the one without (this package's predictor
	// is tested in the tests package, by reading the pixels back).
	{"deflate+pred", &Options{Compression: Deflate}, &tiff.Options{Compression: tiff.Deflate, Predictor: true}},
}

func wtRef(t *testing.T, m image.Image, o *tiff.Options) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := tiff.Encode(&buf, m, o); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func wtCheckClean(t *testing.T, what string, env *pipeline.Env) {
	t.Helper()
	if u := env.Budget.Used(); u != 0 {
		t.Errorf("%s: %d bytes still reserved", what, u)
	}
	if p := env.Budget.Peak(); p > env.Budget.Limit() {
		t.Errorf("%s: peak %d over limit %d", what, p, env.Budget.Limit())
	}
	ents, err := os.ReadDir(env.TempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Errorf("%s: %d temp files left", what, len(ents))
	}
}

// wtCompare encodes m in strips of n rows and compares with the reference.
func wtCompare(t *testing.T, what string, m image.Image, n int) {
	t.Helper()
	for _, opt := range wtOptions {
		what := fmt.Sprintf("%s/%s/%d", what, opt.name, n)
		env := pipeline.NewEnv(0, t.TempDir())
		var got bytes.Buffer
		if err := Encode(&got, testutil.ChoppedEnv(m, n, env), opt.o); err != nil {
			t.Errorf("%s: %v", what, err)
			continue
		}
		if want := wtRef(t, m, opt.ref); !bytes.Equal(got.Bytes(), want) {
			t.Errorf("%s: %d bytes differ from the reference's %d", what, got.Len(), len(want))
		}
		wtCheckClean(t, what, env)
	}
}

func TestEncodeMatchesReference(t *testing.T) {
	ms := wtImages(67, 131)
	for _, name := range wtNames(ms) {
		for _, n := range []int{1, 7, 16, 500} {
			wtCompare(t, name, ms[name], n)
		}
	}
}

func TestEncodeSubImage(t *testing.T) {
	ms := wtImages(67, 131)
	r := image.Rect(5, 9, 60, 120)
	for _, name := range wtNames(ms) {
		m := wtSubImage(ms[name], r)
		if m.Bounds() != r {
			t.Fatalf("%s: bounds %v", name, m.Bounds())
		}
		for _, n := range []int{1, 7, 500} {
			wtCompare(t, name, m, n)
		}
	}
}

func TestEncodeSmallSizes(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {2, 3}, {5, 9}, {1, 40}, {40, 1}} {
		ms := wtImages(size.X, size.Y)
		for _, name := range wtNames(ms) {
			wtCompare(t, fmt.Sprint(name, size), ms[name], 2)
		}
	}
}

// A palette shorter than 256 entries, and what was encoded reads back.
func TestEncodeRoundTrip(t *testing.T) {
	rect := image.Rect(0, 0, 40, 30)
	pal := color.Palette{color.Black, color.White, color.RGBA{200, 10, 10, 255}}
	m := image.NewPaletted(rect, pal)
	for i := range m.Pix {
		m.Pix[i] = uint8(i % 3)
	}
	wtCompare(t, "pal3", m, 7)

	for _, o := range []*Options{nil, {Compression: Deflate}} {
		var buf bytes.Buffer
		if err := Encode(&buf, testutil.Chopped(m, 7), o); err != nil {
			t.Fatal(err)
		}
		src, err := Open(bytes.NewReader(buf.Bytes()), int64(buf.Len()), nil)
		if err != nil {
			t.Fatal(err)
		}
		got, err := pipeline.Collect(src)
		src.Close()
		if err != nil {
			t.Fatal(err)
		}
		for y := 0; y < 30; y++ {
			for x := 0; x < 40; x++ {
				r0, g0, b0, a0 := m.At(x, y).RGBA()
				r1, g1, b1, a1 := got.At(x, y).RGBA()
				if r0 != r1 || g0 != g1 || b0 != b1 || a0 != a1 {
					t.Fatalf("pixel %d,%d differs after a round trip", x, y)
				}
			}
		}
	}
}

// wtSpillWatch notes whether a spill file exists while the output is written.
type wtSpillWatch struct {
	bytes.Buffer
	dir    string
	onDisk bool
}

func (s *wtSpillWatch) Write(p []byte) (int, error) {
	if ents, _ := os.ReadDir(s.dir); len(ents) > 0 {
		s.onDisk = true
	}
	return s.Buffer.Write(p)
}

func TestEncodeSmallBudget(t *testing.T) {
	const limit = 1 << 20
	rect := image.Rect(0, 0, 2000, 2000)
	rnd := rand.New(rand.NewSource(3))
	nrgba, rgba64, gray := image.NewNRGBA(rect), image.NewRGBA64(rect), image.NewGray(rect)
	rnd.Read(nrgba.Pix)
	rnd.Read(gray.Pix)
	rnd.Read(rgba64.Pix)
	ycbcr := image.NewYCbCr(rect, image.YCbCrSubsampleRatio420)
	rnd.Read(ycbcr.Y)
	rnd.Read(ycbcr.Cb)
	rnd.Read(ycbcr.Cr)
	ms := map[string]image.Image{"nrgba": nrgba, "rgba64": rgba64, "gray": gray, "ycbcr": ycbcr}
	for _, name := range wtNames(ms) {
		for _, opt := range wtOptions[2:4] {
			what := name + "/" + opt.name
			env := pipeline.NewEnv(limit, t.TempDir())
			got := &wtSpillWatch{dir: env.TempDir}
			if err := Encode(got, testutil.ChoppedEnv(ms[name], 16, env), opt.o); err != nil {
				t.Fatalf("%s: %v", what, err)
			}
			if opt.o.Compression == Deflate {
				// Larger than a strip: written in strips, which x/image
				// does not; the pixels it reads back are the image's.
				back, err := tiff.Decode(bytes.NewReader(got.Bytes()))
				if err != nil {
					t.Fatalf("%s: %v", what, err)
				}
				want, _ := tiff.Decode(bytes.NewReader(wtRef(t, ms[name], opt.ref)))
				for y := 0; y < 2000; y += 7 {
					for x := 0; x < 2000; x += 3 {
						if back.At(x, y) != want.At(x, y) {
							t.Fatalf("%s at %d,%d: %v, want %v", what, x, y, back.At(x, y), want.At(x, y))
						}
					}
				}
			} else if !bytes.Equal(got.Bytes(), wtRef(t, ms[name], opt.ref)) {
				t.Errorf("%s: differs from the reference", what)
			}
			// Random pixels do not compress, so the deflate stream cannot
			// fit in the budget; uncompressed output spools nothing.
			if deflate := opt.o.Compression == Deflate; got.onDisk != deflate {
				t.Errorf("%s: temp file used: %v", what, got.onDisk)
			}
			wtCheckClean(t, what, env)
			t.Logf("%s: budget peak %d of %d", what, env.Budget.Peak(), limit)
		}
	}
}

// wtCutSource fails with err after n strips.
type wtCutSource struct {
	pipeline.Source
	n      int
	err    error
	closed bool
}

func (c *wtCutSource) Env() *pipeline.Env { return pipeline.EnvOf(c.Source) }
func (c *wtCutSource) Close() error       { c.closed = true; return nil }
func (c *wtCutSource) Next() (image.Image, error) {
	if c.n == 0 {
		return nil, c.err
	}
	c.n--
	return c.Source.Next()
}

type wtFailWriter struct{ left int }

func (f *wtFailWriter) Write(p []byte) (int, error) {
	if f.left -= len(p); f.left < 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

type wtEmptySource struct{ r image.Rectangle }

func (e wtEmptySource) Bounds() image.Rectangle    { return e.r }
func (e wtEmptySource) ColorModel() color.Model    { return color.RGBAModel }
func (e wtEmptySource) Next() (image.Image, error) { return nil, io.EOF }
func (e wtEmptySource) Env() *pipeline.Env         { return nil }
func (e wtEmptySource) Close() error               { return nil }

// wtLyingSource reports bounds its strips do not have.
type wtLyingSource struct {
	pipeline.Source
	r image.Rectangle
}

func (l wtLyingSource) Bounds() image.Rectangle { return l.r }
func (l wtLyingSource) Env() *pipeline.Env      { return pipeline.EnvOf(l.Source) }

// wtMixSource switches to another pixel type after the first strip.
type wtMixSource struct {
	pipeline.Source
	other image.Image
	n     int
}

func (m *wtMixSource) Env() *pipeline.Env { return pipeline.EnvOf(m.Source) }
func (m *wtMixSource) Next() (image.Image, error) {
	s, err := m.Source.Next()
	if m.n++; err == nil && m.n > 1 {
		return wtSubImage(m.other, s.Bounds()), nil
	}
	return s, err
}

func TestEncodeErrors(t *testing.T) {
	ms := wtImages(67, 131)
	opts := []*Options{nil, {Compression: Deflate}}
	for _, r := range []image.Rectangle{{}, image.Rect(0, 0, 0, 5), image.Rect(3, 3, 9, 3)} {
		var buf bytes.Buffer
		if err := Encode(&buf, wtEmptySource{r}, nil); err == nil || buf.Len() != 0 {
			t.Errorf("zero-size %v: err %v, %d bytes written", r, err, buf.Len())
		}
	}
	// The reference encoder cannot write these either (LZW this package
	// can).
	for _, c := range []CompressionType{CCITTGroup3, CCITTGroup4} {
		env := pipeline.NewEnv(0, t.TempDir())
		var buf bytes.Buffer
		if err := Encode(&buf, testutil.ChoppedEnv(ms["gray"], 7, env), &Options{Compression: c}); err == nil || buf.Len() != 0 {
			t.Errorf("compression %d: err %v, %d bytes written", c, err, buf.Len())
		}
		wtCheckClean(t, "unsupported", env)
	}
	boom := errors.New("boom")
	for _, name := range []string{"gray", "gray16", "nrgba", "ycbcr420"} {
		for _, o := range opts {
			for _, c := range []struct {
				n         int
				err, want error
			}{
				{0, io.EOF, io.ErrUnexpectedEOF},
				{5, io.EOF, io.ErrUnexpectedEOF},
				{0, boom, boom},
				{5, boom, boom},
			} {
				env := pipeline.NewEnv(0, t.TempDir())
				src := &wtCutSource{Source: testutil.ChoppedEnv(ms[name], 7, env), n: c.n, err: c.err}
				if err := Encode(io.Discard, src, o); !errors.Is(err, c.want) {
					t.Errorf("%s cut at %d with %v: got %v", name, c.n, c.err, err)
				}
				if src.closed {
					t.Errorf("%s: Encode closed the source", name)
				}
				wtCheckClean(t, name, env)
			}
			// The writer failing at any point.
			for _, left := range []int{0, 6, 20, 2000} {
				env := pipeline.NewEnv(0, t.TempDir())
				if err := Encode(&wtFailWriter{left}, testutil.ChoppedEnv(ms[name], 7, env), o); err == nil {
					t.Errorf("%s: no error from a writer failing after %d bytes", name, left)
				}
				wtCheckClean(t, name, env)
			}
		}
	}

	// Strips that break the Source contract are refused, not indexed.
	for _, o := range opts {
		env := pipeline.NewEnv(0, t.TempDir())
		full := ms["nrgba"]
		narrow := testutil.ChoppedEnv(wtSubImage(full, image.Rect(0, 0, 50, 131)), 7, env)
		if err := Encode(io.Discard, wtLyingSource{narrow, full.Bounds()}, o); err == nil {
			t.Error("narrow strips accepted")
		}
		mixed := &wtMixSource{Source: testutil.ChoppedEnv(full, 7, env), other: ms["rgba"]}
		if err := Encode(io.Discard, mixed, o); err == nil {
			t.Error("a change of pixel type accepted")
		}
		wtCheckClean(t, "contract", env)
	}
}
