package tests

import (
	"bytes"
	"encoding/binary"
	"math/rand"
	"testing"

	. "github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/inspect"
	"github.com/whhhh1500/goimgtools/internal/testutil"
)

// iptcRecord is an IPTC record: a caption, two keywords, a byline and a
// copyright notice, and filler to make it n bytes or more.
func iptcRecord(n int) []byte {
	var out []byte
	set := func(dataset byte, value string) {
		out = append(out, 0x1c, 2, dataset, byte(len(value)>>8), byte(len(value)))
		out = append(out, value...)
	}
	set(120, "A caption, with an odd length")
	set(25, "keyword one")
	set(25, "keyword two")
	set(80, "The Photographer")
	set(116, "(c) The Agency")
	for len(out) < n {
		set(40, string(bytes.Repeat([]byte("special instructions "), 100)))
	}
	if len(out)%2 == 0 {
		set(5, "an odd length in all") // so that what holds it has to pad it
	}
	return out
}

// app13 is what the APP13 segments of a JPEG hold after "Photoshop 3.0"
// and its zero, one after the other.
func app13(t *testing.T, jpg []byte) []byte {
	t.Helper()
	const head = "Photoshop 3.0\x00"
	var out []byte
	for i := 2; i+4 <= len(jpg) && jpg[i] == 0xff && jpg[i+1] != 0xda; {
		n := int(binary.BigEndian.Uint16(jpg[i+2:]))
		if seg := jpg[i+4 : i+2+n]; jpg[i+1] == 0xed && bytes.HasPrefix(seg, []byte(head)) {
			out = append(out, seg[len(head):]...)
		}
		i += 2 + n
	}
	return out
}

// photoshopResources is the image resources Photoshop writes into APP13
// segments: one before the IPTC record (with a name), the record
// (resource 0x0404), one after.
func photoshopResources(iptc []byte) []byte {
	res := func(id uint16, name string, data []byte) []byte {
		out := append([]byte("8BIM"), byte(id>>8), byte(id), byte(len(name)))
		out = append(out, name...)
		if len(out)%2 == 1 {
			out = append(out, 0)
		}
		out = binary.BigEndian.AppendUint32(out, uint32(len(data)))
		out = append(out, data...)
		if len(data)%2 == 1 {
			out = append(out, 0)
		}
		return out
	}
	out := res(0x03ed, "res", make([]byte, 15)) // of an odd length: padded
	out = append(out, res(0x0404, "", iptc)...)
	return append(out, res(0x040c, "thumbnail", []byte{1, 2, 3})...)
}

// TestIptc: the IPTC record of a JPEG (Photoshop's resource 0x0404 in
// APP13 segments, in one or split over several), of a TIFF (tag 33723, of
// bytes or of LONGs) and of a Photoshop file is read; KeepIptc keeps it in
// JPEG and TIFF output, and without it, and in the other formats, it is
// dropped.
func TestIptc(t *testing.T) {
	read := func(name string, data []byte) []byte {
		t.Helper()
		x, ok, err := inspect.ReadIptc(FromBytes(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if info, err := FromBytes(data).Metadata(); err != nil || info.HasIptc != ok {
			t.Errorf("%s: HasIptc %v, ReadIptc found %v (%v)", name, info.HasIptc, ok, err)
		}
		return x
	}
	plain := jpegBytes(photoLike(40, 30), 85)
	const head = "Photoshop 3.0\x00"
	for _, size := range []int{0, 70000, 200000} {
		rec := iptcRecord(size)
		irb := photoshopResources(rec)
		// In one segment when it fits, and in pieces of 65,000 and of
		// 1,000 bytes.
		for _, piece := range []int{65000, 1000} {
			if piece == 1000 && size > 70000 {
				continue
			}
			var segs [][]byte
			for rest := irb; len(rest) > 0; rest = rest[min(piece, len(rest)):] {
				segs = append(segs, app(0xed, append([]byte(head), rest[:min(piece, len(rest))]...)))
			}
			in := withAPP(plain, segs...)
			name := "JPEG"
			if got := read(name, in); !bytes.Equal(got, rec) {
				t.Fatalf("%s (%d bytes, in %d segments): read %d bytes", name, len(rec), len(segs), len(got))
			}
			if got := read("JPEG to JPEG, not kept", must(FromBytes(in).JPEG().ToBytes())); got != nil {
				t.Errorf("IPTC kept without KeepIptc")
			}
			jpg := must(FromBytes(in).KeepIptc().JPEG().ToBytes())
			if got := read("JPEG to JPEG", jpg); !bytes.Equal(got, rec) {
				t.Errorf("JPEG to JPEG (%d bytes): %d bytes kept", len(rec), len(got))
			}
			// Written as Photoshop writes a resource: its number, no
			// name, its length, the record, padded to an even length.
			want := append([]byte("8BIM\x04\x04\x00\x00"), byte(len(rec)>>24), byte(len(rec)>>16), byte(len(rec)>>8), byte(len(rec)))
			want = append(append(want, rec...), 0)
			if got := app13(t, jpg); len(rec)%2 != 1 || !bytes.Equal(got, want) {
				t.Errorf("the resource written: %d bytes (a record of %d), want %d", len(got), len(rec), len(want))
			}
			tif := must(FromBytes(in).KeepIptc().TIFF().ToBytes())
			if got := read("JPEG to TIFF", tif); !bytes.Equal(got, rec) {
				t.Errorf("JPEG to TIFF (%d bytes): %d bytes kept", len(rec), len(got))
			}
			if got := read("TIFF to JPEG", must(FromBytes(tif).KeepIptc().JPEG().ToBytes())); !bytes.Equal(got, rec) {
				t.Errorf("TIFF to JPEG (%d bytes): %d bytes kept", len(rec), len(got))
			}
			if got := read("TIFF, JPEG compression", must(FromBytes(in).KeepIptc().TIFF(TIFFOptions{Compression: TIFFJPEG}).ToBytes())); !bytes.Equal(got, rec) {
				t.Errorf("JPEG to a TIFF of JPEG (%d bytes): %d bytes kept", len(rec), len(got))
			}
			for _, f := range []string{"png", "webp", "gif"} {
				out, err := FromBytes(in).KeepIptc().ToFormat(f).ToBytes()
				if err != nil {
					t.Errorf("to %s with KeepIptc: %v", f, err)
				} else if got := read("JPEG to "+f, out); got != nil {
					t.Errorf("to %s: an IPTC record", f)
				}
			}
			// The pixels are those of the file without the record.
			if !bytes.Equal(must(FromBytes(in).PNG().ToBytes()), must(FromBytes(plain).PNG().ToBytes())) {
				t.Error("the record changed the pixels")
			}
		}
	}

	rec := iptcRecord(0)
	// A TIFF whose record is said to be of LONGs, as Photoshop writes it,
	// in either byte order (the words' bytes swapped by a writer that took
	// them for numbers).
	tif := must(FromImage(photoLike(8, 8)).TIFF().ToBytes())
	padded := append(append([]byte(nil), rec...), make([]byte, (4-len(rec)%4)%4)...)
	for _, swapped := range []bool{false, true} {
		words := append([]byte(nil), padded...)
		if swapped {
			for i := 0; i < len(words); i += 4 {
				words[i], words[i+1], words[i+2], words[i+3] = words[i+3], words[i+2], words[i+1], words[i]
			}
		}
		in := tiffWithEntry(tif, 33723, 4, uint32(len(words)/4), words)
		if got := read("TIFF of LONGs", in); !bytes.Equal(got, padded) {
			t.Errorf("a TIFF record of LONGs (swapped %v): %d bytes, want %d", swapped, len(got), len(padded))
		}
	}
	// As libvips writes the record of a JPEG into a TIFF: wrapped in
	// Photoshop's resources, as LONGs.
	wrapped := append([]byte("Photoshop 3.0\x00"), photoshopResources(rec)...)
	wrapped = append(wrapped, make([]byte, (4-len(wrapped)%4)%4)...)
	if got := read("TIFF, wrapped", tiffWithEntry(tif, 33723, 4, uint32(len(wrapped)/4), wrapped)); !bytes.Equal(got, rec) {
		t.Errorf("a TIFF record in Photoshop's resources: %d bytes, want %d", len(got), len(rec))
	}
	// What is not a record (no dataset at its start) is not taken for one.
	if got := read("TIFF, not a record", tiffWithEntry(tif, 33723, 7, 8, []byte("notiptc!"))); got != nil {
		t.Error("eight bytes that are no record: read as one")
	}

	// A Photoshop file: image resource 1028.
	psd := testutil.PSD(testutil.PSDFile{Mode: 1, Depth: 8, W: 4, H: 3, Channels: [][]byte{make([]byte, 12)},
		Res: map[int][]byte{1028: rec}})
	if got := read("PSD", psd); !bytes.Equal(got, rec) {
		t.Errorf("PSD: %d bytes read", len(got))
	}
	if got := read("PSD to JPEG", must(FromBytes(psd).KeepIptc().JPEG().ToBytes())); !bytes.Equal(got, rec) {
		t.Errorf("PSD to JPEG: %d bytes kept", len(got))
	}

	// Damaged resources: the image is read, with a record or without.
	r := rand.New(rand.NewSource(61))
	irb := photoshopResources(rec)
	for i := 0; i < 2000; i++ {
		bad := append([]byte(nil), irb...)
		switch r.Intn(3) {
		case 0:
			bad = bad[:r.Intn(len(bad))]
		default:
			for k := 1 + r.Intn(4); k > 0; k-- {
				bad[r.Intn(len(bad))] = byte(r.Intn(256))
			}
		}
		in := withAPP(plain, app(0xed, append([]byte(head), bad...)))
		if _, err := FromBytes(in).KeepIptc().JPEG().ToBytes(); err != nil {
			t.Fatalf("damaged resources: %v", err)
		}
	}
}

// tiffWithEntry adds an entry (tag, type, count, its values' bytes, longer
// than 4) to the first directory of the little-endian TIFF data, which is
// written anew at the end.
func tiffWithEntry(data []byte, tag, typ uint16, count uint32, value []byte) []byte {
	le := binary.LittleEndian
	off := le.Uint32(data[4:])
	n := int(le.Uint16(data[off:]))
	out := append([]byte(nil), data...)
	valueAt := uint32(len(out))
	out = append(out, value...)
	if len(out)%2 == 1 {
		out = append(out, 0)
	}
	dir := uint32(len(out))
	entries := make([][]byte, 0, n+1)
	for i := 0; i < n; i++ {
		entries = append(entries, data[int(off)+2+12*i:][:12])
	}
	e := make([]byte, 12)
	le.PutUint16(e, tag)
	le.PutUint16(e[2:], typ)
	le.PutUint32(e[4:], count)
	le.PutUint32(e[8:], valueAt)
	at := len(entries)
	for i, old := range entries {
		if le.Uint16(old) > tag {
			at = i
			break
		}
	}
	entries = append(entries[:at], append([][]byte{e}, entries[at:]...)...)
	out = le.AppendUint16(out, uint16(len(entries)))
	for _, e := range entries {
		out = append(out, e...)
	}
	out = le.AppendUint32(out, 0)
	le.PutUint32(out[4:], dir)
	return out
}
