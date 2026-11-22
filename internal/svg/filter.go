package svg

import (
	"image"
	"math"
	"strconv"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/csscolor"
	"github.com/whhhh1500/goimgtools/internal/raster"
	"github.com/whhhh1500/goimgtools/internal/trig"
)

// The filter effects drawn: feGaussianBlur, feOffset, feFlood, feComposite,
// feMerge, feColorMatrix, feBlend, feDropShadow and feComponentTransfer,
// in linear light or sRGB as color-interpolation-filters says. An element
// with a filter is drawn into a buffer the size of the filter's region,
// the filter applied, and the result put into the scene as a picture.

// fbuf is a picture of the filter region: premultiplied RGBA, 0 to 1.
type fbuf []float32

// maxFilterPixels bounds a filter's region.
const maxFilterPixels = 16 << 20

// filterRef is the filter element an element refers to, or nil.
func (b *builder) filterRef(n *node) *node {
	v, ok := n.prop("filter")
	if !ok {
		return nil
	}
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "url(") {
		return nil
	}
	end := strings.IndexByte(v, ')')
	if end < 0 {
		return nil
	}
	f := b.ids[strings.TrimPrefix(strings.Trim(strings.TrimSpace(v[4:end]), `"'`), "#")]
	if f == nil || f.name != "filter" {
		return nil
	}
	return f
}

// filtered draws an element through its filter.
func (b *builder) filtered(n *node, f *node, m matrix, st style, vp viewport, own, outer *clip) {
	if b.filtering[f] {
		return
	}
	b.filtering[f] = true
	defer delete(b.filtering, f)
	bx, hasBox := b.bboxOf(n, vp, st)
	bboxUnits := f.attrs["filterUnits"] != "userSpaceOnUse"
	primBox := f.attrs["primitiveUnits"] == "objectBoundingBox"
	if !hasBox && (n.name == "text" || n.name == "image" || n.name == "g" || n.name == "use" || n.name == "svg" || n.name == "a") {
		// A box not worked out here (that of text): the whole picture,
		// in the element's user space.
		if inv, ok := m.invert(); ok {
			x0, y0 := inv.apply(0, 0)
			x1, y1 := inv.apply(float64(b.full.Dx()), float64(b.full.Dy()))
			bx = [4]float64{math.Min(x0, x1), math.Min(y0, y1), math.Abs(x1 - x0), math.Abs(y1 - y0)}
			hasBox = true
		}
	}
	if (bboxUnits || primBox) && (!hasBox || bx[2] <= 0 || bx[3] <= 0) {
		return // a filter on nothing draws nothing
	}
	frac := func(name, def string) float64 {
		s := strings.TrimSpace(attrOr(f, name, def))
		if strings.HasSuffix(s, "%") {
			v, _ := strconv.ParseFloat(strings.TrimSuffix(s, "%"), 64)
			return v / 100
		}
		v, _ := strconv.ParseFloat(s, 64)
		return v
	}
	var rx, ry, rw, rh float64
	if bboxUnits {
		rx, ry = bx[0]+float64(frac("x", "-10%")*bx[2]), bx[1]+float64(frac("y", "-10%")*bx[3])
		rw, rh = float64(frac("width", "120%")*bx[2]), float64(frac("height", "120%")*bx[3])
	} else {
		rx, _ = parseLength(attrOr(f, "x", "-10%"), st.fontSize, vp, axisX)
		ry, _ = parseLength(attrOr(f, "y", "-10%"), st.fontSize, vp, axisY)
		rw, _ = parseLength(attrOr(f, "width", "120%"), st.fontSize, vp, axisX)
		rh, _ = parseLength(attrOr(f, "height", "120%"), st.fontSize, vp, axisY)
	}
	if !(rw > 0) || !(rh > 0) {
		return
	}
	// The region on the device: the box round its corners, within the
	// picture.
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, c := range [][2]float64{{rx, ry}, {rx + rw, ry}, {rx, ry + rh}, {rx + rw, ry + rh}} {
		x, y := m.apply(c[0], c[1])
		minX, maxX = math.Min(minX, x), math.Max(maxX, x)
		minY, maxY = math.Min(minY, y), math.Max(maxY, y)
	}
	reg := image.Rect(int(math.Floor(minX)), int(math.Floor(minY)), int(math.Ceil(maxX)), int(math.Ceil(maxY))).Intersect(b.full)
	if reg.Empty() {
		return
	}
	w, h := reg.Dx(), reg.Dy()
	// The primitives are worked a band of rows at a time, each band with
	// as many rows above and below as they reach (all of them summed): only
	// the element's picture, those of feImage and the result are held
	// whole, as bytes. The region is worked at a lower resolution only when
	// even a band of a few rows would not fit in half the memory left; and
	// whole when a primitive needs it all (feTile, a convolution that wraps).
	sx, sy := 1.0, 1.0
	bufs := filterBufs(f)
	images := 0
	for _, p := range f.children {
		if p.name == "feImage" {
			images++
		}
	}
	bufs += int64(images)
	unit := [2]float64{1, 1}
	if primBox {
		unit = [2]float64{bx[2], bx[3]}
	}
	halo0, haloPx := filterHalo(f, m, unit)
	// plan is the band height for a region of w by h at scale k, and
	// whether it fits.
	plan := func(w, h int, k float64) (int, bool) {
		px := int64(w) * int64(h)
		if b.avail == nil {
			return h, true
		}
		room := b.avail() / 2
		fixed := 4 * px * int64(2+images)
		perRow := int64(w) * (16*bufs + 32)
		if halo0 < 0 {
			return h, fixed+int64(h)*perRow <= room
		}
		halo := int(math.Ceil(float64(halo0)*k)) + haloPx + 1
		if fixed+int64(h)*perRow <= room {
			return h, true
		}
		rows := (room-fixed)/perRow - 2*int64(halo)
		if rows >= int64(max(16, halo)) {
			return int(min(int64(h), rows)), true
		}
		return 0, false
	}
	bandH, ok := plan(w, h, 1)
	// Smaller, a tenth at a time (no more than the largest region allows),
	// until a band fits.
	for k := 1.0; (!ok || int64(w)*int64(h) > maxFilterPixels) && k > 1e-3; {
		if k == 1 {
			k = 0.95
			if a := int64(reg.Dx()) * int64(reg.Dy()); a > maxFilterPixels {
				k = math.Min(k, math.Sqrt(float64(maxFilterPixels)/float64(a)))
			}
		} else {
			k = float64(k * 0.9)
		}
		nw, nh := max(1, int(float64(float64(reg.Dx())*k))), max(1, int(float64(float64(reg.Dy())*k)))
		sx, sy = float64(nw)/float64(reg.Dx()), float64(nh)/float64(reg.Dy())
		w, h = nw, nh
		bandH, ok = plan(w, h, k)
	}
	if !ok || int64(w)*int64(h) > maxFilterPixels {
		b.err = errTooMuch
		return
	}
	halo := 0
	if bandH < h {
		halo = int(math.Ceil(float64(halo0)*sy)) + haloPx + 1
	}
	// The region's pixels from the device's.
	toRegion := matrix{sx, 0, 0, sy, 0, 0}.mul(matrix{1, 0, 0, 1, -float64(reg.Min.X), -float64(reg.Min.Y)})
	// The element alone, into the region, as a scene of its own.
	inner := st
	inner.opacity = 1
	cm := toRegion.mul(m)
	px := int64(w) * int64(h)
	src, err := b.renderRegion(w, h, func() { b.draw(n, cm, inner, vp, nil, nil) })
	if err != nil {
		b.err = err
		return
	}
	defer b.release(4 * px) // src
	linear := true
	if v, ok := f.prop("color-interpolation-filters"); ok && strings.TrimSpace(v) == "sRGB" {
		linear = false
	}
	// Worked at a lower resolution, a filter that puts the element itself
	// on top (a drop shadow) leaves it out, to draw it after, sharp. Drawn
	// after, it is laid on in sRGB: in linear light only an element of
	// opaque paint, which shows the difference at its edges alone.
	under := (sx < 1 || sy < 1) && sourceOnTop(f) && (!linear || paintsOpaque(n, inner, vp, 0))
	// The pictures of feImage: a picture of a data URI in the subregion, or
	// an element of the document, drawn as the element is.
	pictures := map[*node][]uint8{}
	defer func() { b.release(4 * px * int64(len(pictures))) }()
	for _, p := range f.children {
		if p.name != "feImage" {
			continue
		}
		href, ok := p.attr("href")
		if !ok {
			href, _ = p.attr("xlink:href")
		}
		href = strings.TrimSpace(href)
		var draw func()
		if strings.HasPrefix(href, "#") {
			t := b.ids[href[1:]]
			if t == nil {
				continue
			}
			draw = func() { b.element(t, cm, inner, vp, nil) }
		} else if strings.HasPrefix(href, "data:") {
			// The subregion in user space; of primitive units of the
			// bounding box, fractions (or percentages) of it.
			coord := func(name string, def float64, axis int, origin, size float64) float64 {
				v := strings.TrimSpace(p.attrs[name])
				if v == "" {
					return def
				}
				if primBox {
					pct := strings.HasSuffix(v, "%")
					f, ok := finiteNum(strings.TrimSuffix(v, "%"))
					if !ok {
						return def
					}
					if pct {
						f /= 100
					}
					return origin + float64(f*size)
				}
				if l, ok := parseLength(v, st.fontSize, vp, axis); ok {
					return l
				}
				return def
			}
			x, y := coord("x", rx, axisX, bx[0], bx[2]), coord("y", ry, axisY, bx[1], bx[3])
			iw, ih := coord("width", rw, axisX, 0, bx[2]), coord("height", rh, axisY, 0, bx[3])
			attrs := map[string]string{"x": strconv.FormatFloat(x, 'g', -1, 64), "y": strconv.FormatFloat(y, 'g', -1, 64),
				"width": strconv.FormatFloat(iw, 'g', -1, 64), "height": strconv.FormatFloat(ih, 'g', -1, 64), "href": href}
			if v, ok := p.attrs["preserveAspectRatio"]; ok {
				attrs["preserveAspectRatio"] = v
			}
			img := &node{name: "image", attrs: attrs, parent: p}
			draw = func() { b.image(img, cm, inner, vp, nil, nil) }
		} else {
			continue
		}
		pic, err := b.renderRegion(w, h, draw)
		if err != nil {
			b.err = err
			return
		}
		pictures[p] = pic
	}
	// The result, as bytes, made a band at a time.
	if err := b.reserve(4 * px); err != nil {
		b.err = err
		return
	}
	b.sc.imageBytes += 4 * px
	res := make([]uint8, 4*px)
	for y0 := 0; y0 < h; y0 += bandH {
		top, bot := max(0, y0-halo), min(h, y0+bandH+halo)
		bh := bot - top
		// The primitives on pictures of floats, rows top to bot.
		fc := &fctx{w: w, h: bh, m: matrix{sx, 0, 0, sy, 0, 0}.mul(m), results: map[string]fbuf{}, reserve: b.reserve, release: b.release,
			full: matrix{1, 0, 0, 1, 0, -float64(top)}.mul(cm), region: [4]float64{rx, ry, rw, rh}, bbox: bx, primBox: primBox, fontSize: st.fontSize, vp: vp, work: [2]float64{sx, sy},
			whole: image.Rect(0, -top, w, h-top), rects: map[string]image.Rectangle{}, images: map[*node]fbuf{},
			linear: linear, under: under, unit: unit}
		fc.lastRect = fc.whole
		err := fc.begin(src[4*w*top : 4*w*bot])
		for p, pic := range pictures {
			if err != nil {
				break
			}
			fc.images[p], err = fc.fromBytes(pic[4*w*top : 4*w*bot])
		}
		var out fbuf
		if err == nil {
			out, err = fc.run(f, vp, st)
		}
		if err != nil {
			fc.end()
			b.err = err
			return
		}
		rows := fc.finish(out)
		n := min(bandH, h-y0)
		copy(res[4*w*y0:4*w*(y0+n)], rows[4*w*(y0-top):4*w*(y0-top+n)])
		fc.end()
	}
	fcUnder := under
	// The result, as a picture at the region's place.
	area := fillPath(flatten(rectSegs(float64(reg.Min.X), float64(reg.Min.Y), float64(reg.Dx()), float64(reg.Dy()), 0, 0), identity), identity, false)
	sh := &shader{tile: &tileSampler{tile: res, tw: w, th: h, inv: toRegion, clamp: true}}
	if fcUnder {
		// The result, then the element over it within the region, as a
		// group of the element's opacity and clip.
		b.group(n, st, own, outer, func(c *clip) {
			it := item{op: opShape, path: area, paint: sh, bounds: pathBounds(area, b.full), clip: c}
			if c != nil {
				it.bounds = it.bounds.Intersect(c.bounds)
			}
			b.add(it)
			regClip := &clip{paths: []*raster.Path{area}, bounds: pathBounds(area, b.full)}
			b.draw(n, m, inner, vp, regClip, c)
		})
		return
	}
	if st.opacity < 1 {
		sh.opacity = st.opacity
	}
	it := item{op: opShape, path: area, paint: sh, bounds: pathBounds(area, b.full)}
	c := chainClip(own, outer)
	it.clip = c
	if c != nil {
		it.bounds = it.bounds.Intersect(c.bounds)
	}
	b.add(it)
}

// renderRegion draws what draw adds into a scene of its own, w by h, and
// returns its pixels, 4 w h bytes reserved for the caller to release.
func (b *builder) renderRegion(w, h int, draw func()) ([]uint8, error) {
	outerSc, full := b.sc, b.full
	b.sc = &scene{w: w, h: h, points: outerSc.points, itemCount: outerSc.itemCount, charged: outerSc.charged, imageBytes: outerSc.imageBytes}
	b.full = image.Rect(0, 0, w, h)
	draw()
	sub := b.sc
	b.sc, b.full = outerSc, full
	outerSc.points, outerSc.itemCount, outerSc.charged, outerSc.imageBytes = sub.points, sub.itemCount, sub.charged, sub.imageBytes
	if b.err != nil {
		return nil, b.err
	}
	px := int64(w) * int64(h)
	if err := b.reserve(4 * px); err != nil {
		return nil, err
	}
	pic := make([]uint8, 4*px)
	if len(sub.items) > 0 {
		// Drawn a band of rows at a time, as many as a quarter of the
		// memory left holds (a pixel's coverage is the same in any band).
		layers := layersNeeded(sub.items)
		perRow := int64(w) * (4*int64(layers) + 4)
		rows := h
		if b.avail != nil {
			if room := b.avail() / 4; room/perRow < int64(h) {
				rows = int(max(1, room/perRow))
			}
		}
		need := perRow * int64(rows)
		if err := b.reserve(need); err != nil {
			b.release(4 * px) // pic
			return nil, err
		}
		n := w * rows
		r := &renderer{sc: sub, n: rows, cov: make([]uint8, n), ccov: make([]uint8, n), tmp: make([]uint8, n), mcov: make([]uint8, n)}
		for i := 0; i < layers; i++ {
			r.pool = append(r.pool, make([]uint8, 4*n))
		}
		for y := 0; y < h; y += rows {
			k := min(rows, h-y)
			copy(pic[4*w*y:], r.band(y, k))
		}
		b.release(need)
	}
	return pic, nil
}

// fctx runs a filter's primitives.
type fctx struct {
	w, h     int
	m        matrix // user space to the device, for lengths and offsets
	unit     [2]float64
	linear   bool
	source   fbuf
	last     fbuf
	results  map[string]fbuf
	reserve  func(int64) error
	release  func(int64)
	reserved int64
	owned    map[*float32]bool // the pictures made, by their first value
	under    bool              // the last primitive leaves out the source it puts on top
	// For primitives placed in user space: the transform from it to the
	// region's pixels, the filter region (x, y, width, height), the
	// element's bounding box, whether primitive units are fractions of it,
	// the font size and viewport for lengths.
	full     matrix
	region   [4]float64
	bbox     [4]float64
	primBox  bool
	fontSize float64
	vp       viewport
	work     [2]float64                 // the region's pixels to the device's, across and down (1 at full resolution)
	whole    image.Rectangle            // the filter region in the pixels of this band
	rects    map[string]image.Rectangle // the subregions of the results
	lastRect image.Rectangle            // and of the last one
	images   map[*node]fbuf             // the pictures of feImage
}

// sourceOnTop reports whether the last primitive of a filter puts the
// element itself (SourceGraphic) over the rest, as drop shadows do.
func sourceOnTop(f *node) bool {
	var last *node
	count := 0
	for _, p := range f.children {
		if drawnPrimitive(p.name) {
			last = p
			count++
		}
	}
	if last == nil {
		return false
	}
	isSource := func(name string) bool {
		name = strings.TrimSpace(name)
		return name == "SourceGraphic" || name == "" && count == 1
	}
	switch last.name {
	case "feMerge":
		var top *node
		for _, mn := range last.children {
			if mn.name == "feMergeNode" {
				top = mn
			}
		}
		return top != nil && isSource(top.attrs["in"])
	case "feBlend":
		mode := strings.TrimSpace(last.attrs["mode"])
		return (mode == "" || mode == "normal") && isSource(last.attrs["in"])
	case "feComposite":
		op := strings.TrimSpace(last.attrs["operator"])
		return (op == "" || op == "over") && isSource(last.attrs["in"])
	case "feDropShadow":
		return isSource(last.attrs["in"])
	}
	return false
}

// paintsOpaque reports whether what n draws is all of opaque colours: no
// opacity, no fill or stroke opacity, no colour with alpha, nothing
// painted by a gradient or a pattern, no picture.
func paintsOpaque(n *node, st style, vp viewport, depth int) bool {
	if depth > 16 || st.opacity < 1 {
		return false
	}
	switch n.name {
	case "g", "a", "switch", "svg":
		for _, k := range n.children {
			if !strings.HasPrefix(k.name, "#") && !paintsOpaque(k, computeStyle(k, st, vp), vp, depth+1) {
				return false
			}
		}
		return true
	case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon", "text", "tspan":
		for _, p := range []struct {
			p       paint
			opacity float64
		}{{st.fill, st.fillOpacity}, {st.stroke, st.strokeOpacity}} {
			switch p.p.kind {
			case paintNone:
			case paintColor:
				if p.p.c.A < 255 || p.opacity < 1 {
					return false
				}
			case paintCurrent:
				if st.color.A < 255 || p.opacity < 1 {
					return false
				}
			default:
				return false
			}
		}
		for _, k := range n.children {
			if k.name == "tspan" && !paintsOpaque(k, computeStyle(k, st, vp), vp, depth+1) {
				return false
			}
		}
		return true
	case "title", "desc", "metadata", "defs", "style":
		return true
	}
	return false // a use, a picture, ...: not looked into
}

// drawnPrimitive reports whether run draws a primitive of that name.
func drawnPrimitive(name string) bool {
	switch name {
	case "feGaussianBlur", "feOffset", "feFlood", "feComposite", "feMerge", "feColorMatrix", "feBlend", "feDropShadow", "feComponentTransfer",
		"feTurbulence", "feMorphology", "feConvolveMatrix", "feDisplacementMap", "feTile", "feImage", "feDiffuseLighting", "feSpecularLighting":
		return true
	}
	return false
}

func (c *fctx) alloc() (fbuf, error) {
	n := int64(4 * c.w * c.h)
	if err := c.reserve(4 * n); err != nil {
		return nil, err
	}
	c.reserved += 4 * n
	b := make(fbuf, n)
	if c.owned == nil {
		c.owned = map[*float32]bool{}
	}
	if n > 0 {
		c.owned[&b[0]] = true
	}
	return b, nil
}

// freeAllBut gives back every picture made but those kept.
func (c *fctx) freeAllBut(keep ...fbuf) {
	held := map[*float32]bool{}
	for _, b := range keep {
		if len(b) > 0 {
			held[&b[0]] = true
		}
	}
	n := int64(16 * c.w * c.h)
	for p := range c.owned {
		if !held[p] {
			delete(c.owned, p)
			c.release(n)
			c.reserved -= n
		}
	}
}

func (c *fctx) end() { c.release(c.reserved); c.reserved = 0 }

// The sRGB curve and its inverse, by table (trig.Pow, the same on every
// processor).
var (
	toLinear  [256]float32
	linearLUT [4097]float32
)

func init() {
	for i := range toLinear {
		v := float64(i) / 255
		if v <= 0.04045 {
			v /= 12.92
		} else {
			v = trig.Pow((v+0.055)/1.055, 2.4)
		}
		toLinear[i] = float32(v)
	}
	for i := range linearLUT {
		v := float64(i) / 4096
		if v <= 0.0031308 {
			v *= 12.92
		} else {
			v = float64(1.055*trig.Pow(v, 1/2.4)) - 0.055
		}
		linearLUT[i] = float32(v)
	}
}

// fromLinear is a linear light in the sRGB curve, interpolated in the table.
func fromLinear(v float32) float32 {
	x := float32(v * 4096)
	i := int(x)
	if i >= 4096 {
		return linearLUT[4096]
	}
	f := x - float32(i)
	return linearLUT[i] + float32(f*(linearLUT[i+1]-linearLUT[i]))
}

// begin turns the drawn element into the source picture.
func (c *fctx) begin(src []uint8) error {
	b, err := c.fromBytes(src)
	if err != nil {
		return err
	}
	c.source, c.last = b, b
	return nil
}

// fromBytes turns premultiplied sRGB bytes into a picture of floats.
func (c *fctx) fromBytes(src []uint8) (fbuf, error) {
	b, err := c.alloc()
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(src); i += 4 {
		a := float32(src[i+3]) / 255
		b[i+3] = a
		if a == 0 {
			continue
		}
		for k := 0; k < 3; k++ {
			v := float32(src[i+k]) / 255 / a // unpremultiplied
			if c.linear {
				v = toLinear[min(255, int(float32(v*255)+0.5))]
			}
			b[i+k] = float32(min(v, 1) * a)
		}
	}
	return b, nil
}

// finish turns a result back into premultiplied sRGB bytes.
func (c *fctx) finish(b fbuf) []uint8 {
	out := make([]uint8, len(b))
	for i := 0; i < len(b); i += 4 {
		a := clampf(b[i+3])
		if a == 0 {
			continue
		}
		out[i+3] = uint8(float32(a*255) + 0.5)
		for k := 0; k < 3; k++ {
			v := clampf(b[i+k] / a)
			if c.linear {
				v = fromLinear(v)
			}
			out[i+k] = uint8(float32(float32(v*a)*255) + 0.5)
		}
	}
	return out
}

func clampf(v float32) float32 {
	if !(v > 0) {
		return 0
	}
	return min(v, 1)
}

// input is a primitive's input.
func (c *fctx) input(name string) (fbuf, error) {
	switch strings.TrimSpace(name) {
	case "":
		return c.last, nil
	case "SourceGraphic":
		return c.source, nil
	case "SourceAlpha":
		b, err := c.alloc()
		if err != nil {
			return nil, err
		}
		for i := 3; i < len(b); i += 4 {
			b[i] = c.source[i]
		}
		return b, nil
	}
	if r, ok := c.results[strings.TrimSpace(name)]; ok {
		return r, nil
	}
	// BackgroundImage, FillPaint and the like: nothing.
	return c.alloc()
}

// num reads a number of a primitive, scaled by the primitive units.
func (c *fctx) num(n *node, name string, def float64, axis int) float64 {
	v, ok := n.attrs[name]
	if !ok {
		return def
	}
	f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
	if err != nil {
		return def
	}
	return float64(f * c.unit[axis])
}

// run applies the primitives in order and returns the last result.
func (c *fctx) run(f *node, vp viewport, st style) (fbuf, error) {
	lastAt := -1
	for i, p := range f.children {
		if drawnPrimitive(p.name) {
			lastAt = i
		}
	}
	readers := lastReaders(f)
	outs := map[int]fbuf{} // the results still to be read
	for i, p := range f.children {
		var out fbuf
		var err error
		under := c.under && i == lastAt
		var r image.Rectangle
		if drawnPrimitive(p.name) {
			r = c.subregion(p, inputsOf(p))
		}
		band := image.Rect(0, 0, c.w, c.h)
		switch p.name {
		case "feGaussianBlur":
			out, err = c.blur(p)
		case "feOffset":
			out, err = c.offset(p)
		case "feFlood":
			out, err = c.flood(p, vp, st)
		case "feComposite":
			if under {
				out, err = c.input(p.attrs["in2"]) // what it goes over
			} else {
				out, err = c.composite(p)
			}
		case "feMerge":
			out, err = c.merge(p, under)
		case "feColorMatrix":
			out, err = c.colorMatrix(p)
		case "feBlend":
			if under {
				out, err = c.input(p.attrs["in2"])
			} else {
				out, err = c.blend(p)
			}
		case "feDropShadow":
			out, err = c.dropShadow(p, vp, st, under)
		case "feComponentTransfer":
			out, err = c.transfer(p)
		case "feTurbulence":
			out, err = c.turbulence(p, r)
		case "feMorphology":
			out, err = c.morphology(p)
		case "feConvolveMatrix":
			out, err = c.convolve(p)
		case "feDisplacementMap":
			out, err = c.displacement(p)
		case "feTile":
			out, err = c.tile(p, r)
		case "feImage":
			out, err = c.image(p)
		case "feDiffuseLighting":
			out, err = c.lighting(p, st, false)
		case "feSpecularLighting":
			out, err = c.lighting(p, st, true)
		default:
			continue // not drawn: the input passes on
		}
		if err != nil {
			return nil, err
		}
		if r.Intersect(band) != band {
			// Only its subregion: on a copy, the result may be an input's.
			cp, err := c.alloc()
			if err != nil {
				return nil, err
			}
			copy(cp, out)
			c.clip(cp, r)
			out = cp
		}
		c.last, c.lastRect = out, r
		if name := strings.TrimSpace(p.attrs["result"]); name != "" {
			c.results[name] = out
			c.rects[name] = r
		}
		// The pictures no primitive still to come reads are given back.
		outs[i] = out
		keep := []fbuf{c.source, out}
		for k, b := range outs {
			if readers[k] > i {
				keep = append(keep, b)
			} else if k != i {
				delete(outs, k)
			}
		}
		for _, b := range c.images {
			keep = append(keep, b)
		}
		c.freeAllBut(keep...)
		for name, b := range c.results {
			if len(b) > 0 && !c.owned[&b[0]] && &b[0] != &c.source[0] {
				delete(c.results, name) // read by none to come
			}
		}
	}
	return c.last, nil
}

// lastReaders gives, for each primitive of a filter (by its place among
// the children), the place of the last one that reads its result: by its
// name, or as the one before it (no in). The pictures of those read by none
// to come can go.
func lastReaders(f *node) map[int]int {
	last := map[int]int{}
	named := map[string]int{}
	prev := -1
	for i, p := range f.children {
		if !drawnPrimitive(p.name) {
			continue
		}
		for _, in := range inputsOf(p) {
			switch in = strings.TrimSpace(in); in {
			case "":
				if prev >= 0 {
					last[prev] = i
				}
			case "SourceGraphic", "SourceAlpha", "BackgroundImage", "BackgroundAlpha", "FillPaint", "StrokePaint":
			default:
				if k, ok := named[in]; ok {
					last[k] = i
				}
			}
		}
		if r := strings.TrimSpace(p.attrs["result"]); r != "" {
			named[r] = i
		}
		prev = i
	}
	return last
}

// filterBufs is how many pictures of floats a filter holds at once at
// most: the source, the results some primitive to come reads, and what one
// primitive makes for itself (three: a temporary, its result, the copy of
// a subregion).
func filterBufs(f *node) int64 {
	readers := lastReaders(f)
	most := 0
	var live []int
	for i, p := range f.children {
		if !drawnPrimitive(p.name) {
			continue
		}
		kept := live[:0]
		for _, k := range live {
			if readers[k] >= i {
				kept = append(kept, k)
			}
		}
		live = kept
		most = max(most, len(live))
		live = append(live, i)
	}
	return int64(most) + 4
}

// devScale is how much the transform stretches lengths along x and y.
func (c *fctx) devScale() (float64, float64) {
	return hypot(c.m.a, c.m.b), hypot(c.m.c, c.m.d)
}

func (c *fctx) blur(p *node) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	sd, _ := numbers(p.attrs["stdDeviation"])
	sx, sy := 0.0, 0.0
	if len(sd) > 0 {
		sx, sy = sd[0], sd[0]
	}
	if len(sd) > 1 {
		sy = sd[1]
	}
	kx, ky := c.devScale()
	return c.gauss(in, float64(sx*c.unit[0])*kx, float64(sy*c.unit[1])*ky)
}

// gauss blurs a picture by standard deviations dx and dy (device pixels):
// below 2, by the Gaussian itself; from 2, by three box blurs, as the
// specification describes, each way.
func (c *fctx) gauss(in fbuf, dx, dy float64) (fbuf, error) {
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	copy(out, in)
	w, h := c.w, c.h
	line := make([]float32, 4*max(w, h))
	res := make([]float32, 4*max(w, h))
	pass := func(d float64, n, lines int, at func(line, i int) int) {
		if !(d > 0) {
			return
		}
		for l := 0; l < lines; l++ {
			for i := 0; i < n; i++ {
				copy(line[4*i:4*i+4], out[at(l, i):at(l, i)+4])
			}
			blurLine(line[:4*n], res[:4*n], d)
			for i := 0; i < n; i++ {
				copy(out[at(l, i):at(l, i)+4], res[4*i:4*i+4])
			}
		}
	}
	pass(dx, w, h, func(y, x int) int { return 4 * (y*w + x) })
	pass(dy, h, w, func(x, y int) int { return 4 * (y*w + x) })
	// What the running sums leave where nothing is (a millionth or so)
	// is nothing: divided by an alpha of that, it would be a colour.
	for i, v := range out {
		if v < 1.0/65536 {
			out[i] = 0
		}
	}
	return out, nil
}

// blurLine blurs a line of premultiplied pixels into out, what lies
// beyond it transparent.
func blurLine(line, out []float32, d float64) {
	n := len(line) / 4
	if d < 2 {
		r := int(math.Ceil(float64(3 * d)))
		k := make([]float32, 2*r+1)
		var total float64
		for i := -r; i <= r; i++ {
			v := trig.Pow(math.E, -float64(float64(i*i))/float64(2*float64(d*d)))
			k[i+r] = float32(v)
			total += v
		}
		for i := range k {
			k[i] = float32(float64(k[i]) / total)
		}
		for i := 0; i < n; i++ {
			var acc [4]float32
			for j := -r; j <= r; j++ {
				if p := i + j; p >= 0 && p < n {
					for q := 0; q < 4; q++ {
						acc[q] += float32(k[j+r] * line[4*p+q])
					}
				}
			}
			copy(out[4*i:4*i+4], acc[:])
		}
		return
	}
	size := int(math.Floor(float64(d*3*math.Sqrt(2*math.Pi)/4) + 0.5))
	// The boxes, as pixels before and after: an even size is centred on
	// the boundaries either side, then one wider on the pixel.
	boxes := [][2]int{{size / 2, size / 2}, {size / 2, size / 2}, {size / 2, size / 2}}
	if size%2 == 0 {
		boxes = [][2]int{{size / 2, size/2 - 1}, {size/2 - 1, size / 2}, {size / 2, size / 2}}
	}
	src, dst := line, out
	for _, bx := range boxes {
		lo, hi := bx[0], bx[1]
		inv := 1 / float32(lo+hi+1)
		var sum [4]float32
		for j := 0; j <= hi && j < n; j++ {
			for q := 0; q < 4; q++ {
				sum[q] += src[4*j+q]
			}
		}
		for i := 0; i < n; i++ {
			for q := 0; q < 4; q++ {
				dst[4*i+q] = float32(sum[q] * inv)
			}
			if r := i - lo; r >= 0 {
				for q := 0; q < 4; q++ {
					sum[q] -= src[4*r+q]
				}
			}
			if a := i + hi + 1; a < n {
				for q := 0; q < 4; q++ {
					sum[q] += src[4*a+q]
				}
			}
		}
		src, dst = dst, src
	}
	if &src[0] != &out[0] {
		copy(out, src)
	}
}

func (c *fctx) offset(p *node) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	dx, dy := c.num(p, "dx", 0, 0), c.num(p, "dy", 0, 1)
	return c.shift(in, dx, dy)
}

// shift moves a picture by (dx, dy) of user space, rounded to whole pixels.
func (c *fctx) shift(in fbuf, dx, dy float64) (fbuf, error) {
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	ox := int(math.Round(float64(c.m.a*dx) + float64(c.m.c*dy)))
	oy := int(math.Round(float64(c.m.b*dx) + float64(c.m.d*dy)))
	for y := 0; y < c.h; y++ {
		sy := y - oy
		if sy < 0 || sy >= c.h {
			continue
		}
		for x := 0; x < c.w; x++ {
			sx := x - ox
			if sx < 0 || sx >= c.w {
				continue
			}
			copy(out[4*(y*c.w+x):4*(y*c.w+x)+4], in[4*(sy*c.w+sx):4*(sy*c.w+sx)+4])
		}
	}
	return out, nil
}

// floodColor is a primitive's flood-color with its opacity, premultiplied
// in the filter's colour space.
func (c *fctx) floodColor(p *node, prop, opProp string, st style) [4]float32 {
	col := [4]float32{0, 0, 0, 1}
	if v, ok := p.prop(prop); ok {
		if strings.EqualFold(strings.TrimSpace(v), "currentcolor") {
			col = [4]float32{float32(st.color.R) / 255, float32(st.color.G) / 255, float32(st.color.B) / 255, float32(st.color.A) / 255}
		} else if cc, ok := csscolor.Parse(v); ok {
			col = [4]float32{float32(cc.R) / 255, float32(cc.G) / 255, float32(cc.B) / 255, float32(cc.A) / 255}
		}
	}
	if v, ok := p.prop(opProp); ok {
		col[3] = float32(float64(col[3]) * opacityValue(v, 1))
	}
	for k := 0; k < 3; k++ {
		if c.linear {
			col[k] = toLinear[int(float32(col[k]*255)+0.5)]
		}
		col[k] = float32(col[k] * col[3])
	}
	return col
}

func (c *fctx) flood(p *node, vp viewport, st style) (fbuf, error) {
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	col := c.floodColor(p, "flood-color", "flood-opacity", st)
	for i := 0; i < len(out); i += 4 {
		copy(out[i:i+4], col[:])
	}
	return out, nil
}

func (c *fctx) composite(p *node) (fbuf, error) {
	a, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	b, err := c.input(p.attrs["in2"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	op := strings.TrimSpace(p.attrs["operator"])
	k := [4]float32{}
	for i, name := range []string{"k1", "k2", "k3", "k4"} {
		if v, err := strconv.ParseFloat(strings.TrimSpace(p.attrs[name]), 64); err == nil {
			k[i] = float32(v)
		}
	}
	for i := 0; i < len(out); i += 4 {
		aa, ba := a[i+3], b[i+3]
		for q := 0; q < 4; q++ {
			ca, cb := a[i+q], b[i+q]
			var v float32
			switch op {
			case "in":
				v = float32(ca * ba)
			case "out":
				v = float32(ca * (1 - ba))
			case "atop":
				v = float32(ca*ba) + float32(cb*(1-aa))
			case "xor":
				v = float32(ca*(1-ba)) + float32(cb*(1-aa))
			case "arithmetic":
				v = clampf(float32(float32(k[0]*ca)*cb) + float32(k[1]*ca) + float32(k[2]*cb) + k[3])
			case "lighter":
				v = min(1, ca+cb)
			default: // over
				v = ca + float32(cb*(1-aa))
			}
			out[i+q] = v
		}
		if op == "arithmetic" {
			// Colours stay within their alpha.
			for q := 0; q < 3; q++ {
				out[i+q] = min(out[i+q], out[i+3])
			}
		}
	}
	return out, nil
}

// merge puts its inputs over one another; under, all but the last.
func (c *fctx) merge(p *node, under bool) (fbuf, error) {
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	nodes := make([]*node, 0, len(p.children))
	for _, mn := range p.children {
		if mn.name == "feMergeNode" {
			nodes = append(nodes, mn)
		}
	}
	if under && len(nodes) > 0 {
		nodes = nodes[:len(nodes)-1]
	}
	for _, mn := range nodes {
		in, err := c.input(mn.attrs["in"])
		if err != nil {
			return nil, err
		}
		for i := 0; i < len(out); i += 4 {
			ia := 1 - in[i+3]
			for q := 0; q < 4; q++ {
				out[i+q] = in[i+q] + float32(out[i+q]*ia)
			}
		}
	}
	return out, nil
}

func (c *fctx) colorMatrix(p *node) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	vals, _ := numbers(p.attrs["values"])
	var mtx [20]float64
	switch strings.TrimSpace(p.attrs["type"]) {
	case "saturate":
		s := 1.0
		if len(vals) > 0 {
			s = vals[0]
		}
		f := func(k, ks float64) float64 { return k + float64(ks*s) }
		mtx = [20]float64{
			f(0.213, 0.787), f(0.715, -0.715), f(0.072, -0.072), 0, 0,
			f(0.213, -0.213), f(0.715, 0.285), f(0.072, -0.072), 0, 0,
			f(0.213, -0.213), f(0.715, -0.715), f(0.072, 0.928), 0, 0,
			0, 0, 0, 1, 0}
	case "hueRotate":
		a := 0.0
		if len(vals) > 0 {
			a = vals[0] * math.Pi / 180
		}
		sn, cs := trig.SinCos(a)
		f := func(k, kc, ks float64) float64 { return k + float64(kc*cs) + float64(ks*sn) }
		mtx = [20]float64{
			f(0.213, 0.787, -0.213), f(0.715, -0.715, -0.715), f(0.072, -0.072, 0.928), 0, 0,
			f(0.213, -0.213, 0.143), f(0.715, 0.285, 0.140), f(0.072, -0.072, -0.283), 0, 0,
			f(0.213, -0.213, -0.787), f(0.715, -0.715, 0.715), f(0.072, 0.928, 0.072), 0, 0,
			0, 0, 0, 1, 0}
	case "luminanceToAlpha":
		mtx = [20]float64{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0.2125, 0.7154, 0.0721, 0, 0}
	default:
		if len(vals) == 20 {
			copy(mtx[:], vals)
		} else {
			mtx = [20]float64{1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 1, 0}
		}
	}
	for i := 0; i < len(out); i += 4 {
		a := in[i+3]
		var u [4]float32
		if a > 0 {
			u = [4]float32{in[i] / a, in[i+1] / a, in[i+2] / a, a}
		}
		var o [4]float32
		for r := 0; r < 4; r++ {
			// Added up in float64: in float32 the luminance of a grey 0.5 is
			// 0.49999997, and a table of discrete steps takes the step
			// below (Figma's noise came out a quarter darker).
			v := float64(mtx[5*r]*float64(u[0])) + float64(mtx[5*r+1]*float64(u[1])) + float64(mtx[5*r+2]*float64(u[2])) + float64(mtx[5*r+3]*float64(u[3])) + mtx[5*r+4]
			o[r] = clampf(float32(v))
		}
		for q := 0; q < 3; q++ {
			out[i+q] = float32(o[q] * o[3])
		}
		out[i+3] = o[3]
	}
	return out, nil
}

func (c *fctx) blend(p *node) (fbuf, error) {
	a, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	b, err := c.input(p.attrs["in2"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	// The modes of SVG 1.1 as they were; those of CSS (SVG 2) by the
	// compositing spec.
	mode := strings.TrimSpace(p.attrs["mode"])
	m, ok := parseBlend(mode)
	if !ok || m == blendPlusLighter { // plus-lighter is mix-blend-mode's only
		m = blendNormal
	}
	for i := 0; i < len(out); i += 4 {
		qa, qb := a[i+3], b[i+3]
		switch m {
		case blendNormal, blendMultiply, blendScreen, blendDarken, blendLighten:
			for q := 0; q < 3; q++ {
				ca, cb := a[i+q], b[i+q]
				var v float32
				switch m {
				case blendMultiply:
					v = float32(ca*(1-qb)) + float32(cb*(1-qa)) + float32(ca*cb)
				case blendScreen:
					v = cb + ca - float32(cb*ca)
				case blendDarken:
					v = min(float32(ca*(1-qb))+cb, float32(cb*(1-qa))+ca)
				case blendLighten:
					v = max(float32(ca*(1-qb))+cb, float32(cb*(1-qa))+ca)
				default: // normal
					v = ca + float32(cb*(1-qa))
				}
				out[i+q] = v
			}
			out[i+3] = qa + qb - float32(qa*qb)
		default:
			o := blendPixel(m, [4]float32{a[i], a[i+1], a[i+2], qa}, [4]float32{b[i], b[i+1], b[i+2], qb})
			copy(out[i:i+4], o[:])
		}
	}
	return out, nil
}

// dropShadow puts the input over its shadow; under, the shadow alone.
func (c *fctx) dropShadow(p *node, vp viewport, st style, under bool) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	sd, _ := numbers(attrOr(p, "stdDeviation", "2"))
	sx, sy := 2.0, 2.0
	if len(sd) > 0 {
		sx, sy = sd[0], sd[0]
	}
	if len(sd) > 1 {
		sy = sd[1]
	}
	kx, ky := c.devScale()
	blurred, err := c.gauss(in, float64(sx*c.unit[0])*kx, float64(sy*c.unit[1])*ky)
	if err != nil {
		return nil, err
	}
	moved, err := c.shift(blurred, c.num(p, "dx", 2, 0), c.num(p, "dy", 2, 1))
	if err != nil {
		return nil, err
	}
	col := c.floodColor(p, "flood-color", "flood-opacity", st)
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	for i := 0; i < len(out); i += 4 {
		sa := moved[i+3]
		var sh [4]float32
		for q := 0; q < 4; q++ {
			sh[q] = float32(col[q] * sa)
		}
		if under {
			out[i], out[i+1], out[i+2], out[i+3] = sh[0], sh[1], sh[2], sh[3]
			continue
		}
		ia := 1 - in[i+3]
		for q := 0; q < 4; q++ {
			out[i+q] = in[i+q] + float32(sh[q]*ia)
		}
	}
	return out, nil
}

func (c *fctx) transfer(p *node) (fbuf, error) {
	in, err := c.input(p.attrs["in"])
	if err != nil {
		return nil, err
	}
	out, err := c.alloc()
	if err != nil {
		return nil, err
	}
	var fns [4]func(float32) float32
	for _, f := range p.children {
		ch := -1
		switch f.name {
		case "feFuncR":
			ch = 0
		case "feFuncG":
			ch = 1
		case "feFuncB":
			ch = 2
		case "feFuncA":
			ch = 3
		}
		if ch < 0 {
			continue
		}
		get := func(name string, def float64) float64 {
			v, err := strconv.ParseFloat(strings.TrimSpace(f.attrs[name]), 64)
			if err != nil {
				return def
			}
			return v
		}
		table, _ := numbers(f.attrs["tableValues"])
		switch strings.TrimSpace(f.attrs["type"]) {
		case "linear":
			slope, icpt := float32(get("slope", 1)), float32(get("intercept", 0))
			fns[ch] = func(v float32) float32 { return float32(slope*v) + icpt }
		case "gamma":
			amp, ex, off := get("amplitude", 1), get("exponent", 1), get("offset", 0)
			fns[ch] = func(v float32) float32 { return float32(float64(amp*trig.Pow(float64(v), ex)) + off) }
		case "table":
			if len(table) > 0 {
				fns[ch] = func(v float32) float32 {
					n := len(table) - 1
					if n == 0 {
						return float32(table[0])
					}
					x := float64(float64(v) * float64(n))
					k := min(int(x), n-1)
					return float32(table[k] + float64((x-float64(k))*(table[k+1]-table[k])))
				}
			}
		case "discrete":
			if len(table) > 0 {
				fns[ch] = func(v float32) float32 {
					k := min(int(float64(v)*float64(len(table))), len(table)-1)
					return float32(table[k])
				}
			}
		}
	}
	for i := 0; i < len(out); i += 4 {
		a := in[i+3]
		var u [4]float32
		if a > 0 {
			u = [4]float32{in[i] / a, in[i+1] / a, in[i+2] / a, a}
		}
		for q := 0; q < 4; q++ {
			if fns[q] != nil {
				u[q] = clampf(fns[q](u[q]))
			}
		}
		for q := 0; q < 3; q++ {
			out[i+q] = float32(u[q] * u[3])
		}
		out[i+3] = u[3]
	}
	return out, nil
}
