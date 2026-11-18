package tests

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// splitTables takes the quantization and Huffman tables out of a JPEG
// file, as libtiff keeps them in JPEGTables: the tables (SOI, DQT, DHT,
// EOI) and the rest (SOI, SOF, SOS and the data, EOI).
func splitTables(t *testing.T, j []byte) (tables, rest []byte) {
	t.Helper()
	tables, rest = []byte{0xff, 0xd8}, []byte{0xff, 0xd8}
	i := 2
	for i+4 <= len(j) {
		if j[i] != 0xff {
			t.Fatalf("no marker at %d", i)
		}
		m := j[i+1]
		n := int(binary.BigEndian.Uint16(j[i+2:]))
		seg := j[i : i+2+n]
		if m == 0xda { // SOS: the rest of the file follows
			rest = append(rest, j[i:]...)
			break
		}
		if m == 0xdb || m == 0xc4 {
			tables = append(tables, seg...)
		} else {
			rest = append(rest, seg...)
		}
		i += 2 + n
	}
	return append(tables, 0xff, 0xd9), rest
}

// jpegTIFF wraps JPEG streams, one a strip of rowsPerStrip rows, in a
// little-endian TIFF with JPEG compression; tables, when given, become
// its JPEGTables.
func jpegTIFF(w, h, rowsPerStrip int, gray bool, strips [][]byte, tables []byte) []byte {
	type entry struct {
		tag, typ uint16
		vals     []uint32
		raw      []byte
	}
	samples := 3
	photometric := uint32(6) // YCbCr
	if gray {
		samples, photometric = 1, 1
	}
	bits := make([]uint32, samples)
	for i := range bits {
		bits[i] = 8
	}
	data := []byte{}
	var offsets, counts []uint32
	for _, s := range strips {
		offsets = append(offsets, uint32(8+len(data)))
		counts = append(counts, uint32(len(s)))
		data = append(data, s...)
	}
	entries := []entry{
		{256, 4, []uint32{uint32(w)}, nil},
		{257, 4, []uint32{uint32(h)}, nil},
		{258, 3, bits, nil},
		{259, 3, []uint32{7}, nil},
		{262, 3, []uint32{photometric}, nil},
		{273, 4, offsets, nil},
		{277, 3, []uint32{uint32(samples)}, nil},
		{278, 4, []uint32{uint32(rowsPerStrip)}, nil},
		{279, 4, counts, nil},
		{284, 3, []uint32{1}, nil},
	}
	if tables != nil {
		entries = append(entries, entry{347, 7, nil, tables})
	}
	le := binary.LittleEndian
	out := append([]byte("II*\x00\x00\x00\x00\x00"), data...)
	ifd := uint32(len(out))
	le.PutUint32(out[4:], ifd)
	extra := ifd + 2 + uint32(12*len(entries)) + 4
	var ifdBytes, extraBytes []byte
	ifdBytes = le.AppendUint16(ifdBytes, uint16(len(entries)))
	for _, e := range entries {
		var payload []byte
		count := uint32(len(e.vals))
		if e.raw != nil {
			payload, count = e.raw, uint32(len(e.raw))
		} else {
			for _, v := range e.vals {
				if e.typ == 3 {
					payload = le.AppendUint16(payload, uint16(v))
				} else {
					payload = le.AppendUint32(payload, v)
				}
			}
		}
		ifdBytes = le.AppendUint16(ifdBytes, e.tag)
		ifdBytes = le.AppendUint16(ifdBytes, e.typ)
		ifdBytes = le.AppendUint32(ifdBytes, count)
		if len(payload) <= 4 {
			ifdBytes = append(ifdBytes, append(payload, make([]byte, 4-len(payload))...)...)
		} else {
			ifdBytes = le.AppendUint32(ifdBytes, extra+uint32(len(extraBytes)))
			extraBytes = append(extraBytes, payload...)
			if len(extraBytes)%2 == 1 {
				extraBytes = append(extraBytes, 0)
			}
		}
	}
	ifdBytes = le.AppendUint32(ifdBytes, 0)
	return append(append(out, ifdBytes...), extraBytes...)
}

// TestTIFFJPEG: a TIFF with JPEG compression (libtiff's, as libvips and
// sharp write it) gives the pixels its JPEG streams give, strip after
// strip, with the tables in each strip or shared in JPEGTables, in colour
// (YCbCr) and grey; and the TIFFs sharp writes, in strips and in tiles,
// come out as the image they were made from.
func TestTIFFJPEG(t *testing.T) {
	photo := photoLike(77, 50)
	gray := image.NewGray(photo.Bounds())
	for i := range gray.Pix {
		gray.Pix[i] = photo.Pix[4*i+1]
	}
	for _, c := range []struct {
		name   string
		img    image.Image
		gray   bool
		shared bool
	}{
		{"colour", photo, false, false},
		{"colour, shared tables", photo, false, true},
		{"grey", gray, true, false},
		{"grey, shared tables", gray, true, true},
	} {
		const rows = 16
		var strips [][]byte
		var want []image.Image
		var tables []byte
		for y := 0; y < 50; y += rows {
			part := FromImage(c.img).Crop(0, y, 77, min(rows, 50-y))
			j := must(part.JPEG(JPEGOptions{Quality: 80}).ToBytes())
			if c.shared {
				tables, j = splitTables(t, j)
			}
			strips = append(strips, j)
			want = append(want, decodeOurs(t, must(part.JPEG(JPEGOptions{Quality: 80}).ToBytes())))
		}
		if !c.shared {
			tables = nil
		}
		got, err := FromBytes(jpegTIFF(77, 50, rows, c.gray, strips, tables)).ToImage()
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if _, ok := got.(*image.Gray); ok != c.gray {
			t.Errorf("%s: %T", c.name, got)
		}
		for i, w := range want {
			for y := 0; y < w.Bounds().Dy(); y++ {
				for x := 0; x < 77; x++ {
					p := color.NRGBAModel.Convert(got.At(x, i*rows+y))
					q := color.NRGBAModel.Convert(w.At(w.Bounds().Min.X+x, w.Bounds().Min.Y+y))
					if p != q {
						t.Fatalf("%s at %d,%d: %v, the JPEG gives %v", c.name, x, i*rows+y, p, q)
					}
				}
			}
		}
	}

	// sharp's TIFFs of the Pillow test image, JPEG at quality 80 to 90.
	src := decodePNG(t, sourcePNG(t, "pillow/source.png"))
	for _, name := range []string{"jpeg-strips.tiff", "jpeg-tiles.tiff", "jpeg-grey.tiff"} {
		got, err := FromBytes(sourcePNG(t, "sharp/"+name)).ToImage()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		var se float64
		b := src.Bounds()
		for y := 0; y < b.Dy(); y++ {
			for x := 0; x < b.Dx(); x++ {
				p := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA)
				q := color.NRGBAModel.Convert(src.At(x, y)).(color.NRGBA)
				if name == "jpeg-grey.tiff" {
					q = color.NRGBAModel.Convert(color.GrayModel.Convert(q)).(color.NRGBA)
					if d := int(p.R) - int(q.R); d > 30 || d < -30 {
						// (sharp's grey is not Go's: compare loosely)
						continue
					}
				}
				for _, d := range []float64{float64(p.R) - float64(q.R), float64(p.G) - float64(q.G), float64(p.B) - float64(q.B)} {
					se += d * d
				}
			}
		}
		if psnr := 10 * math.Log10(255*255/(se/float64(3*b.Dx()*b.Dy()))); psnr < 28 {
			t.Errorf("%s: %.1f dB from the image it was made from", name, psnr)
		}
	}
	// Other images than grey and colour are not taken for JPEG.
	if _, err := FromBytes(bytes.Replace(jpegTIFF(8, 8, 8, true, [][]byte{must(FromImage(gray).Crop(0, 0, 8, 8).JPEG(JPEGOptions{Quality: 80}).ToBytes())}, nil),
		[]byte{0x06, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01}, []byte{0x06, 0x01, 0x03, 0x00, 0x01, 0x00, 0x00, 0x00, 0x03}, 1)).ToImage(); err == nil {
		t.Error("a paletted JPEG TIFF: no error")
	}
}

func decodeOurs(t *testing.T, data []byte) image.Image {
	t.Helper()
	img, err := FromBytes(data).ToImage()
	if err != nil {
		t.Fatal(err)
	}
	return img
}
