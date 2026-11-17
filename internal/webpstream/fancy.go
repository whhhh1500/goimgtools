package webpstream

import (
	"image"
	"image/color"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// ToRGB returns src with its YCbCr strips (and NYCbCrA ones, with their
// alpha) converted to NRGBA as libwebp converts them, which is what
// browsers and dwebp show: the chroma, stored for each 2x2 block, is
// interpolated between blocks ("fancy upsampling", libwebp's
// src/dsp/upsampling.c), and Y'CbCr, in the limited range of video, is
// turned into RGB with libwebp's integer arithmetic (src/dsp/yuv.h). The
// pixels are libwebp's to the byte.
//
// Taking each chroma sample for its own 2x2 block, as image.YCbCr does,
// leaves blocks of colour along edges, and encoders that shape their
// chroma for interpolation (libwebp's and this module's sharp YUV) count
// on it.
//
// Strips of other types (a lossless image's NRGBA) pass through.
func ToRGB(src pipeline.Source) pipeline.Source {
	if m := src.ColorModel(); m != color.YCbCrModel && m != color.NYCbCrAModel {
		return src
	}
	b := src.Bounds()
	return &toRGB{src: src, env: pipeline.EnvOf(src), b: b, cw: (b.Dx() + 1) / 2}
}

// toRGB converts strips as they come, handing each out in pieces of the
// usual size of a strip of RGBA (pipeline.StripRows). The last row of a
// strip, when it is odd and not the last of the image, also needs the
// first chroma row of the next strip, so it is held back until then; and
// the first row of a strip may need the last chroma row of the one before,
// which is kept.
type toRGB struct {
	src     pipeline.Source
	env     *pipeline.Env
	b       image.Rectangle
	cw      int // chroma samples in a row
	band    []uint8
	maxRows int // rows of a strip out
	// The strip being converted, the next of its rows to give out and
	// where they end, and whether its last row is to be held back.
	cur       image.Rectangle
	yc        *image.YCbCr
	al        *image.NYCbCrA
	next, end int
	hold      bool
	// The last chroma row of the strip before (index lastC), and the row
	// held back (index held, or -1) with its luma and alpha.
	lastU, lastV []uint8
	lastC        int
	heldY, heldA []uint8
	held         int
	reserved     int64
	// up holds a row of upsampled chroma (Cb then Cr) for each part a
	// strip is cut into when helpers share the conversion.
	up      [][]uint8
	started bool
	// scaled: the decoder scales the image (ScaleOnLoad) and makes the
	// RGB itself.
	scaled bool
}

// testRGBRows, when positive, overrides the number of rows of a strip
// out, so that tests can cut strips into pieces.
var testRGBRows int

func (t *toRGB) Env() *pipeline.Env      { return t.env }
func (t *toRGB) Bounds() image.Rectangle { return t.b }
func (t *toRGB) ColorModel() color.Model { return color.NRGBAModel }

// StoredColorModel is the colour model of the file: Y'CbCr, with or
// without alpha.
func (t *toRGB) StoredColorModel() color.Model { return t.src.ColorModel() }
func (t *toRGB) Opaque() bool                  { return pipeline.IsOpaque(t.src) }
func (t *toRGB) Meta() pipeline.Meta           { return pipeline.MetaOf(t.src) }

func (t *toRGB) Close() error {
	t.env.Budget.Release(t.reserved)
	t.reserved, t.band = 0, nil
	return t.src.Close()
}

func (t *toRGB) start() error {
	w := t.b.Dx()
	t.maxRows = pipeline.StripRows(4*int64(w), t.b.Dy())
	if testRGBRows > 0 {
		t.maxRows = testRGBRows
	}
	parts := t.env.Helpers() + 1
	n := int64(3*w+4*t.cw) + 2*int64(w)*int64(parts) + 4*int64(w)*int64(t.maxRows)
	if err := t.env.Budget.Reserve(n, "webp rgb rows"); err != nil {
		return err
	}
	t.reserved += n
	t.lastU, t.lastV = make([]uint8, t.cw), make([]uint8, t.cw)
	t.up = make([][]uint8, parts)
	for i := range t.up {
		t.up[i] = make([]uint8, 2*w)
	}
	t.heldY, t.heldA = make([]uint8, w), make([]uint8, w)
	t.band = make([]uint8, 4*w*t.maxRows)
	t.held, t.lastC = -1, -1
	t.started = true
	return nil
}

// ScaleOnLoad has the decoder scale the image as libwebp does while
// decoding it (see source.ScaleOnLoad); it then turns the Y'CbCr into RGB
// itself, without upsampling.
func (t *toRGB) ScaleOnLoad(w, h int) bool {
	j, ok := t.src.(interface{ ScaleOnLoad(w, h int) bool })
	if t.started || !ok || !j.ScaleOnLoad(w, h) {
		return false
	}
	t.scaled, t.b = true, t.src.Bounds()
	return true
}

func (t *toRGB) Next() (image.Image, error) {
	if t.scaled {
		return t.src.Next()
	}
	if !t.started {
		if err := t.start(); err != nil {
			return nil, err
		}
	}
	for t.yc == nil {
		s, err := t.src.Next()
		if err != nil {
			return nil, err
		}
		switch p := s.(type) {
		case *image.YCbCr:
			t.yc, t.al = p, nil
		case *image.NYCbCrA:
			t.yc, t.al = &p.YCbCr, p
		default:
			return s, nil
		}
		// The rows out: the one held back, those of this strip, less its
		// last if that has to wait for the next strip.
		t.cur = s.Bounds()
		t.next, t.end = t.cur.Min.Y, t.cur.Max.Y
		if t.held >= 0 {
			t.next = t.held
		}
		t.hold = t.end < t.b.Max.Y && (t.end-1-t.b.Min.Y)&1 == 1
		if t.hold {
			t.end--
		}
		if t.next == t.end {
			t.finish()
		}
	}
	w := t.b.Dx()
	yc, r := t.yc, t.cur
	y0, y1 := t.next, min(t.next+t.maxRows, t.end)
	out := &image.NRGBA{Pix: t.band[:4*w*(y1-y0)], Stride: 4 * w, Rect: image.Rect(t.b.Min.X, y0, t.b.Max.X, y1)}

	// chroma returns chroma row k (relative to the image), from this strip
	// or the last one kept from the strip before.
	ch := (t.b.Dy() + 1) / 2
	chroma := func(k int) ([]uint8, []uint8) {
		k = min(max(k, 0), ch-1)
		if k == t.lastC && 2*k < r.Min.Y-t.b.Min.Y {
			return t.lastU, t.lastV
		}
		o := yc.COffset(t.b.Min.X, t.b.Min.Y+2*k)
		return yc.Cb[o : o+t.cw], yc.Cr[o : o+t.cw]
	}
	// Rows are converted on their own; helpers share them out.
	rows := y1 - y0
	parts := max(1, min(len(t.up), rows, rows*w/(8<<10)))
	t.env.Parallel(parts, func(k int) {
		upU, upV := t.up[k][:w], t.up[k][w:]
		for y := y0 + k*rows/parts; y < y0+(k+1)*rows/parts; y++ {
			var lum, a []uint8
			if y == t.held {
				lum = t.heldY
				if t.al != nil {
					a = t.heldA
				}
			} else {
				o := yc.YOffset(t.b.Min.X, y)
				lum = yc.Y[o : o+w]
				if t.al != nil {
					o := t.al.AOffset(t.b.Min.X, y)
					a = t.al.A[o : o+w]
				}
			}
			j := y - t.b.Min.Y
			near, far := j>>1, j>>1-1
			if j&1 == 1 {
				far = j>>1 + 1
			}
			nu, nv := chroma(near)
			fu, fv := chroma(far)
			dst := out.Pix[(y-y0)*4*w : (y-y0+1)*4*w]
			if a == nil && yuvRowSIMD(dst, lum, nu, fu, nv, fv) {
				continue
			}
			upsample(nu, fu, upU)
			upsample(nv, fv, upV)
			for x, v := range lum {
				d := dst[4*x : 4*x+4]
				u, vv := int(upU[x]), int(upV[x])
				yy := multHi(int(v), 19077)
				d[0] = clip8(yy + multHi(vv, 26149) - 14234)
				d[1] = clip8(yy - multHi(u, 6419) - multHi(vv, 13320) + 8708)
				d[2] = clip8(yy + multHi(u, 33050) - 17685)
				d[3] = 0xff
				if a != nil {
					d[3] = a[x]
				}
			}
		}
	})
	t.next = y1
	if t.next == t.end {
		t.finish()
	}
	return out, nil
}

// finish is done with the strip: it holds its last row back if need be
// and keeps its last chroma row.
func (t *toRGB) finish() {
	yc, r, w := t.yc, t.cur, t.b.Dx()
	t.held = -1
	if t.hold {
		y := r.Max.Y - 1
		o := yc.YOffset(t.b.Min.X, y)
		copy(t.heldY, yc.Y[o:o+w])
		if t.al != nil {
			o := t.al.AOffset(t.b.Min.X, y)
			copy(t.heldA, t.al.A[o:o+w])
		}
		t.held = y
	}
	k := (r.Max.Y - 1 - t.b.Min.Y) >> 1
	o := yc.COffset(t.b.Min.X, t.b.Min.Y+2*k)
	copy(t.lastU, yc.Cb[o:o+t.cw])
	copy(t.lastV, yc.Cr[o:o+t.cw])
	t.lastC = k
	t.yc, t.al = nil, nil
}

// upsample interpolates a row of chroma between row n, the nearer, and
// row f to full width, weighing them 3 to 1 and the two nearest samples
// across 3 to 1 (libwebp's UpsampleRgbLinePair, with its rounding).
func upsample(n, f, dst []uint8) {
	w := len(dst)
	dst[0] = uint8((3*int(n[0]) + int(f[0]) + 2) >> 2)
	last := (w - 1) >> 1
	for i := 1; i <= last; i++ {
		n0, n1, f0, f1 := int(n[i-1]), int(n[i]), int(f[i-1]), int(f[i])
		dst[2*i-1] = uint8(((n0+3*n1+3*f0+f1+8)>>3 + n0) >> 1)
		dst[2*i] = uint8(((3*n0+n1+f0+3*f1+8)>>3 + n1) >> 1)
	}
	if w&1 == 0 {
		dst[w-1] = uint8((3*int(n[last]) + int(f[last]) + 2) >> 2)
	}
}

// yuvRowSIMD converts a row (without alpha) as the loop of Next does, with
// AVX2, and reports whether it did: the pairs of pixels from the second on
// in chunks of 32 (those of 16 pairs of chroma samples), the rest here.
func yuvRowSIMD(dst, lum, nu, fu, nv, fv []uint8) bool {
	w := len(lum)
	last := (w - 1) >> 1 // the last pair: pixels 2*last-1 and 2*last
	if !useSIMD || last < 16 {
		return false
	}
	k := last / 16
	n := len(nu)
	_, _, _, _ = nu[16*k], fu[:n][16*k], nv[:n][16*k], fv[:n][16*k]
	_, _ = dst[4*(32*k+1)-1], lum[32*k]
	yuvRowAsm(&dst[4], &lum[1], &nu[0], &fu[0], &nv[0], &fv[0], k)
	yuvPixel(dst, lum, nu, fu, nv, fv, 0)
	for x := 32*k + 1; x < w; x++ {
		yuvPixel(dst, lum, nu, fu, nv, fv, x)
	}
	return true
}

// yuvPixel converts pixel x of a row: its chroma upsampled as upsample
// does, converted as Next does.
func yuvPixel(dst, lum, nu, fu, nv, fv []uint8, x int) {
	up := func(n, f []uint8) int {
		w := len(lum)
		switch {
		case x == 0:
			return (3*int(n[0]) + int(f[0]) + 2) >> 2
		case x == w-1 && w&1 == 0:
			l := (w - 1) >> 1
			return (3*int(n[l]) + int(f[l]) + 2) >> 2
		case x&1 == 1:
			i := (x + 1) >> 1
			n0, n1, f0, f1 := int(n[i-1]), int(n[i]), int(f[i-1]), int(f[i])
			return ((n0+3*n1+3*f0+f1+8)>>3 + n0) >> 1
		}
		i := x >> 1
		n0, n1, f0, f1 := int(n[i-1]), int(n[i]), int(f[i-1]), int(f[i])
		return ((3*n0+n1+f0+3*f1+8)>>3 + n1) >> 1
	}
	u, vv := up(nu, fu), up(nv, fv)
	d := dst[4*x : 4*x+4]
	yy := multHi(int(lum[x]), 19077)
	d[0] = clip8(yy + multHi(vv, 26149) - 14234)
	d[1] = clip8(yy - multHi(u, 6419) - multHi(vv, 13320) + 8708)
	d[2] = clip8(yy + multHi(u, 33050) - 17685)
	d[3] = 0xff
}

func multHi(v, coeff int) int { return (v * coeff) >> 8 }

// clip8 descales libwebp's 6-bit fixed point and clamps to 0..255.
func clip8(v int) uint8 {
	if v&^(256<<6-1) == 0 {
		return uint8(v >> 6)
	}
	if v < 0 {
		return 0
	}
	return 255
}
