// Copyright 2012 Google Inc. All Rights Reserved.
// Use of this source code is governed by a BSD-style license that can be
// found in the licenses/LICENSE-LIBWEBP file.

package webpstream

import "slices"

// rescaler is libwebp's WebPRescaler (src/utils/rescaler_utils.c and the
// plain C of src/dsp/rescaler.c), which scales a plane while it is
// decoded, row by row, in 32-bit fixed point: when reducing, each value
// out is the average of the area of the plane it covers (the samples at
// its edges weighted by how much of them it covers); when enlarging,
// bilinear interpolation.
//
// Rows go in with importRow while pending is false and come out with
// exportRow while it is true; the order of the two is so forced, and the
// values out do not depend on how the rows are fed.
type rescaler struct {
	xExpand, yExpand bool
	ch               int // interleaved channels
	fxScale          uint32
	fyScale          uint32
	fxyScale         uint32
	yAccum           int
	yAdd, ySub       int
	xAdd, xSub       int
	srcW, srcH       int
	dstW, dstH       int
	srcY, dstY       int
	irow, frow       []uint32
}

const (
	rescalerOne     = uint64(1) << 32
	rescalerRounder = rescalerOne >> 1
)

// frac is WEBP_RESCALER_FRAC(x, y): x/y in 32-bit fixed point.
func frac(x, y uint64) uint32 { return uint32((x << 32) / y) }

func multFix(x, y uint32) uint32 { return uint32((uint64(x)*uint64(y) + rescalerRounder) >> 32) }

func multFixFloor(x, y uint32) uint32 { return uint32((uint64(x) * uint64(y)) >> 32) }

// newRescaler is WebPRescalerInit, for a plane of ch channels.
func newRescaler(srcW, srcH, dstW, dstH, ch int) *rescaler {
	r := &rescaler{
		xExpand: srcW < dstW,
		yExpand: srcH < dstH,
		ch:      ch,
		srcW:    srcW, srcH: srcH,
		dstW: dstW, dstH: dstH,
	}
	r.xAdd, r.xSub = srcW, dstW
	if r.xExpand {
		// Bilinear interpolation.
		r.xAdd, r.xSub = dstW-1, srcW-1
	} else {
		r.fxScale = frac(1, uint64(r.xSub))
	}
	r.yAdd, r.ySub = srcH, dstH
	if r.yExpand {
		r.yAdd, r.ySub = srcH-1, dstH-1
		r.yAccum = r.ySub
		r.fyScale = frac(1, uint64(r.xAdd))
	} else {
		r.yAccum = r.yAdd
		// dstH / (xAdd*yAdd); when it is one (a plane one wide, kept as
		// high) it does not fit, and exportRow copies instead.
		ratio := (uint64(dstH) << 32) / (uint64(r.xAdd) * uint64(r.yAdd))
		if ratio == uint64(uint32(ratio)) {
			r.fxyScale = uint32(ratio)
		}
		r.fyScale = frac(1, uint64(r.ySub))
	}
	work := make([]uint32, 2*ch*dstW)
	r.irow, r.frow = work[:ch*dstW], work[ch*dstW:]
	return r
}

// workBytes is the memory of a rescaler's two rows.
func workBytes(dstW, ch int) int64 { return 8 * int64(dstW) * int64(ch) }

// pending reports whether a row is ready to go out.
func (r *rescaler) pending() bool { return r.dstY < r.dstH && r.yAccum <= 0 }

// importRow is WebPRescalerImport for one row.
func (r *rescaler) importRow(src []byte) {
	if r.yExpand {
		r.irow, r.frow = r.frow, r.irow
	}
	if r.xExpand {
		r.importExpand(src)
	} else {
		r.importShrink(src)
	}
	if !r.yExpand {
		for x, v := range r.frow {
			r.irow[x] += v
		}
	}
	r.srcY++
	r.yAccum -= r.ySub
}

func (r *rescaler) importExpand(src []byte) {
	stride, xMax := r.ch, r.dstW*r.ch
	xAdd, xSub := uint32(r.xAdd), r.xSub
	for c := 0; c < stride; c++ {
		xIn, xOut := c, c
		accum := r.xAdd
		left := uint32(src[xIn])
		right := left
		if r.srcW > 1 {
			right = uint32(src[xIn+stride])
		}
		xIn += stride
		for {
			r.frow[xOut] = right*xAdd + (left-right)*uint32(accum)
			xOut += stride
			if xOut >= xMax {
				break
			}
			accum -= xSub
			if accum < 0 {
				left = right
				xIn += stride
				right = uint32(src[xIn])
				accum += r.xAdd
			}
		}
	}
}

func (r *rescaler) importShrink(src []byte) {
	switch r.ch {
	case 1:
		r.importShrink1(src)
		return
	case 4:
		r.importShrink4(src)
		return
	}
	stride, xMax := r.ch, r.dstW*r.ch
	xSub := uint32(r.xSub)
	for c := 0; c < stride; c++ {
		xIn, xOut := c, c
		var sum uint32
		accum := 0
		for xOut < xMax {
			var base uint32
			accum += r.xAdd
			for accum > 0 {
				accum -= r.xSub
				base = uint32(src[xIn])
				sum += base
				xIn += stride
			}
			// The part of the last sample that belongs to the next value.
			f := base * uint32(-accum)
			r.frow[xOut] = sum*xSub - f
			sum = multFix(f, r.fxScale)
			xOut += stride
		}
	}
}

// importShrink1 is importShrink for one channel (a plane of a lossy
// image).
func (r *rescaler) importShrink1(src []byte) {
	src = src[:r.srcW]
	frow := r.frow[:r.dstW]
	xAdd, xSub := r.xAdd, r.xSub
	var sum uint32
	accum, xIn := 0, 0
	for xOut := range frow {
		var base uint32
		accum += xAdd
		for accum > 0 {
			accum -= xSub
			base = uint32(src[xIn])
			sum += base
			xIn++
		}
		f := base * uint32(-accum)
		frow[xOut] = sum*uint32(xSub) - f
		sum = multFix(f, r.fxScale)
	}
}

// importShrink4 is importShrink for four channels (a lossless image), the
// channels side by side: each goes through the same steps.
func (r *rescaler) importShrink4(src []byte) {
	src = src[:4*r.srcW]
	frow := r.frow[:4*r.dstW]
	xAdd, xSub, fx := r.xAdd, r.xSub, r.fxScale
	var s0, s1, s2, s3 uint32
	accum, xIn := 0, 0
	for xOut := 0; xOut+3 < len(frow); xOut += 4 {
		var b0, b1, b2, b3 uint32
		accum += xAdd
		for accum > 0 {
			accum -= xSub
			px := src[xIn : xIn+4]
			b0, b1, b2, b3 = uint32(px[0]), uint32(px[1]), uint32(px[2]), uint32(px[3])
			s0, s1, s2, s3 = s0+b0, s1+b1, s2+b2, s3+b3
			xIn += 4
		}
		k, m := uint32(-accum), uint32(xSub)
		f0, f1, f2, f3 := b0*k, b1*k, b2*k, b3*k
		out := frow[xOut : xOut+4]
		out[0], out[1], out[2], out[3] = s0*m-f0, s1*m-f1, s2*m-f2, s3*m-f3
		s0, s1, s2, s3 = multFix(f0, fx), multFix(f1, fx), multFix(f2, fx), multFix(f3, fx)
	}
}

// exportRow is WebPRescalerExportRow: it appends the next row out to dst.
func (r *rescaler) exportRow(dst []byte) []byte {
	n := r.ch * r.dstW
	at := len(dst)
	dst = slices.Grow(dst, n)[:at+n]
	out := dst[at:]
	switch {
	case r.yExpand:
		if r.yAccum == 0 {
			for x, j := range r.frow {
				out[x] = clip255(multFix(j, r.fyScale))
			}
		} else {
			b := frac(uint64(-r.yAccum), uint64(r.ySub))
			a := uint32(rescalerOne - uint64(b))
			for x := range out {
				i := uint64(a)*uint64(r.frow[x]) + uint64(b)*uint64(r.irow[x])
				j := uint32((i + rescalerRounder) >> 32)
				out[x] = clip255(multFix(j, r.fyScale))
			}
		}
	case r.fxyScale != 0:
		yscale := r.fyScale * uint32(-r.yAccum)
		if yscale != 0 {
			for x := range out {
				f := multFixFloor(r.frow[x], yscale)
				out[x] = clip255(multFix(r.irow[x]-f, r.fxyScale))
				r.irow[x] = f
			}
		} else {
			for x := range out {
				out[x] = clip255(multFix(r.irow[x], r.fxyScale))
				r.irow[x] = 0
			}
		}
	default:
		// One sample wide and as high as the source: the sums are the
		// values.
		for x := range out {
			out[x] = uint8(r.irow[x])
			r.irow[x] = 0
		}
	}
	r.yAccum += r.yAdd
	r.dstY++
	return dst
}

func clip255(v uint32) uint8 {
	if int32(v) > 255 {
		return 255
	}
	return uint8(v)
}

// plane is a rescaler with the rows it has given out and that have not
// been handed on yet.
type plane struct {
	r *rescaler
	q []byte
}

// push feeds the plane one row, taking out first the rows it has ready.
func (p *plane) push(row []byte) {
	for p.r.pending() {
		p.q = p.r.exportRow(p.q)
	}
	p.r.importRow(row)
}

// flush takes out the rows the plane has ready.
func (p *plane) flush() {
	for p.r.pending() {
		p.q = p.r.exportRow(p.q)
	}
}

// rows is how many rows out the plane holds.
func (p *plane) rows() int { return len(p.q) / (p.r.ch * p.r.dstW) }

// drop forgets the first n rows out.
func (p *plane) drop(n int) {
	p.q = p.q[:copy(p.q, p.q[n*p.r.ch*p.r.dstW:])]
}

// Premultiplying and unpremultiplying by alpha, as libwebp's
// WebPMultARGBRow (src/dsp/alpha_processing.c), in 24-bit fixed point.
const (
	alphaHalf = 1 << 23
	kinv255   = (1 << 24) / 255
)

// premultiply multiplies the colour of the RGBA pixels of row by their
// alpha.
func premultiply(row []byte) {
	for i := 0; i+3 < len(row); i += 4 {
		a := uint32(row[i+3])
		switch a {
		case 255:
		case 0:
			row[i], row[i+1], row[i+2] = 0, 0, 0
		default:
			s := a * kinv255
			row[i] = uint8((uint32(row[i])*s + alphaHalf) >> 24)
			row[i+1] = uint8((uint32(row[i+1])*s + alphaHalf) >> 24)
			row[i+2] = uint8((uint32(row[i+2])*s + alphaHalf) >> 24)
		}
	}
}

// unpremultiply divides the colour of the RGBA pixels of row by their
// alpha.
func unpremultiply(row []byte) {
	for i := 0; i+3 < len(row); i += 4 {
		a := uint32(row[i+3])
		switch a {
		case 255:
		case 0:
			row[i], row[i+1], row[i+2] = 0, 0, 0
		default:
			s := (255 << 24) / a
			row[i] = uint8((uint32(row[i])*s + alphaHalf) >> 24)
			row[i+1] = uint8((uint32(row[i+1])*s + alphaHalf) >> 24)
			row[i+2] = uint8((uint32(row[i+2])*s + alphaHalf) >> 24)
		}
	}
}
