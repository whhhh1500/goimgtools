// Copyright 2015 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the licenses/LICENSE-GO file.

// Package resize scales a pipeline.Source with a separable kernel, one source row
// at a time. The weights (newDistrib) and the per-pixel arithmetic are those
// of golang.org/x/image/draw (v0.47.0, scale.go / impl.go), so the output is
// byte-identical to
//
//	q.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
//
// with dst an *image.RGBA. x/image keeps a dstWidth × srcHeight buffer of
// float64 quadruples between its two passes; here each source row is scaled
// horizontally and added straight into the few destination rows it
// contributes to, in the same order x/image adds them, so memory is a handful
// of rows.
package resize

import (
	"errors"
	"image"
	"image/color"
	"io"
	"math"

	"golang.org/x/image/draw"

	"github.com/whhhh1500/goimgtools/pipeline"
)

type source struct {
	i, j               int32
	invTotalWeight     float64
	invTotalWeightFFFF float64
}

type contrib struct {
	coord  int32
	weight float64
}

type distrib struct {
	sources  []source
	contribs []contrib
}

// newDistrib is x/image/draw's newDistrib, statement for statement, except
// that the extent of the source the dw outputs span is given separately
// (ext). x/image always spans the whole source, ext == sw; Shrink leaves a
// last column or row that stands for only part of a cell, and then ext is a
// little less than sw.
func newDistrib(q *draw.Kernel, dw, sw int32, ext float64) distrib {
	scale := ext / float64(dw)
	halfWidth, kernelArgScale := q.Support, 1.0
	if scale > 1 {
		halfWidth *= scale
		kernelArgScale = 1 / scale
	}

	n, sources := int32(0), make([]source, dw)
	for x := range sources {
		center := float64((float64(x)+0.5)*scale) - 0.5
		i := int32(math.Floor(center - halfWidth))
		if i < 0 {
			i = 0
		}
		j := int32(math.Ceil(center + halfWidth))
		if j > sw {
			j = sw
			if j < i {
				j = i
			}
		}
		sources[x] = source{i: i, j: j, invTotalWeight: center}
		n += j - i
	}

	contribs := make([]contrib, 0, n)
	for k, b := range sources {
		totalWeight := 0.0
		l := int32(len(contribs))
		for coord := b.i; coord < b.j; coord++ {
			t := abs((b.invTotalWeight - float64(coord)) * kernelArgScale)
			if t >= q.Support {
				continue
			}
			weight := q.At(t)
			if weight == 0 {
				continue
			}
			totalWeight += weight
			contribs = append(contribs, contrib{coord, weight})
		}
		totalWeight = 1 / totalWeight
		sources[k] = source{
			i:                  l,
			j:                  int32(len(contribs)),
			invTotalWeight:     totalWeight,
			invTotalWeightFFFF: totalWeight / 0xffff,
		}
	}
	return distrib{sources, contribs}
}

func abs(f float64) float64 {
	if f < 0 {
		f = -f
	}
	return f
}

// ftou converts the range [0.0, 1.0] to [0, 0xffff], as x/image does.
func ftou(f float64) uint16 {
	i := int32(float64(0xffff*f) + 0.5)
	if i > 0xffff {
		return 0xffff
	}
	if i > 0 {
		return uint16(i)
	}
	return 0
}

func ycbcrToRGB16(yy, cb, cr uint8) (uint32, uint32, uint32) {
	pyy1 := int(yy) * 0x10101
	pcb1 := int(cb) - 128
	pcr1 := int(cr) - 128
	pru := (pyy1 + 91881*pcr1) >> 8
	pgu := (pyy1 - 22554*pcb1 - 46802*pcr1) >> 8
	pbu := (pyy1 + 116130*pcb1) >> 8
	if pru < 0 {
		pru = 0
	} else if pru > 0xffff {
		pru = 0xffff
	}
	if pgu < 0 {
		pgu = 0
	} else if pgu > 0xffff {
		pgu = 0xffff
	}
	if pbu < 0 {
		pbu = 0
	} else if pbu > 0xffff {
		pbu = 0xffff
	}
	return uint32(pru), uint32(pgu), uint32(pbu)
}

// clamp16 holds v to the range of 16 bits, as ycbcrToRGB16 does.
func clamp16(v int) int {
	if v < 0 {
		return 0
	}
	if v > 0xffff {
		return 0xffff
	}
	return v
}

// constAlpha reports whether x/image's fast path for this source type keeps
// the intermediate alpha fixed at 1 (Gray, and YCbCr 444/422/420/440).
func constAlpha(m image.Image) bool {
	switch s := m.(type) {
	case *image.Gray:
		return true
	case *image.YCbCr:
		switch s.SubsampleRatio {
		case image.YCbCrSubsampleRatio444, image.YCbCrSubsampleRatio422,
			image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio440:
			return true
		}
	}
	return false
}

// readRow converts row y of strip m to the 16-bit premultiplied values
// (as float64, which is how the horizontal pass uses them) x/image's horizontal pass reads, following its type dispatch
// (Gray, NRGBA, RGBA, YCbCr, RGBA64Image, then At).
func readRow(m image.Image, y int, out [][4]float64) {
	b := m.Bounds()
	x0, sw := b.Min.X, len(out)
	switch s := m.(type) {
	case *image.Gray:
		base := (y-s.Rect.Min.Y)*s.Stride + x0 - s.Rect.Min.X
		for x, p := range s.Pix[base : base+sw] {
			v := float64(uint32(p) * 0x101)
			out[x] = [4]float64{v, v, v, 0}
		}
		return
	case *image.NRGBA:
		base := (y-s.Rect.Min.Y)*s.Stride + (x0-s.Rect.Min.X)*4
		pix := s.Pix[base : base+sw*4]
		for x := range out {
			p := pix[x*4 : x*4+4 : x*4+4]
			pau := uint32(p[3]) * 0x101
			out[x] = [4]float64{
				float64(uint32(p[0]) * pau / 0xff),
				float64(uint32(p[1]) * pau / 0xff),
				float64(uint32(p[2]) * pau / 0xff),
				float64(pau),
			}
		}
		return
	case *image.RGBA:
		base := (y-s.Rect.Min.Y)*s.Stride + (x0-s.Rect.Min.X)*4
		pix := s.Pix[base : base+sw*4]
		for x := range out {
			p := pix[x*4 : x*4+4 : x*4+4]
			out[x] = [4]float64{
				float64(uint32(p[0]) * 0x101),
				float64(uint32(p[1]) * 0x101),
				float64(uint32(p[2]) * 0x101),
				float64(uint32(p[3]) * 0x101),
			}
		}
		return
	case *image.YCbCr:
		if constAlpha(s) {
			// The offsets are YOffset / COffset with the row part hoisted.
			yrow := s.Y[s.YOffset(x0, y):]
			cb, cr := s.Cb[s.COffset(s.Rect.Min.X, y):], s.Cr[s.COffset(s.Rect.Min.X, y):]
			switch s.SubsampleRatio {
			case image.YCbCrSubsampleRatio420, image.YCbCrSubsampleRatio422:
				// Two pixels side by side share their chroma, and with it
				// most of the arithmetic of ycbcrToRGB16: the terms that
				// depend on the chroma are worked out once for the pair.
				c0 := s.Rect.Min.X / 2
				x := 0
				if x0&1 == 1 && sw > 0 { // the second pixel of a pair comes first
					pj := x0/2 - c0
					r, g, b := ycbcrToRGB16(yrow[0], cb[pj], cr[pj])
					out[0] = [4]float64{float64(r), float64(g), float64(b), 0}
					x = 1
				}
				for ; x+1 < sw; x += 2 {
					pj := (x0+x)/2 - c0
					pcb1, pcr1 := int(cb[pj])-128, int(cr[pj])-128
					ra, ga, ba := 91881*pcr1, -22554*pcb1-46802*pcr1, 116130*pcb1
					for k := x; k < x+2; k++ {
						pyy1 := int(yrow[k]) * 0x10101
						out[k] = [4]float64{float64(clamp16((pyy1 + ra) >> 8)), float64(clamp16((pyy1 + ga) >> 8)), float64(clamp16((pyy1 + ba) >> 8)), 0}
					}
				}
				if x < sw {
					pj := (x0+x)/2 - c0
					r, g, b := ycbcrToRGB16(yrow[x], cb[pj], cr[pj])
					out[x] = [4]float64{float64(r), float64(g), float64(b), 0}
				}
			default: // 444, 440
				c0 := x0 - s.Rect.Min.X
				for x := range out {
					r, g, b := ycbcrToRGB16(yrow[x], cb[c0+x], cr[c0+x])
					out[x] = [4]float64{float64(r), float64(g), float64(b), 0}
				}
			}
			return
		}
		// Other subsampling ratios take x/image's generic At path.
		for x := range out {
			r, g, b, a := m.At(x0+x, y).RGBA()
			out[x] = [4]float64{float64(r), float64(g), float64(b), float64(a)}
		}
		return
	case *image.Paletted:
		// Same values as the RGBA64Image case below, with each palette
		// entry converted once per row instead of once per pixel.
		var lut [256][4]float64
		n := min(len(s.Palette), 256)
		for i, c := range s.Palette[:n] {
			r, g, b, a := c.RGBA()
			lut[i] = [4]float64{float64(r & 0xffff), float64(g & 0xffff), float64(b & 0xffff), float64(a & 0xffff)}
		}
		base := (y-s.Rect.Min.Y)*s.Stride + x0 - s.Rect.Min.X
		for x, p := range s.Pix[base : base+sw] {
			if int(p) >= n { // let the standard path decide (it panics)
				pu := s.RGBA64At(x0+x, y)
				out[x] = [4]float64{float64(pu.R), float64(pu.G), float64(pu.B), float64(pu.A)}
				continue
			}
			out[x] = lut[p]
		}
		return
	case image.RGBA64Image:
		for x := range out {
			pu := s.RGBA64At(x0+x, y)
			out[x] = [4]float64{float64(pu.R), float64(pu.G), float64(pu.B), float64(pu.A)}
		}
		return
	}
	for x := range out {
		r, g, b, a := m.At(x0+x, y).RGBA()
		out[x] = [4]float64{float64(r), float64(g), float64(b), float64(a)}
	}
}

// readRowGray is readRow for a greyscale source: the one value x/image
// reads into each of red, green and blue.
func readRowGray(m image.Image, y int, out []float64) {
	b := m.Bounds()
	x0, sw := b.Min.X, len(out)
	if s, ok := m.(*image.Gray); ok {
		base := (y-s.Rect.Min.Y)*s.Stride + x0 - s.Rect.Min.X
		for x, p := range s.Pix[base : base+sw] {
			out[x] = float64(uint32(p) * 0x101)
		}
		return
	}
	for x := range out {
		r, _, _, _ := m.At(x0+x, y).RGBA()
		out[x] = float64(r)
	}
}

// open is a destination row still receiving source rows.
type open struct {
	acc []([4]float64)
	k   int32 // next contrib index
	// For a greyscale source: one value per pixel, and the alpha every
	// pixel of the row has in common.
	g  []float64
	pa float64
}

type scaler struct {
	src    pipeline.Source
	env    *pipeline.Env
	rows   *pipeline.RowReader
	sb     image.Rectangle
	dw, dh int
	hz, vt distrib
	first  []int32
	row    [][4]float64
	tmp    [][4]float64
	opens  []open // destination rows [dLo, dLo+len(opens))
	free   [][][4]float64
	dLo    int // next destination row to emit
	sy     int // next source row (relative to sb.Min.Y)
	band   *image.RGBA
	// A greyscale source is scaled as one channel and stays greyscale:
	// the values are the ones the red, green and blue of the four-channel
	// path would all get, at a quarter of the memory and the work.
	gray       bool
	grow       []float64
	gtmp       []float64
	gfree      [][]float64
	gband      *image.Gray
	n          int
	reserved   int64
	constAlpha bool
	started    bool
	done       bool
	err        error
}

// Scale returns a Source holding src scaled to dw × dh with kernel q (for
// example draw.CatmullRom): *image.Gray strips for a greyscale source,
// *image.RGBA strips for every other.
func Scale(src pipeline.Source, dw, dh int, q *draw.Kernel) (pipeline.Source, error) {
	b := src.Bounds()
	return ScaleExtent(src, dw, dh, q, float64(b.Dx()), float64(b.Dy()))
}

// ScaleExtent is Scale for a source whose last column and row may stand for
// less than a full pixel: the result spans ew × eh source pixels (at most
// the size of src) instead of all of them. It is used after Shrink, where
// ew × eh is the original size divided by the shrink factor.
func ScaleExtent(src pipeline.Source, dw, dh int, q *draw.Kernel, ew, eh float64) (pipeline.Source, error) {
	sb := src.Bounds()
	if !(ew > 0 && ew <= float64(sb.Dx()) && eh > 0 && eh <= float64(sb.Dy())) {
		return nil, errors.New("goimgtools: resize extent outside the image")
	}
	if dw <= 0 || dh <= 0 || sb.Dx() <= 0 || sb.Dy() <= 0 {
		return nil, errors.New("goimgtools: resize with an empty size")
	}
	if dw > math.MaxInt32/8 || dh > math.MaxInt32/8 || sb.Dx() > math.MaxInt32/8 || sb.Dy() > math.MaxInt32/8 {
		return nil, errors.New("goimgtools: resize dimensions too large")
	}
	s := &scaler{src: src, env: pipeline.EnvOf(src), sb: sb, dw: dw, dh: dh}
	s.gray = src.ColorModel() == color.GrayModel
	// Bytes per pixel: of a strip, and of a row of sums.
	bpp, fpp := int64(4), int64(32)
	if s.gray {
		bpp, fpp = 1, 8
	}
	s.n = pipeline.StripRows(int64(dw)*bpp, dh)

	// Weight tables: 16 bytes per contribution, 32 per destination column/row.
	kw := func(d, sdim int) int64 {
		per := 2*q.Support*math.Max(1, float64(sdim)/float64(d)) + 2
		return int64(float64(d)*per)*16 + int64(d)*32
	}
	need := kw(dw, sb.Dx()) + kw(dh, sb.Dy()) +
		int64(sb.Dx())*fpp + int64(dw)*fpp + int64(dw)*bpp*int64(s.n) + int64(dh)*8
	if err := s.env.Budget.Reserve(need, "resize tables"); err != nil {
		return nil, err
	}
	s.reserved = need
	s.hz = newDistrib(q, int32(dw), int32(sb.Dx()), ew)
	s.vt = newDistrib(q, int32(dh), int32(sb.Dy()), eh)

	// first[d] is the source row at which destination row d must be open:
	// the earliest first contribution of d or any later row. (Zero weights
	// are dropped from the tables, so first contributions are not strictly
	// ordered by row.)
	s.first = make([]int32, dh)
	next := int32(math.MaxInt32)
	for d := dh - 1; d >= 0; d-- {
		if v := s.vt.sources[d]; v.i != v.j {
			next = min(next, s.vt.contribs[v.i].coord)
		}
		s.first[d] = next
	}
	// The most destination rows open at once decides the accumulator memory.
	// Rows are emitted in order, so a row stays open until every earlier row
	// has its last contribution.
	maxOpen, lo, lastMax := 1, 0, int32(-1)
	lasts := make([]int32, dh) // running max of last contributions
	for d := range lasts {
		if v := s.vt.sources[d]; v.i != v.j {
			lastMax = max(lastMax, s.vt.contribs[v.j-1].coord)
		}
		lasts[d] = lastMax
	}
	for d := 0; d < dh; d++ {
		for lo < d && lasts[lo] < s.first[d] {
			lo++
		}
		maxOpen = max(maxOpen, d-lo+1)
	}
	accBytes := int64(maxOpen) * int64(dw) * fpp
	if err := s.env.Budget.Reserve(accBytes, "resize accumulators"); err != nil {
		s.env.Budget.Release(s.reserved)
		return nil, err
	}
	s.reserved += accBytes

	if s.gray {
		s.grow = make([]float64, sb.Dx())
		s.gtmp = make([]float64, dw)
		s.gband = image.NewGray(image.Rect(0, 0, dw, s.n))
	} else {
		s.row = make([][4]float64, sb.Dx())
		s.tmp = make([][4]float64, dw)
		s.band = image.NewRGBA(image.Rect(0, 0, dw, s.n))
	}
	s.rows = pipeline.NewRowReader(src)
	return s, nil
}

func (s *scaler) Env() *pipeline.Env      { return s.env }
func (s *scaler) Bounds() image.Rectangle { return image.Rect(0, 0, s.dw, s.dh) }
func (s *scaler) ColorModel() color.Model {
	if s.gray {
		return color.GrayModel
	}
	return color.RGBAModel
}

// Opaque reports whether the result has no transparency. An opaque source
// stays opaque: the alpha sums differ from 1.0 only by float rounding, far
// less than the half step that would round to 254.
func (s *scaler) Opaque() bool { return pipeline.IsOpaque(s.src) }

func (s *scaler) Close() error {
	if s.reserved != 0 {
		s.env.Budget.Release(s.reserved)
		s.reserved = 0
	}
	if s.err == nil {
		s.err = errors.New("goimgtools: resize closed")
	}
	return s.src.Close()
}

// openRow opens the next destination row, starting at contribution k.
func (s *scaler) openRow(k int32) {
	o := open{k: k}
	switch {
	case s.gray && len(s.gfree) > 0:
		o.g = s.gfree[len(s.gfree)-1]
		s.gfree = s.gfree[:len(s.gfree)-1]
		clear(o.g)
	case s.gray:
		o.g = make([]float64, s.dw)
	case len(s.free) > 0:
		o.acc = s.free[len(s.free)-1]
		s.free = s.free[:len(s.free)-1]
		clear(o.acc)
	default:
		o.acc = make([][4]float64, s.dw)
	}
	s.opens = append(s.opens, o)
}

// feed scales source row s.sy horizontally and adds it to the open rows.
func (s *scaler) feed() error {
	y := int32(s.sy)
	// Open the destination rows whose first contribution is at or before y.
	for d := s.dLo + len(s.opens); d < s.dh; d++ {
		if s.first[d] > y {
			break
		}
		s.openRow(s.vt.sources[d].i)
	}
	used := false
	for i := range s.opens {
		o := &s.opens[i]
		if o.k < s.vt.sources[s.dLo+i].j && s.vt.contribs[o.k].coord == y {
			used = true
			break
		}
	}
	strip, err := s.rows.Row(s.sb.Min.Y + s.sy)
	if err != nil {
		return err
	}
	s.sy++
	if !used {
		return nil
	}
	if s.gray {
		s.feedGray(strip, y)
		return nil
	}
	if !s.started {
		s.started, s.constAlpha = true, constAlpha(strip)
	}
	readRow(strip, s.sb.Min.Y+int(y), s.row)

	// Horizontal pass: x/image's scaleX_*.
	row, tmp, ca := s.row, s.tmp, s.constAlpha
	for x, h := range s.hz.sources {
		var pr, pg, pb, pa float64
		if ca {
			for _, c := range s.hz.contribs[h.i:h.j] {
				p := &row[c.coord]
				pr += float64(p[0] * c.weight)
				pg += float64(p[1] * c.weight)
				pb += float64(p[2] * c.weight)
			}
			tmp[x] = [4]float64{pr * h.invTotalWeightFFFF, pg * h.invTotalWeightFFFF, pb * h.invTotalWeightFFFF, 1}
		} else {
			for _, c := range s.hz.contribs[h.i:h.j] {
				p := &row[c.coord]
				pr += float64(p[0] * c.weight)
				pg += float64(p[1] * c.weight)
				pb += float64(p[2] * c.weight)
				pa += float64(p[3] * c.weight)
			}
			tmp[x] = [4]float64{pr * h.invTotalWeightFFFF, pg * h.invTotalWeightFFFF, pb * h.invTotalWeightFFFF, pa * h.invTotalWeightFFFF}
		}
	}

	// Vertical pass, one source row's worth: the additions each destination
	// pixel receives arrive in the same order as in x/image's scaleY loop.
	for i := range s.opens {
		o := &s.opens[i]
		if o.k >= s.vt.sources[s.dLo+i].j {
			continue
		}
		c := s.vt.contribs[o.k]
		if c.coord != y {
			continue
		}
		o.k++
		w, acc := c.weight, o.acc[:len(tmp)]
		for x := range tmp {
			p, a := &tmp[x], &acc[x]
			a[0] += float64(p[0] * w)
			a[1] += float64(p[1] * w)
			a[2] += float64(p[2] * w)
			a[3] += float64(p[3] * w)
		}
	}
	return nil
}

// feedGray is the two passes of feed for one channel. Each sum is made
// of the same terms in the same order as the red channel of feed, and the
// alpha of the row is the sum feed makes for every pixel alike, so the
// results are the same to the last bit.
func (s *scaler) feedGray(strip image.Image, y int32) {
	readRowGray(strip, s.sb.Min.Y+int(y), s.grow)
	row, tmp := s.grow, s.gtmp
	for x, h := range s.hz.sources {
		var p float64
		for _, c := range s.hz.contribs[h.i:h.j] {
			p += float64(row[c.coord] * c.weight)
		}
		tmp[x] = p * h.invTotalWeightFFFF
	}
	for i := range s.opens {
		o := &s.opens[i]
		if o.k >= s.vt.sources[s.dLo+i].j {
			continue
		}
		c := s.vt.contribs[o.k]
		if c.coord != y {
			continue
		}
		o.k++
		w, acc := c.weight, o.g[:len(tmp)]
		for x, p := range tmp {
			acc[x] += float64(p * w)
		}
		o.pa += float64(1 * w)
	}
}

// emit writes destination row s.dLo (x/image's scaleY_RGBA_Src) into dst.
func (s *scaler) emit(dst []uint8) {
	o := s.opens[0]
	inv := s.vt.sources[s.dLo].invTotalWeight
	if s.gray {
		pa := o.pa
		for x, pr := range o.g {
			if pr > pa {
				pr = pa
			}
			dst[x] = uint8(ftou(pr*inv) >> 8)
		}
		s.gfree = append(s.gfree, o.g)
		copy(s.opens, s.opens[1:])
		s.opens = s.opens[:len(s.opens)-1]
		s.dLo++
		return
	}
	for x := range o.acc {
		p := &o.acc[x]
		pr, pg, pb, pa := p[0], p[1], p[2], p[3]
		if pr > pa {
			pr = pa
		}
		if pg > pa {
			pg = pa
		}
		if pb > pa {
			pb = pa
		}
		d := dst[x*4 : x*4+4 : x*4+4]
		d[0] = uint8(ftou(pr*inv) >> 8)
		d[1] = uint8(ftou(pg*inv) >> 8)
		d[2] = uint8(ftou(pb*inv) >> 8)
		d[3] = uint8(ftou(pa*inv) >> 8)
	}
	s.free = append(s.free, o.acc)
	copy(s.opens, s.opens[1:])
	s.opens = s.opens[:len(s.opens)-1]
	s.dLo++
}

func (s *scaler) Next() (image.Image, error) {
	if s.done {
		return nil, io.EOF
	}
	if s.err != nil {
		return nil, s.err
	}
	y0, y1 := s.dLo, min(s.dLo+s.n, s.dh)
	rect := image.Rect(0, y0, s.dw, y1)
	var strip image.Image
	var pix []uint8
	stride := s.dw * 4
	if s.gray {
		g := pipeline.Relabel(s.gband, rect).(*image.Gray)
		strip, pix, stride = g, g.Pix, g.Stride
	} else {
		c := pipeline.Relabel(s.band, rect).(*image.RGBA)
		strip, pix, stride = c, c.Pix, c.Stride
	}
	rowBytes := s.dw * 4
	if s.gray {
		rowBytes = s.dw
	}
	for s.dLo < y1 {
		// The first open row is complete once all its contributions are in.
		if len(s.opens) > 0 && s.opens[0].k >= s.vt.sources[s.dLo].j {
			off := (s.dLo - y0) * stride
			s.emit(pix[off : off+rowBytes])
			continue
		}
		if s.sy >= s.sb.Dy() {
			if len(s.opens) == 0 { // a row with no contributions at the very end
				s.openRow(s.vt.sources[s.dLo].i)
				continue
			}
			s.err = errors.New("goimgtools: resize ran out of source rows")
			return nil, s.err
		}
		if err := s.feed(); err != nil {
			s.err = err
			return nil, err
		}
	}
	if y1 == s.dh {
		// Let the decoder verify the rest of its input before the last strip.
		if err := s.rows.Drain(); err != nil {
			s.err = err
			return nil, err
		}
		s.done = true
	}
	return strip, nil
}
