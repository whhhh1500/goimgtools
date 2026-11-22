package tests

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// tiffLevels is the sizes of the images of a little-endian TIFF, one for
// each directory.
func tiffLevels(t *testing.T, data []byte) (sizes []image.Point) {
	t.Helper()
	le := binary.LittleEndian
	for off := le.Uint32(data[4:]); off != 0 && len(sizes) < 30; {
		n := int(le.Uint16(data[off:]))
		var p image.Point
		for i := 0; i < n; i++ {
			e := data[int(off)+2+12*i:][:12]
			v := int(le.Uint32(e[8:]))
			if le.Uint16(e[2:]) == 3 {
				v = int(le.Uint16(e[8:]))
			}
			switch le.Uint16(e) {
			case 256:
				p.X = v
			case 257:
				p.Y = v
			}
		}
		sizes = append(sizes, p)
		off = le.Uint32(data[int(off)+2+12*n:])
	}
	return sizes
}

// TestTIFFTilesAndPyramid: TIFFOptions.Tile writes the image in tiles and
// Pyramid adds its halves; the file reads back as the image (without loss
// but for JPEG), keeps its transparency, and has the levels libvips'
// pyramid has.
func TestTIFFTilesAndPyramid(t *testing.T) {
	photo := photoLike(300, 200)
	same := func(name string, data []byte, want image.Image, limit int) {
		t.Helper()
		got, err := FromBytes(data).ToImage()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		b := want.Bounds()
		if got.Bounds().Size() != b.Size() {
			t.Errorf("%s: %v", name, got.Bounds())
			return
		}
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				p := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
				q := color.NRGBAModel.Convert(want.At(b.Min.X+x, b.Min.Y+y)).(color.NRGBA)
				for _, d := range []int{int(p.R) - int(q.R), int(p.G) - int(q.G), int(p.B) - int(q.B), int(p.A) - int(q.A)} {
					if d > limit || d < -limit {
						t.Errorf("%s: pixel (%d, %d) is %v, want %v", name, x, y, p, q)
						return
					}
				}
			}
		}
	}
	tiled := must(FromImage(photo).TIFF(TIFFOptions{Tile: 64, Compression: TIFFDeflate, Predictor: true}).ToBytes())
	same("tiles", tiled, photo, 0)
	if sizes := tiffLevels(t, tiled); len(sizes) != 1 || sizes[0] != image.Pt(300, 200) {
		t.Errorf("tiles: levels %v", sizes)
	}
	strips := must(FromImage(photo).TIFF(TIFFOptions{Compression: TIFFDeflate, Predictor: true}).ToBytes())
	if bytes.Equal(tiled, strips) {
		t.Error("Tile changed nothing")
	}

	pyr := must(FromImage(photo).TIFF(TIFFOptions{Pyramid: true, Compression: TIFFLZW}).ToBytes())
	same("pyramid", pyr, photo, 0)
	if sizes := tiffLevels(t, pyr); len(sizes) != 2 || sizes[0] != image.Pt(300, 200) || sizes[1] != image.Pt(150, 100) {
		t.Errorf("pyramid in tiles of 256: levels %v", sizes) // 300x200 does not fit a tile, 150x100 does
	}
	small := must(FromImage(photo).TIFF(TIFFOptions{Pyramid: true, Tile: 32, Compression: TIFFJPEG, Quality: 90}).ToBytes())
	same("pyramid of JPEG tiles", small, photo, 40)
	want := []image.Point{{300, 200}, {150, 100}, {75, 50}, {37, 25}, {18, 12}}
	if sizes := tiffLevels(t, small); len(sizes) != len(want) {
		t.Errorf("pyramid in tiles of 32: levels %v", sizes)
	} else {
		for i := range want {
			if sizes[i] != want[i] {
				t.Errorf("pyramid in tiles of 32: level %d is %v, want %v", i, sizes[i], want[i])
			}
		}
	}

	// Transparency is kept in tiles; JPEG tiles cannot hold it.
	clear := image.NewNRGBA(image.Rect(0, 0, 90, 70))
	for i := 0; i < len(clear.Pix); i += 4 {
		clear.Pix[i], clear.Pix[i+1], clear.Pix[i+2], clear.Pix[i+3] = uint8(i), uint8(i>>3), uint8(i>>6), uint8(i>>2)
	}
	same("tiles with transparency", must(FromImage(clear).TIFF(TIFFOptions{Tile: 48, Pyramid: true}).ToBytes()), clear, 0)
	if _, err := FromImage(clear).TIFF(TIFFOptions{Tile: 48, Compression: TIFFJPEG}).ToBytes(); err == nil {
		t.Error("JPEG tiles with transparency: written")
	}
	// Grey stays grey.
	grey := image.NewGray(image.Rect(0, 0, 80, 50))
	for i := range grey.Pix {
		grey.Pix[i] = uint8(i * 3)
	}
	g := must(FromImage(grey).TIFF(TIFFOptions{Tile: 16, Compression: TIFFDeflate}).ToBytes())
	if info, err := FromBytes(g).Metadata(); err != nil || info.Color != "gray" {
		t.Errorf("grey in tiles: %+v, %v", info, err)
	}
	same("grey in tiles", g, grey, 0)

	for _, tile := range []int{-16, 8, 100, 16384} {
		if _, err := FromImage(photo).TIFF(TIFFOptions{Tile: tile}).ToBytes(); err == nil {
			t.Errorf("tiles of %d: written", tile)
		}
	}
}
