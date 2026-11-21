package jxl

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"testing"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/jxl/internal/libjxl"
)

// TestDeep: a file of 16 bits a sample is read as 16 bits (NRGBA64), and
// written to PNG as such, its samples exact.
func TestDeep(t *testing.T) {
	w, h := 23, 17
	pix := make([]byte, 8*w*h)
	for i := range pix {
		pix[i] = byte(i*29 + i/7)
	}
	b, err := libjxl.Encode(pix, w, h, libjxl.Options{Lossless: true, Effort: 3, Depth16: true})
	if err != nil {
		t.Fatal(err)
	}
	out, err := goimgtools.FromBytes(b).PNG().ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	m, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatal(err)
	}
	n, ok := m.(*image.NRGBA64)
	if !ok {
		t.Fatalf("%T", m)
	}
	if !bytes.Equal(n.Pix, pix) {
		t.Error("other samples")
	}
}

// FuzzDecode: whatever the bytes, decoding neither panics nor hangs, under
// a memory limit.
func FuzzDecode(f *testing.F) {
	for _, name := range []string{"photo.jxl", "gray.jxl", "exif6.jxl", "icc.jxl"} {
		b, err := os.ReadFile("testdata/" + name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		goimgtools.FromBytes(data).MemoryLimit(16 << 20).PNG().ToBytes()
	})
}
