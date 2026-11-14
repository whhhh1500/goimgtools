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
	pix := t.pp
	p, mask := 4*t.oldWidth, int32(1)<<t.bits-1
	if y == 0 {
		// The first pixel's predictor is mode 0 (opaque black).
		pix[p+3] += 0xff
		p += 4
		for x := int32(1); x < t.oldWidth; x++ {
			// The rest of the first row's predictor is mode 1 (L).
			pix[p+0] += pix[p-4]
			pix[p+1] += pix[p-3]
			pix[p+2] += pix[p-2]
			pix[p+3] += pix[p-1]
			p += 4
		}
		return
	}
	top := 0
	{
		// The first column's predictor is mode 2 (T).
		pix[p+0] += pix[top+0]
		pix[p+1] += pix[top+1]
		pix[p+2] += pix[top+2]
		pix[p+3] += pix[top+3]
		p, top = p+4, top+4

		q := 0
		predictorMode := t.tileRow[q+1] & 0x0f
		q += 4
		for x := int32(1); x < t.oldWidth; x++ {
			if x&mask == 0 {
				predictorMode = t.tileRow[q+1] & 0x0f
				q += 4
			}
			switch predictorMode {
			case 0: // Opaque black.
				pix[p+3] += 0xff

			case 1: // L.
				pix[p+0] += pix[p-4]
				pix[p+1] += pix[p-3]
				pix[p+2] += pix[p-2]
				pix[p+3] += pix[p-1]

			case 2: // T.
				pix[p+0] += pix[top+0]
				pix[p+1] += pix[top+1]
				pix[p+2] += pix[top+2]
				pix[p+3] += pix[top+3]

			case 3: // TR.
				pix[p+0] += pix[top+4]
				pix[p+1] += pix[top+5]
				pix[p+2] += pix[top+6]
				pix[p+3] += pix[top+7]

			case 4: // TL.
				pix[p+0] += pix[top-4]
				pix[p+1] += pix[top-3]
				pix[p+2] += pix[top-2]
				pix[p+3] += pix[top-1]

			case 5: // Average2(Average2(L, TR), T).
				pix[p+0] += avg2(avg2(pix[p-4], pix[top+4]), pix[top+0])
				pix[p+1] += avg2(avg2(pix[p-3], pix[top+5]), pix[top+1])
				pix[p+2] += avg2(avg2(pix[p-2], pix[top+6]), pix[top+2])
				pix[p+3] += avg2(avg2(pix[p-1], pix[top+7]), pix[top+3])

			case 6: // Average2(L, TL).
				pix[p+0] += avg2(pix[p-4], pix[top-4])
				pix[p+1] += avg2(pix[p-3], pix[top-3])
				pix[p+2] += avg2(pix[p-2], pix[top-2])
				pix[p+3] += avg2(pix[p-1], pix[top-1])

			case 7: // Average2(L, T).
				pix[p+0] += avg2(pix[p-4], pix[top+0])
				pix[p+1] += avg2(pix[p-3], pix[top+1])
				pix[p+2] += avg2(pix[p-2], pix[top+2])
				pix[p+3] += avg2(pix[p-1], pix[top+3])

			case 8: // Average2(TL, T).
				pix[p+0] += avg2(pix[top-4], pix[top+0])
				pix[p+1] += avg2(pix[top-3], pix[top+1])
				pix[p+2] += avg2(pix[top-2], pix[top+2])
				pix[p+3] += avg2(pix[top-1], pix[top+3])

			case 9: // Average2(T, TR).
				pix[p+0] += avg2(pix[top+0], pix[top+4])
				pix[p+1] += avg2(pix[top+1], pix[top+5])
				pix[p+2] += avg2(pix[top+2], pix[top+6])
				pix[p+3] += avg2(pix[top+3], pix[top+7])

			case 10: // Average2(Average2(L, TL), Average2(T, TR)).
				pix[p+0] += avg2(avg2(pix[p-4], pix[top-4]), avg2(pix[top+0], pix[top+4]))
				pix[p+1] += avg2(avg2(pix[p-3], pix[top-3]), avg2(pix[top+1], pix[top+5]))
				pix[p+2] += avg2(avg2(pix[p-2], pix[top-2]), avg2(pix[top+2], pix[top+6]))
				pix[p+3] += avg2(avg2(pix[p-1], pix[top-1]), avg2(pix[top+3], pix[top+7]))

			case 11: // Select(L, T, TL).
				l0 := int32(pix[p-4])
				l1 := int32(pix[p-3])
				l2 := int32(pix[p-2])
				l3 := int32(pix[p-1])
				c0 := int32(pix[top-4])
				c1 := int32(pix[top-3])
				c2 := int32(pix[top-2])
				c3 := int32(pix[top-1])
				t0 := int32(pix[top+0])
				t1 := int32(pix[top+1])
				t2 := int32(pix[top+2])
				t3 := int32(pix[top+3])
				l := abs(c0-t0) + abs(c1-t1) + abs(c2-t2) + abs(c3-t3)
				t := abs(c0-l0) + abs(c1-l1) + abs(c2-l2) + abs(c3-l3)
				if l < t {
					pix[p+0] += uint8(l0)
					pix[p+1] += uint8(l1)
					pix[p+2] += uint8(l2)
					pix[p+3] += uint8(l3)
				} else {
					pix[p+0] += uint8(t0)
					pix[p+1] += uint8(t1)
					pix[p+2] += uint8(t2)
					pix[p+3] += uint8(t3)
				}

			case 12: // ClampAddSubtractFull(L, T, TL).
				pix[p+0] += clampAddSubtractFull(pix[p-4], pix[top+0], pix[top-4])
				pix[p+1] += clampAddSubtractFull(pix[p-3], pix[top+1], pix[top-3])
				pix[p+2] += clampAddSubtractFull(pix[p-2], pix[top+2], pix[top-2])
				pix[p+3] += clampAddSubtractFull(pix[p-1], pix[top+3], pix[top-1])

			case 13: // ClampAddSubtractHalf(Average2(L, T), TL).
				pix[p+0] += clampAddSubtractHalf(avg2(pix[p-4], pix[top+0]), pix[top-4])
				pix[p+1] += clampAddSubtractHalf(avg2(pix[p-3], pix[top+1]), pix[top-3])
				pix[p+2] += clampAddSubtractHalf(avg2(pix[p-2], pix[top+2]), pix[top-2])
				pix[p+3] += clampAddSubtractHalf(avg2(pix[p-1], pix[top+3]), pix[top-1])
			}
			p, top = p+4, top+4
		}
	}
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

func avg2(a, b uint8) uint8 {
	return uint8((int32(a) + int32(b)) / 2)
}

func clampAddSubtractFull(a, b, c uint8) uint8 {
	x := int32(a) + int32(b) - int32(c)
	if x < 0 {
		return 0
	}
	if x > 255 {
		return 255
	}
	return uint8(x)
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
