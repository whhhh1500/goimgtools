// Package heic teaches goimgtools the HEIC format (HEIF images coded with
// HEVC, as phones take photographs). Import it for its side effect:
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
// Files are read by github.com/gen2brain/h265, an HEVC decoder in pure Go
// (no cgo, no WebAssembly), and written by Kvazaar, the HEVC encoder of
// Tampere University, compiled to WebAssembly and translated to Go
// (internal/kvazaar): plain Go too, which writes the same file on every
// processor, with any Threads and memory limit. See Options for how its
// files compare with x265's.
//
// An image of up to 4 megapixels (2 at 4:4:4) is coded as one picture, a
// larger one as a grid of tiles of 1024 x 1024 (cut where the image ends),
// as many at once as its Threads have room for under its memory limit, the
// result the same however many. The image is kept one row of tiles at a
// time (1.5 bytes a pixel, 3 at 4:4:4, and 1 more with transparency), and
// each picture being coded takes about 1.3MB and 6.2 bytes a pixel more
// (2MB and 16.6 at 4:4:4; the memory limit counts a little more). Each
// 64 x 64 block's QP is moved by up to 13 by how much its samples vary
// (Kvazaar's variance adaptive quantization, measured against one value
// for every picture, so that a grid's tiles get the QPs the picture would).
//
// It is a module of its own: the codecs are large, and HEVC is covered by
// patents, whose licensing is the user's matter. The licences of the code
// it includes (Kvazaar's BSD licence among them) are in its licenses
// directory.
//
// A HEIC without transparency and of 8 bits (as phones write them: a grid
// of 512 x 512 tiles, or one picture) is decoded without holding its
// pixels. A grid is decoded a row of tiles at a time and handed on in
// strips: up to four rows of tiles are held (as many as the memory limit
// has room for, at least the two a row of pixels is made from), their tiles
// decoded in the order they are stored, on as many goroutines as the
// image's Threads, ahead of the rows being made into pixels. A single
// picture, and a grid turned by irot or flipped by imir (a photograph taken
// upright), are decoded into their planes (1.5 bytes a pixel) and made into
// pixels a strip at a time. Converted to JPEG, a photograph of 3992 x 2992
// in 48 tiles peaks at 27MB of process memory instead of 81MB decoded whole
// (37MB instead of 87MB with Threads(8)), the same turned a quarter by irot
// at 32MB instead of 124MB, and one of 4032 x 3024 as a single picture at
// 56MB instead of 102MB: 4% to 7% slower on one thread, as fast or faster
// with Threads(8). Nothing is decoded before the first strip is asked for:
// Metadata takes 2ms for such a file, and LimitInputPixels refuses one
// before it is decoded. Its pixels are those h265 gives with its SIMD, on
// every processor: the colours are converted here with its arithmetic,
// each product rounded on its own. A picture smaller than its ispe box says
// is an error.
//
// Other files (with transparency, or of more than 8 bits) are decoded
// whole: the pixels (4 bytes each, 8 for more than 8 bits) are counted in
// the memory limit. The decoder takes as many goroutines as the image's
// Threads (a grid's tiles and the rows of a picture are decoded at once).
// The output depends on neither Threads nor the memory limit; for files
// decoded whole it does depend, by a level here and there, on whether h265
// converts the colours with SIMD (amd64, arm64 and riscv64 with RVV) or in
// Go (-tags noasm, other processors): Go fuses multiplications and
// additions on some processors, and its rounding then differs.
package heic

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"image/color"
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
//
// Against libheif 1.23.6 with x265 4.3 at its defaults (pillow-heif 1.8.1,
// x265's slow preset), on the 24 Kodak photographs each written at
// qualities 30 to 90 and read by libheif, the files are larger at equal
// SSIMULACRA2 by 5.3% at the default effort (from 2.1% to 10.5%), 1.2% at
// effort 6, 0.7% at 7 and 0.4% at 8 (from 3.4% smaller to 3.7% larger), and
// on six of them 0.6% smaller at effort 9; gen2brain/h265's encoder, which
// wrote them before, 23% larger. On one core a photograph of 3840 x 2160
// takes about 7s at the default effort (2.3s with Threads(8)), 20s at effort 6
// (7.1s), where x265 takes 3.2s and gen2brain/h265 took 3.5s: Kvazaar runs
// here as plain Go, without the SIMD of x265.
type Options struct {
	// Quality is 1 to 100; 0 means the default, 60. The QP of a picture is
	// 51 - Quality/2, moved for each 64 x 64 block by how much it varies (see
	// the package doc); the files come out about the size and quality of
	// libheif's at the same quality.
	Quality int
	// Effort trades time for size: 1 is the fastest, 9 gives the smallest
	// files; 0 means the default, 5. They are Kvazaar's presets: ultrafast
	// at 1, superfast and fast (with rate-distortion optimized quantization)
	// at 2 and 3, medium at 4 and 5, slower from 6; with more tried from 5
	// (the colour's own prediction modes, sign hiding), from 7 split
	// transforms, at 8 and 9 more prediction modes compared by their cost
	// (at 9 all of them). See Options for how they compare; a Kodak
	// photograph (768 x 512) takes 0.16s at effort 1, 0.49s at 5, 1.5s at 6,
	// 3.5s at 8 and 14s at 9 on one core.
	Effort int
	// Lossless codes the pixels without loss (Quality is then not used): G,
	// B and R are coded 4:4:4 as they are, with the colour description that
	// says so (as libheif reads it), and come back exactly. On the 24 Kodak
	// photographs, 0.4% larger than x265's lossless files at the default
	// effort and 1.5% smaller at effort 7; still 9% larger than the PNG
	// files (and lossless WebP is far smaller). gen2brain/h265 stored the
	// samples uncoded, 70% larger.
	Lossless bool
	// Chroma444 keeps all of the colour; by default it is kept at half the
	// width and height (4:2:0). Such files are of HEVC's range extensions
	// (the brand heix), which not every reader takes.
	Chroma444 bool
}

// DefaultQuality is the quality of HEIC output when none is given.
const DefaultQuality = 60

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
	if s, err := newStream(data, env); s != nil || err != nil {
		if err != nil {
			return nil, err
		}
		return s, nil
	}
	cfg, err := gheic.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", goimgtools.ErrFormat, err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, fmt.Errorf("%w: heic: the image has no pixels", goimgtools.ErrFormat)
	}
	// The decoded pixels: 4 bytes each, 8 above 8 bits. (A 12-megapixel
	// photograph takes 48MB; while the decoder works and turns the image
	// upright it holds its planes and a second copy besides, which are not
	// counted.)
	bpp := int64(4)
	if cfg.ColorModel == color.NRGBA64Model || cfg.ColorModel == color.RGBA64Model {
		bpp = 8
	}
	need := int64(cfg.Width) * int64(cfg.Height) * bpp
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
