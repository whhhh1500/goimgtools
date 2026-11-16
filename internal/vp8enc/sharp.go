package vp8enc

// sharpYUV converts RGB to Y'CbCr as libwebp's "sharp YUV" does
// (sharpyuv/sharpyuv.c and sharpyuv_dsp.c, with the gamma tables of
// sharpyuv_gamma.c in sharp_tables.go). Plain conversion averages the
// colour of each 2x2 block into its chroma sample, and a decoder that
// interpolates the chroma back between samples (as libwebp's and so the
// browsers' do) blurs colour edges and shifts the brightness of thin
// coloured lines. Sharp YUV looks instead for the luma and chroma from
// which that interpolation gives back the source most closely, comparing
// in linear light: starting from the plain conversion, it decodes what it
// has, and corrects luma and chroma by what came out wrong, four times
// over.
//
// libwebp does this on the whole image, one pass over all of it after the
// other, and may stop after two or three passes when a pass changes little
// or makes things worse. Here it is done as the rows arrive, keeping six
// pairs of rows: a pass over a pair of rows needs only the pair above
// as the same pass left it and the pair below as the pass before left it,
// so the four passes can follow each other down the image a pair apart. All
// four passes are always made (whether to stop needs the whole image);
// otherwise the arithmetic is libwebp's, and with four passes so is every
// sample (checked against libwebp's sharpyuv; see TestSharpLibwebp).
type sharpYUV struct {
	w, uvw  int // width rounded up to even, and half of it
	n       int // pairs taken
	last    int // index of the last pair, -1 until it is known
	emitted int // pairs given out
	done    [sharpPasses + 1]int
	// For each pair kept: the best W (luma-like) and chroma found so far,
	// and the targets they are corrected towards. W is two rows of w
	// samples; chroma is a row of R-W, a row of G-W and a row of B-W, uvw
	// samples each. Samples carry two bits more than the source.
	by, ty   [sharpRing][]uint16
	buv, tuv [sharpRing][]int16
	// Scratch: two rows of R, G and B planes (w samples each), and what
	// they convert to.
	src   [2][]uint16
	rgbY  []uint16
	rgbUV []int16
}

const (
	sharpPasses = 4
	// Pairs kept: the four being worked on, the one above them and the
	// one coming in.
	sharpRing = 6
	sharpMax  = 1<<10 - 1
)

func sharpBytes(width int) int64 {
	w := int64(width+1) &^ 1
	// per pair kept: 2 rows of uint16 twice, 3 half rows of int16 twice
	return sharpRing*(8*w+6*w) + 2*6*w + 4*w + 3*w
}

func newSharpYUV(width int) *sharpYUV {
	w := (width + 1) &^ 1
	s := &sharpYUV{w: w, uvw: w / 2, last: -1}
	for i := range s.by {
		s.by[i] = make([]uint16, 2*w)
		s.ty[i] = make([]uint16, 2*w)
		s.buv[i] = make([]int16, 3*s.uvw)
		s.tuv[i] = make([]int16, 3*s.uvw)
	}
	s.src[0] = make([]uint16, 3*w)
	s.src[1] = make([]uint16, 3*w)
	s.rgbY = make([]uint16, 2*w)
	s.rgbUV = make([]int16, 3*s.uvw)
	return s
}

// push takes the next pair of rows of non-premultiplied 8-bit RGBA, each
// holding at least w pixels (the converter repeats the last pixel of an
// odd width); final tells that it is the last pair. put is called with the
// index of each pair that is finished, in order, and may then collect it
// with yuv.
func (s *sharpYUV) push(r0, r1 []uint8, final bool, put func(p int) error) error {
	p := s.n
	i := p % sharpRing
	w := s.w
	importRow(r0, s.src[0], w)
	importRow(r1, s.src[1], w)
	by, ty := s.by[i], s.ty[i]
	storeGray(s.src[0], by[:w], w)
	storeGray(s.src[1], by[w:], w)
	updateW(s.src[0], ty[:w], w)
	updateW(s.src[1], ty[w:], w)
	updateChroma(s.src[0], s.src[1], s.tuv[i], s.uvw)
	copy(s.buv[i], s.tuv[i])
	s.n++
	s.done[0] = s.n
	if final {
		s.last = p
	}
	// Pass k may go over pair q once pass k-1 is past the pair below it
	// (or q is the last pair and pass k-1 is done with it).
	for k := 1; k <= sharpPasses; k++ {
		for {
			q := s.done[k]
			if !(s.done[k-1] > q+1 || q == s.last && s.done[k-1] > q) {
				break
			}
			s.pass(q)
			s.done[k]++
		}
	}
	for s.emitted < s.done[sharpPasses] {
		if err := put(s.emitted); err != nil {
			return err
		}
		s.emitted++
	}
	return nil
}

// pass makes one correction of pair q (libwebp's loop body in
// DoSharpArgbToYuv): it upsamples the chroma as a decoder would, converts
// the result as the targets were converted, and moves W and chroma by the
// difference from the targets.
func (s *sharpYUV) pass(q int) {
	i := q % sharpRing
	cur := s.buv[i]
	prev, next := cur, cur
	if q > 0 {
		prev = s.buv[(q-1)%sharpRing]
	}
	if q != s.last {
		next = s.buv[(q+1)%sharpRing]
	}
	w, uvw := s.w, s.uvw
	by := s.by[i]
	for c := 0; c < 3; c++ {
		a := cur[c*uvw : (c+1)*uvw]
		filterRow(a, prev[c*uvw:(c+1)*uvw], by[:w], s.src[0][c*w:(c+1)*w])
		filterRow(a, next[c*uvw:(c+1)*uvw], by[w:], s.src[1][c*w:(c+1)*w])
	}
	updateW(s.src[0], s.rgbY[:w], w)
	updateW(s.src[1], s.rgbY[w:], w)
	updateChroma(s.src[0], s.src[1], s.rgbUV, uvw)
	ty := s.ty[i]
	for j, v := range by {
		by[j] = clip10(int32(v) + int32(ty[j]) - int32(s.rgbY[j]))
	}
	tuv := s.tuv[i]
	for j := range cur {
		cur[j] += tuv[j] - s.rgbUV[j]
	}
}

// yuv writes the samples of finished pair p: len(y0) luma samples of its
// two rows, and len(u) chroma samples (libwebp's ConvertWRGBToYUV, with
// libwebp's matrix).
func (s *sharpYUV) yuv(p int, y0, y1, u, v []uint8) {
	i := p % sharpRing
	by, buv := s.by[i], s.buv[i]
	uvw := s.uvw
	cr, cg, cb := buv[:uvw], buv[uvw:2*uvw], buv[2*uvw:]
	for row, dst := range [2][]uint8{y0, y1} {
		wr := by[row*s.w:]
		for x := range dst {
			o := x >> 1
			W := int32(wr[x])
			r, g, b := int32(cr[o])+W, int32(cg[o])+W, int32(cb[o])+W
			dst[x] = clip8((16839*r + 33059*g + 6420*b + 16<<18 + 1<<17) >> 18)
		}
	}
	for x := range u {
		r, g, b := int32(cr[x]), int32(cg[x]), int32(cb[x])
		u[x] = clip8((-9719*r - 19081*g + 28800*b + 128<<18 + 1<<17) >> 18)
		v[x] = clip8((28800*r - 24116*g - 4684*b + 128<<18 + 1<<17) >> 18)
	}
}

// importRow spreads w pixels of RGBA into R, G and B planes, two bits up.
func importRow(rgba []uint8, dst []uint16, w int) {
	r, g, b := dst[:w], dst[w:2*w], dst[2*w:3*w]
	for x := range r {
		px := rgba[4*x : 4*x+3]
		r[x], g[x], b[x] = uint16(px[0])<<2, uint16(px[1])<<2, uint16(px[2])<<2
	}
}

func clip10(v int32) uint16 {
	return uint16(min(max(v, 0), sharpMax))
}

// toLinear and fromLinear convert between 10-bit gamma-coded samples and
// 16-bit linear light (libwebp's ToLinearSrgb and FromLinearSrgb at a bit
// depth of 10).
func toLinear(v uint16) uint32 { return gammaToLinear[v] }

func fromLinear(v uint32) uint32 {
	pos, x := v>>7, v&127
	v0, v1 := linearToGamma[pos]>>6, linearToGamma[pos+1]>>6
	return v0 + ((v1-v0)*x+64)>>7
}

// gray is the luma of a colour with Rec. 709 weights (libwebp's RGBToGray).
func gray(r, g, b int64) int64 {
	return (13933*r + 46871*g + 4732*b + 1<<15) >> 16
}

// storeGray sets W to the gray of the gamma-coded colours.
func storeGray(rgb, y []uint16, w int) {
	r, g, b := rgb[:w], rgb[w:2*w], rgb[2*w:3*w]
	for i := range y[:w] {
		y[i] = uint16(gray(int64(r[i]), int64(g[i]), int64(b[i])))
	}
}

// updateW sets W to the gray of the colours taken in linear light.
func updateW(rgb, y []uint16, w int) {
	r, g, b := rgb[:w], rgb[w:2*w], rgb[2*w:3*w]
	for i := range y[:w] {
		l := gray(int64(toLinear(r[i])), int64(toLinear(g[i])), int64(toLinear(b[i])))
		y[i] = uint16(fromLinear(uint32(l)))
	}
}

// scaleDown averages four samples in linear light.
func scaleDown(a, b, c, d uint16) int64 {
	return int64(fromLinear((toLinear(a) + toLinear(b) + toLinear(c) + toLinear(d) + 2) >> 2))
}

// updateChroma sets the chroma of each 2x2 block of two rows: its colour
// averaged in linear light, less the gray of that colour.
func updateChroma(rgb1, rgb2 []uint16, dst []int16, uvw int) {
	w := 2 * uvw
	for i := 0; i < uvw; i++ {
		x := 2 * i
		r := scaleDown(rgb1[x], rgb1[x+1], rgb2[x], rgb2[x+1])
		g := scaleDown(rgb1[w+x], rgb1[w+x+1], rgb2[w+x], rgb2[w+x+1])
		b := scaleDown(rgb1[2*w+x], rgb1[2*w+x+1], rgb2[2*w+x], rgb2[2*w+x+1])
		W := gray(r, g, b)
		dst[i], dst[uvw+i], dst[2*uvw+i] = int16(r-W), int16(g-W), int16(b-W)
	}
}

// filterRow upsamples one plane of a chroma row between row a, the nearer,
// and row b, as a decoder does (9-3-3-1), and adds it to the W row wy
// (libwebp's InterpolateTwoRows for one plane).
func filterRow(a, b []int16, wy, out []uint16) {
	n := len(a)
	out[0] = filter2(a[0], b[0], wy[0])
	for i := 0; i < n-1; i++ {
		a0, a1, b0, b1 := int32(a[i]), int32(a[i+1]), int32(b[i]), int32(b[i+1])
		v0 := (a0*9 + a1*3 + b0*3 + b1 + 8) >> 4
		v1 := (a1*9 + a0*3 + b1*3 + b0 + 8) >> 4
		out[2*i+1] = clip10(int32(wy[2*i+1]) + v0)
		out[2*i+2] = clip10(int32(wy[2*i+2]) + v1)
	}
	out[2*n-1] = filter2(a[n-1], b[n-1], wy[2*n-1])
}

func filter2(a, b int16, w uint16) uint16 {
	return clip10((int32(a)*3+int32(b)+2)>>2 + int32(w))
}
