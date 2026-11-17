// Package iccprof reads the ICC colour profiles of RGB images that are
// described by three primaries and three tone curves (matrix/TRC
// profiles: Display P3, Adobe RGB, ProPhoto, the sRGB variants cameras
// write) and converts pixel values from such a profile to sRGB, the
// colours a viewer without colour management assumes.
//
// It follows the ICC specification (ICC.1:2010, the same structures in
// version 2 profiles): the colorant tags (rXYZ, gXYZ, bXYZ) are adapted to
// the D50 illuminant of the connection space, so a pixel's linear values
// times the colorant matrix are its D50 XYZ; sRGB's own colorants, adapted
// to D50 the same way (Bradford), take it back. The rendering is relative
// colorimetric: colours outside sRGB are clipped.
//
// It also converts from profiles described by lookup tables (lut8Type,
// lut16Type, lutAToBType), RGB or CMYK, whose connection space is XYZ or
// Lab: by their perceptual table (AToB0), as browsers and sharp convert a
// CMYK photograph, interpolated as LittleCMS interpolates; and from grey
// profiles of a tone curve (those of a printing press's dot gain).
package iccprof

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"image/color"
	"math"
	"sync"
)

// Profile is a matrix/TRC RGB profile, or a profile of lookup tables.
type Profile struct {
	// toXYZ maps linear red, green and blue to D50 XYZ, by rows.
	toXYZ [3][3]float64
	trc   [3]curve
	// lut, of a profile of lookup tables: its AToB transform; lab says
	// that its connection space is Lab. key identifies the profile's
	// bytes, for the cache of conversions.
	lut *lut
	lab bool
	key [32]byte
	// gray, of a grey profile: its tone curve, to Y (or, with a Lab
	// connection space, to L*).
	gray *curve
}

// Channels is how many values a pixel in the profile's colour space has:
// 3 (RGB) or 4 (CMYK).
func (p *Profile) Channels() int {
	switch {
	case p.lut != nil:
		return p.lut.in
	case p.gray != nil:
		return 1
	}
	return 3
}

// curve is a tone curve, from encoded value to linear light, on [0, 1].
type curve struct {
	kind  int // 0 table, 1 parametric
	table []float64
	fn    int
	p     [7]float64
}

func (c *curve) at(x float64) float64 {
	if c.kind == 0 {
		n := len(c.table)
		switch n {
		case 0:
			return x
		case 1:
			return math.Pow(x, c.table[0])
		}
		f := float64(x * float64(n-1))
		i := int(f)
		if i >= n-1 {
			return c.table[n-1]
		}
		t := f - float64(i)
		return c.table[i] + float64((c.table[i+1]-c.table[i])*t)
	}
	g, a, b, cc, d, e, f := c.p[0], c.p[1], c.p[2], c.p[3], c.p[4], c.p[5], c.p[6]
	pow := func(v float64) float64 {
		if v <= 0 {
			return 0
		}
		return math.Pow(v, g)
	}
	switch c.fn {
	case 0:
		return pow(x)
	case 1:
		if x >= -b/a {
			return pow(float64(a*x) + b)
		}
		return 0
	case 2:
		if x >= -b/a {
			return pow(float64(a*x)+b) + cc
		}
		return cc
	case 3:
		if x >= d {
			return pow(float64(a*x) + b)
		}
		return float64(cc * x)
	default: // 4
		if x >= d {
			return pow(float64(a*x)+b) + e
		}
		return float64(cc*x) + f
	}
}

var (
	errNotICC      = errors.New("iccprof: not an ICC profile")
	errUnsupported = errors.New("iccprof: not a matrix/TRC RGB profile or an RGB or CMYK profile of lookup tables")
)

// Parse reads a profile. It returns an error for data that is not an ICC
// profile and for profiles it cannot convert from.
func Parse(b []byte) (*Profile, error) {
	if len(b) < 132 || string(b[36:40]) != "acsp" {
		return nil, errNotICC
	}
	if size := binary.BigEndian.Uint32(b); size < 132 || int64(size) > int64(len(b)) {
		return nil, errNotICC
	}
	space, pcs := string(b[16:20]), string(b[20:24])
	if space != "RGB " && space != "CMYK" && space != "GRAY" || pcs != "XYZ " && pcs != "Lab " {
		return nil, errUnsupported
	}
	n := binary.BigEndian.Uint32(b[128:])
	if n > 1000 || 132+12*int64(n) > int64(len(b)) {
		return nil, errNotICC
	}
	tags := map[string][]byte{}
	for i := 0; i < int(n); i++ {
		e := b[132+12*i:]
		off, size := binary.BigEndian.Uint32(e[4:]), binary.BigEndian.Uint32(e[8:])
		if int64(off)+int64(size) > int64(len(b)) || size < 8 {
			return nil, errNotICC
		}
		tags[string(e[:4])] = b[off : off+size]
	}
	if space == "RGB " && pcs == "XYZ " {
		if p, err := matrixProfile(tags); err == nil {
			return p, nil
		}
	}
	if space == "GRAY" {
		c, err := readCurve(tags["kTRC"])
		if err != nil {
			return nil, err
		}
		return &Profile{gray: &c, lab: pcs == "Lab "}, nil
	}
	// A profile of lookup tables: its perceptual transform, or failing
	// that another.
	in := 3
	if space == "CMYK" {
		in = 4
	}
	for _, name := range []string{"A2B0", "A2B1", "A2B2"} {
		if t := tags[name]; t != nil {
			if l, err := readLut(t, in); err == nil {
				return &Profile{lut: l, lab: pcs == "Lab ", key: sha256.Sum256(b)}, nil
			}
		}
	}
	return nil, errUnsupported
}

// matrixProfile reads the colorants and tone curves of a matrix/TRC
// profile.
func matrixProfile(tags map[string][]byte) (*Profile, error) {
	var p Profile
	for i, name := range []string{"rXYZ", "gXYZ", "bXYZ"} {
		t := tags[name]
		if len(t) < 20 || string(t[:4]) != "XYZ " {
			return nil, errUnsupported
		}
		for k := 0; k < 3; k++ {
			p.toXYZ[k][i] = s15(t[8+4*k:])
		}
	}
	for i, name := range []string{"rTRC", "gTRC", "bTRC"} {
		c, err := readCurve(tags[name])
		if err != nil {
			return nil, err
		}
		p.trc[i] = c
	}
	if det(p.toXYZ) == 0 {
		return nil, errUnsupported
	}
	return &p, nil
}

func s15(b []byte) float64 { return float64(int32(binary.BigEndian.Uint32(b))) / 65536 }

func readCurve(t []byte) (curve, error) {
	if len(t) < 12 {
		return curve{}, errUnsupported
	}
	switch string(t[:4]) {
	case "curv":
		n := binary.BigEndian.Uint32(t[8:])
		if n > 1<<16 || 12+2*int64(n) > int64(len(t)) {
			return curve{}, errUnsupported
		}
		if n == 1 {
			g := float64(binary.BigEndian.Uint16(t[12:])) / 256
			return curve{kind: 0, table: []float64{g}}, nil
		}
		c := curve{kind: 0, table: make([]float64, n)}
		for i := range c.table {
			c.table[i] = float64(binary.BigEndian.Uint16(t[12+2*i:])) / 65535
		}
		return c, nil
	case "para":
		fn := int(binary.BigEndian.Uint16(t[8:]))
		counts := [5]int{1, 3, 4, 5, 7}
		if fn > 4 || 12+4*int64(counts[fn]) > int64(len(t)) {
			return curve{}, errUnsupported
		}
		c := curve{kind: 1, fn: fn}
		for i := 0; i < counts[fn]; i++ {
			c.p[i] = s15(t[12+4*i:])
		}
		if fn >= 1 && c.p[1] == 0 {
			return curve{}, errUnsupported
		}
		return c, nil
	}
	return curve{}, errUnsupported
}

// The matrix arithmetic rounds every product before adding it (the
// explicit conversions), as Convert does.

func det(m [3][3]float64) float64 {
	minor := func(a, b, c, d float64) float64 { return float64(a*b) - float64(c*d) }
	return float64(m[0][0]*minor(m[1][1], m[2][2], m[1][2], m[2][1])) -
		float64(m[0][1]*minor(m[1][0], m[2][2], m[1][2], m[2][0])) +
		float64(m[0][2]*minor(m[1][0], m[2][1], m[1][1], m[2][0]))
}

func inverse(m [3][3]float64) [3][3]float64 {
	d := det(m)
	var r [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			a, b := (j+1)%3, (j+2)%3
			c, e := (i+1)%3, (i+2)%3
			r[i][j] = (float64(m[a][c]*m[b][e]) - float64(m[a][e]*m[b][c])) / d
		}
	}
	return r
}

func mul(a, b [3][3]float64) [3][3]float64 {
	var r [3][3]float64
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			r[i][j] = float64(a[i][0]*b[0][j]) + float64(a[i][1]*b[1][j]) + float64(a[i][2]*b[2][j])
		}
	}
	return r
}

// srgbXYZ is the colorant matrix of sRGB adapted to D50 (Bradford), as
// the sRGB profiles of the ICC and LittleCMS have it.
var srgbXYZ = [3][3]float64{
	{0.4360747, 0.3850649, 0.1430804},
	{0.2225045, 0.7168786, 0.0606169},
	{0.0139322, 0.0971045, 0.7141733},
}

func srgbDecode(v float64) float64 {
	if v <= 0.04045 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// IsSRGB reports whether the profile is sRGB, to within what 8 bits can
// tell: its colorants and curves those of sRGB.
func (p *Profile) IsSRGB() bool {
	if p.lut != nil {
		return false
	}
	if p.gray != nil {
		t := p.grayTable()
		for v := range t {
			if t[v] != uint8(v) {
				return false
			}
		}
		return true
	}
	for i := range p.toXYZ {
		for j := range p.toXYZ[i] {
			if math.Abs(p.toXYZ[i][j]-srgbXYZ[i][j]) > 0.002 {
				return false
			}
		}
	}
	for c := range p.trc {
		for v := 0; v < 256; v += 5 {
			x := float64(v) / 255
			if math.Abs(p.trc[c].at(x)-srgbDecode(x)) > 0.5/255 {
				return false
			}
		}
	}
	return true
}

// grayTable is the grey of sRGB of each value of a grey profile: the
// white of the connection space is sRGB's white, so a grey stays a grey
// of the same luminance.
func (p *Profile) grayTable() *[256]uint8 {
	var t [256]uint8
	for v := range t {
		y := p.gray.at(float64(v) / 255)
		if p.lab {
			// The curve gives L* (0 to 1 for 0 to 100).
			f := (float64(y*100) + 16) / 116
			if f > 6.0/29 {
				y = float64(f*f) * f
			} else {
				y = float64(3*float64(6.0/29*6.0/29)) * (f - 4.0/29)
			}
		}
		y = clamp01(y)
		if y <= 0.0031308 {
			y *= 12.92
		} else {
			y = float64(1.055*math.Pow(y, 1/2.4)) - 0.055
		}
		t[v] = uint8(math.Round(clamp01(y) * 255))
	}
	return &t
}

// Transform converts 8-bit values in the profile to 8-bit sRGB.
type Transform struct {
	// gray, for a grey profile: the sRGB grey of each value.
	gray *[256]uint8
	// grid, for a profile of lookup tables: the conversion at the points
	// of a grid, which the values are interpolated in.
	grid  *lutGrid
	lin   [3][256]float64
	m     [3][3]float64
	gamma [gammaSteps + 1]uint8
	// ident is set when the conversion changes no value.
	ident bool
}

const gammaSteps = 1 << 14

// ToSRGB returns the conversion from p to sRGB.
func (p *Profile) ToSRGB() *Transform {
	if p.lut != nil {
		return &Transform{grid: cachedGrid(p)}
	}
	if p.gray != nil {
		t := &Transform{gray: p.grayTable()}
		t.ident = p.IsSRGB()
		return t
	}
	t := &Transform{m: mul(inverse(srgbXYZ), p.toXYZ)}
	for c := 0; c < 3; c++ {
		for v := 0; v < 256; v++ {
			t.lin[c][v] = p.trc[c].at(float64(v) / 255)
		}
	}
	for i := range t.gamma {
		v := float64(i) / gammaSteps
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = float64(1.055*math.Pow(v, 1/2.4)) - 0.055
		}
		t.gamma[i] = uint8(math.Round(math.Max(0, math.Min(1, v)) * 255))
	}
	t.ident = p.IsSRGB()
	return t
}

// Identity reports whether the conversion leaves every value as it is
// (the profile is sRGB).
func (t *Transform) Identity() bool { return t.ident }

// Convert converts one pixel. Every product is rounded before it is added
// (the explicit conversions), so the result is the same on processors that
// fuse a multiplication and an addition and on those that do not.
func (t *Transform) Convert(r, g, b uint8) (uint8, uint8, uint8) {
	if t.gray != nil {
		return t.gray[r], t.gray[g], t.gray[b]
	}
	if t.grid != nil {
		if t.grid.in != 3 {
			return r, g, b // not an RGB profile
		}
		dev := [3]uint8{r, g, b}
		return t.grid.convert(dev[:])
	}
	lr, lg, lb := t.lin[0][r], t.lin[1][g], t.lin[2][b]
	var out [3]uint8
	for i := 0; i < 3; i++ {
		v := float64(t.m[i][0]*lr) + float64(t.m[i][1]*lg) + float64(t.m[i][2]*lb)
		switch {
		case !(v > 0):
			out[i] = 0
		case v >= 1:
			out[i] = 255
		default:
			out[i] = t.gamma[int(float64(v*gammaSteps)+0.5)]
		}
	}
	return out[0], out[1], out[2]
}

// ConvertCMYK converts one pixel of a CMYK profile (values of ink: 0 none,
// 255 full).
func (t *Transform) ConvertCMYK(c, m, y, k uint8) (uint8, uint8, uint8) {
	if t.grid == nil || t.grid.in != 4 {
		// Not a CMYK profile: the values as color.CMYK takes them.
		r, g, b := color.CMYKToRGB(c, m, y, k)
		return r, g, b
	}
	dev := [4]uint8{c, m, y, k}
	return t.grid.convert(dev[:])
}

// The conversions of the profiles of lookup tables used last, since
// working out a grid takes some milliseconds: a batch of photographs from
// one source has one profile.
var (
	gridMu    sync.Mutex
	gridCache = map[[32]byte]*lutGrid{}
)

func cachedGrid(p *Profile) *lutGrid {
	gridMu.Lock()
	defer gridMu.Unlock()
	if g := gridCache[p.key]; g != nil {
		return g
	}
	if len(gridCache) >= 4 {
		clear(gridCache)
	}
	g := newLutGrid(p.lut, p.lab)
	gridCache[p.key] = g
	return g
}
