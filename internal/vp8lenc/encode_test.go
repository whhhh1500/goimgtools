package vp8lenc

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"

	"golang.org/x/image/webp"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// want is the image a decoder must return for m: every pixel through
// color.NRGBAModel, origin at (0,0).
func want(m image.Image) *image.NRGBA {
	b := m.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			out.SetNRGBA(x-b.Min.X, y-b.Min.Y, color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA))
		}
	}
	return out
}

func encodeBytes(t testing.TB, src pipeline.Source, o *Options) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Encode(&buf, src, o); err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return buf.Bytes()
}

// checkFile decodes data with x/image and compares it with m. It also
// checks the container sizes.
func checkFile(t testing.TB, name string, data []byte, m image.Image) {
	t.Helper()
	if len(data) < 20 || len(data)%2 != 0 {
		t.Fatalf("%s: bad file length %d", name, len(data))
	}
	riff := int(data[4]) | int(data[5])<<8 | int(data[6])<<16 | int(data[7])<<24
	chunk := int(data[16]) | int(data[17])<<8 | int(data[18])<<16 | int(data[19])<<24
	if riff != len(data)-8 || chunk+chunk&1 != len(data)-20 {
		t.Fatalf("%s: RIFF size %d, chunk size %d, file %d bytes", name, riff, chunk, len(data))
	}
	got, err := webp.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("%s: decode: %v", name, err)
	}
	g, ok := got.(*image.NRGBA)
	if !ok {
		t.Fatalf("%s: decoded as %T", name, got)
	}
	w := want(m)
	if g.Bounds() != w.Bounds() {
		t.Fatalf("%s: bounds %v, want %v", name, g.Bounds(), w.Bounds())
	}
	if !bytes.Equal(g.Pix, w.Pix) {
		for i := range g.Pix {
			if g.Pix[i] != w.Pix[i] {
				t.Fatalf("%s: pixel (%d,%d) channel %d: got %d, want %d", name,
					i/4%w.Rect.Dx(), i/4/w.Rect.Dx(), i%4, g.Pix[i], w.Pix[i])
			}
		}
	}
	opaque := true
	for i := 3; i < len(w.Pix); i += 4 {
		if w.Pix[i] != 0xff {
			opaque = false
			break
		}
	}
	// Byte 24 holds the top bits of height-1, the alpha hint and the version.
	if hint := data[24]>>4&1 == 1; hint == opaque {
		t.Fatalf("%s: alpha_is_used = %v for an image with opaque = %v", name, hint, opaque)
	}
}

func roundTrip(t testing.TB, name string, m image.Image, n int, o *Options) []byte {
	t.Helper()
	env := pipeline.NewEnv(0, t.TempDir())
	data := encodeBytes(t, testutil.ChoppedEnv(m, n, env), o)
	checkFile(t, name, data, m)
	if u := env.Budget.Used(); u != 0 {
		t.Fatalf("%s: %d bytes of budget still reserved", name, u)
	}
	leftovers(t, env.TempDir)
	return data
}

func leftovers(t testing.TB, dir string) {
	t.Helper()
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 0 {
		t.Fatalf("%d temporary files left in %s", len(ents), dir)
	}
}

// extraImages are the shapes and contents beyond testutil.Images.
func extraImages() map[string]image.Image {
	r := rand.New(rand.NewSource(11))
	out := map[string]image.Image{}
	nrgba := func(w, h int, f func(x, y int) color.NRGBA) *image.NRGBA {
		m := image.NewNRGBA(image.Rect(0, 0, w, h))
		for y := 0; y < h; y++ {
			for x := 0; x < w; x++ {
				m.SetNRGBA(x, y, f(x, y))
			}
		}
		return m
	}
	out["1x1"] = nrgba(1, 1, func(x, y int) color.NRGBA { return color.NRGBA{10, 20, 30, 255} })
	out["1x1-clear"] = nrgba(1, 1, func(x, y int) color.NRGBA { return color.NRGBA{10, 20, 30, 0} })
	out["1xN"] = nrgba(1, 97, func(x, y int) color.NRGBA { return color.NRGBA{uint8(y * 3), uint8(y), 7, 255} })
	out["Nx1"] = nrgba(97, 1, func(x, y int) color.NRGBA { return color.NRGBA{uint8(x * 3), uint8(x), 7, uint8(200 + x%50)} })
	out["2x2"] = nrgba(2, 2, func(x, y int) color.NRGBA { return color.NRGBA{uint8(x * 200), uint8(y * 200), 7, 255} })
	out["flat"] = nrgba(123, 77, func(x, y int) color.NRGBA { return color.NRGBA{200, 100, 50, 255} })
	out["flat-big"] = nrgba(900, 700, func(x, y int) color.NRGBA { return color.NRGBA{1, 2, 3, 128} })
	out["noise"] = nrgba(150, 120, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256))}
	})
	out["noise-opaque"] = nrgba(333, 217, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255}
	})
	out["gradient300"] = nrgba(300, 300, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(x * 255 / 299), uint8(y * 255 / 299), uint8((x + y) * 255 / 598), 255}
	})
	out["2colours"] = nrgba(131, 60, func(x, y int) color.NRGBA {
		if (x/7+y/5)%2 == 0 {
			return color.NRGBA{255, 255, 255, 255}
		}
		return color.NRGBA{0, 0, 0, 255}
	})
	out["4colours"] = nrgba(130, 61, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(r.Intn(2) * 255), uint8(r.Intn(2) * 90), 0, 255}
	})
	out["16colours"] = nrgba(203, 155, func(x, y int) color.NRGBA {
		c := (x/9 + y/4*3 + r.Intn(2)) % 16
		return color.NRGBA{uint8(c * 17), uint8(255 - c*13), uint8(c * c), 255}
	})
	out["11colours-alpha"] = nrgba(64, 64, func(x, y int) color.NRGBA {
		c := (x ^ y) % 11
		return color.NRGBA{uint8(c * 20), uint8(c), 9, uint8(c * 25)}
	})
	out["100colours"] = nrgba(260, 180, func(x, y int) color.NRGBA {
		c := (x/13*7 + y/11*3 + r.Intn(3)) % 100
		return color.NRGBA{uint8(c * 2), uint8(c * 77), uint8(255 - c), 255}
	})
	// A smooth image of 256 colours: indices compress worse than the
	// colours predict, so it takes the ARGB path after the colour probe.
	out["gray-as-rgb"] = nrgba(310, 240, func(x, y int) color.NRGBA {
		v := uint8((x*x/400 + y*3/4 + r.Intn(3)) % 256)
		return color.NRGBA{v, v, v, 255}
	})
	// Few colours but more than 256, in runs: the colour cache and long
	// copies matter here.
	pal := make([]color.NRGBA, 700)
	for i := range pal {
		pal[i] = color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255}
	}
	out["700colours"] = nrgba(640, 500, func(x, y int) color.NRGBA {
		return pal[(x/5*31+y/3*17+r.Intn(4))%len(pal)]
	})
	// The 257th colour only appears near the bottom.
	out["late-colours"] = nrgba(180, 400, func(x, y int) color.NRGBA {
		if y < 380 {
			return color.NRGBA{uint8((x / 6) % 200), 0, uint8(y / 40), 255}
		}
		return color.NRGBA{uint8(x), uint8(y), uint8(x * y), 255}
	})
	// Repeated texture further back than one row, larger than the window
	// of the lowest effort, so the window slides.
	tile := nrgba(64, 64, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255}
	})
	out["tiled"] = nrgba(700, 520, func(x, y int) color.NRGBA { return tile.NRGBAAt(x%64, y%64) })
	// Busy, flat, busy: several Huffman groups, and whole block rows that
	// lie inside one long copy.
	out["noise-flat-noise"] = nrgba(300, 420, func(x, y int) color.NRGBA {
		if y >= 100 && y < 330 {
			return color.NRGBA{90, 90, 200, 255}
		}
		return color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(64)), uint8(x), 255}
	})
	out["noise-column"] = nrgba(1, 9000, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(y), 255}
	})
	out["noise-row"] = nrgba(9000, 1, func(x, y int) color.NRGBA {
		return color.NRGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(x), uint8(r.Intn(256))}
	})
	// Smooth in the top half, text-like in the bottom half.
	out["mixed"] = nrgba(400, 300, func(x, y int) color.NRGBA {
		if y < 150 {
			return color.NRGBA{uint8(x/2 + r.Intn(3)), uint8(y + r.Intn(3)), uint8((x + y) / 3), 255}
		}
		if (x/3+y/5)%4 == 0 || r.Intn(9) == 0 {
			return color.NRGBA{20, 20, uint8(40 + x%3*60), 255}
		}
		return color.NRGBA{250, 250, 245, 255}
	})
	out["transparent-edges"] = nrgba(200, 150, func(x, y int) color.NRGBA {
		if x < 20 || y < 20 || x > 180 {
			return color.NRGBA{}
		}
		return color.NRGBA{uint8(x), uint8(y), uint8(x + y), uint8(128 + x/2)}
	})
	gray := image.NewGray(image.Rect(0, 0, 500, 300))
	for i := range gray.Pix {
		gray.Pix[i] = uint8(i%500/2+i/500/3) + uint8(r.Intn(4))
	}
	out["gray-smooth"] = gray
	premul := image.NewRGBA(image.Rect(0, 0, 256, 256))
	for y := 0; y < 256; y++ {
		for x := 0; x < 256; x++ {
			// Every alpha with a range of colour values below it.
			premul.SetRGBA(x, y, color.RGBA{uint8(x * y / 255), uint8(x * (255 - y) / 255), uint8(x / 2), uint8(x)})
		}
	}
	out["premultiplied"] = premul
	rgba64 := image.NewRGBA64(image.Rect(0, 0, 90, 70))
	for y := 0; y < 70; y++ {
		for x := 0; x < 90; x++ {
			a := uint16(x * 65535 / 89)
			rgba64.SetRGBA64(x, y, color.RGBA64{uint16(int(a) * y / 69), a / 3, a, a})
		}
	}
	out["rgba64"] = rgba64
	alpha := image.NewAlpha(image.Rect(0, 0, 100, 40))
	alpha16 := image.NewAlpha16(image.Rect(0, 0, 100, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 100; x++ {
			alpha.SetAlpha(x, y, color.Alpha{uint8(x*3 + y)})
			alpha16.SetAlpha16(x, y, color.Alpha16{uint16(x*655 + y*7)})
		}
	}
	out["alpha"], out["alpha16"] = alpha, alpha16
	cmyk := image.NewCMYK(image.Rect(0, 0, 80, 60))
	for i := range cmyk.Pix {
		cmyk.Pix[i] = uint8(i*7) ^ uint8(r.Intn(8))
	}
	out["cmyk"] = cmyk
	nycbcra := image.NewNYCbCrA(image.Rect(0, 0, 70, 50), image.YCbCrSubsampleRatio420)
	for i := range nycbcra.Y {
		nycbcra.Y[i] = uint8(i)
	}
	for i := range nycbcra.Cb {
		nycbcra.Cb[i], nycbcra.Cr[i] = uint8(i*3), uint8(200-i)
	}
	for i := range nycbcra.A {
		nycbcra.A[i] = uint8(255 - i%70*4)
	}
	out["nycbcra"] = nycbcra
	return out
}

func sorted(m map[string]image.Image) []string {
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func TestRoundTripPixelTypes(t *testing.T) {
	imgs := testutil.Images(67, 131)
	for _, name := range sorted(imgs) {
		for _, n := range []int{1, 7, 16, 500} {
			roundTrip(t, fmt.Sprintf("%s/strips of %d", name, n), imgs[name], n, nil)
		}
	}
}

func TestRoundTripShapes(t *testing.T) {
	imgs := extraImages()
	for _, name := range sorted(imgs) {
		for _, n := range []int{1, 16, 500} {
			roundTrip(t, fmt.Sprintf("%s/strips of %d", name, n), imgs[name], n, nil)
		}
	}
}

func TestRoundTripEfforts(t *testing.T) {
	imgs := extraImages()
	for k, v := range testutil.Images(67, 131) {
		imgs[k] = v
	}
	for effort := 0; effort <= 9; effort++ {
		for _, name := range sorted(imgs) {
			roundTrip(t, fmt.Sprintf("%s/effort %d", name, effort), imgs[name], 16, &Options{Effort: effort})
		}
	}
}

// TestSubImageSource feeds sources whose Bounds do not start at (0,0).
func TestSubImageSource(t *testing.T) {
	imgs := testutil.Images(67, 131)
	for k, v := range extraImages() {
		imgs[k] = v
	}
	for _, name := range sorted(imgs) {
		m := imgs[name]
		b := m.Bounds()
		if b.Dx() < 8 || b.Dy() < 8 {
			continue
		}
		// Even offsets keep subsampled chroma aligned the way SubImage needs.
		r := image.Rect(b.Min.X+4, b.Min.Y+6, b.Max.X-3, b.Max.Y-1)
		sub := m.(interface {
			SubImage(image.Rectangle) image.Image
		}).SubImage(r)
		if sub.Bounds().Min == (image.Point{}) {
			t.Fatalf("%s: sub-image starts at the origin", name)
		}
		roundTrip(t, name+"/sub-image", sub, 5, nil)
	}
}

// genericImage hides the concrete type so the encoder has to use At.
type genericImage struct{ m image.Image }

func (g genericImage) Bounds() image.Rectangle { return g.m.Bounds() }
func (g genericImage) ColorModel() color.Model { return g.m.ColorModel() }
func (g genericImage) At(x, y int) color.Color { return g.m.At(x, y) }

type genericSource struct {
	pipeline.Source
}

func (g genericSource) Next() (image.Image, error) {
	m, err := g.Source.Next()
	if err != nil {
		return nil, err
	}
	return genericImage{m}, nil
}

func TestUnknownPixelType(t *testing.T) {
	m := testutil.Images(67, 131)["nrgba64"]
	data := encodeBytes(t, genericSource{testutil.Chopped(m, 9)}, nil)
	checkFile(t, "generic", data, m)
}

// opaqueSource claims to be opaque, as a JPEG decoder would.
type opaqueSource struct{ pipeline.Source }

func (opaqueSource) Opaque() bool { return true }

func TestOpaqueHint(t *testing.T) {
	m := testutil.Images(67, 131)["rgba-opaque"]
	data := encodeBytes(t, opaqueSource{testutil.Chopped(m, 9)}, nil)
	checkFile(t, "opaque source", data, m)
}

// TestPIL decodes the files with libwebp (through Pillow) and compares the
// pixels. It is skipped when python or Pillow's WebP support is missing.
func TestPIL(t *testing.T) {
	py, err := exec.LookPath("python")
	if err != nil {
		t.Skip("python not found")
	}
	if err := exec.Command(py, "-c", "import sys; from PIL import features; sys.exit(0 if features.check('webp') else 3)").Run(); err != nil {
		t.Skipf("Pillow with WebP support not available: %v", err)
	}
	dir := t.TempDir()
	imgs := extraImages()
	for k, v := range testutil.Images(67, 131) {
		imgs[k] = v
	}
	n := 0
	for _, name := range sorted(imgs) {
		for _, effort := range []int{1, 5, 9} {
			m := imgs[name]
			data := encodeBytes(t, testutil.Chopped(m, 13), &Options{Effort: effort})
			base := filepath.Join(dir, fmt.Sprintf("%s-e%d", name, effort))
			if err := os.WriteFile(base+".webp", data, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(base+".rgba", want(m).Pix, 0o644); err != nil {
				t.Fatal(err)
			}
			n++
		}
	}
	script := `
import sys, glob, os
from PIL import Image
bad = 0
n = 0
for p in sorted(glob.glob(os.path.join(sys.argv[1], "*.webp"))):
    raw = open(p[:-5] + ".rgba", "rb").read()
    try:
        im = Image.open(p)
        im.load()
        got = im.convert("RGBA").tobytes()
    except Exception as e:
        print("FAIL", os.path.basename(p), "cannot decode:", e)
        bad += 1
        continue
    n += 1
    if got != raw:
        print("FAIL", os.path.basename(p), "pixels differ", im.size, im.mode, len(got), len(raw))
        bad += 1
print("checked", n)
sys.exit(1 if bad else 0)
`
	out, err := exec.Command(py, "-c", script, dir).CombinedOutput()
	if err != nil {
		t.Fatalf("libwebp disagrees: %v\n%s", err, out)
	}
	if want := fmt.Sprintf("checked %d", n); !bytes.Contains(out, []byte(want)) {
		t.Fatalf("expected %q, got:\n%s", want, out)
	}
}

// photoLike is a deterministic image with smooth areas, edges and noise.
func photoLike(w, h int) *image.NRGBA {
	r := rand.New(rand.NewSource(5))
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			v := x*200/w + y*55/h
			if (x/97+y/61)%3 == 0 {
				v = 255 - v
			}
			n := r.Intn(9)
			i := m.PixOffset(x, y)
			m.Pix[i], m.Pix[i+1], m.Pix[i+2], m.Pix[i+3] = uint8(v+n), uint8(v*3/4+n), uint8(255-v+r.Intn(5)), 255
		}
	}
	return m
}

func TestSmallBudget(t *testing.T) {
	const limit = 2 << 20
	imgs := map[string]image.Image{"photo": photoLike(1500, 1500)}
	// The same size with 200 colours in runs: the index spill and the token
	// spill both go to disk.
	pal := image.NewPaletted(image.Rect(0, 0, 1500, 1500), nil)
	for i := 0; i < 200; i++ {
		pal.Palette = append(pal.Palette, color.NRGBA{uint8(i), uint8(i * 7), uint8(255 - i), 255})
	}
	r := rand.New(rand.NewSource(3))
	for i := range pal.Pix {
		pal.Pix[i] = uint8((i%1500/9*5 + i/1500/7*3 + r.Intn(3)) % 200)
	}
	imgs["paletted"] = pal
	for _, name := range sorted(imgs) {
		m := imgs[name]
		env := pipeline.NewEnv(limit, t.TempDir())
		data := encodeBytes(t, testutil.ChoppedEnv(m, 32, env), nil)
		checkFile(t, name, data, m)
		if p := env.Budget.Peak(); p > limit {
			t.Fatalf("%s: peak %d above the limit %d", name, p, limit)
		}
		if u := env.Budget.Used(); u != 0 {
			t.Fatalf("%s: %d bytes of budget still reserved", name, u)
		}
		leftovers(t, env.TempDir)
		t.Logf("%s: %d bytes, budget peak %d", name, len(data), env.Budget.Peak())
	}
}

// fakeSource reports any bounds and fails or ends where told to.
type fakeSource struct {
	b      image.Rectangle
	m      image.Image // strips come from here
	n      int         // rows per strip
	y      int
	stopAt int // row at which Next returns err
	err    error
	calls  int
}

func (f *fakeSource) Bounds() image.Rectangle { return f.b }
func (f *fakeSource) ColorModel() color.Model { return color.NRGBAModel }
func (f *fakeSource) Close() error            { panic("Encode must not close the source") }
func (f *fakeSource) Env() *pipeline.Env      { return nil }
func (f *fakeSource) Next() (image.Image, error) {
	f.calls++
	if f.y >= f.stopAt {
		return nil, f.err
	}
	r := image.Rect(f.b.Min.X, f.y, f.b.Max.X, min(f.y+f.n, f.b.Max.Y))
	f.y = r.Max.Y
	return f.m.(*image.NRGBA).SubImage(r), nil
}

func TestErrors(t *testing.T) {
	boom := errors.New("boom")
	m := photoLike(120, 90)
	cases := []struct {
		name string
		src  *fakeSource
		opt  *Options
		is   error
		used bool // whether Next may be called
	}{
		{"zero width", &fakeSource{b: image.Rect(0, 0, 0, 5)}, nil, nil, false},
		{"zero height", &fakeSource{b: image.Rect(3, 3, 9, 3)}, nil, nil, false},
		{"too wide", &fakeSource{b: image.Rect(0, 0, 16385, 5)}, nil, nil, false},
		{"too tall", &fakeSource{b: image.Rect(0, 0, 5, 16385)}, nil, nil, false},
		{"bad effort", &fakeSource{b: m.Bounds(), m: m, n: 8, stopAt: 90, err: io.EOF}, &Options{Effort: 10}, nil, false},
		{"negative effort", &fakeSource{b: m.Bounds(), m: m, n: 8, stopAt: 90, err: io.EOF}, &Options{Effort: -1}, nil, false},
		{"fails at once", &fakeSource{b: m.Bounds(), m: m, n: 8, stopAt: 0, err: boom}, nil, boom, true},
		{"fails midway", &fakeSource{b: m.Bounds(), m: m, n: 8, stopAt: 40, err: boom}, nil, boom, true},
		{"fails at the end", &fakeSource{b: m.Bounds(), m: m, n: 8, stopAt: 90, err: boom}, nil, boom, true},
		{"empty", &fakeSource{b: m.Bounds(), m: m, n: 8, stopAt: 0, err: io.EOF}, nil, io.ErrUnexpectedEOF, true},
		{"ends early", &fakeSource{b: m.Bounds(), m: m, n: 8, stopAt: 40, err: io.EOF}, nil, io.ErrUnexpectedEOF, true},
		{"too many rows", &fakeSource{b: image.Rect(0, 0, 120, 50), m: m, n: 8, stopAt: 90, err: io.EOF}, nil, nil, true},
	}
	for _, c := range cases {
		env := pipeline.NewEnv(0, t.TempDir())
		var buf bytes.Buffer
		err := Encode(&buf, envSource{c.src, env}, c.opt)
		if err == nil {
			t.Fatalf("%s: no error", c.name)
		}
		if c.is != nil && !errors.Is(err, c.is) {
			t.Fatalf("%s: error %v, want %v", c.name, err, c.is)
		}
		if !c.used && c.src.calls != 0 {
			t.Fatalf("%s: source was read", c.name)
		}
		if u := env.Budget.Used(); u != 0 {
			t.Fatalf("%s: %d bytes of budget still reserved", c.name, u)
		}
		leftovers(t, env.TempDir)
	}

	// A gray source that changes pixel type half-way, a palette index past
	// the palette, and a budget too small for even the row buffers.
	g := image.NewGray(image.Rect(0, 0, 40, 40))
	mixed := &mixedSource{b: g.Bounds(), strips: []image.Image{
		g.SubImage(image.Rect(0, 0, 40, 20)), m.SubImage(image.Rect(0, 20, 40, 40))}}
	if err := Encode(io.Discard, mixed, nil); err == nil {
		t.Fatal("mixed pixel types: no error")
	}
	p := image.NewPaletted(image.Rect(0, 0, 10, 10), color.Palette{color.Black, color.White})
	p.Pix[55] = 2
	if err := Encode(io.Discard, testutil.Chopped(p, 4), nil); err == nil {
		t.Fatal("palette index out of range: no error")
	}
	env := pipeline.NewEnv(64<<10, t.TempDir())
	err := Encode(io.Discard, testutil.ChoppedEnv(photoLike(4000, 64), 8, env), nil)
	if !errors.Is(err, pipeline.ErrMemoryLimit) {
		t.Fatalf("tiny budget: error %v, want ErrMemoryLimit", err)
	}
	if u := env.Budget.Used(); u != 0 {
		t.Fatalf("tiny budget: %d bytes still reserved", u)
	}
	leftovers(t, env.TempDir)
}

type envSource struct {
	pipeline.Source
	env *pipeline.Env
}

func (e envSource) Env() *pipeline.Env { return e.env }

type mixedSource struct {
	b      image.Rectangle
	strips []image.Image
}

func (s *mixedSource) Bounds() image.Rectangle { return s.b }
func (s *mixedSource) ColorModel() color.Model { return color.NRGBAModel }
func (s *mixedSource) Close() error            { return nil }
func (s *mixedSource) Next() (image.Image, error) {
	if len(s.strips) == 0 {
		return nil, io.EOF
	}
	m := s.strips[0]
	s.strips = s.strips[1:]
	return m, nil
}

// TestCompresses guards against the encoder falling back to something
// trivially bad: the bounds are far above what it produces today.
func TestCompresses(t *testing.T) {
	imgs := extraImages()
	for name, limit := range map[string]int{
		"flat-big":     200,   // 900x700 of one colour
		"2colours":     300,   // 131x60 checkerboard
		"tiled":        30000, // a 64x64 noise tile (12 KB raw) repeated
		"gradient300":  20000, // 270 KB raw
		"gray-smooth":  60000, // 150 KB raw, two bits of noise per pixel
		"late-colours": 12000,
	} {
		data := encodeBytes(t, testutil.Chopped(imgs[name], 16), nil)
		if len(data) > limit {
			t.Errorf("%s: %d bytes, expected at most %d", name, len(data), limit)
		}
	}
	// More effort must not cost much: the highest level may lose a little
	// to the default on some images, never a lot.
	m := photoLike(500, 400)
	var sizes []int
	for _, effort := range []int{1, 5, 9} {
		sizes = append(sizes, len(encodeBytes(t, testutil.Chopped(m, 16), &Options{Effort: effort})))
	}
	if sizes[1] > sizes[0] || sizes[2] > sizes[1]+sizes[1]/50 {
		t.Errorf("sizes at effort 1, 5, 9: %v", sizes)
	}
}

// TestFailingWriter makes sure write errors come back.
func TestFailingWriter(t *testing.T) {
	m := photoLike(300, 200)
	full := encodeBytes(t, testutil.Chopped(m, 16), nil)
	for _, limit := range []int{0, 10, 20, 100, len(full) - 1} {
		err := Encode(&limitedWriter{n: limit}, testutil.Chopped(m, 16), nil)
		if err == nil {
			t.Fatalf("writer failing after %d bytes: no error", limit)
		}
	}
}

type limitedWriter struct{ n int }

func (l *limitedWriter) Write(p []byte) (int, error) {
	if len(p) > l.n {
		n := l.n
		l.n = 0
		return n, errors.New("disk full")
	}
	l.n -= len(p)
	return len(p), nil
}

func TestPrefixCode(t *testing.T) {
	// Decode side of section 5.2.2, as in x/image/vp8l.
	decode := func(sym, extra uint32) uint32 {
		if sym < 4 {
			return sym + 1
		}
		eb := (sym - 2) >> 1
		return (2+sym&1)<<eb + extra + 1
	}
	for v := uint32(1); v <= 1<<20; v++ {
		sym, nb, extra := prefixCode(v)
		if sym >= 40 || extra >= 1<<nb || decode(sym, extra) != v {
			t.Fatalf("prefixCode(%d) = %d, %d, %d", v, sym, nb, extra)
		}
		if sym >= 4 && nb != uint((sym-2)>>1) {
			t.Fatalf("prefixCode(%d): %d extra bits for symbol %d", v, nb, sym)
		}
	}
	if sym, _, _ := prefixCode(maxMatch); sym != numLength-1 {
		t.Fatalf("longest match uses length symbol %d", sym)
	}
}

func TestDistCode(t *testing.T) {
	// The decoder's mapping, as in x/image/vp8l.
	decode := func(w int, code uint32) int {
		if code > 120 {
			return int(code) - 120
		}
		dc := int(distMap[code-1])
		if d := (dc>>4)*w + 8 - dc&0xf; d >= 1 {
			return d
		}
		return 1
	}
	for _, w := range []int{1, 2, 3, 7, 8, 9, 15, 16, 17, 100, 4000} {
		short := 0
		for d := 1; d <= 9*w+20; d++ {
			c := distCode(d, w)
			if c == 0 || decode(w, c) != d {
				t.Fatalf("width %d: distance %d coded as %d, which decodes to %d", w, d, c, decode(w, c))
			}
			if c <= 120 {
				short++
			}
		}
		if w >= 17 && short != 120 {
			t.Fatalf("width %d: %d distances got a short code, want 120", w, short)
		}
	}
}

func TestCodeLengths(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	for iter := 0; iter < 3000; iter++ {
		n := 1 + r.Intn(600)
		hist := make([]uint32, n)
		for i := range hist {
			switch r.Intn(4) {
			case 0:
				hist[i] = uint32(r.Intn(5))
			case 1:
				hist[i] = uint32(1) << uint(r.Intn(31)) // forces very deep trees
			case 2:
				hist[i] = uint32(r.Intn(1000))
			}
		}
		limit := 15
		if n <= 19 && iter%2 == 0 {
			limit = 7
		}
		lens := make([]uint8, n)
		codeLengths(hist, limit, lens)
		used, kraft := 0, 0
		for i, l := range lens {
			if (l != 0) != (hist[i] != 0) || int(l) > limit {
				t.Fatalf("symbol %d: count %d, length %d", i, hist[i], l)
			}
			if l != 0 {
				used++
				kraft += 1 << (limit - int(l))
			}
		}
		if used >= 2 && kraft != 1<<limit {
			t.Fatalf("%d symbols: incomplete or over-subscribed code (%d / %d)", used, kraft, 1<<limit)
		}
	}
}

func (s *mixedSource) Env() *pipeline.Env { return nil }

// TestThreads: the file is the same whatever the number of helpers
// choosing predictors and making residuals at once, and decodes to the
// image.
func TestThreads(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	photo := image.NewNRGBA(image.Rect(0, 0, 700, 523))
	for y := 0; y < 523; y++ {
		for x := 0; x < 700; x++ {
			n := uint8(r.Intn(12))
			photo.SetNRGBA(x, y, color.NRGBA{uint8(x/3) + n, uint8(y/3) + n, uint8((x+y)/5) + n, 255})
		}
	}
	imgs := map[string]image.Image{"photo": photo}
	for name, m := range testutil.Images(301, 207) {
		imgs[name] = m
	}
	for name, m := range imgs {
		for _, effort := range []int{1, 5, 9} {
			o := &Options{Effort: effort}
			ref := encodeBytes(t, testutil.Chopped(m, 16), o)
			for _, helpers := range []int{1, 3, 7} {
				env := pipeline.NewEnv(0, t.TempDir())
				env.SetHelpers(helpers)
				got := encodeBytes(t, testutil.ChoppedEnv(m, 11, env), o)
				if !bytes.Equal(got, ref) {
					t.Errorf("%s effort %d with %d helpers: another file", name, effort, helpers)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: %d bytes reserved", name, env.Budget.Used())
				}
			}
			checkFile(t, name, ref, m)
		}
	}
}
