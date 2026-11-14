package pngstream

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"path/filepath"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

type opaqueSource struct{ pipeline.Source }

func (opaqueSource) Opaque() bool { return true }

// TestEncodeMatchesStdlib requires the bytes png.Encode writes, for every
// pixel type and several compression levels, with awkward strip heights,
// both when the opacity is known up front and when it has to be found by
// collecting the pixels first (in memory and on disk).
func TestEncodeMatchesStdlib(t *testing.T) {
	levels := []struct {
		ours int
		std  png.CompressionLevel
	}{{0, png.DefaultCompression}, {1, png.BestSpeed}, {9, png.BestCompression}}
	imgs := randomImages(301, 257)
	for name, m := range testutil.Images(301, 257) {
		imgs["t-"+name] = m
	}
	for name, m := range imgs {
		var ref image.Image = m
		switch m.(type) {
		case *image.YCbCr: // written as 8-bit RGB, not the 16-bit of png.Encode
			rgba := image.NewRGBA(m.Bounds())
			draw.Draw(rgba, rgba.Bounds(), m, m.Bounds().Min, draw.Src)
			ref = rgba
		}
		for i, lv := range levels {
			var want bytes.Buffer
			if err := (&png.Encoder{CompressionLevel: lv.std}).Encode(&want, ref); err != nil {
				t.Fatal(err)
			}
			for _, limit := range []int64{0, 1 << 20} {
				dir := t.TempDir()
				env := pipeline.NewEnv(limit, dir)
				var got bytes.Buffer
				src := testutil.ChoppedEnv(m, []int{1, 7, 64}[i], env)
				if err := Encode(&got, src, &Options{Level: lv.ours}); err != nil {
					t.Fatalf("%s level %d limit %d: %v", name, lv.ours, limit, err)
				}
				if !bytes.Equal(got.Bytes(), want.Bytes()) {
					t.Errorf("%s level %d limit %d: bytes differ from png.Encode", name, lv.ours, limit)
				}
				if left, _ := filepath.Glob(filepath.Join(dir, "*")); len(left) != 0 {
					t.Errorf("%s: temp files left behind: %v", name, left)
				}
				if env.Budget.Used() != 0 {
					t.Errorf("%s: %d bytes still reserved", name, env.Budget.Used())
				}
			}
		}
		// Opacity declared by the source: no collecting, same bytes.
		if o, ok := m.(interface{ Opaque() bool }); ok && o.Opaque() {
			var want, got bytes.Buffer
			png.Encode(&want, ref)
			if err := Encode(&got, opaqueSource{testutil.Chopped(m, 5)}, nil); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			if !bytes.Equal(got.Bytes(), want.Bytes()) {
				t.Errorf("%s (declared opaque): bytes differ from png.Encode", name)
			}
		}
	}
}

// TestRoundTrip decodes what Encode wrote with the streaming decoder.
func TestRoundTrip(t *testing.T) {
	for name, m := range randomImages(123, 77) {
		var buf bytes.Buffer
		if err := Encode(&buf, testutil.Chopped(m, 9), nil); err != nil {
			t.Fatal(err)
		}
		want, err := png.Decode(bytes.NewReader(buf.Bytes()))
		if err != nil {
			t.Fatal(err)
		}
		got, err := decodeStream(buf.Bytes())
		if err != nil {
			t.Fatal(err)
		}
		samePixels(t, name, want, got)
	}
}
