package svg

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Source is an SVG document drawn a band of rows at a time.
type Source struct {
	env       *pipeline.Env
	root      *node
	iw, ih    float64 // its intrinsic size
	m         matrix  // its user space to that size
	w, h      int     // the pixels drawn
	r         *renderer
	y         int
	reserved  int64
	err       error
	started   bool
	dataBytes int64
}

// MaxBytes bounds the documents read.
const MaxBytes = 64 << 20

// Open reads an SVG document. Its size is that of its width and height
// (or its viewBox), in pixels: lengths in physical units count 96 to the
// inch.
func Open(data []byte, env *pipeline.Env) (*Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	if len(data) > MaxBytes {
		return nil, errors.New("goimgtools: svg: the document is larger than 64MB")
	}
	// The tree: its text, and its elements as they are read.
	need := int64(len(data))
	if err := env.Budget.Reserve(need, "svg document"); err != nil {
		return nil, err
	}
	root, err := parse(data, func(n int64) error {
		if err := env.Budget.Reserve(n, "svg document"); err != nil {
			return err
		}
		need += n
		return nil
	})
	if err != nil {
		env.Budget.Release(need)
		return nil, err
	}
	s := &Source{env: env, root: root, reserved: need, dataBytes: need}
	s.iw, s.ih, s.m = size(root)
	s.w = max(1, int(math.Round(s.iw)))
	s.h = max(1, int(math.Round(s.ih)))
	return s, nil
}

// ScaleOnLoad draws the document at w by h pixels instead of its own size,
// as sharp's density and resize do it at once: a vector is drawn sharp at
// any size. It reports false once drawing has begun.
func (s *Source) ScaleOnLoad(w, h int) bool {
	if s.started || w < 1 || h < 1 {
		return false
	}
	s.w, s.h = w, h
	return true
}

func (s *Source) Env() *pipeline.Env      { return s.env }
func (s *Source) Bounds() image.Rectangle { return image.Rect(0, 0, s.w, s.h) }
func (s *Source) ColorModel() color.Model { return color.RGBAModel }

func (s *Source) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	s.r, s.root = nil, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: svg: closed")
	}
	return nil
}

func (s *Source) start() error {
	s.started = true
	m := matrix{float64(s.w) / s.iw, 0, 0, float64(s.h) / s.ih, 0, 0}.mul(s.m)
	reserve := func(n int64) error {
		if err := s.env.Budget.Reserve(n, "svg scene"); err != nil {
			return err
		}
		return nil
	}
	avail := func() int64 { return s.env.Budget.Limit() - s.env.Budget.Used() }
	if s.env.Budget.Limit() <= 0 {
		avail = nil
	}
	sc, err := build(s.root, s.w, s.h, m, reserve, s.env.Budget.Release, avail)
	if sc != nil {
		// Reserved as the scene grew, and for its pictures: given back at
		// Close, the build failed or not.
		s.reserved += sc.charged + sc.imageBytes
	}
	if err != nil {
		return err
	}
	// The tree is no longer needed; the scene takes about 40 bytes a
	// point of its outlines.
	s.env.Budget.Release(s.dataBytes)
	s.reserved -= s.dataBytes
	s.root = nil
	layers := layersNeeded(sc.items)
	n := pipeline.StripRows(4*int64(s.w)*int64(layers)+4*int64(s.w), s.h)
	need := int64(n) * (4*int64(s.w)*int64(layers) + 4*int64(s.w))
	if err := s.env.Budget.Reserve(need, "svg scene"); err != nil {
		return err
	}
	s.reserved += need
	r := &renderer{sc: sc, n: n, cov: make([]uint8, s.w*n), ccov: make([]uint8, s.w*n), tmp: make([]uint8, s.w*n), mcov: make([]uint8, s.w*n)}
	for i := 0; i < layers; i++ {
		r.pool = append(r.pool, make([]uint8, 4*s.w*n))
	}
	s.r = r
	return nil
}

func (s *Source) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	if !s.started {
		if err := s.start(); err != nil {
			s.err = err
			return nil, err
		}
	}
	if s.y >= s.h {
		return nil, io.EOF
	}
	n := min(s.r.n, s.h-s.y)
	pix := s.r.band(s.y, n)
	out := &image.RGBA{Pix: pix, Stride: 4 * s.w, Rect: image.Rect(0, s.y, s.w, s.y+n)}
	s.y += n
	return out, nil
}
