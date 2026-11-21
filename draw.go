package goimgtools

import (
	"errors"
	"image"
	"image/color"
	"math"

	"github.com/whhhh1500/goimgtools/internal/raster"
	"github.com/whhhh1500/goimgtools/internal/trig"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Drawing figures onto the image, as Pillow's ImageDraw does, but with
// smooth (anti-aliased) edges. Coordinates are in pixels from the top-left
// corner of the image as it is at that point of the chain, and may have
// fractions: a pixel's square runs from x to x+1, so a line between pixel
// centres goes from x+0.5.

type shapeKind int

const (
	shapeRect shapeKind = iota
	shapeEllipse
	shapePolygon
	shapePolyline
)

// Shape is a figure for Draw: a rectangle, an ellipse or circle, a polygon,
// a line or a line of several segments, made by Rect, Ellipse, Circle,
// Polygon, Line and Polyline, and then given what to paint with Fill
// (inside) and Stroke (the outline, or the line itself), and for a
// rectangle round corners with Rounded. A Shape is a value: each method
// returns a changed copy.
type Shape struct {
	kind   shapeKind
	xy     []float64 // x, y, w, h; cx, cy, rx, ry; or the points
	radius float64
	fill   color.Color
	stroke color.Color
	width  float64
}

// Rect is the rectangle with its top-left corner at (x, y), w wide and h
// high.
func Rect(x, y, w, h float64) Shape { return Shape{kind: shapeRect, xy: []float64{x, y, w, h}} }

// Ellipse is the ellipse centred at (cx, cy) with radii rx across and ry
// down.
func Ellipse(cx, cy, rx, ry float64) Shape {
	return Shape{kind: shapeEllipse, xy: []float64{cx, cy, rx, ry}}
}

// Circle is the circle centred at (cx, cy) of radius r.
func Circle(cx, cy, r float64) Shape { return Ellipse(cx, cy, r, r) }

// Polygon is the closed figure through the points x1, y1, x2, y2, ... (at
// least three; its sides may cross, the parts covered once or more being
// inside).
func Polygon(xy ...float64) Shape {
	return Shape{kind: shapePolygon, xy: append([]float64(nil), xy...)}
}

// Line is the line from (x1, y1) to (x2, y2): give it a Stroke.
func Line(x1, y1, x2, y2 float64) Shape { return Polyline(x1, y1, x2, y2) }

// Polyline is the line through the points x1, y1, x2, y2, ... (at least
// two), joined with round corners: give it a Stroke.
func Polyline(xy ...float64) Shape {
	return Shape{kind: shapePolyline, xy: append([]float64(nil), xy...)}
}

// Fill paints the inside of the shape with c (its alpha counts). A line
// has no inside.
func (s Shape) Fill(c color.Color) Shape { s.fill = c; return s }

// Stroke paints the outline of the shape, width pixels wide and centred on
// it, with c; for a line, the line itself, its ends cut square.
func (s Shape) Stroke(c color.Color, width float64) Shape { s.stroke, s.width = c, width; return s }

// Rounded gives a rectangle corners of radius r (at most half its smaller
// side).
func (s Shape) Rounded(r float64) Shape { s.radius = r; return s }

// Draw paints the shapes onto the image, one over the other in the order
// given, a stroke over its shape's fill. Their edges are smooth: each pixel
// is painted by the part of it the shape covers (worked out exactly, in
// 1/256 of a pixel, the same on every processor). Shapes may reach past
// the edges of the image. It takes a few rows of memory, whatever the size
// of the image or of the shapes.
//
// A shape with nothing to paint (no Fill and no Stroke, a line without a
// Stroke), a negative size or a value that is not a number is an error.
func (m *Image) Draw(shapes ...Shape) *Image {
	var ds []drawn
	for _, s := range shapes {
		d, err := s.prepare()
		if err != nil {
			return m.fail(err)
		}
		ds = append(ds, d...)
	}
	return m.add(drawOp{ds})
}

// drawn is a path to fill with a colour.
type drawn struct {
	path *raster.Path
	c    color.NRGBA
}

func (s Shape) prepare() ([]drawn, error) {
	for _, v := range append(s.xy, s.radius, s.width) {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return nil, errors.New("goimgtools: Draw: a shape's coordinates and sizes must be numbers")
		}
	}
	if s.stroke != nil && s.width <= 0 {
		return nil, errors.New("goimgtools: Draw: a Stroke must be wider than 0")
	}
	var out []drawn
	add := func(c color.Color, build func(p *raster.Path)) {
		if c == nil {
			return
		}
		p := &raster.Path{}
		build(p)
		if !p.Empty() {
			out = append(out, drawn{p, color.NRGBAModel.Convert(c).(color.NRGBA)})
		}
	}
	hw := s.width / 2
	switch s.kind {
	case shapeRect, shapeEllipse:
		x, y, w, h := s.xy[0], s.xy[1], s.xy[2], s.xy[3]
		if w < 0 || h < 0 || s.radius < 0 {
			return nil, errors.New("goimgtools: Draw: a shape's sizes must not be negative")
		}
		if s.fill == nil && s.stroke == nil {
			return nil, errors.New("goimgtools: Draw: a shape needs a Fill or a Stroke")
		}
		// The outline moved out by grow (in by a negative one).
		outline := func(p *raster.Path, grow float64, reverse bool) {
			if s.kind == shapeEllipse {
				// x, y are the centre and w, h the radii.
				if w+grow > 0 && h+grow > 0 {
					p.Add(ellipse(x, y, w+grow, h+grow, reverse))
				}
				return
			}
			r := 0.0 // square corners stay square
			if s.radius > 0 {
				r = max(0, s.radius+grow)
			}
			if w+2*grow > 0 && h+2*grow > 0 {
				p.Add(roundRect(x-grow, y-grow, w+2*grow, h+2*grow, r, reverse))
			}
		}
		add(s.fill, func(p *raster.Path) { outline(p, 0, false) })
		add(s.stroke, func(p *raster.Path) {
			outline(p, hw, false)
			outline(p, -hw, true) // the hole inside the outline
		})
	case shapePolygon:
		if len(s.xy) < 6 || len(s.xy)%2 != 0 {
			return nil, errors.New("goimgtools: Draw: a Polygon needs at least three points, x and y each")
		}
		if s.fill == nil && s.stroke == nil {
			return nil, errors.New("goimgtools: Draw: a shape needs a Fill or a Stroke")
		}
		add(s.fill, func(p *raster.Path) { p.Add(s.xy) })
		add(s.stroke, func(p *raster.Path) { strokeLine(p, s.xy, hw, true) })
	case shapePolyline:
		if len(s.xy) < 4 || len(s.xy)%2 != 0 {
			return nil, errors.New("goimgtools: Draw: a line needs at least two points, x and y each")
		}
		if s.stroke == nil {
			return nil, errors.New("goimgtools: Draw: a line needs a Stroke")
		}
		add(s.stroke, func(p *raster.Path) { strokeLine(p, s.xy, hw, false) })
	}
	return out, nil
}

// arcSteps is how many sides a quarter of an ellipse of radius r gets:
// enough to keep within a twentieth of a pixel of the curve.
func arcSteps(r float64) int {
	return max(2, int(math.Ceil(float64(math.Pi/2)/math.Sqrt(float64(0.1/max(r, 0.1))))))
}

// ellipse is the ellipse centred at (cx, cy) with radii rx and ry as a
// polygon, clockwise on the screen (counter-clockwise with reverse).
func ellipse(cx, cy, rx, ry float64, reverse bool) []float64 {
	n := 4 * arcSteps(max(rx, ry))
	xy := make([]float64, 0, 2*n)
	for i := 0; i < n; i++ {
		k := i
		if reverse {
			k = n - i
		}
		xy = append(xy, cx+float64(rx*trig.CosPi(2*k, n)), cy+float64(ry*trig.SinPi(2*k, n)))
	}
	return xy
}

// roundRect is the rectangle with corners of radius r as a polygon.
func roundRect(x, y, w, h, r float64, reverse bool) []float64 {
	r = min(r, w/2, h/2)
	var xy []float64
	if r <= 0 {
		xy = []float64{x, y, x + w, y, x + w, y + h, x, y + h}
	} else {
		n := arcSteps(r)
		corner := func(cx, cy float64, q int) {
			// A quarter from angle q*90 degrees, clockwise on the screen.
			for i := 0; i <= n; i++ {
				xy = append(xy, cx+float64(r*trig.CosPi(q*n+i, 2*n)), cy+float64(r*trig.SinPi(q*n+i, 2*n)))
			}
		}
		corner(x+w-r, y+h-r, 0)
		corner(x+r, y+h-r, 1)
		corner(x+r, y+r, 2)
		corner(x+w-r, y+r, 3)
	}
	if reverse {
		for i, j := 0, len(xy)-2; i < j; i, j = i+2, j-2 {
			xy[i], xy[j] = xy[j], xy[i]
			xy[i+1], xy[j+1] = xy[j+1], xy[i+1]
		}
	}
	return xy
}

// strokeLine adds a line of half width hw through the points: a rectangle
// along each segment and a disc at each corner (and, closed, at the first
// point), all in the same direction so that where they overlap they count
// once.
func strokeLine(p *raster.Path, xy []float64, hw float64, closed bool) {
	n := len(xy) / 2
	segs := n - 1
	if closed {
		segs = n
	}
	for i := 0; i < segs; i++ {
		x1, y1 := xy[2*i], xy[2*i+1]
		x2, y2 := xy[2*((i+1)%n)], xy[2*((i+1)%n)+1]
		dx, dy := x2-x1, y2-y1
		l := math.Sqrt(float64(dx*dx) + float64(dy*dy))
		if l == 0 {
			continue
		}
		nx, ny := float64(-dy/l*hw), float64(dx/l*hw)
		p.Add(clockwise([]float64{x1 + nx, y1 + ny, x2 + nx, y2 + ny, x2 - nx, y2 - ny, x1 - nx, y1 - ny}))
	}
	first, last := 1, n-1
	if closed {
		first, last = 0, n
	}
	for i := first; i < last; i++ {
		p.Add(ellipse(xy[2*i], xy[2*i+1], hw, hw, false))
	}
}

// clockwise is the polygon turned to run clockwise on the screen (as
// ellipse runs), so that overlapping parts add up rather than cancel.
func clockwise(xy []float64) []float64 {
	var a float64
	n := len(xy) / 2
	for i := 0; i < n; i++ {
		j := (i + 1) % n
		a += float64(xy[2*i]*xy[2*j+1]) - float64(xy[2*j]*xy[2*i+1])
	}
	if a < 0 {
		for i, j := 0, len(xy)-2; i < j; i, j = i+2, j-2 {
			xy[i], xy[j] = xy[j], xy[i]
			xy[i+1], xy[j+1] = xy[j+1], xy[i+1]
		}
	}
	return xy
}

// drawOp is Draw as a step of the chain.
type drawOp struct{ shapes []drawn }

func (d drawOp) apply(src pipeline.Source, r *run) (pipeline.Source, error) {
	if r.sizeOnly || len(d.shapes) == 0 {
		return src, nil
	}
	var cov []uint8
	var cells raster.Cells
	maxW := 0
	b := src.Bounds()
	env := pipeline.EnvOf(src)
	ds := &drawSource{env: env}
	ds.Source = pipeline.Map(src, func(s *image.NRGBA) error {
		sb := s.Rect
		for _, sh := range d.shapes {
			x0, y0, x1, y1 := sh.path.Bounds()
			// Within the image (its origin is the strip's bounds' origin).
			x0, x1 = max(x0, 0)+b.Min.X, min(x1, b.Dx())+b.Min.X
			y0, y1 = max(y0+b.Min.Y, sb.Min.Y), min(y1+b.Min.Y, sb.Max.Y)
			if x0 >= x1 || y0 >= y1 || sh.c.A == 0 {
				continue
			}
			w, h := x1-x0, y1-y0
			// The coverage of a strip's rows, and a row of cells (of the
			// widest so far).
			maxW = max(maxW, w)
			if need := int64(max(len(cov), w*h)) + 16*int64(maxW); need > ds.held {
				if err := env.Budget.Reserve(need-ds.held, "draw coverage"); err != nil {
					return err
				}
				ds.held = need
			}
			if len(cov) < w*h {
				cov = make([]uint8, w*h)
			}
			sh.path.Render(cov, w, x0-b.Min.X, y0-b.Min.Y, w, h, &cells)
			for yy := 0; yy < h; yy++ {
				row := s.Pix[s.PixOffset(x0, y0+yy):]
				for xx, c := range cov[yy*w : yy*w+w] {
					if c != 0 {
						over(row[4*xx:4*xx+4:4*xx+4], sh.c, uint32(c))
					}
				}
			}
		}
		return nil
	})
	return ds, nil
}

// drawSource gives back the memory of the coverage when it is closed.
type drawSource struct {
	pipeline.Source
	env  *pipeline.Env
	held int64
}

func (d *drawSource) Env() *pipeline.Env { return d.env }
func (d *drawSource) Opaque() bool       { return pipeline.IsOpaque(d.Source) }

func (d *drawSource) Close() error {
	d.env.Budget.Release(d.held)
	d.held = 0
	return d.Source.Close()
}

// over paints colour c with coverage cov (0 to 255) over the non-premultiplied
// pixel px, in integer arithmetic.
func over(px []uint8, c color.NRGBA, cov uint32) {
	sa := (uint32(c.A)*cov + 127) / 255
	if sa == 0 {
		return
	}
	da := uint32(px[3])
	switch {
	case sa == 255:
		px[0], px[1], px[2], px[3] = c.R, c.G, c.B, 255
		return
	case da == 0:
		px[0], px[1], px[2], px[3] = c.R, c.G, c.B, uint8(sa)
		return
	}
	keep := da * (255 - sa) // of 255*255
	oa := sa*255 + keep
	px[0] = uint8((uint32(c.R)*sa*255 + uint32(px[0])*keep + oa/2) / oa)
	px[1] = uint8((uint32(c.G)*sa*255 + uint32(px[1])*keep + oa/2) / oa)
	px[2] = uint8((uint32(c.B)*sa*255 + uint32(px[2])*keep + oa/2) / oa)
	px[3] = uint8((oa + 127) / 255)
}
