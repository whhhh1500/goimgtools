package webpstream

import (
	"image"
	"testing"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// TestFullRange checks the range tables at the values the standard fixes
// (16 to 235 for luma, 16 to 240 for chroma, 128 neutral) and that a stream
// is mapped sample by sample.
func TestFullRange(t *testing.T) {
	for v, want := range map[int]uint8{0: 0, 16: 0, 17: 1, 126: 128, 235: 255, 255: 255} {
		if lumaFull[v] != want {
			t.Errorf("luma %d -> %d, want %d", v, lumaFull[v], want)
		}
	}
	for v, want := range map[int]uint8{0: 0, 16: 0, 128: 128, 240: 255, 255: 255, 129: 129, 127: 127} {
		if chromaFull[v] != want {
			t.Errorf("chroma %d -> %d, want %d", v, chromaFull[v], want)
		}
	}
	for i := 1; i < 256; i++ {
		if lumaFull[i] < lumaFull[i-1] || chromaFull[i] < chromaFull[i-1] {
			t.Fatalf("tables are not monotonic at %d", i)
		}
	}
	m := image.NewYCbCr(image.Rect(0, 0, 37, 21), image.YCbCrSubsampleRatio420)
	for i := range m.Y {
		m.Y[i] = uint8(i * 7)
	}
	for i := range m.Cb {
		m.Cb[i], m.Cr[i] = uint8(i*5), uint8(255-i*3)
	}
	env := pipeline.NewEnv(0, "")
	src := FullRange(pipeline.FromImage(m, env))
	got, err := pipeline.Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	src.Close()
	g := got.(*image.YCbCr)
	for y := 0; y < 21; y++ {
		for x := 0; x < 37; x++ {
			a, b := m.YCbCrAt(x, y), g.YCbCrAt(x, y)
			if b.Y != lumaFull[a.Y] || b.Cb != chromaFull[a.Cb] || b.Cr != chromaFull[a.Cr] {
				t.Fatalf("(%d,%d): %v -> %v", x, y, a, b)
			}
		}
	}
	if env.Budget.Used() != 0 {
		t.Errorf("%d bytes still reserved", env.Budget.Used())
	}
	// NRGBA (a lossless image) is handed through untouched.
	n := pipeline.FromImage(image.NewNRGBA(image.Rect(0, 0, 4, 4)), nil)
	if FullRange(n) != n {
		t.Error("an NRGBA source was wrapped")
	}
}
