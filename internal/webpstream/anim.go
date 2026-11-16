package webpstream

import (
	"encoding/binary"
	"image"
	"image/color"
	"io"
	"time"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// flagAnim is the animation flag of the VP8X chunk.
const flagAnim = 1 << 1

// u24 decodes the first three bytes of b as a little-endian integer.
func u24(b []byte) int { return int(b[0]) | int(b[1])<<8 | int(b[2])<<16 }

// frameHeader is the start of an ANMF chunk's data: where the frame goes on
// the canvas, how long it shows, and how it is put there.
type frameHeader struct {
	rect     image.Rectangle
	duration int // milliseconds
	noBlend  bool
	dispose  bool
}

func parseFrameHeader(b []byte) frameHeader {
	x, y := 2*u24(b[0:]), 2*u24(b[3:])
	return frameHeader{
		rect:     image.Rect(x, y, x+1+u24(b[6:]), y+1+u24(b[9:])),
		duration: u24(b[12:]),
		noBlend:  b[15]&2 != 0,
		dispose:  b[15]&1 != 0,
	}
}

// openFrame takes the image of the ANMF chunk at pos (n bytes, all in the
// input) as the source's image: its optional ALPH chunk and its VP8 or
// VP8L chunk. The frame has to lie within canvas.
func (s *source) openFrame(r io.ReaderAt, pos int64, n uint32, canvas image.Rectangle) error {
	if n < 16 {
		return errInvalidFormat
	}
	var b [16]byte
	if _, err := r.ReadAt(b[:], pos); err != nil {
		return err
	}
	fh := parseFrameHeader(b[:])
	if !fh.rect.In(canvas) {
		return errInvalidFormat
	}
	s.frame = fh.rect
	want := fh.rect.Size()
	p, end := pos+16, pos+int64(n)
	for end-p >= 8 {
		if _, err := r.ReadAt(b[:8], p); err != nil {
			return err
		}
		m := u32(b[4:])
		p += 8
		if int64(m) > end-p {
			return errListSubchunkTooLong
		}
		switch string(b[:4]) {
		case "ALPH":
			if s.hasAlpha {
				return errInvalidFormat
			}
			if err := s.alph(r, p, m, int64(m), want.X, want.Y); err != nil {
				return err
			}
		case "VP8 ":
			return s.vp8(r, p, m, int64(m), want)
		case "VP8L":
			if s.hasAlpha {
				return errInvalidFormat
			}
			return s.vp8l(r, p, int64(m), want)
		}
		p += int64(m) + int64(m&1)
	}
	// A frame without an image.
	return errInvalidFormat
}

// onCanvas returns the source of the first frame as the canvas shows it.
// A frame covering the whole canvas is the source itself; a smaller one
// is put at its place on a transparent canvas.
func (s *source) onCanvas(canvas image.Rectangle) pipeline.Source {
	if s.frame == canvas {
		return s
	}
	return &placed{src: pipeline.Map(ToRGB(s), func(*image.NRGBA) error { return nil }),
		env: s.env, meta: s.meta, canvas: canvas, frame: s.frame,
		rows: pipeline.StripRows(4*int64(canvas.Dx()), canvas.Dy())}
}

// placed is a frame smaller than the canvas, at its place on the canvas:
// NRGBA strips of the canvas width, transparent outside the frame.
type placed struct {
	src      pipeline.Source // the frame, as NRGBA strips
	env      *pipeline.Env
	meta     pipeline.Meta
	canvas   image.Rectangle
	frame    image.Rectangle
	rows     int // most rows in a strip
	y        int // the next row of the canvas
	cur      *image.NRGBA
	band     []byte
	reserved int64
	err      error
}

func (p *placed) Bounds() image.Rectangle { return p.canvas }
func (p *placed) ColorModel() color.Model { return color.NRGBAModel }
func (p *placed) Env() *pipeline.Env      { return p.env }
func (p *placed) Meta() pipeline.Meta     { return p.meta }

func (p *placed) Close() error {
	p.env.Budget.Release(p.reserved)
	p.reserved, p.band, p.cur = 0, nil, nil
	return p.src.Close()
}

func (p *placed) Next() (image.Image, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.y >= p.canvas.Max.Y {
		return nil, io.EOF
	}
	m, err := p.next()
	if err != nil && err != io.EOF {
		p.err = err
	}
	return m, err
}

func (p *placed) next() (image.Image, error) {
	w := p.canvas.Dx()
	n := 0
	in := p.y >= p.frame.Min.Y && p.y < p.frame.Max.Y
	if in {
		// The frame's rows, as far as its current strip goes.
		fy := p.y - p.frame.Min.Y
		if p.cur == nil || fy >= p.cur.Rect.Max.Y {
			m, err := p.src.Next()
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			if err != nil {
				return nil, err
			}
			p.cur = m.(*image.NRGBA)
		}
		n = min(p.cur.Rect.Max.Y-fy, p.rows)
	} else {
		// Transparent rows, up to the frame or the end of the canvas.
		end := p.canvas.Max.Y
		if p.y < p.frame.Min.Y {
			end = p.frame.Min.Y
		}
		n = min(end-p.y, p.rows)
	}
	need := int64(4*w) * int64(n)
	if int64(len(p.band)) < need {
		p.env.Budget.Release(p.reserved)
		p.reserved, p.band = 0, nil
		if err := p.env.Budget.Reserve(need, "webp frame strip"); err != nil {
			return nil, err
		}
		p.reserved, p.band = need, make([]byte, need)
	}
	band := p.band[:need]
	clear(band)
	if in {
		fy := p.y - p.frame.Min.Y
		for i := 0; i < n; i++ {
			src := p.cur.Pix[p.cur.PixOffset(0, fy+i):][:4*p.frame.Dx()]
			copy(band[i*4*w+4*p.frame.Min.X:], src)
		}
	}
	out := &image.NRGBA{Pix: band, Stride: 4 * w, Rect: image.Rect(0, p.y, w, p.y+n)}
	p.y += n
	return out, nil
}

// Animation is what the chunks of an animated WebP file say about it.
type Animation struct {
	// Frames is the number of frames: 0 for a still image.
	Frames int
	// Duration is how long the frames show, once through.
	Duration time.Duration
	// LoopCount is how often the animation plays: 0 for ever.
	LoopCount int
	// Width and Height are the size of the canvas.
	Width, Height int
}

// ReadAnimation reads the chunk headers of the WebP file r (size bytes)
// and the headers of its frames, nothing else. A still image gives an
// Animation of no frames.
func ReadAnimation(r io.ReaderAt, size int64) (Animation, error) {
	var a Animation
	var b [16]byte
	if n, err := readAt(r, b[:12], 0, size); err != nil {
		return a, err
	} else if n < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return a, errInvalidFormat
	}
	end := min(size, 8+int64(u32(b[4:])))
	anim := false
	for pos := int64(12); end-pos >= 8; {
		if _, err := r.ReadAt(b[:8], pos); err != nil {
			return a, err
		}
		n := int64(u32(b[4:]))
		pos += 8
		switch string(b[:4]) {
		case "VP8X":
			if n >= 10 && end-pos >= 10 {
				if _, err := r.ReadAt(b[:10], pos); err != nil {
					return a, err
				}
				anim = b[0]&flagAnim != 0
				a.Width, a.Height = u24(b[4:])+1, u24(b[7:])+1
			}
		case "ANIM":
			if anim && n >= 6 && end-pos >= 6 {
				if _, err := r.ReadAt(b[:6], pos); err != nil {
					return a, err
				}
				a.LoopCount = int(binary.LittleEndian.Uint16(b[4:]))
			}
		case "ANMF":
			if anim && n >= 16 && end-pos >= 16 {
				if _, err := r.ReadAt(b[:16], pos); err != nil {
					return a, err
				}
				a.Frames++
				a.Duration += time.Duration(parseFrameHeader(b[:]).duration) * time.Millisecond
			}
		}
		pos += n + n&1
	}
	if !anim {
		return Animation{}, nil
	}
	return a, nil
}
