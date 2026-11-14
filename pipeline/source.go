package pipeline

import (
	"image"
	"image/color"
)

// Source streams an image from top to bottom as horizontal strips.
//
// Every strip spans the full width of Bounds and a contiguous run of rows, in
// the same absolute coordinates as Bounds (Bounds.Min need not be zero, as
// with a cropped image). Strips arrive in increasing row order and together
// cover Bounds exactly once. A strip is only valid until the next call to
// Next or Close; consumers that need rows for longer must copy them.
//
// Next returns io.EOF after the last strip. A decoder returns its final strip
// only after the whole input has been verified (checksums, trailing data), so
// a pipeline that reaches io.EOF has seen a complete, valid image. Output
// written before an error must be discarded by the caller.
type Source interface {
	Bounds() image.Rectangle
	ColorModel() color.Model
	Next() (image.Image, error)
	Close() error
	// Env is the memory budget and temporary directory of the pipeline
	// the source belongs to. It is part of the interface, not an optional
	// extra, so that a stage cannot forget to pass it on: a stage without
	// it would account its buffers against a budget of its own and the
	// memory limit would quietly stop holding.
	Env() *Env
}

// EnvOf returns the Env of s. A source that has none (only test sources
// do) gets a default one.
func EnvOf(s Source) *Env {
	if e := s.Env(); e != nil {
		return e
	}
	return NewEnv(0, "")
}

// StripRows picks how many rows a stage should put in one strip: about 256KB
// per strip, at least one row, and a multiple of 16 when that is possible so
// JPEG MCU rows and chroma subsampling line up.
func StripRows(rowBytes int64, height int) int {
	n := int64(1)
	if rowBytes > 0 {
		n = (256 << 10) / rowBytes
	}
	if n >= 16 {
		n = n / 16 * 16
	}
	n = max(1, min(n, 256))
	return int(min(n, int64(max(height, 1))))
}

// OpaqueSource is implemented by sources that know every pixel is opaque.
type OpaqueSource interface {
	Opaque() bool
}

// IsOpaque reports whether s is known to have no transparency.
func IsOpaque(s Source) bool {
	o, ok := s.(OpaqueSource)
	return ok && o.Opaque()
}
