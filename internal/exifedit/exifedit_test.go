package exifedit

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

// fieldsOf reads the first directory of a block: each tag's entry, and the
// value of its ASCII ones; it checks that the tags are in order.
func fieldsOf(t *testing.T, b []byte) (bo binary.ByteOrder, entries map[uint16]entry, text map[uint16]string, next uint32) {
	t.Helper()
	bo, es, next, ok := firstDir(b)
	if !ok {
		t.Fatal("no first directory")
	}
	entries, text = map[uint16]entry{}, map[uint16]string{}
	for i, e := range es {
		tag := e.tag(bo)
		if i > 0 && tag <= es[i-1].tag(bo) {
			t.Fatalf("tag %d after %d", tag, es[i-1].tag(bo))
		}
		entries[tag] = e
		if bo.Uint16(e[2:]) == 2 {
			n := bo.Uint32(e[4:])
			v := e[8:12]
			if n > 4 {
				off := bo.Uint32(e[8:])
				if int(off+n) > len(b) {
					t.Fatalf("tag %d: value beyond the block", tag)
				}
				v = b[off : off+n]
			} else {
				v = v[:n]
			}
			if len(v) == 0 || v[len(v)-1] != 0 {
				t.Fatalf("tag %d: %q not ended by NUL", tag, v)
			}
			text[tag] = string(v[:len(v)-1])
		}
	}
	return bo, entries, text, next
}

// jpegExif is the EXIF block of a JPEG file.
func jpegExif(t *testing.T, data []byte) []byte {
	t.Helper()
	for i := 2; i+4 < len(data) && data[i] == 0xff; {
		n := int(data[i+2])<<8 | int(data[i+3])
		if data[i+1] == 0xe1 && bytes.HasPrefix(data[i+4:], []byte("Exif\x00\x00")) {
			return data[i+10 : i+2+n]
		}
		i += 2 + n
	}
	t.Fatal("no EXIF block")
	return nil
}

func TestNewBlock(t *testing.T) {
	for _, in := range [][]byte{nil, []byte("not exif"), []byte("II*\x00\xff\xff\x00\x00")} {
		b := Set(in, []Field{{33432, "© 2026 Somebody"}, {315, "Ann"}, {305, "x"}, {315, "Bo"}})
		_, entries, text, next := fieldsOf(t, b)
		if text[33432] != "© 2026 Somebody" || text[315] != "Bo" || text[305] != "x" || len(entries) != 3 || next != 0 {
			t.Fatalf("%q: %v, %d entries, next %d", in, text, len(entries), next)
		}
		if !bytes.Equal(b, Set(in, []Field{{33432, "© 2026 Somebody"}, {315, "Ann"}, {305, "x"}, {315, "Bo"}})) {
			t.Fatal("another block for the same fields")
		}
	}
	if b := []byte("II*\x00"); !bytes.Equal(Set(b, nil), b) {
		t.Error("no fields: the block changed")
	}
}

// TestMerge: on a camera's block (in both byte orders) the fields are set
// and everything else stays: the block grows only at its end, every other
// entry of the first directory is the same, and the next directory too.
func TestMerge(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "tests", "testdata", "exif-pillow.jpg"))
	if err != nil {
		t.Fatal(err)
	}
	block := jpegExif(t, data)
	for _, exif := range [][]byte{block, swapOrder(t, block)} {
		bo, before, text0, next0 := fieldsOf(t, exif)
		out := Set(exif, []Field{{33432, "Rights"}, {270, "A caption that is long"}, {305, "goimgtools"}})
		if !bytes.Equal(out[8:len(exif)], exif[8:]) || !bytes.Equal(out[:4], exif[:4]) {
			t.Fatal("bytes of the block changed")
		}
		_, after, text, next := fieldsOf(t, out)
		if next != next0 {
			t.Errorf("next directory %d, was %d", next, next0)
		}
		for tag, e := range before {
			if tag == 33432 || tag == 270 || tag == 305 {
				continue
			}
			if after[tag] != e {
				t.Errorf("tag %d changed", tag)
			}
		}
		if text[33432] != "Rights" || text[270] != "A caption that is long" || text[305] != "goimgtools" {
			t.Errorf("fields %v", text)
		}
		for tag, v := range text0 {
			if tag != 33432 && tag != 270 && tag != 305 && text[tag] != v {
				t.Errorf("tag %d: %q, was %q", tag, text[tag], v)
			}
		}
		if bo.Uint32(out[4:]) != uint32(len(exif)+len(exif)%2) {
			t.Error("the header does not point to the new directory")
		}
		// The same block after "Exif\0\0", as some tools leave it.
		fs := []Field{{33432, "Rights"}, {270, "A caption that is long"}, {305, "goimgtools"}}
		if !bytes.Equal(Set(append([]byte("Exif\x00\x00"), exif...), fs), out) {
			t.Error("a block after Exif\\0\\0 is set otherwise")
		}
	}
}

// swapOrder rewrites a block's first directory in the other byte order,
// with its values of up to four bytes, enough for TestMerge (the values
// elsewhere and the other directories are not looked at).
func swapOrder(t *testing.T, b []byte) []byte {
	t.Helper()
	from, _, _, ok := firstDir(b)
	if !ok {
		t.Fatal("no first directory")
	}
	var to binary.ByteOrder = binary.BigEndian
	out := append([]byte(nil), b...)
	copy(out, "MM\x00*")
	if from == binary.BigEndian {
		to = binary.LittleEndian
		copy(out, "II*\x00")
	}
	to.PutUint32(out[4:], from.Uint32(b[4:]))
	off := from.Uint32(b[4:])
	n := from.Uint16(b[off:])
	to.PutUint16(out[off:], n)
	for i := uint32(0); i < uint32(n); i++ {
		p := off + 2 + 12*i
		typ := from.Uint16(b[p+2:])
		to.PutUint16(out[p:], from.Uint16(b[p:]))
		to.PutUint16(out[p+2:], typ)
		to.PutUint32(out[p+4:], from.Uint32(b[p+4:]))
		size := map[uint16]uint32{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 7: 1}[typ] * from.Uint32(b[p+4:])
		switch {
		case size > 4 || typ == 4:
			to.PutUint32(out[p+8:], from.Uint32(b[p+8:]))
		case typ == 3:
			to.PutUint16(out[p+8:], from.Uint16(b[p+8:]))
		}
	}
	p := off + 2 + 12*uint32(n)
	to.PutUint32(out[p:], from.Uint32(b[p:]))
	return out
}
