package vp8lenc

import (
	"errors"
	"io"
	"math"
)

const (
	minMatch  = 3
	maxMatch  = 4096
	cacheMult = 0x1e35a7bd
)

// Token stream. Pass one writes what the LZ77 stage decided, pass two reads
// it back and turns it into bits once the prefix codes are known:
//
//	n (1..255), then n literal pixels (4 bytes each, or 1 byte holding green)
//	0, length-1 (2 bytes LE), distance in pixels (4 bytes LE)

// cacheCand simulates one colour-cache size during pass one so the best
// size can be picked afterwards.
type cacheCand struct {
	bits  uint
	shift uint
	tab   []uint32
	hit   []uint32       // hits per cache index
	hitCh [4][256]uint32 // channel histograms of the pixels that hit
}

// stats collects the symbol histograms of a token stream.
type stats struct {
	lit       [4][256]uint32 // green, red, blue, alpha of every literal
	length    [numLength]uint32
	dist      [numDistance]uint32
	extraBits int64
	nLit      int64
	nCopy     int64
	cands     []cacheCand
}

func newStats(cacheBits []uint) *stats {
	s := &stats{}
	for _, b := range cacheBits {
		s.cands = append(s.cands, cacheCand{
			bits: b, shift: 32 - b,
			tab: make([]uint32, 1<<b), hit: make([]uint32, 1<<b),
		})
	}
	return s
}

func (s *stats) addLit(p uint32) {
	s.nLit++
	s.lit[0][p>>8&0xff]++
	s.lit[1][p>>16&0xff]++
	s.lit[2][p&0xff]++
	s.lit[3][p>>24]++
	if len(s.cands) != 0 {
		h := p * cacheMult
		for i := range s.cands {
			c := &s.cands[i]
			k := h >> c.shift
			if c.tab[k] == p {
				c.hit[k]++
				c.hitCh[0][p>>8&0xff]++
				c.hitCh[1][p>>16&0xff]++
				c.hitCh[2][p&0xff]++
				c.hitCh[3][p>>24]++
			} else {
				c.tab[k] = p
			}
		}
	}
}

func (s *stats) addCopy(length int, dcode uint32, px []uint32) {
	s.nCopy++
	ls, ln, _ := prefixCode(uint32(length))
	ds, dn, _ := prefixCode(dcode)
	s.length[ls]++
	s.dist[ds]++
	s.extraBits += int64(ln + dn)
	for i := range s.cands {
		c := &s.cands[i]
		tab, shift := c.tab, c.shift
		for _, p := range px {
			tab[p*cacheMult>>shift] = p
		}
	}
}

// histSet is the five histograms of one Huffman group.
type histSet struct {
	green []uint32 // literals, length prefixes, cache indices
	red   []uint32
	blue  []uint32
	alpha []uint32
	dist  []uint32
}

// histograms returns the histograms for cache candidate ci (-1: no cache).
func (s *stats) histograms(ci int) *histSet {
	n := 0
	if ci >= 0 {
		n = 1 << s.cands[ci].bits
	}
	h := &histSet{
		green: make([]uint32, numLiteral+numLength+n),
		red:   make([]uint32, 256), blue: make([]uint32, 256), alpha: make([]uint32, 256),
		dist: make([]uint32, numDistance),
	}
	copy(h.green, s.lit[0][:])
	copy(h.red, s.lit[1][:])
	copy(h.blue, s.lit[2][:])
	copy(h.alpha, s.lit[3][:])
	copy(h.green[numLiteral:], s.length[:])
	copy(h.dist, s.dist[:])
	if ci >= 0 {
		c := &s.cands[ci]
		for v := 0; v < 256; v++ {
			h.green[v] -= c.hitCh[0][v]
			h.red[v] -= c.hitCh[1][v]
			h.blue[v] -= c.hitCh[2][v]
			h.alpha[v] -= c.hitCh[3][v]
		}
		copy(h.green[numLiteral+numLength:], c.hit)
	}
	return h
}

// estimate is roughly the number of bits the tokens counted so far will
// take, with the best of the colour caches being simulated.
func (s *stats) estimate() float64 {
	best := math.Inf(1)
	for ci := -1; ci < len(s.cands); ci++ {
		var b float64
		for _, l := range s.histograms(ci).lists() {
			b += entropy(l)
		}
		best = min(best, b)
	}
	return best + float64(s.extraBits)
}

// codeSet is the five prefix codes of one Huffman group.
type codeSet struct {
	c [5]*code // green, red, blue, alpha, distance
}

func (h *histSet) lists() [5][]uint32 {
	return [5][]uint32{h.green, h.red, h.blue, h.alpha, h.dist}
}

// build makes the codes for h and returns them with the exact number of
// bits the trees and the symbols take (extra bits not included).
func (h *histSet) build() (*codeSet, int64) {
	cs := &codeSet{}
	var bits int64
	for i, l := range h.lists() {
		cs.c[i] = newCode(l, maxCodeLen)
		bits += cs.c[i].cost(l) + treeBits(cs.c[i])
	}
	return cs, bits
}

// costModel estimates what a symbol costs, in 1/16 bit, from the statistics
// gathered so far. It steers the LZ77 match decisions and the choice of
// predictor per block; it never affects correctness.
type costModel struct {
	lit    [4][256]uint16
	length [numLength]uint16
	dist   [numDistance]uint16
	hit    uint32 // a pixel found in the largest colour cache; 0 = unknown
	avg    uint32 // an average literal pixel
}

const costCap = 16 * 16

func (c *costModel) init() {
	for v := 0; v < 256; v++ {
		m := v
		if m > 128 {
			m = 256 - m
		}
		k := uint16(16 + 32*math.Log2(1+float64(m)))
		for ch := range c.lit {
			c.lit[ch][v] = min(k, costCap)
		}
	}
	for i := range c.length {
		c.length[i] = 16 * 6
	}
	for i := range c.dist {
		c.dist[i] = 16 * 7
	}
	c.avg = 16 * 8
}

func fillCost(dst []uint16, hist []uint32, total float64) {
	if total <= 0 {
		return
	}
	lt := math.Log2(total + 1)
	for i, h := range hist {
		v := 16 * (lt - math.Log2(float64(h)+0.5))
		dst[i] = uint16(max(0, min(v, costCap)))
	}
}

func (c *costModel) update(s *stats) {
	g := float64(s.nLit + s.nCopy)
	fillCost(c.lit[0][:], s.lit[0][:], g)
	for ch := 1; ch < 4; ch++ {
		fillCost(c.lit[ch][:], s.lit[ch][:], float64(s.nLit))
	}
	fillCost(c.length[:], s.length[:], g)
	fillCost(c.dist[:], s.dist[:], float64(s.nCopy))
	// The average literal: what a copy saves per pixel.
	avg := 16.0 * 8
	if s.nLit > 0 {
		avg = 0
		for ch := range c.lit {
			for v, h := range s.lit[ch] {
				avg += float64(h) * float64(c.lit[ch][v])
			}
		}
		avg /= float64(s.nLit)
	}
	c.hit = 0
	if n := len(s.cands); n > 0 && s.nLit > 0 {
		// A cache hit costs the share of hits among the green symbols plus
		// the spread of the hits over the cache indices.
		var hits float64
		for _, h := range s.cands[n-1].hit {
			hits += float64(h)
		}
		if hits >= 16 {
			v := 16 * (math.Log2((g+1)/hits) + entropy(s.cands[n-1].hit)/hits)
			c.hit = uint32(max(1, min(v, costCap)))
			// The share of literals the cache catches pays the hit price.
			if r := hits / float64(s.nLit); v < avg {
				avg = r*v + (1-r)*avg
			}
		}
	}
	c.avg = uint32(max(avg, 1))
}

func (c *costModel) pixel(p uint32) uint32 {
	return uint32(c.lit[0][p>>8&0xff]) + uint32(c.lit[1][p>>16&0xff]) +
		uint32(c.lit[2][p&0xff]) + uint32(c.lit[3][p>>24])
}

// tokenizer is the LZ77 stage: it receives the transformed pixels in stream
// order, finds backward references with a hash chain over a sliding window
// of the most recent pixels, and writes tokens.
type tokenizer struct {
	width   int // pixels per row of the stream, for the 2-D distance codes
	buf     []uint32
	base    int // stream position of buf[0]
	n       int // pixels in buf
	cur     int // stream position of the next pixel to encode
	ins     int // stream position of the next pixel to put in the hash
	wsize   int
	wmask   int
	maxDist int
	head    []uint32 // hash -> position+1 of the latest pixel with it
	prev    []uint32 // position&wmask -> position+1 of the previous one
	hshift  uint
	chain   int
	nice    int
	lazy    bool
	lit1    bool
	st      *stats
	cm      costModel
	nextCM  int64
	out     io.Writer
	obuf    []byte
	run     int // index in obuf of the open literal run's count, or -1
	err     error
}

const tokenFlush = 32 << 10

// tokenizerSizes gives the sizes, in elements, of the pixel buffer, the hash
// heads and the chain links of a tokenizer for `total` pixels.
func tokenizerSizes(total int64, window int) (nbuf, nhead, nprev int) {
	look := max(2*(maxMatch+4), window/4)
	nbuf = int(min(total, int64(window+look)))
	hb := 12
	for 1<<hb < window && hb < 17 {
		hb++
	}
	return nbuf, 1 << hb, int(min(total, int64(window)))
}

// newTokenizer builds a tokenizer whose window is `window` pixels (a power
// of two). If buf is not nil it already holds the whole stream.
func newTokenizer(width int, total int64, window int, buf []uint32, out io.Writer, st *stats) *tokenizer {
	nbuf, nhead, nprev := tokenizerSizes(total, window)
	t := &tokenizer{
		width: width, wsize: window, wmask: window - 1,
		maxDist: min(window-1, maxDistance),
		head:    make([]uint32, nhead),
		prev:    make([]uint32, nprev),
		st:      st, out: out, run: -1,
		chain: 32, nice: 256, lazy: true,
		nextCM: 256,
	}
	for 1<<(32-t.hshift) != nhead {
		t.hshift++
	}
	if buf != nil {
		t.buf, t.n = buf, len(buf)
	} else {
		t.buf = make([]uint32, nbuf)
	}
	t.obuf = make([]byte, 0, tokenFlush+1024)
	t.cm.init()
	return t
}

func (t *tokenizer) hash(p int) uint32 {
	b := t.buf[p : p+3 : p+3]
	return ((b[0]*0x9e3779b1+b[1])*0x85ebca6b + b[2]) * 0xc2b2ae35 >> t.hshift
}

// insertTo enters every position before pos into the hash chains.
func (t *tokenizer) insertTo(pos int) {
	end := t.base + t.n - 2
	for ; t.ins < pos; t.ins++ {
		if t.ins >= end {
			continue
		}
		h := t.hash(t.ins - t.base)
		t.prev[t.ins&t.wmask] = t.head[h]
		t.head[h] = uint32(t.ins + 1)
	}
}

func matchLen(buf []uint32, a, b, limit int) int {
	x, y := buf[a:a+limit], buf[b:b+limit]
	for i := range x {
		if x[i] != y[i] {
			return i
		}
	}
	return limit
}

// matchCost is what coding a copy of the given length and distance costs,
// in 1/16 bit.
func (t *tokenizer) matchCost(length, dist int) int32 {
	ls, ln, _ := prefixCode(uint32(length))
	ds, dn, _ := prefixCode(distCode(dist, t.width))
	return int32(t.cm.length[ls]) + int32(t.cm.dist[ds]) + 16*int32(ln+dn)
}

// find looks for the best match for the pixels at pos: the one that saves
// the most bits over literals, going by the average cost of a literal.
// Distances are far from equally expensive (a pixel in the row above costs
// a few bits, one 100000 pixels back about twenty), so the longest match is
// not always the best. value is the estimated saving in 1/16 bit.
func (t *tokenizer) find(pos int) (bestLen, bestDist int, value int32) {
	buf := t.buf
	p := pos - t.base
	limit := min(maxMatch, t.n-p)
	if limit < minMatch {
		return 0, 0, 0
	}
	avg := int32(t.cm.avg)
	value = math.MinInt32
	if p >= 1 && buf[p-1] == buf[p] {
		bestLen, bestDist = matchLen(buf, p-1, p, limit), 1
		value = int32(bestLen)*avg - t.matchCost(bestLen, 1)
	}
	if w := t.width; w > 1 && p >= w && w <= t.maxDist && buf[p-w] == buf[p] && bestLen < limit {
		l := matchLen(buf, p-w, p, limit)
		if v := int32(l)*avg - t.matchCost(l, w); v > value {
			bestLen, bestDist, value = l, w, v
		}
	}
	if bestLen >= t.nice || bestLen == limit || limit < minMatch {
		return
	}
	lowest := max(pos-t.maxDist, t.base)
	cand := int(t.head[t.hash(p)])
	for chain := t.chain; cand > 0 && chain > 0; chain-- {
		c := cand - 1
		if c < lowest {
			break
		}
		q := c - t.base
		// Only candidates at least as long as the best so far are measured.
		if chk := max(bestLen-1, 0); buf[q+chk] == buf[p+chk] && buf[q] == buf[p] {
			if l := matchLen(buf, q, p, limit); l >= bestLen && l >= minMatch {
				if v := int32(l)*avg - t.matchCost(l, pos-c); v > value {
					bestLen, bestDist, value = l, pos-c, v
					if l >= t.nice || l == limit {
						break
					}
				}
			}
		}
		next := int(t.prev[c&t.wmask])
		if next >= cand {
			break
		}
		cand = next
	}
	return
}

// worth reports whether coding the match costs less than the literals it
// replaces, this time going by the cost of those very pixels.
func (t *tokenizer) worth(pos, length, dist int) bool {
	if length < minMatch {
		return false
	}
	if length >= 64 {
		return true
	}
	cc := uint32(t.matchCost(length, dist))
	var lc uint32
	p := pos - t.base
	if hit := t.cm.hit; hit != 0 {
		// Literals the colour cache is likely to catch are cheap.
		cache := &t.st.cands[len(t.st.cands)-1]
		for _, px := range t.buf[p : p+length] {
			c := t.cm.pixel(px)
			if c > hit && cache.tab[px*cacheMult>>cache.shift] == px {
				c = hit
			}
			if lc += c; lc > cc {
				return true
			}
		}
		return false
	}
	for _, px := range t.buf[p : p+length] {
		if lc += t.cm.pixel(px); lc > cc {
			return true
		}
	}
	return false
}

func (t *tokenizer) flushOut() {
	if t.err == nil && len(t.obuf) > 0 {
		_, t.err = t.out.Write(t.obuf)
	}
	t.obuf = t.obuf[:0]
	t.run = -1
}

func (t *tokenizer) literal(pos int) {
	p := t.buf[pos-t.base]
	t.st.addLit(p)
	if t.run < 0 || t.obuf[t.run] == 255 {
		if len(t.obuf) >= tokenFlush {
			t.flushOut()
		}
		t.run = len(t.obuf)
		t.obuf = append(t.obuf, 0)
	}
	t.obuf[t.run]++
	if t.lit1 {
		t.obuf = append(t.obuf, byte(p>>8))
	} else {
		t.obuf = append(t.obuf, byte(p), byte(p>>8), byte(p>>16), byte(p>>24))
	}
}

func (t *tokenizer) copyRef(pos, length, dist int) {
	p := pos - t.base
	t.st.addCopy(length, distCode(dist, t.width), t.buf[p:p+length])
	if len(t.obuf) >= tokenFlush {
		t.flushOut()
	}
	t.run = -1
	l := length - 1
	t.obuf = append(t.obuf, 0, byte(l), byte(l>>8), byte(dist), byte(dist>>8), byte(dist>>16), byte(dist>>24))
}

// process encodes the buffered pixels. Unless final, it stops short of the
// end so that every match search sees a full look-ahead.
func (t *tokenizer) process(final bool) {
	end := t.base + t.n
	stop := end
	if !final {
		stop -= maxMatch + 2
	}
	for t.cur < stop {
		if n := t.st.nLit + t.st.nCopy; n >= t.nextCM {
			t.cm.update(t.st)
			if t.nextCM < 1<<18 {
				t.nextCM *= 4
			} else {
				t.nextCM += 1 << 18
			}
		}
		pos := t.cur
		l, d, v := t.find(pos)
		if !t.worth(pos, l, d) {
			t.literal(pos)
			t.insertTo(pos + 1)
			t.cur = pos + 1
			continue
		}
		if t.lazy {
			// Take a match starting one pixel later if it saves more.
			for l < t.nice && pos+1 < stop {
				t.insertTo(pos + 1)
				l2, d2, v2 := t.find(pos + 1)
				if v2 <= v || !t.worth(pos+1, l2, d2) {
					break
				}
				t.literal(pos)
				pos++
				l, d, v = l2, d2, v2
			}
		}
		t.copyRef(pos, l, d)
		t.insertTo(pos + l)
		t.cur = pos + l
	}
}

// push appends pixels to the stream.
func (t *tokenizer) push(px []uint32) error {
	for len(px) > 0 {
		if t.n == len(t.buf) {
			// Slide: keep the window behind cur and whatever is not encoded.
			shift := t.cur - t.wsize - t.base
			if shift <= 0 {
				return errors.New("goimgtools: vp8l: window stalled")
			}
			copy(t.buf, t.buf[shift:t.n])
			t.n -= shift
			t.base += shift
		}
		k := copy(t.buf[t.n:], px)
		t.n += k
		px = px[k:]
		t.process(false)
		if t.err != nil {
			return t.err
		}
	}
	return nil
}

// finish encodes what is left and flushes the tokens.
func (t *tokenizer) finish() error {
	t.process(true)
	t.flushOut()
	return t.err
}
