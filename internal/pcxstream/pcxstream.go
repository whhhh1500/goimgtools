// Package pcxstream reads PCX, the format of ZSoft's PC Paintbrush: two
// colours, the 4, 8 or 16 colours of the palette in the header (as bit
// planes or packed), 256 greys or colours with the palette at the end of
// the file, and true colour as three or four planes of eight bits,
// run-length compressed or not, a band of rows at a time.
package pcxstream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var (
	errFormat      = errors.New("goimgtools: pcx: not a valid PCX file")
	errUnsupported = errors.New("goimgtools: pcx: unsupported bits and planes")
)

const (
	headerSize = 128
	// tailSize is the palette of 256 colours at the end of the file: the
	// byte 12, then the colours.
	tailSize = 769
	rleBuf   = 32 << 10
)

// header is what the 128 bytes a PCX file starts with say of the image.
type header struct {
	version  int
	packed   bool // run-length compressed
	bits     int  // of a pixel in a plane
	planes   int
	w, h     int
	rowBytes int // of a plane of a row, as stored
}

// sniff reads the part of the header that lies in the first 12 bytes.
func sniff(b []byte) (header, bool) {
	if len(b) < 12 || b[0] != 10 || b[1] > 5 || b[1] == 1 || b[2] > 1 {
		return header{}, false
	}
	h := header{version: int(b[1]), packed: b[2] == 1, bits: int(b[3])}
	if h.bits != 1 && h.bits != 2 && h.bits != 4 && h.bits != 8 {
		return h, false
	}
	x0, y0 := int(binary.LittleEndian.Uint16(b[4:])), int(binary.LittleEndian.Uint16(b[6:]))
	x1, y1 := int(binary.LittleEndian.Uint16(b[8:])), int(binary.LittleEndian.Uint16(b[10:]))
	if x1 < x0 || y1 < y0 {
		return h, false
	}
	h.w, h.h = x1-x0+1, y1-y0+1
	return h, true
}

// Match reports whether head starts what can be a PCX file: the byte 10,
// a version of 0, 2, 3, 4 or 5, an encoding of 0 or 1, 1, 2, 4 or 8 bits
// and a window that is not empty.
func Match(head []byte) bool {
	_, ok := sniff(head)
	return ok
}

// The kinds of image a file holds.
const (
	kindMono    = iota // 1 bit, 1 plane: black and white
	kindPlanes         // 1 bit, 2 to 4 planes: the palette of the header
	kindPacked         // 2 or 4 bits, 1 plane: the palette of the header
	kindGrey           // 8 bits, 1 plane, no palette or a grey one
	kindPalette        // 8 bits, 1 plane, the palette at the end
	kindColour         // 8 bits, 3 or 4 planes
)

// stream is a PCX image decoded a band of rows at a time.
type stream struct {
	r        io.ReaderAt
	env      *pipeline.Env
	h        header
	kind     int
	data     int64 // where the rows of an uncompressed file start
	palette  color.Palette
	band     image.Image
	raw      []byte
	n, y     int
	reserved int64
	rle      *bufio.Reader
	count    int // bytes left of the run
	value    byte
	err      error
}

// Open reads the header of a PCX file of size bytes and returns its image,
// decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	var hb [headerSize]byte
	if readAt(r, hb[:], 0) != nil {
		return nil, errFormat
	}
	h, ok := sniff(hb[:])
	if !ok {
		return nil, errFormat
	}
	h.planes = int(hb[65])
	s := &stream{r: r, env: env, h: h, data: headerSize}
	end := size
	switch {
	case h.bits == 1 && h.planes == 1:
		s.kind = kindMono
	case h.bits == 1 && h.planes >= 2 && h.planes <= 4:
		s.kind = kindPlanes
		s.palette = headerPalette(hb[16:64], 1<<h.planes)
	case (h.bits == 2 || h.bits == 4) && h.planes == 1:
		s.kind = kindPacked
		s.palette = headerPalette(hb[16:64], 1<<h.bits)
	case h.bits == 8 && h.planes == 1:
		s.kind = kindGrey
		// As Pillow reads it: the palette is the last 769 bytes when
		// they start with 12, and one that is the greys in order makes
		// a grey image.
		var tail [tailSize]byte
		if size >= headerSize+tailSize {
			if readAt(r, tail[:], size-tailSize) == nil && tail[0] == 12 {
				end = size - tailSize
				for i := 0; i < 256; i++ {
					if c := tail[1+3*i:]; c[0] != byte(i) || c[1] != byte(i) || c[2] != byte(i) {
						s.kind = kindPalette
						break
					}
				}
			}
		}
		if s.kind == kindPalette {
			s.palette = make(color.Palette, 256)
			for i := range s.palette {
				s.palette[i] = color.NRGBA{tail[1+3*i], tail[2+3*i], tail[3+3*i], 255}
			}
		}
	case h.bits == 8 && (h.planes == 3 || h.planes == 4):
		s.kind = kindColour
	default:
		return nil, errUnsupported
	}
	// The bytes of a plane of a row: the header's, unless that is too few
	// for the width; then, as Pillow, what the width needs, made even.
	need := (h.w*h.bits + 7) / 8
	s.h.rowBytes = int(binary.LittleEndian.Uint16(hb[66:]))
	if s.h.rowBytes < need {
		s.h.rowBytes = need + need%2
	}
	rowBytes := int64(s.h.rowBytes) * int64(h.planes)
	if !h.packed && rowBytes > (end-s.data)/int64(h.h) {
		return nil, io.ErrUnexpectedEOF
	}
	out := int64(1)
	if s.kind == kindColour {
		out = 4
	}
	s.n = pipeline.StripRows(int64(h.w)*out+rowBytes, h.h)
	s.reserved = int64(s.n) * (int64(h.w)*out + rowBytes)
	if h.packed {
		s.reserved += rleBuf
	}
	if err := env.Budget.Reserve(s.reserved, "pcx band"); err != nil {
		return nil, err
	}
	r0 := image.Rect(0, 0, h.w, s.n)
	switch {
	case s.palette != nil:
		s.band = image.NewPaletted(r0, s.palette)
	case s.kind == kindColour:
		s.band = image.NewNRGBA(r0)
	default:
		s.band = image.NewGray(r0)
	}
	s.raw = make([]byte, int64(s.n)*rowBytes)
	if h.packed {
		s.rle = bufio.NewReaderSize(io.NewSectionReader(r, s.data, end-s.data), rleBuf)
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

// standard are the 16 colours of the EGA, for a file that has none.
var standard = [16][3]uint8{
	{0, 0, 0}, {0, 0, 170}, {0, 170, 0}, {0, 170, 170}, {170, 0, 0}, {170, 0, 170}, {170, 85, 0}, {170, 170, 170},
	{85, 85, 85}, {85, 85, 255}, {85, 255, 85}, {85, 255, 255}, {255, 85, 85}, {255, 85, 255}, {255, 255, 85}, {255, 255, 255},
}

// headerPalette is the first n of the 16 colours in the header. A header
// of nothing but zeros there (a file written without palette information)
// would make every pixel black: it gives the standard colours instead, of
// 4 the black, cyan, magenta and white of the CGA.
func headerPalette(b []byte, n int) color.Palette {
	empty := true
	for _, v := range b {
		if v != 0 {
			empty = false
			break
		}
	}
	pal := make(color.Palette, n)
	for i := range pal {
		c := [3]uint8{b[3*i], b[3*i+1], b[3*i+2]}
		if empty {
			c = standard[i]
			if n == 4 {
				c = standard[[4]int{0, 11, 13, 15}[i]]
			}
		}
		pal[i] = color.NRGBA{c[0], c[1], c[2], 255}
	}
	return pal
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.h.w, s.h.h) }
func (s *stream) ColorModel() color.Model {
	if p, ok := s.band.(*image.Paletted); ok {
		return p.Palette
	}
	return s.band.ColorModel()
}

// Opaque reports whether the image has no transparency: all but four
// planes of eight bits, whose fourth is alpha.
func (s *stream) Opaque() bool { return s.kind != kindColour || s.h.planes == 3 }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.raw = nil
	if s.err == nil {
		s.err = errors.New("goimgtools: pcx: closed")
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
	rb := s.h.rowBytes * s.h.planes
	raw := s.raw[:n*rb]
	var err error
	if s.rle != nil {
		err = s.unpack(raw)
	} else {
		err = readAt(s.r, raw, s.data+int64(s.y)*int64(rb))
	}
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		s.err = err
		return nil, err
	}
	out := s.compose(n)
	s.y += n
	return out, nil
}

// unpack fills rows from the run-length packets: a byte of 192 or more is
// a count of up to 63 in its low bits and the byte after it the value, any
// other byte is itself. A run may go on from one row into the next (the
// format says it should not; files that do it are read all the same).
func (s *stream) unpack(raw []byte) error {
	for i := 0; i < len(raw); {
		if s.count == 0 {
			b, err := s.rle.ReadByte()
			if err != nil {
				return err
			}
			if b < 0xc0 {
				raw[i] = b
				i++
				continue
			}
			s.count = int(b & 0x3f)
			if s.value, err = s.rle.ReadByte(); err != nil {
				return err
			}
		}
		k := min(s.count, len(raw)-i)
		run := raw[i : i+k]
		for j := range run {
			run[j] = s.value
		}
		i += k
		s.count -= k
	}
	return nil
}

// compose turns n stored rows into the band.
func (s *stream) compose(n int) image.Image {
	w, bpl := s.h.w, s.h.rowBytes
	rb := bpl * s.h.planes
	rect := image.Rect(0, s.y, w, s.y+n)
	for k := 0; k < n; k++ {
		src := s.raw[k*rb : (k+1)*rb]
		switch b := s.band.(type) {
		case *image.Gray:
			dst := b.Pix[k*b.Stride : k*b.Stride+w]
			if s.kind == kindGrey {
				copy(dst, src)
				break
			}
			// As Pillow reads it: a set bit is white.
			for x := range dst {
				dst[x] = -(src[x>>3] >> (7 - x&7) & 1)
			}
		case *image.Paletted:
			dst := b.Pix[k*b.Stride : k*b.Stride+w]
			switch {
			case s.kind == kindPalette:
				copy(dst, src)
			case s.kind == kindPlanes:
				// The first plane is the lowest bit of the colour's number.
				for x := range dst {
					var v uint8
					for p := 0; p < s.h.planes; p++ {
						v |= (src[p*bpl+x>>3] >> (7 - x&7) & 1) << p
					}
					dst[x] = v
				}
			case s.h.bits == 2:
				for x := range dst {
					dst[x] = src[x>>2] >> (6 - 2*(x&3)) & 3
				}
			default:
				for x := range dst {
					dst[x] = src[x>>1] >> (4 - 4*(x&1)) & 15
				}
			}
		case *image.NRGBA:
			dst := b.Pix[k*b.Stride : k*b.Stride+4*w]
			for x := 0; x < w; x++ {
				d := dst[4*x : 4*x+4]
				d[0], d[1], d[2], d[3] = src[x], src[bpl+x], src[2*bpl+x], 255
				if s.h.planes == 4 {
					d[3] = src[3*bpl+x]
				}
			}
		}
	}
	switch b := s.band.(type) {
	case *image.Paletted:
		return &image.Paletted{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect, Palette: b.Palette}
	case *image.Gray:
		return &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	}
	b := s.band.(*image.NRGBA)
	return &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
}
