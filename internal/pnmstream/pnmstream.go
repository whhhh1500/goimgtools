// Package pnmstream reads and writes the Netpbm formats: PBM, PGM and PPM
// (P1 to P6, in text or binary), PAM (P7, with transparency) and PFM (PF
// and Pf, floating point), a band of rows at a time. Values of any maximum
// are scaled to 8 bits, or to 16 above 255, as Pillow scales them;
// written are PGM (grey) and PPM (colour), as Pillow writes them.
package pnmstream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/trig"
	"github.com/whhhh1500/goimgtools/pipeline"
)

var errFormat = errors.New("goimgtools: pnm: not a valid Netpbm file")

// Match reports whether head starts a Netpbm file.
func Match(head []byte) bool {
	if len(head) < 3 || head[0] != 'P' {
		return false
	}
	switch head[1] {
	case '1', '2', '3', '4', '5', '6', '7', 'f', 'F':
	default:
		return false
	}
	return isSpace(head[2])
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}

// header is what the start of the file says.
type header struct {
	magic  byte // '1' to '7', 'f' or 'F'
	w, h   int
	depth  int // channels: 1 grey, 2 grey and alpha, 3 RGB, 4 RGBA
	maxval int
	bits   bool  // a bitmap: 1 is black
	little bool  // PFM: little-endian
	data   int64 // where the pixels start
}

// scanner reads the tokens of a header.
type scanner struct {
	r   *bufio.Reader
	off int64
}

func (s *scanner) byte() (byte, error) {
	c, err := s.r.ReadByte()
	if err == nil {
		s.off++
	}
	return c, err
}

// token reads the next word, past white space and comments.
func (s *scanner) token() (string, error) {
	var b []byte
	for {
		c, err := s.byte()
		if err != nil {
			if err == io.EOF && len(b) > 0 {
				return string(b), nil
			}
			return "", errFormat
		}
		switch {
		case c == '#' && len(b) == 0:
			for c != '\n' && c != '\r' {
				if c, err = s.byte(); err != nil {
					return "", errFormat
				}
			}
		case isSpace(c):
			if len(b) > 0 {
				return string(b), nil
			}
		default:
			if len(b) >= 32 {
				return "", errFormat
			}
			b = append(b, c)
		}
	}
}

func (s *scanner) number(lo, hi int) (int, error) {
	t, err := s.token()
	if err != nil {
		return 0, err
	}
	v, err := strconv.Atoi(t)
	if err != nil || v < lo || v > hi {
		return 0, errFormat
	}
	return v, nil
}

const maxSide = 1 << 24

func readHeader(r io.ReaderAt, size int64) (*header, error) {
	s := &scanner{r: bufio.NewReaderSize(io.NewSectionReader(r, 0, size), 4096)}
	var m [2]byte
	if _, err := io.ReadFull(s.r, m[:]); err != nil || m[0] != 'P' {
		return nil, errFormat
	}
	s.off = 2
	h := &header{magic: m[1]}
	var err error
	switch h.magic {
	case '1', '2', '3', '4', '5', '6':
		if h.w, err = s.number(1, maxSide); err != nil {
			return nil, err
		}
		if h.h, err = s.number(1, maxSide); err != nil {
			return nil, err
		}
		h.depth = 1
		if h.magic == '3' || h.magic == '6' {
			h.depth = 3
		}
		if h.magic == '1' || h.magic == '4' {
			h.bits, h.maxval = true, 1
		} else if h.maxval, err = s.number(1, 65535); err != nil {
			return nil, err
		}
	case '7':
		var tupl string
		for {
			t, err := s.token()
			if err != nil {
				return nil, err
			}
			if t == "ENDHDR" {
				break
			}
			switch t {
			case "WIDTH":
				h.w, err = s.number(1, maxSide)
			case "HEIGHT":
				h.h, err = s.number(1, maxSide)
			case "DEPTH":
				h.depth, err = s.number(1, 4)
			case "MAXVAL":
				h.maxval, err = s.number(1, 65535)
			case "TUPLTYPE":
				tupl, err = s.token()
			default:
				err = errFormat
			}
			if err != nil {
				return nil, err
			}
		}
		if h.w == 0 || h.h == 0 || h.depth == 0 || h.maxval == 0 {
			return nil, errFormat
		}
		switch tupl {
		case "BLACKANDWHITE", "BLACKANDWHITE_ALPHA":
			// 0 is black, 1 white: as grey of maximum 1.
			if h.maxval != 1 {
				return nil, errFormat
			}
		}
		// The line ENDHDR ends: its newline is the last byte of the header.
		return h, h.finish(s)
	case 'f', 'F':
		if h.w, err = s.number(1, maxSide); err != nil {
			return nil, err
		}
		if h.h, err = s.number(1, maxSide); err != nil {
			return nil, err
		}
		t, err := s.token()
		if err != nil {
			return nil, err
		}
		scale, err := strconv.ParseFloat(t, 64)
		if err != nil || scale == 0 || math.IsInf(scale, 0) || math.IsNaN(scale) {
			return nil, errFormat
		}
		h.little = scale < 0
		h.depth = 1
		if h.magic == 'F' {
			h.depth = 3
		}
	default:
		return nil, errFormat
	}
	h.data = s.off
	return h, nil
}

// finish sets where the pixels start, past the newline that ends ENDHDR.
func (h *header) finish(s *scanner) error {
	h.data = s.off
	return nil
}

// plain reports whether the values are written as text.
func (h *header) plain() bool { return h.magic >= '1' && h.magic <= '3' }

// sampleBytes is the size of a stored value.
func (h *header) sampleBytes() int {
	switch {
	case h.magic == 'f' || h.magic == 'F':
		return 4
	case h.maxval > 255:
		return 2
	}
	return 1
}

// rowBytes is the size of a stored row of a binary file.
func (h *header) rowBytes() int64 {
	if h.bits && h.magic == '4' {
		return int64(h.w+7) / 8
	}
	return int64(h.w) * int64(h.depth) * int64(h.sampleBytes())
}

// wide reports whether the image comes out with 16 bits a value.
func (h *header) wide() bool {
	return h.maxval > 255 || h.magic == 'f' || h.magic == 'F'
}

// stream is a Netpbm image decoded a band of rows at a time.
type stream struct {
	r        io.ReaderAt
	env      *pipeline.Env
	h        *header
	text     *scanner // the values of a text file
	vals     []int    // a row of them
	band     image.Image
	raw      []byte
	scale    []uint16 // value to output, for a maximum other than 255 or 65535
	n, y     int
	reserved int64
	err      error
}

// Open reads the header of a Netpbm file of size bytes and returns its
// image, decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	h, err := readHeader(r, size)
	if err != nil {
		return nil, err
	}
	s := &stream{r: r, env: env, h: h}
	if h.plain() {
		s.text = &scanner{r: bufio.NewReaderSize(io.NewSectionReader(r, h.data, size-h.data), 32<<10)}
	} else if h.rowBytes() > (size-h.data)/int64(h.h) {
		return nil, io.ErrUnexpectedEOF
	}
	if !h.bits && h.maxval != 255 && h.maxval != 65535 && h.maxval > 0 {
		out := 255
		if h.maxval > 255 {
			out = 65535
		}
		s.scale = make([]uint16, h.maxval+1)
		for v := range s.scale {
			// Pillow's round(value / maxval * out), halves to even.
			s.scale[v] = uint16(min(float64(out), math.RoundToEven(float64(float64(v)/float64(h.maxval))*float64(out))))
		}
	}
	bpp := 1
	switch {
	case h.depth >= 2 || h.magic == '3' || h.magic == '6' || h.magic == 'F':
		bpp = 4
	}
	if h.wide() {
		bpp *= 2
	}
	s.n = pipeline.StripRows(int64(bpp)*int64(h.w)+h.rowBytes(), h.h)
	s.reserved = int64(s.n)*(int64(bpp)*int64(h.w)+h.rowBytes()) + 2*int64(len(s.scale))
	if h.plain() {
		s.reserved += 8 * int64(h.w) * int64(h.depth)
	}
	if err := env.Budget.Reserve(s.reserved, "pnm band"); err != nil {
		return nil, err
	}
	if h.plain() {
		s.vals = make([]int, 0, h.w*h.depth)
	}
	r0 := image.Rect(0, 0, h.w, s.n)
	switch {
	case bpp == 1:
		s.band = image.NewGray(r0)
	case bpp == 2:
		s.band = image.NewGray16(r0)
	case bpp == 4:
		s.band = image.NewNRGBA(r0)
	default:
		s.band = image.NewNRGBA64(r0)
	}
	if !h.plain() {
		s.raw = make([]byte, int64(s.n)*h.rowBytes())
	}
	return s, nil
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.h.w, s.h.h) }
func (s *stream) ColorModel() color.Model { return s.band.ColorModel() }

// Opaque reports whether the file has no alpha.
func (s *stream) Opaque() bool { return s.h.depth != 2 && s.h.depth != 4 }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.raw = nil
	if s.err == nil {
		s.err = errors.New("goimgtools: pnm: closed")
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
	switch b := s.band.(type) {
	case *image.Gray:
		return &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	case *image.Gray16:
		return &image.Gray16{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	case *image.NRGBA:
		return &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	case *image.NRGBA64:
		return &image.NRGBA64{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	}
	panic("unreachable")
}

// value maps a stored value to the output's range.
func (s *stream) value(v int) (int, error) {
	if v > s.h.maxval {
		if s.text == nil && s.scale != nil {
			return int(s.scale[s.h.maxval]), nil // binary: held at the top, as Pillow does
		}
		return 0, fmt.Errorf("goimgtools: pnm: a value of %d above the maximum %d", v, s.h.maxval)
	}
	if s.scale != nil {
		return int(s.scale[v]), nil
	}
	return v, nil
}

// decode reads n rows into the band.
func (s *stream) decode(n int) error {
	h := s.h
	w, depth := h.w, h.depth
	// get gives value c of pixel x of row k, in the output's range.
	var get func(k, x, c int) (int, error)
	switch {
	case h.plain():
		// The values in order; a bitmap's are 0 and 1, 1 black, and need
		// no space between them.
		vals := s.vals
		get = func(k, x, c int) (int, error) {
			if x == 0 && c == 0 {
				vals = vals[:0]
				for i := 0; i < w*depth; i++ {
					var v int
					if h.bits {
						for {
							b, err := s.text.byte()
							if err != nil {
								return 0, io.ErrUnexpectedEOF
							}
							if b == '#' {
								for b != '\n' && b != '\r' {
									if b, err = s.text.byte(); err != nil {
										return 0, io.ErrUnexpectedEOF
									}
								}
								continue
							}
							if b == '0' || b == '1' {
								v = int(b-'0') ^ 1
								break
							}
							if !isSpace(b) {
								return 0, errFormat
							}
						}
					} else {
						t, err := s.text.token()
						if err != nil {
							return 0, io.ErrUnexpectedEOF
						}
						u, err := strconv.Atoi(t)
						if err != nil || u < 0 {
							return 0, errFormat
						}
						if v, err = s.value(u); err != nil {
							return 0, err
						}
					}
					vals = append(vals, v)
				}
			}
			return vals[x*depth+c], nil
		}
	default:
		rb := h.rowBytes()
		raw := s.raw[:int64(n)*rb]
		off := h.data + int64(s.y)*rb
		if h.magic == 'f' || h.magic == 'F' {
			// PFM rows go from the bottom up.
			off = h.data + int64(h.h-s.y-n)*rb
		}
		if _, err := s.r.ReadAt(raw, off); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		if h.maxval == 255 {
			// A byte a value, taken as it is: most files there are.
			s.direct(raw, n)
			return nil
		}
		switch {
		case h.magic == '4':
			get = func(k, x, c int) (int, error) {
				return int(raw[int64(k)*rb+int64(x>>3)]>>(7-x&7)&1) ^ 1, nil
			}
		case h.magic == 'f' || h.magic == 'F':
			order := binary.ByteOrder(binary.BigEndian)
			if h.little {
				order = binary.LittleEndian
			}
			get = func(k, x, c int) (int, error) {
				i := int64(n-1-k)*rb + int64(x*depth+c)*4
				return int(encode16(float64(math.Float32frombits(order.Uint32(raw[i:]))))), nil
			}
		case h.maxval > 255:
			get = func(k, x, c int) (int, error) {
				return s.value(int(binary.BigEndian.Uint16(raw[int64(k)*rb+int64(x*depth+c)*2:])))
			}
		default:
			get = func(k, x, c int) (int, error) {
				return s.value(int(raw[int64(k)*rb+int64(x*depth+c)]))
			}
		}
	}
	for k := 0; k < n; k++ {
		for x := 0; x < w; x++ {
			var px [4]int
			px[3] = -1
			for c := 0; c < depth; c++ {
				v, err := get(k, x, c)
				if err != nil {
					return err
				}
				px[c] = v
			}
			switch depth {
			case 1:
				px[1], px[2] = px[0], px[0]
			case 2:
				px[3], px[1], px[2] = px[1], px[0], px[0]
			}
			if h.bits {
				// 0 and 1 to black and white.
				for c := 0; c < 3; c++ {
					px[c] *= 255
				}
				if px[3] >= 0 {
					px[3] *= 255
				}
			}
			switch b := s.band.(type) {
			case *image.Gray:
				b.Pix[k*b.Stride+x] = uint8(px[0])
			case *image.Gray16:
				binary.BigEndian.PutUint16(b.Pix[k*b.Stride+2*x:], uint16(px[0]))
			case *image.NRGBA:
				if px[3] < 0 {
					px[3] = 255
				}
				p := b.Pix[k*b.Stride+4*x:]
				p[0], p[1], p[2], p[3] = uint8(px[0]), uint8(px[1]), uint8(px[2]), uint8(px[3])
			case *image.NRGBA64:
				if px[3] < 0 {
					px[3] = 65535
				}
				p := b.Pix[k*b.Stride+8*x:]
				for c := 0; c < 4; c++ {
					binary.BigEndian.PutUint16(p[2*c:], uint16(px[c]))
				}
			}
		}
	}
	return nil
}

// direct fills n rows of the band from the rows of a binary file of
// maximum 255, in raw: its bytes are the values, grey to grey, and grey
// with alpha, RGB or RGBA to NRGBA.
func (s *stream) direct(raw []byte, n int) {
	w, depth := s.h.w, s.h.depth
	switch b := s.band.(type) {
	case *image.Gray:
		for k := 0; k < n; k++ {
			copy(b.Pix[k*b.Stride:][:w], raw[k*w:])
		}
	case *image.NRGBA:
		for k := 0; k < n; k++ {
			src, dst := raw[k*w*depth:][:w*depth], b.Pix[k*b.Stride:][:4*w]
			switch depth {
			case 2:
				for x := 0; x < w; x++ {
					g, d := src[2*x], dst[4*x:4*x+4:4*x+4]
					d[0], d[1], d[2], d[3] = g, g, g, src[2*x+1]
				}
			case 3:
				for x := 0; x < w; x++ {
					p, d := src[3*x:3*x+3:3*x+3], dst[4*x:4*x+4:4*x+4]
					d[0], d[1], d[2], d[3] = p[0], p[1], p[2], 255
				}
			default:
				copy(dst, src)
			}
		}
	}
}

// encode16 is a linear light, 0 to 1 (PFM stores radiance), in the sRGB
// curve on 16 bits.
func encode16(v float64) uint16 {
	if !(v > 0) {
		return 0
	}
	v = min(v, 1)
	if v <= 0.0031308 {
		v *= 12.92
	} else {
		v = float64(1.055*trig.Pow(v, 1/2.4)) - 0.055 // the same on every processor
	}
	return uint16(math.Round(float64(v * 65535)))
}

// noClose keeps the caller's source open.
type noClose struct{ pipeline.Source }

func (noClose) Close() error { return nil }

// Encode writes src as Pillow writes it: grey as PGM (P5), anything else as
// PPM (P6), with 16 bits a value when src has them, its alpha left out.
// src is not closed.
func Encode(w io.Writer, src pipeline.Source) error {
	b := src.Bounds()
	if b.Empty() {
		return errors.New("goimgtools: pnm: an empty image")
	}
	m := src.ColorModel()
	grey := m == color.GrayModel || m == color.Gray16Model
	wide := m == color.Gray16Model || m == color.NRGBA64Model || m == color.RGBA64Model
	bw := bufio.NewWriterSize(w, 64<<10)
	head, maxval := "P6", 255
	if grey {
		head = "P5"
	}
	if wide {
		maxval = 65535
	}
	fmt.Fprintf(bw, "%s\n%d %d\n%d\n", head, b.Dx(), b.Dy(), maxval)
	bpp := 1
	if !grey {
		bpp = 3
	}
	if wide {
		bpp *= 2
	}
	env := pipeline.EnvOf(src)
	if err := env.Budget.Reserve(int64(bpp)*int64(b.Dx()), "pnm row"); err != nil {
		return err
	}
	defer env.Budget.Release(int64(bpp) * int64(b.Dx()))
	row := make([]byte, bpp*b.Dx())
	if !grey && !wide {
		// Colour on 8 bits as every other encoder takes it.
		rows := pipeline.Map(noClose{src}, func(*image.NRGBA) error { return nil })
		defer rows.Close()
		src = rows
	}
	y := b.Min.Y
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		r := s.Bounds()
		for yy := r.Min.Y; yy < r.Max.Y; yy++ {
			switch p := s.(type) {
			case *image.Gray:
				if grey && !wide {
					copy(row, p.Pix[p.PixOffset(r.Min.X, yy):][:r.Dx()])
					break
				}
				fill(row, s, r.Min.X, yy, grey, wide)
			case *image.Gray16:
				if grey && wide {
					copy(row, p.Pix[p.PixOffset(r.Min.X, yy):][:2*r.Dx()])
					break
				}
				fill(row, s, r.Min.X, yy, grey, wide)
			case *image.NRGBA:
				if !grey && !wide {
					px := p.Pix[p.PixOffset(r.Min.X, yy):]
					for x := 0; x < r.Dx(); x++ {
						copy(row[3*x:3*x+3], px[4*x:4*x+3])
					}
					break
				}
				fill(row, s, r.Min.X, yy, grey, wide)
			default:
				fill(row, s, r.Min.X, yy, grey, wide)
			}
			if _, err := bw.Write(row); err != nil {
				return err
			}
			y++
		}
	}
	if y != b.Max.Y {
		return io.ErrUnexpectedEOF
	}
	return bw.Flush()
}

// fill writes row y of m from column x0 into row, as stored.
func fill(row []byte, m image.Image, x0, y int, grey, wide bool) {
	n := len(row)
	if !grey {
		n /= 3
	}
	if wide {
		n /= 2
	}
	for x := 0; x < n; x++ {
		c := m.At(x0+x, y)
		switch {
		case grey && wide:
			binary.BigEndian.PutUint16(row[2*x:], color.Gray16Model.Convert(c).(color.Gray16).Y)
		case grey:
			row[x] = color.GrayModel.Convert(c).(color.Gray).Y
		case wide:
			p := color.NRGBA64Model.Convert(c).(color.NRGBA64)
			binary.BigEndian.PutUint16(row[6*x:], p.R)
			binary.BigEndian.PutUint16(row[6*x+2:], p.G)
			binary.BigEndian.PutUint16(row[6*x+4:], p.B)
		default:
			p := color.NRGBAModel.Convert(c).(color.NRGBA)
			row[3*x], row[3*x+1], row[3*x+2] = p.R, p.G, p.B
		}
	}
}
