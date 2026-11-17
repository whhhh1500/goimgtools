package resize

import (
	"math"
	"sync"

	"github.com/whhhh1500/goimgtools/pipeline"
	"golang.org/x/image/draw"
)

// ScaleLinear is ScaleExtent in linear light: the levels are made linear
// (undoing the sRGB curve) as the rows are read, and encoded again as the
// rows of the result are made, so that what the kernel averages is light.
func ScaleLinear(src pipeline.Source, dw, dh int, q *draw.Kernel, ew, eh float64) (pipeline.Source, error) {
	s, err := ScaleExtent(src, dw, dh, q, ew, eh)
	if err != nil {
		return nil, err
	}
	s.(*scaler).lin = linTables()
	return s, nil
}

// linTable converts between sRGB levels and linear light.
type linTable struct {
	// decode is the linear light of each 16-bit level, on the same scale.
	decode [0x10000]float32
	// encode is the 8-bit sRGB level of linear light i/0xffff, rounded.
	encode [0x10000]uint8
}

var linTables = sync.OnceValue(func() *linTable {
	t := &linTable{}
	for i := range t.decode {
		v := float64(i) / 0xffff
		if v <= 0.04045 {
			v /= 12.92
		} else {
			v = math.Pow((v+0.055)/1.055, 2.4)
		}
		t.decode[i] = float32(v * 0xffff)
	}
	for i := range t.encode {
		v := float64(i) / 0xffff
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = float64(1.055*math.Pow(v, 1/2.4)) - 0.055
		}
		t.encode[i] = uint8(math.Round(math.Min(1, math.Max(0, v)) * 255))
	}
	return t
})

// level16 is the 16-bit level nearest v.
func level16(v float64) uint16 { return uint16(min(max(v+0.5, 0), 0xffff)) }

// row makes a row readRow read linear: its colours, multiplied by alpha
// (16-bit levels, 0 to 0xffff), are divided by alpha, made linear and
// multiplied by alpha again.
func (t *linTable) row(row [][4]float64) {
	for i := range row {
		p := &row[i]
		switch a := p[3]; {
		case a >= 0xffff:
			p[0], p[1], p[2] = float64(t.decode[level16(p[0])]), float64(t.decode[level16(p[1])]), float64(t.decode[level16(p[2])])
		case a > 0:
			k := 0xffff / a
			for c := 0; c < 3; c++ {
				p[c] = float64(t.decode[level16(float64(p[c]*k))]) * a / 0xffff
			}
		}
	}
}

// grayRow is row for the levels of a greyscale row.
func (t *linTable) grayRow(row []float64) {
	for i, v := range row {
		row[i] = float64(t.decode[level16(v)])
	}
}
