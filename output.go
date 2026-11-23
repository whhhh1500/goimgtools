package goimgtools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"image/color"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/whhhh1500/goimgtools/internal/density"
	"github.com/whhhh1500/goimgtools/internal/exifedit"
	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/internal/metric"
	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/pdfwrite"
	"github.com/whhhh1500/goimgtools/internal/pngstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The output: choosing the format and its settings, and writing the
// result. The formats themselves are in formats.go.

// KeepExif copies the EXIF block of the input (camera, date, GPS, ...) to
// the output. By default it is dropped. After Rotate() the copied block
// says the image is upright.
func (m *Image) KeepExif() *Image { m.keepExif = true; return m }

// ExifFields are text fields of the EXIF block that SetExif writes. An
// empty one is left as it is.
type ExifFields struct {
	// Copyright is who holds the rights to the image.
	Copyright string
	// Artist is who made it: the photographer, the author.
	Artist string
	// ImageDescription says what it shows: a title or a caption.
	ImageDescription string
	// Software is the program that made the file.
	Software string
	// DateTime is when the file was last changed, written in its own
	// time zone as EXIF writes it (2006:01:02 15:04:05); the zero time
	// leaves it as it is.
	DateTime time.Time
}

// SetExif writes fields into the EXIF block of the output, as sharp's
// withExif and withExifMerge do: into the input's block when KeepExif
// keeps it, every other field and directory as it was (a maker note's
// offsets stay right), and else into a block of their own. JPEG, PNG,
// WebP and TIFF hold it; GIF and BMP have no place for one. Calls add up,
// a later value winning. The text is written as it is, UTF-8, as exiftool
// writes it (EXIF's own type for it is ASCII, which a few old readers show
// byte by byte).
func (m *Image) SetExif(f ExifFields) *Image {
	add := func(tag uint16, v string) {
		if v != "" {
			m.exifSet = append(m.exifSet[:len(m.exifSet):len(m.exifSet)], exifedit.Field{Tag: tag, Value: v})
		}
	}
	add(270, f.ImageDescription)
	add(305, f.Software)
	if !f.DateTime.IsZero() {
		add(306, f.DateTime.Format("2006:01:02 15:04:05"))
	}
	add(315, f.Artist)
	add(33432, f.Copyright)
	return m
}

// KeepXmp copies the XMP packet of the input (Adobe's metadata: keywords,
// rating, rights, the history of edits) to the output, for JPEG, PNG, WebP
// and TIFF, as sharp's keepXmp. By default it is dropped.
func (m *Image) KeepXmp() *Image { m.keepXmp = true; return m }

// KeepIptc copies the IPTC record of the input (the press's metadata:
// caption, keywords, byline, copyright notice) to the output, in JPEG (a
// Photoshop resource in an APP13 segment) and TIFF (tag 33723), as sharp's
// keepMetadata keeps it. By default it is dropped. It is read from JPEG,
// TIFF and Photoshop files.
func (m *Image) KeepIptc() *Image { m.keepIptc = true; return m }

// StripICC drops the ICC colour profile of the input. By default it is
// copied to the output, because the pixel values only mean the right
// colours together with it. Without it the values are taken for sRGB, as
// they are; see ToSRGB for converting them.
func (m *Image) StripICC() *Image { m.stripICC = true; return m }

// Density sets the resolution recorded in the output, in pixels per inch
// (dots per inch, 1 to 65535): for print, the size the image is meant to
// have, the width in pixels divided by dpi inches. JPEG (JFIF), PNG (pHYs),
// TIFF and BMP record it; WebP and GIF have no place for it. When the EXIF
// block is kept (KeepExif), its resolution fields are set to it too.
// Without Density, the resolution of the input is kept (as libvips and
// sharp do), also when the image is resized. Density(0) records none
// (TIFF, which must, records 72; a kept EXIF block is left as it is).
func (m *Image) Density(dpi float64) *Image {
	if dpi != 0 && !(dpi >= 1 && dpi <= 65535) {
		return m.fail(errors.New("goimgtools: Density must be 1 to 65535 pixels per inch, or 0 for none"))
	}
	m.density, m.densitySet = dpi, true
	return m
}

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
// ProPhoto), those of lookup tables by their perceptual table, and those
// of grey images (a dot gain) by their tone curve, the image staying grey;
// the black point of the last two kinds is compensated as LittleCMS does
// it. (A CMYK image is turned into sRGB through its profile when it is
// decoded, whether or not this is asked for; a CMYK file without one
// through libvips's CMYK profile, as sharp does.) A converted image has 8
// bits a channel.
func (m *Image) ToSRGB() *Image { m.toSRGB = true; return m }

// ToProfile converts the colours of the image into the colour space of the
// ICC profile icc at the end of the recipe, and writes the profile with
// the output: from the input's profile when it is one of primaries and
// tone curves (directly, so that colours sRGB cannot show are kept), from
// sRGB otherwise (an input's other profile taken to sRGB first). The
// profile must be an RGB one of primaries and tone curves, such as
// ProfileDisplayP3 and ProfileAdobeRGB give; the rendering is relative
// colorimetric, as LittleCMS converts, to within one level; the result has
// 8 bits a channel, as with ToSRGB. For an output format that cannot carry
// a profile (GIF, BMP, ICO, QOI, ...), for tiles and for an animation that
// Animated keeps it is an error.
func (m *Image) ToProfile(icc []byte) *Image {
	p, err := iccprof.Parse(icc)
	if err == nil {
		_, err = iccprof.Between(nil, p)
	}
	if err != nil {
		return m.fail(fmt.Errorf("goimgtools: ToProfile: %w", err))
	}
	m.toProfile = append([]byte(nil), icc...)
	return m
}

// ProfileDisplayP3 returns an ICC profile of Display P3 (the colour space
// of Apple's displays and of the photographs of its phones): DCI-P3
// primaries, the white of D65 and the tone curve of sRGB.
func ProfileDisplayP3() []byte {
	return iccprof.RGBProfile("Display P3", [2]float64{0.680, 0.320}, [2]float64{0.265, 0.690}, [2]float64{0.150, 0.060}, [2]float64{0.3127, 0.3290}, 0)
}

// ProfileAdobeRGB returns an ICC profile of Adobe RGB (1998), a space for
// print wider than sRGB in the greens and cyans: its primaries, the white
// of D65 and a gamma of 563/256.
func ProfileAdobeRGB() []byte {
	return iccprof.RGBProfile("Adobe RGB (1998)", [2]float64{0.64, 0.33}, [2]float64{0.21, 0.71}, [2]float64{0.15, 0.06}, [2]float64{0.3127, 0.3290}, 563.0/256)
}

// manageColour converts src to sRGB when its profile asks for it: on
// ToSRGB, or for an output format that cannot carry the profile; or into
// the profile of ToProfile. It returns the source to encode and the
// profile to write.
func (m *Image) manageColour(src pipeline.Source, icc []byte, format string) (pipeline.Source, []byte) {
	if len(m.toProfile) > 0 {
		target, err := iccprof.Parse(m.toProfile)
		if err != nil {
			return src, icc // ToProfile checked it
		}
		var from *iccprof.Profile
		if len(icc) > 0 {
			if p, err := iccprof.Parse(icc); err == nil {
				if _, err := iccprof.Between(p, target); err == nil {
					from = p
				} else if t := p.ToSRGB(); !t.Identity() && p.Channels() == 3 {
					src = ops.ToSRGB(src, t)
				} else if p.Channels() == 1 {
					if t := p.ToSRGB(); !t.Identity() {
						src = ops.ToSRGB(src, t)
					}
				}
			}
		}
		if t, err := iccprof.Between(from, target); err == nil {
			src = ops.ToSRGB(src, t)
		}
		return src, m.toProfile
	}
	if len(icc) == 0 {
		return src, icc
	}
	if model := src.ColorModel(); model != color.GrayModel && model != color.Gray16Model && len(icc) >= 20 && string(icc[16:20]) == "GRAY" {
		// A grey profile on what is no longer grey (after a coloured
		// overlay, say), which pixels of colour cannot carry: each channel
		// is taken through its curve as a grey would be, to sRGB.
		if p, err := iccprof.Parse(icc); err == nil && p.Channels() == 1 {
			if t := p.ToSRGB(); !t.Identity() {
				src = ops.ToSRGB(src, t)
			}
			return src, nil
		}
	}
	if def := formatNamed(format); !m.toSRGB && (def == nil || !def.noProfile) {
		return src, icc
	}
	p, err := iccprof.Parse(icc)
	if err != nil {
		return src, icc // not one this package can convert from
	}
	switch {
	case p.Channels() == 1 && src.ColorModel() == color.GrayModel:
		// A grey image stays grey.
		if t := p.ToSRGB(); !t.Identity() {
			src = ops.GrayToSRGB(src, t)
		}
	case p.Channels() == 3:
		if t := p.ToSRGB(); !t.Identity() {
			src = ops.ToSRGB(src, t)
		}
	default:
		return src, icc
	}
	return src, nil
}

// BMP selects BMP output. The file is written bottom row first, so the rows
// are kept in working storage (a temporary file when they do not fit in
// the memory limit) until the last one has arrived.
func (m *Image) BMP() *Image { m.format, m.options = "bmp", nil; return m }

// QOI selects QOI output (the "Quite OK Image" format, qoiformat.org):
// lossless, eight bits, RGB when the image is known to be opaque and RGBA
// otherwise, written and read about as fast as the pixels are copied. The
// file is the reference encoder's and Pillow's, but that its colour space is
// marked sRGB (Pillow marks it linear unless told otherwise).
func (m *Image) QOI() *Image { m.format, m.options = "qoi", nil; return m }

// PNM selects Netpbm output, as Pillow writes it: PGM (P5) for a grey image,
// PPM (P6) for any other, with 16 bits a value when the image has them.
// Netpbm has no transparency: it is laid over black, as for JPEG. Read are
// also PBM, PAM (with transparency) and PFM, in text or binary.
func (m *Image) PNM() *Image { m.format, m.options = "pnm", nil; return m }

// TGA selects Truevision TGA (Targa) output, the format of game textures:
// run-length compressed, top row first, grey for a grey image,
// colour-mapped for one of at most 256 colours, otherwise true colour, 24
// bits when the image is known to be opaque and 32 with alpha otherwise;
// as Pillow writes it with compression "tga_rle" and orientation 1, to the
// byte.
func (m *Image) TGA() *Image { m.format, m.options = "tga", nil; return m }

// ICNSOptions are the optional settings of ICNS.
type ICNSOptions struct {
	// Sizes are the sides of the icons to write, among 16, 32, 64, 128,
	// 256, 512 and 1024; by default the ones up to the image's own (16 for
	// a smaller image).
	Sizes []int
	// CompressionLevel is the PNG level of each image, as for ICOOptions;
	// 0 means 9.
	CompressionLevel int
}

// ICNS selects ICNS output, the icon file of macOS: square PNG images of
// standard sizes (ICNSOptions.Sizes), the image fitted into each in its
// proportions (Lanczos3) and centred on transparency, each also standing
// for the Retina image of half its size, with a table of contents, as
// iconutil writes it. An ICNS file read is its largest image, PNG or one
// of the older run-length ones.
func (m *Image) ICNS(opts ...ICNSOptions) *Image {
	m.atMostOne("ICNS", len(opts))
	m.format, m.options = "icns", first(opts)
	return m
}

// PDFOptions are the settings of PDF output.
type PDFOptions struct {
	// Quality, 1 to 100, stores the picture as a JPEG of that quality,
	// much smaller for a photograph; 0 (the default) stores it without
	// loss.
	Quality int
}

// PDF selects PDF output: a document of one page the size of the image at
// its resolution (72 dots per inch when it records none, so a pixel is a
// point), the picture stored without loss (Flate, rows filtered as PNG
// filters them) or as a JPEG (PDFOptions.Quality), its transparency as a
// soft mask and its colour profile kept. The document is written as the
// image is read; the soft mask waits in working storage until the colours
// are done.
func (m *Image) PDF(opts ...PDFOptions) *Image {
	m.atMostOne("PDF", len(opts))
	m.format, m.options = "pdf", first(opts)
	return m
}

// TIFFCompression is how TIFF output is compressed.
type TIFFCompression int

const (
	TIFFNone    TIFFCompression = iota // uncompressed, the default
	TIFFDeflate                        // zlib
	TIFFLZW                            // LZW, as libtiff writes it
	// TIFFJPEG is JPEG, as libtiff writes it for libvips and sharp (whose
	// TIFFs are JPEG by default): lossy, far smaller for photographs;
	// colour as YCbCr at half the width and height below quality 90 and
	// in full from there, as libvips writes it, grey as grey, every strip a
	// JPEG stream sharing its tables. It cannot keep transparency: an image
	// with transparent pixels is an error (Flatten it first).
	TIFFJPEG
)

// TIFFOptions are the optional settings of TIFF.
type TIFFOptions struct {
	Compression TIFFCompression
	// Predictor stores each sample as its difference from the same
	// sample of the pixel to its left (horizontal differencing), which
	// makes photographs compress much better with TIFFDeflate and TIFFLZW
	// (sharp's predictor "horizontal", its default). It does nothing for
	// uncompressed and paletted images.
	Predictor bool
	// Quality is the JPEG quality of TIFFJPEG, 1 to 100; 0 means 80, as
	// sharp's.
	Quality int
	// Tile, when not 0, writes the image in square tiles of this many
	// pixels a side (a multiple of 16, 16 to 8192; libvips and sharp use
	// 256) instead of strips, each tile compressed on its own, so that a
	// reader can take a part of the image without the rest. Samples are
	// then 8 bits (grey, RGB, or RGB with alpha).
	Tile int
	// Pyramid writes, after the image in tiles (of 256 when Tile is 0),
	// the image at half the size, a quarter and so on down to one that
	// fits in a tile, as the pyramid of libvips and sharp: what an image
	// server (IIIF) or a zooming viewer reads. A row of tiles of each
	// level is held in memory while it fills.
	Pyramid bool
}

// TIFF selects TIFF output.
func (m *Image) TIFF(opts ...TIFFOptions) *Image {
	m.atMostOne("TIFF", len(opts))
	m.format, m.options = "tiff", first(opts)
	return m
}

// WebPOptions are the optional settings of WebP.
type WebPOptions struct {
	// Quality is 1 to 100 for lossy output (and with NearLossless, how
	// near); 0 means the default, 75.
	Quality int
	// Lossless keeps every pixel exactly, the colour of fully transparent
	// ones too with Exact. Quality is then not used.
	Lossless bool
	// Effort trades time for size: 1 is the fastest, 9 gives the smallest
	// files; 0 means the default, 5. For lossy output, effort 5 already
	// does all there is (sharp colour conversion, trellis quantization and
	// loop filter levels chosen by measuring); 2 leaves them out and takes
	// about half the time, for files about 3% larger at the same look, 4
	// leaves out the measured loop filter, and 3 also weighs the 4x4 modes
	// without the trellis, which it keeps for the mode chosen (as libwebp's
	// method 5; 15% less time than 4, files about 1% larger at the same
	// look). 1 is 2 with the 16x16 and chroma predictors picked by the
	// error of their prediction and only those coded (as libwebp's methods
	// 0 to 2 pick them), and two rather than four 4x4 modes coded for each
	// block: 28% less time than 2, files 2.5% to 4% larger. Above 5, only
	// transparency gets more: every filter of the alpha plane is tried, and
	// planes of more than 4 megapixels get the optimal parse too. Lossless
	// output gets more at every level: on photographs 3 takes a quarter of
	// the time of 5 for files 1% larger, and 1 an eighth for 4%;
	// screenshots and drawings gain more from 4, which has the optimal
	// parse (6% over 3, in 3.5 times its time); 5 adds 0.6%, in 1.7 times
	// the time of 4, and 7 another 0.3%.
	Effort int
	// NearLossless writes lossless WebP whose pixels may come back a
	// little changed where they differ from a neighbour by more than the
	// change anyway, never in smooth areas (libwebp's near-lossless, as
	// sharp's nearLossless and cwebp -near_lossless): Quality then says
	// how much, 100 nothing (a lossless file), 80 to 99 a channel at most
	// 1 level, 60 to 79 2, 40 to 59 4, 20 to 39 8 and below 16; 0 means
	// the default, 75. Alpha 0 and 255 stay as they are. It needs no
	// Lossless. On photographs 60 makes the file 37% smaller than lossless
	// (6 Kodak photographs), and 2.9% smaller than cwebp -near_lossless 60
	// (7.3% on 23 photographs, screenshots and drawings), in about twice the
	// time of lossless, as libwebp takes; screenshots and drawings, which
	// have few pixels to round and code well without loss, gain much less.
	// A variant that stays lossless runs beside, and wins where a pattern
	// repeats exactly, which rounding would break; a colour table, which
	// images of at most 256 colours may get, stays lossless too. Animated
	// frames are written the same way.
	NearLossless bool
	// Exact keeps the colour of fully transparent pixels exactly. By
	// default, as libwebp, sharp and Pillow do, it is left to the encoder,
	// as it cannot be seen: lossless output stores those pixels as the
	// colour their neighbours predict, lossy output smooths them out. That
	// makes images with transparency smaller, much smaller when what is
	// under the transparent pixels is not flat (a cut-out photograph 27%;
	// icons and drawings change by about 1%, either way); Exact is for
	// images whose colour is used under the transparency too (a mask, or a
	// texture whose alpha is replaced later).
	Exact bool
	// AlphaQuality, 1 to 99, makes the transparency of lossy output lossy
	// too (libwebp's alpha_q, sharp's alphaQuality): its values are reduced
	// to fewer levels chosen for the image (2 to 16 up to 70, up to 256 at
	// 99; the fully transparent and the fully opaque ones stay) before they
	// are coded. 0 and 100 keep it exactly, the default.
	AlphaQuality int
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
	// photographic animations: on one of 556KB (400x533, 8 frames) 16
	// made it 24% smaller and 24 39% smaller, looking better at about the
	// same size than fewer Colors (SSIMULACRA2 85.7 at 486KB with 12
	// against 82.8 at 471KB with 128 colours). A dithered still image
	// loses more (8 to 16 suit it), pictures of a few flat colours gain
	// little.
	Lossy int
	// Effort trades time for size and exactness, as WebPOptions.Effort
	// and sharp's effort for GIF: 1 is the fastest, 9 the most thorough;
	// 0 means 9, what this package has always done. The colours chosen
	// are moved toward the pixels three times from 7 on, once from 4 to
	// 6, not at all below; and only at 9 is each frame of an animation
	// coded five ways (clearing the code table when it fills, or putting
	// that off) to keep the smallest. On an animation of photographs,
	// effort 1 took 0.30s where 9 took 0.70s, for a file 0.6% larger; a
	// still photograph is 0.3 dB less exact at 1 than at 9.
	Effort int
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

// ICOOptions are the optional settings of ICO.
type ICOOptions struct {
	// Sizes are the sides of the icons to write, 1 to 256 pixels (16, 32,
	// 48 and 256 make a favicon.ico for every screen): the image resized to
	// fit each, in its proportions, as Pillow writes them. Without them the
	// image is written as it is, which must then be at most 256x256.
	Sizes []int
	// CompressionLevel is the PNG level of each image, 1 to 10 as
	// PNGOptions.CompressionLevel; 0 means 9, the smallest files short of
	// 10 (7 writes them several times as fast, a few percent larger).
	CompressionLevel int
}

// ICO selects ICO output, the icon file of Windows and of web sites
// (favicon.ico): one image or several sizes of it (ICOOptions.Sizes), each
// stored as a PNG with transparency, which every Windows since Vista and
// every browser shows. For several sizes the image is kept in working
// storage (a temporary file beyond its memory share) and resized for each.
// An ICO or CUR file read is its largest image, of those as large the one
// with the most colours, as Windows shows it (Pillow takes the one with the
// fewest).
func (m *Image) ICO(opts ...ICOOptions) *Image {
	m.atMostOne("ICO", len(opts))
	m.format, m.options = "ico", first(opts)
	return m
}

// ErrFileSize is returned (wrapped) when MaxFileSize asks for a file smaller
// than the lowest quality (or two colours) can make.
var ErrFileSize = errors.New("goimgtools: cannot reach the requested file size")

// MaxFileSize makes the output at most n bytes by lowering the quality: the
// highest quality that fits is used, never more than the one asked for with
// JPEG or WebP (or their default). It applies to JPEG and lossy WebP
// output (and near-lossless WebP, whose quality is how near, and TIFF with
// TIFFJPEG), and to PNG and GIF, whose quality is their number of colours: a
// PNG is first tried without loss, then with the most colours (at most
// PNGOptions.Colors or GIFOptions.Colors when set) that fit, as Colors
// reduces them. When even quality 1 (two colours) is too large the result
// is an ErrFileSize; shrink the image (Resize) to get further.
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

// MinSSIM makes the output as small as the quality allows while it stays
// at least s alike to the image (its SSIM, see inspect.SSIM; 0 to 1): the
// lowest quality whose file does, as jpeg-recompress and similar tools
// choose it: from 1 to the quality asked for, or to 100 when none was
// (the file can then be larger than at the default quality). 0.98 is
// about where a difference starts to show; 0.99 and above keep the file
// close to the original. It applies to JPEG and lossy WebP output, TIFF
// with TIFFJPEG, and PNG and GIF, whose quality is their number of
// colours (a PNG with no Colors given stays lossless when even 256
// colours fall short). When even the highest quality falls short it is
// written at that.
//
// The image is encoded, decoded and compared some eight times; it is kept
// in working storage meanwhile (a temporary file beyond its memory share).
// It does not go with MaxFileSize, nor with an animation that Animated
// keeps (an error wrapping errors.ErrUnsupported). A transparent image
// written as JPEG is compared laid on black, as JPEG lays it.
func (m *Image) MinSSIM(s float64) *Image {
	if !(s > 0 && s <= 1) {
		return m.fail(errors.New("goimgtools: MinSSIM must be above 0 and at most 1"))
	}
	m.minSSIM = s
	return m
}

// JPEGOptions are the optional settings of JPEG. Optimize and Progressive
// make the file smaller without changing a single pixel; they need the
// whole image before the first byte can be written, so its coefficients
// are kept in working storage (a temporary file beyond its memory share),
// which takes some extra time. Trellis, QuantTable and ChromaSubsampling
// change the pixels.
type JPEGOptions struct {
	// Quality is 1 to 100; 0 means the default, 80.
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
	// ChromaSubsampling is how finely colour is kept (see
	// ChromaSubsampling): by default at half the width and height, as the
	// standard library and libjpeg keep it; Chroma444 keeps all of it, for
	// crisp coloured text, lines and edges (sharp's chromaSubsampling).
	ChromaSubsampling ChromaSubsampling
	// OvershootDeringing lessens the ringing beside dark text and lines on
	// white (or on any colour at 255 in a channel), as mozjpeg does (sharp's
	// overshootDeringing): the pixels at the largest value of a block that
	// also has others are raised above it along a smooth curve before the
	// transform, which a decoder clamps back to 255, and which leaves less
	// of the ripples that quantization makes of a hard edge. Screenshots
	// and documents look much better at the same quality setting
	// (SSIMULACRA2 +8.5 at 50, +4.3 at 75), for files 0.5 to 7% larger; a
	// screenshot at 50 with it looks as good as at 75 without, in a file
	// 25% smaller. Photographs, with few such pixels, are about unchanged.
	// The file is sharp's with the same settings, byte for byte.
	OvershootDeringing bool
}

// ChromaSubsampling is how finely a JPEG keeps colour (its Cb and Cr
// planes) beside brightness, which it always keeps in full.
type ChromaSubsampling int

const (
	// Chroma420 keeps colour at half the width and half the height
	// (4:2:0): the smallest files, as the standard library and libjpeg
	// write by default. Fine coloured detail (red text, thin coloured
	// lines) blurs.
	Chroma420 ChromaSubsampling = iota
	// Chroma422 keeps colour at half the width and the full height
	// (4:2:2), as many cameras write.
	Chroma422
	// Chroma444 keeps all of the colour (4:4:4): coloured edges stay
	// sharp; the file is larger (about 30% on a photograph at the same
	// quality setting).
	Chroma444
)

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
	// Trellis, the files are 16% smaller than mozjpeg's defaults at equal
	// SSIMULACRA2 and 13% at equal butteraugli (jpegli itself, at 4:2:0:
	// 13% and 12%). The quality means about what it means for the
	// standard tables: jpegli maps it to a distance meant to look alike.
	QuantTableJpegli
)

// Keep QuantTableJpegli the value jpegstream knows it by.
var _ = [1]struct{}{}[QuantTableJpegli-jpegstream.QuantTableJpegli]

// JPEG selects JPEG output, at quality 80 unless JPEGOptions says
// otherwise. The file is what libjpeg-turbo, and so Pillow and sharp,
// write with the same settings, to the byte (see the compatibility notes).
func (m *Image) JPEG(opts ...JPEGOptions) *Image {
	m.atMostOne("JPEG", len(opts))
	m.format, m.options = "jpeg", first(opts)
	return m
}

// DefaultPNGLevel is the compression level of PNG output when PNGOptions
// does not set one: 7, compressed with a port of libdeflate's lazy parsing
// (its level 7, to the byte), the same with every Go version.
//
// Against level 6 (the standard library's compress/flate), on 38 images:
// gradients, skies and icons 1.3 to 4.2 times smaller, screenshots 20 to
// 23% smaller, photographs 1.8 to 7.1% smaller, in 1.85 times the time on
// one core. Against compress/flate's own level 7 (Go 1.27), on 126 images,
// 1.8% smaller on average (from 28% smaller to 2.5% larger), in 19% more
// time. The compressor takes about 2.3MB of memory (0.8MB for an image of
// 150 x 103), counted in the memory limit; level 6 or below gives the
// standard library's speed and memory, and bytes that differ between Go
// 1.26 and 1.27.
const DefaultPNGLevel = pngstream.DefaultLevel

// PNGOptions are the optional settings of PNG.
type PNGOptions struct {
	// CompressionLevel is 1 (fastest) to 10 (smallest); 0 means
	// DefaultPNGLevel.
	//
	// Levels 8 and 9 search for the smallest file: each row's filter is
	// chosen by trying them all, as oxipng's Brute strategy does, and the
	// data is compressed with a port of libdeflate's near-optimal parsing
	// (its levels 10 and 12), its blocks split where it saves bits, as
	// zopfli splits them, and further where their symbols' statistics
	// change, its symbols priced at their entropy, as zopfli prices them,
	// each block written with the smallest of more codes and ways of
	// writing its header, as ECT writes them. On six screenshots and
	// photographs the files are 9 to 15% (8) and 10 to 21% (9) smaller than
	// at level 7; level 9 is 0.9% smaller than oxipng -o max and 0.16%
	// larger than ECT -9. Level 8 takes 5 to 20 times as long as level 7 on
	// one core, level 9 18 to 65 times (oxipng -o max about 6 times as long
	// as level 9, ECT -9 about 25 times). They take about 4MB (level 8) and 6.5MB (level 9) more
	// memory per thread, and up to 1MB for the image data, which from level
	// 8 is written in chunks of 1MB (one for up to that, as ECT and oxipng
	// write it) rather than 32KB; they give the same file with every Go
	// version.
	//
	// Level 10 is smaller still, and much slower: each row's filter is
	// chosen by compressing the rows with each (libdeflate's level 10, the
	// data before as the dictionary), as oxipng's Brute strategy does, and
	// the data is compressed with a port of Zopfli (60 runs of its optimal
	// parse, up to 200 for an image of less than 1MB, with ECT's
	// additions: runs priced by the code lengths after them, the codes and
	// header of each block chosen among more, short matches written as
	// literals where that is smaller; the data split further where its
	// symbols' statistics change, before the parse and after it; its master
	// blocks of 1MB on threads of their own, joined into one stream), each
	// block by level 9's compressor instead where that is smaller; a
	// palette is written in the order its colours first appear, as ECT
	// writes it, without the colours no pixel uses. On the same 17 images
	// it is 0.39% smaller than level 9 and 0.23% smaller than ECT -9
	// (smaller on 15 of them), in 438s with 8 threads against level 9's
	// 31.5s (ECT: 606s on 6 cores). Its compressor takes about 50MB for
	// each thread compressing: with the default memory limit it compresses
	// on one thread, with a limit of 480MB on 8.
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
// everything succeeded. A path ending in .dzi, .dz or .zip is ToTiles (a
// Deep Zoom pyramid, with the default TileOptions; in a ZIP archive for
// .zip), as sharp's toFile.
func (m *Image) ToFile(path string) (err error) {
	if ext := strings.ToLower(filepath.Ext(path)); ext == ".dzi" || ext == ".dz" || ext == ".zip" {
		return m.ToTiles(path)
	}
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
	if err = m.write(&fileOut{bw, tmp}, strings.TrimPrefix(strings.ToLower(filepath.Ext(path)), ".")); err != nil {
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
	if m.pages != nil {
		return m.writePages(w, ext)
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
		if keeps && inFormat != "gif" {
			// A still WebP or PNG is not an animation to keep.
			n, err := inputFrames(f, inFormat, env)
			if err != nil {
				f.Close()
				return err
			}
			keeps = n > 0
		}
		if keeps && m.minSSIM > 0 {
			f.Close()
			return fmt.Errorf("goimgtools: MinSSIM does not apply to an animation that Animated keeps: %w", errors.ErrUnsupported)
		}
		if keeps && len(m.toProfile) > 0 {
			f.Close()
			return fmt.Errorf("goimgtools: ToProfile does not apply to an animation that Animated keeps: %w", errors.ErrUnsupported)
		}
		if keeps && m.maxBytes == 0 {
			defer f.Close()
			return m.writeAnimated(w, f, env, inFormat, format)
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
	if format == "pdf" {
		// A JPEG as it is, when it can be (see asJPEGPage).
		po, err := optionsOf[PDFOptions](m.options, "pdf")
		if err != nil {
			f.Close()
			return err
		}
		p, err := m.asJPEGPage(po, nil)
		if err != nil {
			f.Close()
			return err
		}
		if p != nil {
			f.Close()
			doc := pdfwrite.NewDocument(w)
			if err := m.writeJPEGPage(doc, p); err != nil {
				return err
			}
			return doc.Close()
		}
	}
	if def := formatNamed(format); len(m.toProfile) > 0 && def != nil && def.noProfile {
		f.Close()
		return fmt.Errorf("goimgtools: ToProfile: %s output cannot carry a colour profile", format)
	}
	src, closer, inFormat, meta, err := m.buildFrom(env, true, f, inFormat, false, m.takesYCbCr(format))
	if err != nil {
		return err
	}
	out := m.outputMeta(meta)
	defer closer.Close()
	src, out.ICC = m.manageColour(src, out.ICC, format)
	if m.threadCount() >= 3 {
		src = pipeline.Async(src)
	}
	defer src.Close()
	if m.maxBytes > 0 && m.minSSIM > 0 {
		return errors.New("goimgtools: MaxFileSize and MinSSIM do not go together")
	}
	if m.maxBytes > 0 {
		return m.writeWithin(w, src, format, out)
	}
	if m.minSSIM > 0 {
		return m.writeAtSSIM(w, src, format, out)
	}
	return m.encode(w, src, format, 0, out)
}

// fileOut is the file ToFile writes, buffered, which encoders may write
// over where they have written (outfile.Patcher).
type fileOut struct {
	*bufio.Writer
	f *os.File
}

func (o *fileOut) Patch(p []byte, off int64) error {
	if err := o.Flush(); err != nil {
		return err
	}
	_, err := o.f.WriteAt(p, off)
	return err
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

// outputMeta returns what of the input's metadata goes into the output,
// and the resolution to record.
func (m *Image) outputMeta(meta pipeline.Meta) (out pipeline.Meta) {
	out.Density = meta.Density
	if m.densitySet {
		out.Density = m.density
	}
	if m.keepExif {
		out.Exif = meta.Exif
		if m.autoOrient {
			out.Exif = pipeline.ExifUpright(out.Exif)
		}
		if m.density > 0 {
			out.Exif = density.SetExif(out.Exif, m.density)
		}
	}
	out.Exif = exifedit.Set(out.Exif, m.exifSet)
	if !m.stripICC {
		out.ICC = meta.ICC
	}
	if m.keepXmp {
		out.Xmp = meta.Xmp
	}
	if m.keepIptc {
		out.Iptc = meta.Iptc
	}
	return out
}

// encode writes src in the given format. quality, when positive, replaces
// the quality asked for.
func (m *Image) encode(w io.Writer, src pipeline.Source, format string, quality int, meta pipeline.Meta) error {
	return m.encodeWith(w, src, format, m.optionsFor(format), quality, meta)
}

// encodeWith is encode with the format's options given.
func (m *Image) encodeWith(w io.Writer, src pipeline.Source, format string, options any, quality int, meta pipeline.Meta) error {
	if m.ctx != nil {
		// Also here: an encoder can work for a long time on pixels that
		// were read long ago (a second pass, another try at a file size).
		src = cancelable{src, m.ctx}
	}
	def := formatNamed(format)
	if def == nil || def.encode == nil {
		return fmt.Errorf("goimgtools: cannot write %s (%s)", format, formatList())
	}
	o := EncodeOptions{Exif: meta.Exif, ICC: meta.ICC, Xmp: meta.Xmp, Iptc: meta.Iptc, Density: meta.Density, Options: options}
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
// takesYCbCr reports whether a JPEG's pixels go to the encoder of format
// untouched, an encoder that takes YCbCr as it is (JPEG, lossy WebP): no
// operation, no conversion of colour. The chroma then goes from the file
// into the new one without being brought to full size and back.
func (m *Image) takesYCbCr(format string) bool {
	if len(m.chain) != 0 || m.toSRGB {
		return false
	}
	switch format {
	case "jpeg":
		return true
	case "webp":
		o, _ := m.optionsFor("webp").(WebPOptions)
		return !o.Lossless
	}
	return false
}

func (m *Image) optionsFor(format string) any {
	if m.format == format {
		return m.options
	}
	return nil
}

// writeWithin encodes src at the highest quality whose file is at most
// m.maxBytes: for PNG and GIF the most colours (a PNG first without loss).
func (m *Image) writeWithin(w io.Writer, src pipeline.Source, format string, meta pipeline.Meta) error {
	def := formatNamed(format)
	if def == nil || def.encode == nil {
		return fmt.Errorf("goimgtools: cannot write %s (%s)", format, formatList())
	}
	encodeAt, bottom, top, what, err := m.knob(format, meta, "MaxFileSize")
	if err != nil {
		return err
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
	// try encodes at step q and reports whether the file fits; a file
	// that fits replaces the best one so far.
	try := func(q int) (bool, int64, error) {
		in, err := rec.Source()
		if err != nil {
			return false, 0, err
		}
		out := pipeline.NewSpill(env)
		err = encodeAt(out, in, q)
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
	fits, size, err := false, int64(0), error(nil)
	if format == "png" {
		// Without loss, unless colours were asked for.
		if po, _ := m.optionsFor(format).(PNGOptions); po.Colors == 0 {
			if fits, size, err = try(0); err != nil {
				return err
			}
		}
	}
	if !fits {
		fits, size, err = try(top)
		if err != nil {
			return err
		}
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
		// too large, the lowest step settles whether the limit can be met at
		// all and gives the search its lower end.
		if hi > bottom {
			if err := probe(max(bottom, min(top-1, int(float64(top)*float64(m.maxBytes)/float64(size))))); err != nil {
				return err
			}
		}
		if lo == 0 && hi > bottom {
			if err := probe(bottom); err != nil {
				return err
			}
		}
		if lo == 0 {
			return fmt.Errorf("%w: %s %d gives %d bytes, the limit is %d", ErrFileSize, what, bottom, hiSize, m.maxBytes)
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

// knob is what MaxFileSize and MinSSIM turn: encodeAt encodes at step k,
// the quality or the number of colours (0: a PNG's own, without loss),
// from bottom to top; what names it.
func (m *Image) knob(format string, meta pipeline.Meta, by string) (encodeAt func(w io.Writer, src pipeline.Source, k int) error, bottom, top int, what string, err error) {
	def := formatNamed(format)
	what = "quality"
	switch format {
	case "png", "gif":
		what, bottom, top = "colours", 2, 256
		options := m.optionsFor(format)
		var colors int
		if format == "png" {
			po, _ := options.(PNGOptions)
			colors = po.Colors
		} else {
			g, _ := options.(GIFOptions)
			colors = g.Colors
		}
		if colors >= bottom && colors < top {
			top = colors
		}
		encodeAt = func(w io.Writer, src pipeline.Source, k int) error {
			o := options
			if format == "png" {
				po, _ := options.(PNGOptions)
				po.Colors = k
				o = po
			} else {
				g, _ := options.(GIFOptions)
				g.Colors = k
				o = g
			}
			return m.encodeWith(w, src, format, o, 0, meta)
		}
	default:
		if def.quality == nil {
			return nil, 0, 0, "", fmt.Errorf("goimgtools: %s applies to JPEG, lossy WebP, PNG and GIF output, not %s", by, format)
		}
		var ok bool
		top, ok = def.quality(m.optionsFor(format))
		if !ok {
			return nil, 0, 0, "", fmt.Errorf("goimgtools: %s applies to JPEG, lossy WebP, PNG and GIF output, not lossless %s", by, format)
		}
		if top < 1 || top > 100 {
			return nil, 0, 0, "", errors.New("goimgtools: quality must be 1 to 100")
		}
		bottom = 1
		encodeAt = func(w io.Writer, src pipeline.Source, q int) error { return m.encode(w, src, format, q, meta) }
	}
	return encodeAt, bottom, top, what, nil
}

// writeAtSSIM encodes src at the lowest step of the knob whose file is at
// least m.minSSIM alike to src (see MinSSIM).
func (m *Image) writeAtSSIM(w io.Writer, src pipeline.Source, format string, meta pipeline.Meta) error {
	def := formatNamed(format)
	if def == nil || def.encode == nil {
		return fmt.Errorf("goimgtools: cannot write %s (%s)", format, formatList())
	}
	encodeAt, bottom, top, _, err := m.knob(format, meta, "MinSSIM")
	if err != nil {
		return err
	}
	// Up to the quality asked for, or to 100 without one.
	switch o := m.optionsFor(format).(type) {
	case JPEGOptions:
		if o.Quality == 0 {
			top = 100
		}
	case WebPOptions:
		if o.Quality == 0 {
			top = 100
		}
	case TIFFOptions:
		if o.Quality == 0 {
			top = 100
		}
	case nil:
		if format == "jpeg" || format == "webp" || format == "tiff" {
			top = 100
		}
	}
	rec, err := pipeline.Record(src)
	if err != nil {
		return err
	}
	defer rec.Close()
	env := pipeline.EnvOf(src)
	// score encodes at step q and measures the file against the image.
	score := func(q int) (float64, *pipeline.Spill, error) {
		in, err := rec.Source()
		if err != nil {
			return 0, nil, err
		}
		out := pipeline.NewSpill(env)
		err = encodeAt(out, in, q)
		in.Close()
		if err != nil {
			out.Close()
			return 0, nil, err
		}
		s, err := ssimOf(rec, out, format, env)
		if err != nil {
			out.Close()
			return 0, nil, err
		}
		return s, out, nil
	}
	var best *pipeline.Spill
	defer func() {
		if best != nil {
			best.Close()
		}
	}()
	keep := func(s *pipeline.Spill) {
		if best != nil {
			best.Close()
		}
		best = s
	}
	sTop, out, err := score(top)
	if err != nil {
		return err
	}
	keep(out)
	if sTop < m.minSSIM {
		// Not even the top: a lossless PNG when no colours were asked for,
		// else the top as it is.
		if po, _ := m.optionsFor(format).(PNGOptions); format == "png" && po.Colors == 0 {
			_, out, err := score(0)
			if err != nil {
				return err
			}
			keep(out)
		}
	} else {
		// The likeness grows with the step: the lowest that reaches it.
		lo, hi := bottom-1, top
		for hi-lo > 1 {
			mid := (lo + hi) / 2
			s, out, err := score(mid)
			if err != nil {
				return err
			}
			if s >= m.minSSIM {
				hi = mid
				keep(out)
			} else {
				lo = mid
				out.Close()
			}
		}
	}
	_, err = io.Copy(w, io.NewSectionReader(best, 0, best.Size()))
	return err
}

// ssimOf decodes the file in out (of format) and measures it against the
// image recorded in rec: laid on black, as the encoder lays it, for a
// format without transparency.
func ssimOf(rec *pipeline.Recording, out *pipeline.Spill, format string, env *pipeline.Env) (float64, error) {
	ref, err := rec.Source()
	if err != nil {
		return 0, err
	}
	defer ref.Close()
	got, closer, _, _, err := FromReader(io.NewSectionReader(out, 0, out.Size())).build(env, false)
	if err != nil {
		return 0, err
	}
	defer closer.Close()
	defer got.Close()
	var a pipeline.Source = ref
	if format == "jpeg" || format == "tiff" {
		a = ops.Flatten(ref, color.Black)
	}
	return metric.SSIM(a, got)
}
