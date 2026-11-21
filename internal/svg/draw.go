package svg

import (
	"image"

	"github.com/whhhh1500/goimgtools/internal/raster"
)

// renderer draws the bands of a scene. Bands are premultiplied RGBA, eight
// bits a channel, as cairo (and so librsvg) draws.
type renderer struct {
	sc    *scene
	cells raster.Cells
	cov   []uint8 // a shape's coverage over a band
	ccov  []uint8 // a clip's
	tmp   []uint8 // one path of a clip
	mcov  []uint8 // a mask's
	pool  [][]uint8
	n     int // rows a band
}

// layersNeeded is how many layers drawing items takes at most: one, one
// more for each group open, and those of the masks.
func layersNeeded(items []item) int {
	most, d := 1, 0
	for _, it := range items {
		switch it.op {
		case opPush:
			d++
			most = max(most, d+1)
		case opPop:
			if it.mask != nil {
				most = max(most, d+1+layersNeeded(it.mask.items))
			}
			d--
		}
	}
	return most
}

// mul255 is x*y/255, rounded (x, y at most 255).
func mul255(x, y uint32) uint32 {
	t := x*y + 128
	return (t + t>>8) >> 8
}

// band draws rows y0 to y0+n-1 and returns them.
func (r *renderer) band(y0, n int) []uint8 {
	w := r.sc.w
	out := r.pool[0][:4*w*n]
	clear(out)
	r.draw(r.sc.items, 0, y0, n)
	return out
}

// draw paints items onto layer base of the pool, over rows y0 to y0+n-1,
// the layers above it taken by the groups.
func (r *renderer) draw(items []item, base, y0, n int) {
	w := r.sc.w
	depth := base
	band := image.Rect(0, y0, w, y0+n)
	for i := 0; i < len(items); i++ {
		it := &items[i]
		rows := it.bounds.Intersect(band)
		switch it.op {
		case opPush:
			if rows.Empty() {
				i = it.end
				continue
			}
			depth++
			r.clearRows(r.pool[depth][:4*w*n], rows, y0)
		case opPop:
			r.putDown(it, rows, y0, n, depth)
			depth--
		case opShape:
			if !rows.Empty() {
				r.paint(r.pool[depth][:4*w*n], it, rows, y0)
			}
		}
	}
}

func (r *renderer) clearRows(l []uint8, rows image.Rectangle, y0 int) {
	w := r.sc.w
	for y := rows.Min.Y; y < rows.Max.Y; y++ {
		clear(l[4*((y-y0)*w+rows.Min.X) : 4*((y-y0)*w+rows.Max.X)])
	}
}

// coverage renders a path over rows (a rectangle within the band) into
// cov, its clip applied; a nil path covers everything.
func (r *renderer) coverage(p *raster.Path, c *clip, rows image.Rectangle) []uint8 {
	w, h := rows.Dx(), rows.Dy()
	cov := r.cov[:w*h]
	if p != nil {
		p.Render(cov, w, rows.Min.X, rows.Min.Y, w, h, &r.cells)
	} else {
		for i := range cov {
			cov[i] = 255
		}
	}
	for ; c != nil; c = c.parent {
		cc := r.ccov[:w*h]
		clear(cc)
		for _, cp := range c.paths {
			t := r.tmp[:w*h]
			cp.Render(t, w, rows.Min.X, rows.Min.Y, w, h, &r.cells)
			for i, v := range t {
				cc[i] = uint8(min(255, uint32(cc[i])+uint32(v)))
			}
		}
		for i, v := range cc {
			cov[i] = uint8(mul255(uint32(cov[i]), uint32(v)))
		}
	}
	return cov
}

// paint draws a shape over the layer.
func (r *renderer) paint(dst []uint8, it *item, rows image.Rectangle, y0 int) {
	w := r.sc.w
	cov := r.coverage(it.path, it.clip, rows)
	rw := rows.Dx()
	for y := rows.Min.Y; y < rows.Max.Y; y++ {
		row := dst[4*(y-y0)*w:]
		cr := cov[(y-rows.Min.Y)*rw:][:rw]
		for i, c := range cr {
			if c == 0 {
				continue
			}
			x := rows.Min.X + i
			s := it.paint.at(x, y)
			over(row[4*x:4*x+4:4*x+4], s, uint32(c))
		}
	}
}

// over puts colour s, premultiplied, with coverage c, over d.
func over(d []uint8, s [4]uint8, c uint32) {
	if c == 255 && s[3] == 255 {
		d[0], d[1], d[2], d[3] = s[0], s[1], s[2], 255
		return
	}
	sr, sg, sb, sa := mul255(uint32(s[0]), c), mul255(uint32(s[1]), c), mul255(uint32(s[2]), c), mul255(uint32(s[3]), c)
	ia := 255 - sa
	d[0] = uint8(sr + mul255(uint32(d[0]), ia))
	d[1] = uint8(sg + mul255(uint32(d[1]), ia))
	d[2] = uint8(sb + mul255(uint32(d[2]), ia))
	d[3] = uint8(sa + mul255(uint32(d[3]), ia))
}

// putDown puts the layer of a group (at depth) over the one below, with
// its opacity, clip and mask.
func (r *renderer) putDown(it *item, rows image.Rectangle, y0, n, depth int) {
	if rows.Empty() {
		return
	}
	w := r.sc.w
	rw := rows.Dx()
	var mask []uint8
	if it.mask != nil {
		// The mask's content, drawn above the group's layer, and the
		// luminance times the alpha of each pixel of it.
		ml := r.pool[depth+1][:4*w*n]
		r.clearRows(ml, rows, y0)
		r.draw(it.mask.items, depth+1, y0, n)
		mcov := r.coverage(nil, it.mask.region, rows)
		mask = r.mcov[:rw*rows.Dy()]
		for y := rows.Min.Y; y < rows.Max.Y; y++ {
			for i := 0; i < rw; i++ {
				p := ml[4*((y-y0)*w+rows.Min.X+i):]
				// Rec. 709 luminance of the premultiplied colour: the
				// luminance times the alpha.
				l := (2125*uint32(p[0]) + 7154*uint32(p[1]) + 721*uint32(p[2]) + 5000) / 10000
				j := (y-rows.Min.Y)*rw + i
				mask[j] = uint8(mul255(min(255, l), uint32(mcov[j])))
			}
		}
	}
	src, dst := r.pool[depth][:4*w*n], r.pool[depth-1][:4*w*n]
	k := uint32(float64(it.opacity*255) + 0.5)
	cov := r.coverage(nil, it.clip, rows)
	for y := rows.Min.Y; y < rows.Max.Y; y++ {
		o := 4 * (y - y0) * w
		j0 := (y - rows.Min.Y) * rw
		for i, c := range cov[j0 : j0+rw] {
			x := rows.Min.X + i
			p := src[o+4*x : o+4*x+4 : o+4*x+4]
			if c == 0 || p[3] == 0 {
				continue
			}
			a := mul255(uint32(c), k)
			if mask != nil {
				a = mul255(a, uint32(mask[j0+i]))
			}
			over(dst[o+4*x:o+4*x+4:o+4*x+4], [4]uint8{p[0], p[1], p[2], p[3]}, a)
		}
	}
}
