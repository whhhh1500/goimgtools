// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE-GO file.

// Package vp8l implements a decoder for the VP8L lossless image format.
//
// The VP8L specification is at:
// https://developers.google.com/speed/webp/docs/riff_container
//
// It is a fork of golang.org/x/image/vp8l that produces the image one row at
// a time instead of as one whole *image.NRGBA. The bit-stream decoding and
// the arithmetic are unchanged, so the pixels are identical. What differs:
//
//   - the entropy-decoded pixels go into a ring buffer instead of a buffer
//     for the whole image. An LZ77 backward reference reaches at most 2^20
//     pixels back (the largest value of a distance code), so the ring holds
//     that many pixels plus one row and one maximal copy: about 4.2MB for
//     any image, and less when the image is smaller than that;
//   - the color cache is brought up to date at least once per row, not only
//     when it is next used, because older pixels leave the ring;
//   - the inverse transforms work on one row (see transform.go);
//   - the sub-images (transform tiles, Huffman group map) are held in a
//     pipeline.Scratch, which is a temporary file when the budget is short;
//   - every buffer is accounted for in the memory budget.
package vp8l

import (
	"errors"
	"io"

	"github.com/whhhh1500/goimgtools/pipeline"
)

var (
	errInvalidCodeLengths = errors.New("vp8l: invalid code lengths")
	errInvalidHuffmanTree = errors.New("vp8l: invalid Huffman tree")
)

// colorCacheMultiplier is the multiplier used for the color cache hash
// function, specified in section 4.2.3.
const colorCacheMultiplier = 0x1e35a7bd

// distanceMapTable is the look-up table for distanceMap.
var distanceMapTable = [120]uint8{
	0x18, 0x07, 0x17, 0x19, 0x28, 0x06, 0x27, 0x29, 0x16, 0x1a,
	0x26, 0x2a, 0x38, 0x05, 0x37, 0x39, 0x15, 0x1b, 0x36, 0x3a,
	0x25, 0x2b, 0x48, 0x04, 0x47, 0x49, 0x14, 0x1c, 0x35, 0x3b,
	0x46, 0x4a, 0x24, 0x2c, 0x58, 0x45, 0x4b, 0x34, 0x3c, 0x03,
	0x57, 0x59, 0x13, 0x1d, 0x56, 0x5a, 0x23, 0x2d, 0x44, 0x4c,
	0x55, 0x5b, 0x33, 0x3d, 0x68, 0x02, 0x67, 0x69, 0x12, 0x1e,
	0x66, 0x6a, 0x22, 0x2e, 0x54, 0x5c, 0x43, 0x4d, 0x65, 0x6b,
	0x32, 0x3e, 0x78, 0x01, 0x77, 0x79, 0x53, 0x5d, 0x11, 0x1f,
	0x64, 0x6c, 0x42, 0x4e, 0x76, 0x7a, 0x21, 0x2f, 0x75, 0x7b,
	0x31, 0x3f, 0x63, 0x6d, 0x52, 0x5e, 0x00, 0x74, 0x7c, 0x41,
	0x4f, 0x10, 0x20, 0x62, 0x6e, 0x30, 0x73, 0x7d, 0x51, 0x5f,
	0x40, 0x72, 0x7e, 0x61, 0x6f, 0x50, 0x71, 0x7f, 0x60, 0x70,
}

// distanceMap maps a LZ77 backwards reference distance to a two-dimensional
// pixel offset, specified in section 4.2.2.
func distanceMap(w int32, code uint32) int32 {
	if int32(code) > int32(len(distanceMapTable)) {
		return int32(code) - int32(len(distanceMapTable))
	}
	distCode := int32(distanceMapTable[code-1])
	yOffset := distCode >> 4
	xOffset := 8 - distCode&0xf
	if d := yOffset*w + xOffset; d >= 1 {
		return d
	}
	return 1
}

const (
	// maxLZ77Length is the largest length lz77Param can return for a length
	// symbol (23): (3<<10) + (1<<10 - 1) + 1.
	maxLZ77Length = 1 << 12
	// maxLZ77Distance is the largest distance, in pixels, that distanceMap
	// can return: lz77Param gives at most (3<<18) + (1<<18 - 1) + 1 for a
	// distance symbol (39), and the 120 short codes reach at most 7 rows and
	// 8 pixels back, which is less for any valid width.
	maxLZ77Distance = 1 << 20

	// readBufSize is the size of the bit-stream read buffer.
	readBufSize = 16 << 10
)

// testWindow, when positive, replaces maxLZ77Distance, so that tests can make
// the ring wrap on small images. A backward reference that reaches further
// than the ring then fails with errWindow.
var testWindow int64

var errWindow = errors.New("vp8l: LZ77 distance exceeds the pixel window")

// decoder holds the bit-stream for a VP8L image, and the memory accounting
// of everything decoded from it.
type decoder struct {
	// ra, off and end locate the bytes not read into buf yet.
	ra       io.ReaderAt
	off, end int64
	// buf[r:w] are the bytes read but not consumed.
	buf   []byte
	r, w  int
	bits  uint32
	nBits uint32

	env *pipeline.Env
	// reserved is what this decoder holds of the budget.
	reserved int64
	// scratches are the sub-image stores to delete on Close.
	scratches []*pipeline.Scratch
}

func (d *decoder) reserve(n int64, what string) error {
	if err := d.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	d.reserved += n
	return nil
}

func (d *decoder) release(n int64) {
	d.env.Budget.Release(n)
	d.reserved -= n
}

// readByte returns the next byte of the bit-stream, or io.EOF at its end.
func (d *decoder) readByte() (byte, error) {
	if d.r == d.w {
		if err := d.fill(); err != nil {
			return 0, err
		}
	}
	c := d.buf[d.r]
	d.r++
	return c, nil
}

func (d *decoder) fill() error {
	if d.off >= d.end {
		return io.EOF
	}
	if d.buf == nil {
		d.buf = make([]byte, readBufSize)
	}
	n := int(min(int64(len(d.buf)), d.end-d.off))
	k, err := d.ra.ReadAt(d.buf[:n], d.off)
	if k == 0 {
		if err == nil {
			err = io.ErrNoProgress
		}
		return err
	}
	d.off += int64(k)
	d.r, d.w = 0, k
	return nil
}

// read reads the next n bits from the decoder's bit-stream.
func (d *decoder) read(n uint32) (uint32, error) {
	for d.nBits < n {
		c, err := d.readByte()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		d.bits |= uint32(c) << d.nBits
		d.nBits += 8
	}
	u := d.bits & (1<<n - 1)
	d.bits >>= n
	d.nBits -= n
	return u, nil
}

// decodeTransform decodes the next transform and the width of the image after
// transformation (or equivalently, before inverse transformation), specified
// in section 3.
func (d *decoder) decodeTransform(w int32, h int32) (t transform, newWidth int32, err error) {
	t.oldWidth = w
	t.tileRowY = -1
	t.transformType, err = d.read(2)
	if err != nil {
		return transform{}, 0, err
	}
	switch t.transformType {
	case transformTypePredictor, transformTypeCrossColor:
		t.bits, err = d.read(3)
		if err != nil {
			return transform{}, 0, err
		}
		t.bits += 2
		t.tilesPerRow = nTiles(w, t.bits)
		t.tiles, err = d.decodeSubImage(t.tilesPerRow, nTiles(h, t.bits))
		if err != nil {
			return transform{}, 0, err
		}
		need := 4 * int64(t.tilesPerRow)
		if t.transformType == transformTypePredictor {
			need += 8 * int64(w)
		}
		if err := d.reserve(need, "vp8l transform rows"); err != nil {
			return transform{}, 0, err
		}
		t.tileRow = make([]byte, 4*t.tilesPerRow)
		if t.transformType == transformTypePredictor {
			t.pp = make([]byte, 8*w)
		}
	case transformTypeSubtractGreen:
		// No-op.
	case transformTypeColorIndexing:
		nColors, err := d.read(8)
		if err != nil {
			return transform{}, 0, err
		}
		nColors++
		t.bits = 0
		switch {
		case nColors <= 2:
			t.bits = 3
		case nColors <= 4:
			t.bits = 2
		case nColors <= 16:
			t.bits = 1
		}
		w = nTiles(w, t.bits)
		// The spec says that "if the index is equal or larger than color_table_size,
		// the argb color value should be set to 0x00000000 (transparent black)."
		// The palette has room for 256 4-byte pixels.
		pix := make([]byte, 4*256)
		ps, err := d.newPixels(int32(nColors), 1, false)
		if err != nil {
			return transform{}, 0, err
		}
		err = ps.row(0, pix[:4*nColors])
		ps.release()
		if err != nil {
			return transform{}, 0, err
		}
		for p := 4; p < 4*int(nColors); p += 4 {
			pix[p+0] += pix[p-4]
			pix[p+1] += pix[p-3]
			pix[p+2] += pix[p-2]
			pix[p+3] += pix[p-1]
		}
		t.pix = pix
		if t.bits != 0 {
			if err := d.reserve(4*int64(t.oldWidth), "vp8l transform rows"); err != nil {
				return transform{}, 0, err
			}
			t.dst = make([]byte, 4*t.oldWidth)
		}
	}
	return t, w, nil
}

// decodeSubImage decodes a w by h sub-image (transform tiles or the Huffman
// group map) into a store of 4 bytes per pixel.
func (d *decoder) decodeSubImage(w, h int32) (*pipeline.Scratch, error) {
	ps, err := d.newPixels(w, h, false)
	if err != nil {
		return nil, err
	}
	defer ps.release()
	sc, err := pipeline.NewScratch(d.env, 4*ps.total)
	if err != nil {
		return nil, err
	}
	d.scratches = append(d.scratches, sc)
	if ps.ringPix == ps.total {
		// The ring holds the whole sub-image and never wraps.
		if err := ps.run(ps.total); err != nil {
			return nil, err
		}
		if _, err := sc.WriteAt(ps.ring, 0); err != nil {
			return nil, err
		}
		return sc, nil
	}
	row := make([]byte, 4*w)
	for y := int32(0); y < h; y++ {
		if err := ps.row(y, row); err != nil {
			return nil, err
		}
		if _, err := sc.WriteAt(row, int64(y)*int64(len(row))); err != nil {
			return nil, err
		}
	}
	return sc, nil
}

// repeatsCodeLength is the minimum code length for repeated codes.
const repeatsCodeLength = 16

// These magic numbers are specified at the end of section 5.2.2.
// The 3-length arrays apply to code lengths >= repeatsCodeLength.
var (
	codeLengthCodeOrder = [19]uint8{
		17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15,
	}
	repeatBits    = [3]uint8{2, 3, 7}
	repeatOffsets = [3]uint8{3, 3, 11}
)

// decodeCodeLengths decodes a Huffman tree's code lengths which are themselves
// encoded via a Huffman tree, specified in section 5.2.2.
func (d *decoder) decodeCodeLengths(dst []uint32, codeLengthCodeLengths []uint32) error {
	h := hTree{}
	if err := h.build(codeLengthCodeLengths); err != nil {
		return err
	}

	maxSymbol := len(dst)
	useLength, err := d.read(1)
	if err != nil {
		return err
	}
	if useLength != 0 {
		n, err := d.read(3)
		if err != nil {
			return err
		}
		n = 2 + 2*n
		ms, err := d.read(n)
		if err != nil {
			return err
		}
		maxSymbol = int(ms) + 2
		if maxSymbol > len(dst) {
			return errInvalidCodeLengths
		}
	}

	// The spec says that "if code 16 [meaning repeat] is used before
	// a non-zero value has been emitted, a value of 8 is repeated."
	prevCodeLength := uint32(8)

	for symbol := 0; symbol < len(dst); {
		if maxSymbol == 0 {
			break
		}
		maxSymbol--
		codeLength, err := h.next(d)
		if err != nil {
			return err
		}
		if codeLength < repeatsCodeLength {
			dst[symbol] = codeLength
			symbol++
			if codeLength != 0 {
				prevCodeLength = codeLength
			}
			continue
		}

		repeat, err := d.read(uint32(repeatBits[codeLength-repeatsCodeLength]))
		if err != nil {
			return err
		}
		repeat += uint32(repeatOffsets[codeLength-repeatsCodeLength])
		if symbol+int(repeat) > len(dst) {
			return errInvalidCodeLengths
		}
		// A code length of 16 repeats the previous non-zero code.
		// A code length of 17 or 18 repeats zeroes.
		cl := uint32(0)
		if codeLength == 16 {
			cl = prevCodeLength
		}
		for ; repeat > 0; repeat-- {
			dst[symbol] = cl
			symbol++
		}
	}
	return nil
}

// decodeHuffmanTree decodes a Huffman tree into h.
// h may be nil, in which case we decode and discard the tree.
func (d *decoder) decodeHuffmanTree(h *hTree, alphabetSize uint32) error {
	useSimple, err := d.read(1)
	if err != nil {
		return err
	}
	if useSimple != 0 {
		nSymbols, err := d.read(1)
		if err != nil {
			return err
		}
		nSymbols++
		firstSymbolLengthCode, err := d.read(1)
		if err != nil {
			return err
		}
		firstSymbolLengthCode = 7*firstSymbolLengthCode + 1
		var symbols [2]uint32
		symbols[0], err = d.read(firstSymbolLengthCode)
		if err != nil {
			return err
		}
		if nSymbols == 2 {
			symbols[1], err = d.read(8)
			if err != nil {
				return err
			}
		}
		if h == nil {
			return nil
		}
		return h.buildSimple(nSymbols, symbols, alphabetSize)
	}

	nCodes, err := d.read(4)
	if err != nil {
		return err
	}
	nCodes += 4
	if int(nCodes) > len(codeLengthCodeOrder) {
		return errInvalidHuffmanTree
	}
	codeLengthCodeLengths := [len(codeLengthCodeOrder)]uint32{}
	for i := uint32(0); i < nCodes; i++ {
		codeLengthCodeLengths[codeLengthCodeOrder[i]], err = d.read(3)
		if err != nil {
			return err
		}
	}
	codeLengths := make([]uint32, alphabetSize)
	if err = d.decodeCodeLengths(codeLengths, codeLengthCodeLengths[:]); err != nil {
		return err
	}
	if h == nil {
		return nil
	}
	return h.build(codeLengths)
}

const (
	huffGreen    = 0
	huffRed      = 1
	huffBlue     = 2
	huffAlpha    = 3
	huffDistance = 4
	nHuff        = 5
)

// hGroup is an array of 5 Huffman trees.
type hGroup [nHuff]hTree

const (
	// hGroupBytes is the size of an hGroup without its nodes.
	hGroupBytes = nHuff * (24 + 4<<lutSize)
	// hNodeBytes is the size of an hNode.
	hNodeBytes = 8
)

// decodeHuffmanGroups decodes the one or more hGroups used to decode the pixel
// data. If one hGroup is used for the entire image, then s.hStore will be nil
// and s.hBits zero. If more than one hGroup is used, then s.hStore contains
// the meta-image that maps tiles to hGroup index, and s.hBits contains the
// log-2 tile size.
func (s *pixels) decodeHuffmanGroups(topLevel bool, ccBits uint32) error {
	d, w, h := s.d, s.w, s.h

	// hStore maps tiles to hGroup indices.
	// The number of hGroups is determined by the maximum index.
	// VP8L permits unused hGroups in the input.
	// While the number of used hGroups is bounded by the number of tiles,
	// the number of decoded hGroups is only bounded by the maximum hGroup index (2^16-1).
	//
	// When the total number of hGroups is large, we store only the referenced hGroups
	// and rewrite hGroup indices in hStore.
	//
	// When mapping is nil, we do no remapping.
	// Otherwise, for hGroup i (where i is the real index of the group),
	// mapping[i] is -1 for an unreferenced group or the remapped index otherwise.
	var mapping []int16

	maxHGroupIndex := 0
	numHGroups := int16(1)

	if topLevel {
		useMeta, err := d.read(1)
		if err != nil {
			return err
		}
		if useMeta != 0 {
			hBits, err := d.read(3)
			if err != nil {
				return err
			}
			hBits += 2
			tileW := nTiles(w, hBits)
			tileH := nTiles(h, hBits)
			numTiles := tileH * tileW
			hStore, err := d.decodeSubImage(tileW, tileH)
			if err != nil {
				return err
			}
			if err := s.reserve(4*int64(tileW), "vp8l Huffman group row"); err != nil {
				return err
			}
			hRow := make([]byte, 4*tileW)
			for ty := int32(0); ty < tileH; ty++ {
				if _, err := hStore.ReadAt(hRow, int64(ty)*int64(len(hRow))); err != nil {
					return err
				}
				for p := 0; p < len(hRow); p += 4 {
					i := int(hRow[p])<<8 | int(hRow[p+1])
					if maxHGroupIndex < i {
						maxHGroupIndex = i
					}
				}
			}

			// The standard allows for up to 2^16-1 hGroups.
			//
			// An input file containing a 1600x900 image can include
			// and reference every possible group. With maximum-size trees
			// (a ~12MiB input file), this is >3GiB of allocation.
			//
			// libwebp will decode a file containing 2^16-1 hGroups,
			// but it won't produce one. It encodes at most 2600 hGroups
			// (MAX_HUFF_IMAGE_SIZE, src/dec/common_dec.h)
			// Reject any image using more.
			const maxHuffImageSize = 2600
			const _ int16 = maxHuffImageSize + 1 // must fit in int16
			if maxHGroupIndex >= maxHuffImageSize {
				return errors.New("vp8l: too many Huffman trees")
			}

			// If maxHGroupIndex is too large, map indices to contiguous indices
			// to avoid allocating large slices of unused Huffman trees.
			//
			// The definition of "too large" matches the one used in libwebp
			// (see src/dec/vp8l_dec.c).
			if maxHGroupIndex >= 1000 || int32(maxHGroupIndex) >= numTiles {
				mapping = make([]int16, maxHGroupIndex+1)
				for i := range mapping {
					mapping[i] = -1
				}
				numHGroups = 0
				for ty := int32(0); ty < tileH; ty++ {
					off := int64(ty) * int64(len(hRow))
					if _, err := hStore.ReadAt(hRow, off); err != nil {
						return err
					}
					for p := 0; p < len(hRow); p += 4 {
						i := int(hRow[p])<<8 | int(hRow[p+1])
						if mapping[i] == -1 {
							mapping[i] = numHGroups
							numHGroups++
						}
						hRow[p] = byte(mapping[i] >> 8)
						hRow[p+1] = byte(mapping[i])
					}
					if _, err := hStore.WriteAt(hRow, off); err != nil {
						return err
					}
				}
			} else {
				numHGroups = int16(maxHGroupIndex + 1)
			}
			s.hStore, s.hBits, s.hRow = hStore, hBits, hRow
		}
	}

	if err := s.reserve(int64(numHGroups)*hGroupBytes, "vp8l Huffman groups"); err != nil {
		return err
	}
	hGroups := make([]hGroup, numHGroups)
	for i := range maxHGroupIndex + 1 {
		var hg *hGroup
		if mapping == nil {
			hg = &hGroups[i]
		} else if mapping[i] != -1 {
			hg = &hGroups[mapping[i]]
		}

		for j, alphabetSize := range alphabetSizes {
			if j == 0 && ccBits > 0 {
				alphabetSize += 1 << ccBits
			}
			var hTreeDst *hTree
			if hg != nil {
				hTreeDst = &hg[j]
			}
			if err := d.decodeHuffmanTree(hTreeDst, alphabetSize); err != nil {
				return err
			}
			if hTreeDst != nil {
				if err := s.reserve(int64(cap(hTreeDst.nodes))*hNodeBytes, "vp8l Huffman trees"); err != nil {
					return err
				}
			}
		}
	}
	s.hGroups = hGroups
	return nil
}

const (
	nLiteralCodes  = 256
	nLengthCodes   = 24
	nDistanceCodes = 40
)

var alphabetSizes = [nHuff]uint32{
	nLiteralCodes + nLengthCodes,
	nLiteralCodes,
	nLiteralCodes,
	nLiteralCodes,
	nDistanceCodes,
}

// pixels is the state of decoding one image's pixel data, specified in
// section 5.2.2: the main image or a sub-image. It is the body of
// golang.org/x/image/vp8l's decodePix made resumable, with the pixels going
// into a ring buffer.
type pixels struct {
	d     *decoder
	w, h  int32
	total int64 // w*h

	// The color cache.
	ccShift   uint32
	ccEntries []uint32

	// The Huffman groups. hRow is the row of the group map (hStore) numbered
	// hRowY, or -1.
	hGroups     []hGroup
	hStore      *pipeline.Scratch
	hBits       uint32
	hMask       int32
	tilesPerRow int32
	hRow        []byte
	hRowY       int32

	// ring holds the last ringPix pixels, 4 bytes each in R, G, B, A order;
	// pixel number i is at 4*(i%ringPix).
	ring    []byte
	ringPix int64

	// p is the number of pixels decoded, pos the ring offset of pixel p.
	// cachedP is the number of pixels entered in the color cache, cpos the
	// ring offset of pixel cachedP.
	p, cachedP int64
	pos, cpos  int
	x, y       int32
	hg         *hGroup
	lookupHG   bool

	// reserved is what this image holds of the budget.
	reserved int64
}

func (s *pixels) reserve(n int64, what string) error {
	if err := s.d.reserve(n, what); err != nil {
		return err
	}
	s.reserved += n
	return nil
}

// release gives back the image's buffers. The group map stays with the
// decoder until Close.
func (s *pixels) release() {
	s.d.release(s.reserved)
	s.reserved = 0
	s.ring, s.hGroups, s.ccEntries, s.hRow = nil, nil, nil, nil
}

// newPixels reads the color cache parameters and Huffman groups of a w by h
// image and gets ready to decode its pixels.
func (d *decoder) newPixels(w int32, h int32, topLevel bool) (_ *pixels, err error) {
	s := &pixels{d: d, w: w, h: h, total: int64(w) * int64(h), hRowY: -1}
	defer func() {
		if err != nil {
			s.release()
		}
	}()

	// Decode the color cache parameters.
	ccBits := uint32(0)
	useColorCache, err := d.read(1)
	if err != nil {
		return nil, err
	}
	if useColorCache != 0 {
		ccBits, err = d.read(4)
		if err != nil {
			return nil, err
		}
		if ccBits < 1 || 11 < ccBits {
			return nil, errors.New("vp8l: invalid color cache parameters")
		}
		s.ccShift = 32 - ccBits
		if err := s.reserve(4<<ccBits, "vp8l color cache"); err != nil {
			return nil, err
		}
		s.ccEntries = make([]uint32, 1<<ccBits)
	}

	// Decode the Huffman groups.
	if err := s.decodeHuffmanGroups(topLevel, ccBits); err != nil {
		return nil, err
	}
	if s.hBits != 0 {
		s.hMask, s.tilesPerRow = 1<<s.hBits-1, nTiles(w, s.hBits)
	}

	// The ring keeps what a backward reference can reach, plus the row being
	// completed (a row is taken out as soon as it is complete) and the
	// longest copy, which can overshoot the end of that row.
	window := int64(maxLZ77Distance)
	if testWindow > 0 {
		window = testWindow
	}
	s.ringPix = min(s.total, window+int64(w)+maxLZ77Length)
	if err := s.reserve(4*s.ringPix, "vp8l pixel window"); err != nil {
		return nil, err
	}
	s.ring = make([]byte, 4*s.ringPix)
	s.hg, s.lookupHG = &s.hGroups[0], s.hMask != 0
	return s, nil
}

// row decodes up to the end of row y and copies that row into dst. Rows must
// be asked for in increasing order, each at most once.
func (s *pixels) row(y int32, dst []byte) error {
	if err := s.run(int64(y+1) * int64(s.w)); err != nil {
		return err
	}
	start := int(int64(y) * int64(s.w) % s.ringPix * 4)
	n := copy(dst, s.ring[start:])
	copy(dst[n:], s.ring)
	return nil
}

// updateColorCache inserts the pixels decoded since the last update into the
// color cache. Note that VP8L assumes ARGB order, but the ring is in RGBA
// order.
func (s *pixels) updateColorCache() {
	ring, cpos := s.ring, s.cpos
	for ; s.cachedP < s.p; s.cachedP++ {
		argb := uint32(ring[cpos+0])<<16 |
			uint32(ring[cpos+1])<<8 |
			uint32(ring[cpos+2])<<0 |
			uint32(ring[cpos+3])<<24
		s.ccEntries[(argb*colorCacheMultiplier)>>s.ccShift] = argb
		cpos += 4
		if cpos == len(ring) {
			cpos = 0
		}
	}
	s.cpos = cpos
}

// run decodes pixels until at least target of them are decoded. It can
// overshoot by less than maxLZ77Length pixels.
func (s *pixels) run(target int64) error {
	d := s.d
	ring, w, hMask := s.ring, s.w, s.hMask
	p, pos := s.p, s.pos
	x, y := s.x, s.y
	hg, lookupHG := s.hg, s.lookupHG
	for p < target {
		if lookupHG {
			if ty := y >> s.hBits; ty != s.hRowY {
				if _, err := s.hStore.ReadAt(s.hRow, int64(ty)*int64(len(s.hRow))); err != nil {
					return err
				}
				s.hRowY = ty
			}
			i := 4 * (x >> s.hBits)
			hg = &s.hGroups[uint32(s.hRow[i])<<8|uint32(s.hRow[i+1])]
		}

		green, err := hg[huffGreen].next(d)
		if err != nil {
			return err
		}
		switch {
		case green < nLiteralCodes:
			// We have a literal pixel.
			red, err := hg[huffRed].next(d)
			if err != nil {
				return err
			}
			blue, err := hg[huffBlue].next(d)
			if err != nil {
				return err
			}
			alpha, err := hg[huffAlpha].next(d)
			if err != nil {
				return err
			}
			ring[pos+0] = uint8(red)
			ring[pos+1] = uint8(green)
			ring[pos+2] = uint8(blue)
			ring[pos+3] = uint8(alpha)
			pos += 4
			if pos == len(ring) {
				pos = 0
			}
			p++

			x++
			if x == w {
				x, y = 0, y+1
			}
			lookupHG = hMask != 0 && x&hMask == 0

		case green < nLiteralCodes+nLengthCodes:
			// We have a LZ77 backwards reference.
			length, err := d.lz77Param(green - nLiteralCodes)
			if err != nil {
				return err
			}
			distSym, err := hg[huffDistance].next(d)
			if err != nil {
				return err
			}
			distCode, err := d.lz77Param(distSym)
			if err != nil {
				return err
			}
			dist := distanceMap(w, distCode)
			// The copy must stay inside the image and its source must not
			// start before the first pixel. A valid source is always still
			// in the ring: dist <= maxLZ77Distance < ringPix, or the ring
			// holds the whole image.
			if s.total < p+int64(length) || p < int64(dist) {
				return errors.New("vp8l: invalid LZ77 parameters")
			}
			if s.ringPix < int64(dist) {
				return errWindow // Only with testWindow.
			}
			q := pos - 4*int(dist)
			if q < 0 {
				q += len(ring)
			}
			for n := 4 * int(length); n > 0; {
				k := min(n, len(ring)-pos, len(ring)-q)
				dst, src := ring[pos:pos+k], ring[q:q+k]
				if q < pos && pos-q < k {
					// The source overlaps the destination from behind:
					// bytes written by this copy are read again by it.
					for i := range dst {
						dst[i] = src[i]
					}
				} else {
					copy(dst, src)
				}
				n -= k
				if pos += k; pos == len(ring) {
					pos = 0
				}
				if q += k; q == len(ring) {
					q = 0
				}
			}
			p += int64(length)

			x += int32(length)
			for x >= w {
				x, y = x-w, y+1
			}
			lookupHG = hMask != 0

		default:
			// We have a color cache lookup. First, insert previous pixels
			// into the cache.
			s.p = p
			if s.ccEntries != nil {
				s.updateColorCache()
			}
			green -= nLiteralCodes + nLengthCodes
			if int(green) >= len(s.ccEntries) {
				return errors.New("vp8l: invalid color cache index")
			}
			argb := s.ccEntries[green]
			ring[pos+0] = uint8(argb >> 16)
			ring[pos+1] = uint8(argb >> 8)
			ring[pos+2] = uint8(argb >> 0)
			ring[pos+3] = uint8(argb >> 24)
			pos += 4
			if pos == len(ring) {
				pos = 0
			}
			p++

			x++
			if x == w {
				x, y = 0, y+1
			}
			lookupHG = hMask != 0 && x&hMask == 0
		}
	}
	s.p, s.pos = p, pos
	s.x, s.y = x, y
	s.hg, s.lookupHG = hg, lookupHG
	// Enter the new pixels in the color cache while they are in the ring.
	// golang.org/x/image/vp8l does this only before a cache lookup; the cache
	// contents at that point are the same either way.
	if s.ccEntries != nil {
		s.updateColorCache()
	}
	return nil
}

// lz77Param returns the next LZ77 parameter: a length or a distance, specified
// in section 4.2.2.
func (d *decoder) lz77Param(symbol uint32) (uint32, error) {
	if symbol < 4 {
		return symbol + 1, nil
	}
	extraBits := (symbol - 2) >> 1
	offset := (2 + symbol&1) << extraBits
	n, err := d.read(extraBits)
	if err != nil {
		return 0, err
	}
	return offset + n + 1, nil
}

// Decoder decodes one VP8L image row by row.
type Decoder struct {
	d    decoder
	w, h int32

	started     bool
	nTransforms int
	transforms  [nTransformTypes]transform
	pix         *pixels
	// row is the entropy-decoded row, before the inverse transforms.
	row []byte
	y   int32
	err error
}

// NewDecoder returns a Decoder for the VP8L bit-stream in bytes [off, end)
// of ra. The caller guarantees that ra holds all of them. Call DecodeHeader
// or SetSize next.
func NewDecoder(ra io.ReaderAt, off, end int64, env *pipeline.Env) *Decoder {
	return &Decoder{d: decoder{ra: ra, off: off, end: end, env: env}}
}

// DecodeHeader decodes the VP8L header and returns the image size.
func (z *Decoder) DecodeHeader() (w, h int, err error) {
	d := &z.d
	magic, err := d.read(8)
	if err != nil {
		return 0, 0, err
	}
	if magic != 0x2f {
		return 0, 0, errors.New("vp8l: invalid header")
	}
	width, err := d.read(14)
	if err != nil {
		return 0, 0, err
	}
	width++
	height, err := d.read(14)
	if err != nil {
		return 0, 0, err
	}
	height++
	_, err = d.read(1) // Read and ignore the hasAlpha hint.
	if err != nil {
		return 0, 0, err
	}
	version, err := d.read(3)
	if err != nil {
		return 0, 0, err
	}
	if version != 0 {
		return 0, 0, errors.New("vp8l: invalid version")
	}
	z.w, z.h = int32(width), int32(height)
	return int(width), int(height), nil
}

// SetSize sets the image size for a bit-stream that has no header (the alpha
// plane of a lossy image). Both must be in [1, 16384].
func (z *Decoder) SetSize(w, h int) { z.w, z.h = int32(w), int32(h) }

// start decodes the transforms and gets ready for the first row.
func (z *Decoder) start() error {
	d, w, h := &z.d, z.w, z.h
	if err := d.reserve(readBufSize, "vp8l read buffer"); err != nil {
		return err
	}
	// Decode the transforms.
	var transformsSeen [nTransformTypes]bool
	for {
		more, err := d.read(1)
		if err != nil {
			return err
		}
		if more == 0 {
			break
		}
		var t transform
		t, w, err = d.decodeTransform(w, h)
		if err != nil {
			return err
		}
		if transformsSeen[t.transformType] {
			return errors.New("vp8l: repeated transform")
		}
		transformsSeen[t.transformType] = true
		z.transforms[z.nTransforms] = t
		z.nTransforms++
	}
	// Get ready to decode the transformed pixels.
	pix, err := d.newPixels(w, h, true)
	if err != nil {
		return err
	}
	z.pix = pix
	if err := d.reserve(4*int64(w), "vp8l row"); err != nil {
		return err
	}
	z.row = make([]byte, 4*w)
	return nil
}

// NextRow decodes the next row and returns its pixels: 4 bytes each, in
// R, G, B, A order, not premultiplied. The slice is valid until the next
// call. It returns io.EOF after the last row.
func (z *Decoder) NextRow() ([]byte, error) {
	if z.err != nil {
		return nil, z.err
	}
	if !z.started {
		z.started = true
		if z.err = z.start(); z.err != nil {
			return nil, z.err
		}
	}
	if z.y >= z.h {
		return nil, io.EOF
	}
	row := z.row
	if z.err = z.pix.row(z.y, row); z.err != nil {
		return nil, z.err
	}
	// Apply the inverse transformations.
	for i := z.nTransforms - 1; i >= 0; i-- {
		row, z.err = z.transforms[i].inverse(row, z.y)
		if z.err != nil {
			return nil, z.err
		}
	}
	z.y++
	return row, nil
}

// Close releases the decoder's memory and deletes its temporary files. It is
// safe to call more than once.
func (z *Decoder) Close() error {
	d := &z.d
	var err error
	for _, sc := range d.scratches {
		if cerr := sc.Close(); err == nil {
			err = cerr
		}
	}
	d.scratches = nil
	d.env.Budget.Release(d.reserved)
	d.reserved = 0
	z.pix, z.row, d.buf = nil, nil, nil
	z.transforms = [nTransformTypes]transform{}
	z.nTransforms = 0
	if z.err == nil {
		z.err = errors.New("vp8l: decoder closed")
	}
	return err
}
