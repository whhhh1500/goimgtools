package vp8lenc

import (
	"errors"
	"fmt"
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

type memSink struct{ b []byte }

func (m *memSink) Write(p []byte) (int, error) {
	m.b = append(m.b, p...)
	return len(p), nil
}

// subImage codes a small image held in memory (a transform's data or the
// entropy image): no colour cache, one Huffman group, LZ77 over the whole
// image. With green set, only the green channel varies and the others are
// those of hi.
func (e *encoder) subImage(bw *bitWriter, pix []uint32, width int, green bool, hi uint32) error {
	total := int64(len(pix))
	need := subImageMem(total, green)
	if err := e.reserve(need, "webp transform data"); err != nil {
		return err
	}
	defer e.unreserve(need)
	var sink memSink
	st := newStats(nil)
	t := newTokenizer(width, total, subImageWindow(total), pix, &sink, st)
	t.chain, t.nice, t.lit1 = 64, 1024, green
	if err := t.finish(); err != nil {
		return err
	}
	cs, _ := st.histograms(-1).build()
	bw.put(0, 1) // no colour cache
	for _, c := range cs.c {
		writeTree(bw, c)
	}
	w := &walker{width: width, lit1: green, hi: hi}
	return w.run(&memReader{sink.b}, int64(len(sink.b)), &emitter{bw: bw, groups: []*codeSet{cs}})
}

func subImageWindow(total int64) int {
	window := 16
	for int64(window) < total && window < 1<<14 {
		window *= 2
	}
	return window
}

// subImageMem is what subImage reserves for an image of total pixels, the
// pixels themselves not included.
func subImageMem(total int64, green bool) int64 {
	_, nhead, nprev := tokenizerSizes(total, subImageWindow(total))
	tokens := total + total/64 + 64
	if !green {
		tokens *= 4
	}
	return int64(nhead+nprev)*4 + tokens + 2*tokenFlush
}

// plan is how one variant is going to be written.
type plan struct {
	f         *frontend
	bw        *bitWriter // the header up to the colour cache size
	cache     int        // index of the colour cache candidate, -1 for none
	groups    []*codeSet // the Huffman groups
	assign    []uint16   // group per block of the entropy image; nil for one group
	hbits     uint
	tw        int
	tail      int64 // bits after bw: entropy image, codes, pixels
	totalBits int64 // exact size of the VP8L bitstream
}

// plan builds the header of variant f and works out the size of its
// bitstream with one Huffman group.
func (e *encoder) plan(f *frontend, alpha bool) (*plan, error) {
	st := f.tok.st
	p := &plan{f: f, cache: -1}
	// Pick the colour cache size that makes the stream smallest.
	hs := st.histograms(-1)
	cs, bits := hs.build()
	for i := range st.cands {
		h := st.histograms(i)
		c, b := h.build()
		if b+4 < bits {
			p.cache, cs, bits = i, c, b+4
		}
	}
	p.groups = []*codeSet{cs}

	bw := &bitWriter{}
	p.bw = bw
	bw.put(0x2f, 8)
	bw.put(uint32(e.w-1), 14)
	bw.put(uint32(e.h-1), 14)
	if alpha {
		bw.put(1, 1)
	} else {
		bw.put(0, 1)
	}
	bw.put(0, 3) // version
	if f.kind == kindIndex {
		bw.put(1, 1)
		bw.put(3, 2)
		if err := e.writePalette(bw, f.palette); err != nil {
			return nil, err
		}
	} else if f.sg {
		bw.put(1, 1)
		bw.put(2, 2)
	}
	if f.pred {
		bw.put(1, 1)
		bw.put(0, 2)
		bw.put(uint32(f.bits-2), 3)
		need := int64(len(f.modes)) * 4
		if err := e.reserve(need, "webp predictor modes"); err != nil {
			return nil, err
		}
		pix := make([]uint32, len(f.modes))
		for i, m := range f.modes {
			pix[i] = 0xff000000 | uint32(m)<<8
		}
		err := e.subImage(bw, pix, f.tw, true, 0xff000000)
		e.unreserve(need)
		if err != nil {
			return nil, err
		}
	}
	bw.put(0, 1) // no more transforms
	if p.cache >= 0 {
		bw.put(1, 1)
		bw.put(uint32(st.cands[p.cache].bits), 4)
	} else {
		bw.put(0, 1)
	}
	// bits covers the codes and their symbols (and the 4 bits of the cache
	// size, already written); the meta bit and the extra bits follow.
	if p.cache >= 0 {
		bits -= 4
	}
	p.tail = 1 + bits + st.extraBits
	p.totalBits = bw.bitLen() + p.tail
	return p, nil
}

const replayReadBuf = 64 << 10

// maxGroupBlocks bounds the size of the entropy image.
const maxGroupBlocks = 16 << 10

// group tries to split the image into several Huffman groups and keeps the
// split if the stream gets smaller. w is the walker pass two replays with.
func (e *encoder) group(p *plan, w *walker) error {
	f := p.f
	st := f.tok.st
	// A group costs a few hundred bytes of codes: aim at no more than a
	// few percent of overhead.
	want := int(min(p.totalBits/8/3000, int64(e.p.groups)))
	if want < 2 {
		return nil
	}
	hbits := e.p.groupBits
	for hbits < 7 && int64((f.sw+1<<hbits-1)>>hbits)*int64((f.h+1<<hbits-1)>>hbits) > maxGroupBlocks {
		hbits++
	}
	var cacheBits uint
	if p.cache >= 0 {
		cacheBits = st.cands[p.cache].bits
	}
	need := grouperMem(f.sw, f.h, hbits, cacheBits, want)
	for hbits < 7 && need > e.avail()/2 {
		hbits++
		need = grouperMem(f.sw, f.h, hbits, cacheBits, want)
	}
	if need > e.avail()/2 {
		return nil // not worth failing for
	}
	if err := e.reserve(need, "webp Huffman groups"); err != nil {
		return err
	}
	g := newGrouper(f.sw, f.h, hbits, cacheBits, want, f.tok.lit1)
	size := f.tokens.Size()
	for i := 0; i <= e.p.groupIter; i++ {
		if i > 0 {
			g.beginReplay()
		}
		if err := w.run(pipeline.NewSeqReader(f.tokens, 0, size, replayReadBuf), size, g); err != nil {
			return err
		}
		if g.endReplay() < 2 {
			e.unreserve(need)
			return nil
		}
	}
	sets := g.result()

	// Price the split: entropy image, one set of codes per group, symbols.
	tail := 1 + 3 + st.extraBits
	groups := make([]*codeSet, len(sets))
	for i, hs := range sets {
		cs, bits := hs.build()
		groups[i] = cs
		tail += bits
	}
	npix := int64(len(g.assign))
	if err := e.reserve(npix*4, "webp entropy image"); err != nil {
		return err
	}
	pix := make([]uint32, npix)
	for i, a := range g.assign {
		pix[i] = 0xff000000 | uint32(a)<<8 // at most 256 groups: the index fits in green
	}
	var img bitWriter
	err := e.subImage(&img, pix, g.tw, true, 0xff000000)
	e.unreserve(npix * 4)
	if err != nil {
		return err
	}
	tail += img.bitLen()
	if tail >= p.tail {
		e.unreserve(need)
		return nil
	}
	p.groups, p.assign, p.hbits, p.tw = groups, g.assign, hbits, g.tw
	p.tail = tail
	p.totalBits = p.bw.bitLen() + tail
	return nil
}

// write is pass two: it finishes the variants still running, keeps the
// smallest and writes it out.
func (e *encoder) write(rc *race, alpha bool) error {
	var best *plan
	for _, f := range rc.alive {
		if err := f.finish(); err != nil {
			return err
		}
	}
	for _, f := range rc.alive {
		p, err := e.plan(f, alpha)
		if err != nil {
			return err
		}
		if best == nil || p.totalBits < best.totalBits {
			if best != nil {
				best.f.close()
			}
			best = p
		} else {
			f.close()
		}
	}
	if best == nil {
		return errors.New("goimgtools: webp: internal error: no variant left")
	}
	f, bw := best.f, best.bw
	st := f.tok.st
	wk := &walker{width: f.sw, lit1: f.tok.lit1, hi: f.hi}
	if best.cache >= 0 {
		// Replays follow the copies through the window to keep the cache
		// in step with the decoder.
		wk.cacheBits = st.cands[best.cache].bits
		wk.cache = make([]uint32, 1<<wk.cacheBits)
		wk.ring = f.tok.buf
		wk.ringMask = f.tok.wmask
	} else {
		f.dropBuf()
	}
	if err := e.reserve(2*bitFlush+replayReadBuf, "webp output buffers"); err != nil {
		return err
	}
	if e.p.groups > 1 {
		if err := e.group(best, wk); err != nil {
			return err
		}
	}

	// The size of the chunk is known exactly before any of it is written.
	size := (best.totalBits + 7) / 8
	if size > math.MaxUint32-64 {
		return errors.New("goimgtools: webp: image too large for a RIFF container")
	}
	riff := size + size&1 + 12
	hdr := [20]byte{'R', 'I', 'F', 'F', byte(riff), byte(riff >> 8), byte(riff >> 16), byte(riff >> 24),
		'W', 'E', 'B', 'P', 'V', 'P', '8', 'L', byte(size), byte(size >> 8), byte(size >> 16), byte(size >> 24)}
	if _, err := e.out.Write(hdr[:]); err != nil {
		return err
	}
	bw.w = e.out
	if best.assign == nil {
		bw.put(0, 1) // one Huffman group for the whole image
	} else {
		bw.put(1, 1)
		bw.put(uint32(best.hbits-2), 3)
		npix := int64(len(best.assign))
		if err := e.reserve(npix*4, "webp entropy image"); err != nil {
			return err
		}
		pix := make([]uint32, npix)
		for i, a := range best.assign {
			pix[i] = 0xff000000 | uint32(a)<<8
		}
		err := e.subImage(bw, pix, best.tw, true, 0xff000000)
		e.unreserve(npix * 4)
		if err != nil {
			return err
		}
	}
	for _, cs := range best.groups {
		for _, c := range cs.c {
			writeTree(bw, c)
		}
	}
	em := &emitter{bw: bw, groups: best.groups, assign: best.assign, hbits: best.hbits, tw: best.tw}
	rd := pipeline.NewSeqReader(f.tokens, 0, f.tokens.Size(), replayReadBuf)
	if err := wk.run(rd, f.tokens.Size(), em); err != nil {
		return err
	}
	if bw.err != nil {
		return bw.err
	}
	if bw.bitLen() != best.totalBits {
		return fmt.Errorf("goimgtools: webp: internal error: wrote %d bits, expected %d", bw.bitLen(), best.totalBits)
	}
	if err := bw.finish(); err != nil {
		return err
	}
	if size&1 == 1 {
		if _, err := e.out.Write([]byte{0}); err != nil {
			return err
		}
	}
	return nil
}
