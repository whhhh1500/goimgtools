package pngstream

import (
	"encoding/binary"
	"hash"
	"hash/crc32"
	"image"
	"io"
	"time"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// AnimReader reads the frames of an APNG file one by one.
type AnimReader struct {
	r      io.ReaderAt
	size   int64
	env    *pipeline.Env
	w, h   int
	ihdr   [13]byte // the image's header, for the frames'
	shared []byte   // the PLTE and tRNS chunks, whole, which every frame shares
	pos    int64    // the next chunk to look at
	frames int      // fcTL chunks read
}

// FrameInfo is how a frame goes on the canvas.
type FrameInfo struct {
	// Rect is where the frame goes.
	Rect image.Rectangle
	// Delay is how long the canvas shows once the frame is on it.
	Delay time.Duration
	// Dispose is what happens to the frame's rectangle before the next
	// frame is drawn: 0 nothing, 1 cleared to transparent, 2 put back as
	// it was before the frame.
	Dispose byte
	// Over says that the frame is composited over the canvas; it replaces
	// what it covers otherwise.
	Over bool
}

// OpenAnimation reads the headers of the APNG file r (size bytes) for its
// frames to be read with NextFrame.
func OpenAnimation(r io.ReaderAt, size int64, env *pipeline.Env) (*AnimReader, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	a := &AnimReader{r: r, size: size, env: env, pos: 8}
	var b [8]byte
	seenIHDR := false
	for pos := int64(8); size-pos >= 12; {
		if _, err := r.ReadAt(b[:], pos); err != nil {
			return nil, err
		}
		n := int64(binary.BigEndian.Uint32(b[:4]))
		if n > size-pos-12 {
			return nil, io.ErrUnexpectedEOF
		}
		name := string(b[4:8])
		switch name {
		case "IHDR":
			if n != 13 {
				return nil, FormatError("bad IHDR length")
			}
			if _, err := r.ReadAt(a.ihdr[:], pos+8); err != nil {
				return nil, err
			}
			a.w, a.h = int(binary.BigEndian.Uint32(a.ihdr[0:])), int(binary.BigEndian.Uint32(a.ihdr[4:]))
			seenIHDR = true
		case "PLTE", "tRNS":
			if n > 1<<12 {
				return nil, FormatError("bad " + name + " length")
			}
			c := make([]byte, 12+n)
			if _, err := r.ReadAt(c, pos); err != nil {
				return nil, err
			}
			a.shared = append(a.shared, c...)
		}
		if name == "IDAT" || name == "IEND" {
			break
		}
		pos += 12 + n
	}
	if !seenIHDR || a.w <= 0 || a.h <= 0 {
		return nil, FormatError("missing IHDR")
	}
	return a, nil
}

// Config returns the size of the canvas.
func (a *AnimReader) Config() (width, height int) { return a.w, a.h }

// NextFrame returns the pixels of the next frame, as the PNG decoder gives
// them (from 0, 0, of the frame's size), and how it goes on the canvas;
// io.EOF after the last. The caller closes the frame before asking for the
// next.
func (a *AnimReader) NextFrame() (pipeline.Source, FrameInfo, error) {
	var b [26]byte
	for a.size-a.pos >= 12 {
		if _, err := a.r.ReadAt(b[:8], a.pos); err != nil {
			return nil, FrameInfo{}, err
		}
		n := int64(binary.BigEndian.Uint32(b[:4]))
		if n > a.size-a.pos-12 {
			return nil, FrameInfo{}, io.ErrUnexpectedEOF
		}
		name := string(b[4:8])
		p := a.pos + 8
		a.pos = p + n + 4
		if name == "IEND" {
			a.pos = a.size
			return nil, FrameInfo{}, io.EOF
		}
		if name != "fcTL" {
			continue
		}
		if n < 26 {
			return nil, FrameInfo{}, FormatError("bad fcTL length")
		}
		if _, err := a.r.ReadAt(b[:26], p); err != nil {
			return nil, FrameInfo{}, err
		}
		w, h := binary.BigEndian.Uint32(b[4:]), binary.BigEndian.Uint32(b[8:])
		x, y := binary.BigEndian.Uint32(b[12:]), binary.BigEndian.Uint32(b[16:])
		if w == 0 || h == 0 || uint64(x)+uint64(w) > uint64(a.w) || uint64(y)+uint64(h) > uint64(a.h) {
			return nil, FrameInfo{}, FormatError("fcTL frame outside the image")
		}
		num, den := int64(binary.BigEndian.Uint16(b[20:])), int64(binary.BigEndian.Uint16(b[22:]))
		if den == 0 {
			den = 100
		}
		info := FrameInfo{
			Rect:    image.Rect(int(x), int(y), int(x+w), int(y+h)),
			Delay:   time.Duration(num) * time.Second / time.Duration(den),
			Dispose: b[24],
			Over:    b[25] == 1,
		}
		if info.Dispose > 2 || b[25] > 1 {
			return nil, FrameInfo{}, FormatError("bad fcTL dispose or blend operation")
		}
		if a.frames == 0 && info.Dispose == 2 {
			info.Dispose = 1 // what the APNG specification says of the first frame
		}
		a.frames++
		ihdr := a.ihdr
		binary.BigEndian.PutUint32(ihdr[0:], w)
		binary.BigEndian.PutUint32(ihdr[4:], h)
		fr := &frameStream{a: a, pos: a.pos}
		fr.head = append(fr.head, pngHeader...)
		fr.head = appendChunk(fr.head, "IHDR", ihdr[:])
		fr.head = append(fr.head, a.shared...)
		src, err := Open(fr, a.env)
		if err != nil {
			return nil, FrameInfo{}, err
		}
		return src, info, nil
	}
	return nil, FrameInfo{}, io.EOF
}

// Close lets go of the input.
func (a *AnimReader) Close() error {
	a.r = nil
	return nil
}

// appendChunk appends a PNG chunk.
func appendChunk(dst []byte, name string, data []byte) []byte {
	dst = binary.BigEndian.AppendUint32(dst, uint32(len(data)))
	start := len(dst)
	dst = append(dst, name...)
	dst = append(dst, data...)
	return binary.BigEndian.AppendUint32(dst, crc32.ChecksumIEEE(dst[start:]))
}

// frameStream is a frame of an APNG as a PNG file of its own, made as it is
// read: the signature, the frame's IHDR, the shared PLTE and tRNS, then the
// frame's data chunks (IDAT, or fdAT without its sequence number) as IDAT
// chunks, then IEND.
type frameStream struct {
	a    *AnimReader
	head []byte // what is to be read before the next chunk's data
	pos  int64  // the next chunk of the file to look at
	left int64  // what is left of the current chunk's data
	at   int64  // where it is
	crc  hash.Hash32
	done bool
}

func (f *frameStream) Read(p []byte) (int, error) {
	for len(f.head) == 0 && f.left == 0 {
		if f.crc != nil {
			f.head = binary.BigEndian.AppendUint32(f.head, f.crc.Sum32())
			f.crc = nil
			continue
		}
		if f.done {
			return 0, io.EOF
		}
		if err := f.nextChunk(); err != nil {
			return 0, err
		}
	}
	if len(f.head) > 0 {
		n := copy(p, f.head)
		f.head = f.head[n:]
		return n, nil
	}
	if int64(len(p)) > f.left {
		p = p[:f.left]
	}
	n, err := f.a.r.ReadAt(p, f.at)
	if n > 0 {
		f.crc.Write(p[:n])
		f.at += int64(n)
		f.left -= int64(n)
		return n, nil
	}
	if err == io.EOF || err == nil {
		err = io.ErrUnexpectedEOF
	}
	return 0, err
}

// nextChunk sets up the next data chunk of the frame, or the end.
func (f *frameStream) nextChunk() error {
	var b [8]byte
	for f.a.size-f.pos >= 12 {
		if _, err := f.a.r.ReadAt(b[:], f.pos); err != nil {
			return err
		}
		n := int64(binary.BigEndian.Uint32(b[:4]))
		if n > f.a.size-f.pos-12 {
			return io.ErrUnexpectedEOF
		}
		name := string(b[4:8])
		p := f.pos + 8
		f.pos = p + n + 4
		switch name {
		case "IDAT":
		case "fdAT":
			if n < 4 {
				return FormatError("bad fdAT length")
			}
			p, n = p+4, n-4
		case "fcTL", "IEND":
			f.head = appendChunk(f.head, "IEND", nil)
			f.done = true
			return nil
		default:
			continue
		}
		f.head = binary.BigEndian.AppendUint32(f.head, uint32(n))
		f.head = append(f.head, "IDAT"...)
		f.crc = crc32.NewIEEE()
		f.crc.Write([]byte("IDAT"))
		f.at, f.left = p, n
		return nil
	}
	f.head = appendChunk(f.head, "IEND", nil)
	f.done = true
	return nil
}
