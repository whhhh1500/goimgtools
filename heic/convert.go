package heic

// The conversion of decoded Y'CbCr rows to RGBA is
// github.com/gen2brain/h265's (heic/color.go and heic/color_row.go, MIT
// licence; licenses/LICENSE-H265 here), its arithmetic as its Go code does
// it, with every product converted to float32 on its own so that no
// processor fuses it with an addition: the pixels are those h265 gives on
// amd64, on every processor. Samples of 8 bits give NRGBA; of more, NRGBA64
// (16 bits a channel, big-endian), as h265 gives them.

import "encoding/binary"

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

// sample is a sample of a plane: 8 bits, or more in 16.
type sample interface{ ~uint8 | ~uint16 }

// converter converts rows of the planes to pixels.
type converter interface {
	// convert writes samples x0 to x1 of row y (of h) as pixels into dst,
	// alpha opaque: the pixels converting the whole row gives there.
	convert(y, x0, x1, h int, dst []uint8, sc *rowScratch)
	// chromaRows are the rows of chroma row y (of h) is made from: its
	// own, and the one next to it (the same for a row that has none).
	chromaRows(y, h int) (int, int)
	newScratch() *rowScratch
}

// rowConverter converts the rows of planes of samples T, of depth bits: Y
// of w x h samples, Cb and Cr (unless chroma is 0) of the chroma format's
// size, which rows gives: plane 0 (Y), 1 (Cb) or 2 (Cr), row y.
type rowConverter[T sample] struct {
	rows                     func(plane, y int) []T
	matrix                   int
	ssHor, ssVer             int
	hasColor, fast           bool
	maxCh                    T
	bpp                      int // bytes a pixel: 4, or 8 above 8 bits
	outMax                   float32
	tableY, tableUV          []float32
	pixLUT                   []uint16
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

func (s *rowConverter[T]) newScratch() *rowScratch {
	c := &rowScratch{yf: make([]float32, s.w), cbRow: make([]float32, s.w), crRow: make([]float32, s.w),
		row: make([]uint8, s.bpp*s.w)}
	for i := range 2 {
		c.uPad[i], c.vPad[i] = make([]float32, s.uw+2), make([]float32, s.uw+2)
	}
	return c
}

// scratchBytes is at most the size of a rowScratch for rows w wide, of
// pixels of bpp bytes.
func scratchBytes(w, bpp int) int64 { return 4*(3*int64(w)+4*int64(w+2)) + int64(bpp)*int64(w) }

// newRowConverter prepares the conversion of rows w samples wide, of depth
// bits; chroma is 0 (4:0:0), 1 (4:2:0), 2 (4:2:2) or 3 (4:4:4). ok is false
// for a matrix h265 does not convert.
func newRowConverter[T sample](rows func(plane, y int) []T, c colourOf, chroma, depth, w int) (*rowConverter[T], bool) {
	s := &rowConverter[T]{rows: rows, matrix: c.matrix, hasColor: chroma != 0}
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
	maxCh := 1<<depth - 1
	s.maxCh, s.bpp, s.outMax = T(maxCh), 4, 255
	if depth > 8 {
		s.bpp, s.outMax = 8, 65535
	}
	shift := depth - 8
	biasY, rangeY := float32(0), float32(maxCh)
	biasUV, rangeUV := float32(int(1)<<(depth-1)), float32(maxCh)
	if !c.fullRange {
		biasY, rangeY = float32(int(16)<<shift), float32(int(219)<<shift)
		rangeUV = float32(int(224) << shift)
	}
	s.tableY, s.pixLUT = make([]float32, maxCh+1), make([]uint16, maxCh+1)
	for i := range s.tableY {
		s.tableY[i] = (float32(i) - biasY) / rangeY
		s.pixLUT[i] = uint16(0.5 + float32(clampF(s.tableY[i])*s.outMax))
	}
	if s.matrix == mcIdentity {
		s.tableUV = s.tableY
	} else {
		s.tableUV = make([]float32, maxCh+1)
		for i := range s.tableUV {
			s.tableUV[i] = (float32(i) - biasUV) / rangeUV
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

func (s *rowConverter[T]) chromaRows(y, h int) (int, int) {
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

// put stores a pixel, alpha opaque: its channels clamped and scaled to the
// output depth.
func (s *rowConverter[T]) put(d []uint8, r, g, b float32) {
	if s.bpp == 4 {
		put8(d, r, g, b)
		return
	}
	put16(d, r, g, b)
}

func put8(d []uint8, r, g, b float32) {
	d = d[:4:4]
	d[0] = uint8(0.5 + float32(clampF(r)*255))
	d[1] = uint8(0.5 + float32(clampF(g)*255))
	d[2] = uint8(0.5 + float32(clampF(b)*255))
	d[3] = 255
}

func put16(d []uint8, r, g, b float32) {
	d = d[:8:8]
	binary.BigEndian.PutUint16(d[0:], uint16(0.5+float32(clampF(r)*65535)))
	binary.BigEndian.PutUint16(d[2:], uint16(0.5+float32(clampF(g)*65535)))
	binary.BigEndian.PutUint16(d[4:], uint16(0.5+float32(clampF(b)*65535)))
	d[6], d[7] = 0xff, 0xff
}

func (s *rowConverter[T]) convert(y, x0, x1, h int, dst []uint8, sc *rowScratch) {
	luma := s.rows(0, y)[x0:x1]
	if !s.hasColor {
		for x, v := range luma {
			p := s.pixLUT[min(v, s.maxCh)]
			if s.bpp == 4 {
				d := dst[4*x : 4*x+4 : 4*x+4]
				d[0], d[1], d[2], d[3] = uint8(p), uint8(p), uint8(p), 255
				continue
			}
			d := dst[8*x : 8*x+8 : 8*x+8]
			binary.BigEndian.PutUint16(d[0:], p)
			binary.BigEndian.PutUint16(d[2:], p)
			binary.BigEndian.PutUint16(d[4:], p)
			d[6], d[7] = 0xff, 0xff
		}
		return
	}
	uvY, ay := s.chromaRows(y, h)
	// h265 weighs in the row next to it wherever chromaRows gives one, even
	// where clamping at the edge makes it the row itself.
	adj := s.ssVer != 0 && y != 0 && !(y == h-1 && y%2 != 0)
	if s.fast {
		s.convertFast(luma, uvY, ay, adj, x0, x1, dst, sc)
		return
	}
	s.convertScalar(luma, uvY, ay, adj, x0, x1, dst, sc)
}

// pad normalises samples pa-1 to pb-1 of a row of chroma into dst[pa:pb+1],
// the row's edge samples repeated on either side of it (dst[0] and
// dst[uw+1]).
func (s *rowConverter[T]) pad(dst []float32, src []T, pa, pb int) []float32 {
	uw := s.uw
	dst = dst[:uw+2]
	// tableUV holds (v - biasUV) / rangeUV, as h265 works it out for each
	// sample (the fast kernel is never used for the identity matrix).
	tab := s.tableUV
	src = src[:uw]
	for p := pa; p <= pb; p++ {
		dst[p] = tab[min(src[min(max(p-1, 0), uw-1)], s.maxCh)]
	}
	return dst
}

// convertFast is h265's row kernel (convertRowGo, convertRow444Go,
// convertRow16Go, convertRow16x444Go).
func (s *rowConverter[T]) convertFast(luma []T, uvY, ay int, adj bool, x0, x1 int, dst []uint8, sc *rowScratch) {
	yf := sc.yf[:x1-x0]
	ty := s.tableY // (v - biasY) / rangeY
	for i, v := range luma {
		yf[i] = ty[min(v, s.maxCh)]
	}
	// The padded chroma the samples read.
	pa, pb := x0+1, x1
	if s.ssHor != 0 {
		pa, pb = x0>>1, (x1-1)>>1+2
	}
	u0 := s.pad(sc.uPad[0], s.rows(1, uvY), pa, pb)
	v0 := s.pad(sc.vPad[0], s.rows(2, uvY), pa, pb)
	u1, v1 := u0, v0
	if adj {
		u1 = s.pad(sc.uPad[1], s.rows(1, ay), pa, pb)
		v1 = s.pad(sc.vPad[1], s.rows(2, ay), pa, pb)
	}
	const w0, w1, w2 = 9.0 / 16.0, 3.0 / 16.0, 1.0 / 16.0
	crCoef, cbCoef, gcr, gcb, kg := s.crCoef, s.cbCoef, s.gcr, s.gcb, s.kg
	bpp := s.bpp
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
		if bpp == 4 {
			put8(dst[4*(x-x0):], r, g, b)
		} else {
			put16(dst[8*(x-x0):], r, g, b)
		}
	}
}

// convertScalar is h265's rgbRow, for the identity and YCgCo matrices.
func (s *rowConverter[T]) convertScalar(luma []T, uvY, ay int, adj bool, x0, x1 int, dst []uint8, sc *rowScratch) {
	tab := s.tableUV
	n, uw, maxCh := x1-x0, s.uw, s.maxCh
	cbRow, crRow := sc.cbRow[:n], sc.crRow[:n]
	cb0, cr0 := s.rows(1, uvY)[:uw], s.rows(2, uvY)[:uw]
	if s.ssHor == 0 {
		for x := x0; x < x1; x++ {
			cbRow[x-x0], crRow[x-x0] = tab[min(cb0[x], maxCh)], tab[min(cr0[x], maxCh)]
		}
	} else {
		cb1, cr1 := cb0, cr0
		if adj {
			cb1, cr1 = s.rows(1, ay)[:uw], s.rows(2, ay)[:uw]
		}
		const w0, w1, w2, w3 = 9.0 / 16.0, 3.0 / 16.0, 3.0 / 16.0, 1.0 / 16.0
		at := func(row []T, i int) float32 { return tab[min(row[i], maxCh)] }
		for x := x0; x < x1; x++ {
			i, j := s.uvIdx[x], s.uvAdj[x]
			cbRow[x-x0] = float32(w0*at(cb0, i)) + float32(w1*at(cb0, j)) + float32(w2*at(cb1, i)) + float32(w3*at(cb1, j))
			crRow[x-x0] = float32(w0*at(cr0, i)) + float32(w1*at(cr0, j)) + float32(w2*at(cr1, i)) + float32(w3*at(cr1, j))
		}
	}
	for x := range n {
		yv, u, v := s.tableY[min(luma[x], maxCh)], cbRow[x], crRow[x]
		d := dst[s.bpp*x:]
		switch s.matrix {
		case mcIdentity:
			s.put(d, v, yv, u)
		case mcYCgCo:
			t := yv - u
			s.put(d, t+v, yv+u, t-v)
		default:
			r := yv + float32(s.crCoef*v)
			b := yv + float32(s.cbCoef*u)
			g := yv - float32(float32(float32(s.gcr*v)+float32(s.gcb*u))*2)/s.kg
			s.put(d, r, g, b)
		}
	}
}
