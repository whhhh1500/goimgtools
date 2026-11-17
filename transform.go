package goimgtools

import (
	"errors"
	"fmt"
	"image/color"
	"math"
	"strconv"

	"github.com/whhhh1500/goimgtools/internal/ops"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Transformation is how Transform maps each pixel of its output to a point
// of the image: one of Pillow's transform methods, made by TransformAffine,
// TransformExtent, TransformPerspective or TransformQuad. The zero
// Transformation is none, which Transform reports as an error.
type Transformation struct {
	kind transformKind
	data [8]float64
}

type transformKind int

const (
	noTransform transformKind = iota
	affineTransform
	extentTransform
	perspectiveTransform
	quadTransform
)

var transformNames = map[transformKind]string{affineTransform: "affine", extentTransform: "extent",
	perspectiveTransform: "perspective", quadTransform: "quad"}

// transformArgs is how many numbers each kind of Transformation takes.
var transformArgs = map[transformKind]int{affineTransform: 6, extentTransform: 4, perspectiveTransform: 8, quadTransform: 8}

// TransformAffine maps the centre (x, y) of an output pixel to (a·x + b·y +
// c, d·x + e·y + f) in the image: Pillow's Transform.AFFINE with the data
// (a, b, c, d, e, f). The matrix goes from the output to the image, the
// inverse of Affine's.
func TransformAffine(a, b, c, d, e, f float64) Transformation {
	return Transformation{affineTransform, [8]float64{a, b, c, d, e, f}}
}

// TransformExtent maps the output onto the rectangle of the image from (x0,
// y0) to (x1, y1), stretched to fill it: Pillow's Transform.EXTENT, a crop
// and resize in one (with a rectangle beyond the image, what lies outside
// takes the background).
func TransformExtent(x0, y0, x1, y1 float64) Transformation {
	return Transformation{extentTransform, [8]float64{x0, y0, x1, y1}}
}

// TransformPerspective maps (x, y) to ((a·x + b·y + c) / (g·x + h·y + 1),
// (d·x + e·y + f) / (g·x + h·y + 1)) in the image: Pillow's
// Transform.PERSPECTIVE with the data (a, b, c, d, e, f, g, h), which
// straightens a photographed page or sign (the coefficients of the plane
// that takes the corners of the output to its corners in the photograph).
func TransformPerspective(a, b, c, d, e, f, g, h float64) Transformation {
	return Transformation{perspectiveTransform, [8]float64{a, b, c, d, e, f, g, h}}
}

// TransformQuad maps the output onto the quadrilateral of the image whose
// corners are (x0, y0), the output's top left, (x1, y1), its bottom left,
// (x2, y2), its bottom right, and (x3, y3), its top right, bilinearly:
// Pillow's Transform.QUAD with the data (x0, y0, x1, y1, x2, y2, x3, y3).
func TransformQuad(x0, y0, x1, y1, x2, y2, x3, y3 float64) Transformation {
	return Transformation{quadTransform, [8]float64{x0, y0, x1, y1, x2, y2, x3, y3}}
}

// TransformOptions are the optional settings of Transform.
type TransformOptions struct {
	// Background fills the pixels that map outside the image (Pillow's
	// fillcolor); nil leaves them transparent black, or black in an image
	// without transparency.
	Background color.Color
	// Interpolator works out the colours between pixels: bilinear (the
	// default), nearest or bicubic, Pillow's. (Pillow's own default is
	// nearest.)
	Interpolator Interpolator
}

// Transform makes an image of width × height each of whose pixels takes the
// colour at the point of the image that t maps its centre to: Pillow's
// Image.transform((width, height), method, data, resample,
// fillcolor=Background), to the byte, with and without transparency (an
// image with transparency is interpolated with its colours multiplied by
// alpha, as Pillow's RGBa). A palette image is sampled nearest, as Pillow
// samples one.
//
// The whole image is kept in working storage first (a temporary file beyond
// its share of the limit), as for RotateBy.
func (m *Image) Transform(width, height int, t Transformation, opts ...TransformOptions) *Image {
	m.atMostOne("Transform", len(opts))
	o := first(opts)
	n, ok := transformArgs[t.kind]
	if !ok {
		return m.fail(errors.New("goimgtools: Transform takes a Transformation made by TransformAffine, TransformExtent, TransformPerspective or TransformQuad"))
	}
	for _, v := range t.data[:n] {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return m.fail(errors.New("goimgtools: Transform takes finite numbers"))
		}
	}
	if width < 1 || height < 1 {
		return m.fail(fmt.Errorf("goimgtools: Transform to %dx%d: the size must be positive", width, height))
	}
	if _, ok := interpolatorNames[o.Interpolator]; !ok {
		return m.fail(fmt.Errorf("goimgtools: unknown Interpolator %d", o.Interpolator))
	}
	return m.add(transformOp{width, height, t, o})
}

type transformOp struct {
	w, h int
	t    Transformation
	o    TransformOptions
}

func (o transformOp) apply(src pipeline.Source, _ *run) (pipeline.Source, error) {
	kind, a := pillowTransform(o.t, o.w, o.h)
	filter := map[Interpolator]int{InterpolateBilinear: ops.PillowBilinear, InterpolateNearest: ops.PillowNearest, InterpolateBicubic: ops.PillowBicubic}[o.o.Interpolator]
	return ops.TransformPillow(src, kind, a, o.w, o.h, filter, o.o.Background)
}

// pillowTransform is what Pillow's Image.__transformer hands its C code
// for t and an output of w x h: an extent becomes a matrix and a quad its
// coefficients, worked out as its Python does.
func pillowTransform(t Transformation, w, h int) (int, [8]float64) {
	d := t.data
	switch t.kind {
	case extentTransform:
		x0, y0, x1, y1 := d[0], d[1], d[2], d[3]
		return ops.PillowAffine, [8]float64{(x1 - x0) / float64(w), 0, x0, 0, (y1 - y0) / float64(h), y0}
	case perspectiveTransform:
		return ops.PillowPerspective, d
	case quadTransform:
		x0, y0 := d[0], d[1]
		sw, se, ne := d[2:4], d[4:6], d[6:8]
		as, at := 1/float64(w), 1/float64(h)
		return ops.PillowQuad, [8]float64{
			x0, (ne[0] - x0) * as, (sw[0] - x0) * at, float64((se[0]-sw[0]-ne[0]+x0)*as) * at,
			y0, (ne[1] - y0) * as, (sw[1] - y0) * at, float64((se[1]-sw[1]-ne[1]+y0)*as) * at,
		}
	}
	d[6], d[7] = 0, 0
	return ops.PillowAffine, d
}

func (o transformOp) words() []string {
	w := []string{strconv.Itoa(o.w), strconv.Itoa(o.h), transformNames[o.t.kind]}
	for _, v := range o.t.data[:transformArgs[o.t.kind]] {
		w = append(w, strconv.FormatFloat(v, 'g', -1, 64))
	}
	if o.o.Interpolator != InterpolateBilinear {
		w = append(w, interpolatorNames[o.o.Interpolator])
	}
	if o.o.Background != nil {
		w = append(w, colorWord(o.o.Background))
	}
	return w
}
