// Copyright 2012 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package tiffstream

import (
	"compress/zlib"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"io"
	"math"
	"reflect"
	"sort"

	"github.com/whhhh1500/goimgtools/internal/density"
	"github.com/whhhh1500/goimgtools/internal/tiffstream/lzw"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// The TIFF format allows to choose the order of the different elements freely.
// The basic structure of a TIFF file written by this package is:
//
//   1. Header (8 bytes).
//   2. Image data.
//   3. Image File Directory (IFD).
//   4. "Pointer area" for larger entries in the IFD.

// We only write little-endian TIFF files.
var enc = binary.LittleEndian

// An ifdEnt is a single entry in an Image File Directory.
// A value of type dtRational is composed of two 32-bit values,
// thus data contains two uints (numerator and denominator) for a single number.
type ifdEnt struct {
	tag      int
	datatype int
	data     []uint32
	raw      []byte // the bytes of an entry given as bytes (UNDEFINED, BYTE)
}

func (e ifdEnt) putData(p []byte) {
	if e.raw != nil {
		copy(p, e.raw)
		return
	}
	for _, d := range e.data {
		switch e.datatype {
		case dtByte, dtASCII:
			p[0] = byte(d)
			p = p[1:]
		case dtShort:
			enc.PutUint16(p, uint16(d))
			p = p[2:]
		case dtLong, dtRational:
			enc.PutUint32(p, uint32(d))
			p = p[4:]
		}
	}
}

type byTag []ifdEnt

func (d byTag) Len() int           { return len(d) }
func (d byTag) Less(i, j int) bool { return d[i].tag < d[j].tag }
func (d byTag) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }

func writeIFD(w io.Writer, ifdOffset int64, d []ifdEnt) error {
	var buf [ifdLen]byte
	// Make space for "pointer area" containing IFD entry data
	// longer than 4 bytes.
	parea := make([]byte, 1024)
	pstart := ifdOffset + int64(ifdLen*len(d)) + 6
	var o int // Current offset in parea.

	// The IFD has to be written with the tags in ascending order.
	sort.Sort(byTag(d))

	// Write the number of entries in this IFD.
	if err := binary.Write(w, enc, uint16(len(d))); err != nil {
		return err
	}
	for _, ent := range d {
		enc.PutUint16(buf[0:2], uint16(ent.tag))
		enc.PutUint16(buf[2:4], uint16(ent.datatype))
		count := uint32(len(ent.data))
		if ent.datatype == dtRational {
			count /= 2
		}
		datalen := 0
		if ent.raw != nil {
			count, datalen = uint32(len(ent.raw)), len(ent.raw)
		} else {
			datalen = int(count * lengths[ent.datatype])
		}
		enc.PutUint32(buf[4:8], count)
		if datalen <= 4 {
			ent.putData(buf[8:12])
		} else {
			if (o + datalen) > len(parea) {
				newlen := len(parea) + 1024
				for (o + datalen) > newlen {
					newlen += 1024
				}
				newarea := make([]byte, newlen)
				copy(newarea, parea)
				parea = newarea
			}
			ent.putData(parea[o : o+datalen])
			enc.PutUint32(buf[8:12], uint32(pstart+int64(o)))
			o += datalen
		}
		if _, err := w.Write(buf[:]); err != nil {
			return err
		}
	}
	// The IFD ends with the offset of the next IFD in the file,
	// or zero if it is the last one (page 14).
	if err := binary.Write(w, enc, uint32(0)); err != nil {
		return err
	}
	_, err := w.Write(parea[:o])
	return err
}

// Options are the encoding parameters.
type Options struct {
	// Compression is the type of compression used: Uncompressed or Deflate.
	Compression CompressionType
	// Predictor stores each sample of a compressed image as its difference
	// from the same sample of the pixel before (horizontal differencing,
	// Predictor 2), which compresses photographs better. It applies to LZW
	// and Deflate, not to paletted images.
	Predictor bool
	// ICC is a colour profile to embed (tag 34675), at most
	// pipeline.MaxMetaBytes.
	ICC []byte
	// Density, when above 0, is the resolution to record, in pixels per
	// inch; otherwise 72, as the reference writes.
	Density float64
	// Xmp is an XMP packet to embed (tag 700), at most
	// pipeline.MaxMetaBytes.
	Xmp []byte
}

var (
	errZeroSize = errors.New("tiff: zero-size image")
	errTooLarge = errors.New("tiff: image too large")
	errStrip    = errors.New("tiff: source strip out of place")
	errType     = errors.New("tiff: source changed pixel type")
)

const (
	// encChunk is how many bytes of converted rows are handed to the output
	// at a time, and the size of the buffer that copies the spill to it.
	encChunk = 256 << 10
	copyBuf  = 64 << 10
	// stripBytes is about how many bytes of pixel data a strip of a
	// compressed image holds: compressed images up to this size are one
	// strip, as x/image writes them; larger ones are cut into strips of
	// this size, each compressed on its own, as libtiff writes them (libvips
	// refuses a strip whose compressed data exceeds 50MB).
	stripBytes = 1 << 20
	// maxIFD bounds the directory and its pointer area (a 768-entry color
	// map is the bulk of it).
	maxIFD = 2048
)

// stripReader pulls the strips of a source and checks that they are where
// the Source contract says they are, so the row loops cannot run off a
// buffer.
type stripReader struct {
	src pipeline.Source
	b   image.Rectangle
	y   int
	typ reflect.Type
}

// next returns the next strip, io.EOF after the last row, and
// io.ErrUnexpectedEOF when the source ends before that.
func (r *stripReader) next() (image.Image, error) {
	m, err := r.src.Next()
	if err != nil {
		if err == io.EOF && r.y < r.b.Max.Y {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	if m == nil {
		return nil, errStrip
	}
	sb := m.Bounds()
	if sb.Min.X != r.b.Min.X || sb.Max.X != r.b.Max.X || sb.Min.Y != r.y || sb.Max.Y < sb.Min.Y || sb.Max.Y > r.b.Max.Y {
		return nil, errStrip
	}
	if t := reflect.TypeOf(m); r.typ == nil {
		r.typ = t
	} else if t != r.typ {
		return nil, errType
	}
	if pix, stride, bpp := encPix(m); bpp != 0 && sb.Dy() > 0 {
		if stride < sb.Dx()*bpp || len(pix) < (sb.Dy()-1)*stride+sb.Dx()*bpp {
			return nil, errStrip
		}
	}
	r.y = sb.Max.Y
	return m, nil
}

// encPix returns the pixel buffer of the strip types the encoder has a
// dedicated path for; bpp is zero for the others.
func encPix(m image.Image) (pix []uint8, stride, bpp int) {
	switch p := m.(type) {
	case *image.Paletted:
		return p.Pix, p.Stride, 1
	case *image.Gray:
		return p.Pix, p.Stride, 1
	case *image.Gray16:
		return p.Pix, p.Stride, 2
	case *image.RGBA:
		return p.Pix, p.Stride, 4
	case *image.NRGBA:
		return p.Pix, p.Stride, 4
	case *image.RGBA64:
		return p.Pix, p.Stride, 8
	case *image.NRGBA64:
		return p.Pix, p.Stride, 8
	}
	return nil, 0, 0
}

// swap16 copies src to dst turning big-endian 16-bit samples (the order of
// Gray16 and RGBA64 pixel buffers) into little-endian ones.
func swap16(dst, src []byte) {
	for i := 0; i+1 < len(src); i += 2 {
		dst[i+0] = src[i+1]
		dst[i+1] = src[i+0]
	}
}

// genericRow writes row y of m as 8-bit RGBA, the way the reference
// encoder's At() loop does. RGBA64At is by contract the same value as
// At().RGBA() without the interface allocation.
func genericRow(buf []byte, m image.Image, y int) {
	b := m.Bounds()
	off := 0
	if rm, ok := m.(image.RGBA64Image); ok {
		for x := b.Min.X; x < b.Max.X; x++ {
			c := rm.RGBA64At(x, y)
			buf[off+0] = uint8(c.R >> 8)
			buf[off+1] = uint8(c.G >> 8)
			buf[off+2] = uint8(c.B >> 8)
			buf[off+3] = uint8(c.A >> 8)
			off += 4
		}
		return
	}
	for x := b.Min.X; x < b.Max.X; x++ {
		r, g, b, a := m.At(x, y).RGBA()
		buf[off+0] = uint8(r >> 8)
		buf[off+1] = uint8(g >> 8)
		buf[off+2] = uint8(b >> 8)
		buf[off+3] = uint8(a >> 8)
		off += 4
	}
}

// writePix writes the internal byte array of an image to w. It is less general
// but much faster then encode. writePix is used when pix directly
// corresponds to one of the TIFF image types.
func writePix(w io.Writer, pix []byte, nrows, length, stride int) error {
	if length == stride {
		_, err := w.Write(pix[:nrows*length])
		return err
	}
	for ; nrows > 0; nrows-- {
		if _, err := w.Write(pix[:length]); err != nil {
			return err
		}
		if nrows > 1 {
			pix = pix[stride:]
		}
	}
	return nil
}

// Encode writes src to w as a TIFF. o determines the options used for
// encoding, such as the compression type. If o is nil, an uncompressed
// image is written.
//
// It is the golang.org/x/image/tiff encoder reading strips, and the bytes
// are the ones tiff.Encode writes for the same image: Paletted, Gray,
// Gray16, RGBA, NRGBA, RGBA64 and NRGBA64 strips keep their pixel format,
// every other pixel type is written as 8-bit RGBA through At().RGBA(). (The
// one difference: an image wider or taller than 65535 gets its size written
// as a LONG, where the reference truncates it to a SHORT.)
//
// The file is a single strip of pixel data followed by the directory, with
// the directory's offset in the header. Uncompressed rows go straight to w.
// Deflate output has to be complete before its size, and so that offset, is
// known; it is collected in a pipeline.Spill (a temporary file when it does not
// fit in the memory budget) and copied to w afterwards.
//
// It does not close src. If it returns an error, what was written to w is
// not a valid image and must be discarded.
func Encode(w io.Writer, src pipeline.Source, o *Options) error {
	b := src.Bounds()
	dx, dy := b.Dx(), b.Dy()
	if dx <= 0 || dy <= 0 {
		return errZeroSize
	}
	compression := uint32(cNone)
	var icc, xmp []byte
	res := []uint32{72, 1}
	if o != nil && o.Density > 0 {
		num, den := density.Rational(o.Density)
		res = []uint32{num, den}
	}
	if o != nil {
		compression = o.Compression.specValue()
		if len(o.ICC) <= pipeline.MaxMetaBytes {
			icc = o.ICC
		}
		if len(o.Xmp) <= pipeline.MaxMetaBytes {
			xmp = o.Xmp
		}
	}
	if compression != cNone && compression != cDeflate && compression != cLZW {
		return errors.New("tiff: unsupported compression")
	}
	env := pipeline.EnvOf(src)
	rd := &stripReader{src: src, b: b, y: b.Min.Y}
	first, err := rd.next()
	if err != nil {
		return err
	}

	_, _, bpp := encPix(first)
	swap := bpp == 2 || bpp == 8 // 16-bit samples
	generic := bpp == 0
	if generic {
		bpp = 4
	}
	rowLen := int64(dx) * int64(bpp)
	// imageLen is the length of the pixel data in bytes.
	// The offset of the IFD is imageLen + 8 header bytes.
	imageLen := rowLen * int64(dy)
	if rowLen > math.MaxInt32 || (compression == cNone && imageLen+8+maxIFD+int64(len(icc)+len(xmp)) > math.MaxUint32) {
		return errTooLarge
	}

	_, paletted := first.(*image.Paletted)
	predict := o != nil && o.Predictor && compression != cNone && !paletted
	// The rows of a strip: all of them, or about stripBytes of data in a
	// large compressed image.
	rowsPerStrip := int64(dy)
	if compression != cNone && imageLen > stripBytes {
		rowsPerStrip = max(1, stripBytes/rowLen)
	}
	var strips *stripWriter

	// Rows that are not written as they are get converted a chunk at a time.
	var conv []byte
	if swap || generic {
		n := int64(max(1, min(dy, encChunk/int(rowLen))))
		if err := env.Budget.Reserve(n*rowLen, "tiff rows"); err != nil {
			return err
		}
		defer env.Budget.Release(n * rowLen)
		conv = make([]byte, n*rowLen)
	}

	// dst holds the destination for the pixel data of the image --
	// either w or a zlib writer to the spill.
	var (
		dst io.Writer
		sp  *pipeline.Spill
	)
	if compression == cNone {
		// Write IFD offset before outputting pixel data.
		var hdr [8]byte
		copy(hdr[:], leHeader)
		enc.PutUint32(hdr[4:], uint32(imageLen+8))
		if _, err := w.Write(hdr[:]); err != nil {
			return err
		}
		dst = w
	} else {
		// Compressed data is written into the spill first, so that we
		// know the compressed size. The hint is the uncompressed size: the
		// compressed one is unknown, and going to disk a little early costs
		// less than filling the budget first.
		sp = pipeline.NewSpill(env)
		defer sp.Close()
		sp.Expect(imageLen)
		var zw *zlib.Writer
		var lw *lzw.Writer
		comp := func() io.WriteCloser {
			if compression == cLZW {
				if lw == nil {
					lw = lzw.NewWriter(sp)
				} else {
					lw.Reset(sp)
				}
				return lw
			}
			if zw == nil {
				zw = zlib.NewWriter(sp)
			} else {
				zw.Reset(sp)
			}
			return zw
		}
		strips = &stripWriter{sp: sp, comp: comp, size: rowsPerStrip * rowLen}
		dst = strips
		if predict {
			if err := env.Budget.Reserve(rowLen, "tiff predictor row"); err != nil {
				return err
			}
			defer env.Budget.Release(rowLen)
			bps := 1
			if swap {
				bps = 2
			}
			dst = &predictor{w: dst.(io.WriteCloser), row: make([]byte, rowLen), bps: bps, spp: bpp / bps}
		}
	}

	var pal color.Palette
	for m := first; ; {
		sb := m.Bounds()
		pix, stride, _ := encPix(m)
		if p, ok := m.(*image.Paletted); ok && len(p.Palette) > len(pal) {
			pal = p.Palette
		}
		switch {
		case sb.Dy() == 0:
		case !swap && !generic:
			err = writePix(dst, pix, sb.Dy(), int(rowLen), stride)
		default:
			fill := 0
			for y := 0; y < sb.Dy() && err == nil; y++ {
				row := conv[fill : fill+int(rowLen)]
				if swap {
					swap16(row, pix[y*stride:y*stride+int(rowLen)])
				} else {
					genericRow(row, m, sb.Min.Y+y)
				}
				if fill += int(rowLen); fill == len(conv) || y == sb.Dy()-1 {
					_, err = dst.Write(conv[:fill])
					fill = 0
				}
			}
		}
		if err != nil {
			return err
		}
		if m, err = rd.next(); err == io.EOF {
			break
		} else if err != nil {
			return err
		}
	}

	if compression != cNone {
		if err = dst.(io.Closer).Close(); err != nil {
			return err
		}
		imageLen = sp.Size()
		if imageLen+8+maxIFD+8*int64(len(strips.offsets))+int64(len(icc)+len(xmp)) > math.MaxUint32 {
			return errTooLarge
		}
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
		buf := make([]byte, copyBuf)
		for off := int64(0); off < imageLen; {
			k := int(min(int64(len(buf)), imageLen-off))
			if _, err := sp.ReadAt(buf[:k], off); err != nil {
				if err == io.EOF {
					err = io.ErrUnexpectedEOF
				}
				return err
			}
			if _, err := w.Write(buf[:k]); err != nil {
				return err
			}
			off += int64(k)
		}
	}

	photometricInterpretation := uint32(pRGB)
	samplesPerPixel := uint32(4)
	bitsPerSample := []uint32{8, 8, 8, 8}
	extraSamples := uint32(0)
	colorMap := []uint32{}

	switch first.(type) {
	case *image.Paletted:
		photometricInterpretation = pPaletted
		samplesPerPixel = 1
		bitsPerSample = []uint32{8}
		colorMap = make([]uint32, 256*3)
		for i := 0; i < 256 && i < len(pal); i++ {
			r, g, b, _ := pal[i].RGBA()
			colorMap[i+0*256] = uint32(r)
			colorMap[i+1*256] = uint32(g)
			colorMap[i+2*256] = uint32(b)
		}
	case *image.Gray:
		photometricInterpretation = pBlackIsZero
		samplesPerPixel = 1
		bitsPerSample = []uint32{8}
	case *image.Gray16:
		photometricInterpretation = pBlackIsZero
		samplesPerPixel = 1
		bitsPerSample = []uint32{16}
	case *image.NRGBA:
		extraSamples = 2 // Unassociated alpha.
	case *image.NRGBA64:
		extraSamples = 2 // Unassociated alpha.
		bitsPerSample = []uint32{16, 16, 16, 16}
	case *image.RGBA:
		extraSamples = 1 // Associated alpha.
	case *image.RGBA64:
		extraSamples = 1 // Associated alpha.
		bitsPerSample = []uint32{16, 16, 16, 16}
	default:
		extraSamples = 1 // Associated alpha.
	}

	// The reference writes the image size as SHORTs whatever it is; a size
	// that does not fit in one is written as a LONG instead.
	sizeType := func(v int) int {
		if v > math.MaxUint16 {
			return dtLong
		}
		return dtShort
	}
	// Where the strips are (after the 8 bytes of the header) and their
	// sizes.
	offsets, counts := []uint32{8}, []uint32{uint32(imageLen)}
	if strips != nil {
		offsets, counts = strips.offsets, strips.counts
		for i := range offsets {
			offsets[i] += 8
		}
	}
	ifd := []ifdEnt{
		{tag: tImageWidth, datatype: sizeType(dx), data: []uint32{uint32(dx)}},
		{tag: tImageLength, datatype: sizeType(dy), data: []uint32{uint32(dy)}},
		{tag: tBitsPerSample, datatype: dtShort, data: bitsPerSample},
		{tag: tCompression, datatype: dtShort, data: []uint32{compression}},
		{tag: tPhotometricInterpretation, datatype: dtShort, data: []uint32{photometricInterpretation}},
		{tag: tStripOffsets, datatype: dtLong, data: offsets},
		{tag: tSamplesPerPixel, datatype: dtShort, data: []uint32{samplesPerPixel}},
		{tag: tRowsPerStrip, datatype: sizeType(int(rowsPerStrip)), data: []uint32{uint32(rowsPerStrip)}},
		{tag: tStripByteCounts, datatype: dtLong, data: counts},
		// The resolution given, else 72x72 dpi as the reference writes.
		{tag: tXResolution, datatype: dtRational, data: res},
		{tag: tYResolution, datatype: dtRational, data: res},
		{tag: tResolutionUnit, datatype: dtShort, data: []uint32{resPerInch}},
	}
	if len(colorMap) != 0 {
		ifd = append(ifd, ifdEnt{tag: tColorMap, datatype: dtShort, data: colorMap})
	}
	if extraSamples > 0 {
		ifd = append(ifd, ifdEnt{tag: tExtraSamples, datatype: dtShort, data: []uint32{extraSamples}})
	}
	if len(icc) > 0 {
		ifd = append(ifd, ifdEnt{tag: tICCProfile, datatype: dtUndefined, raw: icc})
	}
	if predict {
		ifd = append(ifd, ifdEnt{tag: tPredictor, datatype: dtShort, data: []uint32{prHorizontal}})
	}
	if len(xmp) > 0 {
		ifd = append(ifd, ifdEnt{tag: tXMP, datatype: dtByte, raw: xmp})
	}

	return writeIFD(w, imageLen+8, ifd)
}

// predictor applies horizontal differencing (Predictor 2) to the rows
// written through it, whole rows at a time: each sample less the same
// sample of the pixel before, as 8-bit or (little-endian) 16-bit values.
// Close closes the compressor it writes to.
type predictor struct {
	w        io.WriteCloser
	row      []byte
	n        int // bytes of the row so far
	bps, spp int // bytes per sample, samples per pixel
}

func (p *predictor) Write(b []byte) (int, error) {
	total := len(b)
	for len(b) > 0 {
		k := copy(p.row[p.n:], b)
		p.n += k
		b = b[k:]
		if p.n == len(p.row) {
			p.difference()
			if _, err := p.w.Write(p.row); err != nil {
				return total - len(b), err
			}
			p.n = 0
		}
	}
	return total, nil
}

func (p *predictor) difference() {
	r := p.row
	if p.bps == 1 {
		for i := len(r) - 1; i >= p.spp; i-- {
			r[i] -= r[i-p.spp]
		}
		return
	}
	step := 2 * p.spp
	for i := len(r) - 2; i >= step; i -= 2 {
		enc.PutUint16(r[i:], enc.Uint16(r[i:])-enc.Uint16(r[i-step:]))
	}
}

func (p *predictor) Close() error {
	if p.n != 0 {
		return errors.New("tiff: a row was cut short")
	}
	return p.w.Close()
}

// stripWriter compresses the pixel data written through it strip by strip
// into the spill, each strip a stream of its own, and notes where each
// begins (from the start of the data) and how long it is. Close ends the
// last strip.
type stripWriter struct {
	sp      *pipeline.Spill
	comp    func() io.WriteCloser // a compressor writing to sp, started anew
	cur     io.WriteCloser
	size, n int64 // bytes of pixel data a strip holds, and the current one has
	start   int64
	offsets []uint32
	counts  []uint32
}

func (s *stripWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		if s.cur == nil {
			s.start, s.cur, s.n = s.sp.Size(), s.comp(), 0
		}
		k := int(min(int64(len(p)), s.size-s.n))
		if _, err := s.cur.Write(p[:k]); err != nil {
			return total - len(p), err
		}
		s.n += int64(k)
		p = p[k:]
		if s.n == s.size {
			if err := s.end(); err != nil {
				return total - len(p), err
			}
		}
	}
	return total, nil
}

// end finishes the current strip.
func (s *stripWriter) end() error {
	if err := s.cur.Close(); err != nil {
		return err
	}
	s.cur = nil
	end := s.sp.Size()
	if end > math.MaxUint32-8 {
		return errTooLarge
	}
	s.offsets = append(s.offsets, uint32(s.start))
	s.counts = append(s.counts, uint32(end-s.start))
	return nil
}

func (s *stripWriter) Close() error {
	if s.cur == nil {
		return nil
	}
	return s.end()
}
