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
	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/pngstream"
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
	// Effort trades time for size: 1 is the fastest, 9 gives the smallest
	// files; 0 means the default, 5. For lossy output, effort 5 already
	// does all there is (sharp colour conversion, trellis quantization and
	// loop filter levels chosen by measuring); 1 and 2 leave them out and
	// take less than half the time, for files about 3% larger at the same
	// look, 3 and 4 leave out the measured loop filter. Above 5, only
	// transparency gets more: every filter of the alpha plane is tried, and
	// planes of more than 4 megapixels get the optimal parse too.
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
	// Lossy, 1 to 255, lets the compression take a colour up to this far
	// (distance in RGB, 0 to 255 a channel) from the pixel's where that
	// makes the data shorter (the lossy LZW of gifsicle, written from its
	// description); 0, the default, keeps every pixel. 16 to 32 suit
	// photographic animations: on one of 557KB (400x533, 8 frames) 16
	// made it 18% smaller and 24 32% smaller, looking better at the same
	// size than fewer Colors (SSIMULACRA2 79 at 453KB against 77 at 501KB
	// with 128 colours). A dithered still image loses more (8 to 16 suit
	// it), pictures of a few flat colours gain little.
	Lossy int
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
// It does not apply to an animation that Animated keeps, which is then an
// error wrapping errors.ErrUnsupported (a GIF of one frame is written as a
// still image, within the size).
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
	// includes Optimize) and shows coarse to fine while it loads. Its scans
	// are chosen as mozjpeg chooses them, for the smallest file; that takes
	// some more time, shared among the Threads.
	Progressive bool
	// Trellis chooses each quantized coefficient to save bits where the
	// eye loses least, as mozjpeg does ("trellis quantization"): a smaller
	// file at about the same look. Unlike Optimize and Progressive it
	// changes the pixels (plain rounding is the most faithful choice, not
	// the smallest). It goes with either of them.
	Trellis bool
	// QuantTable picks the quantization tables (see QuantTable for the
	// choices). The zero value, QuantTableStandard, is what the standard
	// library uses; QuantTableJpegli gives the smallest files.
	QuantTable QuantTable
}

// QuantTable is a set of JPEG quantization tables: how coarsely each DCT
// coefficient of brightness and of colour is kept, scaled by the quality.
// The values 0 to 8 are numbered as mozjpeg's -quant-table numbers them.
type QuantTable int

const (
	// QuantTableStandard is the tables of the JPEG standard (Annex K),
	// which the standard library and libjpeg use. A JPEG saved again at
	// the quality it was made with keeps closest to itself with them,
	// since they are most likely what it was made with.
	QuantTableStandard QuantTable = iota
	// QuantTableFlat quantizes every coefficient alike.
	QuantTableFlat
	// QuantTableMSSSIM is tuned for MS-SSIM (mozjpeg).
	QuantTableMSSSIM
	// QuantTableImageMagick is N. Robidoux's tables, from ImageMagick:
	// mozjpeg's default. They quantize the finest detail more coarsely and
	// colour more finely: on photographs, at the same file size, colour
	// comes out closer to the original (the file is 2 to 15% smaller at
	// the same PSNR) and fine detail in brightness about as close (0 to
	// 2% larger at the same luma SSIM).
	QuantTableImageMagick
	// QuantTablePSNRHVS is tuned for PSNR-HVS (mozjpeg).
	QuantTablePSNRHVS
	// QuantTableKlein is from Klein, Silverstein and Carney, "Relevance of
	// human vision to JPEG-DCT compression" (1992).
	QuantTableKlein
	// QuantTableWatson is from Watson, Taylor and Borthwick, "DCTune
	// perceptual optimization of compressed dental X-Rays" (1997).
	QuantTableWatson
	// QuantTableAhumada is from Ahumada, Watson and Peterson, "A visual
	// detection model for DCT coefficient quantization" (1993).
	QuantTableAhumada
	// QuantTablePeterson is from Peterson, Ahumada and Watson, "An improved
	// detection model for DCT coefficient quantization" (1993).
	QuantTablePeterson
	// QuantTableJpegli makes the tables as jpegli (Google, 2024) makes
	// them for the quality: the step of each coefficient follows a curve
	// of its own as the quality falls, and the colour of a 4:2:0 image is
	// quantized more coarsely where the eye misses it. Without Trellis,
	// coefficients that round to one step are dropped unless they are
	// 0.58 to 0.59 of a step from zero (the dead zone of jpegli), not 0.5.
	//
	// On 14 photographs and game artwork, together with Progressive and
	// Trellis, the files are 15% smaller than mozjpeg's defaults at equal
	// SSIMULACRA2 and 12% at equal butteraugli (jpegli itself, at 4:2:0:
	// 13% and 12%). The quality means about what it means for the
	// standard tables: jpegli maps it to a distance meant to look alike.
	QuantTableJpegli
)

// Keep QuantTableJpegli the value jpegstream knows it by.
var _ = [1]struct{}{}[QuantTableJpegli-jpegstream.QuantTableJpegli]

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
// does not set one: 7 in a program built with Go 1.27 or later, 6 (the
// standard library's) with Go 1.26.
//
// Go 1.27 rewrote compress/flate; its level 6 finds far fewer repeats than
// zlib's: at 7 smooth images (gradients, skies, graphics) come out about
// five times smaller and photos 4 to 25% smaller, in a similar time for
// small images and up to 70% longer for large photos (24 megapixels).
// Level 6 or below gives the standard library's speed. With Go 1.26 level
// 7 is only 1 to 1.5% smaller than 6 and takes half as long again.
const DefaultPNGLevel = pngstream.DefaultLevel

// PNGOptions are the optional settings of PNG.
type PNGOptions struct {
	// CompressionLevel is 1 (fastest) to 9 (smallest); 0 means
	// DefaultPNGLevel.
	//
	// Levels 8 and 9 search for the smallest file: each row's filter is
	// chosen by trying them all, as oxipng's Brute strategy does, and the
	// data is compressed with a port of libdeflate's near-optimal parsing
	// (its levels 10 and 12), its blocks split where it saves bits, as
	// zopfli splits them. On screenshots, photographs and game artwork the
	// files are 14% (8) and 15% (9) smaller than at level 7; level 9 is
	// 0.5% smaller than oxipng -o max and 0.55% larger than ECT -9. Level 8
	// takes about 10 times as long as level 7, level 9 about 20 times
	// (oxipng -o max about 130, ECT -9 about 500). They take about 4MB
	// (level 8) and 6.5MB (level 9) more memory per thread, and give the
	// same file with every Go version.
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
	var env *pipeline.Env
	if m.animated {
		env = m.newEnv()
		keeps := keepsAnimation(inFormat, format)
		if keeps && m.maxBytes == 0 {
			defer f.Close()
			return m.writeAnimated(w, f, env, format)
		}
		var err error
		if keeps {
			err = m.refuseMaxSize(f, inFormat, env)
		} else {
			err = m.refuseAnimation(f, inFormat, format, env)
		}
		if err != nil {
			f.Close()
			return err
		}
	}
	src, closer, inFormat, meta, err := m.buildFrom(env, true, f, inFormat, false)
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
