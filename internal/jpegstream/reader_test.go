package jpegstream

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

func corpus(t *testing.T) map[string][]byte {
	dir := filepath.Join(runtime.GOROOT(), "src", "image", "testdata")
	files, _ := filepath.Glob(filepath.Join(dir, "*.jpeg"))
	if len(files) < 25 {
		t.Fatalf("stdlib testdata not found in %s", dir)
	}
	out := map[string][]byte{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		out[filepath.Base(f)] = data
	}
	return out
}

func decodeStream(data []byte, env *pipeline.Env) (image.Image, error) {
	m, _, err := decodeMode(data, env, false)
	return m, err
}

// decodeMode decodes data, with random access to it when random is set
// (which decodes a progressive image's scans in step), and reports whether
// a scan ended inside an end-of-band run.
func decodeMode(data []byte, env *pipeline.Env, random bool) (m image.Image, overrun bool, err error) {
	src, err := Open(bytes.NewReader(data), env)
	if err != nil {
		return nil, false, err
	}
	defer src.Close()
	if random {
		WithRandomAccess(src, func() (io.ReaderAt, int64, error) { return bytes.NewReader(data), int64(len(data)), nil })
	}
	m, err = pipeline.Collect(src)
	return m, src.(*stream).d.overrun, err
}

// refDecode is the decoding the others are held to: jpeg.Decode's verdict
// (an error, or none) and, where it decodes, this decoder's pixels in its
// plainest mode (bands of automatic height, everything in memory, the input
// read once). The pixels themselves are held to libjpeg-turbo's (Pillow's)
// by TestPillowCorpus.
func refDecode(t *testing.T, data []byte) (image.Image, error) {
	t.Helper()
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		return nil, err
	}
	rows, cursors, noStep := testMCURows, maxCursors, testNoStep
	testMCURows, maxCursors, testNoStep = 0, 1024, false
	defer func() { testMCURows, maxCursors, testNoStep = rows, cursors, noStep }()
	m, err := decodeStream(data, pipeline.NewEnv(0, ""))
	if err != nil {
		t.Fatalf("image/jpeg decodes it, this decoder: %v", err)
	}
	return m, nil
}

func same(t *testing.T, name string, want, got image.Image) {
	t.Helper()
	if reflect.TypeOf(want) != reflect.TypeOf(got) {
		t.Fatalf("%s: type %T, want %T", name, got, want)
	}
	if want.Bounds() != got.Bounds() {
		t.Fatalf("%s: bounds %v, want %v", name, got.Bounds(), want.Bounds())
	}
	if w, ok := want.(*image.YCbCr); ok && w.SubsampleRatio != got.(*image.YCbCr).SubsampleRatio {
		t.Fatalf("%s: ratio %v, want %v", name, got.(*image.YCbCr).SubsampleRatio, w.SubsampleRatio)
	}
	b := want.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if want.At(x, y) != got.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) = %v, want %v", name, x, y, got.At(x, y), want.At(x, y))
			}
		}
	}
}

// TestStdlibCorpus decodes Go's JPEG test files (baseline, progressive,
// every subsampling, CMYK, RGB, restart markers, separate scans) with bands
// of one MCU row, of automatic height, and with the coefficient store forced
// to disk; and with random access to the input (the scans decoded in step,
// or, with too little memory or too many scans for that, read one after the
// other).
func TestStdlibCorpus(t *testing.T) {
	files := corpus(t)
	for _, mode := range []string{"auto", "one-mcu-row", "disk", "random", "random-one-mcu-row", "random-disk", "random-few-cursors", "random-store"} {
		testMCURows = 0
		maxCursors = 1024
		if mode == "random-few-cursors" {
			maxCursors = 2
		}
		testNoStep = mode == "random-store"
		var dir string
		if mode != "auto" && mode != "random" {
			testMCURows = 1
		}
		for name, data := range files {
			want, werr := refDecode(t, data)
			env := pipeline.NewEnv(0, "")
			if mode == "disk" || mode == "random-disk" {
				dir = t.TempDir()
				env = pipeline.NewEnv(96<<10, dir)
			}
			got, _, gerr := decodeMode(data, env, strings.HasPrefix(mode, "random"))
			if werr != nil {
				if gerr == nil {
					t.Errorf("%s/%s: stdlib error %v, stream decoded fine", mode, name, werr)
				}
				continue
			}
			if gerr != nil {
				t.Errorf("%s/%s: %v", mode, name, gerr)
				continue
			}
			same(t, mode+"/"+name, want, got)
			if mode == "disk" || mode == "random-disk" {
				if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
					t.Errorf("%s: temp files left behind: %v", name, left)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: %d bytes still reserved after Close", name, env.Budget.Used())
				}
			}
		}
	}
	testMCURows = 0
	maxCursors = 1024
	testNoStep = false
}

// TestProgressiveUsesDisk checks the small budget above really sends the
// coefficients to a temporary file.
func TestProgressiveUsesDisk(t *testing.T) {
	data := corpus(t)["video-001.progressive.jpeg"]
	env := pipeline.NewEnv(96<<10, t.TempDir())
	src, err := Open(bytes.NewReader(data), env)
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	if _, err := src.Next(); err != nil {
		t.Fatal(err)
	}
	st := src.(*stream)
	if !st.Buffered() || !st.d.coef.sc.OnDisk() {
		t.Fatalf("buffered=%v onDisk=%v, want both true", st.Buffered(), st.d.coef.sc.OnDisk())
	}
}

// TestEncodedImages decodes images made by the standard encoder that span
// many bands.
func TestEncodedImages(t *testing.T) {
	for _, sz := range [][2]int{{397, 611}, {1203, 517}, {16, 16}, {1, 1}, {17, 9}} {
		imgs := testutil.Images(sz[0], sz[1])
		for _, name := range []string{"rgba-opaque", "gray", "ycbcr420", "ycbcr444"} {
			var buf bytes.Buffer
			if err := jpeg.Encode(&buf, imgs[name], &jpeg.Options{Quality: 90}); err != nil {
				t.Fatal(err)
			}
			want, err := refDecode(t, buf.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			testMCURows = 1
			got, err := decodeStream(buf.Bytes(), nil)
			testMCURows = 0
			if err != nil {
				t.Fatalf("%s %v: %v", name, sz, err)
			}
			same(t, name, want, got)
		}
	}
}

// TestTruncatedAndCorrupt requires an error wherever jpeg.Decode errors, and
// success with the same pixels wherever it succeeds.
func TestTruncatedAndCorrupt(t *testing.T) {
	files := corpus(t)
	for _, name := range []string{
		"video-001.jpeg", "video-001.progressive.jpeg", "video-001.restart2.jpeg",
		"video-005.gray.jpeg", "video-001.cmyk.jpeg", "video-001.separate.dc.progression.jpeg",
		"video-001.q50.411.progressive.jpeg", "video-001.221212.jpeg",
	} {
		data := files[name]
		if data == nil {
			t.Fatalf("missing %s", name)
		}
		check := func(what string, in []byte) {
			want, werr := refDecode(t, in)
			for _, random := range []bool{false, true} {
				got, overrun, gerr := decodeMode(in, nil, random)
				if werr != nil {
					if gerr == nil {
						t.Fatalf("%s %s (random %v): stdlib error %v, stream decoded fine", name, what, random, werr)
					}
					continue
				}
				if gerr != nil {
					t.Fatalf("%s %s (random %v): stdlib decoded fine, stream error %v", name, what, random, gerr)
				}
				// Where an end-of-band run overran its scan, the
				// standard library carries it on, libjpeg and this
				// package do not.
				if !overrun {
					same(t, name+" "+what, want, got)
				}
			}
		}
		for cut := 2; cut < len(data); cut += 53 {
			check("cut", data[:cut])
		}
		for i := 20; i < len(data); i += 37 {
			bad := append([]byte(nil), data...)
			bad[i] ^= 0x5a
			check("flip", bad)
		}
	}
}

// TestScansInStep checks that with random access a progressive image, and
// one whose components are in separate scans, is decoded with its scans in
// step, without a coefficient store, when there is the memory for it.
func TestScansInStep(t *testing.T) {
	files := corpus(t)
	for _, name := range []string{"video-001.progressive.jpeg", "video-001.separate.dc.progression.jpeg",
		"video-001.q50.420.progressive.jpeg", "video-005.gray.q50.progressive.jpeg"} {
		data := files[name]
		src, err := Open(bytes.NewReader(data), pipeline.NewEnv(0, t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		WithRandomAccess(src, func() (io.ReaderAt, int64, error) { return bytes.NewReader(data), int64(len(data)), nil })
		if _, err := src.Next(); err != nil {
			t.Fatal(err)
		}
		st := src.(*stream)
		if st.d.rowCoef[0] == nil || st.d.coef != nil || len(st.cursors) < 2 {
			t.Errorf("%s: rowCoef %v, coef %v, %d cursors", name, st.d.rowCoef[0] != nil, st.d.coef != nil, len(st.cursors))
		}
		src.Close()

		// Without the memory for that, the scans are read one after the
		// other into the coefficient store, by their cursors.
		src, err = Open(bytes.NewReader(data), pipeline.NewEnv(96<<10, t.TempDir()))
		if err != nil {
			t.Fatal(err)
		}
		WithRandomAccess(src, func() (io.ReaderAt, int64, error) { return bytes.NewReader(data), int64(len(data)), nil })
		if _, err := src.Next(); err != nil {
			t.Fatal(err)
		}
		st = src.(*stream)
		if st.d.rowCoef[0] != nil || st.d.coef == nil || len(st.cursors) < 1 {
			t.Errorf("%s, low limit: rowCoef %v, coef %v, %d cursors", name, st.d.rowCoef[0] != nil, st.d.coef != nil, len(st.cursors))
		}
		src.Close()
	}
}

// TestManyTables decodes a progressive image of 2000 scans, each after a
// DHT segment of four tables: the tables the cursors keep count against the
// share of the memory limit that may be staged, and the scans past it are
// read on, from Open's reader.
func TestManyTables(t *testing.T) {
	var f bytes.Buffer
	f.Write([]byte{0xff, 0xd8})
	// A 1-bit DC code for size 0, in each of the four DC and four AC slots.
	table := func(id byte) []byte { return append([]byte{id, 1}, append(make([]byte, 15), 0)...) }
	f.Write([]byte{0xff, 0xdb, 0, 67, 0})
	f.Write(bytes.Repeat([]byte{1}, 64))
	f.Write([]byte{0xff, 0xc2, 0, 11, 8, 0, 8, 0, 8, 1, 1, 0x11, 0})
	for i := 0; i < 2000; i++ {
		var dht []byte
		for _, id := range []byte{0x00, 0x01, 0x02, 0x03} {
			dht = append(dht, table(id)...)
		}
		f.Write([]byte{0xff, 0xc4, byte((len(dht) + 2) >> 8), byte(len(dht) + 2)})
		f.Write(dht)
		f.Write([]byte{0xff, 0xda, 0, 8, 1, 1, 0x00, 0, 0, 0})
		f.Write([]byte{0x7f}) // the code 0, then padding
	}
	f.Write([]byte{0xff, 0xd9})
	data := f.Bytes()
	want, werr := refDecode(t, data)
	if werr != nil {
		t.Fatal(werr)
	}
	env := pipeline.NewEnv(0, t.TempDir())
	src, err := Open(bytes.NewReader(data), env)
	if err != nil {
		t.Fatal(err)
	}
	WithRandomAccess(src, func() (io.ReaderAt, int64, error) { return bytes.NewReader(data), int64(len(data)), nil })
	got, err := pipeline.Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	st := src.(*stream)
	if st.rest == nil || len(st.cursors) >= 2000 || st.held > env.Budget.Limit()/pipeline.StagingShare+cursorBytes+4*tableBytes {
		t.Errorf("%d cursors, rest %v, %d bytes held", len(st.cursors), st.rest != nil, st.held)
	}
	src.Close()
	same(t, "many tables", want, got)
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved", env.Budget.Used())
	}
}

// TestFillBytesAfterScan: fill bytes after a scan followed by 0x00 are an
// error for the standard library (an unknown marker), and so with the scans
// planned too.
func TestFillBytesAfterScan(t *testing.T) {
	data := bytes.Clone(corpus(t)["video-001.q50.444.progressive.jpeg"])
	if data[2193] != 0xff || data[2194] != 0xda {
		t.Fatalf("bytes 2193-2194 are %x, want a SOS marker", data[2193:2195])
	}
	data[2194] = 0xff
	if _, err := jpeg.Decode(bytes.NewReader(data)); err == nil {
		t.Fatal("the standard library decodes it")
	}
	for _, random := range []bool{false, true} {
		if _, _, err := decodeMode(data, nil, random); err == nil {
			t.Errorf("random %v: decoded", random)
		}
	}
}

// pillowCorpus is the SHA-256 (its first 16 hex digits) of the RGB pixels
// Pillow 12.1.1 (libjpeg-turbo) decodes Go's JPEG test files to.
var pillowCorpus = map[string]string{
	"video-001.221212.jpeg":                              "f3272ae74a3cffed",
	"video-001.jpeg":                                     "5df7ed5f6d91eb6f",
	"video-001.progressive.jpeg":                         "5df7ed5f6d91eb6f",
	"video-001.q50.121121.jpeg":                          "00a127b2d52cd710",
	"video-001.q50.211211.jpeg":                          "fa95323e4a632f2c",
	"video-001.q50.221122.jpeg":                          "a00bb4cf047462f0",
	"video-001.q50.222112.jpeg":                          "88a4e0a056b8772e",
	"video-001.q50.410.jpeg":                             "6883a5ad925c211c",
	"video-001.q50.410.progressive.jpeg":                 "6883a5ad925c211c",
	"video-001.q50.411.jpeg":                             "75c569670c9826c1",
	"video-001.q50.411.progressive.jpeg":                 "75c569670c9826c1",
	"video-001.q50.420.jpeg":                             "34c30048db657703",
	"video-001.q50.420.progressive.jpeg":                 "34c30048db657703",
	"video-001.q50.422.jpeg":                             "973938af4699d5cc",
	"video-001.q50.422.progressive.jpeg":                 "973938af4699d5cc",
	"video-001.q50.440.jpeg":                             "c7b186405bd89744",
	"video-001.q50.440.progressive.jpeg":                 "c7b186405bd89744",
	"video-001.q50.444.jpeg":                             "4870a71780d96c99",
	"video-001.q50.444.progressive.jpeg":                 "4870a71780d96c99",
	"video-001.restart2.jpeg":                            "5f021700b8dea9da",
	"video-001.rgb.jpeg":                                 "1dc7b1d4075366f5",
	"video-001.separate.dc.progression.jpeg":             "523f1f1024d53793",
	"video-001.separate.dc.progression.progressive.jpeg": "523f1f1024d53793",
	"video-005.gray.jpeg":                                "c289899392b656bc",
	"video-005.gray.q50.2x2.jpeg":                        "2d5942114f3e03e5",
	"video-005.gray.q50.2x2.progressive.jpeg":            "2d5942114f3e03e5",
	"video-005.gray.q50.jpeg":                            "2d5942114f3e03e5",
	"video-005.gray.q50.progressive.jpeg":                "2d5942114f3e03e5",
}

// TestPillowCorpus: the pixels are libjpeg-turbo's, as Pillow decodes Go's
// JPEG test files to; but for the files whose components are sampled in
// ways no encoder chooses (luma at half the width of a chroma component,
// say), which are decoded as the standard library does, each component
// repeated to full size.
func TestPillowCorpus(t *testing.T) {
	flex := map[string]bool{"video-001.q50.121121.jpeg": true, "video-001.q50.211211.jpeg": true,
		"video-001.q50.221122.jpeg": true, "video-001.q50.222112.jpeg": true}
	for name, data := range corpus(t) {
		want, ok := pillowCorpus[name]
		if !ok || flex[name] {
			continue
		}
		m, err := decodeStream(data, nil)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		b := m.Bounds()
		rgb := make([]byte, 0, 3*b.Dx()*b.Dy())
		for y := b.Min.Y; y < b.Max.Y; y++ {
			for x := b.Min.X; x < b.Max.X; x++ {
				r, g, bl, _ := m.At(x, y).RGBA()
				rgb = append(rgb, byte(r>>8), byte(g>>8), byte(bl>>8))
			}
		}
		if got := fmt.Sprintf("%x", sha256.Sum256(rgb))[:16]; got != want {
			t.Errorf("%s: pixels %s, Pillow's %s", name, got, want)
		}
	}
}

// TestKeepYCbCr: with KeepYCbCr the strips are the YCbCr the RGB is made
// from; that YCbCr converted in one go, as a single band, is the RGB the
// decoder hands out band by band, the row held back at the end of each band
// and all (bands of one MCU row, every subsampling, odd sizes).
func TestKeepYCbCr(t *testing.T) {
	for _, ratio := range []int{Subsample444, Subsample422, Subsample420} {
		for _, sz := range [][2]int{{1, 1}, {3, 5}, {37, 23}, {201, 97}} {
			m := image.NewRGBA(image.Rect(0, 0, sz[0], sz[1]))
			for i := range m.Pix {
				m.Pix[i] = uint8(i * 37 % 251)
			}
			var buf bytes.Buffer
			if err := Encode(&buf, pipeline.FromImage(m, nil), &Options{Quality: 90, Subsampling: ratio}); err != nil {
				t.Fatal(err)
			}
			data := buf.Bytes()
			testMCURows = 1
			rgb, err := decodeStream(data, nil)
			if err != nil {
				t.Fatal(err)
			}
			src, err := Open(bytes.NewReader(data), nil)
			if err != nil {
				t.Fatal(err)
			}
			src.(*stream).KeepYCbCr()
			raw, err := pipeline.Collect(src)
			src.Close()
			testMCURows = 0
			if err != nil {
				t.Fatal(err)
			}
			y, ok := raw.(*image.YCbCr)
			if !ok {
				t.Fatalf("%v %v: KeepYCbCr gave %T", ratio, sz, raw)
			}
			u := newUpsampler(y.SubsampleRatio, sz[0], sz[1], false, true, nil)
			whole := image.NewRGBA(image.Rect(0, 0, sz[0], sz[1]))
			band := *y
			band.Rect = image.Rect(0, 0, sz[0], sz[1])
			u.band(&band, 0, sz[1], true, whole)
			same(t, fmt.Sprintf("%v %v", ratio, sz), whole, rgb)
		}
	}
}
