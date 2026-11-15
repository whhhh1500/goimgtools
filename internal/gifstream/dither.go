// Copyright 2009 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

package gifstream

import (
	"image"
	"image/color"
	"sort"
)

// ditherer is the Floyd-Steinberg branch of image/draw's drawPaletted
// (go1.27) for an *image.Paletted destination, with the loop over rows
// turned inside out: the caller asks for one row of color indexes at a
// time, top to bottom. The arithmetic, the palette search and the order in
// which the error is spread are the standard library's, so the indexes are
// those draw.FloydSteinberg.Draw produces.
type ditherer struct {
	palette [][4]int32

	// quantErrorCurr and quantErrorNext are the Floyd-Steinberg quantization
	// errors that have been propagated to the pixels in the current and next
	// rows. The +2 simplifies calculation near the edges.
	quantErrorCurr, quantErrorNext [][4]int32

	// last is the palette index of the pixel before, where the search for
	// the next one starts.
	last int
	// plain turns the error diffusion off: every pixel gets the nearest
	// entry, as draw.Src gives it.
	plain bool
	// byGreen lists the palette indexes in order of their green value.
	byGreen []int
}

// quantErrorBytes is the size of one row of quantization error.
func quantErrorBytes(dx int) int64 { return (int64(dx) + 2) * 16 }

// newDitherer prepares dithering of rows of dx pixels to pal, whose
// entries must not be nil.
func newDitherer(pal color.Palette, dx int) *ditherer {
	d := &ditherer{
		palette:        make([][4]int32, len(pal)),
		quantErrorCurr: make([][4]int32, dx+2),
		quantErrorNext: make([][4]int32, dx+2),
	}
	for i, col := range pal {
		r, g, b, a := col.RGBA()
		d.palette[i][0] = int32(r)
		d.palette[i][1] = int32(g)
		d.palette[i][2] = int32(b)
		d.palette[i][3] = int32(a)
	}
	d.byGreen = make([]int, len(pal))
	for i := range d.byGreen {
		d.byGreen[i] = i
	}
	sort.SliceStable(d.byGreen, func(i, j int) bool { return d.palette[d.byGreen[i]][1] < d.palette[d.byGreen[j]][1] })
	return d
}

// pixelFunc returns the source pixel reader of drawPaletted for src.
func pixelFunc(src image.Image) func(x, y int) (r, g, b, a uint32) {
	// Fast paths for special cases to avoid excessive use of the color.Color
	// interface which escapes to the heap but need to be discovered for
	// each pixel on r. See also https://golang.org/issues/15759.
	switch src0 := src.(type) {
	case *image.RGBA:
		return func(x, y int) (r, g, b, a uint32) { return src0.RGBAAt(x, y).RGBA() }
	case *image.NRGBA:
		return func(x, y int) (r, g, b, a uint32) { return src0.NRGBAAt(x, y).RGBA() }
	case *image.YCbCr:
		return func(x, y int) (r, g, b, a uint32) { return src0.YCbCrAt(x, y).RGBA() }
	}
	return func(x, y int) (r, g, b, a uint32) { return src.At(x, y).RGBA() }
}

// row dithers the pixels (x0, y) ... (x0+len(pix)-1, y) into pix. Rows must
// be given in order, without gaps.
func (d *ditherer) row(pxRGBA func(x, y int) (r, g, b, a uint32), x0, y int, pix []byte) {
	palette := d.palette
	quantErrorCurr, quantErrorNext := d.quantErrorCurr, d.quantErrorNext
	for x := 0; x != len(pix); x++ {
		// er, eg and eb are the pixel's R,G,B values plus the
		// optional Floyd-Steinberg error.
		sr, sg, sb, sa := pxRGBA(x0+x, y)
		er, eg, eb, ea := int32(sr), int32(sg), int32(sb), int32(sa)
		er = clamp(er + quantErrorCurr[x+1][0]/16)
		eg = clamp(eg + quantErrorCurr[x+1][1]/16)
		eb = clamp(eb + quantErrorCurr[x+1][2]/16)
		ea = clamp(ea + quantErrorCurr[x+1][3]/16)

		// Find the closest palette color in Euclidean R,G,B,A space:
		// the one that minimizes sum-squared-difference.
		//
		// The standard library tries every entry and keeps the first of
		// the closest ones. The same entry is found faster here. The
		// search starts from the entry the previous pixel got (neighbours
		// are usually alike) and walks the palette in order of green,
		// outward from the pixel's own green: once an entry's green alone
		// is further away than the best so far, so is every entry beyond
		// it in that direction. Among equally close entries the lowest
		// index still wins.
		bestIndex := d.last
		p := &palette[bestIndex]
		bestSum := sqDiff(er, p[0]) + sqDiff(eg, p[1]) + sqDiff(eb, p[2]) + sqDiff(ea, p[3])
		try := func(k int) bool {
			index := d.byGreen[k]
			p := &palette[index]
			sum := sqDiff(eg, p[1])
			if sum > bestSum {
				return false
			}
			if sum += sqDiff(er, p[0]); sum > bestSum {
				return true
			}
			if sum += sqDiff(eb, p[2]); sum > bestSum {
				return true
			}
			sum += sqDiff(ea, p[3])
			if sum < bestSum || (sum == bestSum && index < bestIndex) {
				bestIndex, bestSum = index, sum
			}
			return true
		}
		// The first entry whose green is not below the pixel's.
		lo, hi := 0, len(palette)
		for lo < hi {
			if mid := (lo + hi) / 2; palette[d.byGreen[mid]][1] < eg {
				lo = mid + 1
			} else {
				hi = mid
			}
		}
		for k := lo; k < len(palette) && try(k); k++ {
		}
		for k := lo - 1; k >= 0 && try(k); k-- {
		}
		d.last = bestIndex
		pix[x] = byte(bestIndex)
		if d.plain {
			continue
		}

		er -= palette[bestIndex][0]
		eg -= palette[bestIndex][1]
		eb -= palette[bestIndex][2]
		ea -= palette[bestIndex][3]

		// Propagate the Floyd-Steinberg quantization error.
		quantErrorNext[x+0][0] += er * 3
		quantErrorNext[x+0][1] += eg * 3
		quantErrorNext[x+0][2] += eb * 3
		quantErrorNext[x+0][3] += ea * 3
		quantErrorNext[x+1][0] += er * 5
		quantErrorNext[x+1][1] += eg * 5
		quantErrorNext[x+1][2] += eb * 5
		quantErrorNext[x+1][3] += ea * 5
		quantErrorNext[x+2][0] += er * 1
		quantErrorNext[x+2][1] += eg * 1
		quantErrorNext[x+2][2] += eb * 1
		quantErrorNext[x+2][3] += ea * 1
		quantErrorCurr[x+2][0] += er * 7
		quantErrorCurr[x+2][1] += eg * 7
		quantErrorCurr[x+2][2] += eb * 7
		quantErrorCurr[x+2][3] += ea * 7
	}

	// Recycle the quantization error buffers.
	d.quantErrorCurr, d.quantErrorNext = quantErrorNext, quantErrorCurr
	clear(d.quantErrorNext)
}

// clamp clamps i to the interval [0, 0xffff].
func clamp(i int32) int32 {
	if i < 0 {
		return 0
	}
	if i > 0xffff {
		return 0xffff
	}
	return i
}

// sqDiff returns the squared-difference of x and y, shifted by 2 so that
// adding four of those won't overflow a uint32.
//
// x and y are both assumed to be in the range [0, 0xffff].
func sqDiff(x, y int32) uint32 {
	// This is an optimized code relying on the overflow/wrap around
	// properties of unsigned integers operations guaranteed by the language
	// spec. See sqDiff from the image/color package for more details.
	d := uint32(x - y)
	return (d * d) >> 2
}
