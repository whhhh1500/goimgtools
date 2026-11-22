package goimgtools

import (
	"fmt"
	"image/color"
	"strings"
)

// The shapes of Draw and Bandbool by name, for Do and the command line's
// -op: the numbers first, then fill=COLOUR, stroke=COLOUR, width=PIXELS
// and, for a rectangle, radius=PIXELS, in any order.

// shapeArgs splits the arguments of a shape into its numbers and its
// named settings, and makes the shape with them.
func shapeArgs(a []string, min, max int, named map[string]bool, make func(v []float64) Shape) (Shape, error) {
	var nums []string
	set := map[string]string{}
	for _, s := range a {
		if k, v, ok := strings.Cut(s, "="); ok {
			k = strings.ToLower(strings.TrimSpace(k))
			if !named[k] {
				return Shape{}, fmt.Errorf("%q is not a setting of this shape", k)
			}
			set[k] = strings.TrimSpace(v)
			continue
		}
		if len(set) > 0 {
			return Shape{}, fmt.Errorf("the numbers come before fill=, stroke=, width= and radius=")
		}
		nums = append(nums, s)
	}
	if len(nums) < min || max > 0 && len(nums) > max {
		if min == max {
			return Shape{}, fmt.Errorf("want %d numbers, got %d", min, len(nums))
		}
		return Shape{}, fmt.Errorf("want at least %d numbers, got %d", min, len(nums))
	}
	v, err := argFloats(nums)
	if err != nil {
		return Shape{}, err
	}
	s := make(v)
	colour := func(k string) (color.Color, error) {
		c, err := ParseColor(set[k])
		if err != nil {
			return nil, fmt.Errorf("%s=: %w", k, err)
		}
		return c, nil
	}
	if _, ok := set["fill"]; ok {
		c, err := colour("fill")
		if err != nil {
			return Shape{}, err
		}
		s = s.Fill(c)
	}
	width := 1.0
	if w, ok := set["width"]; ok {
		f, err := argFloats([]string{w})
		if err != nil {
			return Shape{}, fmt.Errorf("width=: %w", err)
		}
		width = f[0]
	}
	if _, ok := set["stroke"]; ok {
		c, err := colour("stroke")
		if err != nil {
			return Shape{}, err
		}
		s = s.Stroke(c, width)
	}
	if r, ok := set["radius"]; ok {
		f, err := argFloats([]string{r})
		if err != nil {
			return Shape{}, fmt.Errorf("radius=: %w", err)
		}
		s = s.Rounded(f[0])
	}
	return s, nil
}

var (
	shapeNamed = map[string]bool{"fill": true, "stroke": true, "width": true}
	rectNamed  = map[string]bool{"fill": true, "stroke": true, "width": true, "radius": true}
	lineNamed  = map[string]bool{"stroke": true, "width": true}
)

// drawOp1 is a named operation that draws one shape.
func drawOp1(name, usage, help string, min, max int, named map[string]bool, make func(v []float64) Shape) OperationDef {
	return OperationDef{Name: name, Usage: usage, Help: help, builtIn: func(m *Image, a []string) error {
		s, err := shapeArgs(a, min, max, named, make)
		if err != nil {
			return err
		}
		if named["fill"] && s.fill == nil && s.stroke == nil {
			return fmt.Errorf("give fill=COLOUR or stroke=COLOUR")
		}
		if (s.kind == shapePolyline || s.kind == shapeArc) && s.stroke == nil {
			s = s.Stroke(color.Black, 1)
		}
		m.Draw(s)
		return nil
	}}
}

var drawOps = map[string]OperationDef{
	"rect": drawOp1("rect", "X,Y,WIDTH,HEIGHT,[fill=COLOR],[stroke=COLOR],[width=W],[radius=R]",
		"Draw a rectangle, filled and/or outlined (width W pixels, 1 by default), its corners round with radius R",
		4, 4, rectNamed, func(v []float64) Shape { return Rect(v[0], v[1], v[2], v[3]) }),
	"ellipse": drawOp1("ellipse", "CX,CY,RX,RY,[fill=COLOR],[stroke=COLOR],[width=W]",
		"Draw an ellipse centred at CX,CY with radii RX and RY, filled and/or outlined",
		4, 4, shapeNamed, func(v []float64) Shape { return Ellipse(v[0], v[1], v[2], v[3]) }),
	"circle": drawOp1("circle", "CX,CY,R,[fill=COLOR],[stroke=COLOR],[width=W]",
		"Draw a circle centred at CX,CY of radius R, filled and/or outlined",
		3, 3, shapeNamed, func(v []float64) Shape { return Circle(v[0], v[1], v[2]) }),
	"polygon": drawOp1("polygon", "X1,Y1,X2,Y2,X3,Y3,...,[fill=COLOR],[stroke=COLOR],[width=W]",
		"Draw the polygon through the points, filled and/or outlined",
		6, 0, shapeNamed, func(v []float64) Shape { return Polygon(v...) }),
	"arc": drawOp1("arc", "CX,CY,RX,RY,START,END,[stroke=COLOR],[width=W]",
		"Draw the arc of the ellipse centred at CX,CY with radii RX and RY from the angle START to END (degrees, clockwise from three o'clock; black, 1 pixel wide by default)",
		6, 6, lineNamed, func(v []float64) Shape { return Arc(v[0], v[1], v[2], v[3], v[4], v[5]) }),
	"pie": drawOp1("pie", "CX,CY,RX,RY,START,END,[fill=COLOR],[stroke=COLOR],[width=W]",
		"Draw the slice of the ellipse between the angles START and END and its centre, filled and/or outlined",
		6, 6, shapeNamed, func(v []float64) Shape { return Pie(v[0], v[1], v[2], v[3], v[4], v[5]) }),
	"chord": drawOp1("chord", "CX,CY,RX,RY,START,END,[fill=COLOR],[stroke=COLOR],[width=W]",
		"Draw the part of the ellipse between the angles START and END, closed by a straight line, filled and/or outlined",
		6, 6, shapeNamed, func(v []float64) Shape { return Chord(v[0], v[1], v[2], v[3], v[4], v[5]) }),
	"line": drawOp1("line", "X1,Y1,X2,Y2,...,[stroke=COLOR],[width=W]",
		"Draw a line through the points (black, 1 pixel wide by default), round where it turns",
		4, 0, lineNamed, func(v []float64) Shape { return Polyline(v...) }),
	"bandbool": {Name: "bandbool", Usage: "and|or|eor", Help: "Bandbool: every pixel grey, the bitwise and, or or exclusive or of its red, green and blue, as sharp's bandbool",
		builtIn: func(m *Image, a []string) error {
			if err := wantArgs(a, 1, 1); err != nil {
				return err
			}
			op, err := pick(map[BoolOp]string{BoolAnd: "and", BoolOr: "or", BoolEor: "eor"}, strings.TrimSpace(a[0]), "operation")
			if err != nil {
				return err
			}
			m.Bandbool(op)
			return nil
		}},
}

func init() {
	for n, d := range drawOps {
		builtInOps[n] = d
	}
}
