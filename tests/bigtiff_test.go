package tests

import (
	"encoding/binary"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// bigTIFF is a 3x2 RGB BigTIFF in byte order bo, uncompressed, in one
// strip, with an Orientation of 6 (turn a quarter clockwise to view).
func bigTIFF(bo binary.AppendByteOrder, pix []byte) []byte {
	out := []byte("II+\x00")
	if bo == binary.AppendByteOrder(binary.BigEndian) {
		out = []byte("MM\x00+")
	}
	out = bo.AppendUint16(out, 8) // the size of an offset
	out = bo.AppendUint16(out, 0)
	out = bo.AppendUint64(out, uint64(16+len(pix))) // the directory, after the pixels
	out = append(out, pix...)
	short := func(v ...uint16) (b []byte) {
		for _, x := range v {
			b = bo.AppendUint16(b, x)
		}
		return b
	}
	entries := []struct {
		tag, typ uint16
		count    uint64
		val      []byte
	}{
		{256, 3, 1, short(3)}, {257, 3, 1, short(2)}, {258, 3, 3, short(8, 8, 8)}, {259, 3, 1, short(1)},
		{262, 3, 1, short(2)}, {273, 16, 1, bo.AppendUint64(nil, 16)}, {274, 3, 1, short(6)},
		{277, 3, 1, short(3)}, {278, 3, 1, short(2)}, {279, 16, 1, bo.AppendUint64(nil, uint64(len(pix)))},
	}
	out = bo.AppendUint64(out, uint64(len(entries)))
	for _, e := range entries {
		out = bo.AppendUint16(out, e.tag)
		out = bo.AppendUint16(out, e.typ)
		out = bo.AppendUint64(out, e.count)
		out = append(out, append(e.val, make([]byte, 8-len(e.val))...)...)
	}
	return bo.AppendUint64(out, 0) // no next directory
}

// TestBigTIFFFile: a BigTIFF is recognised as a TIFF and read, in either
// byte order, and its orientation is the image's.
func TestBigTIFFFile(t *testing.T) {
	pix := []byte{
		10, 20, 30, 40, 50, 60, 70, 80, 90,
		100, 110, 120, 130, 140, 150, 160, 170, 180,
	}
	for _, bo := range []binary.AppendByteOrder{binary.LittleEndian, binary.BigEndian} {
		in := bigTIFF(bo, pix)
		info, err := FromBytes(in).Metadata()
		if err != nil {
			t.Fatalf("%v: %v", bo, err)
		}
		if info.Format != "tiff" || info.Width != 3 || info.Height != 2 || info.Orientation != 6 {
			t.Errorf("%v: Metadata = %+v", bo, info)
		}
		m, err := FromBytes(in).ToImage()
		if err != nil {
			t.Fatalf("%v: %v", bo, err)
		}
		for i := 0; i < 6; i++ {
			want := color.NRGBA{pix[3*i], pix[3*i+1], pix[3*i+2], 0xff}
			if got := color.NRGBAModel.Convert(m.At(i%3, i/3)); got != want {
				t.Errorf("%v: pixel %d is %v, want %v", bo, i, got, want)
			}
		}
		// Turned as the orientation says: the left column, read upwards,
		// becomes the top row.
		turned, err := FromBytes(in).AutoOrient().ToImage()
		if err != nil {
			t.Fatalf("%v: %v", bo, err)
		}
		if b := turned.Bounds(); b.Dx() != 2 || b.Dy() != 3 {
			t.Fatalf("%v: turned to %v", bo, b)
		}
		if got, want := color.NRGBAModel.Convert(turned.At(0, 0)), (color.NRGBA{100, 110, 120, 0xff}); got != want {
			t.Errorf("%v: the top left after turning is %v, want %v", bo, got, want)
		}
	}
}
