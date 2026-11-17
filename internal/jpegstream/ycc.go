// This file is derived from libjpeg-turbo's jdsample.c (fancy upsampling)
// and jdcolor.c (YCbCr to RGB), which are based on the work of the
// Independent JPEG Group: see licenses/LICENSE-LIBJPEG-TURBO.

package jpegstream

import (
	"encoding/binary"
	"image"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The YCbCr strips of a colour JPEG are turned into RGB as libjpeg-turbo
// does by default, and so Pillow and sharp, to the byte:
//
//   - Chroma halved across, down or both is brought back by its "fancy"
//     triangle filter: each sample three quarters of the nearer chroma
//     sample and a quarter of the next, with libjpeg's alternating
//     rounding; halved both ways, the two directions in one sum. Past the
//     edges of the chroma (its real size, not the padding of the last
//     blocks) the edge sample stands in. Chroma only two samples wide or
//     less, and chroma reduced by four, is repeated instead.
//   - Each pixel is converted with libjpeg's tables of 16-bit fixed-point
//     products, kept within 0 to 255.
//
// Down, a band's last row needs the first chroma row of the next band; it
// is held back and handed out at the start of the next strip.

// yccTab holds libjpeg's tables for YCbCr to RGB.
var yccTab = func() (t struct{ crR, cbB, crG, cbG [256]int32 }) {
	fix := func(x float64) int64 { return int64(float64(x*(1<<16)) + 0.5) }
	for i := range 256 {
		x := int64(i - 128)
		t.crR[i] = int32((fix(1.40200)*x + 1<<15) >> 16)
		t.cbB[i] = int32((fix(1.77200)*x + 1<<15) >> 16)
		t.crG[i] = int32(-fix(0.71414) * x)
		t.cbG[i] = int32(-fix(0.34414)*x + 1<<15)
	}
	return t
}()

// rangeLimit keeps v-384 within 0 to 255, indexed by v modulo 1024: the
// sums yccPair looks up are all within 384-180 and 384+435.
var rangeLimit = func() (t [1024]uint8) {
	for i := range t {
		t[i] = uint8(max(0, min(255, i-384)))
	}
	return t
}()

// yccRow converts a row of luma and of full-width chroma into RGBA.
func yccRow(dst, y, cb, cr []byte) {
	dst = dst[:4*len(y)]
	cb, cr = cb[:len(y)], cr[:len(y)]
	for x, l := range y {
		yccPair((*[4]byte)(dst[4*x:]), int32(l), cb[x], cr[x])
	}
}

// yccPair converts a pixel into d.
func yccPair(d *[4]byte, yy int32, b, r byte) {
	t := &yccTab
	yy += 384
	rr := rangeLimit[(yy+t.crR[r])&1023]
	gg := rangeLimit[(yy+(t.cbG[b]+t.crG[r])>>16)&1023]
	bb := rangeLimit[(yy+t.cbB[b])&1023]
	binary.LittleEndian.PutUint32(d[:], uint32(rr)|uint32(gg)<<8|uint32(bb)<<16|0xff000000)
}

// yccH2V2 is h2v2 of both chroma components (near the nearer rows, far the
// next ones; more than two samples) and yccRow, in one pass. At the ends
// the neighbour outside is the sample itself, which gives libjpeg's
// formulas for the first and last columns.
func yccH2V2(dst, y, cbN, cbF, crN, crF []byte) {
	w, n := len(y), len(cbN)
	dst = dst[:4*w]
	cbF, crN, crF = cbF[:n], crN[:n], crF[:n]
	thisB, thisR := int32(cbN[0])*3+int32(cbF[0]), int32(crN[0])*3+int32(crF[0])
	lastB, lastR := thisB, thisR
	for i := 0; i < n; i++ {
		nextB, nextR := thisB, thisR
		if i+1 < n {
			nextB, nextR = int32(cbN[i+1])*3+int32(cbF[i+1]), int32(crN[i+1])*3+int32(crF[i+1])
		}
		x := 2 * i
		yccPair((*[4]byte)(dst[4*x:]), int32(y[x]), byte((thisB*3+lastB+8)>>4), byte((thisR*3+lastR+8)>>4))
		if x+1 < w {
			yccPair((*[4]byte)(dst[4*x+4:]), int32(y[x+1]), byte((thisB*3+nextB+7)>>4), byte((thisR*3+nextR+7)>>4))
		}
		lastB, lastR, thisB, thisR = thisB, thisR, nextB, nextR
	}
}

// yccH2V1 is h2v1 of both chroma components (more than two samples) and
// yccRow, in one pass, as yccH2V2.
func yccH2V1(dst, y, cb, cr []byte) {
	w, n := len(y), len(cb)
	dst = dst[:4*w]
	cr = cr[:n]
	thisB, thisR := int32(cb[0]), int32(cr[0])
	lastB, lastR := thisB, thisR
	for i := 0; i < n; i++ {
		nextB, nextR := thisB, thisR
		if i+1 < n {
			nextB, nextR = int32(cb[i+1]), int32(cr[i+1])
		}
		x := 2 * i
		yccPair((*[4]byte)(dst[4*x:]), int32(y[x]), byte((thisB*3+lastB+1)>>2), byte((thisR*3+lastR+1)>>2))
		if x+1 < w {
			yccPair((*[4]byte)(dst[4*x+4:]), int32(y[x+1]), byte((thisB*3+nextB+2)>>2), byte((thisR*3+nextR+2)>>2))
		}
		lastB, lastR, thisB, thisR = thisB, thisR, nextB, nextR
	}
}

// rgbRow puts rows of red, green and blue together into RGBA.
func rgbRow(dst, r, g, b []byte) {
	dst = dst[:4*len(r)]
	g, b = g[:len(r)], b[:len(r)]
	for x, v := range r {
		d := dst[4*x : 4*x+4 : 4*x+4]
		d[0], d[1], d[2], d[3] = v, g[x], b[x], 255
	}
}

// upsampler brings the chroma of a band back to full size and holds what
// the next band needs.
type upsampler struct {
	rgb            bool // the components are red, green and blue: copied, not converted
	hdiv, vdiv     int
	w, h           int // the image
	cw, ch         int // the real chroma size
	fancyH, fancyV bool
	// The last luma row of the previous band, held back, and that band's
	// last chroma rows.
	held           bool
	heldY          []byte
	prevCb, prevCr []byte
	upCb, upCr     []byte // a row of upsampled chroma, 4·cw wide at most
	scratch        [][2][]byte
	env            *pipeline.Env // whose helpers share out the rows, if any
}

func newUpsampler(ratio image.YCbCrSubsampleRatio, w, h int, rgb bool, env *pipeline.Env) *upsampler {
	u := &upsampler{rgb: rgb, hdiv: 1, vdiv: 1, w: w, h: h, env: env}
	switch ratio {
	case image.YCbCrSubsampleRatio422:
		u.hdiv = 2
	case image.YCbCrSubsampleRatio420:
		u.hdiv, u.vdiv = 2, 2
	case image.YCbCrSubsampleRatio440:
		u.vdiv = 2
	case image.YCbCrSubsampleRatio411:
		u.hdiv = 4
	case image.YCbCrSubsampleRatio410:
		u.hdiv, u.vdiv = 4, 2
	}
	u.cw, u.ch = (w+u.hdiv-1)/u.hdiv, (h+u.vdiv-1)/u.vdiv
	u.fancyH = u.hdiv == 2 && u.cw > 2
	u.fancyV = u.vdiv == 2 && (u.hdiv == 1 || u.fancyH)
	n := 4 * u.cw
	u.heldY = make([]byte, w)
	u.prevCb, u.prevCr = make([]byte, u.cw), make([]byte, u.cw)
	u.upCb, u.upCr = make([]byte, max(n, w)), make([]byte, max(n, w))
	return u
}

// band converts the luma rows y0 to y1 of the band m (its first row being
// image row y0, its first chroma row y0/vdiv) into RGBA rows of out, from
// out's first row, and returns the rows of the image they are; last says
// whether the band ends the image. It is span, convert and hold in one.
func (u *upsampler) band(m *image.YCbCr, y0, y1 int, last bool, out *image.RGBA) image.Rectangle {
	start, end := u.span(y0, y1, last)
	u.convert(m, y0, start, end, out)
	u.hold(m, y0, y1, last)
	return image.Rect(0, start, u.w, end)
}

// span is the rows of the image the band of rows y0 to y1 gives: the row
// held back from the band before, and not its own last row, when down the
// chroma is interpolated.
func (u *upsampler) span(y0, y1 int, last bool) (start, end int) {
	start, end = y0, y1
	if u.fancyV {
		if u.held {
			start--
		}
		if !last {
			end--
		}
	}
	return start, end
}

// hold keeps what the band after needs: the last luma row, held back, and
// the last chroma row.
func (u *upsampler) hold(m *image.YCbCr, y0, y1 int, last bool) {
	if !u.fancyV || last {
		return
	}
	copy(u.heldY, m.Y[(y1-1-y0)*m.YStride:][:u.w])
	i := ((y1-1)/u.vdiv - y0/u.vdiv) * m.CStride
	copy(u.prevCb, m.Cb[i:][:u.cw])
	copy(u.prevCr, m.Cr[i:][:u.cw])
	u.held = true
}

// convert converts the rows from to to of the image (within the span of the
// band m of rows from y0) into RGBA rows of out, from its first row.
func (u *upsampler) convert(m *image.YCbCr, y0, from, to int, out *image.RGBA) {
	c0 := y0 / u.vdiv
	lumaRow := func(y int) []byte {
		if y < y0 {
			return u.heldY
		}
		return m.Y[(y-y0)*m.YStride:][:u.w]
	}
	chromaRow := func(c int) ([]byte, []byte) {
		if c < c0 {
			return u.prevCb, u.prevCr
		}
		i := (c - c0) * m.CStride
		return m.Cb[i:][:u.cw], m.Cr[i:][:u.cw]
	}
	start, end := from, to
	row := func(y int, upCb, upCr []byte) {
		dst := out.Pix[(y-start)*out.Stride:]
		c := y / u.vdiv
		cb, cr := chromaRow(c)
		switch {
		case u.fancyV:
			far, below := c-1, false
			if y%2 == 1 {
				far, below = c+1, true
			}
			far = max(0, min(far, u.ch-1))
			fcb, fcr := chromaRow(far)
			if u.hdiv == 2 && !u.rgb {
				yccH2V2(dst, lumaRow(y), cb, fcb, cr, fcr)
				return
			}
			if u.hdiv == 2 {
				h2v2(upCb, cb, fcb)
				h2v2(upCr, cr, fcr)
			} else {
				h1v2(upCb, cb, fcb, below)
				h1v2(upCr, cr, fcr, below)
			}
			cb, cr = upCb, upCr
		case u.fancyH && !u.rgb:
			yccH2V1(dst, lumaRow(y), cb, cr)
			return
		case u.fancyH:
			h2v1(upCb, cb)
			h2v1(upCr, cr)
			cb, cr = upCb, upCr
		case u.hdiv > 1:
			repeat(upCb, cb, u.hdiv)
			repeat(upCr, cr, u.hdiv)
			cb, cr = upCb, upCr
		}
		if u.rgb {
			rgbRow(dst, lumaRow(y), cb, cr)
		} else {
			yccRow(dst, lumaRow(y), cb, cr)
		}
	}
	// The rows are independent of one another: with helpers, shared out,
	// each part with scratch rows of its own.
	n := end - start
	parts := 1
	if u.env != nil && n >= 8 {
		parts = max(1, min(u.env.Helpers()+1, n/4))
	}
	for len(u.scratch) < parts {
		u.scratch = append(u.scratch, [2][]byte{make([]byte, len(u.upCb)), make([]byte, len(u.upCr))})
	}
	if parts == 1 {
		for y := start; y < end; y++ {
			row(y, u.upCb, u.upCr)
		}
	} else {
		u.env.Parallel(parts, func(p int) {
			sc := u.scratch[p]
			for y := start + p*n/parts; y < start+(p+1)*n/parts; y++ {
				row(y, sc[0], sc[1])
			}
		})
	}
}

// h2v1 is libjpeg's h2v1_fancy_upsample of a row of in (at least three
// samples) into out.
func h2v1(out, in []byte) {
	n := len(in)
	out = out[:2*n]
	v := int(in[0])
	out[0] = byte(v)
	out[1] = byte((v*3 + int(in[1]) + 2) >> 2)
	for i := 1; i < n-1; i++ {
		v := int(in[i]) * 3
		out[2*i] = byte((v + int(in[i-1]) + 1) >> 2)
		out[2*i+1] = byte((v + int(in[i+1]) + 2) >> 2)
	}
	v = int(in[n-1])
	out[2*n-2] = byte((v*3 + int(in[n-2]) + 1) >> 2)
	out[2*n-1] = byte(v)
}

// h1v2 is libjpeg's h1v2_fancy_upsample of a row: near is the nearer chroma
// row, far the next, below or above.
func h1v2(out, near, far []byte, below bool) {
	bias := 1
	if below {
		bias = 2
	}
	out = out[:len(near)]
	far = far[:len(near)]
	for i, v := range near {
		out[i] = byte((int(v)*3 + int(far[i]) + bias) >> 2)
	}
}

// h2v2 is libjpeg's h2v2_fancy_upsample of a row: near is the nearer chroma
// row (at least three samples), far the next.
func h2v2(out, near, far []byte) {
	n := len(near)
	out = out[:2*n]
	far = far[:n]
	sum := func(i int) int { return int(near[i])*3 + int(far[i]) }
	this, next := sum(0), sum(1)
	out[0] = byte((this*4 + 8) >> 4)
	out[1] = byte((this*3 + next + 7) >> 4)
	lastSum := this
	this = next
	for i := 1; i < n-1; i++ {
		next = sum(i + 1)
		out[2*i] = byte((this*3 + lastSum + 8) >> 4)
		out[2*i+1] = byte((this*3 + next + 7) >> 4)
		lastSum, this = this, next
	}
	out[2*n-2] = byte((this*3 + lastSum + 8) >> 4)
	out[2*n-1] = byte((this*4 + 7) >> 4)
}

// repeat repeats each sample of in k times into out.
func repeat(out, in []byte, k int) {
	out = out[:k*len(in)]
	for i, v := range in {
		for j := range k {
			out[k*i+j] = v
		}
	}
}
