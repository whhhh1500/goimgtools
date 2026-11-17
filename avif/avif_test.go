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
// decodes them: Pillow's AVIF plugin (with exif_transpose for the irot box:
// the image is turned upright) gives about the same pixels, its libavif
// converting with libyuv, the WebAssembly build with libavif's own
// bilinear chroma (39 to 48 dB apart; on a photograph Pillow's encoded,
// Pillow's decoding is 0.5 dB closer to the original, its two conversions
// being libyuv's both).
// test_rot.avif is from github.com/gen2brain/avif (MIT licence).
func TestDecode(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
	}{{"photo.avif", 150, 103}, {"test_rot.avif", 480, 640}} {
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
		if m.Rect != want.Rect || psnr(m, want) < 38 {
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

// TestEncode: written as AVIF and read back: without loss the very pixels
// of an opaque image, and those with transparency to within what colour
// multiplied by alpha keeps (gen2brain/avif hands libavif premultiplied
// pixels and takes them back so: alpha exact, the colour times alpha
// within 2 levels); with loss close, smaller than PNG, and smaller at a lower
// quality; MaxFileSize finds a quality that fits; .avif by the file name.
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
	for i := 0; i < len(alpha.Pix); i += 4 {
		a := int(alpha.Pix[i+3])
		if got.Pix[i+3] != alpha.Pix[i+3] {
			t.Fatalf("lossless with alpha: pixel %d alpha %d, was %d", i/4, got.Pix[i+3], a)
		}
		for k := 0; k < 3; k++ {
			// What shows of the colour: times alpha.
			if d := (int(got.Pix[i+k]) - int(alpha.Pix[i+k])) * a / 255; d < -2 || d > 2 {
				t.Fatalf("lossless with alpha: pixel %d %v, was %v", i/4, got.Pix[i:i+4], alpha.Pix[i:i+4])
			}
		}
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
