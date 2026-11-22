package svg

import (
	"image/color"
	"math"
	"sort"

	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"

	"github.com/whhhh1500/goimgtools/internal/colorfont"
	"github.com/whhhh1500/goimgtools/internal/raster"
	"github.com/whhhh1500/goimgtools/internal/textsrc"
)

// colrGlyph draws glyph gi of f, a colour glyph of COLR version 1, by its
// paint graph (as Chrome draws them); m maps the font's units (y up) to
// the device. The foreground colour is the text's fill.
func (b *builder) colrGlyph(n *node, cf *colorfont.Font, f *textsrc.Font, gi sfnt.GlyphIndex, m matrix, st style, own, outer *clip) {
	fg := color.NRGBA{0, 0, 0, 255}
	switch st.fill.kind {
	case paintColor:
		fg = st.fill.c
	case paintCurrent:
		fg = st.color
	}
	b.group(n, st, own, outer, func(c *clip) {
		p := &colrPainter{b: b, n: n, f: f, fg: fg, opacity: st.fillOpacity, ms: []matrix{m}, outer: c}
		cf.Paint(uint16(gi), p)
	})
}

// colrPainter draws a paint graph into the scene: an outline or a clip
// box clips what it holds, a brush fills what is clipped (its gradients
// those of SVG, their colours interpolated premultiplied as COLR has
// them; sweeps a shader of their own), a composite is a group of its
// blend mode.
type colrPainter struct {
	b       *builder
	n       *node
	f       *textsrc.Font
	fg      color.NRGBA // the foreground colour
	opacity float64     // the text's fill opacity
	ms      []matrix    // the font's units to the device, innermost last
	clips   []*raster.Path
	kinds   []bool // what each push was: true a clip, false a transform
	outer   *clip  // the clip of what holds it
	buf     sfnt.Buffer
}

func (p *colrPainter) m() matrix { return p.ms[len(p.ms)-1] }

func (p *colrPainter) Transform(t [6]float64) {
	p.ms = append(p.ms, p.m().mul(matrix{t[0], t[1], t[2], t[3], t[4], t[5]}))
	p.kinds = append(p.kinds, false)
}

func (p *colrPainter) Clip(g uint16) { p.clip(p.glyphSegs(g)) }

func (p *colrPainter) ClipBox(x0, y0, x1, y1 float64) {
	p.clip(rectSegs(x0, y0, x1-x0, y1-y0, 0, 0))
}

func (p *colrPainter) clip(segs []seg) {
	m := p.m()
	p.clips = append(p.clips, fillPath(flatten(segs, m), m, false))
	p.kinds = append(p.kinds, true)
}

func (p *colrPainter) Pop() {
	if len(p.kinds) == 0 {
		return
	}
	if p.kinds[len(p.kinds)-1] {
		p.clips = p.clips[:len(p.clips)-1]
	} else {
		p.ms = p.ms[:len(p.ms)-1]
	}
	p.kinds = p.kinds[:len(p.kinds)-1]
}

func (p *colrPainter) Fill(br *colorfont.Brush) {
	if len(p.clips) == 0 || p.b.err != nil {
		return // nothing bounds it
	}
	sh := p.shader(br)
	if sh == nil {
		return
	}
	path := p.clips[len(p.clips)-1]
	bounds := pathBounds(path, p.b.full)
	var cl *clip
	for _, c := range p.clips[:len(p.clips)-1] {
		cb := pathBounds(c, p.b.full)
		cl = &clip{paths: []*raster.Path{c}, bounds: cb, parent: cl}
		bounds = bounds.Intersect(cb)
	}
	cl = chainClip(cl, p.outer)
	if p.outer != nil {
		bounds = bounds.Intersect(p.outer.bounds)
	}
	if bounds.Empty() {
		return
	}
	p.b.add(item{op: opShape, path: path, paint: sh, bounds: bounds, clip: cl})
}

func (p *colrPainter) Composite(mode int, backdrop, source func()) {
	switch mode {
	case colorfont.CompositeClear:
		return
	case colorfont.CompositeDest:
		backdrop()
		return
	case colorfont.CompositeSrc:
		source()
		return
	}
	// The other modes of Porter and Duff are drawn as source over.
	blend := map[int]int{
		colorfont.CompositeScreen: blendScreen, colorfont.CompositeOverlay: blendOverlay, colorfont.CompositeDarken: blendDarken,
		colorfont.CompositeLighten: blendLighten, colorfont.CompositeColorDodge: blendColorDodge, colorfont.CompositeColorBurn: blendColorBurn,
		colorfont.CompositeHardLight: blendHardLight, colorfont.CompositeSoftLight: blendSoftLight, colorfont.CompositeDifference: blendDifference,
		colorfont.CompositeExclusion: blendExclusion, colorfont.CompositeMultiply: blendMultiply, colorfont.CompositeHue: blendHue,
		colorfont.CompositeSaturation: blendSaturation, colorfont.CompositeColor: blendColor, colorfont.CompositeLuminosity: blendLuminosity,
		colorfont.CompositePlus: blendPlusLighter,
	}[mode]
	outer := p.outer
	defer func() { p.outer = outer }()
	p.b.group(p.n, style{opacity: 1, isolate: true}, nil, outer, func(c *clip) {
		p.outer = c
		backdrop()
		p.b.group(p.n, style{opacity: 1, blend: blend}, nil, c, func(c2 *clip) {
			p.outer = c2
			source()
		})
	})
}

// glyphSegs is the outline of glyph g, in font units, y up.
func (p *colrPainter) glyphSegs(g uint16) []seg {
	sf := p.f.SFNT()
	gs, err := sf.LoadGlyph(&p.buf, sfnt.GlyphIndex(g), fixed.Int26_6(sf.UnitsPerEm())<<6, nil)
	if err != nil {
		return nil
	}
	pt := func(q fixed.Point26_6) [2]float64 { return [2]float64{float64(q.X) / 64, -float64(q.Y) / 64} }
	var segs []seg
	for _, s := range gs {
		switch s.Op {
		case sfnt.SegmentOpMoveTo:
			segs = append(segs, seg{cmd: cmdMove, pts: [3][2]float64{pt(s.Args[0])}})
		case sfnt.SegmentOpLineTo:
			segs = append(segs, seg{cmd: cmdLine, pts: [3][2]float64{pt(s.Args[0])}})
		case sfnt.SegmentOpQuadTo:
			segs = append(segs, seg{cmd: cmdQuad, pts: [3][2]float64{pt(s.Args[0]), pt(s.Args[1])}})
		case sfnt.SegmentOpCubeTo:
			segs = append(segs, seg{cmd: cmdCubic, pts: [3][2]float64{pt(s.Args[0]), pt(s.Args[1]), pt(s.Args[2])}})
		}
	}
	return segs
}

// premulStop is a stop's colour premultiplied, on 0 to 255.
func (p *colrPainter) premulStop(s colorfont.Stop) [4]float64 {
	c := s.Color
	if s.Foreground {
		c = p.fg
	}
	a := float64(float64(float64(c.A)*math.Max(0, math.Min(1, s.Alpha))) * p.opacity)
	return [4]float64{float64(float64(c.R) * a / 255), float64(float64(c.G) * a / 255), float64(float64(c.B) * a / 255), a}
}

// shader is the shader of a brush, in the current transform.
func (p *colrPainter) shader(br *colorfont.Brush) *shader {
	round := func(c [4]float64) [4]uint8 {
		return [4]uint8{uint8(math.Round(c[0])), uint8(math.Round(c[1])), uint8(math.Round(c[2])), uint8(math.Round(c[3]))}
	}
	if br.Kind == colorfont.BrushSolid {
		c := p.premulStop(br.Stops[0])
		if c[3] <= 0 {
			return nil
		}
		return &shader{solid: round(c)}
	}
	stops := append([]colorfont.Stop(nil), br.Stops...)
	sort.SliceStable(stops, func(i, j int) bool { return stops[i].Offset < stops[j].Offset })
	lo, hi := stops[0].Offset, stops[len(stops)-1].Offset
	if hi <= lo {
		return &shader{solid: round(p.premulStop(stops[len(stops)-1]))}
	}
	inv, ok := p.m().invert()
	if !ok {
		return nil
	}
	g := &gradient{inv: inv, spread: [...]int{spreadPad, spreadRepeat, spreadReflect}[br.Extend]}
	// The stops brought to 0 to 1, the geometry with them.
	lerp := func(a, b, t float64) float64 { return a + float64((b-a)*t) }
	switch br.Kind {
	case colorfont.BrushLinear:
		p0, p1, p2 := br.P[0], br.P[1], br.P[2]
		// p3: p1 brought onto the line through p0 at right angles to
		// p0p2; the gradient runs from p0 to it.
		p3 := p1
		if vx, vy := p2[0]-p0[0], p2[1]-p0[1]; vx != 0 || vy != 0 {
			dx, dy := -vy, vx
			k := (float64((p1[0]-p0[0])*dx) + float64((p1[1]-p0[1])*dy)) / (float64(dx*dx) + float64(dy*dy))
			p3 = [2]float64{p0[0] + float64(dx*k), p0[1] + float64(dy*k)}
		}
		s := [2]float64{lerp(p0[0], p3[0], lo), lerp(p0[1], p3[1], lo)}
		e := [2]float64{lerp(p0[0], p3[0], hi), lerp(p0[1], p3[1], hi)}
		g.x1, g.y1, g.dx, g.dy = s[0], s[1], e[0]-s[0], e[1]-s[1]
		g.len2 = float64(g.dx*g.dx) + float64(g.dy*g.dy)
		if g.len2 == 0 {
			return nil
		}
	case colorfont.BrushRadial:
		c0, c1 := br.P[0], br.P[1]
		g.radial = true
		g.fx, g.fy, g.fr = lerp(c0[0], c1[0], lo), lerp(c0[1], c1[1], lo), math.Max(0, lerp(br.R0, br.R1, lo))
		g.cx, g.cy, g.r = lerp(c0[0], c1[0], hi), lerp(c0[1], c1[1], hi), math.Max(0, lerp(br.R0, br.R1, hi))
	case colorfont.BrushSweep:
		g.sweep = true
		g.cx, g.cy = br.P[0][0], br.P[0][1]
		g.start, g.end = lerp(br.Start, br.End, lo), lerp(br.Start, br.End, hi)
	default:
		return nil
	}
	// The colours along it, interpolated premultiplied.
	k := 0
	for i := range g.lut {
		t := lerp(lo, hi, float64(i)/(lutSize-1))
		for k+1 < len(stops) && stops[k+1].Offset < t {
			k++
		}
		var c [4]float64
		switch {
		case t <= stops[0].Offset:
			c = p.premulStop(stops[0])
		case k+1 >= len(stops):
			c = p.premulStop(stops[len(stops)-1])
		default:
			s0, s1 := p.premulStop(stops[k]), p.premulStop(stops[k+1])
			f := 0.0
			if d := stops[k+1].Offset - stops[k].Offset; d > 0 {
				f = (t - stops[k].Offset) / d
			}
			for j := range c {
				c[j] = lerp(s0[j], s1[j], f)
			}
		}
		g.lut[i] = round(c)
	}
	return &shader{grad: g}
}
