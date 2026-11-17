package iccprof

import (
	"encoding/binary"
	"math"
	"sync"
)

// lut is the AToB transform of a profile described by lookup tables
// (lut8Type 'mft1', lut16Type 'mft2' or lutAToBType 'mAB '): from the
// device's values (RGB or CMYK) to the connection space (XYZ or Lab),
// every value normalized to [0, 1].
type lut struct {
	in, out int
	// a: one curve per input (the input tables of mft1 and mft2).
	a []curve
	// The grid, grid[i] points along input i, the first input varying
	// slowest; out values per point.
	grid []int
	clut []float64
	// m, matrix (3x3 then offsets): those of mAB, applied after the grid
	// when present.
	m      []curve
	matrix *[12]float64
	// b: one curve per output (the output tables of mft1 and mft2).
	b []curve
	// legacyLab: a 16-bit table (mft2) of a Lab profile, which encodes
	// L* 100 as 0xFF00.
	legacyLab bool
}

// readLut reads an AToB tag of a profile with in device channels.
func readLut(t []byte, in int) (*lut, error) {
	if len(t) < 32 {
		return nil, errUnsupported
	}
	switch string(t[:4]) {
	case "mft1", "mft2":
		return readMft(t, in)
	case "mAB ":
		return readMAB(t, in)
	}
	return nil, errUnsupported
}

func readMft(t []byte, in int) (*lut, error) {
	wide := t[3] == '2'
	ni, no, g := int(t[8]), int(t[9]), int(t[10])
	if ni != in || no != 3 || g < 2 || len(t) < 48 {
		return nil, errUnsupported
	}
	l := &lut{in: ni, out: no, grid: make([]int, ni), legacyLab: wide}
	for i := range l.grid {
		l.grid[i] = g
	}
	inN, outN, size, p := 256, 256, 1, 48
	if wide {
		if len(t) < 52 {
			return nil, errUnsupported
		}
		inN, outN = int(binary.BigEndian.Uint16(t[48:])), int(binary.BigEndian.Uint16(t[50:]))
		size, p = 2, 52
		if inN < 2 || inN > 4096 || outN < 2 || outN > 4096 {
			return nil, errUnsupported
		}
	}
	points := 1
	for i := 0; i < ni; i++ {
		points *= g
		if points > 1<<20 {
			return nil, errUnsupported
		}
	}
	need := p + size*(ni*inN+points*no+no*outN)
	if len(t) < need {
		return nil, errUnsupported
	}
	value := func() float64 {
		var v float64
		if wide {
			v = float64(binary.BigEndian.Uint16(t[p:])) / 65535
		} else {
			v = float64(t[p]) / 255
		}
		p += size
		return v
	}
	table := func(n int) curve {
		c := curve{table: make([]float64, n)}
		for i := range c.table {
			c.table[i] = value()
		}
		return c
	}
	for i := 0; i < ni; i++ {
		l.a = append(l.a, table(inN))
	}
	l.clut = make([]float64, points*no)
	for i := range l.clut {
		l.clut[i] = value()
	}
	for i := 0; i < no; i++ {
		l.b = append(l.b, table(outN))
	}
	return l, nil
}

func readMAB(t []byte, in int) (*lut, error) {
	ni, no := int(t[8]), int(t[9])
	if ni != in || no != 3 {
		return nil, errUnsupported
	}
	l := &lut{in: ni, out: no}
	offset := func(i int) int { return int(binary.BigEndian.Uint32(t[12+4*i:])) }
	curves := func(off, n int) ([]curve, error) {
		var cs []curve
		for i := 0; i < n; i++ {
			if off <= 0 || off >= len(t) {
				return nil, errUnsupported
			}
			c, size, err := readCurveSized(t[off:])
			if err != nil {
				return nil, err
			}
			cs = append(cs, c)
			off += (size + 3) &^ 3
		}
		return cs, nil
	}
	var err error
	if off := offset(0); off != 0 {
		if l.b, err = curves(off, no); err != nil {
			return nil, err
		}
	}
	if off := offset(1); off != 0 {
		if off+48 > len(t) {
			return nil, errUnsupported
		}
		var m [12]float64
		for i := range m {
			m[i] = s15(t[off+4*i:])
		}
		l.matrix = &m
	}
	if off := offset(2); off != 0 {
		if l.m, err = curves(off, no); err != nil {
			return nil, err
		}
	}
	if off := offset(3); off != 0 {
		if off+20 > len(t) {
			return nil, errUnsupported
		}
		points := 1
		for i := 0; i < ni; i++ {
			g := int(t[off+i])
			if g < 2 {
				return nil, errUnsupported
			}
			l.grid = append(l.grid, g)
			points *= g
			if points > 1<<20 {
				return nil, errUnsupported
			}
		}
		size := int(t[off+16])
		if size != 1 && size != 2 || off+20+size*points*no > len(t) {
			return nil, errUnsupported
		}
		l.clut = make([]float64, points*no)
		p := off + 20
		for i := range l.clut {
			if size == 2 {
				l.clut[i] = float64(binary.BigEndian.Uint16(t[p:])) / 65535
			} else {
				l.clut[i] = float64(t[p]) / 255
			}
			p += size
		}
	}
	if off := offset(4); off != 0 {
		if l.a, err = curves(off, ni); err != nil {
			return nil, err
		}
	}
	if l.clut == nil && ni != no {
		return nil, errUnsupported
	}
	return l, nil
}

// readCurveSized is readCurve that also says how many bytes the curve
// takes (before its padding to 4 bytes).
func readCurveSized(t []byte) (curve, int, error) {
	c, err := readCurve(t)
	if err != nil {
		return c, 0, err
	}
	switch string(t[:4]) {
	case "curv":
		return c, 12 + 2*int(binary.BigEndian.Uint32(t[8:])), nil
	default:
		counts := [5]int{1, 3, 4, 5, 7}
		return c, 12 + 4*counts[c.fn], nil
	}
}

func clamp01(v float64) float64 { return math.Max(0, math.Min(1, v)) }

// eval takes device values (in of them, each in [0, 1]) to the
// connection space, normalized.
func (l *lut) eval(x []float64) [3]float64 {
	var buf [4]float64
	v := buf[:l.in]
	for i := range v {
		v[i] = clamp01(x[i])
		if l.a != nil {
			v[i] = clamp01(l.a[i].at(v[i]))
		}
	}
	var o [3]float64
	if l.clut != nil {
		interp(l.clut, l.grid, v, o[:])
	} else {
		copy(o[:], v)
	}
	if l.m != nil {
		for i := range o {
			o[i] = clamp01(l.m[i].at(clamp01(o[i])))
		}
	}
	if m := l.matrix; m != nil {
		r := o
		for i := 0; i < 3; i++ {
			o[i] = float64(m[3*i]*r[0]) + float64(m[3*i+1]*r[1]) + float64(m[3*i+2]*r[2]) + m[9+i]
		}
	}
	if l.b != nil {
		for i := range o {
			o[i] = l.b[i].at(clamp01(o[i]))
		}
	}
	return o
}

// interp interpolates the grid at v as LittleCMS does: tetrahedrally in
// three dimensions; in more, linearly along the first between the
// interpolations of the others; in one, linearly.
func interp[T float32 | float64](clut []T, grid []int, v []float64, out []float64) {
	no := len(out)
	switch len(grid) {
	case 1:
		g := grid[0]
		p := float64(v[0] * float64(g-1))
		i := min(int(p), g-2)
		f := p - float64(i)
		for k := 0; k < no; k++ {
			a, b := float64(clut[i*no+k]), float64(clut[(i+1)*no+k])
			out[k] = a + float64((b-a)*f)
		}
	case 3:
		tetra(clut, grid, v, out)
	default:
		g := grid[0]
		stride := len(clut) / g
		p := float64(v[0] * float64(g-1))
		i := min(int(p), g-2)
		f := p - float64(i)
		// The tables have three outputs at most.
		var los, his [3]float64
		lo, hi := los[:no], his[:no]
		interp(clut[i*stride:(i+1)*stride], grid[1:], v[1:], lo)
		if f == 0 {
			copy(out, lo)
			return
		}
		interp(clut[(i+1)*stride:(i+2)*stride], grid[1:], v[1:], hi)
		for k := 0; k < no; k++ {
			out[k] = lo[k] + float64((hi[k]-lo[k])*f)
		}
	}
}

// tetra is LittleCMS's tetrahedral interpolation in three dimensions.
func tetra[T float32 | float64](clut []T, grid []int, v []float64, out []float64) {
	no := len(out)
	var x0, x1 [3]int
	var r [3]float64
	stride := [3]int{grid[1] * grid[2] * no, grid[2] * no, no}
	for d := 0; d < 3; d++ {
		p := float64(v[d] * float64(grid[d]-1))
		i := int(p)
		r[d] = p - float64(i)
		x0[d] = i * stride[d]
		x1[d] = x0[d]
		if v[d] < 1 {
			x1[d] += stride[d]
		}
	}
	rx, ry, rz := r[0], r[1], r[2]
	X0, X1, Y0, Y1, Z0, Z1 := x0[0], x1[0], x0[1], x1[1], x0[2], x1[2]
	for k := 0; k < no; k++ {
		d := func(a, b, c int) float64 { return float64(clut[a+b+c+k]) }
		c0 := d(X0, Y0, Z0)
		var c1, c2, c3 float64
		switch {
		case rx >= ry && ry >= rz:
			c1 = d(X1, Y0, Z0) - c0
			c2 = d(X1, Y1, Z0) - d(X1, Y0, Z0)
			c3 = d(X1, Y1, Z1) - d(X1, Y1, Z0)
		case rx >= rz && rz >= ry:
			c1 = d(X1, Y0, Z0) - c0
			c2 = d(X1, Y1, Z1) - d(X1, Y0, Z1)
			c3 = d(X1, Y0, Z1) - d(X1, Y0, Z0)
		case rz >= rx && rx >= ry:
			c1 = d(X1, Y0, Z1) - d(X0, Y0, Z1)
			c2 = d(X1, Y1, Z1) - d(X1, Y0, Z1)
			c3 = d(X0, Y0, Z1) - c0
		case ry >= rx && rx >= rz:
			c1 = d(X1, Y1, Z0) - d(X0, Y1, Z0)
			c2 = d(X0, Y1, Z0) - c0
			c3 = d(X1, Y1, Z1) - d(X1, Y1, Z0)
		case ry >= rz && rz >= rx:
			c1 = d(X1, Y1, Z1) - d(X0, Y1, Z1)
			c2 = d(X0, Y1, Z0) - c0
			c3 = d(X0, Y1, Z1) - d(X0, Y1, Z0)
		case rz >= ry && ry >= rx:
			c1 = d(X1, Y1, Z1) - d(X0, Y1, Z1)
			c2 = d(X0, Y1, Z1) - d(X0, Y0, Z1)
			c3 = d(X0, Y0, Z1) - c0
		}
		out[k] = c0 + float64(c1*rx) + float64(c2*ry) + float64(c3*rz)
	}
}

// d50 is the white of the connection space.
var d50 = [3]float64{0.9642, 1.0, 0.8249}

// pcsToXYZ takes what the tables give in the connection space to D50 XYZ.
func (l *lut) pcsToXYZ(o [3]float64, lab bool) [3]float64 {
	if !lab {
		// u1Fixed15: 1.0 is 0x8000.
		s := 65535.0 / 32768
		return [3]float64{float64(o[0] * s), float64(o[1] * s), float64(o[2] * s)}
	}
	var L, a, b float64
	if l.legacyLab {
		L = float64(o[0]*(65535.0/65280)) * 100
		a = float64(o[1]*(65535.0/256)) - 128
		b = float64(o[2]*(65535.0/256)) - 128
	} else {
		L = o[0] * 100
		a = float64(o[1]*255) - 128
		b = float64(o[2]*255) - 128
	}
	fy := (L + 16) / 116
	fx := fy + a/500
	fz := fy - b/200
	inv := func(f float64) float64 {
		if f > 6.0/29 {
			return float64(f*f) * f
		}
		return float64(3*float64(6.0/29*6.0/29)) * (f - 4.0/29)
	}
	return [3]float64{float64(d50[0] * inv(fx)), float64(d50[1] * inv(fy)), float64(d50[2] * inv(fz))}
}

// srgbOf takes D50 XYZ to sRGB on 0 to 255, unrounded, relative
// colorimetric: clipped to the gamut.
func srgbOf(xyz [3]float64) [3]float64 {
	m := toSRGBMatrix()
	var out [3]float64
	for i := 0; i < 3; i++ {
		v := float64(m[i][0]*xyz[0]) + float64(m[i][1]*xyz[1]) + float64(m[i][2]*xyz[2])
		v = clamp01(v)
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = float64(1.055*math.Pow(v, 1/2.4)) - 0.055
		}
		out[i] = v * 255
	}
	return out
}

var toSRGBMatrix = sync.OnceValue(func() [3][3]float64 { return inverse(srgbXYZ) })

// lutGrid is a profile's conversion to sRGB worked out at the points of a
// grid over its device values (gridPoints of them each way): the colours
// of pixels are interpolated in it, as LittleCMS does after it has
// optimized a transform.
type lutGrid struct {
	in   int
	grid []int
	val  []float32 // sRGB, 0 to 255
}

func gridPoints(in int) int {
	if in == 4 {
		return 17
	}
	return 33
}

func newLutGrid(p *Profile) *lutGrid {
	l := p.lut
	g := gridPoints(l.in)
	n := 1
	grid := make([]int, l.in)
	for i := range grid {
		grid[i] = g
		n *= g
	}
	t := &lutGrid{in: l.in, grid: grid, val: make([]float32, 3*n)}
	x := make([]float64, l.in)
	for k := 0; k < n; k++ {
		q := k
		for i := l.in - 1; i >= 0; i-- {
			x[i] = float64(q%g) / float64(g-1)
			q /= g
		}
		s := srgbOf(p.bpc.apply(l.pcsToXYZ(l.eval(x), p.lab)))
		t.val[3*k], t.val[3*k+1], t.val[3*k+2] = float32(s[0]), float32(s[1]), float32(s[2])
	}
	return t
}

// convert takes 8-bit device values to 8-bit sRGB.
func (t *lutGrid) convert(dev []uint8) (uint8, uint8, uint8) {
	var x [4]float64
	for i := 0; i < t.in; i++ {
		x[i] = float64(dev[i]) / 255
	}
	var o [3]float64
	interp(t.val, t.grid, x[:t.in], o[:])
	r := func(v float64) uint8 { return uint8(math.Max(0, math.Min(255, math.Round(v)))) }
	return r(o[0]), r(o[1]), r(o[2])
}
