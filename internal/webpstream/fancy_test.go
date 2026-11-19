package webpstream

import (
	"bytes"
	"hash/fnv"
	"image"
	"os"
	"path/filepath"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestToRGB checks the decoded pixels of lossy files against libwebp's:
// the sums are FNV-64a of the RGBA rows that dwebp 1.6.0 writes (-pam).
// The files are decoded in strips of different heights, since a row at
// the end of a strip needs chroma from the next.
func TestToRGB(t *testing.T) {
	type file struct {
		path string
		want uint64
	}
	files := []file{
		{"../../tests/testdata/webp/odd.lossy.webp", 0xe841777f643791e3},       // 37x23
		{"../../tests/testdata/webp/odd-alpha.lossy.webp", 0x2ee907d587e26dee}, // 33x17, alpha
	}
	if dir := testutil.XImageTestdata(); dir != "" {
		for name, want := range map[string]uint64{
			"blue-purple-pink.lossy.webp":                     0xd18bf8d0d016b2af,
			"blue-purple-pink-large.normal-filter.lossy.webp": 0xa36013719a01537a,
			"blue-purple-pink-large.simple-filter.lossy.webp": 0x83a92cd37f30a019,
			"video-001.lossy.webp":                            0xf4d576d2b0086d9e,
			"yellow_rose.lossy.webp":                          0x46fef47dc41a5a47,
			"yellow_rose.lossy-with-alpha.webp":               0xcbf0b2d9184ff847,
		} {
			files = append(files, file{filepath.Join(dir, name), want})
		}
	} else {
		t.Log("golang.org/x/image testdata not found; testing the files of this module only")
	}
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range [][2]int{{0, 0}, {16, 0}, {32, 0}, {48, 0}, {16, 1}, {32, 5}, {0, 7}} {
			rows := c[0]
			setBandRows(t, rows)
			old := testRGBRows
			testRGBRows = c[1]
			env := pipeline.NewEnv(0, t.TempDir())
			src, err := Open(bytes.NewReader(data), int64(len(data)), env)
			if err != nil {
				t.Fatal(err)
			}
			rgb := ToRGB(src)
			m, err := pipeline.Collect(rgb)
			if err != nil {
				t.Fatalf("%s: %v", f.path, err)
			}
			rgb.Close()
			testRGBRows = old
			n, ok := m.(*image.NRGBA)
			if !ok {
				t.Fatalf("%s: decoded to %T", f.path, m)
			}
			h := fnv.New64a()
			b := n.Bounds()
			for y := b.Min.Y; y < b.Max.Y; y++ {
				h.Write(n.Pix[n.PixOffset(b.Min.X, y):][:4*b.Dx()])
			}
			if got := h.Sum64(); got != f.want {
				t.Errorf("%s, strips of %d rows, out in %d: sum %016x, want %016x", filepath.Base(f.path), rows, c[1], got, f.want)
			}
			if env.Budget.Used() != 0 {
				t.Errorf("%s: %d bytes still reserved", f.path, env.Budget.Used())
			}
		}
	}
	// NRGBA (a lossless image) is handed through untouched.
	n := pipeline.FromImage(image.NewNRGBA(image.Rect(0, 0, 4, 4)), nil)
	if ToRGB(n) != n {
		t.Error("an NRGBA source was wrapped")
	}
}

// TestSameWithHelpers: with helpers (a lossy image's next band decoded
// while the last is used and each row reconstructed while the next is
// parsed, the conversion to RGB shared out) the pixels are the same as
// without, in strips of any height, decoded whole and scaled on load.
func TestSameWithHelpers(t *testing.T) {
	paths := []string{"../../tests/testdata/webp/odd.lossy.webp", "../../tests/testdata/webp/odd-alpha.lossy.webp", "../../tests/testdata/sharp/resize-lossy.webp"}
	if dir := testutil.XImageTestdata(); dir != "" {
		for _, name := range []string{"blue-purple-pink-large.normal-filter.lossy.webp", "blue-purple-pink-large.simple-filter.lossy.webp", "video-001.lossy.webp", "yellow_rose.lossy-with-alpha.webp"} {
			paths = append(paths, filepath.Join(dir, name))
		}
	}
	decode := func(data []byte, helpers, rows int) []byte {
		setBandRows(t, rows)
		env := pipeline.NewEnv(0, t.TempDir())
		env.SetHelpers(helpers)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			t.Fatal(err)
		}
		rgb := ToRGB(src)
		m, err := pipeline.Collect(rgb)
		if err != nil {
			t.Fatal(err)
		}
		rgb.Close()
		if env.Budget.Used() != 0 {
			t.Errorf("%d bytes still reserved", env.Budget.Used())
		}
		return m.(*image.NRGBA).Pix
	}
	// scaled decodes at a third of the size (the loop filter skipped, see
	// ScaleOnLoad).
	scaled := func(data []byte, helpers int) []byte {
		setBandRows(t, 16)
		env := pipeline.NewEnv(0, t.TempDir())
		env.SetHelpers(helpers)
		src, err := Open(bytes.NewReader(data), int64(len(data)), env)
		if err != nil {
			t.Fatal(err)
		}
		b := src.Bounds()
		if !src.(*source).ScaleOnLoad(max(1, b.Dx()/3), max(1, b.Dy()/3)) {
			t.Fatal("not scaled")
		}
		m, err := pipeline.Collect(src)
		if err != nil {
			t.Fatal(err)
		}
		src.Close()
		if env.Budget.Used() != 0 {
			t.Errorf("%d bytes still reserved", env.Budget.Used())
		}
		return m.(*image.NRGBA).Pix
	}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		want := decode(data, 0, 0)
		for _, h := range []int{1, 3} {
			for _, rows := range []int{0, 16, 48} {
				if got := decode(data, h, rows); !bytes.Equal(got, want) {
					t.Errorf("%s, %d helpers, strips of %d rows: other pixels", filepath.Base(p), h, rows)
				}
			}
		}
		want = scaled(data, 0)
		for _, h := range []int{1, 3} {
			if got := scaled(data, h); !bytes.Equal(got, want) {
				t.Errorf("%s, %d helpers, scaled on load: other pixels", filepath.Base(p), h)
			}
		}
	}
}
