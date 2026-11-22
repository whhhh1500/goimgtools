package goimgtools

import (
	"bufio"
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// LUT3D is a colour lookup table of three dimensions, a "cube": for a grid
// of red, green and blue values the colour each becomes, the colours
// between the points of the grid interpolated. It is how a look (a film
// stock, a grade, a correction) made in a colour program is carried to
// other pictures. One is read from a .cube file by ParseCube or worked out
// from a function by NewLUT3D, and applied by ColorLUT.
type LUT3D struct {
	size  int
	table []int16 // size^3 colours of three, red changing fastest; see lutScale
}

const (
	// A side of 2 to 65 points, as Pillow's Color3DLUT has it: 65 points
	// are 1.6MB.
	lutMaxSize = 65
	// The arithmetic of Pillow's ColorLUT.c: values in the table are in
	// 1/64 of a level; a position between two points is in 1/32768.
	lutPrecision = 6
	lutScaleBits = 18
	lutShiftBits = 15
)

// Size is the number of points along each side of the cube.
func (l *LUT3D) Size() int { return l.size }

// lutValue is a value of the table, 0 to 1 (and beyond: it is held to what
// 16 bits keep), in the table's units, rounded as Pillow rounds it.
func lutValue(v float64) int16 {
	const unit = 255 << lutPrecision
	switch {
	case v != v:
		return 0
	case v >= (0x7fff-0.5)/unit:
		return 0x7fff
	case v <= (-0x8000+0.5)/unit:
		return -0x8000
	case v < 0:
		return int16(float64(v*unit) - 0.5)
	}
	return int16(float64(v*unit) + 0.5)
}

// NewLUT3D makes a cube of size points a side (2 to 65) from f, which
// gives the colour (red, green, blue, each 0 to 1) that the colour r, g, b
// (each 0 to 1) becomes, as Pillow's Color3DLUT.generate does.
func NewLUT3D(size int, f func(r, g, b float64) (float64, float64, float64)) (*LUT3D, error) {
	if size < 2 || size > lutMaxSize {
		return nil, fmt.Errorf("goimgtools: a colour cube has 2 to %d points a side, not %d", lutMaxSize, size)
	}
	if f == nil {
		return nil, errors.New("goimgtools: NewLUT3D needs a function")
	}
	l := &LUT3D{size: size, table: make([]int16, 3*size*size*size)}
	i := 0
	top := float64(size - 1)
	for b := 0; b < size; b++ {
		for g := 0; g < size; g++ {
			for r := 0; r < size; r++ {
				x, y, z := f(float64(r)/top, float64(g)/top, float64(b)/top)
				l.table[i], l.table[i+1], l.table[i+2] = lutValue(x), lutValue(y), lutValue(z)
				i += 3
			}
		}
	}
	return l, nil
}

// ParseCube reads a cube in the .cube format of Adobe and of DaVinci
// Resolve: lines of text, LUT_3D_SIZE with the points a side, then a line
// of three numbers for every point, red changing fastest; TITLE and
// comments (#) are passed over. Values are 0 to 1 unless DOMAIN_MIN and
// DOMAIN_MAX say otherwise, to which they are scaled. A cube of one
// dimension (LUT_1D_SIZE) or of more than 65 points a side is not read.
func ParseCube(r io.Reader) (*LUT3D, error) {
	bad := func(format string, a ...any) (*LUT3D, error) {
		return nil, fmt.Errorf("goimgtools: cube file: "+format, a...)
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<16)
	var l *LUT3D
	lo, hi := [3]float64{0, 0, 0}, [3]float64{1, 1, 1}
	n := 0
	three := func(f []string) (v [3]float64, ok bool) {
		if len(f) != 3 {
			return v, false
		}
		for i := range v {
			x, err := strconv.ParseFloat(f[i], 64)
			if err != nil || math.IsNaN(x) || math.IsInf(x, 0) {
				return v, false
			}
			v[i] = x
		}
		return v, true
	}
	for sc.Scan() {
		line := strings.TrimSpace(strings.TrimPrefix(sc.Text(), "\ufeff"))
		if line == "" || line[0] == '#' {
			continue
		}
		f := strings.Fields(line)
		switch key := strings.ToUpper(f[0]); key {
		case "TITLE":
			continue
		case "LUT_1D_SIZE":
			return bad("a table of one dimension is not a cube: %w", errors.ErrUnsupported)
		case "LUT_3D_SIZE":
			size, err := strconv.Atoi(strings.Join(f[1:], ""))
			if err != nil || l != nil {
				return bad("LUT_3D_SIZE %q", strings.Join(f[1:], " "))
			}
			if size < 2 || size > lutMaxSize {
				return bad("%d points a side, 2 to %d are read", size, lutMaxSize)
			}
			l = &LUT3D{size: size, table: make([]int16, 3*size*size*size)}
			continue
		case "DOMAIN_MIN", "DOMAIN_MAX", "LUT_3D_INPUT_RANGE":
			if key == "LUT_3D_INPUT_RANGE" {
				// Resolve's: what the cube is laid over, which is the
				// whole of a picture's values here.
				continue
			}
			v, ok := three(f[1:])
			if !ok || n != 0 {
				return bad("%s", line)
			}
			if key == "DOMAIN_MIN" {
				lo = v
			} else {
				hi = v
			}
			continue
		}
		v, ok := three(f)
		if !ok {
			return bad("line %.40q", line)
		}
		if l == nil || n >= len(l.table) {
			return bad("values before LUT_3D_SIZE, or more than it says")
		}
		for i := range v {
			if !(hi[i] > lo[i]) {
				return bad("DOMAIN_MAX is not above DOMAIN_MIN")
			}
			l.table[n+i] = lutValue((v[i] - lo[i]) / (hi[i] - lo[i]))
		}
		n += 3
	}
	if err := sc.Err(); err != nil {
		return bad("%w", err)
	}
	if l == nil {
		return bad("no LUT_3D_SIZE")
	}
	if n != len(l.table) {
		return bad("%d colours, %d points a side take %d", n/3, l.size, len(l.table)/3)
	}
	return l, nil
}

// ColorLUT gives every pixel the colour the cube l has for its colour,
// interpolated between the eight points around it (trilinear), in the
// arithmetic of Pillow's Image.filter with a Color3DLUT: to the byte the
// same. Transparency is kept; the colours are taken as they are stored
// (not multiplied by alpha). It takes the cube's memory (1.6MB at 65
// points a side) and no more with the size of the image.
func (m *Image) ColorLUT(l *LUT3D) *Image {
	if l == nil || l.size < 2 || len(l.table) != 3*l.size*l.size*l.size {
		return m.fail(errors.New("goimgtools: ColorLUT needs a cube (ParseCube, NewLUT3D)"))
	}
	return m.add(lutOp{l})
}

type lutOp struct{ l *LUT3D }

func (o lutOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	size, table := o.l.size, o.l.table
	// How far along the cube a level is, in 1/2^18 of a step: not rounded,
	// so that level 255 stays below the last point (as Pillow has it).
	scale := uint32(float64(size-1) / 255.0 * (1 << lutScaleBits))
	row, plane := 3*size, 3*size*size
	mix := func(a, b []int16, shift int32) [3]int32 {
		k := 1<<lutShiftBits - shift
		return [3]int32{
			int32(int16((int32(a[0])*k + int32(b[0])*shift) >> lutShiftBits)),
			int32(int16((int32(a[1])*k + int32(b[1])*shift) >> lutShiftBits)),
			int32(int16((int32(a[2])*k + int32(b[2])*shift) >> lutShiftBits)),
		}
	}
	mix2 := func(a, b [3]int32, shift int32) [3]int32 {
		k := 1<<lutShiftBits - shift
		return [3]int32{
			int32(int16((a[0]*k + b[0]*shift) >> lutShiftBits)),
			int32(int16((a[1]*k + b[1]*shift) >> lutShiftBits)),
			int32(int16((a[2]*k + b[2]*shift) >> lutShiftBits)),
		}
	}
	clip := func(v int32) uint8 {
		v = (v + 1<<(lutPrecision-1)) >> lutPrecision
		return uint8(max(0, min(v, 255)))
	}
	return pipeline.Map(src, func(s *image.NRGBA) error {
		b := s.Rect
		for y := b.Min.Y; y < b.Max.Y; y++ {
			px := s.Pix[s.PixOffset(b.Min.X, y):][:4*b.Dx()]
			for i := 0; i+3 < len(px); i += 4 {
				i1, i2, i3 := uint32(px[i])*scale, uint32(px[i+1])*scale, uint32(px[i+2])*scale
				s1 := int32(i1 & (1<<lutScaleBits - 1) >> (lutScaleBits - lutShiftBits))
				s2 := int32(i2 & (1<<lutScaleBits - 1) >> (lutScaleBits - lutShiftBits))
				s3 := int32(i3 & (1<<lutScaleBits - 1) >> (lutScaleBits - lutShiftBits))
				at := 3*int(i1>>lutScaleBits) + row*int(i2>>lutScaleBits) + plane*int(i3>>lutScaleBits)
				t := table[at : at+plane+row+6]
				left := mix2(mix(t[0:], t[3:], s1), mix(t[row:], t[row+3:], s1), s2)
				right := mix2(mix(t[plane:], t[plane+3:], s1), mix(t[plane+row:], t[plane+row+3:], s1), s2)
				v := mix2(left, right, s3)
				px[i], px[i+1], px[i+2] = clip(v[0]), clip(v[1]), clip(v[2])
			}
		}
		return nil
	}), nil
}
