package pipeline

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
)

const (
	spillChunk  = 1 << 20   // memory-mode allocation unit
	spillBufIO  = 256 << 10 // write buffer once on disk
	spillPrefix = "goimgtools-*.spill"
)

// Spill is an append-then-read byte store. It keeps data in memory while the
// budget allows and moves everything to a temporary file the moment it does
// not, so its memory use stays bounded no matter how much is written.
// Writes must all happen before reads start (ReadAt may be interleaved with
// writes, but it flushes the write buffer first).
type Spill struct {
	env      *Env
	chunks   [][]byte
	reserved int64
	f        *os.File
	bw       *bufio.Writer
	size     int64
	err      error
}

// NewSpill returns an empty spill buffer that draws on env's budget.
func NewSpill(env *Env) *Spill { return &Spill{env: env} }

// Expect tells the spill how many bytes are going to be written in total.
// When that cannot fit in memory the spill goes to disk straight away,
// instead of filling memory up to the limit first. It is only a hint.
func (s *Spill) Expect(total int64) {
	if s.f != nil || s.err != nil || len(s.chunks) != 0 {
		return
	}
	need := (total + spillChunk - 1) / spillChunk * spillChunk
	if s.env.Budget.TryReserve(need) {
		s.env.Budget.Release(need)
		return
	}
	if err := s.toDisk(); err != nil {
		s.err = err
	}
}

// Size is the number of bytes written so far.
func (s *Spill) Size() int64 { return s.size }

// OnDisk reports whether the data has moved to a temporary file.
func (s *Spill) OnDisk() bool { return s.f != nil }

func (s *Spill) Write(p []byte) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	n := len(p)
	for len(p) > 0 {
		if s.f != nil {
			if _, err := s.bw.Write(p); err != nil {
				s.err = err
				return 0, err
			}
			break
		}
		if len(s.chunks) == 0 || len(s.chunks[len(s.chunks)-1]) == spillChunk {
			if !s.env.Budget.TryReserve(spillChunk) {
				if err := s.toDisk(); err != nil {
					s.err = err
					return 0, err
				}
				continue
			}
			s.reserved += spillChunk
			s.chunks = append(s.chunks, make([]byte, 0, spillChunk))
		}
		last := &s.chunks[len(s.chunks)-1]
		k := min(len(p), spillChunk-len(*last))
		*last = append(*last, p[:k]...)
		p = p[k:]
	}
	s.size += int64(n)
	return n, nil
}

func (s *Spill) toDisk() error {
	if err := s.env.Budget.Reserve(spillBufIO, "spill write buffer"); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.env.TempDir, spillPrefix)
	if err != nil {
		s.env.Budget.Release(spillBufIO)
		return fmt.Errorf("goimgtools: create temp file: %w", err)
	}
	s.f = f
	s.bw = bufio.NewWriterSize(f, spillBufIO)
	for _, c := range s.chunks {
		if _, err := s.bw.Write(c); err != nil {
			return err
		}
	}
	s.chunks = nil
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
	return nil
}

// ReadAt reads len(p) bytes at off. It returns io.EOF only when reading past
// the end, like io.ReaderAt.
func (s *Spill) ReadAt(p []byte, off int64) (int, error) {
	if s.err != nil {
		return 0, s.err
	}
	if off < 0 {
		return 0, errors.New("goimgtools: negative spill offset")
	}
	if off >= s.size {
		if len(p) == 0 {
			return 0, nil
		}
		return 0, io.EOF
	}
	want := len(p)
	if rem := s.size - off; int64(want) > rem {
		p = p[:rem]
	}
	if s.f != nil {
		if s.bw.Buffered() > 0 {
			if err := s.bw.Flush(); err != nil {
				s.err = err
				return 0, err
			}
		}
		n, err := s.f.ReadAt(p, off)
		if err == nil && n < want {
			err = io.EOF
		}
		return n, err
	}
	n := 0
	for n < len(p) {
		ci, co := (off+int64(n))/spillChunk, (off+int64(n))%spillChunk
		n += copy(p[n:], s.chunks[ci][co:])
	}
	if n < want {
		return n, io.EOF
	}
	return n, nil
}

// Close frees the memory and deletes the temporary file, if any.
func (s *Spill) Close() error {
	var err error
	if s.f != nil {
		name := s.f.Name()
		err = s.f.Close()
		if rerr := os.Remove(name); err == nil {
			err = rerr
		}
		s.f, s.bw = nil, nil
		s.env.Budget.Release(spillBufIO)
	}
	s.chunks = nil
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
	if s.err == nil {
		s.err = errors.New("goimgtools: spill closed")
	}
	return err
}

// SeqReader reads a region of a Spill front to back through a small buffer,
// so per-row reads do not each become a system call.
type SeqReader struct {
	s        *Spill
	off, end int64
	buf      []byte
	r, w     int
}

// NewSeqReader reads [off, end) of s using a buffer of bufSize bytes (the
// caller accounts for it in the budget).
func NewSeqReader(s *Spill, off, end int64, bufSize int) *SeqReader {
	return &SeqReader{s: s, off: off, end: end, buf: make([]byte, bufSize)}
}

// ReadFull fills p completely or fails.
func (q *SeqReader) ReadFull(p []byte) error {
	for len(p) > 0 {
		if q.r == q.w {
			if q.off >= q.end {
				return io.ErrUnexpectedEOF
			}
			k := int(min(int64(len(q.buf)), q.end-q.off))
			if len(p) >= len(q.buf) { // large read: bypass the buffer
				k = int(min(int64(len(p)), q.end-q.off))
				n, err := q.s.ReadAt(p[:k], q.off)
				q.off += int64(n)
				if err != nil && n < k {
					return err
				}
				p = p[n:]
				continue
			}
			n, err := q.s.ReadAt(q.buf[:k], q.off)
			if err != nil && n < k {
				return err
			}
			q.off += int64(n)
			q.r, q.w = 0, n
		}
		c := copy(p, q.buf[q.r:q.w])
		q.r += c
		p = p[c:]
	}
	return nil
}
