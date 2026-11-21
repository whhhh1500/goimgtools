// Package cli is the goimgtools command line, as a package: the program
// in cmd/goimgtools is nothing but cli.Main.
//
//	goimgtools [options] input output
//	goimgtools batch [options] input-dir output-dir
//	goimgtools info input...
//	goimgtools ops
//
// It is a package so that a program of your own can be the same command
// line with your own operations in it: register them and hand over.
//
//	func main() {
//		goimgtools.RegisterOperation(goimgtools.OperationDef{
//			Name: "sepia", Help: "brown and old", New: newSepia,
//		})
//		cli.Main()
//	}
//
// "-op sepia" then applies it, "goimgtools ops" lists it, and Run (for
// tests, or another front end) does the same without ending the process.
package cli

import (
	"errors"
	"flag"
	"fmt"
	"image/color"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/whhhh1500/goimgtools"
)

const usage = `usage:
  goimgtools [options] input output     convert / process one image
  goimgtools batch [options] in-dir out-dir
                                        process every image of a directory
                                        (see: goimgtools batch -h)
  goimgtools info [-json] file...       what each file is: format, size,
                                        colours, metadata, frames
  goimgtools check file...              decode each file completely and report
                                        the damaged ones
  goimgtools compare a b                how much two images differ (PSNR)
  goimgtools stats file...              the colours of each image: average,
                                        dominant, greyscale or not
  goimgtools hash file...               a fingerprint of each image, for
                                        finding duplicates (-similar bits)
  goimgtools placeholder file...        the BlurHash and ThumbHash of each
                                        image, blurred placeholders for pages
  goimgtools exif file...               camera, time and place from the EXIF
                                        block
  goimgtools readmark -key K file...    the invisible mark (-mark) of each
                                        image, if any
  goimgtools formats                    the formats that are read and written
  goimgtools fonts [-all]               list the fonts text is drawn with by
                                        default (-all: every installed font)
  goimgtools ops                        the operations -op takes, with their
                                        arguments

With -text and no input file, the text itself is the image:
  goimgtools -text "Hello" -font-size 64 out.png

The output format follows the extension of output (.jpg, .png, .webp, .gif,
.bmp, .tif, .ico, .qoi, .ppm, .pgm, .tga, .pdf) unless -format is given. Operations are applied in this order:
rotate, flip, flop, trim, crop, resize, extend, grayscale, modulate, blur,
sharpen, the -op steps (in the order given), flatten, watermark, -mark.

options:
`

// Main runs the command line with the arguments of the process and ends
// it: with status 1 and the error on standard error when there is one.
func Main() {
	if err := Run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		msg := err.Error()
		if !strings.HasPrefix(msg, "goimgtools:") {
			msg = "goimgtools: " + msg
		}
		fmt.Fprintln(os.Stderr, msg)
		os.Exit(1)
	}
}

// Run runs the command line with args (without the program's name),
// writing what it prints to stdout and stderr.
func Run(args []string, stdout, stderr io.Writer) error { return run(args, stdout, stderr) }

// options are the settings shared by the single-file and the batch mode:
// what to do to an image and how to write it.
type options struct {
	resize, rotate, fit, bg, kernel, crop, flatten *string
	position, resizeMethod                         *string
	reducingGap                                    *float64
	flip, flop, noEnlarge, fast, linearLight       *bool
	watermark, wmText, gravity, offset, wmGap      *string
	wmRatio, wmAnchor, wmPivot                     *string
	wmWidth                                        *string
	wmAngle, wmOpacity                             *float64
	wmTile                                         *bool
	wmBlend                                        *string
	text, fontList, textColor, textBg              *string
	strokeCol, shadowCol, shadowOff                *string
	fontSize, strokeW                              *float64
	textWidth, shadowBlr                           *int
	format, maxSize, tiffComp                      *string
	minSSIM                                        *float64
	tileSize, tileOverlap                          *int
	tileLayout                                     *string
	quality, pngLevel, pngColors, gifColors        *int
	icoSizes, frameDelay                           *string
	loop                                           *int
	gifFixed                                       *bool
	gifLossy                                       *int
	webpEffort                                     *int
	webpNear, webpExact                            *bool
	webpAlphaQ                                     *int
	lossless, jpegOpt, jpegProg, noDither          *bool
	jpegTrellis, jpegDering                        *bool
	jpegQuantTable                                 *int
	jpegChroma                                     *string
	animated, keepExif, stripICC, toSRGB           *bool
	exifCopyright, exifArtist, exifDescription     *string
	trimLineArt, keepXmp, tiffPredictor            *bool
	trimBackground                                 *string
	trimMargin                                     *int
	density                                        *float64
	mem, threads, maxPixels                        *int
	skipLarger                                     *bool
	tmp                                            *string
	trim, extend, extendWith, modulate, sharpen    *string
	ops                                            []string // -op, in order
	grayscale, grayLinear                          *bool
	blur, blurMinAmp                               *float64
	blurMethod                                     *string
	mark, markKey, markKind                        *string
	markStrength, markDensity                      *float64
}

// register defines the shared options on fs.
func register(fs *flag.FlagSet) *options {
	o := &options{
		resize:          fs.String("resize", "", "target size `WxH`; either side may be left out (1536x or x1024) to keep proportions"),
		rotate:          fs.String("rotate", "", "turn the image first: auto (follow the EXIF orientation), or any number of `degrees` clockwise (90, 180, 270 exactly; other angles enlarge the canvas, with -background in the corners)"),
		flip:            fs.Bool("flip", false, "mirror top to bottom"),
		flop:            fs.Bool("flop", false, "mirror left to right"),
		fit:             fs.String("fit", "inside", "how -resize WxH fits: inside (shrink in proportion), cover (fill and crop), contain (shrink in proportion, pad with -background), outside, fill (stretch)"),
		linearLight:     fs.Bool("linear-light", false, "resize in linear light: fine light detail keeps its brightness when the image is reduced"),
		reducingGap:     fs.Float64("reducing-gap", 0, "with -resize-method pillow, Pillow's reducing_gap: reduce by whole factors first, leaving at least this much (2 as Pillow's thumbnail, which with -fast-shrink also drafts a JPEG for twice the box); 0 for none"),
		resizeMethod:    fs.String("resize-method", "draw", "how -resize works out the pixels: draw (golang.org/x/image/draw's arithmetic), libvips (sharp's resize, to the byte; sharp's default -kernel is lanczos3) or pillow (Pillow's resize, to the byte; its default is cubic)"),
		position:        fs.String("position", "center", "the part -fit cover keeps (and where -fit contain puts the image): northwest, north, northeast, west, center, east, southwest, south, southeast; or, for cover, entropy (the part with the most detail) or attention (the part that draws the eye)"),
		bg:              fs.String("background", "black", "`color` of the bars -fit contain adds, the border of -extend and the corners -rotate leaves at other angles: a CSS colour, such as white, transparent, steelblue, #rrggbb or #rrggbbaa"),
		trimBackground:  fs.String("trim-background", "", "the `color` of the edges -trim cuts away (by default that of the top-left pixel)"),
		trimMargin:      fs.Int("trim-margin", 0, "keep this many `pixels` around what -trim finds"),
		trimLineArt:     fs.Bool("trim-line-art", false, "-trim compares the pixels as they are, without the median filter that passes over noise but also removes lines one pixel thin"),
		trim:            fs.String("trim", "", "cut away the edges that are the colour of the top-left pixel, allowing this `difference` 0-255 (10 also catches JPEG noise)"),
		extend:          fs.String("extend", "", "add a border of `T,R,B,L` pixels in -background"),
		extendWith:      fs.String("extend-with", "", "fill the border of -extend from the image: copy (the edge pixels), repeat (the image) or mirror (the image mirrored)"),
		grayscale:       fs.Bool("grayscale", false, "turn the image into shades of grey"),
		grayLinear:      fs.Bool("grayscale-linear", false, "turn the image into shades of grey by the luminance of its linear light, as sharp's greyscale"),
		modulate:        fs.String("modulate", "", "change `B,S,H[,L]`: brightness and saturation factors, hue degrees and lightness added (L*, 0 to 100); 1.2,0.8,0 is brighter and less vivid"),
		blur:            fs.Float64("blur", 0, "Gaussian blur of this `sigma` in pixels (as Pillow's GaussianBlur, or sharp's with -blur-method libvips)"),
		blurMethod:      fs.String("blur-method", "pillow", "how -blur works out its Gaussian: pillow (Pillow's boxes) or libvips (sharp's kernel)"),
		blurMinAmp:      fs.Float64("blur-min-amplitude", 0, "cut the Gaussian of -blur-method libvips off below this `fraction` of its peak, 0.001 to 1 (0.2 by default, as sharp)"),
		sharpen:         fs.String("sharpen", "", "sharpen: mild (a quick 3x3 sharpen, good after shrinking), an unsharp mask `SIGMA[,AMOUNT[,THRESHOLD]]` as Pillow's (amount 1.5, threshold 0 by default), or SIGMA,libvips[,FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN] as sharp's (the lightness alone; 1,2,2,10,20 by default)"),
		kernel:          fs.String("kernel", "cubic", "resize filter: nearest, linear, cubic, mitchell, lanczos2, lanczos3, mks2013, mks2021, box, hamming"),
		noEnlarge:       fs.Bool("no-enlarge", false, "never make the image larger than it is"),
		fast:            fs.Bool("fast-shrink", false, "let a JPEG reduced to half size or less, or a lossy WebP without alpha to a third, be shrunk while it is decoded (with -resize-method libvips, sharp's fastShrinkOnLoad, every WebP): faster, pixels differ slightly"),
		crop:            fs.String("crop", "", "keep the region `X,Y,W,H` (pixels, before resizing)"),
		flatten:         fs.String("flatten", "", "replace transparency with a background `color`: white, black or #rrggbb"),
		watermark:       fs.String("watermark", "", "image `file` to draw on top"),
		wmText:          fs.String("watermark-text", "", "`text` to draw on top instead of an image (use \\n for a new line)"),
		text:            fs.String("text", "", "make an image of this `text`; give only an output file"),
		fontList:        fs.String("font", "", "fonts for -text and -watermark-text: `files` or installed font names, comma separated, tried in order (see: goimgtools fonts)"),
		fontSize:        fs.Float64("font-size", 32, "text height in `pixels`"),
		textColor:       fs.String("text-color", "black", "`color` of the text: a CSS colour, such as black, steelblue, #rrggbb, #rrggbbaa or rgb(0, 0, 255)"),
		textBg:          fs.String("text-background", "", "`color` behind the text (default: transparent)"),
		textWidth:       fs.Int("text-width", 0, "wrap the text at this many `pixels`"),
		strokeW:         fs.Float64("stroke-width", 0, "outline the text, this many `pixels` wide"),
		strokeCol:       fs.String("stroke-color", "black", "`color` of the text outline"),
		shadowCol:       fs.String("shadow-color", "", "draw a shadow of the text in this `color`"),
		shadowOff:       fs.String("shadow-offset", "2,2", "move the shadow `X,Y` pixels (0,0 with -shadow-blur gives a glow)"),
		shadowBlr:       fs.Int("shadow-blur", 0, "soften the shadow over this many `pixels`"),
		wmOpacity:       fs.Float64("watermark-opacity", 1, "how much of the watermark shows, 0 to 1"),
		wmTile:          fs.Bool("watermark-tile", false, "repeat the watermark over the whole image"),
		wmBlend:         fs.String("watermark-blend", "over", "how the watermark's colours mix with the image's: over, multiply, screen, overlay, darken, lighten, color-dodge, color-burn, hard-light, soft-light, difference, exclusion; or the Porter-Duff clear, source, in, out, atop, dest, dest-over, dest-in (a mask), dest-out, dest-atop, xor, and add, saturate"),
		wmGap:           fs.String("watermark-gap", "0,0", "space between repeated watermarks, `X,Y` pixels"),
		wmWidth:         fs.String("watermark-width", "", "scale the watermark to this `width`: pixels (300), or a share of the image's width (20%), which keeps it the same to the eye on images of any size"),
		gravity:         fs.String("gravity", "southeast", "watermark anchor: northwest, north, northeast, west, center, east, southwest, south, southeast"),
		wmRatio:         fs.String("watermark-ratio", "", "move the watermark `X,Y` shares of the image width and height from its gravity point (0.333,0.333 is a third across and down); without -offset there is no margin"),
		wmAnchor:        fs.String("watermark-anchor", "", "the `point` of the watermark put on that position: northwest ... center ... southeast, or X,Y pixels from its top-left corner (default: the one named like -gravity)"),
		wmAngle:         fs.Float64("watermark-angle", 0, "turn the watermark clockwise by this many `degrees`"),
		wmPivot:         fs.String("watermark-pivot", "", "the `point` of the watermark it turns around, given like -watermark-anchor (default: the anchor)"),
		offset:          fs.String("offset", "", "move the watermark `X,Y` pixels from its anchor (negative moves left / up); default is a 24 pixel margin"),
		quality:         fs.Int("quality", 0, "JPEG, lossy WebP, JPEG-compressed TIFF or PDF quality 1-100 (default 80 for JPEG and TIFF, 75 for WebP; a PDF without it is lossless)"),
		jpegOpt:         fs.Bool("jpeg-optimize", false, "JPEG: build the coding tables for this image (a smaller file, the same pixels)"),
		jpegProg:        fs.Bool("jpeg-progressive", false, "JPEG: write a progressive file (smaller still, the same pixels)"),
		jpegTrellis:     fs.Bool("jpeg-trellis", false, "JPEG: trellis quantization as mozjpeg does it (smaller at about the same look; other pixels)"),
		jpegDering:      fs.Bool("jpeg-overshoot-deringing", false, "JPEG: less ringing beside dark text and lines on white, as mozjpeg (sharp's overshootDeringing): screenshots look much better at the same quality, for files a few percent larger"),
		jpegChroma:      fs.String("jpeg-chroma", "420", "JPEG: how finely colour is kept: 420 (half across and down, the default), 422 (half across) or 444 (all of it: crisp coloured text and edges, a larger file)"),
		jpegQuantTable:  fs.Int("jpeg-quant-table", 0, "JPEG: quantization tables: 0-8 numbered as mozjpeg's -quant-table (0 the standard's, as the standard library; 3 mozjpeg's default), 9 made as jpegli makes them (the smallest files)"),
		pngColors:       fs.Int("png-colors", 0, "PNG: reduce to this many colours, 2-256, chosen for the image (a much smaller, lossy file)"),
		icoSizes:        fs.String("ico-sizes", "", "ICO: the sizes of the icons to write, such as 16,32,48,256, the image fitted to each (default: the image as it is, at most 256x256)"),
		noDither:        fs.Bool("no-dither", false, "no dithering when colours are reduced (-png-colors, GIF output, -animated)"),
		pngLevel:        fs.Int("png-level", 0, fmt.Sprintf("PNG compression 1 (fastest) to 10 (smallest, slowest); default %d", goimgtools.DefaultPNGLevel)),
		format:          fs.String("format", "", "output format: "+formatNames()+" (default: from the output file name)"),
		skipLarger:      fs.Bool("skip-larger", false, "when the result is not smaller than the input file (same format), keep the input file as it is; for runs that only compress"),
		tileSize:        fs.Int("tile-size", 256, "the side of a tile, for an output named .dzi or .dz (a Deep Zoom pyramid of tiles, as sharp's tile)"),
		tileOverlap:     fs.Int("tile-overlap", 0, "how many pixels of its neighbours a tile also holds on each side (.dzi output)"),
		tileLayout:      fs.String("tile-layout", "dz", "the layout of the tiles of a .dzi or .dz output: dz (Deep Zoom), google, iiif, iiif3 or zoomify"),
		maxSize:         fs.String("max-size", "", "make the file at most this `size` by lowering the quality (JPEG, lossy WebP) or the number of colours (PNG, GIF): 200KB, 1.5MB, or a number of bytes"),
		minSSIM:         fs.Float64("min-ssim", 0, "make the file as small as the quality (colours, for PNG and GIF) allows while its SSIM against the image stays at least this, such as 0.98 (JPEG, lossy WebP, PNG, GIF, JPEG TIFF)"),
		lossless:        fs.Bool("webp-lossless", false, "WebP: write without loss"),
		webpNear:        fs.Bool("webp-near-lossless", false, "WebP: lossless, but pixels that differ from a neighbour anyway may change a little (-quality how much: 100 none, 60-79 at most 2 levels a channel; default 75): photographs about a third smaller"),
		webpExact:       fs.Bool("webp-exact", false, "WebP: keep the colour of fully transparent pixels (by default it is dropped, as libwebp does)"),
		webpAlphaQ:      fs.Int("webp-alpha-quality", 0, "WebP (lossy): quality 1-99 of the transparency, reduced to fewer levels (2-16 up to 70); default 100, exact"),
		webpEffort:      fs.Int("webp-effort", 0, "WebP: effort 1 (fastest) to 9 (smallest file); default 5 (for lossy, all there is but for transparency)"),
		gifColors:       fs.Int("gif-colors", 0, "largest number of colours in a GIF, 1-256 (default 256)"),
		gifFixed:        fs.Bool("gif-fixed-palette", false, "GIF: use the standard fixed palette instead of colours chosen for the image (faster, a much worse picture)"),
		gifLossy:        fs.Int("gif-lossy", 0, "GIF: lossy compression, the colour difference 1-255 it may accept (16-32 for photographic animations: 24-48% smaller; 8-16 for dithered stills); 0 keeps every pixel"),
		tiffComp:        fs.String("tiff-compression", "none", "TIFF compression: none, deflate, lzw or jpeg (lossy, -quality, default 80; no transparency)"),
		tiffPredictor:   fs.Bool("tiff-predictor", false, "TIFF: store each sample as its difference from the pixel to its left (horizontal predictor): photographs compress much better with deflate or lzw"),
		animated:        fs.Bool("animated", false, "process every frame of an animated GIF, WebP or PNG and write an animated GIF, WebP or PNG (APNG); only what changes is stored (GIF: one palette for all frames, -gif-colors, -no-dither); another output format is an error"),
		loop:            fs.Int("loop", -1, "with -animated: how often the animation plays, 0 for ever (default: as the input)"),
		frameDelay:      fs.String("frame-delay", "", "with -animated: how long each frame shows, in milliseconds: one value for every frame, or a list such as 100,50 (the last for the rest)"),
		keepExif:        fs.Bool("keep-exif", false, "copy the EXIF block (camera, date, GPS) to the output"),
		exifCopyright:   fs.String("exif-copyright", "", "write this 	ext as the EXIF copyright (into the kept block with -keep-exif, else a block of its own)"),
		exifArtist:      fs.String("exif-artist", "", "write this 	ext as the EXIF artist (who made the image)"),
		exifDescription: fs.String("exif-description", "", "write this 	ext as the EXIF image description (a title or caption)"),
		keepXmp:         fs.Bool("keep-xmp", false, "copy the XMP packet (keywords, rating, rights) to the output (JPEG, PNG, WebP, TIFF)"),
		stripICC:        fs.Bool("strip-icc", false, "drop the ICC colour profile instead of copying it"),
		density:         fs.Float64("density", 0, "record a resolution of `DPI` pixels per inch in the output (JPEG, PNG, TIFF, BMP); by default the input's is kept"),
		toSRGB:          fs.Bool("srgb", false, "convert the colours to sRGB from the image's ICC profile (Display P3, Adobe RGB...) and leave the profile out; GIF and BMP output always are"),
		mem:             fs.Int("mem", 64, "working memory limit in `MB` for one image; beyond it data goes to temporary files"),
		threads:         fs.Int("threads", 1, "CPU cores to use for one image, at most the number of CPUs: 2 decodes on a core of its own, 3 encodes on another, more go to resizing, -blur, -sharpen, -modulate, -rotate and JPEG and PNG encoding"),
		maxPixels:       fs.Int("max-pixels", 0, "largest image accepted or made, in `megapixels` (default 268: 16383 x 16383); all frames together for -animated"),
		tmp:             fs.String("tmp", "", "`directory` for temporary files (default: the system one)"),
		mark:            fs.String("mark", "", "hide this `text` (1 to 32 bytes: a user ID, a serial number) in the image, invisibly, to be read back with goimgtools readmark and -mark-key, without the original"),
		markKey:         fs.String("mark-key", "", "the secret `key` of -mark: only the same key reads the mark"),
		markKind:        fs.String("mark-kind", "blind", "-mark made to survive what is done to a file (blind: re-encoding, resizing, cropping, turning, screenshots; it cannot be seen) or also a photograph of a screen showing the image (screen: against leaks; a faint texture up close)"),
		markStrength:    fs.Float64("mark-strength", 1, "how strong -mark is: 0.5 harder to see and to find, 2 survives more and is easier to see"),
		markDensity:     fs.Float64("mark-density", 0, "about how many times -mark repeats along the shorter side, 1 to 16 (default 4): denser survives smaller crops, sparser greater reductions"),
	}
	fs.Func("op", "apply an operation by name, `NAME[:ARG,ARG...]`, as goimgtools ops lists them (built-in or added by the program); may be given more than once, done in that order after -sharpen", func(v string) error {
		name, _, _ := strings.Cut(v, ":")
		if _, ok := goimgtools.LookupOperation(name); !ok {
			return fmt.Errorf("no operation %q (goimgtools ops lists them)", name)
		}
		o.ops = append(o.ops, v)
		return nil
	})
	return o
}

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) > 0 {
		switch args[0] {
		case "info":
			return info(args[1:], stdout, stderr)
		case "check":
			return check(args[1:], stdout, stderr)
		case "compare":
			return compare(args[1:], stdout, stderr)
		case "stats":
			return stats(args[1:], stdout, stderr)
		case "hash":
			return hash(args[1:], stdout, stderr)
		case "placeholder":
			return placeholder(args[1:], stdout, stderr)
		case "exif":
			return exifInfo(args[1:], stdout, stderr)
		case "readmark":
			return readMark(args[1:], stdout, stderr)
		case "formats":
			return formatTable(args[1:], stdout)
		case "fonts":
			return fonts(args[1:], stdout)
		case "ops":
			return opList(args[1:], stdout)
		case "batch":
			return batch(args[1:], stdout, stderr)
		}
	}
	fs := flag.NewFlagSet("goimgtools", flag.ContinueOnError)
	fs.SetOutput(stderr)
	o := register(fs)
	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	var in, out string
	switch {
	case *o.text != "" && fs.NArg() == 1:
		out = fs.Arg(0)
	case *o.text == "" && fs.NArg() == 2:
		in, out = fs.Arg(0), fs.Arg(1)
	default:
		fs.Usage()
		return errors.New("need an input and an output file (or -text and an output file)")
	}
	im, err := o.image(in, out)
	if err != nil {
		return err
	}
	kept, err := o.finish(im, in, out)
	if kept {
		fmt.Fprintf(stdout, "kept %s as it is: the result would not be smaller\n", in)
	}
	return err
}

// formatOf names the format a file name stands for ("" when unknown). The
// library knows the formats, their names and their extensions.
func formatOf(path string) string {
	f, _ := goimgtools.FormatByName(filepath.Ext(path))
	return f.Name
}

// outputKind names the format that will be written: -format when given,
// else what the extension of out stands for, else "" (the library decides).
func (o *options) outputKind(out string) (string, error) {
	if *o.format == "" {
		return formatOf(out), nil
	}
	f, ok := goimgtools.FormatByName(*o.format)
	if !ok {
		return "", fmt.Errorf("-format: cannot write %q (%s)", *o.format, formatNames())
	}
	return f.Name, nil
}

// formatNames lists the formats for messages: "jpeg, png, ... or tiff".
func formatNames() string {
	var names []string
	for _, f := range goimgtools.Formats() {
		names = append(names, f.Name)
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

var tempCount atomic.Int64

// finish writes the image to out. With -skip-larger, a result of the input's
// own format that is not smaller than the input file is dropped and the
// input kept instead (the idea of pngquant's --skip-if-larger): copied to
// out, or simply left alone when out is the input file itself. Colour
// reduction and re-encoding can both make a file larger than it was, for a
// picture that was already small.
func (o *options) finish(im *goimgtools.Image, in, out string) (kept bool, err error) {
	if ext := strings.ToLower(filepath.Ext(out)); ext == ".dzi" || ext == ".dz" || ext == ".zip" {
		layouts := map[string]goimgtools.TileLayout{"dz": goimgtools.TileDeepZoom, "google": goimgtools.TileGoogle,
			"iiif": goimgtools.TileIIIF, "iiif3": goimgtools.TileIIIF3, "zoomify": goimgtools.TileZoomify}
		layout, ok := layouts[strings.ToLower(*o.tileLayout)]
		if !ok {
			return false, fmt.Errorf("-tile-layout: unknown layout %q (dz, google, iiif, iiif3 or zoomify)", *o.tileLayout)
		}
		return false, im.ToTiles(out, goimgtools.TileOptions{Size: *o.tileSize, Overlap: *o.tileOverlap, Layout: layout})
	}
	kind, _ := o.outputKind(out) // a wrong -format was reported when the chain was made
	before, statErr := os.Stat(in)
	if !*o.skipLarger || in == "" || statErr != nil || kind == "" || kind != formatOf(in) {
		return false, im.ToFile(out)
	}
	// Written next to out under another name, so that the input is still
	// there to keep when out is the input.
	tmp := filepath.Join(filepath.Dir(out), fmt.Sprintf(".goimgtools-%d-%d%s", os.Getpid(), tempCount.Add(1), filepath.Ext(out)))
	if err := im.ToFile(tmp); err != nil {
		return false, err
	}
	if after, err := os.Stat(tmp); err == nil && after.Size() < before.Size() {
		if err := os.Rename(tmp, out); err != nil {
			os.Remove(tmp)
			return false, err
		}
		return false, nil
	}
	os.Remove(tmp)
	if existing, err := os.Stat(out); err == nil && os.SameFile(before, existing) {
		return true, nil
	}
	return true, copyFile(in, out)
}

// copyFile copies a file; out appears only when the copy is complete.
func copyFile(in, out string) error {
	src, err := os.Open(in)
	if err != nil {
		return err
	}
	defer src.Close()
	// Created like any new file, not like a temporary one, which only its
	// owner could read (as the library does for its results).
	mode := os.FileMode(0o666)
	if st, err := src.Stat(); err == nil {
		mode = st.Mode().Perm() | 0o200
	}
	var tmp *os.File
	for try := 0; ; try++ {
		name := filepath.Join(filepath.Dir(out), ".goimgtools-"+strconv.FormatUint(rand.Uint64(), 36))
		if tmp, err = os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_EXCL, mode); err == nil || !os.IsExist(err) || try == 100 {
			break
		}
	}
	if err != nil {
		return err
	}
	_, err = io.Copy(tmp, src)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), out)
	}
	if err != nil {
		os.Remove(tmp.Name())
	}
	return err
}

// textImage makes the image of a piece of text with the text options.
func (o *options) textImage(t string) (*goimgtools.Image, error) {
	to := goimgtools.TextOptions{Size: *o.fontSize, Width: *o.textWidth}
	if *o.fontList != "" {
		for _, f := range strings.Split(*o.fontList, ",") {
			to.Fonts = append(to.Fonts, strings.TrimSpace(f))
		}
	}
	var err error
	if to.Color, err = parseColor(*o.textColor); err != nil {
		return nil, fmt.Errorf("-text-color: %w", err)
	}
	if to.StrokeWidth = *o.strokeW; to.StrokeWidth != 0 {
		if to.StrokeColor, err = parseColor(*o.strokeCol); err != nil {
			return nil, fmt.Errorf("-stroke-color: %w", err)
		}
	}
	if *o.shadowCol != "" {
		if to.ShadowColor, err = parseColor(*o.shadowCol); err != nil {
			return nil, fmt.Errorf("-shadow-color: %w", err)
		}
		v, err := ints(*o.shadowOff, 2)
		if err != nil {
			return nil, fmt.Errorf("-shadow-offset: %w", err)
		}
		to.ShadowX, to.ShadowY, to.ShadowBlur = v[0], v[1], *o.shadowBlr
	}
	if *o.textBg != "" {
		if to.Background, err = parseColor(*o.textBg); err != nil {
			return nil, fmt.Errorf("-text-background: %w", err)
		}
	}
	return goimgtools.Text(strings.ReplaceAll(t, `\n`, "\n"), to), nil
}

// image builds the whole chain for one input file (or for -text, when in
// is not used) that is going to be written to out. Nothing is read yet.
func (o *options) image(in, out string) (*goimgtools.Image, error) {
	if *o.mem <= 0 {
		return nil, errors.New("-mem must be positive")
	}
	im := goimgtools.Open(in)
	if *o.text != "" {
		var err error
		if im, err = o.textImage(*o.text); err != nil {
			return nil, err
		}
	}
	im.MemoryLimit(int64(*o.mem) << 20).Threads(*o.threads)
	if *o.maxPixels < 0 {
		return nil, errors.New("-max-pixels must be positive")
	}
	if *o.maxPixels > 0 {
		im.LimitInputPixels(int64(*o.maxPixels) * 1e6).LimitOutputPixels(int64(*o.maxPixels) * 1e6)
	}
	if *o.tmp != "" {
		im.TempDir(*o.tmp)
	}
	if *o.animated {
		im.Animated()
	}
	if *o.loop >= 0 {
		im.Loop(*o.loop)
	}
	if *o.frameDelay != "" {
		var ds []time.Duration
		for _, s := range strings.Split(*o.frameDelay, ",") {
			n, err := strconv.Atoi(strings.TrimSpace(s))
			if err != nil {
				return nil, fmt.Errorf("-frame-delay: %q is not a number of milliseconds", s)
			}
			ds = append(ds, time.Duration(n)*time.Millisecond)
		}
		im.FrameDelay(ds...)
	}
	if *o.keepExif {
		im.KeepExif()
	}
	if *o.exifCopyright != "" || *o.exifArtist != "" || *o.exifDescription != "" {
		im.SetExif(goimgtools.ExifFields{Copyright: *o.exifCopyright, Artist: *o.exifArtist, ImageDescription: *o.exifDescription})
	}
	if *o.keepXmp {
		im.KeepXmp()
	}
	if *o.stripICC {
		im.StripICC()
	}
	if *o.density != 0 {
		im.Density(*o.density)
	}
	if *o.toSRGB {
		im.ToSRGB()
	}
	bg, err := parseColor(*o.bg)
	if err != nil {
		return nil, fmt.Errorf("-background: %w", err)
	}
	switch strings.ToLower(*o.rotate) {
	case "":
	case "auto":
		im.AutoOrient()
	default:
		deg, err := strconv.ParseFloat(*o.rotate, 64)
		if err != nil || math.IsNaN(deg) || math.IsInf(deg, 0) {
			return nil, fmt.Errorf("-rotate: want auto or a number of degrees, got %q", *o.rotate)
		}
		im.Rotate(deg, goimgtools.RotateOptions{Background: bg})
	}
	if *o.flip {
		im.Flip()
	}
	if *o.flop {
		im.Flop()
	}
	if *o.trim != "" {
		v, err := strconv.ParseFloat(*o.trim, 64)
		if err != nil || !(v >= 0 && v <= 255) {
			return nil, fmt.Errorf("-trim: want a difference of 0 to 255, got %q", *o.trim)
		}
		to := goimgtools.TrimOptions{LineArt: *o.trimLineArt, Margin: *o.trimMargin}
		if *o.trimBackground != "" {
			c, err := parseColor(*o.trimBackground)
			if err != nil {
				return nil, fmt.Errorf("-trim-background: %w", err)
			}
			to.Background = c
		}
		im.Trim(v, to)
	}
	if *o.crop != "" {
		v, err := ints(*o.crop, 4)
		if err != nil {
			return nil, fmt.Errorf("-crop: %w", err)
		}
		im.Crop(v[0], v[1], v[2], v[3])
	}
	if *o.resize != "" {
		w, h, err := size(*o.resize)
		if err != nil {
			return nil, fmt.Errorf("-resize: %w", err)
		}
		f, ok := map[string]goimgtools.Fit{
			"inside": goimgtools.FitInside, "cover": goimgtools.FitCover,
			"outside": goimgtools.FitOutside, "fill": goimgtools.FitFill, "contain": goimgtools.FitContain,
		}[strings.ToLower(*o.fit)]
		if !ok {
			return nil, fmt.Errorf("-fit: unknown mode %q", *o.fit)
		}
		k, ok := map[string]goimgtools.Kernel{
			"nearest": goimgtools.KernelNearest, "linear": goimgtools.KernelLinear, "cubic": goimgtools.KernelCubic,
			"mitchell": goimgtools.KernelMitchell, "lanczos2": goimgtools.KernelLanczos2, "lanczos3": goimgtools.KernelLanczos3,
			"mks2013": goimgtools.KernelMKS2013, "mks2021": goimgtools.KernelMKS2021,
			"box": goimgtools.KernelBox, "hamming": goimgtools.KernelHamming,
		}[strings.ToLower(*o.kernel)]
		if !ok {
			return nil, fmt.Errorf("-kernel: unknown filter %q", *o.kernel)
		}
		method, ok := map[string]goimgtools.Method{"draw": goimgtools.MethodDefault, "libvips": goimgtools.MethodLibvips,
			"pillow": goimgtools.MethodPillow}[strings.ToLower(*o.resizeMethod)]
		if !ok {
			return nil, fmt.Errorf("-resize-method: want draw, libvips or pillow, got %q", *o.resizeMethod)
		}
		ro := goimgtools.ResizeOptions{Fit: f, WithoutEnlargement: *o.noEnlarge, FastShrinkOnLoad: *o.fast,
			Background: bg, Kernel: k, LinearLight: *o.linearLight, Method: method, ReducingGap: *o.reducingGap}
		switch p := strings.ToLower(*o.position); p {
		case "entropy":
			ro.Interesting = goimgtools.InterestingEntropy
		case "attention":
			ro.Interesting = goimgtools.InterestingAttention
		default:
			pos, ok := map[string]goimgtools.Position{
				"northwest": goimgtools.NorthWest, "north": goimgtools.North, "northeast": goimgtools.NorthEast,
				"west": goimgtools.West, "center": goimgtools.Center, "centre": goimgtools.Center, "east": goimgtools.East,
				"southwest": goimgtools.SouthWest, "south": goimgtools.South, "southeast": goimgtools.SouthEast,
			}[p]
			if !ok {
				return nil, fmt.Errorf("-position: unknown position %q", *o.position)
			}
			ro.Position = pos
		}
		im.Resize(w, h, ro)
	}
	if *o.extend != "" {
		v, err := ints(*o.extend, 4)
		if err != nil {
			return nil, fmt.Errorf("-extend: %w", err)
		}
		switch *o.extendWith {
		case "":
			im.Extend(v[0], v[1], v[2], v[3], goimgtools.ExtendOptions{Background: bg})
		case "copy", "repeat", "mirror":
			im.Extend(v[0], v[1], v[2], v[3], goimgtools.ExtendOptions{Mode: map[string]goimgtools.ExtendMode{
				"copy": goimgtools.ExtendCopy, "repeat": goimgtools.ExtendRepeat, "mirror": goimgtools.ExtendMirror}[*o.extendWith]})
		default:
			return nil, fmt.Errorf("-extend-with: unknown way %q", *o.extendWith)
		}
	}
	if *o.grayscale || *o.grayLinear {
		im.Grayscale(goimgtools.GrayscaleOptions{Linear: *o.grayLinear})
	}
	if *o.modulate != "" {
		s := *o.modulate
		if strings.Count(s, ",") == 2 {
			s += ",0" // no lightness
		}
		v, err := floats(s, 4)
		if err != nil {
			return nil, fmt.Errorf("-modulate: %w", err)
		}
		im.Modulate(v[0], v[1], v[2], goimgtools.ModulateOptions{Lightness: v[3]})
	}
	if *o.blur != 0 {
		method, ok := map[string]goimgtools.Method{"pillow": goimgtools.MethodPillow, "libvips": goimgtools.MethodLibvips}[*o.blurMethod]
		if !ok {
			return nil, fmt.Errorf("-blur-method: want pillow or libvips, got %q", *o.blurMethod)
		}
		im.Blur(*o.blur, goimgtools.BlurOptions{Method: method, MinAmplitude: *o.blurMinAmp})
	}
	if *o.sharpen != "" {
		if parts := strings.Split(*o.sharpen, ","); len(parts) >= 2 && strings.EqualFold(parts[1], "libvips") {
			if len(parts) != 2 && len(parts) != 7 {
				return nil, fmt.Errorf("-sharpen: want SIGMA,libvips or SIGMA,libvips,FLAT,JAGGED,JAGGED_FROM,MAX_BRIGHTEN,MAX_DARKEN, got %q", *o.sharpen)
			}
			v, err := floats(strings.Join(append(parts[:1:1], parts[2:]...), ","), len(parts)-1)
			if err != nil {
				return nil, fmt.Errorf("-sharpen: %w", err)
			}
			so := goimgtools.SharpenOptions{Sigma: v[0], Method: goimgtools.MethodLibvips}
			if len(v) == 6 {
				so.Curve = &goimgtools.SharpenCurve{Flat: v[1], Jagged: v[2], JaggedFrom: v[3], MaxBrighten: v[4], MaxDarken: v[5]}
			}
			im.Sharpen(so)
		} else if strings.EqualFold(*o.sharpen, "mild") {
			im.Sharpen()
		} else {
			parts := strings.Split(*o.sharpen, ",")
			if len(parts) > 3 {
				return nil, fmt.Errorf("-sharpen: want mild or SIGMA[,AMOUNT[,THRESHOLD]], got %q", *o.sharpen)
			}
			v, err := floats(*o.sharpen, len(parts))
			if err != nil {
				return nil, fmt.Errorf("-sharpen: %w", err)
			}
			so := goimgtools.SharpenOptions{Sigma: v[0], Amount: 1.5}
			if len(v) > 1 {
				so.Amount = v[1]
			}
			if len(v) > 2 {
				if v[2] != math.Trunc(v[2]) {
					return nil, fmt.Errorf("-sharpen: the threshold is a whole number, got %v", v[2])
				}
				so.Threshold = int(v[2])
			}
			if so.Sigma == 0 {
				return nil, errors.New("-sharpen: the sigma must be above 0 (or use -sharpen mild)")
			}
			im.Sharpen(so)
		}
	}
	for _, v := range o.ops {
		name, rest, _ := strings.Cut(v, ":")
		var args []string
		if rest != "" {
			args = strings.Split(rest, ",")
		}
		im.Do(name, args...)
	}
	if *o.flatten != "" {
		c, err := parseColor(*o.flatten)
		if err != nil {
			return nil, fmt.Errorf("-flatten: %w", err)
		}
		im.Flatten(c)
	}
	if *o.watermark != "" && *o.wmText != "" {
		return nil, errors.New("give -watermark or -watermark-text, not both")
	}
	if *o.watermark != "" || *o.wmText != "" {
		g, ok := map[string]goimgtools.Position{
			"northwest": goimgtools.NorthWest, "north": goimgtools.North, "northeast": goimgtools.NorthEast,
			"west": goimgtools.West, "center": goimgtools.Center, "centre": goimgtools.Center, "east": goimgtools.East,
			"southwest": goimgtools.SouthWest, "south": goimgtools.South, "southeast": goimgtools.SouthEast,
		}[strings.ToLower(*o.gravity)]
		if !ok {
			return nil, fmt.Errorf("-gravity: unknown anchor %q", *o.gravity)
		}
		// Default: a 24 pixel margin away from whichever edges the anchor
		// touches. The nine positions are numbered row by row.
		i := int(g - goimgtools.NorthWest)
		dx := []int{24, 0, -24}[i%3]
		dy := []int{24, 0, -24}[i/3]
		if *o.offset != "" {
			v, err := ints(*o.offset, 2)
			if err != nil {
				return nil, fmt.Errorf("-offset: %w", err)
			}
			dx, dy = v[0], v[1]
		}
		wm := goimgtools.Open(*o.watermark)
		if *o.wmText != "" {
			var err error
			if wm, err = o.textImage(*o.wmText); err != nil {
				return nil, err
			}
		}
		co := goimgtools.CompositeOptions{Gravity: g, Left: dx, Top: dy, Angle: *o.wmAngle, Opacity: *o.wmOpacity, Tile: *o.wmTile}
		co.Blend = -1
		for b := goimgtools.BlendOver; b <= goimgtools.BlendSaturate; b++ {
			if b.String() == strings.ToLower(*o.wmBlend) {
				co.Blend = b
			}
		}
		if co.Blend < 0 {
			return nil, fmt.Errorf("-watermark-blend: unknown mode %q", *o.wmBlend)
		}
		if v := *o.wmWidth; v != "" {
			if pct, ok := strings.CutSuffix(v, "%"); ok {
				f, err := strconv.ParseFloat(pct, 64)
				if err != nil || !(f > 0 && f <= 1000) {
					return nil, fmt.Errorf("-watermark-width: want pixels or a percentage above 0, got %q", v)
				}
				co.WidthRatio = f / 100
			} else {
				n, err := strconv.Atoi(v)
				if err != nil || n <= 0 {
					return nil, fmt.Errorf("-watermark-width: want pixels or a percentage above 0, got %q", v)
				}
				wm.Resize(n, 0)
			}
		}
		gap, err := ints(*o.wmGap, 2)
		if err != nil {
			return nil, fmt.Errorf("-watermark-gap: %w", err)
		}
		co.TileGapX, co.TileGapY = gap[0], gap[1]
		if (*o.wmRatio != "" || *o.wmAnchor != "") && *o.offset == "" {
			co.Left, co.Top = 0, 0 // the margin only suits the plain corner placement
		}
		if *o.wmRatio != "" {
			v, err := floats(*o.wmRatio, 2)
			if err != nil {
				return nil, fmt.Errorf("-watermark-ratio: %w", err)
			}
			co.LeftRatio, co.TopRatio = v[0], v[1]
		}
		if co.Anchor, co.AnchorX, co.AnchorY, err = parseAnchor(*o.wmAnchor); err != nil {
			return nil, fmt.Errorf("-watermark-anchor: %w", err)
		}
		if co.Pivot, co.PivotX, co.PivotY, err = parseAnchor(*o.wmPivot); err != nil {
			return nil, fmt.Errorf("-watermark-pivot: %w", err)
		}
		im.Composite(wm, co)
	}
	if *o.mark != "" {
		kind, ok := map[string]goimgtools.WatermarkKind{"blind": goimgtools.WatermarkBlind, "screen": goimgtools.WatermarkScreen}[*o.markKind]
		if !ok {
			return nil, fmt.Errorf("-mark-kind: %q is not blind or screen", *o.markKind)
		}
		im.InvisibleWatermark([]byte(*o.mark), goimgtools.InvisibleWatermarkOptions{Kind: kind, Key: *o.markKey, Strength: *o.markStrength, Density: *o.markDensity})
	}
	if *o.minSSIM != 0 {
		im.MinSSIM(*o.minSSIM)
	}
	if *o.maxSize != "" {
		n, err := parseSize(*o.maxSize)
		if err != nil {
			return nil, fmt.Errorf("-max-size: %w", err)
		}
		im.MaxFileSize(n)
	}
	kind, err := o.outputKind(out)
	if err != nil {
		return nil, err
	}
	switch kind {
	case "jpeg":
		chroma, ok := map[string]goimgtools.ChromaSubsampling{"420": goimgtools.Chroma420, "4:2:0": goimgtools.Chroma420,
			"422": goimgtools.Chroma422, "4:2:2": goimgtools.Chroma422, "444": goimgtools.Chroma444, "4:4:4": goimgtools.Chroma444}[*o.jpegChroma]
		if !ok {
			return nil, fmt.Errorf("-jpeg-chroma: %q is not 420, 422 or 444", *o.jpegChroma)
		}
		im.JPEG(goimgtools.JPEGOptions{Quality: *o.quality, Optimize: *o.jpegOpt, Progressive: *o.jpegProg, Trellis: *o.jpegTrellis, OvershootDeringing: *o.jpegDering, QuantTable: goimgtools.QuantTable(*o.jpegQuantTable),
			ChromaSubsampling: chroma})
	case "png":
		im.PNG(goimgtools.PNGOptions{CompressionLevel: *o.pngLevel, Colors: *o.pngColors, NoDither: *o.noDither})
	case "webp":
		im.WebP(goimgtools.WebPOptions{Quality: *o.quality, Lossless: *o.lossless, Effort: *o.webpEffort, NearLossless: *o.webpNear, Exact: *o.webpExact, AlphaQuality: *o.webpAlphaQ})
	case "gif":
		im.GIF(goimgtools.GIFOptions{Colors: *o.gifColors, NoDither: *o.noDither, FixedPalette: *o.gifFixed, Lossy: *o.gifLossy})
	case "bmp":
		im.BMP()
	case "tiff":
		c, ok := map[string]goimgtools.TIFFCompression{"none": goimgtools.TIFFNone, "deflate": goimgtools.TIFFDeflate,
			"lzw": goimgtools.TIFFLZW, "jpeg": goimgtools.TIFFJPEG}[strings.ToLower(*o.tiffComp)]
		if !ok {
			return nil, fmt.Errorf("-tiff-compression: want none, deflate, lzw or jpeg, got %q", *o.tiffComp)
		}
		im.TIFF(goimgtools.TIFFOptions{Compression: c, Predictor: *o.tiffPredictor, Quality: *o.quality})
	case "ico":
		var sizes []int
		for _, s := range strings.Split(*o.icoSizes, ",") {
			if s = strings.TrimSpace(s); s == "" {
				continue
			}
			n, err := strconv.Atoi(s)
			if err != nil {
				return nil, fmt.Errorf("-ico-sizes: %q is not a number", s)
			}
			sizes = append(sizes, n)
		}
		im.ICO(goimgtools.ICOOptions{Sizes: sizes})
	case "pdf":
		im.PDF(goimgtools.PDFOptions{Quality: *o.quality})
	}
	// (A name without a known extension: the library reports it.)
	return im, nil
}

func ints(s string, n int) ([]int, error) {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil, fmt.Errorf("want %d numbers separated by commas, got %q", n, s)
	}
	out := make([]int, n)
	for i, p := range parts {
		v, err := strconv.Atoi(strings.TrimSpace(p))
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", p)
		}
		out[i] = v
	}
	return out, nil
}

// size parses "WxH", "Wx" or "xH".
func size(s string) (w, h int, err error) {
	a, b, ok := strings.Cut(strings.ToLower(s), "x")
	if !ok {
		return 0, 0, fmt.Errorf("want WxH, Wx or xH, got %q", s)
	}
	if a != "" {
		if w, err = strconv.Atoi(a); err != nil || w <= 0 {
			return 0, 0, fmt.Errorf("bad width %q", a)
		}
	}
	if b != "" {
		if h, err = strconv.Atoi(b); err != nil || h <= 0 {
			return 0, 0, fmt.Errorf("bad height %q", b)
		}
	}
	if w == 0 && h == 0 {
		return 0, 0, errors.New("need a width or a height")
	}
	return w, h, nil
}

// fonts lists the fonts text is drawn with.
func fonts(args []string, stdout io.Writer) error {
	list := goimgtools.DefaultFonts()
	switch {
	case len(args) == 1 && args[0] == "-all":
		list = goimgtools.SystemFonts()
	case len(args) != 0:
		return errors.New("fonts: the only option is -all")
	default:
		fmt.Fprintln(stdout, "Fonts used when -font is not given, in the order they are tried for each character:")
	}
	for _, f := range list {
		zh, file := "", f.File
		if f.Chinese {
			zh = "  [Chinese]"
		}
		if file == "" {
			file = "(built in)"
		}
		fmt.Fprintf(stdout, "%s%s\n    %s\n", f.Name, zh, file)
	}
	return nil
}

// parseColor is goimgtools.ParseColor without the package prefix on the error.
func parseColor(s string) (color.Color, error) {
	c, err := goimgtools.ParseColor(s)
	if err != nil {
		return nil, errors.New(strings.TrimPrefix(err.Error(), "goimgtools: "))
	}
	return c, nil
}

func floats(s string, n int) ([]float64, error) {
	parts := strings.Split(s, ",")
	if len(parts) != n {
		return nil, fmt.Errorf("want %d numbers separated by commas, got %q", n, s)
	}
	out := make([]float64, n)
	for i, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			return nil, fmt.Errorf("%q is not a number", p)
		}
		out[i] = v
	}
	return out, nil
}

// parseAnchor reads a point of the watermark: a name, or X,Y in pixels.
// The empty string is the default.
func parseAnchor(s string) (goimgtools.Position, float64, float64, error) {
	if s == "" {
		return goimgtools.DefaultPosition, 0, 0, nil
	}
	if a, ok := map[string]goimgtools.Position{
		"northwest": goimgtools.NorthWest, "north": goimgtools.North, "northeast": goimgtools.NorthEast,
		"west": goimgtools.West, "center": goimgtools.Center, "centre": goimgtools.Center, "east": goimgtools.East,
		"southwest": goimgtools.SouthWest, "south": goimgtools.South, "southeast": goimgtools.SouthEast,
	}[strings.ToLower(s)]; ok {
		return a, 0, 0, nil
	}
	v, err := floats(s, 2)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("want a name (northwest ... center ... southeast) or X,Y, got %q", s)
	}
	return goimgtools.CustomPosition, v[0], v[1], nil
}

// parseSize reads a file size: bytes, or a number followed by KB or MB.
func parseSize(s string) (int64, error) {
	t := strings.ToUpper(strings.TrimSpace(s))
	unit := 1.0
	switch {
	case strings.HasSuffix(t, "KB"):
		t, unit = strings.TrimSuffix(t, "KB"), 1<<10
	case strings.HasSuffix(t, "MB"):
		t, unit = strings.TrimSuffix(t, "MB"), 1<<20
	case strings.HasSuffix(t, "K"):
		t, unit = strings.TrimSuffix(t, "K"), 1<<10
	case strings.HasSuffix(t, "M"):
		t, unit = strings.TrimSuffix(t, "M"), 1<<20
	case strings.HasSuffix(t, "B"):
		t = strings.TrimSuffix(t, "B")
	}
	v, err := strconv.ParseFloat(strings.TrimSpace(t), 64)
	if err != nil || v <= 0 || v*unit > 1<<50 {
		return 0, fmt.Errorf("want a size such as 200KB or 1.5MB, got %q", s)
	}
	return int64(v * unit), nil
}

// opList prints the operations -op takes: the built-in ones and those the
// program registered.
func opList(args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return errors.New("ops takes no arguments")
	}
	for _, d := range goimgtools.Operations() {
		use := d.Name
		if d.Usage != "" {
			use += ":" + d.Usage
		}
		fmt.Fprintf(stdout, "%-44s %s\n", use, d.Help)
	}
	return nil
}
