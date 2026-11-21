// Package libavif reads and writes AVIF with libavif (dav1d decoding, aom
// encoding, libyuv converting; BSD licences, in licenses/ of this module),
// compiled to WebAssembly and translated to Go by wasm2go
// (github.com/ncruces/wasm2go): module.go is that translation, and build/
// holds what it was made from and how. It is plain Go, without cgo or a
// WebAssembly runtime: nothing is compiled when a program starts, and the
// output is the same on every processor.
package libavif

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
)

// ErrOption is returned for an encoder option aom does not understand.
var ErrOption = errors.New("libavif: option not understood")

// module is one instance of the WebAssembly module, used for one file and
// dropped: its memory grows to what the file needs and goes with it.
type module struct{ m *Module }

func newModule() *module {
	m := New(&host{})
	m.X_initialize()
	return &module{m: m}
}

// call runs fn, turning a trap of the module (a Go panic in the
// translation: an access outside its memory, unreachable code reached,
// aom's longjmp on an error, an exit) into an error.
func call(fn func() error) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("libavif: %v", r)
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

// Image is a decoded AVIF: its first image.
type Image struct {
	Width, Height int
	// Depth is the bit depth of the samples: 8, 10 or 12. Pix holds 8-bit
	// samples for 8, 16-bit (big endian) otherwise.
	Depth int
	Alpha bool
	// Pix is RGBA, the colour not multiplied by alpha, Stride bytes a row.
	Pix    []byte
	Stride int
	// The transform flags (AVIF_TRANSFORM_*), irot's angle (anticlockwise
	// quarter turns) and imir's axis (0 top and bottom swapped, 1 left and
	// right), and clap's fractions (width, height, horizontal and vertical
	// offset, each a numerator and a denominator).
	Transform int
	Angle     int
	Axis      int
	Clap      [8]uint32
	// The colour description (ISO/IEC 23091-2 code points).
	Primaries, Transfer, Matrix int
	FullRange                   bool
	// The metadata, if any.
	ICC, Exif, XMP []byte
	// Images is how many images the file has (more for a sequence).
	Images int
}

// The transform flags of Image.Transform.
const (
	TransformPasp = 1 << 0
	TransformClap = 1 << 1
	TransformIrot = 1 << 2
	TransformImir = 1 << 3
)

// Decode reads data: the description, and the pixels unless configOnly.
func Decode(data []byte, configOnly bool) (*Image, error) {
	var im *Image
	err := call(func() error {
		m := newModule()
		in := m.alloc(data)
		res := m.m.Xmalloc(8)
		if in == 0 || res == 0 {
			return errors.New("libavif: out of memory")
		}
		d := m.m.Xdec_open(in, int32(len(data)), res)
		if d == 0 {
			return fmt.Errorf("libavif: %s", resultString(int32(binary.LittleEndian.Uint32(m.m.memory[res:]))))
		}
		get := func(what int32) uint32 { return uint32(m.m.Xdec_get(d, what)) }
		blob := func(what, size int32) []byte {
			n := get(size)
			if n == 0 {
				return nil
			}
			p := m.m.Xdec_blob(d, what)
			return append([]byte(nil), m.m.memory[p:int64(p)+int64(n)]...)
		}
		im = &Image{Width: int(get(0)), Height: int(get(1)), Depth: int(get(2)), Alpha: get(3) != 0,
			Transform: int(get(4)), Angle: int(get(5)), Axis: int(get(6)),
			Primaries: int(get(7)), Transfer: int(get(8)), Matrix: int(get(9)), FullRange: get(10) != 0,
			Images: int(get(12)), ICC: blob(0, 21), Exif: blob(1, 22), XMP: blob(2, 23)}
		for i := range im.Clap {
			im.Clap[i] = get(int32(13 + i))
		}
		if configOnly {
			m.m.Xdec_close(d)
			return nil
		}
		depth16 := int32(0)
		if im.Depth > 8 {
			depth16 = 1
		}
		// The memory libavif and dav1d go on to take, made room for at once
		// (6.5 bytes a pixel for a photograph at 8 bits, measured): the
		// module's memory grows by append, which holds the old array and
		// the new at each step, and a 12-megapixel image peaked at 180MB.
		per := int64(7)
		if depth16 != 0 {
			per = 13
		}
		more := min(int64(im.Width)*int64(im.Height)*per+8<<20, 4<<30-int64(len(m.m.memory)))
		m.m.memory = slices.Grow(m.m.memory, int(max(more, 0)))
		pix := m.m.Xdec_pixels(d, depth16, res, res+4)
		if pix == 0 {
			return fmt.Errorf("libavif: %s", resultString(int32(binary.LittleEndian.Uint32(m.m.memory[res+4:]))))
		}
		im.Stride = int(binary.LittleEndian.Uint32(m.m.memory[res:]))
		n := int64(im.Stride) * int64(im.Height)
		// The pixels stay where libavif wrote them: the module is not called
		// again, and its memory goes when they do (a copy was as large again).
		im.Pix = m.m.memory[pix : int64(pix)+n : int64(pix)+n]
		if depth16 != 0 {
			// libavif's 16-bit samples are in the machine's order, which is
			// little endian in WebAssembly; image.NRGBA64 wants big endian.
			for i := 0; i+1 < len(im.Pix); i += 2 {
				im.Pix[i], im.Pix[i+1] = im.Pix[i+1], im.Pix[i]
			}
		}
		m.m.Xdec_close(d)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return im, nil
}

// Options are the settings of Encode.
type Options struct {
	Quality, QualityAlpha int // 0 to 100
	Speed                 int // aom's, 0 (slowest) to 10
	Chroma444             bool
	Lossless              bool // G, B and R coded 4:4:4 without loss
	// Opaque leaves the alpha out.
	Opaque bool
	// The metadata to write, if any. Without a profile the colours are
	// described as sRGB.
	ICC, Exif, XMP []byte
	// CodecOptions are aom's options (avifenc's -a), lines of name=value.
	CodecOptions string
}

// Encode codes width x height pixels of 8-bit RGBA (stride 4 x width,
// colour not multiplied by alpha) as one AVIF image.
func Encode(rgba []byte, width, height int, o Options) ([]byte, error) {
	if width <= 0 || height <= 0 || len(rgba) < 4*width*height {
		return nil, errors.New("libavif: no pixels")
	}
	var out []byte
	err := call(func() error {
		m := newModule()
		// The memory aom goes on to take, made room for at once (see
		// Decode): measured on a photograph of 4.3 megapixels, 28 bytes a
		// pixel at speed 6 and 44 at speed 4.
		per := int64(30)
		if o.Speed <= 5 {
			per = 46
		}
		more := min(int64(width)*int64(height)*(per+4)+8<<20, 4<<30-int64(len(m.m.memory)))
		m.m.memory = slices.Grow(m.m.memory, int(max(more, 0)))
		pix := m.alloc(rgba[:4*width*height])
		icc, exif, xmp := m.alloc(o.ICC), m.alloc(o.Exif), m.alloc(o.XMP)
		opts := m.alloc(append([]byte(o.CodecOptions), 0))
		res := m.m.Xmalloc(8)
		if pix == 0 || opts == 0 || res == 0 {
			return errors.New("libavif: out of memory")
		}
		yuv := int32(3) // AVIF_PIXEL_FORMAT_YUV420
		if o.Chroma444 {
			yuv = 1 // AVIF_PIXEL_FORMAT_YUV444
		}
		b := func(v bool) int32 {
			if v {
				return 1
			}
			return 0
		}
		p := m.m.Xenc(pix, int32(width), int32(height), b(o.Opaque), yuv, b(o.Lossless),
			int32(o.Quality), int32(o.QualityAlpha), int32(o.Speed),
			icc, int32(len(o.ICC)), exif, int32(len(o.Exif)), xmp, int32(len(o.XMP)), opts, res, res+4)
		if p == 0 {
			// -1: the shim found a line it could not read; 19:
			// AVIF_RESULT_INVALID_CODEC_SPECIFIC_OPTION, from aom.
			r := int32(binary.LittleEndian.Uint32(m.m.memory[res+4:]))
			if r == -1 || r == 19 {
				return ErrOption
			}
			return fmt.Errorf("libavif: %s", resultString(r))
		}
		n := binary.LittleEndian.Uint32(m.m.memory[res:])
		out = append([]byte(nil), m.m.memory[p:int64(p)+int64(n)]...)
		return nil
	})
	return out, err
}

// resultString names libavif's result codes (avifResult).
func resultString(r int32) string {
	names := []string{"ok", "unknown error", "invalid ftyp", "no content", "no YUV format selected",
		"reformat failed", "unsupported depth", "encoding of color planes failed",
		"encoding of alpha plane failed", "BMFF parsing failed", "missing or empty image item",
		"decoding of color planes failed", "decoding of alpha plane failed",
		"color and alpha planes size mismatch", "plane sizes don't match ispe values",
		"no codec available", "no images remaining", "invalid Exif payload",
		"invalid image grid", "invalid codec-specific option", "truncated data", "IO not set",
		"IO error", "waiting on IO", "invalid argument", "not implemented", "out of memory",
		"cannot change some setting during encoding", "the image is incompatible with already encoded images",
		"internal error", "encoding of gain map planes failed", "decoding of gain map planes failed",
		"invalid tone mapped image item"}
	if r >= 0 && int(r) < len(names) {
		return names[r]
	}
	return fmt.Sprintf("result %d", r)
}

// host is what the module imports from WASI: the C library asks for the
// time and writes messages, and is given nothing else. The time is always
// 0 and messages are dropped, so nothing outside the arguments changes the
// output.
type host struct{ m *Module }

// Init is called by New with the module.
func (h *host) Init(m any) { h.m = m.(*Module) }

const (
	errnoBadf  = 8
	errnoNocap = 76
)

func (h *host) Xclock_time_get(id int32, precision int64, at int32) int32 {
	binary.LittleEndian.PutUint64(h.m.memory[at:], 0)
	return 0
}

func (h *host) Xfd_close(fd int32) int32                   { return errnoBadf }
func (h *host) Xfd_fdstat_get(fd, at int32) int32          { return errnoBadf }
func (h *host) Xfd_fdstat_set_flags(fd, flags int32) int32 { return errnoBadf }
func (h *host) Xfd_prestat_dir_name(fd, at, n int32) int32 { return errnoBadf }
func (h *host) Xfd_prestat_get(fd, at int32) int32         { return errnoBadf }
func (h *host) Xfd_read(fd, iovs, n, read int32) int32     { return errnoBadf }
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

func (h *host) Xpath_open(fd, dirflags, path, n, oflags int32, rights, inherit int64, fdflags, at int32) int32 {
	return errnoNocap
}

func (h *host) Xproc_exit(code int32) { panic(fmt.Sprintf("exit %d", code)) }
