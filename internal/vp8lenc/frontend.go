package vp8lenc

import (
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

const (
	kindARGB  = iota // colour pixels, subtract green if it helps
	kindGray         // gray pixels: subtract green always, the stream only carries green
	kindIndex        // colour indexing transform, the stream carries indices in green
)

// bandRows is how many rows a variant collects before it transforms them:
// enough for a fair sample when subtract green is decided on the first
// band, at least 32768 pixels but no more than 64 rows.
func bandRows(w, h int64) int64 {
	return min(h, max(16, min(64, (32768+w-1)/w)))
}

// frontend is one variant of pass one: it turns image rows into the pixel
// stream the entropy coder sees (transforms applied) and feeds it to its
// tokenizer, whose tokens go to its own spill.
type frontend struct {
	e     *encoder
	kind  int
	pred  bool
	w, h  int
	sw    int   // width of the stream (smaller than w when indices are packed)
	total int64 // pixels in the stream
	fed   int   // rows received

	// Rows are collected in a band when the predictor needs whole block
	// rows, or when subtract green is still to be decided.
	banded  bool
	bits    uint // log2 of the predictor block size
	tw, th  int
	maxRows int
	band    []uint32 // row 0: the last row of the previous band; then up to maxRows rows
	nrows   int
	y0      int // image row of band row 1
	modes   []uint8
	sg      bool
	sgKnown bool
	modeSet []uint8 // the predictors tried
	res     []uint32
	// With the predictor: the band's residuals, and a row of scratch for
	// each of its block rows (see flush).
	resBand []uint32
	tmps    [][]uint32

	// colour indexing
	palette []uint32
	remap   []uint8 // probe index -> palette index, nil for identity
	idx     []byte  // a row of remapped indices
	xbits   uint

	tok    *tokenizer
	tokens *pipeline.Spill
	hi     uint32 // alpha, red and blue of the stream's 1-byte literals

	mem     int64 // budget held
	passOne int64 // the part of mem pass two does not need
	bufMem  int64 // the tokenizer's pixel buffer, needed in pass two with a colour cache
	closed  bool
}

func (f *frontend) reserve(n int64, what string) error {
	if err := f.e.reserve(n, what); err != nil {
		return err
	}
	f.mem += n
	return nil
}

// close frees everything the variant holds.
func (f *frontend) close() {
	if f.closed {
		return
	}
	f.closed = true
	if f.tokens != nil {
		f.tokens.Close()
		f.tokens = nil
	}
	f.band, f.res, f.resBand, f.tmps, f.modes, f.tok = nil, nil, nil, nil, nil, nil
	f.e.unreserve(f.mem)
	f.mem = 0
}

// maxBlocks bounds the number of predictor blocks, whose modes are held in
// memory (a byte each in pass one, about ten when they are coded).
const maxBlocks = 400 << 10

// predictorBits picks the block size of the predictor: as small as the
// format allows (4x4) while the number of blocks stays reasonable. Small
// blocks follow the image closely and their modes cost little to code.
func predictorBits(w, h int64, least uint) uint {
	bits := least
	for bits < 9 && ((w+1<<bits-1)>>bits)*((h+1<<bits-1)>>bits) > maxBlocks {
		bits++
	}
	return bits
}

// newFrontend starts a variant. share is the number of variants that still
// have to fit in the budget, this one included.
func (e *encoder) newFrontend(s spec, palette []uint32, share int) (*frontend, error) {
	f := &frontend{e: e, kind: s.kind, pred: s.pred, w: e.w, h: e.h, sw: e.w, modeSet: e.p.modes}
	if s.simple {
		f.modeSet = modesFast
	}
	e.fes = append(e.fes, f)
	if err := f.init(palette, s.sg, int64(share)); err != nil {
		f.close()
		return nil, err
	}
	return f, nil
}

func (f *frontend) init(palette []uint32, sg int, share int64) error {
	e := f.e
	h := int64(e.h)
	if f.kind == kindIndex {
		f.palette = palette
		switch n := len(palette); {
		case n <= 2:
			f.xbits = 3
		case n <= 4:
			f.xbits = 2
		case n <= 16:
			f.xbits = 1
		}
		f.sw = (e.w + 1<<f.xbits - 1) >> f.xbits
		if f.pred {
			// Similar colours get close indices, so indices can be predicted.
			f.palette, f.remap = reorder(palette, orderBy(palette, brightness))
		} else {
			var err error
			if f.palette, f.remap, err = e.cheapestPalette(palette); err != nil {
				return err
			}
		}
	}
	sw := int64(f.sw)
	f.total = sw * h
	f.sg = f.kind == kindGray || f.kind == kindARGB && sg == sgOn
	f.sgKnown = f.kind != kindARGB || sg != sgAuto
	// Rows go through the band when the predictor follows; a variant that
	// may yet have to decide on subtract green has one too.
	f.banded = f.pred || f.kind == kindARGB

	rows := sw * 4 // res
	if f.remap != nil {
		rows += int64(e.w)
		f.idx = make([]byte, e.w)
	}
	if f.banded {
		f.maxRows = int(bandRows(sw, h))
		if f.pred {
			tiles := func(b uint) int64 { return ((sw + 1<<b - 1) >> b) * ((h + 1<<b - 1) >> b) }
			least := uint(2)
			if f.kind != kindARGB {
				least = 3 // single-channel residuals are cheap; modes weigh more
			}
			bits := predictorBits(sw, h, least)
			// The band holds whole block rows; its residuals are held
			// too, and a row of scratch for each block row.
			rowsFor := func(b uint) int64 { return min(h, (bandRows(sw, h)+1<<b-1)>>b<<b) }
			bandMem := func(b uint) int64 {
				r := rowsFor(b)
				return (2*r + 1 + (r+1<<b-1)>>b) * sw * 4
			}
			for bits > 2 && bandMem(bits) > e.avail()/(4*share) {
				bits--
			}
			// Pass two holds the modes as pixels while it codes them.
			for bits < 9 && tiles(bits)*4+subImageMem(tiles(bits), true) > e.avail()/(4*share) {
				bits++
			}
			f.bits = bits
			f.tw, f.th = int((sw+1<<bits-1)>>bits), int((h+1<<bits-1)>>bits)
			f.maxRows = int(rowsFor(bits))
			rows += bandMem(bits) - (int64(f.maxRows)+1)*sw*4 + tiles(bits)
		}
		rows += int64(f.maxRows+1) * sw * 4
	}
	if err := f.reserve(rows, "webp transform rows"); err != nil {
		return err
	}
	f.passOne = rows
	f.res = make([]uint32, f.sw)
	if f.banded {
		f.band = make([]uint32, (f.maxRows+1)*f.sw)
	}
	if f.pred {
		f.resBand = make([]uint32, f.maxRows*f.sw)
		f.tmps = make([][]uint32, (f.maxRows+1<<f.bits-1)>>f.bits)
		for i := range f.tmps {
			f.tmps[i] = make([]uint32, f.sw)
		}
		f.modes = make([]uint8, f.tw*f.th)
		f.passOne -= int64(len(f.modes))
	}

	window := e.p.window
	if f.pred {
		window = e.p.predWin
	}
	for window > 16 && int64(window/2) >= f.total {
		window /= 2
	}
	mem := func(win int) (buf, hash int64) {
		nbuf, nhead, nprev := tokenizerSizes(f.total, win)
		return int64(nbuf) * 4, int64(nhead+nprev) * 4
	}
	for window > 4096 {
		b, hs := mem(window)
		if b+hs <= e.avail()/(2*share) {
			break
		}
		window /= 2
	}
	bufMem, hashMem := mem(window)
	hashMem += tokenFlush + 1024
	if err := f.reserve(bufMem+hashMem, "webp LZ77 window"); err != nil {
		return err
	}
	f.bufMem = bufMem
	f.passOne += hashMem
	litBytes := int64(1)
	switch {
	case f.kind == kindARGB:
		litBytes = 4
	case !f.pred:
		// Untransformed gray and index pixels are opaque; residuals are not.
		f.hi = 0xff000000
	}
	f.tokens = pipeline.NewSpill(e.env)
	e.expect(f.tokens, f.total*litBytes+f.total*litBytes/255+64)
	f.tok = newTokenizer(f.sw, f.total, window, nil, f.tokens, newStats(e.p.cache))
	if e.p.optimal && (f.pred || f.kind == kindIndex) {
		if err := f.reserve(optimalBytes, "webp optimal parse"); err != nil {
			return err
		}
		f.passOne += optimalBytes
		f.tok.opt = newOptimal()
	}
	f.tok.lit1 = litBytes == 1
	f.tok.chain, f.tok.nice, f.tok.lazy = e.p.chain, e.p.nice, e.p.lazy
	if !f.pred && f.kind != kindIndex {
		f.tok.chain, f.tok.nice = e.p.chainRaw, e.p.niceRaw
	}
	return nil
}

// feedRow takes the next image row as ARGB pixels.
func (f *frontend) feedRow(row []uint32) error {
	f.fed++
	if !f.banded {
		res := f.res
		for i, p := range row {
			res[i] = subGreen(p)
		}
		return f.tok.push(res)
	}
	dst := f.band[(1+f.nrows)*f.sw:][:f.sw]
	if f.sg {
		for i, p := range row {
			dst[i] = subGreen(p)
		}
	} else {
		copy(dst, row)
	}
	return f.rowDone()
}

// feedIndex takes the next image row as indices into the probe's colour
// table.
func (f *frontend) feedIndex(idx []byte) error {
	f.fed++
	if f.remap != nil {
		for i, v := range idx {
			f.idx[i] = f.remap[v]
		}
		idx = f.idx
	}
	dst := f.res
	if f.banded {
		dst = f.band[(1+f.nrows)*f.sw:][:f.sw]
	}
	if f.xbits == 0 {
		for i, v := range idx {
			dst[i] = 0xff000000 | uint32(v)<<8
		}
	} else {
		for i := range dst {
			dst[i] = 0xff000000
		}
		bpp, mask := uint(8)>>f.xbits, 1<<f.xbits-1
		for x, v := range idx {
			dst[x>>f.xbits] |= uint32(v) << (8 + bpp*uint(x&mask))
		}
	}
	if f.banded {
		return f.rowDone()
	}
	return f.tok.push(dst)
}

func (f *frontend) rowDone() error {
	f.nrows++
	if f.nrows == f.maxRows || f.fed == f.h {
		return f.flush()
	}
	return nil
}

// decideSG picks subtract green from the first band: it is used unless red
// and blue (their left-neighbour residuals when the predictor follows) get
// more expensive with it.
func (f *frontend) decideSG() {
	var plain, sub [2][256]uint32
	for r := 1; r <= f.nrows; r++ {
		row := f.band[r*f.sw : (r+1)*f.sw]
		for x := 1; x < len(row); x++ {
			a, b := row[x], subGreen(row[x])
			if f.pred {
				a, b = subPixels(a, row[x-1]), subPixels(b, subGreen(row[x-1]))
			}
			plain[0][a>>16&0xff]++
			plain[1][a&0xff]++
			sub[0][b>>16&0xff]++
			sub[1][b&0xff]++
		}
	}
	f.sg = entropy(sub[0][:])+entropy(sub[1][:]) <= entropy(plain[0][:])+entropy(plain[1][:])
	f.sgKnown = true
	if f.sg {
		rows := f.band[f.sw : (f.nrows+1)*f.sw]
		for i, p := range rows {
			rows[i] = subGreen(p)
		}
	}
}

// modeBias is the cost (1/16 bit) charged for a block whose predictor
// differs from its left neighbour's: the modes are coded too.
const modeBias = 16 * 3

// chooseModes picks, for every block of the block row made of band rows
// r0 to r1-1, the predictor that makes its residuals cheapest under the
// cost model cm. tmp is a row of scratch.
func (f *frontend) chooseModes(r0, r1 int, cm *costModel, tmp []uint32) {
	w := f.sw
	row := ((f.y0 + r0 - 1) >> f.bits) * f.tw
	left := uint8(255)
	for tx := 0; tx < f.tw; tx++ {
		x0, x1 := max(tx<<f.bits, 1), min(w, (tx+1)<<f.bits)
		best, bestCost := f.modeSet[0], uint32(math.MaxUint32)
		for _, m := range f.modeSet {
			var c uint32
			if m != left {
				c = modeBias
			}
			for r := r0; r < r1 && c < bestCost; r++ {
				if f.y0+r-1 == 0 {
					continue
				}
				residuals(m, f.band[r*w:(r+1)*w], f.band[(r-1)*w:r*w+1], x0, x1, tmp)
				for _, d := range tmp[x0:x1] {
					c += cm.pixel(d)
				}
			}
			if c < bestCost {
				best, bestCost = m, c
			}
		}
		f.modes[row+tx] = best
		left = best
	}
}

// flush transforms the rows in the band and sends them on.
func (f *frontend) flush() error {
	if !f.sgKnown {
		f.decideSG()
	}
	w := f.sw
	if !f.pred {
		for r := 1; r <= f.nrows; r++ {
			if err := f.tok.push(f.band[r*w : (r+1)*w]); err != nil {
				return err
			}
		}
		f.nrows = 0
		return nil
	}
	// The predictors of the band's block rows are chosen with the cost
	// model as the band starts, each block row on its own, and the
	// residuals of its rows are made each on its own too: at the same
	// time, with helpers. Then the rows go to the tokenizer, in order.
	cm := f.tok.cm
	env := f.e.env
	blocks := (f.nrows + 1<<f.bits - 1) >> f.bits
	env.Parallel(blocks, func(i int) {
		r0 := 1 + i<<f.bits
		f.chooseModes(r0, min(r0+1<<f.bits, f.nrows+1), &cm, f.tmps[i])
	})
	env.Parallel(f.nrows, func(i int) {
		r := i + 1
		res := f.resBand[i*w : (i+1)*w]
		cur := f.band[r*w : (r+1)*w]
		if f.y0+r-1 == 0 {
			// The first row is predicted from the left, its first pixel
			// from opaque black.
			res[0] = subPixels(cur[0], 0xff000000)
			residuals(1, cur, nil, 1, w, res)
			return
		}
		up := f.band[(r-1)*w : r*w+1]
		res[0] = subPixels(cur[0], up[0]) // the first column is predicted from above
		y := f.y0 + r - 1
		for tx, m := range f.modes[(y>>f.bits)*f.tw:][:f.tw] {
			residuals(m, cur, up, max(tx<<f.bits, 1), min(w, (tx+1)<<f.bits), res)
		}
	})
	for i := 0; i < f.nrows; i++ {
		if err := f.tok.push(f.resBand[i*w : (i+1)*w]); err != nil {
			return err
		}
	}
	copy(f.band[:w], f.band[f.nrows*w:(f.nrows+1)*w])
	f.y0 += f.nrows
	f.nrows = 0
	return nil
}

// finish ends pass one and frees what only pass one needs. The tokenizer's
// pixel buffer stays: pass two needs it if a colour cache is used.
func (f *frontend) finish() error {
	if f.fed != f.h {
		return io.ErrUnexpectedEOF
	}
	if err := f.tok.finish(); err != nil {
		return err
	}
	f.band, f.res, f.resBand, f.tmps = nil, nil, nil, nil
	f.tok.head, f.tok.prev, f.tok.obuf, f.tok.opt = nil, nil, nil, nil
	f.e.unreserve(f.passOne)
	f.mem -= f.passOne
	f.passOne = 0
	return nil
}

// dropBuf frees the tokenizer's pixel buffer.
func (f *frontend) dropBuf() {
	f.tok.buf = nil
	f.e.unreserve(f.bufMem)
	f.mem -= f.bufMem
	f.bufMem = 0
}
