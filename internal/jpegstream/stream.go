// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package jpegstream

// The decoder is Go's image/jpeg decoder (go1.27.1): reader.go, huffman.go,
// scan.go and dct.go are the standard library's files. The changes are:
//
//   - processSOS is split into beginScan (the header) and scanRow (one MCU
//     row), so a scan can be decoded a row at a time;
//   - the image buffers (img1, img3, blackPix) are a band of a few MCU rows
//     instead of the whole image;
//   - a baseline image whose first scan holds every component is decoded
//     straight into that band, strip by strip;
//   - every other image (progressive, or sequential with the components in
//     separate scans) stores its DCT coefficients in a pipeline.Scratch, which is
//     a temporary file when it does not fit in the memory budget, and turns
//     them into pixels band by band after the last scan;
//   - applyBlack and convertToRGB work on a band.
//
// The pixels are the same as jpeg.Decode's.

import (
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// coefStore holds the DCT coefficients of every block of every component,
// 64 int16 per block, in a Scratch. Scans visit a component's block rows in
// increasing order, so each component keeps a window of v block rows that is
// read and written back as the scan moves down.
type coefStore struct {
	sc   *pipeline.Scratch
	comp [maxComponents]coefComp
}

type coefComp struct {
	off    int64 // offset of the component's region in the Scratch
	stride int   // blocks per block row
	rows   int   // block rows
	v      int   // block rows per window (the vertical sampling factor)
	win    []byte
	w0     int // first block row in win, or -1
	dirty  bool
}

const blockBytes = 2 * blockSize

// testMCURows, when positive, forces the band height (tests only).
var testMCURows int

func (c *coefStore) flush(ci int) error {
	p := &c.comp[ci]
	if !p.dirty {
		return nil
	}
	p.dirty = false
	n := min(p.v, p.rows-p.w0) * p.stride * blockBytes
	_, err := c.sc.WriteAt(p.win[:n], p.off+int64(p.w0)*int64(p.stride)*blockBytes)
	return err
}

func (c *coefStore) flushAll() error {
	for i := range c.comp {
		if c.comp[i].win == nil {
			continue
		}
		if err := c.flush(i); err != nil {
			return err
		}
	}
	return nil
}

// at returns the 128 bytes of block (bx, by) of component ci, loading the
// window that holds it.
func (c *coefStore) at(ci, bx, by int) ([]byte, error) {
	p := &c.comp[ci]
	if p.win == nil || bx < 0 || bx >= p.stride || by < 0 || by >= p.rows {
		return nil, FormatError("block outside the image")
	}
	if r0 := by / p.v * p.v; r0 != p.w0 {
		if err := c.flush(ci); err != nil {
			return nil, err
		}
		n := min(p.v, p.rows-r0) * p.stride * blockBytes
		if _, err := c.sc.ReadAt(p.win[:n], p.off+int64(r0)*int64(p.stride)*blockBytes); err != nil {
			return nil, err
		}
		p.w0 = r0
	}
	o := ((by-p.w0)*p.stride + bx) * blockBytes
	return p.win[o : o+blockBytes : o+blockBytes], nil
}

// load reads the coefficients zigStart to zigEnd (in zig-zag order) of a
// block; a scan reads and changes no others. A narrow range (the DC scans,
// the first luma AC scan of the usual progression) converts only its own
// coefficients.
func (c *coefStore) load(ci, bx, by int, b *block, zigStart, zigEnd int32) error {
	w, err := c.at(ci, bx, by)
	if err != nil {
		return err
	}
	if zigEnd-zigStart < blockSize/2 {
		for zig := zigStart; zig <= zigEnd; zig++ {
			u := unzig[zig]
			b[u] = int32(int16(binary.LittleEndian.Uint16(w[2*u:])))
		}
		return nil
	}
	for i := range b {
		b[i] = int32(int16(binary.LittleEndian.Uint16(w[2*i:])))
	}
	return nil
}

// save writes back the range load read.
func (c *coefStore) save(ci, bx, by int, b *block, zigStart, zigEnd int32) error {
	w, err := c.at(ci, bx, by)
	if err != nil {
		return err
	}
	c.comp[ci].dirty = true
	if zigEnd-zigStart < blockSize/2 {
		for zig := zigStart; zig <= zigEnd; zig++ {
			u := unzig[zig]
			v := b[u]
			if v != int32(int16(v)) {
				return FormatError("DCT coefficient out of range")
			}
			binary.LittleEndian.PutUint16(w[2*u:], uint16(int16(v)))
		}
		return nil
	}
	for i, v := range b {
		if v != int32(int16(v)) {
			// Only corrupt data produces coefficients this large.
			return FormatError("DCT coefficient out of range")
		}
		binary.LittleEndian.PutUint16(w[2*i:], uint16(int16(v)))
	}
	return nil
}

type stream struct {
	d        *decoder
	env      *pipeline.Env
	k        int         // MCU rows per band
	my       int         // next MCU row to output
	rgba     *image.RGBA // band for convertToRGB / applyBlack
	reserved int64
	scanned  bool // full mode: every scan has been read
	done     bool
	err      error
}

// Open reads the JPEG headers up to the first scan and returns a Source that
// decodes the pixels on demand. env may be nil.
func Open(r io.Reader, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	d := &decoder{r: r}
	// Check for the Start Of Image marker.
	if err := d.readFull(d.tmp[:2]); err != nil {
		return nil, err
	}
	if d.tmp[0] != 0xff || d.tmp[1] != soiMarker {
		return nil, FormatError("missing SOI marker")
	}
	for {
		sos, eoi, err := d.nextSegment()
		if err != nil {
			return nil, err
		}
		if eoi {
			return nil, FormatError("missing SOS marker")
		}
		if sos {
			break
		}
	}
	d.finishMeta()
	// The errors the standard library reports after decoding.
	if d.nComp == 4 && !d.adobeTransformValid {
		return nil, UnsupportedError("unknown color model: 4-component JPEG doesn't have Adobe APP14 metadata")
	}
	if d.nComp == 3 && d.isRGB() {
		h0, h1, h2 := d.comp[0].h, d.comp[1].h, d.comp[2].h
		v0, v1, v2 := d.comp[0].v, d.comp[1].v, d.comp[2].v
		if (h1 != h2) || (h0%h1 != 0) || (v1 != v2) || (v0%v1 != 0) {
			return nil, errUnsupportedSubsamplingRatio
		}
	}
	d.full = d.progressive || d.scan.nComp != d.nComp

	s := &stream{d: d, env: env}
	// Bytes per pixel row across the band buffers, at MCU-padded width.
	pw := 8 * d.maxH * d.mxx
	rowBytes := int64(pw)
	if d.nComp > 1 {
		rowBytes = 3 * int64(pw) // an upper bound for every subsampling
	}
	if d.nComp == 4 {
		rowBytes += int64(pw)
	}
	if d.nComp == 4 || (d.nComp == 3 && d.isRGB()) {
		rowBytes += 4 * int64(d.width)
	}
	mcuRows := 8 * d.maxV
	s.k = max(1, pipeline.StripRows(rowBytes, d.height)/mcuRows)
	if testMCURows > 0 {
		s.k = testMCURows
	}
	need := rowBytes * int64(mcuRows*s.k)
	if err := env.Budget.Reserve(need, "jpeg band"); err != nil {
		return nil, err
	}
	s.reserved = need
	band := image.Rect(0, 0, pw, mcuRows*s.k)
	if d.nComp == 1 {
		d.img1 = image.NewGray(band)
	} else {
		d.img3 = image.NewYCbCr(band, d.ratio)
	}
	if d.nComp == 4 {
		d.blackStride = 8 * d.comp[3].h * d.mxx
		d.blackPix = make([]byte, d.blackStride*8*d.comp[3].v*s.k)
	}
	if d.nComp == 4 || (d.nComp == 3 && d.isRGB()) {
		s.rgba = image.NewRGBA(image.Rect(0, 0, d.width, mcuRows*s.k))
	}
	return s, nil
}

// nextSegment is one iteration of the marker loop in the standard library's
// decode. It reports whether the segment was a scan header (now parsed by
// beginScan) or the End Of Image marker.
func (d *decoder) nextSegment() (sos, eoi bool, err error) {
	for {
		err := d.readFull(d.tmp[:2])
		if err != nil {
			return false, false, err
		}
		for d.tmp[0] != 0xff {
			// Extraneous data is silently ignored, as libjpeg does; see the
			// standard library for the reasoning.
			d.tmp[0] = d.tmp[1]
			d.tmp[1], err = d.readByte()
			if err != nil {
				return false, false, err
			}
		}
		marker := d.tmp[1]
		if marker == 0 {
			// Treat "\xff\x00" as extraneous data.
			continue
		}
		for marker == 0xff {
			// Section B.1.1.2 says, "Any marker may optionally be preceded by any
			// number of fill bytes, which are bytes assigned code X'FF'".
			marker, err = d.readByte()
			if err != nil {
				return false, false, err
			}
		}
		if marker == eoiMarker { // End Of Image.
			return false, true, nil
		}
		if rst0Marker <= marker && marker <= rst7Marker {
			// A restart marker after the final entropy coded segment is
			// ignored as a harmless error.
			continue
		}

		// Read the 16-bit length of the segment. The value includes the 2 bytes for the
		// length itself, so we subtract 2 to get the number of remaining bytes.
		if err = d.readFull(d.tmp[:2]); err != nil {
			return false, false, err
		}
		n := int(d.tmp[0])<<8 + int(d.tmp[1]) - 2
		if n < 0 {
			return false, false, FormatError("short segment length")
		}

		switch marker {
		case sof0Marker, sof1Marker, sof2Marker:
			d.baseline = marker == sof0Marker
			d.progressive = marker == sof2Marker
			err = d.processSOF(n)
		case dhtMarker:
			err = d.processDHT(n)
		case dqtMarker:
			err = d.processDQT(n)
		case sosMarker:
			return true, false, d.beginScan(n)
		case driMarker:
			err = d.processDRI(n)
		case app0Marker:
			err = d.processApp0Marker(n)
		case app1Marker:
			err = d.processApp1(n)
		case app2Marker:
			err = d.processApp2(n)
		case app14Marker:
			err = d.processApp14Marker(n)
		default:
			if app0Marker <= marker && marker <= app15Marker || marker == comMarker {
				err = d.ignore(n)
			} else if marker < 0xc0 { // See Table B.1 "Marker code assignments".
				err = FormatError("unknown marker")
			} else {
				err = UnsupportedError("unknown marker")
			}
		}
		return false, false, err
	}
}

func (s *stream) Env() *pipeline.Env      { return s.env }
func (s *stream) Bounds() image.Rectangle { return image.Rect(0, 0, s.d.width, s.d.height) }
func (s *stream) Opaque() bool            { return true }

// Meta returns the EXIF block and ICC profile found before the first scan.
func (s *stream) Meta() pipeline.Meta { return s.d.meta }

// Subsampling reports the chroma subsampling of the strips when they are
// Gray (gray true) or YCbCr; ok is false for CMYK and RGB images, whose
// strips are neither.
func (s *stream) Subsampling() (ratio image.YCbCrSubsampleRatio, gray, ok bool) {
	d := s.d
	switch {
	case d.nComp == 1:
		return 0, true, true
	case d.nComp == 3 && !d.isRGB():
		return d.ratio, false, true
	}
	return 0, false, false
}

// Buffered reports whether the image needs its coefficients stored
// (progressive, or sequential with the components in separate scans).
func (s *stream) Buffered() bool { return s.d.full }

func (s *stream) ColorModel() color.Model {
	d := s.d
	switch {
	case d.nComp == 1:
		return color.GrayModel
	case d.nComp == 4:
		return color.CMYKModel
	case d.isRGB():
		return color.RGBAModel
	}
	return color.YCbCrModel
}

func (s *stream) Close() error {
	var err error
	if s.d.coef != nil && s.d.coef.sc != nil {
		err = s.d.coef.sc.Close()
		s.d.coef.sc = nil
	}
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	if s.err == nil {
		s.err = errors.New("goimgtools: jpeg: closed")
	}
	return err
}

// scanAll reads every scan of a progressive or multi-scan image into the
// coefficient store, through to the End Of Image marker.
func (s *stream) scanAll() error {
	d := s.d
	c := &coefStore{}
	var total, win int64
	for i := 0; i < d.nComp; i++ {
		p := &c.comp[i]
		p.off, p.stride, p.rows, p.v, p.w0 = total, d.mxx*d.comp[i].h, d.myy*d.comp[i].v, d.comp[i].v, -1
		total += int64(p.stride) * int64(p.rows) * blockBytes
		win += int64(p.stride) * int64(p.v) * blockBytes
	}
	if err := s.env.Budget.Reserve(win, "jpeg coefficient windows"); err != nil {
		return err
	}
	s.reserved += win
	for i := 0; i < d.nComp; i++ {
		p := &c.comp[i]
		p.win = make([]byte, p.stride*p.v*blockBytes)
	}
	sc, err := pipeline.NewScratch(s.env, total)
	if err != nil {
		return err
	}
	c.sc = sc
	d.coef = c
	for {
		for my := 0; my < d.myy; my++ {
			if err := d.scanRow(my); err != nil {
				return err
			}
		}
		if err := c.flushAll(); err != nil {
			return err
		}
		for {
			sos, eoi, err := d.nextSegment()
			if err != nil {
				return err
			}
			if eoi {
				return nil
			}
			if sos {
				break
			}
		}
	}
}

// reconstructBand is the standard library's reconstructProgressiveImage for
// MCU rows [my0, my1), writing into the band.
func (s *stream) reconstructBand(my0, my1 int) error {
	d := s.d
	if d.img1 != nil {
		clear(d.img1.Pix)
	} else {
		clear(d.img3.Y)
		clear(d.img3.Cb)
		clear(d.img3.Cr)
		clear(d.blackPix)
	}
	var b block
	for i := 0; i < d.nComp; i++ {
		if !d.touched[i] {
			continue
		}
		v := 8 * d.maxV / d.comp[i].v
		h := 8 * d.maxH / d.comp[i].h
		vi := d.comp[i].v
		for by := my0 * vi; by < my1*vi && by*v < d.height; by++ {
			for bx := 0; bx*h < d.width; bx++ {
				if err := d.coef.load(i, bx, by, &b, 0, blockSize-1); err != nil {
					return err
				}
				if err := d.reconstructBlock(&b, bx, by-my0*vi, i); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func (s *stream) Next() (image.Image, error) {
	if s.done {
		return nil, io.EOF
	}
	if s.err != nil {
		return nil, s.err
	}
	strip, err := s.next()
	if err != nil {
		if err != io.EOF {
			s.err = err
		}
		return nil, err
	}
	return strip, nil
}

func (s *stream) next() (image.Image, error) {
	d := s.d
	if d.full && !s.scanned {
		if err := s.scanAll(); err != nil {
			return nil, err
		}
		s.scanned = true
	}
	if s.my >= d.myy { // an image with no rows
		if !d.full {
			if err := s.finishStream(); err != nil {
				return nil, err
			}
		}
		s.done = true
		return nil, io.EOF
	}
	my0, my1 := s.my, min(s.my+s.k, d.myy)
	d.bandMy = my0
	if d.full {
		if err := s.reconstructBand(my0, my1); err != nil {
			return nil, err
		}
	} else {
		for my := my0; my < my1; my++ {
			if err := d.scanRow(my); err != nil {
				return nil, err
			}
		}
	}
	s.my = my1
	if my1 == d.myy {
		if !d.full {
			if err := s.finishStream(); err != nil {
				return nil, err
			}
		}
		s.done = true
	}
	mcuRows := 8 * d.maxV
	r := image.Rect(0, my0*mcuRows, d.width, min(my1*mcuRows, d.height))
	return s.finishBand(r), nil
}

// finishStream reads what follows the single scan of a streamed image, up
// to the End Of Image marker. The standard library would decode a further
// scan over the pixels already produced; those pixels have left, so such a
// file is rejected.
func (s *stream) finishStream() error {
	for {
		sos, eoi, err := s.d.nextSegment()
		if err != nil {
			return err
		}
		if eoi {
			return nil
		}
		if sos {
			return UnsupportedError("another scan after a complete sequential scan")
		}
	}
}

// finishBand turns the band into the strip for rows r, applying the
// standard library's applyBlack or convertToRGB where they apply.
func (s *stream) finishBand(r image.Rectangle) image.Image {
	d := s.d
	if d.img1 != nil {
		m := *d.img1
		m.Rect = r
		return &m
	}
	m := *d.img3
	m.Rect = r
	img3 := &m
	if d.nComp == 4 {
		return s.applyBlack(img3)
	}
	if d.isRGB() {
		return s.convertToRGB(img3)
	}
	return img3
}

// applyBlack is the standard library's applyBlack for one band.
func (s *stream) applyBlack(img3 *image.YCbCr) image.Image {
	d := s.d
	bounds := img3.Bounds()
	out := &image.CMYK{Pix: s.rgba.Pix[:bounds.Dy()*s.rgba.Stride], Stride: s.rgba.Stride, Rect: bounds}
	if d.adobeTransform != adobeTransformUnknown {
		// YCbCrK: convert the YCbCr part to RGB (CMY after the implicit
		// inversion) and patch in the inverted K. imageutil.DrawYCbCr, which
		// the standard library calls here, only handles these four ratios
		// and otherwise leaves the pixels zero.
		clear(out.Pix)
		switch img3.SubsampleRatio {
		case image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422,
			image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio440:
			img := &image.RGBA{Pix: out.Pix, Stride: out.Stride, Rect: bounds}
			draw.Draw(img, bounds, img3, bounds.Min, draw.Src)
		}
		for iBase, y := 0, bounds.Min.Y; y < bounds.Max.Y; iBase, y = iBase+out.Stride, y+1 {
			for i, x := iBase+3, bounds.Min.X; x < bounds.Max.X; i, x = i+4, x+1 {
				out.Pix[i] = 255 - d.blackPix[(y-bounds.Min.Y)*d.blackStride+(x-bounds.Min.X)]
			}
		}
		return out
	}
	translations := [4]struct {
		src    []byte
		stride int
	}{
		{img3.Y, img3.YStride},
		{img3.Cb, img3.CStride},
		{img3.Cr, img3.CStride},
		{d.blackPix, d.blackStride},
	}
	for t, translation := range translations {
		subsample := d.comp[t].h != d.comp[0].h || d.comp[t].v != d.comp[0].v
		for iBase, y := 0, bounds.Min.Y; y < bounds.Max.Y; iBase, y = iBase+out.Stride, y+1 {
			sy := y - bounds.Min.Y
			if subsample {
				sy /= 2
			}
			for i, x := iBase+t, bounds.Min.X; x < bounds.Max.X; i, x = i+4, x+1 {
				sx := x - bounds.Min.X
				if subsample {
					sx /= 2
				}
				out.Pix[i] = 255 - translation.src[sy*translation.stride+sx]
			}
		}
	}
	return out
}

// convertToRGB is the standard library's convertToRGB for one band.
func (s *stream) convertToRGB(img3 *image.YCbCr) image.Image {
	d := s.d
	cScale := d.comp[0].h / d.comp[1].h
	bounds := img3.Bounds()
	rg := *s.rgba
	rg.Rect = bounds
	img := &rg
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		po := img.PixOffset(bounds.Min.X, y)
		yo := img3.YOffset(bounds.Min.X, y)
		co := img3.COffset(bounds.Min.X, y)
		for i, iMax := 0, bounds.Max.X-bounds.Min.X; i < iMax; i++ {
			img.Pix[po+4*i+0] = img3.Y[yo+i]
			img.Pix[po+4*i+1] = img3.Cb[co+i/cScale]
			img.Pix[po+4*i+2] = img3.Cr[co+i/cScale]
			img.Pix[po+4*i+3] = 255
		}
	}
	return img
}
