package tiffstream

import (
	"bytes"
	"encoding/binary"
	"testing"
)

// tiffWithExif builds a 2x1 grey TIFF in byte order bo whose first
// directory has a Make, an Orientation and the EXIF and GPS directories, the
// EXIF one with a long value and an Interoperability directory.
func tiffWithExif(bo binary.AppendByteOrder, header string) []byte {
	type entry struct {
		tag, typ uint16
		count    uint32
		value    []byte // the value, or what the offset points to
	}
	b := []byte(header)
	b = bo.AppendUint32(b, 8)
	// Directories are written at their offsets; values that do not fit
	// after the directory.
	var dir func(entries []entry, sub map[uint16][]entry) uint32
	dir = func(entries []entry, sub map[uint16][]entry) uint32 {
		start := uint32(len(b))
		b = bo.AppendUint16(b, uint16(len(entries)))
		at := len(b)
		b = append(b, make([]byte, 12*len(entries)+4)...)
		for i, e := range entries {
			p := at + 12*i
			eb := bo.AppendUint16(nil, e.tag)
			eb = bo.AppendUint16(eb, e.typ)
			eb = bo.AppendUint32(eb, e.count)
			if s, ok := sub[e.tag]; ok {
				eb = bo.AppendUint32(eb, dir(s, sub))
			} else if len(e.value) <= 4 {
				eb = append(eb, append(e.value, make([]byte, 4-len(e.value))...)...)
			} else {
				eb = bo.AppendUint32(eb, uint32(len(b)))
				b = append(b, e.value...)
			}
			copy(b[p:], eb)
		}
		return start
	}
	short := func(v uint16) []byte { return bo.AppendUint16(nil, v) }
	long := func(v uint32) []byte { return bo.AppendUint32(nil, v) }
	rational := append(long(28), long(10)...)
	sub := map[uint16][]entry{
		34665: {{36867, 2, 20, []byte("2024:01:02 03:04:05\x00")}, {33437, 5, 1, rational}, {40965, 4, 1, nil}},
		40965: {{1, 2, 4, []byte("R98\x00")}},
		34853: {{0, 1, 4, []byte{2, 3, 0, 0}}},
	}
	dir([]entry{
		{256, 3, 1, short(2)}, {257, 3, 1, short(1)}, {258, 3, 1, short(8)}, {262, 3, 1, short(1)},
		{271, 2, 9, []byte("TestMake\x00")}, {273, 4, 1, long(0)}, {274, 3, 1, short(6)},
		{277, 3, 1, short(1)}, {279, 4, 1, long(2)}, {34665, 4, 1, nil}, {34853, 4, 1, nil},
	}, sub)
	// The strip: two pixels at the end, StripOffsets pointed at them.
	off := uint32(len(b))
	b = append(b, 10, 200)
	n := int(bo.(binary.ByteOrder).Uint16(b[8:]))
	for i := 0; i < n; i++ {
		e := b[10+12*i:]
		if bo.(binary.ByteOrder).Uint16(e) == 273 {
			bo.(binary.ByteOrder).PutUint32(e[8:], off)
		}
	}
	return b
}

// TestExif: the EXIF block of a TIFF, in either byte order: its first
// directory holds the tags that describe the picture (not how it is
// stored), and the EXIF, GPS and Interoperability directories follow with
// their values, long ones copied.
func TestExif(t *testing.T) {
	for _, c := range []struct {
		bo     binary.ByteOrder
		header string
	}{{binary.LittleEndian, leHeader}, {binary.BigEndian, beHeader}} {
		file := tiffWithExif(c.bo.(binary.AppendByteOrder), c.header)
		d, err := newDecoder(bytes.NewReader(file))
		if err != nil {
			t.Fatal(err)
		}
		x := d.exif
		bo := c.bo
		if !bytes.HasPrefix(x, []byte(c.header)) {
			t.Fatalf("header %q", x[:4])
		}
		// read returns the entries of the directory at off by tag.
		read := func(off uint32) map[uint16][]byte {
			n := int(bo.Uint16(x[off:]))
			m := map[uint16][]byte{}
			for i := 0; i < n; i++ {
				e := x[int(off)+2+12*i:][:12]
				m[bo.Uint16(e)] = e
			}
			return m
		}
		value := func(e []byte, n int) []byte {
			if n <= 4 {
				return e[8 : 8+n]
			}
			o := bo.Uint32(e[8:])
			return x[o : int(o)+n]
		}
		ifd0 := read(bo.Uint32(x[4:]))
		if len(ifd0) != 4 || ifd0[256] != nil || ifd0[273] != nil {
			t.Fatalf("first directory: tags %v", keys(ifd0))
		}
		if got := string(value(ifd0[271], 9)); got != "TestMake\x00" {
			t.Errorf("Make %q", got)
		}
		if got := bo.Uint16(value(ifd0[274], 2)); got != 6 {
			t.Errorf("Orientation %d", got)
		}
		ex := read(bo.Uint32(ifd0[34665][8:]))
		if got := string(value(ex[36867], 20)); got != "2024:01:02 03:04:05\x00" {
			t.Errorf("DateTimeOriginal %q", got)
		}
		if r := value(ex[33437], 8); bo.Uint32(r) != 28 || bo.Uint32(r[4:]) != 10 {
			t.Errorf("FNumber %v", r)
		}
		if got := string(value(read(bo.Uint32(ex[40965][8:]))[1], 4)); got != "R98\x00" {
			t.Errorf("interoperability %q", got)
		}
		if got := value(read(bo.Uint32(ifd0[34853][8:]))[0], 4); !bytes.Equal(got, []byte{2, 3, 0, 0}) {
			t.Errorf("GPS version %v", got)
		}
		// A pointer to itself, or past the end: no EXIF block, the image
		// still read.
		bad := append([]byte(nil), file...)
		for i := 0; i < int(bo.Uint16(bad[8:])); i++ {
			if e := bad[10+12*i:]; bo.Uint16(e) == 34665 {
				bo.PutUint32(e[8:], 8)
			}
		}
		if d, err := newDecoder(bytes.NewReader(bad)); err != nil || d.exif != nil {
			t.Errorf("a directory pointing to the first: %v, %d bytes of EXIF", err, len(d.exif))
		}
	}
}

func keys(m map[uint16][]byte) []uint16 {
	var k []uint16
	for t := range m {
		k = append(k, t)
	}
	return k
}
