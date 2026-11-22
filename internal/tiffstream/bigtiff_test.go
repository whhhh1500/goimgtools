package tiffstream

import (
	"bytes"
	"encoding/binary"
	"image"
	"math/rand"
	"testing"

	"github.com/whhhh1500/goimgtools/internal/testutil"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// toBig rewrites the classic TIFF data as a BigTIFF: a header of 16 bytes,
// the rest of the file after it (every byte 8 further), and its
// directories (the first, and the EXIF, GPS and Interoperability ones it
// points to) written anew at the end in entries of 20 bytes, values of up
// to 8 bytes in the entry. With long8 the places and sizes of the blocks
// are of 8 bytes each.
func toBig(data []byte, long8 bool) []byte {
	var bo binary.ByteOrder = binary.LittleEndian
	out := []byte(bigLEHeader)
	if string(data[:4]) == beHeader {
		bo, out = binary.BigEndian, []byte(bigBEHeader)
	}
	out = append(out, make([]byte, 12)...)
	bo.PutUint16(out[4:], 8)
	out = append(out, data[8:]...)
	type ent struct {
		tag, typ uint16
		val      []byte
	}
	var dir func(off uint32) uint64
	dir = func(off uint32) uint64 {
		ents := make([]ent, bo.Uint16(data[off:]))
		for i := range ents {
			e := data[int(off)+2+ifdLen*i:][:ifdLen]
			tag, typ := bo.Uint16(e), bo.Uint16(e[2:])
			val := e[8:12]
			if size := lengths[typ] * bo.Uint32(e[4:]); size > 4 {
				val = data[bo.Uint32(val):][:size]
			} else {
				val = val[:size]
			}
			val = append([]byte(nil), val...)
			switch tag {
			case 34665, 34853, 40965:
				sub := dir(bo.Uint32(val))
				typ, val = dtIFD8, make([]byte, 8)
				bo.PutUint64(val, sub)
			case tStripOffsets, tTileOffsets, tStripByteCounts, tTileByteCounts:
				vals := make([]uint64, len(val)/int(lengths[typ]))
				for k := range vals {
					if typ == dtShort {
						vals[k] = uint64(bo.Uint16(val[2*k:]))
					} else {
						vals[k] = uint64(bo.Uint32(val[4*k:]))
					}
					if tag == tStripOffsets || tag == tTileOffsets {
						vals[k] += 8
					}
				}
				if typ, val = dtLong, make([]byte, 4*len(vals)); long8 {
					typ, val = dtLong8, make([]byte, 8*len(vals))
				}
				for k, v := range vals {
					if long8 {
						bo.PutUint64(val[8*k:], v)
					} else {
						bo.PutUint32(val[4*k:], uint32(v))
					}
				}
			}
			ents[i] = ent{tag, typ, val}
		}
		pos := len(out)
		table := make([]byte, 8+bigIFDLen*len(ents)+8)
		bo.PutUint64(table, uint64(len(ents)))
		var extra []byte
		for i, e := range ents {
			p := table[8+bigIFDLen*i:]
			bo.PutUint16(p, e.tag)
			bo.PutUint16(p[2:], e.typ)
			bo.PutUint64(p[4:], uint64(len(e.val))/uint64(lengths[e.typ]))
			if len(e.val) <= 8 {
				copy(p[12:20], e.val)
			} else {
				bo.PutUint64(p[12:], uint64(pos+len(table)+len(extra)))
				extra = append(extra, e.val...)
			}
		}
		out = append(append(out, table...), extra...)
		return uint64(pos)
	}
	first := dir(bo.Uint32(data[4:])) // out grows
	bo.PutUint64(out[8:], first)
	return out
}

// sameImage is same, and for CMYK the same inks.
func sameImage(want, got image.Image) error {
	if w, ok := want.(*image.CMYK); ok {
		want = &image.NRGBA{Pix: w.Pix, Stride: w.Stride, Rect: w.Rect}
		if g, ok := got.(*image.CMYK); ok {
			got = &image.NRGBA{Pix: g.Pix, Stride: g.Stride, Rect: g.Rect}
		}
	}
	return same(want, got)
}

// TestBigTIFF: a BigTIFF reads as the classic file of the same content
// does, in every mode, compression, byte order and layout, its block
// offsets of 4 bytes or of 8; so do its profile, XMP packet, resolution
// and EXIF block.
func TestBigTIFF(t *testing.T) {
	r := rand.New(rand.NewSource(23))
	modes := append([]pixelMode{
		{"gray4", []uint32{4}, pBlackIsZero, 0},
		{"paletted4", []uint32{4}, pPaletted, 0},
		{"graya8", []uint32{8, 8}, pBlackIsZero, 2},
		{"cmyk8", []uint32{8, 8, 8, 8}, pCMYK, 0},
		{"cmyk16", []uint32{16, 16, 16, 16}, pCMYK, 0},
	}, pixelModes...)
	n := 0
	for _, m := range modes {
		for _, comp := range []uint32{0, cLZW, cDeflate, cPackBits} {
			for _, bo := range bothOrders {
				for _, l := range []layout{{}, {rps: 3}, {tw: 16, th: 16}, {rps: 5}} {
					s := spec{w: 52, h: 37, bps: m.bps, photo: m.photo, extra: m.extra, comp: comp, bo: bo,
						rps: l.rps, tw: l.tw, th: l.th, pred: l.rps == 5 && m.bps[0] >= 8, ifdFirst: l.rps == 3}
					data := build(s, r)
					want, err := decodeStream(data, nil)
					if err != nil {
						t.Fatalf("%s: %v", s, err)
					}
					for _, long8 := range []bool{false, true} {
						got, err := decodeStream(toBig(data, long8), nil)
						if err != nil {
							t.Errorf("%s as a BigTIFF (long8 %v): %v", s, long8, err)
						} else if err := sameImage(want, got); err != nil {
							t.Errorf("%s as a BigTIFF (long8 %v): %v", s, long8, err)
						}
						n++
					}
				}
			}
		}
		if t.Failed() {
			return
		}
	}
	t.Logf("%d files", n)

	// The EXIF block is the classic file's, byte for byte.
	for _, c := range []struct {
		bo     binary.AppendByteOrder
		header string
	}{{binary.LittleEndian, leHeader}, {binary.BigEndian, beHeader}} {
		file := tiffWithExif(c.bo, c.header)
		d, err := newDecoder(bytes.NewReader(file))
		if err != nil || len(d.exif) == 0 {
			t.Fatalf("classic: %v, %d bytes of EXIF", err, len(d.exif))
		}
		big, err := newDecoder(bytes.NewReader(toBig(file, false)))
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(big.exif, d.exif) {
			t.Errorf("EXIF of the BigTIFF (%q): %d bytes, differ from the classic file's %d", c.header, len(big.exif), len(d.exif))
		}
	}

	// What a file this package writes carries.
	m := image.NewNRGBA(image.Rect(0, 0, 40, 30))
	r.Read(m.Pix)
	icc, xmp := make([]byte, 700), []byte("<x:xmpmeta xmlns:x='adobe:ns:meta/'></x:xmpmeta>")
	r.Read(icc)
	var buf bytes.Buffer
	env := pipeline.NewEnv(0, t.TempDir())
	if err := Encode(&buf, testutil.ChoppedEnv(m, 7, env), &Options{Compression: Deflate, ICC: icc, Xmp: xmp, Density: 300}); err != nil {
		t.Fatal(err)
	}
	meta := func(data []byte) (pipeline.Meta, image.Image) {
		t.Helper()
		src, err := Open(bytes.NewReader(data), int64(len(data)), nil)
		if err != nil {
			t.Fatal(err)
		}
		mt := src.(interface{ Meta() pipeline.Meta }).Meta()
		src.Close()
		img, err := decodeStream(data, nil)
		if err != nil {
			t.Fatal(err)
		}
		return mt, img
	}
	wantMeta, wantImg := meta(buf.Bytes())
	if !bytes.Equal(wantMeta.ICC, icc) || !bytes.Equal(wantMeta.Xmp, xmp) || wantMeta.Density == (pipeline.Meta{}).Density {
		t.Fatalf("classic: profile of %d bytes, packet of %d, density %v", len(wantMeta.ICC), len(wantMeta.Xmp), wantMeta.Density)
	}
	gotMeta, gotImg := meta(toBig(buf.Bytes(), true))
	if !bytes.Equal(gotMeta.ICC, icc) || !bytes.Equal(gotMeta.Xmp, xmp) || gotMeta.Density != wantMeta.Density {
		t.Errorf("BigTIFF: profile of %d bytes, packet of %d, density %v (want %v)", len(gotMeta.ICC), len(gotMeta.Xmp), gotMeta.Density, wantMeta.Density)
	}
	if err := same(wantImg, gotImg); err != nil {
		t.Errorf("BigTIFF of a written file: %v", err)
	}
}

// TestBigTIFFDamaged: a BigTIFF cut short or with bytes changed is an
// error or an image, never a panic; one whose offsets are not of 8 bytes
// and a classic file with values of 8 bytes are not read.
func TestBigTIFFDamaged(t *testing.T) {
	r := rand.New(rand.NewSource(29))
	s := spec{w: 40, h: 30, bps: []uint32{8, 8, 8}, photo: pRGB, comp: cLZW, bo: binary.LittleEndian, rps: 4}
	classic := build(s, r)
	file := toBig(classic, true)
	if _, err := decodeStream(file, nil); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < len(file); n++ {
		if n < 16 || n > len(file)-400 || n%7 == 0 {
			if _, err := decodeStream(file[:n], nil); err == nil && n < len(file)-8 {
				t.Errorf("cut at %d of %d: read", n, len(file))
			}
		}
	}
	// The directory is at the end: damage there, and in the header.
	for i := 0; i < 3000; i++ {
		bad := append([]byte(nil), file...)
		for k := 1 + r.Intn(3); k > 0; k-- {
			at := len(bad) - 1 - r.Intn(400)
			if r.Intn(8) == 0 {
				at = r.Intn(16)
			}
			bad[at] = byte(r.Intn(256))
		}
		decodeStream(bad, nil)
	}
	bad := append([]byte(nil), file...)
	bad[4] = 4
	if _, err := decodeStream(bad, nil); err == nil {
		t.Error("offsets of 4 bytes in a BigTIFF: read")
	}
	// The classic file with the type of StripOffsets made LONG8.
	bad = append([]byte(nil), classic...)
	off := binary.LittleEndian.Uint32(bad[4:])
	for i := 0; i < int(binary.LittleEndian.Uint16(bad[off:])); i++ {
		if e := bad[int(off)+2+ifdLen*i:]; binary.LittleEndian.Uint16(e) == tStripOffsets {
			binary.LittleEndian.PutUint16(e[2:], dtLong8)
			binary.LittleEndian.PutUint32(e[4:], binary.LittleEndian.Uint32(e[4:])/2)
		}
	}
	if _, err := decodeStream(bad, nil); err == nil {
		t.Error("values of 8 bytes in a classic TIFF: read")
	}
}
