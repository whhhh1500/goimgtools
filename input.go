package goimgtools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// input is an opened image file: always readable front to back, and
// readable at any offset either directly (a file, a byte slice) or after
// being copied into a spill buffer (a plain io.Reader).
type input struct {
	closer io.Closer
	br     *bufio.Reader
	ra     io.ReaderAt
	size   int64
	spill  *pipeline.Spill
	// at, when set, is asked for random access instead (the Input of a
	// Codec that calls a decoder of this package).
	at func() (io.ReaderAt, int64, error)
	// gen makes the pixels of an input that is not a file (Text,
	// FromImage, FromSource); kind names it in Info.Format.
	gen  func(env *pipeline.Env) (pipeline.Source, error)
	kind string
	// ioErr is the first error (other than the end of the data) that
	// reading the input itself gave. When it is set, whatever the decoder
	// says afterwards is the fault of the reading, not of the image.
	ioErr error
}

// tracked passes reads through and remembers the first that failed.
type tracked struct {
	r  io.Reader
	in *input
}

func (t tracked) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if err != nil && err != io.EOF && t.in.ioErr == nil {
		t.in.ioErr = err
	}
	return n, err
}

type trackedAt struct {
	r  io.ReaderAt
	in *input
}

func (t trackedAt) ReadAt(p []byte, off int64) (int, error) {
	n, err := t.r.ReadAt(p, off)
	if err != nil && err != io.EOF && t.in.ioErr == nil {
		t.in.ioErr = err
	}
	return n, err
}

// classify sorts an error that came out of recognising or decoding the
// input: it is the input's fault (ErrFormat) unless something else is to
// blame, namely the reading itself, the memory limit, or the working
// storage (a temporary file).
func (in *input) classify(err error) error {
	if err == nil || err == io.EOF || in.gen != nil || errors.Is(err, ErrFormat) || errors.Is(err, pipeline.ErrMemoryLimit) {
		return err
	}
	if in.ioErr != nil {
		if errors.Is(err, in.ioErr) {
			return err
		}
		return fmt.Errorf("goimgtools: reading the input: %w", in.ioErr)
	}
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return err
	}
	return fmt.Errorf("%w: %w", ErrFormat, err)
}

// decoded is a decoder's Source with its errors sorted by classify.
type decoded struct {
	pipeline.Source
	in *input
}

func (d *decoded) Next() (image.Image, error) {
	m, err := d.Source.Next()
	if err != nil && err != io.EOF {
		return nil, d.in.classify(err)
	}
	return m, err
}

func (d *decoded) Env() *pipeline.Env  { return pipeline.EnvOf(d.Source) }
func (d *decoded) Opaque() bool        { return pipeline.IsOpaque(d.Source) }
func (d *decoded) Meta() pipeline.Meta { return pipeline.MetaOf(d.Source) }

// Subsampling passes on what a JPEG decoder says about its strips (see
// shrinkFactor); ok is false for every other decoder.
func (d *decoded) Subsampling() (ratio image.YCbCrSubsampleRatio, gray, ok bool) {
	if j, is := d.Source.(interface {
		Subsampling() (image.YCbCrSubsampleRatio, bool, bool)
	}); is {
		return j.Subsampling()
	}
	return 0, false, false
}

func fileInput(path string) (*input, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	in := &input{closer: f, size: st.Size()}
	in.br, in.ra = bufio.NewReaderSize(tracked{f, in}, 64<<10), trackedAt{f, in}
	return in, nil
}

func bytesInput(data []byte) *input {
	r := bytes.NewReader(data)
	return &input{br: bufio.NewReaderSize(r, 64<<10), ra: r, size: int64(len(data))}
}

func readerInput(r io.Reader) *input {
	in := &input{}
	in.br = bufio.NewReaderSize(tracked{r, in}, 64<<10)
	return in
}

// readerAt returns random access to the whole input. A plain reader is
// first copied to a spill buffer, which is a temporary file when the data
// does not fit in the memory budget.
func (in *input) readerAt(env *pipeline.Env) (io.ReaderAt, int64, error) {
	return in.readerAtAfter(env, nil)
}

// readerAtAfter is readerAt for an input of which head has already been
// read front to back.
func (in *input) readerAtAfter(env *pipeline.Env, head []byte) (io.ReaderAt, int64, error) {
	if in.ra != nil {
		return in.ra, in.size, nil
	}
	if in.at != nil {
		ra, size, err := in.at()
		if err != nil {
			return nil, 0, err
		}
		in.ra, in.size = ra, size
		return ra, size, nil
	}
	in.spill = pipeline.NewSpill(env)
	if _, err := in.spill.Write(head); err != nil {
		return nil, 0, err
	}
	if _, err := io.Copy(in.spill, in.br); err != nil {
		return nil, 0, err
	}
	in.ra, in.size = in.spill, in.spill.Size()
	return in.ra, in.size, nil
}

func (in *input) Close() error {
	if in.spill != nil {
		in.spill.Close()
		in.spill = nil
	}
	if in.closer != nil {
		return in.closer.Close()
	}
	return nil
}

// generated is the "format" of an input that is made, not read.
const generated = "generated"

// decoder opens the decoder of a recognised format, or makes the pixels of
// an input that is not read from anywhere.
func (in *input) decoder(format string, env *pipeline.Env) (pipeline.Source, error) {
	if format == generated {
		return in.gen(env)
	}
	f := formatNamed(format)
	if f == nil {
		return nil, fmt.Errorf("goimgtools: %s input is not supported", format)
	}
	return f.open(in, env)
}
