// Package sgistream reads the SGI image format (IRIS RGB, the .sgi, .rgb,
// .rgba and .bw files of Silicon Graphics): grey, grey with alpha, RGB and
// RGBA of one or two bytes a channel, stored verbatim or run-length
// compressed, a band of rows at a time. The file keeps the channels one
// after the other and the rows from the bottom up; the rows are read from
// where they lie, so nothing of the image is held but the band.
package sgistream

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var (
	errFormat      = errors.New("goimgtools: sgi: not a valid SGI image file")
	errUnsupported = errors.New("goimgtools: sgi: more than 4 channels")
)

const (
	headerSize = 512
	// window is the least that is read of a compressed file at a time.
	window = 64 << 10
)

// header is what the 512 bytes an SGI file starts with say of the image.
type header struct {
	packed  bool // run-length compressed
	bpc     int  // bytes a channel
	w, h, z int
}

// sniff reads the first 12 bytes of the header.
func sniff(b []byte) (header, bool) {
	if len(b) < 12 || b[0] != 0x01 || b[1] != 0xda || b[2] > 1 || b[3] != 1 && b[3] != 2 {
		return header{}, false
	}
	h := header{packed: b[2] == 1, bpc: int(b[3]), w: int(binary.BigEndian.Uint16(b[6:])),
		h: int(binary.BigEndian.Uint16(b[8:])), z: int(binary.BigEndian.Uint16(b[10:]))}
	// The number of dimensions says which of the sizes count: 1 for a
	// single row, 2 for a single channel, 3 for several.
	switch binary.BigEndian.Uint16(b[4:]) {
	case 1:
		h.h, h.z = 1, 1
	case 2:
		h.z = 1
	case 3:
	default:
		return h, false
	}
	return h, h.w > 0 && h.h > 0 && h.z > 0
}

// Match reports whether head starts an SGI image file: the number 474,
// then a storage of 0 or 1, 1 or 2 bytes a channel, 1 to 3 dimensions and
// sizes that are not zero.
func Match(head []byte) bool {
	_, ok := sniff(head)
	return ok
}

// stream is an SGI image decoded a band of rows at a time.
type stream struct {
	r        io.ReaderAt
	size     int64
	env      *pipeline.Env
	h        header
	band     image.Image
	raw      []byte // the rows of a band of one channel, or one unpacked row
	n, y     int
	reserved int64
	// Compressed files: where the rows of the band start and how long they
	// are, and the part of the file last read.
	table   []byte
	win     []byte
	winOff  int64
	winLen  int
	forward bool // a row lies after the one above it
	err     error
}

// Open reads the header of an SGI file of size bytes and returns its
// image, decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	var hb [12]byte
	if readAt(r, hb[:], 0) != nil {
		return nil, errFormat
	}
	h, ok := sniff(hb[:])
	if !ok {
		return nil, errFormat
	}
	if h.z > 4 {
		return nil, errUnsupported
	}
	s := &stream{r: r, size: size, env: env, h: h}
	rowBytes := int64(h.w) * int64(h.bpc)
	// The rows, or for a compressed file the two tables of their places,
	// must lie in the file.
	need := rowBytes * int64(h.h) * int64(h.z)
	if h.packed {
		need = 8 * int64(h.h) * int64(h.z)
	}
	if need > size-headerSize {
		return nil, io.ErrUnexpectedEOF
	}
	out := int64(h.bpc)
	if h.z > 1 {
		out *= 4
	}
	s.n = pipeline.StripRows(int64(h.w)*out+rowBytes, h.h)
	s.reserved = int64(s.n) * int64(h.w) * out
	var win int64
	if h.packed {
		// A row of w values takes 2w+1 at most (every value a run of
		// one, and the count that ends the row).
		win = max(window, (2*int64(h.w)+1)*int64(h.bpc))
		s.reserved += rowBytes + 8*int64(s.n) + win
	} else {
		s.reserved += int64(s.n) * rowBytes
	}
	if err := env.Budget.Reserve(s.reserved, "sgi band"); err != nil {
		return nil, err
	}
	r0 := image.Rect(0, 0, h.w, s.n)
	switch {
	case h.z == 1 && h.bpc == 1:
		s.band = image.NewGray(r0)
	case h.z == 1:
		s.band = image.NewGray16(r0)
	case h.bpc == 1:
		b := image.NewNRGBA(r0)
		for i := range b.Pix {
			b.Pix[i] = 255
		}
		s.band = b
	default:
		b := image.NewNRGBA64(r0)
		for i := range b.Pix {
			b.Pix[i] = 255
		}
		s.band = b
	}
	if h.packed {
		s.raw = make([]byte, rowBytes)
		s.table = make([]byte, 8*s.n)
		s.win = make([]byte, win)
	} else {
		s.raw = make([]byte, int64(s.n)*rowBytes)
	}
	return s, nil
}

// readAt fills p from off or fails.
func readAt(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.h.w, s.h.h) }
func (s *stream) ColorModel() color.Model { return s.band.ColorModel() }

// Opaque reports whether the file has no alpha: one channel or three.
func (s *stream) Opaque() bool { return s.h.z == 1 || s.h.z == 3 }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.raw, s.table, s.win = nil, nil, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: sgi: closed")
	}
	return nil
}

func (s *stream) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.y >= s.h.h {
		return nil, io.EOF
	}
	n := min(s.n, s.h.h-s.y)
	for c := 0; c < s.h.z; c++ {
		if err := s.channel(c, n); err != nil {
			s.err = err
			return nil, err
		}
	}
	rect := image.Rect(0, s.y, s.h.w, s.y+n)
	s.y += n
	switch b := s.band.(type) {
	case *image.Gray:
		return &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	case *image.Gray16:
		return &image.Gray16{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	case *image.NRGBA:
		return &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	}
	b := s.band.(*image.NRGBA64)
	return &image.NRGBA64{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
}

// channel reads channel c of image rows y to y+n-1 into the band. In the
// file they are rows h-y-n to h-y-1 of the channel, the last of them the
// top one.
func (s *stream) channel(c, n int) error {
	h := s.h
	rb := h.w * h.bpc
	first := int64(c)*int64(h.h) + int64(h.h-s.y-n)
	if !h.packed {
		raw := s.raw[:n*rb]
		if err := readAt(s.r, raw, headerSize+first*int64(rb)); err != nil {
			return err
		}
		for k := 0; k < n; k++ {
			s.put(c, k, raw[(n-1-k)*rb:(n-k)*rb])
		}
		return nil
	}
	// The table of where the rows start, then that of their lengths, an
	// entry of four bytes for every row of every channel.
	starts, lengths := s.table[:4*n], s.table[4*n:8*n]
	entries := int64(h.h) * int64(h.z)
	if err := readAt(s.r, starts, headerSize+4*first); err != nil {
		return err
	}
	if err := readAt(s.r, lengths, headerSize+4*(entries+first)); err != nil {
		return err
	}
	var above int64
	for k := 0; k < n; k++ {
		e := 4 * (n - 1 - k)
		start := int64(binary.BigEndian.Uint32(starts[e:]))
		if k > 0 {
			s.forward = start > above
		}
		above = start
		packed, err := s.packed(start, int64(binary.BigEndian.Uint32(lengths[e:])))
		if err != nil {
			return err
		}
		if err := unpack(s.raw, packed, h.bpc); err != nil {
			return err
		}
		s.put(c, k, s.raw)
	}
	return nil
}

// packed is the length bytes of a compressed row at start, read through
// the window. The rows are stored from the bottom up and asked for from
// the top down, so the window is filled with what lies before the end of
// the row asked for, the rows wanted next; in a file that has its rows the
// other way round, with what follows the start of the row.
func (s *stream) packed(start, length int64) ([]byte, error) {
	if start < headerSize+8*int64(s.h.h)*int64(s.h.z) || length <= 0 {
		return nil, errFormat
	}
	// No row needs more than the window is sure to hold.
	length = min(length, (2*int64(s.h.w)+1)*int64(s.h.bpc))
	if start+length > s.size {
		return nil, io.ErrUnexpectedEOF
	}
	if start < s.winOff || start+length > s.winOff+int64(s.winLen) {
		off := max(headerSize, start+length-int64(len(s.win)))
		if s.forward {
			off = start
		}
		n := int(min(int64(len(s.win)), s.size-off))
		s.winLen = 0
		if err := readAt(s.r, s.win[:n], off); err != nil {
			return nil, err
		}
		s.winOff, s.winLen = off, n
	}
	return s.win[start-s.winOff:][:length], nil
}

// unpack fills a row from its run-length packets: a count in the low seven
// bits (of the second byte when values are two bytes), with the high bit
// that many values as they are, without it one value that many times; a
// count of zero ends the row. A row that is full needs no such end
// (ffmpeg writes none, and Pillow then refuses the file); one that ends
// short, or a packet that goes past the width, is an error.
func unpack(dst, src []byte, bpc int) error {
	for i := 0; i < len(dst); {
		if len(src) < bpc {
			return io.ErrUnexpectedEOF
		}
		c := src[bpc-1]
		src = src[bpc:]
		n := int(c&0x7f) * bpc
		if n == 0 || n > len(dst)-i {
			return errFormat
		}
		if c&0x80 != 0 {
			if len(src) < n {
				return io.ErrUnexpectedEOF
			}
			copy(dst[i:i+n], src)
			src = src[n:]
		} else {
			if len(src) < bpc {
				return io.ErrUnexpectedEOF
			}
			run := dst[i : i+n]
			if bpc == 1 {
				for j := range run {
					run[j] = src[0]
				}
			} else {
				for j := 0; j < len(run); j += 2 {
					run[j], run[j+1] = src[0], src[1]
				}
			}
			src = src[bpc:]
		}
		i += n
	}
	return nil
}

// put stores the values of channel c in row k of the band: a single
// channel as grey, two as grey and alpha, three and four as RGB and RGBA.
// Values of two bytes are stored high byte first, as the band keeps them.
func (s *stream) put(c, k int, row []byte) {
	w := s.h.w
	switch b := s.band.(type) {
	case *image.Gray:
		copy(b.Pix[k*b.Stride:k*b.Stride+w], row)
	case *image.Gray16:
		copy(b.Pix[k*b.Stride:k*b.Stride+2*w], row)
	case *image.NRGBA:
		dst := b.Pix[k*b.Stride : k*b.Stride+4*w]
		switch {
		case s.h.z == 2 && c == 0:
			for x, v := range row {
				dst[4*x], dst[4*x+1], dst[4*x+2] = v, v, v
			}
		case s.h.z == 2:
			for x, v := range row {
				dst[4*x+3] = v
			}
		default:
			for x, v := range row {
				dst[4*x+c] = v
			}
		}
	case *image.NRGBA64:
		dst := b.Pix[k*b.Stride : k*b.Stride+8*w]
		for x := 0; x < w; x++ {
			hi, lo := row[2*x], row[2*x+1]
			d := dst[8*x : 8*x+8]
			switch {
			case s.h.z == 2 && c == 0:
				d[0], d[1], d[2], d[3], d[4], d[5] = hi, lo, hi, lo, hi, lo
			case s.h.z == 2:
				d[6], d[7] = hi, lo
			default:
				d[2*c], d[2*c+1] = hi, lo
			}
		}
	}
}
