// Package libjxl reads and writes JPEG XL with libjxl (with highway,
// brotli and skcms; BSD and Apache licences, in licenses/ of this module),
// compiled to WebAssembly and translated to Go by wasm2go
// (github.com/ncruces/wasm2go): module.go is that translation, and build/
// holds what it was made from and how. It is plain Go, without cgo or a
// WebAssembly runtime: nothing is compiled when a program starts, and the
// output is the same on every processor.
package libjxl

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// module is one instance of the WebAssembly module, used for one file and
// dropped: its memory grows to what the file needs and goes with it.
type module struct{ m *Module }

func newModule() *module {
	m := New(&host{})
	m.X_initialize()
	return &module{m: m}
}

// call runs fn, turning a trap of the module (a Go panic in the
// translation: an access outside its memory, unreachable code reached, an
// abort, an exit) into an error.
func call(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("libjxl: %v", r)
		}
	}()
	return fn()
}

// alloc copies b into the module's memory; 0 for an empty b.
func (m *module) alloc(b []byte) int32 {
	if len(b) == 0 {
		return 0
	}
	p := m.m.Xmalloc(int32(len(b)))
	if p == 0 {
		panic("out of memory")
	}
	copy(m.m.memory[p:], b)
	return p
}

// room makes room in the module's memory for n bytes more at once: it grows
// by append, which holds the old array and the new at each step.
func (m *module) room(n int64) {
	n = min(n, 4<<30-int64(len(m.m.memory)))
	if n > int64(cap(m.m.memory)-len(m.m.memory)) {
		m.m.memory = slices.Grow(m.m.memory, int(n))
	}
}

// Image is a decoded JPEG XL: its first frame.
type Image struct {
	Width, Height int
	// Depth is the bit depth of the samples (for floating point, of their
	// mantissa and exponent together). Pix holds 8-bit samples up to 8,
	// 16-bit (big endian) above.
	Depth int
	Float bool
	Alpha bool
	Gray  bool
	// Pix is RGBA, the colour not multiplied by alpha, Stride bytes a row:
	// upright (the file's orientation applied, Width and Height those of
	// the upright image).
	Pix      []byte
	Stride   int
	Animated bool
	// ICC is the profile of the pixels, nil for sRGB.
	ICC, Exif, XMP []byte
}

// What dec_get answers (shim.c).
const (
	getWidth = iota
	getHeight
	getDepth
	getAlpha
	getGray
	getSRGB
	getOrientation
	getAnimated
	getStride
	getICCSize
	getExifSize
	getXMPSize
	getFloat
)

var decodeErrors = [...]string{1: "out of memory", 2: "not a valid file", 3: "the file ends early"}

// configCapMB is what libjxl may allocate to read a file's description:
// its headers, colour profile and boxes.
const configCapMB = 64

// Decode reads data: the description and metadata, and the pixels unless
// configOnly.
func Decode(data []byte, configOnly bool) (*Image, error) {
	var im *Image
	err := call(func() error {
		m := newModule()
		in := m.alloc(data)
		res := m.m.Xmalloc(4)
		if in == 0 || res == 0 {
			return errors.New("libjxl: out of memory")
		}
		// What libjxl may allocate: for the description, little; for the
		// pixels, about four times what it was measured to take.
		only, capMB := int32(0), int32(configCapMB)
		if configOnly {
			only = 1
		} else if d := m.m.Xdec_open(in, int32(len(data)), 1, configCapMB, res); d != 0 {
			// The size first, and room made for what libjxl takes to decode
			// (measured on photographs of 4.3 megapixels: 9 bytes a pixel, 18
			// without loss, the pixels among them).
			px := int64(m.m.Xdec_get(d, getWidth)) * int64(m.m.Xdec_get(d, getHeight))
			per := int64(20)
			if m.m.Xdec_get(d, getDepth) > 8 {
				per = 28
			}
			m.m.Xdec_close(d)
			m.room(px*per + 8<<20)
			capMB = int32(min((px*per*4+64<<20)>>20, 3800))
		}
		d := m.m.Xdec_open(in, int32(len(data)), only, capMB, res)
		if d == 0 {
			e := binary.LittleEndian.Uint32(m.m.memory[res:])
			if int(e) < len(decodeErrors) && decodeErrors[e] != "" {
				return errors.New("libjxl: " + decodeErrors[e])
			}
			return fmt.Errorf("libjxl: error %d", e)
		}
		get := func(what int32) int { return int(m.m.Xdec_get(d, what)) }
		blob := func(what int32, n int) []byte {
			if n == 0 {
				return nil
			}
			p := m.m.Xdec_blob(d, what)
			return append([]byte(nil), m.m.memory[p:int64(p)+int64(n)]...)
		}
		im = &Image{Width: get(getWidth), Height: get(getHeight), Depth: get(getDepth),
			Float: get(getFloat) != 0, Alpha: get(getAlpha) != 0, Gray: get(getGray) != 0,
			Animated: get(getAnimated) != 0,
			ICC:      blob(0, get(getICCSize)), Exif: blob(1, get(getExifSize)), XMP: blob(2, get(getXMPSize))}
		if !configOnly {
			// The pixels stay where libjxl wrote them: the module is not
			// called again, and its memory goes when they do.
			im.Stride = get(getStride)
			n := int64(im.Stride) * int64(im.Height)
			p := int64(m.m.Xdec_blob(d, 3))
			im.Pix = m.m.memory[p : p+n : p+n]
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return im, nil
}

// Options are the settings of Encode.
type Options struct {
	// Distance is libjxl's butteraugli distance (1 is visually lossless, 0
	// is not allowed: see Lossless); Effort 1 to 10.
	Distance float32
	Effort   int
	Lossless bool
	// Opaque leaves the alpha out.
	Opaque bool
	// Depth16 says Pix holds 16-bit samples, big endian.
	Depth16 bool
	// The metadata to write, if any (Exif a TIFF header and what follows).
	// Without a profile the colours are described as sRGB.
	ICC, Exif, XMP []byte
}

var encodeErrors = [...]string{1: "out of memory", 2: "a setting was refused", 3: "the encoding failed"}

// Encode codes width x height pixels of RGBA (stride 4 x width bytes, 8 for
// 16-bit samples; colour not multiplied by alpha) as a JPEG XL file. The
// pixels are copied: rgba is left as it is.
func Encode(rgba []byte, width, height int, o Options) ([]byte, error) {
	bps := 1
	if o.Depth16 {
		bps = 2
	}
	if width <= 0 || height <= 0 || len(rgba) < 4*bps*width*height {
		return nil, errors.New("libjxl: no pixels")
	}
	var out []byte
	err := call(func() error {
		m := newModule()
		m.room(encodeBytes(width, height, o))
		pix := m.alloc(rgba[:4*bps*width*height])
		icc, exif, xmp := m.alloc(o.ICC), m.alloc(o.Exif), m.alloc(o.XMP)
		res := m.m.Xmalloc(8)
		if pix == 0 || res == 0 {
			return errors.New("libjxl: out of memory")
		}
		b := func(v bool) int32 {
			if v {
				return 1
			}
			return 0
		}
		p := m.m.Xenc(pix, int32(width), int32(height), b(o.Depth16), b(o.Opaque), b(o.Lossless),
			o.Distance, int32(o.Effort), icc, int32(len(o.ICC)), exif, int32(len(o.Exif)),
			xmp, int32(len(o.XMP)), res, res+4)
		if p == 0 {
			e := binary.LittleEndian.Uint32(m.m.memory[res+4:])
			if int(e) < len(encodeErrors) && encodeErrors[e] != "" {
				return errors.New("libjxl: " + encodeErrors[e])
			}
			return fmt.Errorf("libjxl: error %d", e)
		}
		n := binary.LittleEndian.Uint32(m.m.memory[res:])
		out = append([]byte(nil), m.m.memory[p:int64(p)+int64(n)]...)
		return nil
	})
	return out, err
}

// encodeBytes is about the memory libjxl takes to encode. Above 2048 x 2048
// it codes the picture a region of that size at a time, at efforts up to 7:
// what it takes is about some bytes a pixel of the whole and some of at most
// a region, fitted (with a little to spare) to what it took for photographs
// of 0.4, 4.3 and 12 megapixels: at effort 7 93, 65 and 37 bytes a pixel, at
// effort 3 84, 54 and 26, without loss 89 and 43 at 0.4 and 4.3. At efforts 8
// and 9 it holds the whole (310 bytes a pixel at 9 for 0.4 megapixels).
func encodeBytes(width, height int, o Options) int64 {
	px := int64(width) * int64(height)
	whole, region := int64(23), int64(42)
	switch {
	case o.Effort >= 9:
		whole, region = 320, 0
	case o.Effort == 8:
		whole, region = 160, 0
	case o.Lossless:
		whole, region = 13, 30
	case o.Effort <= 3:
		whole, region = 11, 42
	}
	if o.Depth16 {
		whole += 8
	}
	return 16<<20 + px*whole + min(px, 2048*2048)*region
}

// host is what the module imports from WASI: the C library asks for the
// time and writes messages, and is given nothing else. The time is always
// 0 and messages are dropped, so nothing outside the arguments changes the
// output.
type host struct{ m *Module }

// Init is called by New with the module.
func (h *host) Init(m any) { h.m = m.(*Module) }

const errnoBadf = 8

func (h *host) Xclock_time_get(id int32, precision int64, at int32) int32 {
	binary.LittleEndian.PutUint64(h.m.memory[at:], 0)
	return 0
}

func (h *host) Xfd_close(fd int32) int32          { return errnoBadf }
func (h *host) Xfd_fdstat_get(fd, at int32) int32 { return errnoBadf }
func (h *host) Xfd_seek(fd int32, off int64, wh, at int32) int32 {
	return errnoBadf
}

// Xfd_write takes the bytes and drops them.
func (h *host) Xfd_write(fd, iovs, n, written int32) int32 {
	total := uint32(0)
	for i := range n {
		total += binary.LittleEndian.Uint32(h.m.memory[iovs+8*i+4:])
	}
	binary.LittleEndian.PutUint32(h.m.memory[written:], total)
	return 0
}

func (h *host) Xproc_exit(code int32) { panic(fmt.Sprintf("exit %d", code)) }
