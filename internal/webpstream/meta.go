package webpstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"

	"github.com/whhhh1500/goimgtools/internal/density"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The metadata of a WebP file lives in chunks of the extended format: a
// VP8X chunk first, whose flags say which are there, then ICCP (the colour
// profile) before the image data and EXIF after it.
const (
	flagICC  = 1 << 5
	flagEXIF = 1 << 3
	flagXMP  = 1 << 2
)

// Meta is what the file carries besides the pixels.
func (s *source) Meta() pipeline.Meta { return s.meta }

// readMeta reads the payload of an ICCP or EXIF chunk into s.meta, if it is
// not too large. A malformed block is left out, not an error: the pixels
// are still good.
func (s *source) readMeta(name string, r io.ReaderAt, pos int64, n uint32) {
	if n == 0 || n > pipeline.MaxMetaBytes {
		return
	}
	buf := make([]byte, n)
	if _, err := r.ReadAt(buf, pos); err != nil && !(err == io.EOF && len(buf) == int(n)) {
		return
	}
	switch name {
	case "ICCP":
		if s.meta.ICC == nil {
			s.meta.ICC = buf
		}
	case "EXIF":
		// Some writers keep the "Exif\0\0" of a JPEG APP1 segment.
		buf = bytes.TrimPrefix(buf, []byte("Exif\x00\x00"))
		if s.meta.Exif == nil && len(buf) >= 8 && (string(buf[:2]) == "II" || string(buf[:2]) == "MM") {
			s.meta.Exif = buf
			// WebP has no resolution of its own: that of the EXIF block.
			s.meta.Density = density.FromExif(buf)
		}
	case "XMP ":
		if s.meta.Xmp == nil {
			s.meta.Xmp = buf
		}
	}
}

// scanMeta looks through the chunks from pos to end (after the image data)
// for the EXIF block and a late colour profile. It only reads chunk
// headers and what it keeps.
func (s *source) scanMeta(r io.ReaderAt, pos, end int64) {
	var hdr [8]byte
	for pos+8 <= end {
		if _, err := r.ReadAt(hdr[:], pos); err != nil {
			return
		}
		n := binary.LittleEndian.Uint32(hdr[4:])
		pos += 8
		if int64(n) > end-pos {
			return
		}
		if name := string(hdr[:4]); name == "EXIF" || name == "ICCP" || name == "XMP " {
			s.readMeta(name, r, pos, n)
		}
		pos += int64(n) + int64(n&1)
	}
}

// MetaWriter puts a colour profile and an EXIF block into the WebP file an
// encoder writes through it: it turns the simple format the encoder writes
// (RIFF, then VP8 or VP8L) into the extended one, with a VP8X chunk whose
// flags announce them, the ICCP chunk before the image data and the EXIF
// chunk after it, and the RIFF size grown to match. The image data passes
// through untouched. Finish must be called once the encoder has written
// the whole file.
type MetaWriter struct {
	w    io.Writer
	meta pipeline.Meta
	head []byte // what came before the header could be rewritten
	done bool
}

// NewMetaWriter returns a MetaWriter adding m to the file written to w.
func NewMetaWriter(w io.Writer, m pipeline.Meta) *MetaWriter {
	return &MetaWriter{w: w, meta: m}
}

func appendChunk(dst []byte, name string, data []byte) []byte {
	dst = append(dst, name...)
	dst = binary.LittleEndian.AppendUint32(dst, uint32(len(data)))
	dst = append(dst, data...)
	if len(data)&1 != 0 {
		dst = append(dst, 0)
	}
	return dst
}

func chunkSize(data []byte) int64 { return 8 + int64(len(data)) + int64(len(data)&1) }

func (m *MetaWriter) Write(p []byte) (int, error) {
	if m.done {
		return m.w.Write(p)
	}
	m.head = append(m.head, p...)
	ok, err := m.rewrite()
	if err != nil {
		return 0, err
	}
	if ok {
		m.done = true
	}
	return len(p), nil
}

// rewrite writes the new header and what followed it, once enough of the
// file has come to know its first chunk and the size of the image.
func (m *MetaWriter) rewrite() (bool, error) {
	h := m.head
	if len(h) < 20 {
		return false, nil
	}
	if string(h[:4]) != "RIFF" || string(h[8:12]) != "WEBP" {
		return false, errors.New("goimgtools: webp: not a WebP file to add metadata to")
	}
	riff := int64(binary.LittleEndian.Uint32(h[4:]))
	var flags byte
	var w, ht uint32
	rest := 12 // where the chunks to keep begin
	switch string(h[12:16]) {
	case "VP8X":
		if len(h) < 30 {
			return false, nil
		}
		flags = h[20]
		w = uint32(h[24]) | uint32(h[25])<<8 | uint32(h[26])<<16
		ht = uint32(h[27]) | uint32(h[28])<<8 | uint32(h[29])<<16
		rest = 30
		riff -= 18 // written again below
	case "VP8 ":
		if len(h) < 30 {
			return false, nil
		}
		w = uint32(binary.LittleEndian.Uint16(h[26:])&0x3fff) - 1
		ht = uint32(binary.LittleEndian.Uint16(h[28:])&0x3fff) - 1
	case "VP8L":
		if len(h) < 25 {
			return false, nil
		}
		bits := binary.LittleEndian.Uint32(h[21:])
		w, ht = bits&0x3fff, bits>>14&0x3fff
		if bits>>28&1 != 0 {
			flags |= 1 << 4 // alpha
		}
	default:
		return false, errors.New("goimgtools: webp: unexpected first chunk " + string(h[12:16]))
	}
	if len(m.meta.ICC) > 0 {
		flags |= flagICC
	}
	if len(m.meta.Exif) > 0 {
		flags |= flagEXIF
	}
	if len(m.meta.Xmp) > 0 {
		flags |= flagXMP
		riff += chunkSize(m.meta.Xmp)
	}
	riff += 18
	if len(m.meta.ICC) > 0 {
		riff += chunkSize(m.meta.ICC)
	}
	if len(m.meta.Exif) > 0 {
		riff += chunkSize(m.meta.Exif)
	}
	if riff > 1<<32-1 {
		return false, errors.New("goimgtools: webp: file with its metadata would exceed 4GB")
	}
	out := make([]byte, 0, 30+chunkSize(m.meta.ICC)+int64(len(h)-rest))
	out = append(out, "RIFF"...)
	out = binary.LittleEndian.AppendUint32(out, uint32(riff))
	out = append(out, "WEBP"...)
	x := [10]byte{flags, 0, 0, 0, byte(w), byte(w >> 8), byte(w >> 16), byte(ht), byte(ht >> 8), byte(ht >> 16)}
	out = appendChunk(out, "VP8X", x[:])
	if len(m.meta.ICC) > 0 {
		out = appendChunk(out, "ICCP", m.meta.ICC)
	}
	out = append(out, h[rest:]...)
	m.head = nil
	_, err := m.w.Write(out)
	return true, err
}

// Finish writes the EXIF and XMP chunks at the end of the file.
func (m *MetaWriter) Finish() error {
	if !m.done {
		return errors.New("goimgtools: webp: the encoder wrote no complete header")
	}
	var tail []byte
	if len(m.meta.Exif) > 0 {
		tail = appendChunk(tail, "EXIF", m.meta.Exif)
	}
	if len(m.meta.Xmp) > 0 {
		tail = appendChunk(tail, "XMP ", m.meta.Xmp)
	}
	if len(tail) == 0 {
		return nil
	}
	_, err := m.w.Write(tail)
	return err
}
