package tests

import (
	"encoding/binary"
	"image"
	"image/color"
	"testing"

	. "github.com/whhhh1500/goimgtools"
)

// webpTIFF wraps WebP images, one a strip of rowsPerStrip rows, in a
// little-endian TIFF with WebP compression (50001, as libtiff numbers
// it): RGB, or with alpha RGBA whose alpha is not multiplied in.
func webpTIFF(w, h, rowsPerStrip int, alpha bool, strips [][]byte) []byte {
	le := binary.LittleEndian
	out := []byte("II*\x00\x00\x00\x00\x00")
	var offsets, counts []uint32
	for _, s := range strips {
		offsets = append(offsets, uint32(len(out)))
		counts = append(counts, uint32(len(s)))
		out = append(out, s...)
		if len(out)%2 == 1 {
			out = append(out, 0)
		}
	}
	samples := uint32(3)
	if alpha {
		samples = 4
	}
	bits := make([]uint32, samples)
	for i := range bits {
		bits[i] = 8
	}
	type entry struct {
		tag, typ uint16
		vals     []uint32
	}
	entries := []entry{
		{256, 4, []uint32{uint32(w)}}, {257, 4, []uint32{uint32(h)}}, {258, 3, bits}, {259, 3, []uint32{50001}},
		{262, 3, []uint32{2}}, {273, 4, offsets}, {277, 3, []uint32{samples}}, {278, 4, []uint32{uint32(rowsPerStrip)}},
		{279, 4, counts},
	}
	if alpha {
		entries = append(entries, entry{338, 3, []uint32{2}})
	}
	ifd := uint32(len(out))
	le.PutUint32(out[4:], ifd)
	extra := ifd + 2 + uint32(12*len(entries)) + 4
	var table, values []byte
	table = le.AppendUint16(table, uint16(len(entries)))
	for _, e := range entries {
		var payload []byte
		for _, v := range e.vals {
			if e.typ == 3 {
				payload = le.AppendUint16(payload, uint16(v))
			} else {
				payload = le.AppendUint32(payload, v)
			}
		}
		table = le.AppendUint16(table, e.tag)
		table = le.AppendUint16(table, e.typ)
		table = le.AppendUint32(table, uint32(len(e.vals)))
		if len(payload) <= 4 {
			table = append(table, append(payload, make([]byte, 4-len(payload))...)...)
		} else {
			table = le.AppendUint32(table, extra+uint32(len(values)))
			values = append(values, payload...)
		}
	}
	table = le.AppendUint32(table, 0)
	return append(append(out, table...), values...)
}

// TestTIFFWebP: a TIFF whose strips are WebP images (libtiff's WebP
// compression, which libvips writes) gives the pixels each WebP gives as a
// file, lossy and lossless, with and without alpha.
func TestTIFFWebP(t *testing.T) {
	photo := photoLike(70, 45)
	clear := image.NewNRGBA(photo.Bounds())
	copy(clear.Pix, photo.Pix)
	for i := 3; i < len(clear.Pix); i += 4 {
		clear.Pix[i] = uint8(i / 4 * 7)
	}
	for _, c := range []struct {
		name  string
		img   image.Image
		alpha bool
		o     WebPOptions
	}{
		{"lossy", photo, false, WebPOptions{Quality: 80}},
		{"lossless", photo, false, WebPOptions{Lossless: true}},
		{"lossy with alpha", clear, true, WebPOptions{Quality: 70}},
		{"lossless with alpha", clear, true, WebPOptions{Lossless: true}},
	} {
		const rows = 16
		var strips [][]byte
		var want []image.Image
		for y := 0; y < 45; y += rows {
			b := must(FromImage(c.img).Crop(0, y, 70, min(rows, 45-y)).WebP(c.o).ToBytes())
			strips = append(strips, b)
			want = append(want, decodeOurs(t, b))
		}
		got, err := FromBytes(webpTIFF(70, 45, rows, c.alpha, strips)).ToImage()
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if b := got.Bounds(); b.Dx() != 70 || b.Dy() != 45 {
			t.Fatalf("%s: %v", c.name, b)
		}
		transparent := false
		for i, w := range want {
			for y := 0; y < w.Bounds().Dy(); y++ {
				for x := 0; x < 70; x++ {
					p := color.NRGBAModel.Convert(got.At(x, i*rows+y)).(color.NRGBA)
					q := color.NRGBAModel.Convert(w.At(w.Bounds().Min.X+x, w.Bounds().Min.Y+y)).(color.NRGBA)
					if p != q {
						t.Fatalf("%s: pixel (%d, %d) is %v, the strip's is %v", c.name, x, i*rows+y, p, q)
					}
					transparent = transparent || p.A != 0xff
				}
			}
		}
		if transparent != c.alpha {
			t.Errorf("%s: transparency %v", c.name, transparent)
		}
	}
	// Grey, 16 bits and a predictor do not go with WebP: a grey file is
	// not read.
	b := must(FromImage(photo).WebP(WebPOptions{Lossless: true}).ToBytes())
	bad := webpTIFF(70, 45, 45, false, [][]byte{b})
	le := binary.LittleEndian
	off := le.Uint32(bad[4:])
	for i := 0; i < int(le.Uint16(bad[off:])); i++ {
		switch e := bad[int(off)+2+12*i:]; le.Uint16(e) {
		case 258: // BitsPerSample: one sample of 8 bits
			le.PutUint32(e[4:], 1)
			le.PutUint32(e[8:], 8)
		case 262, 277: // black is zero; one sample a pixel
			le.PutUint16(e[8:], 1)
		}
	}
	if _, err := FromBytes(bad).ToImage(); err == nil {
		t.Error("a grey TIFF of WebP blocks: read")
	}
}
