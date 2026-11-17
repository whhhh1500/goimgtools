// Package heic teaches goimgtools the HEIC format (HEIF images coded with
// HEVC, as phones take photographs): read and written by
// github.com/gen2brain/h265, an HEVC codec in pure Go (no cgo, no
// WebAssembly). Import it for its side effect:
//
//	import _ "github.com/whhhh1500/goimgtools/heic"
//
// From then on Open, FromBytes and FromReader read HEIC files (their
// primary image, its grid of tiles put together, its transparency, turned
// upright by the clap, irot and imir boxes as browsers and libheif show
// it), with their EXIF block (its orientation set to upright, since the
// boxes have turned the pixels), XMP packet and colour profile (a Display
// P3 profile for a file that says so with an nclx box instead), and ToFile
// writes .heic files, as does ToFormat("heic", heic.Options{...}), with the
// metadata goimgtools keeps. MaxFileSize works with its Quality.
//
// It is a module of its own: the codec is large, and HEVC is covered by
// patents, whose licensing is the user's matter.
//
// Unlike the formats of goimgtools, a HEIC is decoded whole: the pixels (4
// bytes each, 8 for more than 8 bits) are counted in the memory limit. The
// decoder takes as many goroutines as the image's Threads (a grid's tiles
// and the rows of a picture are decoded at once); its output does not
// depend on them.
package heic

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"io"

	gheic "github.com/gen2brain/h265/heic"
	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// displayP3 is a Display P3 profile (its primaries, D65 white, the sRGB
// curve), for files that describe their colours by an nclx box saying so.
//
//go:embed display-p3.icc
var displayP3 []byte

// Options are the settings of HEIC output, given to ToFormat("heic",
// heic.Options{...}).
type Options struct {
	// Quality is 1 to 100; 0 means the default, 60.
	Quality int
	// Effort trades time for size: 1 is the fastest, 9 gives the smallest
	// files; 0 means the default, 5. From 6 the sample adaptive offset is
	// fitted (about 2.2 times the time, 3.5% smaller).
	Effort int
	// Lossless codes the samples without loss, in 4:4:4 (Quality is then
	// not used): the colours come back within a level, the samples being
	// YCbCr.
	Lossless bool
	// Chroma444 keeps all of the colour; by default it is kept at half
	// the width and height (4:2:0).
	Chroma444 bool
}

// DefaultQuality is the quality of HEIC output when none is given.
const DefaultQuality = gheic.DefaultQuality

func init() { goimgtools.RegisterFormat(Codec()) }

// Codec returns the HEIC format for goimgtools.RegisterFormat (which this
// package's init calls).
func Codec() goimgtools.Codec {
	return goimgtools.Codec{
		Format: goimgtools.Format{Name: "heic", Extensions: []string{".heic", ".heif", ".hif"}},
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

// Match reports a file whose ftyp box names a brand of HEVC images: as its
// major brand, or among the compatible ones of a general HEIF (mif1, msf1)
// file within the head.
func Match(head []byte) bool {
	if len(head) < 12 || string(head[4:8]) != "ftyp" {
		return false
	}
	hevc := func(b string) bool {
		switch b {
		case "heic", "heix", "hevc", "hevx", "heim", "heis":
			return true
		}
		return false
	}
	major := string(head[8:12])
	if hevc(major) {
		return true
	}
	if major != "mif1" && major != "msf1" {
		return false
	}
	for i := 16; i+4 <= len(head); i += 4 {
		if hevc(string(head[i : i+4])) {
			return true
		}
	}
	return false
}

// source is a decoded HEIC, handed out in strips, with its metadata.
type source struct {
	pipeline.Source
	meta     pipeline.Meta
	reserved int64
}

func (s *source) Meta() pipeline.Meta { return s.meta }

// Opaque says whether the image has no transparency.
func (s *source) Opaque() bool { return pipeline.IsOpaque(s.Source) }

func (s *source) Close() error {
	err := s.Source.Close()
	s.Env().Budget.Release(s.reserved)
	s.reserved = 0
	return err
}

func decode(in goimgtools.Input, env *pipeline.Env) (pipeline.Source, error) {
	ra, size, err := in.ReaderAt()
	if err != nil {
		return nil, err
	}
	if size > 1<<31 {
		return nil, errors.New("heic: the file is larger than 2GB")
	}
	data := make([]byte, size)
	if _, err := ra.ReadAt(data, 0); err != nil && err != io.EOF {
		return nil, err
	}
	cfg, err := gheic.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: heic: the image has no pixels", goimgtools.ErrFormat)
	}
	need := int64(cfg.Width) * int64(cfg.Height) * 8
	if err := env.Budget.Reserve(need, "heic image"); err != nil {
		return nil, err
	}
	img, ci, err := gheic.DecodeColor(bytes.NewReader(data), gheic.Options{AutoRotate: true, Threads: env.Helpers() + 1})
	if err != nil {
		env.Budget.Release(need)
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	var meta pipeline.Meta
	if x, err := gheic.RawExif(bytes.NewReader(data)); err == nil && len(x) <= pipeline.MaxMetaBytes {
		// The boxes have turned the pixels: the EXIF orientation, which
		// says the same, must not turn them again.
		meta.Exif = pipeline.ExifUpright(bytes.Clone(x))
	}
	if x, err := gheic.RawXMP(bytes.NewReader(data)); err == nil && len(x) <= pipeline.MaxMetaBytes {
		meta.Xmp = bytes.Clone(x)
	}
	switch {
	case len(ci.ICCP) > 0 && len(ci.ICCP) <= pipeline.MaxMetaBytes:
		meta.ICC = bytes.Clone(ci.ICCP)
	case ci.Primaries == 12 && (ci.Transfer == 13 || ci.Transfer == 1 || ci.Transfer == 6):
		// nclx: Display P3 primaries, the sRGB (or the near BT.709) curve.
		meta.ICC = displayP3
	}
	return &source{Source: pipeline.FromImage(img, env), meta: meta, reserved: need}, nil
}

func encode(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
	opts, _ := o.Options.(Options)
	b := src.Bounds()
	need := int64(b.Dx()) * int64(b.Dy()) * 4
	env := pipeline.EnvOf(src)
	if err := env.Budget.Reserve(need, "heic image"); err != nil {
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
		return errors.New("heic: quality must be 1 to 100")
	}
	effort := opts.Effort
	if effort == 0 {
		effort = 5
	}
	if effort < 1 || effort > 9 {
		return errors.New("heic: effort must be 1 to 9")
	}
	chroma := gheic.Chroma420
	if opts.Chroma444 || opts.Lossless {
		chroma = gheic.Chroma444
	}
	return gheic.Encode(w, img, gheic.EncodeOptions{Quality: q, Lossless: opts.Lossless, Chroma: chroma, SAO: effort >= 6,
		ICC: o.ICC, Exif: o.Exif, XMP: o.Xmp})
}
