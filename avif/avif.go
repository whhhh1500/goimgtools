// Package avif teaches goimgtools the AVIF format: read and written by
// libavif (dav1d decoding, aom encoding) compiled to WebAssembly, through
// github.com/gen2brain/avif, with no cgo. Import it for its side effect:
//
//	import _ "github.com/whhhh1500/goimgtools/avif"
//
// From then on Open, FromBytes and FromReader read AVIF files (their first
// image, turned upright by the irot and imir boxes, as browsers show it),
// and ToFile writes .avif files, as does ToFormat("avif", avif.Options{...}).
// MaxFileSize works with its Quality.
//
// It is a module of its own, so that the WebAssembly runtime and the codec
// (some 4MB in a program) come only with this import.
//
// Unlike the formats of goimgtools, an AVIF is decoded whole: the pixels
// (4 bytes each, 8 for more than 8 bits) are counted in the memory limit,
// the memory of the WebAssembly codec is not. When a libavif is installed
// as a shared library, gen2brain/avif uses it instead of the WebAssembly
// build; build with -tags nodynamic to always use the WebAssembly build (its
// output is then the same on every machine).
//
// What gen2brain/avif's build gives: the chroma of a 4:2:0 file is brought
// to full size by libavif's own bilinear filter, where Pillow's libavif (and
// browsers') converts with libyuv: 39 to 48 dB apart. Pixels go to libavif
// and come back multiplied by alpha: without loss an opaque image keeps
// every pixel, one with transparency its alpha and its colour times alpha
// to within 2 levels. Animated AVIF files are read as their first image.
//
// libavif, dav1d and aom are under BSD licences, gen2brain/avif under the
// MIT licence; their notices come with that module.
package avif

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"

	gavif "github.com/gen2brain/avif"
	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the settings of AVIF output, given to ToFormat("avif",
// avif.Options{...}).
type Options struct {
	// Quality is 1 to 100; 0 means the default, 60 (libavif's scale:
	// 60 looks about like a JPEG of 85).
	Quality int
	// Effort trades time for size: 1 is the fastest, 9 gives the smallest
	// files; 0 means the default, 5 (aom's speed 6, avifenc's default).
	Effort int
	// Lossless keeps every pixel (Quality is then not used).
	Lossless bool
	// Chroma444 keeps all of the colour, as Chroma444 of JPEG; by default
	// it is kept at half the width and height (4:2:0).
	Chroma444 bool
}

// DefaultQuality is the quality of AVIF output when none is given.
const DefaultQuality = 60

func init() { goimgtools.RegisterFormat(Codec()) }

// Codec returns the AVIF format for goimgtools.RegisterFormat (which this
// package's init calls).
func Codec() goimgtools.Codec {
	return goimgtools.Codec{
		Format: goimgtools.Format{Name: "avif", Extensions: []string{".avif"}},
		Match:  match,
		Decode: decode,
		Encode: encode,
		Quality: func(options any) (int, bool) {
			o, _ := options.(Options)
			if o.Lossless {
				return 0, false
			}
			if o.Quality == 0 {
				return DefaultQuality, true
			}
			return o.Quality, true
		},
	}
}

// match reports an ISO media file whose brand is AVIF.
func match(head []byte) bool {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return false
	}
	brand := string(head[8:12])
	return brand == "avif" || brand == "avis"
}

// source is a decoded AVIF, handed out in strips.
type source struct {
	pipeline.Source
	reserved int64
}

func decode(in goimgtools.Input, env *pipeline.Env) (pipeline.Source, error) {
	ra, size, err := in.ReaderAt()
	if err != nil {
		return nil, err
	}
	if size > 1<<31 {
		return nil, errors.New("avif: the file is larger than 2GB")
	}
	data := make([]byte, size)
	if _, err := ra.ReadAt(data, 0); err != nil && err != io.EOF {
		return nil, err
	}
	cfg, err := gavif.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: avif: the image has no pixels", goimgtools.ErrFormat)
	}
	// The pixels, 8 bytes each at most (more than 8 bits), are held
	// whole: the image is decoded at once (turned upright by irot and imir,
	// which a file's header size does not show).
	need := int64(cfg.Width) * int64(cfg.Height) * 8
	if err := env.Budget.Reserve(need, "avif image"); err != nil {
		return nil, err
	}
	m, err := gavif.Decode(bytes.NewReader(data), gavif.Options{AutoRotate: true})
	if err != nil {
		env.Budget.Release(need)
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	return &source{Source: pipeline.FromImage(m, env), reserved: need}, nil
}

func (s *source) Close() error {
	err := s.Source.Close()
	s.Env().Budget.Release(s.reserved)
	s.reserved = 0
	return err
}

// Opaque says whether the image has no transparency.
func (s *source) Opaque() bool { return pipeline.IsOpaque(s.Source) }

func encode(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
	opts, _ := o.Options.(Options)
	b := src.Bounds()
	need := int64(b.Dx()) * int64(b.Dy()) * 4
	env := pipeline.EnvOf(src)
	if err := env.Budget.Reserve(need, "avif image"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	img := image.NewNRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for {
		s, err := src.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		r := s.Bounds()
		draw.Draw(img, r.Sub(b.Min), s, r.Min, draw.Src)
	}
	q := o.Quality
	if q == 0 {
		q = opts.Quality
	}
	if q == 0 {
		q = DefaultQuality
	}
	if q < 1 || q > 100 {
		return errors.New("avif: quality must be 1 to 100")
	}
	effort := opts.Effort
	if effort == 0 {
		effort = 5
	}
	if effort < 1 || effort > 9 {
		return errors.New("avif: effort must be 1 to 9")
	}
	sub := image.YCbCrSubsampleRatio420
	if opts.Chroma444 {
		sub = image.YCbCrSubsampleRatio444
	}
	return gavif.Encode(w, img, gavif.Options{Quality: q, QualityAlpha: q, Speed: 11 - effort,
		ChromaSubsampling: sub, Lossless: opts.Lossless})
}
