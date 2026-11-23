package ops

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The Gaussian blur of sharp (0.35, whose blur is libvips 8.17's
// gaussblur at its default integer precision), worked out here from what
// libvips does, to the byte:
//
//   - The mask is one row of 2r+1 weights: 20·exp(-x²/2σ²) rounded, for x
//     out to the last before the Gaussian falls below the minimum
//     amplitude of its peak; their sum is its scale.
//   - It is applied across and then down, each pass rounded to 8 bits, the
//     pixels beyond the edges the edge pixels repeated. libvips does each
//     pass with 8-bit weights where they are precise enough (its vector
//     path): each weight over the scale, times 128 / 2^shift and rounded,
//     where 2^shift is the largest weight over the scale rounded up to a
//     power of two and then doubled, and the sum, plus half of 2^(7-shift),
//     shifted right by 7-shift. Otherwise (a mask wider than 1024, or one
//     whose 8-bit weights put a grey of 128 more than 2 levels off) the
//     integer weights themselves, the sum plus half the scale divided by
//     the scale.
//   - An image with transparency is premultiplied first (each colour times
//     alpha/255 in single precision, the fraction dropped), all four
//     channels are blurred, and the colours divided again (times 255/alpha
//     in single precision, the fraction dropped; 0 where alpha is 0).
//
// The weights fall from the middle outwards and take at most 21 values, so
// the mask is a sum of at most 20 boxes nested in one another, each a
// weight times the sum of the pixels within a radius. That is how it is
// worked out here: the same integers, so the same result, in time that
// does not grow with sigma.

// kernelSanity is the widest half a mask may reach, as libvips.
const kernelSanity = 5000

// gaussKernel is a Gaussian mask as nested boxes.
type gaussKernel struct {
	r      int      // the mask is 2r+1 wide
	radius []int    // the boxes, the widest first
	weight []uint32 // what each box adds to the weight of the pixels within it
	shift  uint     // the vector path: (sum + 1<<(shift-1)) >> shift
	scale  uint32   // the integer path, when not 0: (sum + scale/2) / scale
	// For the SIMD passes: the byte offsets of the running totals of each
	// box's two ends from those of the pixel, and its weight eight times.
	hiOff, loOff []int64
	wt8          []uint32
}

// newGaussKernel makes the mask of sigma cut off below minAmpl of its peak.
func newGaussKernel(sigma, minAmpl float64) (*gaussKernel, error) {
	sig2 := 2 * sigma * sigma
	maxX := int(min(8*sigma, kernelSanity))
	x := 0
	for ; x < maxX; x++ {
		if math.Exp(-float64(x*x)/sig2) < minAmpl {
			break
		}
	}
	if x >= kernelSanity {
		return nil, errors.New("goimgtools: blur mask too large")
	}
	r := max(x-1, 0)
	// The weights from the middle outwards, and their sum.
	m := make([]float64, r+1)
	sum := 0.0
	for i := -r; i <= r; i++ {
		v := math.RoundToEven(20 * math.Exp(-float64(i*i)/sig2))
		if i >= 0 {
			m[i] = v
		}
		sum += v
	}
	if sum == 0 {
		sum = 1
	}
	k := &gaussKernel{r: r}
	w := make([]uint32, r+1)
	if mant, shift, ok := vectorWeights(m, r, sum); ok {
		k.shift = shift
		for i, v := range mant {
			w[i] = uint32(v)
		}
	} else {
		k.scale = uint32(sum)
		for i, v := range m {
			w[i] = uint32(v)
		}
	}
	// The boxes: from the outside in, a box wherever the weight goes up.
	cur := uint32(0)
	for i := r; i >= 0; i-- {
		if w[i] < cur {
			return nil, errors.New("goimgtools: blur mask not falling from the middle")
		}
		if w[i] > cur {
			k.radius = append(k.radius, i)
			k.weight = append(k.weight, w[i]-cur)
			cur = w[i]
		}
	}
	for j, b := range k.radius {
		k.hiOff = append(k.hiOff, int64(16*(r+b+1)))
		k.loOff = append(k.loOff, int64(16*(r-b)))
		for range 8 {
			k.wt8 = append(k.wt8, k.weight[j])
		}
	}
	return k, nil
}

// vectorWeights works out the 8-bit weights of libvips' vector path for
// the mask whose weights from the middle outwards are m, if it takes that
// path: the weights, and the shift of the sum.
func vectorWeights(m []float64, r int, sum float64) ([]int, uint, bool) {
	n := 2*r + 1
	if math.Ceil(math.Log2(float64(n))) > 10 {
		return nil, 0, false
	}
	mx := 0.0
	for _, v := range m {
		mx = max(mx, v/sum)
	}
	shift := int(math.Ceil(math.Log2(mx) + 1))
	exp := 7 - shift
	if exp <= 0 {
		return nil, 0, false
	}
	mant := make([]int, r+1)
	for i, v := range m {
		mant[i] = int(math.RoundToEven(float64(128*(v/sum)) * math.Pow(2, float64(-shift))))
		if mant[i] < -128 || mant[i] > 127 {
			return nil, 0, false
		}
	}
	// libvips checks the weights on a grey of 128, the weights of 0 left
	// out of both sums, in the order of the mask.
	trueSum, intSum := 0.0, 0
	for i := -r; i <= r; i++ {
		a := max(i, -i)
		if mant[a] != 0 {
			trueSum += float64(128 * (m[a] / sum))
			intSum += 128 * mant[a]
		}
	}
	trueValue := int(max(0, min(trueSum, 255)))
	intValue := max(0, min((intSum+1<<(exp-1))>>exp, 255))
	if max(trueValue-intValue, intValue-trueValue) > 2 {
		return nil, 0, false
	}
	return mant, uint(exp), true
}

// value is the 8-bit result of a weighted sum.
func (k *gaussKernel) value(sum uint32) uint8 {
	var v uint32
	if k.scale != 0 {
		v = (sum + k.scale/2) / k.scale
	} else {
		v = (sum + 1<<(k.shift-1)) >> k.shift
	}
	return uint8(min(v, 255))
}

// ---- across ----

// hkernel blurs a row across with a kernel: the sums of the boxes taken
// from running totals of the row, its ends repeated r pixels beyond it.
type hkernel struct {
	k     *gaussKernel
	w, nc int // nc: channels blurred; the alpha of an opaque image stays
	total []uint32
	sum   []uint32
}

func (h *hkernel) apply(dst, row []uint8) {
	k, w, r := h.k, h.w, h.k.r
	t := h.total
	// t[4i+c] is the sum of channel c of the first i pixels of the row
	// with its ends repeated, all four channels.
	clear(t[:4])
	edge := func(i int, p []uint8) {
		p = p[:4]
		q, s := t[4*i:4*i+4:4*i+4], t[4*i+4:4*i+8:4*i+8]
		s[0], s[1], s[2], s[3] = q[0]+uint32(p[0]), q[1]+uint32(p[1]), q[2]+uint32(p[2]), q[3]+uint32(p[3])
	}
	for i := 0; i < r; i++ {
		edge(i, row)
	}
	simd := useKernelSIMD && k.scale == 0 && len(k.radius) > 0
	if simd {
		kernelPrefixSIMD(&t[4*r], &row[0], w)
	} else {
		body, run := t[4*r:4*(r+w)+4], row[:4*w]
		body = body[:len(run)+4]
		for i, p := range run {
			body[i+4] = body[i] + uint32(p)
		}
	}
	for i := r + w; i < w+2*r; i++ {
		edge(i, row[4*(w-1):])
	}
	// A box of radius b about x is the pixels from x+r-b to x+r+b of the
	// row with its ends repeated.
	sum, d := h.sum[:4*w], dst[:4*w]
	if simd {
		// Eight bytes at a time, the alpha of an opaque image too (vkernel
		// makes it 255 again); the rest below.
		n8 := 4 * w &^ 7
		if n8 > 0 {
			kernelHSIMD(&d[0], &t[0], &k.hiOff[0], &k.loOff[0], &k.wt8[0], len(k.radius), n8, 1<<(k.shift-1), uint32(k.shift), 0)
		}
		sum, d, t = sum[n8:], d[n8:], t[n8:]
	}
	clear(sum)
	for j, b := range k.radius {
		wt, hi, lo := k.weight[j], t[4*(r+b+1):], t[4*(r-b):]
		hi, lo = hi[:len(sum)], lo[:len(sum)]
		for i := range sum {
			sum[i] += wt * (hi[i] - lo[i])
		}
	}
	if h.nc == 4 || simd {
		for i, v := range sum {
			d[i] = k.value(v)
		}
		return
	}
	for i := 0; i+3 < len(sum); i += 4 {
		v, q := sum[i:i+3:i+3], d[i:i+3:i+3]
		q[0], q[1], q[2] = k.value(v[0]), k.value(v[1]), k.value(v[2])
	}
}

// ---- down ----

// vkernel blurs down with a kernel: a window of rows of its input, kept in
// a store, and the weighted sum of the boxes about the row to work out,
// column by column. That is moved down a row by adding, for each box, its
// weight times the row that comes in at its bottom less the one that
// leaves at its top, in unsigned arithmetic, which may wrap on the way but
// not at the end. It works out a batch of rows at a time, its columns
// shared out; with a store on disk, one row at a time, box by box.
type vkernel struct {
	prev      rowSource
	k         *gaussKernel
	w4, h     int
	opaque    bool // the alpha stays 255
	store     rowStore
	disk      bool
	sum       []uint32 // the weighted sum about the next row to work out
	box       []uint32 // a box, while the sum about the first row is made
	out       [][]uint8
	in, gone  [][][]uint8 // the rows that move each box, for each row of the batch
	inP       [][]*byte   // the starts of in and gone, for the SIMD pass
	goneP     [][]*byte
	work      batch
	got, next int // input rows taken; next output row to work out
	n, handed int // rows of the batch, of them handed out
}

func (v *vkernel) row(i int) ([]uint8, error) {
	return v.store.get(min(max(i, 0), v.h-1))
}

func (v *vkernel) nextRow() ([]uint8, error) {
	if v.handed < v.n {
		v.handed++
		return v.out[v.handed-1], nil
	}
	y0 := v.next
	if y0 >= v.h {
		return nil, io.EOF
	}
	n := min(len(v.out), v.h-y0)
	k := v.k
	for need := min(y0+n+k.r, v.h-1); v.got <= need; v.got++ {
		r, err := v.prev.nextRow()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if err := v.store.put(v.got, r); err != nil {
			return nil, err
		}
	}
	if y0 == 0 {
		// The boxes about row 0 from the narrowest out, each the one before
		// and its two ends.
		clear(v.sum)
		clear(v.box)
		done := -1
		for j := len(k.radius) - 1; j >= 0; j-- {
			for i := done + 1; i <= k.radius[j]; i++ {
				for _, y := range [2]int{i, -i} {
					r, err := v.row(y)
					if err != nil {
						return nil, err
					}
					box := v.box[:len(r)]
					for x, p := range r {
						box[x] += uint32(p)
					}
					if i == 0 {
						break
					}
				}
			}
			done = k.radius[j]
			wt := k.weight[j]
			for x, b := range v.box {
				v.sum[x] += wt * b
			}
		}
	}
	emit := func(i, lo, hi int) {
		sum := v.sum[lo:hi]
		out := v.out[i][lo:hi]
		out = out[:len(sum)]
		for x, s := range sum {
			out[x] = k.value(s)
		}
		if v.opaque {
			for x := 3; x < len(out); x += 4 {
				out[x] = 255
			}
		}
	}
	move := func(wt uint32, in, gone []uint8, lo, hi int) {
		sum := v.sum[lo:hi]
		a, g := in[lo:hi], gone[lo:hi]
		a, g = a[:len(sum)], g[:len(sum)]
		for x := range sum {
			sum[x] += wt * (uint32(a[x]) - uint32(g[x]))
		}
	}
	if v.disk {
		// A row got from the disk is good for three more gets: each box is
		// moved with the two rows got for it.
		for i := 0; i < n; i++ {
			emit(i, 0, v.w4)
			for j, b := range k.radius {
				a, err := v.row(y0 + i + b + 1)
				if err != nil {
					return nil, err
				}
				g, err := v.row(y0 + i - b)
				if err != nil {
					return nil, err
				}
				move(k.weight[j], a, g, 0, v.w4)
			}
		}
	} else {
		// A store in memory hands out its rows as they are, without fail.
		simd := useKernelSIMD && k.scale == 0 && len(k.radius) > 0
		for i := 0; i < n; i++ {
			for j, b := range k.radius {
				v.in[i][j], _ = v.row(y0 + i + b + 1)
				v.gone[i][j], _ = v.row(y0 + i - b)
				if simd {
					v.inP[i][j], v.goneP[i][j] = &v.in[i][j][0], &v.gone[i][j][0]
				}
			}
		}
		var alpha uint64
		if v.opaque {
			alpha = 0xff000000ff000000
		}
		// A few thousand columns at a time, whose sums stay in the cache
		// while every box moves them.
		v.work.split(v.w4, 4096, 64, func(lo, hi int) {
			for t := lo; t < hi; t += 2048 {
				te := min(t+2048, hi)
				for i := 0; i < n; i++ {
					if simd {
						// Eight bytes at a time; the rest below.
						if n8 := (te - t) &^ 7; n8 > 0 {
							kernelVSIMD(&v.out[i][t], &v.sum[t], &v.inP[i][0], &v.goneP[i][0], &k.wt8[0], len(k.radius), n8, t,
								1<<(k.shift-1), uint32(k.shift), alpha)
							if t+n8 == te {
								continue
							}
							emit(i, t+n8, te)
							for j, wt := range k.weight {
								move(wt, v.in[i][j], v.gone[i][j], t+n8, te)
							}
							continue
						}
					}
					emit(i, t, te)
					for j, wt := range k.weight {
						move(wt, v.in[i][j], v.gone[i][j], t, te)
					}
				}
			}
		})
	}
	v.next += n
	v.n, v.handed = n, 1
	return v.out[0], nil
}

// ---- the stage ----

// kernelPasses puts the passes of the kernel behind f's input.
func (f *filterStage) kernelPasses(k *gaussKernel) (rowSource, error) {
	w, h := f.in.w, f.in.h
	w4 := 4 * w
	rows, ways := f.work.rows, f.work.ways
	nc := 4
	if f.opaque {
		nc = 3
	}
	// The blurred rows of a batch, and the running totals of each part of
	// the work.
	if err := f.reserve(int64(w4)*int64(rows)+int64(ways)*4*int64(2*w4+8*k.r+4), "blur rows"); err != nil {
		return nil, err
	}
	f.in.out = rowBatch(rows, w4)
	f.in.hblur = make([]rowPass, ways)
	for g := range f.in.hblur {
		f.in.hblur[g] = &hkernel{k: k, w: w, nc: nc, total: make([]uint32, 4*(w+2*k.r+1)), sum: make([]uint32, w4)}
	}
	nb := len(k.radius)
	// The weighted sum and a box, and the batch.
	if err := f.reserve(int64(w4)*int64(8+rows), "blur sums"); err != nil {
		return nil, err
	}
	// The rows of a batch and the r above and r+1 below it.
	st, err := f.newStore(min(2*k.r+2+rows, h), w4)
	if err != nil {
		return nil, err
	}
	_, disk := st.(*diskStore)
	v := &vkernel{prev: f.in, k: k, w4: w4, h: h, opaque: f.opaque, store: st, disk: disk,
		sum: make([]uint32, w4), box: make([]uint32, w4), out: rowBatch(rows, w4), work: f.work}
	if !disk {
		v.in, v.gone = make([][][]uint8, rows), make([][][]uint8, rows)
		v.inP, v.goneP = make([][]*byte, rows), make([][]*byte, rows)
		for i := range v.in {
			v.in[i], v.gone[i] = make([][]uint8, nb), make([][]uint8, nb)
			v.inP[i], v.goneP[i] = make([]*byte, nb), make([]*byte, nb)
		}
	}
	return v, nil
}

// premultiplied is src with its colours multiplied by alpha as libvips
// does it, as RGBA strips.
func premultiplied(src pipeline.Source) pipeline.Source {
	var f [256]float32
	for a := range f {
		f[a] = float32(float64(a) / 255)
	}
	return rgbaStrips{pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			if a := p[3]; a != 255 {
				m := f[a]
				p[0], p[1], p[2] = uint8(float32(p[0])*m), uint8(float32(p[1])*m), uint8(float32(p[2])*m)
			}
		}
		return nil
	})}
}

// rgbaStrips hands out the NRGBA strips of its source, whose colours are
// already multiplied by alpha, as RGBA.
type rgbaStrips struct{ pipeline.Source }

func (s rgbaStrips) ColorModel() color.Model { return color.RGBAModel }

// Opaque is the source's: its strips only change type.
func (s rgbaStrips) Opaque() bool { return pipeline.IsOpaque(s.Source) }

func (s rgbaStrips) Next() (image.Image, error) {
	m, err := s.Source.Next()
	if err != nil {
		return nil, err
	}
	n := m.(*image.NRGBA)
	return &image.RGBA{Pix: n.Pix, Stride: n.Stride, Rect: n.Rect}, nil
}

// MinAmplitude is the default cut-off of BlurLibvips, as sharp's.
const MinAmplitude = 0.2

// BlurLibvips blurs src as sharp's blur does (see gaussKernel), to the
// byte: with the Gaussian of standard deviation sigma pixels, cut off where
// it falls below minAmpl of its peak. An image with transparency gives
// NRGBA strips, one without RGBA.
func BlurLibvips(src pipeline.Source, sigma, minAmpl float64) (pipeline.Source, error) {
	if err := checkSigma(sigma); err != nil {
		return nil, err
	}
	if !(minAmpl >= 0.001 && minAmpl <= 1) {
		return nil, errors.New("goimgtools: blur minimum amplitude must be 0.001 to 1")
	}
	k, err := newGaussKernel(sigma, minAmpl)
	if err != nil {
		return nil, err
	}
	opaque := pipeline.IsOpaque(src)
	if !opaque {
		src = premultiplied(src)
	}
	f := newFilter(src)
	f.opaque = opaque
	if err := f.setup(func() (err error) {
		f.last, err = f.kernelPasses(k)
		return err
	}); err != nil {
		return nil, f.fail(err)
	}
	if opaque {
		f.finish = func(_ int, row []uint8, dst []uint8) error {
			copy(dst, row)
			return nil
		}
		return f, nil
	}
	var div [256]float32
	for a := 1; a < 256; a++ {
		div[a] = float32(255 / float64(a))
	}
	f.straight = true
	f.finish = func(_ int, row []uint8, dst []uint8) error {
		for i := 0; i+3 < len(row); i += 4 {
			p, q := row[i:i+4:i+4], dst[i:i+4:i+4]
			m := div[p[3]]
			for c := 0; c < 3; c++ {
				q[c] = uint8(min(float32(p[c])*m, 255))
			}
			q[3] = p[3]
		}
		return nil
	}
	return f, nil
}
