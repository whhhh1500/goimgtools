package tests

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"strings"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// TestFloodFill: FloodFill fills the pixels Pillow 12's
// ImageDraw.floodfill fills (testdata/flood: pictures, and what Pillow made
// of each case of cases.txt): areas of one colour and within a threshold,
// up to a border colour, a corridor that winds to the centre of a spiral
// (which the area reaches over many rows back and forth), pictures with
// transparency, a point that has the colour already.
func TestFloodFill(t *testing.T) {
	read := func(name string) []byte {
		t.Helper()
		b, err := os.ReadFile("testdata/flood/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	rgba := func(s string) color.NRGBA {
		var c [4]int
		fmt.Sscanf(strings.ReplaceAll(s, ",", " "), "%d %d %d %d", &c[0], &c[1], &c[2], &c[3])
		return color.NRGBA{uint8(c[0]), uint8(c[1]), uint8(c[2]), uint8(c[3])}
	}
	cases := strings.Split(strings.TrimSpace(string(read("cases.txt"))), "\n")
	if len(cases) != 16 {
		t.Fatalf("%d cases", len(cases))
	}
	for _, line := range cases {
		var name, src, col, border string
		var x, y, thresh int
		if _, err := fmt.Sscan(strings.TrimSpace(line), &name, &src, &x, &y, &col, &thresh, &border); err != nil {
			t.Fatalf("%q: %v", line, err)
		}
		o := FloodFillOptions{Threshold: thresh}
		if border != "-" {
			o.Border = rgba(border)
		}
		in := read(src + ".png")
		want := decodeOurs(t, read("fill_"+name+".png"))
		// With the default memory, and with so little that the picture
		// is kept in a temporary file and handed out in short strips.
		for _, limit := range []int64{0, 1 << 20} {
			m := FromBytes(in).FloodFill(x, y, rgba(col), o)
			if limit != 0 {
				m = m.MemoryLimit(limit).TempDir(t.TempDir())
			}
			got, err := m.ToImage()
			if err != nil {
				t.Errorf("%s (limit %d): %v", name, limit, err)
				continue
			}
			b := want.Bounds()
			bad := 0
			for py := b.Min.Y; py < b.Max.Y; py++ {
				for px := b.Min.X; px < b.Max.X; px++ {
					p, q := color.NRGBAModel.Convert(got.At(px, py)), color.NRGBAModel.Convert(want.At(px, py))
					if p != q {
						if bad == 0 {
							t.Errorf("%s (limit %d): pixel (%d, %d) is %v, Pillow's is %v", name, limit, px, py, p, q)
						}
						bad++
					}
				}
			}
			if bad > 1 {
				t.Errorf("%s (limit %d): %d pixels differ", name, limit, bad)
			}
		}
	}

	// A large picture within a small limit: a frame of one colour around
	// a photograph is filled, the photograph is not.
	const w, h = 1800, 1200
	big := image.NewNRGBA(image.Rect(0, 0, w, h))
	photo := photoLike(w-200, h-200)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			p := big.Pix[big.PixOffset(x, y):]
			if x < 100 || y < 100 || x >= w-100 || y >= h-100 {
				p[0], p[1], p[2], p[3] = 7, 7, 7, 255
			} else {
				copy(p[:4], photo.Pix[photo.PixOffset(x-100, y-100):])
				p[0] |= 16 // never the frame's colour
			}
		}
	}
	file, err := FromImage(big).FloodFill(w-1, h-1, color.NRGBA{250, 0, 0, 255}).MemoryLimit(8 << 20).TempDir(t.TempDir()).PNG(PNGOptions{CompressionLevel: 1}).ToBytes()
	if err != nil {
		t.Fatal(err)
	}
	out := decodeOurs(t, file)
	filled := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			if c := color.NRGBAModel.Convert(out.At(x, y)).(color.NRGBA); c == (color.NRGBA{250, 0, 0, 255}) {
				filled++
			} else if x < 100 || y < 100 || x >= w-100 || y >= h-100 {
				t.Fatalf("the frame at (%d, %d) is %v", x, y, c)
			}
		}
	}
	if want := w*h - (w-200)*(h-200); filled != want {
		t.Errorf("%d pixels filled, the frame has %d", filled, want)
	}

	// The size is known without reading; a colour with transparency makes
	// the picture one with transparency.
	plain := FromImage(photoLike(30, 20))
	if gw, gh, err := plain.Clone().FloodFill(3, 3, color.Black).OutputSize(); err != nil || gw != 30 || gh != 20 {
		t.Errorf("OutputSize: %dx%d, %v", gw, gh, err)
	}
	clear := must(Blank(30, 20, color.White).FloodFill(3, 3, color.NRGBA{}).PNG().ToBytes())
	if info, err := FromBytes(clear).Metadata(); err != nil || !info.HasAlpha {
		t.Errorf("filled with transparency: %+v, %v", info, err)
	}
	if again := must(FromBytes(clear).FloodFill(3, 3, color.NRGBA{}).PNG().ToBytes()); !bytes.Equal(must(FromBytes(again).PNG().ToBytes()), must(FromBytes(clear).PNG().ToBytes())) {
		t.Error("filling with the colour the point has changed the picture")
	}

	for name, m := range map[string]*Image{
		"a point left of the image":  plain.Clone().FloodFill(-1, 0, color.Black),
		"a point right of the image": plain.Clone().FloodFill(30, 0, color.Black),
		"a point below the image":    plain.Clone().FloodFill(0, 20, color.Black),
		"no colour":                  plain.Clone().FloodFill(0, 0, nil),
		"a threshold below 0":        plain.Clone().FloodFill(0, 0, color.Black, FloodFillOptions{Threshold: -1}),
		"a threshold above 1020":     plain.Clone().FloodFill(0, 0, color.Black, FloodFillOptions{Threshold: 1021}),
		"two options":                plain.Clone().FloodFill(0, 0, color.Black, FloodFillOptions{}, FloodFillOptions{}),
	} {
		if _, err := m.PNG().ToBytes(); err == nil {
			t.Errorf("FloodFill with %s: run", name)
		}
	}
	// Too little memory for the bits of the picture is said, not gone over.
	if _, err := FromImage(big).FloodFill(0, 0, color.Black).MemoryLimit(300 << 10).TempDir(t.TempDir()).PNG().ToBytes(); !errors.Is(err, ErrMemoryLimit) {
		t.Errorf("FloodFill within 300KB: %v", err)
	}
}
