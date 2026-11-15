package ops

import (
	"errors"
	"image"
	"image/color"
	"image/draw"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// The filters that look at the neighbours of a pixel: Blur and Sharpen.
// They work on rows of 8-bit premultiplied RGBA pulled one at a time
// through a chain of passes, so that only the rows a pass needs at once
// are held: a few for a small filter, the height of the window for a large
// one, and those in a temporary file when they do not fit in their share
// of the memory limit.
//
// The Gaussian blur is Pillow's (src/libImaging/BoxBlur.c, Pillow 12.1.1,
// MIT-CMU licence, see licenses/LICENSE-PILLOW): three box blurs of the
// same fractional radius across and then three down, each rounded to 8
// bits, with the same fixed-point arithmetic, so that for an image without
// transparency the result is Pillow's to the byte. The unsharp mask is
// Pillow's UnsharpMask.c (by Kevin Cazabon) on top of it.

// rowSource hands out the rows of an image in order, each valid until the
// next call; io.EOF after the last.
type rowSource interface {
	nextRow() ([]uint8, error)
}

// ---- rows of the source ----

// inputRows turns the strips of a Source into rows of premultiplied RGBA,
// optionally keeping each in a store (for a filter that needs the original
// next to its result) and passing it through the horizontal blurs.
type inputRows struct {
	src   pipeline.Source
	w, h  int
	band  rgbaBand
	cur   *image.RGBA
	y     int // rows handed out
	keep  rowStore
	hblur *hbox
}

func (in *inputRows) nextRow() ([]uint8, error) {
	if in.y >= in.h {
		return nil, io.EOF
	}
	sb := in.src.Bounds()
	abs := sb.Min.Y + in.y
	if in.cur == nil || abs >= in.cur.Rect.Max.Y {
		s, err := in.src.Next()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		r := s.Bounds()
		dst, err := in.band.get(r)
		if err != nil {
			return nil, err
		}
		draw.Draw(dst, r, s, r.Min, draw.Src)
		in.cur = dst
	}
	row := in.cur.Pix[in.cur.PixOffset(sb.Min.X, abs):][:4*in.w]
	if in.keep != nil {
		if err := in.keep.put(in.y, row); err != nil {
			return nil, err
		}
	}
	in.y++
	if in.hblur != nil {
		return in.hblur.apply(row), nil
	}
	return row, nil
}

// drain reads the source to its end, so that a decoder has verified its
// input before the last strip of the result goes out.
func (in *inputRows) drain() error {
	for {
		_, err := in.src.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// ---- box blur of fractional radius ----

// boxRadius is the radius of the box that, applied passes times, comes
// closest to a Gaussian of standard deviation sigma: Pillow's
// _gaussian_blur_radius, after Gwosdek, Grewenig, Bruhn and Weickert,
// "Theoretical Foundations of Gaussian Convolution by Extended Box
// Filtering" (2011), in Pillow's single precision. (The explicit
// conversions keep the compiler from fusing a multiplication and an
// addition, which some processors would round differently.)
func boxRadius(sigma float64, passes int) float32 {
	r := float32(sigma)
	sigma2 := float32(r*r) / float32(passes)
	L := float32(math.Sqrt(12.0*float64(sigma2) + 1.0))
	l := float32(math.Floor((float64(L) - 1.0) / 2.0))
	a := float32(2*l+1) * (float32(l*(l+1)) - float32(3*sigma2))
	a /= 6 * (sigma2 - float32((l+1)*(l+1)))
	return l + a
}

// box holds the weights of a box of fractional radius: the 2*ri+1 pixels
// of its body count ww each, the one beyond each end fw, in 24 fractional
// bits, as Pillow's ImagingHorizontalBoxBlur makes them.
type box struct {
	ri     int
	ww, fw uint32
	any    bool // the box is more than the pixel itself
}

func newBox(r float32) box {
	ri := int(r)
	ww := uint32(float32(1<<24) / float32(r*2+1))
	return box{ri: ri, ww: ww, fw: ((1 << 24) - uint32(ri*2+1)*ww) / 2, any: r != 0}
}

// value is the result for the sum of the body and the two pixels beyond
// it, rounded to 8 bits.
func (b box) value(acc, left, right uint32) uint8 {
	return uint8((acc*b.ww + (left+right)*b.fw + 1<<23) >> 24)
}

// hbox blurs a row three times over with the same box.
type hbox struct {
	b  box
	w  int
	nc int // channels blurred: 3 when there is no transparency, the alpha staying 255
	t  [2][]uint8
}

func (h *hbox) apply(row []uint8) []uint8 {
	if !h.b.any {
		return row
	}
	h.pass(h.t[0], row)
	h.pass(h.t[1], h.t[0])
	h.pass(h.t[0], h.t[1])
	return h.t[0]
}

// pass is one horizontal box blur of src into dst, the pixels at the ends
// repeated beyond them.
func (h *hbox) pass(dst, src []uint8) {
	w, ri := h.w, h.b.ri
	last := w - 1
	clamp := func(x int) int {
		if x < 0 {
			return 0
		}
		if x > last {
			return last
		}
		return x
	}
	// Between lo and hi the window lies inside the row.
	lo := min(ri+1, w)
	hi := max(lo, w-ri-1)
	d := 4 * (ri + 1)
	for c := 0; c < h.nc; c++ {
		var acc uint32
		for k := -ri; k <= ri; k++ {
			acc += uint32(src[4*clamp(k)+c])
		}
		x := 0
		for ; x < lo; x++ {
			r := uint32(src[4*clamp(x+ri+1)+c])
			dst[4*x+c] = h.b.value(acc, uint32(src[4*clamp(x-ri-1)+c]), r)
			acc += r - uint32(src[4*clamp(x-ri)+c])
		}
		for i := 4*lo + c; i < 4*hi; i += 4 {
			r := uint32(src[i+d])
			dst[i] = h.b.value(acc, uint32(src[i-d]), r)
			acc += r - uint32(src[i-d+4])
		}
		for x = hi; x < w; x++ {
			r := uint32(src[4*clamp(x+ri+1)+c])
			dst[4*x+c] = h.b.value(acc, uint32(src[4*clamp(x-ri-1)+c]), r)
			acc += r - uint32(src[4*clamp(x-ri)+c])
		}
	}
}

// vbox is one vertical box blur: a window of rows of its input, kept in a
// store, and the running sum of its body.
type vbox struct {
	prev  rowSource
	b     box
	w4, h int
	nc    int // as hbox's
	store rowStore
	sum   []uint32
	out   []uint8
	got   int // input rows taken
	next  int // next output row
}

func (v *vbox) row(i int) ([]uint8, error) {
	if i < 0 {
		i = 0
	} else if i >= v.h {
		i = v.h - 1
	}
	return v.store.get(i)
}

func (v *vbox) nextRow() ([]uint8, error) {
	y := v.next
	if y >= v.h {
		return nil, io.EOF
	}
	ri := v.b.ri
	for need := min(y+ri+1, v.h-1); v.got <= need; v.got++ {
		r, err := v.prev.nextRow()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if err := v.store.put(v.got, r); err != nil {
			return nil, err
		}
	}
	if y == 0 {
		clear(v.sum)
		for k := -ri; k <= ri; k++ {
			r, err := v.row(k)
			if err != nil {
				return nil, err
			}
			for j, x := range r {
				v.sum[j] += uint32(x)
			}
		}
	}
	// Three gets: all three rows stay valid.
	a, err := v.row(y - ri - 1)
	if err != nil {
		return nil, err
	}
	b, err := v.row(y + ri + 1)
	if err != nil {
		return nil, err
	}
	c, err := v.row(y - ri)
	if err != nil {
		return nil, err
	}
	out, sum := v.out, v.sum[:len(v.out)]
	a, b, c = a[:len(out)], b[:len(out)], c[:len(out)]
	if v.nc == 4 {
		for j := range out {
			s, bj := sum[j], uint32(b[j])
			out[j] = v.b.value(s, uint32(a[j]), bj)
			sum[j] = s + bj - uint32(c[j])
		}
	} else {
		for j := 0; j+3 < len(out); j += 4 {
			for k := j; k < j+3; k++ {
				s, bk := sum[k], uint32(b[k])
				out[k] = v.b.value(s, uint32(a[k]), bk)
				sum[k] = s + bk - uint32(c[k])
			}
		}
	}
	v.next++
	return v.out, nil
}

// ---- a store of rows ----

// rowStore keeps rows by their number, as many as it was made for, the
// newest replacing the oldest.
type rowStore interface {
	put(i int, row []uint8) error
	// get returns row i; the slice stays valid until three more gets.
	get(i int) ([]uint8, error)
	close()
}

type memStore struct {
	rows     [][]uint8
	env      *pipeline.Env
	reserved int64
}

func (m *memStore) put(i int, row []uint8) error {
	copy(m.rows[i%len(m.rows)], row)
	return nil
}

func (m *memStore) get(i int) ([]uint8, error) { return m.rows[i%len(m.rows)], nil }

func (m *memStore) close() {
	m.env.Budget.Release(m.reserved)
	m.reserved, m.rows = 0, nil
}

// diskStore keeps the rows in a Scratch and reads them back through a few
// row buffers. A row is read into the buffer used longest ago, so that the
// three rows got last stay where they are.
type diskStore struct {
	sc     *pipeline.Scratch
	n, row int
	buf    [4][]uint8
	which  [4]int    // the row in each buffer, -1 for none
	used   [4]uint64 // when each buffer was last handed out
	clock  uint64
}

func (d *diskStore) put(i int, row []uint8) error {
	// The buffer that holds the row this one replaces no longer does.
	for k, r := range d.which {
		if r >= 0 && r%d.n == i%d.n {
			d.which[k] = -1
		}
	}
	_, err := d.sc.WriteAt(row, int64(i%d.n)*int64(d.row))
	return err
}

func (d *diskStore) get(i int) ([]uint8, error) {
	d.clock++
	k := 0
	for j, r := range d.which {
		if r == i {
			d.used[j] = d.clock
			return d.buf[j], nil
		}
		if d.used[j] < d.used[k] {
			k = j
		}
	}
	d.used[k] = d.clock
	d.which[k] = -1
	if _, err := d.sc.ReadAt(d.buf[k], int64(i%d.n)*int64(d.row)); err != nil {
		return nil, err
	}
	d.which[k] = i
	return d.buf[k], nil
}

func (d *diskStore) close() {
	if d.sc != nil {
		d.sc.Close()
		d.sc = nil
	}
}

// ---- the stage ----

// filterStage hands out the rows of a chain of passes as RGBA strips.
type filterStage struct {
	src      pipeline.Source
	env      *pipeline.Env
	in       *inputRows
	last     rowSource
	finish   func(y int, row []uint8, dst []uint8) error
	band     rgbaBand
	n, y     int
	stores   []rowStore
	reserved int64
	opaque   bool
}

func (f *filterStage) Env() *pipeline.Env      { return f.env }
func (f *filterStage) Bounds() image.Rectangle { return f.src.Bounds() }
func (f *filterStage) ColorModel() color.Model { return color.RGBAModel }
func (f *filterStage) Opaque() bool            { return f.opaque }

func (f *filterStage) Close() error {
	for _, s := range f.stores {
		s.close()
	}
	f.stores = nil
	f.band.release()
	f.in.band.release()
	f.env.Budget.Release(f.reserved)
	f.reserved = 0
	return f.src.Close()
}

func (f *filterStage) Next() (image.Image, error) {
	b := f.src.Bounds()
	h := b.Dy()
	if f.y >= h {
		return nil, io.EOF
	}
	y1 := min(f.y+f.n, h)
	r := image.Rect(b.Min.X, b.Min.Y+f.y, b.Max.X, b.Min.Y+y1)
	out, err := f.band.get(r)
	if err != nil {
		return nil, err
	}
	for y := f.y; y < y1; y++ {
		row, err := f.last.nextRow()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if err := f.finish(y, row, out.Pix[(y-f.y)*out.Stride:][:4*b.Dx()]); err != nil {
			return nil, err
		}
	}
	f.y = y1
	if f.y == h {
		if err := f.in.drain(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// reserve takes n bytes from the budget for the life of the stage.
func (f *filterStage) reserve(n int64, what string) error {
	if err := f.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	f.reserved += n
	return nil
}

// newStore makes a store of n rows of rowBytes: in memory when its share
// of the limit has room, in a temporary file otherwise.
func (f *filterStage) newStore(n, rowBytes int) (rowStore, error) {
	size := int64(n) * int64(rowBytes)
	var s rowStore
	if f.env.Budget.TryReserve(size) {
		m := &memStore{env: f.env, reserved: size, rows: make([][]uint8, n)}
		for i := range m.rows {
			m.rows[i] = make([]uint8, rowBytes)
		}
		s = m
	} else {
		if err := f.reserve(int64(len(diskStore{}.buf))*int64(rowBytes), "filter row buffers"); err != nil {
			return nil, err
		}
		sc, err := pipeline.NewScratch(f.env, size)
		if err != nil {
			return nil, err
		}
		d := &diskStore{sc: sc, n: n, row: rowBytes, which: [4]int{-1, -1, -1, -1}}
		for k := range d.buf {
			d.buf[k] = make([]uint8, rowBytes)
		}
		s = d
	}
	f.stores = append(f.stores, s)
	return s, nil
}

func newFilter(src pipeline.Source) *filterStage {
	env := pipeline.EnvOf(src)
	b := src.Bounds()
	f := &filterStage{src: src, env: env, band: rgbaBand{env: env}, opaque: pipeline.IsOpaque(src)}
	f.n = pipeline.StripRows(int64(b.Dx())*4, b.Dy())
	f.in = &inputRows{src: src, w: b.Dx(), h: b.Dy(), band: rgbaBand{env: env}}
	return f
}

// fail gives back what a stage took while it was being set up, when that
// failed; src stays the caller's.
func (f *filterStage) fail(err error) error {
	for _, s := range f.stores {
		s.close()
	}
	f.env.Budget.Release(f.reserved)
	return err
}

// blurPasses puts the three horizontal and three vertical box blurs for
// sigma behind f's input.
func (f *filterStage) blurPasses(sigma float64) (rowSource, error) {
	b := newBox(boxRadius(sigma, 3))
	if !b.any {
		return f.in, nil
	}
	w := f.in.w
	w4 := 4 * w
	if err := f.reserve(int64(w4)*2, "blur rows"); err != nil {
		return nil, err
	}
	nc := 4
	if f.opaque {
		nc = 3
	}
	f.in.hblur = &hbox{b: b, w: w, nc: nc, t: [2][]uint8{opaqueRow(w4), opaqueRow(w4)}}
	var last rowSource = f.in
	h := f.in.h
	for i := 0; i < 3; i++ {
		if err := f.reserve(int64(w4)*(4+1), "blur sums"); err != nil {
			return nil, err
		}
		st, err := f.newStore(min(2*b.ri+4, h), w4)
		if err != nil {
			return nil, err
		}
		last = &vbox{prev: last, b: b, w4: w4, h: h, nc: nc, store: st, sum: make([]uint32, w4), out: opaqueRow(w4)}
	}
	return last, nil
}

// opaqueRow is a row of RGBA with all its alpha 255, which a pass that
// blurs only the colours leaves so.
func opaqueRow(n int) []uint8 {
	r := make([]uint8, n)
	for i := 3; i < n; i += 4 {
		r[i] = 255
	}
	return r
}

// MaxSigma is the largest blur Blur and Sharpen take, as sharp.
const MaxSigma = 1000

func checkSigma(sigma float64) error {
	if !(sigma > 0) || sigma > MaxSigma {
		return errors.New("goimgtools: sigma must be above 0 and at most 1000")
	}
	return nil
}

// Blur blurs src with a Gaussian of standard deviation sigma pixels, as
// Pillow's GaussianBlur does it. Pixels beyond the edges are the edge
// pixels repeated. Colours are blurred premultiplied, so that transparent
// pixels lend no colour to their neighbours (Pillow blurs them as they
// are); for an image without transparency that makes no difference. The
// result is RGBA.
func Blur(src pipeline.Source, sigma float64) (pipeline.Source, error) {
	if err := checkSigma(sigma); err != nil {
		return nil, err
	}
	f := newFilter(src)
	var err error
	if f.last, err = f.blurPasses(sigma); err != nil {
		return nil, f.fail(err)
	}
	f.finish = func(_ int, row []uint8, dst []uint8) error {
		copy(dst, row)
		return nil
	}
	return f, nil
}

// Sharpen sharpens src.
//
// With sigma zero it is the mild sharpen of sharp: the 3x3 kernel of -1s
// around 32, divided by 24, on every channel.
//
// With sigma it is Pillow's UnsharpMask: each colour channel whose value
// differs from the Gaussian blur of sigma by more than threshold moves
// away from it by percent hundredths of the difference (truncated, as
// Pillow does). The colours compared are those not multiplied by alpha,
// and transparency is kept as it is (Pillow sharpens it too).
func Sharpen(src pipeline.Source, sigma float64, percent, threshold int) (pipeline.Source, error) {
	if sigma == 0 {
		return sharpenMild(src)
	}
	if err := checkSigma(sigma); err != nil {
		return nil, err
	}
	if percent < 0 || percent > 10000 {
		return nil, errors.New("goimgtools: sharpen amount must be 0 to 100")
	}
	if threshold < 0 || threshold > 255 {
		return nil, errors.New("goimgtools: sharpen threshold must be 0 to 255")
	}
	f := newFilter(src)
	w4 := 4 * f.in.w
	b := newBox(boxRadius(sigma, 3))
	// The originals are kept until the blurred row of the same number
	// comes out: each vertical pass is at most ri+2 rows behind the one
	// before it.
	keep, err := f.newStore(min(3*(b.ri+2)+2, f.in.h), w4)
	if err != nil {
		return nil, f.fail(err)
	}
	f.in.keep = keep
	if f.last, err = f.blurPasses(sigma); err != nil {
		return nil, f.fail(err)
	}
	opaque := f.opaque
	f.finish = func(y int, blurred []uint8, dst []uint8) error {
		orig, err := keep.get(y)
		if err != nil {
			return err
		}
		if opaque { // the loop below, with every alpha 255
			orig, blurred = orig[:len(dst)], blurred[:len(dst)]
			for x := 0; x+3 < len(dst); x += 4 {
				for c := x; c < x+3; c++ {
					in := int(orig[c])
					out := in
					if diff := in - int(blurred[c]); diff > threshold || -diff > threshold {
						out = max(0, min(255, in+diff*percent/100))
					}
					dst[c] = uint8(out)
				}
				dst[x+3] = 255
			}
			return nil
		}
		for x := 0; x < len(dst); x += 4 {
			a := uint32(orig[x+3])
			if a == 0 {
				dst[x], dst[x+1], dst[x+2], dst[x+3] = 0, 0, 0, 0
				continue
			}
			ba := uint32(blurred[x+3])
			for c := 0; c < 3; c++ {
				in := straight(uint32(orig[x+c]), a)
				bl := in
				if ba > 0 {
					bl = straight(uint32(blurred[x+c]), ba)
				}
				out := in
				if diff := in - bl; diff > threshold || -diff > threshold {
					out = max(0, min(255, in+diff*percent/100))
				}
				if a == 255 {
					dst[x+c] = uint8(out)
				} else {
					dst[x+c] = uint8((uint32(out)*a + 127) / 255)
				}
			}
			dst[x+3] = uint8(a)
		}
		return nil
	}
	return f, nil
}

// straight is the colour of a premultiplied 8-bit value.
func straight(c, a uint32) int {
	if a == 255 {
		return int(c)
	}
	return int(min(255, (c*255+a/2)/a))
}

func sharpenMild(src pipeline.Source) (pipeline.Source, error) {
	f := newFilter(src)
	w4 := 4 * f.in.w
	if err := f.reserve(int64(w4), "sharpen row"); err != nil {
		return nil, f.fail(err)
	}
	st, err := f.newStore(min(4, f.in.h), w4)
	if err != nil {
		return nil, f.fail(err)
	}
	nc := 4
	if f.opaque {
		nc = 3
	}
	f.last = &conv3{prev: f.in, w: f.in.w, h: f.in.h, nc: nc, store: st, out: opaqueRow(w4)}
	f.finish = func(_ int, row []uint8, dst []uint8) error {
		copy(dst, row)
		return nil
	}
	return f, nil
}

// conv3 applies sharp's mild sharpening kernel.
type conv3 struct {
	prev  rowSource
	w, h  int
	nc    int // as hbox's
	store rowStore
	out   []uint8
	got   int
	next  int
}

func (c *conv3) row(i int) ([]uint8, error) {
	if i < 0 {
		i = 0
	} else if i >= c.h {
		i = c.h - 1
	}
	return c.store.get(i)
}

func (c *conv3) nextRow() ([]uint8, error) {
	y := c.next
	if y >= c.h {
		return nil, io.EOF
	}
	for need := min(y+1, c.h-1); c.got <= need; c.got++ {
		r, err := c.prev.nextRow()
		if err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
		if err := c.store.put(c.got, r); err != nil {
			return nil, err
		}
	}
	up, err := c.row(y - 1)
	if err != nil {
		return nil, err
	}
	mid, err := c.row(y)
	if err != nil {
		return nil, err
	}
	down, err := c.row(y + 1)
	if err != nil {
		return nil, err
	}
	last := c.w - 1
	for x := 0; x < c.w; x++ {
		l, r := 4*max(x-1, 0), 4*min(x+1, last)
		m := 4 * x
		for ch := 0; ch < c.nc; ch++ {
			n := int(up[l+ch]) + int(up[m+ch]) + int(up[r+ch]) +
				int(mid[l+ch]) + int(mid[r+ch]) +
				int(down[l+ch]) + int(down[m+ch]) + int(down[r+ch])
			v := 32*int(mid[m+ch]) - n
			// Divided by 24, rounded half away from zero.
			if v >= 0 {
				v = (v + 12) / 24
			} else {
				v = -((-v + 12) / 24)
			}
			c.out[m+ch] = uint8(max(0, min(255, v)))
		}
		if c.nc == 4 {
			// Premultiplied: no colour above the alpha.
			a := c.out[m+3]
			for ch := 0; ch < 3; ch++ {
				c.out[m+ch] = min(c.out[m+ch], a)
			}
		}
	}
	c.next++
	return c.out, nil
}
