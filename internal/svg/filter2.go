package svg

import (
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/trig"
)

// The primitives that make or move pixels: turbulence, morphology,
// convolution, displacement, tiles, images and lighting, as the Filter
// Effects specification describes them; and the subregions primitives
// draw in.

// subregion is the rectangle of the region's pixels primitive p draws in:
// its x, y, width and height, those missing taken from the filter region;
// without any, the union of those of its inputs (the whole region for the
// image of the element, and for a primitive of no input).
func (c *fctx) subregion(p *node, inputs []string) image.Rectangle {
	whole := image.Rect(0, 0, c.w, c.h)
	_, hx := p.attrs["x"]
	_, hy := p.attrs["y"]
	_, hw := p.attrs["width"]
	_, hh := p.attrs["height"]
	if !hx && !hy && !hw && !hh {
		// feTile, made to fill more than its input, fills the region.
		if len(inputs) == 0 || p.name == "feTile" {
			return whole
		}
		var r image.Rectangle
		for _, name := range inputs {
			switch name = strings.TrimSpace(name); name {
			case "":
				r = r.Union(c.lastRect)
			default:
				if rr, ok := c.rects[name]; ok {
					r = r.Union(rr)
				} else {
					r = whole // the element's image, or another the region holds
				}
			}
		}
		return r.Intersect(whole)
	}
	x, y, w, h := c.region[0], c.region[1], c.region[2], c.region[3]
	length := func(name string, def float64, axis int, of float64) float64 {
		v, ok := p.attrs[name]
		if !ok {
			return def
		}
		v = strings.TrimSpace(v)
		if c.primBox {
			if strings.HasSuffix(v, "%") {
				f, err := strconv.ParseFloat(strings.TrimSuffix(v, "%"), 64)
				if err != nil {
					return def
				}
				return float64(f / 100 * of)
			}
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return def
			}
			return float64(f * of)
		}
		f, ok := parseLength(v, c.fontSize, c.vp, axis)
		if !ok {
			return def
		}
		return f
	}
	if c.primBox {
		x = c.bbox[0] + length("x", x-c.bbox[0], axisX, c.bbox[2])
		y = c.bbox[1] + length("y", y-c.bbox[1], axisY, c.bbox[3])
		w = length("width", w, axisX, c.bbox[2])
		h = length("height", h, axisY, c.bbox[3])
	} else {
		x, y = length("x", x, axisX, 0), length("y", y, axisY, 0)
		w, h = length("width", w, axisX, 0), length("height", h, axisY, 0)
	}
	if !(w > 0) || !(h > 0) {
		return image.Rectangle{}
	}
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, k := range [][2]float64{{x, y}, {x + w, y}, {x, y + h}, {x + w, y + h}} {
		px, py := c.full.apply(k[0], k[1])
		minX, maxX = math.Min(minX, px), math.Max(maxX, px)
		minY, maxY = math.Min(minY, py), math.Max(maxY, py)
	}
	lim := float64(1 << 24)
	toInt := func(v float64) int { return int(math.Max(-lim, math.Min(lim, v))) }
	return image.Rect(toInt(math.Floor(minX)), toInt(math.Floor(minY)), toInt(math.Ceil(maxX)), toInt(math.Ceil(maxY))).Intersect(whole)
}

// inputsOf names the inputs of a primitive.
func inputsOf(p *node) []string {
	switch p.name {
	case "feFlood", "feTurbulence", "feImage":
		return nil
	case "feMerge":
		var in []string
		for _, k := range p.children {
			if k.name == "feMergeNode" {
				in = append(in, k.attrs["in"])
			}
		}
		return in
	case "feComposite", "feBlend", "feDisplacementMap":
		return []string{p.attrs["in"], p.attrs["in2"]}
	}
	return []string{p.attrs["in"]}
}

// clip clears the pixels of b outside r.
func (c *fctx) clip(b fbuf, r image.Rectangle) {
	if r == image.Rect(0, 0, c.w, c.h) {
		return
	}
	for y := 0; y < c.h; y++ {
		row := b[4*y*c.w : 4*(y+1)*c.w]
		if y < r.Min.Y || y >= r.Max.Y {
			clear(row)
			continue
		}
		clear(row[:4*max(0, min(c.w, r.Min.X))])
		clear(row[4*max(0, min(c.w, r.Max.X)):])
	}
}

// at is the pixel (x, y) of b, transparent outside it.
func (c *fctx) at(b fbuf, x, y int) []float32 {
	if x < 0 || y < 0 || x >= c.w || y >= c.h {
		return nil
	}
	return b[4*(y*c.w+x) : 4*(y*c.w+x)+4]
}

// The pseudo-random lattice of feTurbulence, as the specification gives
// it (Ken Perlin's noise).
const (
	turbBSize   = 0x100
	turbBM      = 0xff
	turbPerlinN = 0x1000
	turbRandM   = 2147483647
	turbRandA   = 16807
	turbRandQ   = 127773
	turbRandR   = 2836
)

type turbulence struct {
	lattice  [turbBSize + turbBSize + 2]int
	gradient [4][turbBSize + turbBSize + 2][2]float64
}

func turbRandom(seed int64) int64 {
	r := turbRandA*(seed%turbRandQ) - turbRandR*(seed/turbRandQ)
	if r <= 0 {
		r += turbRandM
	}
	return r
}

func newTurbulence(seed int64) *turbulence {
	t := &turbulence{}
	if seed <= 0 {
		seed = -(seed % (turbRandM - 1)) + 1
	}
	if seed > turbRandM-1 {
		seed = turbRandM - 1
	}
	for k := 0; k < 4; k++ {
		for i := 0; i < turbBSize; i++ {
			t.lattice[i] = i
			for j := 0; j < 2; j++ {
				seed = turbRandom(seed)
				t.gradient[k][i][j] = float64((seed%(turbBSize+turbBSize))-turbBSize) / turbBSize
			}
			g := &t.gradient[k][i]
			s := math.Sqrt(float64(g[0]*g[0]) + float64(g[1]*g[1]))
			g[0], g[1] = g[0]/s, g[1]/s
		}
	}
	for i := turbBSize - 1; i > 0; i-- {
		k := t.lattice[i]
		seed = turbRandom(seed)
		j := int(seed % turbBSize)
		t.lattice[i], t.lattice[j] = t.lattice[j], k
	}
	for i := 0; i < turbBSize+2; i++ {
		t.lattice[turbBSize+i] = t.lattice[i]
		for k := 0; k < 4; k++ {
			t.gradient[k][turbBSize+i] = t.gradient[k][i]
		}
	}
	return t
}

type turbStitch struct{ width, height, wrapX, wrapY int }

func sCurve(t float64) float64 { return float64(float64(t*t) * (3 - float64(2*t))) }

func lerpf(t, a, b float64) float64 { return a + float64(t*(b-a)) }

func (t *turbulence) noise2(ch int, vx, vy float64, st *turbStitch) float64 {
	u := vx + turbPerlinN
	bx0 := int(u) & turbBM
	bx1 := (bx0 + 1) & turbBM
	rx0 := u - float64(int(u))
	rx1 := rx0 - 1
	u = vy + turbPerlinN
	by0 := int(u) & turbBM
	by1 := (by0 + 1) & turbBM
	ry0 := u - float64(int(u))
	ry1 := ry0 - 1
	if st != nil {
		if bx0 >= st.wrapX {
			bx0 -= st.width
		}
		if bx1 >= st.wrapX {
			bx1 -= st.width
		}
		if by0 >= st.wrapY {
			by0 -= st.height
		}
		if by1 >= st.wrapY {
			by1 -= st.height
		}
	}
	bx0 &= turbBM
	bx1 &= turbBM
	by0 &= turbBM
	by1 &= turbBM
	i, j := t.lattice[bx0], t.lattice[bx1]
	b00, b10 := t.lattice[i+by0], t.lattice[j+by0]
	b01, b11 := t.lattice[i+by1], t.lattice[j+by1]
	sx, sy := sCurve(rx0), sCurve(ry0)
	g := &t.gradient[ch]
	dot := func(q [2]float64, x, y float64) float64 { return float64(x*q[0]) + float64(y*q[1]) }
	a := lerpf(sx, dot(g[b00], rx0, ry0), dot(g[b10], rx1, ry0))
	b := lerpf(sx, dot(g[b01], rx0, ry1), dot(g[b11], rx1, ry1))
	return lerpf(sy, a, b)
}

// sum is the turbulence function of the specification at (x, y).
func (t *turbulence) sum(ch int, x, y, fx, fy float64, octaves int, fractal bool, stitch *turbStitch) float64 {
	var st turbStitch
	var ps *turbStitch
	if stitch != nil {
		st = *stitch
		ps = &st
	}
	vx, vy := float64(x*fx), float64(y*fy)
	total, ratio := 0.0, 1.0
	for o := 0; o < octaves; o++ {
		n := t.noise2(ch, vx, vy, ps)
		if !fractal {
			n = math.Abs(n)
		}
		total += n / ratio
		vx, vy, ratio = vx*2, vy*2, ratio*2
		if ps != nil {
			st.width *= 2
			st.wrapX = 2*st.wrapX - turbPerlinN
			st.height *= 2
			st.wrapY = 2*st.wrapY - turbPerlinN
		}
	}
	return total
}

func (c *fctx) turbulence(p *node, r image.Rectangle) (fbuf, error) {
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	fr, _ := numbers(p.attrs["baseFrequency"])
	fx, fy := 0.0, 0.0
	if len(fr) > 0 {
		fx, fy = fr[0], fr[0]
	}
	if len(fr) > 1 {
		fy = fr[1]
	}
	if fx < 0 || fy < 0 {
		return out, nil // an error: transparent black
	}
	octaves := 1
	if v, err := strconv.ParseFloat(strings.TrimSpace(p.attrs["numOctaves"]), 64); err == nil {
		octaves = int(max(0, min(24, v))) // beyond this the octaves add nothing a pixel shows
	}
	seed := 0.0
	if v, err := strconv.ParseFloat(strings.TrimSpace(p.attrs["seed"]), 64); err == nil {
		seed = math.Max(-1e9, math.Min(1e9, math.Trunc(v)))
	}
	fractal := strings.TrimSpace(p.attrs["type"]) == "fractalNoise"
	inv, ok := c.full.invert()
	if !ok {
		return out, nil
	}
	var stitch *turbStitch
	if strings.TrimSpace(p.attrs["stitchTiles"]) == "stitch" {
		// The tile: the subregion, in user space.
		tx, ty := inv.apply(float64(r.Min.X), float64(r.Min.Y))
		bx, by := inv.apply(float64(r.Max.X), float64(r.Max.Y))
		tw, th := math.Abs(bx-tx), math.Abs(by-ty)
		tx, ty = math.Min(tx, bx), math.Min(ty, by)
		adjust := func(f, size float64) float64 {
			if f == 0 || !(size > 0) {
				return f
			}
			lo := math.Floor(float64(size*f)) / size
			hi := math.Ceil(float64(size*f)) / size
			if lo > 0 && f/lo < hi/f {
				return lo
			}
			return hi
		}
		fx, fy = adjust(fx, tw), adjust(fy, th)
		w := int(float64(tw*fx) + 0.5)
		h := int(float64(th*fy) + 0.5)
		stitch = &turbStitch{width: w, height: h, wrapX: int(float64(tx*fx)) + turbPerlinN + w, wrapY: int(float64(ty*fy)) + turbPerlinN + h}
	}
	t := newTurbulence(int64(seed))
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ux, uy := inv.apply(float64(x), float64(y))
			var px [4]float32
			for ch := 0; ch < 4; ch++ {
				v := t.sum(ch, ux, uy, fx, fy, octaves, fractal, stitch)
				if fractal {
					v = float64(v+1) / 2
				}
				px[ch] = clampf(float32(v))
			}
			o := out[4*(y*c.w+x):]
			for ch := 0; ch < 3; ch++ {
				o[ch] = float32(px[ch] * px[3])
			}
			o[3] = px[3]
		}
	}
	return out, nil
}

// radius reads a pair of lengths of a primitive (one for both, or x then
// y) in device pixels.
func (c *fctx) pair(p *node, name string, def float64) (float64, float64) {
	v, _ := numbers(p.attrs[name])
	x, y := def, def
	if len(v) > 0 {
		x, y = v[0], v[0]
	}
	if len(v) > 1 {
		y = v[1]
	}
	kx, ky := c.devScale()
	return float64(float64(x*c.unit[0]) * kx), float64(float64(y*c.unit[1]) * ky)
}

func (c *fctx) morphology(p *node) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	rxf, ryf := c.pair(p, "radius", 0)
	if !(rxf > 0) || !(ryf > 0) {
		// A radius of nothing: the input as it is.
		out, err := c.alloc()
		if err == nil {
			copy(out, in)
		}
		return out, err
	}
	// Whole pixels, rounded up, as librsvg.
	rx := int(math.Min(float64(c.w), math.Ceil(rxf)))
	ry := int(math.Min(float64(c.h), math.Ceil(ryf)))
	dilate := strings.TrimSpace(p.attrs["operator"]) == "dilate"
	pick := func(a, b float32) float32 {
		if dilate {
			return max(a, b)
		}
		return min(a, b)
	}
	tmp, err := c.alloc()
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	// The minimum (or maximum) over the rectangle, a row then a column at
	// a time, beyond the picture transparent: by van Herk and Gil-Werman's
	// running extremes over blocks of the window's size, whatever the
	// radius three steps a value.
	long := max(c.w, c.h) + 2*max(rx, ry) + 2*max(rx, ry) + 2
	pre, suf, line := make([]float32, long), make([]float32, long), make([]float32, long)
	pass := func(src, dst fbuf, n, lines, r int, at func(line, i int) int) {
		k := 2*r + 1
		m := n + 2*r // the line with r transparent pixels either side
		blocks := (m + k - 1) / k * k
		for l := 0; l < lines; l++ {
			for ch := 0; ch < 4; ch++ {
				for i := 0; i < blocks; i++ {
					line[i] = 0
					if j := i - r; j >= 0 && j < n {
						line[i] = src[at(l, j)+ch]
					}
				}
				for b0 := 0; b0 < blocks; b0 += k {
					pre[b0] = line[b0]
					for i := b0 + 1; i < b0+k; i++ {
						pre[i] = pick(pre[i-1], line[i])
					}
					suf[b0+k-1] = line[b0+k-1]
					for i := b0 + k - 2; i >= b0; i-- {
						suf[i] = pick(suf[i+1], line[i])
					}
				}
				// The window of pixel i is line[i .. i+2r].
				for i := 0; i < n; i++ {
					dst[at(l, i)+ch] = pick(suf[i], pre[i+k-1])
				}
			}
		}
	}
	pass(in, tmp, c.w, c.h, rx, func(y, x int) int { return 4 * (y*c.w + x) })
	pass(tmp, out, c.h, c.w, ry, func(x, y int) int { return 4 * (y*c.w + x) })
	return out, nil
}

func (c *fctx) convolve(p *node) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	pass := func() (fbuf, error) { copy(out, in); return out, nil }
	order, _ := numbers(attrOr(p, "order", "3"))
	ox, oy := 3, 3
	if len(order) > 0 {
		ox, oy = int(order[0]), int(order[0])
		if order[0] != math.Trunc(order[0]) {
			return pass()
		}
	}
	if len(order) > 1 {
		oy = int(order[1])
		if order[1] != math.Trunc(order[1]) {
			return pass()
		}
	}
	km, _ := numbers(p.attrs["kernelMatrix"])
	if ox < 1 || oy < 1 || ox > 64 || oy > 64 || len(km) != ox*oy {
		return pass() // not a kernel: the input passes on
	}
	divisor := 0.0
	for _, k := range km {
		divisor += k
	}
	if v, ok := p.attrs["divisor"]; ok {
		if d, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			if d == 0 {
				return pass()
			}
			divisor = d
		}
	}
	if divisor == 0 {
		divisor = 1
	}
	bias, _ := strconv.ParseFloat(strings.TrimSpace(attrOr(p, "bias", "0")), 64)
	tx, ty := ox/2, oy/2
	if v, err := strconv.Atoi(strings.TrimSpace(attrOr(p, "targetX", "x"))); err == nil {
		if v < 0 || v >= ox {
			return pass()
		}
		tx = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(attrOr(p, "targetY", "x"))); err == nil {
		if v < 0 || v >= oy {
			return pass()
		}
		ty = v
	}
	edge := strings.TrimSpace(attrOr(p, "edgeMode", "duplicate"))
	keepAlpha := strings.TrimSpace(p.attrs["preserveAlpha"]) == "true"
	// The kernel turned half a turn, as the specification applies it.
	k := make([]float32, ox*oy)
	for i := 0; i < oy; i++ {
		for j := 0; j < ox; j++ {
			k[i*ox+j] = float32(km[(oy-1-i)*ox+(ox-1-j)])
		}
	}
	src := in
	if keepAlpha {
		// The colours unpremultiplied, the alpha left as it is.
		if src, err = c.alloc(); err != nil {
			return nil, err
		}
		for i := 0; i < len(in); i += 4 {
			if a := in[i+3]; a > 0 {
				src[i], src[i+1], src[i+2], src[i+3] = in[i]/a, in[i+1]/a, in[i+2]/a, a
			}
		}
	}
	get := func(x, y int) []float32 {
		switch edge {
		case "wrap":
			x, y = ((x%c.w)+c.w)%c.w, ((y%c.h)+c.h)%c.h
		case "none":
			if x < 0 || y < 0 || x >= c.w || y >= c.h {
				return nil
			}
		default: // duplicate
			x, y = max(0, min(c.w-1, x)), max(0, min(c.h-1, y))
		}
		return src[4*(y*c.w+x) : 4*(y*c.w+x)+4]
	}
	inv := float32(1 / divisor)
	b := float32(bias)
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			var acc [4]float32
			for i := 0; i < oy; i++ {
				for j := 0; j < ox; j++ {
					q := get(x-tx+j, y-ty+i)
					if q == nil {
						continue
					}
					kv := k[i*ox+j]
					for ch := 0; ch < 4; ch++ {
						acc[ch] += float32(q[ch] * kv)
					}
				}
			}
			o := out[4*(y*c.w+x) : 4*(y*c.w+x)+4]
			if keepAlpha {
				a := in[4*(y*c.w+x)+3]
				for ch := 0; ch < 3; ch++ {
					o[ch] = float32(clampf(float32(acc[ch]*inv)+b) * a)
				}
				o[3] = a
				continue
			}
			a := clampf(float32(acc[3]*inv) + b)
			for ch := 0; ch < 3; ch++ {
				o[ch] = min(clampf(float32(acc[ch]*inv)+float32(b*a)), a)
			}
			o[3] = a
		}
	}
	return out, nil
}

func (c *fctx) displacement(p *node) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	mp, err := c.input(p.attrs["in2"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	scale := 0.0
	if v, err := strconv.ParseFloat(strings.TrimSpace(p.attrs["scale"]), 64); err == nil {
		scale = v
	}
	kx, ky := c.devScale()
	sx, sy := float64(float64(scale*c.unit[0])*kx), float64(float64(scale*c.unit[1])*ky)
	channel := func(name string) int {
		switch strings.TrimSpace(p.attrs[name]) {
		case "R":
			return 0
		case "G":
			return 1
		case "B":
			return 2
		}
		return 3
	}
	xc, yc := channel("xChannelSelector"), channel("yChannelSelector")
	for y := 0; y < c.h; y++ {
		for x := 0; x < c.w; x++ {
			m := mp[4*(y*c.w+x) : 4*(y*c.w+x)+4]
			comp := func(ch int) float64 {
				if ch == 3 {
					return float64(m[3])
				}
				if m[3] == 0 {
					return 0
				}
				return float64(m[ch] / m[3]) // the map unpremultiplied
			}
			dx := float64(sx * (comp(xc) - 0.5))
			dy := float64(sy * (comp(yc) - 0.5))
			// The nearest pixel.
			px := c.at(in, int(math.Floor(float64(x)+dx+0.5)), int(math.Floor(float64(y)+dy+0.5)))
			if px != nil {
				copy(out[4*(y*c.w+x):4*(y*c.w+x)+4], px)
			}
		}
	}
	return out, nil
}

// tile fills r with copies of the input's subregion.
func (c *fctx) tile(p *node, r image.Rectangle) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	t := c.lastRect
	if name := strings.TrimSpace(p.attrs["in"]); name != "" {
		if rr, ok := c.rects[name]; ok {
			t = rr
		} else {
			t = image.Rect(0, 0, c.w, c.h)
		}
	}
	if t.Empty() {
		return out, nil
	}
	tw, th := t.Dx(), t.Dy()
	for y := r.Min.Y; y < r.Max.Y; y++ {
		sy := t.Min.Y + ((y-t.Min.Y)%th+th)%th
		for x := r.Min.X; x < r.Max.X; x++ {
			sx := t.Min.X + ((x-t.Min.X)%tw+tw)%tw
			copy(out[4*(y*c.w+x):4*(y*c.w+x)+4], in[4*(sy*c.w+sx):4*(sy*c.w+sx)+4])
		}
	}
	return out, nil
}

// feImage: the picture made for it before the primitives ran.
func (c *fctx) image(p *node) (fbuf, error) {
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	if b, ok := c.images[p]; ok {
		copy(out, b)
	}
	return out, nil
}

// lightingColor is the lighting-color of a primitive, in the filter's
// colour space.
func (c *fctx) lightingColor(p *node, st style) [3]float32 {
	col := c.floodColor(p, "lighting-color", "", st)
	if _, ok := p.prop("lighting-color"); !ok {
		col = [4]float32{1, 1, 1, 1}
	}
	return [3]float32{col[0], col[1], col[2]}
}

// light is a light source of a lighting primitive, in device pixels.
type light struct {
	kind              int // 0 distant, 1 point, 2 spot
	dir               [3]float64
	pos, at           [3]float64
	exponent, cosCone float64
	hasCone           bool
}

func (c *fctx) lightOf(p *node) (light, bool) {
	for _, k := range p.children {
		num := func(name string, def float64) float64 {
			v, err := strconv.ParseFloat(strings.TrimSpace(k.attrs[name]), 64)
			if err != nil {
				return def
			}
			return v
		}
		// A point of user space (of the primitive units) on the device;
		// its height scaled as lengths are.
		point := func(xn, yn, zn string) [3]float64 {
			x := num(xn, 0)
			y := num(yn, 0)
			z := num(zn, 0)
			if c.primBox {
				x, y = c.bbox[0]+float64(x*c.bbox[2]), c.bbox[1]+float64(y*c.bbox[3])
				z = float64(z * math.Sqrt(float64(float64(c.bbox[2]*c.bbox[2])+float64(c.bbox[3]*c.bbox[3]))/2))
			}
			px, py := c.full.apply(x, y)
			kx, ky := c.devScale()
			return [3]float64{px, py, float64(z*float64(kx+ky)) / 2}
		}
		switch k.name {
		case "feDistantLight":
			sa, ca := trig.SinCosDeg(num("azimuth", 0))
			se, ce := trig.SinCosDeg(num("elevation", 0))
			return light{kind: 0, dir: [3]float64{float64(ca * ce), float64(sa * ce), se}}, true
		case "fePointLight":
			return light{kind: 1, pos: point("x", "y", "z")}, true
		case "feSpotLight":
			l := light{kind: 2, pos: point("x", "y", "z"), at: point("pointsAtX", "pointsAtY", "pointsAtZ"), exponent: num("specularExponent", 1)}
			if v, ok := k.attrs["limitingConeAngle"]; ok {
				if a, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					_, l.cosCone = trig.SinCosDeg(math.Abs(a))
					l.hasCone = true
				}
			}
			return l, true
		}
	}
	return light{}, false
}

func norm3(v [3]float64) [3]float64 {
	l := math.Sqrt(float64(v[0]*v[0]) + float64(v[1]*v[1]) + float64(v[2]*v[2]))
	if l == 0 {
		return v
	}
	return [3]float64{v[0] / l, v[1] / l, v[2] / l}
}

func dot3(a, b [3]float64) float64 {
	return float64(a[0]*b[0]) + float64(a[1]*b[1]) + float64(a[2]*b[2])
}

// lighting lights the alpha of the input as a surface: diffuse (Lambert)
// or specular (Phong's half vector), as the specification describes them.
func (c *fctx) lighting(p *node, st style, specular bool) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	lt, ok := c.lightOf(p)
	if !ok {
		return out, nil // no light: transparent black
	}
	fnum := func(name string, def float64) float64 {
		if v, err := strconv.ParseFloat(strings.TrimSpace(p.attrs[name]), 64); err == nil {
			return v
		}
		return def
	}
	ss := fnum("surfaceScale", 1)
	kd := fnum("diffuseConstant", 1)
	ks := fnum("specularConstant", 1)
	exp := fnum("specularExponent", 1)
	if specular && (exp < 1 || exp > 128) {
		exp = 1
	}
	if kd < 0 || ks < 0 {
		return out, nil
	}
	col := c.lightingColor(p, st)
	var s [3]float64 // the spot light's direction
	if lt.kind == 2 {
		s = norm3([3]float64{lt.at[0] - lt.pos[0], lt.at[1] - lt.pos[1], lt.at[2] - lt.pos[2]})
	}
	alpha := func(x, y int) float64 { return float64(in[4*(y*c.w+x)+3]) }
	for y := 0; y < c.h; y++ {
		top, bot := max(0, y-1), min(c.h-1, y+1)
		for x := 0; x < c.w; x++ {
			left, right := max(0, x-1), min(c.w-1, x+1)
			// The Sobel kernels over the neighbours there are, as the
			// specification's table for edges and corners gives them.
			var nx, ny float64
			if right > left {
				var sum, wsum float64
				for yy := top; yy <= bot; yy++ {
					w := 1.0
					if yy == y {
						w = 2
					}
					sum += float64(w * (alpha(right, yy) - alpha(left, yy)))
					wsum += w
				}
				nx = -float64(float64(ss*2/(wsum*float64(right-left))) * sum)
			}
			if bot > top {
				var sum, wsum float64
				for xx := left; xx <= right; xx++ {
					w := 1.0
					if xx == x {
						w = 2
					}
					sum += float64(w * (alpha(xx, bot) - alpha(xx, top)))
					wsum += w
				}
				ny = -float64(float64(ss*2/(wsum*float64(bot-top))) * sum)
			}
			n := norm3([3]float64{nx, ny, 1})
			z := float64(ss * alpha(x, y))
			var l [3]float64
			lc := [3]float64{float64(col[0]), float64(col[1]), float64(col[2])}
			switch lt.kind {
			case 0:
				l = lt.dir
			default:
				l = norm3([3]float64{lt.pos[0] - float64(x), lt.pos[1] - float64(y), lt.pos[2] - z})
				if lt.kind == 2 {
					m := -dot3(l, s)
					if m <= 0 || lt.hasCone && m < lt.cosCone {
						lc = [3]float64{}
					} else {
						f := trig.Pow(m, lt.exponent)
						lc = [3]float64{float64(lc[0] * f), float64(lc[1] * f), float64(lc[2] * f)}
					}
				}
			}
			o := out[4*(y*c.w+x) : 4*(y*c.w+x)+4]
			if !specular {
				f := float64(kd * dot3(n, l))
				for ch := 0; ch < 3; ch++ {
					o[ch] = clampf(float32(float64(f * lc[ch])))
				}
				o[3] = 1
				continue
			}
			h := norm3([3]float64{l[0], l[1], l[2] + 1})
			nh := dot3(n, h)
			f := 0.0
			if nh > 0 {
				f = float64(ks * trig.Pow(nh, exp))
			}
			var a float32
			for ch := 0; ch < 3; ch++ {
				o[ch] = clampf(float32(float64(f * lc[ch])))
				a = max(a, o[ch])
			}
			o[3] = a
		}
	}
	return out, nil
}
