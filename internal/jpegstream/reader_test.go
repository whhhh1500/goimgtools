package jpegstream

import (
	"bytes"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
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
	src, err := Open(bytes.NewReader(data), env)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	return pipeline.Collect(src)
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
// to disk.
func TestStdlibCorpus(t *testing.T) {
	files := corpus(t)
	for _, mode := range []string{"auto", "one-mcu-row", "disk"} {
		testMCURows = 0
		var dir string
		if mode != "auto" {
			testMCURows = 1
		}
		for name, data := range files {
			want, werr := jpeg.Decode(bytes.NewReader(data))
			env := pipeline.NewEnv(0, "")
			if mode == "disk" {
				dir = t.TempDir()
				env = pipeline.NewEnv(96<<10, dir)
			}
			got, gerr := decodeStream(data, env)
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
			if mode == "disk" {
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
			got, gerr := decodeStream(in, nil)
			if werr != nil {
				if gerr == nil {
					t.Fatalf("%s %s: stdlib error %v, stream decoded fine", name, what, werr)
				}
				return
			}
			if gerr != nil {
				t.Fatalf("%s %s: stdlib decoded fine, stream error %v", name, what, gerr)
			}
			same(t, name+" "+what, want, got)
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
