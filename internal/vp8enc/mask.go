package vp8enc

import "bytes"

// An alpha plane that is a mask of blocks: every pixel fully transparent or
// opaque, and all those of a block of cell x cell pixels (on the grid from
// the top left corner) alike. The frames of an animation that leave out the
// blocks that stay as they are have such planes (internal/webpanim), one
// for every frame, each small: a few hundred blocks to tell apart, in an
// image of as many thousand pixels. The lossless encoder would look at every
// pixel of them, with all it has (several megabytes of tables and a few
// milliseconds a frame); the coder here looks at the blocks only.
//
// The stream is what the lossless encoder would write for such a plane, in
// outline: the colour indexing transform with two colours, which packs
// eight pixels into one (a row of the plane is then a block, or two, to a
// packed pixel), and the packed pixels as literals and LZ77 copies with one
// set of Huffman codes. The copies are from the four nearest places only,
// whose distance codes take no extra bits: the pixel above, the one to the
// left, and those above to the left and right. Every row of a block but
// its first is a copy of the row above, so a row of blocks takes the bits
// of its first row and one copy, which goes on into the next row of blocks
// for as long as that is like this one.

const maskMaxMatch = 4096 // the longest copy of the format

// blockMask is a mask of blocks: rows holds, for each row of blocks, the
// pw packed pixels of its rows of pixels (bit i of packed pixel x set when
// pixel 8x+i is opaque).
type blockMask struct {
	w, h, cell int
	pw, bh     int
	rows       []uint8
	reserved   int64
}

// readMask reads the spooled alpha plane as a mask of blocks of cell x
// cell pixels. It returns nil when the plane is not one, or there is no
// memory for it.
func (e *encoder) readMask(cell int) (*blockMask, error) {
	a := e.alpha
	if cell < 2 || a.levels != nil || int64(a.raw[0])+int64(a.raw[0xff]) != int64(e.w)*int64(e.h) {
		return nil, nil
	}
	m := &blockMask{w: e.w, h: e.h, cell: cell, pw: (e.w + 7) >> 3, bh: (e.h + cell - 1) / cell}
	m.reserved = int64(m.pw)*int64(m.bh) + int64(e.w) + int64(m.pw)
	if e.env.Budget.Reserve(m.reserved, "webp alpha mask") != nil {
		return nil, nil
	}
	m.rows = make([]uint8, m.pw*m.bh)
	row, packed := make([]uint8, e.w), make([]uint8, m.pw)
	a.rewind(e.readBuf)
	for y := 0; y < e.h; y++ {
		if err := a.next(row); err != nil {
			e.env.Budget.Release(m.reserved)
			return nil, err
		}
		first := y%cell == 0
		ok := true
		if first {
			// Each block of the row is of one value.
			for x := 0; x < e.w && ok; x += cell {
				for _, v := range row[x+1 : min(x+cell, e.w)] {
					if v != row[x] {
						ok = false
						break
					}
				}
			}
		}
		clear(packed)
		for x, v := range row {
			if v != 0 {
				packed[x>>3] |= 1 << (x & 7)
			}
		}
		dst := m.rows[y/cell*m.pw:][:m.pw]
		if first {
			copy(dst, packed)
		} else if !bytes.Equal(dst, packed) {
			ok = false
		}
		if !ok {
			e.env.Budget.Release(m.reserved)
			return nil, nil
		}
	}
	return m, nil
}

// at is packed pixel p of the plane, counted row by row.
func (m *blockMask) at(p int) uint8 {
	return m.rows[p/m.pw/m.cell*m.pw+p%m.pw]
}

// match is how many packed pixels from p on, limit at most, are those d
// before them.
func (m *blockMask) match(p, d, limit int) int {
	pw := m.pw
	ya, xa := p/pw, p%pw
	yb, xb := (p-d)/pw, (p-d)%pw
	n := 0
	for n < limit {
		seg := min(pw-xa, pw-xb, limit-n)
		ra, rb := ya/m.cell, yb/m.cell
		if ra != rb || xa != xb {
			a, b := m.rows[ra*pw+xa:][:seg], m.rows[rb*pw+xb:][:seg]
			for i, v := range a {
				if v != b[i] {
					return n + i
				}
			}
		}
		n += seg
		if xa += seg; xa == pw {
			xa, ya = 0, ya+1
		}
		if xb += seg; xb == pw {
			xb, yb = 0, yb+1
		}
	}
	return n
}

// parse cuts the packed pixels into literals and copies and calls emit for
// each: with n = 0 for the literal lit, else for a copy of n pixels from
// the place of distance symbol sym. The copies come from the first nd of
// the four places, and are minMatch pixels at least.
func (m *blockMask) parse(nd, minMatch int, emit func(lit uint8, sym, n int)) {
	total := m.pw * m.h
	dist := [4]int{m.pw, 1, m.pw + 1, max(m.pw-1, 1)}
	for p := 0; p < total; {
		best, sym := 0, 0
		limit := min(maskMaxMatch, total-p)
		for s := 0; s < nd; s++ {
			if d := dist[s]; d <= p {
				if n := m.match(p, d, limit); n > best {
					best, sym = n, s
				}
			}
		}
		if best < minMatch {
			emit(m.at(p), 0, 0)
			p++
			continue
		}
		emit(0, sym, best)
		p += best
	}
}

// putSimpleCode writes a Huffman code of the one or two symbols syms (in
// order, each below 256): a single symbol then takes no bits, two take one.
func putSimpleCode(bw *bitWriter, syms ...int) {
	bw.put(1, 1)                   // a simple code
	bw.put(uint32(len(syms)-1), 1) // of one or two symbols
	if syms[0] < 2 {
		bw.put(0, 1) // the first in one bit
		bw.put(uint32(syms[0]), 1)
	} else {
		bw.put(1, 1) // in eight
		bw.put(uint32(syms[0]), 8)
	}
	if len(syms) == 2 {
		bw.put(uint32(syms[1]), 8)
	}
}

// code writes the plane as a VP8L stream without its header (the data of
// an ALPH chunk after its first byte) to bw, parsed as parse does with nd
// and minMatch.
func (m *blockMask) code(bw *bitWriter, nd, minMatch int) {
	var hist [alphaGreenSyms]uint32
	var dhist [40]uint32
	m.parse(nd, minMatch, func(lit uint8, sym, n int) {
		if n == 0 {
			hist[lit]++
			return
		}
		ls, _, _ := lz77Prefix(n)
		hist[256+ls]++
		dhist[sym]++
	})
	var used []int
	for s, c := range dhist[:4] {
		if c != 0 {
			used = append(used, s)
		}
	}

	bw.put(1, 1) // a transform:
	bw.put(3, 2) // colour indexing,
	bw.put(1, 8) // with two colours.
	// The colour table, an image of 2x1 pixels, each less the one before:
	// green 0 (transparent), then 255 more (opaque).
	bw.put(0, 1) // no colour cache
	putSimpleCode(bw, 0, 255)
	for i := 0; i < 4; i++ {
		putSimpleCode(bw, 0) // red, blue, alpha; distances (none)
	}
	bw.put(0, 1) // green 0
	bw.put(1, 1) // green 255
	bw.put(0, 1) // No other transform.
	// The image of packed pixels.
	bw.put(0, 1) // no colour cache
	bw.put(0, 1) // one group of Huffman codes
	green := newHuffCode(huffLengths(hist[:], 15))
	putHuffCodeLens(bw, green.lens, true)
	for i := 0; i < 3; i++ {
		putSimpleCode(bw, 0) // red, blue and alpha are not used
	}
	var dcode *huffCode
	switch len(used) {
	case 0:
		putSimpleCode(bw, 0)
	case 1, 2:
		putSimpleCode(bw, used...)
	default:
		dcode = newHuffCode(huffLengths(dhist[:], 15))
		putHuffCodeLens(bw, dcode.lens, true)
	}
	m.parse(nd, minMatch, func(lit uint8, sym, n int) {
		if n == 0 {
			green.put(bw, int(lit))
			return
		}
		ls, extra, ne := lz77Prefix(n)
		green.put(bw, 256+ls)
		bw.put(extra, ne)
		switch {
		case dcode != nil:
			dcode.put(bw, sym)
		case len(used) == 2 && sym == used[1]:
			bw.put(1, 1)
		case len(used) == 2:
			bw.put(0, 1)
		}
	})
}

// codeMask codes the alpha plane as a mask of blocks of cell x cell pixels
// and keeps the result for write, as codeAlpha keeps the lossless
// encoder's. It reports whether it did: not when the plane is no such mask.
func (e *encoder) codeMask(cell int) (bool, error) {
	m, err := e.readMask(cell)
	if m == nil || err != nil {
		return false, err
	}
	defer e.env.Budget.Release(m.reserved)
	// The parse that comes out smallest: copies from above and from the
	// left only, whose distances take a bit together, or from all four
	// places.
	nd, minMatch, size := 0, 0, int64(0)
	for _, try := range [][2]int{{2, 3}, {4, 3}, {2, 2}, {4, 2}} {
		var count bitWriter
		m.code(&count, try[0], try[1])
		if nd == 0 || count.total < size {
			nd, minMatch, size = try[0], try[1], count.total
		}
	}
	st := newStore(e.env)
	bw := bitWriter{w: st, buf: e.ioBuf}
	m.code(&bw, nd, minMatch)
	if err := bw.finish(); err != nil {
		st.Close()
		return false, err
	}
	e.alphaLL = &alphaLL{st: st, n: st.Size(), method: alphaFilterNone}
	if e.dbg != nil {
		e.dbg.maskBits = bw.total
	}
	return true, nil
}
