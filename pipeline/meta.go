package pipeline

import (
	"encoding/binary"
	"image"
)

// Meta is the metadata a decoder found next to the pixels.
type Meta struct {
	// Exif is the EXIF block in its TIFF form (starting "II" or "MM"),
	// without any container prefix.
	Exif []byte
	// ICC is the embedded colour profile.
	ICC []byte
	// Xmp is the XMP packet (Adobe's metadata, in XML): keywords, rating,
	// rights, the history of edits.
	Xmp []byte
	// Iptc is the IPTC record (the datasets of the Information Interchange
	// Model, each starting 0x1C: caption, keywords, byline, copyright),
	// without what a container wraps it in.
	Iptc []byte
	// Density is the resolution the file records, in pixels per inch
	// (dots per inch), horizontally; 0 when it records none.
	Density float64
}

// MetaSource is implemented by decoders that read metadata.
type MetaSource interface {
	Meta() Meta
}

// MetaOf returns the metadata of s, if it has any.
func MetaOf(s Source) Meta {
	if m, ok := s.(MetaSource); ok {
		return m.Meta()
	}
	return Meta{}
}

// MaxMetaBytes bounds the EXIF block and the colour profile a decoder keeps;
// larger ones are dropped rather than held in memory.
const MaxMetaBytes = 8 << 20

const tagOrientation = 0x0112

// exifOrientation finds the orientation entry of the first image directory
// and returns the byte order and the offset of its value.
func exifOrientation(exif []byte) (bo binary.ByteOrder, off int, ok bool) {
	if len(exif) < 8 {
		return nil, 0, false
	}
	switch string(exif[:2]) {
	case "II":
		bo = binary.LittleEndian
	case "MM":
		bo = binary.BigEndian
	default:
		return nil, 0, false
	}
	if bo.Uint16(exif[2:]) != 42 {
		return nil, 0, false
	}
	ifd := int64(bo.Uint32(exif[4:]))
	if ifd+2 > int64(len(exif)) {
		return nil, 0, false
	}
	n := int64(bo.Uint16(exif[ifd:]))
	for i := int64(0); i < n; i++ {
		e := ifd + 2 + 12*i
		if e+12 > int64(len(exif)) {
			return nil, 0, false
		}
		if bo.Uint16(exif[e:]) != tagOrientation {
			continue
		}
		// A SHORT with one value, stored in the entry itself.
		if bo.Uint16(exif[e+2:]) != 3 || bo.Uint32(exif[e+4:]) != 1 {
			return nil, 0, false
		}
		return bo, int(e + 8), true
	}
	return nil, 0, false
}

// ExifOrientation returns the EXIF orientation (1 to 8) recorded in an EXIF
// block, or 1 (upright) when there is none or the block cannot be read.
func ExifOrientation(exif []byte) int {
	bo, off, ok := exifOrientation(exif)
	if !ok {
		return 1
	}
	if v := int(bo.Uint16(exif[off:])); v >= 1 && v <= 8 {
		return v
	}
	return 1
}

// ExifUpright returns exif with its orientation set to 1 (upright), for
// pixels that have been turned accordingly. The input is not modified.
func ExifUpright(exif []byte) []byte {
	bo, off, ok := exifOrientation(exif)
	if !ok || bo.Uint16(exif[off:]) == 1 {
		return exif
	}
	out := append([]byte(nil), exif...)
	bo.PutUint16(out[off:], 1)
	return out
}

// Pix returns the pixel bytes of a strip whose type stores whole pixels
// side by side (everything but the YCbCr family): the buffer, its stride,
// bytes per pixel and rectangle. ok is false for other types.
func Pix(m image.Image) (pix []uint8, stride, bpp int, rect image.Rectangle, ok bool) {
	p, ok := asPix(m)
	return p.pix, p.stride, p.bpp, p.rect, ok
}
