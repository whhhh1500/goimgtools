// This file is derived from libjpeg-turbo's jfdctint.c (the accurate
// integer forward DCT, jpeg_fdct_islow), which is based on the work of the
// Independent JPEG Group: see licenses/LICENSE-LIBJPEG-TURBO.

package jpegstream

// The factors of jfdctint.c, in 13-bit fixed point.
const (
	fdctConstBits = 13
	fdctPass1Bits = 2

	fix0_298631336 = 2446
	fix0_390180644 = 3196
	fix0_541196100 = 4433
	fix0_765366865 = 6270
	fix0_899976223 = 7373
	fix1_175875602 = 9633
	fix1_501321110 = 12299
	fix1_847759065 = 15137
	fix1_961570560 = 16069
	fix2_053119869 = 16819
	fix2_562915447 = 20995
	fix3_072711026 = 25172
)

// fdct is libjpeg's accurate integer forward DCT (jpeg_fdct_islow) of the
// samples of b, in natural order, level shifted by -128 first as libjpeg
// does: the coefficients, scaled up by 8, that libjpeg-turbo (and so
// Pillow and sharp) quantizes. As in libjpeg, 32 bits are enough for every
// step.
func fdct(b *block) {
	// Rows, the results scaled up by 2^fdctPass1Bits.
	const s1 = fdctConstBits - fdctPass1Bits
	for r := 0; r < blockSize; r += 8 {
		d := b[r : r+8 : r+8]
		x0, x1, x2, x3 := d[0]-128, d[1]-128, d[2]-128, d[3]-128
		x4, x5, x6, x7 := d[4]-128, d[5]-128, d[6]-128, d[7]-128
		tmp0, tmp7 := x0+x7, x0-x7
		tmp1, tmp6 := x1+x6, x1-x6
		tmp2, tmp5 := x2+x5, x2-x5
		tmp3, tmp4 := x3+x4, x3-x4

		tmp10, tmp13 := tmp0+tmp3, tmp0-tmp3
		tmp11, tmp12 := tmp1+tmp2, tmp1-tmp2
		d[0] = (tmp10 + tmp11) << fdctPass1Bits
		d[4] = (tmp10 - tmp11) << fdctPass1Bits
		z1 := (tmp12 + tmp13) * fix0_541196100
		d[2] = (z1 + tmp13*fix0_765366865 + 1<<(s1-1)) >> s1
		d[6] = (z1 - tmp12*fix1_847759065 + 1<<(s1-1)) >> s1

		z1 = tmp4 + tmp7
		z2 := tmp5 + tmp6
		z3 := tmp4 + tmp6
		z4 := tmp5 + tmp7
		z5 := (z3 + z4) * fix1_175875602
		tmp4 *= fix0_298631336
		tmp5 *= fix2_053119869
		tmp6 *= fix3_072711026
		tmp7 *= fix1_501321110
		z1 *= -fix0_899976223
		z2 *= -fix2_562915447
		z3 = z3*-fix1_961570560 + z5
		z4 = z4*-fix0_390180644 + z5
		d[7] = (tmp4 + z1 + z3 + 1<<(s1-1)) >> s1
		d[5] = (tmp5 + z2 + z4 + 1<<(s1-1)) >> s1
		d[3] = (tmp6 + z2 + z3 + 1<<(s1-1)) >> s1
		d[1] = (tmp7 + z1 + z4 + 1<<(s1-1)) >> s1
	}
	// Columns, the scaling of the rows taken out again.
	const s2 = fdctConstBits + fdctPass1Bits
	for c := 0; c < 8; c++ {
		d := b[c : c+57 : c+57]
		tmp0, tmp7 := d[0]+d[56], d[0]-d[56]
		tmp1, tmp6 := d[8]+d[48], d[8]-d[48]
		tmp2, tmp5 := d[16]+d[40], d[16]-d[40]
		tmp3, tmp4 := d[24]+d[32], d[24]-d[32]

		tmp10, tmp13 := tmp0+tmp3, tmp0-tmp3
		tmp11, tmp12 := tmp1+tmp2, tmp1-tmp2
		d[0] = (tmp10 + tmp11 + 1<<(fdctPass1Bits-1)) >> fdctPass1Bits
		d[32] = (tmp10 - tmp11 + 1<<(fdctPass1Bits-1)) >> fdctPass1Bits
		z1 := (tmp12 + tmp13) * fix0_541196100
		d[16] = (z1 + tmp13*fix0_765366865 + 1<<(s2-1)) >> s2
		d[48] = (z1 - tmp12*fix1_847759065 + 1<<(s2-1)) >> s2

		z1 = tmp4 + tmp7
		z2 := tmp5 + tmp6
		z3 := tmp4 + tmp6
		z4 := tmp5 + tmp7
		z5 := (z3 + z4) * fix1_175875602
		tmp4 *= fix0_298631336
		tmp5 *= fix2_053119869
		tmp6 *= fix3_072711026
		tmp7 *= fix1_501321110
		z1 *= -fix0_899976223
		z2 *= -fix2_562915447
		z3 = z3*-fix1_961570560 + z5
		z4 = z4*-fix0_390180644 + z5
		d[56] = (tmp4 + z1 + z3 + 1<<(s2-1)) >> s2
		d[40] = (tmp5 + z2 + z4 + 1<<(s2-1)) >> s2
		d[24] = (tmp6 + z2 + z3 + 1<<(s2-1)) >> s2
		d[8] = (tmp7 + z1 + z4 + 1<<(s2-1)) >> s2
	}
}
