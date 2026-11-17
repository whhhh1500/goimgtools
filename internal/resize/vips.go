package resize

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Vips is sharp's resize, which is libvips' resize (vips_resize, 8.17 and 8.18) on
// the image with its colours multiplied by alpha in 8 bits, worked out here
// from what they do, to the byte:
//
//   - sharp asks libvips for scales 1/hshrink and 1/vshrink, libvips
//     reduces by 1/(1/hshrink) and 1/(1/vshrink).
//   - KernelNearest first takes every k-th pixel (vips_subsample) for the
//     whole part of the reduction beyond a factor of 2.
//   - Down the columns, then across the rows, each direction that shrinks
//     is reduced on its own (vips_reducev, vips_reduceh), each rounded to 8
//     bits: the size is the input divided by the factor, rounded; the whole
//     part of the factor beyond 2 is a plain average of boxes of pixels
//     (vips_shrinkv, vips_shrinkh: the last box filled up with copies of
//     the last pixel, the sum times 2^32/(256·box) shifted right by 24);
//     what is left is a convolution with the kernel: a window of points
//     (2·round(factor·reach)+1, the reach 1 for linear, 2 for cubic,
//     Mitchell and Lanczos2, 3 for Lanczos3 and MKS2013, 5 for MKS2021)
//     placed at 1/64 of a pixel from a table of 65 masks, normalised and
//     times 4096 dropped to whole numbers, the sum plus 2048 shifted right
//     by 12, kept within 0 to 255; past the edges the edge pixels repeat.
//     Output pixel i is centred at (i+0.5)·factor - 0.5 - offset, the
//     offset ((1+extra)/2 - 1, extra the input the rounded size invents)
//     spreading what is cut off over both ends. libvips works out that
//     place afresh at the start of each row and, down the columns, of each
//     run of 10 rows (sharp reads in order, and libvips caches the reduced
//     rows in tiles of 10 then), adding the factor for each pixel after;
//     the same is done here.
//   - A direction that grows is interpolated as vips_affine with the
//     matrix of the scales and an input offset of half a pixel (none for
//     KernelNearest), the edge pixels repeating: nearest, bilinear for
//     KernelLinear and bicubic for the others (see ops.Affine), in fixed
//     point on all four channels; KernelNearest by whole factors in both
//     directions repeats pixels (vips_zoom).
//   - With transparency each colour is multiplied by alpha/255 (in single
//     precision, the fraction dropped) before, and after divided again
//     (by alpha, 255/alpha in single precision, kept within 255, the
//     fraction dropped; 0 where alpha is 0), as sharp does.
//
// The result is NRGBA, or Gray for a Gray source (whose three channels,
// alike, are worked out alike).
func Vips(src pipeline.Source, hshrink, vshrink float64, k VipsKernel) (pipeline.Source, error) {
	b := src.Bounds()
	p, err := planVips(b.Dx(), b.Dy(), hshrink, vshrink, k)
	if err != nil {
		return nil, err
	}
	if p.none() {
		return src, nil
	}
	opaque := pipeline.IsOpaque(src)
	var nal [256]float32
	for a := range nal {
		nal[a] = float32(float64(a) / 255)
	}
	in := pipeline.Map(src, func(s *image.NRGBA) error {
		if opaque {
			return nil
		}
		for y := 0; y < s.Rect.Dy(); y++ {
			row := s.Pix[y*s.Stride:][:4*s.Rect.Dx()]
			for x := 0; x+3 < len(row); x += 4 {
				if a := row[x+3]; a != 255 {
					f := nal[a]
					row[x] = uint8(float32(row[x]) * f)
					row[x+1] = uint8(float32(row[x+1]) * f)
					row[x+2] = uint8(float32(row[x+2]) * f)
				}
			}
		}
		return nil
	})
	env := pipeline.EnvOf(src)
	s := &vipsResize{src: in, env: env, p: p, opaque: opaque, gray: src.ColorModel() == color.GrayModel}
	if err := s.setup(); err != nil {
		in.Close()
		return nil, err
	}
	return s, nil
}

// VipsSize is the size Vips makes of a w × h image.
func VipsSize(w, h int, hshrink, vshrink float64, k VipsKernel) (int, int, error) {
	p, err := planVips(w, h, hshrink, vshrink, k)
	if err != nil {
		return 0, 0, err
	}
	return p.w, p.h, nil
}

// Kernels of Vips, as libvips' VipsKernel; VipsCustom is a filter of one's
// own.
const (
	VipsNearest = iota
	VipsLinear
	VipsCubic
	VipsMitchell
	VipsLanczos2
	VipsLanczos3
	VipsMKS2013
	VipsMKS2021
	VipsCustom
)

// VipsKernel is the kernel of Vips: Kind, and for VipsCustom the filter At,
// reaching Support either side (its window is that of the whole number of
// pixels at or above Support, as MKS2013's 2.5 is reckoned as 3); At is
// called with distances from 0 up to Support, as golang.org/x/image/draw
// calls a kernel.
type VipsKernel struct {
	Kind    int
	At      func(float64) float64
	Support float64
}

const (
	vipsGap      = 2.0  // libvips' default gap: box shrinks leave at least 2
	vipsMaxPoint = 2000 // libvips' largest window
	vipsRun      = 10   // rows of the cache after vips_reducev
)

func sinc(x float64) float64 {
	if x == 0 {
		return 1
	}
	x = x * math.Pi
	return math.Sin(x) / x
}

// cubicFilter is the Mitchell-Netravali cubic of b and c, as libvips works
// it out (the products kept apart so that no platform fuses them).
func cubicFilter(x, b, c float64) float64 {
	ax := math.Abs(x)
	ax2 := ax * ax
	ax3 := ax2 * ax
	if ax <= 1 {
		k3 := 12 - float64(9*b) - float64(6*c)
		k2 := -18 + float64(12*b) + float64(6*c)
		k0 := 6 - float64(2*b)
		return (float64(k3*ax3) + float64(k2*ax2) + k0) / 6
	}
	if ax <= 2 {
		k3 := -b - float64(6*c)
		k2 := float64(6*b) + float64(30*c)
		k1 := -float64(12*b) - float64(48*c)
		k0 := float64(8*b) + float64(24*c)
		return (float64(k3*ax3) + float64(k2*ax2) + float64(k1*ax) + k0) / 6
	}
	return 0
}

// at is the kernel at x, as libvips' filters.
func (k VipsKernel) at(x float64) float64 {
	switch k.Kind {
	case VipsLinear:
		x = math.Abs(x)
		if x < 1 {
			return 1 - x
		}
		return 0
	case VipsCubic:
		return cubicFilter(x, 0, 0.5)
	case VipsMitchell:
		return cubicFilter(x, 1.0/3.0, 1.0/3.0)
	case VipsLanczos2:
		if x >= -2 && x <= 2 {
			return sinc(x) * sinc(x/2)
		}
		return 0
	case VipsLanczos3:
		if x >= -3 && x <= 3 {
			return sinc(x) * sinc(x/3)
		}
		return 0
	case VipsMKS2013:
		x = math.Abs(x)
		switch {
		case x >= 2.5:
			return 0
		case x >= 1.5:
			return float64(float64((x-5.0/2.0)*(x-5.0/2.0)) / -8.0)
		case x >= 0.5:
			return float64((float64(float64(4.0*x)*x) - float64(11.0*x) + 7.0) / 4.0)
		}
		return 17.0/16.0 - float64(float64(float64(7.0*x)*x)/4.0)
	case VipsMKS2021:
		x = math.Abs(x)
		sq := func(a, b, c, d float64) float64 { return (float64(float64(a*x)*x) - float64(b*x) + c) / d }
		switch {
		case x >= 4.5:
			return 0
		case x >= 3.5:
			return sq(4, 36, 81, -1152)
		case x >= 2.5:
			return sq(4, 27, 45, 144)
		case x >= 1.5:
			return sq(24, 113, 130, -144)
		case x >= 0.5:
			return sq(140, 379, 239, 144)
		}
		return 577.0/576.0 - float64(float64(float64(239.0*x)*x)/144.0)
	case VipsCustom:
		return Within(k.At, k.Support)(x)
	}
	return 0
}

// Within is the filter at of support for any distance: at(|x|) below
// support, 0 beyond.
func Within(at func(float64) float64, support float64) func(float64) float64 {
	return func(x float64) float64 {
		x = math.Abs(x)
		if x >= support {
			return 0
		}
		return at(x)
	}
}

// points is the size of the window for a reduction by shrink.
func (k VipsKernel) points(shrink float64) (int, error) {
	reach := 0.0
	switch k.Kind {
	case VipsNearest:
		return 1, nil
	case VipsLinear:
		reach = 1
	case VipsCubic, VipsMitchell, VipsLanczos2:
		reach = 2
	case VipsLanczos3, VipsMKS2013:
		reach = 3
	case VipsMKS2021:
		reach = 5
	case VipsCustom:
		reach = math.Ceil(k.Support)
	default:
		return 0, errors.New("goimgtools: unknown resize kernel")
	}
	n := 2*math.RoundToEven(reach*shrink) + 1
	if !(n <= vipsMaxPoint) {
		return 0, errors.New("goimgtools: resize reduction too large for the kernel")
	}
	return int(n), nil
}

// masks are libvips' 65 masks of n points for a reduction by shrink, at
// 0/64 to 64/64 of a pixel, times 4096 dropped to whole numbers.
func (k VipsKernel) masks(n int, shrink float64) ([]int32, error) {
	m := make([]int32, 65*n)
	c := make([]float64, n)
	for y := 0; y <= 64; y++ {
		if k.Kind == VipsNearest {
			m[y*n] = 4096
			continue
		}
		x := float64(float32(y) / 64)
		half := x + float64(float64(n)/2.0) - 1
		sum := 0.0
		for i := range c {
			l := k.at((float64(i) - half) / shrink)
			c[i] = l
			sum += l
		}
		for i := range c {
			v := float64(c[i]/sum) * 4096
			if !(math.Abs(v) < 32768) {
				return nil, errors.New("goimgtools: the resize filter has no usable weights")
			}
			m[y*n+i] = int32(int16(v))
		}
	}
	return m, nil
}

// roundUint is libvips' VIPS_ROUND_UINT.
func roundUint(v float64) int {
	return int(v + 0.5)
}

// vipsAxis is a reduction in one direction (vips_reducev, vips_reduceh).
type vipsAxis struct {
	in, out  int
	box      int // the box shrink before, 1 for none
	mid      int // the size after it
	reduce   bool
	residual float64
	off      float64
	n, pad   int
	masks    []int32
	// pairs are the masks for the AVX2 reduce: the weights two to a
	// uint32 (16 bits each, the second in the high half; an odd last one
	// with 0), np to a mask; nil when a weight does not fit 16 bits.
	pairs []uint32
	np    int
}

// packPairs sets a.pairs from a.masks.
func (a *vipsAxis) packPairs() {
	a.np = (a.n + 1) / 2
	pairs := make([]uint32, 0, len(a.masks)/a.n*a.np)
	for m := 0; m+a.n <= len(a.masks); m += a.n {
		k := a.masks[m : m+a.n]
		for j := 0; j < a.n; j += 2 {
			w0, w1 := k[j], int32(0)
			if j+1 < a.n {
				w1 = k[j+1]
			}
			if w0 != int32(int16(w0)) || w1 != int32(int16(w1)) {
				return
			}
			pairs = append(pairs, uint32(uint16(w0))|uint32(uint16(w1))<<16)
		}
	}
	a.pairs = pairs
}

func planAxis(in int, shrink float64, k VipsKernel) (*vipsAxis, error) {
	out := roundUint(float64(in) / shrink)
	if out < 1 || out > 1<<26 {
		return nil, errors.New("goimgtools: resize to no pixels, or too many")
	}
	a := &vipsAxis{in: in, out: out, box: 1, mid: in, residual: shrink}
	extra := float64(float64(out)*shrink) - float64(in)
	if k.Kind != VipsNearest {
		if b := math.Floor(float64(in) / float64(out) / vipsGap); b > 1 {
			a.box = int(b)
			a.mid = int(math.Ceil(float64(in) / b))
			extra /= b
			a.residual /= b
		}
	}
	if a.residual == 1 {
		a.out = a.mid
		return a, nil
	}
	var err error
	if a.n, err = k.points(a.residual); err != nil {
		return nil, err
	}
	a.reduce = true
	a.pad = (a.n+1)/2 - 1
	a.off = float64((1+extra)/2.0) - 1
	if a.masks, err = k.masks(a.n, a.residual); err != nil {
		return nil, err
	}
	if useAVX2 {
		a.packPairs()
	}
	return a, nil
}

// place is the window start (in the axis with its pad) and the mask of
// output o, the place worked out afresh at the start of each run of run
// outputs and added to after.
func (a *vipsAxis) place(o, run int) (p, t int) {
	s := o - o%run
	v := float64(float64(float64(s)+0.5)*a.residual) - 0.5 - a.off
	for i := s; i < o; i++ {
		v += a.residual
	}
	sv := int(float64(v*64) * 2)
	return int(v), (sv&127 + 1) >> 1
}

// Enlargements of vipsPlan.
const (
	upNone = iota
	upZoom
	upAffine
)

// Interpolations of an enlargement.
const (
	interpNearest = iota
	interpBilinear
	interpBicubic
)

type vipsPlan struct {
	sw, sh     int
	subX, subY int       // vips_subsample, 1 for none
	w0, h0     int       // after it
	down       *vipsAxis // the reduction down the columns
	across     *vipsAxis // and across the rows
	w1, h1     int       // after the reductions
	up         int
	zx, zy     int     // vips_zoom
	ia, id     float64 // the inverse of the affine scales
	interp     int
	idx        float64 // the input offset less the pixel of border
	w, h       int
}

func (p *vipsPlan) none() bool {
	return p.subX == 1 && p.subY == 1 && p.down == nil && p.across == nil && p.up == upNone
}

func planVips(sw, sh int, hshrink, vshrink float64, k VipsKernel) (vipsPlan, error) {
	p := vipsPlan{sw: sw, sh: sh, subX: 1, subY: 1, w0: sw, h0: sh}
	if sw < 1 || sh < 1 || !(hshrink > 0) || !(vshrink > 0) || math.IsInf(hshrink, 0) || math.IsInf(vshrink, 0) {
		return p, errors.New("goimgtools: resize of no pixels")
	}
	if k.Kind < VipsNearest || k.Kind > VipsCustom || k.Kind == VipsCustom && (k.At == nil || !(k.Support > 0)) {
		return p, errors.New("goimgtools: unknown resize kernel")
	}
	hscale, vscale := 1.0/hshrink, 1.0/vshrink
	if k.Kind == VipsNearest {
		tw, th := roundUint(float64(float64(sw)*hscale)), roundUint(float64(float64(sh)*vscale))
		if tw >= 1 && th >= 1 {
			p.subX = max(1, int(math.Floor(float64(sw)/float64(tw)/vipsGap)))
			p.subY = max(1, int(math.Floor(float64(sh)/float64(th)/vipsGap)))
		}
		if p.subX > 1 || p.subY > 1 {
			p.w0, p.h0 = sw/p.subX, sh/p.subY
			hscale *= float64(p.subX)
			vscale *= float64(p.subY)
		}
	}
	hscale = max(hscale, 1.0/float64(p.w0))
	vscale = max(vscale, 1.0/float64(p.h0))
	p.w1, p.h1 = p.w0, p.h0
	var err error
	if vscale < 1 {
		if p.down, err = planAxis(p.h0, 1.0/vscale, k); err != nil {
			return p, err
		}
		p.h1 = p.down.out
	}
	if hscale < 1 {
		if p.across, err = planAxis(p.w0, 1.0/hscale, k); err != nil {
			return p, err
		}
		p.w1 = p.across.out
	}
	p.w, p.h = p.w1, p.h1
	if hscale > 1 || vscale > 1 {
		p.interp = interpBicubic
		id := 0.5
		switch k.Kind {
		case VipsNearest:
			p.interp, id = interpNearest, 0
		case VipsLinear:
			p.interp = interpBilinear
		}
		if k.Kind == VipsNearest && hscale == math.Floor(hscale) && vscale == math.Floor(vscale) {
			p.up, p.zx, p.zy = upZoom, int(hscale), int(vscale)
			p.w, p.h = p.w1*p.zx, p.h1*p.zy
		} else {
			a, d := 1.0, 1.0
			if hscale > 1 {
				a = hscale
			}
			if vscale > 1 {
				d = vscale
			}
			// As ops.Affine: the inverse of the matrix, the box around the
			// corners rounded.
			tmp := 1 / float64(a*d)
			p.up, p.ia, p.id, p.idx = upAffine, tmp*d, tmp*a, id-1
			p.w, p.h = roundUint(float64(a*float64(p.w1))), roundUint(float64(d*float64(p.h1)))
		}
		if p.w < 1 || p.h < 1 || p.w > 1<<26 || p.h > 1<<26 {
			return p, errors.New("goimgtools: resize to no pixels, or too many")
		}
	}
	return p, nil
}

// vipsResize is the stage of Vips. Rows go through three steps: the source
// (subsampled, or averaged in boxes down the columns: the "shrunk" rows,
// held in a ring), the reductions (the "reduced" rows, in a ring too), and
// the enlargement into the strip handed on.
type vipsResize struct {
	src      pipeline.Source // NRGBA, premultiplied
	env      *pipeline.Env
	p        vipsPlan
	opaque   bool
	gray     bool // Gray strips out
	reserved int64

	strip *image.NRGBA // the source strip being read
	sy    int          // its first row
	next  int          // the source row read next

	shrunk [][]uint8 // shrunk row j at j % len
	made   int       // shrunk rows made
	box    []uint32

	reduced [][]uint8 // reduced row j at j % len
	rlo     int       // reduced rows rlo to rhi-1 are held
	rhi     int

	parts []vipsScratch

	hcol, hmask []int32 // reduceh: each output's first column (in the padded row) and mask
	// hpair is each output's first pair of weights in across.pairs, and
	// the outputs from hxa to hxb-1 read no column outside the row: the
	// AVX2 reduce makes them.
	hpair    []int32
	hxa, hxb int
	xi, xw   []int32 // the enlargement: each output's column and weight (or mask)
	xok      []bool  // and whether it is within the image

	out  image.NRGBA
	gout image.Gray // for gray
	n    int        // rows of a strip
	y    int
	err  error
}

type vipsScratch struct {
	acc  []int32
	rows []*uint8 // the rows of a reduce down, for the AVX2 one
	row  []uint8  // a row reduced down the columns
	box  []uint8  // and in boxes across
	out  []uint8  // an output row, for gray
}

func (s *vipsResize) Env() *pipeline.Env      { return s.env }
func (s *vipsResize) Bounds() image.Rectangle { return image.Rect(0, 0, s.p.w, s.p.h) }
func (s *vipsResize) ColorModel() color.Model {
	if s.gray {
		return color.GrayModel
	}
	return color.NRGBAModel
}
func (s *vipsResize) Opaque() bool { return s.opaque }

func (s *vipsResize) Close() error {
	s.env.Budget.Release(s.reserved)
	s.reserved = 0
	s.shrunk, s.reduced, s.parts = nil, nil, nil
	if s.err == nil {
		s.err = errors.New("goimgtools: resize closed")
	}
	return s.src.Close()
}

// winRows is the number of reduced rows the enlargement reads for a row.
func (p *vipsPlan) winRows() int {
	switch {
	case p.up == upAffine && p.interp == interpBicubic:
		return 4
	case p.up == upAffine && p.interp == interpBilinear:
		return 2
	}
	return 1
}

// wo is the window offset of the enlargement: the pixels before the point.
func (p *vipsPlan) wo() int {
	if p.interp == interpBicubic {
		return 1
	}
	return 0
}

func (s *vipsResize) setup() error {
	p := &s.p
	s.n = pipeline.StripRows(int64(p.w)*4, p.h)
	parts := max(1, min(s.env.Helpers()+1, 16))
	// The reduced rows a strip needs: for an enlargement, those its rows
	// reach, at most a row more than the strip covers and the window.
	rcap := s.n + p.winRows() + 2
	if p.up == upAffine {
		rcap = int(math.Ceil(float64(s.n)*p.id)) + p.winRows() + 3
	} else if p.up == upZoom {
		rcap = s.n/p.zy + 2
	}
	rcap = min(rcap, p.h1)
	// The shrunk rows the reduced rows of a batch (parts rows) need.
	scap := parts + 1
	if v := p.down; v != nil && v.reduce {
		scap = int(math.Ceil(float64(parts)*v.residual)) + v.n + 3
	}
	scap = min(scap, max(1, s.midH()))
	need := int64(scap)*int64(p.w0)*4 + int64(rcap)*int64(p.w1)*4 + int64(s.n)*int64(p.w)*4
	if v := p.down; v != nil && v.box > 1 {
		need += int64(p.w0) * 4 * 4
	}
	perPart := int64(p.w0) * 4 * 5
	if h := p.across; h != nil {
		perPart += int64(h.mid) * 4
		need += int64(p.w1) * 8
	}
	need += int64(parts) * perPart
	if p.up == upAffine {
		need += int64(p.w) * 9
	}
	if s.gray {
		need += int64(parts) * int64(p.w) * 4
	}
	if err := s.env.Budget.Reserve(need, "resize rows"); err != nil {
		return err
	}
	s.reserved = need
	s.shrunk = make([][]uint8, scap)
	for i := range s.shrunk {
		s.shrunk[i] = make([]uint8, 4*p.w0)
	}
	if v := p.down; v != nil && v.box > 1 {
		s.box = make([]uint32, 4*p.w0)
	}
	s.reduced = make([][]uint8, rcap)
	for i := range s.reduced {
		s.reduced[i] = make([]uint8, 4*p.w1)
	}
	s.parts = make([]vipsScratch, parts)
	for i := range s.parts {
		sc := &s.parts[i]
		sc.acc = make([]int32, 4*p.w0)
		sc.row = make([]uint8, 4*p.w0)
		if v := p.down; v != nil && v.pairs != nil {
			sc.rows = make([]*uint8, 2*v.np)
		}
		if h := p.across; h != nil && h.box > 1 {
			sc.box = make([]uint8, 4*h.mid)
		}
	}
	if h := p.across; h != nil && h.reduce {
		s.hcol, s.hmask = make([]int32, p.w1), make([]int32, p.w1)
		v := float64(float64(0.5)*h.residual) - 0.5 - h.off
		for x := range p.w1 {
			sv := int(float64(v*64) * 2)
			s.hcol[x] = int32(int(v) - h.pad)
			s.hmask[x] = int32((sv&127+1)>>1) * int32(h.n)
			v += h.residual
		}
		if h.pairs != nil {
			s.hpair = make([]int32, p.w1)
			for x := range p.w1 {
				s.hpair[x] = s.hmask[x] / int32(h.n) * int32(h.np)
			}
			for s.hxa < p.w1 && s.hcol[s.hxa] < 0 {
				s.hxa++
			}
			s.hxb = s.hxa
			for s.hxb < p.w1 && int(s.hcol[s.hxb])+2*h.np <= h.mid {
				s.hxb++
			}
		}
	}
	if p.up == upAffine {
		s.xi, s.xw, s.xok = make([]int32, p.w), make([]int32, p.w), make([]bool, p.w)
		wo := p.wo()
		ix := 0 - p.idx + float64(wo)
		for x := range p.w {
			fx := math.Floor(ix) - float64(wo)
			s.xok[x] = fx >= 0 && fx <= float64(p.w1)
			xi := int(ix)
			s.xi[x] = int32(xi)
			switch p.interp {
			case interpBilinear:
				s.xw[x] = int32(float64(ix-float64(xi)) * 4096)
			case interpBicubic:
				s.xw[x] = int32((int(float64(ix*64)*2)&127 + 1) >> 1)
			}
			ix += p.ia
		}
	}
	if s.gray {
		s.gout = image.Gray{Pix: make([]uint8, s.n*p.w), Stride: p.w}
		for i := range s.parts {
			s.parts[i].out = make([]uint8, 4*p.w)
		}
	} else {
		s.out = image.NRGBA{Pix: make([]uint8, s.n*4*p.w), Stride: 4 * p.w}
	}
	return nil
}

// midH is the height of the shrunk image.
func (s *vipsResize) midH() int {
	if v := s.p.down; v != nil {
		return v.mid
	}
	return s.p.h0
}

// sourceRow returns source row y (at or after the last one asked for).
func (s *vipsResize) sourceRow(y int) ([]uint8, error) {
	for s.strip == nil || y >= s.sy+s.strip.Rect.Dy() {
		m, err := s.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		s.strip, s.sy = m.(*image.NRGBA), s.next
		s.next += s.strip.Rect.Dy()
	}
	return s.strip.Pix[(y-s.sy)*s.strip.Stride:][:4*s.p.sw], nil
}

// makeShrunk makes shrunk rows up to row j.
func (s *vipsResize) makeShrunk(j int) error {
	p := &s.p
	for ; s.made <= j; s.made++ {
		dst := s.shrunk[s.made%len(s.shrunk)]
		v := p.down
		if v == nil || v.box == 1 {
			row, err := s.sourceRow(s.made * p.subY)
			if err != nil {
				return err
			}
			if p.subX == 1 {
				copy(dst, row)
				continue
			}
			for x := range p.w0 {
				copy(dst[4*x:4*x+4], row[4*x*p.subX:])
			}
			continue
		}
		clear(s.box)
		var row []uint8
		for i := 0; i < v.box; i++ {
			if y := s.made*v.box + i; y < p.h0 {
				var err error
				if row, err = s.sourceRow(y); err != nil {
					return err
				}
			}
			// Past the last row, the last row again.
			box := s.box[:len(row)]
			for x, c := range row {
				box[x] += uint32(c)
			}
		}
		amend, mult := uint32(v.box/2), uint32(uint64(1<<32)/uint64(256*v.box))
		box := s.box[:len(dst)]
		for x, c := range box {
			dst[x] = uint8(((c + amend) * mult) >> 24)
		}
	}
	return nil
}

// reduceRow makes reduced row j into dst with the scratch sc; the shrunk
// rows it needs are in the ring.
func (s *vipsResize) reduceRow(j int, dst []uint8, sc *vipsScratch) {
	p := &s.p
	row := s.shrunk[j%len(s.shrunk)]
	if v := p.down; v != nil && v.reduce {
		py, t := v.place(j, vipsRun)
		k := v.masks[t*v.n : t*v.n+v.n]
		acc := sc.acc
		at := func(i int) []uint8 {
			r := min(max(py-v.pad+i, 0), v.mid-1)
			return s.shrunk[r%len(s.shrunk)][:len(acc)]
		}
		from := 0
		if v.pairs != nil && len(acc) >= 16 {
			rows := sc.rows
			for i := range k {
				rows[i] = &at(i)[0]
			}
			if len(k)%2 == 1 {
				rows[len(k)] = rows[len(k)-1] // with a weight of 0
			}
			from = len(acc) / 16 * 16
			reduceVAVX2(&sc.row[0], &rows[0], &v.pairs[t*v.np], v.np, from/16)
		}
		// Four taps a pass over the row (the sums are whole numbers: in
		// any order the same).
		for x := range acc[from:] {
			acc[from+x] = 2048
		}
		i := 0
		for ; i+3 < len(k); i += 4 {
			w0, w1, w2, w3 := k[i], k[i+1], k[i+2], k[i+3]
			r0, r1, r2, r3 := at(i), at(i+1), at(i+2), at(i+3)
			r0, r1, r2, r3 = r0[:len(acc)], r1[:len(acc)], r2[:len(acc)], r3[:len(acc)]
			for x := from; x < len(acc); x++ {
				acc[x] += w0*int32(r0[x]) + w1*int32(r1[x]) + w2*int32(r2[x]) + w3*int32(r3[x])
			}
		}
		for ; i < len(k); i++ {
			w, r0 := k[i], at(i)
			r0 = r0[:len(acc)]
			for x := from; x < len(acc); x++ {
				acc[x] += w * int32(r0[x])
			}
		}
		row = sc.row
		for x := from; x < len(acc); x++ {
			row[x] = uint8(min(max(acc[x]>>12, 0), 255))
		}
	}
	h := p.across
	if h == nil {
		copy(dst, row)
		return
	}
	if h.box > 1 {
		amend, mult := uint32(h.box/2), uint32(uint64(1<<32)/uint64(256*h.box))
		w0 := p.w0
		for x := range h.mid {
			var sum [4]uint32
			for i := 0; i < h.box; i++ {
				c := min(x*h.box+i, w0-1)
				q := row[4*c : 4*c+4 : 4*c+4]
				sum[0] += uint32(q[0])
				sum[1] += uint32(q[1])
				sum[2] += uint32(q[2])
				sum[3] += uint32(q[3])
			}
			d := sc.box[4*x : 4*x+4 : 4*x+4]
			for c := range d {
				d[c] = uint8(((sum[c] + amend) * mult) >> 24)
			}
		}
		row = sc.box
	}
	if !h.reduce {
		copy(dst, row[:len(dst)])
		return
	}
	last := h.mid - 1
	xa, xb := 0, 0 // the outputs the AVX2 reduce made
	if h.pairs != nil && s.hxb > s.hxa {
		_ = row[4*h.mid-1]
		_ = dst[4*s.hxb-1]
		reduceHAVX2(&dst[4*s.hxa], &row[0], &s.hcol[s.hxa], &s.hpair[s.hxa], &h.pairs[0], h.np, s.hxb-s.hxa)
		xa, xb = s.hxa, s.hxb
	}
	for x := 0; x < p.w1; x++ {
		if x == xa && xb > xa {
			x = xb - 1
			continue
		}
		c0 := int(s.hcol[x])
		k := h.masks[s.hmask[x]:][:h.n]
		a0, a1, a2, a3 := int32(2048), int32(2048), int32(2048), int32(2048)
		if c0 >= 0 && c0+h.n-1 <= last {
			q := row[4*c0 : 4*(c0+h.n)]
			for i, w := range k {
				q := q[4*i : 4*i+4 : 4*i+4]
				a0 += w * int32(q[0])
				a1 += w * int32(q[1])
				a2 += w * int32(q[2])
				a3 += w * int32(q[3])
			}
		} else {
			for i, w := range k {
				c := min(max(c0+i, 0), last)
				q := row[4*c : 4*c+4 : 4*c+4]
				a0 += w * int32(q[0])
				a1 += w * int32(q[1])
				a2 += w * int32(q[2])
				a3 += w * int32(q[3])
			}
		}
		d := dst[4*x : 4*x+4 : 4*x+4]
		d[0] = uint8(min(max(a0>>12, 0), 255))
		d[1] = uint8(min(max(a1>>12, 0), 255))
		d[2] = uint8(min(max(a2>>12, 0), 255))
		d[3] = uint8(min(max(a3>>12, 0), 255))
	}
}

// shrunkNeed is the last shrunk row reduced row j reads.
func (s *vipsResize) shrunkNeed(j int) (lo, hi int) {
	v := s.p.down
	if v == nil || !v.reduce {
		return j, j
	}
	py, _ := v.place(j, vipsRun)
	return max(py-v.pad, 0), min(py-v.pad+v.n-1, v.mid-1)
}

// ensure makes the reduced rows up to hi held, dropping those before lo.
func (s *vipsResize) ensure(lo, hi int) error {
	if s.rhi < lo {
		s.rhi = lo
	}
	s.rlo = max(s.rlo, lo)
	for s.rhi <= hi {
		// A batch of rows, as many as there are parts, whose shrunk rows
		// fit in the ring together.
		n := min(len(s.parts), hi+1-s.rhi)
		first, _ := s.shrunkNeed(s.rhi)
		for n > 1 {
			_, last := s.shrunkNeed(s.rhi + n - 1)
			if last-first < len(s.shrunk) {
				break
			}
			n--
		}
		_, last := s.shrunkNeed(s.rhi + n - 1)
		if err := s.makeShrunk(last); err != nil {
			return err
		}
		j0 := s.rhi
		s.env.Parallel(n, func(i int) {
			s.reduceRow(j0+i, s.reduced[(j0+i)%len(s.reduced)], &s.parts[i])
		})
		s.rhi += n
	}
	return nil
}

func (s *vipsResize) Next() (image.Image, error) {
	if s.err != nil {
		return nil, s.err
	}
	p := &s.p
	if s.y >= p.h {
		return nil, io.EOF
	}
	y0, y1 := s.y, min(s.y+s.n, p.h)
	// The reduced rows these rows read.
	lo, hi := y0, y1-1
	switch p.up {
	case upZoom:
		lo, hi = y0/p.zy, (y1-1)/p.zy
	case upAffine:
		a, _ := s.affineRows(y0)
		_, b := s.affineRows(y1 - 1)
		lo, hi = a, b
	}
	if err := s.ensure(lo, hi); err != nil {
		s.err = err
		return nil, err
	}
	n := y1 - y0
	parts := max(1, min(s.env.Helpers()+1, n))
	s.env.Parallel(parts, func(i int) {
		for y := y0 + i*n/parts; y < y0+(i+1)*n/parts; y++ {
			if !s.gray {
				s.outRow(y, s.out.Pix[(y-y0)*s.out.Stride:][:4*p.w])
				continue
			}
			row := s.parts[i].out
			s.outRow(y, row)
			g := s.gout.Pix[(y-y0)*s.gout.Stride:][:p.w]
			for x := range g {
				g[x] = row[4*x]
			}
		}
	})
	var out image.Image
	if s.gray {
		g := s.gout
		g.Pix = g.Pix[:n*g.Stride]
		g.Rect = image.Rect(0, y0, p.w, y1)
		out = &g
	} else {
		o := s.out
		o.Pix = o.Pix[:n*o.Stride]
		o.Rect = image.Rect(0, y0, p.w, y1)
		out = &o
	}
	s.y = y1
	if s.y == p.h {
		for {
			if _, err := s.src.Next(); err != nil {
				if err == io.EOF {
					break
				}
				s.err = err
				return nil, err
			}
		}
	}
	return out, nil
}

// affineRows is the range of reduced rows output row y of the enlargement
// reads, and where that row is.
func (s *vipsResize) affineRows(y int) (lo, hi int) {
	p := &s.p
	wo := p.wo()
	iy := float64(p.id*float64(y)) - p.idx + float64(wo)
	yi := int(iy)
	return min(max(yi-wo-1, 0), p.h1-1), min(max(yi-wo-1+p.winRows()-1, 0), p.h1-1)
}

// rowAt is reduced row j, within the image.
func (s *vipsResize) rowAt(j int) []uint8 {
	j = min(max(j, 0), s.p.h1-1)
	return s.reduced[j%len(s.reduced)]
}

// outRow makes output row y into d: enlarged, divided by alpha again.
func (s *vipsResize) outRow(y int, d []uint8) {
	p := &s.p
	switch p.up {
	case upNone:
		copy(d, s.rowAt(y))
	case upZoom:
		r := s.rowAt(y / p.zy)
		for x := range p.w {
			sx := x / p.zx
			copy(d[4*x:4*x+4], r[4*sx:4*sx+4])
		}
	case upAffine:
		s.affineRow(y, d)
	}
	if s.opaque {
		for x := 3; x < len(d); x += 4 {
			d[x] = 255
		}
		return
	}
	for x := 0; x+3 < len(d); x += 4 {
		a := d[x+3]
		if a == 255 {
			continue
		}
		if a == 0 {
			d[x], d[x+1], d[x+2] = 0, 0, 0
			continue
		}
		f := float32(255 / float64(a))
		d[x] = uint8(min(float32(d[x])*f, 255))
		d[x+1] = uint8(min(float32(d[x+1])*f, 255))
		d[x+2] = uint8(min(float32(d[x+2])*f, 255))
	}
}

// affineRow is row y of vips_affine on the reduced rows, in fixed point
// (see ops.Affine), the edge pixels repeating.
func (s *vipsResize) affineRow(y int, d []uint8) {
	p := &s.p
	wo := p.wo()
	iy := float64(p.id*float64(y)) - p.idx + float64(wo)
	fy := math.Floor(iy) - float64(wo)
	if !(fy >= 0 && fy <= float64(p.h1)) {
		clear(d)
		return
	}
	yi := int(iy)
	last := p.w1 - 1
	col := func(x int) int { return 4 * min(max(x-wo-1, 0), last) }
	switch p.interp {
	case interpNearest:
		r := s.rowAt(yi - wo - 1)
		for x := range p.w {
			if !s.xok[x] {
				clear(d[4*x : 4*x+4])
				continue
			}
			c := col(int(s.xi[x]))
			copy(d[4*x:4*x+4], r[c:c+4])
		}
	case interpBilinear:
		r0, r1 := s.rowAt(yi-wo-1), s.rowAt(yi-wo)
		Y := int32(float64(iy-float64(yi)) * 4096)
		Yd := 4096 - Y
		for x := range p.w {
			q := d[4*x : 4*x+4 : 4*x+4]
			if !s.xok[x] {
				clear(q)
				continue
			}
			X := s.xw[x]
			c4, c2 := Y*X>>12, Yd*X>>12
			c3, c1 := Y-c4, Yd-c2
			a, b := col(int(s.xi[x])), col(int(s.xi[x])+1)
			for c := range q {
				q[c] = uint8((c1*int32(r0[a+c]) + c2*int32(r0[b+c]) + c3*int32(r1[a+c]) + c4*int32(r1[b+c]) + 2048) >> 12)
			}
		}
	case interpBicubic:
		ty := (int(float64(iy*64)*2)&127 + 1) >> 1
		cy := Catmull.I[ty]
		var rows [4][]uint8
		for j := range rows {
			rows[j] = s.rowAt(yi - 1 + j - wo - 1)
		}
		for x := range p.w {
			q := d[4*x : 4*x+4 : 4*x+4]
			if !s.xok[x] {
				clear(q)
				continue
			}
			cx := Catmull.I[s.xw[x]]
			xi := int(s.xi[x])
			var cs [4]int
			for i := range cs {
				cs[i] = col(xi - 1 + i)
			}
			for c := range q {
				var r [4]int
				for j, row := range rows {
					r[j] = (cx[0]*int(row[cs[0]+c]) + cx[1]*int(row[cs[1]+c]) + cx[2]*int(row[cs[2]+c]) + cx[3]*int(row[cs[3]+c]) + 2048) >> 12
				}
				v := (cy[0]*r[0] + cy[1]*r[1] + cy[2]*r[2] + cy[3]*r[3] + 2048) >> 12
				q[c] = uint8(max(0, min(v, 255)))
			}
		}
	}
}

// Catmull is libvips' table of Catmull-Rom weights at 65 places between two
// pixels, in double precision (F) and in 12-bit fixed point, dropped to
// whole numbers (I).
var Catmull = func() (t struct {
	F [65][4]float64
	I [65][4]int
}) {
	for x := range t.F {
		v := float64(float32(x) / 64)
		cr1 := 1 - v
		cr2 := float64(-0.5 * v)
		cr3 := float64(cr1 * cr2)
		one := float64(cr1 * cr3)
		fou := float64(v * cr3)
		cr4 := fou - one
		two := cr1 - one + cr4
		thr := v - fou - cr4
		t.F[x] = [4]float64{one, two, thr, fou}
		for i, c := range t.F[x] {
			t.I[x][i] = int(float64(c * 4096))
		}
	}
	return t
}()
