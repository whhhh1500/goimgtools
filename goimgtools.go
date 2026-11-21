// Package goimgtools processes images in pure Go with very little memory:
// the image flows through the pipeline a few rows at a time instead of
// being decoded whole, so memory hardly grows with the size of the image.
// Resizing and watermarking a 6000x4000 JPEG peaks at 8.3MB for the whole
// process (decoding it whole the standard library's way takes 242MB). The
// memory has a hard limit (MemoryLimit, 64MB by default); steps that must
// see the whole image keep what does not fit in temporary files.
//
// The interface follows sharp (the Node.js library): open an image, chain
// operations, pick an output format, write it.
//
//	err := goimgtools.Open("in.png").
//		Resize(1536, 0).                 // 1536 wide, height in proportion
//		Flatten(color.White).            // transparent areas become white
//		Composite(goimgtools.Open("logo.png"), goimgtools.CompositeOptions{
//			Gravity: goimgtools.SouthEast, Left: -24, Top: -24,
//		}).
//		JPEG(goimgtools.JPEGOptions{Quality: 85}).
//		ToFile("out.jpg")
//
// Operations run in the order they are chained. Nothing is read until
// ToFile, ToWriter, ToBytes or Metadata is called, and any error from the
// chain is returned there. An output is only left behind (ToFile) when the
// whole input decoded and verified without error.
//
// # Building on it
//
// The package is made to be extended without being changed. Each of its
// parts has a place where code of your own goes in, and the parts of the
// package itself are built the same way:
//
//   - an operation of your own: Image.Pixels for one that changes pixel
//     values, Image.Pipe for anything else;
//   - a set of steps under one name: Image.Apply;
//   - an input of your own: FromImage, FromSource;
//   - a file format of your own, or one of the package's read or written
//     differently: RegisterFormat, CodecByName, Image.ToFormat;
//   - a resampling filter of your own: ResizeOptions.CustomKernel;
//   - something else done with the result than writing a file:
//     Image.Stream, Image.ToImage;
//   - a type of your own with the methods of Image and more: embed *Image.
//
// The pixel stream all of this speaks is in package pipeline.
package goimgtools

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/whhhh1500/goimgtools/internal/exifedit"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// DefaultMemoryLimit is the working-memory limit used unless MemoryLimit
// sets another: 64MB.
const DefaultMemoryLimit = pipeline.DefaultMemoryLimit

// ErrMemoryLimit is returned (wrapped) when an image cannot be processed
// within the memory limit.
var ErrMemoryLimit = pipeline.ErrMemoryLimit

// PanicError is the error a panic inside a run is turned into: a bug, in
// this package or in code given to it, that an input brought out. No
// method of Image panics while it runs a recipe; errors.As finds this
// instead, with the stack of the place it happened.
type PanicError = pipeline.PanicError

// ErrFormat is returned (wrapped) when the input is not an image this
// package can read: a file of another kind, a format or a variety of a
// format that is not supported, or image data that is damaged or cut
// short. It tells such an input apart from a failure of the system: an
// input that cannot be opened or read, a full disk, the memory limit, a
// cancelled context. For a service this is the difference between "the
// upload is bad" and "try again".
var ErrFormat = errors.New("goimgtools: not a readable image")

// ErrPixelLimit is returned (wrapped) when the input has more pixels than
// LimitInputPixels allows, or a step of the chain makes an image of more
// than LimitOutputPixels.
var ErrPixelLimit = errors.New("goimgtools: image has too many pixels")

// Image is a recipe: an input plus the operations to apply to it.
//
// The methods that add an operation or choose the output change the Image
// they are called on and return that same Image, so that calls can be
// chained; they do not make a copy. To make several results from one
// starting point, Clone it first:
//
//	base := goimgtools.Open("in.jpg").AutoOrient()
//	thumb := base.Clone().Resize(200, 0)
//	large := base.Clone().Resize(1600, 0)
//
// A finished recipe may be run (ToFile, ToWriter, ToBytes, OutputSize) any
// number of times, also from several goroutines at once, except one made
// with FromReader, which can be read only once.
type Image struct {
	ctx        context.Context
	open       func() (*input, error)
	once       bool        // FromReader: the input can be read only once
	chain      []operation // the operations, in the order they were asked for
	limit      int64
	tempDir    string
	format     string // the output format chosen, "" to go by the file name
	options    any    // its options: a JPEGOptions and so on, or what ToFormat was given
	animated   bool
	maxBytes   int64
	minSSIM    float64 // MinSSIM
	maxPixels  int64
	maxOutput  int64
	threads    int
	toSRGB     bool   // see ToSRGB
	hooks      []Hook // see Around
	autoOrient bool   // Rotate() was asked to follow the EXIF orientation
	keepExif   bool
	exifSet    []exifedit.Field // SetExif
	loopSet    bool             // Loop
	loop       int
	delays     []time.Duration // FrameDelay
	keepXmp    bool
	stripICC   bool
	toProfile  []byte   // see ToProfile
	pages      []*Image // see Pages
	pagesBase  int      // the operations of the first page, which Pages copies
	density    float64  // to record in the output (0: none), when densitySet
	densitySet bool
	err        error
}

// Open reads the image from a file.
func Open(path string) *Image {
	return &Image{open: func() (*input, error) { return fileInput(path) }}
}

// FromBytes reads the image from memory.
func FromBytes(data []byte) *Image {
	return &Image{open: func() (*input, error) { return bytesInput(data), nil }}
}

// FromReader reads the image from r. Such an Image can be written once.
func FromReader(r io.Reader) *Image {
	var used atomic.Bool
	return &Image{once: true, open: func() (*input, error) {
		if used.Swap(true) {
			return nil, errors.New("goimgtools: a FromReader image can only be read once")
		}
		return readerInput(r), nil
	}}
}

// Clone returns an independent copy of the recipe: operations added to one
// do not appear in the other. The input is shared, which matters only for
// FromReader: its reader can still be read just once.
func (m *Image) Clone() *Image {
	c := *m
	c.chain = append([]operation(nil), m.chain...)
	return &c
}

// WithContext makes the run stop when ctx is cancelled or its deadline
// passes; the run then returns ctx.Err(). The context is looked at between
// strips of the image (a few rows), while decoding and while encoding, so a
// run ends within a moment of the cancellation; a file being written is
// removed.
func (m *Image) WithContext(ctx context.Context) *Image { m.ctx = ctx; return m }

// first returns the one optional value a method was given, or the zero
// value when it was given none.
func first[T any](opts []T) T {
	var zero T
	if len(opts) > 0 {
		zero = opts[0]
	}
	return zero
}

// atMostOne records an error when a method was given more than one value
// for its optional settings: the second would silently do nothing.
func (m *Image) atMostOne(method string, n int) {
	if n > 1 {
		m.fail(fmt.Errorf("goimgtools: %s takes at most one options value, got %d", method, n))
	}
}

// MemoryLimit sets the working-memory limit in bytes (default 64MB).
func (m *Image) MemoryLimit(bytes int64) *Image { m.limit = bytes; return m }

// DefaultInputPixels is the largest input accepted unless LimitInputPixels
// sets another limit: 16383 x 16383 pixels, the same default as sharp.
const DefaultInputPixels = 268402689

// LimitInputPixels sets the largest input image accepted, in pixels
// (width x height). Larger images are refused before any pixel is decoded.
// For an animation read with Animated it is the pixels of all its frames
// together.
func (m *Image) LimitInputPixels(pixels int64) *Image { m.maxPixels = pixels; return m }

// LimitOutputPixels sets the largest image the chain may make, in pixels
// (width x height): at any of its steps, and so as its result. The default
// is the same 16383 x 16383. A chain that leads to more (an enlargement by
// a factor that came from outside, say) is refused before any pixel is
// made, with an ErrPixelLimit. Without this a small file could be turned
// into hours of work or a file that fills the disk.
func (m *Image) LimitOutputPixels(pixels int64) *Image { m.maxOutput = pixels; return m }

func (m *Image) outputLimit() int64 {
	if m.maxOutput > 0 {
		return m.maxOutput
	}
	return DefaultInputPixels
}

func (m *Image) pixelLimit() int64 {
	if m.maxPixels > 0 {
		return m.maxPixels
	}
	return DefaultInputPixels
}

// Threads sets how many CPU cores one run may use, at most the number of
// CPUs. Without it a run uses what SetDefaultThreads says, which is 1
// unless the program changed it. With 2 the input is decoded on one core
// while the other does the rest; with 3 the encoder gets a core of its own
// as well; from 4 on, the cores beyond those three are shared by the
// steps that can split their work: Resize, Blur, Sharpen, Modulate,
// Rotate, JPEG encoding and PNG compression. However the work is split,
// no more than n of the run's goroutines work at a time. Each extra thread costs a few strips of memory (under 1MB each),
// counted in the memory limit. The output does not depend on it.
func (m *Image) Threads(n int) *Image { m.threads = n; return m }

// defaultThreads is what SetDefaultThreads was given; 0 stands for 1.
var defaultThreads atomic.Int32

// SetDefaultThreads sets how many CPU cores a run uses when its Image does
// not say (Threads): as there; anything below 1 goes back to 1,
// the default of the package. It is the one setting for a whole program,
// for a service that wants every run on two cores without saying so at
// each place an image is processed. It may be called at any time; runs
// that have started keep what they started with.
func SetDefaultThreads(n int) { defaultThreads.Store(int32(max(0, min(n, runtime.NumCPU())))) }

// threadCount is how many cores this run may use.
func (m *Image) threadCount() int {
	if m.threads > 0 {
		return min(m.threads, runtime.NumCPU())
	}
	return max(1, int(defaultThreads.Load()))
}

// helperCount is how many cores of the run go to the operations that
// split their work: those beyond the run's own, the decoder's and the
// encoder's.
func (m *Image) helperCount() int { return max(0, m.threadCount()-3) }

// TempDir sets where temporary files go when an image has to spill to disk
// (default: the system temporary directory).
func (m *Image) TempDir(dir string) *Image { m.tempDir = dir; return m }

func (m *Image) add(o operation) *Image { m.chain = append(m.chain, o); return m }

func (m *Image) fail(err error) *Image {
	if m.err == nil {
		m.err = err
	}
	return m
}
