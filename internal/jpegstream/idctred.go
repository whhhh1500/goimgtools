package jpegstream

// The reduced inverse DCTs of libjpeg-turbo's jidctred.c (jpeg_idct_4x4,
// jpeg_idct_2x2 and jpeg_idct_1x1), ported statement by statement: a block
// decoded straight to 4×4, 2×2 or 1×1 pixels, for an image shrunk by 2, 4 or
// 8 while it is decoded, as libjpeg does it (and so Pillow's draft and
// sharp's shrink-on-load). In 32-bit arithmetic, as libjpeg's JLONG on
// Windows; a valid image never comes near its limits.
//
// Copyright (C) 1994-1998, Thomas G. Lane. Modified 2002-2010 by Guido
// Vollbeding. libjpeg-turbo Modifications: Copyright (C) 2015, 2022, D. R.
// Commander. See licenses/LICENSE-LIBJPEG-TURBO.

const (
	redConstBits = 13
	redPass1Bits = 2

	fix0211 = 1730  // FIX(0.211164243)
	fix0509 = 4176  // FIX(0.509795579)
	fix0601 = 4926  // FIX(0.601344887)
	fix0720 = 5906  // FIX(0.720959822)
	fix0765 = 6270  // FIX(0.765366865)
	fix0850 = 6967  // FIX(0.850430095)
	fix0899 = 7373  // FIX(0.899976223)
	fix1061 = 8697  // FIX(1.061594337)
	fix1272 = 10426 // FIX(1.272758580)
	fix1451 = 11893 // FIX(1.451774981)
	fix1847 = 15137 // FIX(1.847759065)
	fix2172 = 17799 // FIX(2.172734803)
	fix2562 = 20995 // FIX(2.562915447)
	fix3624 = 29692 // FIX(3.624509785)
)

// descale is libjpeg's DESCALE: v divided by 2^n, rounded.
func descale(v int32, n uint) int32 { return (v + 1<<(n-1)) >> n }

// idct4Store stores jpeg_idct_4x4 of the dequantized block b into dst,
// whose rows are stride apart.
func idct4Store(b *block, dst []byte, stride int) {
	_ = dst[3*stride+3]
	var ws [8 * 4]int32
	// Pass 1: the columns (but the fifth, which the second pass does not
	// use) into the workspace.
	for c := 0; c < 8; c++ {
		if c == 4 {
			continue
		}
		col := (*[57]int32)(b[c:])
		if col[8] == 0 && col[16] == 0 && col[24] == 0 && col[40] == 0 && col[48] == 0 && col[56] == 0 {
			dc := col[0] << redPass1Bits
			ws[c], ws[8+c], ws[16+c], ws[24+c] = dc, dc, dc, dc
			continue
		}
		// Even part.
		tmp0 := col[0] << (redConstBits + 1)
		tmp2 := col[16]*fix1847 + col[48]*-fix0765
		tmp10, tmp12 := tmp0+tmp2, tmp0-tmp2
		// Odd part.
		z1, z2, z3, z4 := col[56], col[40], col[24], col[8]
		tmp0 = z1*-fix0211 + z2*fix1451 + z3*-fix2172 + z4*fix1061
		tmp2 = z1*-fix0509 + z2*-fix0601 + z3*fix0899 + z4*fix2562
		const n = redConstBits - redPass1Bits + 1
		ws[c] = descale(tmp10+tmp2, n)
		ws[24+c] = descale(tmp10-tmp2, n)
		ws[8+c] = descale(tmp12+tmp0, n)
		ws[16+c] = descale(tmp12-tmp0, n)
	}
	// Pass 2: the 4 rows of the workspace into dst.
	for r := 0; r < 4; r++ {
		w := ws[8*r : 8*r+8 : 8*r+8]
		d := dst[r*stride : r*stride+4 : r*stride+4]
		if w[1] == 0 && w[2] == 0 && w[3] == 0 && w[5] == 0 && w[6] == 0 && w[7] == 0 {
			v := idctLimit[descale(w[0], redPass1Bits+3)&1023]
			d[0], d[1], d[2], d[3] = v, v, v, v
			continue
		}
		tmp0 := w[0] << (redConstBits + 1)
		tmp2 := w[2]*fix1847 + w[6]*-fix0765
		tmp10, tmp12 := tmp0+tmp2, tmp0-tmp2
		z1, z2, z3, z4 := w[7], w[5], w[3], w[1]
		tmp0 = z1*-fix0211 + z2*fix1451 + z3*-fix2172 + z4*fix1061
		tmp2 = z1*-fix0509 + z2*-fix0601 + z3*fix0899 + z4*fix2562
		const n = redConstBits + redPass1Bits + 3 + 1
		d[0] = idctLimit[descale(tmp10+tmp2, n)&1023]
		d[3] = idctLimit[descale(tmp10-tmp2, n)&1023]
		d[1] = idctLimit[descale(tmp12+tmp0, n)&1023]
		d[2] = idctLimit[descale(tmp12-tmp0, n)&1023]
	}
}

// idct2Store stores jpeg_idct_2x2 of b into dst.
func idct2Store(b *block, dst []byte, stride int) {
	_ = dst[stride+1]
	var ws [8 * 2]int32
	// Pass 1: the columns 0, 1, 3, 5 and 7, the others unused.
	for _, c := range [...]int{0, 1, 3, 5, 7} {
		col := (*[57]int32)(b[c:])
		if col[8] == 0 && col[24] == 0 && col[40] == 0 && col[56] == 0 {
			dc := col[0] << redPass1Bits
			ws[c], ws[8+c] = dc, dc
			continue
		}
		tmp10 := col[0] << (redConstBits + 2)
		tmp0 := col[56] * -fix0720
		tmp0 += col[40] * fix0850
		tmp0 += col[24] * -fix1272
		tmp0 += col[8] * fix3624
		const n = redConstBits - redPass1Bits + 2
		ws[c] = descale(tmp10+tmp0, n)
		ws[8+c] = descale(tmp10-tmp0, n)
	}
	// Pass 2: the 2 rows.
	for r := 0; r < 2; r++ {
		w := ws[8*r : 8*r+8 : 8*r+8]
		d := dst[r*stride : r*stride+2 : r*stride+2]
		if w[1] == 0 && w[3] == 0 && w[5] == 0 && w[7] == 0 {
			v := idctLimit[descale(w[0], redPass1Bits+3)&1023]
			d[0], d[1] = v, v
			continue
		}
		tmp10 := w[0] << (redConstBits + 2)
		tmp0 := w[7]*-fix0720 + w[5]*fix0850 + w[3]*-fix1272 + w[1]*fix3624
		const n = redConstBits + redPass1Bits + 3 + 2
		d[0] = idctLimit[descale(tmp10+tmp0, n)&1023]
		d[1] = idctLimit[descale(tmp10-tmp0, n)&1023]
	}
}

// idct1Store stores jpeg_idct_1x1 of b, its DC coefficient over 8.
func idct1Store(b *block, dst []byte) {
	dst[0] = idctLimit[descale(b[0], 3)&1023]
}

// dcStore stores a block whose AC coefficients are zero, of dequantized DC
// coefficient dc, at 1<<scale times smaller (scale 0 to 3): every pixel is
// the same, the one islowStore, idct4Store, idct2Store or idct1Store makes
// of such a block (their arithmetic, with the zeros left out).
func dcStore(dc int32, dst []byte, stride int, scale uint) {
	var v uint8
	switch scale {
	case 0:
		v = uint8(max(0, min(255, (dc<<17+1<<19)>>20+128)))
	case 1, 2:
		v = idctLimit[descale(dc<<redPass1Bits, redPass1Bits+3)&1023]
	default:
		dst[0] = idctLimit[descale(dc, 3)&1023]
		return
	}
	n := 8 >> scale
	_ = dst[(n-1)*stride+n-1]
	for y := range n {
		row := dst[y*stride : y*stride+n]
		for x := range row {
			row[x] = v
		}
	}
}
