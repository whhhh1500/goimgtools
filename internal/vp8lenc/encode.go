// Package vp8lenc writes lossless WebP (VP8L, RFC 9649) from a strip source.
//
// VP8L codes the whole image as one entropy-coded stream whose prefix codes
// come before the pixel data, so the image cannot be written in one pass.
// Encode makes two:
//
//  1. The strips are transformed a block row at a time (subtract green and
//     the spatial predictor with a mode chosen per block, or a colour table
//     for images of at most 256 colours), the resulting pixel stream goes
//     through LZ77 over a sliding window of recent pixels, and the tokens
//     are appended to a pipeline.Spill (a temporary file once they do not fit
//     the memory budget) while their histograms are counted. Which
//     transforms pay off depends on the image (a photo wants the predictor,
//     a screenshot does not), so a few variants start side by side and the
//     ones that fall behind are dropped as soon as that is clear.
//  2. The tokens of the smallest variant are replayed from the spill: a few
//     times to sort the blocks of the image into groups with prefix codes
//     of their own (see meta.go), which also gives the exact size of the
//     bitstream for the RIFF header, and once more to write them as bits.
//     The colour cache is applied here too: its best size is only known
//     when pass one is over.
//
// Memory is bounded by a few block rows of pixels, the LZ77 windows and
// what is kept per block (a predictor mode, a group index), whatever the
// image size.
package vp8lenc

import (
	"errors"
	"fmt"
	"io"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// Options are the encoding parameters.
type Options struct {
	// Effort trades speed for size: 1 is the fastest, 9 gives the smallest
	// files. Zero means the default (5).
	Effort int
}

// DefaultEffort is used when Options is nil or Effort is zero.
const DefaultEffort = 5

// maxDim is the largest width or height a VP8L header can hold.
const maxDim = 1 << 14

// params are the knobs an effort level sets.
type params struct {
	modes     []uint8 // predictor modes tried per block
	chain     int     // hash chain positions examined per match search
	nice      int     // stop searching at a match this long
	lazy      bool
	window    int     // LZ77 window in pixels (power of two) without predictor
	predWin   int     // the same with predictor: residuals rarely repeat far back
	cache     []uint  // colour cache sizes (bits) evaluated
	variants  int     // transform variants raced against each other
	dropRatio float64 // a variant is dropped when it is this much worse than the best
	groups    int     // most Huffman groups an image is split into
	groupBits uint    // log2 of the block size of the entropy image
	groupIter int     // refinement replays after the first grouping
}

// The predictor modes in the order they are tried; on a tie the earlier
// one wins.
var (
	modesFast = []uint8{1, 2, 11}
	modesMid  = []uint8{1, 2, 11, 12, 7, 5, 13}
	modesAll  = []uint8{1, 2, 11, 12, 13, 7, 5, 10, 9, 8, 6, 3, 4, 0}
)

func paramsFor(effort int) params {
	p := params{lazy: effort >= 3, variants: 5, dropRatio: 1.15}
	switch {
	case effort <= 1:
		p.modes, p.dropRatio = modesFast, 1
	case effort <= 3:
		p.modes, p.dropRatio = modesMid, 1
	case effort >= 7:
		p.modes, p.dropRatio = modesAll, 1.3
	default:
		p.modes = modesAll
	}
	p.groups = [...]int{0, 0, 8, 16, 24, 32, 48, 64, 64, 64}[effort]
	p.groupBits = 4
	p.groupIter = [...]int{0, 0, 1, 2, 3, 4, 4, 5, 6, 8}[effort]
	p.chain = [...]int{0, 4, 8, 16, 24, 32, 48, 64, 128, 256}[effort]
	p.nice = [...]int{0, 32, 64, 128, 256, 256, 512, 1024, 4096, 4096}[effort]
	p.window = 1 << [...]uint{0, 17, 17, 18, 18, 19, 19, 20, 20, 20}[effort]
	p.predWin = 1 << [...]uint{0, 15, 15, 15, 15, 16, 16, 16, 16, 16}[effort]
	switch {
	case effort <= 1:
		p.cache = []uint{10}
	case effort <= 4:
		p.cache = []uint{4, 7, 10}
	default:
		p.cache = []uint{1, 2, 3, 4, 5, 6, 7, 8, 9, 10}
	}
	return p
}

// Encode writes src as a lossless WebP file: a RIFF container holding one
// VP8L chunk. The decoded image has exactly the pixels color.NRGBAModel
// gives for the source's (8-bit, non-premultiplied; 16-bit sources keep
// their high bytes).
//
// It does not close src. If it returns an error, what was written to w is
// not a valid image and must be discarded.
func Encode(w io.Writer, src pipeline.Source, o *Options) error {
	b := src.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return fmt.Errorf("goimgtools: webp: invalid image size %dx%d", b.Dx(), b.Dy())
	}
	if b.Dx() > maxDim || b.Dy() > maxDim {
		return fmt.Errorf("goimgtools: webp: image size %dx%d exceeds %dx%d", b.Dx(), b.Dy(), maxDim, maxDim)
	}
	effort := DefaultEffort
	if o != nil && o.Effort != 0 {
		if o.Effort < 1 || o.Effort > 9 {
			return errors.New("goimgtools: webp effort must be 1 to 9")
		}
		effort = o.Effort
	}
	e := &encoder{env: pipeline.EnvOf(src), out: w, w: b.Dx(), h: b.Dy(), p: paramsFor(effort)}
	defer e.close()
	return e.encode(src)
}

type encoder struct {
	env      *pipeline.Env
	out      io.Writer
	w, h     int
	p        params
	reserved int64
	probe    *probe
	fes      []*frontend // every variant started, closed or not
}

func (e *encoder) reserve(n int64, what string) error {
	if err := e.env.Budget.Reserve(n, what); err != nil {
		return err
	}
	e.reserved += n
	return nil
}

func (e *encoder) unreserve(n int64) {
	e.env.Budget.Release(n)
	e.reserved -= n
}

// avail is the part of the budget nobody has reserved.
func (e *encoder) avail() int64 { return e.env.Budget.Limit() - e.env.Budget.Used() }

func (e *encoder) close() {
	if e.probe != nil {
		e.probe.close()
	}
	for _, f := range e.fes {
		f.close()
	}
	e.env.Budget.Release(e.reserved)
	e.reserved = 0
}

// expect sizes a spill. Anything above 1/16 of the budget goes straight to
// a temporary file: several variants may be collecting tokens at once, and
// the point of streaming is to stay well below the limit, not at it.
func (e *encoder) expect(s *pipeline.Spill, n int64) {
	if limit := e.env.Budget.Limit(); n > limit/16 {
		n = max(n, 2*limit)
	}
	s.Expect(n)
}

func (e *encoder) encode(src pipeline.Source) error {
	rr, err := newRowReader(src)
	if err != nil {
		return err
	}
	if err := e.reserve(int64(e.w)*4, "webp row"); err != nil {
		return err
	}
	row := make([]uint32, e.w)
	var rc *race
	if rr.gray {
		// Gray needs no palette and always gains from subtract green: red
		// and blue become zero.
		if rc, err = e.newRace(nil, spec{kindGray, true, sgOn, false}, spec{kindGray, false, sgOn, false},
			spec{kindGray, true, sgOn, true}); err != nil {
			return err
		}
		for y := 0; y < e.h; y++ {
			if err := rr.next(row); err != nil {
				return err
			}
			if err := rc.feed(row, nil); err != nil {
				return err
			}
		}
	} else if rc, err = e.feedColour(rr, row); err != nil {
		return err
	}
	if err := rr.drain(); err != nil {
		return err
	}
	return e.write(rc, !(pipeline.IsOpaque(src) || rr.opaque()))
}

// feedColour runs pass one for a source that may have colour. Rows are
// first collected as indices into a growing colour table. If the image ends
// with at most 256 colours it is replayed from there, with and without the
// colour indexing transform; otherwise, as soon as a 257th colour shows up,
// the rows collected so far are replayed as ARGB and the rest of the source
// follows directly.
func (e *encoder) feedColour(rr *rowReader, row []uint32) (*race, error) {
	pr, err := e.newProbe()
	if err != nil {
		return nil, err
	}
	y := 0
	for ; y < e.h; y++ {
		if err := rr.next(row); err != nil {
			return nil, err
		}
		ok, err := pr.add(row)
		if err != nil {
			return nil, err
		}
		if !ok {
			break
		}
	}
	var rc *race
	paletted := y == e.h
	switch {
	case !paletted:
		rc, err = e.newRace(nil, spec{kindARGB, true, sgOn, false}, spec{kindARGB, false, sgAuto, false},
			spec{kindARGB, true, sgOff, false}, spec{kindARGB, true, sgAuto, true})
	case pr.n <= 16:
		// Few enough colours to pack several pixels into one.
		rc, err = e.newRace(pr.pal[:pr.n], spec{kindIndex, false, sgOff, false})
	case pr.wantPalette():
		rc, err = e.newRace(pr.pal[:pr.n], spec{kindIndex, false, sgOff, false}, spec{kindARGB, true, sgAuto, false},
			spec{kindIndex, true, sgOff, false}, spec{kindARGB, false, sgAuto, false}, spec{kindARGB, true, sgAuto, true})
	default:
		rc, err = e.newRace(pr.pal[:pr.n], spec{kindARGB, true, sgAuto, false}, spec{kindIndex, false, sgOff, false},
			spec{kindIndex, true, sgOff, false}, spec{kindARGB, false, sgAuto, false}, spec{kindARGB, true, sgAuto, true})
	}
	if err != nil {
		return nil, err
	}
	if y > 0 {
		rd := pr.reader()
		if err := e.reserve(int64(e.w)*4, "webp replay row"); err != nil {
			return nil, err
		}
		tmp := make([]uint32, e.w)
		for i := 0; i < y; i++ {
			if err := rd.ReadFull(pr.idx); err != nil {
				return nil, err
			}
			if rc.wantsARGB() {
				for x, v := range pr.idx {
					tmp[x] = pr.pal[v]
				}
			}
			if err := rc.feed(tmp, pr.idx); err != nil {
				return nil, err
			}
		}
		e.unreserve(int64(e.w) * 4)
	}
	pr.close()
	if !paletted {
		// row holds the row that overflowed the colour table.
		if err := rc.feed(row, nil); err != nil {
			return nil, err
		}
		for y++; y < e.h; y++ {
			if err := rr.next(row); err != nil {
				return nil, err
			}
			if err := rc.feed(row, nil); err != nil {
				return nil, err
			}
		}
	}
	return rc, nil
}

// spec names one way of transforming the image.
type spec struct {
	kind int
	pred bool
	sg   int // subtract green: sgOn, sgOff, or sgAuto to decide on the first rows
	// simple restricts the predictor to left, top and select. The full
	// set leaves smaller residuals, but on drawings and other regular
	// images those three leave residuals that repeat, which LZ77 turns
	// into far less than the prefix codes could.
	simple bool
}

const (
	sgAuto = iota
	sgOn
	sgOff
)

// race feeds the image to several variants and drops the ones whose output
// is clearly going to be larger.
type race struct {
	e     *encoder
	alive []*frontend
	fed   int64 // image pixels fed
	next  int64 // compare when fed reaches this
}

// raceMem is the budget a variant should have to itself; with less than
// that per variant fewer are started.
const raceMem = 3 << 19

// raceFirst is the number of pixels after which variants are first compared.
const raceFirst = 1 << 18

func (e *encoder) newRace(palette []uint32, specs ...spec) (*race, error) {
	if len(e.p.modes) == len(modesFast) {
		// The full set is the simple one at this effort.
		keep := specs[:0]
		for _, s := range specs {
			if !s.simple {
				keep = append(keep, s)
			}
		}
		specs = keep
	}
	n := min(len(specs), e.p.variants, max(1, int(e.avail()/raceMem)))
	rc := &race{e: e, next: raceFirst}
	for i, s := range specs[:n] {
		f, err := e.newFrontend(s, palette, n-i)
		if err != nil {
			if i > 0 && errors.Is(err, pipeline.ErrMemoryLimit) {
				break // no room for more variants
			}
			return nil, err
		}
		rc.alive = append(rc.alive, f)
	}
	// A variant with subtract green forced on is meant to run against its
	// twin without; if the twin did not fit, it decides for itself.
	for _, f := range rc.alive {
		if f.kind != kindARGB || !f.sgKnown || !f.sg {
			continue
		}
		twin := false
		for _, o := range rc.alive {
			twin = twin || o.kind == kindARGB && o.pred == f.pred && o.sgKnown && !o.sg
		}
		if !twin {
			f.sgKnown, f.sg = false, false
		}
	}
	return rc, nil
}

// family reports whether two variants differ only in subtract green or in
// the predictors they try.
func family(a, b *frontend) bool { return a.kind == b.kind && a.pred == b.pred }

func (rc *race) wantsARGB() bool {
	for _, f := range rc.alive {
		if f.kind != kindIndex {
			return true
		}
	}
	return false
}

// feed hands one image row to every variant still running: as pixels, and
// (for the variants that use a colour table) as indices.
func (rc *race) feed(row []uint32, idx []byte) error {
	for _, f := range rc.alive {
		var err error
		if f.kind == kindIndex {
			err = f.feedIndex(idx)
		} else {
			err = f.feedRow(row)
		}
		if err != nil {
			return err
		}
	}
	rc.fed += int64(rc.e.w)
	if len(rc.alive) > 1 && rc.fed >= rc.next {
		rc.compare()
	}
	return nil
}

// compare projects the final size of every variant from what it has coded
// so far and drops the ones that are too far behind the best.
func (rc *race) compare() {
	proj := make([]float64, len(rc.alive))
	best := math.Inf(1)
	for i, f := range rc.alive {
		done := f.tok.cur
		if done < raceFirst/8 {
			return // not enough coded yet to judge; try again after the next row
		}
		proj[i] = f.tok.st.estimate() * float64(f.total) / float64(done)
		best = min(best, proj[i])
	}
	rc.next *= 2
	// Of the variants of one family only the best goes on: what has been
	// coded is enough to tell them apart, and they all run the predictor
	// search, the expensive part.
	drop := make([]bool, len(rc.alive))
	for i, f := range rc.alive {
		drop[i] = proj[i] > best*rc.e.p.dropRatio
		for j, o := range rc.alive {
			if j != i && family(f, o) && (proj[j] < proj[i] || proj[j] == proj[i] && j < i) {
				drop[i] = true
			}
		}
	}
	keep := rc.alive[:0]
	for i, f := range rc.alive {
		if drop[i] {
			f.close()
		} else {
			keep = append(keep, f)
		}
	}
	rc.alive = keep
}
