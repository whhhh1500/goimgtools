package jpegstream

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"image"
	"image/draw"
	"os"
	"path/filepath"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestShrinkOnLoadLibjpeg: ShrinkOnLoadLibjpeg is libjpeg-turbo's scaled decoding,
// to the byte: the sums are of the RGBA pixels of Pillow's draft (which
// decodes with libjpeg-turbo scaled by 1/2, 1/4 or 1/8) for a baseline
// 4:2:0 and 4:2:2, a progressive 4:4:4 and a progressive grey JPEG; also
// with a band of one MCU row.
func TestShrinkOnLoadLibjpeg(t *testing.T) {
	for _, c := range []struct {
		file string
		k    int
		sum  string
	}{
		{"resize-420.jpg", 2, "20dc381bca1e2b0d"},
		{"resize-420.jpg", 4, "322e053fc7400c89"},
		{"resize-420.jpg", 8, "bd9a241c58454495"},
		{"resize-422.jpg", 2, "382dc666b7195b64"},
		{"resize-422.jpg", 4, "758a649da6c07aef"},
		{"resize-422.jpg", 8, "ac2e91d66179502d"},
		{"resize-444p.jpg", 2, "2cffc30e81f338ff"},
		{"resize-444p.jpg", 4, "155e69e595ba603f"},
		{"resize-444p.jpg", 8, "4c3f8e1e52dce39f"},
		{"resize-grayp.jpg", 2, "6ef0185ed0df7d04"},
		{"resize-grayp.jpg", 4, "613c59567bdf22c0"},
		{"resize-grayp.jpg", 8, "626be92f6a98d83e"},
	} {
		data, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "sharp", c.file))
		if err != nil {
			t.Fatal(err)
		}
		for _, rows := range []int{0, 1} {
			testMCURows = rows
			src, err := Open(bytes.NewReader(data), nil)
			if err != nil {
				t.Fatal(err)
			}
			if !src.(*stream).ShrinkOnLoadLibjpeg(c.k) {
				t.Fatalf("%s: not shrunk by %d", c.file, c.k)
			}
			m, err := pipeline.Collect(src)
			src.Close()
			testMCURows = 0
			if err != nil {
				t.Fatal(err)
			}
			n := image.NewNRGBA(m.Bounds())
			draw.Draw(n, n.Rect, m, m.Bounds().Min, draw.Src)
			if got := fmt.Sprintf("%x", sha256.Sum256(n.Pix))[:16]; got != c.sum {
				t.Errorf("%s by %d, bands of %d MCU rows: pixels %s, Pillow's draft %s", c.file, c.k, rows, got, c.sum)
			}
		}
	}
}
