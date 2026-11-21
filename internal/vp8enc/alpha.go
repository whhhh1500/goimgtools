package vp8enc

import (
	"errors"
	"io"
	"math/bits"
)

// The alpha plane of a lossy WebP file lives in an ALPH chunk, losslessly.
// It is coded by the lossless encoder (alphall.go); this file keeps the
// plane until then, and has a simple coder of its own for when there is
// too little memory for the lossless encoder: the plane is run through the
// "horizontal" filter (each value minus the one to its left) when that
// makes it more regular, and then stored as a headerless VP8L (WebP
// lossless) stream whose green channel carries the values.
//
// That VP8L stream uses no transforms and no colour cache: only Huffman
// coded values and LZ77 references to the pixel on the left (runs) or the
// one above. The plane is read from its spool one row at a time, twice:
// once to count symbols for the Huffman code, once to write.

const (
	alphaGreenSyms = 256 + 24 // literals and LZ77 length prefixes
	alphaMinMatch  = 3
	alphaMaxMatch  = 4096

	distAbove = 0 // distance code 1: the pixel above
	distLeft  = 1 // distance code 2: the pixel to the left
)

// alphaSpool holds the alpha plane while the frame is being encoded. Most
// alpha planes are largely flat (and the plane of an opaque image, which is
// only known to be opaque once it has all been seen, entirely so), so rows
// are stored run-length packed:
//
//	c < 0x80:  c+1 literal bytes follow
//	c < 0xff:  the next byte is repeated c-0x80+2 times
//	c == 0xff: a 16-bit count-1 (low byte first), then the byte to repeat
//
// Runs do not cross rows. Rows are read back in order.
type alphaSpool struct {
	st   *store
	w    int
	pack []byte

	// Statistics of the plane and of its horizontally filtered version
	// (each value minus its left neighbour; the first column minus the
	// value above), to choose between the two.
	raw, horiz [256]uint32
	rows       int
	prevFirst  uint8
	// For estimating the best filter (see bestFilter): the two rows
	// before, and which coarse sizes of difference each filter leaves.
	r1, r2 []uint8
	seen   [4][16]bool
	// levels, when the plane is reduced to fewer values (alphaquant.go),
	// maps each value to its level as rows are read back.
	levels *[256]uint8

	// Reading.
	off  int64
	buf  []byte
	r, n int
}

func alphaSpoolBytes(w int) int64 { return int64(w+w/128+8) + 2*int64(w) }

func newAlphaSpool(st *store, w int) *alphaSpool {
	return &alphaSpool{st: st, w: w, pack: make([]byte, 0, w+w/128+8), r1: make([]uint8, w), r2: make([]uint8, w)}
}

// add appends one row.
func (s *alphaSpool) add(row []uint8) error {
	prev := s.prevFirst
	if s.rows == 0 {
		prev = 0
	}
	s.prevFirst = row[0]
	// The row before is sampled now that it is known not to be the last.
	if j := s.rows - 1; j >= 2 && j%2 == 0 {
		s.sample(s.r1, s.r2)
	}
	s.r2, s.r1 = s.r1, s.r2
	copy(s.r1, row)
	s.rows++
	for _, v := range row {
		s.raw[v]++
		s.horiz[v-prev]++
		prev = v
	}

	p := s.pack[:0]
	for i := 0; i < len(row); {
		v := row[i]
		run := 1
		for i+run < len(row) && row[i+run] == v {
			run++
		}
		if run >= 3 {
			i += run
			for run > 0 {
				switch {
				case run > 128:
					k := min(run, 1<<16)
					p = append(p, 0xff, byte(k-1), byte((k-1)>>8), v)
					run -= k
				case run >= 2:
					p = append(p, byte(0x80+run-2), v)
					run = 0
				default:
					p = append(p, 0, v)
					run = 0
				}
			}
			continue
		}
		// Literals up to the next run of three.
		j := i + run
		for j < len(row) && j-i < 128 && !(j+2 < len(row) && row[j] == row[j+1] && row[j] == row[j+2]) {
			j++
		}
		p = append(p, byte(j-i-1))
		p = append(p, row[i:j]...)
		i = j
	}
	s.pack = p
	_, err := s.st.Write(p)
	return err
}

// sample notes, for every other value of row p (below row above), how far
// it is from what each filter predicts (libwebp's WebPEstimateBestFilter).
func (s *alphaSpool) sample(p, above []uint8) {
	coarse := func(a, b int) int { return min(abs(a-b)>>4, 15) }
	mean := int(p[0])
	for i := 2; i < len(p)-1; i += 2 {
		v := int(p[i])
		left, up, ul := int(p[i-1]), int(above[i]), int(above[i-1])
		s.seen[alphaFilterNone][coarse(v, mean)] = true
		s.seen[alphaFilterHorizontal][coarse(v, left)] = true
		s.seen[alphaFilterVertical][coarse(v, up)] = true
		s.seen[alphaFilterGradient][coarse(v, min(max(left+up-ul, 0), 255))] = true
		mean = (3*mean + v + 2) >> 2
	}
}

// bestFilter is the filter estimated to leave the smallest differences:
// the one whose differences, taken coarsely, are fewest and smallest.
func (s *alphaSpool) bestFilter() int {
	best, score := alphaFilterNone, -1
	for f := range s.seen {
		sc := 0
		for i, ok := range s.seen[f] {
			if ok {
				sc += i
			}
		}
		if score < 0 || sc < score {
			best, score = f, sc
		}
	}
	return best
}

// colours is the number of different values in the plane.
func (s *alphaSpool) colours() int {
	n := 0
	for _, c := range s.raw {
		if c != 0 {
			n++
		}
	}
	return n
}

// transparent reports whether any value is below 255.
func (s *alphaSpool) transparent() bool { return s.raw[0xff] != uint32(s.w*s.rows) }

// useFilter reports whether the filtered values are more concentrated on a
// few values than the raw ones (compared by the sum of squared counts).
func (s *alphaSpool) useFilter() bool {
	var a, b uint64
	for i := range s.raw {
		a += uint64(s.raw[i]) * uint64(s.raw[i])
		b += uint64(s.horiz[i]) * uint64(s.horiz[i])
	}
	return b > a
}

// rewind starts reading from the first row, through buf.
func (s *alphaSpool) rewind(buf []byte) { s.off, s.buf, s.r, s.n = 0, buf, 0, 0 }

func (s *alphaSpool) byte() (byte, error) {
	if s.r == s.n {
		k := int(min(int64(len(s.buf)), s.st.Size()-s.off))
		if k <= 0 {
			return 0, io.ErrUnexpectedEOF
		}
		if n, err := s.st.ReadAt(s.buf[:k], s.off); n < k {
			if err == nil {
				err = io.ErrUnexpectedEOF
			}
			return 0, err
		}
		s.off += int64(k)
		s.r, s.n = 0, k
	}
	c := s.buf[s.r]
	s.r++
	return c, nil
}

// next unpacks the next row into dst.
func (s *alphaSpool) next(dst []uint8) error {
	for i := 0; i < len(dst); {
		c, err := s.byte()
		if err != nil {
			return err
		}
		n := int(c) + 1
		if c < 0x80 {
			if i+n > len(dst) {
				return errAlphaSpool
			}
			for ; n > 0; n-- {
				if dst[i], err = s.byte(); err != nil {
					return err
				}
				i++
			}
			continue
		}
		n = int(c) - 0x80 + 2
		if c == 0xff {
			lo, _ := s.byte()
			hi, err := s.byte()
			if err != nil {
				return err
			}
			n = int(lo) | int(hi)<<8 + 1
		}
		v, err := s.byte()
		if err != nil {
			return err
		}
		if i+n > len(dst) {
			return errAlphaSpool
		}
		for ; n > 0; n-- {
			dst[i] = v
			i++
		}
	}
	if m := s.levels; m != nil {
		for i, v := range dst {
			dst[i] = m[v]
		}
	}
	return nil
}

var errAlphaSpool = errors.New("goimgtools: webp: alpha spool is damaged")

// bitWriter packs bits least significant first, as VP8L wants. With a nil
// writer it only counts.
type bitWriter struct {
	w     io.Writer
	buf   []byte
	n     int
	acc   uint64
	nacc  uint
	total int64 // bits
	err   error
}

func (b *bitWriter) put(v uint32, n uint) {
	b.total += int64(n)
	if b.w == nil {
		return
	}
	b.acc |= uint64(v) << b.nacc
	b.nacc += n
	for b.nacc >= 8 {
		b.byte(byte(b.acc))
		b.acc >>= 8
		b.nacc -= 8
	}
}

func (b *bitWriter) byte(c byte) {
	if b.n == len(b.buf) {
		b.flush()
	}
	b.buf[b.n] = c
	b.n++
}

func (b *bitWriter) flush() {
	if b.n > 0 && b.err == nil {
		_, b.err = b.w.Write(b.buf[:b.n])
	}
	b.n = 0
}

// finish pads to a whole byte and flushes.
func (b *bitWriter) finish() error {
	if b.w != nil {
		if b.nacc > 0 {
			b.byte(byte(b.acc))
			b.acc, b.nacc = 0, 0
		}
		b.flush()
	}
	return b.err
}

// huffCode is a canonical Huffman code: per symbol its length and its bits
// in the order they are written.
type huffCode struct {
	lens  []uint8
	codes []uint16
}

func (h *huffCode) put(bw *bitWriter, sym int) { bw.put(uint32(h.codes[sym]), uint(h.lens[sym])) }

// huffLengths computes code lengths, none above limit, for the symbol
// counts. At least two symbols get a code, so that the code is complete.
func huffLengths(count []uint32, limit int) []uint8 {
	lens := make([]uint8, len(count))
	used := 0
	for _, c := range count {
		if c != 0 {
			used++
		}
	}
	if used < 2 {
		// One symbol (or none): pair it with a neighbour, one bit each.
		s := 0
		for i, c := range count {
			if c != 0 {
				s = i
			}
		}
		lens[s] = 1
		if s == 0 {
			lens[1] = 1
		} else {
			lens[s-1] = 1
		}
		return lens
	}
	// Huffman's algorithm; if the tree comes out too deep, flatten the
	// distribution by raising the small counts and try again.
	n := len(count)
	weight := make([]uint64, 2*n)
	parent := make([]int32, 2*n)
	live := make([]int32, 0, n)
	for floor := uint32(1); ; floor *= 2 {
		live = live[:0]
		for i, c := range count {
			if c != 0 {
				weight[i] = uint64(max(c, floor))
				live = append(live, int32(i))
			}
		}
		next := int32(n)
		for len(live) > 1 {
			// Take out the two lightest nodes.
			var pick [2]int32
			for k := range pick {
				best := 0
				for i := range live {
					if weight[live[i]] < weight[live[best]] {
						best = i
					}
				}
				pick[k] = live[best]
				live[best] = live[len(live)-1]
				live = live[:len(live)-1]
			}
			weight[next] = weight[pick[0]] + weight[pick[1]]
			parent[pick[0]], parent[pick[1]] = next, next
			live = append(live, next)
			next++
		}
		root := live[0]
		deepest := 0
		for i, c := range count {
			if c == 0 {
				continue
			}
			d := 0
			for j := int32(i); j != root; j = parent[j] {
				d++
			}
			lens[i] = uint8(d)
			deepest = max(deepest, d)
		}
		if deepest <= limit {
			return lens
		}
	}
}

func newHuffCode(lens []uint8) *huffCode {
	h := &huffCode{lens: lens, codes: make([]uint16, len(lens))}
	var hist [16]uint32
	for _, l := range lens {
		hist[l]++
	}
	hist[0] = 0
	var next [16]uint32
	code := uint32(0)
	for l := 1; l < 16; l++ {
		code = (code + hist[l-1]) << 1
		next[l] = code
	}
	for s, l := range lens {
		if l > 0 {
			// The decoder reads a code from its most significant bit.
			h.codes[s] = bits.Reverse16(uint16(next[l])) >> (16 - l)
			next[l]++
		}
	}
	return h
}

var codeLengthCodeOrder = [19]uint8{17, 18, 0, 1, 2, 3, 4, 5, 16, 6, 7, 8, 9, 10, 11, 12, 13, 14, 15}

// putHuffCode writes the description of a code (VP8L "normal" code lengths:
// the lengths are themselves Huffman coded, with runs of zeros shortened).
func putHuffCode(bw *bitWriter, lens []uint8) {
	type item struct {
		sym   uint8
		extra uint8
	}
	var items []item
	var hist [19]uint32
	for i := 0; i < len(lens); {
		if lens[i] != 0 {
			items = append(items, item{sym: lens[i]})
			hist[lens[i]]++
			i++
			continue
		}
		run := 1
		for i+run < len(lens) && lens[i+run] == 0 {
			run++
		}
		i += run
		for run > 0 {
			switch {
			case run >= 11:
				k := min(run, 138)
				items = append(items, item{18, uint8(k - 11)})
				hist[18]++
				run -= k
			case run >= 3:
				items = append(items, item{17, uint8(run - 3)})
				hist[17]++
				run = 0
			default:
				items = append(items, item{sym: 0})
				hist[0]++
				run--
			}
		}
	}
	cl := newHuffCode(huffLengths(hist[:], 7))
	bw.put(0, 1)    // not a "simple" code
	bw.put(19-4, 4) // all 19 code length code lengths follow
	for _, s := range codeLengthCodeOrder {
		bw.put(uint32(cl.lens[s]), 3)
	}
	bw.put(0, 1) // code lengths for the whole alphabet
	for _, it := range items {
		cl.put(bw, int(it.sym))
		switch it.sym {
		case 17:
			bw.put(uint32(it.extra), 3)
		case 18:
			bw.put(uint32(it.extra), 7)
		}
	}
}

// lz77Prefix splits a length (1..4096) into its prefix symbol and extra
// bits (section 4.2.2 of the VP8L specification).
func lz77Prefix(v int) (sym int, extra uint32, nExtra uint) {
	if v <= 4 {
		return v - 1, 0, 0
	}
	d := uint32(v - 1)
	hb := uint(bits.Len32(d) - 1)
	sb := d >> (hb - 1) & 1
	return int(2*hb) + int(sb), d & (1<<(hb-1) - 1), hb - 1
}

// alphaEnc turns the spooled alpha plane into the payload of an ALPH chunk.
type alphaEnc struct {
	src      *alphaSpool
	w, h     int
	filter   bool
	prev     []uint8 // previous row, as the coder sees it (filtered)
	cur      []uint8
	raw      []uint8
	runLeft  []uint16
	runAbove []uint16
	tokens   []uint16 // per row: a literal, or 0x8000|distance<<12 then the length
	scratch  int64

	green *huffCode
	dist  [2]bool // which distance symbols occur
	bits  int64   // size of the VP8L stream
}

func alphaScratchBytes(w int) int64 { return int64(w)*3 + int64(w+1)*4 + int64(w)*4 }

func newAlphaEnc(src *alphaSpool, w, h int, filter bool) *alphaEnc {
	return &alphaEnc{
		src: src, w: w, h: h, filter: filter,
		prev: make([]uint8, w), cur: make([]uint8, w), raw: make([]uint8, w),
		runLeft: make([]uint16, w+1), runAbove: make([]uint16, w+1),
		tokens: make([]uint16, 0, 2*w),
	}
}

// row reads row y (rows come in order) and turns it into tokens.
func (a *alphaEnc) row(y int) error {
	w := a.w
	a.prev, a.cur = a.cur, a.prev
	if a.filter {
		first := a.raw[0] // of the row above
		if y == 0 {
			first = 0
		}
		if err := a.src.next(a.raw); err != nil {
			return err
		}
		left := first
		for x, v := range a.raw {
			a.cur[x] = v - left
			left = v
		}
	} else if err := a.src.next(a.cur); err != nil {
		return err
	}
	cur, prev := a.cur, a.prev
	// How far each position keeps matching its left and upper neighbour.
	a.runLeft[w], a.runAbove[w] = 0, 0
	for x := w - 1; x >= 0; x-- {
		a.runLeft[x], a.runAbove[x] = 0, 0
		if x > 0 && cur[x] == cur[x-1] {
			a.runLeft[x] = a.runLeft[x+1] + 1
		}
		if y > 0 && cur[x] == prev[x] {
			a.runAbove[x] = a.runAbove[x+1] + 1
		}
	}
	a.tokens = a.tokens[:0]
	for x := 0; x < w; {
		n, d := int(a.runLeft[x]), distLeft
		if int(a.runAbove[x]) > n {
			n, d = int(a.runAbove[x]), distAbove
		}
		if n < alphaMinMatch {
			a.tokens = append(a.tokens, uint16(cur[x]))
			x++
			continue
		}
		n = min(n, alphaMaxMatch)
		a.tokens = append(a.tokens, uint16(0x8000|d<<12), uint16(n))
		x += n
	}
	return nil
}

// prepare makes the first pass: it counts the symbols, builds the Huffman
// code and works out the size of the stream.
func (a *alphaEnc) prepare(readBuf []byte) error {
	a.src.rewind(readBuf)
	var hist [alphaGreenSyms]uint32
	var distCount [2]int64
	var extra int64
	for y := 0; y < a.h; y++ {
		if err := a.row(y); err != nil {
			return err
		}
		for i := 0; i < len(a.tokens); i++ {
			t := a.tokens[i]
			if t&0x8000 == 0 {
				hist[t]++
				continue
			}
			i++
			sym, _, n := lz77Prefix(int(a.tokens[i]))
			hist[256+sym]++
			extra += int64(n)
			distCount[t>>12&1]++
		}
	}
	a.green = newHuffCode(huffLengths(hist[:], 15))
	a.dist = [2]bool{distCount[0] != 0, distCount[1] != 0}

	var count bitWriter
	a.putHeader(&count)
	a.bits = count.total + extra
	for s, c := range hist {
		a.bits += int64(c) * int64(a.green.lens[s])
	}
	if a.dist[0] && a.dist[1] {
		a.bits += distCount[0] + distCount[1] // one bit each
	}
	return nil
}

// size is the length of the VP8L stream in bytes.
func (a *alphaEnc) size() int64 { return (a.bits + 7) / 8 }

func (a *alphaEnc) putHeader(bw *bitWriter) {
	bw.put(0, 1) // no transform
	bw.put(0, 1) // no colour cache
	bw.put(0, 1) // one group of Huffman codes for the whole image
	putHuffCode(bw, a.green.lens)
	// Red, blue and alpha are not used: a code with the single symbol 0,
	// which takes no bits per pixel.
	for i := 0; i < 3; i++ {
		bw.put(1, 1) // simple code
		bw.put(0, 1) // one symbol
		bw.put(0, 1) // given in one bit
		bw.put(0, 1) // symbol 0
	}
	// Distances: symbol 0 (above), symbol 1 (left), or both.
	bw.put(1, 1)
	switch {
	case a.dist[0] && a.dist[1]:
		bw.put(1, 1) // two symbols
		bw.put(0, 1)
		bw.put(0, 1) // symbol 0
		bw.put(1, 8) // symbol 1
	case a.dist[1]:
		bw.put(0, 1)
		bw.put(0, 1)
		bw.put(1, 1) // symbol 1
	default:
		bw.put(0, 1)
		bw.put(0, 1)
		bw.put(0, 1) // symbol 0
	}
}

// write makes the second pass and writes the stream to w.
func (a *alphaEnc) write(w io.Writer, buf, readBuf []byte) error {
	a.src.rewind(readBuf)
	bw := bitWriter{w: w, buf: buf}
	a.putHeader(&bw)
	both := a.dist[0] && a.dist[1]
	for y := 0; y < a.h; y++ {
		if err := a.row(y); err != nil {
			return err
		}
		for i := 0; i < len(a.tokens); i++ {
			t := a.tokens[i]
			if t&0x8000 == 0 {
				a.green.put(&bw, int(t))
				continue
			}
			i++
			sym, extra, n := lz77Prefix(int(a.tokens[i]))
			a.green.put(&bw, 256+sym)
			bw.put(extra, n)
			if both {
				bw.put(uint32(t>>12&1), 1)
			}
		}
		if bw.err != nil {
			return bw.err
		}
	}
	if err := bw.finish(); err != nil {
		return err
	}
	if bw.total != a.bits {
		return errors.New("goimgtools: webp: alpha stream size mismatch")
	}
	return nil
}
