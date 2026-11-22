package colorfont

import (
	"encoding/binary"
	"image/color"

	"github.com/whhhh1500/goimgtools/internal/trig"
)

// Version 1 of COLR: a colour glyph is a graph of paints (solid colours,
// gradients, glyph outlines that clip what they hold, transforms and
// composites), as Noto Color Emoji is made. A Painter draws it.

// Painter draws the paint graph of a colour glyph, in font units, y up.
// Each Transform, Clip and ClipBox lasts until its Pop.
type Painter interface {
	// Transform maps what follows by m: x' = m[0]x + m[2]y + m[4],
	// y' = m[1]x + m[3]y + m[5].
	Transform(m [6]float64)
	// Clip clips what follows to the outline of glyph g.
	Clip(g uint16)
	// ClipBox clips what follows to a rectangle.
	ClipBox(x0, y0, x1, y1 float64)
	// Composite draws what source draws onto what backdrop draws with a
	// composite mode (Composite*), the two in a layer of their own.
	Composite(mode int, backdrop, source func())
	Pop()
	// Fill paints the whole of what is clipped with a brush.
	Fill(b *Brush)
}

// The kinds of brushes.
const (
	BrushSolid = iota
	BrushLinear
	BrushRadial
	BrushSweep
)

// The extend modes of a colour line.
const (
	ExtendPad = iota
	ExtendRepeat
	ExtendReflect
)

// The composite modes of PaintComposite, as COLR numbers them.
const (
	CompositeClear = iota
	CompositeSrc
	CompositeDest
	CompositeSrcOver
	CompositeDestOver
	CompositeSrcIn
	CompositeDestIn
	CompositeSrcOut
	CompositeDestOut
	CompositeSrcAtop
	CompositeDestAtop
	CompositeXor
	CompositePlus
	CompositeScreen
	CompositeOverlay
	CompositeDarken
	CompositeLighten
	CompositeColorDodge
	CompositeColorBurn
	CompositeHardLight
	CompositeSoftLight
	CompositeDifference
	CompositeExclusion
	CompositeMultiply
	CompositeHue
	CompositeSaturation
	CompositeColor
	CompositeLuminosity
)

// Stop is a colour stop of a gradient: a colour of the palette (or the
// text's, Foreground) with an alpha.
type Stop struct {
	Offset     float64
	Color      color.NRGBA
	Foreground bool
	Alpha      float64
}

// Brush is what Fill paints with: a solid colour (the first stop) or a
// gradient, in font units: Linear from P[0] to P[1], turned so that its
// lines are parallel to P[0]→P[2]; Radial from the circle at P[0] of
// radius R0 to that at P[1] of R1; Sweep about P[0] from the angle Start
// to End (degrees, counter-clockwise).
type Brush struct {
	Kind       int
	Stops      []Stop
	Extend     int
	P          [3][2]float64
	R0, R1     float64
	Start, End float64
}

// maxPaintDepth bounds how deep a paint graph is followed, maxPaints how
// many paints are drawn (a graph whose paints are shared many times over
// would take long).
const (
	maxPaintDepth = 64
	maxPaints     = 1 << 16
)

// v1 reads where the lists of version 1 are.
func (f *Font) v1(colr []byte) {
	be := binary.BigEndian
	in := func(o int, n int) bool { return o > 0 && o+n <= len(colr) }
	if o := int(be.Uint32(colr[14:])); in(o, 4) {
		n := int(be.Uint32(colr[o:]))
		if n > 0 && n <= (len(colr)-o-4)/6 {
			f.baseList, f.numPaints = o, n
		}
	}
	if o := int(be.Uint32(colr[18:])); in(o, 4) {
		f.layerList = o
	}
	if o := int(be.Uint32(colr[22:])); in(o, 5) {
		f.clipList = o
	}
}

// HasPaint reports whether glyph g is a colour glyph of version 1.
func (f *Font) HasPaint(g uint16) bool {
	_, ok := f.paintOf(g)
	return ok
}

// paintOf is where glyph g's paint is.
func (f *Font) paintOf(g uint16) (int, bool) {
	if f == nil || f.numPaints == 0 {
		return 0, false
	}
	be := binary.BigEndian
	lo, hi := 0, f.numPaints
	for lo < hi {
		m := (lo + hi) / 2
		r := f.colr[f.baseList+4+6*m:]
		switch id := be.Uint16(r); {
		case id == g:
			o := f.baseList + int(be.Uint32(r[2:]))
			return o, o > f.baseList && o < len(f.colr)
		case id < g:
			lo = m + 1
		default:
			hi = m
		}
	}
	return 0, false
}

// Paint draws the paint graph of colour glyph g with p, within its clip
// box when the font gives one; false for a glyph that is not a colour
// glyph of version 1.
func (f *Font) Paint(g uint16, p Painter) bool {
	o, ok := f.paintOf(g)
	if !ok {
		return false
	}
	w := &walker{f: f, p: p, on: map[int]bool{}}
	if box, ok := f.clipBox(g); ok {
		p.ClipBox(box[0], box[1], box[2], box[3])
		defer p.Pop()
	}
	w.paint(o, 0)
	return true
}

// clipBox is the clip box the font gives glyph g.
func (f *Font) clipBox(g uint16) ([4]float64, bool) {
	c := f.colr
	o := f.clipList
	if o == 0 || c[o] != 1 {
		return [4]float64{}, false
	}
	be := binary.BigEndian
	n := int(be.Uint32(c[o+1:]))
	if n > (len(c)-o-5)/7 {
		return [4]float64{}, false
	}
	for i := 0; i < n; i++ {
		r := c[o+5+7*i:]
		if g < be.Uint16(r) || g > be.Uint16(r[2:]) {
			continue
		}
		b := o + int(uint32(r[4])<<16|uint32(r[5])<<8|uint32(r[6]))
		if b+9 > len(c) || c[b] != 1 && c[b] != 2 {
			return [4]float64{}, false
		}
		fw := func(k int) float64 { return float64(int16(be.Uint16(c[b+1+2*k:]))) }
		return [4]float64{fw(0), fw(1), fw(2), fw(3)}, true
	}
	return [4]float64{}, false
}

// walker follows a paint graph; on marks the paints on the way to the
// one being drawn, against cycles.
type walker struct {
	f     *Font
	p     Painter
	on    map[int]bool
	count int
}

func (w *walker) paint(o, depth int) {
	c := w.f.colr
	if depth > maxPaintDepth || o <= 0 || o >= len(c) || w.on[o] || w.count >= maxPaints {
		return
	}
	w.count++
	w.on[o] = true
	defer delete(w.on, o)
	be := binary.BigEndian
	// need reports whether n bytes from the paint's start are there.
	need := func(n int) bool { return o+n <= len(c) }
	u24 := func(k int) int { return int(uint32(c[o+k])<<16 | uint32(c[o+k+1])<<8 | uint32(c[o+k+2])) }
	sub := func(k int) int { // a paint or table by its Offset24 at k
		if v := u24(k); v != 0 {
			return o + v
		}
		return 0
	}
	fw := func(k int) float64 { return float64(int16(be.Uint16(c[o+k:]))) }
	f2 := func(k int) float64 { return float64(float64(int16(be.Uint16(c[o+k:]))) / (1 << 14)) }
	fixed := func(k int) float64 { return float64(int32(be.Uint32(c[o+k:]))) / (1 << 16) }
	switch format := c[o]; format {
	case 1: // PaintColrLayers
		if !need(6) || w.f.layerList == 0 {
			return
		}
		n, first := int(c[o+1]), int(be.Uint32(c[o+2:]))
		ll := w.f.layerList
		total := int(be.Uint32(c[ll:]))
		for i := first; i < first+n && i < total && ll+4+4*i+4 <= len(c); i++ {
			w.paint(ll+int(be.Uint32(c[ll+4+4*i:])), depth+1)
		}
	case 2, 3: // PaintSolid
		if !need(5) {
			return
		}
		s, ok := w.f.stop(be.Uint16(c[o+1:]), f2(3))
		if ok {
			w.p.Fill(&Brush{Kind: BrushSolid, Stops: []Stop{s}})
		}
	case 4, 5: // PaintLinearGradient
		if !need(16) {
			return
		}
		b := &Brush{Kind: BrushLinear, P: [3][2]float64{{fw(4), fw(6)}, {fw(8), fw(10)}, {fw(12), fw(14)}}}
		if w.f.colorLine(sub(1), format == 5, b) {
			w.p.Fill(b)
		}
	case 6, 7: // PaintRadialGradient
		if !need(16) {
			return
		}
		b := &Brush{Kind: BrushRadial, P: [3][2]float64{{fw(4), fw(6)}, {fw(10), fw(12)}}, R0: float64(be.Uint16(c[o+8:])), R1: float64(be.Uint16(c[o+14:]))}
		if w.f.colorLine(sub(1), format == 7, b) {
			w.p.Fill(b)
		}
	case 8, 9: // PaintSweepGradient
		if !need(12) {
			return
		}
		// Angles in half turns counter-clockwise, less one (so that a whole
		// turn fits: as HarfBuzz reads them).
		b := &Brush{Kind: BrushSweep, P: [3][2]float64{{fw(4), fw(6)}}, Start: float64((f2(8) + 1) * 180), End: float64((f2(10) + 1) * 180)}
		if w.f.colorLine(sub(1), format == 9, b) {
			w.p.Fill(b)
		}
	case 10: // PaintGlyph
		if !need(6) {
			return
		}
		w.p.Clip(be.Uint16(c[o+4:]))
		w.paint(sub(1), depth+1)
		w.p.Pop()
	case 11: // PaintColrGlyph
		if !need(3) {
			return
		}
		if po, ok := w.f.paintOf(be.Uint16(c[o+1:])); ok {
			if box, ok := w.f.clipBox(be.Uint16(c[o+1:])); ok {
				w.p.ClipBox(box[0], box[1], box[2], box[3])
				defer w.p.Pop()
			}
			w.paint(po, depth+1)
		}
	case 12, 13: // PaintTransform
		if !need(7) {
			return
		}
		t := sub(4)
		if t == 0 || t+24 > len(c) {
			return
		}
		k := t - o
		w.child(sub(1), depth, [6]float64{fixed(k), fixed(k + 4), fixed(k + 8), fixed(k + 12), fixed(k + 16), fixed(k + 20)})
	case 14, 15: // PaintTranslate
		if !need(8) {
			return
		}
		w.child(sub(1), depth, [6]float64{1, 0, 0, 1, fw(4), fw(6)})
	case 16, 17, 18, 19: // PaintScale, around a centre
		if !need(8) || format >= 18 && !need(12) {
			return
		}
		sx, sy := f2(4), f2(6)
		cx, cy := 0.0, 0.0
		if format >= 18 {
			cx, cy = fw(8), fw(10)
		}
		w.child(sub(1), depth, about([6]float64{sx, 0, 0, sy, 0, 0}, cx, cy))
	case 20, 21, 22, 23: // PaintScaleUniform, around a centre
		if !need(6) || format >= 22 && !need(10) {
			return
		}
		s := f2(4)
		cx, cy := 0.0, 0.0
		if format >= 22 {
			cx, cy = fw(6), fw(8)
		}
		w.child(sub(1), depth, about([6]float64{s, 0, 0, s, 0, 0}, cx, cy))
	case 24, 25, 26, 27: // PaintRotate, around a centre (half turns, counter-clockwise)
		if !need(6) || format >= 26 && !need(10) {
			return
		}
		s, co := trig.SinCosDeg(float64(f2(4) * 180))
		cx, cy := 0.0, 0.0
		if format >= 26 {
			cx, cy = fw(6), fw(8)
		}
		w.child(sub(1), depth, about([6]float64{co, s, -s, co, 0, 0}, cx, cy))
	case 28, 29, 30, 31: // PaintSkew, around a centre
		if !need(8) || format >= 30 && !need(12) {
			return
		}
		tan := func(halfTurns float64) float64 {
			s, co := trig.SinCosDeg(float64(halfTurns * 180))
			if co == 0 {
				return 0
			}
			return s / co
		}
		cx, cy := 0.0, 0.0
		if format >= 30 {
			cx, cy = fw(8), fw(10)
		}
		// As HarfBuzz: x' = x - tan(xSkew) y, y' = tan(ySkew) x + y.
		w.child(sub(1), depth, about([6]float64{1, tan(f2(6)), -tan(f2(4)), 1, 0, 0}, cx, cy))
	case 32: // PaintComposite
		if !need(8) {
			return
		}
		src, back := sub(1), sub(5)
		w.p.Composite(int(c[o+4]), func() { w.paint(back, depth+1) }, func() { w.paint(src, depth+1) })
	}
}

// child draws the paint at o through a transform.
func (w *walker) child(o, depth int, m [6]float64) {
	w.p.Transform(m)
	w.paint(o, depth+1)
	w.p.Pop()
}

// about is m about the point (cx, cy).
func about(m [6]float64, cx, cy float64) [6]float64 {
	if cx == 0 && cy == 0 {
		return m
	}
	// Moved to the origin, m, moved back.
	m[4] = cx - (float64(m[0]*cx) + float64(m[2]*cy)) + m[4]
	m[5] = cy - (float64(m[1]*cx) + float64(m[3]*cy)) + m[5]
	return m
}

// stop is a colour of the palette, or the text's (0xFFFF), with an alpha.
func (f *Font) stop(i uint16, alpha float64) (Stop, bool) {
	s := Stop{Alpha: alpha}
	switch {
	case i == 0xFFFF:
		s.Foreground = true
	case int(i) < len(f.palette):
		s.Color = f.palette[i]
	default:
		return s, false
	}
	return s, true
}

// colorLine reads the colour line at o (of variations: var) into b.
func (f *Font) colorLine(o int, isVar bool, b *Brush) bool {
	c := f.colr
	if o <= 0 || o+3 > len(c) {
		return false
	}
	be := binary.BigEndian
	b.Extend = int(c[o])
	if b.Extend > ExtendReflect {
		b.Extend = ExtendPad
	}
	n := int(be.Uint16(c[o+1:]))
	size := 6
	if isVar {
		size = 10
	}
	if n == 0 || o+3+size*n > len(c) {
		return false
	}
	b.Stops = make([]Stop, 0, n)
	for i := 0; i < n; i++ {
		r := c[o+3+size*i:]
		s, ok := f.stop(be.Uint16(r[2:]), float64(int16(be.Uint16(r[4:])))/(1<<14))
		if !ok {
			continue
		}
		s.Offset = float64(int16(be.Uint16(r))) / (1 << 14)
		b.Stops = append(b.Stops, s)
	}
	return len(b.Stops) > 0
}
