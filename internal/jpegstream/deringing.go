package jpegstream

import "math"

// Overshoot deringing, as mozjpeg does it before the forward DCT
// (jcdctmgr.c: preprocess_deringing; BSD-style licence, see
// licenses/LICENSE-LIBJPEG-TURBO). In a block with pixels at the largest
// value (white, or a channel at 255) and others below it, the JPEG of the
// edge between them rings: the coefficients that quantization drops leave
// ripples, and those below white show as grey ghosts beside dark text and
// lines. Since a decoder clamps whatever comes out above 255, the white
// pixels can be given any value above it at no cost to how they look; a
// smooth curve over each run of them (in zig-zag order, a Catmull-Rom
// spline from the slopes at its ends, capped) is a shape the DCT codes with
// less high frequency, so less of it rings. The arithmetic is mozjpeg's,
// in float32 with every product rounded before it is added, so a block
// comes out the same on every processor (and as mozjpeg built for x86-64).

// deringMax is the largest sample, level shifted as the DCT sees it.
const deringMax = 255 - 128

// dering changes the samples of b (natural order, 0 to 255) as mozjpeg's
// preprocess_deringing does, q0 being the DC quantizer of its table, and
// reports whether it changed any. Changed samples can be above 255 (at most
// 255+31).
func dering(b *block, q0 int32) bool {
	const size = blockSize
	var sum, count int32
	for _, v := range b {
		v -= 128
		sum += v
		if v >= deringMax {
			count++
		}
	}
	// Nothing at the largest value, or nothing else: no edge to ring.
	if count == 0 || count == size {
		return false
	}
	// Overshooting costs bits in proportion to the quantization (the DC
	// quantizer as a rough guide), and the DC must not overflow: at most
	// what the block's mean is below the largest value.
	maxOvershoot := deringMax + min(31, 2*q0, (deringMax*size-sum)/count)
	at := func(k int) int32 { return b[unzig[k]] - 128 }
	for n := 0; n < size; {
		if at(n) < deringMax {
			n++
			continue
		}
		// A run of the largest value, from start to before end, in
		// zig-zag order.
		start := n
		for n++; n < size && at(n) >= deringMax; n++ {
		}
		end := n
		// The slopes at either end, from the pixels around the run (as
		// mozjpeg picks them, by hand).
		f1, f2 := at(max(start-1, 0)), at(0)
		if start >= 2 {
			f2 = at(start - 2)
		}
		l1, l2 := at(min(end, size-1)), at(size-1)
		if end < size-2 {
			l2 = at(end + 1)
		}
		fslope := max(f1-f2, deringMax-f1)
		lslope := max(l1-l2, deringMax-l1)
		// A run at either end of the block: the curve made symmetric.
		if start == 0 {
			fslope = lslope
		}
		if end == size {
			lslope = fslope
		}
		// The curve fits better without the first and the last pixel.
		length := int32(end - start)
		step := 1 / float32(length+1)
		pos := step
		for i := start; i < end; i, pos = i+1, pos+step {
			v := int32(math.Ceil(float64(catmullRom(deringMax-fslope, deringMax, deringMax, deringMax-lslope, pos, length))))
			b[unzig[i]] = min(v, maxOvershoot) + 128
		}
		n++
	}
	return true
}

// catmullRom is mozjpeg's catmull_rom: the curve through v2 and v3 with
// the slopes v1 and v4 give, times size, at t (0 to 1), in float32 as
// mozjpeg computes it: every operation rounded to float32, none fused.
func catmullRom(v1, v2, v3, v4 int32, t float32, size int32) float32 {
	tan1 := (v3 - v1) * size
	tan2 := (v4 - v2) * size
	t2 := float32(t * t)
	t3 := float32(t2 * t)
	f1 := float32(2*t3) - float32(3*t2) + 1
	f2 := float32(-2*t3) + float32(3*t2)
	f3 := t3 - float32(2*t2) + t
	f4 := t3 - t2
	r := float32(float32(v2) * f1)
	r += float32(float32(tan1) * f3)
	r += float32(float32(v3) * f2)
	r += float32(float32(tan2) * f4)
	return r
}
