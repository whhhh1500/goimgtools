// Package avif teaches goimgtools the AVIF format: read and written by
// libavif 1.4.2 (dav1d decoding, aom encoding, libyuv converting the
// colours), compiled to WebAssembly and translated to Go
// (internal/libavif): plain Go, without cgo or a WebAssembly runtime, so
// nothing is compiled when a program starts, and the output is the same on
// every processor. Import it for its side effect:
//
//	import _ "github.com/whhhh1500/goimgtools/avif"
//
// From then on Open, FromBytes and FromReader read AVIF files (their first
// image, turned upright by the irot and imir boxes as browsers show it),
// with their EXIF block (its orientation set to upright when the boxes have
// turned the pixels), XMP packet and colour profile (a Display P3 profile
// for a file that says so with an nclx box instead), and ToFile writes
// .avif files, as does ToFormat("avif", avif.Options{...}), with the
// metadata goimgtools keeps. MaxFileSize works with its Quality.
//
// It is a module of its own, so that the codecs (some 25MB in a program)
// come only with this import. The licences of what it includes are in its
// licenses directory.
//
// Unlike the formats of goimgtools, an AVIF is decoded whole: the pixels
// (4 bytes each, 8 for more than 8 bits) are counted in the memory limit,
// the codec's own memory is not: about 6.4 bytes a pixel more while it
// decodes, and 8MB and 28 bytes a pixel while it encodes (243MB for a
// picture of 3840 x 2160). The colours of a 4:2:0 file are brought to full
// size by libyuv, as Pillow's libavif does; pixels go to libavif and come
// back with the colour not multiplied by alpha, so that without loss an
// image with transparency keeps every pixel too. Animated AVIF files are
// read as their first image.
package avif

import (
	_ "embed"
	"errors"
	"fmt"
	"image"
	"io"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/avif/internal/libavif"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// displayP3 is a Display P3 profile (its primaries, D65 white, the sRGB
// curve), for files that describe their colours by an nclx box saying so.
//
//go:embed display-p3.icc
var displayP3 []byte

// Options are the settings of AVIF output, given to ToFormat("avif",
// avif.Options{...}).
type Options struct {
	// Quality is 1 to 100; 0 means the default, 60 (libavif's scale:
	// 60 looks about like a JPEG of 85).
	Quality int
	// Effort trades time for size: 1 is the fastest, 9 gives the smallest
	// files; 0 means the default, 5 (aom's speed 6, avifenc's default).
	Effort int
	// Lossless keeps every pixel (Quality is then not used): G, B and R are
	// coded 4:4:4 as they are.
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
		Match:  Match,
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

// Match reports a file whose ftyp box names AVIF: as its major brand, or
// among the compatible ones of a general HEIF (mif1, msf1) file within the
// head.
func Match(head []byte) bool {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return false
	}
	major := string(head[8:12])
	if major == "avif" || major == "avis" {
		return true
	}
	if major != "mif1" && major != "msf1" {
		return false
	}
	for i := 16; i+4 <= len(head); i += 4 {
		if b := string(head[i : i+4]); b == "avif" || b == "avis" {
			return true
		}
	}
	return false
}

// source is a decoded AVIF, handed out in strips, with its metadata.
type source struct {
	pipeline.Source
	meta     pipeline.Meta
	reserved int64
}

func (s *source) Meta() pipeline.Meta { return s.meta }

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
	cfg, err := libavif.Decode(data, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: avif: the image has no pixels", goimgtools.ErrFormat)
	}
	// The pixels, 8 bytes each at most (more than 8 bits), are held whole,
	// and once more while they are turned upright.
	need := int64(cfg.Width) * int64(cfg.Height) * 8
	if err := env.Budget.Reserve(need, "avif image"); err != nil {
		return nil, err
	}
	im, err := libavif.Decode(data, false)
	if err != nil {
		env.Budget.Release(need)
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	var m image.Image
	if im.Depth > 8 {
		m = &image.NRGBA64{Pix: im.Pix, Stride: im.Stride, Rect: image.Rect(0, 0, im.Width, im.Height)}
	} else {
		m = &image.NRGBA{Pix: im.Pix, Stride: im.Stride, Rect: image.Rect(0, 0, im.Width, im.Height)}
	}
	o := orientation(im.Transform&libavif.TransformIrot != 0, im.Angle, im.Transform&libavif.TransformImir != 0, im.Axis)
	m = orient(m, o)
	var meta pipeline.Meta
	if len(im.Exif) > 0 && len(im.Exif) <= pipeline.MaxMetaBytes {
		meta.Exif = im.Exif
		if o > 1 {
			// The boxes have turned the pixels: the EXIF orientation, which
			// says the same, must not turn them again.
			meta.Exif = pipeline.ExifUpright(meta.Exif)
		}
	}
	if len(im.XMP) <= pipeline.MaxMetaBytes {
		meta.Xmp = im.XMP
	}
	switch {
	case len(im.ICC) > 0 && len(im.ICC) <= pipeline.MaxMetaBytes:
		meta.ICC = im.ICC
	case im.Primaries == 12 && (im.Transfer == 13 || im.Transfer == 1 || im.Transfer == 6):
		// nclx: Display P3 primaries, the sRGB (or the near BT.709) curve.
		meta.ICC = displayP3
	}
	return &source{Source: pipeline.FromImage(m, env), meta: meta, reserved: need}, nil
}

func (s *source) Close() error {
	err := s.Source.Close()
	s.Env().Budget.Release(s.reserved)
	s.reserved = 0
	return err
}

// Opaque says whether the image has no transparency.
func (s *source) Opaque() bool { return pipeline.IsOpaque(s.Source) }

// keepOpen is a source whose Close leaves the source it wraps open: the
// encoder's caller closes it.
type keepOpen struct{ pipeline.Source }

func (keepOpen) Close() error { return nil }

func encode(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
	opts, _ := o.Options.(Options)
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
	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	need := int64(width) * int64(height) * 4
	env := pipeline.EnvOf(src)
	if err := env.Budget.Reserve(need, "avif image"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	pix := make([]byte, need)
	opaque := true
	strips := pipeline.Map(keepOpen{src}, func(*image.NRGBA) error { return nil })
	defer strips.Close()
	for y := 0; ; {
		s, err := strips.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		m := s.(*image.NRGBA)
		for r := range m.Rect.Dy() {
			row := m.Pix[r*m.Stride:][:4*width]
			copy(pix[(y+r)*4*width:], row)
			for x := 3; x < len(row) && opaque; x += 4 {
				opaque = row[x] == 255
			}
		}
		y += m.Rect.Dy()
	}
	out, err := libavif.Encode(pix, width, height, libavif.Options{Quality: q, QualityAlpha: q,
		Speed: 11 - effort, Chroma444: opts.Chroma444, Lossless: opts.Lossless, Opaque: opaque,
		ICC: o.ICC, Exif: o.Exif, XMP: o.Xmp})
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}
