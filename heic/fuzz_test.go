package heic

import (
	"bytes"
	"image"
	"testing"

	gheic "github.com/gen2brain/h265/heic"
)

// FuzzStream: whatever the bytes, the stream neither panics nor hangs and
// keeps to its memory limit; and where both it and gen2brain/h265's whole
// decoding decode a file (and h265 converts as the stream does), they give
// the same pixels.
func FuzzStream(f *testing.F) {
	for _, name := range []string{"basic.heic", "chroma444.heic", "alpha.heic", "grid_alpha.heic", "test_exif.heic"} {
		f.Add(testFile(f, name))
	}
	f.Add(heicOf(f, 301, 203, 64, Options{Effort: 1}))
	f.Add(heicOf(f, 97, 75, 32, Options{Effort: 1, Chroma444: true}))
	f.Add(heicOf(f, 131, 77, 0, Options{Effort: 1}))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, _, err := streamedWith(data, 2, 8<<20)
		if err != nil || got == nil || !wholeSame {
			return
		}
		m, _, err := gheic.DecodeColor(bytes.NewReader(data), gheic.Options{AutoRotate: true, FrameSizeLimit: 8 << 20})
		if err != nil {
			return
		}
		if n, ok := m.(*image.NRGBA); ok && (n.Rect != got.Rect || !bytes.Equal(n.Pix, got.Pix)) {
			t.Fatalf("the stream gives %v, the whole decoding %v, other pixels", got.Rect, n.Rect)
		}
	})
}
