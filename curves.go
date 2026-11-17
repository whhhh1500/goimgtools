package goimgtools

import (
	"errors"
	"fmt"
	"image"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// CurvesOptions are the optional settings of Curves.
type CurvesOptions struct {
	// Red, Green and Blue are curves of one channel each, points as
	// Curves takes them, applied after the curve of all three (nil: none).
	Red, Green, Blue [][2]uint8
}

// Curves maps each red, green and blue level through a tone curve, as the
// Curves of image editors: the smooth curve through points, each {input
// level, output level}, 0 to 255, a monotone cubic (Fritsch and Carlson's:
// it never overshoots, and rises or falls between two points as they do);
// flat before the first point and after the last. Curves({0, 0}, {64, 40},
// {192, 220}, {255, 255}) adds contrast; {{0, 255}, {255, 0}} is Negate.
// No points leaves the levels as they are. Transparency is kept.
//
// For a table of your own (Pillow's Image.point), see Pixels.
func (m *Image) Curves(points [][2]uint8, opts ...CurvesOptions) *Image {
	m.atMostOne("Curves", len(opts))
	o := first(opts)
	var tables [3]*[256]uint8
	all, err := curveTable(points)
	if err != nil {
		return m.fail(err)
	}
	for i, ch := range [][][2]uint8{o.Red, o.Green, o.Blue} {
		t, err := curveTable(ch)
		if err != nil {
			return m.fail(err)
		}
		var c [256]uint8
		for v := range c {
			c[v] = t[all[v]]
		}
		tables[i] = &c
	}
	return m.add(curvesOp{points: slices.Clone(points), o: o, tables: tables})
}

// curveTable is the table of the curve through points.
func curveTable(points [][2]uint8) (*[256]uint8, error) {
	var t [256]uint8
	for v := range t {
		t[v] = uint8(v)
	}
	if len(points) == 0 {
		return &t, nil
	}
	p := slices.Clone(points)
	slices.SortFunc(p, func(a, b [2]uint8) int { return int(a[0]) - int(b[0]) })
	for i := 1; i < len(p); i++ {
		if p[i][0] == p[i-1][0] {
			return nil, fmt.Errorf("goimgtools: Curves: two points at level %d", p[i][0])
		}
	}
	n := len(p)
	x := func(i int) float64 { return float64(p[i][0]) }
	y := func(i int) float64 { return float64(p[i][1]) }
	// The slopes between the points, and at each point (Fritsch–Carlson).
	d := make([]float64, max(n-1, 0))
	for k := range d {
		d[k] = (y(k+1) - y(k)) / (x(k+1) - x(k))
	}
	s := make([]float64, n)
	if n > 1 {
		s[0], s[n-1] = d[0], d[n-2]
	}
	for k := 1; k < n-1; k++ {
		if d[k-1]*d[k] <= 0 {
			s[k] = 0
		} else {
			s[k] = (d[k-1] + d[k]) / 2
		}
	}
	for k := range d {
		if d[k] == 0 {
			s[k], s[k+1] = 0, 0
			continue
		}
		a, b := s[k]/d[k], s[k+1]/d[k]
		if h := float64(a*a) + float64(b*b); h > 9 {
			tau := 3 / math.Sqrt(h)
			s[k], s[k+1] = float64(tau*a)*d[k], float64(tau*b)*d[k]
		}
	}
	k := 0
	for v := range t {
		var out float64
		switch {
		case v <= int(p[0][0]):
			out = y(0)
		case v >= int(p[n-1][0]):
			out = y(n - 1)
		default:
			for v > int(p[k+1][0]) {
				k++
			}
			h := x(k+1) - x(k)
			u := (float64(v) - x(k)) / h
			u2 := float64(u * u)
			u3 := float64(u2 * u)
			// Hermite: h00, h10, h01, h11.
			h00 := float64(2*u3) - float64(3*u2) + 1
			h10 := u3 - float64(2*u2) + u
			h01 := float64(-2*u3) + float64(3*u2)
			h11 := u3 - u2
			out = float64(h00*y(k)) + float64(float64(h10*h)*s[k]) + float64(h01*y(k+1)) + float64(float64(h11*h)*s[k+1])
		}
		t[v] = uint8(math.Max(0, math.Min(255, math.Round(out))))
	}
	return &t, nil
}

type curvesOp struct {
	points [][2]uint8
	o      CurvesOptions
	tables [3]*[256]uint8
}

func (c curvesOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly {
		return src, nil
	}
	tr, tg, tb := c.tables[0], c.tables[1], c.tables[2]
	if *tr == *tg && *tg == *tb {
		return ops.Levels(src, tr), nil
	}
	return pipeline.Map(src, func(strip *image.NRGBA) error {
		for i := 0; i+3 < len(strip.Pix); i += 4 {
			p := strip.Pix[i : i+4 : i+4]
			p[0], p[1], p[2] = tr[p[0]], tg[p[1]], tb[p[2]]
		}
		return nil
	}), nil
}

// curveWords writes points as X:Y, joined by sep.
func curveWords(points [][2]uint8, sep string) string {
	var w []string
	for _, p := range points {
		w = append(w, strconv.Itoa(int(p[0]))+":"+strconv.Itoa(int(p[1])))
	}
	return strings.Join(w, sep)
}

func (c curvesOp) words() []string {
	var w []string
	for _, p := range c.points {
		w = append(w, curveWords([][2]uint8{p}, ""))
	}
	for i, ch := range [][][2]uint8{c.o.Red, c.o.Green, c.o.Blue} {
		if ch != nil {
			w = append(w, []string{"red", "green", "blue"}[i]+"="+curveWords(ch, "/"))
		}
	}
	return w
}

// parseCurvePoints reads X:Y points, joined by sep.
func parseCurvePoints(s, sep string) ([][2]uint8, error) {
	var pts [][2]uint8
	for _, w := range strings.Split(s, sep) {
		xs, ys, ok := strings.Cut(w, ":")
		x, err1 := strconv.ParseUint(xs, 10, 8)
		y, err2 := strconv.ParseUint(ys, 10, 8)
		if !ok || err1 != nil || err2 != nil {
			return nil, fmt.Errorf("a point is X:Y, levels 0 to 255, not %q", w)
		}
		pts = append(pts, [2]uint8{uint8(x), uint8(y)})
	}
	return pts, nil
}

func curvesStep(m *Image, a []string) error {
	var points [][2]uint8
	var o CurvesOptions
	for _, w := range a {
		name, val, isChannel := strings.Cut(w, "=")
		if !isChannel {
			p, err := parseCurvePoints(w, "/")
			if err != nil {
				return err
			}
			points = append(points, p...)
			continue
		}
		p, err := parseCurvePoints(val, "/")
		if err != nil {
			return err
		}
		dst := map[string]*[][2]uint8{"red": &o.Red, "green": &o.Green, "blue": &o.Blue}[strings.ToLower(name)]
		if dst == nil {
			return errors.New("a channel curve is red=, green= or blue=")
		}
		if *dst != nil {
			return fmt.Errorf("%s given twice", name)
		}
		*dst = p
	}
	m.Curves(points, o)
	return nil
}
