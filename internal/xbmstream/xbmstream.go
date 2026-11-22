// Package xbmstream reads XBM, the bitmap of X11 written as C source: the
// lines "#define name_width 16" and "#define name_height 16", perhaps a
// hot spot, and an array of bytes, "static unsigned char name_bits[] = {
// 0x3c, ... }", each row from a new byte, the lowest bit of a byte its
// leftmost pixel. A bit that is set is drawn black on white, as X11,
// ImageMagick and browsers draw it (Pillow reads it as white on black).
// The text is read as the rows are asked for, a band of rows at a time.
package xbmstream

import (
	"bufio"
	"errors"
	"image"
	"image/color"
	"io"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var (
	errFormat      = errors.New("goimgtools: xbm: not a valid XBM file")
	errUnsupported = errors.New("goimgtools: xbm: an X10 bitmap of 16-bit words")
)

const (
	// maxSide is the most pixels read across or down, as for Netpbm.
	maxSide = 1 << 24
	bufSize = 32 << 10
	// maxWord is the longest name, number or declaration of the header.
	maxWord = 1024
)

// Match reports whether head starts what can be an XBM file: "#define"
// after any white space, as Pillow tells it.
func Match(head []byte) bool {
	i := 0
	for i < len(head) && isSpace(head[i]) {
		i++
	}
	return strings.HasPrefix(string(head[i:]), "#define")
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v'
}

func isWord(c byte) bool {
	return c == '_' || c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}

// scanner reads the text a byte at a time and knows how far it has read.
type scanner struct {
	r   *bufio.Reader
	off int64
}

func (s *scanner) get() (byte, error) {
	c, err := s.r.ReadByte()
	if err == nil {
		s.off++
	}
	return c, err
}

// unget puts back the byte get returned last.
func (s *scanner) unget() {
	s.r.UnreadByte()
	s.off--
}

// space steps over white space and comments; newline says whether the end
// of a line is white space too.
func (s *scanner) space(newline bool) error {
	for {
		c, err := s.get()
		if err != nil {
			return err
		}
		switch {
		case c == '\n' || c == '\r':
			if !newline {
				s.unget()
				return nil
			}
		case isSpace(c):
		case c == '/':
			if c, err = s.get(); err != nil {
				return err
			}
			switch c {
			case '*':
				for last := byte(0); ; last = c {
					if c, err = s.get(); err != nil {
						return err
					}
					if last == '*' && c == '/' {
						break
					}
				}
			case '/':
				for c != '\n' {
					if c, err = s.get(); err != nil {
						return err
					}
				}
				s.unget()
			default:
				return errFormat
			}
		default:
			s.unget()
			return nil
		}
	}
}

// word reads the letters, digits and underscores that follow (and a sign
// before them).
func (s *scanner) word() (string, error) {
	var b []byte
	for {
		c, err := s.get()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		if !isWord(c) && !(c == '-' && len(b) == 0) {
			s.unget()
			break
		}
		if b = append(b, c); len(b) > maxWord {
			return "", errFormat
		}
	}
	return string(b), nil
}

// header reads the sizes and the declaration of the array, up to its
// opening brace.
func (s *scanner) header() (w, h int, err error) {
	for {
		if err := s.space(true); err != nil {
			return 0, 0, errFormat
		}
		c, _ := s.get()
		if c != '#' {
			s.unget()
			break
		}
		// #define name value
		if err := s.space(false); err != nil {
			return 0, 0, errFormat
		}
		if d, err := s.word(); err != nil || d != "define" {
			return 0, 0, errFormat
		}
		if err := s.space(false); err != nil {
			return 0, 0, errFormat
		}
		name, err := s.word()
		if err != nil || name == "" {
			return 0, 0, errFormat
		}
		if err := s.space(false); err != nil {
			return 0, 0, errFormat
		}
		val, err := s.word()
		if err != nil {
			return 0, 0, errFormat
		}
		side := &w
		switch {
		case name == "width" || strings.HasSuffix(name, "_width"):
		case name == "height" || strings.HasSuffix(name, "_height"):
			side = &h
		default:
			side = nil // the hot spot, or anything else: not used
		}
		if side != nil {
			v, err := strconv.ParseInt(val, 0, 32)
			if err != nil || v < 1 || v > maxSide {
				return 0, 0, errFormat
			}
			*side = int(v)
		}
		// Whatever else the line holds.
		for {
			c, err := s.get()
			if err != nil {
				return 0, 0, errFormat
			}
			if c == '\n' || c == '\r' {
				break
			}
		}
	}
	if w == 0 || h == 0 {
		return 0, 0, errFormat
	}
	// The declaration: "static unsigned char name_bits[] = {".
	char, short := false, false
	for n := 0; ; n++ {
		if err := s.space(true); err != nil || n > maxWord {
			return 0, 0, errFormat
		}
		c, _ := s.get()
		if c == '{' {
			break
		}
		if !isWord(c) {
			continue
		}
		s.unget()
		t, err := s.word()
		if err != nil {
			return 0, 0, errFormat
		}
		char = char || t == "char"
		short = short || t == "short"
	}
	switch {
	case short:
		return 0, 0, errUnsupported
	case !char:
		return 0, 0, errFormat
	}
	return w, h, nil
}

// value reads the next number of the array: hexadecimal as every writer
// has it, or decimal or octal as C reads them. Its low eight bits count.
func (s *scanner) value() (byte, error) {
	var c byte
	for {
		if err := s.space(true); err != nil {
			return 0, err
		}
		c, _ = s.get()
		if c != ',' {
			break
		}
	}
	switch {
	case c == '}':
		return 0, io.ErrUnexpectedEOF // the array ends before the image
	case c < '0' || c > '9':
		return 0, errFormat
	}
	v, base, digits := uint(c-'0'), uint(10), 1
	if c == '0' {
		base = 8
		if c, err := s.get(); err == nil {
			if c == 'x' || c == 'X' {
				base, digits = 16, 0
			} else {
				s.unget()
			}
		}
	}
	for {
		c, err := s.get()
		if err != nil {
			break
		}
		d := uint(16)
		switch {
		case c >= '0' && c <= '9':
			d = uint(c - '0')
		case c >= 'a' && c <= 'f':
			d = uint(c-'a') + 10
		case c >= 'A' && c <= 'F':
			d = uint(c-'A') + 10
		}
		if d >= base {
			if isWord(c) {
				return 0, errFormat
			}
			s.unget()
			break
		}
		v = (v*base + d) & 0xffff
		digits++
	}
	if digits == 0 {
		return 0, errFormat
	}
	return byte(v), nil
}

// stream is an XBM image decoded a band of rows at a time.
type stream struct {
	text     scanner
	env      *pipeline.Env
	w, h     int
	band     *image.Gray
	n, y     int
	reserved int64
	err      error
}

// Open reads the header of an XBM file of size bytes and returns its
// image, decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	if err := env.Budget.Reserve(bufSize, "xbm text"); err != nil {
		return nil, err
	}
	s := &stream{env: env, reserved: bufSize}
	s.text.r = bufio.NewReaderSize(io.NewSectionReader(r, 0, size), bufSize)
	var err error
	if s.w, s.h, err = s.text.header(); err != nil {
		s.Close()
		return nil, err
	}
	// A byte takes two characters at the least, its digit and a comma.
	if bytes := int64(s.w+7) / 8 * int64(s.h); 2*bytes-1 > size-s.text.off {
		s.Close()
		return nil, io.ErrUnexpectedEOF
	}
	s.n = pipeline.StripRows(int64(s.w), s.h)
	band := int64(s.n) * int64(s.w)
	if err := env.Budget.Reserve(band, "xbm band"); err != nil {
		s.Close()
		return nil, err
	}
	s.reserved += band
	s.band = image.NewGray(image.Rect(0, 0, s.w, s.n))
	return s, nil
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *stream) ColorModel() color.Model { return color.GrayModel }

// Opaque reports that a bitmap has no transparency.
func (s *stream) Opaque() bool { return true }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.band, s.text.r = nil, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: xbm: closed")
	}
	return nil
}

func (s *stream) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.y >= s.h {
		return nil, io.EOF
	}
	n := min(s.n, s.h-s.y)
	b := s.band
	for k := 0; k < n; k++ {
		row := b.Pix[k*b.Stride : k*b.Stride+s.w]
		for x := 0; x < s.w; x += 8 {
			v, err := s.text.value()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				s.err = err
				return nil, err
			}
			// The lowest bit is the leftmost pixel; a set bit is black.
			for i := range row[x:min(x+8, s.w)] {
				row[x+i] = (v>>i)&1 - 1
			}
		}
	}
	out := &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: image.Rect(0, s.y, s.w, s.y+n)}
	s.y += n
	return out, nil
}
