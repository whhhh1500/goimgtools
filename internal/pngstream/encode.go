package pngstream

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/density"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the encoding parameters.
type Options struct {
	// Level is the zlib compression level, 1 (fastest) to 9 (smallest).
	// Zero means the default, which is what the standard library uses.
	Level int
	// Exif (in TIFF form) and ICC are written as eXIf and iCCP chunks when
	// set. They are the only bytes png.Encode would not write.
	Exif []byte
	ICC  []byte
	// Density, when above 0, is the resolution to record in a pHYs chunk,
	// in pixels per inch (stored per metre).
	Density float64
}

// Encode writes src to w as a PNG. writer.go is the standard library's
// encoder with its row loop reading strips, so for Gray, Gray16, RGBA,
// NRGBA, RGBA64, NRGBA64 and Paletted strips the bytes are those png.Encode
// writes for the same image. Other pixel types (YCbCr, CMYK, ...) are
// converted to 8-bit RGBA or NRGBA first; the standard library would write
// them as 16-bit.
//
// The PNG header must say whether the image has an alpha channel. When the
// source cannot tell (pipeline.IsOpaque), the pixels are first collected in a
// pipeline.Spill, which is a temporary file when they do not fit in the memory
// budget, while their alpha is checked.
//
// It does not close src. If it returns an error, what was written to w is
// not a valid image and must be discarded.
func Encode(w io.Writer, src pipeline.Source, o *Options) error {
	b := src.Bounds()
	mw, mh := int64(b.Dx()), int64(b.Dy())
	if mw <= 0 || mh <= 0 || mw >= 1<<32 || mh >= 1<<32 {
		return FormatError("invalid image size: " + strconv.FormatInt(mw, 10) + "x" + strconv.FormatInt(mh, 10))
	}
	level := zlib.DefaultCompression
	if o != nil && o.Level != 0 {
		if o.Level < 1 || o.Level > 9 {
			return errors.New("goimgtools: PNG compression level must be 1 to 9")
		}
		level = o.Level
	}
	env := pipeline.EnvOf(src)
	first, err := src.Next()
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return err
	}

	e := &encoder{w: w, bounds: b, level: level, env: env}
	var (
		pal      color.Palette
		conv     *converter
		bits     = 8  // bits per channel
		hasAlpha bool // the pixel type can hold transparency
		knownOpq = pipeline.IsOpaque(src)
	)
	switch m := first.(type) {
	case *image.Gray:
		e.cb = cbG8
	case *image.Gray16:
		e.cb = cbG16
	case *image.Paletted:
		pal = m.Palette
		switch {
		case len(pal) <= 2:
			e.cb = cbP1
		case len(pal) <= 4:
			e.cb = cbP2
		case len(pal) <= 16:
			e.cb = cbP4
		default:
			e.cb = cbP8
		}
	case *image.RGBA, *image.NRGBA:
		hasAlpha = true
	case *image.RGBA64, *image.NRGBA64:
		hasAlpha, bits = true, 16
	case *image.YCbCr, *image.CMYK:
		hasAlpha, knownOpq = true, true
		conv = &converter{env: env}
	default:
		hasAlpha = true
		conv = &converter{env: env, nrgba: !knownOpq}
	}
	defer conv.release()

	// pull yields the strips: the first one already read, then the rest.
	pending := first
	pull := func() (image.Image, error) {
		m := pending
		pending = nil
		if m == nil {
			var err error
			if m, err = src.Next(); err != nil {
				return nil, err
			}
		}
		if conv != nil {
			return conv.convert(m)
		}
		if p, ok := m.(*image.Paletted); ok && len(p.Palette) > len(pal) {
			// The PLTE chunk is already written. Only a damaged PNG input
			// (pixels pointing past its palette) gets here.
			return nil, errors.New("goimgtools: palette grew while encoding")
		}
		return m, nil
	}

	if hasAlpha {
		opq := knownOpq
		if !knownOpq {
			// Whether there is transparency decides the header, and is only
			// known once every pixel has been seen: the strips are recorded
			// (in a temporary file beyond their memory share) while their
			// alpha is looked at, and then played back.
			scan := &alphaScan{pull: pull, bounds: b, model: src.ColorModel(), env: env, opaque: true}
			rec, err := pipeline.Record(scan)
			if err != nil {
				return err
			}
			defer rec.Close()
			play, err := rec.Source()
			if err != nil {
				return err
			}
			defer play.Close()
			opq, pull = scan.opaque, play.Next
		}
		switch {
		case bits == 8 && opq:
			e.cb = cbTC8
		case bits == 8:
			e.cb = cbTCA8
		case opq:
			e.cb = cbTC16
		default:
			e.cb = cbTCA16
		}
	}
	e.next = pull

	// The encoder keeps the current row under each of the five filters and
	// the previous row, plus a 32KB chunk buffer.
	rowBytes := int64(b.Dx())*8 + 1
	need := 6*rowBytes + 1<<15
	if err := env.Budget.Reserve(need, "png encoder rows"); err != nil {
		return err
	}
	defer env.Budget.Release(need)

	_, e.err = io.WriteString(w, pngHeader)
	e.writeIHDR()
	if o != nil {
		e.writeMeta(o.Exif, o.ICC)
		if o.Density > 0 {
			var b [9]byte
			ppm := density.PerMetre(o.Density)
			binary.BigEndian.PutUint32(b[0:], ppm)
			binary.BigEndian.PutUint32(b[4:], ppm)
			b[8] = 1 // the metre
			e.writeChunk(b[:], "pHYs")
		}
	}
	if pal != nil {
		e.writePLTEAndTRNS(pal)
	}
	e.writeIDATs()
	e.writeIEND()
	return e.err
}

// converter turns strips of a type PNG has no direct encoding for into RGBA
// (or NRGBA when the source may be transparent) strips.
type converter struct {
	env      *pipeline.Env
	nrgba    bool
	band     image.Image
	reserved int64
}

func (c *converter) convert(m image.Image) (image.Image, error) {
	r := m.Bounds()
	if c.band == nil || c.band.Bounds().Dy() < r.Dy() {
		c.release()
		rows := max(r.Dy(), 16)
		need := int64(r.Dx()) * 4 * int64(rows)
		if err := c.env.Budget.Reserve(need, "png conversion strip"); err != nil {
			return nil, err
		}
		c.reserved = need
		full := image.Rect(r.Min.X, 0, r.Max.X, rows)
		if c.nrgba {
			c.band = image.NewNRGBA(full)
		} else {
			c.band = image.NewRGBA(full)
		}
	}
	out := pipeline.Relabel(c.band, r)
	draw.Draw(out.(draw.Image), r, m, r.Min, draw.Src)
	return out, nil
}

func (c *converter) release() {
	if c == nil {
		return
	}
	if c.reserved != 0 {
		c.env.Budget.Release(c.reserved)
		c.reserved = 0
	}
	c.band = nil
}

// alphaScan hands strips on while noting whether any pixel is transparent.
type alphaScan struct {
	pull   func() (image.Image, error)
	bounds image.Rectangle
	model  color.Model
	env    *pipeline.Env
	opaque bool
}

func (a *alphaScan) Env() *pipeline.Env      { return a.env }
func (a *alphaScan) Bounds() image.Rectangle { return a.bounds }
func (a *alphaScan) ColorModel() color.Model { return a.model }
func (a *alphaScan) Close() error            { return nil }

func (a *alphaScan) Next() (image.Image, error) {
	m, err := a.pull()
	if err != nil {
		return nil, err
	}
	if !a.opaque {
		return m, nil
	}
	pix, stride, bpp, rect, ok := pipeline.Pix(m)
	r := m.Bounds()
	if !ok || (bpp != 4 && bpp != 8) {
		return nil, errors.New("goimgtools: png source changed pixel type")
	}
	for y := r.Min.Y; y < r.Max.Y && a.opaque; y++ {
		o := (y-rect.Min.Y)*stride + (r.Min.X-rect.Min.X)*bpp
		row := pix[o : o+r.Dx()*bpp]
		if bpp == 4 {
			for i := 3; i < len(row); i += 4 {
				if row[i] != 0xff {
					a.opaque = false
					break
				}
			}
		} else { // 16 bits per channel
			for i := 6; i < len(row); i += 8 {
				if row[i] != 0xff || row[i+1] != 0xff {
					a.opaque = false
					break
				}
			}
		}
	}
	return m, nil
}

// writeMeta writes the iCCP and eXIf chunks, which belong before PLTE and
// IDAT.
func (e *encoder) writeMeta(exif, icc []byte) {
	if len(icc) > 0 {
		var buf bytes.Buffer
		buf.WriteString("ICC Profile\x00\x00") // name, terminator, method 0
		zw := zlib.NewWriter(&buf)
		zw.Write(icc)
		zw.Close()
		e.writeChunk(buf.Bytes(), "iCCP")
	}
	if len(exif) > 0 {
		e.writeChunk(exif, "eXIf")
	}
}
