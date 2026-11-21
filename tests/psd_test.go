package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// planes splits an image's pixels into Photoshop's planes: f(x, y) gives
// a pixel's values, one per channel, each as stored (bytes per value).
func planes(w, h, n, bytesPer int, f func(x, y int) []uint64) [][]byte {
	out := make([][]byte, n)
	for c := range out {
		out[c] = make([]byte, 0, w*h*bytesPer)
	}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := f(x, y)
			for c := 0; c < n; c++ {
				switch bytesPer {
				case 1:
					out[c] = append(out[c], uint8(v[c]))
				case 2:
					out[c] = binary.BigEndian.AppendUint16(out[c], uint16(v[c]))
				case 4:
					out[c] = binary.BigEndian.AppendUint32(out[c], uint32(v[c]))
				}
			}
		}
	}
	return out
}

func psdImage(t *testing.T, data []byte) image.Image {
	t.Helper()
	m, err := FromBytes(data).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestPSD: Photoshop's composite image in each colour mode and depth,
// stored or PackBits compressed, in a PSD or a PSB, comes out as stored;
// CMYK as other CMYK images do.
func TestPSD(t *testing.T) {
	const w, h = 37, 23
	src := testutil.Images(w, h)["rgba-opaque"].(*image.RGBA)
	px := func(x, y int) []uint64 {
		p := src.RGBAAt(x, y)
		return []uint64{uint64(p.R), uint64(p.G), uint64(p.B), uint64(255 - p.R), uint64(x * 7)}
	}
	for _, psb := range []bool{false, true} {
		for _, rle := range []bool{false, true} {
			// RGB, 8 bits.
			m := psdImage(t, testutil.PSD(testutil.PSDFile{PSB: psb, RLE: rle, Mode: 3, Depth: 8, W: w, H: h, Channels: planes(w, h, 3, 1, px)}))
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					p := src.RGBAAt(x, y)
					if got := color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA); got != (color.NRGBA{p.R, p.G, p.B, 255}) {
						t.Fatalf("psb %v rle %v: RGB pixel %d,%d: %v, want %v", psb, rle, x, y, got, p)
					}
				}
			}
			// Grey, 16 bits.
			m = psdImage(t, testutil.PSD(testutil.PSDFile{PSB: psb, RLE: rle, Mode: 1, Depth: 16, W: w, H: h,
				Channels: planes(w, h, 1, 2, func(x, y int) []uint64 { return []uint64{uint64(x*1771 + y*13)} })}))
			if g, ok := m.(*image.Gray16); !ok || g.Gray16At(5, 3).Y != 5*1771+3*13 {
				t.Errorf("psb %v rle %v: grey 16: %T %v", psb, rle, m, m.At(5, 3))
			}
		}
	}
	// CMYK, through libvips's CMYK profile as other CMYK files without one
	// (Photoshop stores 255 for no ink).
	cmyk := image.NewCMYK(image.Rect(0, 0, w, h))
	for i := range cmyk.Pix {
		cmyk.Pix[i] = uint8(i * 37)
	}
	want, err := FromSource(func(env *pipeline.Env) (pipeline.Source, error) {
		return ops.CMYKToSRGB(testutil.ChoppedEnv(cmyk, 5, env), iccprof.DefaultCMYK().ToSRGB()), nil
	}).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	got := psdImage(t, mustBytes(t, FromBytes(testutil.PSD(testutil.PSDFile{RLE: true, Mode: 4, Depth: 8, W: w, H: h,
		Channels: planes(w, h, 4, 1, func(x, y int) []uint64 {
			c := cmyk.CMYKAt(x, y)
			return []uint64{uint64(255 - c.C), uint64(255 - c.M), uint64(255 - c.Y), uint64(255 - c.K)}
		})})).PNG()))
	if !psdSame(got, want) {
		t.Error("CMYK: another picture than the inks through the CMYK profile")
	}
	// Indexed, with a transparent colour.
	pal := make([]byte, 768)
	for i := 0; i < 256; i++ {
		pal[i], pal[256+i], pal[512+i] = uint8(i), uint8(255-i), uint8(i/2)
	}
	m := psdImage(t, testutil.PSD(testutil.PSDFile{Mode: 2, Depth: 8, W: w, H: h, Palette: pal, Res: map[int][]byte{1047: {0, 9}},
		Channels: planes(w, h, 1, 1, func(x, y int) []uint64 { return []uint64{uint64(x + y)} })}))
	if c := color.NRGBAModel.Convert(m.At(4, 5)).(color.NRGBA); c != (color.NRGBA{9, 246, 4, 0}) && c.A != 0 {
		t.Errorf("indexed transparent colour: %v", c)
	}
	if c := color.NRGBAModel.Convert(m.At(4, 6)).(color.NRGBA); c != (color.NRGBA{10, 245, 5, 255}) {
		t.Errorf("indexed colour: %v", c)
	}
	// Bitmap: a set bit is black.
	bits := make([]byte, 0, h*((w+7)/8))
	for y := 0; y < h; y++ {
		for i := 0; i < (w+7)/8; i++ {
			bits = append(bits, 0xa5^uint8(y))
		}
	}
	m = psdImage(t, testutil.PSD(testutil.PSDFile{Mode: 0, Depth: 1, W: w, H: h, Channels: [][]byte{bits}}))
	for x := 0; x < 8; x++ {
		set := (0xa5^3)>>(7-x)&1 == 1
		if v := color.GrayModel.Convert(m.At(x, 3)).(color.Gray).Y; (v == 0) != set || v != 0 && v != 255 {
			t.Fatalf("bitmap pixel %d: %d", x, v)
		}
	}
	// Lab: white, black and a middle grey.
	m = psdImage(t, testutil.PSD(testutil.PSDFile{Mode: 9, Depth: 8, W: 3, H: 1, Channels: [][]byte{{255, 0, 119}, {128, 128, 128}, {128, 128, 128}}}))
	for x, want := range []uint8{255, 0, 112} {
		c := color.NRGBAModel.Convert(m.At(x, 0)).(color.NRGBA)
		if d := int(c.R) - int(want); c.R != c.G || c.G != c.B || d < -1 || d > 1 {
			t.Errorf("Lab pixel %d: %v, want grey %d", x, c, want)
		}
	}
	// 32 bits: linear light, in the sRGB curve.
	half := uint64(math.Float32bits(0.5))
	m = psdImage(t, testutil.PSD(testutil.PSDFile{Mode: 1, Depth: 32, W: 2, H: 1, Channels: [][]byte{binary.BigEndian.AppendUint32(binary.BigEndian.AppendUint32(nil, uint32(half)), math.Float32bits(1))}}))
	if g := m.(*image.Gray16); g.Gray16At(0, 0).Y != 48192 || g.Gray16At(1, 0).Y != 65535 {
		t.Errorf("32 bits: %v %v", g.Gray16At(0, 0), g.Gray16At(1, 0))
	}
}

// TestPSDTransparency: the composite's transparency, where psd-tools finds
// it, with its colours freed of the white they were laid on; an extra
// channel that is not transparency is left out.
func TestPSDTransparency(t *testing.T) {
	const w, h = 16, 16
	matte := func(c, a int) uint64 { return uint64((c*a + 255*(255-a) + 127) / 255) }
	px := func(x, y int) []uint64 {
		a := x * 17
		return []uint64{matte(200, a), matte(40, a), matte(90, a), uint64(a)}
	}
	for _, f := range []testutil.PSDFile{
		{Mode: 3, Depth: 8, W: w, H: h, Channels: planes(w, h, 4, 1, px), Layers: -1},
		{Mode: 3, Depth: 8, W: w, H: h, Channels: planes(w, h, 4, 1, px), Layers: 2, Tagged: map[string]bool{"Mtrn": true}},
		{Mode: 3, Depth: 8, W: w, H: h, Channels: planes(w, h, 4, 1, px), Res: map[int][]byte{1053: {0, 0, 0, 0}}},
		{Mode: 3, Depth: 8, W: w, H: h, Channels: planes(w, h, 4, 1, px), PSB: true, RLE: true, Layers: -3},
	} {
		m := psdImage(t, testutil.PSD(f))
		for x := 1; x < w; x++ {
			c := color.NRGBAModel.Convert(m.At(x, 2)).(color.NRGBA)
			d := max(abs(int(c.R)-200), abs(int(c.G)-40), abs(int(c.B)-90))
			if int(c.A) != x*17 || d > 255/(x*17)+1 {
				t.Fatalf("%+v: pixel %d: %v", f.Layers, x, c)
			}
		}
	}
	// A saved selection, not transparency: the layers say so, or its id.
	for _, f := range []testutil.PSDFile{
		{Mode: 3, Depth: 8, W: w, H: h, Channels: planes(w, h, 4, 1, px), Layers: 2},
		{Mode: 3, Depth: 8, W: w, H: h, Channels: planes(w, h, 4, 1, px), Res: map[int][]byte{1053: {0, 0, 0, 5}}},
	} {
		m := psdImage(t, testutil.PSD(f))
		if c := color.NRGBAModel.Convert(m.At(3, 2)).(color.NRGBA); c.A != 255 || uint64(c.R) != matte(200, 51) {
			t.Errorf("an extra channel taken for transparency: %v", c)
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// TestPSDMetadata: the colour profile, EXIF, XMP and resolution of the
// file, and its format.
func TestPSDMetadata(t *testing.T) {
	res := make([]byte, 16)
	binary.BigEndian.PutUint32(res, 300<<16)
	exif := exifWithOrientation(binary.BigEndian, 1)
	data := testutil.PSD(testutil.PSDFile{Mode: 1, Depth: 8, W: 4, H: 3, Channels: [][]byte{make([]byte, 12)},
		Res: map[int][]byte{1005: res, 1058: exif, 1060: []byte("<x:xmpmeta/>")}})
	info, err := FromBytes(data).Metadata()
	if err != nil {
		t.Fatal(err)
	}
	if info.Format != "psd" || info.Width != 4 || info.Height != 3 || info.Density != 300 || !info.HasExif || !info.HasXmp {
		t.Errorf("metadata: %+v", info)
	}
	if err := FromBytes(data).ToFile(t.TempDir() + "/x.psd"); err == nil {
		t.Error("a PSD was written")
	}
}

// TestPSDLimits: a large composite within a small memory limit, the same
// in any strips; a broken file is an error.
func TestPSDLimits(t *testing.T) {
	const w, h = 1500, 1100
	data := testutil.PSD(testutil.PSDFile{RLE: true, Mode: 3, Depth: 8, W: w, H: h,
		Channels: planes(w, h, 3, 1, func(x, y int) []uint64 { return []uint64{uint64(x ^ y), uint64(x / 7), uint64(y % 251)} })})
	a := mustBytes(t, FromBytes(data).MemoryLimit(3<<20).PNG())
	b := mustBytes(t, FromBytes(data).PNG())
	if !bytes.Equal(a, b) {
		t.Error("another picture within 3MB")
	}
	for _, n := range []int{len(data) - 1, len(data) / 2, 40} {
		if _, err := FromBytes(data[:n]).PNG().ToBytes(); !errors.Is(err, ErrFormat) {
			t.Errorf("cut at %d: %v", n, err)
		}
	}
}

func mustBytes(t *testing.T, m *Image) []byte {
	t.Helper()
	b, err := m.ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func psdSame(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	r := a.Bounds()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			if color.NRGBA64Model.Convert(a.At(x, y)) != color.NRGBA64Model.Convert(b.At(x, y)) {
				return false
			}
		}
	}
	return true
}

// TestPSDPixelLimit: a PSB of 300000 by 300000 is refused by its size,
// before anything is read of it.
func TestPSDPixelLimit(t *testing.T) {
	data := testutil.PSD(testutil.PSDFile{PSB: true, Mode: 3, Depth: 8, W: 1, H: 1, Channels: [][]byte{{1}, {2}, {3}}})
	binary.BigEndian.PutUint32(data[14:], 300000)
	binary.BigEndian.PutUint32(data[18:], 300000)
	if _, err := FromBytes(data).PNG().ToBytes(); !errors.Is(err, ErrPixelLimit) && !errors.Is(err, ErrFormat) {
		t.Errorf("huge PSB: %v", err)
	}
}
