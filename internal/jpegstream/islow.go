// This file is derived from libjpeg-turbo's jidctint.c (the accurate
// integer inverse DCT, jpeg_idct_islow), which is based on the work of the
// Independent JPEG Group: see licenses/LICENSE-LIBJPEG-TURBO.

package jpegstream

// islowStore is libjpeg's accurate integer inverse DCT of the dequantized
// block b (natural order), level shifted by +128, its range limited as
// libjpeg's table does it, stored into dst, whose rows are stride apart:
// the pixels libjpeg-turbo, and so Pillow and sharp, decode. b is used as
// the workspace.
//
// The arithmetic is in 32 bits, as libjpeg-turbo's SIMD versions (and its
// C version where a long is 32 bits) do it, so that the result is the same
// on every processor. Where the coefficients of the high frequencies are
// zero, as in most blocks, the terms they would add are left out: the sums
// are the same.
func islowStore(b *block, dst []byte, stride int) {
	const (
		constBits = 13
		pass1Bits = 2
		fix0298   = 2446
		fix0390   = 3196
		fix0541   = 4433
		fix0765   = 6270
		fix0899   = 7373
		fix1175   = 9633
		fix1501   = 12299
		fix1847   = 15137
		fix1961   = 16069
		fix2053   = 16819
		fix2562   = 20995
		fix3072   = 25172
	)
	_ = dst[7*stride+7]
	// Pass 1: the columns, into the workspace (b itself), scaled up by
	// 2^pass1Bits. cols marks the columns of the workspace that are not
	// zero.
	var cols uint
	allDC := true // every column only its DC term: the rows of the workspace are all alike
	for c := 0; c < 8; c++ {
		col := (*[57]int32)(b[c:]) // one bounds check for the column
		if col[8] == 0 && col[16] == 0 && col[24] == 0 && col[32] == 0 && col[40] == 0 && col[48] == 0 && col[56] == 0 {
			dc := col[0] << pass1Bits
			for r := 0; r < 64; r += 8 {
				col[r] = dc
			}
			if dc != 0 {
				cols |= 1 << c
			}
			continue
		}
		cols |= 1 << c
		allDC = false
		if col[32] == 0 && col[40] == 0 && col[48] == 0 && col[56] == 0 {
			// Rows 4 to 7 zero.
			z2 := col[16]
			z1 := z2 * fix0541
			tmp2, tmp3 := z1, z1+z2*fix0765
			tmp0 := col[0] << constBits
			tmp10, tmp13 := tmp0+tmp3, tmp0-tmp3
			tmp11, tmp12 := tmp0+tmp2, tmp0-tmp2
			o3, o1 := col[24], col[8]
			z5 := (o3 + o1) * fix1175
			z1, z2 = o1*-fix0899, o3*-fix2562
			z3, z4 := o3*-fix1961+z5, o1*-fix0390+z5
			t0, t1 := z1+z3, z2+z4
			t2, t3 := o3*fix3072+z2+z3, o1*fix1501+z1+z4
			const s, h = constBits - pass1Bits, 1 << (constBits - pass1Bits - 1)
			col[0] = (tmp10 + t3 + h) >> s
			col[56] = (tmp10 - t3 + h) >> s
			col[8] = (tmp11 + t2 + h) >> s
			col[48] = (tmp11 - t2 + h) >> s
			col[16] = (tmp12 + t1 + h) >> s
			col[40] = (tmp12 - t1 + h) >> s
			col[24] = (tmp13 + t0 + h) >> s
			col[32] = (tmp13 - t0 + h) >> s
			continue
		}
		z2, z3 := col[16], col[48]
		z1 := (z2 + z3) * fix0541
		tmp2 := z1 + z3*-fix1847
		tmp3 := z1 + z2*fix0765
		z2, z3 = col[0], col[32]
		tmp0 := (z2 + z3) << constBits
		tmp1 := (z2 - z3) << constBits
		tmp10, tmp13 := tmp0+tmp3, tmp0-tmp3
		tmp11, tmp12 := tmp1+tmp2, tmp1-tmp2
		tmp0, tmp1, tmp2, tmp3 = col[56], col[40], col[24], col[8]
		z1, z2, z3, z4 := tmp0+tmp3, tmp1+tmp2, tmp0+tmp2, tmp1+tmp3
		z5 := (z3 + z4) * fix1175
		tmp0 *= fix0298
		tmp1 *= fix2053
		tmp2 *= fix3072
		tmp3 *= fix1501
		z1 *= -fix0899
		z2 *= -fix2562
		z3 *= -fix1961
		z4 *= -fix0390
		z3 += z5
		z4 += z5
		tmp0 += z1 + z3
		tmp1 += z2 + z4
		tmp2 += z2 + z3
		tmp3 += z1 + z4
		const s, h = constBits - pass1Bits, 1 << (constBits - pass1Bits - 1)
		col[0] = (tmp10 + tmp3 + h) >> s
		col[56] = (tmp10 - tmp3 + h) >> s
		col[8] = (tmp11 + tmp2 + h) >> s
		col[48] = (tmp11 - tmp2 + h) >> s
		col[16] = (tmp12 + tmp1 + h) >> s
		col[40] = (tmp12 - tmp1 + h) >> s
		col[24] = (tmp13 + tmp0 + h) >> s
		col[32] = (tmp13 - tmp0 + h) >> s
	}
	// Pass 2: the rows, descaled, into dst.
	half := cols&0xf0 == 0 // columns 4 to 7 zero
	rows := 8
	if allDC {
		rows = 1 // and copied below
	}
	for r := 0; r < rows; r++ {
		w := (*[8]int32)(b[8*r:])
		d := (*[8]byte)(dst[r*stride:])
		if w[1] == 0 && w[2] == 0 && w[3] == 0 && w[4] == 0 && w[5] == 0 && w[6] == 0 && w[7] == 0 {
			v := idctLimit[((w[0]+1<<(pass1Bits+2))>>(pass1Bits+3))&1023]
			for i := range d {
				d[i] = v
			}
			continue
		}
		if half {
			z2 := w[2]
			z1 := z2 * fix0541
			tmp2, tmp3 := z1, z1+z2*fix0765
			tmp0 := w[0] << constBits
			tmp10, tmp13 := tmp0+tmp3, tmp0-tmp3
			tmp11, tmp12 := tmp0+tmp2, tmp0-tmp2
			o3, o1 := w[3], w[1]
			z5 := (o3 + o1) * fix1175
			z1, z2 = o1*-fix0899, o3*-fix2562
			z3, z4 := o3*-fix1961+z5, o1*-fix0390+z5
			t0, t1 := z1+z3, z2+z4
			t2, t3 := o3*fix3072+z2+z3, o1*fix1501+z1+z4
			const s, h = constBits + pass1Bits + 3, 1 << (constBits + pass1Bits + 2)
			d[0] = idctLimit[((tmp10+t3+h)>>s)&1023]
			d[7] = idctLimit[((tmp10-t3+h)>>s)&1023]
			d[1] = idctLimit[((tmp11+t2+h)>>s)&1023]
			d[6] = idctLimit[((tmp11-t2+h)>>s)&1023]
			d[2] = idctLimit[((tmp12+t1+h)>>s)&1023]
			d[5] = idctLimit[((tmp12-t1+h)>>s)&1023]
			d[3] = idctLimit[((tmp13+t0+h)>>s)&1023]
			d[4] = idctLimit[((tmp13-t0+h)>>s)&1023]
			continue
		}
		z2, z3 := w[2], w[6]
		z1 := (z2 + z3) * fix0541
		tmp2 := z1 + z3*-fix1847
		tmp3 := z1 + z2*fix0765
		tmp0 := (w[0] + w[4]) << constBits
		tmp1 := (w[0] - w[4]) << constBits
		tmp10, tmp13 := tmp0+tmp3, tmp0-tmp3
		tmp11, tmp12 := tmp1+tmp2, tmp1-tmp2
		tmp0, tmp1, tmp2, tmp3 = w[7], w[5], w[3], w[1]
		z1, z2, z3, z4 := tmp0+tmp3, tmp1+tmp2, tmp0+tmp2, tmp1+tmp3
		z5 := (z3 + z4) * fix1175
		tmp0 *= fix0298
		tmp1 *= fix2053
		tmp2 *= fix3072
		tmp3 *= fix1501
		z1 *= -fix0899
		z2 *= -fix2562
		z3 *= -fix1961
		z4 *= -fix0390
		z3 += z5
		z4 += z5
		tmp0 += z1 + z3
		tmp1 += z2 + z4
		tmp2 += z2 + z3
		tmp3 += z1 + z4
		const s, h = constBits + pass1Bits + 3, 1 << (constBits + pass1Bits + 2)
		d[0] = idctLimit[((tmp10+tmp3+h)>>s)&1023]
		d[7] = idctLimit[((tmp10-tmp3+h)>>s)&1023]
		d[1] = idctLimit[((tmp11+tmp2+h)>>s)&1023]
		d[6] = idctLimit[((tmp11-tmp2+h)>>s)&1023]
		d[2] = idctLimit[((tmp12+tmp1+h)>>s)&1023]
		d[5] = idctLimit[((tmp12-tmp1+h)>>s)&1023]
		d[3] = idctLimit[((tmp13+tmp0+h)>>s)&1023]
		d[4] = idctLimit[((tmp13-tmp0+h)>>s)&1023]
	}
	if allDC {
		row := (*[8]byte)(dst)
		for r := 1; r < 8; r++ {
			*(*[8]byte)(dst[r*stride:]) = *row
		}
	}
}

// idctLimit is libjpeg's range limit after the inverse DCT, indexed by the
// value modulo 1024: v+128 kept within 0 to 255 for v from -512 to 511
// (larger values wrap around, as there).
var idctLimit = func() (t [1024]uint8) {
	for i := range t {
		v := i
		if v >= 512 {
			v -= 1024
		}
		t[i] = uint8(max(0, min(255, v+128)))
	}
	return t
}()
