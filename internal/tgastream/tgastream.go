// Package tgastream reads and writes Truevision TGA (Targa), the format of
// game textures: colour-mapped, true colour (15, 16, 24 and 32 bits) and
// grey images, run-length compressed or not, stored from either corner, a
// band of rows at a time. Files are written run-length compressed from the
// top row down, as Pillow writes them with compression "tga_rle" and
// orientation 1, to the byte.
package tgastream

import (
	"bufio"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var errFormat = errors.New("goimgtools: tga: not a valid TGA file")

// header is the 18 bytes a TGA file starts with.
type header struct {
	idLen, cmType, imgType int
	cmStart, cmLen         int
	cmDepth                int
	w, h                   int
	depth                  int
	desc                   int
}

func parseHeader(b []byte) (header, bool) {
	if len(b) < 18 {
		return header{}, false
	}
	h := header{idLen: int(b[0]), cmType: int(b[1]), imgType: int(b[2]),
		cmStart: int(binary.LittleEndian.Uint16(b[3:])), cmLen: int(binary.LittleEndian.Uint16(b[5:])), cmDepth: int(b[7]),
		w: int(binary.LittleEndian.Uint16(b[12:])), h: int(binary.LittleEndian.Uint16(b[14:])), depth: int(b[16]), desc: int(b[17])}
	if h.w == 0 || h.h == 0 || h.desc&0xc0 != 0 || h.cmType > 1 {
		return h, false
	}
	alphaBits := h.desc & 15
	switch h.imgType &^ 8 {
	case 1:
		if h.cmType != 1 || h.depth != 8 || h.cmLen == 0 || h.cmStart+h.cmLen > 256 ||
			h.cmDepth != 15 && h.cmDepth != 16 && h.cmDepth != 24 && h.cmDepth != 32 {
			return h, false
		}
	case 2:
		ok := h.depth == 15 && alphaBits == 0 || h.depth == 16 && alphaBits <= 1 ||
			h.depth == 24 && alphaBits == 0 || h.depth == 32 && (alphaBits == 0 || alphaBits == 8)
		if !ok {
			return h, false
		}
	case 3:
		if !(h.depth == 8 && alphaBits == 0 || h.depth == 16 && (alphaBits == 0 || alphaBits == 8)) {
			return h, false
		}
	default:
		return h, false
	}
	if h.imgType != 1 && h.imgType != 2 && h.imgType != 3 && h.imgType != 9 && h.imgType != 10 && h.imgType != 11 {
		return h, false
	}
	return h, true
}

// Match reports whether head starts what can only be a TGA file (TGA has
// no signature: its header must make sense).
func Match(head []byte) bool {
	_, ok := parseHeader(head)
	return ok
}

// stream is a TGA image decoded a band of rows at a time.
type stream struct {
	r        io.ReaderAt
	env      *pipeline.Env
	h        header
	bpp      int // stored bytes a pixel
	data     int64
	alpha    bool // the image has transparency
	premul   bool // its colours are multiplied by it
	palette  color.Palette
	band     image.Image
	raw      []byte
	n, y     int
	reserved int64
	// Run-length compressed rows, in the order stored.
	rle     *bufio.Reader
	count   int  // pixels left in the packet
	run     bool // the packet repeats pixel
	pixel   [4]byte
	spill   *pipeline.Spill // the rows of a bottom-up compressed image
	spilled bool
	err     error
}

// Open reads the header of a TGA file of size bytes and returns its image,
// decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	var hb [18]byte
	if _, err := r.ReadAt(hb[:], 0); err != nil {
		return nil, errFormat
	}
	h, ok := parseHeader(hb[:])
	if !ok {
		return nil, errFormat
	}
	s := &stream{r: r, env: env, h: h, bpp: (h.depth + 7) / 8}
	off := int64(18 + h.idLen)
	if h.cmType == 1 && h.imgType&^8 != 1 {
		// A colour map of a true colour or grey image: not used, stepped over.
		off += int64(h.cmLen) * int64((h.cmDepth+7)/8)
	} else if h.cmType == 1 {
		eb := (h.cmDepth + 7) / 8
		cm := make([]byte, h.cmLen*eb)
		if _, err := r.ReadAt(cm, off); err != nil {
			return nil, io.ErrUnexpectedEOF
		}
		off += int64(len(cm))
		s.palette = make(color.Palette, 256)
		for i := range s.palette {
			s.palette[i] = color.NRGBA{0, 0, 0, 255}
		}
		for i := 0; i < h.cmLen; i++ {
			s.palette[h.cmStart+i] = pixelOf(cm[i*eb:], h.cmDepth, h.cmDepth == 32 || h.cmDepth == 16)
		}
	}
	s.data = off
	alphaBits := h.desc & 15
	switch {
	case h.imgType&^8 == 2 && h.depth == 32:
		// As Pillow, stb_image and most readers: the fourth byte is alpha
		// whatever the header says, unless the extension area says not.
		s.alpha = true
	case h.imgType&^8 == 2 && h.depth == 16:
		s.alpha = alphaBits == 1
	case h.imgType&^8 == 3 && h.depth == 16:
		s.alpha = true
	}
	if attr, ok := attributes(r, size); ok {
		switch attr {
		case 0, 1, 2:
			if h.imgType&^8 == 2 {
				s.alpha = false
			}
		case 4:
			s.premul = s.alpha
		}
	}
	rowBytes := int64(h.w) * int64(s.bpp)
	if h.imgType&8 == 0 && rowBytes > (size-s.data)/int64(h.h) {
		return nil, io.ErrUnexpectedEOF
	}
	out := int64(4)
	switch {
	case h.imgType&^8 == 1, h.imgType&^8 == 3 && !s.alpha:
		out = 1
	}
	s.n = pipeline.StripRows(int64(h.w)*out+rowBytes, h.h)
	s.reserved = int64(s.n) * (int64(h.w)*out + rowBytes)
	if err := env.Budget.Reserve(s.reserved, "tga band"); err != nil {
		return nil, err
	}
	r0 := image.Rect(0, 0, h.w, s.n)
	switch {
	case h.imgType&^8 == 1:
		pal := s.palette
		if h.cmDepth != 32 && h.cmDepth != 16 {
			pal = append(color.Palette(nil), pal...)
		}
		s.band = image.NewPaletted(r0, pal)
	case out == 1:
		s.band = image.NewGray(r0)
	default:
		s.band = image.NewNRGBA(r0)
	}
	s.raw = make([]byte, int64(s.n)*rowBytes)
	if h.imgType&8 != 0 {
		s.rle = bufio.NewReaderSize(io.NewSectionReader(r, s.data, size-s.data), 32<<10)
	}
	return s, nil
}

// attributes reads the attributes type of a TGA 2.0 extension area: 0 no
// alpha, 1 and 2 alpha to ignore, 3 alpha, 4 premultiplied alpha.
func attributes(r io.ReaderAt, size int64) (int, bool) {
	if size < 18+26 {
		return 0, false
	}
	var f [26]byte
	if _, err := r.ReadAt(f[:], size-26); err != nil || string(f[8:]) != "TRUEVISION-XFILE.\x00" {
		return 0, false
	}
	ext := int64(binary.LittleEndian.Uint32(f[:]))
	if ext < 18 || ext+495 > size-26 {
		return 0, false
	}
	var e [495]byte
	if _, err := r.ReadAt(e[:], ext); err != nil || binary.LittleEndian.Uint16(e[:]) != 495 {
		return 0, false
	}
	return int(e[494]), true
}

// pixelOf is a stored pixel of the given bits.
func pixelOf(p []byte, bits int, alpha bool) color.NRGBA {
	switch bits {
	case 15, 16:
		v := binary.LittleEndian.Uint16(p)
		c := color.NRGBA{five(v >> 10), five(v >> 5), five(v), 255}
		if alpha && bits == 16 && v&0x8000 == 0 {
			c.A = 0
		}
		return c
	case 24:
		return color.NRGBA{p[2], p[1], p[0], 255}
	}
	a := p[3]
	if !alpha {
		a = 255
	}
	return color.NRGBA{p[2], p[1], p[0], a}
}

// five widens 5 bits as Pillow does (BGR;5): v * 255 / 31, rounded down.
func five(v uint16) uint8 { v &= 31; return uint8(uint32(v) * 255 / 31) }

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.h.w, s.h.h) }
func (s *stream) ColorModel() color.Model {
	if p, ok := s.band.(*image.Paletted); ok {
		return p.Palette
	}
	return s.band.ColorModel()
}

// Opaque reports whether the image has no transparency.
func (s *stream) Opaque() bool {
	if s.palette != nil {
		for _, c := range s.palette {
			if c.(color.NRGBA).A != 255 {
				return false
			}
		}
		return true
	}
	return !s.alpha
}

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	if s.spill != nil {
		s.spill.Close()
		s.spill = nil
	}
	s.raw = nil
	if s.err == nil {
		s.err = errors.New("goimgtools: tga: closed")
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
	if err := s.read(n); err != nil {
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

// topDown reports whether the first row stored is the top one.
func (s *stream) topDown() bool { return s.h.desc&0x20 != 0 }

// read puts the stored bytes of image rows y to y+n-1 into raw, in the
// order of the image.
func (s *stream) read(n int) error {
	rb := s.h.w * s.bpp
	raw := s.raw[:n*rb]
	switch {
	case s.rle == nil:
		first := s.y
		if !s.topDown() {
			first = s.h.h - s.y - n
		}
		if _, err := s.r.ReadAt(raw, s.data+int64(first)*int64(rb)); err != nil {
			return err
		}
	case s.topDown():
		for k := 0; k < n; k++ {
			if err := s.unpack(raw[k*rb : (k+1)*rb]); err != nil {
				return err
			}
		}
		return nil
	default:
		// Bottom up and compressed: the whole image unpacked into working
		// storage first, then read from the end.
		if !s.spilled {
			s.spilled = true
			s.spill = pipeline.NewSpill(s.env)
			s.spill.Expect(int64(s.h.h) * int64(rb))
			row := raw[:rb]
			for y := 0; y < s.h.h; y++ {
				if err := s.unpack(row); err != nil {
					return err
				}
				if _, err := s.spill.Write(row); err != nil {
					return err
				}
			}
		}
		if _, err := s.spill.ReadAt(raw, int64(s.h.h-s.y-n)*int64(rb)); err != nil {
			return err
		}
	}
	if !s.topDown() {
		// Rows come bottom up: turn the band over.
		for a, b := 0, n-1; a < b; a, b = a+1, b-1 {
			ra, rb2 := raw[a*rb:(a+1)*rb], raw[b*rb:(b+1)*rb]
			for i := range ra {
				ra[i], rb2[i] = rb2[i], ra[i]
			}
		}
	}
	return nil
}

// unpack fills a row from the run-length packets, which may run on from
// one row to the next.
func (s *stream) unpack(row []byte) error {
	bpp := s.bpp
	for i := 0; i < len(row); i += bpp {
		if s.count == 0 {
			h, err := s.rle.ReadByte()
			if err != nil {
				return err
			}
			s.count, s.run = int(h&0x7f)+1, h&0x80 != 0
			if s.run {
				if _, err := io.ReadFull(s.rle, s.pixel[:bpp]); err != nil {
					return err
				}
			}
		}
		if s.run {
			copy(row[i:i+bpp], s.pixel[:bpp])
		} else if _, err := io.ReadFull(s.rle, row[i:i+bpp]); err != nil {
			return err
		}
		s.count--
	}
	return nil
}

// compose turns n stored rows into the band.
func (s *stream) compose(n int) image.Image {
	h := s.h
	w, rb := h.w, h.w*s.bpp
	flip := h.desc&0x10 != 0
	rect := image.Rect(0, s.y, w, s.y+n)
	for k := 0; k < n; k++ {
		src := s.raw[k*rb : (k+1)*rb]
		for x := 0; x < w; x++ {
			sx := x
			if flip {
				sx = w - 1 - x
			}
			p := src[sx*s.bpp:]
			switch b := s.band.(type) {
			case *image.Paletted:
				b.Pix[k*b.Stride+x] = p[0]
			case *image.Gray:
				b.Pix[k*b.Stride+x] = p[0]
			case *image.NRGBA:
				var c color.NRGBA
				if h.imgType&^8 == 3 {
					c = color.NRGBA{p[0], p[0], p[0], p[1]}
				} else {
					c = pixelOf(p, h.depth, s.alpha)
				}
				if s.premul && c.A != 0 && c.A != 255 {
					a := uint32(c.A)
					c.R = uint8(min(255, (uint32(c.R)*255+a/2)/a))
					c.G = uint8(min(255, (uint32(c.G)*255+a/2)/a))
					c.B = uint8(min(255, (uint32(c.B)*255+a/2)/a))
				}
				d := b.Pix[k*b.Stride+4*x:]
				d[0], d[1], d[2], d[3] = c.R, c.G, c.B, c.A
			}
		}
	}
	switch b := s.band.(type) {
	case *image.Paletted:
		return &image.Paletted{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect, Palette: b.Palette}
	case *image.Gray:
		return &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	case *image.NRGBA:
		return &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}
	}
	panic("unreachable")
}

// noClose keeps the caller's source open.
type noClose struct{ pipeline.Source }

func (noClose) Close() error { return nil }

// Encode writes src as a run-length compressed TGA, top row first: grey
// for a grey image, colour-mapped for one of at most 256 colours,
// otherwise true colour with alpha (32 bits) unless src is known to be
// opaque (24 bits). src is not closed.
func Encode(w io.Writer, src pipeline.Source) error {
	b := src.Bounds()
	if b.Empty() || b.Dx() > 65535 || b.Dy() > 65535 {
		return errors.New("goimgtools: tga: an image of 1 to 65535 pixels each way is needed")
	}
	var pal color.Palette
	grey := false
	switch m := src.ColorModel().(type) {
	case color.Palette:
		pal = m
	default:
		grey = m == color.GrayModel || m == color.Gray16Model
	}
	opaque := pipeline.IsOpaque(src)
	bpp, imgType, flags := 4, 10, 0x20|8
	var head []byte
	switch {
	case pal != nil && len(pal) <= 256:
		bpp, imgType, flags = 1, 9, 0x20
		mapBits := 24
		for _, c := range pal {
			if color.NRGBAModel.Convert(c).(color.NRGBA).A != 255 {
				mapBits = 32
			}
		}
		head = tgaHeader(1, imgType, len(pal), mapBits, b.Dx(), b.Dy(), 8, flags)
		for _, c := range pal {
			n := color.NRGBAModel.Convert(c).(color.NRGBA)
			head = append(head, n.B, n.G, n.R)
			if mapBits == 32 {
				head = append(head, n.A)
			}
		}
	case grey:
		bpp, imgType, flags = 1, 11, 0x20
		head = tgaHeader(0, imgType, 0, 0, b.Dx(), b.Dy(), 8, flags)
	case opaque:
		bpp, flags = 3, 0x20
		head = tgaHeader(0, imgType, 0, 0, b.Dx(), b.Dy(), 24, flags)
	default:
		head = tgaHeader(0, imgType, 0, 0, b.Dx(), b.Dy(), 32, flags)
	}
	env := pipeline.EnvOf(src)
	need := int64(b.Dx()) * int64(bpp) * 2
	if err := env.Budget.Reserve(need, "tga row"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	bw := bufio.NewWriterSize(w, 64<<10)
	bw.Write(head)
	row := make([]byte, b.Dx()*bpp)
	packed := make([]byte, 0, b.Dx()*bpp+b.Dx())
	rows := src
	if pal == nil && !grey {
		rows = pipeline.Map(noClose{src}, func(*image.NRGBA) error { return nil })
		defer rows.Close()
	}
	y := b.Min.Y
	for {
		m, err := rows.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		r := m.Bounds()
		for yy := r.Min.Y; yy < r.Max.Y; yy++ {
			switch p := m.(type) {
			case *image.Paletted:
				copy(row, p.Pix[p.PixOffset(r.Min.X, yy):][:r.Dx()])
			case *image.NRGBA:
				px := p.Pix[p.PixOffset(r.Min.X, yy):]
				for x := 0; x < r.Dx(); x++ {
					q := row[x*bpp:]
					q[0], q[1], q[2] = px[4*x+2], px[4*x+1], px[4*x]
					if bpp == 4 {
						q[3] = px[4*x+3]
					}
				}
			case *image.Gray:
				copy(row, p.Pix[p.PixOffset(r.Min.X, yy):][:r.Dx()])
			default:
				for x := 0; x < r.Dx(); x++ {
					row[x] = color.GrayModel.Convert(m.At(r.Min.X+x, yy)).(color.Gray).Y
				}
			}
			packed = packRow(packed[:0], row, bpp)
			if _, err := bw.Write(packed); err != nil {
				return err
			}
			y++
		}
	}
	if y != b.Max.Y {
		return io.ErrUnexpectedEOF
	}
	// The footer of TGA 2.0, with no extension or developer area.
	bw.Write(make([]byte, 8))
	bw.WriteString("TRUEVISION-XFILE.\x00")
	return bw.Flush()
}

func tgaHeader(cmType, imgType, cmLen, cmDepth, w, h, depth, flags int) []byte {
	b := make([]byte, 18)
	b[1], b[2] = byte(cmType), byte(imgType)
	binary.LittleEndian.PutUint16(b[5:], uint16(cmLen))
	b[7] = byte(cmDepth)
	binary.LittleEndian.PutUint16(b[12:], uint16(w))
	binary.LittleEndian.PutUint16(b[14:], uint16(h))
	b[16], b[17] = byte(depth), byte(flags)
	return b
}

// packRow compresses a row as Pillow's TgaRleEncode does: two equal pixels
// in a row start a run, the rest go in literal packets, 128 pixels at most
// each, none past the end of the row.
func packRow(out, row []byte, bpp int) []byte {
	w := len(row) / bpp
	px := func(i int) []byte { return row[i*bpp : (i+1)*bpp] }
	eq := func(i int) bool { // pixel i equals pixel i+1
		a, b := px(i), px(i+1)
		for k := range a {
			if a[k] != b[k] {
				return false
			}
		}
		return true
	}
	for x := 0; x < w; {
		s := x
		last := s
		maxL := min(s+127, w-1)
		run := x+1 < w && eq(x)
		switch {
		case run:
			last = s + 1
			for last < maxL && eq(last) {
				last++
			}
		case x+1 < w:
			last = s + 1
			for last < maxL {
				if !eq(last) {
					last++
				} else {
					last--
					break
				}
			}
		}
		if run {
			out = append(out, 0x80|byte(last-s))
			out = append(out, px(s)...)
		} else {
			out = append(out, byte(last-s))
			out = append(out, row[s*bpp:(last+1)*bpp]...)
		}
		x = last + 1
	}
	return out
}
