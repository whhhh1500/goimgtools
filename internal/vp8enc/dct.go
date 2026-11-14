package vp8enc

// The inverse transforms here repeat the decoder's arithmetic step for step
// (RFC 6386 sections 14.3 and 14.4): the encoder predicts later blocks from
// its own reconstruction, which therefore has to equal the decoder's output
// bit for bit. The forward transforms only need to be close inverses.
//
// Coefficients are in raster order: index = 4*vertical + horizontal frequency.

func clip8(i int32) uint8 {
	if uint32(i) < 256 {
		return uint8(i)
	}
	if i < 0 {
		return 0
	}
	return 255
}

// fdct transforms the 4x4 difference src-ref.
func fdct(src []uint8, sStride int, ref []uint8, rStride int, out *[16]int16) {
	var tmp [16]int32
	for i := 0; i < 4; i++ {
		s, r := src[i*sStride:i*sStride+4], ref[i*rStride:i*rStride+4]
		d0 := int32(s[0]) - int32(r[0])
		d1 := int32(s[1]) - int32(r[1])
		d2 := int32(s[2]) - int32(r[2])
		d3 := int32(s[3]) - int32(r[3])
		a0, a1, a2, a3 := d0+d3, d1+d2, d1-d2, d0-d3
		tmp[0+i*4] = (a0 + a1) * 8
		tmp[1+i*4] = (a2*2217 + a3*5352 + 1812) >> 9
		tmp[2+i*4] = (a0 - a1) * 8
		tmp[3+i*4] = (a3*2217 - a2*5352 + 937) >> 9
	}
	for i := 0; i < 4; i++ {
		a0 := tmp[0+i] + tmp[12+i]
		a1 := tmp[4+i] + tmp[8+i]
		a2 := tmp[4+i] - tmp[8+i]
		a3 := tmp[0+i] - tmp[12+i]
		out[0+i] = int16((a0 + a1 + 7) >> 4)
		v := (a2*2217 + a3*5352 + 12000) >> 16
		if a3 != 0 {
			v++
		}
		out[4+i] = int16(v)
		out[8+i] = int16((a0 - a1 + 7) >> 4)
		out[12+i] = int16((a3*2217 - a2*5352 + 51000) >> 16)
	}
}

// fwht is the forward Walsh-Hadamard transform of the 16 luma DC values.
func fwht(in *[16]int16, out *[16]int16) {
	var tmp [16]int32
	for i := 0; i < 4; i++ {
		a0 := int32(in[i*4+0]) + int32(in[i*4+2])
		a1 := int32(in[i*4+1]) + int32(in[i*4+3])
		a2 := int32(in[i*4+1]) - int32(in[i*4+3])
		a3 := int32(in[i*4+0]) - int32(in[i*4+2])
		tmp[0+i*4] = a0 + a1
		tmp[1+i*4] = a3 + a2
		tmp[2+i*4] = a3 - a2
		tmp[3+i*4] = a0 - a1
	}
	for i := 0; i < 4; i++ {
		a0 := tmp[0+i] + tmp[8+i]
		a1 := tmp[4+i] + tmp[12+i]
		a2 := tmp[4+i] - tmp[12+i]
		a3 := tmp[0+i] - tmp[8+i]
		out[0+i] = int16((a0 + a1) >> 1)
		out[4+i] = int16((a3 + a2) >> 1)
		out[8+i] = int16((a3 - a2) >> 1)
		out[12+i] = int16((a0 - a1) >> 1)
	}
}

// iwht is the decoder's inverseWHT16: in holds the dequantized Y2
// coefficients, out the DC coefficient of each of the 16 luma blocks.
func iwht(in *[16]int16, out *[16]int16) {
	var m [16]int32
	for i := 0; i < 4; i++ {
		a0 := int32(in[0+i]) + int32(in[12+i])
		a1 := int32(in[4+i]) + int32(in[8+i])
		a2 := int32(in[4+i]) - int32(in[8+i])
		a3 := int32(in[0+i]) - int32(in[12+i])
		m[0+i] = a0 + a1
		m[8+i] = a0 - a1
		m[4+i] = a3 + a2
		m[12+i] = a3 - a2
	}
	for i := 0; i < 4; i++ {
		dc := m[0+i*4] + 3
		a0 := dc + m[3+i*4]
		a1 := m[1+i*4] + m[2+i*4]
		a2 := m[1+i*4] - m[2+i*4]
		a3 := dc - m[3+i*4]
		out[i*4+0] = int16((a0 + a1) >> 3)
		out[i*4+1] = int16((a3 + a2) >> 3)
		out[i*4+2] = int16((a0 - a1) >> 3)
		out[i*4+3] = int16((a3 - a2) >> 3)
	}
}

// idctAdd is the decoder's inverseDCT4: dst = clip(ref + idct(coef)).
// dst and ref may be the same memory.
func idctAdd(coef *[16]int16, ref []uint8, rStride int, dst []uint8, dStride int) {
	const (
		c1 = 85627 // 65536 * cos(pi/8) * sqrt(2).
		c2 = 35468 // 65536 * sin(pi/8) * sqrt(2).
	)
	var m [4][4]int32
	for i := 0; i < 4; i++ {
		c0, c4, c8, c12 := int32(coef[i]), int32(coef[4+i]), int32(coef[8+i]), int32(coef[12+i])
		a := c0 + c8
		b := c0 - c8
		c := (c4*c2)>>16 - (c12*c1)>>16
		d := (c4*c1)>>16 + (c12*c2)>>16
		m[i][0] = a + d
		m[i][1] = b + c
		m[i][2] = b - c
		m[i][3] = a - d
	}
	for j := 0; j < 4; j++ {
		dc := m[0][j] + 4
		a := dc + m[2][j]
		b := dc - m[2][j]
		c := (m[1][j]*c2)>>16 - (m[3][j]*c1)>>16
		d := (m[1][j]*c1)>>16 + (m[3][j]*c2)>>16
		r, o := ref[j*rStride:j*rStride+4], dst[j*dStride:j*dStride+4]
		o[0] = clip8(int32(r[0]) + (a+d)>>3)
		o[1] = clip8(int32(r[1]) + (b+c)>>3)
		o[2] = clip8(int32(r[2]) + (b-c)>>3)
		o[3] = clip8(int32(r[3]) + (a-d)>>3)
	}
}

// dcAdd is idctAdd for a block whose only non-zero coefficient is the DC.
func dcAdd(dc0 int16, ref []uint8, rStride int, dst []uint8, dStride int) {
	dc := (int32(dc0) + 4) >> 3
	for j := 0; j < 4; j++ {
		r, o := ref[j*rStride:j*rStride+4], dst[j*dStride:j*dStride+4]
		o[0] = clip8(int32(r[0]) + dc)
		o[1] = clip8(int32(r[1]) + dc)
		o[2] = clip8(int32(r[2]) + dc)
		o[3] = clip8(int32(r[3]) + dc)
	}
}

// copy4 copies a 4x4 block.
func copy4(ref []uint8, rStride int, dst []uint8, dStride int) {
	for j := 0; j < 4; j++ {
		copy(dst[j*dStride:j*dStride+4], ref[j*rStride:j*rStride+4])
	}
}

// sse4 is the sum of squared differences of two 4x4 blocks.
func sse4(a []uint8, aStride int, b []uint8, bStride int) int {
	s := 0
	for j := 0; j < 4; j++ {
		p, q := a[j*aStride:j*aStride+4], b[j*bStride:j*bStride+4]
		d0 := int(p[0]) - int(q[0])
		d1 := int(p[1]) - int(q[1])
		d2 := int(p[2]) - int(q[2])
		d3 := int(p[3]) - int(q[3])
		s += d0*d0 + d1*d1 + d2*d2 + d3*d3
	}
	return s
}
