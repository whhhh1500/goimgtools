package pngstream

import (
	"encoding/binary"
	"slices"

	"github.com/whhhh1500/goimgtools/internal/deflate"
	"github.com/whhhh1500/goimgtools/pipeline"
)

// filterRow writes to dst the row cur filtered with filter f against the
// row before, prev (both without the filter type byte; prev all zeros for
// the first row), and the filter type byte first; bpp is the bytes per
// pixel.
func filterRow(dst, cur, prev []byte, bpp, f int) {
	dst[0] = byte(f)
	d := dst[1 : len(cur)+1]
	prev = prev[:len(cur)]
	switch f {
	case ftNone:
		copy(d, cur)
	case ftSub:
		copy(d[:bpp], cur[:bpp])
		for i := bpp; i < len(cur); i++ {
			d[i] = cur[i] - cur[i-bpp]
		}
	case ftUp:
		for i := range cur {
			d[i] = cur[i] - prev[i]
		}
	case ftAverage:
		for i := 0; i < bpp; i++ {
			d[i] = cur[i] - prev[i]/2
		}
		for i := bpp; i < len(cur); i++ {
			d[i] = cur[i] - byte((int(cur[i-bpp])+int(prev[i]))/2)
		}
	case ftPaeth:
		for i := 0; i < bpp; i++ {
			d[i] = cur[i] - prev[i]
		}
		for i := bpp; i < len(cur); i++ {
			d[i] = cur[i] - paeth(cur[i-bpp], prev[i], prev[i-bpp])
		}
	}
}

// bruteFilter chooses each row's filter after oxipng's Brute strategy
// (src/filters/strategies.rs of github.com/shssoichiro/oxipng; MIT
// licence): the row is filtered with each of the five filters, and the
// filter wins with which the last bruteLines rows, the row and those
// chosen before it, compress smallest (the first on a tie). A row of zeros
// is left unfiltered. oxipng compresses the rows with libdeflate for each
// filter; here the row alone is parsed for matches, in what came before
// it, and the cost is that of the symbols of the row and of the rows
// before it, each counted when it was chosen, under Huffman codes made
// for them all: the same choice, mostly, in a fraction of the time.
//
// At level 10 the rows are compressed for each filter, as oxipng does:
// the last bruteLines rows, with the data before them as the dictionary
// (as far back as the window kept, 32KB at least), with libdeflate's level
// 10 (17 images 0.12% smaller than with the estimate, in 18 times the
// time).
//
// Everything is integer arithmetic on the rows alone, so the choices are
// the same on every processor.
type bruteFilter struct {
	row   int // bytes in a row, with the type byte
	chain int // earlier positions a match is looked for at, at most
	kept  int // the data kept before the next row (see keptBytes)
	cands [nFilter][]byte

	// The data of deflate's window and the row: buf[i] is at position
	// base+i. The hash chains of the window.
	buf  []byte
	base int64
	prev []uint16 // distance back to the last position with the same hash, by position modulo its length; 0 for none
	head []int64  // the last position with each hash; -1 for none
	mask int64
	pos  int64 // where the next row starts
	undo []undoEntry

	// The symbols of the rows chosen, the last bruteLines-1 of them, and
	// their sum.
	hists  [][nSyms]int64
	extras []int64
	next   int
	sum    [nSyms]int64
	extra  int64

	candHist  [nFilter][nSyms]int64
	candExtra [nFilter]int64
	total     [nSyms]int64
	scratch   []int64
	inner     []int64

	reserved int64
	env      *pipeline.Env

	// At level 10: the compressors that score the rows, each with an input
	// and an output (the first also scores the segments of the filter
	// trial; the others, when the memory limit has room for them, score
	// candidates on helpers at the same time), and the candidates' costs.
	real     *deflate.Compressor // reals[0]
	reals    []*deflate.Compressor
	realIns  [][]byte
	realOuts [][]byte
	costs    [nFilter]int
}

// bruteReal are the settings of the compressor that scores rows of
// rowBytes at level 10, and segments of the filter trial: libdeflate's
// level 10, with blocks of at most 8 rows or 2 segments rather than 300000
// bytes (what it compresses makes one block either way), so that it takes
// 2 to 4MB rather than 8.6MB.
func bruteReal(rowBytes int) deflate.Params {
	p := deflate.Level10
	p.BlockLen = min(p.BlockLen, max(8*rowBytes, 2*(trialBytes+rowBytes)))
	return p
}

type undoEntry struct {
	h   int32
	old int64
}

const (
	bruteLines = 4
	// bruteHashBits: with 13 the chains of a photograph's rows were mostly
	// other data of the same hash, each a load from far off; with 15 a
	// 6000×4000 photograph at level 9 took 9% less time, the file the same.
	bruteHashBits = 15
	bruteWindow   = 32 << 10 // deflate's
	// bruteChain is how many earlier positions a match is looked for at:
	// at level 8 bruteChain, at 9 bruteChain9 (the 17 images of level 9
	// 0.05% smaller, in 15% more time with eight threads; 64: 0.04%).
	bruteChain  = 16
	bruteChain9 = 256
	nSyms       = 286 + 30 // literals and lengths, then distances
)

// bruteBytes is the memory a bruteFilter takes for rows of rowBytes, with
// real when the rows are scored by compressing them.
func bruteBytes(rowBytes int, real bool) int64 {
	n := int64(nFilter*rowBytes) + int64(bufSize(rowBytes, real)) + int64(2*prevSize(rowBytes)) + 8<<bruteHashBits +
		int64(bruteLines)*8*nSyms + int64(2*nFilter+2)*8*nSyms + 16*int64(rowBytes)
	if real {
		n += scorerBytes(rowBytes)
	}
	return n
}

// scorerBytes is the memory of a compressor that scores rows of rowBytes,
// with its input and output.
func scorerBytes(rowBytes int) int64 {
	in := realInSize(rowBytes)
	return bruteReal(rowBytes).Bytes() + deflate.SharedBytes + int64(in) + int64(in+in/8+1<<10)
}

// realInSize is the most a compression of the rows takes in: the window
// kept and a row.
func realInSize(rowBytes int) int { return bufSize(rowBytes, true) + rowBytes }

// prevSize holds the window behind the last position of a row, and the
// row.
func prevSize(rowBytes int) int {
	n := 1
	for n < bruteWindow+rowBytes {
		n <<= 1
	}
	return n
}

// bufSize holds the data kept and a row twice over, so that the window is
// moved back at most every other row.
func bufSize(rowBytes int, real bool) int {
	return 2 * (keptBytes(rowBytes, real) + rowBytes)
}

// keptBytes is the data kept before the next row: deflate's window, and
// when the rows are scored by compressing them, the rows compressed with
// the next before that window, its dictionary.
func keptBytes(rowBytes int, real bool) int {
	if real {
		return bruteWindow + (bruteLines-1)*rowBytes
	}
	return bruteWindow
}

// newBruteFilter makes a bruteFilter for rows of rowBytes that looks for
// matches at chain earlier positions, or with real scores them by
// compressing them; then it takes compressors to score candidates on
// helpers at the same time, as many as the memory limit has room for while
// leaving keep bytes free.
func newBruteFilter(env *pipeline.Env, rowBytes, chain int, real bool, keep int64) (*bruteFilter, error) {
	need := bruteBytes(rowBytes, real)
	if err := env.Budget.Reserve(need, "png filter search"); err != nil {
		return nil, err
	}
	ps := prevSize(rowBytes)
	b := &bruteFilter{
		row:      rowBytes,
		chain:    chain,
		buf:      make([]byte, bufSize(rowBytes, real)),
		kept:     keptBytes(rowBytes, real),
		prev:     make([]uint16, ps),
		head:     make([]int64, 1<<bruteHashBits),
		mask:     int64(ps - 1),
		hists:    make([][nSyms]int64, bruteLines-1),
		extras:   make([]int64, bruteLines-1),
		scratch:  make([]int64, 0, nSyms),
		inner:    make([]int64, nSyms),
		reserved: need,
		env:      env,
	}
	for i := range b.cands {
		b.cands[i] = make([]byte, rowBytes)
	}
	for i := range b.head {
		b.head[i] = -1
	}
	if real {
		in := realInSize(rowBytes)
		add := func() {
			b.reals = append(b.reals, deflate.New(bruteReal(rowBytes)))
			b.realIns = append(b.realIns, make([]byte, 0, in))
			b.realOuts = append(b.realOuts, make([]byte, 0, in+in/8+1<<10))
		}
		add()
		b.real = b.reals[0]
		for env.Helpers() > 0 && len(b.reals) < nFilter {
			more := scorerBytes(rowBytes)
			if env.Budget.Limit()-env.Budget.Used()-more < keep || env.Budget.Reserve(more, "png filter search") != nil {
				break
			}
			b.reserved += more
			add()
		}
	}
	return b, nil
}

func (b *bruteFilter) release() {
	b.env.Budget.Release(b.reserved)
	b.reserved = 0
}

// choose filters cur (without type byte) against prev and returns the
// filtered row, type byte first; it is valid until the next call.
func (b *bruteFilter) choose(cur, prev []byte, bpp int) []byte {
	zero := true
	for _, v := range cur {
		if v != 0 {
			zero = false
			break
		}
	}
	best := ftNone
	if zero {
		filterRow(b.cands[ftNone], cur, prev, bpp, ftNone)
		b.parse(b.cands[ftNone], ftNone, false)
	} else if b.real != nil {
		return b.chooseReal(cur, prev, bpp)
	} else {
		var bestCost int64 = -1
		for f := ftNone; f <= ftPaeth; f++ {
			filterRow(b.cands[f], cur, prev, bpp, f)
			b.parse(b.cands[f], f, true)
			for s := range b.total {
				b.total[s] = b.sum[s] + b.candHist[f][s]
			}
			b.total[256]++ // the end of block
			cost := b.huffmanBits(b.total[:286]) + b.huffmanBits(b.total[286:]) + b.extra + b.candExtra[f]
			if bestCost < 0 || cost < bestCost {
				bestCost, best = cost, f
			}
		}
		b.enter(b.cands[best])
	}
	b.keep(best)
	return b.cands[best]
}

// chooseReal chooses the filter with which the last rows compress
// smallest, the window kept before them as the dictionary (the first on a
// tie). The dictionary is entered once for the five, when the rows before
// the candidate are long enough (see deflate.SharedAfter); the candidates
// are scored on helpers at the same time when there are compressors for
// them. The choice is the same either way.
func (b *bruteFilter) chooseReal(cur, prev []byte, bpp int) []byte {
	hist := b.buf[:b.pos-b.base]
	from := max(0, len(hist)-bruteWindow-(bruteLines-1)*b.row)
	start := max(0, len(hist)-(bruteLines-1)*b.row) - from
	share := start > 0 && len(hist)-from-start >= deflate.SharedAfter
	for f := ftNone; f <= ftPaeth; f++ {
		filterRow(b.cands[f], cur, prev, bpp, f)
	}
	lanes := len(b.reals)
	input := func(k, f int) []byte {
		b.realIns[k] = append(append(b.realIns[k][:0], hist[from:]...), b.cands[f]...)
		return b.realIns[k]
	}
	if share {
		b.reals[0].EnterShared(input(0, ftNone), start)
		for _, r := range b.reals[1:] {
			r.TakeShared(b.reals[0])
		}
	}
	score := func(k int) {
		r := b.reals[k]
		for f := k; f < nFilter; f += lanes {
			var bits int
			if share {
				b.realOuts[k], bits = r.CompressShared(b.realOuts[k][:0], input(k, f), start, true, false)
			} else {
				b.realOuts[k], bits, _ = r.CompressBits(b.realOuts[k][:0], input(k, f), start, true)
			}
			b.costs[f] = 8*len(b.realOuts[k]) - 8 + bits
		}
	}
	if lanes > 1 {
		b.env.Parallel(lanes, score)
	} else {
		score(0)
	}
	best := ftNone
	for f := ftNone + 1; f <= ftPaeth; f++ {
		if b.costs[f] < b.costs[best] {
			best = f
		}
	}
	b.place(b.cands[best])
	b.pos += int64(b.row)
	return b.cands[best]
}

func (b *bruteFilter) hash(p int64) int32 {
	v := binary.LittleEndian.Uint32(b.buf[p-b.base:])
	return int32(v * 2654435761 >> (32 - bruteHashBits))
}

// place copies row to the end of the data, after moving the window back
// to the start of the buffer if the row would not fit.
func (b *bruteFilter) place(row []byte) {
	if int(b.pos-b.base)+len(row) > len(b.buf) {
		from := max(b.base, b.pos-int64(b.kept))
		copy(b.buf, b.buf[from-b.base:b.pos-b.base])
		b.base = from
	}
	copy(b.buf[b.pos-b.base:], row)
}

// insert enters position p, of hash h, in the hash chains, noting what it
// replaces when trying.
func (b *bruteFilter) insert(p int64, h int32, trying bool) {
	c := b.head[h]
	if trying {
		b.undo = append(b.undo, undoEntry{h, c})
	}
	b.head[h] = p
	d := uint16(0)
	if c >= 0 && p-c < bruteWindow {
		d = uint16(p - c)
	}
	b.prev[p&b.mask] = d
}

// parse counts the symbols of a greedy deflate parse of row placed at the
// end of the data, into candHist[f] and candExtra[f]. When trying, the
// hash chains are left as they were; otherwise the row is entered.
func (b *bruteFilter) parse(row []byte, f int, trying bool) {
	m := b.mask
	b.place(row)
	d, base := b.buf, b.base
	hist := &b.candHist[f]
	*hist = [nSyms]int64{}
	var extra int64
	n := int64(len(row))
	end := b.pos + n
	b.undo = b.undo[:0]
	for p := b.pos; p < end; {
		if p+4 <= end {
			h := b.hash(p)
			c := b.head[h]
			b.insert(p, h, trying)
			l, at := 0, int64(-1)
			pi := int(p - base)
			maxLen := int(min(258, end-p))
			for k := 0; k < b.chain && c >= 0 && p-c < bruteWindow; k++ {
				ci := int(c - base)
				if d[ci] == d[pi] && d[ci+1] == d[pi+1] && d[ci+2] == d[pi+2] {
					if j := matchLen(d, pi, ci, 3, maxLen); j > l {
						l, at = j, c
					}
				}
				d := int64(b.prev[c&m])
				if d == 0 {
					break
				}
				c -= d
			}
			if at >= 0 {
				ls, lx := lengthCode(l)
				ds, dx := distCode(int(p - at))
				hist[ls]++
				hist[286+ds]++
				extra += int64(lx + dx)
				for q := p + 1; q < p+int64(l) && q+4 <= end; q++ {
					b.insert(q, b.hash(q), trying)
				}
				p += int64(l)
				continue
			}
		}
		hist[d[p-base]]++
		p++
	}
	b.candExtra[f] = extra
	if trying {
		for i := len(b.undo) - 1; i >= 0; i-- {
			b.head[b.undo[i].h] = b.undo[i].old
		}
	}
}

// enter places the chosen row at the end of the data, in the hash chains.
func (b *bruteFilter) enter(row []byte) {
	b.place(row)
	end := b.pos + int64(len(row))
	for p := b.pos; p+4 <= end; p++ {
		b.insert(p, b.hash(p), false)
	}
}

// keep adds the symbols of the chosen row to those of the rows before it,
// and drops the oldest row's.
func (b *bruteFilter) keep(f int) {
	old := &b.hists[b.next]
	for s := range b.sum {
		b.sum[s] += b.candHist[f][s] - old[s]
	}
	b.extra += b.candExtra[f] - b.extras[b.next]
	*old = b.candHist[f]
	b.extras[b.next] = b.candExtra[f]
	b.next = (b.next + 1) % len(b.hists)
	b.pos += int64(b.row)
}

// huffmanBits is the size in bits of symbols coded with a Huffman code
// made for their counts: the sum of the weights of the inner nodes, found
// by merging the sorted counts with the queue of the inner nodes.
func (b *bruteFilter) huffmanBits(counts []int64) int64 {
	leaves := b.scratch[:0]
	for _, c := range counts {
		if c > 0 {
			leaves = append(leaves, c)
		}
	}
	if len(leaves) < 2 {
		var n int64
		for _, c := range leaves {
			n += c
		}
		return n
	}
	slices.Sort(leaves)
	// The inner nodes are made in increasing order, so the two smallest
	// nodes are always at the front of the leaves or of the inner nodes.
	inner := b.inner
	var total int64
	i, qi, qn := 0, 0, 0
	take := func() int64 {
		if qi == qn || (i < len(leaves) && leaves[i] <= inner[qi]) {
			i++
			return leaves[i-1]
		}
		qi++
		return inner[qi-1]
	}
	for k := 1; k < len(leaves); k++ {
		w := take() + take()
		total += w
		inner[qn] = w
		qn++
	}
	b.scratch = leaves
	return total
}
