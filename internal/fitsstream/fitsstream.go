// Package fitsstream reads FITS (Flexible Image Transport System), the
// file of astronomy: the first image of the file when it has two
// dimensions (further ones of length 1 are allowed), a band of rows at a
// time. That is the primary array or, when the primary header has no data,
// the first IMAGE extension. Samples of 8, 16, 32 and 64 bits and floating
// point of 32 and 64, all big-endian, are read; tables and tile-compressed
// images are refused.
//
// FITS stores measurements, not display values. The value of a pixel is
// BZERO + BSCALE * the stored number (both keywords optional: 0 and 1).
// Samples of 8 bits give an *image.Gray, the value limited to 0..255; all
// other kinds give an *image.Gray16, the value limited to 0..65535 (below
// 0 is 0, above 65535 is 65535, not a number is 0) and a fraction dropped
// (rounded toward zero, as Pillow converts floating point to integers).
// The first row stored is the bottom of the image, as Pillow reads it.
//
// Compared with Pillow 12.1: samples of 8 bits without BZERO and BSCALE
// are the same pixels. Pillow ignores BZERO and BSCALE, reads 16 and 32
// bit integers and 32-bit floating point in the byte order of a
// little-endian machine (FITS is big-endian, so on such a machine its
// numbers are byte-swapped), reads 64-bit floating point as if it were 32
// and does not open 64-bit integers; here the numbers are read as the FITS
// standard defines them. Pillow cannot turn its 32-bit and floating point
// modes into 16 bits without loss (it limits floating point to 0..255 on
// the way), so the limit of 0..65535 is this package's own.
package fitsstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strconv"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var (
	errFormat  = errors.New("goimgtools: fits: not a valid FITS file")
	errNoImage = errors.New("goimgtools: fits: the file has no image")
)

// unsupported is the error for a valid file of a kind that is not read.
func unsupported(what string) error {
	return fmt.Errorf("goimgtools: fits: %s is not supported: %w", what, errors.ErrUnsupported)
}

const (
	cardLen  = 80   // a header is lines ("cards") of 80 characters
	blockLen = 2880 // headers and data are padded to blocks of 2880 bytes
	// maxSide bounds the width and the height.
	maxSide = 1 << 24
	// maxUnits is how many header units without data are stepped over
	// before the file is said to have no image.
	maxUnits = 64
	// maxBlocks bounds the blocks of one header (real ones have a few).
	maxBlocks = 1 << 14
)

// Match reports whether head starts a FITS file: the card "SIMPLE  = T".
func Match(head []byte) bool {
	if len(head) < 10 || string(head[:9]) != "SIMPLE  =" {
		return false
	}
	v := bytes.TrimLeft(head[9:], " ")
	return len(v) > 0 && v[0] == 'T'
}

// unit is what the header of one unit of the file says.
type unit struct {
	xtension string // "" for the primary header
	bitpix   int64
	naxis    int64
	axis     [2]int64 // NAXIS1 and NAXIS2
	deep     int64    // the first axis after the second longer than 1, or 0
	zimage   bool     // a tile-compressed image in a table
	bscale   float64
	bzero    float64
	seen     uint // bit 0 BITPIX, 1 NAXIS, 2 NAXIS1, 3 NAXIS2
	end      int64
}

// header describes the image to read.
type header struct {
	w, h   int
	bitpix int
	data   int64 // where the first (the bottom) row starts
	bscale float64
	bzero  float64
}

// trim removes the blanks around b.
func trim(b []byte) []byte { return bytes.Trim(b, " \t\r\n\v\f") }

// readUnit reads the header that starts at off. Cards are read as Pillow
// reads them: the keyword is the first 8 characters, the value what stands
// between them (and an "=") and the first "/".
func readUnit(r io.ReaderAt, off int64, primary bool) (*unit, error) {
	u := &unit{bscale: 1}
	var buf [blockLen]byte
	for first, blocks := true, 0; ; off, blocks = off+blockLen, blocks+1 {
		if blocks == maxBlocks {
			return nil, errFormat
		}
		n, _ := r.ReadAt(buf[:], off)
		for c := 0; c+cardLen <= n; c += cardLen {
			card := buf[c : c+cardLen]
			key := trim(card[:8])
			val := card[8:]
			if i := bytes.IndexByte(val, '/'); i >= 0 {
				val = val[:i]
			}
			val = trim(val)
			if len(val) > 0 && val[0] == '=' {
				val = trim(val[1:])
			}
			if first {
				first = false
				switch {
				case primary && (!bytes.HasPrefix(key, []byte("SIMPLE")) || string(val) != "T"):
					return nil, errFormat
				case !primary && string(key) != "XTENSION":
					return nil, errNoImage
				}
			}
			var err error
			switch string(key) {
			case "END":
				u.end = (off + int64(c) + cardLen + blockLen - 1) / blockLen * blockLen
				return u, nil
			case "XTENSION":
				u.xtension = string(bytes.TrimRight(bytes.Trim(val, "'"), " "))
			case "BITPIX":
				u.bitpix, err = strconv.ParseInt(string(val), 10, 64)
				u.seen |= 1
			case "NAXIS":
				u.naxis, err = strconv.ParseInt(string(val), 10, 64)
				u.seen |= 2
			case "NAXIS1":
				u.axis[0], err = strconv.ParseInt(string(val), 10, 64)
				u.seen |= 4
			case "NAXIS2":
				u.axis[1], err = strconv.ParseInt(string(val), 10, 64)
				u.seen |= 8
			case "BSCALE":
				u.bscale, err = number(val)
			case "BZERO":
				u.bzero, err = number(val)
			case "ZIMAGE":
				u.zimage = string(val) == "T"
			default:
				// NAXIS3 and up: only whether one is longer than 1 matters.
				if len(key) > 5 && string(key[:5]) == "NAXIS" {
					if i, e := strconv.ParseInt(string(key[5:]), 10, 64); e == nil && i >= 3 {
						var v int64
						if v, err = strconv.ParseInt(string(val), 10, 64); err == nil && v != 1 && (u.deep == 0 || i < u.deep) {
							u.deep = i
						}
					}
				}
			}
			if err != nil {
				return nil, errFormat
			}
		}
		if n < blockLen {
			// The file ends before the END card.
			if first && primary {
				return nil, errFormat
			}
			if first {
				return nil, errNoImage
			}
			return nil, io.ErrUnexpectedEOF
		}
	}
}

// number reads a floating point value of a card; FITS also writes the
// exponent with a D.
func number(val []byte) (float64, error) {
	b := append([]byte(nil), val...)
	for i, c := range b {
		if c == 'D' || c == 'd' {
			b[i] = 'E'
		}
	}
	v, err := strconv.ParseFloat(string(b), 64)
	if err == nil && (math.IsNaN(v) || math.IsInf(v, 0)) {
		err = errFormat
	}
	return v, err
}

// readHeader finds the first unit of the file with data and checks that it
// is an image this package reads.
func readHeader(r io.ReaderAt) (*header, error) {
	off := int64(0)
	for i := 0; i < maxUnits; i++ {
		u, err := readUnit(r, off, i == 0)
		if err != nil {
			return nil, err
		}
		switch u.xtension {
		case "", "IMAGE":
		case "BINTABLE":
			if u.zimage {
				return nil, unsupported("a tile-compressed image")
			}
			return nil, unsupported("a table (the file has no plain image before it)")
		case "TABLE":
			return nil, unsupported("a table (the file has no plain image before it)")
		default:
			return nil, unsupported(fmt.Sprintf("an extension of type %q", u.xtension))
		}
		if u.seen&3 != 3 || u.naxis < 0 || u.naxis > 999 {
			return nil, errFormat
		}
		if u.naxis == 0 {
			// No data here: the image may be in the extension that follows.
			off = u.end
			continue
		}
		switch u.bitpix {
		case 8, 16, 32, 64, -32, -64:
		default:
			return nil, errFormat
		}
		if u.naxis == 1 {
			return nil, unsupported("a one-dimensional array")
		}
		if u.seen&12 != 12 || u.axis[0] < 0 || u.axis[1] < 0 || u.axis[0] > maxSide || u.axis[1] > maxSide {
			return nil, errFormat
		}
		if u.axis[0] == 0 || u.axis[1] == 0 {
			return nil, errNoImage
		}
		if u.deep != 0 && u.deep <= u.naxis {
			return nil, unsupported(fmt.Sprintf("a data cube (NAXIS%d is not 1)", u.deep))
		}
		return &header{w: int(u.axis[0]), h: int(u.axis[1]), bitpix: int(u.bitpix), data: u.end, bscale: u.bscale, bzero: u.bzero}, nil
	}
	return nil, errNoImage
}

// stream is a FITS image decoded a band of rows at a time.
type stream struct {
	r   io.ReaderAt
	env *pipeline.Env
	h   *header
	bpp int // stored bytes of a sample
	// How a stored number becomes a value: plain is BZERO 0 and BSCALE 1;
	// exact is BSCALE 1 and a whole BZERO, added (shift) in integers;
	// unsigned64 is BZERO 2^63, the way 64-bit numbers without sign are
	// stored. Otherwise the value is worked out in floating point.
	plain, exact, unsigned64 bool
	shift                    int64
	top                      int64      // the largest output value
	lut                      [256]uint8 // of 8-bit samples
	gray                     *image.Gray
	gray16                   *image.Gray16
	raw                      []byte
	n, y                     int
	reserved                 int64
	err                      error
}

// Open reads the headers of a FITS file of size bytes and returns its
// image, decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	h, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	s := &stream{r: r, env: env, h: h, bpp: max(h.bitpix, -h.bitpix) / 8, top: 65535}
	rb := int64(h.w) * int64(s.bpp)
	if rb > (size-h.data)/int64(h.h) {
		return nil, io.ErrUnexpectedEOF
	}
	s.plain = h.bscale == 1 && h.bzero == 0
	if h.bscale == 1 && h.bzero == math.Trunc(h.bzero) {
		const big = 1 << 62
		switch {
		case h.bzero >= -big && h.bzero <= big:
			s.exact, s.shift = true, int64(h.bzero)
		case h.bzero == 2*big:
			s.unsigned64 = true
		}
	}
	out := int64(2)
	if h.bitpix == 8 {
		out, s.top = 1, 255
		for v := range s.lut {
			s.lut[v] = uint8(s.level(int64(v)))
		}
	}
	// Samples of 8 bits are read straight into the band.
	raw := rb
	if h.bitpix == 8 {
		raw = 0
	}
	s.n = pipeline.StripRows(int64(h.w)*out+raw, h.h)
	s.reserved = int64(s.n) * (int64(h.w)*out + raw)
	if err := env.Budget.Reserve(s.reserved, "fits band"); err != nil {
		return nil, err
	}
	r0 := image.Rect(0, 0, h.w, s.n)
	if h.bitpix == 8 {
		s.gray = image.NewGray(r0)
	} else {
		s.gray16 = image.NewGray16(r0)
		s.raw = make([]byte, int64(s.n)*rb)
	}
	return s, nil
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.h.w, s.h.h) }
func (s *stream) ColorModel() color.Model {
	if s.h.bitpix == 8 {
		return color.GrayModel
	}
	return color.Gray16Model
}

// Opaque reports that a FITS image has no transparency.
func (s *stream) Opaque() bool { return true }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.raw, s.gray, s.gray16 = nil, nil, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: fits: closed")
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
	if err := s.decode(n); err != nil {
		s.err = err
		return nil, err
	}
	rect := image.Rect(0, s.y, s.h.w, s.y+n)
	s.y += n
	if b := s.gray; b != nil {
		return &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	}
	b := s.gray16
	return &image.Gray16{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
}

// level is the output value of a stored whole number v.
func (s *stream) level(v int64) uint16 {
	switch {
	case s.unsigned64:
		return uint16(min(uint64(v)^1<<63, uint64(s.top)))
	case s.exact:
		// v + shift, limited to 0..top, without overflow.
		b := s.shift
		if b >= 0 {
			if v > s.top-b {
				return uint16(s.top)
			}
			if v < -b {
				return 0
			}
			return uint16(v + b)
		}
		if v < -b {
			return 0
		}
		return uint16(min(v+b, s.top))
	}
	return s.levelOf(float64(v))
}

// levelOf is the output value of a stored number v: BZERO + BSCALE * v,
// its fraction dropped, limited to 0..top; not a number is 0.
func (s *stream) levelOf(v float64) uint16 {
	if !s.plain {
		v = float64(s.h.bscale*v) + s.h.bzero
	}
	if !(v >= 1) {
		return 0
	}
	if v >= float64(s.top) {
		return uint16(s.top)
	}
	return uint16(v)
}

// decode reads image rows y to y+n-1 and converts them into the band. The
// file stores the bottom row first, so they are the n rows that end
// h-y rows into the data, in reverse.
func (s *stream) decode(n int) error {
	h := s.h
	if h.bitpix == 8 {
		b := s.gray
		pix := b.Pix[:n*h.w]
		if err := readFull(s.r, pix, h.data+int64(h.h-s.y-n)*int64(h.w)); err != nil {
			return err
		}
		for i, j := 0, n-1; i < j; i, j = i+1, j-1 {
			ri, rj := pix[i*h.w:(i+1)*h.w], pix[j*h.w:(j+1)*h.w]
			for x := range ri {
				ri[x], rj[x] = rj[x], ri[x]
			}
		}
		if !s.plain {
			for i, v := range pix {
				pix[i] = s.lut[v]
			}
		}
		return nil
	}
	rb := h.w * s.bpp
	raw := s.raw[:n*rb]
	if err := readFull(s.r, raw, h.data+int64(h.h-s.y-n)*int64(rb)); err != nil {
		return err
	}
	for k := 0; k < n; k++ {
		src := raw[(n-1-k)*rb : (n-k)*rb]
		dst := s.gray16.Pix[k*s.gray16.Stride:][:2*h.w]
		switch h.bitpix {
		case 16:
			switch {
			case s.plain:
				// The stored number, big-endian as the band is; below 0 is 0.
				for i := 0; i < len(dst); i += 2 {
					if src[i]&0x80 != 0 {
						dst[i], dst[i+1] = 0, 0
					} else {
						dst[i], dst[i+1] = src[i], src[i+1]
					}
				}
			case s.exact && s.shift == 32768:
				// Numbers without sign, stored 32768 lower.
				for i := 0; i < len(dst); i += 2 {
					dst[i], dst[i+1] = src[i]^0x80, src[i+1]
				}
			default:
				for i := 0; i < len(dst); i += 2 {
					binary.BigEndian.PutUint16(dst[i:], s.level(int64(int16(binary.BigEndian.Uint16(src[i:])))))
				}
			}
		case 32:
			for i, j := 0, 0; i < len(dst); i, j = i+2, j+4 {
				binary.BigEndian.PutUint16(dst[i:], s.level(int64(int32(binary.BigEndian.Uint32(src[j:])))))
			}
		case 64:
			for i, j := 0, 0; i < len(dst); i, j = i+2, j+8 {
				binary.BigEndian.PutUint16(dst[i:], s.level(int64(binary.BigEndian.Uint64(src[j:]))))
			}
		case -32:
			for i, j := 0, 0; i < len(dst); i, j = i+2, j+4 {
				binary.BigEndian.PutUint16(dst[i:], s.levelOf(float64(math.Float32frombits(binary.BigEndian.Uint32(src[j:])))))
			}
		default:
			for i, j := 0, 0; i < len(dst); i, j = i+2, j+8 {
				binary.BigEndian.PutUint16(dst[i:], s.levelOf(math.Float64frombits(binary.BigEndian.Uint64(src[j:]))))
			}
		}
	}
	return nil
}

// readFull reads the len(p) bytes at off; a file that ends before the last
// of them is cut short.
func readFull(r io.ReaderAt, p []byte, off int64) error {
	n, err := r.ReadAt(p, off)
	if n == len(p) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}
