// Package qoistream reads and writes QOI, the "Quite OK Image" format
// (qoiformat.org): lossless RGB or RGBA, eight bits, as fast to code as a
// row of pixels is to copy. The image is read and written a band of rows
// at a time; writing is the reference encoder's, so its files are those of
// qoi.h and of Pillow.
package qoistream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

const (
	opIndex = 0x00
	opDiff  = 0x40
	opLuma  = 0x80
	opRun   = 0xc0
	opRGB   = 0xfe
	opRGBA  = 0xff
	// maxPixels is the reference's bound on the pixels of a file.
	maxPixels = 400_000_000
)

var errFormat = errors.New("goimgtools: qoi: not a valid QOI file")

// Match reports whether head starts a QOI file.
func Match(head []byte) bool { return len(head) >= 4 && string(head[:4]) == "qoif" }

func hash(p [4]uint8) int {
	return (int(p[0])*3 + int(p[1])*5 + int(p[2])*7 + int(p[3])*11) % 64
}

// stream is a QOI image decoded a band of rows at a time.
type stream struct {
	r        *bufio.Reader
	env      *pipeline.Env
	w, h     int
	channels int
	index    [64][4]uint8
	px       [4]uint8
	run      int
	band     *image.NRGBA
	n, y     int
	reserved int64
	err      error
}

// Open reads the header of a QOI file from r and returns its image,
// decoded as it is read.
func Open(r io.Reader, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	br, ok := r.(*bufio.Reader)
	if !ok {
		br = bufio.NewReader(r)
	}
	var h [14]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	s := &stream{r: br, env: env, w: int(binary.BigEndian.Uint32(h[4:])), h: int(binary.BigEndian.Uint32(h[8:])), channels: int(h[12]), px: [4]uint8{0, 0, 0, 255}}
	if !Match(h[:]) || s.w <= 0 || s.h <= 0 || s.channels != 3 && s.channels != 4 || h[13] > 1 || int64(s.w)*int64(s.h) > maxPixels {
		return nil, errFormat
	}
	s.n = pipeline.StripRows(4*int64(s.w), s.h)
	s.reserved = 4 * int64(s.w) * int64(s.n)
	if err := env.Budget.Reserve(s.reserved, "qoi band"); err != nil {
		return nil, err
	}
	s.band = image.NewNRGBA(image.Rect(0, 0, s.w, s.n))
	return s, nil
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *stream) ColorModel() color.Model { return color.NRGBAModel }

// Opaque reports whether the file stores no alpha.
func (s *stream) Opaque() bool { return s.channels == 3 }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	if s.err == nil {
		s.err = errors.New("goimgtools: qoi: closed")
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
		row := b.Pix[k*b.Stride : k*b.Stride+4*s.w]
		for x := 0; x < len(row); x += 4 {
			if s.run > 0 {
				s.run--
			} else if err := s.chunk(); err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				s.err = err
				return nil, err
			}
			copy(row[x:x+4], s.px[:])
		}
	}
	out := &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: image.Rect(0, s.y, s.w, s.y+n)}
	s.y += n
	return out, nil
}

// chunk reads the next chunk into px.
func (s *stream) chunk() error {
	b1, err := s.r.ReadByte()
	if err != nil {
		return err
	}
	p := &s.px
	switch {
	case b1 == opRGB:
		var c [3]byte
		if _, err := io.ReadFull(s.r, c[:]); err != nil {
			return err
		}
		p[0], p[1], p[2] = c[0], c[1], c[2]
	case b1 == opRGBA:
		var c [4]byte
		if _, err := io.ReadFull(s.r, c[:]); err != nil {
			return err
		}
		*p = c
	case b1&0xc0 == opIndex:
		*p = s.index[b1]
	case b1&0xc0 == opDiff:
		p[0] += (b1>>4)&3 - 2
		p[1] += (b1>>2)&3 - 2
		p[2] += b1&3 - 2
	case b1&0xc0 == opLuma:
		b2, err := s.r.ReadByte()
		if err != nil {
			return err
		}
		vg := b1&0x3f - 32
		p[0] += vg - 8 + (b2>>4)&0xf
		p[1] += vg
		p[2] += vg - 8 + b2&0xf
	default:
		s.run = int(b1 & 0x3f)
	}
	s.index[hash(*p)] = *p
	return nil
}

// noClose keeps the caller's source open when the band converter closes.
type noClose struct{ pipeline.Source }

func (noClose) Close() error { return nil }

// Encode writes src as QOI: RGB when it is known to be opaque, RGBA
// otherwise, its colour space marked sRGB. src is not closed.
func Encode(w io.Writer, src pipeline.Source) error {
	b := src.Bounds()
	if b.Empty() || int64(b.Dx())*int64(b.Dy()) > maxPixels {
		return errors.New("goimgtools: qoi: an image of 1 to 400 million pixels is needed")
	}
	channels := 4
	if pipeline.IsOpaque(src) {
		channels = 3
	}
	bw := bufio.NewWriterSize(w, 64<<10)
	var h [14]byte
	copy(h[:], "qoif")
	binary.BigEndian.PutUint32(h[4:], uint32(b.Dx()))
	binary.BigEndian.PutUint32(h[8:], uint32(b.Dy()))
	h[12], h[13] = byte(channels), 0
	bw.Write(h[:])
	var index [64][4]uint8
	prev := [4]uint8{0, 0, 0, 255}
	run := 0
	left := int64(b.Dx()) * int64(b.Dy())
	rows := pipeline.Map(noClose{src}, func(*image.NRGBA) error { return nil })
	defer rows.Close()
	for {
		m, err := rows.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		s := m.(*image.NRGBA)
		r := s.Rect
		for y := r.Min.Y; y < r.Max.Y; y++ {
			row := s.Pix[s.PixOffset(r.Min.X, y):][:4*r.Dx()]
			for x := 0; x < len(row); x += 4 {
				px := [4]uint8{row[x], row[x+1], row[x+2], row[x+3]}
				if channels == 3 {
					px[3] = 255
				}
				left--
				if px == prev {
					run++
					if run == 62 || left == 0 {
						bw.WriteByte(opRun | byte(run-1))
						run = 0
					}
					continue
				}
				if run > 0 {
					bw.WriteByte(opRun | byte(run-1))
					run = 0
				}
				i := hash(px)
				switch {
				case index[i] == px:
					bw.WriteByte(opIndex | byte(i))
				case px[3] != prev[3]:
					index[i] = px
					bw.Write([]byte{opRGBA, px[0], px[1], px[2], px[3]})
				default:
					index[i] = px
					vr, vg, vb := int8(px[0]-prev[0]), int8(px[1]-prev[1]), int8(px[2]-prev[2])
					vgr, vgb := vr-vg, vb-vg
					switch {
					case vr > -3 && vr < 2 && vg > -3 && vg < 2 && vb > -3 && vb < 2:
						bw.WriteByte(opDiff | byte(vr+2)<<4 | byte(vg+2)<<2 | byte(vb+2))
					case vgr > -9 && vgr < 8 && vg > -33 && vg < 32 && vgb > -9 && vgb < 8:
						bw.Write([]byte{opLuma | byte(vg+32), byte(vgr+8)<<4 | byte(vgb+8)})
					default:
						bw.Write([]byte{opRGB, px[0], px[1], px[2]})
					}
				}
				prev = px
			}
		}
	}
	if left != 0 {
		return io.ErrUnexpectedEOF
	}
	bw.Write([]byte{0, 0, 0, 0, 0, 0, 0, 1})
	return bw.Flush()
}
