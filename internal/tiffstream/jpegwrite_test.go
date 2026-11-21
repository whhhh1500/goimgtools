package tiffstream

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// decodeAll decodes a file with this package's reader (or a JPEG with
// jpegstream's) into NRGBA.
func decodeAll(t *testing.T, data []byte, jpeg bool) *image.NRGBA {
	t.Helper()
	env := pipeline.NewEnv(0, t.TempDir())
	var src pipeline.Source
	var err error
	if jpeg {
		src, err = jpegstream.Open(bytes.NewReader(data), env)
	} else {
		src, err = Open(bytes.NewReader(data), int64(len(data)), env)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	m, err := pipeline.Collect(src)
	if err != nil {
		t.Fatal(err)
	}
	b := m.Bounds()
	n := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			n.Set(x, y, m.At(b.Min.X+x, b.Min.Y+y))
		}
	}
	return n
}

// TestJPEGCompression: the picture of a JPEG TIFF, whatever its strips and
// however the source is cut, is the one a JPEG of the whole image gives at
// the same quality (4:2:0 below 90, 4:4:4 from there, grey as grey); the
// strips are whole rows of blocks and share their tables.
func TestJPEGCompression(t *testing.T) {
	photo := image.NewNRGBA(image.Rect(0, 0, 1003, 811))
	for y := 0; y < 811; y++ {
		for x := 0; x < 1003; x++ {
			photo.SetNRGBA(x, y, color.NRGBA{uint8(x*y>>6 + x), uint8(y * 3), uint8((x ^ y) * 5), 255})
		}
	}
	gray := image.NewGray(photo.Rect)
	for i := range gray.Pix {
		gray.Pix[i] = photo.Pix[4*i+1]
	}
	small := testutil.Images(67, 45)
	for name, m := range map[string]image.Image{"photo": photo, "gray": gray, "small": small["rgba-opaque"], "gray16": small["gray16"]} {
		for _, q := range []int{40, 80, 92} {
			var buf bytes.Buffer
			if err := Encode(&buf, testutil.Chopped(m, 7), &Options{Compression: JPEG, Quality: q}); err != nil {
				t.Fatalf("%s q%d: %v", name, q, err)
			}
			sub := jpegstream.Subsample420
			if q >= 90 {
				sub = jpegstream.Subsample444
			}
			var ref bytes.Buffer
			if err := jpegstream.Encode(&ref, testutil.Chopped(m, 64), &jpegstream.Options{Quality: q, Subsampling: sub}); err != nil {
				t.Fatal(err)
			}
			got, want := decodeAll(t, buf.Bytes(), false), decodeAll(t, ref.Bytes(), true)
			// Each strip is decoded on its own, so with the colour at half
			// the height the rows on either side of a strip's end are
			// upsampled without the colour across it (as from libtiff).
			rows := m.Bounds().Dy()
			per := rows
			if p := int(stripBytes / (int64(m.Bounds().Dx()) * 3)); rows > p {
				per = p / 16 * 16
			}
			for y := 0; y < rows; y++ {
				edge := sub == jpegstream.Subsample420 && name != "gray" && name != "gray16" && (y%per == 0 && y > 0 || (y+1)%per == 0 && y+1 < rows)
				r := got.Pix[y*got.Stride:][:4*got.Rect.Dx()]
				if !edge && !bytes.Equal(r, want.Pix[y*want.Stride:][:len(r)]) {
					t.Fatalf("%s q%d: row %d is not that of a JPEG of the image", name, q, y)
				}
			}
			// The same with the source cut otherwise.
			var again bytes.Buffer
			if err := Encode(&again, testutil.Chopped(m, 300), &Options{Compression: JPEG, Quality: q}); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(again.Bytes(), buf.Bytes()) {
				t.Errorf("%s q%d: another file for other source strips", name, q)
			}
		}
	}
}

// TestJPEGTransparency: transparent pixels are an error; an opaque image of
// a type with alpha, and a palette with a transparent entry no pixel uses,
// are written.
func TestJPEGTransparency(t *testing.T) {
	imgs := testutil.Images(40, 30)
	for _, name := range []string{"nrgba", "rgba"} {
		err := Encode(&bytes.Buffer{}, testutil.Chopped(imgs[name], 9), &Options{Compression: JPEG})
		if !errors.Is(err, errJPEGAlpha) {
			t.Errorf("%s: %v", name, err)
		}
	}
	opaque := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	for i := range opaque.Pix {
		opaque.Pix[i] = 255
	}
	pal := image.NewPaletted(image.Rect(0, 0, 40, 30), color.Palette{color.NRGBA{1, 2, 3, 255}, color.Transparent})
	for _, m := range []image.Image{opaque, pal} {
		if err := Encode(&bytes.Buffer{}, testutil.Chopped(m, 9), &Options{Compression: JPEG}); err != nil {
			t.Errorf("%T: %v", m, err)
		}
	}
	for _, q := range []int{-1, 101} {
		if err := Encode(&bytes.Buffer{}, testutil.Chopped(opaque, 9), &Options{Compression: JPEG, Quality: q}); err == nil {
			t.Errorf("quality %d: no error", q)
		}
	}
}
