package svg

import (
	"errors"
	"fmt"
	"image"
	"math"
	"slices"
	"strings"

	"github.com/whhhh1500/goimgtools/internal/raster"
	"github.com/whhhh1500/goimgtools/internal/textsrc"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// Items of a scene, in painting order.
const (
	opShape = iota
	opPush  // the start of a group drawn apart, then put down as a whole
	opPop
)

type item struct {
	op     int
	path   *raster.Path
	dx, dy float64 // where the path is drawn: moved by this many pixels
	paint  *shader
	clip   *clip
	bounds image.Rectangle // the device pixels it can touch
	// A group: its opacity, clip and mask, its blend mode, and where it
	// ends.
	opacity float64
	mask    *mask
	blend   int
	end     int
}

// mask is the content of a mask, drawn as a scene of its own, and the
// rectangle it covers.
type mask struct {
	items  []item
	region *clip
	alpha  bool // mask-type: alpha, the alpha of the content (else its luminance)
}

// clip is the coverage of a clip path: the union of its shapes.
type clip struct {
	paths  []*raster.Path
	subs   []*clip // the clip paths of the outlines, by their index (to its length; nil: none)
	bounds image.Rectangle
	parent *clip // a clip path's own clip path, or the clip of an outer group
}

// scene is a document made ready to draw.
type scene struct {
	items      []item
	w, h       int
	points     int   // the points of all its outlines, for the memory they take
	imageBytes int64 // the pictures of image elements, reserved
	usedPixels int64 // the pixels of pictures drawn, those of the documents it is a picture in among them
	itemCount  int   // its items, those of masks among them
	charged    int64 // reserved for its points and items
}

// Bytes reserved for a point of an outline and for an item.
const (
	pointBytes = 40
	itemBytes  = 160
)

// roomPoints is how many more points the scene may take: within the limit
// on them and the memory left; byMemory says which is the smaller.
func (b *builder) roomPoints() (n int, byMemory bool) {
	room := int64(maxScenePts - b.sc.points)
	if b.avail != nil {
		pending := int64(b.sc.points)*pointBytes + int64(b.sc.itemCount)*itemBytes - b.sc.charged
		if m := (b.avail() - pending) / pointBytes; m < room {
			room, byMemory = m, true
		}
	}
	return int(max(0, room)), byMemory
}

// account reserves the memory the scene has grown into, a megabyte at a
// time, so that a document that asks for too much stops early.
func (b *builder) account() {
	need := int64(b.sc.points)*pointBytes + int64(b.sc.itemCount)*itemBytes
	if need-b.sc.charged < 1<<20 || b.err != nil {
		return
	}
	if err := b.reserve(need - b.sc.charged); err != nil {
		b.err = err
		return
	}
	b.sc.charged = need
}

// Limits on the work a document can ask for.
const (
	maxItems     = 1 << 20
	maxUseDepth  = 24
	maxScenePts  = 8 << 20
	errTooLarge  = "goimgtools: svg: the document draws too much"
	defaultSize  = 100
	maxDimension = 1 << 20
)

var errTooMuch = errors.New(errTooLarge)

// builder walks the document into a scene.
type builder struct {
	imagePixels int64
	inputPixels int64           // of the files of the pictures, against maxInputPixels
	masking     map[*node]bool  // the masks being built, against cycles
	patterning  map[*node]bool  // and the patterns
	marking     map[*node]bool  // and the markers
	filtering   map[*node]bool  // and the filters
	merged      map[*node]*node // filters with what they take by href
	clipping    map[*node]bool  // and the clip paths
	release     func(int64)
	reserve     func(int64) error
	avail       func() int64 // memory the budget has left
	fontSets    map[string]*textsrc.Set
	root        *node
	ids         map[string]*node
	sc          *scene
	err         error
	useing      map[*node]bool // the use elements being expanded, against cycles
	outlines    map[*node]*outline
	clipText    func(segs []seg, m matrix) // set while the text of a clip path is laid out: takes its outlines
	ctx         *paintContext              // what context-fill and context-stroke take, while a marker or use is drawn
	measureText func(cell [4][2]float64)   // set while text is measured, not drawn: takes the corners of each character's cell
	opts        buildOptions
	depth       int
	full        image.Rectangle
}

// size is the intrinsic size of the document, in pixels, and the
// transform from its user space to them.
func size(root *node) (w, h float64, m matrix) {
	vb, hasVB := viewBox(root)
	vp := viewport{defaultSize, defaultSize}
	if hasVB {
		vp = viewport{vb[2], vb[3]}
	}
	ws, okW := root.attr("width")
	hs, okH := root.attr("height")
	w, errW := parseLength(ws, 16, vp, axisX)
	h, errH := parseLength(hs, 16, vp, axisY)
	if strings.HasSuffix(strings.TrimSpace(ws), "%") {
		okW = false
	}
	if strings.HasSuffix(strings.TrimSpace(hs), "%") {
		okH = false
	}
	okW, okH = okW && errW && w > 0, okH && errH && h > 0
	switch {
	case okW && okH:
	case hasVB && okW:
		h = w * vb[3] / vb[2]
	case hasVB && okH:
		w = h * vb[2] / vb[3]
	case hasVB:
		w, h = vb[2], vb[3]
	default:
		if !okW {
			w = defaultSize
		}
		if !okH {
			h = defaultSize
		}
	}
	w, h = math.Min(w, maxDimension), math.Min(h, maxDimension)
	m = identity
	if hasVB {
		m = viewBoxTransform(vb, root.attrs["preserveAspectRatio"], w, h)
	}
	// The root's transform (SVG 2) moves its box, the viewBox's mapping
	// within it: about its centre by default, as Chrome does.
	if tm, has := transformOf(root, 16, viewport{w, h}); has {
		m = aboutOrigin(root, tm, w, h, 16, viewport{w, h}, [2]float64{0.5, 0.5}).mul(m)
	}
	return w, h, m
}

// viewBox reads an element's viewBox: min x, min y, width, height.
func viewBox(n *node) ([4]float64, bool) {
	s, ok := n.attr("viewBox")
	if !ok {
		return [4]float64{}, false
	}
	v, ok := numbers(s)
	if !ok || len(v) != 4 || !(v[2] > 0) || !(v[3] > 0) {
		return [4]float64{}, false
	}
	return [4]float64{v[0], v[1], v[2], v[3]}, true
}

// viewBoxTransform maps a viewBox into a viewport of w by h as
// preserveAspectRatio says.
func viewBoxTransform(vb [4]float64, par string, w, h float64) matrix {
	sx, sy := w/vb[2], h/vb[3]
	f := strings.Fields(par)
	align, slice := "xMidYMid", false
	if len(f) > 0 {
		align = f[0]
	}
	if len(f) > 1 && f[1] == "slice" {
		slice = true
	}
	if align == "none" {
		return matrix{sx, 0, 0, sy, float64(-vb[0] * sx), float64(-vb[1] * sy)}
	}
	s := math.Min(sx, sy)
	if slice {
		s = math.Max(sx, sy)
	}
	tx, ty := float64(-vb[0]*s), float64(-vb[1]*s)
	ex, ey := w-float64(vb[2]*s), h-float64(vb[3]*s)
	switch {
	case strings.Contains(align, "xMid"):
		tx += float64(ex / 2)
	case strings.Contains(align, "xMax"):
		tx += ex
	}
	switch {
	case strings.Contains(align, "YMid"):
		ty += float64(ey / 2)
	case strings.Contains(align, "YMax"):
		ty += ey
	}
	return matrix{s, 0, 0, s, tx, ty}
}

// buildOptions is what a scene is built with besides its document.
type buildOptions struct {
	nesting      int   // how deep a picture within pictures the document is
	usedPixels   int64 // the pixels of pictures the documents it is in have drawn
	contextPaint bool  // whether it may use context-fill or context-stroke
}

// build makes the scene of a document drawn at w by h pixels, its user
// space taken there by m.
func build(root *node, w, h int, m matrix, reserve func(int64) error, release func(int64), avail func() int64, opts buildOptions) (*scene, error) {
	b := &builder{avail: avail, masking: map[*node]bool{}, patterning: map[*node]bool{}, marking: map[*node]bool{}, filtering: map[*node]bool{}, clipping: map[*node]bool{}, release: release, reserve: reserve, fontSets: map[string]*textsrc.Set{}, root: root, ids: map[string]*node{}, sc: &scene{w: w, h: h}, useing: map[*node]bool{}, full: image.Rect(0, 0, w, h), opts: opts, imagePixels: opts.usedPixels}
	// The fonts are needed while the outlines of text are made.
	defer func() {
		for _, s := range b.fontSets {
			if s != nil {
				s.Close(release)
			}
		}
	}()
	var index func(n *node)
	index = func(n *node) {
		if id, ok := n.attrs["id"]; ok {
			if _, dup := b.ids[id]; !dup {
				b.ids[id] = n
			}
		}
		for _, c := range n.children {
			index(c)
		}
	}
	index(root)
	applySheets(root)
	vb, hasVB := viewBox(root)
	vp := viewport{float64(w), float64(h)}
	if hasVB {
		vp = viewport{vb[2], vb[3]}
	}
	st := computeStyle(root, initialStyle(), vp)
	if !st.display {
		return b.sc, nil // a root not displayed: nothing is drawn
	}
	// The root's background colour (CSS, as mermaid and draw.io write it)
	// is the canvas's, under all else, as browsers draw it.
	if c, ok := rootBackground(root); ok {
		if sh := solidShader(c, 1); sh != nil {
			b.add(item{op: opShape, paint: sh, bounds: b.full})
		}
	}
	// The root's clip path, mask and opacity apply to all it holds (as in
	// Chrome), in its user space.
	own := b.clipOf(root, m, vp, st, nil)
	switch mk := b.maskOf(root, m, vp, st); {
	case own == errClip || mk == errMask:
	case mk != nil:
		b.groupMasked(root, st, own, nil, mk, func(c *clip) { b.children(root, m, st, vp, c) })
	case own != nil || st.opacity < 1:
		b.group(root, st, own, nil, func(c *clip) { b.children(root, m, st, vp, c) })
	default:
		b.children(root, m, st, vp, nil)
	}
	b.sc.usedPixels = b.imagePixels
	if b.err != nil {
		return b.sc, b.err
	}
	return b.sc, nil
}

func (b *builder) add(it item) int {
	if b.sc.itemCount >= maxItems || b.err != nil {
		if b.err == nil {
			b.err = errTooMuch
		}
		return -1
	}
	b.sc.itemCount++
	if b.sc.itemCount%1024 == 0 {
		b.account()
	}
	b.sc.items = append(b.sc.items, it)
	return len(b.sc.items) - 1
}

// children draws the children of a container.
func (b *builder) children(n *node, m matrix, st style, vp viewport, cl *clip) {
	for _, c := range n.children {
		if b.err != nil {
			return
		}
		b.element(c, m, st, vp, cl)
	}
}

// element draws one element and what it holds.
func (b *builder) element(n *node, m matrix, parent style, vp viewport, cl *clip) {
	switch n.name {
	case "defs", "symbol", "clipPath", "mask", "linearGradient", "radialGradient", "pattern", "marker",
		"style", "title", "desc", "metadata", "filter", "script", "font", "font-face":
		return
	}
	if !conditionsMet(n) {
		return
	}
	st := computeStyle(n, parent, vp)
	if !st.display {
		return
	}
	if tm, has := transformOf(n, st.fontSize, vp); has {
		m = m.mul(aboutOrigin(n, tm, vp.w, vp.h, st.fontSize, vp, [2]float64{}))
	}
	if m.det() == 0 {
		return
	}
	// A clip path, an opacity on a group: the group is drawn apart.
	ownClip := b.clipOf(n, m, vp, st, cl)
	if ownClip == errClip {
		return // a reference to a clip path that clips everything away
	}
	f := b.filterRef(n)
	if mk := b.maskOf(n, m, vp, st); mk != nil {
		if mk == errMask {
			return
		}
		inner := st
		inner.opacity = 1
		b.groupMasked(n, st, ownClip, cl, mk, func(c *clip) {
			if f != nil {
				b.filtered(n, f, m, inner, vp, nil, c)
				return
			}
			b.draw(n, m, inner, vp, nil, c)
		})
		return
	}
	if f != nil {
		b.filtered(n, f, m, st, vp, ownClip, cl)
		return
	}
	if st.blend != blendNormal || st.isolate {
		// Drawn apart, then blended with what is under it (or, isolated,
		// its own content blended among itself).
		inner := st
		inner.opacity, inner.blend, inner.isolate = 1, blendNormal, false
		b.group(n, st, ownClip, cl, func(c *clip) { b.draw(n, m, inner, vp, nil, c) })
		return
	}
	b.draw(n, m, st, vp, ownClip, cl)
}

// draw draws an element once its style, transform and clip are known.
func (b *builder) draw(n *node, m matrix, st style, vp viewport, ownClip, cl *clip) {
	switch n.name {
	case "g", "a", "switch":
		b.group(n, st, ownClip, cl, func(c *clip) {
			if n.name == "switch" {
				// The first child whose conditions hold.
				for _, k := range n.children {
					switch k.name {
					case "#text", "title", "desc", "metadata", "foreignObject":
						continue // nothing to draw here: the next may be
					}
					if conditionsMet(k) {
						b.element(k, m, st, vp, c)
						return
					}
				}
				return
			}
			b.children(n, m, st, vp, c)
		})
	case "svg":
		b.nested(n, m, st, vp, ownClip, cl)
	case "use":
		b.use(n, m, st, vp, ownClip, cl)
	case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon":
		segs := shapeSegs(n, st, vp)
		if len(segs) == 0 {
			return
		}
		if n.name == "rect" || n.name == "circle" || n.name == "ellipse" || !hasMarkers(n) {
			b.shape(n, segs, m, st, vp, ownClip, cl)
			return
		}
		// Markers belong to the shape: with an opacity, a clip, they are
		// grouped with it.
		inner := st
		inner.opacity = 1
		b.group(n, st, ownClip, cl, func(c *clip) {
			marks := func() {
				if st.visible {
					b.markers(n, segs, m, inner, vp, c)
				}
			}
			switch inner.markersEarly {
			case 2: // paint-order: markers first
				marks()
				b.shape(n, segs, m, inner, vp, nil, c)
			case 1: // between the fill and the stroke
				first, second := inner, inner
				first.stroke, second.fill = paint{kind: paintNone}, paint{kind: paintNone}
				if inner.strokeFirst {
					first, second = second, first
				}
				b.shape(n, segs, m, first, vp, nil, c)
				marks()
				b.shape(n, segs, m, second, vp, nil, c)
			default:
				b.shape(n, segs, m, inner, vp, nil, c)
				marks()
			}
		})
	case "text":
		if !st.visible && len(n.children) == 0 {
			return
		}
		b.group(n, st, ownClip, cl, func(c *clip) { b.text(n, m, st, vp, nil, c) })
	case "image":
		b.image(n, m, st, vp, ownClip, cl)
	}
}

// errClip marks a clip that leaves nothing.
var errClip = &clip{}

// group draws what draw adds as one group, with its opacity and clip.
func (b *builder) group(n *node, st style, own, outer *clip, draw func(c *clip)) {
	b.groupMasked(n, st, own, outer, nil, draw)
}

// groupMasked is group with a mask.
func (b *builder) groupMasked(n *node, st style, own, outer *clip, mk *mask, draw func(c *clip)) {
	if st.opacity <= 0 {
		return
	}
	if st.opacity >= 1 && own == nil && mk == nil && st.blend == blendNormal && !st.isolate {
		draw(outer)
		return
	}
	push := b.add(item{op: opPush})
	if push < 0 {
		return
	}
	draw(nil) // inside the group, the clip applies once, to all of it
	if b.err != nil {
		return
	}
	c := chainClip(own, outer)
	pop := b.add(item{op: opPop, opacity: st.opacity, clip: c, mask: mk, blend: st.blend})
	if pop < 0 {
		return
	}
	// The group's bounds: what its items touch, within its clip.
	var r image.Rectangle
	for _, it := range b.sc.items[push+1 : pop] {
		if it.op == opShape {
			r = r.Union(it.bounds)
		}
	}
	if c != nil {
		r = r.Intersect(c.bounds)
	}
	b.sc.items[push].bounds, b.sc.items[pop].bounds = r, r
	b.sc.items[push].end = pop
}

// nested draws an svg element inside another: a new viewport, clipped to
// its rectangle.
func (b *builder) nested(n *node, m matrix, st style, vp viewport, own, outer *clip) {
	x, _ := parseLength(n.attrs["x"], st.fontSize, vp, axisX)
	y, _ := parseLength(n.attrs["y"], st.fontSize, vp, axisY)
	w, okW := parseLength(attrOr(n, "width", "100%"), st.fontSize, vp, axisX)
	h, okH := parseLength(attrOr(n, "height", "100%"), st.fontSize, vp, axisY)
	if !okW || !okH || w <= 0 || h <= 0 {
		return
	}
	b.viewportInto(n, m, st, x, y, w, h, own, outer, func(mm matrix, inner viewport, c *clip) {
		b.children(n, mm, st, inner, c)
	})
}

// viewportInto draws through a viewport of w by h at (x, y), its viewBox
// fitted in, what is outside it clipped.
func (b *builder) viewportInto(n *node, m matrix, st style, x, y, w, h float64, own, outer *clip, draw func(matrix, viewport, *clip)) {
	mm := m.mul(matrix{1, 0, 0, 1, x, y})
	inner := viewport{w, h}
	if vb, ok := viewBox(n); ok {
		mm = mm.mul(viewBoxTransform(vb, n.attrs["preserveAspectRatio"], w, h))
		inner = viewport{vb[2], vb[3]}
	}
	// overflow is hidden unless it says otherwise.
	rect := own
	if ov, _ := n.prop("overflow"); ov != "visible" && ov != "auto" {
		segs := rectSegs(x, y, w, h, 0, 0)
		p := fillPath(flatten(segs, m), m, false)
		rect = chainClip(&clip{paths: []*raster.Path{p}, bounds: pathBounds(p, b.full)}, own)
	}
	b.group(n, st, rect, outer, func(c *clip) { draw(mm, inner, c) })
}

func attrOr(n *node, name, def string) string {
	if v, ok := n.attr(name); ok {
		return v
	}
	return def
}

// href is the element a use, a gradient or a clip path refers to.
func (b *builder) href(n *node) *node {
	h, ok := n.attr("href")
	if !ok {
		h, ok = n.attr("xlink:href")
	}
	if !ok || !strings.HasPrefix(strings.TrimSpace(h), "#") {
		return nil
	}
	return b.ids[strings.TrimPrefix(strings.TrimSpace(h), "#")]
}

// use draws the element a use refers to, as if it stood in the use's place.
func (b *builder) use(n *node, m matrix, st style, vp viewport, own, outer *clip) {
	t := b.href(n)
	if t == nil || b.useing[n] || b.depth >= maxUseDepth {
		return
	}
	for p := n; p != nil; p = p.parent {
		if p == t {
			return // a use inside what it uses
		}
	}
	b.useing[n] = true
	b.depth++
	defer func() { delete(b.useing, n); b.depth-- }()
	x, _ := parseLength(n.attrs["x"], st.fontSize, vp, axisX)
	y, _ := parseLength(n.attrs["y"], st.fontSize, vp, axisY)
	if !b.opts.contextPaint {
		b.useContent(n, t, m, st, vp, x, y, own, outer)
		return
	}
	// The use element is the context of the paints of what it draws, its
	// bounding box that of its content.
	box := func() []seg {
		bx, ok := b.bboxOf(n, vp, st)
		if !ok {
			return nil
		}
		return rectSegs(bx[0], bx[1], bx[2], bx[3], 0, 0)
	}
	b.withContext(n, st, box, m, vp, func() { b.useContent(n, t, m, st, vp, x, y, own, outer) })
}

func (b *builder) useContent(n, t *node, m matrix, st style, vp viewport, x, y float64, own, outer *clip) {
	switch t.name {
	case "symbol", "svg":
		w, okW := parseLength(attrOr(n, "width", attrOr(t, "width", "100%")), st.fontSize, vp, axisX)
		h, okH := parseLength(attrOr(n, "height", attrOr(t, "height", "100%")), st.fontSize, vp, axisY)
		if !okW || !okH || w <= 0 || h <= 0 {
			return
		}
		ts := computeStyle(t, st, vp)
		if !ts.display {
			return
		}
		if t.name == "svg" {
			// An svg element keeps its place, moved by the use's.
			tx, _ := parseLength(t.attrs["x"], ts.fontSize, vp, axisX)
			ty, _ := parseLength(t.attrs["y"], ts.fontSize, vp, axisY)
			x, y = x+tx, y+ty
		}
		into := func(own, outer *clip) {
			b.viewportInto(t, m, ts, x, y, w, h, own, outer, func(mm matrix, inner viewport, c *clip) {
				b.children(t, mm, ts, inner, c)
			})
		}
		if st.opacity < 1 || st.blend != blendNormal {
			// The use's own opacity and blending, over what it draws.
			b.group(n, st, own, outer, func(c *clip) { into(nil, c) })
			return
		}
		into(own, outer)
	default:
		mm := m.mul(matrix{1, 0, 0, 1, x, y})
		b.group(n, st, own, outer, func(c *clip) { b.element(t, mm, st, vp, c) })
	}
}

// clipOf is the clip path an element refers to, in device space, or nil;
// errClip when it clips everything.
func (b *builder) clipOf(n *node, m matrix, vp viewport, st style, outer *clip) *clip {
	v, ok := n.prop("clip-path")
	if !ok {
		return nil
	}
	v = strings.TrimSpace(v)
	if v == "none" || !strings.HasPrefix(v, "url(") {
		return nil
	}
	end := strings.IndexByte(v, ')')
	if end < 0 {
		return nil
	}
	id := strings.TrimPrefix(strings.Trim(strings.TrimSpace(v[4:end]), `"'`), "#")
	cp := b.ids[id]
	if cp == nil || cp.name != "clipPath" {
		return nil // a reference to nothing: no clip
	}
	cm := m
	if tm, has := transformOf(cp, st.fontSize, vp); has {
		cm = cm.mul(aboutOrigin(cp, tm, vp.w, vp.h, st.fontSize, vp, [2]float64{}))
	}
	if cp.attrs["clipPathUnits"] == "objectBoundingBox" {
		bx, ok := b.bboxOf(n, vp, st)
		if !ok || bx[2] <= 0 || bx[3] <= 0 {
			return errClip
		}
		cm = cm.mul(matrix{bx[2], 0, 0, bx[3], bx[0], bx[1]})
	}
	// A clip path that refers to itself, through its children or its own
	// clip path: the second time, no clip (as Chrome).
	if b.clipping[cp] {
		return nil
	}
	b.clipping[cp] = true
	defer delete(b.clipping, cp)
	c := &clip{}
	// Its children inherit what it has where it is (its clip-rule), not
	// from the element that refers to it.
	cst := b.styleOf(cp, vp)
	var add func(k *node, km matrix, kst style, depth int)
	// part adds the outline of a child, within the child's own clip path.
	part := func(k *node, km matrix, s style, p *raster.Path) {
		sub := b.clipOf(k, km, vp, s, nil)
		if sub == errClip {
			return // clipped away
		}
		c.paths = append(c.paths, p)
		if sub != nil {
			for len(c.subs) < len(c.paths)-1 {
				c.subs = append(c.subs, nil)
			}
			c.subs = append(c.subs, sub)
		}
		c.bounds = c.bounds.Union(pathBounds(p, b.full))
	}
	add = func(k *node, km matrix, kst style, depth int) {
		s := computeStyle(k, kst, vp)
		if !s.display || !s.visible {
			return
		}
		if tm, has := transformOf(k, s.fontSize, vp); has {
			km = km.mul(aboutOrigin(k, tm, vp.w, vp.h, s.fontSize, vp, [2]float64{}))
		}
		switch k.name {
		case "use":
			if t := b.href(k); t != nil && depth < 4 && t.name != "symbol" && t.name != "svg" {
				x, _ := parseLength(k.attrs["x"], s.fontSize, vp, axisX)
				y, _ := parseLength(k.attrs["y"], s.fontSize, vp, axisY)
				add(t, km.mul(matrix{1, 0, 0, 1, x, y}), s, depth+1)
			}
		case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon":
			segs := shapeSegs(k, s, vp)
			if len(segs) == 0 {
				return
			}
			p := fillPath(flatten(segs, km), km, s.clipEvenOdd)
			b.sc.points += countPoints(segs)
			part(k, km, s, p)
		case "text":
			// Its glyphs' outlines, as it would draw them.
			rule := s.clipEvenOdd
			b.clipText = func(segs []seg, m matrix) {
				p := fillPath(flatten(segs, m), m, rule)
				b.sc.points += countPoints(segs)
				c.paths = append(c.paths, p)
				c.bounds = c.bounds.Union(pathBounds(p, b.full))
			}
			b.text(k, km, s, vp, nil, nil)
			b.clipText = nil
		}
	}
	for _, k := range cp.children {
		add(k, cm, cst, 0)
	}
	if len(c.paths) == 0 {
		return errClip
	}
	// The clip path's own clip path, in the same user space.
	if own := b.clipOf(cp, m, vp, cst, nil); own == errClip {
		return errClip
	} else if own != nil {
		c.parent = own
	}
	return c
}

// styleOf is the style a node has where it is in the document, computed
// down from the root through its ancestors.
func (b *builder) styleOf(n *node, vp viewport) style {
	var chain []*node
	for p := n; p != nil; p = p.parent {
		chain = append(chain, p)
	}
	st := initialStyle()
	for i := len(chain) - 1; i >= 0; i-- {
		st = computeStyle(chain[i], st, vp)
	}
	return st
}

func segsBBox(segs []seg) ([4]float64, bool) {
	ps := flatten(segs, identity)
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	for _, q := range ps {
		for i := 0; i+1 < len(q.xy); i += 2 {
			minX, maxX = math.Min(minX, q.xy[i]), math.Max(maxX, q.xy[i])
			minY, maxY = math.Min(minY, q.xy[i+1]), math.Max(maxY, q.xy[i+1])
		}
	}
	if minX > maxX {
		return [4]float64{}, false
	}
	return [4]float64{minX, minY, maxX - minX, maxY - minY}, true
}

func countPoints(segs []seg) int { return 3 * len(segs) }

// pathBounds is the rectangle of pixels a path touches, within full.
func pathBounds(p *raster.Path, full image.Rectangle) image.Rectangle {
	x0, y0, x1, y1 := p.Bounds()
	return image.Rect(x0, y0, x1, y1).Intersect(full)
}

// outline is the fill and the stroke of a shape drawn through a use or a
// marker, kept for the next time it is drawn: where the matrix differs only
// by its move (the markers of a plot), the outlines are the same, moved,
// and its points are not made and held again.
type outline struct {
	segs       []seg
	a, b, c, d float64 // the matrix but for its move
	fill       outlinePart
	evenOdd    bool
	stroke     outlinePart
	width      float64
	cap, join  int
	miterLimit float64
	dash       []float64
	dashOffset float64
	nonScaling bool
}

// outlinePart is an outline and the move of the matrix it was made by.
type outlinePart struct {
	path *raster.Path
	e, f float64
}

// shapeItem is the item of a shape's outline, moved by (dx, dy).
func (b *builder) shapeItem(path *raster.Path, dx, dy float64, p *shader) *item {
	x0, y0, x1, y1 := path.BoundsAt(dx, dy)
	return &item{op: opShape, path: path, dx: dx, dy: dy, paint: p, bounds: image.Rect(x0, y0, x1, y1).Intersect(b.full)}
}

// shape draws a shape's fill, then its stroke.
func (b *builder) shape(n *node, segs []seg, m matrix, st style, vp viewport, own, outer *clip) {
	if b.clipText != nil {
		b.clipText(segs, m)
		return
	}
	if !st.visible || st.opacity <= 0 {
		return
	}
	fp := b.shader(n, st.fill, st.fillOpacity, st, segs, m, vp)
	var sp *shader
	if st.strokeWidth > 0 {
		sp = b.shader(n, st.stroke, st.strokeOpacity, st, segs, m, vp)
	}
	if fp == nil && sp == nil {
		return
	}
	// The outlines made the last time it was drawn, if they serve.
	o := b.outlines[n]
	if o != nil && (o.a != m.a || o.b != m.b || o.c != m.c || o.d != m.d || !slices.Equal(o.segs, segs)) {
		o = nil
	}
	var fill, stroke *item
	if fp != nil && o != nil && o.fill.path != nil && o.evenOdd == st.evenOdd {
		fill = b.shapeItem(o.fill.path, float64(m.e-o.fill.e), float64(m.f-o.fill.f), fp)
		fp = nil
	}
	if sp != nil && o != nil && o.stroke.path != nil && o.width == st.strokeWidth && o.cap == st.lineCap && o.join == st.lineJoin &&
		o.miterLimit == st.miterLimit && slices.Equal(o.dash, st.dash) && o.dashOffset == st.dashOffset && o.nonScaling == st.nonScalingStroke {
		stroke = b.shapeItem(o.stroke.path, float64(m.e-o.stroke.e), float64(m.f-o.stroke.f), sp)
		sp = nil
	}
	if fp != nil || sp != nil {
		// Kept for the next time, when drawn through a use or a marker.
		keep := len(b.useing) > 0 || len(b.marking) > 0
		if keep && o == nil {
			if old := b.outlines[n]; old != nil {
				b.sc.points -= countPoints(old.segs)
			}
			o = &outline{segs: segs, a: m.a, b: m.b, c: m.c, d: m.d}
			b.sc.points += countPoints(segs) // held while the document is built
			if b.outlines == nil {
				b.outlines = map[*node]*outline{}
			}
			b.outlines[n] = o
		}
		b.sc.points += countPoints(segs)
		if b.sc.points > maxScenePts {
			b.err = errTooMuch
			return
		}
		b.account()
		if b.err != nil {
			return
		}
		room, byMemory := b.roomPoints()
		ps, ok := flattenLimit(segs, m, room)
		b.sc.points -= countPoints(segs) // the segments give way to their points
		for _, q := range ps {
			b.sc.points += len(q.xy) / 2
		}
		if !ok || b.sc.points > maxScenePts {
			b.err = b.tooMuch(byMemory)
			return
		}
		b.account()
		if b.err != nil {
			return
		}
		if fp != nil {
			path := fillPath(ps, m, st.evenOdd)
			if !path.Empty() {
				fill = b.shapeItem(path, 0, 0, fp)
				if keep {
					o.fill, o.evenOdd = outlinePart{path, m.e, m.f}, st.evenOdd
				}
			}
		}
		if sp != nil {
			lines, sm := ps, m
			if st.nonScalingStroke {
				// Stroked in device space: its width and dashes in pixels.
				lines, sm = deviceLines(ps, m), identity
			}
			if st.dash != nil {
				room, byMemory := b.roomPoints()
				var ok bool
				if lines, ok = dashes(lines, st.dash, st.dashOffset, room); !ok {
					b.err = b.tooMuch(byMemory)
					return
				}
			}
			sk := newStroker(sm, st.strokeWidth, st.lineCap, st.lineJoin, st.miterLimit)
			var byMemory bool
			sk.limit, byMemory = b.roomPoints()
			for _, q := range lines {
				sk.stroke(q)
				if sk.points > sk.limit {
					break
				}
			}
			if sk.points > sk.limit {
				b.err = b.tooMuch(byMemory)
				return
			}
			b.sc.points += sk.points
			b.account()
			if b.err != nil {
				return
			}
			if !sk.path.Empty() {
				stroke = b.shapeItem(sk.path, 0, 0, sp)
				if keep {
					o.stroke = outlinePart{sk.path, m.e, m.f}
					o.width, o.cap, o.join, o.miterLimit = st.strokeWidth, st.lineCap, st.lineJoin, st.miterLimit
					o.dash, o.dashOffset, o.nonScaling = st.dash, st.dashOffset, st.nonScalingStroke
				}
			}
		}
	}
	if fill == nil && stroke == nil {
		return
	}
	// One part alone takes the opacity and the clip itself; both, as a
	// group, so that where the stroke covers the fill it is not seen.
	if fill == nil || stroke == nil {
		it := fill
		if it == nil {
			it = stroke
		}
		if st.opacity < 1 {
			it.paint = it.paint.faded(st.opacity)
		}
		c := chainClip(own, outer)
		it.clip = c
		if c != nil {
			it.bounds = it.bounds.Intersect(c.bounds)
		}
		b.add(*it)
		return
	}
	parts := []*item{fill, stroke}
	if st.strokeFirst {
		parts[0], parts[1] = stroke, fill
	}
	b.group(n, st, own, outer, func(c *clip) {
		for _, it := range parts {
			it.clip = c
			if c != nil {
				it.bounds = it.bounds.Intersect(c.bounds)
			}
			b.add(*it)
		}
	})
}

// chainClip is the clip own, then outer: outer is put at the end of own's
// chain (own is new to the element, so it may be changed).
func chainClip(own, outer *clip) *clip {
	if own == nil {
		return outer
	}
	c := own
	for c.parent != nil {
		c = c.parent
	}
	if c != outer {
		c.parent = outer
	}
	return own
}

// errMask marks a mask that hides everything.
var errMask = &mask{}

// maskOf is the mask an element refers to, its content built in device
// space; nil for none.
func (b *builder) maskOf(n *node, m matrix, vp viewport, st style) *mask {
	v, ok := n.prop("mask")
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
	mn := b.ids[strings.TrimPrefix(strings.Trim(strings.TrimSpace(v[4:end]), `"'`), "#")]
	if mn == nil || mn.name != "mask" || b.masking[mn] {
		return nil
	}
	b.masking[mn] = true
	defer delete(b.masking, mn)
	bx, hasBox := b.bboxOf(n, vp, st)
	// The region: by default the bounding box and a tenth more each way.
	var rx, ry, rw, rh float64
	if mn.attrs["maskUnits"] == "userSpaceOnUse" {
		rx, _ = parseLength(attrOr(mn, "x", "-10%"), st.fontSize, vp, axisX)
		ry, _ = parseLength(attrOr(mn, "y", "-10%"), st.fontSize, vp, axisY)
		rw, _ = parseLength(attrOr(mn, "width", "120%"), st.fontSize, vp, axisX)
		rh, _ = parseLength(attrOr(mn, "height", "120%"), st.fontSize, vp, axisY)
	} else {
		if !hasBox || bx[2] <= 0 || bx[3] <= 0 {
			return errMask
		}
		frac := func(name, def string) float64 {
			s := strings.TrimSpace(attrOr(mn, name, def))
			if strings.HasSuffix(s, "%") {
				f, _ := parseLength(strings.TrimSuffix(s, "%"), 0, vp, axisX)
				return f / 100
			}
			f, _ := parseLength(s, 0, vp, axisX)
			return f
		}
		rx, ry = bx[0]+float64(frac("x", "-10%")*bx[2]), bx[1]+float64(frac("y", "-10%")*bx[3])
		rw, rh = float64(frac("width", "120%")*bx[2]), float64(frac("height", "120%")*bx[3])
	}
	if !(rw > 0) || !(rh > 0) {
		return errMask
	}
	region := fillPath(flatten(rectSegs(rx, ry, rw, rh, 0, 0), m), m, false)
	mk := &mask{region: &clip{paths: []*raster.Path{region}, bounds: pathBounds(region, b.full)}}
	if v, ok := mn.prop("mask-type"); ok && strings.TrimSpace(v) == "alpha" {
		mk.alpha = true
	}
	cm := m
	if mn.attrs["maskContentUnits"] == "objectBoundingBox" {
		if !hasBox || bx[2] <= 0 || bx[3] <= 0 {
			return errMask
		}
		cm = cm.mul(matrix{bx[2], 0, 0, bx[3], bx[0], bx[1]})
	}
	// The content is drawn into a list of its own.
	saved := b.sc.items
	b.sc.items = nil
	mst := computeStyle(mn, initialStyle(), vp)
	b.children(mn, cm, mst, vp, nil)
	mk.items = b.sc.items
	b.sc.items = saved
	return mk
}

// bboxOf is the bounding box of an element's geometry, its descendants'
// for a container, in its user space: x, y, width, height.
func (b *builder) bboxOf(n *node, vp viewport, st style) ([4]float64, bool) {
	minX, minY, maxX, maxY := math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)
	var walk func(k *node, m matrix, kst style, depth int)
	walk = func(k *node, m matrix, kst style, depth int) {
		if depth > 32 || depth > 0 && !kst.display {
			return // what is not drawn takes no room
		}
		switch k.name {
		case "path", "rect", "circle", "ellipse", "line", "polyline", "polygon":
			for _, q := range flatten(shapeSegs(k, kst, vp), identity) {
				for i := 0; i+1 < len(q.xy); i += 2 {
					x, y := m.apply(q.xy[i], q.xy[i+1])
					minX, maxX = math.Min(minX, x), math.Max(maxX, x)
					minY, maxY = math.Min(minY, y), math.Max(maxY, y)
				}
			}
		case "image":
			x, _ := parseLength(k.attrs["x"], kst.fontSize, vp, axisX)
			y, _ := parseLength(k.attrs["y"], kst.fontSize, vp, axisY)
			w, okW := parseLength(k.attrs["width"], kst.fontSize, vp, axisX)
			h, okH := parseLength(k.attrs["height"], kst.fontSize, vp, axisY)
			if okW && okH && w > 0 && h > 0 {
				for _, c := range [][2]float64{{x, y}, {x + w, y + h}, {x + w, y}, {x, y + h}} {
					px, py := m.apply(c[0], c[1])
					minX, maxX = math.Min(minX, px), math.Max(maxX, px)
					minY, maxY = math.Min(minY, py), math.Max(maxY, py)
				}
			}
		case "text":
			// Its characters' cells, as laid out (and not drawn).
			outer := b.measureText
			b.measureText = func(cell [4][2]float64) {
				for _, c := range cell {
					x, y := m.apply(c[0], c[1])
					minX, maxX = math.Min(minX, x), math.Max(maxX, x)
					minY, maxY = math.Min(minY, y), math.Max(maxY, y)
				}
			}
			b.text(k, identity, kst, vp, nil, nil)
			b.measureText = outer
		case "g", "a", "switch", "svg":
			for _, c := range k.children {
				cm := m
				cst := computeStyle(c, kst, vp)
				if tm, has := transformOf(c, cst.fontSize, vp); has {
					cm = cm.mul(aboutOrigin(c, tm, vp.w, vp.h, cst.fontSize, vp, [2]float64{}))
				}
				walk(c, cm, cst, depth+1)
			}
		case "use":
			if t := b.href(k); t != nil {
				x, _ := parseLength(k.attrs["x"], kst.fontSize, vp, axisX)
				y, _ := parseLength(k.attrs["y"], kst.fontSize, vp, axisY)
				walk(t, m.mul(matrix{1, 0, 0, 1, x, y}), computeStyle(t, kst, vp), depth+1)
			}
		}
	}
	walk(n, identity, st, 0)
	if minX > maxX {
		return [4]float64{}, false
	}
	return [4]float64{minX, minY, maxX - minX, maxY - minY}, true
}

// conditionsMet evaluates an element's conditional attributes: it asks for
// no extension and no feature (as librsvg, none is taken for supported: a
// switch then falls back from the foreignObject of draw.io's labels to
// their text) and, if it names languages, English.
func conditionsMet(n *node) bool {
	if _, ok := n.attrs["requiredExtensions"]; ok {
		return false
	}
	if _, ok := n.attrs["requiredFeatures"]; ok {
		return false
	}
	if langs, ok := n.attrs["systemLanguage"]; ok {
		for _, l := range strings.Split(langs, ",") {
			l = strings.ToLower(strings.TrimSpace(l))
			if l == "en" || strings.HasPrefix(l, "en-") {
				return true
			}
		}
		return false
	}
	return true
}

// hasMarkers reports whether an element asks for markers.
func hasMarkers(n *node) bool {
	for _, p := range []string{"marker-start", "marker-mid", "marker-end", "marker"} {
		if v, ok := n.prop(p); ok && strings.HasPrefix(strings.TrimSpace(v), "url(") {
			return true
		}
	}
	return false
}

// tooMuch is the error of a document whose outlines outgrow what is left:
// the memory limit, or the limit on points.
func (b *builder) tooMuch(byMemory bool) error {
	if byMemory {
		return fmt.Errorf("%w: the outlines of an SVG document", pipeline.ErrMemoryLimit)
	}
	return errTooMuch
}
