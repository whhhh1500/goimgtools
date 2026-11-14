package jpegstream

import (
	"bytes"
	"image"
	"image/jpeg"
	"io"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestMatchesStdlib requires the same bytes as jpeg.Encode for every pixel
// type, odd sizes and several qualities, with misaligned source strips.
func TestMatchesStdlib(t *testing.T) {
	for _, sz := range [][2]int{{191, 143}, {16, 16}, {1, 1}, {33, 8}, {64, 257}} {
		for name, m := range testutil.Images(sz[0], sz[1]) {
			for i, q := range []int{1, 50, 75, 92, 100} {
				var want, got bytes.Buffer
				if err := jpeg.Encode(&want, m, &jpeg.Options{Quality: q}); err != nil {
					t.Fatal(err)
				}
				src := testutil.Chopped(m, []int{1, 7, 16, 32, 300}[i])
				if err := Encode(&got, src, &Options{Quality: q}); err != nil {
					t.Fatalf("%s/%v/q%d: %v", name, sz, q, err)
				}
				if !bytes.Equal(got.Bytes(), want.Bytes()) {
					t.Errorf("%s/%v/q%d: bytes differ from jpeg.Encode", name, sz, q)
				}
			}
		}
	}
	// A source whose bounds do not start at the origin.
	full := testutil.Images(200, 160)["rgba"].(*image.RGBA)
	m := full.SubImage(image.Rect(13, 21, 180, 150))
	var want, got bytes.Buffer
	jpeg.Encode(&want, m, nil)
	if err := Encode(&got, testutil.Chopped(m, 5), nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.Bytes(), want.Bytes()) {
		t.Error("sub-image: bytes differ from jpeg.Encode")
	}
}

// TestEncodeGivesBackItsBand: Encode does not close the source, but what it
// reserved for itself is released when it returns.
func TestEncodeGivesBackItsBand(t *testing.T) {
	for name, m := range testutil.Images(640, 48) {
		env := pipeline.NewEnv(0, "")
		if err := Encode(io.Discard, testutil.ChoppedEnv(m, 7, env), &Options{Quality: 80}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if env.Budget.Used() != 0 {
			t.Errorf("%s: %d bytes still reserved after Encode", name, env.Budget.Used())
		}
	}
}
