package tiffstream

import (
	"bytes"
	"image"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestStripsOnHelpers: with helpers the strips are compressed on them, and
// the file is the same, byte for byte: an image of many strips, Deflate and
// LZW, with and without the predictor, 8- and 16-bit.
func TestStripsOnHelpers(t *testing.T) {
	m8 := image.NewNRGBA(image.Rect(0, 0, 700, 1100)) // about 3MB: 3 strips
	for i := range m8.Pix {
		m8.Pix[i] = uint8(i*7 + i/2800)
	}
	m16 := image.NewNRGBA64(image.Rect(0, 0, 300, 1300))
	for i := range m16.Pix {
		m16.Pix[i] = uint8(i*13 + i/1700)
	}
	for _, m := range []image.Image{m8, m16} {
		for _, o := range []Options{{Compression: Deflate}, {Compression: Deflate, Predictor: true}, {Compression: LZW}, {Compression: LZW, Predictor: true}} {
			var want []byte
			for _, helpers := range []int{0, 1, 3} {
				env := pipeline.NewEnv(0, t.TempDir())
				env.SetHelpers(helpers)
				var buf bytes.Buffer
				if err := Encode(&buf, testutil.ChoppedEnv(m, 64, env), &o); err != nil {
					t.Fatal(err)
				}
				if want == nil {
					want = buf.Bytes()
					continue
				}
				if !bytes.Equal(buf.Bytes(), want) {
					t.Errorf("%T %+v, %d helpers: another file", m, o, helpers)
				}
			}
		}
	}
}
