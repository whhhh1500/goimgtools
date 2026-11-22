package tests

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"strconv"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestColorLUT: ColorLUT gives the pixels Pillow 12's Image.filter gives
// with a Color3DLUT of the same values (testdata/lut: a picture with
// every level and some transparency, and what Pillow made of it through
// each cube), for cubes read from .cube files and cubes made by a
// function, of 2 to 65 points a side, with values beyond 0 to 1.
func TestColorLUT(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile("testdata/lut/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	in := read("lut_in.png")
	same := func(name string, l *LUT3D) {
		t.Helper()
		got, err := FromBytes(in).ColorLUT(l).ToImage()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		want := decodeOurs(t, read(name+".png"))
		b := want.Bounds()
		if got.Bounds() != b {
			t.Fatalf("%s: %v", name, got.Bounds())
		}
		bad := 0
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				p, q := color.NRGBAModel.Convert(got.At(x, y)), color.NRGBAModel.Convert(want.At(x, y))
				if p != q {
					if bad == 0 {
						t.Errorf("%s: pixel (%d, %d) is %v, Pillow's is %v", name, x, y, p, q)
					}
					bad++
				}
			}
		}
		if bad > 1 {
			t.Errorf("%s: %d pixels differ", name, bad)
		}
	}
	for _, name := range []string{"warm_9", "random_9", "beyond_5", "negative_5", "identity_2"} {
		l, err := ParseCube(bytes.NewReader(read(name + ".cube")))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		same(name, l)
	}
	for name, c := range map[string]struct {
		size int
		f    func(r, g, b float64) (float64, float64, float64)
	}{
		"identity_65": {65, func(r, g, b float64) (float64, float64, float64) { return r, g, b }},
		"swap_33":     {33, func(r, g, b float64) (float64, float64, float64) { return b, r, g }},
	} {
		l, err := NewLUT3D(c.size, c.f)
		if err != nil || l.Size() != c.size {
			t.Fatalf("%s: %v", name, err)
		}
		same(name, l)
	}

	// A file with Windows line ends, a byte order mark, values over a
	// domain of 0 to 255 and Resolve's range line is the same cube.
	var scaled strings.Builder
	scaled.WriteString("\ufeffTITLE \"x\"\r\nLUT_3D_SIZE 2\r\nLUT_3D_INPUT_RANGE 0 1\r\nDOMAIN_MIN 0 0 0\r\nDOMAIN_MAX 255 255 255\r\n")
	for _, v := range [][3]int{{0, 0, 0}, {255, 0, 0}, {0, 255, 0}, {255, 255, 0}, {0, 0, 255}, {255, 0, 255}, {0, 255, 255}, {255, 255, 255}} {
		scaled.WriteString(strings.Join([]string{strconv.Itoa(v[0]), strconv.Itoa(v[1]), strconv.Itoa(v[2])}, "\t") + "\r\n")
	}
	if l, err := ParseCube(strings.NewReader(scaled.String())); err != nil {
		t.Errorf("a cube over 0 to 255: %v", err)
	} else {
		same("identity_2", l)
	}

	// Transparency stays, grey goes through as its colour, and the memory
	// is the cube's.
	l33, _ := NewLUT3D(33, func(r, g, b float64) (float64, float64, float64) { return 1 - r, 1 - g, 1 - b })
	grey := image.NewGray(image.Rect(0, 0, 9, 1))
	for i := range grey.Pix {
		grey.Pix[i] = uint8(i * 31)
	}
	out, err := FromImage(grey).ColorLUT(l33).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	for i := range grey.Pix {
		c := color.NRGBAModel.Convert(out.At(i, 0)).(color.NRGBA)
		if want := 255 - grey.Pix[i]; math.Abs(float64(c.R)-float64(want)) > 1 || c.R != c.G || c.G != c.B || c.A != 255 {
			t.Errorf("grey %d inverted: %v", grey.Pix[i], c)
		}
	}
	if w, h, err := FromBytes(in).ColorLUT(l33).Resize(20, 0).OutputSize(); err != nil || w != 20 || h != 15 {
		t.Errorf("OutputSize after ColorLUT: %dx%d, %v", w, h, err)
	}

	for name, text := range map[string]string{
		"no size":            "0 0 0\n1 1 1\n",
		"too few":            "LUT_3D_SIZE 2\n0 0 0\n",
		"too many":           "LUT_3D_SIZE 2\n" + strings.Repeat("0 0 0\n", 9),
		"one dimension":      "LUT_1D_SIZE 4\n0 0 0\n",
		"too large":          "LUT_3D_SIZE 129\n",
		"a side of one":      "LUT_3D_SIZE 1\n0 0 0\n",
		"not numbers":        "LUT_3D_SIZE 2\n0 0 zero\n",
		"two numbers":        "LUT_3D_SIZE 2\n0 0\n",
		"not a number":       "LUT_3D_SIZE 2\nNaN 0 0\n",
		"an empty domain":    "LUT_3D_SIZE 2\nDOMAIN_MAX 0 0 0\n" + strings.Repeat("0 0 0\n", 8),
		"the size twice":     "LUT_3D_SIZE 2\nLUT_3D_SIZE 2\n" + strings.Repeat("0 0 0\n", 8),
		"nothing":            "",
		"a domain too late":  "LUT_3D_SIZE 2\n0 0 0\nDOMAIN_MIN 0 0 0\n" + strings.Repeat("0 0 0\n", 7),
		"a size of no digit": "LUT_3D_SIZE two\n",
	} {
		if _, err := ParseCube(strings.NewReader(text)); err == nil {
			t.Errorf("a cube file with %s: read", name)
		} else if name == "one dimension" && !errors.Is(err, errors.ErrUnsupported) {
			t.Errorf("a table of one dimension: %v", err)
		}
	}
	if _, err := NewLUT3D(66, func(r, g, b float64) (float64, float64, float64) { return r, g, b }); err == nil {
		t.Error("a cube of 66 points a side: made")
	}
	if _, err := NewLUT3D(4, nil); err == nil {
		t.Error("a cube of no function: made")
	}
	if _, err := FromBytes(in).ColorLUT(nil).PNG().ToBytes(); err == nil {
		t.Error("ColorLUT without a cube: run")
	}
}
