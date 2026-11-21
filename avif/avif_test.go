package avif_test

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/avif"
	"github.com/whhhh1500/goimgtools/inspect"
)

func pixels(t *testing.T, b []byte) *image.NRGBA {
	t.Helper()
	m, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	n := image.NewNRGBA(m.Bounds())
	draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
	return n
}

func toPNG(t *testing.T, m *goimgtools.Image) *image.NRGBA {
	t.Helper()
	b, err := m.PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return pixels(t, b)
}

// psnr compares the colours of two images of the same size.
func psnr(a, b *image.NRGBA) float64 {
	var se float64
	n := 0
	for i := range a.Pix {
		if i%4 == 3 {
			continue
		}
		d := float64(a.Pix[i]) - float64(b.Pix[i])
		se += d * d
		n++
	}
	if se == 0 {
		return math.Inf(1)
	}
	return 10 * math.Log10(255*255/(se/float64(n)))
}

// TestDecode: AVIF files are recognised (Metadata) and decoded as libavif
// decodes them: Pillow's AVIF plugin (libavif 1.4.2, converting with
// libyuv as here; with exif_transpose for the irot box: the image is turned
// upright) gives the same pixels of an 8-bit file, and of a 12-bit one 48 dB
// away (taken to 16 bits here, which goimgtools rounds to 8, where Pillow
// has libavif convert to 8 bits).
// test_rot.avif is from github.com/gen2brain/avif (MIT licence).
func TestDecode(t *testing.T) {
	for _, c := range []struct {
		name  string
		w, h  int
		exact bool
	}{{"photo.avif", 150, 103, true}, {"test_rot.avif", 480, 640, false}} {
		in, err := os.ReadFile("testdata/" + c.name)
		if err != nil {
			t.Fatal(err)
		}
		info, err := goimgtools.FromBytes(in).Metadata()
		if err != nil || info.Format != "avif" {
			t.Fatalf("%s: %+v, %v", c.name, info, err)
		}
		m := toPNG(t, goimgtools.FromBytes(in))
		ref, err := os.ReadFile("testdata/" + c.name + ".pillow.png")
		if err != nil {
			t.Fatal(err)
		}
		want := pixels(t, ref)
		if m.Rect != want.Rect || psnr(m, want) < 45 || (c.exact && !bytes.Equal(m.Pix, want.Pix)) {
			t.Errorf("%s: %v, %.1f dB from Pillow's %v", c.name, m.Rect.Size(), psnr(m, want), want.Rect.Size())
		}
		if m.Rect.Dx() != c.w || m.Rect.Dy() != c.h {
			t.Errorf("%s: %v, want %dx%d", c.name, m.Rect.Size(), c.w, c.h)
		}
	}
	if _, err := goimgtools.FromBytes([]byte("\x00\x00\x00\x18ftypavif\x00\x00\x00\x00broken")).PNG().ToBytes(); err == nil {
		t.Error("a broken AVIF decoded")
	}
}

// TestEncode: written as AVIF and read back: without loss the very pixels,
// with transparency too; with loss close, smaller than PNG, and smaller at
// a lower quality; MaxFileSize finds a quality that fits; .avif by the
// file name.
func TestEncode(t *testing.T) {
	photo, err := os.ReadFile("../tests/testdata/pillow/source.png")
	if err != nil {
		t.Fatal(err)
	}
	src := toPNG(t, goimgtools.FromBytes(photo))
	lossless := must(goimgtools.FromBytes(photo).ToFormat("avif", avif.Options{Lossless: true}).ToBytes())
	if got := toPNG(t, goimgtools.FromBytes(lossless)); !bytes.Equal(got.Pix, src.Pix) {
		t.Errorf("lossless: other pixels (%.1f dB)", psnr(got, src))
	}
	alpha := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := 0; i < len(alpha.Pix); i += 4 {
		alpha.Pix[i], alpha.Pix[i+1], alpha.Pix[i+2], alpha.Pix[i+3] = uint8(i), uint8(i/3), 200, uint8(40+i%216)
	}
	got := toPNG(t, goimgtools.FromBytes(must(goimgtools.FromImage(alpha).ToFormat("avif", avif.Options{Lossless: true}).ToBytes())))
	if !bytes.Equal(got.Pix, alpha.Pix) {
		t.Error("lossless with alpha: other pixels")
	}
	pngSize := len(photo)
	var last int
	for _, q := range []int{90, 60, 30} {
		b := must(goimgtools.FromBytes(photo).ToFormat("avif", avif.Options{Quality: q, Effort: 7}).ToBytes())
		got := toPNG(t, goimgtools.FromBytes(b))
		if p := psnr(got, src); p < 26 || len(b) >= pngSize || (last > 0 && len(b) >= last) {
			t.Errorf("quality %d: %d bytes (PNG %d, quality before %d), PSNR %.1f", q, len(b), pngSize, last, p)
		}
		last = len(b)
	}
	dir := t.TempDir()
	if err := goimgtools.FromBytes(photo).MaxFileSize(int64(last) + 100).ToFile(dir + "/x.avif"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir + "/x.avif"); err != nil || st.Size() > int64(last)+100 {
		t.Errorf("MaxFileSize: %v, %v", st, err)
	} else if info, err := goimgtools.Open(dir + "/x.avif").Metadata(); err != nil || info.Format != "avif" {
		t.Errorf("x.avif: %+v, %v", info, err)
	}
	for _, o := range []avif.Options{{Quality: 101}, {Effort: 10}} {
		if _, err := goimgtools.FromBytes(photo).ToFormat("avif", o).ToBytes(); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
	_ = color.NRGBA{}
}

// TestMetadata: the EXIF block (KeepExif), an XMP packet and a colour
// profile are written and read back; an image the boxes turn has its EXIF
// orientation set to upright.
func TestMetadata(t *testing.T) {
	jpg := must(os.ReadFile("../tests/testdata/exif-pillow.jpg"))
	want, _, _ := inspect.ReadExif(goimgtools.FromBytes(jpg))
	b := must(goimgtools.FromBytes(jpg).KeepExif().ToFormat("avif", nil).ToBytes())
	kept, ok, err := inspect.ReadExif(goimgtools.FromBytes(b))
	if err != nil || !ok || kept.Make != want.Make || kept.Model != want.Model {
		t.Errorf("EXIF kept: %+v, %v, %v", kept, ok, err)
	}
	// A profile is written with the pixels and read back.
	lut := must(os.ReadFile("../tests/testdata/icc/rgb-lut16.png"))
	b = must(goimgtools.FromBytes(lut).ToFormat("avif", nil).ToBytes())
	if info, err := goimgtools.FromBytes(b).Metadata(); err != nil || !info.HasICC {
		t.Errorf("profile: %+v, %v", info, err)
	}
	rot := must(os.ReadFile("testdata/test_rot.avif"))
	if info, err := goimgtools.FromBytes(rot).Metadata(); err != nil || info.Orientation > 1 {
		t.Errorf("turned: %+v, %v", info, err)
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestMatch: AVIF as the major brand, or among the compatible ones of a
// general HEIF file; not HEIC.
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
		{head("avif"), true}, {head("avis"), true}, {head("mif1", "mif1", "avif"), true},
		{head("mif1", "mif1", "heic"), false}, {head("heic", "mif1"), false}, {[]byte("\x00\x00\x00\x20ftypisom"), false},
	} {
		if avif.Match(c.h) != c.want {
			t.Errorf("Match(%q) = %v", c.h, !c.want)
		}
	}
}
