package pngstream

import (
	"bytes"
	"compress/flate"
	"encoding/binary"
	"hash"
	"hash/adler32"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// zBlocks writes a zlib stream the way pigz does, so that it can be
// compressed on several cores: the data is cut into blocks of zBlockBytes,
// each compressed on its own with the 32KB before it as its dictionary,
// and the compressed blocks follow each other in one deflate stream (every
// block but the last ends with a sync flush, the last is final). The
// checksum is over all the data, in order.
//
// The blocks are the same however many helpers the pipeline has (see
// pipeline.Env.Go), and so is the file. Without helpers the blocks are
// compressed as the data comes, straight to the output, with no buffer
// beyond the 32KB of the dictionary; with them, they are gathered and
// compressed on the helpers while the next ones fill. Data of up to one
// block comes out exactly as compress/zlib writes it.
type zBlocks struct {
	w     io.Writer
	env   *pipeline.Env
	level int
	sum   hash.Hash32
	tail  []byte // the last 32KB of the data so far
	n     int64  // data so far
	err   error
	held  int64 // reserved from the budget

	// Without helpers: the compressor of the current block.
	fw *flate.Writer

	// With helpers.
	ways int
	cur  *zBlock   // being filled
	busy []*zBlock // handed out to be compressed, oldest first
	free []*zBlock
}

const (
	zBlockBytes = 256 << 10
	zWindow     = 32 << 10
)

// A zBlock compressed on a helper keeps its output and the sizes of the
// writes the compressor made: written out again in those sizes, the bytes
// are grouped into IDAT chunks as they are when the compressor writes to
// the output itself, without helpers (the buffer that makes the chunks
// passes a large write on whole).
type zBlock struct {
	data   []byte
	dict   []byte
	out    bytes.Buffer
	writes []int
	last   bool
	err    error
	wait   func()
}

// recorder appends to the output of a block and notes each write's size.
type recorder struct{ b *zBlock }

func (r recorder) Write(p []byte) (int, error) {
	r.b.writes = append(r.b.writes, len(p))
	return r.b.out.Write(p)
}

func newZBlocks(w io.Writer, level int, env *pipeline.Env) (*zBlocks, error) {
	z := &zBlocks{w: w, env: env, level: level, sum: adler32.New(), ways: env.Helpers() + 1}
	if err := env.Budget.Reserve(zWindow, "png compression window"); err != nil {
		return nil, err
	}
	z.held = zWindow
	if _, err := z.w.Write(z.header()); err != nil {
		return z, err
	}
	if z.ways == 1 {
		fw, err := flate.NewWriter(z.w, level)
		if err != nil {
			return z, err
		}
		z.fw = fw
	}
	return z, nil
}

// header is that of compress/zlib for the level.
func (z *zBlocks) header() []byte {
	h := []byte{0x78, 0}
	var flags byte
	switch z.level {
	case -2, 0, 1:
		flags = 0
	case 2, 3, 4, 5:
		flags = 1
	case 6, -1:
		flags = 2
	case 7, 8, 9:
		flags = 3
	}
	h[1] = flags << 6
	h[1] += uint8(31 - binary.BigEndian.Uint16(h)%31)
	return h
}

// keepTail remembers the end of the data: the dictionary of the next block.
func (z *zBlocks) keepTail(p []byte) {
	if len(p) >= zWindow {
		z.tail = append(z.tail[:0], p[len(p)-zWindow:]...)
		return
	}
	z.tail = append(z.tail, p...)
	if over := len(z.tail) - zWindow; over > 0 {
		z.tail = append(z.tail[:0], z.tail[over:]...)
	}
}

func (z *zBlocks) Write(p []byte) (int, error) {
	if z.err != nil {
		return 0, z.err
	}
	n := len(p)
	z.sum.Write(p)
	for len(p) > 0 {
		// A full block is ended only when more data comes, so that the last
		// block is the one Close finishes.
		if z.n > 0 && z.n%zBlockBytes == 0 {
			if z.err = z.next(); z.err != nil {
				return 0, z.err
			}
		}
		k := min(len(p), zBlockBytes-int(z.n%zBlockBytes))
		if z.fw != nil {
			if _, z.err = z.fw.Write(p[:k]); z.err != nil {
				return 0, z.err
			}
		} else {
			if z.cur == nil {
				if z.cur, z.err = z.block(); z.err != nil {
					return 0, z.err
				}
			}
			z.cur.data = append(z.cur.data, p[:k]...)
		}
		z.keepTail(p[:k])
		z.n += int64(k)
		p = p[k:]
	}
	return n, nil
}

// next ends the full block. Without helpers: flushed, and a new compressor
// for the next block, with the dictionary; with them, handed to a helper.
func (z *zBlocks) next() error {
	if z.fw != nil {
		if err := z.fw.Flush(); err != nil {
			return err
		}
		fw, err := flate.NewWriterDict(z.w, z.level, z.tail)
		if err != nil {
			return err
		}
		z.fw = fw
		return nil
	}
	return z.send()
}

// block returns an empty block whose dictionary is the end of the data so
// far.
func (z *zBlocks) block() (*zBlock, error) {
	var b *zBlock
	if n := len(z.free); n > 0 {
		b = z.free[n-1]
		z.free = z.free[:n-1]
		b.data, b.writes, b.last, b.err = b.data[:0], b.writes[:0], false, nil
		b.out.Reset()
	} else {
		// The data, its dictionary and about as much compressed again.
		need := int64(2*zBlockBytes + zWindow)
		if err := z.env.Budget.Reserve(need, "png compression block"); err != nil {
			return nil, err
		}
		z.held += need
		b = &zBlock{data: make([]byte, 0, zBlockBytes), dict: make([]byte, 0, zWindow)}
	}
	b.dict = append(b.dict[:0], z.tail...)
	return b, nil
}

// send hands the current block to a helper to be compressed, and writes
// out what is done when too many blocks are waiting.
func (z *zBlocks) send() error {
	b := z.cur
	z.cur = nil
	level := z.level
	b.wait = z.env.Go(func() { b.err = compressBlock(recorder{b}, b.data, b.dict, level, b.last) })
	z.busy = append(z.busy, b)
	for len(z.busy) > z.ways {
		if err := z.drain(); err != nil {
			return err
		}
	}
	return nil
}

func compressBlock(out io.Writer, data, dict []byte, level int, last bool) error {
	var fw *flate.Writer
	var err error
	if len(dict) == 0 {
		fw, err = flate.NewWriter(out, level)
	} else {
		fw, err = flate.NewWriterDict(out, level, dict)
	}
	if err != nil {
		return err
	}
	if _, err := fw.Write(data); err != nil {
		return err
	}
	if last {
		return fw.Close()
	}
	return fw.Flush()
}

// drain waits for the oldest block and writes it out.
func (z *zBlocks) drain() error {
	b := z.busy[0]
	z.busy = z.busy[1:]
	b.wait()
	b.wait = nil
	if b.err != nil {
		return b.err
	}
	out := b.out.Bytes()
	for _, n := range b.writes {
		if _, err := z.w.Write(out[:n]); err != nil {
			return err
		}
		out = out[n:]
	}
	z.free = append(z.free, b)
	return nil
}

// Close ends the last block and writes everything out with the checksum.
func (z *zBlocks) Close() error {
	if z.err != nil {
		return z.err
	}
	if z.fw != nil {
		z.err = z.fw.Close()
	} else {
		if z.cur == nil {
			if z.cur, z.err = z.block(); z.err != nil {
				return z.err
			}
		}
		z.cur.last = true
		if z.err = z.send(); z.err == nil {
			for len(z.busy) > 0 && z.err == nil {
				z.err = z.drain()
			}
		}
	}
	if z.err != nil {
		return z.err
	}
	var sum [4]byte
	binary.BigEndian.PutUint32(sum[:], z.sum.Sum32())
	_, z.err = z.w.Write(sum[:])
	return z.err
}

// release waits for whatever is still being compressed (after an error)
// and gives back the budget.
func (z *zBlocks) release() {
	for _, b := range z.busy {
		if b.wait != nil {
			b.wait()
		}
	}
	z.busy, z.free, z.cur, z.fw = nil, nil, nil, nil
	z.env.Budget.Release(z.held)
	z.held = 0
}
