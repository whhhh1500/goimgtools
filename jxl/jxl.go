// Package jxl teaches goimgtools the JPEG XL format: read and written by
// libjxl 0.12.0, the reference implementation, compiled to WebAssembly and
// translated to Go (internal/libjxl): plain Go, without cgo or a
// WebAssembly runtime, so nothing is compiled when a program starts, and
// the output is the same on every processor. Import it for its side
// effect:
//
//	import _ "github.com/whhhh1500/goimgtools/jxl"
//
// From then on Open, FromBytes and FromReader read JPEG XL files (their
// first frame, as libjxl's djxl gives it: upright, the colour not
// multiplied by alpha, a grey image as RGB), with their EXIF block (its
// orientation set to upright, since the pixels have been turned), XMP
// packet and colour profile (none for sRGB), and ToFile writes .jxl files,
// as does ToFormat("jxl", jxl.Options{...}), with the metadata goimgtools
// keeps. MaxFileSize works with its Quality.
//
// It is a module of its own, so that the codec (some 21MB of Go source,
// whose compiling takes about 6GB of memory once) comes only with this
// import. The licences of what it includes are in its licenses directory.
//
// The files written are those cjxl 0.12.0 writes at the same settings,
// within a few bytes (a Kodak photograph at the defaults: 126,552 bytes
// against 126,555, the same SSIMULACRA2), and files decode to the pixels
// djxl 0.12.0 gives: without loss the very pixels, with loss within a level
// (djxl runs libjxl with AVX2, whose fused multiplications and additions
// round a little differently).
//
// libjxl runs here on one thread, without SIMD (wasm2go does not translate
// WebAssembly's), as highway's scalar target. On one core a Kodak
// photograph (768 x 512) decodes in 51ms (djxl, with AVX2: 15ms) and
// without loss in 0.22s (0.10s, writing a PNG); it is encoded in 1.3s at the
// default effort (cjxl: 0.11s), 57ms at effort 3 (25ms) and 1.3s without
// loss (0.47s).
//
// A JPEG XL is decoded whole: the pixels (4 bytes each, 8 for more than 8
// bits) are counted in the memory limit, libjxl's own memory is not: about 5
// bytes a pixel more while it decodes (14 without loss), and while it
// encodes, above 2048 x 2048 a region of that size at a time: for
// photographs of 0.4, 4.3 and 12 megapixels 37MB, 278MB and 445MB at the
// default effort, 33MB, 229MB and 310MB at effort 3 (35MB and 184MB without
// loss for the first two), and at efforts 8 and 9 the whole (310 bytes a
// pixel at 9), room for which is made at once. A 12-megapixel photograph is
// encoded in 38s at the default effort, the process peaking at 509MB, and in
// 1.3s at effort 3 (371MB). What libjxl may allocate to decode a file is
// bounded by what its image could need, and EXIF and XMP boxes and profiles
// of more than 8MB are dropped.
package jxl

import (
	"encoding/binary"
	"errors"
	"fmt"
	"image"
	"io"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/jxl/internal/libjxl"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the settings of JPEG XL output, given to ToFormat("jxl",
// jxl.Options{...}).
type Options struct {
	// Quality is 1 to 100, cjxl's -q: libjxl's distance from it (90 is
	// distance 1, visually lossless; 100 is lossless); 0 means the default,
	// 90.
	Quality int
	// Effort trades time for size: 1 is the fastest, 10 gives the smallest
	// files; 0 means the default, 7 (cjxl's).
	Effort int
	// Lossless keeps every pixel (Quality is then not used).
	Lossless bool
}

// DefaultQuality is the quality of JPEG XL output when none is given.
const DefaultQuality = 90

func init() { goimgtools.RegisterFormat(Codec()) }

// Codec returns the JPEG XL format for goimgtools.RegisterFormat (which
// this package's init calls).
func Codec() goimgtools.Codec {
	return goimgtools.Codec{
		Format: goimgtools.Format{Name: "jxl", Extensions: []string{".jxl"}},
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

// Match reports a JPEG XL codestream or container.
func Match(head []byte) bool {
	if len(head) >= 2 && head[0] == 0xff && head[1] == 0x0a {
		return true
	}
	return len(head) >= 12 && string(head[:12]) == "\x00\x00\x00\x0cJXL \x0d\x0a\x87\x0a"
}

// distance is libjxl's distance for a quality (JxlEncoderDistanceFromQuality).
func distance(quality int) float32 {
	q := float64(quality)
	if q >= 30 {
		return float32(0.1 + (100-q)*0.09)
	}
	return float32(53.0/3000.0*q*q - 23.0/20.0*q + 25.0)
}

// source is a decoded JPEG XL, handed out in strips, with its metadata.
type source struct {
	pipeline.Source
	meta     pipeline.Meta
	reserved int64
}

func (s *source) Meta() pipeline.Meta { return s.meta }

func (s *source) Close() error {
	err := s.Source.Close()
	s.Env().Budget.Release(s.reserved)
	s.reserved = 0
	return err
}

// Opaque says whether the image has no transparency.
func (s *source) Opaque() bool { return pipeline.IsOpaque(s.Source) }

func decode(in goimgtools.Input, env *pipeline.Env) (pipeline.Source, error) {
	ra, size, err := in.ReaderAt()
	if err != nil {
		return nil, err
	}
	if size > 1<<31 {
		return nil, errors.New("jxl: the file is larger than 2GB")
	}
	data := make([]byte, size)
	if _, err := ra.ReadAt(data, 0); err != nil && err != io.EOF {
		return nil, err
	}
	cfg, err := libjxl.Decode(data, true)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: jxl: the image has no pixels", goimgtools.ErrFormat)
	}
	bpp := int64(4)
	if cfg.Depth > 8 || cfg.Float {
		bpp = 8
	}
	need := int64(cfg.Width) * int64(cfg.Height) * bpp
	if err := env.Budget.Reserve(need, "jxl image"); err != nil {
		return nil, err
	}
	im, err := libjxl.Decode(data, false)
	if err != nil {
		env.Budget.Release(need)
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	// The size is the upright one: libjxl has turned the pixels.
	r := image.Rect(0, 0, im.Width, im.Height)
	var m image.Image
	if bpp == 8 {
		m = &image.NRGBA64{Pix: im.Pix, Stride: im.Stride, Rect: r}
	} else {
		m = &image.NRGBA{Pix: im.Pix, Stride: im.Stride, Rect: r}
	}
	var meta pipeline.Meta
	if x := exifOf(im.Exif); len(x) > 0 && len(x) <= pipeline.MaxMetaBytes {
		// libjxl has turned the pixels: the EXIF orientation, which says the
		// same, must not turn them again.
		meta.Exif = pipeline.ExifUpright(x)
	}
	if len(im.XMP) <= pipeline.MaxMetaBytes {
		meta.Xmp = im.XMP
	}
	if len(im.ICC) <= pipeline.MaxMetaBytes {
		meta.ICC = im.ICC
	}
	return &source{Source: pipeline.FromImage(m, env), meta: meta, reserved: need}, nil
}

// exifOf is the TIFF header and what follows of an EXIF box, which starts
// with the offset of the header.
func exifOf(box []byte) []byte {
	if len(box) < 4 {
		return nil
	}
	off := binary.BigEndian.Uint32(box)
	if uint64(off) > uint64(len(box)-4) {
		return nil
	}
	return box[4+off:]
}

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
		return errors.New("jxl: quality must be 1 to 100")
	}
	effort := opts.Effort
	if effort == 0 {
		effort = 7
	}
	if effort < 1 || effort > 10 {
		return errors.New("jxl: effort must be 1 to 10")
	}
	lossless := opts.Lossless || q == 100
	b := src.Bounds()
	width, height := b.Dx(), b.Dy()
	need := int64(width) * int64(height) * 4
	env := pipeline.EnvOf(src)
	if err := env.Budget.Reserve(need, "jxl image"); err != nil {
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
	out, err := libjxl.Encode(pix, width, height, libjxl.Options{Distance: distance(q), Effort: effort,
		Lossless: lossless, Opaque: opaque, ICC: o.ICC, Exif: o.Exif, XMP: o.Xmp})
	if err != nil {
		return err
	}
	_, err = w.Write(out)
	return err
}
