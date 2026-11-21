// Package kvazaar codes pictures with Kvazaar, the HEVC encoder of Tampere
// University (github.com/ultravideo/kvazaar, BSD 3-clause licence, in
// licenses/LICENSE-KVAZAAR of this module), compiled to WebAssembly and
// translated to Go by wasm2go (github.com/ncruces/wasm2go): module.go is
// that translation, and build/ holds what it was made from and how. It is
// plain Go, without cgo or a WebAssembly runtime, and gives the same stream
// on every processor (WebAssembly fixes the rounding of every operation,
// and the translation keeps Go from fusing them; the tests check it on
// amd64, 386, arm64, arm, s390x, ppc64le, loong64, riscv64 and mips).
package kvazaar

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// Chroma is the sampling of a picture's colour planes.
type Chroma int

// The samplings Kvazaar codes.
const (
	Chroma400 Chroma = 0 // luma only
	Chroma420 Chroma = 1 // colour at half the width and height
	Chroma444 Chroma = 3 // colour at every sample
)

// ErrOption is returned for an option Kvazaar does not understand.
var ErrOption = errors.New("kvazaar: option not understood")

// errFailed is returned when Kvazaar gives up, for want of memory or on
// what it cannot code.
var errFailed = errors.New("kvazaar: encoding failed")

// errBroken is returned by an Encoder after a fault in its module.
var errBroken = errors.New("kvazaar: the encoder failed before")

// Encoder is one instance of the module, which codes pictures one after
// another; its memory grows to what the largest needed and stays so until
// the Encoder is dropped. It is not safe for use by several goroutines.
type Encoder struct {
	m      *Module
	broken bool
}

// NewEncoder makes an instance of the module.
func NewEncoder() (e *Encoder, err error) {
	defer recoverTrap(&err)
	m := New(&host{})
	m.X_initialize()
	return &Encoder{m: m}, nil
}

// MemorySize is the module's memory.
func (e *Encoder) MemorySize() int { return len(e.m.memory) }

// call runs fn on the module, turning a trap of it (a Go panic in the
// translation: an access outside its memory, unreachable code reached, an
// exit) into an error, so that a fault in it does not end the program; the
// module is not used again after one.
func (e *Encoder) call(fn func() error) (err error) {
	if e.broken {
		return errBroken
	}
	defer func() {
		if r := recover(); r != nil {
			e.broken = true
			err = fmt.Errorf("kvazaar: %v", r)
		}
	}()
	return fn()
}

// Picture is a picture of 8-bit samples in an Encoder's module, filled
// through Plane (and Deltas) and coded once by Encode.
type Picture struct {
	e      *Encoder
	ptr    int32
	width  int
	height int
	chroma Chroma
}

// NewPicture allocates a width x height picture; Kvazaar codes only even
// sizes. The stream says the size given, though Kvazaar codes the picture
// to a multiple of 8 samples each way, which Encode fills by repeating the
// edge samples.
func (e *Encoder) NewPicture(width, height int, c Chroma) (*Picture, error) {
	if width <= 0 || height <= 0 || width > 1<<15 || height > 1<<15 || width%2 != 0 || height%2 != 0 {
		return nil, errFailed
	}
	var p *Picture
	err := e.call(func() error {
		ptr := e.m.Xpicture(int32(c), int32(padded(width)), int32(padded(height)))
		if ptr == 0 {
			return errFailed
		}
		p = &Picture{e: e, ptr: ptr, width: width, height: height, chroma: c}
		return nil
	})
	return p, err
}

// padded is a size rounded up to a multiple of 8.
func padded(n int) int { return (n + 7) &^ 7 }

// planeSize is the size of plane i of a picture of w x h (nothing for the
// colour of 4:0:0).
func (p *Picture) planeSize(i, w, h int) (int, int) {
	if i > 0 {
		switch p.chroma {
		case Chroma400:
			return 0, 0
		case Chroma420:
			return w / 2, h / 2
		}
	}
	return w, h
}

// Plane returns plane i (0 luma, 1 Cb, 2 Cr) and the distance between its
// rows, as many rows as the picture's size gives it. The slice lies in the
// module's memory: it is valid until the next call to the Encoder's other
// methods or to Deltas and Encode.
func (p *Picture) Plane(i int) ([]byte, int) {
	w, h := p.planeSize(i, p.width, p.height)
	if w == 0 || p.e == nil {
		return nil, 0
	}
	var plane []byte
	var stride int
	if p.e.call(func() error {
		at := int(p.e.m.Xplane(p.ptr, int32(i)))
		stride = int(p.e.m.Xstride(p.ptr, int32(i)))
		plane = p.e.m.memory[at : at+(h-1)*stride+w]
		return nil
	}) != nil {
		return nil, 0
	}
	return plane, stride
}

// Deltas gives the picture a map of w x h QP deltas (signed bytes), one for
// each region of that grid over it, to be filled before Encode: with one
// for each 64 x 64 coding tree unit, the QP of each. The slice lies in the
// module's memory, valid as the planes are; nil if out of memory.
func (p *Picture) Deltas(w, h int) []byte {
	if p.e == nil || w <= 0 || h <= 0 {
		return nil
	}
	var m []byte
	p.e.call(func() error {
		if at := int(p.e.m.Xdeltas(p.ptr, int32(w), int32(h))); at != 0 {
			m = p.e.m.memory[at : at+w*h]
		}
		return nil
	})
	return m
}

// pad repeats the edge samples of each plane out to the size coded.
func (p *Picture) pad() {
	m := p.e.m
	for i := range 3 {
		w, h := p.planeSize(i, p.width, p.height)
		pw, ph := p.planeSize(i, padded(p.width), padded(p.height))
		if w == 0 || (w == pw && h == ph) {
			continue
		}
		at := int(m.Xplane(p.ptr, int32(i)))
		stride := int(m.Xstride(p.ptr, int32(i)))
		plane := m.memory[at : at+(ph-1)*stride+pw]
		for y := range h {
			row := plane[y*stride : y*stride+pw]
			for x := w; x < pw; x++ {
				row[x] = row[w-1]
			}
		}
		for y := h; y < ph; y++ {
			copy(plane[y*stride:y*stride+pw], plane[(h-1)*stride:])
		}
	}
}

// Encode codes the picture as one intra picture at the QP, with options
// given as lines of name=value (Kvazaar's command line options, without the
// dashes), and returns the stream (Annex B: start codes and NAL units). The
// picture is freed: it cannot be used afterwards, and its Encoder can make
// another.
func (p *Picture) Encode(qp int, options string) ([]byte, error) {
	if p.e == nil {
		return nil, errFailed
	}
	e := p.e
	p.e = nil
	var stream []byte
	err := e.call(func() error {
		m := e.m
		opts := m.Xmalloc(int32(len(options) + 1))
		size := m.Xmalloc(4)
		if opts == 0 || size == 0 {
			m.Xrelease(p.ptr)
			return errFailed
		}
		defer m.Xfree(opts)
		defer m.Xfree(size)
		copy(m.memory[opts:], options)
		m.memory[int(opts)+len(options)] = 0
		p.e = e
		p.pad()
		p.e = nil
		out := m.Xencode(p.ptr, int32(p.width), int32(p.height), int32(qp), opts, size)
		n := binary.LittleEndian.Uint32(m.memory[size:])
		if out == 0 {
			if n == 1 {
				return ErrOption
			}
			return errFailed
		}
		stream = append([]byte(nil), m.memory[out:int(out)+int(n)]...)
		m.Xfree(out)
		return nil
	})
	return stream, err
}

// host is what the module imports from WASI: Kvazaar's C library asks for
// the time and writes messages, and is given nothing else. The time is
// always 0 and messages are dropped, so nothing outside the arguments
// changes the stream.
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

func (h *host) Xenviron_get(env, buf int32) int32 { return 0 }

func (h *host) Xenviron_sizes_get(count, size int32) int32 {
	binary.LittleEndian.PutUint32(h.m.memory[count:], 0)
	binary.LittleEndian.PutUint32(h.m.memory[size:], 0)
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

// recoverTrap turns a trap of a module that is not kept (see call) into an
// error.
func recoverTrap(err *error) {
	if r := recover(); r != nil {
		*err = fmt.Errorf("kvazaar: %v", r)
	}
}
