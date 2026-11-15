// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package tiffstream

import (
	"errors"
	"fmt"
	"github.com/klauspost/compress/zlib"
	"image"
	"image/color"
	"io"

	"github.com/whhhh1500/goimgtools/internal/tiffstream/lzw"
	"golang.org/x/image/ccitt"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// drainChunk is the minimum size of the row buffer, which is also used to
// read (and discard) what is left of a block after its last row.
const drainChunk = 4096

// stream is the block loop of x/image's Decode, turned inside out: it keeps
// one streaming decompressor open on the current block (strip or tile) and
// pulls one row of samples at a time from it.
type stream struct {
	d   *decoder
	env *pipeline.Env

	// Block layout, as computed at the top of x/image's Decode.
	blockPadding     bool
	blockWidth       int
	blockHeight      int
	blocksAcross     int
	blocksDown       int
	blockOffsets     []uint
	blockCounts      []uint
	blockMaxDataSize int64
	rowBytes         int // bytes in one decompressed row of a block

	band     image.Image // reusable strip buffer, n rows
	n        int         // rows per output strip
	y        int         // next row to hand out
	row      []byte      // one decompressed block row
	reserved int64
	err      error
	done     bool

	// The block being read.
	br        io.Reader
	lz        *lzw.Reader   // LZW reader, reused from block to block
	zr        io.ReadCloser // zlib reader, reused from block to block
	blkW      int
	blkH      int
	blockRead int64 // decompressed bytes read from the block so far
	blockEOF  bool  // the decompressor reported the end of the block
	// blockLimited says the reference stops reading the block after
	// blockMaxDataSize bytes without an error (io.LimitReader).
	blockLimited bool
	// blockLen is the known length of an uncompressed block, or -1.
	blockLen int64
}

// Open parses the TIFF header and IFD in r, which holds size bytes, and
// returns a Source that decodes the pixel rows on demand. It yields the same
// image type, palette and pixels as golang.org/x/image/tiff.Decode, and
// fails (in Open or in Next) whenever that decoder fails. env may be nil.
func Open(r io.ReaderAt, size int64, env *pipeline.Env) (pipeline.Source, error) {
	if env == nil {
		env = pipeline.NewEnv(0, "")
	}
	if size >= 0 {
		r = io.NewSectionReader(r, 0, size)
	}
	s, err := open(r, env)
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	return s, nil
}

func open(r io.ReaderAt, env *pipeline.Env) (_ *stream, err error) {
	d, err := newDecoder(r)
	if err != nil {
		return nil, err
	}

	blockPadding := false
	blockWidth := d.config.Width
	blockHeight := d.config.Height
	blocksAcross := 1
	blocksDown := 1

	if d.config.Width == 0 {
		blocksAcross = 0
	}
	if d.config.Height == 0 {
		blocksDown = 0
	}

	var blockOffsets, blockCounts []uint

	if d.firstVal(tTileWidth) != 0 {
		blockPadding = true

		if blockWidth, err = d.firstIntVal(tTileWidth); err != nil {
			return nil, err
		}
		if blockHeight, err = d.firstIntVal(tTileLength); err != nil {
			return nil, err
		}

		// The specification says that tile widths and lengths must be a multiple of 16.
		// We currently permit invalid sizes, but reject anything too small to limit the
		// amount of work a malicious input can force us to perform.
		if blockWidth < 8 || blockHeight < 8 {
			return nil, FormatError("tile size is too small")
		}
		// Same conservative assumption on bytes-per-pixel as for the image dimensions.
		if _, ok := mul3(blockWidth, blockHeight, maxBytesPerPixel); !ok {
			return nil, FormatError("tile size is too large")
		}
		if blockWidth-d.config.Width > 16 || blockHeight-d.config.Height > 16 {
			// Tiles may be padded to the nearest multiple of 16, but one of
			// the dimensions of the tile exceeds the image dimension by more
			// than padding would require. See x/image for the rationale.
			if blockWidth > 1024 || blockHeight > 1024 {
				return nil, FormatError("tile size exceeds image size")
			}
		}
		if blockWidth != 0 {
			blocksAcross = (d.config.Width + blockWidth - 1) / blockWidth
		}
		if blockHeight != 0 {
			blocksDown = (d.config.Height + blockHeight - 1) / blockHeight
		}

		blockOffsets, err = d.parseIFDOffsets(tTileOffsets, blocksAcross*blocksDown)
		if err != nil {
			return nil, err
		}
		blockCounts, err = d.parseIFDOffsets(tTileByteCounts, blocksAcross*blocksDown)
		if err != nil {
			return nil, err
		}
	} else {

		if v := d.firstVal(tRowsPerStrip); v > 0 && v < uint(blockHeight) {
			blockHeight = int(v)
		}

		if blockHeight != 0 {
			// This can't overflow: We require that w*h*8 not overflow and
			// blockHeight is no more than d.config.Height.
			blocksDown = (d.config.Height + blockHeight - 1) / blockHeight
		}

		blockOffsets, err = d.parseIFDOffsets(tStripOffsets, blocksDown)
		if err != nil {
			return nil, err
		}
		blockCounts, err = d.parseIFDOffsets(tStripByteCounts, blocksDown)
		if err != nil {
			return nil, err
		}
	}

	// Check if we have the right number of strips/tiles, offsets and counts.
	if n := blocksAcross * blocksDown; len(blockOffsets) < n || len(blockCounts) < n {
		return nil, FormatError("inconsistent header")
	}

	// The reference decoder finds the next two problems while decoding the
	// first block; they do not depend on the pixel data, so report them now.
	switch d.firstVal(tCompression) {
	case cNone, 0, cG3, cG4, cLZW, cDeflate, cDeflateOld, cPackBits:
	default:
		return nil, UnsupportedError(fmt.Sprintf("compression value %d", d.firstVal(tCompression)))
	}
	if d.firstVal(tPredictor) == prHorizontal && d.bpp == 1 {
		return nil, UnsupportedError("horizontal predictor with 1 BitsPerSample")
	}

	s := &stream{
		d:            d,
		env:          env,
		blockPadding: blockPadding,
		blockWidth:   blockWidth,
		blockHeight:  blockHeight,
		blocksAcross: blocksAcross,
		blocksDown:   blocksDown,
		blockOffsets: blockOffsets,
		blockCounts:  blockCounts,
		// Maximum data per pixel is 8 bytes (RGBA64).
		blockMaxDataSize: int64(blockWidth) * int64(blockHeight) * 8,
	}

	// The row layout of a block's decompressed data, as in decoder.decode.
	numSamples := 1
	switch d.mode {
	case mRGB:
		numSamples = 3
	case mNRGBA, mRGBA:
		numSamples = 4
	}
	s.rowBytes = int((int64(blockWidth)*int64(numSamples)*int64(d.bpp) + 7) / 8)

	// A stripped image is handed out in strips of about 256KB, whatever the
	// TIFF's own strip size. A tiled image is handed out one tile row at a
	// time, because the tiles of a row are decoded one after another.
	proto := s.newImage(image.Rect(0, 0, 1, 1))
	if blockPadding {
		s.n = min(blockHeight, d.config.Height)
	} else {
		s.n = pipeline.StripRows(pipeline.BandBytes(proto, image.Rect(0, 0, d.config.Width, 1)), d.config.Height)
	}
	full := image.Rect(0, 0, d.config.Width, s.n)
	rowCap := max(s.rowBytes, drainChunk)
	need := pipeline.BandBytes(proto, full) + int64(rowCap)
	switch d.firstVal(tCompression) {
	case cG3, cG4:
		// The CCITT reader keeps two rows of one byte per pixel.
		need += 2 * int64(blockWidth)
	}
	if err := env.Budget.Reserve(need, "tiff strip buffers"); err != nil {
		return nil, err
	}
	s.reserved = need
	s.band = s.newImage(full)
	s.row = make([]byte, rowCap)
	return s, nil
}

// newImage allocates the image type x/image's Decode picks for the mode.
func (s *stream) newImage(imgRect image.Rectangle) (img image.Image) {
	d := s.d
	switch d.mode {
	case mGray, mGrayInvert:
		if d.bpp == 16 {
			img = image.NewGray16(imgRect)
		} else {
			img = image.NewGray(imgRect)
		}
	case mPaletted:
		img = image.NewPaletted(imgRect, d.palette)
	case mNRGBA:
		if d.bpp == 16 {
			img = image.NewNRGBA64(imgRect)
		} else {
			img = image.NewNRGBA(imgRect)
		}
	case mRGB, mRGBA:
		if d.bpp == 16 {
			img = image.NewRGBA64(imgRect)
		} else {
			img = image.NewRGBA(imgRect)
		}
	}
	return img
}

func (s *stream) Env() *pipeline.Env { return s.env }

// Meta is the colour profile of the file. (EXIF, which TIFF keeps in a
// directory of its own, is not read.)
func (s *stream) Meta() pipeline.Meta { return pipeline.Meta{ICC: s.d.icc} }

func (s *stream) Bounds() image.Rectangle {
	return image.Rect(0, 0, s.d.config.Width, s.d.config.Height)
}

func (s *stream) ColorModel() color.Model { return s.d.config.ColorModel }

// Opaque reports whether the mode has no alpha channel. TIFF palettes are
// always opaque.
func (s *stream) Opaque() bool {
	switch s.d.mode {
	case mNRGBA, mRGBA:
		return false
	}
	return true
}

func (s *stream) Close() error {
	s.closeBlock()
	if s.zr != nil {
		s.zr.Close()
		s.zr = nil
	}
	s.band, s.row, s.d.buf = nil, nil, nil
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	if s.err == nil {
		s.err = errors.New("goimgtools: tiff: closed")
	}
	return nil
}

// Next decodes the next strip. The final strip is returned only after every
// block has been decompressed to its end without error, as Decode does.
func (s *stream) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	if s.done {
		return nil, io.EOF
	}
	var strip image.Image
	var err error
	if s.blockPadding {
		strip, err = s.nextTileRow()
	} else {
		strip, err = s.nextStrip()
	}
	if err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		s.closeBlock()
		s.err = err
		return nil, err
	}
	s.y = strip.Bounds().Max.Y
	if s.y == s.d.config.Height {
		s.done = true
	}
	return strip, nil
}

// nextStrip fills one output strip of a stripped image, moving from one
// TIFF strip to the next as rows are consumed.
func (s *stream) nextStrip() (image.Image, error) {
	d := s.d
	y0, y1 := s.y, min(s.y+s.n, d.config.Height)
	strip := pipeline.Relabel(s.band, image.Rect(0, y0, d.config.Width, y1))
	for y := y0; y < y1; y++ {
		j := y / s.blockHeight
		ymin := j * s.blockHeight
		if y == ymin {
			if err := s.openBlock(0, j); err != nil {
				return nil, err
			}
		}
		if err := s.decodeRow(strip, 0, y); err != nil {
			return nil, err
		}
		if y+1 == ymin+s.blkH {
			if err := s.finishBlock(); err != nil {
				return nil, err
			}
		}
	}
	return strip, nil
}

// nextTileRow decodes the tiles of one tile row, one after another, into a
// band that is as tall as a tile (clipped to the image).
func (s *stream) nextTileRow() (image.Image, error) {
	d := s.d
	j := s.y / s.blockHeight
	y0 := j * s.blockHeight
	y1 := min(y0+s.blockHeight, d.config.Height)
	band := pipeline.Relabel(s.band, image.Rect(0, y0, d.config.Width, y1))
	for i := 0; i < s.blocksAcross; i++ {
		if err := s.openBlock(i, j); err != nil {
			return nil, err
		}
		xmin := i * s.blockWidth
		for y := y0; y < y1; y++ {
			if err := s.decodeRow(band, xmin, y); err != nil {
				return nil, err
			}
		}
		if err := s.finishBlock(); err != nil {
			return nil, err
		}
	}
	return band, nil
}

// openBlock opens a streaming decompressor on block (i, j). It is the
// compression switch of x/image's Decode with the io.ReadAll calls removed.
func (s *stream) openBlock(i, j int) (err error) {
	d := s.d
	blkW := s.blockWidth
	if !s.blockPadding && i == s.blocksAcross-1 && d.config.Width%s.blockWidth != 0 {
		blkW = d.config.Width % s.blockWidth
	}
	blkH := s.blockHeight
	if !s.blockPadding && j == s.blocksDown-1 && d.config.Height%s.blockHeight != 0 {
		blkH = d.config.Height % s.blockHeight
	}
	s.blkW, s.blkH = blkW, blkH
	s.blockRead, s.blockEOF, s.blockLimited, s.blockLen = 0, false, true, -1

	offset := int64(s.blockOffsets[j*s.blocksAcross+i])
	n := int64(s.blockCounts[j*s.blocksAcross+i])
	switch d.firstVal(tCompression) {

	// According to the spec, Compression does not have a default value,
	// but some tools interpret a missing Compression value as none, so we do
	// the same.
	case cNone, 0:
		if n > s.blockMaxDataSize {
			return FormatError("block data size too large")
		}
		// Decode reads all n bytes before it looks at any of them, so a
		// block that runs past the end of the file is an error even when
		// the rows the image needs are present.
		if n > 0 {
			var b [1]byte
			if m, err := d.r.ReadAt(b[:], offset+n-1); m != 1 {
				if err == nil {
					err = io.ErrUnexpectedEOF
				}
				return err
			}
		}
		s.br = io.NewSectionReader(d.r, offset, n)
		s.blockLen = n
	case cG3:
		inv := d.firstVal(tPhotometricInterpretation) == pWhiteIsZero
		order := ccittFillOrder(d.firstVal(tFillOrder))
		s.br = ccitt.NewReader(io.NewSectionReader(d.r, offset, n), order, ccitt.Group3, blkW, blkH, &ccitt.Options{Invert: inv, Align: false})
	case cG4:
		inv := d.firstVal(tPhotometricInterpretation) == pWhiteIsZero
		order := ccittFillOrder(d.firstVal(tFillOrder))
		s.br = ccitt.NewReader(io.NewSectionReader(d.r, offset, n), order, ccitt.Group4, blkW, blkH, &ccitt.Options{Invert: inv, Align: false})
	case cLZW:
		sr := io.NewSectionReader(d.r, offset, n)
		if s.lz == nil {
			s.lz = lzw.NewReader(sr, lzw.MSB, 8)
		} else {
			s.lz.Reset(sr)
		}
		s.br = s.lz
	case cDeflate, cDeflateOld:
		sr := io.NewSectionReader(d.r, offset, n)
		if s.zr == nil {
			s.zr, err = zlib.NewReader(sr)
		} else {
			err = s.zr.(zlib.Resetter).Reset(sr, nil)
		}
		if err != nil {
			return err
		}
		s.br = s.zr
	case cPackBits:
		// unpackBits has no silent limit: it reads to the end of the block
		// and fails if that yields more than blockMaxDataSize bytes.
		s.br = newPackBitsReader(io.NewSectionReader(d.r, offset, n), s.blockMaxDataSize)
		s.blockLimited = false
	default:
		err = UnsupportedError(fmt.Sprintf("compression value %d", d.firstVal(tCompression)))
	}
	return err
}

// read reads from the block's decompressor until p is full or the block
// ends. A short count with a nil error means the block ended early.
func (s *stream) read(p []byte) (int, error) {
	n, idle := 0, 0
	for n < len(p) && !s.blockEOF {
		m, err := s.br.Read(p[n:])
		n += m
		if err == io.EOF {
			s.blockEOF = true
			break
		}
		if err != nil {
			s.blockRead += int64(n)
			return n, err
		}
		if m == 0 {
			if idle++; idle > 100 {
				s.blockRead += int64(n)
				return n, io.ErrNoProgress
			}
		} else {
			idle = 0
		}
	}
	s.blockRead += int64(n)
	return n, nil
}

// decodeRow reads the next row of the current block and converts it into
// row y of dst with the unchanged decoder.decode, which sees a block that is
// one row high. A row that is cut short fails there exactly as it does in
// the reference, whose bounds checks are all relative to the row start.
func (s *stream) decodeRow(dst image.Image, xmin, y int) error {
	n, err := s.read(s.row[:s.rowBytes])
	if err != nil {
		return err
	}
	s.d.buf = s.row[:n]
	return s.d.decode(dst, xmin, y, xmin+s.blkW, y+1)
}

// finishBlock reads whatever the reference decoder would still read from the
// current block after the rows that were decoded, so that a checksum error,
// a truncated stream or a missing predictor row is reported as it is there.
func (s *stream) finishBlock() error {
	d := s.d
	if s.blockLen >= 0 {
		// Uncompressed: the bytes are known to exist, nothing to verify.
		s.blockRead = s.blockLen
	}
	for !s.blockEOF && s.blockLen < 0 {
		p := s.row
		if s.blockLimited {
			rem := s.blockMaxDataSize - s.blockRead
			if rem <= 0 {
				break
			}
			if int64(len(p)) > rem {
				p = p[:rem]
			}
		}
		if _, err := s.read(p); err != nil {
			return err
		}
	}
	s.closeBlock()

	// decoder.decode applies the horizontal predictor to every row of the
	// block, including the rows of a bottom tile that lie below the image,
	// and fails if one of them is incomplete. Rows inside the image were
	// checked by decodeRow; the count covers the rest.
	if d.firstVal(tPredictor) == prHorizontal && (d.bpp == 8 || d.bpp == 16) && s.blkW > 1 {
		if s.blockRead < int64(s.blkH)*int64(s.rowBytes) {
			return errNoPixels
		}
	}
	return nil
}

func (s *stream) closeBlock() {
	s.br = nil
}
