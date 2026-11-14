package bmpstream

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math/rand"
	"os"
	"sort"
	"testing"

	"golang.org/x/image/bmp"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// encImages is testutil.Images plus the pixel types it leaves out.
func encImages(w, h int) map[string]image.Image {
	ms := testutil.Images(w, h)
	rect := image.Rect(0, 0, w, h)
	rgba64, cmyk, nrgbaOpq := image.NewRGBA64(rect), image.NewCMYK(rect), image.NewNRGBA(rect)
	draw.Draw(rgba64, rect, ms["nrgba64"], image.Point{}, draw.Src)
	draw.Draw(cmyk, rect, ms["rgba-opaque"], image.Point{}, draw.Src)
	draw.Draw(nrgbaOpq, rect, ms["rgba-opaque"], image.Point{}, draw.Src)
	ms["rgba64"], ms["cmyk"], ms["nrgba-opaque"] = rgba64, cmyk, nrgbaOpq
	return ms
}

func encNames(ms map[string]image.Image) []string {
	var names []string
	for name := range ms {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func subImage(m image.Image, r image.Rectangle) image.Image {
	return m.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(r)
}

// opaqueSource says its pixels are opaque, as a decoder of an alpha-less
// format does.
type opaqueSource struct{ pipeline.Source }

func (o opaqueSource) Opaque() bool       { return true }
func (o opaqueSource) Env() *pipeline.Env { return pipeline.EnvOf(o.Source) }

// cutSource fails with err after n strips.
type cutSource struct {
	pipeline.Source
	n      int
	err    error
	closed bool
}

func (c *cutSource) Env() *pipeline.Env { return pipeline.EnvOf(c.Source) }
func (c *cutSource) Close() error       { c.closed = true; return nil }
func (c *cutSource) Next() (image.Image, error) {
	if c.n == 0 {
		return nil, c.err
	}
	c.n--
	return c.Source.Next()
}

func refBMP(t *testing.T, m image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := bmp.Encode(&buf, m); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func checkClean(t *testing.T, what string, env *pipeline.Env) {
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

func TestEncodeMatchesReference(t *testing.T) {
	ms := encImages(67, 131)
	for _, name := range encNames(ms) {
		m := ms[name]
		want := refBMP(t, m)
		for _, n := range []int{1, 7, 16, 500} {
			env := pipeline.NewEnv(0, t.TempDir())
			var got bytes.Buffer
			if err := Encode(&got, testutil.ChoppedEnv(m, n, env)); err != nil {
				t.Errorf("%s/%d: %v", name, n, err)
				continue
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("%s/%d: %d bytes differ from the reference's %d", name, n, got.Len(), len(want))
			}
			checkClean(t, name, env)
		}
	}
}

// A source that knows it is opaque skips the alpha scan and spools 24-bit
// rows; the result must be what the scan would have produced.
func TestEncodeKnownOpaque(t *testing.T) {
	ms := encImages(67, 131)
	for _, name := range []string{"rgba-opaque", "nrgba-opaque", "gray", "paletted", "ycbcr420"} {
		m := ms[name]
		want := refBMP(t, m)
		for _, n := range []int{1, 7, 500} {
			env := pipeline.NewEnv(0, t.TempDir())
			var got bytes.Buffer
			if err := Encode(&got, opaqueSource{testutil.ChoppedEnv(m, n, env)}); err != nil {
				t.Fatalf("%s/%d: %v", name, n, err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("%s/%d: differs from the reference", name, n)
			}
			checkClean(t, name, env)
		}
	}
}

func TestEncodeSubImage(t *testing.T) {
	ms := encImages(67, 131)
	r := image.Rect(5, 9, 60, 120)
	for _, name := range encNames(ms) {
		m := subImage(ms[name], r)
		if m.Bounds() != r {
			t.Fatalf("%s: bounds %v", name, m.Bounds())
		}
		want := refBMP(t, m)
		for _, n := range []int{1, 7, 500} {
			env := pipeline.NewEnv(0, t.TempDir())
			var got bytes.Buffer
			if err := Encode(&got, testutil.ChoppedEnv(m, n, env)); err != nil {
				t.Errorf("%s/%d: %v", name, n, err)
				continue
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Errorf("%s/%d: differs from the reference", name, n)
			}
			checkClean(t, name, env)
		}
	}
}

// Widths that need 0 to 3 padding bytes, and single rows and columns.
func TestEncodeSmallSizes(t *testing.T) {
	for _, size := range []image.Point{{1, 1}, {2, 3}, {3, 2}, {4, 1}, {5, 9}, {1, 40}, {40, 1}} {
		ms := encImages(size.X, size.Y)
		for _, name := range encNames(ms) {
			env := pipeline.NewEnv(0, t.TempDir())
			var got bytes.Buffer
			if err := Encode(&got, testutil.ChoppedEnv(ms[name], 2, env)); err != nil {
				t.Errorf("%s %v: %v", name, size, err)
				continue
			}
			if !bytes.Equal(got.Bytes(), refBMP(t, ms[name])) {
				t.Errorf("%s %v: differs from the reference", name, size)
			}
		}
	}
}

// spillWatch notes whether a spill file exists while the output is written.
type spillWatch struct {
	bytes.Buffer
	dir    string
	onDisk bool
}

func (s *spillWatch) Write(p []byte) (int, error) {
	if ents, _ := os.ReadDir(s.dir); len(ents) > 0 {
		s.onDisk = true
	}
	return s.Buffer.Write(p)
}

func TestEncodeSmallBudget(t *testing.T) {
	const limit = 1 << 20
	rect := image.Rect(0, 0, 2000, 2000)
	rnd := rand.New(rand.NewSource(3))
	nrgba, opq, gray := image.NewNRGBA(rect), image.NewRGBA(rect), image.NewGray(rect)
	rnd.Read(nrgba.Pix)
	rnd.Read(gray.Pix)
	rnd.Read(opq.Pix)
	for i := 3; i < len(opq.Pix); i += 4 {
		opq.Pix[i] = 0xff
	}
	ycbcr := image.NewYCbCr(rect, image.YCbCrSubsampleRatio420)
	rnd.Read(ycbcr.Y)
	rnd.Read(ycbcr.Cb)
	rnd.Read(ycbcr.Cr)
	for name, m := range map[string]image.Image{"nrgba": nrgba, "rgba-opaque": opq, "gray": gray, "ycbcr": ycbcr} {
		env := pipeline.NewEnv(limit, t.TempDir())
		got := &spillWatch{dir: env.TempDir}
		if err := Encode(got, testutil.ChoppedEnv(m, 16, env)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if !bytes.Equal(got.Bytes(), refBMP(t, m)) {
			t.Errorf("%s: differs from the reference", name)
		}
		if !got.onDisk {
			t.Errorf("%s: the rows did not go to a temp file", name)
		}
		checkClean(t, name, env)
		t.Logf("%s: budget peak %d of %d", name, env.Budget.Peak(), limit)
	}
}

type failWriter struct{ left int }

func (f *failWriter) Write(p []byte) (int, error) {
	if f.left -= len(p); f.left < 0 {
		return 0, errors.New("disk full")
	}
	return len(p), nil
}

type emptySource struct{ r image.Rectangle }

func (e emptySource) Bounds() image.Rectangle    { return e.r }
func (e emptySource) ColorModel() color.Model    { return color.RGBAModel }
func (e emptySource) Next() (image.Image, error) { return nil, io.EOF }
func (e emptySource) Env() *pipeline.Env         { return nil }
func (e emptySource) Close() error               { return nil }

func TestEncodeErrors(t *testing.T) {
	ms := encImages(67, 131)
	for _, r := range []image.Rectangle{{}, image.Rect(0, 0, 0, 5), image.Rect(3, 3, 9, 3)} {
		var buf bytes.Buffer
		if err := Encode(&buf, emptySource{r}); err == nil || buf.Len() != 0 {
			t.Errorf("zero-size %v: err %v, %d bytes written", r, err, buf.Len())
		}
	}
	boom := errors.New("boom")
	for _, name := range []string{"gray", "nrgba", "rgba-opaque", "ycbcr420"} {
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
			src := &cutSource{Source: testutil.ChoppedEnv(ms[name], 7, env), n: c.n, err: c.err}
			if err := Encode(io.Discard, src); !errors.Is(err, c.want) {
				t.Errorf("%s cut at %d with %v: got %v", name, c.n, c.err, err)
			}
			if src.closed {
				t.Errorf("%s: Encode closed the source", name)
			}
			checkClean(t, name, env)
		}
		// The writer failing at any point.
		for _, left := range []int{0, 20, 60, 2000} {
			env := pipeline.NewEnv(0, t.TempDir())
			if err := Encode(&failWriter{left}, testutil.ChoppedEnv(ms[name], 7, env)); err == nil {
				t.Errorf("%s: no error from a writer failing after %d bytes", name, left)
			}
			checkClean(t, name, env)
		}
	}

	// Strips that break the Source contract are refused, not indexed.
	env := pipeline.NewEnv(0, t.TempDir())
	full := ms["nrgba"]
	wrongBounds := testutil.ChoppedEnv(subImage(full, image.Rect(0, 0, 50, 131)), 7, env)
	if err := Encode(io.Discard, lyingSource{wrongBounds, full.Bounds()}); err == nil {
		t.Error("narrow strips accepted")
	}
	mixed := &mixSource{Source: testutil.ChoppedEnv(full, 7, env), other: ms["rgba"]}
	if err := Encode(io.Discard, mixed); err == nil {
		t.Error("a change of pixel type accepted")
	}
	checkClean(t, "contract", env)
}

// lyingSource reports bounds its strips do not have.
type lyingSource struct {
	pipeline.Source
	r image.Rectangle
}

func (l lyingSource) Bounds() image.Rectangle { return l.r }
func (l lyingSource) Env() *pipeline.Env      { return pipeline.EnvOf(l.Source) }

// mixSource switches to another pixel type after the first strip.
type mixSource struct {
	pipeline.Source
	other image.Image
	n     int
}

func (m *mixSource) Env() *pipeline.Env { return pipeline.EnvOf(m.Source) }
func (m *mixSource) Next() (image.Image, error) {
	s, err := m.Source.Next()
	if m.n++; err == nil && m.n > 1 {
		return subImage(m.other, s.Bounds()), nil
	}
	return s, err
}
