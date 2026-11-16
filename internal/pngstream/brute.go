package pngstream

import (
	"encoding/binary"
	"slices"

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
// Everything is integer arithmetic on the rows alone, so the choices are
// the same on every processor.
type bruteFilter struct {
	row   int // bytes in a row, with the type byte
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
}

type undoEntry struct {
	h   int32
	old int64
}

const (
	bruteLines    = 4
	bruteHashBits = 13
	bruteWindow   = 32 << 10 // deflate's
	bruteChain    = 16
	nSyms         = 286 + 30 // literals and lengths, then distances
)

// bruteBytes is the memory a bruteFilter takes for rows of rowBytes.
func bruteBytes(rowBytes int) int64 {
	return int64(nFilter*rowBytes) + int64(bufSize(rowBytes)) + int64(2*prevSize(rowBytes)) + 8<<bruteHashBits +
		int64(bruteLines)*8*nSyms + int64(2*nFilter+2)*8*nSyms + 16*int64(rowBytes)
}

// prevSize holds the window behind the last position of a row, and the
// row.
func prevSize(rowBytes int) int {
	n := 1
	for n < bruteWindow+rowBytes {
		n <<= 1
	}
	return n
}

// bufSize holds the window and a row twice over, so that the window is
// moved back at most every other row.
func bufSize(rowBytes int) int {
	return 2 * (bruteWindow + rowBytes)
}

func newBruteFilter(env *pipeline.Env, rowBytes int) (*bruteFilter, error) {
	need := bruteBytes(rowBytes)
	if err := env.Budget.Reserve(need, "png filter search"); err != nil {
		return nil, err
	}
	ps := prevSize(rowBytes)
	b := &bruteFilter{
		row:      rowBytes,
		buf:      make([]byte, bufSize(rowBytes)),
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

func (b *bruteFilter) hash(p int64) int32 {
	v := binary.LittleEndian.Uint32(b.buf[p-b.base:])
	return int32(v * 2654435761 >> (32 - bruteHashBits))
}

// place copies row to the end of the data, after moving the window back
// to the start of the buffer if the row would not fit.
func (b *bruteFilter) place(row []byte) {
	if int(b.pos-b.base)+len(row) > len(b.buf) {
		from := max(b.base, b.pos-bruteWindow)
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
			for k := 0; k < bruteChain && c >= 0 && p-c < bruteWindow; k++ {
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
