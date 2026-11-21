// Package exifedit sets text fields of the first directory of an EXIF
// block, in its TIFF form, as JPEG, PNG, WebP and TIFF files hold it.
package exifedit

import (
	"encoding/binary"
	"sort"
)

// Field is a text field of the first directory: its tag (Copyright is
// 33432, Artist 315, ImageDescription 270, Software 305, DateTime 306) and
// its value, written as a string of type ASCII (UTF-8 as it is, as
// exiftool writes it).
type Field struct {
	Tag   uint16
	Value string
}

// entry is a field of a directory as it is stored: 12 bytes.
type entry [12]byte

func (e entry) tag(bo binary.ByteOrder) uint16 { return bo.Uint16(e[:2]) }

// order reads and appends in the block's byte order.
type order interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// Set returns exif with fields set in its first directory, each added or
// replacing the field of its tag, or a new block of only them when exif is
// empty or cannot be read. Every other field, directory and value of the
// block stays where it is: the new first directory, and the values that do
// not fit in its entries, go after the end of the block and the header
// points to them (the old directory is left unused). So the offsets in the
// rest of the block, a maker note's among them, stay right. exif is not
// changed.
func Set(exif []byte, fields []Field) []byte {
	if len(fields) == 0 {
		return exif
	}
	var bo order
	var entries []entry
	var next uint32
	out := []byte(nil)
	if b, es, n, ok := firstDir(exif); ok {
		bo, entries, next = b, es, n
		out = append(out, exif...)
	} else {
		bo = binary.LittleEndian
		out = []byte{'I', 'I', 42, 0, 8, 0, 0, 0}
	}
	set := map[uint16]bool{}
	for _, f := range fields {
		set[f.Tag] = true
	}
	keep := entries[:0:0]
	for _, e := range entries {
		if !set[e.tag(bo)] {
			keep = append(keep, e)
		}
	}
	// The last value given for a tag wins.
	last := map[uint16]string{}
	for _, f := range fields {
		last[f.Tag] = f.Value
	}
	if len(out)%2 == 1 {
		out = append(out, 0) // directories start on a word boundary
	}
	dir := uint32(len(out))
	n := len(keep) + len(last)
	values := dir + 2 + 12*uint32(n) + 4
	tags := make([]uint16, 0, len(last))
	for tag := range last {
		tags = append(tags, tag)
	}
	sort.Slice(tags, func(i, j int) bool { return tags[i] < tags[j] })
	var data []byte
	for _, tag := range tags {
		v := last[tag]
		var e entry
		bo.PutUint16(e[0:], tag)
		bo.PutUint16(e[2:], 2) // ASCII
		s := append([]byte(v), 0)
		bo.PutUint32(e[4:], uint32(len(s)))
		if len(s) <= 4 {
			copy(e[8:], s)
		} else {
			bo.PutUint32(e[8:], values+uint32(len(data)))
			data = append(data, s...)
			if len(data)%2 == 1 {
				data = append(data, 0)
			}
		}
		keep = append(keep, e)
	}
	// Fields in order of their tags, as TIFF wants them.
	sort.SliceStable(keep, func(i, j int) bool { return keep[i].tag(bo) < keep[j].tag(bo) })
	out = bo.AppendUint16(out, uint16(n))
	for _, e := range keep {
		out = append(out, e[:]...)
	}
	out = bo.AppendUint32(out, next)
	out = append(out, data...)
	bo.PutUint32(out[4:8], dir)
	return out
}

// firstDir reads the first directory of an EXIF block: the byte order, its
// entries and the offset of the next directory.
func firstDir(exif []byte) (order, []entry, uint32, bool) {
	if len(exif) < 8 {
		return nil, nil, 0, false
	}
	var bo order
	switch string(exif[:4]) {
	case "II*\x00":
		bo = binary.LittleEndian
	case "MM\x00*":
		bo = binary.BigEndian
	default:
		return nil, nil, 0, false
	}
	off := int64(bo.Uint32(exif[4:8]))
	if off < 8 || off+2 > int64(len(exif)) {
		return nil, nil, 0, false
	}
	n := int64(bo.Uint16(exif[off:]))
	end := off + 2 + 12*n
	if end+4 > int64(len(exif)) {
		return nil, nil, 0, false
	}
	entries := make([]entry, n)
	for i := range entries {
		copy(entries[i][:], exif[off+2+12*int64(i):])
	}
	return bo, entries, bo.Uint32(exif[end:]), true
}
