package goimgtools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The output: choosing the format and its settings, and writing the
// result. The formats themselves are in formats.go.

// KeepExif copies the EXIF block of the input (camera, date, GPS, ...) to
// the output. By default it is dropped. After Rotate() the copied block
// says the image is upright.
func (m *Image) KeepExif() *Image { m.keepExif = true; return m }

// StripICC drops the ICC colour profile of the input. By default it is
// copied to the output, because the pixel values only mean the right
// colours together with it. Without it the values are taken for sRGB, as
// they are; see ToSRGB for converting them.
func (m *Image) StripICC() *Image { m.stripICC = true; return m }

// ToSRGB converts the colours of an image with an ICC profile other than
// sRGB (a Display P3 or Adobe RGB photograph) to sRGB at the end of the
// recipe, and leaves the profile out of the output: the colours then look
// right in programs that ignore profiles, and the file is smaller by the
// profile. Colours sRGB cannot show are clipped (relative colorimetric
// rendering, as LittleCMS does it, to within one level).
//
// GIF and BMP output, which cannot carry a profile, is converted this way
// without being asked. Profiles described by primaries and tone curves
// are converted (those of cameras, phones, Display P3, Adobe RGB,
// ProPhoto); others (lookup tables) are left as they are, kept in the
// output where the format can carry them.
func (m *Image) ToSRGB() *Image { m.toSRGB = true; return m }

// manageColour converts src to sRGB when its profile asks for it: on
// ToSRGB, or for an output format that cannot carry the profile. It
// returns the source to encode and the profile to write.
func (m *Image) manageColour(src pipeline.Source, icc []byte, format string) (pipeline.Source, []byte) {
	if len(icc) == 0 {
		return src, icc
	}
	if def := formatNamed(format); !m.toSRGB && (def == nil || !def.noProfile) {
		return src, icc
	}
	p, err := iccprof.Parse(icc)
	if err != nil {
		return src, icc // not one this package can convert from
	}
	if t := p.ToSRGB(); !t.Identity() {
		src = ops.ToSRGB(src, t)
	}
	return src, nil
}

// BMP selects BMP output. The file is written bottom row first, so the rows
// are kept in working storage (a temporary file when they do not fit in
// the memory limit) until the last one has arrived.
func (m *Image) BMP() *Image { m.format, m.options = "bmp", nil; return m }

// TIFFCompression is how TIFF output is compressed.
type TIFFCompression int

const (
	TIFFNone    TIFFCompression = iota // uncompressed, the default
	TIFFDeflate                        // zlib
)

// TIFFOptions are the optional settings of TIFF.
type TIFFOptions struct {
	Compression TIFFCompression
}

// TIFF selects TIFF output.
func (m *Image) TIFF(opts ...TIFFOptions) *Image {
	m.atMostOne("TIFF", len(opts))
	m.format, m.options = "tiff", first(opts)
	return m
}

// WebPOptions are the optional settings of WebP.
type WebPOptions struct {
	// Quality is 1 to 100 for lossy output; 0 means the default, 75.
	Quality int
	// Lossless keeps every pixel exactly. Quality is then not used.
	Lossless bool
	// Effort, for lossless output, trades time for size: 1 is the fastest,
	// 9 gives the smallest files; 0 means the default.
	Effort int
}

// WebP selects WebP output: lossy by default, like a JPEG that can also
// hold transparency, or lossless. Both encoders are part of this module
// and work a few rows at a time; what they must keep until the end of the
// image (already compressed data) goes to working storage.
func (m *Image) WebP(opts ...WebPOptions) *Image {
	m.atMostOne("WebP", len(opts))
	m.format, m.options = "webp", first(opts)
	return m
}

// GIFOptions are the optional settings of GIF.
type GIFOptions struct {
	// Colors is the largest number of colours used, 1 to 256; 0 means 256.
	// In an animation one of them is the transparent entry that frames
	// written as what changed use for "as before".
	Colors int
	// NoDither turns off the dithering that hides the missing colours:
	// every pixel gets the nearest colour.
	NoDither bool
	// FixedPalette uses the fixed palette of the standard library (Plan 9;
	// its first Colors entries) instead of colours chosen for the image.
	// The file is then the one gif.Encode writes (with NoDither, with
	// draw.Src as its Drawer) and needs no second look at the pixels, but
	// it is a much worse picture: a photograph comes out some 14 dB lower.
	// An animation is then written frame by frame as it is read, each
	// frame whole (see Animated).
	FixedPalette bool
}

// GIF selects GIF output. An image that is not already paletted with few
// enough colours is reduced to at most 256 colours chosen for it, with
// dithering; the pixels are kept in working storage (a temporary file
// beyond its memory share) while the colours are chosen. A GIF pixel is
// either transparent or opaque: pixels less than half opaque become
// transparent.
func (m *Image) GIF(opts ...GIFOptions) *Image {
	m.atMostOne("GIF", len(opts))
	m.format, m.options = "gif", first(opts)
	return m
}

// ErrFileSize is returned (wrapped) when MaxFileSize asks for a file smaller
// than the lowest quality can make.
var ErrFileSize = errors.New("goimgtools: cannot reach the requested file size")

// MaxFileSize makes the output at most n bytes by lowering the quality: the
// highest quality that fits is used, never more than the one asked for with
// JPEG or WebP (or their default). It applies to JPEG and lossy WebP
// output. When even quality 1 is too large the result is an ErrFileSize;
// shrink the image (Resize) to get further.
//
// The image is encoded several times (up to about eight), so this costs
// that many times the encoding time; decoding and the operations run once,
// their result kept in working storage (a temporary file beyond its memory
// share).
func (m *Image) MaxFileSize(n int64) *Image {
	if n <= 0 {
		return m.fail(errors.New("goimgtools: MaxFileSize must be positive"))
	}
	m.maxBytes = n
	return m
}

// JPEGOptions are the optional settings of JPEG. Both make the file
// smaller without changing a single pixel; both need the whole image
// before the first byte can be written, so its coefficients are kept in
// working storage (a temporary file beyond its memory share), which takes
// some extra time.
type JPEGOptions struct {
	// Quality is 1 to 100; 0 means the default, 80. It is where the
	// quality given to JPEG goes, and is for ToFormat, which has no
	// argument for it.
	Quality int
	// Optimize builds the coding tables for this image instead of using
	// the standard ones.
	Optimize bool
	// Progressive writes a progressive JPEG, which is smaller still (it
	// includes Optimize) and shows coarse to fine while it loads.
	Progressive bool
	// Trellis chooses each quantized coefficient to save bits where the
	// eye loses least, as mozjpeg does ("trellis quantization"): a smaller
	// file at about the same look. Unlike Optimize and Progressive it
	// changes the pixels (plain rounding is the most faithful choice, not
	// the smallest). It goes with either of them.
	Trellis bool
}

// JPEG selects JPEG output. quality is 1–100; 0 means the default, 80.
// Without options the file is the one the standard library writes.
func (m *Image) JPEG(quality int, opts ...JPEGOptions) *Image {
	m.atMostOne("JPEG", len(opts))
	o := first(opts)
	if quality != 0 {
		o.Quality = quality
	}
	m.format, m.options = "jpeg", o
	return m
}

// DefaultPNGLevel is the compression level of PNG output when PNGOptions
// does not set one. It is 7, not the 6 of the standard library, because
// Go's level 6 finds far fewer repeats than zlib's: at 7 smooth images
// (gradients, skies, graphics) come out about five times smaller and
// photos 4 to 25% smaller, in a similar time for small images and up to
// 70% longer for large photos (24 megapixels). Level 6 or below gives the
// standard library's speed.
const DefaultPNGLevel = 7

// PNGOptions are the optional settings of PNG.
type PNGOptions struct {
	// CompressionLevel is 1 (fastest) to 9 (smallest); 0 means
	// DefaultPNGLevel.
	CompressionLevel int
	// Colors, 2 to 256, reduces the image to that many colours, chosen for
	// this image, and writes a paletted PNG. This is how PNG files are made
	// much smaller ("lossy PNG"): a photo typically shrinks to well under
	// half, with dithering hiding the missing colours. Transparency is
	// kept. Zero keeps every colour (PNG is then lossless).
	//
	// The palette can only be chosen once the whole image has been seen,
	// so the pixels are kept in working storage (a temporary file beyond
	// its memory share) and read a second time.
	Colors int
	// NoDither turns off the dithering Colors uses: flat areas instead of
	// fine grain, which compresses better and shows bands in gradients.
	NoDither bool
}

// PNG selects PNG output.
func (m *Image) PNG(opts ...PNGOptions) *Image {
	m.atMostOne("PNG", len(opts))
	m.format, m.options = "png", first(opts)
	return m
}

// ToWriter runs the chain and writes the result to w. If it returns an
// error, what was written to w is incomplete and must be discarded.
func (m *Image) ToWriter(w io.Writer) error { return m.write(w, "") }

// ToBytes runs the chain and returns the encoded result.
func (m *Image) ToBytes() ([]byte, error) {
	var buf bytes.Buffer
	if err := m.write(&buf, ""); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ToFile runs the chain and writes the result to path. Without an explicit
// output format the file extension decides. The file appears only if
// everything succeeded.
func (m *Image) ToFile(path string) (err error) {
	tmp, err := createBeside(path)
	if err != nil {
		return err
	}
	// Whatever goes wrong from here on, the unfinished file is removed.
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(tmp.Name())
		}
	}()
	bw := bufio.NewWriterSize(tmp, 64<<10)
	if err = m.write(bw, strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")); err != nil {
		return err
	}
	if err = bw.Flush(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *Image) write(w io.Writer, ext string) (err error) {
	defer guard(&err)
	if m.err != nil {
		return m.err
	}
	if m.ctx != nil {
		if err := m.ctx.Err(); err != nil {
			return err
		}
	}
	f, inFormat, err := m.openInput()
	if err != nil {
		return err
	}
	format, err := m.outputFormat(ext, inFormat)
	if err != nil {
		f.Close()
		return err
	}
	if m.animated && inFormat == "gif" && format == "gif" {
		defer f.Close()
		return m.writeAnimated(w, f, m.newEnv())
	}
	src, closer, inFormat, meta, err := m.buildFrom(nil, f, inFormat, false)
	if err != nil {
		return err
	}
	exif, icc := m.outputMeta(meta)
	defer closer.Close()
	src, icc = m.manageColour(src, icc, format)
	if m.threadCount() >= 3 {
		src = pipeline.Async(src)
	}
	defer src.Close()
	if m.maxBytes > 0 {
		return m.writeWithin(w, src, format, exif, icc)
	}
	return m.encode(w, src, format, 0, exif, icc)
}

// createBeside creates a new, empty file in the directory of path, for a
// result that is given the name path once it is complete.
//
// The file gets the permissions any new file gets (read and write for
// everyone, less what the umask takes away), or those of the file at path
// when there is one to replace. A temporary file as os.CreateTemp makes it
// can be read by its owner alone, which is right for working storage and
// wrong for a result: a web server running as another user could not
// serve the image.
func createBeside(path string) (*os.File, error) {
	mode := os.FileMode(0o666)
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		mode = st.Mode().Perm() | 0o200 // its writer can always write it
	}
	for try := 0; ; try++ {
		name := filepath.Join(filepath.Dir(path), ".goimgtools-"+strconv.FormatUint(rand.Uint64(), 36))
		f, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode)
		if err == nil || !os.IsExist(err) || try == 100 {
			return f, err
		}
	}
}

// outputMeta returns what of the input's metadata goes into the output.
func (m *Image) outputMeta(meta pipeline.Meta) (exif, icc []byte) {
	if m.keepExif {
		exif = meta.Exif
		if m.autoOrient {
			exif = pipeline.ExifUpright(exif)
		}
	}
	if !m.stripICC {
		icc = meta.ICC
	}
	return exif, icc
}

// encode writes src in the given format. quality, when positive, replaces
// the quality asked for.
func (m *Image) encode(w io.Writer, src pipeline.Source, format string, quality int, exif, icc []byte) error {
	if m.ctx != nil {
		// Also here: an encoder can work for a long time on pixels that
		// were read long ago (a second pass, another try at a file size).
		src = cancelable{src, m.ctx}
	}
	def := formatNamed(format)
	if def == nil || def.encode == nil {
		return fmt.Errorf("goimgtools: cannot write %s (%s)", format, formatList())
	}
	o := EncodeOptions{Exif: exif, ICC: icc, Options: m.optionsFor(format)}
	if def.quality != nil {
		if q, ok := def.quality(o.Options); ok {
			o.Quality = q
		}
	}
	if quality > 0 {
		o.Quality = quality
	}
	return def.encode(w, src, o)
}

// optionsFor returns the output options that belong to format: the ones
// given together with it, and none when the format was decided by the file
// name or the input.
func (m *Image) optionsFor(format string) any {
	if m.format == format {
		return m.options
	}
	return nil
}

// writeWithin encodes src at the highest quality whose file is at most
// m.maxBytes.
func (m *Image) writeWithin(w io.Writer, src pipeline.Source, format string, exif, icc []byte) error {
	def := formatNamed(format)
	if def == nil || def.encode == nil {
		return fmt.Errorf("goimgtools: cannot write %s (%s)", format, formatList())
	}
	if def.quality == nil {
		return fmt.Errorf("goimgtools: MaxFileSize applies to JPEG and lossy WebP output, not %s", format)
	}
	top, ok := def.quality(m.optionsFor(format))
	if !ok {
		return fmt.Errorf("goimgtools: MaxFileSize applies to JPEG and lossy WebP output, not lossless %s", format)
	}
	if top < 1 || top > 100 {
		return errors.New("goimgtools: quality must be 1 to 100")
	}
	rec, err := pipeline.Record(src)
	if err != nil {
		return err
	}
	defer rec.Close()
	env := pipeline.EnvOf(src)
	var best *pipeline.Spill // the largest file found that fits
	defer func() {
		if best != nil {
			best.Close()
		}
	}()
	// try encodes at quality q and reports whether the file fits; a file
	// that fits replaces the best one so far.
	try := func(q int) (bool, int64, error) {
		in, err := rec.Source()
		if err != nil {
			return false, 0, err
		}
		out := pipeline.NewSpill(env)
		err = m.encode(out, in, format, q, exif, icc)
		in.Close()
		if err != nil {
			out.Close()
			return false, 0, err
		}
		size := out.Size()
		if size > m.maxBytes {
			out.Close()
			return false, size, nil
		}
		if best != nil {
			best.Close()
		}
		best = out
		return true, size, nil
	}
	fits, size, err := try(top)
	if err != nil {
		return err
	}
	if !fits {
		// File size grows with quality: find the highest quality below top
		// that fits. lo is the highest known to fit (0: none yet), hi the
		// lowest known not to; the answer is lo once they are neighbours.
		lo, hi := 0, top
		loSize, hiSize := int64(0), size
		probe := func(q int) error {
			fits, size, err := try(q)
			if err != nil {
				return err
			}
			if fits {
				lo, loSize = q, size
			} else {
				hi, hiSize = q, size
			}
			return nil
		}
		// A first guess: as if size went with quality. When that is still
		// too large, quality 1 settles whether the limit can be met at all
		// and gives the search its lower end.
		if err := probe(max(1, min(top-1, int(float64(top)*float64(m.maxBytes)/float64(size))))); err != nil {
			return err
		}
		if lo == 0 && hi > 1 {
			if err := probe(1); err != nil {
				return err
			}
		}
		if lo == 0 {
			return fmt.Errorf("%w: quality 1 gives %d bytes, the limit is %d", ErrFileSize, hiSize, m.maxBytes)
		}
		// Then read the next quality off a straight line between the two
		// ends (in the logarithm of the size, which is close to straight).
		// That usually lands within a step or two of the answer; when it
		// keeps creeping from one side instead, halve the interval.
		limit := math.Log(float64(m.maxBytes))
		for slow := 0; hi-lo > 1; {
			width := hi - lo
			q := (lo + hi) / 2
			if slow < 2 {
				a, b := math.Log(float64(loSize)), math.Log(float64(hiSize))
				q = max(lo+1, min(hi-1, lo+int(float64(width)*(limit-a)/(b-a))))
			} else {
				slow = 0
			}
			if err := probe(q); err != nil {
				return err
			}
			if hi-lo > width/2 {
				slow++
			} else {
				slow = 0
			}
		}
	}
	_, err = io.Copy(w, io.NewSectionReader(best, 0, best.Size()))
	return err
}
