package vp8enc

import (
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// store is an append-then-read byte store for data whose size is not known
// in advance. Small amounts stay in a slice that grows as needed; beyond
// memMax the data moves to a pipeline.Spill, which keeps it in memory or in a
// temporary file depending on the budget. (A Spill by itself reserves
// memory a megabyte at a time, far more than a small image needs.)
type store struct {
	env      *pipeline.Env
	mem      []byte
	memMax   int
	reserved int64
	sp       *pipeline.Spill
	toDisk   bool // what goes beyond the slice goes to a temporary file at once
}

// expect says that n bytes will be written and nothing else should be
// kept in memory: a store for n bytes of at most max goes in one slice
// of that size, a larger one to a temporary file from the start.
func (s *store) expect(n int64, max int) {
	s.toDisk = true
	if n <= int64(max) && s.grow(int(n)) {
		s.memMax = int(n)
		return
	}
	s.memMax = 0
}

// newStore returns an empty store. The encoder has up to four of them at a
// time, so each keeps at most 1/32 of the budget (and at most 1MB) in its
// slice.
func newStore(env *pipeline.Env) *store {
	return &store{env: env, memMax: int(min(max(env.Budget.Limit()/32, 64<<10), 1<<20))}
}

func (s *store) Size() int64 {
	if s.sp != nil {
		return s.sp.Size()
	}
	return int64(len(s.mem))
}

// grow makes room for n bytes in memory, if the budget allows.
func (s *store) grow(n int) bool {
	if n <= cap(s.mem) {
		return true
	}
	c := min(max(4<<10, 2*cap(s.mem), n), s.memMax)
	if !s.env.Budget.TryReserve(int64(c) - s.reserved) {
		return false
	}
	s.reserved = int64(c)
	mem := make([]byte, len(s.mem), c)
	copy(mem, s.mem)
	s.mem = mem
	return true
}

func (s *store) Write(p []byte) (int, error) {
	if s.sp == nil {
		if n := len(s.mem) + len(p); n <= s.memMax && s.grow(n) {
			s.mem = append(s.mem, p...)
			return len(p), nil
		}
		s.sp = pipeline.NewSpill(s.env)
		// A Spill holds on to memory while it can. With a small budget
		// that could starve the other stores, so send it to disk at once.
		if b := s.env.Budget; s.toDisk || b.Limit()-b.Used() < 4<<20 {
			s.sp.Expect(b.Limit() + 1)
		}
		_, err := s.sp.Write(s.mem)
		s.mem = nil
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
		if err != nil {
			return 0, err
		}
	}
	return s.sp.Write(p)
}

// ReadAt fills p from offset off; the range must lie inside the store.
func (s *store) ReadAt(p []byte, off int64) (int, error) {
	if s.sp != nil {
		return s.sp.ReadAt(p, off)
	}
	if off < 0 || off+int64(len(p)) > int64(len(s.mem)) {
		return 0, io.ErrUnexpectedEOF
	}
	return copy(p, s.mem[off:]), nil
}

// settle makes the store safe to read from several goroutines at once:
// a store that moved to a temporary file still holds buffered data, which
// the first read writes out.
func (s *store) settle() error {
	if s.sp == nil || s.sp.Size() == 0 {
		return nil
	}
	var b [1]byte
	_, err := s.sp.ReadAt(b[:], 0)
	return err
}

// rowBuf holds one macroblock row's stream in memory, which is counted in
// the budget: a row's coded size, small next to the image.
type rowBuf struct {
	env      *pipeline.Env
	b        []byte
	reserved int64
	err      error
}

func (r *rowBuf) Write(p []byte) (int, error) {
	if r.err != nil {
		return 0, r.err
	}
	if n := len(r.b) + len(p); n > cap(r.b) {
		c := max(4<<10, 2*cap(r.b), n)
		if err := r.env.Budget.Reserve(int64(c)-r.reserved, "webp row stream"); err != nil {
			r.err = err
			return 0, err
		}
		r.reserved = int64(c)
		b := make([]byte, len(r.b), c)
		copy(b, r.b)
		r.b = b
	}
	r.b = append(r.b, p...)
	return len(p), nil
}

// reset empties the buffer, keeping its memory.
func (r *rowBuf) reset() { r.b, r.err = r.b[:0], nil }

func (r *rowBuf) release() {
	r.env.Budget.Release(r.reserved)
	r.b, r.reserved = nil, 0
}

// Close frees the memory and deletes the temporary file, if any.
func (s *store) Close() {
	if s == nil {
		return
	}
	if s.sp != nil {
		s.sp.Close()
		s.sp = nil
	}
	s.mem = nil
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
}
