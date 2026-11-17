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

// rowPass filters a row on its own into dst (whose alpha stays as it is
// when only the colours are filtered).
type rowPass interface {
	apply(dst, row []uint8)
}

// ---- work shared with helpers ----

// batch says how the passes of a filter share their work. Without helpers
// a pass works out one row at a time, on the goroutine that reads the
// stage; with them, rows at a time, its work split into ways parts done at
// once. Each part does to its rows or columns what one pass alone would:
// the result is the same to the byte.
type batch struct {
	env  *pipeline.Env
	rows int // rows a pass works out at a time
	ways int // parts its work is split into
}

// split cuts [0, n) into at most ways parts of at least least each, their
// ends on multiples of align, and calls fn on them at the same time.
func (w batch) split(n, least, align int, fn func(lo, hi int)) {
	parts := min(w.ways, max(1, n/max(least, 1)))
	if parts <= 1 {
		fn(0, n)
		return
	}
	w.env.Parallel(parts, func(i int) {
		lo, hi := i*n/parts/align*align, (i+1)*n/parts/align*align
		if i == parts-1 {
			hi = n
		}
		fn(lo, hi)
	})
}

// ---- rows of the source ----

// inputRows turns the strips of a Source into rows of premultiplied RGBA,
// optionally keeping each in a store (for a filter that needs the original
// next to its result) and passing it through the horizontal blurs, a
// batch of rows at a time.
type inputRows struct {
	src   pipeline.Source
	w, h  int
	band  rgbaBand
	cur   *image.RGBA
	y     int // rows taken from the source
	keep  rowStore
	hblur []rowPass // one for each part of the work; nil for none
	work  batch
	// The batch: rows of cur, or their blurred copies in out; n of them,
	// handed of those handed out.
	rows      [][]uint8
	out       [][]uint8
	n, handed int
}

func (in *inputRows) nextRow() ([]uint8, error) {
	if in.handed < in.n {
		in.handed++
		return in.rows[in.handed-1], nil
	}
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
		in.work.split(r.Dy(), 1, 1, func(lo, hi int) {
			part := image.Rect(r.Min.X, r.Min.Y+lo, r.Max.X, r.Min.Y+hi)
			draw.Draw(dst, part, s, part.Min, draw.Src)
		})
		in.cur = dst
	}
	n := min(len(in.rows), in.cur.Rect.Max.Y-abs)
	for i := 0; i < n; i++ {
		row := in.cur.Pix[in.cur.PixOffset(sb.Min.X, abs+i):][:4*in.w]
		if in.keep != nil {
			if err := in.keep.put(in.y+i, row); err != nil {
				return nil, err
			}
		}
		in.rows[i] = row
	}
	if in.hblur != nil {
		k := len(in.hblur)
		in.work.env.Parallel(min(k, n), func(g int) {
			for i := g; i < n; i += k {
				in.hblur[g].apply(in.out[i], in.rows[i])
			}
		})
		copy(in.rows, in.out[:n])
	}
	in.y += n
	in.n, in.handed = n, 1
	return in.rows[0], nil
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
	L := float32(math.Sqrt(float64(12.0*float64(sigma2)) + 1.0))
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

// apply puts the blurred row into dst (whose alpha stays as it is when
// only the colours are blurred).
func (h *hbox) apply(dst, row []uint8) {
	h.pass(h.t[0], row)
	h.pass(h.t[1], h.t[0])
	h.pass(dst, h.t[1])
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
// store, and the running sum of its body. It works out a batch of rows at
// a time, its columns shared out.
type vbox struct {
	prev       rowSource
	b          box
	w4, h      int
	store      rowStore
	sum        []uint32
	out        [][]uint8 // the batch
	ra, rb, rc [][]uint8 // the rows each row of the batch is made of
	work       batch
	got        int // input rows taken
	next       int // next output row to work out
	n, handed  int // rows of the batch, of them handed out
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
	if v.handed < v.n {
		v.handed++
		return v.out[v.handed-1], nil
	}
	y0 := v.next
	if y0 >= v.h {
		return nil, io.EOF
	}
	n := min(len(v.out), v.h-y0)
	ri := v.b.ri
	for need := min(y0+n+ri, v.h-1); v.got <= need; v.got++ {
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
	if y0 == 0 {
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
	// Three gets for each row. A store on disk comes with batches of one
	// row, whose three rows then all stay valid; in memory every row does.
	for i := 0; i < n; i++ {
		y := y0 + i
		var err error
		if v.ra[i], err = v.row(y - ri - 1); err != nil {
			return nil, err
		}
		if v.rb[i], err = v.row(y + ri + 1); err != nil {
			return nil, err
		}
		if v.rc[i], err = v.row(y - ri); err != nil {
			return nil, err
		}
	}
	// Every column is a sum of its own. All four channels: the alpha of an
	// opaque image comes in as 255 and stays so.
	v.work.split(v.w4, 4096, 64, func(lo, hi int) {
		sum := v.sum[lo:hi]
		for i := 0; i < n; i++ {
			out := v.out[i][lo:hi]
			a, b, c := v.ra[i][lo:hi], v.rb[i][lo:hi], v.rc[i][lo:hi]
			a, b, c = a[:len(out)], b[:len(out)], c[:len(out)]
			for j := range out {
				s, bj := sum[j], uint32(b[j])
				out[j] = v.b.value(s, uint32(a[j]), bj)
				sum[j] = s + bj - uint32(c[j])
			}
		}
	})
	v.next += n
	v.n, v.handed = n, 1
	return v.out[0], nil
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
	onDisk   bool // a store went to a temporary file
	reserved int64
	opaque   bool
	straight bool // the rows finished are NRGBA
	work     batch
}

func (f *filterStage) Env() *pipeline.Env      { return f.env }
func (f *filterStage) Bounds() image.Rectangle { return f.src.Bounds() }
func (f *filterStage) ColorModel() color.Model {
	if f.straight {
		return color.NRGBAModel
	}
	return color.RGBAModel
}
func (f *filterStage) Opaque() bool { return f.opaque }

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
	if f.straight {
		return &image.NRGBA{Pix: out.Pix, Stride: out.Stride, Rect: out.Rect}, nil
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
		f.onDisk = true
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

// setup builds the passes of f with build: with the helpers of the
// pipeline, if there are any and the image is wide enough to share the
// work out, and everything fits in memory; otherwise (a store would go to
// a temporary file, whose rows are read back one at a time) row by row.
func (f *filterStage) setup(build func() error) error {
	alone := batch{env: f.env, rows: 1, ways: 1}
	if h := f.env.Helpers(); h > 0 && f.in.w >= 256 {
		f.work = batch{env: f.env, rows: max(f.n, 8), ways: h + 1}
		if err := f.prepare(build); err == nil && !f.onDisk {
			return nil
		}
		f.undo()
	}
	f.work = alone
	return f.prepare(build)
}

// prepare runs build after making the batch buffers of the input.
func (f *filterStage) prepare(build func() error) error {
	f.in.work = f.work
	f.in.rows = make([][]uint8, f.work.rows)
	f.in.out = nil
	return build()
}

// undo gives back what a setup took.
func (f *filterStage) undo() {
	for _, s := range f.stores {
		s.close()
	}
	f.stores, f.onDisk = nil, false
	f.env.Budget.Release(f.reserved)
	f.reserved = 0
	f.in.keep, f.in.hblur = nil, nil
}

// rowBatch is n rows of rowBytes, all alpha 255 (see opaqueRow).
func rowBatch(n, rowBytes int) [][]uint8 {
	r := make([][]uint8, n)
	for i := range r {
		r[i] = opaqueRow(rowBytes)
	}
	return r
}

// fail gives back what a stage took while it was being set up, when that
// failed; src stays the caller's.
func (f *filterStage) fail(err error) error {
	f.undo()
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
	rows, ways := f.work.rows, f.work.ways
	// The blurred rows of a batch, and two rows for each part of the work.
	if err := f.reserve(int64(w4)*int64(rows+2*ways), "blur rows"); err != nil {
		return nil, err
	}
	nc := 4
	if f.opaque {
		nc = 3
	}
	f.in.out = rowBatch(rows, w4)
	f.in.hblur = make([]rowPass, ways)
	for g := range f.in.hblur {
		f.in.hblur[g] = &hbox{b: b, w: w, nc: nc, t: [2][]uint8{opaqueRow(w4), opaqueRow(w4)}}
	}
	var last rowSource = f.in
	h := f.in.h
	for i := 0; i < 3; i++ {
		// The sums, and the batch of rows worked out.
		if err := f.reserve(int64(w4)*int64(4+rows), "blur sums"); err != nil {
			return nil, err
		}
		// What a batch of rows is made of: from ri+1 rows above the first
		// to ri+1 below the last.
		st, err := f.newStore(min(2*b.ri+3+rows, h), w4)
		if err != nil {
			return nil, err
		}
		last = &vbox{prev: last, b: b, w4: w4, h: h, store: st, sum: make([]uint32, w4), out: rowBatch(rows, w4),
			ra: make([][]uint8, rows), rb: make([][]uint8, rows), rc: make([][]uint8, rows), work: f.work}
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
	if err := f.setup(func() (err error) {
		f.last, err = f.blurPasses(sigma)
		return err
	}); err != nil {
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
	var keep rowStore
	if err := f.setup(func() (err error) {
		// The originals are kept until the blurred row of the same number
		// comes out: each vertical pass is at most ri+2 rows behind the one
		// before it, and a batch more for each pass and the input.
		if keep, err = f.newStore(min(3*(b.ri+2)+2+4*(f.work.rows-1), f.in.h), w4); err != nil {
			return err
		}
		f.in.keep = keep
		if f.last, err = f.blurPasses(sigma); err != nil {
			return err
		}
		return f.reserve(1<<16, "sharpen table")
	}); err != nil {
		return nil, f.fail(err)
	}
	lut := usmTable(percent, threshold)
	opaque := f.opaque
	f.finish = func(y int, blurred []uint8, dst []uint8) error {
		orig, err := keep.get(y)
		if err != nil {
			return err
		}
		if opaque { // the loop below, with every alpha 255
			orig, blurred = orig[:len(dst)], blurred[:len(dst)]
			for x := 0; x+3 < len(dst); x += 4 {
				o, b, d := orig[x:x+4:x+4], blurred[x:x+4:x+4], dst[x:x+4:x+4]
				d[0] = lut[uint16(o[0])<<8|uint16(b[0])]
				d[1] = lut[uint16(o[1])<<8|uint16(b[1])]
				d[2] = lut[uint16(o[2])<<8|uint16(b[2])]
				d[3] = 255
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
				out := lut[uint16(in)<<8|uint16(bl)]
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

// usmTable holds the unsharp mask's result for every original value (the
// high byte of the index) and blurred value (the low byte).
func usmTable(percent, threshold int) *[1 << 16]uint8 {
	t := new([1 << 16]uint8)
	for in := 0; in < 256; in++ {
		for bl := 0; bl < 256; bl++ {
			out := in
			if diff := in - bl; diff > threshold || -diff > threshold {
				out = max(0, min(255, in+diff*percent/100))
			}
			t[in<<8|bl] = uint8(out)
		}
	}
	return t
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
	nc := 4
	if f.opaque {
		nc = 3
	}
	if err := f.setup(func() error {
		rows := f.work.rows
		if err := f.reserve(int64(w4)*int64(rows), "sharpen rows"); err != nil {
			return err
		}
		// A batch of rows and the one above and below it.
		st, err := f.newStore(min(rows+3, f.in.h), w4)
		if err != nil {
			return err
		}
		f.last = &conv3{prev: f.in, w: f.in.w, h: f.in.h, nc: nc, store: st, out: rowBatch(rows, w4),
			up: make([][]uint8, rows), mid: make([][]uint8, rows), down: make([][]uint8, rows), work: f.work}
		return nil
	}); err != nil {
		return nil, f.fail(err)
	}
	f.finish = func(_ int, row []uint8, dst []uint8) error {
		copy(dst, row)
		return nil
	}
	return f, nil
}

// conv3 applies the mild sharpening kernel of sharp, a batch of rows at a
// time, the rows shared out.
type conv3 struct {
	prev          rowSource
	w, h          int
	nc            int // as hbox's
	store         rowStore
	out           [][]uint8 // the batch
	up, mid, down [][]uint8 // the rows each row of the batch is made of
	work          batch
	got           int
	next          int
	n, handed     int
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
	if c.handed < c.n {
		c.handed++
		return c.out[c.handed-1], nil
	}
	y0 := c.next
	if y0 >= c.h {
		return nil, io.EOF
	}
	n := min(len(c.out), c.h-y0)
	for need := min(y0+n, c.h-1); c.got <= need; c.got++ {
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
	// Three gets for each row, as for vbox.
	for i := 0; i < n; i++ {
		y := y0 + i
		var err error
		if c.up[i], err = c.row(y - 1); err != nil {
			return nil, err
		}
		if c.mid[i], err = c.row(y); err != nil {
			return nil, err
		}
		if c.down[i], err = c.row(y + 1); err != nil {
			return nil, err
		}
	}
	c.work.split(n, 1, 1, func(lo, hi int) {
		for i := lo; i < hi; i++ {
			c.sharpenRow(c.out[i], c.up[i], c.mid[i], c.down[i])
		}
	})
	c.next += n
	c.n, c.handed = n, 1
	return c.out[0], nil
}

func (c *conv3) sharpenRow(out, up, mid, down []uint8) {
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
			out[m+ch] = uint8(max(0, min(255, v)))
		}
		if c.nc == 4 {
			// Premultiplied: no colour above the alpha.
			a := out[m+3]
			for ch := 0; ch < 3; ch++ {
				out[m+ch] = min(out[m+ch], a)
			}
		}
	}
}
