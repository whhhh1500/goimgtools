package vp8enc

import "github.com/whhhh1500/goimgtools/pipeline"

// sharpYUV converts RGB to Y'CbCr as libwebp's "sharp YUV" does
// (sharpyuv/sharpyuv.c and sharpyuv_dsp.c, with the gamma tables of
// sharpyuv_gamma.c in sharp_tables.go). Plain conversion averages the
// colour of each 2x2 block into its chroma sample, and a decoder that
// interpolates the chroma back between samples (as libwebp's and so the
// browsers' do) blurs colour edges and shifts the brightness of thin
// coloured lines. Sharp YUV looks instead for the luma and chroma from
// which that interpolation gives back the source most closely, comparing
// in linear light: starting from the plain conversion, it decodes what it
// has, and corrects luma and chroma by what came out wrong, twice over.
//
// libwebp does this on the whole image, one pass over all of it after the
// other, and goes on to a third and a fourth only while the pass just made
// still found luma off by 3/4 of a level a sample or more on average, and
// by less than the pass before had. On real pictures it stops after two:
// on each of 39 frames of 13 GIF animations and of 35 photographs the
// samples made here are libwebp's (the second pass found luma off by 2/3
// of a level at most, on a dithered banner, and by 1/8 on a photograph).
// Here the conversion is done as the rows arrive, keeping four pairs of
// rows: a pass over a pair of rows needs only the pair above as the same
// pass left it and the pair below as the pass before left it, so the
// second pass can follow the first down the image a pair behind. Whether
// to go on needs the whole image, so two passes are always made: a
// drawing of thin saturated lines all over, on which libwebp makes three
// or four, comes out with other samples than libwebp's, and its lines as
// bright (TestSharpColour).
//
// Four passes always, which this package made until 0.3.1, were no gain:
// the 39 frames coded 1.8% larger at the same SSIMULACRA2 and 2.6% at the
// same butteraugli (those of a heavily dithered animation 5% and 6%),
// 2.7% smaller at the same PSNR; the photographs the same within 0.4%.
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
	// With helpers the columns of each step are shared out among parts:
	// every sample depends on its own column and on chroma next to it
	// that the step only reads, so the result is the same.
	env   *pipeline.Env
	parts int
}

const (
	sharpPasses = 2
	// Pairs kept: those being worked on, the one above them and the one
	// coming in.
	sharpRing = sharpPasses + 2
	sharpMax  = 1<<10 - 1
	// sharpPartMin is the fewest columns a part of a step is given.
	sharpPartMin = 512
)

// split calls fn for even columns x0 to x1 covering the width: once, or
// for each part at the same time.
func (s *sharpYUV) split(fn func(x0, x1 int)) {
	if s.parts <= 1 {
		fn(0, s.w)
		return
	}
	s.env.Parallel(s.parts, func(k int) {
		x0, x1 := k*s.w/s.parts&^1, (k+1)*s.w/s.parts&^1
		if k == s.parts-1 {
			x1 = s.w
		}
		fn(x0, x1)
	})
}

func sharpBytes(width int) int64 {
	w := int64(width+1) &^ 1
	// per pair kept: 2 rows of uint16 twice, 3 half rows of int16 twice
	return sharpRing*(8*w+6*w) + 2*6*w + 4*w + 3*w
}

func newSharpYUV(width int, env *pipeline.Env) *sharpYUV {
	w := (width + 1) &^ 1
	s := &sharpYUV{w: w, uvw: w / 2, last: -1, env: env, parts: 1}
	if env != nil {
		s.parts = max(1, min(env.Helpers()+1, w/sharpPartMin))
	}
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
	by, ty := s.by[i], s.ty[i]
	s.split(func(x0, x1 int) {
		importRow(r0, s.src[0], w, x0, x1)
		importRow(r1, s.src[1], w, x0, x1)
		storeGray(s.src[0], by[:w], w, x0, x1)
		storeGray(s.src[1], by[w:], w, x0, x1)
		updateW(s.src[0], ty[:w], w, x0, x1)
		updateW(s.src[1], ty[w:], w, x0, x1)
		updateChroma(s.src[0], s.src[1], s.tuv[i], s.uvw, x0/2, x1/2)
	})
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
	by, ty := s.by[i], s.ty[i]
	// The chroma is only read until every part is done with it.
	s.split(func(x0, x1 int) {
		for c := 0; c < 3; c++ {
			a := cur[c*uvw : (c+1)*uvw]
			filterRow(a, prev[c*uvw:(c+1)*uvw], by[:w], s.src[0][c*w:(c+1)*w], x0, x1)
			filterRow(a, next[c*uvw:(c+1)*uvw], by[w:], s.src[1][c*w:(c+1)*w], x0, x1)
		}
		updateW(s.src[0], s.rgbY[:w], w, x0, x1)
		updateW(s.src[1], s.rgbY[w:], w, x0, x1)
		updateChroma(s.src[0], s.src[1], s.rgbUV, uvw, x0/2, x1/2)
		for _, j0 := range [2]int{x0, w + x0} {
			j := j0
			if useSIMD {
				if n := (x1 - x0) &^ 15; n > 0 {
					updateBySIMD(&by[j], &ty[j], &s.rgbY[j], n)
					j += n
				}
			}
			for ; j < j0+x1-x0; j++ {
				by[j] = clip10(int32(by[j]) + int32(ty[j]) - int32(s.rgbY[j]))
			}
		}
	})
	tuv := s.tuv[i]
	j := 0
	if useSIMD {
		if j = len(cur) &^ 15; j > 0 {
			updateUVSIMD(&cur[0], &tuv[0], &s.rgbUV[0], j)
		}
	}
	for ; j < len(cur); j++ {
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
	s.split(func(x0, x1 int) {
		for row, dst := range [2][]uint8{y0, y1} {
			wr := by[row*s.w:]
			for x := x0; x < min(x1, len(dst)); x++ {
				o := x >> 1
				W := int32(wr[x])
				r, g, b := int32(cr[o])+W, int32(cg[o])+W, int32(cb[o])+W
				dst[x] = clip8((16839*r + 33059*g + 6420*b + 16<<18 + 1<<17) >> 18)
			}
		}
		for x := x0 / 2; x < min(x1/2, len(u)); x++ {
			r, g, b := int32(cr[x]), int32(cg[x]), int32(cb[x])
			u[x] = clip8((-9719*r - 19081*g + 28800*b + 128<<18 + 1<<17) >> 18)
			v[x] = clip8((28800*r - 24116*g - 4684*b + 128<<18 + 1<<17) >> 18)
		}
	})
}

// importRow spreads pixels x0 to x1 of RGBA into R, G and B planes of w
// samples, two bits up.
func importRow(rgba []uint8, dst []uint16, w, x0, x1 int) {
	r, g, b := dst[:w], dst[w:2*w], dst[2*w:3*w]
	for x := x0; x < x1; x++ {
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

// storeGray sets W to the gray of the gamma-coded colours (samples x0 to
// x1).
func storeGray(rgb, y []uint16, w, x0, x1 int) {
	r, g, b := rgb[:w], rgb[w:2*w], rgb[2*w:3*w]
	for i := x0; i < x1; i++ {
		y[i] = uint16(gray(int64(r[i]), int64(g[i]), int64(b[i])))
	}
}

// updateW sets W to the gray of the colours taken in linear light
// (samples x0 to x1).
func updateW(rgb, y []uint16, w, x0, x1 int) {
	r, g, b := rgb[:w], rgb[w:2*w], rgb[2*w:3*w]
	for i := x0; i < x1; i++ {
		l := gray(int64(toLinear(r[i])), int64(toLinear(g[i])), int64(toLinear(b[i])))
		y[i] = uint16(fromLinear(uint32(l)))
	}
}

// scaleDown averages four samples in linear light.
func scaleDown(a, b, c, d uint16) int64 {
	return int64(fromLinear((toLinear(a) + toLinear(b) + toLinear(c) + toLinear(d) + 2) >> 2))
}

// updateChroma sets the chroma of each 2x2 block of two rows (blocks c0 to
// c1): its colour averaged in linear light, less the gray of that colour.
func updateChroma(rgb1, rgb2 []uint16, dst []int16, uvw, c0, c1 int) {
	w := 2 * uvw
	for i := c0; i < c1; i++ {
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
// (libwebp's InterpolateTwoRows for one plane): samples x0 to x1, both
// even.
func filterRow(a, b []int16, wy, out []uint16, x0, x1 int) {
	n := len(a)
	x := x0
	if x == 0 {
		out[0] = filter2(a[0], b[0], wy[0])
	} else {
		// The second sample of the pair of chroma x/2-1 and x/2.
		i := x/2 - 1
		a0, a1, b0, b1 := int32(a[i]), int32(a[i+1]), int32(b[i]), int32(b[i+1])
		out[x] = clip10(int32(wy[x]) + (a1*9+a0*3+b1*3+b0+8)>>4)
	}
	x++
	if useSIMD {
		// Whole groups of eight pairs: x+2*np is x1-1 at most, and the
		// pairs read the chroma up to x1/2-1.
		if np := (x1 - x - 1) / 2 &^ 7; np > 0 {
			i := x / 2
			filterPairsSIMD(&a[i], &b[i], &wy[x], &out[x], np)
			x += 2 * np
		}
	}
	for ; x+1 < x1; x += 2 {
		i := x / 2
		a0, a1, b0, b1 := int32(a[i]), int32(a[i+1]), int32(b[i]), int32(b[i+1])
		v0 := (a0*9 + a1*3 + b0*3 + b1 + 8) >> 4
		v1 := (a1*9 + a0*3 + b1*3 + b0 + 8) >> 4
		out[x] = clip10(int32(wy[x]) + v0)
		out[x+1] = clip10(int32(wy[x+1]) + v1)
	}
	// x is x1-1: the first of a pair, or the last sample of the row.
	if i := x / 2; i == n-1 {
		out[x] = filter2(a[n-1], b[n-1], wy[x])
	} else {
		a0, a1, b0, b1 := int32(a[i]), int32(a[i+1]), int32(b[i]), int32(b[i+1])
		out[x] = clip10(int32(wy[x]) + (a0*9+a1*3+b0*3+b1+8)>>4)
	}
}

func filter2(a, b int16, w uint16) uint16 {
	return clip10((int32(a)*3+int32(b)+2)>>2 + int32(w))
}
