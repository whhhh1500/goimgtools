package tiffstream

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/internal/density"
	"github.com/whhhh1500/goimgtools/internal/jpegstream"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// goimgtools addition: writing JPEG compression (Compression 7, the
// "new-style" JPEG of TIFF Technical Note 2) as libtiff writes it for
// libvips and sharp: colour as YCbCr (Photometric 6, the JPEG's own) and
// grey as grey, every strip a JPEG stream of its own whose tables (the
// quantization and Huffman tables, which are the same in every strip) are
// in JPEGTables, and strips of a whole number of rows of blocks, so that
// the picture is the one a single JPEG of the image gives.

// DefaultJPEGQuality is the JPEG quality of Options.Quality 0, as sharp's.
const DefaultJPEGQuality = 80

// errJPEGAlpha is returned for transparent pixels, which JPEG cannot hold.
var errJPEGAlpha = errors.New("goimgtools: tiff: JPEG compression cannot keep transparency: flatten the image first")

// encodeJPEG writes src as a TIFF with JPEG compression.
func encodeJPEG(w io.Writer, src pipeline.Source, o *Options) error {
	b := src.Bounds()
	dx, dy := b.Dx(), b.Dy()
	if dx <= 0 || dy <= 0 {
		return errZeroSize
	}
	if dx > 65500 || dy > 65500 {
		return errTooLarge // what a JPEG stream can hold
	}
	q := o.Quality
	if q == 0 {
		q = DefaultJPEGQuality
	}
	if q < 1 || q > 100 {
		return errors.New("goimgtools: tiff: JPEG quality must be 1 to 100")
	}
	// As libvips: the colour at half the width and height below quality
	// 90, in full from there.
	sub, hs, vs := jpegstream.Subsample420, 2, 2
	if q >= 90 {
		sub, hs, vs = jpegstream.Subsample444, 1, 1
	}
	env := pipeline.EnvOf(src)
	rd := &stripReader{src: src, b: b, y: b.Min.Y}
	first, err := rd.next()
	if err != nil {
		return err
	}
	gray := false
	switch first.(type) {
	case *image.Gray, *image.Gray16:
		gray = true
		hs, vs = 1, 1
	}
	// Strips of about stripBytes of samples, whole rows of blocks.
	unit := 8 * vs
	rowsPerStrip := dy
	if per := stripBytes / (int64(dx) * 3); int64(dy) > per {
		rowsPerStrip = int(max(1, per/int64(unit))) * unit
	}
	sp := pipeline.NewSpill(env)
	defer sp.Close()
	sp.Expect(int64(dx) * int64(dy) / 4)
	var tables []byte
	var offsets, counts []uint32
	feed := &rowFeed{rd: rd, cur: first, gray: gray}
	var held int64
	defer func() { env.Budget.Release(held) }()
	var buf bytes.Buffer // a strip's JPEG stream, its room kept for the next
	for y := 0; y < dy; y += rowsPerStrip {
		n := min(rowsPerStrip, dy-y)
		feed.start(b.Min.Y+y, n)
		buf.Reset()
		err := jpegstream.Encode(&buf, &stripSource{feed: feed, env: env, rect: image.Rect(b.Min.X, b.Min.Y+y, b.Max.X, b.Min.Y+y+n)},
			&jpegstream.Options{Quality: q, Subsampling: sub})
		if err == nil && feed.err != nil {
			err = feed.err
		}
		if err != nil {
			return err
		}
		if int64(buf.Cap()) > held {
			if err := env.Budget.Reserve(int64(buf.Cap())-held, "tiff jpeg strip"); err != nil {
				return err
			}
			held = int64(buf.Cap())
		}
		t, head, scan, err := splitTables(buf.Bytes())
		if err != nil {
			return err
		}
		if tables == nil {
			tables = t
		} else if !bytes.Equal(tables, t) {
			return errors.New("goimgtools: tiff: the JPEG strips have different tables")
		}
		offsets = append(offsets, uint32(sp.Size()))
		counts = append(counts, uint32(len(head)+len(scan)))
		if _, err := sp.Write(head); err != nil {
			return err
		}
		if _, err := sp.Write(scan); err != nil {
			return err
		}
		if sp.Size() > math.MaxUint32-maxIFD-int64(len(tables)+len(o.ICC)+len(o.Xmp)+len(o.Iptc))-8*int64(len(offsets)) {
			return errTooLarge
		}
	}
	if feed.err != nil {
		return feed.err
	}
	if _, err := rd.next(); err != io.EOF {
		if err == nil {
			err = errStrip
		}
		return err
	}

	imageLen := sp.Size()
	var hdr [8]byte
	copy(hdr[:], leHeader)
	enc.PutUint32(hdr[4:], uint32(imageLen+8))
	if _, err := w.Write(hdr[:]); err != nil {
		return err
	}
	if err := env.Budget.Reserve(copyBuf, "tiff copy buffer"); err != nil {
		return err
	}
	defer env.Budget.Release(copyBuf)
	cbuf := make([]byte, copyBuf)
	for off := int64(0); off < imageLen; {
		k := int(min(int64(len(cbuf)), imageLen-off))
		if _, err := sp.ReadAt(cbuf[:k], off); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
		if _, err := w.Write(cbuf[:k]); err != nil {
			return err
		}
		off += int64(k)
	}
	for i := range offsets {
		offsets[i] += 8
	}
	res := []uint32{72, 1}
	if o.Density > 0 {
		num, den := density.Rational(o.Density)
		res = []uint32{num, den}
	}
	sizeType := func(v int) int {
		if v > math.MaxUint16 {
			return dtLong
		}
		return dtShort
	}
	photometric, spp, bps := uint32(pYCbCr), uint32(3), []uint32{8, 8, 8}
	if gray {
		photometric, spp, bps = pBlackIsZero, 1, []uint32{8}
	}
	ifd := []ifdEnt{
		{tag: tImageWidth, datatype: sizeType(dx), data: []uint32{uint32(dx)}},
		{tag: tImageLength, datatype: sizeType(dy), data: []uint32{uint32(dy)}},
		{tag: tBitsPerSample, datatype: dtShort, data: bps},
		{tag: tCompression, datatype: dtShort, data: []uint32{cJPEG}},
		{tag: tPhotometricInterpretation, datatype: dtShort, data: []uint32{photometric}},
		{tag: tStripOffsets, datatype: dtLong, data: offsets},
		{tag: tSamplesPerPixel, datatype: dtShort, data: []uint32{spp}},
		{tag: tRowsPerStrip, datatype: sizeType(rowsPerStrip), data: []uint32{uint32(rowsPerStrip)}},
		{tag: tStripByteCounts, datatype: dtLong, data: counts},
		{tag: tXResolution, datatype: dtRational, data: res},
		{tag: tYResolution, datatype: dtRational, data: res},
		{tag: tResolutionUnit, datatype: dtShort, data: []uint32{resPerInch}},
		{tag: tJPEGTables, datatype: dtUndefined, raw: tables},
	}
	if !gray {
		ifd = append(ifd,
			ifdEnt{tag: tYCbCrSubSampling, datatype: dtShort, data: []uint32{uint32(hs), uint32(vs)}},
			ifdEnt{tag: tReferenceBlackWhite, datatype: dtRational, data: []uint32{0, 1, 255, 1, 128, 1, 255, 1, 128, 1, 255, 1}})
	}
	if len(o.ICC) > 0 && len(o.ICC) <= pipeline.MaxMetaBytes {
		ifd = append(ifd, ifdEnt{tag: tICCProfile, datatype: dtUndefined, raw: o.ICC})
	}
	if len(o.Xmp) > 0 && len(o.Xmp) <= pipeline.MaxMetaBytes {
		ifd = append(ifd, ifdEnt{tag: tXMP, datatype: dtByte, raw: o.Xmp})
	}
	if len(o.Iptc) > 0 && len(o.Iptc) <= pipeline.MaxMetaBytes {
		ifd = append(ifd, ifdEnt{tag: tIPTC, datatype: dtUndefined, raw: o.Iptc})
	}
	ifd = append(ifd, exifEntries(o.Exif)...)
	return writeIFD(w, imageLen+8, ifd)
}

// splitTables cuts a JPEG stream into its tables (SOI, the DQT and DHT
// segments, EOI), which go into JPEGTables, and the stream without them
// or any other segment but the frame, the scan and what they need: its
// head (SOI, the frame), copied, and its scan to the EOI, a part of s.
func splitTables(s []byte) (tables, head, scan []byte, err error) {
	bad := errors.New("goimgtools: tiff: unexpected JPEG stream")
	if len(s) < 4 || s[0] != 0xff || s[1] != 0xd8 {
		return nil, nil, nil, bad
	}
	tables = []byte{0xff, 0xd8}
	head = []byte{0xff, 0xd8}
	for i := 2; ; {
		if i+4 > len(s) || s[i] != 0xff {
			return nil, nil, nil, bad
		}
		m := s[i+1]
		n := int(s[i+2])<<8 | int(s[i+3])
		if i+2+n > len(s) {
			return nil, nil, nil, bad
		}
		seg := s[i : i+2+n]
		switch {
		case m == 0xdb || m == 0xc4:
			tables = append(tables, seg...)
		case m >= 0xe0 && m <= 0xef:
			// APPn: nothing of the image (no JFIF here, and no metadata
			// was given to the encoder).
		case m == 0xda:
			// The scan and everything after it, to the EOI, left where
			// it is.
			return append(tables, 0xff, 0xd9), head, s[i:], nil
		default:
			head = append(head, seg...)
		}
		i += 2 + n
	}
}

// rowFeed hands the rows of the image to the strips one after the other,
// cutting the source's strips where a JPEG strip ends.
type rowFeed struct {
	rd    *stripReader
	cur   image.Image // rows not handed out yet, from cur.Bounds().Min.Y
	gray  bool
	until int // the JPEG strip being written ends before this row
	pos   int // the next row to hand out
	err   error
}

func (f *rowFeed) start(y, n int) { f.pos, f.until = y, y+n }

// next returns the next rows of the JPEG strip, nil at its end.
func (f *rowFeed) next() image.Image {
	for f.err == nil && f.pos < f.until {
		if f.cur == nil {
			m, err := f.rd.next()
			if err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				f.err = err
				return nil
			}
			f.cur = m
		}
		cb := f.cur.Bounds()
		if cb.Min.Y >= f.until {
			return nil
		}
		if cb.Empty() {
			f.cur = nil
			continue
		}
		if err := opaqueRows(f.cur); err != nil {
			f.err = err
			return nil
		}
		if cb.Max.Y <= f.until {
			m := f.cur
			f.cur, f.pos = nil, cb.Max.Y
			return m
		}
		head := subImage(f.cur, image.Rect(cb.Min.X, cb.Min.Y, cb.Max.X, f.until))
		f.cur, f.pos = subImage(f.cur, image.Rect(cb.Min.X, f.until, cb.Max.X, cb.Max.Y)), f.until
		return head
	}
	return nil
}

// subImage is the part r of a strip.
func subImage(m image.Image, r image.Rectangle) image.Image {
	if s, ok := m.(interface {
		SubImage(image.Rectangle) image.Image
	}); ok {
		return s.SubImage(r)
	}
	return &cropped{m, r}
}

type cropped struct {
	image.Image
	r image.Rectangle
}

func (c *cropped) Bounds() image.Rectangle { return c.r }

// opaqueRows reports the first transparent pixel of a strip as an error.
func opaqueRows(m image.Image) error {
	switch p := m.(type) {
	case *image.Gray, *image.Gray16, *image.YCbCr, *image.CMYK:
		return nil
	case *image.NRGBA:
		return alphaRows(p.Pix, p.Stride, 4, 3, p.Rect)
	case *image.RGBA:
		return alphaRows(p.Pix, p.Stride, 4, 3, p.Rect)
	case *image.NRGBA64:
		return alphaRows(p.Pix, p.Stride, 8, 6, p.Rect)
	case *image.RGBA64:
		return alphaRows(p.Pix, p.Stride, 8, 6, p.Rect)
	case *image.Paletted:
		for _, c := range p.Palette {
			if _, _, _, a := c.RGBA(); a != 0xffff {
				// A transparent entry: the pixels decide.
				for y := p.Rect.Min.Y; y < p.Rect.Max.Y; y++ {
					for x := p.Rect.Min.X; x < p.Rect.Max.X; x++ {
						if _, _, _, a := p.At(x, y).RGBA(); a != 0xffff {
							return errJPEGAlpha
						}
					}
				}
				return nil
			}
		}
		return nil
	}
	b := m.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := m.At(x, y).RGBA(); a != 0xffff {
				return errJPEGAlpha
			}
		}
	}
	return nil
}

// alphaRows reports a byte of alpha (the first of it for 16 bits) below
// its largest value.
func alphaRows(pix []byte, stride, bpp, at int, r image.Rectangle) error {
	w := r.Dx() * bpp
	for y := 0; y < r.Dy(); y++ {
		row := pix[y*stride:][:w]
		for i := at; i < w; i += bpp {
			if row[i] != 0xff || bpp == 8 && row[i+1] != 0xff {
				return errJPEGAlpha
			}
		}
	}
	return nil
}

// stripSource gives the rows of one JPEG strip to the JPEG encoder.
type stripSource struct {
	feed *rowFeed
	env  *pipeline.Env
	rect image.Rectangle
}

func (s *stripSource) Bounds() image.Rectangle { return s.rect }
func (s *stripSource) ColorModel() color.Model {
	if s.feed.gray {
		return color.GrayModel
	}
	return color.RGBAModel
}
func (s *stripSource) Env() *pipeline.Env { return s.env }
func (s *stripSource) Opaque() bool       { return true }
func (s *stripSource) Close() error       { return nil }
func (s *stripSource) Next() (image.Image, error) {
	if m := s.feed.next(); m != nil {
		return m, nil
	}
	if s.feed.err != nil {
		return nil, s.feed.err
	}
	return nil, io.EOF
}
