// Package icostream reads and writes ICO files, the icons of Windows and
// of web sites (favicon.ico), and reads CUR files, its cursors.
//
// A file is a directory of images of the same picture at different sizes
// and colour depths, each stored as a PNG file or as a device-independent
// bitmap (a BMP without its file header, its height doubled for a 1-bit
// mask of transparency after the colours). Open decodes the largest (the
// deepest colours among those of that size), a PNG one as it streams, a
// bitmap one whole (at most 256x256 pixels of 4 bytes each). Encode writes
// PNG images, which every Windows since Vista and every browser shows.
package icostream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/internal/pngstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// MaxSide is the largest side of an image a directory entry can say.
const MaxSide = 256

// maxEntries bounds the directory read (an icon holds a handful).
const maxEntries = 1024

// Match reports whether h starts an ICO or a CUR file.
func Match(h []byte) bool {
	return len(h) >= 6 && h[0] == 0 && h[1] == 0 && (h[2] == 1 || h[2] == 2) && h[3] == 0 && (h[4] != 0 || h[5] != 0)
}

// entry is a directory entry.
type entry struct {
	w, h   int // 256 for 0
	bits   int
	size   int64
	offset int64
}

var errFormat = errors.New("goimgtools: ico: not a valid icon file")

// readDir reads the directory of an icon file of size bytes.
func readDir(r io.ReaderAt, size int64) ([]entry, error) {
	var hdr [6]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return nil, errFormat
	}
	if !Match(hdr[:]) {
		return nil, errFormat
	}
	n := int(binary.LittleEndian.Uint16(hdr[4:]))
	if n > maxEntries || int64(6+16*n) > size {
		return nil, errFormat
	}
	buf := make([]byte, 16*n)
	if _, err := r.ReadAt(buf, 6); err != nil {
		return nil, errFormat
	}
	es := make([]entry, 0, n)
	for i := 0; i < n; i++ {
		b := buf[16*i:]
		e := entry{w: int(b[0]), h: int(b[1]), bits: int(binary.LittleEndian.Uint16(b[6:])),
			size: int64(binary.LittleEndian.Uint32(b[8:])), offset: int64(binary.LittleEndian.Uint32(b[12:]))}
		if e.w == 0 {
			e.w = 256
		}
		if e.h == 0 {
			e.h = 256
		}
		if e.size < 8 || e.offset < int64(6+16*n) || e.offset > size || e.size > size-e.offset {
			continue // an entry pointing outside the file is passed over
		}
		es = append(es, e)
	}
	if len(es) == 0 {
		return nil, errFormat
	}
	return es, nil
}

// best is the entry decoded: the largest, the deepest colours of that size,
// the first of those.
func best(es []entry) entry {
	b := es[0]
	for _, e := range es[1:] {
		if a, c := e.w*e.h, b.w*b.h; a > c || a == c && e.bits > b.bits {
			b = e
		}
	}
	return b
}

// Config reads the size of the image Open would decode.
func Config(r io.ReaderAt, size int64) (w, h int, err error) {
	es, err := readDir(r, size)
	if err != nil {
		return 0, 0, err
	}
	e := best(es)
	var head [32]byte
	if _, err := r.ReadAt(head[:], e.offset); err != nil {
		return 0, 0, errFormat
	}
	if bytes.HasPrefix(head[:], []byte("\x89PNG\r\n\x1a\n")) {
		return int(binary.BigEndian.Uint32(head[16:])), int(binary.BigEndian.Uint32(head[20:])), nil
	}
	dw, dh := int(int32(binary.LittleEndian.Uint32(head[4:]))), int(int32(binary.LittleEndian.Uint32(head[8:])))/2
	if dw <= 0 || dh <= 0 {
		return 0, 0, errFormat
	}
	return dw, dh, nil
}

// Open decodes the largest image of an icon file of size bytes (see best).
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	es, err := readDir(r, size)
	if err != nil {
		return nil, err
	}
	e := best(es)
	sec := io.NewSectionReader(r, e.offset, e.size)
	var magic [8]byte
	if _, err := sec.ReadAt(magic[:], 0); err != nil {
		return nil, errFormat
	}
	if bytes.Equal(magic[:], []byte("\x89PNG\r\n\x1a\n")) {
		return pngstream.Open(sec, env)
	}
	m, err := decodeDIB(sec, e.size, env)
	if err != nil {
		return nil, err
	}
	return &held{Source: pipeline.FromImage(m, env), env: env, n: int64(len(m.Pix))}, nil
}

// held is a decoded bitmap's source, which gives its memory back when
// closed.
type held struct {
	pipeline.Source
	env  *pipeline.Env
	n    int64
	done bool
}

func (h *held) Env() *pipeline.Env { return h.env }
func (h *held) Opaque() bool       { return pipeline.IsOpaque(h.Source) }

func (h *held) Close() error {
	if !h.done {
		h.done = true
		h.env.Budget.Release(h.n)
	}
	return h.Source.Close()
}

// maxDIBSide bounds the bitmaps decoded: the directory says at most 256,
// a little more is let through for icons that write their header loosely.
const maxDIBSide = 1024

// decodeDIB decodes a bitmap image of an icon: colours of 1, 4, 8, 24 or
// 32 bits, bottom row first, then the mask, 1 bit a pixel, 1 where the
// pixel is transparent. A 32-bit image whose alpha is all 0 is opaque but
// where the mask says otherwise (the icons of Windows XP and before).
func decodeDIB(r io.ReaderAt, size int64, env *pipeline.Env) (*image.NRGBA, error) {
	var hdr [40]byte
	if _, err := r.ReadAt(hdr[:], 0); err != nil {
		return nil, errFormat
	}
	le := binary.LittleEndian
	hsize := int64(le.Uint32(hdr[0:]))
	w := int(int32(le.Uint32(hdr[4:])))
	h2 := int(int32(le.Uint32(hdr[8:])))
	bits := int(le.Uint16(hdr[14:]))
	comp := le.Uint32(hdr[16:])
	used := int(le.Uint32(hdr[32:]))
	if hsize < 40 || hsize > size || w <= 0 || h2 <= 0 || w > maxDIBSide || h2/2 > maxDIBSide || h2 < 2 {
		return nil, errFormat
	}
	h := h2 / 2
	if comp != 0 && !(comp == 3 && bits == 32) {
		return nil, fmt.Errorf("goimgtools: ico: compressed bitmaps (%d) are not supported", comp)
	}
	var pal []color.NRGBA
	off := hsize
	if comp == 3 {
		off += 12 // the three masks of BI_BITFIELDS, BGR order taken as it is
	}
	switch bits {
	case 1, 4, 8:
		n := 1 << bits
		if used > 0 && used < n {
			n = used
		}
		buf := make([]byte, 4*n)
		if _, err := r.ReadAt(buf, off); err != nil {
			return nil, errFormat
		}
		for i := 0; i < n; i++ {
			pal = append(pal, color.NRGBA{buf[4*i+2], buf[4*i+1], buf[4*i], 255})
		}
		off += int64(4 * n)
	case 24, 32:
	default:
		return nil, fmt.Errorf("goimgtools: ico: %d-bit bitmaps are not supported", bits)
	}
	row := (w*bits + 31) / 32 * 4
	maskRow := (w + 31) / 32 * 4
	// The image is held until the source is closed (held); the rows as
	// stored only while they are converted.
	img, rows := int64(4*w*h), int64((row+maskRow)*h)
	if err := env.Budget.Reserve(img+rows, "ico image"); err != nil {
		return nil, err
	}
	defer env.Budget.Release(rows)
	ok := false
	defer func() {
		if !ok {
			env.Budget.Release(img)
		}
	}()
	pix := make([]byte, row*h)
	if _, err := r.ReadAt(pix, off); err != nil {
		return nil, errFormat
	}
	mask := make([]byte, maskRow*h)
	// Some icons end without the mask of a 32-bit image; it is all clear.
	if _, err := r.ReadAt(mask, off+int64(len(pix))); err != nil && bits != 32 {
		return nil, errFormat
	}
	m := image.NewNRGBA(image.Rect(0, 0, w, h))
	anyAlpha := false
	for y := 0; y < h; y++ {
		src := pix[(h-1-y)*row:][:row]
		dst := m.Pix[y*m.Stride:]
		for x := 0; x < w; x++ {
			var c color.NRGBA
			switch bits {
			case 32:
				c = color.NRGBA{src[4*x+2], src[4*x+1], src[4*x], src[4*x+3]}
				anyAlpha = anyAlpha || c.A != 0
			case 24:
				c = color.NRGBA{src[3*x+2], src[3*x+1], src[3*x], 255}
			default:
				i := int(src[x*bits/8]>>(8-bits-(x*bits)%8)) & (1<<bits - 1)
				if i < len(pal) {
					c = pal[i]
				} else {
					c = color.NRGBA{A: 255}
				}
			}
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = c.R, c.G, c.B, c.A
		}
	}
	ok = true
	if bits == 32 && anyAlpha {
		return m, nil
	}
	for y := 0; y < h; y++ {
		mr := mask[(h-1-y)*maskRow:][:maskRow]
		dst := m.Pix[y*m.Stride:]
		for x := 0; x < w; x++ {
			if mr[x/8]>>(7-x%8)&1 == 1 {
				dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = 0, 0, 0, 0
			} else {
				dst[4*x+3] = 255
			}
		}
	}
	return m, nil
}

// Image is one image of an icon to write: its size and its PNG file.
type Image struct {
	W, H int
	PNG  []byte
}

// Encode writes an icon file of the images, smallest first, each at most
// MaxSide a side.
func Encode(w io.Writer, imgs []Image) error {
	if len(imgs) == 0 || len(imgs) > 0xffff {
		return errors.New("goimgtools: ico: no images to write")
	}
	hdr := make([]byte, 6+16*len(imgs))
	binary.LittleEndian.PutUint16(hdr[2:], 1)
	binary.LittleEndian.PutUint16(hdr[4:], uint16(len(imgs)))
	off := int64(len(hdr))
	for i, im := range imgs {
		if im.W < 1 || im.H < 1 || im.W > MaxSide || im.H > MaxSide {
			return fmt.Errorf("goimgtools: ico: an icon of %dx%d (at most %dx%d)", im.W, im.H, MaxSide, MaxSide)
		}
		e := hdr[6+16*i:]
		e[0], e[1] = byte(im.W), byte(im.H) // 256 is written as 0
		binary.LittleEndian.PutUint16(e[4:], 1)
		binary.LittleEndian.PutUint16(e[6:], 32)
		binary.LittleEndian.PutUint32(e[8:], uint32(len(im.PNG)))
		binary.LittleEndian.PutUint32(e[12:], uint32(off))
		off += int64(len(im.PNG))
		if off > 1<<32-1 {
			return errors.New("goimgtools: ico: file too large")
		}
	}
	if _, err := w.Write(hdr); err != nil {
		return err
	}
	for _, im := range imgs {
		if _, err := w.Write(im.PNG); err != nil {
			return err
		}
	}
	return nil
}
