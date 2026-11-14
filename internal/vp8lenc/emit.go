package vp8lenc

import "io"

// tokenReader is where pass two reads the tokens from.
type tokenReader interface {
	ReadFull(p []byte) error
}

type memReader struct{ b []byte }

func (m *memReader) ReadFull(p []byte) error {
	if len(m.b) < len(p) {
		return io.ErrUnexpectedEOF
	}
	copy(p, m.b)
	m.b = m.b[len(p):]
	return nil
}

// symSink receives the symbols of a token stream in coding order, each with
// the position of the pixel it starts at (which selects the Huffman group).
type symSink interface {
	literal(x, y int, p uint32)
	cacheHit(x, y int, index uint32)
	copyRef(x, y, length int, dcode uint32)
}

// walker replays a token stream: it tracks the pixel position and, when a
// colour cache is used, keeps the cache in step with the decoder, which
// means following the copies through a ring of the most recent pixels.
type walker struct {
	width int
	lit1  bool
	hi    uint32 // alpha, red and blue of a 1-byte literal

	cacheBits uint
	cache     []uint32
	ring      []uint32
	ringMask  int

	x, y, pos int
}

func (w *walker) literal(p uint32, sink symSink) {
	if w.cacheBits != 0 {
		w.ring[w.pos&w.ringMask] = p
		w.pos++
		if k := p * cacheMult >> (32 - w.cacheBits); w.cache[k] == p {
			sink.cacheHit(w.x, w.y, k)
		} else {
			w.cache[k] = p
			sink.literal(w.x, w.y, p)
		}
	} else {
		sink.literal(w.x, w.y, p)
	}
	if w.x++; w.x == w.width {
		w.x, w.y = 0, w.y+1
	}
}

func (w *walker) copyRef(length, dist int, sink symSink) {
	sink.copyRef(w.x, w.y, length, distCode(dist, w.width))
	if w.cacheBits != 0 {
		shift := 32 - w.cacheBits
		for i := 0; i < length; i++ {
			p := w.ring[(w.pos-dist)&w.ringMask]
			w.ring[w.pos&w.ringMask] = p
			w.pos++
			w.cache[p*cacheMult>>shift] = p
		}
	}
	w.x += length
	for w.x >= w.width {
		w.x -= w.width
		w.y++
	}
}

// run replays size bytes of tokens from the start of the stream.
func (w *walker) run(rd tokenReader, size int64, sink symSink) error {
	w.x, w.y, w.pos = 0, 0, 0
	clear(w.cache)
	var hdr [8]byte
	var lits [255 * 4]byte
	em, _ := sink.(*emitter)
	for size > 0 {
		if em != nil && em.bw.err != nil {
			return em.bw.err // the output failed: no point in going on
		}
		if err := rd.ReadFull(hdr[:1]); err != nil {
			return err
		}
		size--
		n := int(hdr[0])
		if n == 0 {
			if err := rd.ReadFull(hdr[:6]); err != nil {
				return err
			}
			size -= 6
			length := int(hdr[0]) | int(hdr[1])<<8
			dist := int(hdr[2]) | int(hdr[3])<<8 | int(hdr[4])<<16 | int(hdr[5])<<24
			w.copyRef(length+1, dist, sink)
			continue
		}
		if w.lit1 {
			b := lits[:n]
			if err := rd.ReadFull(b); err != nil {
				return err
			}
			size -= int64(n)
			for _, g := range b {
				w.literal(w.hi|uint32(g)<<8, sink)
			}
			continue
		}
		b := lits[:4*n]
		if err := rd.ReadFull(b); err != nil {
			return err
		}
		size -= int64(4 * n)
		for ; len(b) >= 4; b = b[4:] {
			w.literal(uint32(b[0])|uint32(b[1])<<8|uint32(b[2])<<16|uint32(b[3])<<24, sink)
		}
	}
	return nil
}

// emitter writes symbols as bits. With assign set, the Huffman group of a
// symbol is the one of the block its pixel lies in.
type emitter struct {
	bw     *bitWriter
	groups []*codeSet
	assign []uint16 // group per block, row by row; nil for a single group
	hbits  uint
	tw     int
}

func (e *emitter) codes(x, y int) *codeSet {
	if e.assign == nil {
		return e.groups[0]
	}
	return e.groups[e.assign[(y>>e.hbits)*e.tw+x>>e.hbits]]
}

func (e *emitter) literal(x, y int, p uint32) {
	cs, bw := e.codes(x, y), e.bw
	g, r := cs.c[0].enc[p>>8&0xff], cs.c[1].enc[p>>16&0xff]
	bw.put(g&0xffff|r&0xffff<<(g>>16), uint(g>>16+r>>16))
	b, a := cs.c[2].enc[p&0xff], cs.c[3].enc[p>>24]
	bw.put(b&0xffff|a&0xffff<<(b>>16), uint(b>>16+a>>16))
}

func (e *emitter) cacheHit(x, y int, index uint32) {
	e.codes(x, y).c[0].put(e.bw, numLiteral+numLength+index)
}

func (e *emitter) copyRef(x, y, length int, dcode uint32) {
	cs, bw := e.codes(x, y), e.bw
	ls, ln, le := prefixCode(uint32(length))
	cs.c[0].put(bw, numLiteral+ls)
	if ln != 0 {
		bw.put(le, ln)
	}
	ds, dn, de := prefixCode(dcode)
	cs.c[4].put(bw, ds)
	if dn != 0 {
		bw.put(de, dn)
	}
}
