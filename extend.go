package goimgtools

import (
	"errors"
	"fmt"
	"image"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// This file is where code of your own goes into a recipe: operations
// (Pipe, Pixels), named groups of steps (Apply), inputs (FromImage,
// FromSource) and other ends than a file (Stream, ToImage). Formats are in
// formats.go (RegisterFormat).

// Operation is a step of a recipe that this package does not have. Apply
// is given the image as it is at that point of the chain, as a stream of
// strips, and returns the stream of what the operation makes of it. See
// package pipeline for how such a stream is written, and Pipe for how the
// operation is put into a recipe.
type Operation interface {
	// Apply returns a Source that reads src. It must not read from src
	// itself: making the stage is cheap, the pixels flow when the result
	// is asked for them. The Source returned closes src when it is closed.
	// When Apply returns an error, src stays the caller's to close.
	//
	// Apply is called once for every run of the recipe (and, with
	// Animated, for every frame), possibly from several goroutines at
	// once, so what belongs to one run is kept in the Source it returns,
	// not in the Operation.
	Apply(src pipeline.Source) (pipeline.Source, error)
}

// OperationFunc makes an Operation of a function.
type OperationFunc func(src pipeline.Source) (pipeline.Source, error)

// Apply calls f.
func (f OperationFunc) Apply(src pipeline.Source) (pipeline.Source, error) { return f(src) }

// Pipe adds an operation of your own to the chain, at this place: it gets
// what the operations before it made, and the ones after it get what it
// makes. It may change the size of the image; OutputSize then reports the
// size the operation's Source says it has.
//
//	type border struct{ width int }
//
//	func (b border) Apply(src pipeline.Source) (pipeline.Source, error) { ... }
//
//	goimgtools.Open("in.jpg").Resize(800, 0).Pipe(border{8}).ToFile("out.jpg")
func (m *Image) Pipe(op Operation) *Image {
	if op == nil {
		return m.fail(errors.New("goimgtools: Pipe needs an operation"))
	}
	return m.add(customOp{op: op})
}

// Pixels adds an operation that changes pixel values and nothing else: a
// colour adjustment, a filter that looks at one pixel at a time. fn is
// called with every strip of the image, top to bottom, as 8-bit NRGBA
// (red, green, blue, alpha, a byte each, colour not multiplied by alpha)
// whatever the image is stored as, and changes it in place. strip.Rect
// says which rows it holds. An error of fn ends the run with that error.
//
//	// Greyscale.
//	img.Pixels(func(strip *image.NRGBA) error {
//		for i := 0; i < len(strip.Pix); i += 4 {
//			p := strip.Pix[i : i+4]
//			y := uint8((299*int(p[0]) + 587*int(p[1]) + 114*int(p[2])) / 1000)
//			p[0], p[1], p[2] = y, y, y
//		}
//		return nil
//	})
//
// fn may be called from several goroutines when the recipe is run several
// times at once. It must not make pixels of an image without transparency
// transparent; an operation that does is written with Pipe and
// pipeline.WithAlpha.
func (m *Image) Pixels(fn func(strip *image.NRGBA) error) *Image {
	if fn == nil {
		return m.fail(errors.New("goimgtools: Pixels needs a function"))
	}
	return m.add(customOp{op: OperationFunc(func(src pipeline.Source) (pipeline.Source, error) {
		return pipeline.Map(src, fn), nil
	}), pixels: true})
}

// customOp is an Operation as a step of the chain.
type customOp struct {
	op     Operation
	pixels bool // made by Pixels
}

func (c customOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	out, err := c.op.Apply(src)
	if err != nil {
		return nil, err
	}
	if out == nil {
		return nil, fmt.Errorf("goimgtools: the operation %T returned neither an image nor an error", c.op)
	}
	return out, nil
}

// Apply runs each of steps on the recipe, in order: a way to give a group
// of steps a name and use it in the middle of a chain.
//
//	// Thumbnail is the house style for small pictures.
//	func Thumbnail(size int) func(*goimgtools.Image) *goimgtools.Image {
//		return func(m *goimgtools.Image) *goimgtools.Image {
//			return m.AutoOrient().Resize(size, size, goimgtools.ResizeOptions{Fit: goimgtools.FitCover})
//		}
//	}
//
//	goimgtools.Open("in.jpg").Apply(Thumbnail(200)).JPEG().ToFile("out.jpg")
//
// A step returns the Image to go on with: normally the one it was given,
// as every method of Image does.
func (m *Image) Apply(steps ...func(*Image) *Image) *Image {
	for _, step := range steps {
		if step == nil {
			return m.fail(errors.New("goimgtools: Apply was given a nil step"))
		}
		if next := step(m); next != nil {
			m = next
		}
	}
	return m
}

// FromImage takes the pixels from an image that is already in memory: one
// decoded by another package, drawn by the program, or made by ToImage. The
// image must not be changed while a recipe that reads it runs.
//
// Without an output format the result is written as PNG.
func FromImage(img image.Image) *Image {
	if img == nil {
		return &Image{err: errors.New("goimgtools: FromImage needs an image")}
	}
	return &Image{open: func() (*input, error) {
		return &input{kind: "image", gen: func(env *pipeline.Env) (pipeline.Source, error) {
			return pipeline.FromImage(img, env), nil
		}}, nil
	}}
}

// FromSource takes the pixels from a stream of your own: an image that is
// computed (a gradient, a chart, a map tile), read from somewhere this
// package does not read, or the result of another recipe (Stream). open is
// called at the start of every run, with the memory budget and temporary
// directory of that run, and returns the stream; see package pipeline for
// how one is written.
//
// The recipe can be run as often as open can make a fresh Source.
// Without an output format the result is written as PNG.
func FromSource(open func(env *pipeline.Env) (pipeline.Source, error)) *Image {
	if open == nil {
		return &Image{err: errors.New("goimgtools: FromSource needs a function")}
	}
	return &Image{open: func() (*input, error) {
		return &input{kind: "source", gen: func(env *pipeline.Env) (pipeline.Source, error) {
			src, err := open(env)
			if err == nil && src == nil {
				err = errors.New("goimgtools: the function given to FromSource returned neither an image nor an error")
			}
			return src, err
		}}, nil
	}}
}

// ToFormat selects the output format by name: one of this package's
// ("jpeg", "png", "webp", "gif", "bmp", "tiff") or one added with
// RegisterFormat, also by a file extension ("jpg", ".tif"). It is for a
// format that has no method of its own, and for programs that get the
// format as a string.
//
// options, at most one value, goes to the encoder of the format. For the
// formats of this package it is the options type of the format's method
// (JPEGOptions for "jpeg", PNGOptions for "png", and so on) or nothing; for
// a registered format, whatever its Codec expects.
func (m *Image) ToFormat(name string, options ...any) *Image {
	m.atMostOne("ToFormat", len(options))
	f, ok := FormatByName(name)
	def := formatNamed(f.Name)
	if !ok || def == nil || def.encode == nil {
		return m.fail(fmt.Errorf("goimgtools: cannot write %q (%s)", name, formatList()))
	}
	o := first(options)
	if def.check != nil {
		if err := def.check(o); err != nil {
			return m.fail(err)
		}
	}
	m.format, m.options = def.Name, o
	return m
}

// Stream runs the recipe and returns its result as a stream of strips
// instead of writing a file: for an encoder, a display or an analysis of
// your own. The pixels are made as the stream is read. The stream must be
// closed, which also closes the input; its Meta method (pipeline.MetaOf)
// gives the EXIF block and colour profile the recipe would have written.
//
// env is the memory budget and temporary directory the run uses. Nil gives
// the run its own, from MemoryLimit and TempDir. Inside an Operation or a
// Codec, pass the Env you were given (src.Env()), so that everything in
// one run counts against one limit:
//
//	// An Operation that needs a second image.
//	func (o blend) Apply(src pipeline.Source) (pipeline.Source, error) {
//		mask, err := goimgtools.Open(o.maskFile).Stream(src.Env())
//		...
//	}
//
// The other way round, the operations of this package can be used on a
// stream you have, by making it the input of a recipe:
//
//	small, err := goimgtools.FromSource(func(*pipeline.Env) (pipeline.Source, error) {
//		return src, nil
//	}).Resize(200, 0).Stream(src.Env())
func (m *Image) Stream(env *pipeline.Env) (src pipeline.Source, err error) {
	defer guard(&err)
	if m.ctx != nil {
		if err := m.ctx.Err(); err != nil {
			return nil, err
		}
	}
	out, closer, _, meta, err := m.build(env, false)
	if err != nil {
		return nil, err
	}
	s := &stream{in: closer}
	s.meta = m.outputMeta(meta)
	s.Source, s.meta.ICC = m.manageColour(out, s.meta.ICC, "")
	return s, nil
}

// stream is the result of a recipe together with the input it reads.
type stream struct {
	pipeline.Source
	in   io.Closer
	meta pipeline.Meta
}

func (s *stream) Opaque() bool        { return pipeline.IsOpaque(s.Source) }
func (s *stream) Meta() pipeline.Meta { return s.meta }

func (s *stream) Close() error {
	err := s.Source.Close()
	if cerr := s.in.Close(); err == nil {
		err = cerr
	}
	return err
}

// ToImage runs the recipe and returns the result as one image in memory,
// for code that needs an image.Image.
//
// This is the one place where the whole image is held at once: width ×
// height × 4 bytes for most images (1.5 to 3 for a JPEG that was not
// changed, 8 for 16-bit). It counts against the memory limit like
// everything else, so a large result needs a larger MemoryLimit; an image
// that does not fit gives an ErrMemoryLimit.
func (m *Image) ToImage() (img image.Image, err error) {
	defer guard(&err)
	src, err := m.Stream(nil)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	b, budget := src.Bounds(), pipeline.EnvOf(src).Budget
	for {
		strip, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if img == nil {
			need := pipeline.BandBytes(strip, b)
			if err := budget.Reserve(need, "the whole image (ToImage)"); err != nil {
				return nil, err
			}
			// Held only for the check: once returned the image is the
			// caller's, and the run's budget ends with the run.
			defer budget.Release(need)
			img = pipeline.NewBand(strip, b)
		}
		pipeline.CopyRows(img, strip, strip.Bounds().Min.Y, strip.Bounds().Max.Y)
	}
	if img == nil {
		return nil, io.ErrUnexpectedEOF
	}
	return img, nil
}
