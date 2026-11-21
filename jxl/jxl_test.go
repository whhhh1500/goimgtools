package jxl_test

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"io"
	"math"
	"os"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/jxl"
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

// pngICC is the profile of a PNG's iCCP chunk.
func pngICC(t *testing.T, b []byte) []byte {
	t.Helper()
	i := bytes.Index(b, []byte("iCCP"))
	if i < 4 {
		return nil
	}
	n := int(binary.BigEndian.Uint32(b[i-4:]))
	chunk := b[i+4 : i+4+n]
	z := bytes.IndexByte(chunk, 0)
	r, err := zlib.NewReader(bytes.NewReader(chunk[z+2:]))
	if err != nil {
		t.Fatal(err)
	}
	return must(io.ReadAll(r))
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

// TestDecode: JPEG XL files made by cjxl 0.12.0 are recognised (Metadata)
// and decoded as djxl 0.12.0 decodes them: without loss the very pixels;
// with loss within a level (djxl runs libjxl with AVX2, whose fused
// multiplications and additions round a little differently; here libjxl
// runs without SIMD, the same on every processor, which the sums hold). A
// lossy photograph, a grey one (as RGB here), a JPEG transcoded without loss
// whose orientation (6) turns it upright, and an image with a profile.
func TestDecode(t *testing.T) {
	for _, c := range []struct {
		name  string
		w, h  int
		exact bool
		sum   string
	}{
		{"photo.jxl", 150, 103, false, "06e28a6f3de2a07f"},
		{"gray.jxl", 150, 103, false, "147a97c0976c379d"},
		{"exif6.jxl", 209, 304, false, "0f1a19a32c4c8ac7"},
		{"icc.jxl", 64, 48, true, "144815440b0cb272"},
	} {
		in := must(os.ReadFile("testdata/" + c.name))
		info, err := goimgtools.FromBytes(in).Metadata()
		if err != nil || info.Format != "jxl" || info.Width != c.w || info.Height != c.h || info.Orientation > 1 {
			t.Fatalf("%s: %+v, %v", c.name, info, err)
		}
		m := toPNG(t, goimgtools.FromBytes(in))
		if got := fmt.Sprintf("%x", sha256.Sum256(m.Pix))[:16]; got != c.sum {
			t.Errorf("%s: sum %s, want %s", c.name, got, c.sum)
		}
		want := pixels(t, must(os.ReadFile("testdata/"+c.name+".djxl.png")))
		if m.Rect != want.Rect {
			t.Fatalf("%s: %v, djxl %v", c.name, m.Rect.Size(), want.Rect.Size())
		}
		worst := 0
		for i := range m.Pix {
			worst = max(worst, int(m.Pix[i])-int(want.Pix[i]), int(want.Pix[i])-int(m.Pix[i]))
		}
		if worst > 1 || c.exact && worst > 0 {
			t.Errorf("%s: up to %d levels from djxl", c.name, worst)
		}
	}
	for _, b := range [][]byte{[]byte("\xff\x0abroken"), []byte("\x00\x00\x00\x0cJXL \x0d\x0a\x87\x0a\x00\x00")} {
		if _, err := goimgtools.FromBytes(b).PNG().ToBytes(); !errors.Is(err, goimgtools.ErrFormat) {
			t.Errorf("%q: %v", b, err)
		}
	}
}

// TestEncode: written as JPEG XL and read back: without loss the very
// pixels, with transparency too; with loss close, smaller than PNG, and
// smaller at a lower quality; MaxFileSize finds a quality that fits; .jxl by
// the file name.
func TestEncode(t *testing.T) {
	photo := must(os.ReadFile("../tests/testdata/pillow/source.png"))
	src := toPNG(t, goimgtools.FromBytes(photo))
	lossless := must(goimgtools.FromBytes(photo).ToFormat("jxl", jxl.Options{Lossless: true}).ToBytes())
	if got := toPNG(t, goimgtools.FromBytes(lossless)); !bytes.Equal(got.Pix, src.Pix) {
		t.Errorf("lossless: other pixels (%.1f dB)", psnr(got, src))
	}
	alpha := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := 0; i < len(alpha.Pix); i += 4 {
		alpha.Pix[i], alpha.Pix[i+1], alpha.Pix[i+2], alpha.Pix[i+3] = uint8(i), uint8(i/3), 200, uint8(40+i%216)
	}
	got := toPNG(t, goimgtools.FromBytes(must(goimgtools.FromImage(alpha).ToFormat("jxl", jxl.Options{Lossless: true}).ToBytes())))
	if !bytes.Equal(got.Pix, alpha.Pix) {
		t.Error("lossless with alpha: other pixels")
	}
	var last int
	for _, q := range []int{95, 80, 50} {
		b := must(goimgtools.FromBytes(photo).ToFormat("jxl", jxl.Options{Quality: q, Effort: 3}).ToBytes())
		got := toPNG(t, goimgtools.FromBytes(b))
		if p := psnr(got, src); p < 28 || len(b) >= len(photo) || (last > 0 && len(b) >= last) {
			t.Errorf("quality %d: %d bytes (PNG %d, quality before %d), PSNR %.1f", q, len(b), len(photo), last, p)
		}
		last = len(b)
	}
	dir := t.TempDir()
	if err := goimgtools.FromBytes(photo).MaxFileSize(int64(last) + 200).ToFile(dir + "/x.jxl"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(dir + "/x.jxl"); err != nil || st.Size() > int64(last)+200 {
		t.Errorf("MaxFileSize: %v, %v", st, err)
	} else if info, err := goimgtools.Open(dir + "/x.jxl").Metadata(); err != nil || info.Format != "jxl" {
		t.Errorf("x.jxl: %+v, %v", info, err)
	}
	for _, o := range []jxl.Options{{Quality: 101}, {Effort: 11}} {
		if _, err := goimgtools.FromBytes(photo).ToFormat("jxl", o).ToBytes(); err == nil {
			t.Errorf("%+v accepted", o)
		}
	}
}

// TestMetadata: the EXIF block (KeepExif), an XMP packet and a colour
// profile are written and read back; a file whose orientation turned its
// pixels has its EXIF orientation set to upright.
func TestMetadata(t *testing.T) {
	jpg := must(os.ReadFile("../tests/testdata/exif-pillow.jpg"))
	want, _, _ := inspect.ReadExif(goimgtools.FromBytes(jpg))
	b := must(goimgtools.FromBytes(jpg).KeepExif().ToFormat("jxl", nil).ToBytes())
	kept, ok, err := inspect.ReadExif(goimgtools.FromBytes(b))
	if err != nil || !ok || kept.Make != want.Make || kept.Model != want.Model {
		t.Errorf("EXIF kept: %+v, %v, %v", kept, ok, err)
	}
	lut := must(os.ReadFile("../tests/testdata/icc/rgb-lut16.png"))
	icc := must(os.ReadFile("../tests/testdata/icc/rgb-lut16.icc"))
	for _, o := range []jxl.Options{{Lossless: true}, {}} {
		b = must(goimgtools.FromBytes(lut).ToFormat("jxl", o).ToBytes())
		if info, err := goimgtools.FromBytes(b).Metadata(); err != nil || !info.HasICC {
			t.Errorf("profile %+v: %+v, %v", o, info, err)
		}
	}
	// Without loss the profile is the file's own.
	in := must(os.ReadFile("testdata/icc.jxl"))
	if p := pngICC(t, must(goimgtools.FromBytes(in).PNG().ToBytes())); !bytes.Equal(p, icc) {
		t.Errorf("icc.jxl: a profile of %d bytes, want %d", len(p), len(icc))
	}
	rot := must(os.ReadFile("testdata/exif6.jxl"))
	if x, ok, err := inspect.ReadExif(goimgtools.FromBytes(rot)); err != nil || !ok || x.Orientation != 1 {
		t.Errorf("turned: %+v, %v, %v", x, ok, err)
	}
}

// TestMatch: a codestream, and the container; not other files.
func TestMatch(t *testing.T) {
	for _, c := range []struct {
		h    []byte
		want bool
	}{
		{[]byte("\xff\x0a\x00"), true}, {[]byte("\x00\x00\x00\x0cJXL \x0d\x0a\x87\x0a\x00\x00\x00\x14ftypjxl "), true},
		{[]byte("\xff\xd8\xff"), false}, {[]byte("\x00\x00\x00\x0cJXL \x0d\x0a\x87"), false}, {nil, false},
	} {
		if jxl.Match(c.h) != c.want {
			t.Errorf("Match(%q) = %v", c.h, !c.want)
		}
	}
}
