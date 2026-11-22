package svg

import (
	"image"
	"math"
	"strings"
)

// maxTile bounds the side, in pixels, of the tile a pattern is drawn in.
const maxTile = 2048

// tileSampler colours a shape with a pattern's tile, repeated: bilinear,
// premultiplied.
type tileSampler struct {
	tile   []uint8 // premultiplied RGBA
	tw, th int
	inv    matrix // device to tile pixels
	clamp  bool   // its edges held rather than repeated (a filter's result)
}

func (s *tileSampler) at(px, py float64) [4]uint8 {
	u, v := s.inv.apply(px, py)
	u, v = u-0.5, v-0.5
	x0, y0 := math.Floor(u), math.Floor(v)
	fx, fy := u-x0, v-y0
	wrap := func(i float64, n int) int {
		if s.clamp {
			return int(math.Max(0, math.Min(float64(n-1), i)))
		}
		k := int(math.Mod(i, float64(n)))
		if k < 0 {
			k += n
		}
		return k
	}
	var acc [4]float64
	for k := 0; k < 4; k++ {
		xi, yi := wrap(x0+float64(k%2), s.tw), wrap(y0+float64(k/2), s.th)
		wx, wy := 1-fx, 1-fy
		if k%2 == 1 {
			wx = fx
		}
		if k/2 == 1 {
			wy = fy
		}
		wgt := float64(wx * wy)
		p := s.tile[4*(yi*s.tw+xi):]
		for c := 0; c < 4; c++ {
			acc[c] += float64(float64(p[c]) * wgt)
		}
	}
	return [4]uint8{uint8(math.Round(acc[0])), uint8(math.Round(acc[1])), uint8(math.Round(acc[2])), uint8(math.Round(acc[3]))}
}

// pattern draws a pattern's tile for a shape and returns the shader that
// repeats it; nil when it draws nothing.
func (b *builder) pattern(p *node, opacity float64, st style, segs []seg, m matrix, vp viewport) *shader {
	if b.patterning[p] || len(b.patterning) >= 4 {
		return nil
	}
	b.patterning[p] = true
	defer delete(b.patterning, p)
	// Attributes, and the content, come from the patterns it refers to
	// when it has none of its own.
	chain := []*node{p}
	for k := p; len(chain) < 8; {
		next := b.href(k)
		if next == nil || next.name != "pattern" {
			break
		}
		cycle := false
		for _, c := range chain {
			cycle = cycle || c == next
		}
		if cycle {
			break
		}
		chain = append(chain, next)
		k = next
	}
	attr := func(name string) (string, bool) {
		for _, c := range chain {
			if v, ok := c.attrs[name]; ok {
				return v, true
			}
		}
		return "", false
	}
	content := p
	for _, c := range chain {
		if len(c.children) > 0 {
			content = c
			break
		}
	}
	bx, hasBox := segsBBox(segs)
	units := "objectBoundingBox"
	if v, ok := attr("patternUnits"); ok {
		units = strings.TrimSpace(v)
	}
	// The tile: x, y, width, height in the shape's user space.
	var tx, ty, tw, th float64
	if units == "userSpaceOnUse" {
		get := func(name string, axis int) float64 {
			v, _ := attr(name)
			f, _ := parseLength(v, st.fontSize, vp, axis)
			return f
		}
		tx, ty, tw, th = get("x", axisX), get("y", axisY), get("width", axisX), get("height", axisY)
	} else {
		if !hasBox || bx[2] <= 0 || bx[3] <= 0 {
			return nil
		}
		frac := func(name string) float64 {
			v, _ := attr(name)
			v = strings.TrimSpace(v)
			if strings.HasSuffix(v, "%") {
				f, _ := parseLength(strings.TrimSuffix(v, "%"), 0, vp, axisX)
				return f / 100
			}
			f, _ := parseLength(v, 0, vp, axisX)
			return f
		}
		tx, ty = bx[0]+float64(frac("x")*bx[2]), bx[1]+float64(frac("y")*bx[3])
		tw, th = float64(frac("width")*bx[2]), float64(frac("height")*bx[3])
	}
	if !(tw > 0) || !(th > 0) {
		return nil
	}
	pm := m
	if t, ok := chainTransform(chain, "patternTransform"); ok {
		pm = pm.mul(aboutOrigin(p, t, vp.w, vp.h, st.fontSize, vp, [2]float64{}))
	}
	// The tile's pixels: as many as it covers on the device.
	sc := maxScale(pm)
	pw := int(math.Min(maxTile, math.Max(1, math.Ceil(float64(tw*sc)))))
	ph := int(math.Min(maxTile, math.Max(1, math.Ceil(float64(th*sc)))))
	// The content's space to the tile's pixels.
	cm := matrix{float64(pw) / tw, 0, 0, float64(ph) / th, 0, 0}
	if vb, ok := viewBoxOf(attr); ok {
		par, _ := attr("preserveAspectRatio")
		cm = cm.mul(viewBoxTransform(vb, par, tw, th))
	} else if v, _ := attr("patternContentUnits"); strings.TrimSpace(v) == "objectBoundingBox" {
		if !hasBox || bx[2] <= 0 || bx[3] <= 0 {
			return nil
		}
		cm = cm.mul(matrix{bx[2], 0, 0, bx[3], 0, 0})
	}
	tileBytes := 4 * int64(pw) * int64(ph)
	if err := b.reserve(tileBytes); err != nil {
		b.err = err
		return nil
	}
	b.sc.imageBytes += tileBytes
	// The content, drawn as a scene of its own the size of the tile.
	outer, full := b.sc, b.full
	b.sc = &scene{w: pw, h: ph, points: outer.points, itemCount: outer.itemCount, charged: outer.charged, imageBytes: outer.imageBytes}
	b.full = image.Rect(0, 0, pw, ph)
	cst := computeStyle(content, initialStyle(), vp)
	b.children(content, cm, cst, viewport{tw, th}, nil)
	inner := b.sc
	b.sc, b.full = outer, full
	outer.points, outer.itemCount, outer.charged, outer.imageBytes = inner.points, inner.itemCount, inner.charged, inner.imageBytes
	if b.err != nil {
		return nil
	}
	tile := make([]uint8, tileBytes)
	if len(inner.items) > 0 {
		layers := layersNeeded(inner.items)
		need := int64(layers)*tileBytes + 4*int64(pw)*int64(ph)
		if err := b.reserve(need); err != nil {
			b.err = err
			return nil
		}
		r := &renderer{sc: inner, n: ph, cov: make([]uint8, pw*ph), ccov: make([]uint8, pw*ph), tmp: make([]uint8, pw*ph), mcov: make([]uint8, pw*ph)}
		for i := 0; i < layers; i++ {
			r.pool = append(r.pool, make([]uint8, tileBytes))
		}
		copy(tile, r.band(0, ph))
		b.release(need)
	}
	// Device pixels to the tile's: through the pattern's space, the tile
	// at its origin.
	toTile := matrix{float64(pw) / tw, 0, 0, float64(ph) / th, 0, 0}.mul(matrix{1, 0, 0, 1, -tx, -ty})
	inv, ok := pm.invert()
	if !ok {
		return nil
	}
	sh := &shader{tile: &tileSampler{tile: tile, tw: pw, th: ph, inv: toTile.mul(inv)}}
	if opacity < 1 {
		sh.opacity = opacity
	}
	return sh
}

// viewBoxOf reads a viewBox from attributes looked up by attr.
func viewBoxOf(attr func(string) (string, bool)) ([4]float64, bool) {
	s, ok := attr("viewBox")
	if !ok {
		return [4]float64{}, false
	}
	v, ok := numbers(s)
	if !ok || len(v) != 4 || !(v[2] > 0) || !(v[3] > 0) {
		return [4]float64{}, false
	}
	return [4]float64{v[0], v[1], v[2], v[3]}, true
}
