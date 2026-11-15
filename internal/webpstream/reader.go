// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package webpstream is a streaming WebP decoder: it produces the image as
// horizontal strips instead of one whole image.
//
// It is a fork of golang.org/x/image/webp (with its riff, vp8 and vp8l
// packages; the latter two are in the vp8 and vp8l sub-packages) and decodes
// to exactly the same pixels:
//
//   - lossy (VP8) images come out as *image.YCbCr 4:2:0 strips of whole
//     macroblock rows; the decoder keeps one more macroblock row than it
//     hands out, because the loop filter still changes it;
//   - lossy images with an alpha channel (VP8X + ALPH) come out as
//     *image.NYCbCrA strips; the alpha plane is read (uncompressed) or
//     decoded (VP8L-compressed) row by row alongside the color data;
//   - lossless (VP8L) images come out as *image.NRGBA strips.
//
// The input has to be an io.ReaderAt: a lossy image is several
// arithmetic-coded partitions that are consumed in parallel, and the alpha
// chunk precedes the color chunk in the file but is consumed with it.
package webpstream

import (
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/internal/webpstream/vp8"
	"github.com/whhhh1500/goimgtools/internal/webpstream/vp8l"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// These are the errors of golang.org/x/image/webp and golang.org/x/image/riff.
var (
	errInvalidFormat = errors.New("webp: invalid format")

	errMissingPaddingByte     = errors.New("riff: missing padding byte")
	errMissingRIFFChunkHeader = errors.New("riff: missing RIFF chunk header")
	errListSubchunkTooLong    = errors.New("riff: list subchunk too long")
	errShortChunkData         = errors.New("riff: short chunk data")
	errShortChunkHeader       = errors.New("riff: short chunk header")
)

// errEmpty is returned for a lossy image whose header says it is zero pixels
// wide or high. (golang.org/x/image/webp returns an empty image for it.)
var errEmpty = errors.New("webp: image has no pixels")

// testBandRows, when positive, overrides the number of rows per strip, so
// that tests exercise strip boundaries on small images. Lossy strips are
// whole macroblock rows: at least 16 rows.
var testBandRows int

// u32 decodes the first four bytes of b as a little-endian integer.
func u32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}

// readAt reads len(p) bytes at off, or fewer if the input ends first. It
// returns an error only for a failed read, not for a short input.
func readAt(r io.ReaderAt, p []byte, off, size int64) (int, error) {
	if off >= size {
		return 0, nil
	}
	if rem := size - off; int64(len(p)) > rem {
		p = p[:rem]
	}
	n, err := r.ReadAt(p, off)
	if n == len(p) || err == io.EOF {
		err = nil
	}
	return n, err
}

// source is the Source of all three kinds of image.
type source struct {
	env      *pipeline.Env
	meta     pipeline.Meta
	ra       io.ReaderAt
	bounds   image.Rectangle
	model    color.Model
	reserved int64
	started  bool
	closed   bool
	err      error

	// A lossy image.
	lossy bool
	vd    *vp8.Decoder
	// Its alpha plane, if hasAlpha.
	hasAlpha   bool
	alphaOff   int64 // file offset of the alpha data, after the flags byte
	alphaEnd   int64
	alphaFlags byte
	alphaLD    *vp8l.Decoder // for VP8L-compressed alpha
	alphaY     int
	alphaPrev  []byte // the last alpha row of the previous strip
	alphaBuf   []byte // the alpha rows of the current strip

	// A lossless image: ld is not nil.
	ld        *vp8l.Decoder
	band      []byte
	stripRows int
	y         int
}

// Open reads the WebP headers from the size bytes of r and returns a Source
// of the image's pixels. Nothing else is read or allocated until the first
// call to Next.
//
// It fails wherever golang.org/x/image/webp.Decode fails (some failures only
// show up in Next), and for animated images, which have no still-image chunk,
// with the same "webp: invalid format" error.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	var buf [12]byte
	n, err := readAt(r, buf[:8], 0, size)
	if err != nil {
		return nil, err
	}
	if n < 8 || string(buf[:4]) != "RIFF" {
		return nil, errMissingRIFFChunkHeader
	}
	riffLen := u32(buf[4:])
	if riffLen < 4 {
		return nil, errShortChunkData
	}
	if n, err = readAt(r, buf[:4], 8, size); err != nil {
		return nil, err
	}
	if n < 4 {
		return nil, errShortChunkData
	}
	// totalLen is what is left of the RIFF chunk, as in riff.Reader.
	totalLen := riffLen - 4
	if string(buf[:4]) != "WEBP" {
		return nil, errInvalidFormat
	}

	s := &source{env: env, ra: r}
	var (
		wantAlpha      bool
		seenVP8X       bool
		vp8xFlags      byte
		widthMinusOne  uint32
		heightMinusOne uint32
	)
	pos := int64(12)
	for {
		// Read the next chunk header.
		if totalLen == 0 {
			return nil, errInvalidFormat
		}
		if totalLen < 8 {
			return nil, errShortChunkHeader
		}
		totalLen -= 8
		if n, err = readAt(r, buf[:8], pos, size); err != nil {
			return nil, err
		}
		if n < 8 {
			return nil, errShortChunkHeader
		}
		pos += 8
		chunkLen := u32(buf[4:])
		if chunkLen > totalLen {
			return nil, errListSubchunkTooLong
		}
		// avail is how much of the chunk's data the input really has.
		avail := max(min(size-pos, int64(chunkLen)), 0)

		switch string(buf[:4]) {
		case "ALPH":
			if !wantAlpha {
				return nil, errInvalidFormat
			}
			wantAlpha = false
			// Read the Pre-processing | Filter | Compression byte.
			if avail == 0 {
				return nil, errInvalidFormat
			}
			if _, err := r.ReadAt(buf[:1], pos); err != nil {
				return nil, err
			}
			w, h := int64(widthMinusOne)+1, int64(heightMinusOne)+1
			switch buf[0] & 0x03 {
			case 0:
				if avail-1 < w*h {
					return nil, io.ErrUnexpectedEOF
				}
			case 1:
				if widthMinusOne > 0x3fff || heightMinusOne > 0x3fff {
					return nil, errInvalidFormat
				}
			default:
				return nil, errInvalidFormat
			}
			// golang.org/x/image/webp skips to the end of the chunk next,
			// which fails if the input ends inside it.
			if avail < int64(chunkLen) {
				return nil, errShortChunkData
			}
			s.hasAlpha = true
			s.alphaFlags = buf[0]
			s.alphaOff, s.alphaEnd = pos+1, pos+int64(chunkLen)

		case "VP8 ":
			if wantAlpha || int32(chunkLen) < 0 {
				return nil, errInvalidFormat
			}
			vd := vp8.NewDecoder(r, pos, int(min(avail, int64(chunkLen))))
			fh, err := vd.DecodeFrameHeader()
			if err != nil {
				return nil, err
			}
			if seenVP8X && (fh.Width != int(widthMinusOne)+1 || fh.Height != int(heightMinusOne)+1) {
				return nil, errInvalidFormat
			}
			// The decoder reads the whole chunk.
			if avail < int64(chunkLen) {
				return nil, io.ErrUnexpectedEOF
			}
			if !fh.KeyFrame {
				return nil, errors.New("vp8: Golden / AltRef frames are not implemented")
			}
			if fh.Width == 0 || fh.Height == 0 {
				return nil, errEmpty
			}
			if seenVP8X && vp8xFlags&(flagEXIF|flagICC) != 0 {
				s.scanMeta(r, pos+int64(chunkLen)+int64(chunkLen&1), min(size, pos+int64(totalLen)))
			}
			s.vd, s.lossy = vd, true
			s.bounds = image.Rect(0, 0, fh.Width, fh.Height)
			s.model = color.YCbCrModel
			if s.hasAlpha {
				s.model = color.NYCbCrAModel
			}
			return s, nil

		case "VP8L":
			if s.hasAlpha {
				return nil, errInvalidFormat
			}
			ld := vp8l.NewDecoder(r, pos, pos+avail, env)
			w, h, err := ld.DecodeHeader()
			if err != nil {
				if seenVP8X && err == io.ErrUnexpectedEOF {
					err = errInvalidFormat
				}
				return nil, err
			}
			if seenVP8X && (w != int(widthMinusOne)+1 || h != int(heightMinusOne)+1) {
				// A small VP8X header followed by a huge VP8L chunk.
				return nil, errInvalidFormat
			}
			if seenVP8X && vp8xFlags&(flagEXIF|flagICC) != 0 {
				s.scanMeta(r, pos+int64(chunkLen)+int64(chunkLen&1), min(size, pos+int64(totalLen)))
			}
			s.ld = ld
			s.bounds = image.Rect(0, 0, w, h)
			s.model = color.NRGBAModel
			return s, nil

		case "VP8X":
			if seenVP8X {
				return nil, errInvalidFormat
			}
			seenVP8X = true
			if chunkLen != 10 {
				return nil, errInvalidFormat
			}
			if avail < 10 {
				return nil, io.ErrUnexpectedEOF
			}
			if _, err := r.ReadAt(buf[:10], pos); err != nil {
				return nil, err
			}
			const alphaBit = 1 << 4
			wantAlpha = (buf[0] & alphaBit) != 0
			vp8xFlags = buf[0]
			widthMinusOne = uint32(buf[4]) | uint32(buf[5])<<8 | uint32(buf[6])<<16
			heightMinusOne = uint32(buf[7]) | uint32(buf[8])<<8 | uint32(buf[9])<<16
			w := uint64(widthMinusOne) + 1
			h := uint64(heightMinusOne) + 1
			if w*h > 1<<31-1 {
				// The product of _Canvas Width_ and _Canvas Height_ MUST be
				// at most 2^32 - 1.
				// But it also needs to fit in an int, so limit it to MaxInt32.
				return nil, errInvalidFormat
			}

		default:
			// Any other chunk (XMP, ANIM, ANMF, ...) is skipped; the colour
			// profile, and EXIF in the rare files that put it before the
			// image, are kept.
			if avail < int64(chunkLen) {
				return nil, errShortChunkData
			}
			if name := string(buf[:4]); name == "ICCP" || name == "EXIF" {
				s.readMeta(name, r, pos, chunkLen)
			}
		}

		// Move past the chunk's data and its padding byte.
		totalLen -= chunkLen
		pos += int64(chunkLen)
		if chunkLen&1 == 1 {
			if totalLen == 0 {
				return nil, errListSubchunkTooLong
			}
			totalLen--
			if pos >= size {
				return nil, errMissingPaddingByte
			}
			pos++
		}
	}
}

func (s *source) Bounds() image.Rectangle { return s.bounds }
func (s *source) ColorModel() color.Model { return s.model }
func (s *source) Env() *pipeline.Env      { return s.env }

// Opaque reports whether the image is known to have no transparency: a
// lossy image without an alpha chunk.
func (s *source) Opaque() bool { return s.lossy && !s.hasAlpha }

func (s *source) reserve(n int64, what string) error {
	if err := s.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	s.reserved += n
	return nil
}

func (s *source) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.closed {
		return nil, io.EOF
	}
	var m image.Image
	var err error
	if s.lossy {
		m, err = s.nextLossy()
	} else {
		m, err = s.nextLossless()
	}
	if err != nil {
		if err != io.EOF {
			s.err = err
		}
		return nil, err
	}
	return m, nil
}

// startLossy allocates the lossy decoder's buffers and parses the rest of
// its headers.
func (s *source) startLossy() error {
	w, h := s.bounds.Dx(), s.bounds.Dy()
	mbw := (w + 15) / 16
	// A strip is whole macroblock rows: about 256KB of them, at least one.
	bandMB := max(pipeline.StripRows(int64(mbw)*24, h)/16, 1)
	if testBandRows > 0 {
		bandMB = max(testBandRows/16, 1)
	}
	if err := s.reserve(s.vd.BufferBytes(bandMB), "webp macroblock rows"); err != nil {
		return err
	}
	if err := s.vd.Start(bandMB); err != nil {
		return err
	}
	if !s.hasAlpha {
		return nil
	}
	// The last strip can be one macroblock row taller than the others.
	rows := min(16*(bandMB+1), h)
	if err := s.reserve(int64(w)*int64(rows+1), "webp alpha rows"); err != nil {
		return err
	}
	s.alphaBuf = make([]byte, w*rows)
	s.alphaPrev = make([]byte, w)
	if s.alphaFlags&0x03 == 1 {
		// The alpha values are the green values of a VP8L image that has no
		// header of its own.
		s.alphaLD = vp8l.NewDecoder(s.ra, s.alphaOff, s.alphaEnd, s.env)
		s.alphaLD.SetSize(w, h)
	}
	return nil
}

func (s *source) nextLossy() (image.Image, error) {
	if !s.started {
		s.started = true
		if err := s.startLossy(); err != nil {
			return nil, err
		}
	}
	m, err := s.vd.NextBand()
	if err != nil {
		return nil, err
	}
	if !s.hasAlpha {
		return m, nil
	}
	w, n := m.Rect.Dx(), m.Rect.Dy()
	a := s.alphaBuf[:w*n]
	if err := s.readAlpha(a, w, n); err != nil {
		return nil, err
	}
	return &image.NYCbCrA{YCbCr: *m, A: a, AStride: w}, nil
}

// readAlpha fills a with the next n rows of the alpha plane.
func (s *source) readAlpha(a []byte, w, n int) error {
	if s.alphaLD != nil {
		for y := 0; y < n; y++ {
			row, err := s.alphaLD.NextRow()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return err
			}
			// The green values of the inner image are the alpha values of
			// the outer image.
			dst := a[y*w : y*w+w]
			for i := range dst {
				dst[i] = row[4*i+1]
			}
		}
	} else {
		k, err := s.ra.ReadAt(a, s.alphaOff+int64(s.alphaY)*int64(w))
		if k < len(a) {
			if err == nil || err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
	}
	// Undo the filter. A row needs the row above, which for the first row of
	// a strip is the last row of the previous strip.
	if filter := (s.alphaFlags >> 2) & 0x03; filter != 0 {
		prev := s.alphaPrev
		for y := 0; y < n; y++ {
			cur := a[y*w : y*w+w]
			unfilterAlphaRow(cur, prev, filter, s.alphaY+y == 0)
			prev = cur
		}
		copy(s.alphaPrev, prev)
	}
	s.alphaY += n
	return nil
}

// unfilterAlphaRow undoes the alpha filter for the row cur, given the row
// above it (already unfiltered). It is unfilterAlpha of
// golang.org/x/image/webp for one row.
func unfilterAlphaRow(cur, prev []byte, filter byte, first bool) {
	if first {
		// The first row is equivalent to the horizontal filter.
		for i := 1; i < len(cur); i++ {
			cur[i] += cur[i-1]
		}
		return
	}
	switch filter {
	case 1: // Horizontal filter.
		// The first column is equivalent to the vertical filter.
		cur[0] += prev[0]
		for j := 1; j < len(cur); j++ {
			cur[j] += cur[j-1]
		}

	case 2: // Vertical filter.
		for i := range cur {
			cur[i] += prev[i]
		}

	case 3: // Gradient filter.
		// The first column is equivalent to the vertical filter.
		cur[0] += prev[0]
		// The interior is predicted on the three top/left pixels.
		for j := 1; j < len(cur); j++ {
			c := int(prev[j-1])
			b := int(prev[j])
			a := int(cur[j-1])
			x := a + b - c
			if x < 0 {
				x = 0
			} else if x > 255 {
				x = 255
			}
			cur[j] += uint8(x)
		}
	}
}

func (s *source) nextLossless() (image.Image, error) {
	w, h := s.bounds.Dx(), s.bounds.Dy()
	if !s.started {
		s.started = true
		s.stripRows = pipeline.StripRows(4*int64(w), h)
		if testBandRows > 0 {
			s.stripRows = min(testBandRows, h)
		}
		if err := s.reserve(4*int64(w)*int64(s.stripRows), "webp strip"); err != nil {
			return nil, err
		}
		s.band = make([]byte, 4*w*s.stripRows)
	}
	if s.y >= h {
		return nil, io.EOF
	}
	y0, y1 := s.y, min(s.y+s.stripRows, h)
	for y := y0; y < y1; y++ {
		row, err := s.ld.NextRow()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		copy(s.band[(y-y0)*4*w:], row)
	}
	s.y = y1
	return &image.NRGBA{
		Pix:    s.band[:(y1-y0)*4*w],
		Stride: 4 * w,
		Rect:   image.Rect(0, y0, w, y1),
	}, nil
}

// Close releases the memory and deletes the temporary files. It is safe to
// call more than once.
func (s *source) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	var err error
	if s.ld != nil {
		err = s.ld.Close()
	}
	if s.alphaLD != nil {
		if cerr := s.alphaLD.Close(); err == nil {
			err = cerr
		}
	}
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
	s.vd, s.ld, s.alphaLD = nil, nil, nil
	s.band, s.alphaBuf, s.alphaPrev = nil, nil, nil
	return err
}
