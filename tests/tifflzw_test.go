package tests

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"golang.org/x/image/tiff"
)

// tiffStrips is how many strips the first directory of a little-endian
// TIFF has, and its Predictor (0 when not given).
func tiffStrips(t *testing.T, data []byte) (strips int, predictor uint16) {
	t.Helper()
	le := binary.LittleEndian
	ifd := le.Uint32(data[4:])
	n := int(le.Uint16(data[ifd:]))
	for i := 0; i < n; i++ {
		e := data[int(ifd)+2+12*i:]
		switch le.Uint16(e) {
		case 273:
			strips = int(le.Uint32(e[4:]))
		case 317:
			predictor = le.Uint16(e[8:])
		}
	}
	return
}

// TestTIFFCompression: LZW and Deflate, with and without the predictor,
// for every kind of pixel the writer stores: x/image reads the pixels back
// unchanged. A compressed image of more than 1MB of data is written in
// strips of about 1MB (libvips refuses one strip of more than 50MB); a
// smaller one is one strip, as x/image writes it. The predictor makes a
// photograph smaller.
func TestTIFFCompression(t *testing.T) {
	imgs := testutil.Images(523, 397)
	photo := photoLike(700, 520) // 1.4MB of RGBA
	imgs["photo"] = toNRGBA(photo)
	sizes := map[string]int{}
	for name, m := range imgs {
		// The pixels as stored without compression.
		plain, err := tiff.Decode(bytes.NewReader(must(FromImage(m).TIFF().ToBytes())))
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range []TIFFCompression{TIFFLZW, TIFFDeflate} {
			for _, p := range []bool{false, true} {
				data := must(FromImage(m).TIFF(TIFFOptions{Compression: c, Predictor: p}).ToBytes())
				back, err := tiff.Decode(bytes.NewReader(data))
				if err != nil {
					t.Fatalf("%s %v predictor %v: %v", name, c, p, err)
				}
				b := plain.Bounds()
				for y := b.Min.Y; y < b.Max.Y; y += 3 {
					for x := b.Min.X; x < b.Max.X; x += 2 {
						if g, w := color.NRGBA64Model.Convert(back.At(x, y)), color.NRGBA64Model.Convert(plain.At(x, y)); g != w {
							t.Fatalf("%s %v predictor %v at %d,%d: %v, want %v", name, c, p, x, y, g, w)
						}
					}
				}
				strips, pred := tiffStrips(t, data)
				_, paletted := m.(*image.Paletted)
				if want := uint16(0); p && !paletted {
					if pred != 2 {
						t.Errorf("%s %v: Predictor %d, want 2", name, c, pred)
					}
				} else if pred != want {
					t.Errorf("%s %v: Predictor %d without one", name, c, pred)
				}
				if name == "photo" {
					sizes[map[TIFFCompression]string{TIFFLZW: "lzw", TIFFDeflate: "deflate"}[c]+map[bool]string{false: "", true: "+p"}[p]] = len(data)
					if strips < 2 {
						t.Errorf("photo %v: %d strip", c, strips)
					}
				}
			}
		}
	}
	if !(sizes["lzw+p"] < sizes["lzw"]*9/10 && sizes["deflate+p"] < sizes["deflate"]*9/10) {
		t.Errorf("the predictor saves too little: %v", sizes)
	}
	// Small: one strip, as x/image writes it.
	small := must(FromImage(imgs["nrgba"]).TIFF(TIFFOptions{Compression: TIFFLZW}).ToBytes())
	if n, _ := tiffStrips(t, small); n != 1 {
		t.Errorf("small image: %d strips", n)
	}
}

func toNRGBA(m image.Image) *image.NRGBA {
	out := image.NewNRGBA(m.Bounds())
	for y := m.Bounds().Min.Y; y < m.Bounds().Max.Y; y++ {
		for x := m.Bounds().Min.X; x < m.Bounds().Max.X; x++ {
			out.Set(x, y, m.At(x, y))
		}
	}
	return out
}
