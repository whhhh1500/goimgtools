// Package xpmstream reads XPM version 3, the pixmap of X11 written as C
// source: an array of strings, the first the width, the height, the number
// of colours and the characters a pixel takes (1 to 4), then a string for
// every colour and one for every row. An image of at most 256 colours
// comes out with their palette, a larger one as colour; the colour None is
// transparent. The text is read as the rows are asked for, a band of rows
// at a time, and only the table of colours is kept.
package xpmstream

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/csscolor"
	"github.com/whhhh1500/goimgtools/pipeline"
)

var errFormat = errors.New("goimgtools: xpm: not a valid XPM file")

const (
	// maxSide is the most pixels read across or down, as for Netpbm.
	maxSide    = 1 << 24
	maxColours = 65536
	maxChars   = 4
	// maxLine is the longest the string of the sizes or of a colour is.
	maxLine = 4096
	bufSize = 32 << 10
	magic   = "/* XPM */"
)

// Match reports whether head starts an XPM file of version 3: the comment
// "/* XPM */".
func Match(head []byte) bool { return strings.HasPrefix(string(head), magic) }

// scanner reads the text and knows how far it has read.
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

// open steps to the next string, past its opening quote: over the
// comments, the commas and the declaration between the strings. The brace
// that closes the array ends the file.
func (s *scanner) open() error {
	for {
		c, err := s.get()
		if err != nil {
			return err
		}
		switch c {
		case '"':
			return nil
		case '}':
			return io.ErrUnexpectedEOF
		case '/':
			if c, err = s.get(); err != nil {
				return err
			}
			switch c {
			case '"':
				return nil
			case '*':
				for last := byte(0); ; last = c {
					if c, err = s.get(); err != nil {
						return err
					}
					if last == '*' && c == '/' {
						break
					}
				}
			}
		}
	}
}

// line reads the next string, of the header: one of at most maxLine bytes
// on a line.
func (s *scanner) line(buf []byte) ([]byte, error) {
	if err := s.open(); err != nil {
		return nil, err
	}
	buf = buf[:0]
	for {
		c, err := s.get()
		switch {
		case err != nil:
			return nil, err
		case c == '"':
			return buf, nil
		case c == '\n' || len(buf) == maxLine:
			return nil, errFormat
		}
		buf = append(buf, c)
	}
}

// table finds the number of a colour from the characters of a pixel.
type table struct {
	keys  []uint32
	vals  []int32 // the number of the colour and one; 0 where there is none
	shift uint
}

// tableSlots is the size of the table of n colours: a power of two, at
// most half of it used.
func tableSlots(n int) (slots int, shift uint) {
	bits := uint(4)
	for 1<<bits < 2*n {
		bits++
	}
	return 1 << bits, 32 - bits
}

// slot is where key is, or where it goes.
func (t *table) slot(key uint32) int {
	i := int(key * 0x9e3779b1 >> t.shift)
	for t.vals[i] != 0 && t.keys[i] != key {
		i = (i + 1) & (len(t.vals) - 1)
	}
	return i
}

// stream is an XPM image decoded a band of rows at a time.
type stream struct {
	text     scanner
	env      *pipeline.Env
	w, h     int
	chars    int // a pixel takes
	table    table
	colours  []color.NRGBA // of more than 256
	palette  color.Palette // of 256 at most
	opaque   bool
	band     image.Image
	row      []byte
	lastKey  uint32
	lastVal  int32
	n, y     int
	reserved int64
	err      error
}

// Open reads the sizes and the colours of an XPM file of size bytes and
// returns its image, decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	s := &stream{env: env, opaque: true}
	if err := s.reserve(bufSize+maxLine, "xpm text"); err != nil {
		return nil, err
	}
	s.text.r = bufio.NewReaderSize(io.NewSectionReader(r, 0, size), bufSize)
	if err := s.header(size); err != nil {
		s.Close()
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return s, nil
}

// reserve takes n more bytes of the budget.
func (s *stream) reserve(n int64, what string) error {
	if err := s.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	s.reserved += n
	return nil
}

// header reads up to the first row.
func (s *stream) header(size int64) error {
	var m [len(magic)]byte
	if _, err := io.ReadFull(s.text.r, m[:]); err != nil || string(m[:]) != magic {
		return errFormat
	}
	s.text.off = int64(len(m))
	buf := make([]byte, 0, maxLine)
	line, err := s.text.line(buf)
	if err != nil {
		return err
	}
	// The width, the height, the number of colours and the characters a
	// pixel takes; a hot spot and the word XPMEXT may follow.
	f := strings.Fields(string(line))
	if len(f) < 4 {
		return errFormat
	}
	var v [4]int
	for i, most := range [4]int{maxSide, maxSide, maxColours, maxChars} {
		if v[i], err = strconv.Atoi(f[i]); err != nil || v[i] < 1 || v[i] > most {
			return errFormat
		}
	}
	var colours int
	s.w, s.h, colours, s.chars = v[0], v[1], v[2], v[3]
	// A colour takes its characters, a key and a value in quotes, a row
	// its pixels in quotes: a file too short for them is not read further.
	rowText := int64(s.w) * int64(s.chars)
	if int64(colours)*int64(s.chars+5)+int64(s.h)*(rowText+2) > size-s.text.off {
		return io.ErrUnexpectedEOF
	}
	slots, shift := tableSlots(colours)
	if err := s.reserve(8*int64(slots), "xpm colours"); err != nil {
		return err
	}
	s.table = table{keys: make([]uint32, slots), vals: make([]int32, slots), shift: shift}
	if colours <= 256 {
		s.palette = make(color.Palette, colours)
	} else {
		if err := s.reserve(4*int64(colours), "xpm colours"); err != nil {
			return err
		}
		s.colours = make([]color.NRGBA, colours)
	}
	for i := 0; i < colours; i++ {
		if line, err = s.text.line(buf); err != nil {
			return err
		}
		if len(line) < s.chars {
			return errFormat
		}
		c, err := colourLine(string(line[s.chars:]))
		if err != nil {
			return err
		}
		if c.A != 255 {
			s.opaque = false
		}
		if s.palette != nil {
			s.palette[i] = c
		} else {
			s.colours[i] = c
		}
		// Of two colours with the same characters the first counts.
		key := keyOf(line[:s.chars])
		if at := s.table.slot(key); s.table.vals[at] == 0 {
			s.table.keys[at], s.table.vals[at] = key, int32(i+1)
		}
	}
	out := int64(4)
	if s.palette != nil {
		out = 1
	}
	s.n = pipeline.StripRows(int64(s.w)*out+rowText, s.h)
	if err := s.reserve(int64(s.n)*int64(s.w)*out+rowText, "xpm band"); err != nil {
		return err
	}
	r0 := image.Rect(0, 0, s.w, s.n)
	if s.palette != nil {
		s.band = image.NewPaletted(r0, s.palette)
	} else {
		s.band = image.NewNRGBA(r0)
	}
	s.row = make([]byte, rowText)
	return nil
}

// keyOf is the characters of a pixel as a number.
func keyOf(b []byte) uint32 {
	var key uint32
	for _, c := range b {
		key = key<<8 | uint32(c)
	}
	return key
}

// colourLine reads what follows the characters in the string of a colour:
// pairs of a key and a value, the keys c (colour), g (grey), g4 (four
// greys), m (black and white) and s (a name for the colour, not used).
// The value of c counts, without one that of g, g4 or m.
func colourLine(rest string) (color.NRGBA, error) {
	var vals [5]string
	cur := -1
	for _, f := range strings.Fields(rest) {
		k := -1
		switch f {
		case "c":
			k = 0
		case "g":
			k = 1
		case "g4":
			k = 2
		case "m":
			k = 3
		case "s":
			k = 4
		}
		switch {
		case k >= 0 && (cur < 0 || vals[cur] != ""):
			// A key; a value may be of several words ("light blue").
			cur = k
			vals[k] = ""
		case cur < 0:
			return color.NRGBA{}, errFormat
		case vals[cur] == "":
			vals[cur] = f
		default:
			vals[cur] += " " + f
		}
	}
	for _, v := range vals[:4] {
		if v != "" {
			return colourOf(v)
		}
	}
	return color.NRGBA{}, errFormat
}

// x11 are the names X11 gives another colour than CSS does.
var x11 = map[string]color.NRGBA{
	"gray":   {190, 190, 190, 255},
	"grey":   {190, 190, 190, 255},
	"green":  {0, 255, 0, 255},
	"maroon": {176, 48, 96, 255},
	"purple": {160, 32, 240, 255},
}

// colourOf reads a colour: None, #rgb, #rrggbb, #rrrgggbbb or
// #rrrrggggbbbb (the digits given are the high ones, as X11 has it: #fff is
// 240, 240, 240; of the longer ones the high eight bits count), or a name
// of X11: those CSS has too, in capitals or not, with spaces or not, and
// gray0 to gray100.
func colourOf(v string) (color.NRGBA, error) {
	unknown := func() (color.NRGBA, error) {
		return color.NRGBA{}, fmt.Errorf("goimgtools: xpm: unknown colour %.40q", v)
	}
	if strings.EqualFold(v, "none") {
		return color.NRGBA{}, nil
	}
	if v == "" {
		return unknown()
	}
	if v[0] == '#' {
		n := (len(v) - 1) / 3
		if len(v) != 1+3*n || n < 1 || n > 4 {
			return unknown()
		}
		c := color.NRGBA{A: 255}
		for i, p := range []*uint8{&c.R, &c.G, &c.B} {
			x, err := strconv.ParseUint(v[1+i*n:1+(i+1)*n], 16, 16)
			if err != nil {
				return unknown()
			}
			switch n {
			case 1:
				x <<= 4 // as X11 reads it (and ffmpeg): the digit is the high one
			case 3:
				x >>= 4
			case 4:
				x >>= 8
			}
			*p = uint8(x)
		}
		return c, nil
	}
	name := strings.ToLower(strings.ReplaceAll(v, " ", ""))
	for i := 0; i < len(name); i++ {
		if c := name[i]; (c < 'a' || c > 'z') && (c < '0' || c > '9') {
			return unknown()
		}
	}
	if c, ok := x11[name]; ok {
		return c, nil
	}
	if strings.HasPrefix(name, "gray") || strings.HasPrefix(name, "grey") {
		if n, err := strconv.Atoi(name[4:]); err == nil && n <= 100 {
			// The greys of X11's list: 255 n / 100 rounded, the half
			// up but for gray50 and gray90.
			g := uint8((n*255 + 50) / 100)
			if n == 50 || n == 90 {
				g--
			}
			return color.NRGBA{g, g, g, 255}, nil
		}
	}
	if c, ok := csscolor.Parse(name); ok {
		return c, nil
	}
	return unknown()
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *stream) ColorModel() color.Model {
	if s.palette != nil {
		return s.palette
	}
	return color.NRGBAModel
}

// Opaque reports whether no colour of the file is None.
func (s *stream) Opaque() bool { return s.opaque }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.band, s.row, s.colours, s.table, s.text.r = nil, nil, nil, table{}, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: xpm: closed")
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
	for k := 0; k < n; k++ {
		if err := s.readRow(k); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			s.err = err
			return nil, err
		}
	}
	rect := image.Rect(0, s.y, s.w, s.y+n)
	s.y += n
	if b, ok := s.band.(*image.Paletted); ok {
		return &image.Paletted{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect, Palette: b.Palette}, nil
	}
	b := s.band.(*image.NRGBA)
	return &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
}

// readRow reads the next string into row k of the band. Characters that
// are no colour of the table, as those a string too short brings in, are
// an error; what a string has past the width is not looked at.
func (s *stream) readRow(k int) error {
	if err := s.text.open(); err != nil {
		return err
	}
	if _, err := io.ReadFull(s.text.r, s.row); err != nil {
		return io.ErrUnexpectedEOF
	}
	s.text.off += int64(len(s.row))
	var pal []uint8
	var rgba []uint8
	switch b := s.band.(type) {
	case *image.Paletted:
		pal = b.Pix[k*b.Stride : k*b.Stride+s.w]
	case *image.NRGBA:
		rgba = b.Pix[k*b.Stride : k*b.Stride+4*s.w]
	}
	chars := s.chars
	for x := 0; x < s.w; x++ {
		key := keyOf(s.row[x*chars : (x+1)*chars])
		if key != s.lastKey || s.lastVal == 0 {
			v := s.table.vals[s.table.slot(key)]
			if v == 0 {
				return errFormat
			}
			s.lastKey, s.lastVal = key, v
		}
		if pal != nil {
			pal[x] = uint8(s.lastVal - 1)
		} else {
			c := s.colours[s.lastVal-1]
			d := rgba[4*x : 4*x+4]
			d[0], d[1], d[2], d[3] = c.R, c.G, c.B, c.A
		}
	}
	for {
		c, err := s.text.get()
		if err != nil {
			return err
		}
		if c == '"' {
			return nil
		}
	}
}
