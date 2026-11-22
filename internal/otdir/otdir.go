// Package otdir reads the table directory of an OpenType font, or of a
// font of a collection, so that its tables can be read one by one.
package otdir

import (
	"encoding/binary"
	"errors"
	"io"
)

// Table is where a table is in the file.
type Table struct {
	Offset, Length int64
	Record         [16]byte // its record in the directory: tag, checksum, offset, length
}

// Dir is a font's table directory.
type Dir struct {
	Version [4]byte // the kind of the font: TrueType or CFF outlines
	Tables  map[string]Table
}

// Read reads the directory of font number index of a font or collection
// file.
func Read(r io.ReaderAt, index int) (*Dir, error) {
	hdr := make([]byte, 12)
	if _, err := r.ReadAt(hdr, 0); err != nil {
		return nil, err
	}
	base := int64(0)
	if string(hdr[:4]) == "ttcf" {
		n := int(binary.BigEndian.Uint32(hdr[8:]))
		if index < 0 || index >= n {
			return nil, errors.New("otdir: no such font in the collection")
		}
		o := make([]byte, 4)
		if _, err := r.ReadAt(o, 12+4*int64(index)); err != nil {
			return nil, err
		}
		base = int64(binary.BigEndian.Uint32(o))
		if _, err := r.ReadAt(hdr, base); err != nil {
			return nil, err
		}
	} else if index != 0 {
		return nil, errors.New("otdir: no such font in the file")
	}
	n := int(binary.BigEndian.Uint16(hdr[4:]))
	recs := make([]byte, 16*n)
	if n > 0 {
		if _, err := r.ReadAt(recs, base+12); err != nil {
			return nil, err
		}
	}
	d := &Dir{Tables: make(map[string]Table, n)}
	copy(d.Version[:], hdr[:4])
	for i := 0; i < n; i++ {
		var t Table
		copy(t.Record[:], recs[16*i:])
		t.Offset = int64(binary.BigEndian.Uint32(t.Record[8:]))
		t.Length = int64(binary.BigEndian.Uint32(t.Record[12:]))
		d.Tables[string(t.Record[:4])] = t
	}
	return d, nil
}

// ReadTable reads a whole table; nil, nil when the font has none.
func (d *Dir) ReadTable(r io.ReaderAt, tag string, max int64) ([]byte, error) {
	t, ok := d.Tables[tag]
	if !ok {
		return nil, nil
	}
	if t.Length > max {
		return nil, errors.New("otdir: table " + tag + " too large")
	}
	b := make([]byte, t.Length)
	if _, err := r.ReadAt(b, t.Offset); err != nil {
		return nil, err
	}
	return b, nil
}
