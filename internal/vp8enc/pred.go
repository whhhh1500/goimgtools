package vp8enc

// Intra prediction, RFC 6386 chapter 12. The predictors read the borders of
// the macroblock workspace, which is laid out exactly like the decoder's
// (see golang.org/x/image/vp8/reconstruct.go): 26 rows of 32 bytes holding
// the 16x16 luma block at row 1, column 8 and the two 8x8 chroma blocks at
// row 18, columns 8 and 24, each with the row above and the column to the
// left of it, plus four luma samples above and to the right.

const (
	bps = 32 // workspace stride

	wsYX = 8
	wsYY = 1
	wsBX = 8
	wsBY = 18
	wsRX = 24
	wsRY = 18

	wsY = wsYY*bps + wsYX
	wsB = wsBY*bps + wsBX
	wsR = wsRY*bps + wsRX
)

// Predictor modes, numbered as in the decoder.
const (
	predDC = iota
	predTM
	predVE
	predHE
	predRD
	predVR
	predLD
	predVL
	predHD
	predHU
)

type workspace [26 * bps]uint8

// predLarge fills dst (n x n, stride n) with the 16x16 or 8x8 prediction of
// the block whose top-left sample is ws[at]. mbx and mby select the DC
// variant used on the frame's top row and left column.
func predLarge(ws *workspace, at, n int, mode uint8, mbx, mby int, dst []uint8) {
	top := ws[at-bps : at-bps+n]
	switch mode {
	case predDC:
		var dc uint8
		shift := uint(3)
		if n == 16 {
			shift = 4
		}
		switch {
		case mbx == 0 && mby == 0:
			dc = 0x80
		case mbx == 0: // only the row above is real
			sum := uint32(n >> 1)
			for _, v := range top {
				sum += uint32(v)
			}
			dc = uint8(sum >> shift)
		case mby == 0: // only the column to the left is real
			sum := uint32(n >> 1)
			for j := 0; j < n; j++ {
				sum += uint32(ws[at+j*bps-1])
			}
			dc = uint8(sum >> shift)
		default:
			sum := uint32(n)
			for _, v := range top {
				sum += uint32(v)
			}
			for j := 0; j < n; j++ {
				sum += uint32(ws[at+j*bps-1])
			}
			dc = uint8(sum >> (shift + 1))
		}
		d := dst[:n*n]
		for i := range d {
			d[i] = dc
		}
	case predTM:
		tl := int32(ws[at-bps-1])
		for j := 0; j < n; j++ {
			d := dst[j*n : j*n+n]
			l := int32(ws[at+j*bps-1]) - tl
			for i, t := range top {
				d[i] = clip8(l + int32(t))
			}
		}
	case predVE:
		for j := 0; j < n; j++ {
			copy(dst[j*n:j*n+n], top)
		}
	case predHE:
		for j := 0; j < n; j++ {
			d := dst[j*n : j*n+n]
			l := ws[at+j*bps-1]
			for i := range d {
				d[i] = l
			}
		}
	}
}

// pred4All computes all ten 4x4 predictions of the block at ws[at].
func pred4All(ws *workspace, at int, out *[nPred][16]uint8) {
	a := int32(ws[at-bps-1])
	t := ws[at-bps : at-bps+8]
	t0, t1, t2, t3 := int32(t[0]), int32(t[1]), int32(t[2]), int32(t[3])
	t4, t5, t6, t7 := int32(t[4]), int32(t[5]), int32(t[6]), int32(t[7])
	p := int32(ws[at-1])
	q := int32(ws[at+bps-1])
	r := int32(ws[at+2*bps-1])
	s := int32(ws[at+3*bps-1])

	// DC.
	{
		dc := uint8((t0 + t1 + t2 + t3 + p + q + r + s + 4) >> 3)
		d := &out[predDC]
		for i := range d {
			d[i] = dc
		}
	}
	// TM.
	{
		d := &out[predTM]
		for j, l := range [4]int32{p, q, r, s} {
			l -= a
			d[j*4+0] = clip8(l + t0)
			d[j*4+1] = clip8(l + t1)
			d[j*4+2] = clip8(l + t2)
			d[j*4+3] = clip8(l + t3)
		}
	}
	// Three-tap averages along the left column, the corner and the top row.
	srq := uint8((s + 2*r + q + 2) >> 2)
	rqp := uint8((r + 2*q + p + 2) >> 2)
	qpa := uint8((q + 2*p + a + 2) >> 2)
	pab := uint8((p + 2*a + t0 + 2) >> 2)
	abc := uint8((a + 2*t0 + t1 + 2) >> 2)
	bcd := uint8((t0 + 2*t1 + t2 + 2) >> 2)
	cde := uint8((t1 + 2*t2 + t3 + 2) >> 2)
	def := uint8((t2 + 2*t3 + t4 + 2) >> 2)
	efg := uint8((t3 + 2*t4 + t5 + 2) >> 2)
	fgh := uint8((t4 + 2*t5 + t6 + 2) >> 2)
	ghi := uint8((t5 + 2*t6 + t7 + 2) >> 2)
	hii := uint8((t6 + 3*t7 + 2) >> 2)
	// VE.
	{
		d := &out[predVE]
		for j := 0; j < 16; j += 4 {
			d[j+0], d[j+1], d[j+2], d[j+3] = abc, bcd, cde, def
		}
	}
	// HE.
	{
		d := &out[predHE]
		apq := uint8((a + 2*p + q + 2) >> 2)
		ssr := uint8((r + 3*s + 2) >> 2)
		for i := 0; i < 4; i++ {
			d[0+i], d[4+i], d[8+i], d[12+i] = apq, rqp, srq, ssr
		}
	}
	// RD.
	out[predRD] = [16]uint8{
		pab, abc, bcd, cde,
		qpa, pab, abc, bcd,
		rqp, qpa, pab, abc,
		srq, rqp, qpa, pab,
	}
	// VR.
	{
		ab := uint8((a + t0 + 1) >> 1)
		bc := uint8((t0 + t1 + 1) >> 1)
		cd := uint8((t1 + t2 + 1) >> 1)
		de := uint8((t2 + t3 + 1) >> 1)
		out[predVR] = [16]uint8{
			ab, bc, cd, de,
			pab, abc, bcd, cde,
			qpa, ab, bc, cd,
			rqp, pab, abc, bcd,
		}
	}
	// LD: the top row and the four samples to its right.
	out[predLD] = [16]uint8{
		bcd, cde, def, efg,
		cde, def, efg, fgh,
		def, efg, fgh, ghi,
		efg, fgh, ghi, hii,
	}
	// VL.
	{
		ab := uint8((t0 + t1 + 1) >> 1)
		bc := uint8((t1 + t2 + 1) >> 1)
		cd := uint8((t2 + t3 + 1) >> 1)
		de := uint8((t3 + t4 + 1) >> 1)
		out[predVL] = [16]uint8{
			ab, bc, cd, de,
			bcd, cde, def, efg,
			bc, cd, de, fgh,
			cde, def, efg, ghi,
		}
	}
	// HD.
	{
		sr := uint8((s + r + 1) >> 1)
		rq := uint8((r + q + 1) >> 1)
		qp := uint8((q + p + 1) >> 1)
		pa := uint8((p + a + 1) >> 1)
		out[predHD] = [16]uint8{
			pa, pab, abc, bcd,
			qp, qpa, pa, pab,
			rq, rqp, qp, qpa,
			sr, srq, rq, rqp,
		}
	}
	// HU.
	{
		pq := uint8((p + q + 1) >> 1)
		qr := uint8((q + r + 1) >> 1)
		rs := uint8((r + s + 1) >> 1)
		pqr := uint8((p + 2*q + r + 2) >> 2)
		qrs := uint8((q + 2*r + s + 2) >> 2)
		rss := uint8((r + 3*s + 2) >> 2)
		ss := uint8(s)
		out[predHU] = [16]uint8{
			pq, pqr, qr, qrs,
			qr, qrs, rs, rss,
			rs, rss, ss, ss,
			ss, ss, ss, ss,
		}
	}
}
