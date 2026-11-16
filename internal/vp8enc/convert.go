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

// converter turns rows of the source into macroblock rows of Y, U and V
// samples, padded to whole macroblocks by repeating the edges, in the
// buffers given to into; flush is called when a macroblock row is
// complete in them.
//
// Sources of Y'CbCr or gray are converted sample by sample. Colour
// sources are converted with sharp YUV (sharp.go) when sharp is set, which
// hands out each pair of rows eight rows after it took it in.
type converter struct {
	b     image.Rectangle
	w, h  int // image size
	w16   int // width padded to a multiple of 16
	y     []uint8
	u, v  []uint8
	flush func() error
	rows  [2][]uint8  // two rows of non-premultiplied RGBA, padded to even width
	alpha *alphaSpool // receives the alpha rows, or nil
	sharp *sharpYUV   // or nil
	arow  []uint8
	// With alpha: the last alphaRing rows of it, for cleanTransparent.
	aring   []uint8
	arows   int
	noClean bool // test hook: keep the samples of transparent pixels
	acc     []uint16
	pal     [256][4]uint8
	palSrc  color.Palette
}

func converterBytes(w int, alpha bool) int64 {
	n := 2*int64(w+1)*4 + 2*int64(w+1)
	if alpha {
		n += int64(w) + alphaSpoolBytes(w) + alphaRing*int64(w)
	}
	return n
}

// alphaRing is how many rows of alpha the converter keeps: those of the
// macroblock row it completes, which sharp YUV hands out up to ten rows
// after it took them in.
const alphaRing = 32

// useSharp tells whether a source whose first strip is m is converted
// with sharp YUV: one of colours, not of Y'CbCr (whose chroma is kept as
// it is) or gray.
func useSharp(m image.Image) bool {
	switch m.(type) {
	case *image.YCbCr, *image.NYCbCrA, *image.Gray, *image.Gray16:
		return false
	}
	return true
}

func newConverter(b image.Rectangle, w16 int, alpha *alphaSpool) *converter {
	w := b.Dx()
	c := &converter{b: b, w: w, h: b.Dy(), w16: w16, alpha: alpha}
	c.rows[0] = make([]uint8, (w+1)*4)
	c.rows[1] = make([]uint8, (w+1)*4)
	if alpha != nil {
		c.arow = make([]uint8, w)
		c.aring = make([]uint8, alphaRing*w)
	}
	return c
}

// into makes the macroblock rows go to y, u and v, and flush be called
// when one is complete there.
func (c *converter) into(y, u, v []uint8, flush func() error) {
	c.y, c.u, c.v, c.flush = y, u, v, flush
}

// row converts row y of the image, which lies in strip m. Chroma comes
// from pairs of rows, so an even row is only kept until its partner
// arrives (the two may be in different strips).
func (c *converter) row(m image.Image, y int) error {
	k := (y - c.b.Min.Y) & 15
	last := y == c.b.Max.Y-1
	if c.sharp != nil {
		return c.sharpRow(m, y, k, last)
	}
	if err := c.direct(m, y, k, last); err != nil {
		return err
	}
	if k == 15 || last {
		c.cleanTransparent(y-c.b.Min.Y-k, k+1)
		c.pad(k + 1)
		return c.flush()
	}
	return nil
}

// sharpRow takes row y in, and hands its pair to sharp YUV once both rows
// are in (the bottom row twice when the height is odd).
func (c *converter) sharpRow(m image.Image, y, k int, last bool) error {
	if pm, ok := m.(*image.Paletted); ok {
		c.loadPalette(pm.Palette, k == 0)
	}
	cur := c.rows[k&1]
	c.fetch(m, y, cur)
	if err := c.alphaOf(cur); err != nil {
		return err
	}
	if k&1 == 0 && !last {
		return nil
	}
	r0 := c.rows[0]
	if k&1 == 0 {
		r0 = cur
	}
	if c.alpha != nil {
		unbleed(r0, cur, c.w)
	}
	return c.sharp.push(r0, cur, last, c.put)
}

// put collects pair p from sharp YUV into the macroblock row, and flushes
// the row when p completes it.
func (c *converter) put(p int) error {
	k := 2 * p & 15
	cw, cw16 := (c.w+1)/2, c.w16/2
	y0 := c.y[k*c.w16 : k*c.w16+c.w]
	y1 := c.y[(k+1)*c.w16 : (k+1)*c.w16+c.w]
	o := k / 2 * cw16
	c.sharp.yuv(p, y0, y1, c.u[o:o+cw], c.v[o:o+cw])
	if bottom := 2*p+2 >= c.h; k == 14 || bottom {
		top := 2 * p &^ 15
		c.cleanTransparent(top, min(16, c.h-top))
		c.pad(min(16, c.h-top))
		return c.flush()
	}
	return nil
}

// unbleed gives the invisible pixels of each 2x2 block of two rows of RGBA
// the colour of the visible ones, weighted by their alpha, so that sharp
// YUV, which does not look at alpha, does not mix the colour of invisible
// pixels into visible ones (the plain conversion weights by alpha).
func unbleed(r0, r1 []uint8, w int) {
	for x := 0; x < w; x += 2 {
		p, q := r0[4*x:4*x+8], r1[4*x:4*x+8]
		px := [4][]uint8{p[:4], p[4:], q[:4], q[4:]}
		var sum [3]int32
		var a, zero int32
		for _, c := range px {
			if c[3] == 0 {
				zero++
			}
			a += int32(c[3])
			for i := range sum {
				sum[i] += int32(c[i]) * int32(c[3])
			}
		}
		if zero == 0 || a == 0 {
			continue
		}
		for _, c := range px {
			if c[3] == 0 {
				for i := range sum {
					c[i] = uint8((sum[i] + a/2) / a)
				}
			}
		}
	}
}

// direct converts row y, which is row k (0..15) of its macroblock row,
// sample by sample; last tells that y is the image's bottom row.
func (c *converter) direct(m image.Image, y, k int, last bool) error {
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
	k := c.arows % alphaRing
	copy(c.aring[k*c.w:(k+1)*c.w], a)
	c.arows++
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

// cleanTransparent changes, in the n rows of the macroblock row from image
// row top, the samples of pixels that cannot be seen, so that they cost
// fewer bits, as libwebp does unless asked to keep them
// (src/enc/picture_tools_enc.c: WebPCleanupTransparentArea): in a block of
// 8x8 pixels that is partly transparent, the luma of the transparent ones
// becomes the mean of the others'; a block that is wholly transparent is
// made flat, luma and chroma, with the values of the first such block of
// a run along the row of blocks, so that a run codes as one. Blocks cut
// by the right or bottom edge are only evened out.
func (c *converter) cleanTransparent(top, n int) {
	if c.alpha == nil || c.noClean {
		return
	}
	w, w16, cw16 := c.w, c.w16, c.w16/2
	alpha := func(y int) []uint8 {
		k := (top + y) % alphaRing
		return c.aring[k*w : (k+1)*w]
	}
	for by := 0; by < n; by += 8 {
		bh := min(8, n-by)
		run := false
		var vy, vu, vv uint8
		for bx := 0; bx < w; bx += 8 {
			bw := min(8, w-bx)
			sum, count := 0, 0
			for y := by; y < by+bh; y++ {
				a := alpha(y)[bx : bx+bw]
				l := c.y[y*w16+bx : y*w16+bx+bw]
				for x, v := range a {
					if v != 0 {
						count++
						sum += int(l[x])
					}
				}
			}
			switch {
			case count > 0 && count < bw*bh:
				avg := uint8(sum / count)
				for y := by; y < by+bh; y++ {
					a := alpha(y)[bx : bx+bw]
					l := c.y[y*w16+bx : y*w16+bx+bw]
					for x, v := range a {
						if v == 0 {
							l[x] = avg
						}
					}
				}
			case count == 0 && bw == 8 && bh == 8:
				if !run {
					vy = c.y[by*w16+bx]
					vu, vv = c.u[by/2*cw16+bx/2], c.v[by/2*cw16+bx/2]
					run = true
				}
				for y := by; y < by+8; y++ {
					l := c.y[y*w16+bx : y*w16+bx+8]
					for x := range l {
						l[x] = vy
					}
				}
				for y := by / 2; y < by/2+4; y++ {
					u := c.u[y*cw16+bx/2 : y*cw16+bx/2+4]
					v := c.v[y*cw16+bx/2 : y*cw16+bx/2+4]
					for x := range u {
						u[x], v[x] = vu, vv
					}
				}
				continue
			}
			run = false
		}
	}
}
