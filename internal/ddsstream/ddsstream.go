// Package ddsstream reads DDS (DirectDraw Surface), the texture file of
// DirectX: the top image of the file (the first mipmap level of the first
// face or array slice), a band of rows at a time. It reads uncompressed
// pixels of 8, 16, 24 and 32 bits with any channel masks, luminance with or
// without alpha, and the block compressions DXT1/BC1, DXT3/BC2, DXT5/BC3,
// ATI1/BC4, ATI2/BC5 and BC7, named by a FourCC or by the DX10 header. The
// pixels are those Pillow gives for the same file.
package ddsstream

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"math/bits"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var errFormat = errors.New("goimgtools: dds: not a valid DDS file")

// unsupported is the error for a valid file of a kind that is not read.
func unsupported(what string) error {
	return fmt.Errorf("goimgtools: dds: %s is not supported: %w", what, errors.ErrUnsupported)
}

// maxSide bounds the width and the height (Direct3D stops at 16384).
const maxSide = 1 << 24

// Flags of the pixel format.
const (
	pfAlphaPixels = 0x1
	pfFourCC      = 0x4
	pfPalette8    = 0x20
	pfRGB         = 0x40
	pfLuminance   = 0x20000
)

// kind is how the pixels are stored.
type kind int

const (
	kMasks     kind = iota // 1 to 4 bytes a pixel, a mask for each channel
	kGray                  // a byte of luminance
	kGrayAlpha             // luminance, then alpha
	kRGBA                  // red, green, blue, alpha
	kBGRA                  // blue, green, red, alpha
	kBGRX                  // blue, green, red and a byte to ignore
	kBC1                   // blocks from here on
	kBC2
	kBC3
	kBC4
	kBC5
	kBC7
)

// header is what the 128 bytes a DDS file starts with, and the 20 of the
// DX10 header when there is one, say of the top image.
type header struct {
	w, h  int
	kind  kind
	data  int64     // where the pixels start
	bytes int       // stored bytes of a pixel, or of a block of 4 by 4
	alpha bool      // the pixels have transparency
	masks [4]uint32 // of red, green, blue and alpha (kMasks)
}

// Match reports whether head starts a DDS file.
func Match(head []byte) bool { return len(head) >= 4 && string(head[:4]) == "DDS " }

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

func readHeader(r io.ReaderAt) (header, error) {
	var b [148]byte
	n, _ := r.ReadAt(b[:], 0)
	if n < 8 || !Match(b[:n]) || binary.LittleEndian.Uint32(b[4:]) != 124 {
		return header{}, errFormat
	}
	if n < 128 {
		return header{}, io.ErrUnexpectedEOF
	}
	hu, wu := binary.LittleEndian.Uint32(b[12:]), binary.LittleEndian.Uint32(b[16:])
	if wu == 0 || hu == 0 || wu > maxSide || hu > maxSide {
		return header{}, errFormat
	}
	h := header{w: int(wu), h: int(hu), data: 128}
	flags := binary.LittleEndian.Uint32(b[80:])
	depth := binary.LittleEndian.Uint32(b[88:])
	// The flags are looked at in the order Pillow looks at them.
	switch {
	case flags&pfRGB != 0:
		if depth != 8 && depth != 16 && depth != 24 && depth != 32 {
			return h, unsupported(fmt.Sprintf("a colour image of %d bits a pixel", depth))
		}
		h.kind, h.bytes, h.alpha = kMasks, int(depth/8), flags&pfAlphaPixels != 0
		for i := range h.masks {
			h.masks[i] = binary.LittleEndian.Uint32(b[92+4*i:])
		}
	case flags&pfLuminance != 0:
		switch {
		case depth == 8:
			h.kind, h.bytes = kGray, 1
		case depth == 16 && flags&pfAlphaPixels != 0:
			h.kind, h.bytes, h.alpha = kGrayAlpha, 2, true
		default:
			return h, unsupported(fmt.Sprintf("a luminance image of %d bits a pixel", depth))
		}
	case flags&pfPalette8 != 0:
		return h, unsupported("a colour-mapped image")
	case flags&pfFourCC != 0:
		switch cc := string(b[84:88]); cc {
		case "DXT1":
			h.kind = kBC1
		case "DXT3":
			h.kind = kBC2
		case "DXT5":
			h.kind = kBC3
		case "ATI1", "BC4U":
			h.kind = kBC4
		case "ATI2", "BC5U":
			h.kind = kBC5
		case "DX10":
			if n < 148 {
				return h, io.ErrUnexpectedEOF
			}
			h.data = 148
			// The DXGI format; the rest of the DX10 header (dimension, cube
			// and array flags) changes nothing for the first image.
			switch f := binary.LittleEndian.Uint32(b[128:]); f {
			case 27, 28, 29: // R8G8B8A8 typeless, UNORM, UNORM_SRGB
				h.kind = kRGBA
			case 87, 90, 91: // B8G8R8A8 UNORM, typeless, UNORM_SRGB
				h.kind = kBGRA
			case 88, 92, 93: // B8G8R8X8 UNORM, typeless, UNORM_SRGB
				h.kind = kBGRX
			case 70, 71, 72:
				h.kind = kBC1
			case 73, 74, 75:
				h.kind = kBC2
			case 76, 77, 78:
				h.kind = kBC3
			case 79, 80:
				h.kind = kBC4
			case 82, 83:
				h.kind = kBC5
			case 97, 98, 99:
				h.kind = kBC7
			case 81, 84:
				return h, unsupported("signed BC4 or BC5")
			case 94, 95, 96:
				return h, unsupported("BC6H (high dynamic range)")
			default:
				return h, unsupported(fmt.Sprintf("DXGI format %d", f))
			}
		default:
			return h, unsupported(fmt.Sprintf("pixel format %q", cc))
		}
	default:
		return h, unsupported(fmt.Sprintf("a pixel format with flags %#x", flags))
	}
	switch h.kind {
	case kRGBA, kBGRA, kBGRX:
		h.bytes, h.alpha = 4, h.kind != kBGRX
	case kBC1, kBC4:
		h.bytes, h.alpha = 8, h.kind == kBC1
	case kBC2, kBC3, kBC5, kBC7:
		h.bytes, h.alpha = 16, h.kind != kBC5
	}
	return h, nil
}

// channel turns the bits of one mask into eight.
type channel struct {
	mask  uint32
	shift uint
	total uint32  // the largest value under the mask
	lut   []uint8 // the result for each value, when they are few
}

// scale is a channel value of a largest value total as eight bits, as Pillow
// reads it: int(v / total * 255) in double precision.
func scale(v, total uint32) uint8 {
	return uint8(float64(v) / float64(total) * 255)
}

func (c *channel) value(px uint32) uint8 {
	v := px & c.mask >> c.shift
	if c.lut != nil {
		return c.lut[v]
	}
	if c.total == 0 {
		return 0
	}
	return scale(v, c.total)
}

// stream is a DDS image decoded a band of rows at a time.
type stream struct {
	r        io.ReaderAt
	env      *pipeline.Env
	hd       header
	gray     *image.Gray  // the band of a grey image
	rgba     *image.NRGBA // or of a colour one
	raw      []byte       // the stored bytes of a band
	rb       int          // stored bytes of a row, or of a row of blocks
	ch       [4]channel
	n, y     int
	reserved int64
	err      error
}

// Open reads the header of a DDS file of size bytes and returns its top
// image, decoded a band at a time from r.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	hd, err := readHeader(r)
	if err != nil {
		return nil, err
	}
	s := &stream{r: r, env: env, hd: hd}
	out := int64(4)
	if hd.kind == kGray || hd.kind == kBC4 {
		out = 1
	}
	rows := 0 // of the band
	var raw int64
	if hd.kind >= kBC1 {
		// Four rows to a row of blocks: a band is whole rows of blocks.
		rb := int64((hd.w+3)/4) * int64(hd.bytes)
		if rb > (size-hd.data)/int64((hd.h+3)/4) {
			return nil, io.ErrUnexpectedEOF
		}
		s.n = max(4, pipeline.StripRows(int64(hd.w)*out+rb/4, hd.h)&^3)
		rows = min(s.n, (hd.h+3)&^3)
		s.rb, raw = int(rb), int64(rows/4)*rb
	} else {
		rb := int64(hd.w) * int64(hd.bytes)
		if rb > (size-hd.data)/int64(hd.h) {
			return nil, io.ErrUnexpectedEOF
		}
		s.n = pipeline.StripRows(int64(hd.w)*out+rb, hd.h)
		rows = s.n
		s.rb = int(rb)
		if hd.kind != kGray && hd.kind != kRGBA {
			// Those two are read straight into the band.
			raw = int64(rows) * rb
		}
	}
	s.reserved = int64(rows)*int64(hd.w)*out + raw
	if err := env.Budget.Reserve(s.reserved, "dds band"); err != nil {
		return nil, err
	}
	r0 := image.Rect(0, 0, hd.w, rows)
	if out == 1 {
		s.gray = image.NewGray(r0)
		s.raw = s.gray.Pix
	} else {
		s.rgba = image.NewNRGBA(r0)
		s.raw = s.rgba.Pix
	}
	if raw != 0 {
		s.raw = make([]byte, raw)
	}
	if hd.kind == kMasks {
		for i := range s.ch {
			c := &s.ch[i]
			c.mask = hd.masks[i]
			if c.mask == 0 {
				continue
			}
			c.shift = uint(bits.TrailingZeros32(c.mask))
			c.total = c.mask >> c.shift
			if c.total < 1024 {
				c.lut = make([]uint8, c.total+1)
				for v := range c.lut {
					c.lut[v] = scale(uint32(v), c.total)
				}
			}
		}
	}
	return s, nil
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.hd.w, s.hd.h) }
func (s *stream) ColorModel() color.Model {
	if s.gray != nil {
		return color.GrayModel
	}
	return color.NRGBAModel
}

// Opaque reports whether the format of the file has no transparency.
func (s *stream) Opaque() bool { return !s.hd.alpha }

func (s *stream) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.raw, s.gray, s.rgba = nil, nil, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: dds: closed")
	}
	return nil
}

func (s *stream) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.y >= s.hd.h {
		return nil, io.EOF
	}
	n := min(s.n, s.hd.h-s.y)
	var err error
	if s.hd.kind >= kBC1 {
		err = s.blocks(n)
	} else {
		err = s.pixels(n)
	}
	if err != nil {
		s.err = err
		return nil, err
	}
	rect := image.Rect(0, s.y, s.hd.w, s.y+n)
	s.y += n
	if b := s.gray; b != nil {
		return &image.Gray{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
	}
	b := s.rgba
	return &image.NRGBA{Pix: b.Pix[:n*b.Stride], Stride: b.Stride, Rect: rect}, nil
}

// pixels reads n rows of uncompressed pixels into the band.
func (s *stream) pixels(n int) error {
	hd := &s.hd
	raw := s.raw[:n*s.rb]
	if err := readFull(s.r, raw, hd.data+int64(s.y)*int64(s.rb)); err != nil {
		return err
	}
	if hd.kind == kGray || hd.kind == kRGBA {
		return nil
	}
	dst := s.rgba.Pix[:4*hd.w*n]
	switch hd.kind {
	case kGrayAlpha:
		for i, j := 0, 0; i < len(dst); i, j = i+4, j+2 {
			l := raw[j]
			dst[i], dst[i+1], dst[i+2], dst[i+3] = l, l, l, raw[j+1]
		}
	case kBGRA:
		for i := 0; i < len(dst); i += 4 {
			dst[i], dst[i+1], dst[i+2], dst[i+3] = raw[i+2], raw[i+1], raw[i], raw[i+3]
		}
	case kBGRX:
		for i := 0; i < len(dst); i += 4 {
			dst[i], dst[i+1], dst[i+2], dst[i+3] = raw[i+2], raw[i+1], raw[i], 255
		}
	default:
		// A pixel is a little-endian number; each channel is the bits under
		// its mask, scaled to eight (as Pillow's DdsRgbDecoder reads it).
		bpp := hd.bytes
		for i, j := 0, 0; i < len(dst); i, j = i+4, j+bpp {
			var px uint32
			switch bpp {
			case 1:
				px = uint32(raw[j])
			case 2:
				px = uint32(binary.LittleEndian.Uint16(raw[j:]))
			case 3:
				px = uint32(raw[j]) | uint32(raw[j+1])<<8 | uint32(raw[j+2])<<16
			default:
				px = binary.LittleEndian.Uint32(raw[j:])
			}
			dst[i], dst[i+1], dst[i+2], dst[i+3] = s.ch[0].value(px), s.ch[1].value(px), s.ch[2].value(px), 255
			if hd.alpha {
				dst[i+3] = s.ch[3].value(px)
			}
		}
	}
	return nil
}

// blocks reads the rows of blocks that hold n rows of the image and decodes
// them into the band, a row of blocks at a time. The band has room for the
// whole last row of blocks even where the image ends inside it.
func (s *stream) blocks(n int) error {
	hd := &s.hd
	across, down := (hd.w+3)/4, (n+3)/4
	raw := s.raw[:down*s.rb]
	if err := readFull(s.r, raw, hd.data+int64(s.y/4)*int64(s.rb)); err != nil {
		return err
	}
	var px [64]byte // the 16 pixels of a block, row by row
	for j := 0; j < down; j++ {
		row := raw[j*s.rb : (j+1)*s.rb]
		for bx := 0; bx < across; bx++ {
			blk := row[bx*hd.bytes : (bx+1)*hd.bytes]
			wide := min(4, hd.w-4*bx) // pixels of the block inside the image
			if hd.kind == kBC4 {
				interpolated(px[:], 1, blk)
				b := s.gray
				for k := 0; k < 4; k++ {
					copy(b.Pix[(4*j+k)*b.Stride+4*bx:][:wide], px[4*k:])
				}
				continue
			}
			switch hd.kind {
			case kBC1:
				colours(&px, blk, false)
			case kBC2:
				colours(&px, blk[8:], true)
				// Four bits of alpha a pixel, widened by repeating them.
				for i := 0; i < 16; i++ {
					a := blk[i/2] >> (4 * uint(i&1)) & 15
					px[4*i+3] = a<<4 | a
				}
			case kBC3:
				colours(&px, blk[8:], true)
				interpolated(px[3:], 4, blk)
			case kBC5:
				// Red and green, each coded as the alpha of BC3; blue is 0,
				// as Pillow reads it.
				for i := 0; i < 64; i += 4 {
					px[i+2], px[i+3] = 0, 255
				}
				interpolated(px[0:], 4, blk)
				interpolated(px[1:], 4, blk[8:])
			case kBC7:
				bc7(&px, blk)
			}
			b := s.rgba
			for k := 0; k < 4; k++ {
				copy(b.Pix[(4*j+k)*b.Stride+16*bx:][:4*wide], px[16*k:])
			}
		}
	}
	return nil
}

// rgb565 widens a colour of 5, 6 and 5 bits by repeating its top bits.
func rgb565(c uint16) (r, g, b uint32) {
	r = uint32(c>>11) & 31
	g = uint32(c>>5) & 63
	b = uint32(c) & 31
	return r<<3 | r>>2, g<<2 | g>>4, b<<3 | b>>2
}

// colours decodes the colour block of BC1 (also the colour half of BC2 and
// BC3) into the 16 pixels of px, as Pillow's BcnDecode does: two colours
// of 16 bits, then two bits a pixel choosing one of four. When the first
// colour is the larger number, or always in BC2 and BC3 (four), the other
// two lie a third and two thirds of the way between them; otherwise the
// third is half way and the fourth is transparent black. Divisions round
// down.
func colours(px *[64]byte, src []byte, four bool) {
	c0, c1 := binary.LittleEndian.Uint16(src), binary.LittleEndian.Uint16(src[2:])
	r0, g0, b0 := rgb565(c0)
	r1, g1, b1 := rgb565(c1)
	var p [4][4]byte
	p[0] = [4]byte{byte(r0), byte(g0), byte(b0), 255}
	p[1] = [4]byte{byte(r1), byte(g1), byte(b1), 255}
	if c0 > c1 || four {
		p[2] = [4]byte{byte((2*r0 + r1) / 3), byte((2*g0 + g1) / 3), byte((2*b0 + b1) / 3), 255}
		p[3] = [4]byte{byte((r0 + 2*r1) / 3), byte((g0 + 2*g1) / 3), byte((b0 + 2*b1) / 3), 255}
	} else {
		p[2] = [4]byte{byte((r0 + r1) / 2), byte((g0 + g1) / 2), byte((b0 + b1) / 2), 255}
	}
	lut := binary.LittleEndian.Uint32(src[4:])
	for i := 0; i < 16; i++ {
		c := &p[lut>>(2*uint(i))&3]
		px[4*i], px[4*i+1], px[4*i+2], px[4*i+3] = c[0], c[1], c[2], c[3]
	}
}

// interpolated decodes a block of one channel (the alpha of BC3, BC4, each
// half of BC5) into dst, a value every stride bytes: two values of 8 bits,
// then three bits a pixel choosing one of eight. When the first value is
// the larger, the other six lie between the two; otherwise four lie
// between and the last two are 0 and 255. Divisions round down, as in
// Pillow's BcnDecode.
func interpolated(dst []byte, stride int, src []byte) {
	a0, a1 := uint32(src[0]), uint32(src[1])
	var l [8]byte
	l[0], l[1] = src[0], src[1]
	if a0 > a1 {
		for i := uint32(1); i < 7; i++ {
			l[i+1] = byte(((7-i)*a0 + i*a1) / 7)
		}
	} else {
		for i := uint32(1); i < 5; i++ {
			l[i+1] = byte(((5-i)*a0 + i*a1) / 5)
		}
		l[6], l[7] = 0, 255
	}
	lut := uint64(src[2]) | uint64(src[3])<<8 | uint64(src[4])<<16 | uint64(src[5])<<24 | uint64(src[6])<<32 | uint64(src[7])<<40
	for i := 0; i < 16; i++ {
		dst[i*stride] = l[lut>>(3*uint(i))&7]
	}
}
