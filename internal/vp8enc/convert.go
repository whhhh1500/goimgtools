package vp8enc

import (
	"image"
	"image/color"
)

// VP8 stores BT.601 limited-range Y'CbCr with 4:2:0 chroma. The RGB
// conversion below uses libwebp's constants (src/dsp/yuv.h), so a decoder's
// inverse conversion gives back the source colours.

const yuvFix = 16

func rgbToY(r, g, b int32) uint8 {
	return uint8((16839*r + 33059*g + 6420*b + 16<<yuvFix + 1<<(yuvFix-1)) >> yuvFix)
}

// rgbToU and rgbToV take the sums of four pixels.
func rgbToU(r, g, b int32) uint8 {
	return clip8((-9719*r - 19081*g + 28800*b + 128<<(yuvFix+2) + 1<<(yuvFix+1)) >> (yuvFix + 2))
}

func rgbToV(r, g, b int32) uint8 {
	return clip8((28800*r - 24116*g - 4684*b + 128<<(yuvFix+2) + 1<<(yuvFix+1)) >> (yuvFix + 2))
}

// Full-range (JFIF) Y'CbCr, as image.YCbCr holds it, uses the same BT.601
// matrix, so it maps to limited range one sample at a time.
var jfifY, jfifC [256]uint8

func init() {
	for i := range jfifY {
		jfifY[i] = uint8(16 + (i*219*2+255)/(255*2))
		c := (i-128)*224*2 + 128*255*2 + 255
		jfifC[i] = uint8(c / (255 * 2))
	}
}

// converter turns rows of the source into one macroblock row of Y, U and V
// samples, padded to whole macroblocks by repeating the edges.
type converter struct {
	b      image.Rectangle
	w      int // image width
	w16    int // width padded to a multiple of 16
	y      []uint8
	u, v   []uint8
	rows   [2][]uint8  // two rows of non-premultiplied RGBA, padded to even width
	alpha  *alphaSpool // receives the alpha rows, or nil
	arow   []uint8
	acc    []uint16
	pal    [256][4]uint8
	palSrc color.Palette
}

func converterBytes(w, w16 int, alpha bool) int64 {
	n := int64(w16)*16 + int64(w16/2)*8*2 + 2*int64(w+1)*4 + 2*int64(w+1)
	if alpha {
		n += int64(w) + alphaSpoolBytes(w)
	}
	return n
}

func newConverter(b image.Rectangle, w16 int, alpha *alphaSpool) *converter {
	w := b.Dx()
	c := &converter{b: b, w: w, w16: w16, alpha: alpha}
	c.y = make([]uint8, w16*16)
	c.u = make([]uint8, w16/2*8)
	c.v = make([]uint8, w16/2*8)
	c.rows[0] = make([]uint8, (w+1)*4)
	c.rows[1] = make([]uint8, (w+1)*4)
	if alpha != nil {
		c.arow = make([]uint8, w)
	}
	return c
}

// row converts row y of the image, which is row k (0..15) of the current
// macroblock row and lies in strip m. last tells that y is the image's
// bottom row. Chroma comes from pairs of rows, so an even row is only kept
// until its partner arrives (the two may be in different strips).
func (c *converter) row(m image.Image, y, k int, last bool) error {
	x0 := c.b.Min.X
	switch p := m.(type) {
	case *image.YCbCr:
		c.ycbcrRow(p, y, k, last)
		return c.opaqueAlpha()
	case *image.NYCbCrA:
		c.ycbcrRow(&p.YCbCr, y, k, last)
		o := p.AOffset(x0, y)
		return c.putAlpha(p.A[o : o+c.w])
	case *image.Gray:
		o := p.PixOffset(x0, y)
		d := c.y[k*c.w16 : k*c.w16+c.w]
		for x, v := range p.Pix[o : o+c.w] {
			d[x] = jfifY[v]
		}
		c.grayChroma(k)
		return c.opaqueAlpha()
	case *image.Gray16:
		o := p.PixOffset(x0, y)
		d := c.y[k*c.w16 : k*c.w16+c.w]
		for x := range d {
			d[x] = jfifY[p.Pix[o+2*x]]
		}
		c.grayChroma(k)
		return c.opaqueAlpha()
	}
	if pm, ok := m.(*image.Paletted); ok {
		c.loadPalette(pm.Palette, k == 0)
	}
	cur := c.rows[k&1]
	c.fetch(m, y, cur)
	if k&1 == 1 {
		c.rgbRows(k-1, c.rows[0], cur)
	} else if last {
		c.rgbRows(k, cur, cur)
	}
	return c.alphaOf(cur)
}

func (c *converter) opaqueAlpha() error {
	if c.alpha == nil {
		return nil
	}
	for i := range c.arow {
		c.arow[i] = 0xff
	}
	return c.putAlpha(c.arow)
}

func (c *converter) putAlpha(a []uint8) error {
	if c.alpha == nil {
		return nil
	}
	return c.alpha.add(a)
}

// alphaOf spools the alpha channel of a row of RGBA pixels.
func (c *converter) alphaOf(row []uint8) error {
	if c.alpha == nil {
		return nil
	}
	for x := range c.arow {
		c.arow[x] = row[4*x+3]
	}
	return c.putAlpha(c.arow)
}

func (c *converter) grayChroma(k int) {
	if k != 0 {
		return
	}
	for i := range c.u {
		c.u[i], c.v[i] = 0x80, 0x80
	}
}

// pad repeats the last column and the last row out to whole macroblocks.
// n is the number of rows that were converted.
func (c *converter) pad(n int) {
	w, w16 := c.w, c.w16
	if w < w16 {
		for y := 0; y < n; y++ {
			row := c.y[y*w16 : (y+1)*w16]
			v := row[w-1]
			for x := w; x < w16; x++ {
				row[x] = v
			}
		}
	}
	for y := n; y < 16; y++ {
		copy(c.y[y*w16:(y+1)*w16], c.y[(n-1)*w16:n*w16])
	}
	cw, cw16, cn := (w+1)/2, w16/2, (n+1)/2
	for _, pl := range [2][]uint8{c.u, c.v} {
		if cw < cw16 {
			for y := 0; y < cn; y++ {
				row := pl[y*cw16 : (y+1)*cw16]
				v := row[cw-1]
				for x := cw; x < cw16; x++ {
					row[x] = v
				}
			}
		}
		for y := cn; y < 8; y++ {
			copy(pl[y*cw16:(y+1)*cw16], pl[(cn-1)*cw16:cn*cw16])
		}
	}
}

// ycbcrRow converts one row of full-range Y'CbCr of any subsampling.
func (c *converter) ycbcrRow(p *image.YCbCr, y, k int, last bool) {
	w, w16, x0 := c.w, c.w16, c.b.Min.X
	o := p.YOffset(x0, y)
	d := c.y[k*w16 : k*w16+w]
	for x, v := range p.Y[o : o+w] {
		d[x] = jfifY[v]
	}
	cw, cw16 := (w+1)/2, w16/2
	du, dv := c.u[k/2*cw16:k/2*cw16+cw], c.v[k/2*cw16:k/2*cw16+cw]
	// 4:2:0 on an even origin already has the chroma grid VP8 wants.
	if p.SubsampleRatio == image.YCbCrSubsampleRatio420 && x0&1 == 0 && c.b.Min.Y&1 == 0 {
		if k&1 == 0 {
			o := p.COffset(x0, y)
			cb, cr := p.Cb[o:o+cw], p.Cr[o:o+cw]
			for x := range du {
				du[x], dv[x] = jfifC[cb[x]], jfifC[cr[x]]
			}
		}
		return
	}
	// Otherwise average the chroma of the four pixels under each sample:
	// sum this row's pairs, and finish when the second row has been added.
	if c.acc == nil {
		c.acc = make([]uint16, 2*cw)
	}
	xmax := c.b.Max.X - 1
	for x := 0; x < cw; x++ {
		o0 := p.COffset(x0+2*x, y)
		o1 := p.COffset(min(x0+2*x+1, xmax), y)
		cb := uint16(p.Cb[o0]) + uint16(p.Cb[o1])
		cr := uint16(p.Cr[o0]) + uint16(p.Cr[o1])
		if k&1 == 0 {
			c.acc[2*x], c.acc[2*x+1] = cb, cr
			if last {
				du[x], dv[x] = jfifC[(cb+1)>>1], jfifC[(cr+1)>>1]
			}
		} else {
			du[x], dv[x] = jfifC[(c.acc[2*x]+cb+2)>>2], jfifC[(c.acc[2*x+1]+cr+2)>>2]
		}
	}
}

// loadPalette converts the palette of a strip. Between two macroblock rows
// (fresh) it is only done again when the strip brings another palette.
func (c *converter) loadPalette(pal color.Palette, fresh bool) {
	if !fresh && len(pal) == len(c.palSrc) && len(pal) > 0 && &pal[0] == &c.palSrc[0] {
		return
	}
	c.palSrc = pal
	for i := range c.pal {
		if i < len(pal) {
			c.pal[i] = nrgba8(pal[i].RGBA())
		} else {
			c.pal[i] = [4]uint8{0, 0, 0, 0xff}
		}
	}
}

// nrgba8 converts a premultiplied 16-bit colour to non-premultiplied 8-bit
// (the same arithmetic as color.NRGBAModel).
func nrgba8(r, g, b, a uint32) [4]uint8 {
	switch a {
	case 0xffff:
		return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xff}
	case 0:
		return [4]uint8{}
	}
	r = (r * 0xffff) / a
	g = (g * 0xffff) / a
	b = (b * 0xffff) / a
	return [4]uint8{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// fetch stores row y of m in dst as non-premultiplied 8-bit RGBA, repeating
// the last pixel when the width is odd.
func (c *converter) fetch(m image.Image, y int, dst []uint8) {
	w, x0 := c.w, c.b.Min.X
	switch p := m.(type) {
	case *image.NRGBA:
		o := p.PixOffset(x0, y)
		copy(dst, p.Pix[o:o+4*w])
	case *image.RGBA:
		o := p.PixOffset(x0, y)
		copy(dst, p.Pix[o:o+4*w])
		for i := 3; i < 4*w; i += 4 {
			if a := dst[i]; a != 0xff {
				px := nrgba8(uint32(dst[i-3])*0x101, uint32(dst[i-2])*0x101, uint32(dst[i-1])*0x101, uint32(a)*0x101)
				dst[i-3], dst[i-2], dst[i-1] = px[0], px[1], px[2]
			}
		}
	case *image.NRGBA64:
		o := p.PixOffset(x0, y)
		s := p.Pix[o : o+8*w]
		for x := 0; x < w; x++ {
			dst[4*x], dst[4*x+1], dst[4*x+2], dst[4*x+3] = s[8*x], s[8*x+2], s[8*x+4], s[8*x+6]
		}
	case *image.Paletted:
		o := p.PixOffset(x0, y)
		for x, i := range p.Pix[o : o+w] {
			copy(dst[4*x:4*x+4], c.pal[i][:])
		}
	default:
		for x := 0; x < w; x++ {
			px := nrgba8(m.At(x0+x, y).RGBA())
			copy(dst[4*x:4*x+4], px[:])
		}
	}
	copy(dst[4*w:4*w+4], dst[4*w-4:4*w])
}

// rgbRows converts two rows of RGBA (the same row twice at the bottom of an
// odd-height image) to rows y and y+1 of luma and row y/2 of chroma.
func (c *converter) rgbRows(y int, r0, r1 []uint8) {
	w, w16 := c.w, c.w16
	d0 := c.y[y*w16 : y*w16+w]
	for x := range d0 {
		d0[x] = rgbToY(int32(r0[4*x]), int32(r0[4*x+1]), int32(r0[4*x+2]))
	}
	if y+1 < 16 {
		d1 := c.y[(y+1)*w16 : (y+1)*w16+w]
		for x := range d1 {
			d1[x] = rgbToY(int32(r1[4*x]), int32(r1[4*x+1]), int32(r1[4*x+2]))
		}
	}
	cw, cw16 := (w+1)/2, w16/2
	du, dv := c.u[y/2*cw16:y/2*cw16+cw], c.v[y/2*cw16:y/2*cw16+cw]
	for x := range du {
		p, q := r0[8*x:8*x+8], r1[8*x:8*x+8]
		a := int32(p[3]) + int32(p[7]) + int32(q[3]) + int32(q[7])
		var r, g, b int32
		if a == 4*0xff || a == 0 {
			r = int32(p[0]) + int32(p[4]) + int32(q[0]) + int32(q[4])
			g = int32(p[1]) + int32(p[5]) + int32(q[1]) + int32(q[5])
			b = int32(p[2]) + int32(p[6]) + int32(q[2]) + int32(q[6])
		} else {
			// Weight by alpha, so that the colour of (nearly) invisible
			// pixels does not bleed into visible ones.
			a0, a1, a2, a3 := int32(p[3]), int32(p[7]), int32(q[3]), int32(q[7])
			r = (4*(int32(p[0])*a0+int32(p[4])*a1+int32(q[0])*a2+int32(q[4])*a3) + a/2) / a
			g = (4*(int32(p[1])*a0+int32(p[5])*a1+int32(q[1])*a2+int32(q[5])*a3) + a/2) / a
			b = (4*(int32(p[2])*a0+int32(p[6])*a1+int32(q[2])*a2+int32(q[6])*a3) + a/2) / a
		}
		du[x], dv[x] = rgbToU(r, g, b), rgbToV(r, g, b)
	}
}
