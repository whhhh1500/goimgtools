package watermark

import (
	"hash/crc32"
	"math"
)

// MaxPayload is the most bytes a mark carries.
const MaxPayload = 32

// The payload goes out with a CRC-32 of the key and the payload (so that a
// mark read with another key, or noise, is not taken for one), coded with
// a convolutional code of constraint length 9 and rate 1/6 (free distance
// 37, the most for the length and rate), ended by eight zeros. (Simulated
// against the constraint length 7, rate 1/3 code of NASA's standard, with
// its bits repeated over the tile as these are, for 9 bytes: 0.6 dB less
// signal for half the payloads read, 0.7 dB for nine in ten. With the list
// decoding of decode, 9 bytes are read down to a coded bit's soft value of
// about 0.53 of its spread, measured on the Kodak photographs marked at
// Strength 0.7 and saved as WebP 60, read with their true tiling; the
// rate's capacity limit is about 0.48, less than 1 dB away, and the copies
// not read were all but one below it: a better code would read hardly any
// more.)
const (
	constraint = 9
	states     = 1 << (constraint - 1)
	rate       = 6
)

var generators = [rate]uint32{0o745, 0o511, 0o657, 0o755, 0o637, 0o533}

// frameBits is the payload and its check, as bits.
func frameBits(payload []byte, k Key) []uint8 {
	crc := checksum(payload, k)
	data := append(append([]byte{}, payload...), byte(crc>>24), byte(crc>>16), byte(crc>>8), byte(crc))
	bits := make([]uint8, 0, 8*len(data))
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			bits = append(bits, b>>i&1)
		}
	}
	return bits
}

func checksum(payload []byte, k Key) uint32 {
	h := crc32.NewIEEE()
	h.Write(k[:])
	h.Write(payload)
	return h.Sum32()
}

// codedLen is the number of coded bits of a payload of n bytes.
func codedLen(n int) int { return rate * (8*(n+4) + constraint - 1) }

func parity(v uint32) uint8 {
	v ^= v >> 16
	v ^= v >> 8
	v ^= v >> 4
	v ^= v >> 2
	v ^= v >> 1
	return uint8(v & 1)
}

// encode is the convolutional code of bits, with the tail.
func encode(bits []uint8) []uint8 {
	out := make([]uint8, 0, rate*(len(bits)+constraint-1))
	var state uint32
	for i := 0; i < len(bits)+constraint-1; i++ {
		var b uint32
		if i < len(bits) {
			b = uint32(bits[i])
		}
		state = (state<<1 | b) & (1<<constraint - 1)
		for _, g := range generators {
			out = append(out, parity(state&g))
		}
	}
	return out
}

// outputs[s][b] are the coded bits of input b from state s (the last
// constraint-1 bits), bit i of the value the i-th.
var outputs = func() (o [states][2]uint8) {
	for s := range states {
		for b := range 2 {
			full := uint32(s<<1|b) & (1<<constraint - 1)
			for i, g := range generators {
				o[s][b] |= parity(full&g) << i
			}
		}
	}
	return
}()

// trellis is a sequence's soft values (positive for 0) and the metrics of
// Viterbi's algorithm forward: the best start of a path to every state at
// every step.
type trellis struct {
	nbits, steps int
	bm           []float64 // per step, the metric of each pattern of coded bits
	alpha        []float64 // per step and state; -Inf where no path goes
}

// forward runs Viterbi's algorithm over soft values for nbits bits, the
// tail known to be zeros, in t's storage (t may be nil).
func forward(t *trellis, soft []float64, nbits int) *trellis {
	steps := nbits + constraint - 1
	if t == nil {
		t = new(trellis)
	}
	t.nbits, t.steps = nbits, steps
	t.bm = grow(t.bm, steps<<rate)
	t.alpha = grow(t.alpha, (steps+1)*states)
	// A step's branch metrics: the soft values signed by the coded bits,
	// for each of the 2^rate patterns (built up a bit at a time).
	for st := range steps {
		bm := t.bm[st<<rate:][:1<<rate]
		sv := soft[rate*st:][:rate]
		bm[0] = 0
		for i := range rate {
			for p := range 1 << i {
				bm[p|1<<i] = bm[p] - sv[i]
				bm[p] += sv[i]
			}
		}
	}
	ninf := math.Inf(-1)
	for i := range t.alpha {
		t.alpha[i] = ninf
	}
	t.alpha[0] = 0
	for st := range steps {
		cur, next := t.alpha[st*states:][:states], t.alpha[(st+1)*states:][:states]
		bm := t.bm[st<<rate:][:1<<rate]
		bits := 2
		if st >= nbits {
			bits = 1
		}
		for s, a := range cur {
			if a == ninf {
				continue
			}
			for b := range bits {
				m := a + bm[outputs[s][b]]
				if d := (s<<1 | b) & (states - 1); m > next[d] {
					next[d] = m
				}
			}
		}
	}
	return t
}

// grow is s with length n, reusing its storage.
func grow(s []float64, n int) []float64 {
	if cap(s) < n {
		return make([]float64, n)
	}
	return s[:n]
}

// best is the metric of the likeliest path.
func (t *trellis) best() float64 { return t.alpha[t.steps*states] }

// list returns up to L likeliest sequences, likeliest first: a
// best-first search back from the end extends partial ends of paths, each
// scored exactly as its metric plus the best start to it (alpha), so
// complete paths come out in order, each in about one path's work (the
// tree-trellis algorithm).
func (t *trellis) list(L int) [][]uint8 {
	ninf := math.Inf(-1)
	if t.best() == ninf {
		return nil
	}
	type node struct {
		score, metric float64
		step, state   int32
		parent        int32 // the node a step later; -1 at the end
		order         int32 // ties go to the earlier
	}
	var nodes []node
	var heap []int32
	before := func(a, b int32) bool {
		na, nb := &nodes[a], &nodes[b]
		if na.score != nb.score {
			return na.score > nb.score
		}
		return na.order < nb.order
	}
	push := func(n node) {
		n.order = int32(len(nodes))
		nodes = append(nodes, n)
		heap = append(heap, n.order)
		for i := len(heap) - 1; i > 0; {
			p := (i - 1) / 2
			if !before(heap[i], heap[p]) {
				break
			}
			heap[i], heap[p] = heap[p], heap[i]
			i = p
		}
	}
	pop := func() int32 {
		top := heap[0]
		last := len(heap) - 1
		heap[0] = heap[last]
		heap = heap[:last]
		for i := 0; ; {
			l, r, m := 2*i+1, 2*i+2, i
			if l < len(heap) && before(heap[l], heap[m]) {
				m = l
			}
			if r < len(heap) && before(heap[r], heap[m]) {
				m = r
			}
			if m == i {
				break
			}
			heap[i], heap[m] = heap[m], heap[i]
			i = m
		}
		return top
	}
	push(node{score: t.best(), step: int32(t.steps), parent: -1})
	var out [][]uint8
	// Every node popped lies on one of the L best paths, so the search ends
	// within about L paths' nodes; the bound only guards against ties.
	for len(heap) > 0 && len(out) < L && len(nodes) < 4*L*(t.steps+1) {
		i := pop()
		n := nodes[i]
		if n.step == 0 {
			bits := make([]uint8, t.steps)
			for j := n.parent; j >= 0; j = nodes[j].parent {
				bits[nodes[j].step-1] = uint8(nodes[j].state & 1)
			}
			out = append(out, bits[:t.nbits])
			continue
		}
		// The step into state s took its newest bit from either of two
		// states.
		st, s := int(n.step)-1, int(n.state)
		b := s & 1
		if st >= t.nbits && b == 1 {
			continue
		}
		bm := t.bm[st<<rate:][:1<<rate]
		for h := range 2 {
			p := s>>1 | h<<(constraint-2)
			a := t.alpha[st*states+p]
			if a == ninf {
				continue
			}
			m := n.metric + bm[outputs[p][b]]
			push(node{score: m + a, metric: m, step: int32(st), state: int32(p), parent: i})
		}
	}
	return out
}

// unframe checks decoded bits: the payload, or false.
func unframe(bits []uint8, k Key) ([]byte, bool) {
	data := make([]byte, len(bits)/8)
	for i := range data {
		for j := range 8 {
			data[i] = data[i]<<1 | bits[8*i+j]
		}
	}
	n := len(data) - 4
	payload := data[:n]
	crc := uint32(data[n])<<24 | uint32(data[n+1])<<16 | uint32(data[n+2])<<8 | uint32(data[n+3])
	if checksum(payload, k) != crc {
		return nil, false
	}
	return payload, true
}
