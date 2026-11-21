package tests

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"os"
	"path/filepath"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// qoiSame8 reports whether two images have the same pixels on 8 bits.
func qoiSame8(a, b image.Image) bool {
	if a.Bounds().Size() != b.Bounds().Size() {
		return false
	}
	ra, rb := a.Bounds(), b.Bounds()
	for y := 0; y < ra.Dy(); y++ {
		for x := 0; x < ra.Dx(); x++ {
			if color.NRGBAModel.Convert(a.At(ra.Min.X+x, ra.Min.Y+y)) != color.NRGBAModel.Convert(b.At(rb.Min.X+x, rb.Min.Y+y)) {
				return false
			}
		}
	}
	return true
}

func pillowFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "pillow", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestQOIAndPNMAsPillow: QOI and Netpbm files are Pillow's to the byte
// (QOI marked sRGB, as Pillow marks it when asked), and Pillow's files,
// Netpbm ones of any maximum, in text or binary, bitmaps among them, read
// as Pillow reads them.
func TestQOIAndPNMAsPillow(t *testing.T) {
	src := pillowFile(t, "fmt-source.png")
	for _, c := range []struct {
		file string
		m    *Image
	}{
		{"fmt-rgba.qoi", FromBytes(src).QOI()},
		{"fmt-rgb.qoi", FromBytes(src).RemoveAlpha().QOI()},
		{"fmt-rgb.ppm", FromBytes(src).RemoveAlpha().PNM()},
		{"fmt-grey.pgm", FromBytes(src).RemoveAlpha().Grayscale().PNM()},
	} {
		got, err := c.m.ToBytes()
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, pillowFile(t, c.file)) {
			t.Errorf("%s: not Pillow's file", c.file)
		}
	}
	for _, name := range []string{"fmt-rgba.qoi", "fmt-rgb.qoi", "fmt-rgb.ppm", "fmt-grey.pgm"} {
		var want *Image
		switch name {
		case "fmt-rgba.qoi":
			want = FromBytes(src)
		case "fmt-grey.pgm":
			want = FromBytes(src).RemoveAlpha().Grayscale()
		default:
			want = FromBytes(src).RemoveAlpha()
		}
		a, err1 := FromBytes(pillowFile(t, name)).ToImage()
		b, err2 := want.ToImage()
		if err1 != nil || err2 != nil {
			t.Fatal(name, err1, err2)
		}
		if !psdSame(a, b) {
			t.Errorf("%s reads as another picture", name)
		}
	}
	for _, name := range []string{"fmt-p1.pbm", "fmt-p2.pgm", "fmt-p3.ppm", "fmt-p4.pbm", "fmt-p5-1000.pgm", "fmt-p5-7.pgm", "fmt-p6-200.ppm"} {
		a, err1 := FromBytes(pillowFile(t, name)).ToImage()
		b, err2 := FromBytes(pillowFile(t, name+".png")).ToImage()
		if err1 != nil || err2 != nil {
			t.Fatal(name, err1, err2)
		}
		if !psdSame(a, b) {
			t.Errorf("%s: not Pillow's pixels", name)
		}
	}
}

// TestQOIAndPNM: any image is written and read back without loss (16 bits
// kept in Netpbm, transparency in QOI; Netpbm lays it over black); PAM
// and PFM are read; broken files are errors.
func TestQOIAndPNM(t *testing.T) {
	for name, m := range testutil.Images(23, 19) {
		q, err := FromImage(m).QOI().ToBytes()
		if err != nil {
			t.Fatal(name, err)
		}
		a, _ := FromBytes(q).ToImage()
		ref, err := pipeline.Collect(pipeline.Map(pipeline.FromImage(m, nil), func(*image.NRGBA) error { return nil }))
		if err != nil {
			t.Fatal(err)
		}
		if !qoiSame8(a, ref) {
			t.Errorf("QOI of %s: another picture", name)
		}
		p, err := FromImage(m).PNM().ToBytes()
		if err != nil {
			t.Fatal(name, err)
		}
		pa, _ := FromBytes(p).ToImage()
		flat, _ := FromBytes(mustBytes(t, FromImage(m).Flatten(color.Black).QOI())).ToImage()
		if g, ok := m.(*image.Gray); ok {
			flat = g
		}
		if !qoiSame8(pa, flat) && !psdSame(pa, m) {
			t.Errorf("PNM of %s: another picture", name)
		}
	}
	// PAM with transparency, and grey.
	pam := []byte("P7\nWIDTH 2\nHEIGHT 1\nDEPTH 4\nMAXVAL 255\nTUPLTYPE RGB_ALPHA\nENDHDR\n\x0a\x14\x1e\x80\x01\x02\x03\xff")
	m, err := FromBytes(pam).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	if c := color.NRGBAModel.Convert(m.At(0, 0)).(color.NRGBA); c != (color.NRGBA{10, 20, 30, 128}) {
		t.Errorf("PAM: %v", c)
	}
	// PFM: little-endian (a negative scale), rows from the bottom up.
	pfm := append([]byte("Pf\n1 2\n-1.0\n"), 0, 0, 0, 0, 0, 0, 0x80, 0x3f) // 0 then 1.0
	if m, err = FromBytes(pfm).ToImage(); err != nil {
		t.Fatal(err)
	}
	if top, bottom := color.Gray16Model.Convert(m.At(0, 0)).(color.Gray16).Y, color.Gray16Model.Convert(m.At(0, 1)).(color.Gray16).Y; top != 65535 || bottom != 0 {
		t.Errorf("PFM: %d %d", top, bottom)
	}
	for _, bad := range [][]byte{
		[]byte("P5\n3 2\n255\n\x01\x02"),
		[]byte("P2\n2 1\n10\n3 11\n"),
		[]byte("P6\n0 2\n255\n"),
		[]byte("qoif\x00\x00\x00\x02\x00\x00\x00\x01\x03\x00\xfe\x01"),
		[]byte("qoif\x00\x00\x00\x02\x00\x00\x00\x01\x05\x00"),
	} {
		if _, err := FromBytes(bad).PNG().ToBytes(); !errors.Is(err, ErrFormat) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

// refQOI decodes a QOI file straight from the specification, apart from
// the package's decoder, as the reference to check its files against.
func refQOI(b []byte) (image.Image, error) {
	if len(b) < 22 || string(b[:4]) != "qoif" {
		return nil, errors.New("qoi: bad header")
	}
	w, h := int(binary.BigEndian.Uint32(b[4:])), int(binary.BigEndian.Uint32(b[8:]))
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	var index [64][4]byte
	px := [4]byte{0, 0, 0, 255}
	p, run := 14, 0
	for i := 0; i < w*h; i++ {
		if run > 0 {
			run--
		} else {
			if p >= len(b)-8 {
				return nil, errors.New("qoi: short")
			}
			op := b[p]
			p++
			switch {
			case op == 0xfe:
				copy(px[:3], b[p:p+3])
				p += 3
			case op == 0xff:
				copy(px[:], b[p:p+4])
				p += 4
			case op>>6 == 0:
				px = index[op]
			case op>>6 == 1:
				px[0] += op>>4&3 - 2
				px[1] += op>>2&3 - 2
				px[2] += op&3 - 2
			case op>>6 == 2:
				dg := op&63 - 32
				px[0] += dg + b[p]>>4 - 8
				px[1] += dg
				px[2] += dg + b[p]&15 - 8
				p++
			default:
				run = int(op & 63)
			}
			index[(int(px[0])*3+int(px[1])*5+int(px[2])*7+int(px[3])*11)%64] = px
		}
		copy(m.Pix[4*i:], px[:])
	}
	if !bytes.Equal(b[p:], []byte{0, 0, 0, 0, 0, 0, 0, 1}) {
		return nil, errors.New("qoi: no end marker")
	}
	return m, nil
}

// refPNM decodes a binary PGM or PPM of 8 or 16 bits, apart from the
// package's decoder.
func refPNM(b []byte) (image.Image, error) {
	var magic string
	var w, h, maxval int
	n, err := fmt.Sscan(string(b[:min(len(b), 64)]), &magic, &w, &h, &maxval)
	if err != nil || n != 4 || magic != "P5" && magic != "P6" {
		return nil, errors.New("pnm: bad header")
	}
	head := len(fmt.Sprintf("%s\n%d %d\n%d\n", magic, w, h, maxval))
	ch, bpv := 1, 1
	if magic == "P6" {
		ch = 3
	}
	if maxval > 255 {
		bpv = 2
	}
	if len(b) != head+w*h*ch*bpv {
		return nil, errors.New("pnm: wrong length")
	}
	m := image.NewNRGBA64(image.Rect(0, 0, w, h))
	d := b[head:]
	for i := 0; i < w*h; i++ {
		var v [3]uint16
		for c := 0; c < ch; c++ {
			if bpv == 2 {
				v[c] = binary.BigEndian.Uint16(d[2*(i*ch+c):])
			} else {
				v[c] = uint16(d[i*ch+c]) * 257
			}
		}
		if ch == 1 {
			v[1], v[2] = v[0], v[0]
		}
		m.SetNRGBA64(i%w, i/w, color.NRGBA64{v[0], v[1], v[2], 0xffff})
	}
	return m, nil
}
