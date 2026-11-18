package resize

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ScalePlanes is ScaleExtent for a source of *image.Gray strips (gray), or of
// *image.YCbCr strips of ratio r (4:4:4, 4:2:2, 4:2:0 or 4:4:0), such as a
// JPEG decoder gives: it scales each plane on its own, with the kernel's
// weights in 14-bit fixed point, into Gray strips or YCbCr strips of 4:4:4.
// The chroma planes are scaled from their own, subsampled, size to the full
// size of the result (whose chroma is as sharp as an RGBA scale's), there
// is less to scale than in RGBA and no colour conversion on either side.
// The pixels are not x/image's: it serves FastShrinkOnLoad, whose pixels
// already differ a little.
func ScalePlanes(src pipeline.Source, dw, dh int, q *draw.Kernel, ew, eh float64, r image.YCbCrSubsampleRatio, gray bool) (pipeline.Source, error) {
	sb := src.Bounds()
	if !(ew > 0 && ew <= float64(sb.Dx()) && eh > 0 && eh <= float64(sb.Dy())) {
		return nil, errors.New("goimgtools: resize extent outside the image")
	}
	if dw <= 0 || dh <= 0 || sb.Dx() <= 0 || sb.Dy() <= 0 || sb.Min != (image.Point{}) {
		return nil, errors.New("goimgtools: resize with an empty size")
	}
	if dw > math.MaxInt32/8 || dh > math.MaxInt32/8 || sb.Dx() > math.MaxInt32/8 || sb.Dy() > math.MaxInt32/8 {
		return nil, errors.New("goimgtools: resize dimensions too large")
	}
	p := &planes{src: src, env: pipeline.EnvOf(src), ratio: r, gray: gray, dw: dw, dh: dh, hdiv: 1, vdiv: 1}
	if !gray {
		var ok bool
		if p.hdiv, p.vdiv, ok = subsampling(r); !ok {
			return nil, errors.New("goimgtools: resize of an unsupported subsampling")
		}
	}
	p.n = pipeline.StripRows(int64(dw)*3, dh)

	sw, sh := sb.Dx(), sb.Dy()
	var need int64
	p.y, need = newPlaneScaler(q, sw, sh, dw, dh, ew, eh)
	if !gray {
		// A chroma sample spans hdiv × vdiv luma pixels: the luma extent
		// is ew/hdiv × eh/vdiv chroma samples.
		csw, csh := (sw+p.hdiv-1)/p.hdiv, (sh+p.vdiv-1)/p.vdiv
		var n1, n2 int64
		p.cb, n1 = newPlaneScaler(q, csw, csh, dw, dh, ew/float64(p.hdiv), eh/float64(p.vdiv))
		p.cr, n2 = newPlaneScaler(q, csw, csh, dw, dh, ew/float64(p.hdiv), eh/float64(p.vdiv))
		need += n1 + n2
	}
	need += int64(dw) * int64(p.n) * 3
	if err := p.env.Budget.Reserve(need, "resize planes"); err != nil {
		return nil, err
	}
	p.reserved = need
	return p, nil
}

// planes scales the planes of a Gray or YCbCr source.
type planes struct {
	src        pipeline.Source
	env        *pipeline.Env
	ratio      image.YCbCrSubsampleRatio
	gray       bool
	dw, dh     int
	hdiv, vdiv int
	n          int // rows per strip
	y, cb, cr  *planeScaler
	gband      *image.Gray
	yband      *image.YCbCr
	oy         int // first row of the next strip
	reserved   int64
	srcDone    bool
	err        error
}

func (p *planes) Env() *pipeline.Env      { return p.env }
func (p *planes) Bounds() image.Rectangle { return image.Rect(0, 0, p.dw, p.dh) }
func (p *planes) Opaque() bool            { return true }
func (p *planes) ColorModel() color.Model {
	if p.gray {
		return color.GrayModel
	}
	return color.YCbCrModel
}

func (p *planes) Close() error {
	if p.reserved != 0 {
		p.env.Budget.Release(p.reserved)
		p.reserved = 0
	}
	if p.err == nil {
		p.err = errors.New("goimgtools: resize closed")
	}
	return p.src.Close()
}

func (p *planes) Next() (image.Image, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.oy >= p.dh {
		return nil, io.EOF
	}
	oy0, oy1 := p.oy, min(p.oy+p.n, p.dh)
	rows := func(pix []byte, stride int) [][]byte {
		r := make([][]byte, oy1-oy0)
		for i := range r {
			r[i] = pix[i*stride : i*stride+p.dw]
		}
		return r
	}
	var out image.Image
	if p.gray {
		if p.gband == nil {
			p.gband = image.NewGray(image.Rect(0, 0, p.dw, p.n))
		}
		m := *p.gband
		m.Rect = image.Rect(0, oy0, p.dw, oy1)
		p.y.fill(oy0, rows(m.Pix, m.Stride))
		out = &m
	} else {
		if p.yband == nil {
			p.yband = image.NewYCbCr(image.Rect(0, 0, p.dw, p.n), image.YCbCrSubsampleRatio444)
		}
		m := *p.yband
		m.Rect = image.Rect(0, oy0, p.dw, oy1)
		p.y.fill(oy0, rows(m.Y, m.YStride))
		p.cb.fill(oy0, rows(m.Cb, m.CStride))
		p.cr.fill(oy0, rows(m.Cr, m.CStride))
		out = &m
	}
	for p.y.done < oy1 || (!p.gray && (p.cb.done < oy1 || p.cr.done < oy1)) {
		if err := p.feed(); err != nil {
			p.err = err
			return nil, err
		}
	}
	p.oy = oy1
	return out, nil
}

// feed scales the rows of the next source strip.
func (p *planes) feed() error {
	if p.srcDone {
		return errors.New("goimgtools: resize source ended early")
	}
	s, err := p.src.Next()
	if err == io.EOF {
		p.srcDone = true
		return errors.New("goimgtools: resize source ended early")
	}
	if err != nil {
		return err
	}
	b := s.Bounds()
	if p.gray {
		m, ok := s.(*image.Gray)
		if !ok {
			return errors.New("goimgtools: resize planes: the strips are not Gray")
		}
		for y := max(b.Min.Y, p.y.next); y < b.Max.Y; y++ {
			i := m.PixOffset(b.Min.X, y)
			p.y.feed(m.Pix[i : i+b.Dx()])
		}
		return nil
	}
	m, ok := s.(*image.YCbCr)
	if !ok || m.SubsampleRatio != p.ratio || b.Min.X != 0 {
		return errors.New("goimgtools: resize planes: the strips are not YCbCr of the expected ratio")
	}
	for y := max(b.Min.Y, p.y.next); y < b.Max.Y; y++ {
		i := m.YOffset(0, y)
		p.y.feed(m.Y[i : i+b.Dx()])
	}
	cw := (b.Dx() + p.hdiv - 1) / p.hdiv
	c0, c1 := b.Min.Y/p.vdiv, (b.Max.Y+p.vdiv-1)/p.vdiv
	for cy := max(c0, p.cb.next); cy < c1; cy++ {
		i := (cy - c0) * m.CStride
		p.cb.feed(m.Cb[i : i+cw])
		p.cr.feed(m.Cr[i : i+cw])
	}
	return nil
}

// fixBits is the fixed-point precision of the weights; the horizontal pass
// keeps hBits fractional bits of each sample for the vertical one.
const (
	fixBits = 14
	hBits   = 6
)

// fixDistrib is a distrib with integer weights that sum to 1<<fixBits for
// every destination pixel, n of them over n consecutive source pixels from
// start (zero where the kernel gives nothing), the same n for all.
type fixDistrib struct {
	n       int
	start   []int32
	weights []int32 // destination d's are weights[d*n : d*n+n]
}

func newFixDistrib(q *draw.Kernel, dw, sw int32, ext float64) fixDistrib {
	// newDistrib's arithmetic, one destination at a time: the weights of
	// destination x are those of the source pixels from i to j-1 whose
	// distance from the centre is within the kernel's support.
	scale := ext / float64(dw)
	halfWidth, kernelArgScale := q.Support, 1.0
	if scale > 1 {
		halfWidth *= scale
		kernelArgScale = 1 / scale
	}
	span := func(x int32) (center float64, i, j int32) {
		center = float64((float64(x)+0.5)*scale) - 0.5
		i = max(0, int32(math.Floor(center-halfWidth)))
		j = int32(math.Ceil(center + halfWidth))
		if j > sw {
			j = max(i, sw)
		}
		return center, i, j
	}
	n := int32(1)
	for x := range dw {
		_, i, j := span(x)
		n = max(n, j-i)
	}
	n = min(n, sw)
	f := fixDistrib{n: int(n), start: make([]int32, dw), weights: make([]int32, int(dw)*int(n))}
	fw := make([]float64, n)
	for x := range dw {
		center, i, j := span(x)
		start := max(0, min(i, sw-n))
		f.start[x] = start
		ws := f.weights[int(x)*int(n) : int(x+1)*int(n)]
		clear(fw)
		total := 0.0
		for c := i; c < j; c++ {
			t := abs((center - float64(c)) * kernelArgScale)
			if t >= q.Support {
				continue
			}
			w := q.At(t)
			fw[c-start] = w
			total += w
		}
		if total == 0 {
			// No weight (a kernel that is zero there): the nearest source
			// pixel.
			c := max(0, min(sw-1, int32(center+0.5)))
			ws[c-start] = 1 << fixBits
			continue
		}
		sum, big := int32(0), 0
		for k, w := range fw {
			v := int32(math.Round(w / total * (1 << fixBits)))
			ws[k] = v
			sum += v
			if v > ws[big] {
				big = k
			}
		}
		// The rounding error goes to the largest weight.
		ws[big] += 1<<fixBits - sum
	}
	return f
}

// planeScaler scales one plane, a source row at a time: each row is scaled
// across into a ring of rows, and each destination row is made from the ring
// once its last source row is in.
type planeScaler struct {
	sw, dw int
	hz, vt fixDistrib
	ring   [][]int16
	emitAt []int32 // destination row d is made once source row emitAt[d] is in
	next   int     // source rows fed
	done   int     // destination rows made
	acc    []int32
	// band holds destination rows bandY to bandY+len(band)-1, the strip
	// being filled; rows made past it wait in pend, in order.
	band  [][]byte
	bandY int
	pend  [][]byte
	free  [][]byte
	// With planeSIMD: the weights across in 16 bits, 16 per destination
	// sample, and how many groups of 8 samples from the first read no
	// further than the row's end; whether the weights down fit 16 bits,
	// and the rows and weight pairs of the destination row being made.
	hz16    []int16
	hGroups int
	vSIMD   bool
	vrows   []*int16
	vpairs  []uint32
}

// newPlaneScaler returns the scaler of a sw × sh plane into dw × dh, and the
// memory it takes.
func newPlaneScaler(q *draw.Kernel, sw, sh, dw, dh int, ew, eh float64) (*planeScaler, int64) {
	p := &planeScaler{sw: sw, dw: dw}
	p.hz = newFixDistrib(q, int32(dw), int32(sw), ew)
	p.vt = newFixDistrib(q, int32(dh), int32(sh), eh)
	p.emitAt = make([]int32, dh)
	// Destination row d needs source rows start[d] to start[d]+n-1, and is
	// made once the last of them, and those of every row before it, are in.
	ringRows, last := int32(1), int32(-1)
	for d := range dh {
		lo := p.vt.start[d]
		last = max(last, lo+int32(p.vt.n)-1)
		p.emitAt[d] = last
		ringRows = max(ringRows, last-lo+1)
	}
	p.ring = make([][]int16, ringRows)
	for i := range p.ring {
		p.ring[i] = make([]int16, dw)
	}
	p.acc = make([]int32, dw)
	n := int64(len(p.hz.weights)+len(p.vt.weights))*4 + int64(dw+dh)*8 + int64(ringRows)*int64(dw)*2 + int64(dw)*4
	if planeSIMD {
		n += p.prepareSIMD()
	}
	return p, n
}

// prepareSIMD sets what the SIMD passes need, where the weights fit them,
// and returns the memory it takes.
func (p *planeScaler) prepareSIMD() int64 {
	fit := func(ws []int32) bool {
		for _, w := range ws {
			if w < math.MinInt16 || w > math.MaxInt16 {
				return false
			}
		}
		return true
	}
	var mem int64
	if hz := &p.hz; hz.n <= 16 && fit(hz.weights) {
		// The destination samples whose 16 bytes from the first lie
		// within the row (start does not decrease), in groups of 8.
		e := 0
		for e < p.dw && int(hz.start[e])+16 <= p.sw {
			e++
		}
		if p.hGroups = e / 8; p.hGroups > 0 {
			p.hz16 = make([]int16, 8*p.hGroups*16)
			for x := range 8 * p.hGroups {
				for k, w := range hz.weights[x*hz.n : x*hz.n+hz.n] {
					p.hz16[x*16+k] = int16(w)
				}
			}
			mem += int64(len(p.hz16)) * 2
		}
	}
	if p.dw >= 16 && fit(p.vt.weights) {
		np := (p.vt.n + 1) / 2
		p.vSIMD = true
		p.vrows = make([]*int16, 2*np)
		p.vpairs = make([]uint32, np)
		mem += int64(np) * 20
	}
	return mem
}

// feed takes the next source row.
func (p *planeScaler) feed(row []byte) {
	s := p.next
	p.next++
	r := p.ring[s%len(p.ring)]
	hz := &p.hz
	n := hz.n
	x0 := 0
	if p.hGroups > 0 {
		planeHSIMD(&r[0], &row[0], &hz.start[0], &p.hz16[0], p.hGroups)
		x0 = 8 * p.hGroups
	}
	for x := x0; x < p.dw; x++ {
		ws := hz.weights[x*n : x*n+n]
		src := row[hz.start[x]:]
		src = src[:len(ws)]
		var a int32
		for k, w := range ws {
			a += int32(src[k]) * w
		}
		// Within int16 for any kernel whose weights add up, in absolute
		// value, to less than 2.5; clamped for the others.
		r[x] = int16(max(math.MinInt16, min(math.MaxInt16, (a+1<<(fixBits-hBits-1))>>(fixBits-hBits))))
	}
	vt := &p.vt
	for p.done < len(p.emitAt) && int(p.emitAt[p.done]) <= s {
		d := p.done
		p.done++
		var out []byte
		if k := d - p.bandY; k >= 0 && k < len(p.band) && len(p.pend) == 0 {
			out = p.band[k]
		} else if n := len(p.free); n > 0 {
			out, p.free = p.free[n-1], p.free[:n-1]
		} else {
			out = make([]byte, p.dw)
		}
		lo := int(vt.start[d])
		ws := vt.weights[d*vt.n : d*vt.n+vt.n]
		c0 := 0
		if p.vSIMD {
			// Rows in pairs; an odd one out goes with itself at weight 0.
			for k := 0; k < len(ws); k += 2 {
				a := p.ring[(lo+k)%len(p.ring)]
				b, wb := a, int32(0)
				if k+1 < len(ws) {
					b, wb = p.ring[(lo+k+1)%len(p.ring)], ws[k+1]
				}
				p.vrows[k], p.vrows[k+1] = &a[0], &b[0]
				p.vpairs[k/2] = uint32(uint16(ws[k])) | uint32(uint16(wb))<<16
			}
			chunks := p.dw / 16
			planeVSIMD(&out[0], &p.vrows[0], &p.vpairs[0], (len(ws)+1)/2, chunks)
			c0 = 16 * chunks
		}
		acc := p.acc[c0:]
		clear(acc)
		for k, w := range ws {
			if w == 0 {
				continue
			}
			src := p.ring[(lo+k)%len(p.ring)][c0:]
			for x, v := range src[:len(acc)] {
				acc[x] += int32(v) * w
			}
		}
		const sh = fixBits + hBits
		for x, a := range acc {
			out[c0+x] = uint8(max(0, min(255, (a+1<<(sh-1))>>sh)))
		}
		if len(p.pend) != 0 || d-p.bandY < 0 || d-p.bandY >= len(p.band) {
			p.pend = append(p.pend, out)
		}
	}
}

// fill starts the strip of destination rows y to y+len(band)-1: the rows
// already made go into it, and the next ones are made there.
func (p *planeScaler) fill(y int, band [][]byte) {
	p.band, p.bandY = band, y
	for len(p.pend) > 0 && p.done-len(p.pend) < y+len(band) {
		k := p.done - len(p.pend) - y
		copy(band[k], p.pend[0])
		p.free = append(p.free, p.pend[0])
		p.pend = p.pend[1:]
	}
}
