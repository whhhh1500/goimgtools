package vp8enc

// Chroma DC error diffusion, as libwebp does it below quality 99
// (src/enc/quant_enc.c: CorrectDCValues, QuantizeSingle,
// StoreDiffusionErrors). In a smooth colour gradient the DC of each chroma
// block is rounded the same way over large areas, which shows as bands;
// carrying each block's rounding error over to the blocks to its right and
// below, as a Floyd-Steinberg dither does, breaks the bands up. The
// errors go from block to block within a macroblock and on to the
// macroblocks to the right and below; they are halved to fit a byte.

const dcDiffuseQuality = 98 // the highest quality that diffuses

// diffuseDC adds to the DC of chroma block n (0..3: top left, top right,
// bottom left, bottom right) of plane pl the errors of the blocks above
// and to its left, quantizes that DC alone, and keeps its error in cd
// (for the blocks after it, and the macroblocks after this one if cd is
// chosen).
func (c *mbCoder) diffuseDC(coef *[16]int16, pl, n, mbx int, cd *candUV) {
	top := &c.e.topDerr[mbx][pl]
	left := &c.leftDerr[pl]
	e := &cd.derr[pl]
	var fromTop, fromLeft int32
	switch n {
	case 0:
		fromTop, fromLeft = int32(top[0]), int32(left[0])
	case 1:
		fromTop, fromLeft = int32(top[1]), int32(c.err0[pl])
	case 2:
		fromTop, fromLeft = int32(c.err0[pl]), int32(left[1])
	case 3:
		fromTop, fromLeft = int32(e[0]), int32(e[1])
	}
	coef[0] += int16((7*fromTop + 8*fromLeft) >> 3)
	err := quantizeDC(coef, &c.seg.qUV)
	if n == 0 {
		c.err0[pl] = err
	} else {
		e[n-1] = err
	}
}

// quantizeDC rounds the DC of a block to its quantized value, as the
// quantizer will, and returns half the error (libwebp's QuantizeSingle).
func quantizeDC(coef *[16]int16, z *quantizer) int8 {
	v := int32(coef[0])
	neg := v < 0
	if neg {
		v = -v
	}
	zthresh := ((1 << 17) - 1 - z.bias[0]) / z.iq[0]
	var err int32
	if v > zthresh {
		qv := ((v*z.iq[0] + z.bias[0]) >> 17) * z.q[0]
		err = v - qv
		v = qv
	} else {
		err, v = v, 0
	}
	if neg {
		err, v = -err, -v
	}
	coef[0] = int16(v)
	return int8(err >> 1)
}

// storeDerr passes the errors of the chosen chroma on: those of the right
// column to the macroblock to the right, those of the bottom row to the
// one below (libwebp's StoreDiffusionErrors).
func (c *mbCoder) storeDerr(mbx int, uv *candUV) {
	for pl := 0; pl < 2; pl++ {
		top := &c.e.topDerr[mbx][pl]
		left := &c.leftDerr[pl]
		e := &uv.derr[pl]
		left[0] = e[0]
		left[1] = int8(3 * int32(e[2]) >> 2)
		top[0] = e[1]
		top[1] = e[2] - left[1]
	}
}
