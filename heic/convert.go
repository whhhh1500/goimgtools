package heic

// The conversion of decoded Y'CbCr rows to 8-bit RGBA is
// github.com/gen2brain/h265's (heic/color.go and heic/color_row.go, MIT
// licence; licenses/LICENSE-H265 here), its arithmetic as its Go code does
// it, cut to 8-bit samples, with every product converted to float32 on its
// own so that no processor fuses it with an addition: the pixels are those
// h265 gives on amd64, on every processor.

const (
	mcIdentity  = 0
	mcBT709     = 1
	mcUnspec    = 2
	mcFCC       = 4
	mcBT470BG   = 5
	mcBT601     = 6
	mcSMPTE240  = 7
	mcYCgCo     = 8
	mcBT2020NCL = 9
)

// colourOf is the description a picture is converted by: its matrix, and
// whether the samples use their full range.
type colourOf struct {
	matrix    int
	fullRange bool
}

// rowConverter converts the rows of 8-bit planes: Y of w x h samples, Cb and
// Cr (unless chroma is 0) of the chroma format's size.
type rowConverter struct {
	matrix                   int
	ssHor, ssVer             int
	hasColor, fast           bool
	tableY, tableUV          [256]float32
	pixLUT                   [256]uint8
	biasY, rangeY            float32
	biasUV, rangeUV          float32
	kr, kg, kb               float32
	crCoef, cbCoef, gcr, gcb float32
	uvIdx, uvAdj             []int
	w, uw                    int
}

// rowScratch is what converting a row writes besides the row: one for each
// goroutine converting rows at once. row holds a row of pixels on its way
// elsewhere (turned, or a span of a column).
type rowScratch struct {
	yf           []float32
	uPad, vPad   [2][]float32
	cbRow, crRow []float32
	row          []uint8
}

func (s *rowConverter) newScratch() *rowScratch {
	c := &rowScratch{yf: make([]float32, s.w), cbRow: make([]float32, s.w), crRow: make([]float32, s.w),
		row: make([]uint8, 4*s.w)}
	for i := range 2 {
		c.uPad[i], c.vPad[i] = make([]float32, s.uw+2), make([]float32, s.uw+2)
	}
	return c
}

// scratchBytes is at most the size of a rowScratch for rows w wide.
func scratchBytes(w int) int64 { return 4*(3*int64(w)+4*int64(w+2)) + 4*int64(w) }

// newRowConverter prepares the conversion of rows w samples wide; chroma is
// 0 (4:0:0), 1 (4:2:0), 2 (4:2:2) or 3 (4:4:4). ok is false for a matrix h265
// does not convert.
func newRowConverter(c colourOf, chroma, w int) (*rowConverter, bool) {
	s := &rowConverter{matrix: c.matrix, hasColor: chroma != 0}
	// ISO/IEC 23091-2: the identity matrix describes GBR, which subsampled
	// chroma cannot carry; a sequence that says so anyway is read as
	// unspecified.
	if s.matrix == mcIdentity && chroma != 3 && chroma != 0 {
		s.matrix = mcUnspec
	}
	if s.matrix == mcUnspec {
		s.matrix = mcBT601
	}
	switch chroma {
	case 1:
		s.ssHor, s.ssVer = 1, 1
	case 2:
		s.ssHor = 1
	}
	s.kr, s.kb = 0.299, 0.114
	switch s.matrix {
	case mcBT709:
		s.kr, s.kb = 0.2126, 0.0722
	case mcFCC:
		s.kr, s.kb = 0.30, 0.11
	case mcSMPTE240:
		s.kr, s.kb = 0.212, 0.087
	case mcBT2020NCL:
		s.kr, s.kb = 0.2627, 0.0593
	case mcYCgCo:
		if !c.fullRange {
			return nil, false
		}
	case 3, 10, 11, 12, 13, 14:
		return nil, false
	}
	s.kg = 1 - s.kr - s.kb
	s.biasY, s.rangeY = 0, 255
	s.biasUV, s.rangeUV = 128, 255
	if !c.fullRange {
		s.biasY, s.rangeY, s.rangeUV = 16, 219, 224
	}
	for i := range 256 {
		s.tableY[i] = (float32(i) - s.biasY) / s.rangeY
		s.pixLUT[i] = uint8(0.5 + float32(clampF(s.tableY[i])*255))
	}
	if s.matrix == mcIdentity {
		s.tableUV = s.tableY
	} else {
		for i := range 256 {
			s.tableUV[i] = (float32(i) - s.biasUV) / s.rangeUV
		}
	}
	s.crCoef, s.cbCoef = 2*(1-s.kr), 2*(1-s.kb)
	s.gcr, s.gcb = float32(s.kr*(1-s.kr)), float32(s.kb*(1-s.kb))
	s.fast = s.hasColor && s.matrix != mcIdentity && s.matrix != mcYCgCo

	uw := (w + s.ssHor) >> s.ssHor
	s.uvIdx, s.uvAdj = make([]int, w), make([]int, w)
	for x := range w {
		uvX := x >> s.ssHor
		adj := 0
		if x != 0 && !(x == w-1 && x%2 != 0) {
			if x%2 != 0 {
				adj = 1
			} else {
				adj = -1
			}
		}
		s.uvIdx[x], s.uvAdj[x] = uvX, min(max(uvX+adj, 0), uw-1)
	}
	s.w, s.uw = w, uw
	return s, true
}

func clampF(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}

// planeRows gives a converter the rows of the planes: plane 0 (Y), 1 (Cb) or
// 2 (Cr), row y, w samples.
type planeRows func(plane, y int) []uint8

// chromaRows are the rows of chroma row y (of h) is made from: its own, and
// the one next to it (the same for a row that has none).
func (s *rowConverter) chromaRows(y, h int) (int, int) {
	uvY := y >> s.ssVer
	if s.ssVer == 0 || y == 0 || (y == h-1 && y%2 != 0) {
		return uvY, uvY
	}
	ch := (h + s.ssVer) >> s.ssVer
	if y%2 != 0 {
		return uvY, min(uvY+1, ch-1)
	}
	return uvY, max(uvY-1, 0)
}

// convert writes samples x0 to x1 of row y (of h) as NRGBA into dst, alpha
// opaque: the pixels converting the whole row gives there.
func (s *rowConverter) convert(rows planeRows, y, x0, x1, h int, dst []uint8, sc *rowScratch) {
	luma := rows(0, y)[x0:x1]
	if !s.hasColor {
		for x, v := range luma {
			p := s.pixLUT[v]
			d := dst[4*x : 4*x+4 : 4*x+4]
			d[0], d[1], d[2], d[3] = p, p, p, 255
		}
		return
	}
	uw := (s.w + s.ssHor) >> s.ssHor
	uvY, ay := s.chromaRows(y, h)
	// h265 weighs in the row next to it wherever chromaRows gives one, even
	// where clamping at the edge makes it the row itself.
	adj := s.ssVer != 0 && y != 0 && !(y == h-1 && y%2 != 0)
	if s.fast {
		s.convertFast(rows, luma, uvY, ay, adj, uw, x0, x1, dst, sc)
		return
	}
	s.convertScalar(rows, luma, uvY, ay, adj, uw, x0, x1, dst, sc)
}

// pad normalises samples pa-1 to pb-1 of a row of chroma into dst[pa:pb+1],
// the row's edge samples repeated on either side of it (dst[0] and
// dst[uw+1]).
func (s *rowConverter) pad(dst []float32, src []uint8, uw, pa, pb int) []float32 {
	dst = dst[:uw+2]
	// tableUV holds (v - biasUV) / rangeUV, as h265 works it out for each
	// sample (the fast kernel is never used for the identity matrix).
	tab := &s.tableUV
	src = src[:uw]
	for p := pa; p <= pb; p++ {
		dst[p] = tab[src[min(max(p-1, 0), uw-1)]]
	}
	return dst
}

// convertFast is h265's row kernel (convertRowGo, convertRow444Go).
func (s *rowConverter) convertFast(rows planeRows, luma []uint8, uvY, ay int, adj bool, uw, x0, x1 int, dst []uint8, sc *rowScratch) {
	yf := sc.yf[:x1-x0]
	ty := &s.tableY // (v - biasY) / rangeY
	for i, v := range luma {
		yf[i] = ty[v]
	}
	// The padded chroma the samples read.
	pa, pb := x0+1, x1
	if s.ssHor != 0 {
		pa, pb = x0>>1, (x1-1)>>1+2
	}
	u0 := s.pad(sc.uPad[0], rows(1, uvY), uw, pa, pb)
	v0 := s.pad(sc.vPad[0], rows(2, uvY), uw, pa, pb)
	u1, v1 := u0, v0
	if adj {
		u1 = s.pad(sc.uPad[1], rows(1, ay), uw, pa, pb)
		v1 = s.pad(sc.vPad[1], rows(2, ay), uw, pa, pb)
	}
	const w0, w1, w2 = 9.0 / 16.0, 3.0 / 16.0, 1.0 / 16.0
	crCoef, cbCoef, gcr, gcb, kg := s.crCoef, s.cbCoef, s.gcr, s.gcb, s.kg
	for x := x0; x < x1; x++ {
		var cb, cr float32
		if s.ssHor == 0 {
			cb, cr = u0[x+1], v0[x+1]
		} else {
			k := x >> 1
			j := k
			if x&1 != 0 {
				j = k + 2
			}
			k++
			cb = float32(u0[k]*w0) + float32(u0[j]*w1) + float32(u1[k]*w1) + float32(u1[j]*w2)
			cr = float32(v0[k]*w0) + float32(v0[j]*w1) + float32(v1[k]*w1) + float32(v1[j]*w2)
		}
		yy := yf[x-x0]
		r := yy + float32(crCoef*cr)
		b := yy + float32(cbCoef*cb)
		g := yy - float32(2*(float32(gcr*cr)+float32(gcb*cb)))/kg
		d := dst[4*(x-x0) : 4*(x-x0)+4 : 4*(x-x0)+4]
		d[0] = uint8(0.5 + float32(clampF(r)*255))
		d[1] = uint8(0.5 + float32(clampF(g)*255))
		d[2] = uint8(0.5 + float32(clampF(b)*255))
		d[3] = 255
	}
}

// convertScalar is h265's rgbRow, for the identity and YCgCo matrices.
func (s *rowConverter) convertScalar(rows planeRows, luma []uint8, uvY, ay int, adj bool, uw, x0, x1 int, dst []uint8, sc *rowScratch) {
	tab := &s.tableUV
	n := x1 - x0
	cbRow, crRow := sc.cbRow[:n], sc.crRow[:n]
	cb0, cr0 := rows(1, uvY)[:uw], rows(2, uvY)[:uw]
	if s.ssHor == 0 {
		for x := x0; x < x1; x++ {
			cbRow[x-x0], crRow[x-x0] = tab[cb0[x]], tab[cr0[x]]
		}
	} else {
		cb1, cr1 := cb0, cr0
		if adj {
			cb1, cr1 = rows(1, ay)[:uw], rows(2, ay)[:uw]
		}
		const w0, w1, w2, w3 = 9.0 / 16.0, 3.0 / 16.0, 3.0 / 16.0, 1.0 / 16.0
		for x := x0; x < x1; x++ {
			i, j := s.uvIdx[x], s.uvAdj[x]
			cbRow[x-x0] = float32(w0*tab[cb0[i]]) + float32(w1*tab[cb0[j]]) + float32(w2*tab[cb1[i]]) + float32(w3*tab[cb1[j]])
			crRow[x-x0] = float32(w0*tab[cr0[i]]) + float32(w1*tab[cr0[j]]) + float32(w2*tab[cr1[i]]) + float32(w3*tab[cr1[j]])
		}
	}
	store := func(d []uint8, r, g, b float32) {
		d[0] = uint8(0.5 + float32(clampF(r)*255))
		d[1] = uint8(0.5 + float32(clampF(g)*255))
		d[2] = uint8(0.5 + float32(clampF(b)*255))
		d[3] = 255
	}
	for x := range n {
		yv, u, v := s.tableY[luma[x]], cbRow[x], crRow[x]
		d := dst[4*x : 4*x+4 : 4*x+4]
		switch s.matrix {
		case mcIdentity:
			store(d, v, yv, u)
		case mcYCgCo:
			t := yv - u
			store(d, t+v, yv+u, t-v)
		default:
			r := yv + float32(s.crCoef*v)
			b := yv + float32(s.cbCoef*u)
			g := yv - float32(float32(float32(s.gcr*v)+float32(s.gcb*u))*2)/s.kg
			store(d, r, g, b)
		}
	}
}
