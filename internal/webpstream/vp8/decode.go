// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package vp8 implements a decoder for the VP8 lossy image format.
//
// The VP8 specification is RFC 6386.
//
// It is a fork of golang.org/x/image/vp8 that produces the frame as bands of
// a few macroblock rows instead of one whole image, and reads the compressed
// partitions through small windows instead of loading them whole. The
// arithmetic is unchanged, so the pixels are identical. What differs:
//
//   - the frame buffer holds bandMB+1 macroblock rows: the loop filter of a
//     macroblock row still changes the bottom rows of the row above, so a row
//     is only final (and handed out) once the row below has been filtered;
//   - intra prediction needs the unfiltered bottom row of the macroblocks
//     above, which is kept in three line buffers (topY, topCb, topCr);
//   - loop filter parameters are kept for one macroblock row, not the frame.
package vp8

// This file implements the top-level decoding algorithm.

import (
	"errors"
	"image"
	"io"
)

// FrameHeader is a frame header, as specified in section 9.1.
type FrameHeader struct {
	KeyFrame          bool
	VersionNumber     uint8
	ShowFrame         bool
	FirstPartitionLen uint32
	Width             int
	Height            int
	XScale            uint8
	YScale            uint8
}

const (
	nSegment     = 4
	nSegmentProb = 3
)

// segmentHeader holds segment-related header information.
type segmentHeader struct {
	useSegment     bool
	updateMap      bool
	relativeDelta  bool
	quantizer      [nSegment]int8
	filterStrength [nSegment]int8
	prob           [nSegmentProb]uint8
}

const (
	nRefLFDelta  = 4
	nModeLFDelta = 4
)

// filterHeader holds filter-related header information.
type filterHeader struct {
	simple          bool
	level           int8
	sharpness       uint8
	useLFDelta      bool
	refLFDelta      [nRefLFDelta]int8
	modeLFDelta     [nModeLFDelta]int8
	perSegmentLevel [nSegment]int8
}

// mb is the per-macroblock decode state. A decoder maintains mbw+1 of these
// as it is decoding macroblocks left-to-right and top-to-bottom: mbw for the
// macroblocks in the row above, and one for the macroblock to the left.
type mb struct {
	// pred is the predictor mode for the 4 bottom or right 4x4 luma regions.
	pred [4]uint8
	// nzMask is a mask of 8 bits: 4 for the bottom or right 4x4 luma regions,
	// and 2 + 2 for the bottom or right 4x4 chroma regions. A 1 bit indicates
	// that region has non-zero coefficients.
	nzMask uint8
	// nzY16 is a 0/1 value that is 1 if the macroblock used Y16 prediction and
	// had non-zero coefficients.
	nzY16 uint8
}

// Decoder decodes one VP8 key frame as bands. Decoding consists of calling
// DecodeFrameHeader, Start and then NextBand until it returns io.EOF.
type Decoder struct {
	// ra, off and n are the input bitstream: the n bytes at off of ra that
	// have not been consumed yet.
	ra  io.ReaderAt
	off int64
	n   int
	// scratch is a scratch buffer.
	scratch [8]byte
	// img is the band buffer: bandMB+1 macroblock rows, the full padded
	// width. Its Rect is not meaningful; NextBand builds the views.
	img *image.YCbCr
	// mbw and mbh are the number of 16x16 macroblocks wide and high the image is.
	mbw, mbh int
	// frameHeader is the frame header.
	frameHeader FrameHeader
	// Other headers.
	segmentHeader segmentHeader
	filterHeader  filterHeader
	// The image data is divided into a number of independent partitions.
	// There is 1 "first partition" and between 1 and 8 "other partitions"
	// for coefficient data.
	fp  partition
	op  [8]partition
	nOP int
	// Quantization factors.
	quant [nSegment]quant
	// DCT/WHT coefficient decoding probabilities.
	tokenProb   [nPlane][nBand][nContext][nProb]uint8
	useSkipProb bool
	skipProb    uint8
	// Loop filter parameters. perMBFilterParams covers the macroblock row
	// being decoded.
	filterParams      [nSegment][2]filterParam
	perMBFilterParams []filterParam

	// topY, topCb and topCr are the unfiltered bottom rows of the macroblock
	// row above the one being decoded.
	topY, topCb, topCr []uint8

	// Band state: bandMB is the number of macroblock rows per band, mby the
	// next macroblock row to decode, carry whether macroblock row slot
	// carrySlot of img (decoded but not final) has to move to slot 0 before
	// decoding continues, and done whether the last band has been returned.
	bandMB    int
	mby       int
	carry     bool
	carrySlot int
	done      bool
	err       error

	// The eight fields below relate to the current macroblock being decoded.
	//
	// Segment-based adjustments.
	segment int
	// Per-macroblock state for the macroblock immediately left of and those
	// macroblocks immediately above the current macroblock.
	leftMB mb
	upMB   []mb
	// Bitmasks for which 4x4 regions of coeff contain non-zero coefficients.
	nzDCMask, nzACMask uint32
	// Predictor modes.
	usePredY16 bool // The libwebp C code calls this !is_i4x4_.
	predY16    uint8
	predC8     uint8
	predY4     [4][4]uint8

	// The two fields below form a workspace for reconstructing a macroblock.
	// Their specific sizes are documented in reconstruct.go.
	coeff [1*16*16 + 2*8*8 + 1*4*4]int16
	ybr   [1 + 16 + 1 + 8][32]uint8
}

// NewDecoder returns a Decoder that reads the n bytes at off of ra. The
// caller guarantees that ra holds all of them.
func NewDecoder(ra io.ReaderAt, off int64, n int) *Decoder {
	return &Decoder{ra: ra, off: off, n: n}
}

// readFull reads exactly len(p) bytes into p.
func (d *Decoder) readFull(p []byte) error {
	if len(p) > d.n {
		return io.ErrUnexpectedEOF
	}
	n, err := d.ra.ReadAt(p, d.off)
	d.off += int64(n)
	d.n -= n
	if n == len(p) {
		return nil
	}
	if err == nil || err == io.EOF {
		err = io.ErrUnexpectedEOF
	}
	return err
}

// DecodeFrameHeader decodes the frame header.
func (d *Decoder) DecodeFrameHeader() (fh FrameHeader, err error) {
	// All frame headers are at least 3 bytes long.
	b := d.scratch[:3]
	if err = d.readFull(b); err != nil {
		return
	}
	d.frameHeader.KeyFrame = (b[0] & 1) == 0
	d.frameHeader.VersionNumber = (b[0] >> 1) & 7
	d.frameHeader.ShowFrame = (b[0]>>4)&1 == 1
	d.frameHeader.FirstPartitionLen = uint32(b[0])>>5 | uint32(b[1])<<3 | uint32(b[2])<<11
	if !d.frameHeader.KeyFrame {
		return d.frameHeader, nil
	}
	// Frame headers for key frames are an additional 7 bytes long.
	b = d.scratch[:7]
	if err = d.readFull(b); err != nil {
		return
	}
	// Check the magic sync code.
	if b[0] != 0x9d || b[1] != 0x01 || b[2] != 0x2a {
		err = errors.New("vp8: invalid format")
		return
	}
	d.frameHeader.Width = int(b[4]&0x3f)<<8 | int(b[3])
	d.frameHeader.Height = int(b[6]&0x3f)<<8 | int(b[5])
	d.frameHeader.XScale = b[4] >> 6
	d.frameHeader.YScale = b[6] >> 6
	d.mbw = (d.frameHeader.Width + 0x0f) >> 4
	d.mbh = (d.frameHeader.Height + 0x0f) >> 4
	d.segmentHeader = segmentHeader{
		prob: [3]uint8{0xff, 0xff, 0xff},
	}
	d.tokenProb = defaultTokenProb
	d.segment = 0
	return d.frameHeader, nil
}

// BufferBytes is the memory Start allocates for bands of bandMB macroblock
// rows: the band buffer, the line buffers and the partition windows.
func (d *Decoder) BufferBytes(bandMB int) int64 {
	mbw := int64(d.mbw)
	band := int64(bandMB+1) * mbw * (16*16 + 2*8*8)
	lines := mbw * (16 + 8 + 8 + 6 + 4)
	return band + lines + 9*partitionWindow
}

// parseSegmentHeader parses the segment header, as specified in section 9.3.
func (d *Decoder) parseSegmentHeader() {
	d.segmentHeader.useSegment = d.fp.readBit(uniformProb)
	if !d.segmentHeader.useSegment {
		d.segmentHeader.updateMap = false
		return
	}
	d.segmentHeader.updateMap = d.fp.readBit(uniformProb)
	if d.fp.readBit(uniformProb) {
		d.segmentHeader.relativeDelta = !d.fp.readBit(uniformProb)
		for i := range d.segmentHeader.quantizer {
			d.segmentHeader.quantizer[i] = int8(d.fp.readOptionalInt(uniformProb, 7))
		}
		for i := range d.segmentHeader.filterStrength {
			d.segmentHeader.filterStrength[i] = int8(d.fp.readOptionalInt(uniformProb, 6))
		}
	}
	if !d.segmentHeader.updateMap {
		return
	}
	for i := range d.segmentHeader.prob {
		if d.fp.readBit(uniformProb) {
			d.segmentHeader.prob[i] = uint8(d.fp.readUint(uniformProb, 8))
		} else {
			d.segmentHeader.prob[i] = 0xff
		}
	}
}

// parseFilterHeader parses the filter header, as specified in section 9.4.
func (d *Decoder) parseFilterHeader() {
	d.filterHeader.simple = d.fp.readBit(uniformProb)
	d.filterHeader.level = int8(d.fp.readUint(uniformProb, 6))
	d.filterHeader.sharpness = uint8(d.fp.readUint(uniformProb, 3))
	d.filterHeader.useLFDelta = d.fp.readBit(uniformProb)
	if d.filterHeader.useLFDelta && d.fp.readBit(uniformProb) {
		for i := range d.filterHeader.refLFDelta {
			d.filterHeader.refLFDelta[i] = int8(d.fp.readOptionalInt(uniformProb, 6))
		}
		for i := range d.filterHeader.modeLFDelta {
			d.filterHeader.modeLFDelta[i] = int8(d.fp.readOptionalInt(uniformProb, 6))
		}
	}
	if d.filterHeader.level == 0 {
		return
	}
	if d.segmentHeader.useSegment {
		for i := range d.filterHeader.perSegmentLevel {
			strength := d.segmentHeader.filterStrength[i]
			if d.segmentHeader.relativeDelta {
				strength += d.filterHeader.level
			}
			d.filterHeader.perSegmentLevel[i] = strength
		}
	} else {
		d.filterHeader.perSegmentLevel[0] = d.filterHeader.level
	}
	d.computeFilterParams()
}

// parseOtherPartitions parses the other partitions, as specified in section 9.5.
func (d *Decoder) parseOtherPartitions() error {
	const maxNOP = 1 << 3
	var partLens [maxNOP]int
	d.nOP = 1 << d.fp.readUint(uniformProb, 2)

	// The final partition length is implied by the remaining chunk data
	// (d.n) and the other d.nOP-1 partition lengths. Those d.nOP-1 partition
	// lengths are stored as 24-bit uints, i.e. up to 16 MiB per partition.
	n := 3 * (d.nOP - 1)
	partLens[d.nOP-1] = d.n - n
	if partLens[d.nOP-1] < 0 {
		return io.ErrUnexpectedEOF
	}
	if n > 0 {
		buf := make([]byte, n)
		if err := d.readFull(buf); err != nil {
			return err
		}
		for i := 0; i < d.nOP-1; i++ {
			pl := int(buf[3*i+0]) | int(buf[3*i+1])<<8 | int(buf[3*i+2])<<16
			if pl > partLens[d.nOP-1] {
				return io.ErrUnexpectedEOF
			}
			partLens[i] = pl
			partLens[d.nOP-1] -= pl
		}
	}

	// We check if the final partition length can also fit into a 24-bit uint.
	// Strictly speaking, this isn't part of the spec, but golang.org/x/image
	// rejects such files (it guards against reading too much into memory), so
	// this decoder does too.
	if 1<<24 <= partLens[d.nOP-1] {
		return errors.New("vp8: too much data to decode")
	}

	// The partitions follow each other in the rest of the chunk. They are
	// read through their windows as the macroblocks are decoded.
	off := d.off
	for i, pl := range partLens {
		if i == d.nOP {
			break
		}
		d.op[i].init(d.ra, off, pl)
		off += int64(pl)
	}
	d.off, d.n = off, 0
	return nil
}

// parseOtherHeaders parses header information other than the frame header.
func (d *Decoder) parseOtherHeaders() error {
	// Initialize and parse the first partition.
	if int64(d.frameHeader.FirstPartitionLen) > int64(d.n) {
		return io.ErrUnexpectedEOF
	}
	d.fp.init(d.ra, d.off, int(d.frameHeader.FirstPartitionLen))
	d.off += int64(d.frameHeader.FirstPartitionLen)
	d.n -= int(d.frameHeader.FirstPartitionLen)
	if d.frameHeader.KeyFrame {
		// Read and ignore the color space and pixel clamp values. They are
		// specified in section 9.2, but are unimplemented.
		d.fp.readBit(uniformProb)
		d.fp.readBit(uniformProb)
	}
	d.parseSegmentHeader()
	d.parseFilterHeader()
	if err := d.parseOtherPartitions(); err != nil {
		return err
	}
	d.parseQuant()
	if !d.frameHeader.KeyFrame {
		// Golden and AltRef frames are specified in section 9.7.
		// They are only used for video, not still images.
		return errors.New("vp8: Golden / AltRef frames are not implemented")
	}
	// Read and ignore the refreshLastFrameBuffer bit, specified in section 9.8.
	// It applies only to video, and not still images.
	d.fp.readBit(uniformProb)
	d.parseTokenProb()
	d.useSkipProb = d.fp.readBit(uniformProb)
	if d.useSkipProb {
		d.skipProb = uint8(d.fp.readUint(uniformProb, 8))
	}
	return d.inputErr()
}

// inputErr reports whether any partition ran out of data or failed to read.
func (d *Decoder) inputErr() error {
	if d.fp.err != nil {
		return d.fp.err
	}
	if d.fp.unexpectedEOF {
		return io.ErrUnexpectedEOF
	}
	for i := 0; i < d.nOP; i++ {
		if d.op[i].err != nil {
			return d.op[i].err
		}
		if d.op[i].unexpectedEOF {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

// Start parses the remaining headers and allocates the buffers for bands of
// bandMB macroblock rows (BufferBytes(bandMB) bytes).
func (d *Decoder) Start(bandMB int) error {
	if err := d.parseOtherHeaders(); err != nil {
		d.err = err
		return err
	}
	d.bandMB = max(bandMB, 1)
	d.img = image.NewYCbCr(image.Rect(0, 0, 16*d.mbw, 16*(d.bandMB+1)), image.YCbCrSubsampleRatio420)
	d.perMBFilterParams = make([]filterParam, d.mbw)
	d.upMB = make([]mb, d.mbw)
	d.topY = make([]uint8, 16*d.mbw)
	d.topCb = make([]uint8, 8*d.mbw)
	d.topCr = make([]uint8, 8*d.mbw)
	d.mby, d.carry, d.done = 0, false, false
	return nil
}

// decodeRow reconstructs macroblock row d.mby into macroblock row slot of
// the band buffer and applies the loop filter to it.
func (d *Decoder) decodeRow(slot int) {
	mby := d.mby
	d.leftMB = mb{}
	for mbx := 0; mbx < d.mbw; mbx++ {
		skip := d.reconstruct(mbx, mby, slot)
		fs := d.filterParams[d.segment][btou(!d.usePredY16)]
		fs.inner = fs.inner || !skip
		d.perMBFilterParams[mbx] = fs
	}
	// Apply the loop filter.
	//
	// Even if we are using per-segment levels, section 15 says that "loop
	// filtering must be skipped entirely if loop_filter_level at either the
	// frame header level or macroblock override level is 0".
	//
	// The filter visits macroblocks in raster order and, for one macroblock,
	// only touches that macroblock, the one to its left and the bottom rows
	// of the one above, so filtering each row right after reconstructing it
	// gives the same result as filtering the whole frame at the end.
	if d.filterHeader.level != 0 {
		if d.filterHeader.simple {
			d.simpleFilter(mby, slot)
		} else {
			d.normalFilter(mby, slot)
		}
	}
}

// NextBand decodes and returns the next band of the frame, in frame
// coordinates. The band is valid until the next call. It returns io.EOF after
// the last band; the last band is only returned once every partition has
// been checked for truncation.
func (d *Decoder) NextBand() (*image.YCbCr, error) {
	if d.err != nil {
		return nil, d.err
	}
	if d.done || d.img == nil {
		return nil, io.EOF
	}
	slot := 0
	if d.carry {
		// The last macroblock row decoded so far is not final: the filter of
		// the next row will change it. Move it to the front of the buffer.
		ys, cs := 16*d.img.YStride, 8*d.img.CStride
		copy(d.img.Y[:ys], d.img.Y[d.carrySlot*ys:])
		copy(d.img.Cb[:cs], d.img.Cb[d.carrySlot*cs:])
		copy(d.img.Cr[:cs], d.img.Cr[d.carrySlot*cs:])
		slot = 1
	}
	first := d.mby - slot // The macroblock row in slot 0.
	for slot <= d.bandMB && d.mby < d.mbh {
		d.decodeRow(slot)
		slot++
		d.mby++
	}
	n := slot
	if d.mby >= d.mbh {
		if err := d.inputErr(); err != nil {
			d.err = err
			return nil, err
		}
		d.done, d.carry = true, false
	} else {
		// Once a partition has run dry the frame cannot succeed.
		if err := d.inputErr(); err != nil {
			d.err = err
			return nil, err
		}
		n = slot - 1
		d.carry, d.carrySlot = true, slot-1
	}
	if n <= 0 {
		return nil, io.EOF
	}
	y0, y1 := 16*first, min(16*(first+n), d.frameHeader.Height)
	return &image.YCbCr{
		Y:              d.img.Y,
		Cb:             d.img.Cb,
		Cr:             d.img.Cr,
		YStride:        d.img.YStride,
		CStride:        d.img.CStride,
		SubsampleRatio: image.YCbCrSubsampleRatio420,
		Rect:           image.Rect(0, y0, d.frameHeader.Width, y1),
	}, nil
}
