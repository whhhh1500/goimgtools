package webpstream

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"image"
	"os"
	"path/filepath"
	"runtime/debug"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// scaled is a size to decode a file at and the FNV-64a of the RGBA rows
// that dwebp 1.6.0 writes for it (-scale w h -pam).
type scaled struct {
	w, h int
	want uint64
}

// TestScaleOnLoad checks the pixels of ScaleOnLoad against libwebp's
// scaled decoding: lossy (the loop filter skipped below three quarters of
// both sides and kept otherwise, simple and normal), lossy with alpha and
// lossless, odd sizes, down to one pixel; in strips of different heights.
func TestScaleOnLoad(t *testing.T) {
	type file struct {
		path  string
		sizes []scaled
	}
	files := []file{
		{"../../tests/testdata/webp/odd.lossy.webp", []scaled{{24, 15, 0x4397cc69d517a143}, {29, 18, 0x1cbb580c715155c5}, {12, 7, 0xa5800add3c4b182a}, {33, 11, 0xa0297dddb7d7a8e6}, {5, 4, 0xec19b0a6c8e20cf1}, {37, 23, 0x1f0be4dc2911b83d}, {1, 1, 0x6a56cc61cfd5a4bd}}},
		{"../../tests/testdata/webp/odd-alpha.lossy.webp", []scaled{{22, 11, 0x5025013c24cc5f6b}, {26, 13, 0xb93d32529b18bf96}, {11, 5, 0x9288e9065fefa436}, {29, 8, 0x6a9f2456c79d4bc5}, {4, 3, 0x17c9a1264bf2ccd7}, {33, 17, 0xcd9885a2a1e0f77c}, {1, 1, 0x8fa9af30c1b6a8ab}}},
		{"../../tests/testdata/sharp/resize-lossy.webp", []scaled{{202, 139, 0x28c8056ce28054c9}, {243, 167, 0x8627c97b179063c9}, {101, 69, 0xd223fec76ed3e420}, {273, 104, 0x8bccef624a5defe7}, {43, 41, 0xc8bf7a5ba9fa0d71}, {304, 209, 0xabb293a884f03f08}, {1, 1, 0xa82ad52611f305bf}}},
		{"../../tests/testdata/sharp/resize-odd.webp", []scaled{{155, 107, 0x0e67caaac92ca23f}, {186, 128, 0x43c417c605947e8e}, {77, 53, 0xa6fe7711080b0e78}, {209, 80, 0xb1421388f39d2952}, {33, 32, 0x19aff42a4d1dbc1a}, {233, 161, 0x72427e9751fea272}, {1, 1, 0x7c6cd5315a4b5e7e}}},
		{"../../tests/testdata/sharp/resize-alpha.webp", []scaled{{100, 68, 0x56a0697da97ff799}, {120, 82, 0x53f5f0b94e100c7b}, {50, 34, 0x87a0783caa1a887e}, {135, 51, 0xeeda2e4eaf72a0e3}, {21, 20, 0xc89e747fb2d4ec77}, {150, 103, 0x94b5adac6e8799e8}, {1, 1, 0xa82ac42611f2e8dc}}},
		{"../../tests/testdata/sharp/resize-lossless.webp", []scaled{{100, 68, 0x909dbbe30047570a}, {120, 82, 0x42d1660d52219388}, {50, 34, 0xbf3b91a257e0f29e}, {135, 51, 0x3562cd04df7b19af}, {21, 20, 0x5c2b770e95f95796}, {150, 103, 0x9183424ae44b55aa}, {1, 1, 0x0a3e11dde01a5a18}}},
	}
	if dir := testutil.XImageTestdata(); dir != "" {
		for name, sizes := range map[string][]scaled{
			"blue-purple-pink-large.simple-filter.lossy.webp": {{400, 266, 0x50cee8004f55d219}, {480, 320, 0xb0429e1d2d4650c1}, {200, 133, 0x7d55a51e5e71871e}, {540, 200, 0xa8f749982d94e3ee}, {85, 80, 0x87f264ef75f09fe6}, {600, 400, 0x13ea532e7660e3a4}, {1, 1, 0x48ca76243b619ae1}},
			"blue-purple-pink-large.normal-filter.lossy.webp": {{400, 266, 0x50cee8004f55d219}, {480, 320, 0x08a28210ee10c9ee}, {200, 133, 0x7d55a51e5e71871e}, {540, 200, 0x6a5182ab4de85141}, {85, 80, 0x87f264ef75f09fe6}, {600, 400, 0x03747c0823de79fa}, {1, 1, 0x48ca76243b619ae1}},
			"blue-purple-pink-large.no-filter.lossy.webp":     {{400, 266, 0x50cee8004f55d219}, {480, 320, 0xe20bdfd7cb7a7eb6}, {200, 133, 0x7d55a51e5e71871e}, {540, 200, 0x5d7602472d1192fb}, {85, 80, 0x87f264ef75f09fe6}, {600, 400, 0xd6a3be83eda0d61d}, {1, 1, 0x48ca76243b619ae1}},
			"video-001.lossy.webp":                            {{100, 68, 0xafa202bef5e9f306}, {120, 82, 0x76a3256a274dee67}, {50, 34, 0xfe520fbf671b6a6c}, {135, 51, 0x0886eb4753a3b23b}, {21, 20, 0x0b2586793092fbcc}, {150, 103, 0x7dbb5935a3aa91ed}, {1, 1, 0xa82ad52611f305bf}},
			"yellow_rose.lossy-with-alpha.webp":               {{266, 200, 0x72cb0e2299e48e2b}, {320, 240, 0x7f6be959485c967b}, {133, 100, 0x4d824c096d0f2c19}, {360, 150, 0x44fbcb6db4dd8637}, {57, 60, 0x091905a690b06d9f}, {400, 301, 0x5bed994f12bae45d}, {1, 1, 0xef169837e4186aab}},
			"yellow_rose.lossless.webp":                       {{266, 200, 0x4533aa04d51d6815}, {320, 240, 0x8066279aded3e18f}, {133, 100, 0x84b436258235e136}, {360, 150, 0x81f2ec1cd9e4073d}, {57, 60, 0x734d4953cb397a8d}, {400, 301, 0x7086d0df455321fc}, {1, 1, 0xc1810afc8c683525}},
			"tux.lossless.webp":                               {{257, 263, 0xe1b2a03e7447e9b0}, {308, 316, 0xec6fd1dd9077fd44}, {128, 131, 0xef4859426b8129f3}, {347, 197, 0x617b872a4f4a6f14}, {55, 79, 0x2eb62334717b5e66}, {386, 395, 0xae1d90190df0675b}, {1, 1, 0xf6024d0893d8f099}},
			"gopher-doc.with-alpha.lossless.webp":             {{50, 66, 0x743d5b8536b30b36}, {60, 80, 0xd9f26f15ab84190c}, {25, 33, 0xf0867c23a8327413}, {67, 50, 0x0783a86d8c4c8af1}, {10, 20, 0x877cbc047f1ca187}, {75, 100, 0xedeb00046765a10f}, {1, 1, 0x4d256a7f9dcdff91}},
		} {
			files = append(files, file{filepath.Join(dir, name), sizes})
		}
	} else {
		t.Log("golang.org/x/image testdata not found; testing the files of this module only")
	}
	for _, f := range files {
		data, err := os.ReadFile(f.path)
		if err != nil {
			t.Fatal(err)
		}
		for _, sz := range f.sizes {
			for _, rows := range []int{0, 16, 48} {
				setBandRows(t, rows)
				env := pipeline.NewEnv(0, t.TempDir())
				src, err := Open(bytes.NewReader(data), int64(len(data)), env)
				if err != nil {
					t.Fatal(err)
				}
				rgb := ToRGB(src)
				if !rgb.(interface{ ScaleOnLoad(w, h int) bool }).ScaleOnLoad(sz.w, sz.h) {
					t.Fatalf("%s: ScaleOnLoad(%d, %d) refused", f.path, sz.w, sz.h)
				}
				if b := rgb.Bounds(); b != image.Rect(0, 0, sz.w, sz.h) {
					t.Fatalf("%s: bounds %v", f.path, b)
				}
				m, err := pipeline.Collect(rgb)
				if err != nil {
					t.Fatalf("%s: %v", f.path, err)
				}
				rgb.Close()
				n := m.(*image.NRGBA)
				h := fnv.New64a()
				for y := 0; y < sz.h; y++ {
					h.Write(n.Pix[n.PixOffset(0, y):][:4*sz.w])
				}
				if got := h.Sum64(); got != sz.want {
					t.Errorf("%s at %dx%d, strips of %d rows: sum %016x, want %016x", filepath.Base(f.path), sz.w, sz.h, rows, got, sz.want)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: %d bytes still reserved", f.path, env.Budget.Used())
				}
			}
		}
	}
}

// TestScaleOnLoadRefused: not once decoding has begun, not larger than the
// image, not twice.
func TestScaleOnLoadRefused(t *testing.T) {
	data, err := os.ReadFile("../../tests/testdata/webp/odd.lossy.webp") // 37x23
	if err != nil {
		t.Fatal(err)
	}
	open := func() pipeline.Source {
		src, err := Open(bytes.NewReader(data), int64(len(data)), nil)
		if err != nil {
			t.Fatal(err)
		}
		return ToRGB(src)
	}
	type scaler interface{ ScaleOnLoad(w, h int) bool }
	for _, sz := range [][2]int{{38, 23}, {37, 24}, {0, 10}, {10, -1}} {
		s := open()
		if s.(scaler).ScaleOnLoad(sz[0], sz[1]) || s.Bounds() != image.Rect(0, 0, 37, 23) {
			t.Errorf("ScaleOnLoad(%d, %d) accepted", sz[0], sz[1])
		}
		s.Close()
	}
	s := open()
	if !s.(scaler).ScaleOnLoad(20, 10) || s.(scaler).ScaleOnLoad(10, 5) || s.Bounds() != image.Rect(0, 0, 20, 10) {
		t.Error("a second ScaleOnLoad changed the size")
	}
	s.Close()
	s = open()
	if _, err := s.Next(); err != nil {
		t.Fatal(err)
	}
	if s.(scaler).ScaleOnLoad(20, 10) {
		t.Error("ScaleOnLoad after Next accepted")
	}
	s.Close()
}

// scaledStream decodes data scaled to two thirds (see stream); ok is false
// when the image cannot be scaled (an animation, or no image).
func scaledStream(t testing.TB, data []byte) (ok bool, err error) {
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("panic: %v\n%s", r, debug.Stack())
			ok, err = true, fmt.Errorf("panic: %v", r)
		}
	}()
	env := pipeline.NewEnv(0, t.TempDir())
	src, err := Open(bytes.NewReader(data), int64(len(data)), env)
	if err != nil {
		return true, err
	}
	defer func() {
		src.Close()
		if used := env.Budget.Used(); used != 0 {
			t.Errorf("budget still has %d bytes reserved after Close", used)
		}
	}()
	rgb := ToRGB(src)
	j, is := rgb.(interface{ ScaleOnLoad(w, h int) bool })
	b := src.Bounds()
	if !is || !j.ScaleOnLoad(max(b.Dx()*2/3, 1), max(b.Dy()*2/3, 1)) {
		return false, nil
	}
	_, err = pipeline.Collect(rgb)
	return true, err
}
