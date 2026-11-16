package jpegstream

import (
	"bytes"
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
	for _, mode := range []string{"auto", "one-mcu-row", "disk", "random", "random-one-mcu-row", "random-disk", "random-few-cursors"} {
		testMCURows = 0
		maxCursors = 1024
		if mode == "random-few-cursors" {
			maxCursors = 2
		}
		var dir string
		if mode != "auto" && mode != "random" {
			testMCURows = 1
		}
		for name, data := range files {
			want, werr := jpeg.Decode(bytes.NewReader(data))
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
			want, err := jpeg.Decode(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatal(err)
			}
			got, err := decodeStream(buf.Bytes(), nil)
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
			want, werr := jpeg.Decode(bytes.NewReader(in))
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
		if st.d.rowCoef[0] != nil || st.d.coef == nil || len(st.cursors) < 2 {
			t.Errorf("%s, low limit: rowCoef %v, coef %v, %d cursors", name, st.d.rowCoef[0] != nil, st.d.coef != nil, len(st.cursors))
		}
		src.Close()
	}
}
