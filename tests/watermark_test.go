package tests

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

func readMark(t *testing.T, data []byte, key string) ([]byte, bool) {
	t.Helper()
	got, ok, err := inspect.ReadWatermark(FromBytes(data), key)
	if err != nil {
		t.Fatal(err)
	}
	return got, ok
}

// TestInvisibleWatermark: a mark of each kind is read back with its key
// after what is done to pictures (saved as JPEG and WebP, made smaller,
// cropped and turned, made darker; for WatermarkScreen photographed off a
// screen: perspective, blur, a smaller picture, JPEG), not with another
// key, and nothing is read from the picture unmarked. (The package's own
// tests and docs/watermark.md measure much more; these are kept few, as
// the tests also run under emulators.)
func TestInvisibleWatermark(t *testing.T) {
	src := pngBytes(t, testutil.Natural(800, 600, 3))
	payload := []byte("user-1234")
	if _, ok := readMark(t, src, "k"); ok {
		t.Fatal("read a mark from an unmarked picture")
	}
	quad := TransformPerspectiveQuad(-20, 14, 16, 590, 808, 615, 780, -8)
	for _, c := range []struct {
		kind    WatermarkKind
		attacks map[string]func(*Image) *Image
	}{
		{WatermarkBlind, map[string]func(*Image) *Image{
			"webp 90":           func(m *Image) *Image { return m.WebP(WebPOptions{Quality: 90}) },
			"70%, jpeg 80":      func(m *Image) *Image { return m.Resize(560, 0).JPEG(JPEGOptions{Quality: 80}) },
			"turned 7, cropped": func(m *Image) *Image { return m.Rotate(7).Crop(120, 100, 600, 440).PNG() },
			"darker, half turn": func(m *Image) *Image { return m.Linear(0.8, 10).Rotate(180).JPEG(JPEGOptions{Quality: 90}) },
		}},
		{WatermarkScreen, map[string]func(*Image) *Image{
			"photo": func(m *Image) *Image {
				return m.Transform(800, 600, quad, TransformOptions{Interpolator: InterpolateBicubic, Background: color.Gray{128}}).
					Blur(1).Resize(640, 0).JPEG(JPEGOptions{Quality: 85})
			},
		}},
	} {
		marked, err := FromBytes(src).InvisibleWatermark(payload, InvisibleWatermarkOptions{Kind: c.kind, Key: "k"}).PNG().ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if got, ok := readMark(t, marked, "k"); !ok || !bytes.Equal(got, payload) {
			t.Errorf("kind %d: read %q, %v", c.kind, got, ok)
		}
		if c.kind == WatermarkBlind {
			if _, ok := readMark(t, marked, "other"); ok {
				t.Error("read with another key")
			}
		}
		for name, attack := range c.attacks {
			out, err := attack(FromBytes(marked)).ToBytes()
			if err != nil {
				t.Fatal(err)
			}
			if got, ok := readMark(t, out, "k"); !ok || !bytes.Equal(got, payload) {
				t.Errorf("kind %d, %s: read %q, %v", c.kind, name, got, ok)
			}
		}
	}
}

// TestInvisibleWatermarkSame: the marked picture is the same however many
// threads make it and whatever the memory limit, transparency is kept,
// and the mark by name is the method's (the steps a hook sees show its
// kind, not the mark or the key).
func TestInvisibleWatermarkSame(t *testing.T) {
	src := testutil.Natural(400, 300, 5)
	for i := 3; i < len(src.Pix); i += 4 {
		src.Pix[i] = uint8(i / 4 % 256)
	}
	in := pngBytes(t, src)
	o := InvisibleWatermarkOptions{Key: "k", Kind: WatermarkScreen, Strength: 1.5, Density: 3}
	want := must(FromBytes(in).InvisibleWatermark([]byte("x"), o).PNG().ToBytes())
	for _, m := range []*Image{
		FromBytes(in).Threads(4).InvisibleWatermark([]byte("x"), o).PNG(),
		FromBytes(in).MemoryLimit(4<<20).InvisibleWatermark([]byte("x"), o).PNG(),
		FromBytes(in).Do("invisible-watermark", "x", "k", "screen", "1.5", "3").PNG(),
	} {
		if got := must(m.ToBytes()); !bytes.Equal(got, want) {
			t.Error("another picture")
		}
	}
	out := decodePNG(t, want)
	for i := 3; i < len(src.Pix); i += 4 {
		if r, g, b, a := out.At((i/4)%400, (i/4)/400).RGBA(); a>>8 != uint32(src.Pix[i]) {
			t.Fatalf("alpha changed at %d: %d %d %d %d", i/4, r, g, b, a)
		}
	}
	var words []string
	FromBytes(in).InvisibleWatermark([]byte("secret mark"), InvisibleWatermarkOptions{Key: "secret key"}).
		Around(func(s Step, src pipeline.Source, run func(pipeline.Source) (pipeline.Source, error)) (pipeline.Source, error) {
			words = append(words, s.Name+" "+fmt.Sprint(s.Args))
			return run(src)
		}).PNG().ToBytes()
	if fmt.Sprint(words) != "[invisible-watermark [blind]]" {
		t.Errorf("steps %v", words)
	}
	// A grey picture stays grey.
	g := image.NewGray(image.Rect(0, 0, 300, 200))
	for i := range g.Pix {
		g.Pix[i] = uint8(i * 7 % 251)
	}
	grey := decodePNG(t, must(FromImage(g).InvisibleWatermark([]byte("x")).PNG().ToBytes()))
	if _, ok := grey.(*image.Gray); !ok {
		t.Errorf("a grey picture came out as %T", grey)
	}
}

// TestInvisibleWatermarkOptions: wrong settings are errors.
func TestInvisibleWatermarkOptions(t *testing.T) {
	in := pngBytes(t, testutil.Natural(200, 150, 1))
	for name, m := range map[string]*Image{
		"no bytes":      FromBytes(in).InvisibleWatermark(nil),
		"33 bytes":      FromBytes(in).InvisibleWatermark(make([]byte, 33)),
		"kind":          FromBytes(in).InvisibleWatermark([]byte("x"), InvisibleWatermarkOptions{Kind: 2}),
		"strength":      FromBytes(in).InvisibleWatermark([]byte("x"), InvisibleWatermarkOptions{Strength: -1}),
		"strength 17":   FromBytes(in).InvisibleWatermark([]byte("x"), InvisibleWatermarkOptions{Strength: 17}),
		"density":       FromBytes(in).InvisibleWatermark([]byte("x"), InvisibleWatermarkOptions{Density: 0.5}),
		"two options":   FromBytes(in).InvisibleWatermark([]byte("x"), InvisibleWatermarkOptions{}, InvisibleWatermarkOptions{}),
		"by name, kind": FromBytes(in).Do("invisible-watermark", "x", "k", "loud"),
	} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, _, err := inspect.ReadWatermark(nil, ""); err == nil {
		t.Error("ReadWatermark(nil): no error")
	}
}
