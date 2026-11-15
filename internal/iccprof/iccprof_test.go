package iccprof

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func readPNG(t *testing.T, name string) image.Image {
	t.Helper()
	f, err := os.Open(filepath.Join("..", "..", "tests", "testdata", "icc", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	m, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestMatchesLittleCMS: converting a colour wheel to sRGB from profiles
// with each kind of tone curve gives what LittleCMS (Pillow's ImageCms,
// relative colorimetric) gives, to within one level.
func TestMatchesLittleCMS(t *testing.T) {
	wheel := readPNG(t, "wheel.png")
	for _, name := range []string{"p3-table", "adobe-gamma", "p3-para"} {
		icc, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "icc", name+".icc"))
		if err != nil {
			t.Fatal(err)
		}
		p, err := Parse(icc)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if p.IsSRGB() {
			t.Errorf("%s taken for sRGB", name)
		}
		tr := p.ToSRGB()
		want := readPNG(t, name+"-srgb.png")
		b := wheel.Bounds()
		worst, off := 0, 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, _ := wheel.At(x, y).RGBA()
				gr, gg, gb := tr.Convert(uint8(r>>8), uint8(g>>8), uint8(bl>>8))
				wr, wg, wb, _ := want.At(x, y).RGBA()
				for _, d := range []int{int(gr) - int(wr>>8), int(gg) - int(wg>>8), int(gb) - int(wb>>8)} {
					d = max(d, -d)
					worst = max(worst, d)
					if d > 0 {
						off++
					}
				}
			}
		}
		t.Logf("%s: at most %d apart, %d of %d values differ", name, worst, off, 3*b.Dx()*b.Dy())
		if worst > 1 {
			t.Errorf("%s: up to %d levels from LittleCMS", name, worst)
		}
	}
}

// TestSRGBAndBadProfiles: an sRGB profile is recognised as such and
// converts nothing; data that is not a matrix/TRC RGB profile is refused,
// not misread.
func TestSRGBAndBadProfiles(t *testing.T) {
	icc, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "icc", "p3-para.icc"))
	if err != nil {
		t.Fatal(err)
	}
	// The same profile with sRGB's colorants is sRGB.
	srgb := append([]byte(nil), icc...)
	p, _ := Parse(icc)
	for i, name := range []string{"rXYZ", "gXYZ", "bXYZ"} {
		off := tagOffset(srgb, name)
		for k := 0; k < 3; k++ {
			putS15(srgb[off+8+4*k:], srgbXYZ[k][i])
		}
	}
	q, err := Parse(srgb)
	if err != nil || !q.IsSRGB() || !q.ToSRGB().Identity() {
		t.Fatalf("sRGB profile: %v, sRGB %v", err, q != nil && q.IsSRGB())
	}
	tr := q.ToSRGB()
	for v := 0; v < 256; v++ {
		if r, g, b := tr.Convert(uint8(v), uint8(255-v), uint8(v/2)); r != uint8(v) || g != uint8(255-v) || b != uint8(v/2) {
			t.Fatalf("sRGB to sRGB changed %d,%d,%d to %d,%d,%d", v, 255-v, v/2, r, g, b)
		}
	}
	_ = p
	bad := map[string][]byte{
		"empty":     nil,
		"short":     icc[:100],
		"no magic":  append([]byte{}, append(icc[:36:36], append([]byte("xxxx"), icc[40:]...)...)...),
		"cmyk":      withAt(icc, 16, "CMYK"),
		"lab pcs":   withAt(icc, 20, "Lab "),
		"truncated": icc[:len(icc)-100],
		"no rTRC":   withAt(icc, tagEntry(icc, "rTRC"), "zzzz"),
		"bad curve": withAt(icc, tagOffset(icc, "gTRC"), "abcd"),
	}
	for name, b := range bad {
		if _, err := Parse(b); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func withAt(b []byte, at int, s string) []byte {
	c := append([]byte(nil), b...)
	copy(c[at:], s)
	return c
}

func tagEntry(b []byte, name string) int {
	n := int(b[131])
	for i := 0; i < n; i++ {
		if string(b[132+12*i:132+12*i+4]) == name {
			return 132 + 12*i
		}
	}
	return -1
}

func tagOffset(b []byte, name string) int {
	e := tagEntry(b, name)
	return int(b[e+4])<<24 | int(b[e+5])<<16 | int(b[e+6])<<8 | int(b[e+7])
}

func putS15(b []byte, v float64) {
	x := uint32(int32(v*65536 + 0.5))
	b[0], b[1], b[2], b[3] = byte(x>>24), byte(x>>16), byte(x>>8), byte(x)
}

// FuzzParse: any bytes either fail to parse or give a profile whose
// conversion works on every value, without a panic.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"p3-table.icc", "adobe-gamma.icc", "p3-para.icc"} {
		b, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "icc", name))
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, b []byte) {
		p, err := Parse(b)
		if err != nil {
			return
		}
		tr := p.ToSRGB()
		for v := 0; v < 256; v += 17 {
			tr.Convert(uint8(v), uint8(255-v), uint8(v*3))
		}
	})
}
