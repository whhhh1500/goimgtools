package pngstream

import (
	"bytes"
	"fmt"
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
// collecting the pixels first (in memory and on disk). Images whose data
// is more than one compression block (see zBlocks) come out compressed
// block by block: for them the same pixels, in a file hardly larger.
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
				sameFile(t, fmt.Sprintf("%s level %d limit %d", name, lv.ours, limit), got.Bytes(), want.Bytes())
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
			sameFile(t, name+" (declared opaque)", got.Bytes(), want.Bytes())
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

// sameFile requires got to be want, png.Encode's file, byte for byte when
// the image data fits one compression block, and otherwise the same
// pixels in a file at most 1% and 64 bytes a block larger (each block ends
// with a sync marker and starts its search for repeats afresh).
func sameFile(t *testing.T, what string, got, want []byte) {
	t.Helper()
	if bytes.Equal(got, want) {
		return
	}
	w, err := png.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	b := w.Bounds()
	bpp := map[string]int{"*image.Gray": 1, "*image.Gray16": 2, "*image.RGBA": 4, "*image.NRGBA": 4,
		"*image.RGBA64": 8, "*image.NRGBA64": 8, "*image.Paletted": 1}[fmt.Sprintf("%T", w)]
	data := (1 + b.Dx()*bpp) * b.Dy()
	if bpp == 0 || data <= zBlockBytes {
		t.Errorf("%s: bytes differ from png.Encode", what)
		return
	}
	g, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if g.At(x, y) != w.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) %v, png.Encode's %v", what, x, y, g.At(x, y), w.At(x, y))
			}
		}
	}
	if blocks := (data + zBlockBytes - 1) / zBlockBytes; len(got) > len(want)+len(want)/100+64*blocks {
		t.Errorf("%s: %d bytes, png.Encode %d", what, len(got), len(want))
	}
}

// TestSameWithHelpers: compressed on helpers, the file is the one without,
// for every pixel type and level, and the blocks are given back.
func TestSameWithHelpers(t *testing.T) {
	for _, sz := range [][2]int{{301, 257}, {1000, 700}, {1, 1}, {7000, 3}} {
		for name, m := range testutil.Images(sz[0], sz[1]) {
			for _, level := range []int{1, 6, 7, 9} {
				var want bytes.Buffer
				if err := Encode(&want, testutil.Chopped(m, 16), &Options{Level: level}); err != nil {
					t.Fatal(err)
				}
				for _, helpers := range []int{1, 3, 7} {
					env := pipeline.NewEnv(0, t.TempDir())
					env.SetHelpers(helpers)
					var got bytes.Buffer
					if err := Encode(&got, testutil.ChoppedEnv(m, 16, env), &Options{Level: level}); err != nil {
						t.Fatalf("%s/%v/level %d/%d helpers: %v", name, sz, level, helpers, err)
					}
					if !bytes.Equal(got.Bytes(), want.Bytes()) {
						t.Errorf("%s/%v/level %d/%d helpers: another file than without helpers", name, sz, level, helpers)
					}
					if u := env.Budget.Used(); u != 0 {
						t.Errorf("%s/%v/level %d/%d helpers: %d bytes still reserved", name, sz, level, helpers, u)
					}
				}
			}
		}
	}
}
