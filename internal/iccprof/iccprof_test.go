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

// TestLookupTables: profiles of lookup tables (lut16 to Lab, lutAToB to
// XYZ; CMYK and RGB) are read and convert paper white to white; damaged
// tables are refused, not misread; conversions asked of the wrong kind of
// profile leave the values alone.
func TestLookupTables(t *testing.T) {
	for _, c := range []struct {
		name string
		ch   int
	}{{"cmyk-lut16", 4}, {"cmyk-mab", 4}, {"rgb-lut16", 3}} {
		icc, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "icc", c.name+".icc"))
		if err != nil {
			t.Fatal(err)
		}
		p, err := Parse(icc)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.Channels() != c.ch || p.IsSRGB() || p.ToSRGB().Identity() {
			t.Errorf("%s: %d channels, sRGB %v", c.name, p.Channels(), p.IsSRGB())
		}
		tr := p.ToSRGB()
		var r, g, b uint8
		if c.ch == 4 {
			r, g, b = tr.ConvertCMYK(0, 0, 0, 0)
			if x, y, z := tr.Convert(10, 20, 30); x != 10 || y != 20 || z != 30 {
				t.Errorf("%s: an RGB conversion of a CMYK profile changed the values", c.name)
			}
		} else {
			r, g, b = tr.Convert(255, 255, 255)
			if x, y, z := tr.ConvertCMYK(0, 0, 0, 255); x != 0 || y != 0 || z != 0 {
				t.Errorf("%s: full black is %d,%d,%d", c.name, x, y, z)
			}
		}
		if r < 254 || g < 254 || b < 254 {
			t.Errorf("%s: white is %d,%d,%d", c.name, r, g, b)
		}
		// A table cut short, a grid too large for its data, a tag of an
		// unknown type.
		a2b := tagOffset(icc, "A2B0")
		size := int(icc[tagEntry(icc, "A2B0")+8])<<24 | int(icc[tagEntry(icc, "A2B0")+9])<<16 |
			int(icc[tagEntry(icc, "A2B0")+10])<<8 | int(icc[tagEntry(icc, "A2B0")+11])
		short := append([]byte(nil), icc...)
		e := tagEntry(short, "A2B0")
		n := size / 2
		short[e+8], short[e+9], short[e+10], short[e+11] = byte(n>>24), byte(n>>16), byte(n>>8), byte(n)
		big := append([]byte(nil), icc...)
		if string(icc[a2b:a2b+4]) == "mAB " {
			clut := int(icc[a2b+24])<<24 | int(icc[a2b+25])<<16 | int(icc[a2b+26])<<8 | int(icc[a2b+27])
			big[a2b+clut] = 200
		} else {
			big[a2b+10] = 200
		}
		for what, b := range map[string][]byte{"cut short": short, "grid too large": big, "unknown type": withAt(icc, a2b, "mBA ")} {
			if _, err := Parse(b); err == nil {
				t.Errorf("%s %s: accepted", c.name, what)
			}
		}
	}
}

// TestGrayProfile: a grey profile of a tone curve keeps white and black,
// darkens the middle greys of a dot gain, and is not sRGB; one without its
// curve is refused.
func TestGrayProfile(t *testing.T) {
	icc, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "icc", "gray-dotgain.icc"))
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(icc)
	if err != nil {
		t.Fatal(err)
	}
	if p.Channels() != 1 || p.IsSRGB() || p.ToSRGB().Identity() {
		t.Fatalf("%d channels, sRGB %v", p.Channels(), p.IsSRGB())
	}
	tr := p.ToSRGB()
	w, _, _ := tr.Convert(255, 255, 255)
	k, _, _ := tr.Convert(0, 0, 0)
	m, _, _ := tr.Convert(128, 128, 128)
	if w != 255 || k != 0 || m >= 128 {
		t.Errorf("white %d, black %d, middle grey %d", w, k, m)
	}
	if _, err := Parse(withAt(icc, tagEntry(icc, "kTRC"), "zzzz")); err == nil {
		t.Error("a grey profile without its curve accepted")
	}
}

// FuzzParse: any bytes either fail to parse or give a profile whose
// conversion works on every value, without a panic.
func FuzzParse(f *testing.F) {
	for _, name := range []string{"p3-table.icc", "adobe-gamma.icc", "p3-para.icc", "cmyk-lut16.icc", "cmyk-mab.icc", "rgb-lut16.icc", "gray-dotgain.icc"} {
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
			tr.ConvertCMYK(uint8(v), uint8(255-v), uint8(v*3), uint8(v*7))
		}
	})
}
