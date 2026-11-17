package tiffstream

import (
	"encoding/binary"
	"io"
	"sort"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// A TIFF file keeps what EXIF holds in its own first directory, among the
// tags of how the image is stored, and in the directories of EXIF and GPS
// that tags of it point to. readExif makes of these an EXIF block, as a JPEG
// or a PNG holds one: a TIFF of its own (in the file's byte order) whose
// first directory has the tags that describe the picture, followed by the
// directories they point to, every value that does not fit in its entry
// copied and its offset made the new one.

// exifTags are the tags of the first directory that go into the EXIF block.
var exifTags = map[uint16]bool{
	270:   true, // ImageDescription
	271:   true, // Make
	272:   true, // Model
	274:   true, // Orientation
	282:   true, // XResolution
	283:   true, // YResolution
	296:   true, // ResolutionUnit
	305:   true, // Software
	306:   true, // DateTime
	315:   true, // Artist
	316:   true, // HostComputer
	33432: true, // Copyright
	34665: true, // the EXIF directory
	34853: true, // the GPS directory
}

// exifPointers are the tags whose value is the offset of a directory.
var exifPointers = map[uint16]bool{34665: true, 34853: true, 40965: true}

// typeSizes are the bytes of a value of each TIFF type.
var typeSizes = [...]int{1: 1, 2: 1, 3: 2, 4: 4, 5: 8, 6: 1, 7: 1, 8: 2, 9: 4, 10: 8, 11: 4, 12: 8, 13: 4}

// byteOrder reads and appends in the file's byte order.
type byteOrder interface {
	binary.ByteOrder
	binary.AppendByteOrder
}

// exifBuilder writes the EXIF block.
type exifBuilder struct {
	r    io.ReaderAt
	bo   byteOrder
	out  []byte
	dirs int // directories written, against loops
}

// readExif returns the EXIF block of a TIFF whose first directory's
// entries are entries; nil when it has none of the tags but the
// resolution, or when they cannot be read.
func readExif(r io.ReaderAt, order binary.ByteOrder, entries []byte) []byte {
	bo, ok := order.(byteOrder)
	if !ok {
		return nil
	}
	var keep [][]byte
	described := false // a tag besides the resolution, which every TIFF has
	for i := 0; i+ifdLen <= len(entries); i += ifdLen {
		if e := entries[i : i+ifdLen]; exifTags[bo.Uint16(e)] {
			keep = append(keep, e)
			if t := bo.Uint16(e); t != 282 && t != 283 && t != 296 {
				described = true
			}
		}
	}
	if !described {
		return nil
	}
	b := &exifBuilder{r: r, bo: bo}
	if order == binary.ByteOrder(binary.LittleEndian) {
		b.out = append(b.out, leHeader...)
	} else {
		b.out = append(b.out, beHeader...)
	}
	b.out = bo.AppendUint32(b.out, 8)
	if !b.dir(keep, 0) || len(b.out) > pipeline.MaxMetaBytes {
		return nil
	}
	return b.out
}

// dir writes a directory of the entries (12 bytes each, as in the file)
// at the end of the block, with what they point to after it; depth counts
// the directories it is in.
func (b *exifBuilder) dir(entries [][]byte, depth int) bool {
	if b.dirs++; b.dirs > 8 || depth > 2 || len(entries) > 1000 {
		return false
	}
	sort.SliceStable(entries, func(i, j int) bool { return b.bo.Uint16(entries[i]) < b.bo.Uint16(entries[j]) })
	start := len(b.out)
	b.out = b.bo.AppendUint16(b.out, uint16(len(entries)))
	for _, e := range entries {
		b.out = append(b.out, e...)
	}
	b.out = b.bo.AppendUint32(b.out, 0) // no next directory
	for i, e := range entries {
		at := start + 2 + ifdLen*i
		tag, typ, count := b.bo.Uint16(e), int(b.bo.Uint16(e[2:])), uint64(b.bo.Uint32(e[4:]))
		if typ <= 0 || typ >= len(typeSizes) || typeSizes[typ] == 0 {
			return false
		}
		if exifPointers[tag] {
			if count != 1 || (typ != 4 && typ != 13) {
				return false
			}
			sub, ok := b.readDir(int64(b.bo.Uint32(e[8:])))
			if !ok {
				return false
			}
			b.bo.PutUint32(b.out[at+8:], uint32(len(b.out)))
			if !b.dir(sub, depth+1) {
				return false
			}
			continue
		}
		n := count * uint64(typeSizes[typ])
		if n <= 4 {
			continue // the value is in the entry
		}
		if n > pipeline.MaxMetaBytes || uint64(len(b.out))+n > pipeline.MaxMetaBytes {
			return false
		}
		data, err := safeReadAt(b.r, n, int64(b.bo.Uint32(e[8:])))
		if err != nil {
			return false
		}
		if len(b.out)%2 == 1 {
			b.out = append(b.out, 0) // values start on a word boundary
		}
		b.bo.PutUint32(b.out[at+8:], uint32(len(b.out)))
		b.out = append(b.out, data...)
	}
	return true
}

// readDir reads the entries of the directory at off.
func (b *exifBuilder) readDir(off int64) ([][]byte, bool) {
	var n [2]byte
	if _, err := b.r.ReadAt(n[:], off); err != nil {
		return nil, false
	}
	count := int(b.bo.Uint16(n[:]))
	if count > 1000 {
		return nil, false
	}
	p, err := safeReadAt(b.r, uint64(count*ifdLen), off+2)
	if err != nil {
		return nil, false
	}
	entries := make([][]byte, count)
	for i := range entries {
		entries[i] = p[i*ifdLen : (i+1)*ifdLen]
	}
	return entries, true
}

// exifEntries returns the entries of the EXIF block x (in TIFF form) that go
// into a file this package writes: the tags of exifTags but the resolution,
// which the writer records, with the directories they point to, every value
// in little-endian order. Nil for a block that cannot be read.
func exifEntries(x []byte) []ifdEnt {
	if len(x) < 8 || len(x) > pipeline.MaxMetaBytes {
		return nil
	}
	var bo binary.ByteOrder
	switch string(x[:4]) {
	case leHeader:
		bo = binary.LittleEndian
	case beHeader:
		bo = binary.BigEndian
	default:
		return nil
	}
	dirs := 0
	var read func(off uint32, depth int, first bool) ([]ifdEnt, bool)
	read = func(off uint32, depth int, first bool) ([]ifdEnt, bool) {
		if dirs++; dirs > 8 || depth > 2 || uint64(off)+2 > uint64(len(x)) {
			return nil, false
		}
		n := int(bo.Uint16(x[off:]))
		if uint64(off)+2+uint64(n)*ifdLen > uint64(len(x)) {
			return nil, false
		}
		var d []ifdEnt
		for i := 0; i < n; i++ {
			e := x[int(off)+2+ifdLen*i:][:ifdLen]
			tag, typ, count := bo.Uint16(e), int(bo.Uint16(e[2:])), uint64(bo.Uint32(e[4:]))
			if first && (!exifTags[tag] || tag == 282 || tag == 283 || tag == 296) {
				continue
			}
			if typ <= 0 || typ >= len(typeSizes) || typeSizes[typ] == 0 {
				return nil, false
			}
			if exifPointers[tag] {
				if count != 1 || (typ != 4 && typ != 13) {
					return nil, false
				}
				sub, ok := read(bo.Uint32(e[8:]), depth+1, false)
				if !ok {
					return nil, false
				}
				d = append(d, ifdEnt{tag: int(tag), datatype: dtLong, sub: sub})
				continue
			}
			size := uint64(typeSizes[typ])
			nb := count * size
			var v []byte
			if nb <= 4 {
				v = e[8 : 8+nb]
			} else {
				o := uint64(bo.Uint32(e[8:]))
				if o+nb > uint64(len(x)) {
					return nil, false
				}
				v = x[o : o+nb]
			}
			raw := make([]byte, nb)
			// Each value of 2, 4 or 8 bytes in little-endian order; a
			// RATIONAL is two LONGs, a DOUBLE one 8-byte value.
			part := size
			if typ == 5 || typ == 10 {
				part = 4
			}
			for j := uint64(0); j < nb; j += part {
				switch part {
				case 2:
					enc.PutUint16(raw[j:], bo.Uint16(v[j:]))
				case 4:
					enc.PutUint32(raw[j:], bo.Uint32(v[j:]))
				case 8:
					enc.PutUint64(raw[j:], bo.Uint64(v[j:]))
				default:
					raw[j] = v[j]
				}
			}
			d = append(d, ifdEnt{tag: int(tag), datatype: typ, raw: raw})
		}
		return d, true
	}
	d, ok := read(binary.ByteOrder(bo).Uint32(x[4:]), 0, true)
	if !ok {
		return nil
	}
	return d
}
