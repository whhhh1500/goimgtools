package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// TestTGAAsPillow: TGA files are Pillow's run-length compressed ones from
// the top row, to the byte, in true colour with and without alpha, grey
// and colour-mapped; Pillow's files, stored or compressed, from either
// corner, read as Pillow reads them.
func TestTGAAsPillow(t *testing.T) {
	src := pillowFile(t, "fmt-source.png")
	for _, c := range []struct {
		file string
		m    *Image
	}{
		{"fmt-tga-rgba.tga", FromBytes(src).TGA()},
		{"fmt-tga-rgb.tga", FromBytes(src).RemoveAlpha().TGA()},
		{"fmt-tga-l.tga", FromBytes(src).RemoveAlpha().Grayscale().TGA()},
		{"fmt-tga-p.tga", FromBytes(pillowFile(t, "fmt-p.png")).TGA()},
	} {
		got, err := c.m.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pillowFile(t, c.file)) {
			t.Errorf("%s: not Pillow's file", c.file)
		}
	}
	for file, want := range map[string]*Image{
		"fmt-tga-rgba.tga":     FromBytes(src),
		"fmt-tga-rgba-raw.tga": FromBytes(src),
		"fmt-tga-rgba-rle.tga": FromBytes(src),
		"fmt-tga-rgb.tga":      FromBytes(src).RemoveAlpha(),
		"fmt-tga-l.tga":        FromBytes(src).RemoveAlpha().Grayscale(),
		"fmt-tga-p.tga":        FromBytes(pillowFile(t, "fmt-p.png")),
		"fmt-tga-la.tga":       FromBytes(src).Grayscale(),
	} {
		a, err1 := FromBytes(pillowFile(t, file)).ToImage()
		b, err2 := want.ToImage()
		if err1 != nil || err2 != nil {
			t.Fatal(file, err1, err2)
		}
		if !qoiSame8(a, b) {
			t.Errorf("%s reads as another picture", file)
		}
	}
}

// TestTGA: the attribute bit of 16-bit pixels is their opacity when the
// header gives one bit of it, the extension area of TGA 2.0 says whether
// a fourth byte is alpha (or alpha premultiplied), a run may run on into
// the next row, a row may be stored right to left; any image is written
// and read back; broken files are errors.
func TestTGA(t *testing.T) {
	head := func(imgType, depth, desc, w, h int) []byte {
		b := make([]byte, 18)
		b[2] = byte(imgType)
		binary.LittleEndian.PutUint16(b[12:], uint16(w))
		binary.LittleEndian.PutUint16(b[14:], uint16(h))
		b[16], b[17] = byte(depth), byte(desc)
		return b
	}
	at := func(data []byte, x, y int) color.NRGBA {
		t.Helper()
		m, err := FromBytes(data).ToImage()
		if err != nil {
			t.Fatal(err)
		}
		return color.NRGBAModel.Convert(m.At(x, y)).(color.NRGBA)
	}
	px16 := binary.LittleEndian.AppendUint16(binary.LittleEndian.AppendUint16(nil, 0x8000|31<<10), 31<<5)
	if c := at(append(head(2, 16, 0x21, 2, 1), px16...), 1, 0); c != (color.NRGBA{0, 255, 0, 0}) && c.A != 0 {
		t.Errorf("16 bits, attribute clear: %v", c)
	}
	if c := at(append(head(2, 16, 0x21, 2, 1), px16...), 0, 0); c != (color.NRGBA{255, 0, 0, 255}) {
		t.Errorf("16 bits, attribute set: %v", c)
	}
	if c := at(append(head(2, 16, 0x20, 2, 1), px16...), 1, 0); c.A != 255 {
		t.Errorf("16 bits without alpha bits: %v", c)
	}
	// 32 bits with an extension area saying alpha premultiplied, then no alpha.
	ext := func(attr byte) []byte {
		file := append(head(2, 32, 0x28, 1, 1), 50, 100, 150, 128)
		e := make([]byte, 495)
		binary.LittleEndian.PutUint16(e, 495)
		e[494] = attr
		off := len(file)
		file = append(file, e...)
		file = binary.LittleEndian.AppendUint32(file, uint32(off))
		file = append(file, 0, 0, 0, 0)
		return append(file, "TRUEVISION-XFILE.\x00"...)
	}
	if c := at(ext(4), 0, 0); c != (color.NRGBA{255, 199, 100, 128}) {
		t.Errorf("premultiplied: %v", c)
	}
	if c := at(ext(0), 0, 0); c != (color.NRGBA{150, 100, 50, 255}) {
		t.Errorf("no alpha: %v", c)
	}
	// A run across rows, from the bottom row up, right to left.
	file := append(head(11, 8, 0x10, 3, 2), 0x82, 7, 0x02, 1, 2, 3)
	m, err := FromBytes(file).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	var got []uint8
	for y := 0; y < 2; y++ {
		for x := 0; x < 3; x++ {
			got = append(got, color.GrayModel.Convert(m.At(x, y)).(color.Gray).Y)
		}
	}
	if !bytes.Equal(got, []byte{3, 2, 1, 7, 7, 7}) {
		t.Errorf("rows %v", got)
	}
	for name, im := range testutil.Images(23, 19) {
		b, err := FromImage(im).TGA().ToBytes()
		if err != nil {
			t.Fatal(name, err)
		}
		back, _ := FromBytes(b).ToImage()
		ref, _ := refTGA(b)
		want, _ := FromBytes(mustBytes(t, FromImage(im).QOI())).ToImage()
		if _, gray := im.(*image.Gray); gray {
			want = im
		}
		if !qoiSame8(back, ref) || !qoiSame8(back, want) && !qoiSame8(back, im) {
			t.Errorf("TGA of %s: another picture", name)
		}
	}
	for _, bad := range [][]byte{
		append(head(2, 24, 0x20, 2, 2), 1, 2, 3),
		append(head(10, 24, 0x20, 2, 2), 0x81, 1, 2),
	} {
		if _, err := FromBytes(bad).PNG().ToBytes(); !errors.Is(err, ErrFormat) {
			t.Errorf("% x: %v", bad, err)
		}
	}
}

// refTGA decodes the TGA files of this package (run-length compressed,
// from the top row) apart from its decoder.
func refTGA(b []byte) (image.Image, error) {
	if len(b) < 18+26 || string(b[len(b)-18:]) != "TRUEVISION-XFILE.\x00" || b[17]&0x20 == 0 {
		return nil, errors.New("tga: not one of ours")
	}
	w, h := int(binary.LittleEndian.Uint16(b[12:])), int(binary.LittleEndian.Uint16(b[14:]))
	bpp := int(b[16]) / 8
	p := 18 + int(b[0])
	var pal [][4]byte
	if b[1] == 1 {
		n, eb := int(binary.LittleEndian.Uint16(b[5:])), int(b[7])/8
		for i := 0; i < n; i++ {
			e := b[p+i*eb:]
			c := [4]byte{e[2], e[1], e[0], 255}
			if eb == 4 {
				c[3] = e[3]
			}
			pal = append(pal, c)
		}
		p += n * eb
	}
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h; {
		if p >= len(b)-26 {
			return nil, errors.New("tga: short")
		}
		hd := b[p]
		p++
		n := int(hd&0x7f) + 1
		for k := 0; k < n && i < w*h; k++ {
			q := b[p:]
			var c [4]byte
			switch {
			case pal != nil:
				c = pal[q[0]]
			case bpp == 1:
				c = [4]byte{q[0], q[0], q[0], 255}
			case bpp == 3:
				c = [4]byte{q[2], q[1], q[0], 255}
			default:
				c = [4]byte{q[2], q[1], q[0], q[3]}
			}
			copy(m.Pix[4*i:], c[:])
			i++
			if hd&0x80 == 0 {
				p += bpp
			}
		}
		if hd&0x80 != 0 {
			p += bpp
		}
	}
	return m, nil
}
