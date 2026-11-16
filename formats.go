package goimgtools

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/whhhh1500/goimgtools/internal/bmpstream"
	"github.com/whhhh1500/goimgtools/internal/gifstream"
	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/internal/pngstream"
	"github.com/whhhh1500/goimgtools/internal/quant"
	"github.com/whhhh1500/goimgtools/internal/tiffstream"
	"github.com/whhhh1500/goimgtools/internal/vp8enc"
	"github.com/whhhh1500/goimgtools/internal/vp8lenc"
	"github.com/whhhh1500/goimgtools/internal/webpstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Format names an image format this package reads or writes.
type Format struct {
	// Name is what the format is called everywhere: in Info.Format, in
	// ToFormat, in error messages, on the command line. The formats of
	// this package are "jpeg", "png", "webp", "gif", "bmp" and "tiff".
	Name string
	// Extensions are the file extensions of the format, in lower case and
	// with the dot. The first is the one to give a new file.
	Extensions []string
}

// Input is the file a decoder reads (see Codec). It can be read in two
// ways; a decoder uses one of them.
type Input interface {
	// Read reads the file front to back, starting at its first byte. It
	// is buffered: small reads cost little.
	io.Reader
	// ReaderAt gives the whole file for reading at any offset, and its
	// size. An input that can only be read front to back (FromReader) is
	// first copied to working storage, a temporary file when it is larger
	// than its share of the memory limit. A decoder may read a header with
	// Read first and then go on with ReaderAt (within the first 64KB);
	// after ReaderAt, Read is no longer to be used.
	ReaderAt() (io.ReaderAt, int64, error)
}

// EncodeOptions is what an encoder is given besides the pixels (see Codec).
type EncodeOptions struct {
	// Quality is the quality to encode with, 1 to 100, for a format that
	// has one (Codec.Quality); zero otherwise. It is the quality the
	// options ask for, or the one MaxFileSize is trying.
	Quality int
	// Exif and ICC are the EXIF block (in its TIFF form, starting "II" or
	// "MM") and the colour profile to put into the file; nil for none. An
	// encoder whose format cannot hold them leaves them out.
	Exif, ICC []byte
	// Options is what the caller gave for the format: the options value
	// of JPEG, PNG, WebP, GIF or TIFF (a JPEGOptions and so on) or the one
	// given to ToFormat. Nil when nothing was given.
	Options any
}

// Codec teaches this package a format: how to recognise its files, read
// them and write them. See RegisterFormat.
type Codec struct {
	Format
	// Match reports whether a file that starts with head is of this
	// format. head holds the first 32 bytes, or all of a shorter file.
	Match func(head []byte) bool
	// Decode starts reading an image. It returns as soon as the size of
	// the image is known; the pixels are read strip by strip as the
	// Source is asked for them. Buffers that grow with the image are
	// reserved from env.Budget (see package pipeline). A Source that also
	// implements pipeline.MetaSource hands on the EXIF block and colour
	// profile it found, one that implements pipeline.OpaqueSource says
	// that the image has no transparency.
	//
	// Nil for a format that can only be written.
	Decode func(in Input, env *pipeline.Env) (pipeline.Source, error)
	// Encode reads src to its end and writes the file to w. It does not
	// close src. It must return the error src.Next gave when that fails:
	// the file is only complete when src reached io.EOF.
	//
	// Nil for a format that can only be read.
	Encode func(w io.Writer, src pipeline.Source, o EncodeOptions) error
	// Quality is for a format whose file size follows a quality of 1 to
	// 100, and is what makes MaxFileSize work for it. It returns the
	// quality the given options ask for (the default when they ask for
	// none), and false when with these options there is no such quality
	// (a lossless mode). Nil for a format without one.
	Quality func(options any) (quality int, ok bool)
}

// formatDef is everything this package knows about one format. Recognising
// a file, opening it, writing it, its names and its extensions are all
// here and nowhere else; the formats of this package and those that
// RegisterFormat adds are kept the same way.
type formatDef struct {
	Format
	// magic reports whether a file that starts with h is of this format.
	magic func(h []byte) bool
	// open starts the decoder on an input; nil when the format is not read.
	open func(in *input, env *pipeline.Env) (pipeline.Source, error)
	// encode writes src; nil when the format is not written.
	encode func(w io.Writer, src pipeline.Source, o EncodeOptions) error
	// quality is set for formats whose file size follows a quality setting
	// (see Codec.Quality).
	quality func(options any) (int, bool)
	// check, for the formats of this package, finds options that are not
	// of the format's own type before anything is read.
	check func(options any) error
	// codec is what RegisterFormat was given, for a format added by it.
	codec *Codec
	// noProfile is set for the formats of this package that cannot carry
	// a colour profile: their pixels are converted to sRGB instead (see
	// ToSRGB).
	noProfile bool
}

// optionsOf returns the output options of one of this package's formats:
// options when it is a T or a *T, the zero value when there are none.
func optionsOf[T any](options any, format string) (T, error) {
	var zero T
	switch v := options.(type) {
	case nil:
		return zero, nil
	case T:
		return v, nil
	case *T:
		if v == nil {
			return zero, nil
		}
		return *v, nil
	}
	return zero, fmt.Errorf("goimgtools: %s output takes a %T, not a %T", format, zero, options)
}

func checks[T any](format string) func(any) error {
	return func(options any) error {
		_, err := optionsOf[T](options, format)
		return err
	}
}

// builtin are the formats of this package.
var builtin = []*formatDef{
	{
		Format: Format{"jpeg", []string{".jpg", ".jpeg"}},
		magic:  func(h []byte) bool { return bytes.HasPrefix(h, []byte("\xff\xd8")) },
		open: func(in *input, env *pipeline.Env) (pipeline.Source, error) {
			src, err := jpegstream.Open(in.br, env)
			// With random access, a progressive JPEG is decoded with its
			// scans in step instead of storing its coefficients.
			if err == nil && (in.ra != nil || in.at != nil) {
				jpegstream.WithRandomAccess(src, func() (io.ReaderAt, int64, error) { return in.readerAt(env) })
			}
			return src, err
		},
		encode: func(w io.Writer, src pipeline.Source, o EncodeOptions) error {
			jo, err := optionsOf[JPEGOptions](o.Options, "jpeg")
			if err != nil {
				return err
			}
			q := o.Quality
			if q == 0 {
				q = jpegQuality(jo)
			}
			if q < 1 || q > 100 {
				return fmt.Errorf("goimgtools: JPEG quality must be 1 to 100, got %d", q)
			}
			return jpegstream.Encode(w, src, &jpegstream.Options{Quality: q, Exif: o.Exif, ICC: o.ICC,
				Optimize: jo.Optimize, Progressive: jo.Progressive, Trellis: jo.Trellis, QuantTable: int(jo.QuantTable)})
		},
		quality: func(options any) (int, bool) {
			jo, _ := optionsOf[JPEGOptions](options, "jpeg")
			return jpegQuality(jo), true
		},
		check: checks[JPEGOptions]("jpeg"),
	},
	{
		Format: Format{"png", []string{".png"}},
		magic:  func(h []byte) bool { return bytes.HasPrefix(h, []byte("\x89PNG\r\n\x1a\n")) },
		open:   func(in *input, env *pipeline.Env) (pipeline.Source, error) { return pngstream.Open(in.br, env) },
		encode: func(w io.Writer, src pipeline.Source, o EncodeOptions) error {
			po, err := optionsOf[PNGOptions](o.Options, "png")
			if err != nil {
				return err
			}
			if po.Colors != 0 {
				// The source stays the caller's to close.
				q, err := quant.Quantize(keepOpen{src}, &quant.Options{Colors: po.Colors, NoDither: po.NoDither})
				if err != nil {
					return err
				}
				defer q.Close()
				src = q
			}
			level := po.CompressionLevel
			if level == 0 {
				level = DefaultPNGLevel
			}
			return pngstream.Encode(w, src, &pngstream.Options{Level: level, Exif: o.Exif, ICC: o.ICC})
		},
		check: checks[PNGOptions]("png"),
	},
	{
		Format: Format{"webp", []string{".webp"}},
		magic: func(h []byte) bool {
			return len(h) >= 12 && string(h[:4]) == "RIFF" && string(h[8:12]) == "WEBP"
		},
		open: func(in *input, env *pipeline.Env) (pipeline.Source, error) {
			src, err := in.openAt(env, webpstream.Open)
			if err != nil {
				return nil, err
			}
			// Lossy WebP is Y'CbCr; turn it into the RGB libwebp, and
			// so the browsers, show (see webpstream.ToRGB).
			return webpstream.ToRGB(src), nil
		},
		encode: func(w io.Writer, src pipeline.Source, o EncodeOptions) error {
			wo, err := optionsOf[WebPOptions](o.Options, "webp")
			if err != nil {
				return err
			}
			// The colour profile and EXIF go into the extended format;
			// without them the file stays in the simple one.
			var mw *webpstream.MetaWriter
			if len(o.ICC) > 0 || len(o.Exif) > 0 {
				mw = webpstream.NewMetaWriter(w, pipeline.Meta{Exif: o.Exif, ICC: o.ICC})
				w = mw
			}
			if wo.Lossless {
				err = vp8lenc.Encode(w, src, &vp8lenc.Options{Effort: wo.Effort})
			} else {
				q := o.Quality
				if q == 0 {
					q = wo.Quality
				}
				err = vp8enc.Encode(w, src, &vp8enc.Options{Quality: q, Effort: wo.Effort})
			}
			if err == nil && mw != nil {
				err = mw.Finish()
			}
			return err
		},
		quality: func(options any) (int, bool) {
			wo, _ := optionsOf[WebPOptions](options, "webp")
			switch {
			case wo.Lossless:
				return 0, false
			case wo.Quality == 0:
				return 75, true
			}
			return wo.Quality, true
		},
		check: checks[WebPOptions]("webp"),
	},
	{
		Format:    Format{"gif", []string{".gif"}},
		noProfile: true,
		magic:     func(h []byte) bool { return bytes.HasPrefix(h, []byte("GIF8")) },
		open:      func(in *input, env *pipeline.Env) (pipeline.Source, error) { return gifstream.Open(in.br, env) },
		encode: func(w io.Writer, src pipeline.Source, o EncodeOptions) error {
			g, err := optionsOf[GIFOptions](o.Options, "gif")
			if err != nil {
				return err
			}
			if g.Colors < 0 || g.Colors > 256 {
				return errors.New("goimgtools: GIF colours must be 1 to 256")
			}
			// One colour is no choice to make; the fixed palette handles it.
			if !g.FixedPalette && g.Colors != 1 {
				// The source stays the caller's to close.
				q, err := quant.Quantize(ops.BinaryAlpha(keepOpen{src}), &quant.Options{Colors: g.Colors, NoDither: g.NoDither})
				if err != nil {
					return err
				}
				defer q.Close()
				src = q
			}
			return gifstream.Encode(w, src, &gifstream.Options{NumColors: g.Colors, NoDither: g.NoDither, Lossy: g.Lossy})
		},
		check: checks[GIFOptions]("gif"),
	},
	{
		Format:    Format{"bmp", []string{".bmp"}},
		noProfile: true,
		magic:     func(h []byte) bool { return bytes.HasPrefix(h, []byte("BM")) },
		open: func(in *input, env *pipeline.Env) (pipeline.Source, error) {
			return in.openAt(env, bmpstream.Open)
		},
		encode: func(w io.Writer, src pipeline.Source, _ EncodeOptions) error {
			return bmpstream.Encode(w, src)
		},
		check: func(options any) error {
			if options != nil {
				return fmt.Errorf("goimgtools: bmp output has no options, got a %T", options)
			}
			return nil
		},
	},
	{
		Format: Format{"tiff", []string{".tif", ".tiff"}},
		magic: func(h []byte) bool {
			return bytes.HasPrefix(h, []byte("II*\x00")) || bytes.HasPrefix(h, []byte("MM\x00*"))
		},
		open: func(in *input, env *pipeline.Env) (pipeline.Source, error) {
			return in.openAt(env, tiffstream.Open)
		},
		encode: func(w io.Writer, src pipeline.Source, o EncodeOptions) error {
			to, err := optionsOf[TIFFOptions](o.Options, "tiff")
			if err != nil {
				return err
			}
			out := &tiffstream.Options{ICC: o.ICC}
			switch to.Compression {
			case TIFFNone:
			case TIFFDeflate:
				out.Compression = tiffstream.Deflate
			default:
				return errors.New("goimgtools: unknown TIFF compression")
			}
			return tiffstream.Encode(w, src, out)
		},
		check: checks[TIFFOptions]("tiff"),
	},
}

func jpegQuality(o JPEGOptions) int {
	if o.Quality == 0 {
		return 80
	}
	return o.Quality
}

// openAt opens a decoder that reads its input at any offset.
func (in *input) openAt(env *pipeline.Env, open func(io.ReaderAt, int64, *pipeline.Env) (pipeline.Source, error)) (pipeline.Source, error) {
	ra, size, err := in.readerAt(env)
	if err != nil {
		return nil, err
	}
	return open(ra, size, env)
}

// The formats known at the moment: those RegisterFormat added, newest
// first, then the ones of this package. The slice is replaced, never
// changed, so that a run can keep using the one it got.
var (
	formatsMu sync.RWMutex
	formats   = builtin
)

func allFormats() []*formatDef {
	formatsMu.RLock()
	defer formatsMu.RUnlock()
	return formats
}

// RegisterFormat adds a format to the ones this package reads and writes.
// From then on its files are recognised by Open, FromBytes and FromReader,
// named in Info.Format, and written by ToFile for its extensions and by
// ToFormat; everything else (the operations, MemoryLimit, MaxFileSize when
// the Codec has a Quality) works on it like on the formats of this package.
//
// A Codec with the name of a known format replaces it. That is the way to
// change how one of this package's formats is read or written, or to add
// to it: take the format with CodecByName, wrap its Decode or Encode in a
// function of your own, and register the result. UnregisterFormat undoes
// it.
//
//	jpeg, _ := goimgtools.CodecByName("jpeg")
//	encode := jpeg.Encode
//	jpeg.Encode = func(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
//		start := time.Now()
//		defer func() { log.Printf("jpeg %v: %v", src.Bounds().Size(), time.Since(start)) }()
//		return encode(w, src, o)
//	}
//	goimgtools.RegisterFormat(jpeg)
//
// A format registered later is asked first whether a file is its own, so a
// new format can claim files an older one would also accept.
//
// RegisterFormat is usually called from an init function. It may be called
// at any time and from any goroutine; runs that have already started keep
// the formats they began with. It panics when the Codec cannot work: no
// name, neither Decode nor Encode, or a Decode without Match.
func RegisterFormat(c Codec) {
	name := strings.ToLower(strings.TrimSpace(c.Name))
	switch {
	case name == "" || strings.ContainsAny(name, " \t\r\n./\\"):
		panic(fmt.Sprintf("goimgtools: RegisterFormat: %q is not a format name (one word, such as \"qoi\")", c.Name))
	case c.Decode == nil && c.Encode == nil:
		panic("goimgtools: RegisterFormat: " + name + " has neither Decode nor Encode")
	case c.Decode != nil && c.Match == nil:
		panic("goimgtools: RegisterFormat: " + name + " has a Decode but no Match to recognise its files")
	}
	def := &formatDef{Format: Format{Name: name}, magic: c.Match, encode: c.Encode, quality: c.Quality}
	for _, e := range c.Extensions {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || e == "." {
			continue
		}
		if !strings.HasPrefix(e, ".") {
			e = "." + e
		}
		def.Extensions = append(def.Extensions, e)
	}
	if decode := c.Decode; decode != nil {
		def.open = func(in *input, env *pipeline.Env) (pipeline.Source, error) {
			src, err := decode(&codecInput{in: in, env: env}, env)
			if err == nil && src == nil {
				err = errors.New("goimgtools: the decoder of " + name + " returned neither an image nor an error")
			}
			return src, err
		}
	}
	kept := c
	kept.Format = def.copyFormat()
	def.codec = &kept

	formatsMu.Lock()
	defer formatsMu.Unlock()
	next := make([]*formatDef, 0, len(formats)+1)
	replaced := false
	for _, f := range formats {
		if f.Name == name {
			f, replaced = def, true
		}
		next = append(next, f)
	}
	if !replaced {
		next = append([]*formatDef{def}, next...)
	}
	formats = next
}

// UnregisterFormat takes a RegisterFormat back: the format is no longer
// known, or, when it had replaced one of the package's own formats, the
// package's own is used again. It reports whether there was such a
// registration. It is for a format that is only wanted for a while, and
// for tests, which should leave the formats as they found them.
func UnregisterFormat(name string) bool {
	name = strings.ToLower(strings.TrimSpace(name))
	formatsMu.Lock()
	defer formatsMu.Unlock()
	next := make([]*formatDef, 0, len(formats))
	found := false
	for _, f := range formats {
		if f.Name != name || f.codec == nil {
			next = append(next, f)
			continue
		}
		found = true
		for _, own := range builtin {
			if own.Name == name {
				next = append(next, own)
			}
		}
	}
	if found {
		formats = next
	}
	return found
}

// CodecByName returns the Codec of a format, found the way FormatByName
// finds it. For a format of this package its Decode and Encode are the
// package's own decoder and encoder, ready to be called from, or wrapped
// by, a Codec of your own (see RegisterFormat).
func CodecByName(name string) (Codec, bool) {
	f, ok := FormatByName(name)
	if !ok {
		return Codec{}, false
	}
	return formatNamed(f.Name).toCodec(), true
}

func (f *formatDef) copyFormat() Format {
	return Format{f.Name, append([]string(nil), f.Extensions...)}
}

func (f *formatDef) toCodec() Codec {
	if f.codec != nil {
		c := *f.codec
		c.Format = f.copyFormat()
		return c
	}
	c := Codec{Format: f.copyFormat(), Match: f.magic, Encode: f.encode, Quality: f.quality}
	if open := f.open; open != nil {
		c.Decode = func(in Input, env *pipeline.Env) (pipeline.Source, error) {
			if env == nil {
				env = pipeline.NewEnv(0, "")
			}
			return open(inputOf(in), env)
		}
	}
	return c
}

// codecInput is the Input a registered decoder is given.
type codecInput struct {
	in  *input
	env *pipeline.Env
	// head is what has been read front to back so far. It is kept for an
	// input that cannot be read again (FromReader), so that a decoder can
	// read the header in order and then ask for random access; and given
	// up (over) once it would be more than headMax.
	head []byte
	over bool
}

const headMax = 64 << 10

// keeping reports whether what is read still has to be remembered.
func (c *codecInput) keeping() bool { return c.in.ra == nil && c.in.at == nil && !c.over }

func (c *codecInput) Read(p []byte) (int, error) {
	n, err := c.in.br.Read(p)
	if n > 0 && c.keeping() {
		if len(c.head)+n > headMax {
			c.head, c.over = nil, true
		} else {
			c.head = append(c.head, p[:n]...)
		}
	}
	return n, err
}

// ReadByte is there for decoders that look for an io.ByteReader before
// they add a buffer of their own.
func (c *codecInput) ReadByte() (byte, error) {
	b, err := c.in.br.ReadByte()
	if err == nil && c.keeping() {
		if len(c.head) >= headMax {
			c.head, c.over = nil, true
		} else {
			c.head = append(c.head, b)
		}
	}
	return b, err
}

func (c *codecInput) ReaderAt() (io.ReaderAt, int64, error) {
	if c.over {
		return nil, 0, errors.New("goimgtools: Input.ReaderAt was asked for after more than 64KB of an input that cannot be read twice had been read front to back")
	}
	ra, size, err := c.in.readerAtAfter(c.env, c.head)
	c.head = nil
	return ra, size, err
}

// inputOf returns what a decoder of this package reads, for the Input a
// Codec's Decode was called with.
func inputOf(in Input) *input {
	if c, ok := in.(*codecInput); ok {
		return c.in
	}
	return &input{br: bufio.NewReaderSize(in, 64<<10), at: in.ReaderAt}
}

// formatNamed returns the format with exactly that name, or nil.
func formatNamed(name string) *formatDef {
	for _, f := range allFormats() {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// Formats lists the formats known to this package: the ones added with
// RegisterFormat, newest first, then its own.
func Formats() []Format {
	all := allFormats()
	out := make([]Format, len(all))
	for i, f := range all {
		out[i] = f.copyFormat()
	}
	return out
}

// FormatByName finds a format by its name or by a file extension, with or
// without the dot, in any letter case: "jpeg", "JPG", ".tif".
func FormatByName(s string) (Format, bool) {
	s = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(s), "."))
	if s == "" {
		return Format{}, false
	}
	all := allFormats()
	for _, f := range all {
		if f.Name == s {
			return f.copyFormat(), true
		}
	}
	for _, f := range all {
		for _, e := range f.Extensions {
			if e == "."+s {
				return f.copyFormat(), true
			}
		}
	}
	return Format{}, false
}

// formatList names every format that can be written, for error messages:
// "jpeg, png, ... or tiff".
func formatList() string {
	var names []string
	for _, f := range allFormats() {
		if f.encode != nil {
			names = append(names, f.Name)
		}
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// sniffLen is how much of a file is looked at to recognise its format.
const sniffLen = 32

// sniff recognises the format of a file from its first bytes.
func sniff(h []byte) (*formatDef, error) {
	for _, f := range allFormats() {
		if f.open != nil && f.magic != nil && f.magic(h) {
			return f, nil
		}
	}
	if len(h) < 4 {
		return nil, errors.New("goimgtools: not an image (too short)")
	}
	return nil, errors.New("goimgtools: unknown image format")
}

// outputFormat decides what is written: the format chosen with JPEG(),
// PNG() and so on, else the one the file extension stands for, else the
// format of the input (PNG for an input that is made, not read).
func (m *Image) outputFormat(ext, inFormat string) (string, error) {
	const choose = "choose the output with JPEG(), PNG(), WebP(), GIF(), BMP(), TIFF() or ToFormat()"
	switch {
	case m.format != "":
		return m.format, nil
	case ext != "":
		f, ok := FormatByName(ext)
		if !ok {
			return "", fmt.Errorf("goimgtools: cannot write a .%s file; use a name that ends in the extension of %s, or %s", ext, formatList(), choose)
		}
		return f.Name, nil
	case inFormat == generated:
		return "png", nil
	}
	if def := formatNamed(inFormat); def == nil || def.encode == nil {
		return "", fmt.Errorf("goimgtools: %s can be read but not written; %s", inFormat, choose)
	}
	return inFormat, nil
}
