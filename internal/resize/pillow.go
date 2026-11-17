package resize

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Pillow is Pillow's Image.resize (Pillow 12, Resample.c), worked out here
// from what it does, to the byte:
//
//   - Across, then down (only the rows the second pass reads): for each
//     output pixel the source pixels within the filter's support, scaled
//     by the factor when it reduces, about the centre (i+0.5)·factor,
//     weighted by the filter at their distance in double precision,
//     normalised by the sum, then times 2^22 rounded half away from zero;
//     the sum plus 2^21 shifted right by 22, kept within 0 to 255.
//   - Nearest takes, for output pixel i, the source pixel at
//     (0.5·factor + factor + … ) truncated: the place added to pixel after
//     pixel (Pillow's affine scaling), across and down.
//   - With transparency (RGBA, LA) the colours are first multiplied by
//     alpha (Pillow's RGBa: c·a/255 rounded, (t + (t>>8)) >> 8 of
//     t = c·a + 128) and divided again after (255·c / alpha, whole numbers,
//     at most 255; as they are where alpha is 0 or 255); not for nearest.
//
// With o.Gap (Pillow's reducing_gap) an image without transparency is first
// reduced by whole factors (Pillow's Image.reduce: the average of each box
// of pixels, a box the edge cuts the average of what it holds, the sum
// plus half the count times 2^32/(256·count) in single precision shifted
// right by 24), from the part of it the filter reaches (_get_safe_box),
// leaving at least o.Gap to the resize; o.Box is the part of the source
// resized (Pillow's box), in source pixels, which need not be whole.
//
// The result is NRGBA, or Gray for a Gray source.
func Pillow(src pipeline.Source, w, h int, f PillowFilter, o PillowOptions) (pipeline.Source, error) {
	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if w < 1 || h < 1 || w > 1<<26 || h > 1<<26 || sw < 1 || sh < 1 {
		return nil, errors.New("goimgtools: resize to no pixels, or too many")
	}
	if !f.Nearest && (f.At == nil || !(f.Support > 0) || math.IsInf(f.Support, 0)) {
		return nil, errors.New("goimgtools: a resize Filter needs an At function and a positive Support")
	}
	box := o.Box
	if box == ([4]float64{}) {
		box = [4]float64{0, 0, float64(sw), float64(sh)}
	}
	if !(box[0] >= 0 && box[1] >= 0 && box[2] <= float64(sw) && box[3] <= float64(sh) && box[2] > box[0] && box[3] > box[1]) {
		return nil, errors.New("goimgtools: the box resized is not within the image")
	}
	if w == sw && h == sh && box == [4]float64{0, 0, float64(sw), float64(sh)} {
		return src, nil
	}
	opaque := pipeline.IsOpaque(src)
	pre := !opaque && !f.Nearest
	in := pipeline.Map(src, func(s *image.NRGBA) error {
		if !pre {
			return nil
		}
		for y := 0; y < s.Rect.Dy(); y++ {
			row := s.Pix[y*s.Stride:][:4*s.Rect.Dx()]
			for x := 0; x+3 < len(row); x += 4 {
				a := uint32(row[x+3])
				for c := 0; c < 3; c++ {
					t := uint32(row[x+c])*a + 128
					row[x+c] = uint8((t>>8 + t) >> 8)
				}
			}
		}
		return nil
	})
	p := &pillowResize{src: in, env: pipeline.EnvOf(src), sw: sw, sh: sh, inW: sw, inH: sh, w: w, h: h, f: f, opaque: opaque, pre: pre,
		gray: src.ColorModel() == color.GrayModel}
	// Pillow reduces first only an image without transparency (the RGBa
	// one is resized without reducing_gap).
	if o.Gap > 0 && !f.Nearest && opaque {
		fx := max(1, int(float64(float64((box[2]-box[0])/float64(w))/o.Gap)))
		fy := max(1, int(float64(float64((box[3]-box[1])/float64(h))/o.Gap)))
		if fx > 1 || fy > 1 {
			// _get_safe_box: the part the filter reaches, in whole pixels.
			support := f.Support - 0.5
			sx := float64(support * float64((box[2]-box[0])/float64(w)))
			sy := float64(support * float64((box[3]-box[1])/float64(h)))
			rb := [4]int{max(0, int(box[0]-sx)), max(0, int(box[1]-sy)), min(sw, int(math.Ceil(box[2]+sx))), min(sh, int(math.Ceil(box[3]+sy)))}
			p.red = &pillowReduce{fx: fx, fy: fy, rb: rb}
			p.inW, p.inH = (rb[2]-rb[0]+fx-1)/fx, (rb[3]-rb[1]+fy-1)/fy
			box = [4]float64{(box[0] - float64(rb[0])) / float64(fx), (box[1] - float64(rb[1])) / float64(fy),
				(box[2] - float64(rb[0])) / float64(fx), (box[3] - float64(rb[1])) / float64(fy)}
		}
	}
	// Pillow takes the box in single precision.
	for i, v := range box {
		p.box[i] = float32(v)
	}
	if err := p.setup(); err != nil {
		in.Close()
		return nil, err
	}
	return p, nil
}

// PillowOptions are the settings of Pillow beyond the size and filter.
type PillowOptions struct {
	Box [4]float64 // left, top, right, bottom; all 0 for the whole image
	Gap float64    // reducing_gap, 0 for none
}

// pillowReduce is Image.reduce by fx × fy of the box rb (left, top, right,
// bottom) of the source.
type pillowReduce struct {
	fx, fy int
	rb     [4]int
	sums   []uint32
	row    []uint8
}

// reduceMult is Pillow's division_UINT32(n, 8): 2^32/(256·n) in single
// precision, the fraction dropped.
func reduceMult(n int) uint32 {
	return uint32(float32(float32(1<<32) / float32(uint32(256*n))))
}

// PillowFilter is a filter of Pillow's resize: At reaching Support either
// side, or Nearest.
type PillowFilter struct {
	At      func(float64) float64
	Support float64
	Nearest bool
}

// Pillow's filters.
var (
	PillowNearest  = PillowFilter{Nearest: true}
	PillowBox      = PillowFilter{At: pillowBox, Support: 0.5}
	PillowBilinear = PillowFilter{At: pillowBilinear, Support: 1}
	PillowHamming  = PillowFilter{At: pillowHamming, Support: 1}
	PillowBicubic  = PillowFilter{At: pillowBicubic, Support: 2}
	PillowLanczos  = PillowFilter{At: pillowLanczos, Support: 3}
)

func pillowBox(x float64) float64 {
	if x > -0.5 && x <= 0.5 {
		return 1
	}
	return 0
}

func pillowBilinear(x float64) float64 {
	x = math.Abs(x)
	if x < 1 {
		return 1 - x
	}
	return 0
}

func pillowHamming(x float64) float64 {
	x = math.Abs(x)
	if x == 0 {
		return 1
	}
	if x >= 1 {
		return 0
	}
	x = x * math.Pi
	// 0.54f and 0.46f: single-precision constants.
	return float64(math.Sin(x)/x) * (float64(float32(0.54)) + float64(float64(float32(0.46))*math.Cos(x)))
}

func pillowBicubic(x float64) float64 {
	const a = -0.5
	x = math.Abs(x)
	if x < 1 {
		t := float64((a + 2.0) * x)
		t = float64((t - (a + 3.0)) * x)
		return float64(t*x) + 1
	}
	if x < 2 {
		t := float64((x - 5) * x)
		t = float64((t + 8) * x)
		return (t - 4) * a
	}
	return 0
}

func pillowLanczos(x float64) float64 {
	if -3 <= x && x < 3 {
		return sinc(x) * sinc(x/3)
	}
	return 0
}

const pillowBits = 22 // PRECISION_BITS

// pillowAxis is the coefficients of one direction: for each output its
// first source pixel and how many, and their weights in fixed point.
type pillowAxis struct {
	ksize int
	first []int32
	count []int32
	k     []int32 // ksize per output
}

func pillowCoeffs(in int, in0, in1 float32, out int, f PillowFilter) pillowAxis {
	scale := float64(in1-in0) / float64(out)
	filterscale := max(scale, 1)
	support := float64(f.Support * filterscale)
	ksize := int(math.Ceil(support))*2 + 1
	a := pillowAxis{ksize: ksize, first: make([]int32, out), count: make([]int32, out), k: make([]int32, out*ksize)}
	pre := make([]float64, ksize)
	for xx := range out {
		center := float64(in0) + float64(float64(float64(xx)+0.5)*scale)
		ss := 1 / filterscale
		xmin := int(center - support + 0.5)
		if xmin < 0 {
			xmin = 0
		}
		xmax := int(center + support + 0.5)
		if xmax > in {
			xmax = in
		}
		xmax -= xmin
		ww := 0.0
		for x := range xmax {
			w := f.At(float64(float64(x+xmin)-center+0.5) * ss)
			pre[x] = w
			ww += w
		}
		k := a.k[xx*ksize : xx*ksize+ksize]
		for x := range xmax {
			v := pre[x]
			if ww != 0 {
				v /= ww
			}
			if v < 0 {
				k[x] = int32(-0.5 + float64(v*(1<<pillowBits)))
			} else {
				k[x] = int32(0.5 + float64(v*(1<<pillowBits)))
			}
		}
		a.first[xx], a.count[xx] = int32(xmin), int32(max(xmax, 0))
	}
	return a
}

// clip8 is Pillow's: the sum shifted right by 22, kept within 0 to 255.
func clip8(v int32) uint8 { return uint8(min(max(v>>pillowBits, 0), 255)) }

type pillowResize struct {
	src         pipeline.Source // NRGBA, premultiplied when pre
	env         *pipeline.Env
	sw, sh      int
	inW, inH    int           // the image the resize reads: the source, or reduced
	red         *pillowReduce // the reduction first, or nil
	box         [4]float32
	rawStrip    *image.NRGBA
	rawSy       int
	rawNext     int
	w, h        int
	f           PillowFilter
	opaque, pre bool
	gray        bool
	reserved    int64

	hz, vt pillowAxis
	needH  bool
	needV  bool
	yFirst int // the first source row the second pass reads
	nx, ny []int32
	rows   [][]uint8 // rows across, row j (from yFirst) at j % len
	have   int       // of them made
	out    image.NRGBA
	gout   image.Gray
	orow   [][]uint8
	n, y   int
	err    error
}

func (p *pillowResize) Env() *pipeline.Env      { return p.env }
func (p *pillowResize) Bounds() image.Rectangle { return image.Rect(0, 0, p.w, p.h) }
func (p *pillowResize) Opaque() bool            { return p.opaque }
func (p *pillowResize) ColorModel() color.Model {
	if p.gray {
		return color.GrayModel
	}
	return color.NRGBAModel
}

func (p *pillowResize) Close() error {
	p.env.Budget.Release(p.reserved)
	p.reserved = 0
	p.rows = nil
	if p.err == nil {
		p.err = errors.New("goimgtools: resize closed")
	}
	return p.src.Close()
}

func (p *pillowResize) setup() error {
	p.n = pipeline.StripRows(int64(p.w)*4, p.h)
	parts := max(1, min(p.env.Helpers()+1, 16))
	var need int64
	if p.f.Nearest {
		// Pillow's affine scaling: the place added to pixel after pixel.
		a0, a4 := float64(p.box[2]-p.box[0])/float64(p.w), float64(p.box[3]-p.box[1])/float64(p.h)
		p.nx, p.ny = make([]int32, p.w), make([]int32, p.h)
		// Outside the source (-1): left as 0, as Pillow fills.
		for i, xo := 0, float64(p.box[0])+float64(a0*0.5); i < p.w; i, xo = i+1, xo+a0 {
			if c := coord(xo); c < p.sw {
				p.nx[i] = int32(c)
			} else {
				p.nx[i] = -1
			}
		}
		for i, yo := 0, float64(p.box[1])+float64(a4*0.5); i < p.h; i, yo = i+1, yo+a4 {
			if c := coord(yo); c < p.sh {
				p.ny[i] = int32(c)
			} else {
				p.ny[i] = -1
			}
		}
		need = int64(p.w)*8 + int64(p.h)*4 + int64(p.n)*int64(p.w)*4 + 4*int64(p.sw)
		if err := p.env.Budget.Reserve(need, "resize rows"); err != nil {
			return err
		}
		p.reserved = need
		p.rows = [][]uint8{make([]uint8, 4*p.sw)}
	} else {
		// As Pillow: across unless the size and the box are the image's,
		// and so down.
		p.needH = p.w != p.inW || p.box[0] != 0 || p.box[2] != float32(p.w)
		p.needV = p.h != p.inH || p.box[1] != 0 || p.box[3] != float32(p.h)
		p.hz, p.vt = pillowCoeffs(p.inW, p.box[0], p.box[2], p.w, p.f), pillowCoeffs(p.inH, p.box[1], p.box[3], p.h, p.f)
		p.yFirst = int(p.vt.first[0])
		// The rows across a strip of output rows reads.
		span := int(math.Ceil(float64(p.n)*float64(p.inH)/float64(p.h))) + p.vt.ksize + 2
		span = min(span, p.inH)
		need = int64(span)*int64(p.w)*4 + int64(len(p.hz.k)+len(p.vt.k))*4 + int64(p.w+p.h)*8 + int64(p.n)*int64(p.w)*4
		if err := p.env.Budget.Reserve(need, "resize rows"); err != nil {
			return err
		}
		p.reserved = need
		p.rows = make([][]uint8, span)
		for i := range p.rows {
			p.rows[i] = make([]uint8, 4*p.w)
		}
	}
	if r := p.red; r != nil {
		need += int64(p.inW) * 4 * 5
		if err := p.env.Budget.Reserve(int64(p.inW)*4*5, "resize rows"); err != nil {
			return err
		}
		p.reserved += int64(p.inW) * 4 * 5
		r.sums, r.row = make([]uint32, 4*p.inW), make([]uint8, 4*p.inW)
	}
	if p.gray {
		p.gout = image.Gray{Pix: make([]uint8, p.n*p.w), Stride: p.w}
		p.orow = make([][]uint8, parts)
		for i := range p.orow {
			p.orow[i] = make([]uint8, 4*p.w)
		}
	} else {
		p.out = image.NRGBA{Pix: make([]uint8, p.n*4*p.w), Stride: 4 * p.w}
	}
	return nil
}

// coord is Pillow's COORD: -1 below 0, else truncated.
func coord(v float64) int {
	if v < 0 {
		return -1
	}
	return int(v)
}

// rawRow returns row y of the source (at or after the last one asked for).
func (p *pillowResize) rawRow(y int) ([]uint8, error) {
	for p.rawStrip == nil || y >= p.rawSy+p.rawStrip.Rect.Dy() {
		m, err := p.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		p.rawStrip, p.rawSy = m.(*image.NRGBA), p.rawNext
		p.rawNext += p.rawStrip.Rect.Dy()
	}
	return p.rawStrip.Pix[(y-p.rawSy)*p.rawStrip.Stride:][:4*p.sw], nil
}

// sourceRow returns row y of the image the resize reads (at or after the
// last one asked for): the source's, or Image.reduce's of it.
func (p *pillowResize) sourceRow(y int) ([]uint8, error) {
	r := p.red
	if r == nil {
		return p.rawRow(y)
	}
	y0 := r.rb[1] + y*r.fy
	y1 := min(y0+r.fy, r.rb[3])
	clear(r.sums)
	for sy := y0; sy < y1; sy++ {
		src, err := p.rawRow(sy)
		if err != nil {
			return nil, err
		}
		for x := 0; x < p.inW; x++ {
			c0, c1 := r.rb[0]+x*r.fx, min(r.rb[0]+(x+1)*r.fx, r.rb[2])
			s := r.sums[4*x : 4*x+4 : 4*x+4]
			for c := c0; c < c1; c++ {
				q := src[4*c : 4*c+4 : 4*c+4]
				s[0] += uint32(q[0])
				s[1] += uint32(q[1])
				s[2] += uint32(q[2])
				s[3] += uint32(q[3])
			}
		}
	}
	rows := y1 - y0
	for x := 0; x < p.inW; x++ {
		n := rows * (min(r.rb[0]+(x+1)*r.fx, r.rb[2]) - (r.rb[0] + x*r.fx))
		amend, mult := uint32(n/2), reduceMult(n)
		s, d := r.sums[4*x:4*x+4:4*x+4], r.row[4*x:4*x+4:4*x+4]
		for c := range d {
			d[c] = uint8(((s[c] + amend) * mult) >> 24)
		}
	}
	return r.row, nil
}

// across makes the rows across up to row j (counted from yFirst).
func (p *pillowResize) across(j int) error {
	for ; p.have <= j; p.have++ {
		src, err := p.sourceRow(p.yFirst + p.have)
		if err != nil {
			return err
		}
		dst := p.rows[p.have%len(p.rows)]
		if !p.needH {
			copy(dst, src[:len(dst)])
			continue
		}
		hz := &p.hz
		for x := range p.w {
			x0, n := int(hz.first[x]), int(hz.count[x])
			k := hz.k[x*hz.ksize : x*hz.ksize+n]
			q := src[4*x0 : 4*(x0+n)]
			s0, s1, s2, s3 := int32(1<<(pillowBits-1)), int32(1<<(pillowBits-1)), int32(1<<(pillowBits-1)), int32(1<<(pillowBits-1))
			for i, w := range k {
				c := q[4*i : 4*i+4 : 4*i+4]
				s0 += int32(c[0]) * w
				s1 += int32(c[1]) * w
				s2 += int32(c[2]) * w
				s3 += int32(c[3]) * w
			}
			d := dst[4*x : 4*x+4 : 4*x+4]
			d[0], d[1], d[2], d[3] = clip8(s0), clip8(s1), clip8(s2), clip8(s3)
		}
	}
	return nil
}

func (p *pillowResize) Next() (image.Image, error) {
	if p.err != nil {
		return nil, p.err
	}
	if p.y >= p.h {
		return nil, io.EOF
	}
	y0, y1 := p.y, min(p.y+p.n, p.h)
	n := y1 - y0
	parts := max(1, min(p.env.Helpers()+1, n))
	if p.f.Nearest {
		if err := p.nearestRows(y0, y1); err != nil {
			p.err = err
			return nil, err
		}
	} else {
		// The rows across these rows read, made in order; then the rows
		// down, shared out.
		last := int(p.vt.first[y1-1]) + int(p.vt.count[y1-1]) - 1 - p.yFirst
		if !p.needV {
			last = y1 - 1 - p.yFirst
		}
		if err := p.across(last); err != nil {
			p.err = err
			return nil, err
		}
		p.env.Parallel(parts, func(i int) {
			for y := y0 + i*n/parts; y < y0+(i+1)*n/parts; y++ {
				p.downRow(y, y0, p.outRow(y, y0, i))
			}
		})
	}
	var out image.Image
	if p.gray {
		g := p.gout
		g.Pix = g.Pix[:n*g.Stride]
		g.Rect = image.Rect(0, y0, p.w, y1)
		out = &g
	} else {
		o := p.out
		o.Pix = o.Pix[:n*o.Stride]
		o.Rect = image.Rect(0, y0, p.w, y1)
		out = &o
	}
	p.y = y1
	if p.y == p.h {
		for {
			if _, err := p.src.Next(); err != nil {
				if err == io.EOF {
					break
				}
				p.err = err
				return nil, err
			}
		}
	}
	return out, nil
}

// outRow is where output row y of the strip from y0 is made (by part i).
func (p *pillowResize) outRow(y, y0, i int) []uint8 {
	if p.gray {
		return p.orow[i]
	}
	return p.out.Pix[(y-y0)*p.out.Stride:][:4*p.w]
}

// finish divides the colours of row d (output row y) by alpha again and
// puts it out.
func (p *pillowResize) finish(y, y0 int, d []uint8) {
	switch {
	case p.opaque:
		for x := 3; x < len(d); x += 4 {
			d[x] = 255
		}
	case p.pre:
		for x := 0; x+3 < len(d); x += 4 {
			a := uint32(d[x+3])
			if a == 255 || a == 0 {
				continue
			}
			for c := 0; c < 3; c++ {
				d[x+c] = uint8(min(255*uint32(d[x+c])/a, 255))
			}
		}
	}
	if p.gray {
		g := p.gout.Pix[(y-y0)*p.gout.Stride:][:p.w]
		for x := range g {
			g[x] = d[4*x]
		}
	}
}

// downRow makes output row y (of the strip from y0) into d from the rows
// across.
func (p *pillowResize) downRow(y, y0 int, d []uint8) {
	if !p.needV {
		copy(d, p.rows[(y-p.yFirst)%len(p.rows)])
		p.finish(y, y0, d)
		return
	}
	vt := &p.vt
	r0, cnt := int(vt.first[y])-p.yFirst, int(vt.count[y])
	k := vt.k[y*vt.ksize : y*vt.ksize+cnt]
	for x := 0; x < 4*p.w; x += 4 {
		s0, s1, s2, s3 := int32(1<<(pillowBits-1)), int32(1<<(pillowBits-1)), int32(1<<(pillowBits-1)), int32(1<<(pillowBits-1))
		for i, w := range k {
			c := p.rows[(r0+i)%len(p.rows)][x : x+4 : x+4]
			s0 += int32(c[0]) * w
			s1 += int32(c[1]) * w
			s2 += int32(c[2]) * w
			s3 += int32(c[3]) * w
		}
		q := d[x : x+4 : x+4]
		q[0], q[1], q[2], q[3] = clip8(s0), clip8(s1), clip8(s2), clip8(s3)
	}
	p.finish(y, y0, d)
}

// nearestRows makes output rows y0 to y1 by Pillow's nearest scaling.
func (p *pillowResize) nearestRows(y0, y1 int) error {
	for y := y0; y < y1; y++ {
		d := p.outRow(y, y0, 0)
		if p.ny[y] < 0 {
			clear(d)
			p.finish(y, y0, d)
			continue
		}
		src, err := p.sourceRow(int(p.ny[y]))
		if err != nil {
			return err
		}
		for x, sx := range p.nx {
			if sx < 0 {
				clear(d[4*x : 4*x+4])
				continue
			}
			copy(d[4*x:4*x+4], src[4*sx:4*sx+4])
		}
		p.finish(y, y0, d)
	}
	return nil
}
