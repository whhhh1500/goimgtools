package pipeline

import (
	"fmt"
	"io"
	"os"
)

// Scratch is a fixed-size, zero-initialised, random-access byte store. It
// lives in memory when the budget has room for all of it and in a temporary
// file otherwise.
type Scratch struct {
	env  *Env
	mem  []byte
	f    *os.File
	size int64
}

// NewScratch returns a store of size bytes, all zero.
func NewScratch(env *Env, size int64) (*Scratch, error) {
	s := &Scratch{env: env, size: size}
	if size == int64(int(size)) && env.Budget.TryReserve(size) {
		s.mem = make([]byte, size)
		return s, nil
	}
	f, err := os.CreateTemp(env.TempDir, spillPrefix)
	if err != nil {
		return nil, fmt.Errorf("goimgtools: create temp file: %w", err)
	}
	if err := f.Truncate(size); err != nil {
		f.Close()
		os.Remove(f.Name())
		return nil, fmt.Errorf("goimgtools: size temp file: %w", err)
	}
	s.f = f
	return s, nil
}

// OnDisk reports whether the store is a temporary file.
func (s *Scratch) OnDisk() bool { return s.f != nil }

// Size is the store's size in bytes.
func (s *Scratch) Size() int64 { return s.size }

func (s *Scratch) check(n int, off int64) error {
	if off < 0 || off+int64(n) > s.size {
		return fmt.Errorf("goimgtools: scratch access [%d,%d) outside %d bytes", off, off+int64(n), s.size)
	}
	return nil
}

// ReadAt fills p from offset off; the range must lie inside the store.
func (s *Scratch) ReadAt(p []byte, off int64) (int, error) {
	if err := s.check(len(p), off); err != nil {
		return 0, err
	}
	if s.f == nil {
		return copy(p, s.mem[off:]), nil
	}
	n, err := s.f.ReadAt(p, off)
	if err == io.EOF && n == len(p) {
		err = nil
	}
	return n, err
}

// WriteAt stores p at offset off; the range must lie inside the store.
func (s *Scratch) WriteAt(p []byte, off int64) (int, error) {
	if err := s.check(len(p), off); err != nil {
		return 0, err
	}
	if s.f == nil {
		return copy(s.mem[off:], p), nil
	}
	return s.f.WriteAt(p, off)
}

// Close frees the memory or deletes the temporary file.
func (s *Scratch) Close() error {
	if s.mem != nil {
		s.mem = nil
		s.env.Budget.Release(s.size)
	}
	if s.f != nil {
		name := s.f.Name()
		err := s.f.Close()
		if rerr := os.Remove(name); err == nil {
			err = rerr
		}
		s.f = nil
		return err
	}
	return nil
}
