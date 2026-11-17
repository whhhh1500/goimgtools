package vp8lenc

import (
	"math"

	"github.com/whhhh1500/goimgtools/pipeline"
)

// probe collects rows as colour-table indices until a 257th colour appears.
type probe struct {
	e        *encoder
	pal      [256]uint32
	n        int
	tab      [1024]uint16 // open-addressing hash: palette index + 1
	idxHist  [256]uint32
	resHist  [4][256]uint32 // left-neighbour residuals after subtract green
	same     uint32         // pixels equal to their left neighbour
	spill    *pipeline.Spill
	idx      []byte
	reserved int64
}

const probeReadBuf = 32 << 10

func (e *encoder) newProbe() (*probe, error) {
	p := &probe{e: e, reserved: int64(e.w) + probeReadBuf}
	if err := e.reserve(p.reserved, "webp colour probe"); err != nil {
		return nil, err
	}
	e.probe = p
	p.idx = make([]byte, e.w)
	p.spill = pipeline.NewSpill(e.env)
	e.expect(p.spill, int64(e.w)*int64(e.h))
	return p, nil
}

func (p *probe) close() {
	if p.spill != nil {
		p.spill.Close()
		p.spill = nil
		p.e.unreserve(p.reserved)
		p.e.probe = nil
	}
}

func (p *probe) reader() *pipeline.SeqReader {
	return pipeline.NewSeqReader(p.spill, 0, p.spill.Size(), probeReadBuf)
}

// lookup returns the index of colour c, adding it if there is room, or -1.
func (p *probe) lookup(c uint32) int {
	h := c * 0x9e3779b1 >> 22
	for {
		v := p.tab[h]
		if v == 0 {
			if p.n == 256 {
				return -1
			}
			p.pal[p.n] = c
			p.n++
			p.tab[h] = uint16(p.n)
			return p.n - 1
		}
		if p.pal[v-1] == c {
			return int(v - 1)
		}
		h = (h + 1) & 1023
	}
}

// add records one row; it returns false when the row brings the image above
// 256 colours (nothing is recorded then).
func (p *probe) add(row []uint32) (bool, error) {
	i := p.lookup(row[0])
	if i < 0 {
		return false, nil
	}
	idx := p.idx
	prev := row[0]
	idx[0] = byte(i)
	p.idxHist[i]++
	for x := 1; x < len(row); x++ {
		c := row[x]
		if c == prev {
			p.same++
		} else {
			if i = p.lookup(c); i < 0 {
				return false, nil
			}
			d := subPixels(subGreen(c), subGreen(prev))
			p.resHist[0][d>>8&0xff]++
			p.resHist[1][d>>16&0xff]++
			p.resHist[2][d&0xff]++
			p.resHist[3][d>>24]++
			prev = c
		}
		idx[x] = byte(i)
		p.idxHist[i]++
	}
	_, err := p.spill.Write(idx)
	return true, err
}

// entropy is the ideal coded size of hist in bits.
func entropy(hist []uint32) float64 {
	var n, s float64
	for _, c := range hist {
		if c != 0 {
			f := float64(c)
			n += f
			s -= float64(f * math.Log2(f))
		}
	}
	if n == 0 {
		return 0
	}
	return s + float64(n*math.Log2(n))
}

// wantPalette guesses, for an image of at most 256 colours, whether the
// colour indexing transform will beat predicting the colours themselves.
// Smooth images (a gray photo stored as RGB) predict far better than their
// indices compress; drawings and dithered images do not. Both are tried
// when the budget has room; the guess decides which goes first.
func (p *probe) wantPalette() bool {
	if p.n <= 16 {
		return true
	}
	var direct float64
	for ch := range p.resHist {
		h := p.resHist[ch]
		h[0] += p.same
		direct += entropy(h[:])
	}
	return entropy(p.idxHist[:]) <= direct
}
