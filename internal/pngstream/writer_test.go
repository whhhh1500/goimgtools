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
// block by block, and from level 7 the filters are tried against none
// (see filterTrial): for them the same pixels, in a file hardly larger.
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
				if limit > 0 {
					limit += nearBytes(lv.ours, m.Bounds().Dx())
				}
				dir := t.TempDir()
				env := pipeline.NewEnv(limit, dir)
				var got bytes.Buffer
				src := testutil.ChoppedEnv(m, []int{1, 7, 64}[i], env)
				if err := Encode(&got, src, &Options{Level: lv.ours}); err != nil {
					t.Fatalf("%s level %d limit %d: %v", name, lv.ours, limit, err)
				}
				what := fmt.Sprintf("%s level %d limit %d", name, lv.ours, limit)
				if lv.ours >= 7 {
					sameFilePixels(t, what, got.Bytes(), want.Bytes())
				} else {
					sameFile(t, what, got.Bytes(), want.Bytes())
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
// sameFilePixels requires the pixels of want in got, in a file at most 1%
// larger.
func sameFilePixels(t *testing.T, what string, got, want []byte) {
	t.Helper()
	w, err := png.Decode(bytes.NewReader(want))
	if err != nil {
		t.Fatal(err)
	}
	g, err := png.Decode(bytes.NewReader(got))
	if err != nil {
		t.Fatalf("%s: %v", what, err)
	}
	b := w.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if g.At(x, y) != w.At(x, y) {
				t.Fatalf("%s: pixel (%d,%d) %v, png.Encode's %v", what, x, y, g.At(x, y), w.At(x, y))
			}
		}
	}
	if len(got) > len(want)+len(want)/100+64 {
		t.Errorf("%s: %d bytes, png.Encode %d", what, len(got), len(want))
	}
}

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
			// Level 9 is level 8 but for the settings of the compressor:
			// left out for time.
			for _, level := range []int{1, 6, 7, 8} {
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

// TestFilterTrial: from level 7, text of anti-aliased glyphs on white
// (as on screenshots) is smaller with the trials than filtered throughout,
// and a photograph-like image of noise and gradients comes out as without
// them.
func TestFilterTrial(t *testing.T) {
	// Twelve glyphs of 8x12: rings and bars, anti-aliased by 4x4 coverage.
	var glyphs [12][12][8]uint8
	for g := range glyphs {
		for y := 0; y < 12; y++ {
			for x := 0; x < 8; x++ {
				n := 0
				for sy := 0; sy < 4; sy++ {
					for sx := 0; sx < 4; sx++ {
						fx, fy := float64(4*x+sx)/4-3.5, float64(4*y+sy)/4-5.5
						r := fx*fx + fy*fy
						inRing := r > float64(2+g%3) && r < float64(6+g%5)
						inBar := g%4 == 1 && fx > -1 && fx < 0.5
						if inRing || inBar {
							n++
						}
					}
				}
				glyphs[g][y][x] = uint8(n * 255 / 16)
			}
		}
	}
	colours := [3][3]int{{32, 33, 36}, {0, 90, 140}, {200, 40, 40}}
	text := image.NewRGBA(image.Rect(0, 0, 640, 480))
	for i := range text.Pix {
		text.Pix[i] = 255
	}
	seed := uint32(7)
	for cy := 0; cy+12 <= 480; cy += 16 {
		for cx := 0; cx+8 <= 640; cx += 9 {
			seed = seed*1664525 + 1013904223
			if seed>>29 == 0 {
				continue // a space
			}
			g, c := glyphs[seed>>24%12], colours[seed>>20%3]
			for y := 0; y < 12; y++ {
				for x := 0; x < 8; x++ {
					a := int(g[y][x])
					o := text.PixOffset(cx+x, cy+y)
					for k := 0; k < 3; k++ {
						text.Pix[o+k] = uint8((c[k]*a + 255*(255-a)) / 255)
					}
				}
			}
		}
	}
	photo := image.NewRGBA(image.Rect(0, 0, 400, 300))
	for i := range photo.Pix {
		seed = seed*1664525 + 1013904223
		photo.Pix[i] = uint8(i/4%400*255/400) + uint8(seed>>28)
		if i%4 == 3 {
			photo.Pix[i] = 255
		}
	}
	encode := func(m image.Image, noTrial bool) []byte {
		testNoTrial = noTrial
		defer func() { testNoTrial = false }()
		var b bytes.Buffer
		if err := Encode(&b, opaqueSource{testutil.Chopped(m, 7)}, &Options{Level: 7}); err != nil {
			t.Fatal(err)
		}
		return b.Bytes()
	}
	with, without := encode(text, false), encode(text, true)
	t.Logf("text: %d bytes with the trials, %d without", len(with), len(without))
	if len(with) >= len(without) {
		t.Errorf("text: %d bytes with the trials, %d without", len(with), len(without))
	}
	sameFilePixels(t, "text", with, without)
	if !bytes.Equal(encode(photo, false), encode(photo, true)) {
		t.Error("photograph: the trials changed the file")
	}

	// Levels 8 and 9 (Brute filtering and libdeflate's near-optimal
	// parsing): the same pixels, smaller than level 7 on both images, and
	// level 9 no larger than 8.
	for name, m := range map[string]image.Image{"text": text, "photograph": photo} {
		var files [10][]byte
		for _, level := range []int{7, 8, 9} {
			var b bytes.Buffer
			if err := Encode(&b, opaqueSource{testutil.Chopped(m, 7)}, &Options{Level: level}); err != nil {
				t.Fatal(err)
			}
			files[level] = b.Bytes()
		}
		t.Logf("%s: level 7 %d bytes, 8 %d, 9 %d", name, len(files[7]), len(files[8]), len(files[9]))
		for _, level := range []int{8, 9} {
			sameFilePixels(t, fmt.Sprintf("%s level %d", name, level), files[level], files[7])
		}
		if len(files[8]) >= len(files[7]) || len(files[9]) > len(files[8]) {
			t.Errorf("%s: level 7 %d bytes, 8 %d, 9 %d", name, len(files[7]), len(files[8]), len(files[9]))
		}
	}
}

// TestFilterRow checks every filter of filterRow against the unfiltering
// of the reader, for every number of bytes per pixel.
func TestFilterRow(t *testing.T) {
	seed := uint32(1)
	next := func() byte {
		seed = seed*1664525 + 1013904223
		return byte(seed >> 24)
	}
	for _, bpp := range []int{1, 2, 3, 4, 6, 8} {
		n := 13 * bpp
		prev, cur := make([]byte, n), make([]byte, n)
		for i := range cur {
			prev[i], cur[i] = next(), next()
		}
		dst := make([]byte, n+1)
		for f := ftNone; f <= ftPaeth; f++ {
			filterRow(dst, cur, prev, bpp, f)
			if dst[0] != byte(f) {
				t.Fatalf("bpp %d filter %d: type byte %d", bpp, f, dst[0])
			}
			got := append([]byte(nil), dst[1:]...)
			switch f {
			case ftSub:
				for i := bpp; i < n; i++ {
					got[i] += got[i-bpp]
				}
			case ftUp:
				for i := range got {
					got[i] += prev[i]
				}
			case ftAverage:
				for i := 0; i < bpp; i++ {
					got[i] += prev[i] / 2
				}
				for i := bpp; i < n; i++ {
					got[i] += uint8((int(got[i-bpp]) + int(prev[i])) / 2)
				}
			case ftPaeth:
				filterPaeth(got, prev, bpp)
			}
			if !bytes.Equal(got, cur) {
				t.Errorf("bpp %d filter %d: unfiltered %v, want %v", bpp, f, got, cur)
			}
		}
	}
}
