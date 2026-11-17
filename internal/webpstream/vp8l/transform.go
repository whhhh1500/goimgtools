// Copyright 2014 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package vp8l

// This file deals with image transforms, specified in section 3.
//
// golang.org/x/image/vp8l undoes each transform over the whole image. Here
// every transform is undone one row at a time: the predictor transform only
// looks at the row above (kept in transform.pp), the others at nothing but
// the pixel itself and the transform's tile data, of which one row of tiles
// is in memory (transform.tileRow).

import (
	"encoding/binary"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// nTiles returns the number of tiles needed to cover size pixels, where each
// tile's side is 1<<bits pixels long.
func nTiles(size int32, bits uint32) int32 {
	return (size + 1<<bits - 1) >> bits
}

const (
	transformTypePredictor     = 0
	transformTypeCrossColor    = 1
	transformTypeSubtractGreen = 2
	transformTypeColorIndexing = 3
	nTransformTypes            = 4
)

// transform holds the parameters for an invertible transform.
type transform struct {
	// transformType is the type of the transform.
	transformType uint32
	// oldWidth is the width of the image before transformation (or
	// equivalently, after inverse transformation). The color-indexing
	// transform can reduce the width. For example, a 50-pixel-wide
	// image that only needs 4 bits (half a byte) per color index can
	// be transformed into a 25-pixel-wide image.
	oldWidth int32
	// bits is the log-2 size of the transform's tiles, for the predictor
	// and cross-color transforms. 8>>bits is the number of bits per
	// color index, for the color-index transform.
	bits uint32
	// pix is the color palette, for the color-index transform.
	pix []byte
	// tiles holds the tile values, for the predictor and cross-color
	// transforms: tilesPerRow 4-byte values per row of tiles. It is on disk
	// when it does not fit the memory budget. tileRow is the row of tiles
	// numbered tileRowY (-1 when none is loaded).
	tiles       *pipeline.Scratch
	tilesPerRow int32
	tileRow     []byte
	tileRowY    int32
	// pp is the predictor transform's workspace: the previous row after
	// inverse prediction followed by the row being worked on, 4*oldWidth
	// bytes each, so that the two rows are laid out exactly as two
	// consecutive rows of the whole image are in golang.org/x/image/vp8l.
	pp []byte
	// dst is the row the color-index transform unpacks into.
	dst []byte
}

// loadTiles makes tileRow the row of tiles that covers pixel row y.
func (t *transform) loadTiles(y int32) error {
	ty := y >> t.bits
	if ty == t.tileRowY {
		return nil
	}
	if _, err := t.tiles.ReadAt(t.tileRow, int64(ty)*int64(len(t.tileRow))); err != nil {
		return err
	}
	t.tileRowY = ty
	return nil
}

// inverse undoes the transform for row y, given as row, and returns the
// result, which is row itself or one of the transform's buffers.
func (t *transform) inverse(row []byte, y int32) ([]byte, error) {
	switch t.transformType {
	case transformTypePredictor:
		if y > 0 {
			if err := t.loadTiles(y); err != nil {
				return nil, err
			}
		}
		cur := t.pp[4*t.oldWidth:]
		copy(cur, row)
		inversePredictor(t, y)
		// Later transforms change cur in place, so keep a copy for the row
		// below now.
		copy(t.pp, cur)
		return cur, nil
	case transformTypeCrossColor:
		if err := t.loadTiles(y); err != nil {
			return nil, err
		}
		inverseCrossColor(t, row)
		return row, nil
	case transformTypeSubtractGreen:
		inverseSubtractGreen(row)
		return row, nil
	}
	return inverseColorIndexing(t, row), nil
}

// inversePredictor undoes the predictor transform for row y, which is the
// second half of t.pp; the first half is the row above.
func inversePredictor(t *transform, y int32) {
	w := t.oldWidth
	pix := t.pp
	cur := pix[4*w : 8*w]
	if y == 0 {
		// The first pixel's predictor is mode 0 (opaque black), the rest
		// of the first row's mode 1 (L).
		cur[3] += 0xff
		predictRun(1, pix, w, 1, w)
		return
	}
	// The first column's predictor is mode 2 (T).
	put32(cur, 0, addPixels(le32(cur, 0), le32(pix, 0)))
	// Each tile of the row in one go, with its mode.
	for x := int32(1); x < w; {
		end := min((x>>t.bits+1)<<t.bits, w)
		predictRun(t.tileRow[4*(x>>t.bits)+1]&0x0f, pix, w, x, end)
		x = end
	}
}

// predictRun undoes the prediction of mode for the pixels x0 to x1 of the
// row (the second row of pix, w pixels wide, under the row above).
// Modes 14 and 15 leave the pixels as they are.
func predictRun(mode uint8, pix []byte, w, x0, x1 int32) {
	p, top := int(4*(w+x0)), int(4*x0)
	n := int(x1 - x0)
	switch mode {
	case 0: // Opaque black.
		for i := 0; i < n; i++ {
			pix[p+4*i+3] += 0xff
		}
	case 1: // L.
		left := le32(pix, p-4)
		for i := 0; i < n; i, p = i+1, p+4 {
			left = addPixels(le32(pix, p), left)
			put32(pix, p, left)
		}
	case 2: // T.
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			put32(pix, p, addPixels(le32(pix, p), le32(pix, top)))
		}
	case 3: // TR.
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			put32(pix, p, addPixels(le32(pix, p), le32(pix, top+4)))
		}
	case 4: // TL.
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			put32(pix, p, addPixels(le32(pix, p), le32(pix, top-4)))
		}
	case 5: // Average2(Average2(L, TR), T).
		left := le32(pix, p-4)
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			left = addPixels(le32(pix, p), average2(average2(left, le32(pix, top+4)), le32(pix, top)))
			put32(pix, p, left)
		}
	case 6: // Average2(L, TL).
		left := le32(pix, p-4)
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			left = addPixels(le32(pix, p), average2(left, le32(pix, top-4)))
			put32(pix, p, left)
		}
	case 7: // Average2(L, T).
		left := le32(pix, p-4)
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			left = addPixels(le32(pix, p), average2(left, le32(pix, top)))
			put32(pix, p, left)
		}
	case 8: // Average2(TL, T).
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			put32(pix, p, addPixels(le32(pix, p), average2(le32(pix, top-4), le32(pix, top))))
		}
	case 9: // Average2(T, TR).
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			put32(pix, p, addPixels(le32(pix, p), average2(le32(pix, top), le32(pix, top+4))))
		}
	case 10: // Average2(Average2(L, TL), Average2(T, TR)).
		left := le32(pix, p-4)
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			left = addPixels(le32(pix, p), average2(average2(left, le32(pix, top-4)), average2(le32(pix, top), le32(pix, top+4))))
			put32(pix, p, left)
		}
	case 11: // Select(L, T, TL).
		left := le32(pix, p-4)
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			tl, tp := le32(pix, top-4), le32(pix, top)
			pred := tp
			if sad(tl, tp) < sad(tl, left) {
				pred = left
			}
			left = addPixels(le32(pix, p), pred)
			put32(pix, p, left)
		}
	case 12: // ClampAddSubtractFull(L, T, TL).
		left := le32(pix, p-4)
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			tp, tl := le32(pix, top), le32(pix, top-4)
			// Each channel L+T-TL, kept to 0..255.
			c0 := left&0xff + tp&0xff - tl&0xff
			c1 := left>>8&0xff + tp>>8&0xff - tl>>8&0xff
			c2 := left>>16&0xff + tp>>16&0xff - tl>>16&0xff
			c3 := left>>24 + tp>>24 - tl>>24
			if (c0|c1|c2|c3)&^0xff != 0 {
				c0, c1, c2, c3 = clip255(c0), clip255(c1), clip255(c2), clip255(c3)
			}
			left = addPixels(le32(pix, p), c0|c1<<8|c2<<16|c3<<24)
			put32(pix, p, left)
		}
	case 13: // ClampAddSubtractHalf(Average2(L, T), TL).
		left := le32(pix, p-4)
		for i := 0; i < n; i, p, top = i+1, p+4, top+4 {
			left = addPixels(le32(pix, p), clampHalf(average2(left, le32(pix, top)), le32(pix, top-4)))
			put32(pix, p, left)
		}
	}
}

// The pixels are handled four bytes at a time, as little-endian words,
// each byte a channel.

func le32(b []byte, i int) uint32 { return binary.LittleEndian.Uint32(b[i:]) }

func put32(b []byte, i int, v uint32) { binary.LittleEndian.PutUint32(b[i:], v) }

// addPixels adds each channel of b to that of a, modulo 256.
func addPixels(a, b uint32) uint32 {
	ag := (a & 0xff00ff00) + (b & 0xff00ff00)
	rb := (a & 0x00ff00ff) + (b & 0x00ff00ff)
	return ag&0xff00ff00 | rb&0x00ff00ff
}

// average2 is the mean of each channel of a and b, rounded down.
func average2(a, b uint32) uint32 { return ((a^b)&0xfefefefe)>>1 + a&b }

// sad is the sum of the absolute differences of the channels of a and b.
func sad(a, b uint32) int32 {
	return abs(int32(a&0xff)-int32(b&0xff)) + abs(int32(a>>8&0xff)-int32(b>>8&0xff)) +
		abs(int32(a>>16&0xff)-int32(b>>16&0xff)) + abs(int32(a>>24)-int32(b>>24))
}

// clip255 keeps a channel worked out in uint32 to 0..255: below 0 it has
// wrapped round to above 2^31, and its complement is below 2^24.
func clip255(v uint32) uint32 {
	if v < 256 {
		return v
	}
	return ^v >> 24
}

// clampHalf is clampAddSubtractHalf of each channel.
func clampHalf(a, b uint32) uint32 {
	return uint32(clampAddSubtractHalf(uint8(a), uint8(b))) |
		uint32(clampAddSubtractHalf(uint8(a>>8), uint8(b>>8)))<<8 |
		uint32(clampAddSubtractHalf(uint8(a>>16), uint8(b>>16)))<<16 |
		uint32(clampAddSubtractHalf(uint8(a>>24), uint8(b>>24)))<<24
}

// inverseCrossColor undoes the cross-color transform for one row.
func inverseCrossColor(t *transform, pix []byte) {
	var greenToRed, greenToBlue, redToBlue int32
	p, mask := int32(0), int32(1)<<t.bits-1
	{
		q := 0
		for x := int32(0); x < t.oldWidth; x++ {
			if x&mask == 0 {
				redToBlue = int32(int8(t.tileRow[q+0]))
				greenToBlue = int32(int8(t.tileRow[q+1]))
				greenToRed = int32(int8(t.tileRow[q+2]))
				q += 4
			}
			red := pix[p+0]
			green := pix[p+1]
			blue := pix[p+2]
			red += uint8(uint32(greenToRed*int32(int8(green))) >> 5)
			blue += uint8(uint32(greenToBlue*int32(int8(green))) >> 5)
			blue += uint8(uint32(redToBlue*int32(int8(red))) >> 5)
			pix[p+0] = red
			pix[p+2] = blue
			p += 4
		}
	}
}

// inverseSubtractGreen undoes the subtract-green transform for one row.
func inverseSubtractGreen(pix []byte) {
	for p := 0; p < len(pix); p += 4 {
		green := pix[p+1]
		pix[p+0] += green
		pix[p+2] += green
	}
}

// inverseColorIndexing undoes the color-index transform for one row. A row
// starts a new packed value, so rows are independent.
func inverseColorIndexing(t *transform, pix []byte) []byte {
	if t.bits == 0 {
		for p := 0; p < len(pix); p += 4 {
			i := 4 * uint32(pix[p+1])
			pix[p+0] = t.pix[i+0]
			pix[p+1] = t.pix[i+1]
			pix[p+2] = t.pix[i+2]
			pix[p+3] = t.pix[i+3]
		}
		return pix
	}

	vMask, xMask, bitsPerPixel := uint32(0), int32(0), uint32(8>>t.bits)
	switch t.bits {
	case 1:
		vMask, xMask = 0x0f, 0x01
	case 2:
		vMask, xMask = 0x03, 0x03
	case 3:
		vMask, xMask = 0x01, 0x07
	}

	d, p, v, dst := 0, 0, uint32(0), t.dst
	{
		for x := int32(0); x < t.oldWidth; x++ {
			if x&xMask == 0 {
				v = uint32(pix[p+1])
				p += 4
			}

			i := 4 * (v & vMask)
			dst[d+0] = t.pix[i+0]
			dst[d+1] = t.pix[i+1]
			dst[d+2] = t.pix[i+2]
			dst[d+3] = t.pix[i+3]
			d += 4

			v >>= bitsPerPixel
		}
	}
	return dst
}

func abs(x int32) int32 {
	if x < 0 {
		return -x
	}
	return x
}

func clampAddSubtractHalf(a, b uint8) uint8 {
	x := int32(a) + (int32(a)-int32(b))/2
	if x < 0 {
		return 0
	}
	if x > 255 {
		return 255
	}
	return uint8(x)
}
