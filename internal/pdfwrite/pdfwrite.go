// Package pdfwrite writes an image as a PDF document of one page, as large
// as the image is at its resolution (72 dots per inch when it records
// none): losslessly (Flate, rows filtered as PNG filters them) or as a JPEG
// (DCT), its transparency as a soft mask, its colour profile with it. The
// document is written as the image is read; only the soft mask waits in
// working storage until the colours are done.
package pdfwrite

import (
	"bufio"
	"compress/zlib"
	"errors"
	"fmt"
	"image"
	"image/color"
	"io"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the settings of a document.
type Options struct {
	// Quality, 1 to 100, stores the picture as a JPEG of that quality; 0
	// stores it without loss.
	Quality int
	// Density is the image's resolution in pixels per inch; 0 is 72.
	Density float64
	// ICC is the colour profile of the image's colours.
	ICC []byte
	// Subsampling is the chroma subsampling of a JPEG picture (jpegstream's).
	Subsampling int
}

// counter counts what it writes.
type counter struct {
	w   *bufio.Writer
	n   int64
	err error
}

func (c *counter) Write(p []byte) (int, error) {
	if c.err != nil {
		return 0, c.err
	}
	n, err := c.w.Write(p)
	c.n += int64(n)
	c.err = err
	return n, err
}

func (c *counter) printf(format string, a ...any) { fmt.Fprintf(c, format, a...) }

// num writes a number as PDF wants it: no exponent.
func num(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// noClose keeps the caller's source open.
type noClose struct{ pipeline.Source }

func (noClose) Close() error { return nil }

// Encode writes src as a PDF document of one page. src is not closed.
func Encode(w io.Writer, src pipeline.Source, o *Options) error {
	d := NewDocument(w)
	if err := d.AddPage(src, o); err != nil {
		return err
	}
	return d.Close()
}

// Document is a PDF document written a page at a time.
type Document struct {
	c       *counter
	offsets []int64
	kids    []int
	next    int // the next object's number
}

// The catalog and the tree of pages are objects 1 and 2; the tree is
// written last, when its pages are known.
const (
	catalog = 1
	pages   = 2
)

// NewDocument starts a document on w.
func NewDocument(w io.Writer) *Document {
	d := &Document{c: &counter{w: bufio.NewWriterSize(w, 64<<10)}, next: 3}
	d.c.printf("%%PDF-1.4\n%%\xe2\xe3\xcf\xd3\n")
	d.obj(catalog)
	d.c.printf("<< /Type /Catalog /Pages %d 0 R >>\nendobj\n", pages)
	return d
}

func (d *Document) obj(n int) {
	for len(d.offsets) < n {
		d.offsets = append(d.offsets, 0)
	}
	d.offsets[n-1] = d.c.n
	d.c.printf("%d 0 obj\n", n)
}

func (d *Document) alloc() int { d.next++; return d.next - 1 }

// AddPage writes a page of src, as large as it is at its resolution. src
// is not closed.
func (d *Document) AddPage(src pipeline.Source, o *Options) error {
	if o == nil {
		o = &Options{}
	}
	b := src.Bounds()
	if b.Empty() {
		return errors.New("goimgtools: pdf: an empty image")
	}
	if o.Quality < 0 || o.Quality > 100 {
		return fmt.Errorf("goimgtools: pdf: quality must be 0 to 100, got %d", o.Quality)
	}
	env := pipeline.EnvOf(src)
	m := src.ColorModel()
	grey := m == color.GrayModel || m == color.Gray16Model
	alpha := !pipeline.IsOpaque(src) && !grey
	// The compressors: about a megabyte each (compress/flate's tables).
	zlibs := int64(1)
	if alpha {
		zlibs++
	}
	if err := env.Budget.Reserve(zlibs<<20, "pdf compressor"); err != nil {
		return err
	}
	defer env.Budget.Release(zlibs << 20)
	c := d.c
	page, contents, picture, pictureLen := d.alloc(), d.alloc(), d.alloc(), d.alloc()
	var mask, maskLen, profile int
	if alpha {
		mask, maskLen = d.alloc(), d.alloc()
	}
	if len(o.ICC) > 0 {
		profile = d.alloc()
	}
	d.kids = append(d.kids, page)
	dpi := o.Density
	if !(dpi > 0) {
		dpi = 72
	}
	pw, ph := float64(b.Dx())*72/dpi, float64(b.Dy())*72/dpi
	d.obj(page)
	c.printf("<< /Type /Page /Parent %d 0 R /MediaBox [0 0 %s %s] /Resources << /XObject << /Im0 %d 0 R >> /ProcSet [/PDF /ImageC /ImageB] >> /Contents %d 0 R >>\nendobj\n",
		pages, num(pw), num(ph), picture, contents)
	draw := fmt.Sprintf("q %s 0 0 %s 0 0 cm /Im0 Do Q\n", num(pw), num(ph))
	d.obj(contents)
	c.printf("<< /Length %d >>\nstream\n%sendstream\nendobj\n", len(draw), draw)
	channels := 3
	space := "/DeviceRGB"
	if grey {
		channels, space = 1, "/DeviceGray"
	}
	if len(o.ICC) > 0 {
		d.obj(profile)
		var zb bufferWriter
		zw := zlib.NewWriter(&zb)
		zw.Write(o.ICC)
		zw.Close()
		c.printf("<< /N %d /Alternate %s /Filter /FlateDecode /Length %d >>\nstream\n", channels, space, len(zb.b))
		c.Write(zb.b)
		c.printf("\nendstream\nendobj\n")
		space = fmt.Sprintf("[/ICCBased %d 0 R]", profile)
	}
	// The picture.
	d.obj(picture)
	c.printf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace %s /BitsPerComponent 8 ", b.Dx(), b.Dy(), space)
	if o.Quality > 0 {
		c.printf("/Filter /DCTDecode ")
	} else {
		c.printf("/Filter /FlateDecode /DecodeParms << /Predictor 15 /Colors %d /BitsPerComponent 8 /Columns %d >> ", channels, b.Dx())
	}
	if alpha {
		c.printf("/SMask %d 0 R ", mask)
	}
	c.printf("/Length %d 0 R >>\nstream\n", pictureLen)
	start := c.n
	// The soft mask is made as the colours are read, kept aside.
	var sp *pipeline.Spill
	var maskZ *zlib.Writer
	var maskRows *rowFilter
	if alpha {
		sp = pipeline.NewSpill(env)
		defer sp.Close()
		maskZ = zlib.NewWriter(sp)
		maskRows = newRowFilter(b.Dx(), 1)
	}
	var err error
	if o.Quality > 0 {
		err = encodeJPEG(c, src, o, grey, maskZ, maskRows)
	} else {
		err = encodeFlate(c, src, grey, maskZ, maskRows)
	}
	if err != nil {
		return err
	}
	if c.err != nil {
		return c.err
	}
	length := c.n - start
	c.printf("\nendstream\nendobj\n")
	d.obj(pictureLen)
	c.printf("%d\nendobj\n", length)
	if alpha {
		if err := maskZ.Close(); err != nil {
			return err
		}
		d.obj(mask)
		c.printf("<< /Type /XObject /Subtype /Image /Width %d /Height %d /ColorSpace /DeviceGray /BitsPerComponent 8 /Filter /FlateDecode /DecodeParms << /Predictor 15 /Colors 1 /BitsPerComponent 8 /Columns %d >> /Length %d 0 R >>\nstream\n",
			b.Dx(), b.Dy(), b.Dx(), maskLen)
		start := c.n
		if _, err := io.Copy(c, io.NewSectionReader(sp, 0, sp.Size())); err != nil {
			return err
		}
		length := c.n - start
		c.printf("\nendstream\nendobj\n")
		d.obj(maskLen)
		c.printf("%d\nendobj\n", length)
	}
	return c.err
}

// Close writes the tree of pages and the cross-reference table.
func (d *Document) Close() error {
	c := d.c
	if len(d.kids) == 0 {
		return errors.New("goimgtools: pdf: a document of no page")
	}
	d.obj(pages)
	c.printf("<< /Type /Pages /Kids [")
	for i, k := range d.kids {
		if i > 0 {
			c.printf(" ")
		}
		c.printf("%d 0 R", k)
	}
	c.printf("] /Count %d >>\nendobj\n", len(d.kids))
	// The cross-reference table: every object, in order.
	xref := c.n
	c.printf("xref\n0 %d\n0000000000 65535 f \n", len(d.offsets)+1)
	for _, off := range d.offsets {
		if off == 0 {
			c.printf("0000000000 65535 f \n")
			continue
		}
		c.printf("%010d 00000 n \n", off)
	}
	c.printf("trailer\n<< /Size %d /Root %d 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(d.offsets)+1, catalog, xref)
	if c.err != nil {
		return c.err
	}
	return c.w.Flush()
}

type bufferWriter struct{ b []byte }

func (w *bufferWriter) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }

// rowFilter filters rows as PNG's predictor 15 lets each choose: the
// filter whose output is least in sum of absolute values.
type rowFilter struct {
	bpp        int
	prev, out  []byte
	candidates [5][]byte
}

func newRowFilter(w, bpp int) *rowFilter {
	f := &rowFilter{bpp: bpp, prev: make([]byte, w*bpp), out: make([]byte, 1+w*bpp)}
	for i := range f.candidates {
		f.candidates[i] = make([]byte, w*bpp)
	}
	return f
}

// filter returns the row with its filter byte first; cur is kept as the
// previous row.
func (f *rowFilter) filter(cur []byte) []byte {
	bpp, prev := f.bpp, f.prev
	best, bestSum := 0, -1
	for t := 0; t < 5; t++ {
		d := f.candidates[t]
		sum := 0
		for i := range cur {
			var a, b, c int
			if i >= bpp {
				a = int(cur[i-bpp])
				c = int(prev[i-bpp])
			}
			b = int(prev[i])
			var p int
			switch t {
			case 1:
				p = a
			case 2:
				p = b
			case 3:
				p = (a + b) / 2
			case 4:
				pa, pb, pc := abs(b-c), abs(a-c), abs(a+b-2*c)
				switch {
				case pa <= pb && pa <= pc:
					p = a
				case pb <= pc:
					p = b
				default:
					p = c
				}
			}
			v := cur[i] - uint8(p)
			d[i] = v
			sum += abs(int(int8(v)))
		}
		if bestSum < 0 || sum < bestSum {
			best, bestSum = t, sum
		}
	}
	f.out[0] = byte(best)
	copy(f.out[1:], f.candidates[best])
	copy(f.prev, cur)
	return f.out
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

// rowsOf converts strips into rows of 8-bit RGB (or grey) and, when wanted,
// alpha.
func rowsOf(src pipeline.Source, grey bool, fn func(colour, alpha []byte) error) error {
	b := src.Bounds()
	w := b.Dx()
	var s pipeline.Source = noClose{src}
	if !grey {
		s = pipeline.Map(noClose{src}, func(*image.NRGBA) error { return nil })
		defer s.Close()
	}
	ch := 3
	if grey {
		ch = 1
	}
	env := pipeline.EnvOf(src)
	need := int64(w) * int64(ch+1)
	if err := env.Budget.Reserve(need, "pdf row"); err != nil {
		return err
	}
	defer env.Budget.Release(need)
	row, arow := make([]byte, w*ch), make([]byte, w)
	y := b.Min.Y
	for {
		m, err := s.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		r := m.Bounds()
		for yy := r.Min.Y; yy < r.Max.Y; yy++ {
			switch p := m.(type) {
			case *image.NRGBA:
				px := p.Pix[p.PixOffset(r.Min.X, yy):]
				for x := 0; x < w; x++ {
					row[3*x], row[3*x+1], row[3*x+2], arow[x] = px[4*x], px[4*x+1], px[4*x+2], px[4*x+3]
				}
			case *image.Gray:
				copy(row, p.Pix[p.PixOffset(r.Min.X, yy):][:w])
			default:
				for x := 0; x < w; x++ {
					row[x] = color.GrayModel.Convert(m.At(r.Min.X+x, yy)).(color.Gray).Y
				}
			}
			if err := fn(row, arow); err != nil {
				return err
			}
			y++
		}
	}
	if y != b.Max.Y {
		return io.ErrUnexpectedEOF
	}
	return nil
}

func encodeFlate(c *counter, src pipeline.Source, grey bool, maskZ *zlib.Writer, maskRows *rowFilter) error {
	ch := 3
	if grey {
		ch = 1
	}
	zw := zlib.NewWriter(c)
	rf := newRowFilter(src.Bounds().Dx(), ch)
	err := rowsOf(src, grey, func(colour, alpha []byte) error {
		if _, err := zw.Write(rf.filter(colour)); err != nil {
			return err
		}
		if maskZ != nil {
			if _, err := maskZ.Write(maskRows.filter(alpha)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return zw.Close()
}

// alphaTap passes strips (its own, mapped) on to the JPEG encoder, their
// alpha kept aside.
type alphaTap struct {
	pipeline.Source
	maskZ    *zlib.Writer
	maskRows *rowFilter
	arow     []byte
	err      error
}

func (a *alphaTap) Close() error { return nil }

func (a *alphaTap) Next() (image.Image, error) {
	m, err := a.Source.Next()
	if err != nil || a.maskZ == nil {
		return m, err
	}
	n := m.(*image.NRGBA)
	r := n.Rect
	for y := r.Min.Y; y < r.Max.Y; y++ {
		px := n.Pix[n.PixOffset(r.Min.X, y):]
		for x := range a.arow {
			// The mask takes the alpha; the colours go on unmultiplied by it.
			a.arow[x], px[4*x+3] = px[4*x+3], 255
		}
		if _, err := a.maskZ.Write(a.maskRows.filter(a.arow)); err != nil {
			return nil, err
		}
	}
	return m, nil
}

func encodeJPEG(c *counter, src pipeline.Source, o *Options, grey bool, maskZ *zlib.Writer, maskRows *rowFilter) error {
	var s pipeline.Source = noClose{src}
	if maskZ != nil {
		mapped := pipeline.Map(noClose{src}, func(*image.NRGBA) error { return nil })
		defer mapped.Close()
		s = &alphaTap{Source: mapped, maskZ: maskZ, maskRows: maskRows, arow: make([]byte, src.Bounds().Dx())}
	}
	return jpegstream.Encode(c, s, &jpegstream.Options{Quality: o.Quality, Subsampling: o.Subsampling})
}
