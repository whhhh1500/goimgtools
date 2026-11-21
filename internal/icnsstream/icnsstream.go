// Package icnsstream reads and writes ICNS, the icon file of macOS: a
// list of images of standard sizes, PNG ones (ic07 to ic14, icp4 to icp6,
// as iconutil writes them) and the older run-length compressed RGB ones
// with an 8-bit mask (is32, il32, ih32, it32). Read is the largest image;
// written are PNG images of the sizes asked for.
package icnsstream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"io"

	"github.com/whhhh1500/goimgtools/internal/pngstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

var errFormat = errors.New("goimgtools: icns: not a valid ICNS file")

// Match reports whether head starts an ICNS file.
func Match(head []byte) bool { return len(head) >= 8 && string(head[:4]) == "icns" }

var pngMagic = []byte("\x89PNG\r\n\x1a\n")

// entry is an image of the file.
type entry struct {
	typ    string
	off    int64 // of its data
	n      int64
	side   int
	png    bool
	maskOf string // a legacy RGB image's mask type
}

// legacy are the run-length RGB types, their sides and masks.
var legacy = map[string]struct {
	side int
	mask string
}{"is32": {16, "s8mk"}, "il32": {32, "l8mk"}, "ih32": {48, "h8mk"}, "it32": {128, "t8mk"}}

func readEntries(r io.ReaderAt, size int64) ([]entry, map[string]entry, error) {
	var h [8]byte
	if _, err := r.ReadAt(h[:], 0); err != nil || !Match(h[:]) {
		return nil, nil, errFormat
	}
	end := min(size, int64(binary.BigEndian.Uint32(h[4:])))
	var out []entry
	all := map[string]entry{}
	for off := int64(8); off+8 <= end && len(out) < 1024; {
		if _, err := r.ReadAt(h[:], off); err != nil {
			break
		}
		typ, n := string(h[:4]), int64(binary.BigEndian.Uint32(h[4:]))
		if n < 8 || off+n > end {
			break
		}
		e := entry{typ: typ, off: off + 8, n: n - 8}
		all[typ] = e
		var head [24]byte
		if e.n >= 24 {
			r.ReadAt(head[:], e.off)
		}
		switch {
		case e.n >= 24 && bytes.HasPrefix(head[:], pngMagic):
			w, hh := int(binary.BigEndian.Uint32(head[16:])), int(binary.BigEndian.Uint32(head[20:]))
			if w == hh && w > 0 {
				e.png, e.side = true, w
				out = append(out, e)
			}
		default:
			if l, ok := legacy[typ]; ok {
				e.side, e.maskOf = l.side, l.mask
				out = append(out, e)
			}
		}
		off += n
	}
	if len(out) == 0 {
		return nil, nil, errFormat
	}
	return out, all, nil
}

// best is the entry read: the largest, a PNG one before a legacy one of
// its size.
func best(es []entry) entry {
	b := es[0]
	for _, e := range es[1:] {
		if e.side > b.side || e.side == b.side && e.png && !b.png {
			b = e
		}
	}
	return b
}

// Open decodes the largest image of an ICNS file of size bytes.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	es, all, err := readEntries(r, size)
	if err != nil {
		return nil, err
	}
	e := best(es)
	if e.png {
		return pngstream.Open(io.NewSectionReader(r, e.off, e.n), env)
	}
	s := e.side
	if err := env.Budget.Reserve(int64(4*s*s), "icns image"); err != nil {
		return nil, err
	}
	m, err := decodeLegacy(r, e, all[e.maskOf], s)
	if err != nil {
		env.Budget.Release(int64(4 * s * s))
		return nil, err
	}
	return &held{Source: pipeline.FromImage(m, env), env: env, n: int64(4 * s * s)}, nil
}

// held is a decoded image's source, which gives its memory back when
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

// decodeLegacy decodes a run-length RGB image and its mask: the red plane,
// then the green, then the blue, each in runs: a byte below 128 copies the
// next n+1 bytes, one from 128 repeats the next byte n-125 times.
func decodeLegacy(r io.ReaderAt, e, mask entry, s int) (*image.NRGBA, error) {
	// At most the planes and a byte for every 128 of them, and it32's four.
	if e.n > int64(3*s*s+3*s*s/128+16) {
		return nil, errFormat
	}
	data := make([]byte, e.n)
	if _, err := r.ReadAt(data, e.off); err != nil {
		return nil, errFormat
	}
	if e.typ == "it32" && len(data) >= 4 {
		data = data[4:] // four zero bytes before it
	}
	m := image.NewNRGBA(image.Rect(0, 0, s, s))
	n := s * s
	if len(data) == 3*n {
		// Not compressed: red, green and blue together, as Pillow reads it.
		for i := 0; i < n; i++ {
			m.Pix[4*i], m.Pix[4*i+1], m.Pix[4*i+2] = data[3*i], data[3*i+1], data[3*i+2]
		}
	} else {
		p := 0
		for c := 0; c < 3; c++ {
			for i := 0; i < n; {
				if p >= len(data) {
					return nil, errFormat
				}
				b := int(data[p])
				p++
				if b < 128 {
					k := b + 1
					if p+k > len(data) || i+k > n {
						return nil, errFormat
					}
					for j := 0; j < k; j++ {
						m.Pix[4*(i+j)+c] = data[p+j]
					}
					p += k
					i += k
				} else {
					k := b - 125
					if p >= len(data) || i+k > n {
						return nil, errFormat
					}
					for j := 0; j < k; j++ {
						m.Pix[4*(i+j)+c] = data[p]
					}
					p++
					i += k
				}
			}
		}
	}
	for i := 0; i < n; i++ {
		m.Pix[4*i+3] = 255
	}
	if mask.n == int64(n) {
		a := make([]byte, n)
		if _, err := r.ReadAt(a, mask.off); err == nil {
			for i := 0; i < n; i++ {
				m.Pix[4*i+3] = a[i]
			}
		}
	}
	return m, nil
}

// Sizes are the sides of the images an ICNS file can hold as PNG.
var Sizes = []int{16, 32, 64, 128, 256, 512, 1024}

// typesOf are the types an image of a side is written as: itself, and the
// double of half its side (a Retina image).
var typesOf = map[int][]string{
	16: {"icp4"}, 32: {"icp5", "ic11"}, 64: {"icp6", "ic12"}, 128: {"ic07"},
	256: {"ic08", "ic13"}, 512: {"ic09", "ic14"}, 1024: {"ic10"},
}

// Encode writes PNG images, keyed by their side (one of Sizes), as an ICNS
// file with a table of contents, as iconutil and Pillow write it.
func Encode(w io.Writer, pngs map[int][]byte) error {
	type item struct {
		typ string
		png []byte
	}
	var items []item
	for _, s := range Sizes {
		p, ok := pngs[s]
		if !ok {
			continue
		}
		for _, t := range typesOf[s] {
			items = append(items, item{t, p})
		}
	}
	if len(items) == 0 {
		return errors.New("goimgtools: icns: no image to write")
	}
	total := 8 + 8 + 8*len(items)
	for _, it := range items {
		total += 8 + len(it.png)
	}
	var b bytes.Buffer
	b.WriteString("icns")
	b.Write(binary.BigEndian.AppendUint32(nil, uint32(total)))
	b.WriteString("TOC ")
	b.Write(binary.BigEndian.AppendUint32(nil, uint32(8+8*len(items))))
	for _, it := range items {
		b.WriteString(it.typ)
		b.Write(binary.BigEndian.AppendUint32(nil, uint32(8+len(it.png))))
	}
	if _, err := w.Write(b.Bytes()); err != nil {
		return err
	}
	for _, it := range items {
		var h [8]byte
		copy(h[:], it.typ)
		binary.BigEndian.PutUint32(h[4:], uint32(8+len(it.png)))
		if _, err := w.Write(h[:]); err != nil {
			return err
		}
		if _, err := w.Write(it.png); err != nil {
			return err
		}
	}
	return nil
}
