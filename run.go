package goimgtools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"runtime/debug"

	"github.com/whhhh1500/goimgtools/internal/iccprof"
	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/resize"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// A run: opening the input, assembling the pipeline for a recipe, and
// what can be asked about an image without writing it.

// Info is what Metadata reports: what the input file is. None of it is
// changed by the operations of a recipe.
type Info struct {
	// Format is the name of the format: "jpeg", "png", "webp", "gif",
	// "bmp", "tiff", "ico" or one added with RegisterFormat (see
	// Formats). For an
	// input that is not a file it says where the pixels come from: "text"
	// (Text), "blank" (Blank), "join" (Join), "image" (FromImage) or
	// "source" (FromSource).
	Format string
	// Width and Height are the size of the image as it is stored.
	Width, Height int
	// HasAlpha reports whether the image can have transparent pixels.
	HasAlpha bool
	// Color is how the pixels are stored: "gray", "palette" (up to 256
	// colours from a table), "rgb", "ycbcr" (brightness and two colour
	// differences, as in JPEG and lossy WebP) or "cmyk". Transparency is
	// told by HasAlpha, not here.
	Color string
	// Bits is the number of bits of one channel of a pixel: 8 or 16.
	Bits int
	// Orientation is the EXIF orientation, 1 (upright) to 8; 1 when the
	// file does not say. Rotate() without arguments undoes it. From 5 on,
	// the upright image has Width and Height exchanged.
	Orientation int
	HasICC      bool // an ICC colour profile is embedded
	HasExif     bool
	// HasXmp reports whether the file carries an XMP packet (KeepXmp).
	HasXmp bool
	// Density is the resolution the file records, in pixels per inch
	// (dots per inch); 0 when it records none.
	Density float64
}

// build opens the input and assembles the pipeline. env is nil for a
// top-level image, or the parent's for an overlay. With sizeOnly only the
// size of the result is wanted.
func (m *Image) build(env *pipeline.Env, sizeOnly bool) (pipeline.Source, io.Closer, string, pipeline.Meta, error) {
	if m.err != nil {
		return nil, nil, "", pipeline.Meta{}, m.err
	}
	f, format, err := m.openInput()
	if err != nil {
		return nil, nil, "", pipeline.Meta{}, err
	}
	return m.buildFrom(env, env == nil, f, format, sizeOnly, false)
}

// openInput opens the input and recognises its format.
func (m *Image) openInput() (*input, string, error) {
	if m.open == nil {
		return nil, "", errors.New("goimgtools: an Image is made with Open, FromBytes, FromReader, FromImage, FromSource or Text")
	}
	f, err := m.open()
	if err != nil {
		return nil, "", err
	}
	if f.gen != nil {
		return f, generated, nil
	}
	head, err := f.br.Peek(sniffLen)
	// A read that failed is remembered by the input; one that never
	// delivers anything is found here.
	if err != nil && err != io.EOF && err != bufio.ErrBufferFull && f.ioErr == nil {
		f.ioErr = err
	}
	def, err := sniff(head)
	if err != nil {
		f.Close()
		return nil, "", f.classify(err)
	}
	return f, def.Name, nil
}

func (m *Image) newEnv() *pipeline.Env {
	limit := m.limit
	if limit == 0 {
		limit = DefaultMemoryLimit
	}
	env := pipeline.NewEnv(limit, m.tempDir)
	env.SetHelpers(m.helperCount())
	return env
}

// decode starts the decoder on an opened input and holds the image against
// the limits. From then on what the decoder reports is sorted into bad
// input (ErrFormat) and everything else. It closes the input when it fails.
func (m *Image) decode(env *pipeline.Env, f *input, format string) (pipeline.Source, error) {
	src, err := f.decoder(format, env)
	if err != nil {
		f.Close()
		return nil, f.classify(err)
	}
	if format != generated {
		src = &decoded{Source: src, in: f}
	}
	if b := src.Bounds(); b.Empty() {
		err = fmt.Errorf("%w: the image has no pixels", ErrFormat)
	} else if limit := m.pixelLimit(); int64(b.Dx())*int64(b.Dy()) > limit {
		err = fmt.Errorf("%w: %dx%d is more than the limit of %d pixels (see LimitInputPixels)", ErrPixelLimit, b.Dx(), b.Dy(), limit)
	}
	if err != nil {
		src.Close()
		f.Close()
		return nil, err
	}
	return src, nil
}

// buildFrom assembles the pipeline over an opened input, which it closes
// when it fails. top is false for an overlay, whose env is the parent's; a
// top-level image may be given its env too.
//
// keepYCbCr says that the pixels go straight to an encoder that takes the
// YCbCr of a JPEG as it is (see output.go's takesYCbCr): the decoder then
// hands it out unconverted.
func (m *Image) buildFrom(env *pipeline.Env, top bool, f *input, format string, sizeOnly, keepYCbCr bool) (pipeline.Source, io.Closer, string, pipeline.Meta, error) {
	var none pipeline.Meta
	if env == nil {
		env = m.newEnv()
	}
	src, err := m.decode(env, f, format)
	if err != nil {
		return nil, nil, "", none, err
	}
	meta := pipeline.MetaOf(src)
	r := &run{exifOrient: pipeline.ExifOrientation(meta.Exif), sizeOnly: sizeOnly, maxPixels: m.outputLimit(), hooks: m.hooks}
	// A JPEG that the chain starts by reducing to half its size or less
	// can be made smaller while it is decoded, when the resize allows it.
	// Also when only the size is wanted: with MethodLibvips the size of
	// the result depends on it. Nothing is decoded yet.
	var pre preShrunk
	if format == "jpeg" {
		// With hooks the steps are done as written: only a resize that
		// comes first can have the decoder's help.
		if z, turn, ok := firstResize(m.chain, r); ok && (z.spec.o.FastShrinkOnLoad || z.spec.o.Method == MethodLibvips) && (len(m.hooks) == 0 || turn == ops.Upright && len(m.chain) > 0 && isResize(m.chain[0])) {
			var k int
			switch {
			case z.spec.o.Method == MethodPillow:
				k = pillowDraft(src, z.spec, turn)
			case z.spec.o.Method != MethodLibvips:
				k = shrinkFactor(src, z.spec, turn)
			case autoTurns(m.chain[:firstResizeAt(m.chain, r)]):
				// sharp reduces a JPEG as it decodes it unless it turns
				// it first.
				k = vipsShrinkOnLoad(src, z.spec, turn)
			}
			if k > 1 {
				pre = preShrunk{orig: src.Bounds().Size(), k: k}
				// The decoder reduces the blocks as it decodes them;
				// failing that, the decoded pixels are averaged.
				shrunk := false
				if z.spec.o.Method == MethodLibvips || z.spec.o.Method == MethodPillow {
					// libvips' decoder, and Pillow's, is libjpeg: its
					// scaled decoding.
					j, ok := src.(interface{ ShrinkOnLoadLibjpeg(k int) bool })
					shrunk = ok && j.ShrinkOnLoadLibjpeg(k)
				} else if j, ok := src.(interface{ ShrinkOnLoad(k int) bool }); ok {
					shrunk = j.ShrinkOnLoad(k)
				}
				if shrunk {
					if z.spec.o.Method == MethodDefault {
						// The resize scales the planes as they are
						// decoded.
						if j, ok := src.(interface{ KeepYCbCr() }); ok {
							j.KeepYCbCr()
						}
						if j, ok := src.(interface {
							Subsampling() (image.YCbCrSubsampleRatio, bool, bool)
						}); ok {
							pre.ratio, pre.gray, pre.planes = j.Subsampling()
						}
					}
				} else {
					// resize.Shrink averages the decoder's YCbCr.
					if j, ok := src.(interface{ KeepYCbCr() }); ok {
						j.KeepYCbCr()
					}
					small, err := resize.Shrink(src, k)
					if err != nil {
						src.Close()
						f.Close()
						return nil, nil, "", none, err
					}
					src = small
				}
			}
		}
	}
	if format == "webp" {
		// With FastShrinkOnLoad libwebp scales a WebP as it decodes it.
		if z, turn, ok := firstResize(m.chain, r); ok && z.spec.o.FastShrinkOnLoad && (len(m.hooks) == 0 || turn == ops.Upright && len(m.chain) > 0 && isResize(m.chain[0])) {
			j, can := src.(interface{ ScaleOnLoad(w, h int) bool })
			switch {
			case !can:
			case z.spec.o.Method == MethodLibvips && autoTurns(m.chain[:firstResizeAt(m.chain, r)]):
				// As sharp (see vipsWebPScale).
				if w, h := vipsWebPScale(src, z.spec, turn); w > 0 {
					j.ScaleOnLoad(w, h)
				}
			case z.spec.o.Method == MethodDefault && !z.spec.o.LinearLight && pipeline.IsOpaque(src):
				// Only a lossy WebP without alpha: libwebp scales the
				// colours of one with alpha without weighing them by it,
				// and decoding a lossless one takes most of the time.
				orig := src.Bounds().Size()
				if w, h := webpScale(src, z.spec, turn); w > 0 && j.ScaleOnLoad(w, h) {
					pre = preShrunk{orig: orig, whole: true}
				}
			}
		}
	}
	if format == "svg" {
		// A vector is drawn at the size the resize scales to, sharp at any
		// size, rather than drawn at its own and resampled.
		if z, turn, ok := firstResize(m.chain, r); ok && z.spec.o.Method == MethodDefault && !z.spec.o.LinearLight && (len(m.hooks) == 0 || turn == ops.Upright && len(m.chain) > 0 && isResize(m.chain[0])) {
			if j, can := src.(interface{ ScaleOnLoad(w, h int) bool }); can {
				b := src.Bounds()
				dw, dh := z.spec.target(turn.Size(b.Dx(), b.Dy()))
				iw, ih := turn.Size(dw, dh)
				if r.fits(dw, dh) == nil && iw > 0 && ih > 0 && j.ScaleOnLoad(iw, ih) {
					pre = preShrunk{orig: b.Size(), whole: true, exact: true}
				}
			}
		}
	}
	// A CMYK image is turned into sRGB through its colour profile as soon
	// as it is decoded, as sharp does; a file without one (or with one that
	// cannot be read, or not a CMYK one) through libvips's CMYK profile, as
	// libvips and sharp take it. An image made in memory without a profile
	// keeps the values color.CMYK gives.
	if !sizeOnly && src.ColorModel() == color.CMYKModel {
		p, err := iccprof.Parse(meta.ICC)
		if err != nil || p.Channels() != 4 {
			p = nil
			if format != generated {
				p = iccprof.DefaultCMYK()
			}
		}
		if p != nil {
			src = ops.CMYKToSRGB(src, p.ToSRGB())
			meta.ICC = nil
		}
	}
	if format == "jpeg" && keepYCbCr && pre.k <= 1 {
		if j, ok := src.(interface{ KeepYCbCr() }); ok {
			j.KeepYCbCr()
		}
	}
	if top && m.ctx != nil {
		src = cancelable{src, m.ctx}
	}
	if top && m.threadCount() >= 2 {
		src = pipeline.Async(src)
	}
	func() {
		// On a panic the chain closes itself (applyOps); the input too.
		defer func() {
			if v := recover(); v != nil {
				f.Close()
				panic(v)
			}
		}()
		src, err = applyOps(m.chain, src, r, pre)
	}()
	if err != nil {
		src.Close()
		f.Close()
		return nil, nil, "", none, err
	}
	return src, f, format, meta, nil
}

// Metadata reads the header of the input and reports what the file is: its
// format, its size as stored, whether it has transparency, its EXIF
// orientation and whether it carries a colour profile or an EXIF block.
// The pixels are not decoded, and the operations of the recipe play no
// part: OutputSize tells what they lead to.
//
// This is also the cheap way to check an upload: a file that is not an
// image this package reads gives an ErrFormat, one with too many pixels an
// ErrPixelLimit. (Damage further inside the file only shows when the
// pixels are decoded.)
func (m *Image) Metadata() (info Info, err error) {
	defer guard(&err)
	if m.err != nil {
		return Info{}, m.err
	}
	f, format, err := m.openInput()
	if err != nil {
		return Info{}, err
	}
	src, err := m.decode(m.newEnv(), f, format)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()
	defer src.Close()
	if format == generated {
		format = f.kind
	}
	b, meta := src.Bounds(), pipeline.MetaOf(src)
	model := src.ColorModel()
	// A decoder that converts the pixels (lossy WebP's Y'CbCr into RGB)
	// tells how they are stored.
	inner := src
	if d, ok := src.(*decoded); ok {
		inner = d.Source
	}
	if s, ok := inner.(interface{ StoredColorModel() color.Model }); ok {
		model = s.StoredColorModel()
	}
	kind, bits := colorOf(model)
	return Info{Format: format, Width: b.Dx(), Height: b.Dy(), HasAlpha: !pipeline.IsOpaque(src), Color: kind, Bits: bits,
		Orientation: pipeline.ExifOrientation(meta.Exif), HasICC: len(meta.ICC) > 0, HasExif: len(meta.Exif) > 0,
		HasXmp: len(meta.Xmp) > 0, Density: meta.Density}, nil
}

// colorOf names how a colour model stores a pixel, and with how many bits
// a channel.
func colorOf(m color.Model) (kind string, bits int) {
	if _, ok := m.(color.Palette); ok {
		return "palette", 8
	}
	switch m {
	case color.GrayModel:
		return "gray", 8
	case color.Gray16Model:
		return "gray", 16
	case color.YCbCrModel, color.NYCbCrAModel:
		return "ycbcr", 8
	case color.CMYKModel:
		return "cmyk", 8
	case color.RGBA64Model, color.NRGBA64Model:
		return "rgb", 16
	}
	return "rgb", 8
}

// OutputSize reports the width and height the result will have, after
// every operation of the recipe, without decoding the pixels.
func (m *Image) OutputSize() (width, height int, err error) {
	defer guard(&err)
	src, closer, _, _, err := m.build(nil, true)
	if err != nil {
		return 0, 0, err
	}
	defer closer.Close()
	defer src.Close()
	b := src.Bounds()
	return b.Dx(), b.Dy(), nil
}

// guard turns a panic into the error of a run: see pipeline.PanicError.
// Every method that runs a recipe defers it.
func guard(err *error) {
	if v := recover(); v != nil {
		if p, ok := v.(*pipeline.PanicError); ok { // raised again by Parallel
			*err = p
			return
		}
		*err = &pipeline.PanicError{Value: v, Stack: debug.Stack()}
	}
}

// keepOpen is a source whose Close does nothing, for a stage that closes
// what it reads but is given a source it does not own.
type keepOpen struct{ pipeline.Source }

func (k keepOpen) Close() error { return nil }

func (k keepOpen) Env() *pipeline.Env { return pipeline.EnvOf(k.Source) }

func (k keepOpen) Opaque() bool { return pipeline.IsOpaque(k.Source) }

// cancelable is a source that ends with the context's error once the
// context is done. Its Close closes the source under it.
type cancelable struct {
	pipeline.Source
	ctx context.Context
}

func (c cancelable) Next() (image.Image, error) {
	if err := c.ctx.Err(); err != nil {
		return nil, err
	}
	return c.Source.Next()
}

func (c cancelable) Env() *pipeline.Env { return pipeline.EnvOf(c.Source) }

func (c cancelable) Opaque() bool { return pipeline.IsOpaque(c.Source) }
