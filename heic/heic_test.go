package heic_test

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/heic"
	"github.com/whhhh1500/goimgtools/inspect"
)

func toPNG(t *testing.T, m *goimgtools.Image) *image.NRGBA {
	t.Helper()
	b, err := m.PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	p, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	n := image.NewNRGBA(p.Bounds())
	draw.Draw(n, n.Rect, p, p.Bounds().Min, draw.Src)
	return n
}

func psnr(a, b *image.NRGBA) float64 {
	var se float64
	for i := range a.Pix {
		d := float64(a.Pix[i]) - float64(b.Pix[i])
		se += d * d
	}
	if se == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/(se/float64(len(a.Pix))))
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestDecode: HEIC files of every kind decode (sums of what gen2brain/h265
// gives, whose HEVC decoding is that of the conformance suite: a regression
// check; a second decoder, the heic crate compiled to WebAssembly, agrees
// on 4:2:0 files within its conversion of colour and fails on the 4:4:4
// one): 4:2:0, 4:4:4, transparency, a grid of tiles with transparency, and
// one turned upright by irot whose EXIF orientation is then 1, so that
// Rotate leaves it. The files are from gen2brain/h265 and gen2brain/heic
// (MIT licence).
func TestDecode(t *testing.T) {
	for _, c := range []struct {
		name  string
		w, h  int
		alpha bool
		sum   string
	}{
		{"alpha.heic", 64, 64, true, "4fb8ae5b77c562aa"},
		{"basic.heic", 320, 240, false, "e84abe406c7ac4d8"},
		{"chroma444.heic", 320, 240, false, "295efa275cc41885"},
		{"grid_alpha.heic", 1100, 700, true, "5a0c0e3f7f3f03cd"},
		{"test_exif.heic", 480, 640, false, "bfec4c500d7df317"},
	} {
		in, err := os.ReadFile("testdata/" + c.name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := goimgtools.FromBytes(in).Metadata()
		if err != nil || info.Format != "heic" || info.Width != c.w || info.Height != c.h || info.HasAlpha != c.alpha || info.Orientation > 1 {
			t.Errorf("%s: %+v, %v", c.name, info, err)
		}
		for _, threads := range []int{1, 4} {
			m := toPNG(t, goimgtools.FromBytes(in).Threads(threads))
			if got := fmt.Sprintf("%x", sha256.Sum256(m.Pix))[:16]; got != c.sum || m.Rect.Dx() != c.w || m.Rect.Dy() != c.h {
				t.Errorf("%s, %d threads: %v %s, want %dx%d %s", c.name, threads, m.Rect.Size(), got, c.w, c.h, c.sum)
			}
		}
	}
	in := must(os.ReadFile("testdata/test_exif.heic"))
	x, ok, err := inspect.ReadExif(goimgtools.FromBytes(in))
	if err != nil || !ok || x.Make != "TestCam" || x.Orientation != 1 {
		t.Errorf("EXIF: %+v, %v, %v", x, ok, err)
	}
	if m := toPNG(t, goimgtools.FromBytes(in).Rotate()); m.Rect.Dx() != 480 {
		t.Errorf("Rotate turned an upright image: %v", m.Rect.Size())
	}
	if _, err := goimgtools.FromBytes([]byte("\x00\x00\x00\x18ftypheic\x00\x00\x00\x00broken")).PNG().ToBytes(); err == nil {
		t.Error("a broken HEIC decoded")
	}
}

// TestMatch: the brands of HEVC images, as the major one or among the
// compatible ones of a general HEIF file; not AVIF.
func TestMatch(t *testing.T) {
	head := func(major string, compat ...string) []byte {
		b := []byte("\x00\x00\x00\x20ftyp" + major + "\x00\x00\x00\x00")
		for _, c := range compat {
			b = append(b, c...)
		}
		return b
	}
	for _, c := range []struct {
		h    []byte
		want bool
	}{
		{head("heic"), true}, {head("heix"), true}, {head("mif1", "mif1", "heic"), true},
		{head("mif1", "mif1", "avif"), false}, {head("avif", "mif1"), false}, {[]byte("\x00\x00\x00\x20ftypisom"), false},
	} {
		if heic.Match(c.h) != c.want {
			t.Errorf("Match(%q) = %v", c.h, !c.want)
		}
	}
}

// TestEncode: written as HEIC and read back: Lossless within a level (the
// samples are YCbCr, coded 4:4:4 without loss); with loss close, smaller
// than PNG, smaller at a lower quality; the EXIF block kept with KeepExif;
// MaxFileSize; .heic by the file name; transparency kept.
func TestEncode(t *testing.T) {
	photo := must(os.ReadFile("../tests/testdata/pillow/source.png"))
	src := toPNG(t, goimgtools.FromBytes(photo))
	got := toPNG(t, goimgtools.FromBytes(must(goimgtools.FromBytes(photo).ToFormat("heic", heic.Options{Lossless: true}).ToBytes())))
	for i := range src.Pix {
		if d := int(got.Pix[i]) - int(src.Pix[i]); d < -1 || d > 1 {
			t.Fatalf("lossless: value %d is %d, was %d", i, got.Pix[i], src.Pix[i])
		}
	}
	var last int
	for _, q := range []int{90, 60, 30} {
		b := must(goimgtools.FromBytes(photo).ToFormat("heic", heic.Options{Quality: q, Effort: 7}).ToBytes())
		got := toPNG(t, goimgtools.FromBytes(b))
		if p := psnr(got, src); p < 26 || len(b) >= len(photo) || (last > 0 && len(b) >= last) {
			t.Errorf("quality %d: %d bytes (PNG %d, quality before %d), PSNR %.1f", q, len(b), len(photo), last, p)
		}
		last = len(b)
	}
	jpg := must(os.ReadFile("../tests/testdata/exif-pillow.jpg"))
	want, _, _ := inspect.ReadExif(goimgtools.FromBytes(jpg))
	kept, ok, err := inspect.ReadExif(goimgtools.FromBytes(must(goimgtools.FromBytes(jpg).KeepExif().ToFormat("heic", nil).ToBytes())))
	if err != nil || !ok || kept.Make != want.Make || kept.Model != want.Model {
		t.Errorf("EXIF kept: %+v, %v, %v", kept, ok, err)
	}
	dir := t.TempDir()
	if err := goimgtools.FromBytes(photo).MaxFileSize(int64(last) + 100).ToFile(dir + "/x.heic"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir + "/x.heic"); err != nil || st.Size() > int64(last)+100 {
		t.Errorf("MaxFileSize: %v, %v", st, err)
	}
	alpha := must(os.ReadFile("testdata/alpha.heic"))
	if info, err := goimgtools.FromBytes(must(goimgtools.FromBytes(alpha).ToFormat("heic", nil).ToBytes())).Metadata(); err != nil || !info.HasAlpha {
		t.Errorf("transparency: %+v, %v", info, err)
	}
	for _, o := range []heic.Options{{Quality: 101}, {Effort: 10}} {
		if _, err := goimgtools.FromBytes(photo).ToFormat("heic", o).ToBytes(); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}
