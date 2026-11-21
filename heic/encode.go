package heic

import (
	"errors"
	"image"
	"io"
	"strings"

	"github.com/whhhh1500/goimgtools"
	"github.com/whhhh1500/goimgtools/heic/internal/kvazaar"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// effortOptions are Kvazaar's settings for each effort, 1 to 9: a preset,
// and what is changed from it after the options of every effort (those
// of kvazaarOptions).
var effortOptions = [10][2]string{
	1: {"ultrafast", ""},
	2: {"superfast", "rdoq=1\n"},
	3: {"fast", "rdoq=1\n"},
	4: {"medium", ""},
	5: {"medium", "intra-chroma-search=1\nsignhide=1\n"},
	6: {"slower", "intra-chroma-search=1\n"},
	7: {"slower", "intra-chroma-search=1\ntr-depth-intra=1\n"},
	8: {"slower", "intra-chroma-search=1\ntr-depth-intra=1\nrd=4\n"},
	9: {"slower", "intra-chroma-search=1\ntr-depth-intra=1\nrd=5\n"},
}

// kvazaarOptions are the options of a picture: the effort's preset, then a
// softer deblocking filter, a QP 2 lower for Cb and Cr (with their
// distortion weighted 2.5 against luma's 0.8, where Kvazaar weighs it 1.5),
// no wavefronts (a picture is coded on one thread, so their entry points
// would only take bits), the colour description of the VUI, which says what
// the nclx box of the file says, and the rest of the effort's options.
func kvazaarOptions(effort int, lossless bool) string {
	var b strings.Builder
	b.WriteString("preset=" + effortOptions[effort][0] + "\n")
	b.WriteString("wpp=0\nrange=pc\ncolorprim=bt709\ntransfer=iec61966-2-1\n")
	if lossless {
		b.WriteString("lossless=1\ncolormatrix=GBR\n")
	} else {
		b.WriteString("deblock=-1:-1\nchroma-qp-offset=-2\nchroma-rd-weight=2.5\ncolormatrix=smpte170m\n")
	}
	b.WriteString(effortOptions[effort][1])
	return b.String()
}

// The memory of coding a picture: its module's to begin with, and for each
// pixel its planes (1.5 bytes at 4:2:0, 3 at 4:4:4) and what Kvazaar takes
// on top while it codes them (measured, the module's memory after coding
// 512 to 2048 samples square: 1.3MB and 6.2 bytes a pixel in all at 4:2:0,
// 2MB and 16.6 at 4:4:4, whatever the effort). The alpha of a tile is coded
// after its colour in the same module, in less.
const moduleBytes = 2 << 20

func planeBytes(c kvazaar.Chroma, pixels int64) int64 {
	if c == kvazaar.Chroma444 {
		return 3 * pixels
	}
	return pixels * 3 / 2
}

func codeBytes(c kvazaar.Chroma, pixels int64) int64 {
	if c == kvazaar.Chroma444 {
		return moduleBytes + 18*pixels
	}
	return moduleBytes + 7*pixels
}

// An image of more pixels than wholePixels (at 4:2:0; half as many at
// 4:4:4) is coded as a grid of tiles of tileSide samples, so that its
// memory stays small whatever its size, and its tiles can be coded at once.
// A grid costs bytes: on a 3840 x 2160 photograph at equal SSIMULACRA2,
// against the picture coded whole, tiles of 1024 take 0.97% more (0.4% at
// quality 65); with an earlier adaptive QP, tiles of 512 took 1.67%, of 1024
// 1.19% and of 2048 0.75%, whose pictures would leave room under the
// default memory limit for no second one to be coded at once.
var (
	tileSide          = 1024
	wholePixels int64 = 4 << 20
)

// keepOpen is a source whose Close leaves the source it wraps open: the
// encoder's caller closes it.
type keepOpen struct{ pipeline.Source }

func (keepOpen) Close() error { return nil }

// encoder converts the rows of the image into a row of tiles (or the one
// picture), codes its tiles when it is full, and keeps what they came to.
type encoder struct {
	env           *pipeline.Env
	width, height int
	chroma        kvazaar.Chroma
	lossless      bool
	qp            int
	options       string
	// The layout: pictures of pw x ph samples, cols across, rows down.
	pw, ph     int
	cols, rows int
	// The row of tiles being filled, ph rows of cols x pw samples: luma
	// (or G), Cb (or B), Cr (or R), alpha; cw x ch is the size of a tile's
	// Cb and Cr. top is the row of the layout it starts at, done the rows
	// stored.
	yb, cbb, crb, ab []byte
	cw, ch           int
	top, done        int
	reserved         int64
	// A row converted, cols x pw samples, as yb, cbb, crb and ab hold them;
	// and the even row of a pair, kept for 4:2:0.
	y, cb, cr, a []byte
	pending      []byte
	hasPending   bool
	taken        int // rows of the image taken
	wantAlpha    bool
	sawAlpha     bool
	color, alpha []coded
}

func encode(w io.Writer, src pipeline.Source, o goimgtools.EncodeOptions) error {
	opts, _ := o.Options.(Options)
	q := o.Quality
	if q == 0 {
		q = opts.Quality
	}
	if q == 0 {
		q = DefaultQuality
	}
	if q < 1 || q > 100 {
		return errors.New("heic: quality must be 1 to 100")
	}
	effort := opts.Effort
	if effort == 0 {
		effort = 5
	}
	if effort < 1 || effort > 9 {
		return errors.New("heic: effort must be 1 to 9")
	}
	b := src.Bounds()
	e := &encoder{env: pipeline.EnvOf(src), width: b.Dx(), height: b.Dy(), chroma: kvazaar.Chroma420,
		lossless: opts.Lossless, qp: 51 - q*50/100, options: kvazaarOptions(effort, opts.Lossless),
		wantAlpha: !pipeline.IsOpaque(src)}
	if e.width <= 0 || e.height <= 0 {
		return errors.New("heic: the image has no pixels")
	}
	if opts.Chroma444 || opts.Lossless {
		e.chroma = kvazaar.Chroma444
	}
	defer e.release()
	if err := e.layout(); err != nil {
		return err
	}
	strips := pipeline.Map(keepOpen{src}, func(*image.NRGBA) error { return nil })
	defer strips.Close()
	for {
		s, err := strips.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		m := s.(*image.NRGBA)
		for y := range m.Rect.Dy() {
			if err := e.row(m.Pix[y*m.Stride:][:4*e.width]); err != nil {
				return err
			}
		}
	}
	if err := e.finish(); err != nil {
		return err
	}
	p := picture{width: e.width, height: e.height, cols: e.cols, rows: e.rows,
		color: e.color, channels: 3, matrix: matrixBT601, rext: e.chroma == kvazaar.Chroma444,
		icc: o.ICC, exif: o.Exif, xmp: o.Xmp}
	if e.lossless {
		p.matrix = matrixGBR
	}
	if e.cols*e.rows > 1 {
		p.tile = e.pw
	} else {
		p.storedW, p.storedH = e.pw, e.ph
	}
	if e.sawAlpha {
		p.alpha = e.alpha
	}
	file, err := p.marshal()
	if err != nil {
		return err
	}
	_, err = w.Write(file)
	return err
}

// layout picks one picture or a grid of tiles, and makes the buffers of a
// row of them. Kvazaar codes pictures of an even size: an odd size is coded
// one larger, its last column or row repeated, which the clap box of the
// file takes off again.
func (e *encoder) layout() error {
	whole := wholePixels
	if e.chroma == kvazaar.Chroma444 {
		whole /= 2
	}
	if int64(e.width)*int64(e.height) <= whole {
		e.pw, e.ph = (e.width+1)&^1, (e.height+1)&^1
		e.cols, e.rows = 1, 1
	} else {
		e.pw, e.ph = tileSide, tileSide
		e.cols, e.rows = (e.width+tileSide-1)/tileSide, (e.height+tileSide-1)/tileSide
		// A grid has at most 256 tiles each way, and a file 65535 items:
		// tiles, their alpha and the grids.
		if e.cols > 256 || e.rows > 256 || 2*e.cols*e.rows+4 > 0xffff {
			return errors.New("heic: the image is too large for a grid of tiles")
		}
	}
	e.cw, e.ch = e.pw, e.ph
	if e.chroma == kvazaar.Chroma420 {
		e.cw, e.ch = e.pw/2, e.ph/2
	}
	n := int64(e.cols) * int64(e.pw)
	need := planeBytes(e.chroma, n*int64(e.ph)) + 4*n + int64(4*e.width)
	if e.wantAlpha {
		need += n * int64(e.ph)
	}
	if err := e.env.Budget.Reserve(need, "heic rows"); err != nil {
		return err
	}
	e.reserved = need
	e.yb = make([]byte, n*int64(e.ph))
	e.cbb = make([]byte, int64(e.cols*e.cw)*int64(e.ch))
	e.crb = make([]byte, len(e.cbb))
	e.y, e.cb, e.cr = make([]byte, n), make([]byte, n), make([]byte, n)
	if e.wantAlpha {
		e.ab = make([]byte, len(e.yb))
		e.a = make([]byte, n)
	}
	return nil
}

// release frees what the encoder holds.
func (e *encoder) release() {
	e.yb, e.cbb, e.crb, e.ab = nil, nil, nil, nil
	e.env.Budget.Release(e.reserved)
	e.reserved = 0
}

// row takes the next row of the image, NRGBA.
func (e *encoder) row(px []byte) error {
	e.taken++
	if e.chroma != kvazaar.Chroma420 {
		e.convert444(px)
		return e.store(true)
	}
	if !e.hasPending {
		e.pending = append(e.pending[:0], px...)
		if e.taken < e.height {
			e.hasPending = true
			return nil
		}
		// The last row of an odd height pairs with itself.
		return e.pair(e.pending, e.pending)
	}
	e.hasPending = false
	return e.pair(e.pending, px)
}

// convert444 converts one row to 4:4:4 (or G, B and R).
func (e *encoder) convert444(px []byte) {
	n := e.width
	if e.lossless {
		for x := range n {
			p := px[4*x : 4*x+4 : 4*x+4]
			e.y[x], e.cb[x], e.cr[x] = p[1], p[2], p[0]
		}
	} else {
		for x := range n {
			p := px[4*x : 4*x+4 : 4*x+4]
			r, g, b := int32(p[0]), int32(p[1]), int32(p[2])
			e.y[x] = uint8((19595*r + 38470*g + 7471*b + 1<<15) >> 16)
			e.cb[x] = clamp8((-11056*r - 21712*g + 32768*b + 257<<15) >> 16)
			e.cr[x] = clamp8((32768*r - 27440*g - 5328*b + 257<<15) >> 16)
		}
	}
	pad(e.y, n)
	pad(e.cb, n)
	pad(e.cr, n)
	e.alphaRow(px)
}

// pair converts two rows to 4:2:0 and stores them; each sample of Cb and Cr
// is the average of the four of the full picture, rounded once.
func (e *encoder) pair(p0, p1 []byte) error {
	n := e.width
	half := (n + 1) / 2
	for x := range half {
		x1 := min(2*x+1, n-1)
		var sb, sr int32
		for _, p := range [4][]byte{p0[8*x:], p0[4*x1:], p1[8*x:], p1[4*x1:]} {
			r, g, b := int32(p[0]), int32(p[1]), int32(p[2])
			sb += -11056*r - 21712*g + 32768*b
			sr += 32768*r - 27440*g - 5328*b
		}
		e.cb[x] = clamp8((sb + 257<<17) >> 18)
		e.cr[x] = clamp8((sr + 257<<17) >> 18)
	}
	pad(e.cb[:e.cols*e.cw], half)
	pad(e.cr[:e.cols*e.cw], half)
	for i, p := range [2][]byte{p0, p1} {
		for x := range n {
			q := p[4*x : 4*x+4 : 4*x+4]
			e.y[x] = uint8((19595*int32(q[0]) + 38470*int32(q[1]) + 7471*int32(q[2]) + 1<<15) >> 16)
		}
		pad(e.y, n)
		e.alphaRow(p)
		if err := e.store(i == 1); err != nil {
			return err
		}
	}
	return nil
}

// alphaRow keeps the alpha of a row.
func (e *encoder) alphaRow(px []byte) {
	if !e.wantAlpha {
		return
	}
	for x := range e.width {
		a := px[4*x+3]
		e.a[x] = a
		if a != 255 {
			e.sawAlpha = true
		}
	}
	pad(e.a, e.width)
}

// store puts the converted row into the row of tiles (its chroma too when
// withChroma: on every row for 4:4:4, on the second of a pair for 4:2:0),
// and codes the tiles once they are full.
func (e *encoder) store(withChroma bool) error {
	at := e.done - e.top
	n := e.cols * e.pw
	copy(e.yb[at*n:][:n], e.y)
	if withChroma {
		cn, crow := e.cols*e.cw, at*e.ch/e.ph
		copy(e.cbb[crow*cn:][:cn], e.cb)
		copy(e.crb[crow*cn:][:cn], e.cr)
	}
	if e.wantAlpha {
		copy(e.ab[at*n:][:n], e.a)
	}
	e.done++
	if e.done-e.top == e.ph {
		return e.code()
	}
	return nil
}

// finish repeats the last row down to the end of the last tile row.
func (e *encoder) finish() error {
	if e.done < e.height {
		return io.ErrUnexpectedEOF
	}
	for e.done < e.rows*e.ph {
		if err := e.store(true); err != nil {
			return err
		}
	}
	return nil
}

// code codes the tiles of the row, as many at once as the pipeline has
// threads for and its memory limit room for (one at least); what comes
// out does not depend on how many.
func (e *encoder) code() error {
	per := codeBytes(e.chroma, int64(e.pw)*int64(e.ph))
	k := min(e.cols, e.env.Helpers()+1)
	if free := e.env.Budget.Limit() - e.env.Budget.Used(); int64(k)*per > free {
		k = int(max(1, free/per))
	}
	need := int64(k) * per
	if err := e.env.Budget.Reserve(need, "heic coding"); err != nil {
		return err
	}
	defer e.env.Budget.Release(need)
	color := make([]coded, e.cols)
	alpha := make([]coded, e.cols)
	errs := make([]error, e.cols)
	for i0 := 0; i0 < e.cols; i0 += k {
		e.env.Parallel(min(k, e.cols-i0), func(j int) {
			errs[i0+j] = e.codeTile(i0+j, &color[i0+j], &alpha[i0+j])
		})
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	e.color = append(e.color, color...)
	if e.wantAlpha {
		e.alpha = append(e.alpha, alpha...)
	}
	e.top = e.done
	return nil
}

// codeTile codes tile i of the row, and its alpha after it in the same
// module.
func (e *encoder) codeTile(i int, color, alpha *coded) error {
	enc, err := kvazaar.NewEncoder()
	if err != nil {
		return err
	}
	p, err := enc.NewPicture(e.pw, e.ph, e.chroma)
	if err != nil {
		return err
	}
	cut := func(p *kvazaar.Picture, plane int, buf []byte, w, h int) {
		dst, stride := p.Plane(plane)
		for r := range h {
			copy(dst[r*stride:][:w], buf[r*e.cols*w+i*w:][:w])
		}
	}
	cut(p, 0, e.yb, e.pw, e.ph)
	cut(p, 1, e.cbb, e.cw, e.ch)
	cut(p, 2, e.crb, e.cw, e.ch)
	if !e.lossless {
		if err := adaptQP(p, e.chroma, e.pw, e.ph); err != nil {
			return err
		}
	}
	s, err := p.Encode(e.qp, e.options)
	if err != nil {
		return err
	}
	if *color, err = splitStream(s); err != nil || !e.wantAlpha {
		return err
	}
	if p, err = enc.NewPicture(e.pw, e.ph, kvazaar.Chroma400); err != nil {
		return err
	}
	cut(p, 0, e.ab, e.pw, e.ph)
	if s, err = p.Encode(e.qp, e.options); err != nil {
		return err
	}
	*alpha, err = splitStream(s)
	return err
}

// varThresholds are where the QP delta of a coding tree unit steps up by
// one, from -13 below the first to 12 above the last: Kvazaar's variance
// adaptive quantization at strength 16 (1.6·ln(variance/6000), rounded), the
// variance being that of its luma, Cb and Cr added (each the variance of
// its samples there, a unit at the edge of the picture extended by its
// edge samples), times 4096² to be an integer. Kvazaar's own measures the
// variance against the picture's: a grid's tiles would each be measured
// against their own, giving tiles alike different QPs (on a 3840 x 2160
// photograph, tiles of 512 came to 12.7% more bytes at equal SSIMULACRA2
// than the picture coded whole); against one value, the same for every
// picture, they get what the whole picture would, and a picture of any size
// the QP a quality stands for. The values are integers so that the deltas
// are the same on every processor. The strength and the value were chosen
// on Kodak photographs at effort 6, by how much larger than x265's their
// files were at equal SSIMULACRA2: strength 0.8 to 2.0 (1.2 and 1.6 alike);
// on eight of them the value 1000 4.3%, 2500 (about the variance of a
// Kodak photograph) 2.2%, 5000 0.5%, 10000 1.5%, 20000 3.1%, and on eight
// others 2500 3.0%, 3500 2.4%, 5000 1.7%, 7000 0.9%.
var varThresholds = [25]int64{40732916, 76099106, 142171848, 265611980, 496228508, 927076905,
	1732007680, 3235816347, 6045300810, 11294108800, 21100173109, 39420313113, 73646840614,
	137590412256, 257052731479, 480237726433, 897202191015, 1676194366364, 3131543348830,
	5850493201977, 10930160273578, 20420227745199, 38150007934814, 71273598100223, 133156611522392}

// adaptQP gives the picture the QP delta of each coding tree unit.
func adaptQP(p *kvazaar.Picture, c kvazaar.Chroma, w, h int) error {
	cols, rows := (w+63)/64, (h+63)/64
	m := p.Deltas(cols, rows)
	if m == nil {
		return errors.New("heic: out of memory for the QP map")
	}
	sub, scale := 2, int64(16)
	if c == kvazaar.Chroma444 {
		sub, scale = 1, 1
	}
	y, ys := p.Plane(0)
	cb, cs := p.Plane(1)
	cr, _ := p.Plane(2)
	for by := range rows {
		for bx := range cols {
			v := blockVariance(y, ys, w, h, bx*64, by*64, 64)
			v += scale * (blockVariance(cb, cs, w/sub, h/sub, bx*64/sub, by*64/sub, 64/sub) +
				blockVariance(cr, cs, w/sub, h/sub, bx*64/sub, by*64/sub, 64/sub))
			d := -13
			for _, t := range varThresholds {
				if v < t {
					break
				}
				d++
			}
			m[by*cols+bx] = byte(int8(d))
		}
	}
	return nil
}

// blockVariance is n·Σx² - (Σx)² over the n x n block at x0, y0 of a plane
// of w x h samples, extended past its edges by its edge samples: the
// variance times n².
func blockVariance(plane []byte, stride, w, h, x0, y0, n int) int64 {
	var s, ss int64
	for y := range n {
		row := plane[min(y0+y, h-1)*stride:]
		for x := range n {
			v := int64(row[min(x0+x, w-1)])
			s += v
			ss += v * v
		}
	}
	return int64(n*n)*ss - s*s
}

// pad repeats the last of the first n samples to the end of row.
func pad(row []byte, n int) {
	if n <= 0 || n >= len(row) {
		return
	}
	v := row[n-1]
	for i := n; i < len(row); i++ {
		row[i] = v
	}
}

func clamp8(v int32) uint8 { return uint8(min(max(v, 0), 255)) }
